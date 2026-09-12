package run_test

import (
	"testing"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/llm"
	"wikiatlas/backend/internal/run"
	"wikiatlas/backend/internal/store"
)

// 写完之后：宿主钩子（节点扫库）必须带上 workId 触发一次；
// 问答工单不写正文，不该触发。
func TestOnWikiWrittenHookFires(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	skillRoot := writeSkillRoot(t)
	med := domain.MediumGame
	w, err := st.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Medium: &med, Title: "钩子靶子",
	})
	if err != nil {
		t.Fatal(err)
	}

	client := &scriptedClient{steps: []llm.ChatResult{
		{ToolCalls: []llm.ToolCall{{
			ID: "c1", Type: "function",
			Function: llm.FunctionCall{Name: "write_content", Arguments: mustJSON(map[string]any{
				"targetType": "work",
				"targetId":   w.ID,
				"contentMd":  longWiki(),
				"summary":    "scripted write",
			})},
		}}},
		{Content: "写入完成。"},
	}}
	mgr := run.NewManager(st, client)
	mgr.SetSkillRoot(skillRoot)

	hooked := make(chan string, 4)
	mgr.SetOnWikiWritten(func(workID string) { hooked <- workID })

	created, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent:  domain.RunIntentCreateWiki,
		Goal:    "为钩子靶子写 Wiki",
		Context: &runCtx{WorkID: &w.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, created.ID, domain.RunStatusCompleted)

	select {
	case got := <-hooked:
		if got != w.ID {
			t.Fatalf("hook workID = %q, want %q", got, w.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("hook not fired for create_wiki")
	}

	// 问答工单：同样带 workId 上下文，但没写正文 → 不该触发
	created2, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent:  domain.RunIntentAnswer,
		Goal:    "这部作品讲了什么？",
		Context: &runCtx{WorkID: &w.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, created2.ID, domain.RunStatusCompleted)
	select {
	case got := <-hooked:
		t.Fatalf("hook should not fire for answer run, got %q", got)
	case <-time.After(500 * time.Millisecond):
	}
}
