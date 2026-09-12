package httpapi

import "testing"

// 解析器容错：围栏/前置废话要能剥掉；编造的节点 id 与未知 key 丢弃；confidence 收敛。
func TestParseAiSuggestionsTolerant(t *testing.T) {
	validKeys := map[string]bool{"komga:X1": true, "emby:E1": true}
	validNodes := map[string]bool{"N1": true}
	raw := "结果如下：\n```json\n" +
		`{"groups":[{"targetNodeId":"N1","confidence":0.95,"reason":"同名","entryKeys":["komga:X1"]},` +
		`{"targetNodeId":"N2","confidence":0.9,"reason":"编造节点","entryKeys":["emby:E1"]}],` +
		`"links":[{"targetNodeId":"N1","confidence":2,"reason":"x","entryKeys":["emby:E1","emby:UNKNOWN"]}],` +
		`"ignores":[{"reason":"画集","entryKeys":["komga:X1"]}]}` + "\n```"

	sugs, err := parseAiSuggestions(raw, validKeys, validNodes)
	if err != nil {
		t.Fatalf("parseAiSuggestions: %v", err)
	}
	counts := map[string]int{}
	for _, s := range sugs {
		counts[s.Action]++
	}
	// N2 的整组被丢（节点不存在）；X1 只在 archive 生效（ignore 里重复被去重）
	if len(sugs) != 2 || counts["archive"] != 1 || counts["link"] != 1 {
		t.Fatalf("suggestions = %#v, counts=%v", sugs, counts)
	}
	for _, s := range sugs {
		if s.Action == "link" && s.Confidence != 1 {
			t.Fatalf("confidence should clamp to 1: %#v", s)
		}
	}
	if _, err := parseAiSuggestions("no json here", validKeys, validNodes); err == nil {
		t.Fatal("garbage input should error")
	}
}
