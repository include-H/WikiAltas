package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"wikiatlas/backend/internal/domain"
)

// 池子是清单制：扫描 → 池子读清单 + 实时挂链覆盖；忽略（单条/整组）持久化。
func TestLibraryPoolScanIgnoreAndOverlay(t *testing.T) {
	ga, _ := newFakeGA(t)
	emby := newFakeEmbyAPI(t)
	ts, st := newTestServer(t)
	doJSON(t, "PUT", ts.URL+"/api/settings", map[string]any{
		"library": map[string]any{
			"gameatlasUrl": ga.URL, "gameatlasApiKey": "ga-pw",
			"embyUrl": emby.URL, "embyApiKey": "k",
			"embyLibraryRoles": []map[string]any{
				{"id": "v-movie", "name": "电影", "role": "work"},
				{"id": "v-drama", "name": "番剧", "role": "work"},
				{"id": "v-mixed", "name": "第九艺术", "role": "mixed"},
				{"id": "v-ost", "name": "OST", "role": "mixed"},
			},
		},
	}, 200)

	// 未扫描时：池子空、给提示数据
	pool0 := doJSON(t, "GET", ts.URL+"/api/library/pool", nil, 200)
	if pool0["scannedAt"] != "" || len(pool0["groups"].([]any)) != 0 {
		t.Fatalf("unscanned pool = %#v", pool0)
	}

	// 扫描 → 池子出组
	scan := doJSON(t, "POST", ts.URL+"/api/library/scan", nil, 200)
	if scan["ok"] != true {
		t.Fatalf("scan = %#v", scan)
	}
	pool := doJSON(t, "GET", ts.URL+"/api/library/pool", nil, 200)
	groups, _ := pool["groups"].([]any)
	// 形状稳定：没有忽略时，ignoredGroups 也必须是空数组而不是 null（否则池子页 .length 就崩）
	if ig, ok := pool["ignoredGroups"].([]any); !ok || len(ig) != 0 {
		t.Fatalf("ignoredGroups = %#v, want []", pool["ignoredGroups"])
	}
	if ie, ok := pool["ignoredEntries"].([]any); !ok || len(ie) != 0 {
		t.Fatalf("ignoredEntries = %#v, want []", pool["ignoredEntries"])
	}

	find := func(source, container string) map[string]any {
		for _, g := range groups {
			m, _ := g.(map[string]any)
			if m["source"] == source && m["containerTitle"] == container {
				return m
			}
		}
		return nil
	}
	titles := func(g map[string]any) []string {
		out := []string{}
		for _, it := range g["unlinked"].([]any) {
			out = append(out, it.(map[string]any)["title"].(string))
		}
		return out
	}

	// GA：使命召唤 组（无限战争）；无系列条目进「（无系列）」
	codGroup := find("gameatlas", "使命召唤")
	if codGroup == nil || len(codGroup["unlinked"].([]any)) != 1 {
		t.Fatalf("GA 使命召唤 group = %#v / groups=%#v", codGroup, pool["groups"])
	}
	// Emby 正片库：电影 组里有 BoxSet；番剧 组里有剧集；混杂库也在清单里
	if g := find("emby", "电影"); g == nil || !containsAny(titles(g), "007（系列）") {
		t.Fatalf("emby 电影 group = %#v", g)
	}
	if g := find("emby", "番剧"); g == nil || !containsAny(titles(g), "葬送的芙莉莲") {
		t.Fatalf("emby 番剧 group = %#v", g)
	}
	if g := find("emby", "第九艺术"); g == nil || len(g["unlinked"].([]any)) != 2 {
		t.Fatalf("emby 第九艺术 group = %#v", g)
	}

	// 实时挂链覆盖：把 GA 无限战争挂到新建节点 → 该组消失（unlinked 空）
	node := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "使命召唤"}, 201)
	doJSON(t, "POST", ts.URL+"/api/library/gameatlas/link", map[string]any{
		"workId": node["id"].(string), "publicId": "abc12345",
	}, 201)
	pool2 := doJSON(t, "GET", ts.URL+"/api/library/pool", nil, 200)
	for _, g := range pool2["groups"].([]any) {
		m, _ := g.(map[string]any)
		if m["source"] == "gameatlas" && m["containerTitle"] == "使命召唤" {
			t.Fatalf("linked group should be hidden: %#v", m)
		}
	}

	// 忽略单条 → 消失且进 ignoredEntries；恢复 → 回来
	doJSON(t, "POST", ts.URL+"/api/library/ignore", map[string]any{"source": "gameatlas", "entryId": "abc12345"}, 200)
	pool3 := doJSON(t, "GET", ts.URL+"/api/library/pool", nil, 200)
	foundIgnored := false
	for _, it := range pool3["ignoredEntries"].([]any) {
		if it.(map[string]any)["publicId"] == "abc12345" {
			foundIgnored = true
		}
	}
	if !foundIgnored {
		t.Fatalf("ignoredEntries = %#v", pool3["ignoredEntries"])
	}
	doJSON(t, "DELETE", ts.URL+"/api/library/ignore?source=gameatlas&entryId=abc12345", nil, 200)

	// 整组忽略：第九艺术 组消失、ignoredGroups 有记录、恢复即回
	doJSON(t, "POST", ts.URL+"/api/library/ignore", map[string]any{
		"source": "emby", "containerKey": "view:v-mixed", "containerTitle": "第九艺术",
	}, 200)
	pool4 := doJSON(t, "GET", ts.URL+"/api/library/pool", nil, 200)
	for _, g := range pool4["groups"].([]any) {
		m, _ := g.(map[string]any)
		if m["containerTitle"] == "第九艺术" {
			t.Fatalf("group-ignored container should be hidden: %#v", m)
		}
	}
	if len(pool4["ignoredGroups"].([]any)) != 1 {
		t.Fatalf("ignoredGroups = %#v", pool4["ignoredGroups"])
	}
	doJSON(t, "DELETE", ts.URL+"/api/library/ignore?source=emby&containerKey=view:v-mixed", nil, 200)

	// AI 建议：没配模型 → 400；手工塞建议 → 池子里带条目渲染；忽略后过滤
	doJSON(t, "POST", ts.URL+"/api/library/ai-suggest", nil, 400)
	if err := st.SaveLibraryAiSuggestions(&domain.LibraryAiSuggestions{
		GeneratedAt: "2026-09-12T00:00:00Z", Model: "test",
		Suggestions: []domain.LibraryAiSuggestion{
			{Key: "gameatlas:abc12345", Source: "gameatlas", Action: "link",
				TargetNodeID: node["id"].(string), Confidence: 0.93, Reason: "同名系列"},
		},
	}); err != nil {
		t.Fatalf("seed suggestions: %v", err)
	}
	pool5 := doJSON(t, "GET", ts.URL+"/api/library/pool", nil, 200)
	ais, _ := pool5["aiSuggestions"].([]any)
	// abc12345 在上一段被恢复忽略前——此时它是未忽略且已挂链 → 建议被过滤（已处理）
	if len(ais) != 0 {
		t.Fatalf("linked entry suggestion should be filtered: %#v", ais)
	}
}

func containsAny(list []string, needle string) bool {
	for _, s := range list {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func TestLibraryPoolRequiresLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	guest := &http.Client{}
	for _, probe := range []struct{ method, url string }{
		{"GET", ts.URL + "/api/library/pool"},
		{"POST", ts.URL + "/api/library/scan"},
		{"POST", ts.URL + "/api/library/ignore"},
		{"DELETE", ts.URL + "/api/library/ignore?source=emby&entryId=x"},
		{"POST", ts.URL + "/api/library/ai-suggest"},
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
