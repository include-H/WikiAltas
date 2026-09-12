package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// maxCharacters 必须嵌在 text / highlights 里面：平铺在 contents 上会被 Exa
// 静默忽略——不报错、直接返回整页正文。旧写法因此拿回 31.7KB/次，
// 再被按字节砍成 1500（≈500 汉字），付费买全文却只留下残缺页首。
func TestExaSearchPayloadNestsMaxCharacters(t *testing.T) {
	const q = "某作品 出版信息"
	p := exaSearchPayload(q)

	contents, ok := p["contents"].(map[string]any)
	if !ok {
		t.Fatalf("contents 不是对象: %T", p["contents"])
	}
	if _, flat := contents["maxCharacters"]; flat {
		t.Fatal("maxCharacters 平铺在 contents 上了，Exa 会忽略它")
	}
	for _, key := range []string{"text", "highlights"} {
		sub, ok := contents[key].(map[string]any)
		if !ok {
			t.Fatalf("contents.%s 必须是带 maxCharacters 的对象，实际 %T", key, contents[key])
		}
		if _, ok := sub["maxCharacters"].(int); !ok {
			t.Fatalf("contents.%s 缺少 maxCharacters", key)
		}
	}
	hl, _ := contents["highlights"].(map[string]any)
	if hl["query"] != q {
		t.Fatalf("highlights.query 应跟随检索词，实际 %v", hl["query"])
	}
	if p["query"] != q || p["numResults"] != exaNumResults {
		t.Fatalf("请求体不对: %v", p)
	}
}

func TestExaItemPrefersHighlightsAndKeepsDate(t *testing.T) {
	got := exaItem(exaResult{
		Title:         "小説 FINAL FANTASY XV",
		URL:           "https://example.com/a",
		Text:          "页面开头的导航废话",
		Highlights:    []string{"2019年4月25日発売。", "価格は1620円。"},
		PublishedDate: "2019-04-22T00:00:00.000Z",
		Author:        "SQUARE ENIX",
	})
	ex, _ := got["excerpt"].(string)
	if !strings.Contains(ex, "2019年4月25日発売") || strings.Contains(ex, "页面开头的导航废话") {
		t.Fatalf("应优先用检索相关的高亮，而不是页首正文: %q", ex)
	}
	// 日期与作者是判断来源层级的主料，不能丢
	if got["publishedDate"] != "2019-04-22T00:00:00.000Z" || got["author"] != "SQUARE ENIX" {
		t.Fatalf("日期/作者丢了: %v", got)
	}
	if got["url"] != "https://example.com/a" || got["title"] != "小説 FINAL FANTASY XV" {
		t.Fatalf("标题/链接丢了: %v", got)
	}

	// 没有高亮退回正文，没有正文退回 snippet
	if fb := exaItem(exaResult{Text: "页首正文"}); fb["excerpt"] != "页首正文" {
		t.Fatalf("高亮为空应退回正文: %v", fb["excerpt"])
	}
	if fb := exaItem(exaResult{Snippet: "摘要"}); fb["excerpt"] != "摘要" {
		t.Fatalf("正文为空应退回 snippet: %v", fb["excerpt"])
	}

	// 截断按 rune：中文被劈成半个会变成乱码
	long := exaItem(exaResult{Text: strings.Repeat("中", 5000)})
	s, _ := long["excerpt"].(string)
	if !utf8.ValidString(s) || strings.ContainsRune(s, utf8.RuneError) {
		t.Fatal("截断劈坏了多字节字符")
	}
	if n := len([]rune(s)); n > exaExcerptRunes+1 {
		t.Fatalf("截断后 %d 字，超过上限 %d", n, exaExcerptRunes)
	}
}

// 预算提醒必须由工具在构造返回时就写进去——执行器事后改写会让
// "模型所见"和"落库/事件"不一致（dsh 的纪律：模型可见 ⟺ 有日志）。
func TestWebBudgetNoteIsBuiltIntoTheResult(t *testing.T) {
	if note := (WebBudget{Used: 2, Limit: 12, WarnAt: 8}).Note(); note != "" {
		t.Fatalf("没到提醒线不该出声: %q", note)
	}
	b := WebBudget{Used: 9, Limit: 12, WarnAt: 8}
	if !b.ShouldWarn() {
		t.Fatalf("9/12 该提醒但没提醒: %+v", b)
	}
	note := b.Note()
	for _, want := range []string{"9", "12", "3"} { // 已用 9、上限 12、还剩 3
		if !strings.Contains(note, want) {
			t.Fatalf("提醒里缺 %q: %q", want, note)
		}
	}

	okRes := []byte(`{"ok":true,"query":"q","results":[],"count":0}`)
	got := withBudgetNote(okRes, b)
	if !strings.Contains(string(got), "budgetNote") {
		t.Fatalf("该把提醒并进结果: %s", got)
	}
	// 不到线：原样返回，不改一个字节
	if plain := withBudgetNote(okRes, WebBudget{Used: 1, Limit: 12, WarnAt: 8}); string(plain) != string(okRes) {
		t.Fatalf("不到线不该改动结果: %s", plain)
	}
	// 失败结果不挂提醒
	failRes := []byte(`{"ok":false,"message":"no web"}`)
	if got := withBudgetNote(failRes, b); string(got) != string(failRes) {
		t.Fatalf("失败结果不该挂提醒: %s", got)
	}
	// 不是 JSON 也不能炸
	if got := withBudgetNote([]byte("not json"), b); string(got) != "not json" {
		t.Fatalf("非 JSON 应原样返回: %s", got)
	}
}

// --- fetch_url：直连超时要给出"下一步动作"，代理要真的被用上 ---

func callFetchURL(t *testing.T, d LibrarianDeps, in map[string]any) map[string]any {
	t.Helper()
	reg := NewLibrarianRegistry(d)
	tool, ok := reg.Get("fetch_url")
	if !ok {
		t.Fatal("fetch_url 没注册")
	}
	b, _ := json.Marshal(in)
	out, err := tool.Execute(context.Background(), b)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// 外网不通时抓取要**快速失败**并给出下一步：只报一句 "timeout"，
// 模型会原地再抓同一页（实测它就是这么干的，白等 20 秒）。
func TestFetchURLTimeoutPointsAtTheProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // 接受连接但永不响应
	}))
	defer srv.Close()
	old := fetchTimeout
	fetchTimeout = 120 * time.Millisecond
	defer func() { fetchTimeout = old }()

	// 1) 没配代理：明说放弃这一页
	r1 := callFetchURL(t, LibrarianDeps{}, map[string]any{"url": srv.URL})
	if r1["ok"] != false {
		t.Fatalf("超时应失败: %v", r1)
	}
	if msg, _ := r1["message"].(string); !strings.Contains(msg, "放弃这一页") {
		t.Fatalf("没配代理时的指引不对: %q", msg)
	}

	// 2) 配了代理：明确让模型带 useProxy 重试**同一个** URL
	r2 := callFetchURL(t, LibrarianDeps{ProxyURL: "http://127.0.0.1:7890"}, map[string]any{"url": srv.URL})
	msg2, _ := r2["message"].(string)
	if !strings.Contains(msg2, "useProxy") || !strings.Contains(msg2, "同一个 URL") {
		t.Fatalf("应告诉模型用 useProxy 重试同一个 URL: %q", msg2)
	}

	// 3) 显式要代理但没配：说清没配，而不是拿去连一个空地址
	r3 := callFetchURL(t, LibrarianDeps{}, map[string]any{"url": srv.URL, "useProxy": true})
	msg3, _ := r3["message"].(string)
	if !strings.Contains(msg3, "没配代理") {
		t.Fatalf("应说明没配代理: %q", msg3)
	}
}

// 目标域名根本解析不了，所以"成功"只可能来自代理——这就证明代理真的接上了。
func TestFetchURLReallyGoesThroughTheProxy(t *testing.T) {
	var got string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.String() // 代理收到的是绝对 URL
		_, _ = w.Write([]byte("<html><body>来自代理的正文</body></html>"))
	}))
	defer proxy.Close()

	r := callFetchURL(t, LibrarianDeps{ProxyURL: proxy.URL},
		map[string]any{"url": "http://example.invalid/never-resolves", "useProxy": true})
	if r["ok"] != true {
		t.Fatalf("走代理应成功: %v", r)
	}
	if txt, _ := r["text"].(string); !strings.Contains(txt, "来自代理的正文") {
		t.Fatalf("正文不对: %v", r["text"])
	}
	if !strings.Contains(got, "example.invalid") {
		t.Fatalf("代理没收到目标 URL: %q", got)
	}
}

// 被挡（403）和超时一样是"直连这条路不通"，同样该给出代理重试。
// 真实观测：抓 Lifestream 与 ffdic 都栽在 403 上，而当时只有超时会给提示，
// 模型两次都被挡死、一次都没换路，白白丢掉两个能用的来源。
func TestFetchURL403AlsoPointsAtTheProxy(t *testing.T) {
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer forbidden.Close()

	r := callFetchURL(t, LibrarianDeps{ProxyURL: "http://127.0.0.1:7890"}, map[string]any{"url": forbidden.URL})
	if r["ok"] != false {
		t.Fatalf("403 应失败: %v", r)
	}
	if msg, _ := r["message"].(string); !strings.Contains(msg, "useProxy") {
		t.Fatalf("403 也该提示换代理: %q", msg)
	}

	// 404 换代理也不会变——不该提示，别浪费模型一次调用
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()
	r2 := callFetchURL(t, LibrarianDeps{ProxyURL: "http://127.0.0.1:7890"}, map[string]any{"url": missing.URL})
	msg2, _ := r2["message"].(string)
	if strings.Contains(msg2, "useProxy") {
		t.Fatalf("404 不该提示换代理: %q", msg2)
	}
	if !strings.Contains(msg2, "404") {
		t.Fatalf("404 应原样报: %q", msg2)
	}
}
