package httpapi

import "testing"

func TestParseSweepVariantsTolerant(t *testing.T) {
	// 围栏 + 多余文字：剥出来即可
	got := parseSweepVariants("这是结果：\n```json\n[\"最终幻想7\",\"FF7\",\"Final Fantasy VII\",\"太空战士\"]\n```\n以上。")
	if len(got) != 4 || got[1] != "FF7" {
		t.Fatalf("variants = %#v", got)
	}
	// 重复 / 太短 / 超长 / 非数组：过滤与兜底
	if got := parseSweepVariants(`["芙莉莲","芙莉莲","a"]`); len(got) != 1 {
		t.Fatalf("dedupe = %#v", got)
	}
	long := ""
	for i := 0; i < 40; i++ {
		long += "长"
	}
	if got := parseSweepVariants(`["` + long + `","芙莉莲"]`); len(got) != 1 {
		t.Fatalf("long filter = %#v", got)
	}
	if got := parseSweepVariants("模型没好好说"); got != nil {
		t.Fatalf("no array = %#v", got)
	}
	// 超过 4 条截断
	got = parseSweepVariants(`["甲一","乙二","丙三","丁四","戊五"]`)
	if len(got) != 4 {
		t.Fatalf("cap = %#v", got)
	}
}

func TestParseSweepJudgementTolerant(t *testing.T) {
	valid := map[string]bool{"emby:1": true, "komga:2": true, "emby:3": true}
	raw := "```json\n{\"links\":[" +
		"{\"key\":\"emby:1\",\"confidence\":1.8,\"reason\":\"正片\"}," +
		"{\"key\":\"komga:2\",\"confidence\":0.5,\"reason\":\"漫画版\"}," +
		"{\"key\":\"emby:999\",\"confidence\":0.9,\"reason\":\"编造的 key\"}," +
		"{\"key\":\"emby:1\",\"confidence\":0.9,\"reason\":\"重复\"}," +
		"{\"key\":\"emby:3\",\"confidence\":0.1,\"reason\":\"低于噪声线\"}" +
		"]}\n```"
	got, err := parseSweepJudgement(raw, valid)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("links = %#v", got)
	}
	// 排序按置信度降序；越界置信收敛到 1
	if got[0].Key != "emby:1" || got[0].Confidence != 1 {
		t.Fatalf("first = %#v", got[0])
	}
	if got[1].Key != "komga:2" {
		t.Fatalf("second = %#v", got[1])
	}
	// 非 JSON 报错
	if _, err := parseSweepJudgement("没有 JSON", valid); err == nil {
		t.Fatal("want error for non-JSON")
	}
}

func TestNameRankAndNormMatch(t *testing.T) {
	norms := []string{"葬送的芙莉莲", "芙莉莲"}
	if got := nameRank("葬送的芙莉莲", norms); got != 0 {
		t.Fatalf("exact rank = %d", got)
	}
	if got := nameRank("葬送的芙莉莲 第一季", norms); got != 1 {
		t.Fatalf("prefix rank = %d", got)
	}
	if got := nameRank("关于葬送的芙莉莲的采访", norms); got != 2 {
		t.Fatalf("contains rank = %d", got)
	}
	if got := nameRank("别的东西", norms); got != 2 {
		t.Fatalf("no-match rank = %d", got)
	}
	if !anyNormMatches(norms, func(n string) bool { return n == "芙莉莲" }) {
		t.Fatal("anyNormMatches should hit 芙莉莲")
	}
}
