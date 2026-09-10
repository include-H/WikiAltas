package run

import (
	"strings"
	"testing"
)

func TestCheckWikiQualityEmpty(t *testing.T) {
	q := CheckWikiQuality("")
	if q.OK {
		t.Fatal("empty should fail")
	}
}

func TestCheckWikiQualityNoSections(t *testing.T) {
	q := CheckWikiQuality("# Title\n\n只有标题没有章节，" + strings.Repeat("内容", 2000))
	if q.OK {
		t.Fatal("no ## should fail")
	}
}

func TestCheckWikiQualityShortStub(t *testing.T) {
	md := "## 1. 概览\n\n短。\n\n## 2. 其他\n\n也是短。\n"
	q := CheckWikiQuality(md)
	if q.OK {
		t.Fatalf("stub sections should fail: %+v", q)
	}
	if q.Sections != 2 {
		t.Fatalf("sections = %d", q.Sections)
	}
}

// 观测中发现的两类硬伤：缺题记、章节留空 / 参考资料不足 —— 质量门必须报出来。
func TestCheckWikiQualityCatchesMissingEpigraphAndEmptyTail(t *testing.T) {
	var b strings.Builder
	b.WriteString("# 作品（游戏）Wiki\n\n> 说明：无题记的稿子。\n\n")
	for i := 1; i <= 5; i++ {
		b.WriteString("## " + string(rune('0'+i)) + ". 章节\n\n")
		b.WriteString(strings.Repeat("本章写清手法与事实细节，避免空泛评价。", 30) + "\n\n")
	}
	b.WriteString("## 6. 主要角色与创作团队\n\n")
	b.WriteString("## 7. 评价与历史定位\n\n")
	b.WriteString("## 8. 一句话总结\n\n")
	b.WriteString("## 9. 参考资料\n\n")
	q := CheckWikiQuality(b.String())
	if q.OK {
		t.Fatal("缺题记 + 空章节 + 无参考资料 不应通过")
	}
	joined := strings.Join(q.Issues, "；")
	for _, want := range []string{"缺少题记围栏", "过短", "参考资料不足 5 条"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("issues 缺少 %q：%s", want, joined)
		}
	}
}

func TestCheckWikiQualityPass(t *testing.T) {
	var b strings.Builder
	b.WriteString("# 作品（游戏）Wiki\n\n> 说明：整理范围与剧透提示。\n\n")
	b.WriteString(":::epigraph\n世界在雾里，他向北走。\n:::\n\n")
	for i := 1; i <= 8; i++ {
		b.WriteString("## " + string(rune('0'+i)) + ". 章节\n\n")
		body := strings.Repeat("本章讨论具体手法与事实细节，避免空泛。", 30)
		b.WriteString(body + "\n\n")
	}
	// 第 9 章：≥5 条实际用过的来源
	b.WriteString("## 9. 参考资料\n\n")
	for i := 1; i <= 5; i++ {
		b.WriteString("- 来源" + string(rune('0'+i)) + "：https://example.com/" + string(rune('0'+i)) + "\n")
	}
	q := CheckWikiQuality(b.String())
	if !q.OK {
		t.Fatalf("expected pass, issues=%v charLen=%d", q.Issues, q.CharLen)
	}
	if q.CharLen < 3000 {
		t.Fatalf("charLen = %d", q.CharLen)
	}
}
