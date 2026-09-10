package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/httpapi"
	"wikiatlas/backend/internal/run"
	"wikiatlas/backend/internal/store"
)

// 测试用的登录态客户端：newTestServer 会登录一次，doJSON 自动带上会话 Cookie。
var testClients = map[string]*http.Client{}

const testAdminPassword = "test-password-123"

func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	mgr := run.NewManager(st, nil)
	// do not Start() expiry loop in tests
	srv := httpapi.New(st, mgr)
	ts := httptest.NewServer(srv.Handler())
	// 简易用户系统：设置管理员密码并登录，得到带 Cookie 的客户端
	if err := st.SetAdminPassword(testAdminPassword); err != nil {
		t.Fatalf("set admin password: %v", err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": testAdminPassword})
	loginReq, _ := http.NewRequest("POST", ts.URL+"/api/auth/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginResp.StatusCode)
	}
	testClients[ts.URL] = client
	t.Cleanup(func() {
		ts.Close()
		mgr.Stop()
		st.Close()
		delete(testClients, ts.URL)
	})
	return ts, st
}

// clientFor 返回该测试服务器对应的登录态客户端（doJSON 之外的裸请求也要带 Cookie）。
// 简易用户系统：访客只能读公开内容，其余接口一律 401。
func TestGuestCanOnlyReadPublicContent(t *testing.T) {
	ts, _ := newTestServer(t) // 内部已登录，用于准备数据
	guest := &http.Client{}   // 不带 Cookie = 访客

	// 公开节点：宇宙 + 系列 + 单作全部 public
	pubUniverse := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "universe", "title": "公开宇宙", "visibility": "public"}, 201)
	pubSeries := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "公开系列", "parentId": pubUniverse["id"], "visibility": "public"}, 201)
	pubWork := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "work", "title": "公开单作", "parentId": pubSeries["id"], "visibility": "public"}, 201)
	doJSON(t, "PUT", ts.URL+"/api/works/"+pubWork["id"].(string)+"/content", map[string]any{"contentMd": "# 公开条目\n\n正文", "author": "human"}, 200)

	// 私有节点（默认 private）
	privSeries := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "私有系列", "parentId": pubUniverse["id"]}, 201)
	privWork := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "work", "title": "私有单作", "parentId": privSeries["id"]}, 201)
	doJSON(t, "PUT", ts.URL+"/api/works/"+privWork["id"].(string)+"/content", map[string]any{"contentMd": "# 私有条目\n\n正文", "author": "human"}, 200)
	doJSON(t, "POST", ts.URL+"/api/works/"+privSeries["id"].(string)+"/docs", map[string]any{"title": "私有资料"}, 201)

	get := func(url string) int {
		req, _ := http.NewRequest("GET", url, nil)
		resp, err := guest.Do(req)
		if err != nil {
			t.Fatalf("guest GET %s: %v", url, err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	post := func(url string, body map[string]any) int {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, err := guest.Do(req)
		if err != nil {
			t.Fatalf("guest POST %s: %v", url, err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if code := get(ts.URL + "/api/works/" + pubWork["id"].(string)); code != 200 {
		t.Fatalf("访客读公开作品 = %d, want 200", code)
	}
	if code := get(ts.URL + "/api/works/" + privWork["id"].(string)); code != 404 {
		t.Fatalf("访客读私有作品 = %d, want 404", code)
	}
	if code := get(ts.URL + "/api/works/" + privSeries["id"].(string) + "/docs"); code != 404 {
		t.Fatalf("访客读私有资料夹 = %d, want 404", code)
	}
	if code := post(ts.URL+"/api/works", map[string]any{"kind": "work", "title": "访客不该能建"}); code != 401 {
		t.Fatalf("访客建节点 = %d, want 401", code)
	}
	if code := post(ts.URL+"/api/runs", map[string]any{"intent": "answer", "goal": "x"}); code != 401 {
		t.Fatalf("访客建工单 = %d, want 401", code)
	}
	if code := get(ts.URL + "/api/settings"); code != 401 {
		t.Fatalf("访客读设置 = %d, want 401", code)
	}

	// 访客的树里只有公开节点
	req, _ := http.NewRequest("GET", ts.URL+"/api/tree", nil)
	resp, err := guest.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tree struct {
		Nodes []map[string]any `json:"nodes"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tree)
	for _, n := range tree.Nodes {
		if vis, _ := n["visibility"].(string); vis != "public" {
			t.Fatalf("访客树里出现了非公开节点: %v", n["title"])
		}
	}
	if len(tree.Nodes) == 0 {
		t.Fatal("访客树里应至少包含公开节点")
	}

	// 错误密码不能登录
	if code := post(ts.URL+"/api/auth/login", map[string]any{"username": "admin", "password": "wrong"}); code != 401 {
		t.Fatalf("错误密码登录 = %d, want 401", code)
	}
}

func clientFor(rawURL string) *http.Client {
	for base, c := range testClients {
		if strings.HasPrefix(rawURL, base) {
			return c
		}
	}
	return http.DefaultClient
}

func doJSON(t *testing.T, method, url string, body any, wantStatus int) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.DefaultClient
	for base, c := range testClients {
		if strings.HasPrefix(url, base) {
			client = c
			break
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		var raw bytes.Buffer
		_, _ = raw.ReadFrom(resp.Body)
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, url, resp.StatusCode, wantStatus, raw.String())
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestHealth(t *testing.T) {
	ts, _ := newTestServer(t)
	doJSON(t, "GET", ts.URL+"/api/health", nil, 200)
}

func TestWorkCRUD(t *testing.T) {
	ts, _ := newTestServer(t)

	// create
	created := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{
		"kind":  "universe",
		"title": "最终幻想",
	}, 201)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id in %v", created)
	}
	if created["status"] != "stub" {
		t.Fatalf("status = %v", created["status"])
	}

	// get
	detail := doJSON(t, "GET", ts.URL+"/api/works/"+id, nil, 200)
	work, _ := detail["work"].(map[string]any)
	if work == nil {
		// maybe returned as work object at top level depending on shape
		work = detail
	}
	if work["title"] != "最终幻想" {
		t.Fatalf("title = %v", work["title"])
	}

	// patch
	patched := doJSON(t, "PATCH", ts.URL+"/api/works/"+id, map[string]any{
		"title":  "最终幻想系列",
		"status": "draft",
	}, 200)
	if patched["title"] != "最终幻想系列" {
		t.Fatalf("patched title = %v", patched["title"])
	}
	if patched["status"] != "draft" {
		t.Fatalf("patched status = %v", patched["status"])
	}

	// tree contains it
	tree := doJSON(t, "GET", ts.URL+"/api/tree", nil, 200)
	nodes, _ := tree["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("tree nodes = %d, want 1", len(nodes))
	}

	// delete
	doJSON(t, "DELETE", ts.URL+"/api/works/"+id, nil, 204)

	// 404 after delete
	req, _ := http.NewRequest("GET", ts.URL+"/api/works/"+id, nil)
	resp, err := clientFor(req.URL.String()).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("after delete status = %d, want 404", resp.StatusCode)
	}
}

func TestContentPut200And409(t *testing.T) {
	ts, _ := newTestServer(t)

	created := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{
		"kind":  "work",
		"title": "血战太平洋",
	}, 201)
	id := created["id"].(string)

	// PUT content → 200
	res := doJSON(t, "PUT", ts.URL+"/api/works/"+id+"/content", map[string]any{
		"contentMd": "# 初稿",
		"author":    "human",
		"summary":   "first commit",
	}, 200)
	if res["contentVer"].(float64) != 1 {
		t.Fatalf("contentVer = %v, want 1", res["contentVer"])
	}
	revID, _ := res["revisionId"].(string)
	if revID == "" {
		t.Fatal("missing revisionId")
	}

	// PUT with wrong expectedVersion → 409
	reqBody := map[string]any{
		"contentMd":       "# 冲突",
		"author":          "llm",
		"expectedVersion": 99,
	}
	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("PUT", ts.URL+"/api/works/"+id+"/content", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := clientFor(req.URL.String()).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("conflict status = %d, want 409", resp.StatusCode)
	}
	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error.Code != "conflict" {
		t.Fatalf("error code = %s", errBody.Error.Code)
	}

	// restore revision → new version 2
	restored := doJSON(t, "POST", ts.URL+"/api/works/"+id+"/revisions/"+revID+"/restore", nil, 200)
	if restored["contentVer"].(float64) != 2 {
		t.Fatalf("restored ver = %v, want 2", restored["contentVer"])
	}

	// revisions list
	revs := doJSON(t, "GET", ts.URL+"/api/works/"+id+"/revisions?limit=10", nil, 200)
	list, _ := revs["revisions"].([]any)
	if len(list) != 2 {
		t.Fatalf("revisions = %d, want 2", len(list))
	}
}

func TestTreeWithChildren(t *testing.T) {
	ts, _ := newTestServer(t)
	u := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "universe", "title": "U"}, 201)
	uid := u["id"].(string)
	c := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{
		"kind": "series", "title": "S", "parentId": uid,
	}, 201)
	cid := c["id"].(string)

	tree := doJSON(t, "GET", ts.URL+"/api/tree", nil, 200)
	nodes, _ := tree["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	// child points to parent
	var child map[string]any
	for _, n := range nodes {
		m := n.(map[string]any)
		if m["id"] == cid {
			child = m
		}
	}
	if child == nil || child["parentId"] != uid {
		t.Fatalf("child = %v", child)
	}

	// cannot delete parent with children
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/works/"+uid, nil)
	resp, _ := clientFor(req.URL.String()).Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("delete parent status = %d, want 400", resp.StatusCode)
	}
}

func TestDocsAndRelationsAPI(t *testing.T) {
	ts, _ := newTestServer(t)
	a := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "work", "title": "A"}, 201)
	b := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "work", "title": "B"}, 201)
	aid := a["id"].(string)
	bid := b["id"].(string)

	// create doc
	doc := doJSON(t, "POST", ts.URL+"/api/works/"+aid+"/docs", map[string]any{
		"title":     "设定集",
		"contentMd": "初始资料",
	}, 201)
	docID := doc["id"].(string)
	if doc["contentVer"].(float64) != 1 {
		t.Fatalf("doc ver = %v", doc["contentVer"])
	}

	// list docs
	docsResp := doJSON(t, "GET", ts.URL+"/api/works/"+aid+"/docs", nil, 200)
	docs, _ := docsResp["docs"].([]any)
	if len(docs) != 1 {
		t.Fatalf("docs = %d", len(docs))
	}

	// put doc content
	doJSON(t, "PUT", ts.URL+"/api/docs/"+docID+"/content", map[string]any{
		"contentMd": "更新后的资料",
		"author":    "human",
	}, 200)

	// relation
	rel := doJSON(t, "POST", ts.URL+"/api/relations", map[string]any{
		"fromId": aid, "toId": bid, "type": "sequel_to",
	}, 201)
	relID := rel["id"].(string)

	rels := doJSON(t, "GET", ts.URL+"/api/works/"+aid+"/relations", nil, 200)
	rlist, _ := rels["relations"].([]any)
	if len(rlist) != 1 {
		t.Fatalf("relations = %d", len(rlist))
	}

	// invalid relation type
	reqB, _ := json.Marshal(map[string]any{"fromId": aid, "toId": bid, "type": "buddy"})
	req, _ := http.NewRequest("POST", ts.URL+"/api/relations", bytes.NewReader(reqB))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := clientFor(req.URL.String()).Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("bad relation status = %d, want 400", resp.StatusCode)
	}

	doJSON(t, "DELETE", ts.URL+"/api/relations/"+relID, nil, 204)
}

func TestSearchAndSettings(t *testing.T) {
	ts, _ := newTestServer(t)
	w := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "work", "title": "圣子降临"}, 201)
	id := w["id"].(string)
	doJSON(t, "PUT", ts.URL+"/api/works/"+id+"/content", map[string]any{
		"contentMd": "克劳德与萨菲罗斯的终战",
		"author":    "human",
	}, 200)

	hitsResp := doJSON(t, "GET", ts.URL+"/api/search?q="+strings.TrimSpace("萨菲罗斯"), nil, 200)
	hits, _ := hitsResp["hits"].([]any)
	if len(hits) == 0 {
		t.Fatalf("search hits empty: %v", hitsResp)
	}

	// settings roundtrip
	doJSON(t, "PUT", ts.URL+"/api/settings", map[string]any{
		"llm":       map[string]any{"endpoint": "http://localhost:1234/v1", "model": "local-model"},
		"library":   map[string]any{},
		"runs":      map[string]any{"expireDays": 7, "keepEventsDays": 90, "maxConcurrentRuns": 3},
		"search":    map[string]any{"exaApiKey": "exa-test-key"},
		"skillRoot": ".claude/skill/wiki-writing",
	}, 200)
	st := doJSON(t, "GET", ts.URL+"/api/settings", nil, 200)
	llm, _ := st["llm"].(map[string]any)
	if llm["model"] != "local-model" {
		t.Fatalf("settings model = %v", llm["model"])
	}
	// 密钥只回"是否已配置"，绝不回原文
	if _, leaked := llm["apiKey"]; leaked {
		t.Fatal("settings 响应泄露了 apiKey 原文")
	}
	search, _ := st["search"].(map[string]any)
	if search["exaApiKeyConfigured"] != true {
		t.Fatalf("exaApiKeyConfigured = %v", search["exaApiKeyConfigured"])
	}
	if _, leaked := search["exaApiKey"]; leaked {
		t.Fatal("settings 响应泄露了 exaApiKey 原文")
	}
	runs, _ := st["runs"].(map[string]any)
	if runs["maxConcurrentRuns"] != float64(3) {
		t.Fatalf("maxConcurrentRuns = %v", runs["maxConcurrentRuns"])
	}
}

func TestRunCreateAndEvents(t *testing.T) {
	ts, st := newTestServer(t)

	// bind a work so mock executor writes content
	w := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "work", "title": "测试作品"}, 201)
	wid := w["id"].(string)

	created := doJSON(t, "POST", ts.URL+"/api/runs", map[string]any{
		"intent":  "create_wiki",
		"goal":    "为测试作品写 Wiki",
		"context": map[string]any{"workId": wid},
	}, 201)
	runID, _ := created["runId"].(string)
	if runID == "" {
		t.Fatalf("no runId: %v", created)
	}

	// wait for mock executor to finish (it sleeps ~200ms total)
	deadline := time.Now().Add(5 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		r, err := st.GetRun(runID)
		if err != nil {
			t.Fatal(err)
		}
		status = string(r.Status)
		if status == "completed" || status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != "completed" {
		t.Fatalf("run status = %s, want completed", status)
	}

	// get run detail
	detail := doJSON(t, "GET", ts.URL+"/api/runs/"+runID, nil, 200)
	runObj, _ := detail["run"].(map[string]any)
	if runObj["status"] != "completed" {
		t.Fatalf("run obj status = %v", runObj["status"])
	}
	events, _ := detail["events"].([]any)
	if len(events) == 0 {
		t.Fatal("expected events in run detail")
	}

	// work content was written by mock executor
	wd := doJSON(t, "GET", ts.URL+"/api/works/"+wid, nil, 200)
	work, _ := wd["work"].(map[string]any)
	if work["contentMd"] == nil || work["contentMd"] == "" {
		t.Fatalf("mock executor did not write content: %v", work["contentMd"])
	}

	// list runs
	list := doJSON(t, "GET", ts.URL+"/api/runs", nil, 200)
	runs, _ := list["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}

	// SSE stream: read a few events
	sseReq, _ := http.NewRequest("GET", ts.URL+"/api/runs/"+runID+"/events/stream", nil)
	sseResp, err := clientFor(sseReq.URL.String()).Do(sseReq)
	if err != nil {
		t.Fatal(err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != 200 {
		t.Fatalf("sse status = %d", sseResp.StatusCode)
	}
	if ct := sseResp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %s", ct)
	}
	buf := make([]byte, 4096)
	n, _ := sseResp.Body.Read(buf)
	if n == 0 {
		t.Fatal("expected SSE data")
	}
	chunk := string(buf[:n])
	if !strings.Contains(chunk, "event:") {
		t.Fatalf("sse chunk missing event: %q", chunk)
	}
}

func TestCreateRunValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	reqB, _ := json.Marshal(map[string]any{"intent": "answer"}) // missing goal
	req, _ := http.NewRequest("POST", ts.URL+"/api/runs", bytes.NewReader(reqB))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := clientFor(req.URL.String()).Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// GET /api/docs/:id 必须包一层 { doc }：前端按 { doc } 解，
// 历史实现返回裸 doc，导致资料页报 "reading 'contentMd' of undefined"。
func TestGetDocReturnsWrappedDoc(t *testing.T) {
	ts, st := newTestServer(t)
	series, err := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindSeries, Title: "巫师系列"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := st.CreateDoc(series.ID, domain.CreateDocBody{Title: "剧情解析"})
	if err != nil {
		t.Fatal(err)
	}
	got := doJSON(t, "GET", ts.URL+"/api/docs/"+doc.ID, nil, 200)
	d, ok := got["doc"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 doc 字段: %v", got)
	}
	if d["title"] != "剧情解析" {
		t.Fatalf("doc.title = %v", d["title"])
	}
}

// 工单要带上"用户所处的文档模式 + 正文选区"，Altas 才能按模式与范围干活。
func TestCreateRunCarriesModeAndSelection(t *testing.T) {
	ts, _ := newTestServer(t)
	created := doJSON(t, "POST", ts.URL+"/api/runs", map[string]any{
		"intent":    "rewrite_section",
		"goal":      "润色这一段",
		"workspace": "work:demo",
		"context": map[string]any{
			"docMode":   "revision",
			"selection": "休在月震后被 IDUS 袭击。",
		},
	}, 201)
	runID, _ := created["runId"].(string)
	if runID == "" {
		t.Fatalf("no runId in %v", created)
	}
	detail := doJSON(t, "GET", ts.URL+"/api/runs/"+runID, nil, 200)
	run, _ := detail["run"].(map[string]any)
	if got, _ := run["workspace"].(string); got != "work:demo" {
		t.Fatalf("workspace = %q, want work:demo", got)
	}
	ctx, _ := run["context"].(map[string]any)
	if got, _ := ctx["docMode"].(string); got != "revision" {
		t.Fatalf("context.docMode = %v, want revision", ctx["docMode"])
	}
	if got, _ := ctx["selection"].(string); got != "休在月震后被 IDUS 袭击。" {
		t.Fatalf("context.selection = %v", ctx["selection"])
	}
}

func TestLibrarySourcesEmpty(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := doJSON(t, "GET", ts.URL+"/api/library/sources", nil, 200)
	sources, _ := resp["sources"].([]any)
	if len(sources) != 0 {
		t.Fatalf("sources = %v", sources)
	}
}

// silence unused in case of build tags
var _ = fmt.Sprintf
var _ = domain.AuthorHuman
