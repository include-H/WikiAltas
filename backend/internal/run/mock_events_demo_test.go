package run_test

import (
	"fmt"
	"testing"

	"wikiatlas/backend/internal/domain"
)

// TestPrintMockEventExample prints a realistic create_wiki mock event stream.
func TestPrintMockEventExample(t *testing.T) {
	mgr, st := newManager(t)
	mgr.SetSkillRoot(writeSkillRoot(t))
	med := domain.MediumGame
	w, err := st.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Medium: &med, Title: "荣誉勋章：血战太平洋",
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent:  domain.RunIntentCreateWiki,
		Goal:    "为《荣誉勋章：血战太平洋》写 Wiki",
		Context: &runCtx{WorkID: &w.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, r.ID, domain.RunStatusCompleted)
	events, _ := st.ListRunEvents(r.ID, 0, 200)
	for _, ev := range events {
		var payload string
		switch ev.Type {
		case "response.output_item.done":
			payload = describeItem(ev.Payload["item"])
		case "response.completed":
			payload = fmt.Sprint(ev.Payload["response"].(map[string]any)["status"])
		case "wikiatlas.content.staging":
			payload = fmt.Sprintf("%v:%v", ev.Payload["targetType"], ev.Payload["targetId"])
		case "wikiatlas.content.committed":
			payload = fmt.Sprintf("%v:%v v%v", ev.Payload["targetType"], ev.Payload["targetId"], ev.Payload["version"])
		case "wikiatlas.tree":
			payload = "status refresh"
		case "wikiatlas.notice":
			payload = fmt.Sprint(ev.Payload["text"])
		case "wikiatlas.todo":
			payload = fmt.Sprintf("%v 条任务", len(ev.Payload["tasks"].([]any)))
		case "wikiatlas.usage":
			payload = fmt.Sprintf("prompt=%v cacheRead=%v", ev.Payload["promptTokens"], ev.Payload["cacheReadTokens"])
		}
		// 只有 Responses 流事件带号；旁路事件打印 "-"
		seq := "-"
		if v, ok := ev.Payload["sequence_number"].(float64); ok {
			seq = fmt.Sprintf("%d", int(v))
		}
		if payload != "" {
			fmt.Printf("[%3s %s] %s\n", seq, ev.Type, payload)
		} else {
			fmt.Printf("[%3s %s]\n", seq, ev.Type)
		}
	}
}

// describeItem 把一条输出项压成一行（这是演示打印，不是断言）。
func describeItem(raw any) string {
	it, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	switch it["type"] {
	case "message":
		parts, _ := it["content"].([]map[string]any)
		if len(parts) > 0 {
			return fmt.Sprint(parts[0]["text"])
		}
	case "reasoning":
		return "（思考）"
	case "function_call":
		extra, _ := it["wikiatlas"].(map[string]any)
		return fmt.Sprintf("%v → %v", it["name"], extra["outputSummary"])
	}
	return ""
}
