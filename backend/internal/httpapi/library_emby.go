package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/library"
)

// --- 媒体库：Emby ---
//
// 载体对位：影视（电影 / 剧集）↔ Emby。与 GameAtlas 的差异：
//   · 库角色由用户在设置里标：work = 正片（喂「建档建议」）；
//     mixed = 混杂内容（喂「关联到节点」的候选，如第九艺术/动漫解析）。
//   · 只有读 + 关联，没有反哺（Emby 无写入面）。
//   · 一个节点可以挂多条 Emby 链（一部游戏可以配多条影像 + OST 专辑）。

func (s *Server) embyClient() (*library.EmbyClient, error) {
	st, err := s.store.GetSettings()
	if err != nil {
		return nil, err
	}
	var base, key string
	if st.Library.EmbyURL != nil {
		base = *st.Library.EmbyURL
	}
	if st.Library.EmbyAPIKey != nil {
		key = *st.Library.EmbyAPIKey
	}
	return library.NewEmbyClient(base, key)
}

func (s *Server) writeEmbyClientErr(w http.ResponseWriter, err error) {
	if errors.Is(err, library.ErrNotConfigured) {
		writeErr(w, http.StatusBadRequest, "not_configured", "还没配置 Emby：到设置页填好地址与 API Key")
		return
	}
	writeErr(w, http.StatusInternalServerError, "internal", err.Error())
}

// embyRoleViews 按设置里的角色把媒体库分组；没标过角色的库两边都不进（静默跳过）。
// 匹配先按 ID 再按名字——库里改名或重建都能兜住。
func (s *Server) embyRoleViews(ctx context.Context, client *library.EmbyClient) (work, mixed []library.EmbyView, err error) {
	st, err := s.store.GetSettings()
	if err != nil {
		return nil, nil, err
	}
	views, err := client.ListViews(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, v := range views {
		role := ""
		for _, r := range st.Library.EmbyLibraryRoles {
			if (r.ID != "" && r.ID == v.ID) || r.Name == v.Name {
				role = r.Role
				break
			}
		}
		switch role {
		case domain.EmbyLibraryRoleWork:
			work = append(work, v)
		case domain.EmbyLibraryRoleMixed:
			mixed = append(mixed, v)
		}
	}
	return work, mixed, nil
}

// linkedIDsBySource 返回 source 下 externalID → workID 的索引。
func (s *Server) linkedIDsBySource(source domain.LibrarySource) map[string]string {
	out := map[string]string{}
	if links, err := s.store.ListLibraryLinksBySource(source); err == nil {
		for _, l := range links {
			out[l.ExternalID] = l.WorkID
		}
	}
	return out
}

// handleEmbyViews 是 GET /api/library/emby/views：给设置页拉库清单（挑角色用）。
func (s *Server) handleEmbyViews(w http.ResponseWriter, r *http.Request) {
	client, err := s.embyClient()
	if err != nil {
		s.writeEmbyClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	views, err := client.ListViews(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "emby", err.Error())
		return
	}
	type viewOut struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		CollectionType string `json:"collectionType"`
	}
	out := make([]viewOut, 0, len(views))
	for _, v := range views {
		out = append(out, viewOut{ID: v.ID, Name: v.Name, CollectionType: v.CollectionType})
	}
	writeJSON(w, http.StatusOK, map[string]any{"views": out})
}

type embySuggestion struct {
	PublicID    string  `json:"publicId"`
	Title       string  `json:"title"`
	TitleAlt    *string `json:"titleAlt"`
	ReleaseDate *string `json:"releaseDate"`
	CoverImage  *string `json:"coverImage"`
	URL         string  `json:"url"`
	Kind        string  `json:"kind"` // tv | movie → 建档介质
}

type embyMediaEntry struct {
	embySuggestion
	Linked       bool   `json:"linked"`
	LinkedWorkID string `json:"linkedWorkId,omitempty"`
}

func stripBoxSetSuffix(name string) string {
	s := strings.TrimSpace(name)
	for _, suf := range []string{"（系列）", "(系列)"} {
		s = strings.TrimSuffix(s, suf)
	}
	return strings.TrimSpace(s)
}

func embyYear(it *library.EmbyItem) *string {
	if it.ProductionYear == nil || *it.ProductionYear <= 0 {
		return nil
	}
	y := strconv.Itoa(*it.ProductionYear)
	return &y
}

func embySuggestionOf(client *library.EmbyClient, it *library.EmbyItem, kind string) embySuggestion {
	return embySuggestion{
		PublicID:    it.ID,
		Title:       it.Name,
		TitleAlt:    it.OriginalTitle,
		ReleaseDate: embyYear(it),
		CoverImage:  client.CoverURL(it),
		URL:         client.ItemURL(it.ID),
		Kind:        kind,
	}
}

func embyKindOf(it *library.EmbyItem) string {
	switch it.Type {
	case "MusicAlbum":
		return "album"
	case "Series":
		return "series"
	case "BoxSet":
		return "collection"
	default:
		return "movie"
	}
}

func (s *Server) embyEntryWithLink(client *library.EmbyClient, it *library.EmbyItem, linked map[string]string) embyMediaEntry {
	e := embyMediaEntry{embySuggestion: embySuggestionOf(client, it, embyKindOf(it))}
	if wid, ok := linked[it.ID]; ok {
		e.Linked = true
		e.LinkedWorkID = wid
	}
	return e
}

// embyItemRelatedTo 是「混杂库 → 节点」的匹配：节点标题（归一化）出现在
// 条目名 / 目录路径 / 所属剧集名里。目录匹配让"第一集.mp4"这种也能靠文件夹归位。
func embyItemRelatedTo(it *library.EmbyItem, target string) bool {
	if strings.Contains(normalizeTitle(it.Name), target) {
		return true
	}
	if it.Path != nil && strings.Contains(normalizeTitle(*it.Path), target) {
		return true
	}
	return it.SeriesName != nil && strings.Contains(normalizeTitle(*it.SeriesName), target)
}

func embyNameRank(name, norm string) int {
	n := normalizeTitle(name)
	switch {
	case n == norm:
		return 0
	case norm != "" && strings.HasPrefix(n, norm):
		return 1
	default:
		return 2
	}
}

// handleEmbySuggest 是 GET /api/library/emby/suggest?workId=：
// 集合页建档建议——正片库里同名 Series；同名 BoxSet（去掉"（系列）"后缀）的成员电影。
func (s *Server) handleEmbySuggest(w http.ResponseWriter, r *http.Request) {
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
	client, err := s.embyClient()
	if err != nil {
		s.writeEmbyClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	workViews, _, err := s.embyRoleViews(ctx, client)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "emby", err.Error())
		return
	}

	target := normalizeTitle(work.Title)
	linked := s.linkedIDsBySource(domain.LibraryEmby)
	seen := map[string]bool{}
	out := make([]embySuggestion, 0)
	if target != "" {
		for _, v := range workViews {
			items, err := client.ListItems(ctx, v.ID, "Series,Movie,BoxSet")
			if err != nil {
				writeErr(w, http.StatusBadGateway, "emby", err.Error())
				return
			}
			for i := range items {
				it := items[i]
				switch it.Type {
				case "Series":
					if normalizeTitle(it.Name) != target || seen[it.ID] {
						continue
					}
					if _, isLinked := linked[it.ID]; isLinked {
						continue
					}
					seen[it.ID] = true
					out = append(out, embySuggestionOf(client, &it, "tv"))
				case "BoxSet":
					if normalizeTitle(stripBoxSetSuffix(it.Name)) != target {
						continue
					}
					members, err := client.ListItems(ctx, it.ID, "Movie")
					if err != nil {
						writeErr(w, http.StatusBadGateway, "emby", err.Error())
						return
					}
					for j := range members {
						m := members[j]
						if seen[m.ID] {
							continue
						}
						if _, isLinked := linked[m.ID]; isLinked {
							continue
						}
						seen[m.ID] = true
						out = append(out, embySuggestionOf(client, &m, "movie"))
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

// handleEmbyArchive 是 POST /api/library/emby/archive {workId, publicId}：
// 一键建档——建 stub 子节点（Series→tv、Movie→movie）+ 挂 Emby 链。
func (s *Server) handleEmbyArchive(w http.ResponseWriter, r *http.Request) {
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
	client, err := s.embyClient()
	if err != nil {
		s.writeEmbyClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	item, err := client.GetItem(ctx, body.PublicID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "Emby 里没找到这条，刷新建议列表再试")
		return
	}
	if item.Type != "Series" && item.Type != "Movie" {
		writeErr(w, http.StatusBadRequest, "bad_type", "只支持给剧集/电影建档")
		return
	}
	if wid, ok := s.linkedIDsBySource(domain.LibraryEmby)[item.ID]; ok {
		if wid == parent.ID {
			writeErr(w, http.StatusConflict, "conflict", "这个节点已经关联过这条了")
		} else {
			writeErr(w, http.StatusConflict, "conflict", "这条已经关联到别的节点了——先去那边解除")
		}
		return
	}

	medium := domain.MediumTV
	if item.Type == "Movie" {
		medium = domain.MediumMovie
	}
	child, err := s.store.CreateWork(domain.CreateWorkBody{
		ParentID: &parent.ID,
		Kind:     domain.WorkKindWork,
		Medium:   &medium,
		Title:    item.Name,
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	url := client.ItemURL(item.ID)
	hint := item.Name
	link, err := s.store.CreateLibraryLink(child.ID, domain.LibraryEmby, item.ID, &url, &hint)
	if err != nil {
		_ = s.store.DeleteWork(child.ID)
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"work": child, "link": link})
}

// handleEmbyRelated 是 GET /api/library/emby/related?workId=：
// 节点页的「Emby 影像/OST」候选——混杂库里名字或目录命中本节点标题的条目。
func (s *Server) handleEmbyRelated(w http.ResponseWriter, r *http.Request) {
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
	client, err := s.embyClient()
	if err != nil {
		s.writeEmbyClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	_, mixed, err := s.embyRoleViews(ctx, client)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "emby", err.Error())
		return
	}

	target := normalizeTitle(work.Title)
	out := make([]embyMediaEntry, 0)
	if len([]rune(target)) >= 2 {
		linked := s.linkedIDsBySource(domain.LibraryEmby)
		for _, v := range mixed {
			items, err := client.ListItems(ctx, v.ID, "Movie,Series,MusicAlbum")
			if err != nil {
				writeErr(w, http.StatusBadGateway, "emby", err.Error())
				return
			}
			for i := range items {
				it := items[i]
				if !embyItemRelatedTo(&it, target) {
					continue
				}
				out = append(out, s.embyEntryWithLink(client, &it, linked))
			}
		}
	}
	if len(out) > 120 {
		out = out[:120]
	}
	writeJSON(w, http.StatusOK, map[string]any{"related": out})
}

// handleEmbySearch 是 GET /api/library/emby/search?q=：全库搜索（关联弹窗用）。
func (s *Server) handleEmbySearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	client, err := s.embyClient()
	if err != nil {
		s.writeEmbyClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	// 合集（BoxSet）也搜得到——合集名和节点名对不上时（如 007 ↔ 詹姆斯·邦德（系列）），
	// 用它把整个合集挂到集合节点上。
	items, err := client.ListItems(ctx, "", "Series,Movie,MusicAlbum,BoxSet")
	if err != nil {
		writeErr(w, http.StatusBadGateway, "emby", err.Error())
		return
	}

	norm := normalizeTitle(query)
	linked := s.linkedIDsBySource(domain.LibraryEmby)
	entries := make([]embyMediaEntry, 0, 64)
	for i := range items {
		it := items[i]
		if norm != "" &&
			!strings.Contains(normalizeTitle(it.Name), norm) &&
			!(it.OriginalTitle != nil && strings.Contains(normalizeTitle(*it.OriginalTitle), norm)) &&
			!(it.SeriesName != nil && strings.Contains(normalizeTitle(*it.SeriesName), norm)) {
			continue
		}
		entries = append(entries, s.embyEntryWithLink(client, &it, linked))
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return embyNameRank(entries[i].Title, norm) < embyNameRank(entries[j].Title, norm)
	})
	if len(entries) > 50 {
		entries = entries[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// handleEmbyLink 是 POST /api/library/emby/link {workId, publicId}：
// 把节点关联到一条 Emby 条目。GameAtlas 限一条，Emby 不限（影像 + OST 可能多条），
// 但一条 Emby 条目仍然只能挂在一个节点上（library_links 的唯一约束）。
func (s *Server) handleEmbyLink(w http.ResponseWriter, r *http.Request) {
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
	client, err := s.embyClient()
	if err != nil {
		s.writeEmbyClientErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	item, err := client.GetItem(ctx, body.PublicID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "Emby 里没找到这条，刷新搜索再试")
		return
	}
	switch item.Type {
	case "Series", "Movie", "MusicAlbum", "BoxSet":
	default:
		writeErr(w, http.StatusBadRequest, "bad_type", "只支持关联剧集/电影/专辑/合集")
		return
	}
	if wid, ok := s.linkedIDsBySource(domain.LibraryEmby)[item.ID]; ok {
		if wid == work.ID {
			writeErr(w, http.StatusConflict, "conflict", "这个节点已经关联过这条了")
		} else {
			writeErr(w, http.StatusConflict, "conflict", "这条已经关联到别的节点了——先去那边解除")
		}
		return
	}

	url := client.ItemURL(item.ID)
	hint := item.Name
	link, err := s.store.CreateLibraryLink(work.ID, domain.LibraryEmby, item.ID, &url, &hint)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"link": link})
}

// handleEmbyUnlink 是 DELETE /api/library/emby/link?id=<linkId>：
// 按链接 id 解除（一个节点可能挂多条，不能按 workId 一刀切）。
func (s *Server) handleEmbyUnlink(w http.ResponseWriter, r *http.Request) {
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
	if link.Source != domain.LibraryEmby {
		writeErr(w, http.StatusBadRequest, "bad_source", "不是 Emby 链接")
		return
	}
	if err := s.store.DeleteLibraryLink(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
