package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/library"
)

// --- 媒体库：Komga（漫画 / 轻小说）---
//
// 载体对位：漫画与小说 ↔ Komga。与另两家的差异：
//   · 判型从内容来：epub 占多数 → 小说，否则 → 漫画（pdf 归漫画）；
//   · "抽屉型"系列（单行本 / 作者柜：书标题多数不含系列名的）按**单册**成条目；
//   · 缩略图必须带鉴权头 → 宿主做图片代理（/api/library/komga/cover）；
//   · 反哺只写元数据：简介 → series / book 的 metadata.summary；
//   · 一个节点可挂多条链（同一作品的漫画版 + 小说版）。

func (s *Server) komgaClient() (*library.KomgaClient, error) {
	st, err := s.store.GetSettings()
	if err != nil {
		return nil, err
	}
	var base, key string
	if st.Library.KomgaURL != nil {
		base = *st.Library.KomgaURL
	}
	if st.Library.KomgaAPIKey != nil {
		key = *st.Library.KomgaAPIKey
	}
	return library.NewKomgaClient(base, key)
}

func (s *Server) writeKomgaClientErr(w http.ResponseWriter, err error) {
	if errors.Is(err, library.ErrNotConfigured) {
		writeErr(w, http.StatusBadRequest, "not_configured", "还没配置 Komga：到设置页填好地址与 API Key")
		return
	}
	writeErr(w, http.StatusInternalServerError, "internal", err.Error())
}

// komgaEntry 是扫库后的一条可处理条目：正常系列一条；抽屉系列拆成单册。
type komgaEntry struct {
	ID     string
	IsBook bool
	Name   string
	Comic  bool // true = 漫画，false = 小说
	// 所属容器（正常系列=自身；抽屉单册=抽屉系列），扫描清单分组用。
	SeriesID   string
	SeriesName string
}

func (e komgaEntry) formatStr() string {
	if e.Comic {
		return "comic"
	}
	return "novel"
}

// komgaCatalog 是进程内的 Komga 目录缓存（TTL 两分钟）：逐系列拉书本判型
// 要几十个请求，池子 / 建议 / 搜索共用，不能每次重扫。
type komgaCatalog struct {
	mu        sync.Mutex
	baseURL   string
	entries   []komgaEntry
	fetchedAt time.Time
}

func (s *Server) komgaEntries(ctx context.Context, client *library.KomgaClient) ([]komgaEntry, error) {
	c := &s.komgaCatalog
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries != nil && c.baseURL == client.BaseURL() && time.Since(c.fetchedAt) < 2*time.Minute {
		return append([]komgaEntry(nil), c.entries...), nil
	}
	series, err := client.ListSeries(ctx, "")
	if err != nil {
		return nil, err
	}
	// 层级（分卷系列 / 抽屉单册）取 LLM 判定缓存；没判过的退回启发式投票。
	judged := s.komgaJudgmentKinds()
	entries := make([]komgaEntry, 0, len(series))
	for _, sr := range series {
		books, err := client.ListBooks(ctx, sr.ID)
		if err != nil {
			return nil, err
		}
		entries = append(entries, komgaEntriesForSeries(sr, books, judged[sr.ID])...)
	}
	c.entries = entries
	c.baseURL = client.BaseURL()
	c.fetchedAt = time.Now()
	return append([]komgaEntry(nil), entries...), nil
}

// komgaVolumeNoise：卷号形态判断时剥掉的字（数字、第/卷/册/话…、括号与常见标点）。
var komgaVolumeNoise = regexp.MustCompile(`[0-9０-９第卷巻册冊话話集部回全之\s\-—–~～:：.。·•,，()（）\[\]【】《》]+`)

// komgaVolumeWord：剥掉数字后只剩这些词（Vol.01、Ch.3…）的，也是分卷。
var komgaVolumeWord = regexp.MustCompile(`^(vol|volume|ch|chapter|ep|episode|no|sp)$`)

// komgaBookIsVolume：书名剥掉卷号噪音后为空、只剩 vol 类词、以 vol 结尾
// （「葬送的芙莉蓮 Vol.01」——注意真库里书名常用繁体，和系列名简繁不一致，
// 不能靠"包含系列名"来判断），或与系列名互相包含 → 这是"分卷"；
// 否则它是独立作品（抽屉系列里的单册）。
//
// 这只是**兜底启发式**：LLM 判过的系列走 refreshKomgaJudgments 的缓存结论，
// 判不准的脏命名（作者前缀、.01、第N卷、kepub 尾巴……）交给模型看，不堆正则。
func komgaBookIsVolume(title, seriesName string) bool {
	cleaned := komgaVolumeNoise.ReplaceAllString(strings.ToLower(title), "")
	if cleaned == "" || komgaVolumeWord.MatchString(cleaned) || strings.HasSuffix(cleaned, "vol") {
		return true
	}
	s := komgaVolumeNoise.ReplaceAllString(strings.ToLower(seriesName), "")
	if s == "" {
		return false
	}
	return strings.Contains(cleaned, s) || strings.Contains(s, cleaned)
}

// komgaEntriesForSeries：epub 多数 → 小说。层级取 LLM 判定（kind："series" 分卷系列 /
// "drawer" 独立单册）；没判过（空串）才退回启发式投票：多数书不是"分卷"形态 → 抽屉拆单册。
func komgaEntriesForSeries(sr library.KomgaSeries, books []library.KomgaBook, kind string) []komgaEntry {
	epub, volumes := 0, 0
	for _, b := range books {
		if strings.Contains(b.Media.MediaType, "epub") {
			epub++
		}
		title := b.Metadata.Title
		if title == "" {
			title = b.Name
		}
		if komgaBookIsVolume(title, sr.Name) {
			volumes++
		}
	}
	isDrawer := kind == "drawer"
	if kind == "" && len(books) > 1 && volumes*2 < len(books) {
		isDrawer = true
	}
	if isDrawer {
		out := make([]komgaEntry, 0, len(books))
		for _, b := range books {
			title := strings.TrimSpace(b.Metadata.Title)
			if title == "" {
				title = b.Name
			}
			out = append(out, komgaEntry{
				ID: b.ID, IsBook: true, Name: title,
				Comic:      !strings.Contains(b.Media.MediaType, "epub"),
				SeriesID:   sr.ID,
				SeriesName: sr.Name,
			})
		}
		return out
	}
	novel := epub*2 > len(books)
	return []komgaEntry{{ID: sr.ID, IsBook: false, Name: sr.Name, Comic: !novel, SeriesID: sr.ID, SeriesName: sr.Name}}
}

// komgaKindHintRe：(小说)/(轻小说)/(漫画) 后缀 → 匹配约束（同名作品多载体时的节点命名）。
var komgaKindHintRe = regexp.MustCompile(`[（(](小说|轻小说|漫画)[)）]\s*$`)

func komgaSplitKindHint(title string) (base, hint string) {
	m := komgaKindHintRe.FindStringSubmatch(title)
	if m == nil {
		return title, ""
	}
	base = strings.TrimSpace(komgaKindHintRe.ReplaceAllString(title, ""))
	if m[1] == "漫画" {
		return base, "comic"
	}
	return base, "novel"
}

// komgaNorm 在通用归一基础上容错尾部装饰标点（「祝福！」↔「祝福」）。
func komgaNorm(s string) string {
	return strings.TrimRight(normalizeTitle(s), "！!~～")
}

func komgaEntryMatches(e komgaEntry, nodeTitle string) bool {
	base, hint := komgaSplitKindHint(nodeTitle)
	if hint == "comic" && !e.Comic {
		return false
	}
	if hint == "novel" && e.Comic {
		return false
	}
	n := komgaNorm(e.Name)
	b := komgaNorm(base)
	if n == b || n == komgaNorm(nodeTitle) {
		return true
	}
	// 抽屉单册：书名常带副标题/装饰（"哈利·波特（全集）"）——节点名被包含即可。
	return e.IsBook && len([]rune(b)) >= 2 && strings.Contains(n, b)
}

func (s *Server) komgaCoverPath(e komgaEntry) string {
	q := url.Values{}
	q.Set("id", e.ID)
	if e.IsBook {
		q.Set("type", "book")
	}
	return "/api/library/komga/cover?" + q.Encode()
}

func komgaEntryURL(client *library.KomgaClient, e komgaEntry) string {
	if e.IsBook {
		return client.BookURL(e.ID)
	}
	return client.SeriesURL(e.ID)
}

type komgaSuggestion struct {
	PublicID    string  `json:"publicId"`
	Title       string  `json:"title"`
	TitleAlt    *string `json:"titleAlt"`
	ReleaseDate *string `json:"releaseDate"`
	CoverImage  *string `json:"coverImage"`
	URL         string  `json:"url"`
	Kind        string  `json:"kind"`   // book（建档介质固定书籍）
	Format      string  `json:"format"` // comic | novel（展示用）
}

type komgaMediaEntry struct {
	komgaSuggestion
	Linked       bool   `json:"linked"`
	LinkedWorkID string `json:"linkedWorkId,omitempty"`
}

func (s *Server) komgaEntryWithLink(client *library.KomgaClient, e komgaEntry, linked map[string]string) komgaMediaEntry {
	cover := s.komgaCoverPath(e)
	item := komgaSuggestion{
		PublicID:   e.ID,
		Title:      e.Name,
		CoverImage: &cover,
		URL:        komgaEntryURL(client, e),
		Kind:       "book",
		Format:     e.formatStr(),
	}
	out := komgaMediaEntry{komgaSuggestion: item}
	if wid, ok := linked[e.ID]; ok {
		out.Linked = true
		out.LinkedWorkID = wid
	}
	return out
}

func (s *Server) findKomgaEntry(entries []komgaEntry, id string) *komgaEntry {
	for i := range entries {
		if entries[i].ID == id {
			return &entries[i]
		}
	}
	return nil
}

// handleKomgaSuggest 是 GET /api/library/komga/suggest?workId=：
// 集合页建档建议——归一化后同名的系列 / 抽屉单册；节点带 (小说)/(漫画) 后缀时约束同型。
func (s *Server) handleKomgaSuggest(w http.ResponseWriter, r *http.Request) {
	workID := strings.TrimSpace(r.URL.Query().Get("workId"))
	if workID == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "workId 必填")
		return
	}
	work, err := s.store.GetWork(workID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	client, err := s.komgaClient()
	if err != nil {
		s.writeKomgaClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	entries, err := s.komgaEntries(ctx, client)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "komga", err.Error())
		return
	}

	linked := s.linkedIDsBySource(domain.LibraryKomga)
	out := make([]komgaSuggestion, 0)
	for _, e := range entries {
		if _, isLinked := linked[e.ID]; isLinked {
			continue
		}
		if !komgaEntryMatches(e, work.Title) {
			continue
		}
		cover := s.komgaCoverPath(e)
		out = append(out, komgaSuggestion{
			PublicID:   e.ID,
			Title:      e.Name,
			CoverImage: &cover,
			URL:        komgaEntryURL(client, e),
			Kind:       "book",
			Format:     e.formatStr(),
		})
	}
	if len(out) > 50 {
		out = out[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

// handleKomgaArchive 是 POST /api/library/komga/archive {workId, publicId}：
// 一键建档——建 stub 子节点（medium=book）+ 挂 Komga 链。
func (s *Server) handleKomgaArchive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkID   string `json:"workId"`
		PublicID string `json:"publicId"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	body.WorkID = strings.TrimSpace(body.WorkID)
	body.PublicID = strings.TrimSpace(body.PublicID)
	if body.WorkID == "" || body.PublicID == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "workId 与 publicId 必填")
		return
	}
	parent, err := s.store.GetWork(body.WorkID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	client, err := s.komgaClient()
	if err != nil {
		s.writeKomgaClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	entries, err := s.komgaEntries(ctx, client)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "komga", err.Error())
		return
	}
	entry := s.findKomgaEntry(entries, body.PublicID)
	if entry == nil {
		writeErr(w, http.StatusNotFound, "not_found", "Komga 里没找到这条，刷新建议列表再试")
		return
	}
	if wid, isLinked := s.linkedIDsBySource(domain.LibraryKomga)[entry.ID]; isLinked {
		if wid == parent.ID {
			writeErr(w, http.StatusConflict, "conflict", "这个节点已经关联过这条了")
		} else {
			writeErr(w, http.StatusConflict, "conflict", "这条已经关联到别的节点了——先去那边解除")
		}
		return
	}

	medium := domain.MediumBook
	child, err := s.store.CreateWork(domain.CreateWorkBody{
		ParentID: &parent.ID,
		Kind:     domain.WorkKindWork,
		Medium:   &medium,
		Title:    entry.Name,
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	urlStr := komgaEntryURL(client, *entry)
	hint := entry.Name
	link, err := s.store.CreateLibraryLink(child.ID, domain.LibraryKomga, entry.ID, &urlStr, &hint)
	if err != nil {
		_ = s.store.DeleteWork(child.ID)
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"work": child, "link": link})
}

// handleKomgaSearch 是 GET /api/library/komga/search?q=：条目集（含抽屉单册）本地过滤。
func (s *Server) handleKomgaSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	client, err := s.komgaClient()
	if err != nil {
		s.writeKomgaClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	entries, err := s.komgaEntries(ctx, client)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "komga", err.Error())
		return
	}
	norm := normalizeTitle(query)
	linked := s.linkedIDsBySource(domain.LibraryKomga)
	out := make([]komgaMediaEntry, 0, len(entries))
	for _, e := range entries {
		if norm != "" && !strings.Contains(normalizeTitle(e.Name), norm) {
			continue
		}
		out = append(out, s.komgaEntryWithLink(client, e, linked))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return embyNameRank(out[i].Title, norm) < embyNameRank(out[j].Title, norm)
	})
	if len(out) > 50 {
		out = out[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// handleKomgaLink 是 POST /api/library/komga/link {workId, publicId}：
// 关联既有条目（漫画版 + 小说版可同时挂在一个节点上）。
func (s *Server) handleKomgaLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkID   string `json:"workId"`
		PublicID string `json:"publicId"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	body.WorkID = strings.TrimSpace(body.WorkID)
	body.PublicID = strings.TrimSpace(body.PublicID)
	if body.WorkID == "" || body.PublicID == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "workId 与 publicId 必填")
		return
	}
	work, err := s.store.GetWork(body.WorkID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	client, err := s.komgaClient()
	if err != nil {
		s.writeKomgaClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	entries, err := s.komgaEntries(ctx, client)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "komga", err.Error())
		return
	}
	entry := s.findKomgaEntry(entries, body.PublicID)
	if entry == nil {
		writeErr(w, http.StatusNotFound, "not_found", "Komga 里没找到这条，刷新搜索再试")
		return
	}
	if wid, isLinked := s.linkedIDsBySource(domain.LibraryKomga)[entry.ID]; isLinked {
		if wid == work.ID {
			writeErr(w, http.StatusConflict, "conflict", "这个节点已经关联过这条了")
		} else {
			writeErr(w, http.StatusConflict, "conflict", "这条已经关联到别的节点了——先去那边解除")
		}
		return
	}

	urlStr := komgaEntryURL(client, *entry)
	hint := entry.Name
	link, err := s.store.CreateLibraryLink(work.ID, domain.LibraryKomga, entry.ID, &urlStr, &hint)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"link": link})
}

// handleKomgaUnlink 是 DELETE /api/library/komga/link?id=<linkId>。
func (s *Server) handleKomgaUnlink(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "id 必填")
		return
	}
	link, err := s.store.GetLibraryLink(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if link.Source != domain.LibraryKomga {
		writeErr(w, http.StatusBadRequest, "bad_source", "不是 Komga 链接")
		return
	}
	if err := s.store.DeleteLibraryLink(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleKomgaPush 是 POST /api/library/komga/push {workId, summary?}：
// 反哺——把简介 PATCH 到该节点所有 Komga 链的 metadata.summary
// （Komga 没有全文位置；漫画版 / 小说版都推同一段简介）。
func (s *Server) handleKomgaPush(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkID  string  `json:"workId"`
		Summary *string `json:"summary"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if strings.TrimSpace(body.WorkID) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "workId 必填")
		return
	}
	work, err := s.store.GetWork(body.WorkID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	links, err := s.store.ListLibraryLinksByWork(work.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	komgaLinks := make([]domain.LibraryLink, 0, len(links))
	for _, l := range links {
		if l.Source == domain.LibraryKomga {
			komgaLinks = append(komgaLinks, l)
		}
	}
	if len(komgaLinks) == 0 {
		writeErr(w, http.StatusNotFound, "no_link", "这个节点还没挂 Komga 链接——先建档或关联")
		return
	}

	summary := body.Summary
	if summary == nil {
		if work.ContentMd == nil || strings.TrimSpace(*work.ContentMd) == "" {
			writeErr(w, http.StatusBadRequest, "empty_content", "正文还是空的——先写点东西再反哺")
			return
		}
		summary = extractWikiatlasSummary(*work.ContentMd)
	} else {
		trimmed := strings.TrimSpace(*summary)
		summary = &trimmed
	}
	if summary == nil || *summary == "" {
		writeErr(w, http.StatusBadRequest, "empty_summary", "没提取到简介——正文先写一段普通文字，或显式传 summary")
		return
	}

	client, err := s.komgaClient()
	if err != nil {
		s.writeKomgaClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	pushed := 0
	for _, l := range komgaLinks {
		if _, err := client.GetSeries(ctx, l.ExternalID); err == nil {
			if err := client.PushSeriesSummary(ctx, l.ExternalID, *summary); err != nil {
				writeErr(w, http.StatusBadGateway, "komga", err.Error())
				return
			}
			pushed++
			continue
		}
		if _, err := client.GetBook(ctx, l.ExternalID); err == nil {
			if err := client.PushBookSummary(ctx, l.ExternalID, *summary); err != nil {
				writeErr(w, http.StatusBadGateway, "komga", err.Error())
				return
			}
			pushed++
			continue
		}
		writeErr(w, http.StatusNotFound, "stale_link", "Komga 里找不到链接指向的条目（可能已删除）")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": pushed, "summary": *summary})
}

// handleKomgaCover 是 GET /api/library/komga/cover?id=&type=series|book：
// 图片代理（浏览器 <img> 带不了 X-API-Key）。登录保护由 withAuth 兜底。
func (s *Server) handleKomgaCover(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	kind := strings.TrimSpace(r.URL.Query().Get("type"))
	if kind != "book" {
		kind = "series"
	}
	if id == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "id 必填")
		return
	}
	client, err := s.komgaClient()
	if err != nil {
		s.writeKomgaClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	b, ct, err := client.ThumbnailFor(ctx, kind, id)
	if err != nil {
		if errors.Is(err, library.ErrCoverMissing) {
			writeErr(w, http.StatusNotFound, "no_cover", "这条没有封面")
			return
		}
		writeErr(w, http.StatusBadGateway, "komga", err.Error())
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
