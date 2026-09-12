package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/library"
)

// --- 媒体库：GameAtlas（孪生）---
//
// 载体对位：游戏 ↔ GameAtlas。两条方向：
//   · 拉：集合页（系列/宇宙）出建档建议——GameAtlas 里属于同名系列、还没挂链的条目；
//   · 推：反哺——把节点正文（连同简介）写回已挂链的 GameAtlas 条目。
//
// 三个接口都不在 guestAllowed 白名单里，withAuth 默认拒绝 → 只有登录会话可达。

// gaClient 从设置读 GameAtlas 地址与管理员密码构造客户端。
// GameAtlas 没有 API Key，只有"管理员密码换会话 Cookie"，所以那个字段存的就是密码。
func (s *Server) gaClient() (*library.Client, error) {
	st, err := s.store.GetSettings()
	if err != nil {
		return nil, err
	}
	var baseURL, password string
	if st.Library.GameAtlasURL != nil {
		baseURL = *st.Library.GameAtlasURL
	}
	if st.Library.GameAtlasAPIKey != nil {
		password = *st.Library.GameAtlasAPIKey
	}
	return library.NewClient(baseURL, password)
}

func (s *Server) writeGAClientErr(w http.ResponseWriter, err error) {
	if errors.Is(err, library.ErrNotConfigured) {
		writeErr(w, http.StatusBadRequest, "not_configured", "还没配置 GameAtlas：到设置页填好地址与管理员密码")
		return
	}
	writeErr(w, http.StatusInternalServerError, "internal", err.Error())
}

// normalizeTitle 用于把 WikiAltas 节点标题和 GameAtlas 系列名对齐：
// 去首尾空白、去空格、小写（中文基本无损，英文大小写归一）。
func normalizeTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	return strings.ToLower(s)
}

type gaSuggestion struct {
	PublicID    string  `json:"publicId"`
	Title       string  `json:"title"`
	TitleAlt    *string `json:"titleAlt"`
	ReleaseDate *string `json:"releaseDate"`
	CoverImage  *string `json:"coverImage"`
	URL         string  `json:"url"`
}

// handleGameAtlasSuggest 是 GET /api/library/gameatlas/suggest?workId=：
// 给一个集合节点（系列/宇宙），列出 GameAtlas 同系列里还没建档的条目。
func (s *Server) handleGameAtlasSuggest(w http.ResponseWriter, r *http.Request) {
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
	client, err := s.gaClient()
	if err != nil {
		s.writeGAClientErr(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	entries, err := s.gaCatalogEntries(ctx, client)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "gameatlas", err.Error())
		return
	}

	linked := map[string]bool{}
	if links, err := s.store.ListLibraryLinksBySource(domain.LibraryGameAtlas); err == nil {
		for _, l := range links {
			linked[l.ExternalID] = true
		}
	}

	target := normalizeTitle(work.Title)
	suggestions := make([]gaSuggestion, 0)
	for _, e := range entries {
		if linked[e.PublicID] {
			continue
		}
		if e.Series == nil || normalizeTitle(e.Series.Name) != target {
			continue
		}
		suggestions = append(suggestions, gaSuggestion{
			PublicID:    e.PublicID,
			Title:       e.Title,
			TitleAlt:    e.TitleAlt,
			ReleaseDate: e.ReleaseDate,
			CoverImage:  absCover(client.BaseURL(), e.CoverImage),
			URL:         client.GameURL(e.PublicID),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": suggestions})
}

// handleGameAtlasArchive 是 POST /api/library/gameatlas/archive {workId, publicId}：
// 一键建档——在集合节点下建 stub 子节点（medium=game），并挂上 GameAtlas 外链。
func (s *Server) handleGameAtlasArchive(w http.ResponseWriter, r *http.Request) {
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
	client, err := s.gaClient()
	if err != nil {
		s.writeGAClientErr(w, err)
		return
	}

	// 先查重：这条已经挂过链就直接拒绝，避免建出一个重复节点。
	if links, err := s.store.ListLibraryLinksBySource(domain.LibraryGameAtlas); err == nil {
		for _, l := range links {
			if l.ExternalID == body.PublicID {
				writeErr(w, http.StatusConflict, "conflict", "这条已经挂过链（应该已经建过档），刷新建议列表看看")
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	entries, err := client.ListAll(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "gameatlas", err.Error())
		return
	}
	var entry *library.GameAtlasEntry
	for i := range entries {
		if entries[i].PublicID == body.PublicID {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		writeErr(w, http.StatusNotFound, "not_found", "GameAtlas 里没找到这条，刷新建议列表再试")
		return
	}

	game := domain.MediumGame
	child, err := s.store.CreateWork(domain.CreateWorkBody{
		ParentID: &parent.ID,
		Kind:     domain.WorkKindWork,
		Medium:   &game,
		Title:    entry.Title,
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	url := client.GameURL(entry.PublicID)
	hint := entry.Title
	link, err := s.store.CreateLibraryLink(child.ID, domain.LibraryGameAtlas, entry.PublicID, &url, &hint)
	if err != nil {
		// 并发下挂链失败：把刚建的节点收回去，别留下没有链的孤儿 stub。
		_ = s.store.DeleteWork(child.ID)
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"work": child, "link": link})
}

// handleGameAtlasPush 是 POST /api/library/gameatlas/push {workId, summary?}：
// 反哺——把节点已保存的正文写回 GameAtlas 条目；summary 缺省时自动从正文
// 提取一段当简介（显式给了就按显式值走，空白=清空 GameAtlas 侧的简介）。
func (s *Server) handleGameAtlasPush(w http.ResponseWriter, r *http.Request) {
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
	if work.ContentMd == nil || strings.TrimSpace(*work.ContentMd) == "" {
		writeErr(w, http.StatusBadRequest, "empty_content", "正文还是空的——先写点东西再反哺")
		return
	}
	links, err := s.store.ListLibraryLinksByWork(work.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var link *domain.LibraryLink
	for i := range links {
		if links[i].Source == domain.LibraryGameAtlas {
			link = &links[i]
			break
		}
	}
	if link == nil {
		writeErr(w, http.StatusNotFound, "no_link", "这个节点还没挂 GameAtlas 链接——先在系列页建档")
		return
	}

	client, err := s.gaClient()
	if err != nil {
		s.writeGAClientErr(w, err)
		return
	}

	summary := body.Summary
	if summary == nil {
		summary = extractWikiatlasSummary(*work.ContentMd)
	} else {
		trimmed := strings.TrimSpace(*summary)
		summary = &trimmed
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := client.PushWiki(ctx, link.ExternalID, *work.ContentMd, summary); err != nil {
		writeErr(w, http.StatusBadGateway, "gameatlas", err.Error())
		return
	}

	resp := map[string]any{
		"ok":      true,
		"gameUrl": client.GameURL(link.ExternalID),
	}
	if summary != nil && *summary != "" {
		resp["summary"] = *summary
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- 手动关联：搜索 / 挂链 / 解除 ---
//
// 建档（archive）只能从建议列表走出新节点；手工建的、Altas 写出来的老条目
// 没有链，反哺会撞 404。这一组接口补上"已有条目 ↔ GameAtlas 条目"的挂链。

type gaSearchSeries struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type gaSearchEntry struct {
	PublicID     string          `json:"publicId"`
	Title        string          `json:"title"`
	TitleAlt     *string         `json:"titleAlt"`
	ReleaseDate  *string         `json:"releaseDate"`
	CoverImage   *string         `json:"coverImage"`
	Series       *gaSearchSeries `json:"series"`
	URL          string          `json:"url"`
	Linked       bool            `json:"linked"`
	LinkedWorkID string          `json:"linkedWorkId,omitempty"`
}

// gaEntryMatches 供搜索过滤：标题/别名/系列名任一命中子串即可。
func gaEntryMatches(e library.GameAtlasEntry, norm string) bool {
	if strings.Contains(normalizeTitle(e.Title), norm) {
		return true
	}
	if e.TitleAlt != nil && strings.Contains(normalizeTitle(*e.TitleAlt), norm) {
		return true
	}
	return e.Series != nil && strings.Contains(normalizeTitle(e.Series.Name), norm)
}

// handleGameAtlasSearch 是 GET /api/library/gameatlas/search?q=：
// 按标题/别名/系列名子串搜 GameAtlas 条目（空白 = 全量，截 50 条），标出已挂链的。
func (s *Server) handleGameAtlasSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	client, err := s.gaClient()
	if err != nil {
		s.writeGAClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	entries, err := s.gaCatalogEntries(ctx, client)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "gameatlas", err.Error())
		return
	}

	linkedBy := map[string]string{}
	if links, err := s.store.ListLibraryLinksBySource(domain.LibraryGameAtlas); err == nil {
		for _, l := range links {
			linkedBy[l.ExternalID] = l.WorkID
		}
	}

	norm := normalizeTitle(query)
	out := make([]gaSearchEntry, 0)
	for _, e := range entries {
		if norm != "" && !gaEntryMatches(e, norm) {
			continue
		}
		item := gaSearchEntry{
			PublicID:    e.PublicID,
			Title:       e.Title,
			TitleAlt:    e.TitleAlt,
			ReleaseDate: e.ReleaseDate,
			CoverImage:  absCover(client.BaseURL(), e.CoverImage),
			URL:         client.GameURL(e.PublicID),
		}
		if e.Series != nil {
			item.Series = &gaSearchSeries{ID: e.Series.ID, Name: e.Series.Name}
		}
		if workID, ok := linkedBy[e.PublicID]; ok {
			item.Linked = true
			item.LinkedWorkID = workID
		}
		out = append(out, item)
	}
	if norm != "" {
		sort.SliceStable(out, func(i, j int) bool {
			return gaSearchRank(out[i], norm) < gaSearchRank(out[j], norm)
		})
	}
	if len(out) > 50 {
		out = out[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func gaSearchRank(item gaSearchEntry, norm string) int {
	title := normalizeTitle(item.Title)
	switch {
	case title == norm:
		return 0
	case strings.HasPrefix(title, norm):
		return 1
	default:
		return 2
	}
}

// handleGameAtlasLink 是 POST /api/library/gameatlas/link {workId, publicId}：
// 把一个已有节点关联到一条 GameAtlas 条目（一个节点只挂一条 GameAtlas 链）。
func (s *Server) handleGameAtlasLink(w http.ResponseWriter, r *http.Request) {
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
	client, err := s.gaClient()
	if err != nil {
		s.writeGAClientErr(w, err)
		return
	}

	if links, err := s.store.ListLibraryLinksBySource(domain.LibraryGameAtlas); err == nil {
		for _, l := range links {
			if l.ExternalID == body.PublicID {
				if l.WorkID == work.ID {
					writeErr(w, http.StatusConflict, "conflict", "这个节点已经关联过这条了")
				} else {
					writeErr(w, http.StatusConflict, "conflict", "这条已经关联到别的节点了——先去那边解除")
				}
				return
			}
			if l.WorkID == work.ID {
				writeErr(w, http.StatusConflict, "conflict", "这个节点已经关联过 GameAtlas 条目（先解除再换）")
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	entries, err := client.ListAll(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "gameatlas", err.Error())
		return
	}
	var entry *library.GameAtlasEntry
	for i := range entries {
		if entries[i].PublicID == body.PublicID {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		writeErr(w, http.StatusNotFound, "not_found", "GameAtlas 里没找到这条，刷新搜索再试")
		return
	}

	url := client.GameURL(entry.PublicID)
	hint := entry.Title
	link, err := s.store.CreateLibraryLink(work.ID, domain.LibraryGameAtlas, entry.PublicID, &url, &hint)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"link": link})
}

// handleGameAtlasUnlink 是 DELETE /api/library/gameatlas/link?workId=：
// 解除节点的 GameAtlas 关联（只动 library_links，正文不受影响）。
func (s *Server) handleGameAtlasUnlink(w http.ResponseWriter, r *http.Request) {
	workID := strings.TrimSpace(r.URL.Query().Get("workId"))
	if workID == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "workId 必填")
		return
	}
	links, err := s.store.ListLibraryLinksByWork(workID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	for _, l := range links {
		if l.Source == domain.LibraryGameAtlas {
			if err := s.store.DeleteLibraryLink(l.ID); err != nil {
				writeStoreErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "no_link", "这个节点没有 GameAtlas 关联")
}

// absCover 把条目的相对封面路径拼成绝对 URL（前端不再需要库地址前缀）。
func absCover(baseURL string, cover *string) *string {
	if cover == nil || *cover == "" {
		return nil
	}
	if strings.HasPrefix(*cover, "http://") || strings.HasPrefix(*cover, "https://") {
		return cover
	}
	u := baseURL + *cover
	return &u
}

// extractWikiatlasSummary 从正文里挑一段当简介：第一个"普通段落"
// （跳过标题/题记/围栏/引用/列表/表格），压平空白、截到 200 字。
// 挑不到就返回 nil——那是"这次只反哺正文，不动 GameAtlas 侧简介"。
func extractWikiatlasSummary(md string) *string {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	for _, paragraph := range strings.Split(md, "\n\n") {
		text := strings.TrimSpace(paragraph)
		if text == "" {
			continue
		}
		firstLine := text
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			firstLine = strings.TrimSpace(text[:i])
		}
		if firstLine == "" {
			continue
		}
		switch firstLine[0] {
		case '#', ':', '>', '|', '-', '*', '`', '[':
			continue
		}
		flat := strings.Join(strings.Fields(text), " ")
		runes := []rune(flat)
		if len(runes) > 200 {
			flat = string(runes[:200]) + "…"
		}
		return &flat
	}
	return nil
}
