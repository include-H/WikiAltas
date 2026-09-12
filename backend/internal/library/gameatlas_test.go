package library

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGameAtlas 是对 GameAtlas 关键接口的最小仿真：登录发 Cookie、
// 列表与反哺都校验会话，反哺把请求体存下来供断言。
type fakeGameAtlas struct {
	password string
	logins   int
	lastPush map[string]any
}

func (f *fakeGameAtlas) hasSession(r *http.Request) bool {
	c, err := r.Cookie("gameatlas_admin")
	return err == nil && c.Value == "sess-1"
}

func writeFakeEnvelope(w http.ResponseWriter, status int, success bool, errMsg string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	payload := map[string]any{"success": success, "data": data}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func newFakeGameAtlas(t *testing.T) (*httptest.Server, *fakeGameAtlas) {
	t.Helper()
	f := &fakeGameAtlas{password: "pw"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Password != f.password {
			writeFakeEnvelope(w, http.StatusUnauthorized, false, "管理员密码不正确", nil)
			return
		}
		f.logins++
		http.SetCookie(w, &http.Cookie{Name: "gameatlas_admin", Value: "sess-1", Path: "/"})
		writeFakeEnvelope(w, http.StatusOK, true, "", map[string]any{"is_admin": true})
	})
	mux.HandleFunc("GET /api/games/all", func(w http.ResponseWriter, r *http.Request) {
		if !f.hasSession(r) {
			writeFakeEnvelope(w, http.StatusUnauthorized, false, "需要管理员登录", nil)
			return
		}
		writeFakeEnvelope(w, http.StatusOK, true, "", []GameAtlasEntry{
			{PublicID: "abc12345", Title: "使命召唤：无限战争", Series: &GameAtlasSeries{ID: 7, Name: "使命召唤"}},
			{PublicID: "def67890", Title: "无系列条目"},
		})
	})
	mux.HandleFunc("PUT /api/games/{publicID}/wiki", func(w http.ResponseWriter, r *http.Request) {
		if !f.hasSession(r) {
			writeFakeEnvelope(w, http.StatusUnauthorized, false, "需要管理员登录", nil)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.lastPush = body
		writeFakeEnvelope(w, http.StatusOK, true, "", map[string]any{"game_id": 1})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, f
}

func TestClientListAllLogsInOnceAndReturnsEntries(t *testing.T) {
	ts, f := newFakeGameAtlas(t)
	client, err := NewClient(ts.URL, "pw")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	entries, err := client.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].PublicID != "abc12345" || entries[0].Series == nil || entries[0].Series.Name != "使命召唤" {
		t.Fatalf("entries[0] = %+v, want 使命召唤：无限战争 with series", entries[0])
	}

	// 第二次调用复用同一会话，不应再登录
	if _, err := client.ListAll(context.Background()); err != nil {
		t.Fatalf("second ListAll: %v", err)
	}
	if f.logins != 1 {
		t.Fatalf("logins = %d, want 1", f.logins)
	}
}

func TestClientPushWikiCarriesOptionalSummary(t *testing.T) {
	ts, f := newFakeGameAtlas(t)
	client, err := NewClient(ts.URL, "pw")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if err := client.PushWiki(context.Background(), "abc12345", "# 正文", nil); err != nil {
		t.Fatalf("PushWiki without summary: %v", err)
	}
	if f.lastPush["content"] != "# 正文" {
		t.Fatalf("content = %v, want 正文", f.lastPush["content"])
	}
	if _, ok := f.lastPush["summary"]; ok {
		t.Fatalf("summary should be omitted when nil, got %v", f.lastPush["summary"])
	}
	if f.lastPush["change_summary"] != "反哺自 WikiAltas" {
		t.Fatalf("change_summary = %v", f.lastPush["change_summary"])
	}

	summary := "一段简介"
	if err := client.PushWiki(context.Background(), "abc12345", "# 正文", &summary); err != nil {
		t.Fatalf("PushWiki with summary: %v", err)
	}
	if f.lastPush["summary"] != "一段简介" {
		t.Fatalf("summary = %v, want 一段简介", f.lastPush["summary"])
	}
}

func TestClientWrongPasswordSurfacesMessage(t *testing.T) {
	ts, _ := newFakeGameAtlas(t)
	client, err := NewClient(ts.URL, "wrong")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.ListAll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "管理员密码不正确") {
		t.Fatalf("err = %v, want login failure message", err)
	}
}

func TestNewClientRejectsMissingConfig(t *testing.T) {
	if _, err := NewClient("", "pw"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("empty url err = %v, want ErrNotConfigured", err)
	}
	if _, err := NewClient("http://example", "  "); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("empty password err = %v, want ErrNotConfigured", err)
	}
}
