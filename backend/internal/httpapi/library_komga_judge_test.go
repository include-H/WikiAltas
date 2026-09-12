package httpapi_test

import (
	"strings"
	"testing"
)

// Komga 系列层级判定：扫描时把「系列名 + 书名样本」交给模型判 series/drawer，
// 判定结果驱动清单（对齐用户的"非单行本只显示系列、单册跳书页"口径）。
// 判定与启发式相反也以 LLM 为准；书单没变时第二次扫描不再调模型（指纹缓存）。
func TestKomgaLlmJudgmentDrivesHierarchy(t *testing.T) {
	komga, _ := newFakeKomgaAPI(t)
	// 全库一次判完；两个关键系列故意与启发式相反：
	// S-drawer（启发式会拆）判成 series；S-comic（启发式会合并）判成 drawer。
	fake := &fakeSweepLLM{komgaJudge: `{
		"S-comic": "drawer", "S-novel": "series", "S-lns": "series",
		"S-num": "series", "S-drawer": "series"
	}`}
	ts, st := newTestServerWithClient(t, fake)
	doJSON(t, "PUT", ts.URL+"/api/settings", map[string]any{
		"library": map[string]any{"komgaUrl": komga.URL, "komgaApiKey": "komga-key"},
	}, 200)
	if err := st.SetAPIKey("test-key"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	doJSON(t, "POST", ts.URL+"/api/library/scan", nil, 200)
	if got := fake.komgaJudgeCalls(); got != 1 {
		t.Fatalf("judge calls = %d, want 1（一次批量判完整库）", got)
	}
	pool := doJSON(t, "GET", ts.URL+"/api/library/pool", nil, 200)
	groups, _ := pool["groups"].([]any)
	findGroup := func(container string) map[string]any {
		for _, g := range groups {
			m, _ := g.(map[string]any)
			if m["source"] == "komga" && m["containerTitle"] == container {
				return m
			}
		}
		return nil
	}

	// 判成 series 的抽屉（单行本）：合并成系列一条、跳系列页
	g := findGroup("单行本")
	if g == nil {
		t.Fatalf("单行本 group missing: %#v", pool["groups"])
	}
	items, _ := g["unlinked"].([]any)
	if len(items) != 1 {
		t.Fatalf("单行本 unlinked = %#v, want 1（LLM 判 series 就该合并）", g["unlinked"])
	}
	first, _ := items[0].(map[string]any)
	if first["title"] != "单行本" || !strings.Contains(first["url"].(string), "/series/") {
		t.Fatalf("单行本 entry = %#v", first)
	}

	// 判成 drawer 的系列（葬送的芙莉莲）：拆成单册、每本跳书页
	g2 := findGroup("葬送的芙莉莲")
	if g2 == nil {
		t.Fatalf("芙莉莲 group missing: %#v", pool["groups"])
	}
	items2, _ := g2["unlinked"].([]any)
	if len(items2) != 3 {
		t.Fatalf("芙莉莲 unlinked = %#v, want 3（LLM 判 drawer 就该拆）", g2["unlinked"])
	}
	for _, it := range items2 {
		m, _ := it.(map[string]any)
		if !strings.Contains(m["url"].(string), "/book/") {
			t.Fatalf("drawer 单册 url = %#v", m)
		}
	}

	// 书单没变：再扫一次不该再调模型（指纹缓存）
	doJSON(t, "POST", ts.URL+"/api/library/scan", nil, 200)
	if got := fake.komgaJudgeCalls(); got != 1 {
		t.Fatalf("judge calls after rescan = %d, want 1（书单没变，指纹应命中）", got)
	}
}
