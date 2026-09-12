package httpapi_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"wikiatlas/backend/internal/llm"
)

// fakeSweepLLM 是媒体库相关流程的模型替身：按系统提示词分辨
// 「题名变体」「判官」「AI 对一遍」「Komga 层级判定」几类调用。
type fakeSweepLLM struct {
	judge      string
	judgeFirst string // 若设置：第一次「判官」调用返回它（模拟偶发空答），之后回到 judge
	aiSuggest  string // 「AI 对一遍」的罐装响应（节点建好后填，因为要引用 workId）
	komgaJudge string // Komga 层级判定的罐装响应
	mu         sync.Mutex
	judgeCalls int
}

func (f *fakeSweepLLM) Model() string { return "fake-model" }

func (f *fakeSweepLLM) komgaJudgeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.judgeCalls
}

func (f *fakeSweepLLM) Chat(_ context.Context, messages []llm.Message, _ []llm.ToolDef) (llm.ChatResult, error) {
	sys := ""
	if len(messages) > 0 {
		sys = messages[0].Content
	}
	switch {
	case strings.Contains(sys, "题名变体"):
		// 带围栏也认（容错路径）：变体只有名字，匹配靠子串
		return llm.ChatResult{Content: "```json\n[\"葬送的芙莉莲\",\"芙莉莲\"]\n```"}, nil
	case strings.Contains(sys, "属于它"):
		if f.judgeFirst != "" {
			first := f.judgeFirst
			f.judgeFirst = ""
			return llm.ChatResult{Content: first}, nil
		}
		return llm.ChatResult{Content: f.judge}, nil
	case strings.Contains(sys, "媒体库管家"):
		if f.aiSuggest == "" {
			return llm.ChatResult{Content: "{}"}, nil
		}
		return llm.ChatResult{Content: f.aiSuggest}, nil
	case strings.Contains(sys, "层级判定"):
		f.mu.Lock()
		f.judgeCalls++
		f.mu.Unlock()
		return llm.ChatResult{Content: f.komgaJudge}, nil
	}
	return llm.ChatResult{Content: "{}"}, nil
}

// 节点扫库全链路：写完之后 → 三源候选 → 判官（fake）→ 建议落库 →
// 读 / 挂链覆盖过滤 / 逐条忽略（per-node）与恢复；最后验证「AI 对一遍」不吞 sweep 建议。
func TestNodeSweepRunLinkDismissRestore(t *testing.T) {
	ga, _ := newFakeGA(t)
	emby := newFakeEmbyAPI(t)
	komga, _ := newFakeKomgaAPI(t)
	judge := `{"links":[
		{"key":"emby:sr-frieren","confidence":0.92,"reason":"正片剧集"},
		{"key":"komga:S-comic","confidence":0.8,"reason":"漫画版"},
		{"key":"gameatlas:abc12345","confidence":0.9,"reason":"编造候选：判官随口提的"}
	]}`
	fake := &fakeSweepLLM{judge: judge}
	ts, st := newTestServerWithClient(t, fake)

	doJSON(t, "PUT", ts.URL+"/api/settings", map[string]any{
		"library": map[string]any{
			"gameatlasUrl": ga.URL, "gameatlasApiKey": "ga-pw",
			"embyUrl": emby.URL, "embyApiKey": "k",
			"embyLibraryRoles": []map[string]any{
				{"id": "v-movie", "name": "电影", "role": "work"},
				{"id": "v-drama", "name": "番剧", "role": "work"},
				{"id": "v-mixed", "name": "第九艺术", "role": "mixed"},
			},
			"komgaUrl": komga.URL, "komgaApiKey": "komga-key",
		},
	}, 200)
	if err := st.SetAPIKey("test-key"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	node := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{
		"kind": "work", "title": "葬送的芙莉莲", "medium": "tv",
	}, 201)
	workID := node["id"].(string)

	// 扫一遍：emby 剧集 + komga 漫画进建议；判官提到的 GA 条目不在候选里 → 丢弃
	res := doJSON(t, "POST", ts.URL+"/api/library/node-sweep", map[string]any{"workId": workID}, 200)
	sugs, _ := res["suggestions"].([]any)
	if len(sugs) != 2 {
		t.Fatalf("suggestions = %#v, want 2", res["suggestions"])
	}
	byKey := map[string]map[string]any{}
	for _, it := range sugs {
		m, _ := it.(map[string]any)
		byKey[m["key"].(string)] = m
	}
	embySug := byKey["emby:sr-frieren"]
	if embySug == nil {
		t.Fatalf("missing emby suggestion: %#v", sugs)
	}
	if embySug["title"] != "葬送的芙莉莲" || embySug["reason"] != "正片剧集" {
		t.Fatalf("emby suggestion = %#v", embySug)
	}
	if conf, _ := embySug["confidence"].(float64); conf < 0.9 {
		t.Fatalf("emby confidence = %#v", embySug["confidence"])
	}
	if cover, _ := embySug["coverImage"].(string); !strings.HasPrefix(cover, "http") {
		t.Fatalf("emby cover = %q, want 绝对地址", cover)
	}
	if byKey["komga:S-comic"] == nil || byKey["gameatlas:abc12345"] != nil {
		t.Fatalf("candidate filtering wrong: %#v", byKey)
	}
	// 三个源都配好了，状态应为 ok
	sources, _ := res["sources"].([]any)
	if len(sources) != 3 {
		t.Fatalf("sources = %#v", res["sources"])
	}
	for _, s := range sources {
		m, _ := s.(map[string]any)
		if m["status"] != "ok" {
			t.Fatalf("source status = %#v", m)
		}
	}

	// 读接口：与扫出来的内容一致，无忽略记录
	read := doJSON(t, "GET", ts.URL+"/api/library/node-sweep?workId="+workID, nil, 200)
	if list, _ := read["suggestions"].([]any); len(list) != 2 {
		t.Fatalf("read suggestions = %#v", read["suggestions"])
	}
	if list, _ := read["dismissed"].([]any); len(list) != 0 {
		t.Fatalf("read dismissed = %#v", read["dismissed"])
	}

	// 把 komga 那条挂链 → 读接口实时过滤（已挂链的不再建议）
	doJSON(t, "POST", ts.URL+"/api/library/komga/link", map[string]any{
		"workId": workID, "publicId": "S-comic",
	}, 201)
	read2 := doJSON(t, "GET", ts.URL+"/api/library/node-sweep?workId="+workID, nil, 200)
	if list, _ := read2["suggestions"].([]any); len(list) != 1 {
		t.Fatalf("after link, suggestions = %#v, want 1", read2["suggestions"])
	}

	// 逐条忽略（per-node）→ 读接口消失、进 dismissed；再扫也不会重新提起（候选层过滤）
	doJSON(t, "POST", ts.URL+"/api/library/node-sweep/dismiss", map[string]any{
		"workId": workID, "key": "emby:sr-frieren",
	}, 200)
	read3 := doJSON(t, "GET", ts.URL+"/api/library/node-sweep?workId="+workID, nil, 200)
	if list, _ := read3["suggestions"].([]any); len(list) != 0 {
		t.Fatalf("after dismiss, suggestions = %#v, want 0", read3["suggestions"])
	}
	dismissed, _ := read3["dismissed"].([]any)
	if len(dismissed) != 1 || dismissed[0].(map[string]any)["key"] != "emby:sr-frieren" {
		t.Fatalf("dismissed = %#v", read3["dismissed"])
	}
	// 再把 komga 那条也忽略（它虽已挂链，建议记录还在 KV 里）——
	// 清空最后一条建议记录后，dismissed 仍必须读得出来（KV 里 suggestions 空 ≠ 整条记录不存在）。
	doJSON(t, "POST", ts.URL+"/api/library/node-sweep/dismiss", map[string]any{
		"workId": workID, "key": "komga:S-comic",
	}, 200)
	readEmpty := doJSON(t, "GET", ts.URL+"/api/library/node-sweep?workId="+workID, nil, 200)
	if list, _ := readEmpty["suggestions"].([]any); len(list) != 0 {
		t.Fatalf("suggestions = %#v, want 0", readEmpty["suggestions"])
	}
	if list, _ := readEmpty["dismissed"].([]any); len(list) != 2 {
		t.Fatalf("dismissed after emptying suggestions = %#v, want 2", readEmpty["dismissed"])
	}
	// 判官嘴硬也没用：候选层就排除了（挂链的 komga 与忽略的 emby 都不再进候选）
	res2 := doJSON(t, "POST", ts.URL+"/api/library/node-sweep", map[string]any{"workId": workID}, 200)
	if list, _ := res2["suggestions"].([]any); len(list) != 0 {
		t.Fatalf("re-sweep should skip linked+dismissed, got %#v", res2["suggestions"])
	}

	// 恢复 → 再扫，emby 建议回来（komga 仍挂链 + 仍被忽略，不回来）
	doJSON(t, "DELETE", ts.URL+"/api/library/node-sweep/dismiss?workId="+workID+"&key=emby:sr-frieren", nil, 200)
	read4 := doJSON(t, "GET", ts.URL+"/api/library/node-sweep?workId="+workID, nil, 200)
	if list, _ := read4["dismissed"].([]any); len(list) != 1 || list[0].(map[string]any)["key"] != "komga:S-comic" {
		t.Fatalf("after restore, dismissed = %#v", read4["dismissed"])
	}
	res3 := doJSON(t, "POST", ts.URL+"/api/library/node-sweep", map[string]any{"workId": workID}, 200)
	list3, _ := res3["suggestions"].([]any)
	if len(list3) != 1 || list3[0].(map[string]any)["key"] != "emby:sr-frieren" {
		t.Fatalf("after restore, suggestions = %#v", res3["suggestions"])
	}

	// 「AI 对一遍」重刷：sweep 产出必须原样保留（只重刷它自己那份）。
	// 罐装响应一次覆盖三个源：每个源只认自己批里合法的 key，其余丢弃。
	doJSON(t, "POST", ts.URL+"/api/library/scan", nil, 200)
	fake.aiSuggest = fmt.Sprintf(`{"groups":[{"targetNodeId":%q,"confidence":0.5,"reason":"测试归档","entryKeys":["gameatlas:abc12345"]}],
		"links":[{"targetNodeId":%q,"confidence":0.6,"reason":"测试关联","entryKeys":["emby:art1","komga:S-novel","komga:S-comic"]}]}`,
		workID, workID)
	doJSON(t, "POST", ts.URL+"/api/library/ai-suggest", nil, 200)
	read5 := doJSON(t, "GET", ts.URL+"/api/library/node-sweep?workId="+workID, nil, 200)
	list5, _ := read5["suggestions"].([]any)
	hasSweep := false
	for _, it := range list5 {
		if it.(map[string]any)["key"] == "emby:sr-frieren" {
			hasSweep = true
		}
	}
	if !hasSweep {
		t.Fatalf("sweep suggestion should survive ai-suggest, got %#v", read5["suggestions"])
	}
}

// 守卫：没配模型 → 400；workId 缺失 / 不存在；忽略缺参。
func TestNodeSweepGuards(t *testing.T) {
	ts, _ := newTestServer(t)
	node := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "work", "title": "葬送的芙莉莲"}, 201)
	workID := node["id"].(string)

	doJSON(t, "POST", ts.URL+"/api/library/node-sweep", map[string]any{"workId": workID}, 400)

	doJSON(t, "GET", ts.URL+"/api/library/node-sweep", nil, 400)
	doJSON(t, "GET", ts.URL+"/api/library/node-sweep?workId=nope", nil, 404)
	doJSON(t, "POST", ts.URL+"/api/library/node-sweep/dismiss", map[string]any{"workId": workID}, 400)
	doJSON(t, "DELETE", ts.URL+"/api/library/node-sweep/dismiss?workId="+workID, nil, 400)
}

// 判官偶发空答（{"links":[]}）时重试一次：候选明明有，不该静默成"没找到"。
func TestNodeSweepRetriesEmptyJudge(t *testing.T) {
	emby := newFakeEmbyAPI(t)
	fake := &fakeSweepLLM{
		judgeFirst: `{"links":[]}`,
		judge:      `{"links":[{"key":"emby:sr-frieren","confidence":0.9,"reason":"正片剧集"}]}`,
	}
	ts, st := newTestServerWithClient(t, fake)
	configureEmbyLibs(t, ts, emby.URL)
	if err := st.SetAPIKey("test-key"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	node := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{
		"kind": "work", "title": "葬送的芙莉莲", "medium": "tv",
	}, 201)
	res := doJSON(t, "POST", ts.URL+"/api/library/node-sweep", map[string]any{"workId": node["id"].(string)}, 200)
	list, _ := res["suggestions"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["key"] != "emby:sr-frieren" {
		t.Fatalf("retry should recover the empty judge, got %#v", res["suggestions"])
	}
}
