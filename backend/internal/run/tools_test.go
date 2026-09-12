package run

import (
	"testing"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/llm"
)

func writeToolsIn(defs []llm.ToolDef) []string {
	var out []string
	for _, d := range defs {
		if writeToolNames[d.Function.Name] {
			out = append(out, d.Function.Name)
		}
	}
	return out
}

// 「翻译这段」这类一次性输出走 answer 意图：工具面里必须没有写工具，
// 否则模型会顺手把结果写回正文（真实事故，见 2026-09-10 的 continue_wiki 翻译工单）。
func TestAnswerIntentHasNoWriteTools(t *testing.T) {
	answer := buildToolDefsFor(domain.RunIntentAnswer, "edit")
	if len(answer) == 0 {
		t.Fatal("问答意图仍应下发只读工具")
	}
	if bad := writeToolsIn(answer); len(bad) > 0 {
		t.Fatalf("问答意图不该有写工具：%v", bad)
	}

	read := buildToolDefsFor(domain.RunIntentContinueWiki, "read")
	if bad := writeToolsIn(read); len(bad) > 0 {
		t.Fatalf("只读模式不该有写工具：%v", bad)
	}

	full := buildToolDefsFor(domain.RunIntentContinueWiki, "edit")
	if bad := writeToolsIn(full); len(bad) == 0 {
		t.Fatal("写作意图必须保留写工具")
	}
}

// edit 是"改一句"的正路（题记、说明行没有 ## 锚点，只有它够得着）：
// 写作意图必须下发，问答/只读必须收走。
func TestEditToolSurface(t *testing.T) {
	has := func(defs []llm.ToolDef) bool {
		for _, d := range defs {
			if d.Function.Name == "edit" {
				return true
			}
		}
		return false
	}
	if !has(buildToolDefsFor(domain.RunIntentContinueWiki, "edit")) {
		t.Fatal("写作意图必须下发 edit")
	}
	if has(buildToolDefsFor(domain.RunIntentAnswer, "edit")) {
		t.Fatal("问答意图不该下发 edit")
	}
	if has(buildToolDefsFor(domain.RunIntentContinueWiki, "read")) {
		t.Fatal("只读模式不该下发 edit")
	}
}

// 前缀检查：请求必须是上一次的追加延长；没有记录原因的改写要被抓出来。
// 这是 dsh"请求可从日志重建"的轻量落地——自建 vLLM 网关不回 cached_tokens，
// 缓存命中在 API 上不可观测，只能自己验。
func TestRequestShapeDetectsPrefixRewrite(t *testing.T) {
	base := []llm.Message{
		{Role: "system", Content: "S"},
		{Role: "user", Content: "U"},
	}
	s := requestShape{}
	if ok, _ := s.check(base); !ok {
		t.Fatal("首次请求不该报分歧")
	}
	s.prev = append([]llm.Message(nil), base...)
	s.seen = true

	extended := append(append([]llm.Message(nil), base...), llm.Message{Role: "assistant", Content: "A"})
	if ok, at := s.check(extended); !ok {
		t.Fatalf("追加延长应通过，却在 %d 处报分歧", at)
	}

	// 改写中间一条（压缩之外的任何改写都是 bug）
	rewritten := []llm.Message{{Role: "system", Content: "S"}, {Role: "user", Content: "被改了"}}
	if ok, at := s.check(rewritten); ok || at != 1 {
		t.Fatalf("改写应被抓到：ok=%v at=%d", ok, at)
	}
	// 变短
	if ok, _ := s.check(base[:1]); ok {
		t.Fatal("变短应被抓到")
	}
	// 有记录原因的重塑（压缩）→ 重建基线，不报
	s.reason = "compaction"
	if ok, _ := s.check(rewritten); !ok {
		t.Fatal("有记录的压缩不该报分歧")
	}
	// 工具调用的差异也要能看出来
	s2 := requestShape{seen: true, prev: []llm.Message{{
		Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "a", Function: llm.FunctionCall{Name: "read_work"}}},
	}}}
	if ok, _ := s2.check([]llm.Message{{
		Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "a", Function: llm.FunctionCall{Name: "write_content"}}},
	}}); ok {
		t.Fatal("工具调用变了却没被发现")
	}
}
