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
		case "narrative":
			payload = fmt.Sprint(ev.Payload["text"])
		case "tool.started":
			payload = fmt.Sprintf("%v %v", ev.Payload["name"], ev.Payload["inputSummary"])
		case "tool.done":
			payload = fmt.Sprintf("%v → %v", ev.Payload["name"], ev.Payload["outputSummary"])
		case "content.staging":
			payload = fmt.Sprintf("%v:%v", ev.Payload["targetType"], ev.Payload["targetId"])
		case "content.committed":
			payload = fmt.Sprintf("%v:%v v%v", ev.Payload["targetType"], ev.Payload["targetId"], ev.Payload["version"])
		case "plan.updated":
			payload = "tasks updated"
		case "run.completed":
			payload = fmt.Sprint(ev.Payload["summary"])
		case "tree.updated":
			payload = "status refresh"
		case "run.started":
			payload = fmt.Sprint(ev.Payload["goal"])
		}
		if payload != "" {
			fmt.Printf("[%s] %s\n", ev.Type, payload)
		} else {
			fmt.Printf("[%s]\n", ev.Type)
		}
	}
}
