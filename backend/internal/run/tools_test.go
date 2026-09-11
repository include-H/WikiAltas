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
