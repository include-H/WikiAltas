package run

import (
	"strings"
	"testing"

	"wikiatlas/backend/internal/llm"
)

func msg(role, content string) llm.Message { return llm.Message{Role: role, Content: content} }

// 上下文压缩：中间整段丢掉，头和尾保留，返回的序列仍然合法
// （不会出现"孤儿 tool 回复"——那会让 provider 直接 400）。
func TestCompactHistoryKeepsHeadAndTail(t *testing.T) {
	messages := []llm.Message{
		msg("system", "S"),
		msg("user", "U"),
	}
	for i := 0; i < 20; i++ {
		messages = append(messages,
			llm.Message{Role: "assistant", Content: strings.Repeat("a", 4000), ToolCalls: []llm.ToolCall{{ID: "c", Type: "function"}}},
			msg("tool", strings.Repeat("t", 4000)),
		)
	}
	messages = append(messages, msg("user", "最后一问"))
	out, dropped := compactHistory(messages)
	if dropped == 0 {
		t.Fatalf("应当压缩：len=%d", len(messages))
	}
	if out[0].Role != "system" || out[1].Role != "user" {
		t.Fatalf("头两条必须保留：%+v", out[:2])
	}
	if out[len(out)-1].Content != "最后一问" {
		t.Fatalf("末条必须保留：%q", out[len(out)-1].Content)
	}
	if out[2].Role == "tool" {
		t.Fatal("压缩后的第一条中间消息不能是 tool 回复")
	}
	if !strings.Contains(out[2].Content, "上下文压缩") {
		t.Fatalf("应有压缩说明：%q", out[2].Content)
	}
	total := 0
	for _, m := range out {
		total += len(m.Content)
	}
	if total >= 120000 {
		t.Fatalf("压缩后仍然超预算：%d", total)
	}
}

// 预算内不动手。
func TestCompactHistoryNoopWhenSmall(t *testing.T) {
	messages := []llm.Message{msg("system", "S"), msg("user", "U"), msg("assistant", "ok")}
	if _, dropped := compactHistory(messages); dropped != 0 {
		t.Fatalf("小上下文不该压缩：dropped=%d", dropped)
	}
}
