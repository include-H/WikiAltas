// Package httpapi serves the WikiAltas REST + SSE API.
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/run"
	"wikiatlas/backend/internal/store"
)

// Server holds dependencies.
type Server struct {
	store *store.Store
	runs  *run.Manager
	mux   *http.ServeMux
}

// New builds the API server.
func New(st *store.Store, runs *run.Manager) *Server {
	s := &Server{store: st, runs: runs, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the root handler with CORS.
func (s *Server) Handler() http.Handler {
	// 默认拒绝：未登录只能走访客白名单（只读公开内容）
	return cors(s.withAuth(s.mux))
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, Last-Event-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	m := s.mux
	// health
	m.HandleFunc("GET /api/health", s.handleHealth)

	// 简易用户系统
	m.HandleFunc("POST /api/auth/login", s.handleLogin)
	m.HandleFunc("POST /api/auth/logout", s.handleLogout)
	m.HandleFunc("GET /api/auth/me", s.handleMe)

	// tree
	m.HandleFunc("GET /api/tree", s.handleTree)

	// works
	m.HandleFunc("GET /api/works/{id}", s.handleGetWork)
	m.HandleFunc("POST /api/works", s.handleCreateWork)
	m.HandleFunc("PATCH /api/works/{id}", s.handlePatchWork)
	m.HandleFunc("PUT /api/works/{id}/content", s.handlePutWorkContent)
	m.HandleFunc("DELETE /api/works/{id}", s.handleDeleteWork)
	m.HandleFunc("GET /api/works/{id}/docs", s.handleListDocs)
	m.HandleFunc("POST /api/works/{id}/docs", s.handleCreateDoc)
	m.HandleFunc("GET /api/works/{id}/relations", s.handleListRelations)
	m.HandleFunc("GET /api/works/{id}/revisions", s.handleListWorkRevisions)
	m.HandleFunc("POST /api/works/{id}/revisions/{revId}/restore", s.handleRestoreWorkRevision)

	// docs
	m.HandleFunc("GET /api/docs/{id}", s.handleGetDoc)
	m.HandleFunc("PATCH /api/docs/{id}", s.handlePatchDoc)
	m.HandleFunc("PUT /api/docs/{id}/content", s.handlePutDocContent)
	m.HandleFunc("GET /api/docs/{id}/revisions", s.handleListDocRevisions)

	// relations
	m.HandleFunc("POST /api/relations", s.handleCreateRelation)
	m.HandleFunc("DELETE /api/relations/{id}", s.handleDeleteRelation)

	// runs
	m.HandleFunc("POST /api/runs", s.handleCreateRun)
	m.HandleFunc("POST /api/runs/batch", s.handleCreateBatch)
	m.HandleFunc("POST /api/runs/{id}/resume", s.handleResumeRun)
	m.HandleFunc("POST /api/runs/{id}/cancel", s.handleCancelRun)
	m.HandleFunc("GET /api/runs", s.handleListRuns)
	m.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	m.HandleFunc("GET /api/runs/{id}/events/stream", s.handleRunEventStream)

	// library
	m.HandleFunc("GET /api/library/sources", s.handleLibrarySources)
	m.HandleFunc("POST /api/library/sync", s.handleLibrarySync)

	// search
	m.HandleFunc("GET /api/search", s.handleSearch)

	// settings
	m.HandleFunc("GET /api/settings", s.handleGetSettings)
	m.HandleFunc("PUT /api/settings", s.handlePutSettings)
	m.HandleFunc("POST /api/settings/test-llm", s.handleTestLLM)
	m.HandleFunc("POST /api/settings/import-env", s.handleImportEnvSettings)
	m.HandleFunc("GET /api/runtime", s.handleRuntime)
}

// --- helpers ---

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	var e apiError
	e.Error.Code = code
	e.Error.Message = msg
	writeJSON(w, status, e)
}

func writeStoreErr(w http.ResponseWriter, err error) {
	var nf store.ErrNotFound
	var cf store.ErrConflict
	var vv store.ErrValidation
	switch {
	case errors.As(err, &nf):
		writeErr(w, http.StatusNotFound, "not_found", nf.Error())
	case errors.As(err, &cf):
		writeErr(w, http.StatusConflict, "conflict", cf.Error())
	case errors.As(err, &vv):
		writeErr(w, http.StatusBadRequest, "validation", vv.Error())
	default:
		log.Printf("internal error: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal", "internal server error")
	}
}

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// --- health ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// --- tree ---

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListTree()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	// 访客只看得到"自身与祖先都是 public"的节点
	if !s.isAuthed(r) {
		public, err := s.store.PublicWorkIDs()
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		visible := make([]domain.WorkSummary, 0, len(nodes))
		for _, n := range nodes {
			if public[n.ID] {
				visible = append(visible, n)
			}
		}
		nodes = visible
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// --- works ---

func (s *Server) handleCreateWork(w http.ResponseWriter, r *http.Request) {
	var body domain.CreateWorkBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	work, err := s.store.CreateWork(body)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, work)
}

func (s *Server) handleGetWork(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.isAuthed(r) && !s.store.IsPublicWork(id) {
		writeErr(w, http.StatusNotFound, "not_found", "文档不存在或未公开")
		return
	}
	work, err := s.store.GetWork(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	detail := domain.WorkDetail{Work: *work}
	detail.LibraryLinks = []domain.LibraryLink{}
	detail.Relations = []domain.Relation{}
	if links, err := s.store.ListLibraryLinksByWork(id); err == nil {
		detail.LibraryLinks = links
	}
	if rels, err := s.store.ListRelationsByWork(id); err == nil {
		detail.Relations = rels
	}
	if revs, err := s.store.ListRevisions("work", id, 1, false); err == nil && len(revs) > 0 {
		detail.LatestRevision = &revs[0]
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handlePatchWork(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body domain.PatchWorkBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	work, err := s.store.PatchWork(id, body)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Server) handlePutWorkContent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body domain.PutContentBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	res, err := s.store.PutWorkContent(id, body)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleDeleteWork(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteWork(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- docs ---

func (s *Server) handleListDocs(w http.ResponseWriter, r *http.Request) {
	workID := r.PathValue("id")
	if !s.isAuthed(r) && !s.store.IsPublicWork(workID) {
		writeErr(w, http.StatusNotFound, "not_found", "文档不存在或未公开")
		return
	}
	if _, err := s.store.GetWork(workID); err != nil {
		writeStoreErr(w, err)
		return
	}
	docs, err := s.store.ListDocsByWork(workID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"docs": docs})
}

func (s *Server) handleCreateDoc(w http.ResponseWriter, r *http.Request) {
	workID := r.PathValue("id")
	var body domain.CreateDocBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	doc, err := s.store.CreateDoc(workID, body)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, doc)
}

func (s *Server) handleGetDoc(w http.ResponseWriter, r *http.Request) {
	doc, err := s.store.GetDoc(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if !s.isAuthed(r) && !s.store.IsPublicWork(doc.FolderOf) {
		writeErr(w, http.StatusNotFound, "not_found", "资料不存在或未公开")
		return
	}
	// 与 GET /api/works/{id} 保持一致：单资源包一层，前端按 { doc } 解
	writeJSON(w, http.StatusOK, map[string]any{"doc": doc})
}

func (s *Server) handlePatchDoc(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body domain.PatchDocBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	doc, err := s.store.PatchDoc(id, body)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handlePutDocContent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body domain.PutContentBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	res, err := s.store.PutDocContent(id, body)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- relations ---

func (s *Server) handleListRelations(w http.ResponseWriter, r *http.Request) {
	workID := r.PathValue("id")
	if _, err := s.store.GetWork(workID); err != nil {
		writeStoreErr(w, err)
		return
	}
	rels, err := s.store.ListRelationsByWork(workID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"relations": rels})
}

func (s *Server) handleCreateRelation(w http.ResponseWriter, r *http.Request) {
	var body domain.CreateRelationBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	rel, err := s.store.CreateRelation(body)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rel)
}

func (s *Server) handleDeleteRelation(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRelation(r.PathValue("id")); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- revisions ---

func (s *Server) handleListWorkRevisions(w http.ResponseWriter, r *http.Request) {
	s.listRevisions(w, r, "work", r.PathValue("id"))
}

func (s *Server) handleListDocRevisions(w http.ResponseWriter, r *http.Request) {
	s.listRevisions(w, r, "doc", r.PathValue("id"))
}

func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request, targetType, targetID string) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	includeContent := r.URL.Query().Get("detail") == "true"
	revs, err := s.store.ListRevisions(targetType, targetID, limit, includeContent)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": revs})
}

func (s *Server) handleRestoreWorkRevision(w http.ResponseWriter, r *http.Request) {
	workID := r.PathValue("id")
	revID := r.PathValue("revId")
	res, err := s.store.RestoreRevision("work", workID, revID, domain.AuthorHuman)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- runs ---

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var body domain.CreateRunBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if body.Goal == "" {
		writeErr(w, http.StatusBadRequest, "validation", "goal is required")
		return
	}
	run, err := s.runs.CreateAndStart(body)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"runId": run.ID, "run": run})
}

func (s *Server) handleResumeRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.runs.Resume(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runId": run.ID, "run": run})
}

// handleCreateBatch 批量建档：给一批作品节点各开一个 create_wiki 工单，
// 共享一个 batch:<id> 会话键；并发由 worker 池控制。
func (s *Server) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	var body domain.CreateBatchBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	res, err := s.runs.CreateBatch(body)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if err := s.runs.Cancel(r.PathValue("id")); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	workspace := r.URL.Query().Get("workspace")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	runs, err := s.store.ListRuns(status, workspace, limit)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.store.GetRun(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	// 事件分页：面板刷新/长工单重建时按 afterSeq 续拉
	afterSeq := int64(0)
	if v := r.URL.Query().Get("afterSeq"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			afterSeq = n
		}
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	events, _ := s.store.ListRunEvents(id, afterSeq, limit)
	if events == nil {
		events = []domain.RunEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "events": events})
}

// handleRunEventStream is SSE with Last-Event-ID resume.
func (s *Server) handleRunEventStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.GetRun(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// resume from Last-Event-ID header or ?lastEventId=
	lastSeq := int64(0)
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			lastSeq = n
		}
	}
	if v := r.URL.Query().Get("lastEventId"); v != "" && lastSeq == 0 {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			lastSeq = n
		}
	}

	// replay backlog
	history, err := s.store.ListRunEvents(id, lastSeq, 500)
	if err == nil {
		for _, ev := range history {
			writeSSE(w, ev)
			lastSeq = ev.Seq
		}
		flusher.Flush()
	}

	sub := s.runs.Subscribe(id)
	defer s.runs.Unsubscribe(id, sub)

	ctx := r.Context()
	heartbeat := heartbeatTicker()
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub:
			if !ok {
				return
			}
			if ev.Seq <= lastSeq {
				continue
			}
			writeSSE(w, ev)
			lastSeq = ev.Seq
			flusher.Flush()
		case <-heartbeat.C:
			// keep-alive comment
			_, _ = w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, ev domain.RunEvent) {
	b, _ := json.Marshal(ev.Payload)
	_, _ = w.Write([]byte("id: " + strconv.FormatInt(ev.Seq, 10) + "\n"))
	_, _ = w.Write([]byte("event: " + ev.Type + "\n"))
	_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
}

// --- library ---

func (s *Server) handleLibrarySources(w http.ResponseWriter, _ *http.Request) {
	st, err := s.store.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	sources := []map[string]any{}
	if st.Library.EmbyURL != nil && *st.Library.EmbyURL != "" {
		sources = append(sources, map[string]any{"source": "emby", "url": *st.Library.EmbyURL, "configured": true})
	}
	if st.Library.KomgaURL != nil && *st.Library.KomgaURL != "" {
		sources = append(sources, map[string]any{"source": "komga", "url": *st.Library.KomgaURL, "configured": true})
	}
	if st.Library.GameAtlasURL != nil && *st.Library.GameAtlasURL != "" {
		sources = append(sources, map[string]any{"source": "gameatlas", "url": *st.Library.GameAtlasURL, "configured": true})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
}

func (s *Server) handleLibrarySync(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source string `json:"source"`
	}
	_ = decodeBody(r, &body)
	// P0: create a sync_library run; actual library pull is later phase.
	run, err := s.runs.CreateAndStart(domain.CreateRunBody{
		Intent: domain.RunIntentSyncLibrary,
		Goal:   "同步媒体库" + body.Source,
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"runId": run.ID})
}

// --- search ---

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	kind := r.URL.Query().Get("kind")
	hits, err := s.store.Search(q, kind, 20)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if !s.isAuthed(r) {
		public, err := s.store.PublicWorkIDs()
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		visible := make([]domain.SearchHit, 0, len(hits))
		for _, h := range hits {
			if h.Kind == "work" {
				if public[h.ID] {
					visible = append(visible, h)
				}
				continue
			}
			if doc, err := s.store.GetDoc(h.ID); err == nil && public[doc.FolderOf] {
				visible = append(visible, h)
			}
		}
		hits = visible
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits})
}

// --- settings ---

func (s *Server) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	st, err := s.store.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsForResponse(st))
}

// settingsForResponse 统一收口：只回"是否已配置"，绝不回密钥原文。
func (s *Server) settingsForResponse(st *domain.Settings) *domain.Settings {
	out := *st
	out.LLM.APIKeyConfigured = s.store.HasAPIKey()
	out.Search.ExaAPIKeyConfigured = s.store.ExaAPIKey() != ""
	out.Library.EmbyAPIKeyConfigured = st.Library.EmbyAPIKey != nil && *st.Library.EmbyAPIKey != ""
	out.Library.KomgaAPIKeyConfigured = st.Library.KomgaAPIKey != nil && *st.Library.KomgaAPIKey != ""
	out.Library.GameAtlasAPIKeyConfigured = st.Library.GameAtlasAPIKey != nil && *st.Library.GameAtlasAPIKey != ""
	out.Library.EmbyAPIKey = nil
	out.Library.KomgaAPIKey = nil
	out.Library.GameAtlasAPIKey = nil
	out.Admin.PasswordConfigured = s.store.AdminPasswordConfigured()
	out.Admin.UsingDefaultPassword = s.store.UsingDefaultPassword()
	return &out
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	s.putSettings(w, r)
}
