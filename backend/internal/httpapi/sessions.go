package httpapi

import (
	"net/http"

	"wikiatlas/backend/internal/domain"
)

// --- sessions（会话 = 一段连续对话；runs.workspace 即会话 id） ---

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	// target 可空：空 = 列出全部会话。工单页要的就是"所有可继续的对话"。
	target := r.URL.Query().Get("target")
	sessions, err := s.store.ListSessions(target)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body domain.CreateSessionBody
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	sess, err := s.store.CreateSession(body.Target, body.Title)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": sess})
}

func (s *Server) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if err := s.store.RenameSession(r.PathValue("id"), body.Title); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeleteSession：删整段会话（含其全部工单）。正在跑的工单先停掉，
// 避免删了还在写的孤儿执行器（和单条工单删除同一套理由）。
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if runs, err := s.store.ListRuns(string(domain.RunStatusRunning), id, 200); err == nil {
		for _, run := range runs {
			_ = s.runs.Cancel(run.ID)
		}
	}
	if err := s.store.DeleteSession(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
