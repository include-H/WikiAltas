package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeGA 是内嵌在 WikiAltas 接口测试里的 GameAtlas 仿真。
type fakeGA struct {
	lastPush map[string]any
}

func newFakeGA(t *testing.T) (*httptest.Server, *fakeGA) {
	t.Helper()
	f := &fakeGA{}
	mux := http.NewServeMux()
	writeEnv := func(w http.ResponseWriter, status int, success bool, errMsg string, data any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		payload := map[string]any{"success": success, "data": data}
		if errMsg != "" {
			payload["error"] = errMsg
		}
		_ = json.NewEncoder(w).Encode(payload)
	}
	hasSession := func(r *http.Request) bool {
		c, err := r.Cookie("gameatlas_admin")
		return err == nil && c.Value == "sess-1"
	}
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Password != "ga-pw" {
			writeEnv(w, http.StatusUnauthorized, false, "管理员密码不正确", nil)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "gameatlas_admin", Value: "sess-1", Path: "/"})
		writeEnv(w, http.StatusOK, true, "", map[string]any{"is_admin": true})
	})
	mux.HandleFunc("GET /api/games/all", func(w http.ResponseWriter, r *http.Request) {
		if !hasSession(r) {
			writeEnv(w, http.StatusUnauthorized, false, "需要管理员登录", nil)
			return
		}
		writeEnv(w, http.StatusOK, true, "", []map[string]any{
			{"public_id": "abc12345", "title": "使命召唤：无限战争", "series": map[string]any{"id": 7, "name": "使命召唤"}},
			{"public_id": "def67890", "title": "一条不该出现的游戏", "series": map[string]any{"id": 8, "name": "别对不上号"}},
		})
	})
	mux.HandleFunc("PUT /api/games/{publicID}/wiki", func(w http.ResponseWriter, r *http.Request) {
		if !hasSession(r) {
			writeEnv(w, http.StatusUnauthorized, false, "需要管理员登录", nil)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.lastPush = body
		writeEnv(w, http.StatusOK, true, "", map[string]any{"game_id": 1})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, f
}

func TestGameAtlasSuggestArchivePushFlow(t *testing.T) {
	ga, fga := newFakeGA(t)
	ts, _ := newTestServer(t)

	doJSON(t, "PUT", ts.URL+"/api/settings", map[string]any{
		"library": map[string]any{"gameatlasUrl": ga.URL, "gameatlasApiKey": "ga-pw"},
	}, 200)

	series := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{
		"kind": "series", "title": "使命召唤", "visibility": "public",
	}, 201)
	seriesID := series["id"].(string)

	// 建议：只回同系列且未挂链的条目
	sug := doJSON(t, "GET", ts.URL+"/api/library/gameatlas/suggest?workId="+seriesID, nil, 200)
	list, _ := sug["suggestions"].([]any)
	if len(list) != 1 {
		t.Fatalf("suggestions = %#v, want exactly 1", sug["suggestions"])
	}
	first, _ := list[0].(map[string]any)
	if first["publicId"] != "abc12345" || first["title"] != "使命召唤：无限战争" {
		t.Fatalf("first suggestion = %#v", first)
	}
	if url, _ := first["url"].(string); !strings.HasPrefix(url, ga.URL) {
		t.Fatalf("suggestion url = %q, want GA deep link", first["url"])
	}

	// 一键建档：建 stub 子节点 + 挂链
	arch := doJSON(t, "POST", ts.URL+"/api/library/gameatlas/archive", map[string]any{
		"workId": seriesID, "publicId": "abc12345",
	}, 201)
	work, _ := arch["work"].(map[string]any)
	workID, _ := work["id"].(string)
	if workID == "" || work["title"] != "使命召唤：无限战争" || work["medium"] != "game" {
		t.Fatalf("archived work = %#v", work)
	}
	if work["status"] != "stub" {
		t.Fatalf("archived status = %v, want stub", work["status"])
	}

	// 挂过链之后建议不再出现
	sug2 := doJSON(t, "GET", ts.URL+"/api/library/gameatlas/suggest?workId="+seriesID, nil, 200)
	if l, _ := sug2["suggestions"].([]any); len(l) != 0 {
		t.Fatalf("after archive suggestions = %#v, want empty", sug2["suggestions"])
	}

	// 空正文反哺 → 400（先写东西）
	doJSON(t, "POST", ts.URL+"/api/library/gameatlas/push", map[string]any{"workId": workID}, 400)

	// 写正文（第一段普通文字就是简介来源），反哺
	doJSON(t, "PUT", ts.URL+"/api/works/"+workID+"/content", map[string]any{
		"contentMd": "# 使命召唤：无限战争\n\n这是自动提取的简介。\n\n## 概览\n\n正文开始。",
		"author":    "human",
	}, 200)
	doJSON(t, "POST", ts.URL+"/api/library/gameatlas/push", map[string]any{"workId": workID}, 200)
	if fga.lastPush["summary"] != "这是自动提取的简介。" {
		t.Fatalf("pushed summary = %v, want 自动提取的简介", fga.lastPush["summary"])
	}
	if content, _ := fga.lastPush["content"].(string); !strings.Contains(content, "正文开始") {
		t.Fatalf("pushed content = %q, want full markdown", fga.lastPush["content"])
	}

	// 显式简介覆盖自动提取（并去空白）
	doJSON(t, "POST", ts.URL+"/api/library/gameatlas/push", map[string]any{
		"workId": workID, "summary": "  手写简介  ",
	}, 200)
	if fga.lastPush["summary"] != "手写简介" {
		t.Fatalf("explicit summary = %v, want trimmed 手写简介", fga.lastPush["summary"])
	}

	// 没挂链的节点反哺 → 404
	lone := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "work", "title": "无链条目"}, 201)
	doJSON(t, "PUT", ts.URL+"/api/works/"+lone["id"].(string)+"/content", map[string]any{
		"contentMd": "# 无链", "author": "human",
	}, 200)
	doJSON(t, "POST", ts.URL+"/api/library/gameatlas/push", map[string]any{"workId": lone["id"]}, 404)
}

// 手工建的（非建档流程出来的）老条目：搜索 → 挂链 → 反哺 → 解除，全程走通。
func TestGameAtlasSearchLinkUnlinkExistingWork(t *testing.T) {
	ga, _ := newFakeGA(t)
	ts, _ := newTestServer(t)

	doJSON(t, "PUT", ts.URL+"/api/settings", map[string]any{
		"library": map[string]any{"gameatlasUrl": ga.URL, "gameatlasApiKey": "ga-pw"},
	}, 200)

	work := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{
		"kind": "work", "title": "使命召唤：无限战争",
	}, 201)
	workID := work["id"].(string)

	// 搜索：子串命中，未挂链
	sr := doJSON(t, "GET", ts.URL+"/api/library/gameatlas/search?q="+url.QueryEscape("无限战争"), nil, 200)
	entries, _ := sr["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("search entries = %#v, want exactly 1", sr["entries"])
	}
	e0, _ := entries[0].(map[string]any)
	if e0["publicId"] != "abc12345" || e0["linked"] != false {
		t.Fatalf("search entry = %#v", e0)
	}

	// 挂链
	lk := doJSON(t, "POST", ts.URL+"/api/library/gameatlas/link", map[string]any{
		"workId": workID, "publicId": "abc12345",
	}, 201)
	if _, ok := lk["link"]; !ok {
		t.Fatalf("link response = %#v, want link object", lk)
	}

	// 再搜：标出已挂链与去向
	sr2 := doJSON(t, "GET", ts.URL+"/api/library/gameatlas/search?q="+url.QueryEscape("无限战争"), nil, 200)
	e2, _ := sr2["entries"].([]any)[0].(map[string]any)
	if e2["linked"] != true || e2["linkedWorkId"] != workID {
		t.Fatalf("linked entry = %#v", e2)
	}

	// 重复挂链 / 同节点换链 → 409
	doJSON(t, "POST", ts.URL+"/api/library/gameatlas/link", map[string]any{"workId": workID, "publicId": "abc12345"}, 409)
	doJSON(t, "POST", ts.URL+"/api/library/gameatlas/link", map[string]any{"workId": workID, "publicId": "def67890"}, 409)

	// 关键回归：已有条目挂链后，反哺不再是 404
	doJSON(t, "PUT", ts.URL+"/api/works/"+workID+"/content", map[string]any{
		"contentMd": "# 无限战争\n\n一段简介。\n\n## 概览\n\n正文。", "author": "human",
	}, 200)
	doJSON(t, "POST", ts.URL+"/api/library/gameatlas/push", map[string]any{"workId": workID}, 200)

	// 解除关联 → 反哺回 404；再解除 → 404
	doJSON(t, "DELETE", ts.URL+"/api/library/gameatlas/link?workId="+workID, nil, 200)
	doJSON(t, "POST", ts.URL+"/api/library/gameatlas/push", map[string]any{"workId": workID}, 404)
	doJSON(t, "DELETE", ts.URL+"/api/library/gameatlas/link?workId="+workID, nil, 404)
}

func TestGameAtlasEndpointsRequireLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	guest := &http.Client{}

	for _, probe := range []struct {
		method, url string
	}{
		{"GET", ts.URL + "/api/library/gameatlas/suggest?workId=whatever"},
		{"POST", ts.URL + "/api/library/gameatlas/archive"},
		{"POST", ts.URL + "/api/library/gameatlas/push"},
		{"GET", ts.URL + "/api/library/gameatlas/search?q=x"},
		{"POST", ts.URL + "/api/library/gameatlas/link"},
		{"DELETE", ts.URL + "/api/library/gameatlas/link?workId=x"},
	} {
		req, _ := http.NewRequest(probe.method, probe.url, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		resp, err := guest.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", probe.method, probe.url, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", probe.method, probe.url, resp.StatusCode)
		}
	}
}
