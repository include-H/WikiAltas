package httpapi

import "testing"

func TestParseKomgaJudgmentsTolerant(t *testing.T) {
	valid := map[string]bool{"s1": true, "s2": true}
	// 围栏 + 大小写 + 编造的 id → 剥出来、归一、丢弃非法
	got, err := parseKomgaJudgments("```json\n{\"s1\":\"Series\",\"s2\":\" DRAWER \",\"s9\":\"series\"}\n```", valid)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 || got["s1"] != "series" || got["s2"] != "drawer" {
		t.Fatalf("verdicts = %#v", got)
	}
	// 值不在词典里 → 丢弃；全被丢 → 报错（不产生半截结论）
	if got, err := parseKomgaJudgments(`{"s1":"maybe"}`, valid); err == nil || len(got) != 0 {
		t.Fatalf("bogus kind => %#v, %v", got, err)
	}
	if _, err := parseKomgaJudgments("模型没有返回 JSON", valid); err == nil {
		t.Fatal("want error for non-JSON")
	}
}

func TestKomgaJudgmentFingerprint(t *testing.T) {
	a := komgaJudgmentFP("单行本", []string{"哈利·波特", "冰与火之歌"})
	b := komgaJudgmentFP("单行本", []string{"冰与火之歌", "哈利·波特"}) // 顺序无关
	if a != b {
		t.Fatal("fingerprint should be order-insensitive")
	}
	c := komgaJudgmentFP("单行本", []string{"哈利·波特", "冰与火之歌", "新书"})
	if a == c {
		t.Fatal("book change should change fingerprint")
	}
	if komgaJudgmentFP("另一个系列", []string{"哈利·波特", "冰与火之歌"}) == a {
		t.Fatal("series rename should change fingerprint")
	}
}
