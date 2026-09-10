package run_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/llm"
	"wikiatlas/backend/internal/run"
	"wikiatlas/backend/internal/store"
)

type runCtx = domain.RunContext

func newManager(t *testing.T) (*run.Manager, *store.Store) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	mgr := run.NewManager(st, nil)
	t.Cleanup(func() {
		mgr.Stop()
		st.Close()
	})
	return mgr, st
}

func writeSkillRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"SKILL.md":       "# skill\n通用边界\n",
		"core.md":        "# core\n9 章骨架\n",
		"media-game.md":  "# game\n",
		"media-video.md": "# video\n",
		"media-book.md":  "# book\n",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(root, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func waitStatus(t *testing.T, st *store.Store, runID string, want domain.RunStatus) *domain.Run {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		r, err := st.GetRun(runID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status == want {
			return r
		}
		if r.Status == domain.RunStatusFailed {
			t.Fatalf("run failed: error=%v", r.Error)
		}
		time.Sleep(30 * time.Millisecond)
	}
	r, _ := st.GetRun(runID)
	t.Fatalf("timeout waiting %s, got %s", want, r.Status)
	return nil
}

func TestMockCreateWikiEventSequence(t *testing.T) {
	mgr, st := newManager(t)
	mgr.SetSkillRoot(writeSkillRoot(t))

	med := domain.MediumGame
	w, err := st.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Medium: &med, Title: "演示作品",
	})
	if err != nil {
		t.Fatal(err)
	}

	r, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent:  domain.RunIntentCreateWiki,
		Goal:    "为演示作品写 Wiki",
		Context: &runCtx{WorkID: &w.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, r.ID, domain.RunStatusCompleted)

	events, err := st.ListRunEvents(r.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, 0, len(events))
	texts := []string{}
	for _, ev := range events {
		types = append(types, ev.Type)
		if ev.Type == "narrative" {
			if s, ok := ev.Payload["text"].(string); ok {
				texts = append(texts, s)
			}
		}
	}
	joined := strings.Join(types, ",")
	for _, need := range []string{
		"run.started", "narrative", "tool.started", "tool.done",
		"plan.updated", "content.staging", "content.committed", "run.completed",
	} {
		if !strings.Contains(joined, need) {
			t.Fatalf("missing event %s in sequence: %s", need, joined)
		}
	}
	foundSkill := false
	for _, txt := range texts {
		if strings.Contains(txt, "skill") || strings.Contains(txt, "SKILL") {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Fatalf("expected skill narrative, got %v", texts)
	}

	wd, err := st.GetWork(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wd.ContentMd == nil || *wd.ContentMd == "" {
		t.Fatal("expected content written by mock")
	}
	if !strings.Contains(*wd.ContentMd, "## ") {
		t.Fatalf("expected ## sections in mock md")
	}
	if wd.Status != domain.WorkStatusDraft {
		t.Fatalf("short mock should stay draft, got %s", wd.Status)
	}
}

func TestMockWriteDoc(t *testing.T) {
	mgr, st := newManager(t)
	mgr.SetSkillRoot(writeSkillRoot(t))

	w, err := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "W"})
	if err != nil {
		t.Fatal(err)
	}
	med := domain.MediumGame
	r, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent:  domain.RunIntentWriteDoc,
		Goal:    "写设定资料",
		Context: &runCtx{WorkID: &w.ID, Medium: &med},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, r.ID, domain.RunStatusCompleted)
	wd, _ := st.GetWork(w.ID)
	if wd.ContentMd == nil || !strings.Contains(*wd.ContentMd, "资料文档") {
		t.Fatalf("write_doc content = %v", wd.ContentMd)
	}
	if strings.Contains(*wd.ContentMd, "## 9. 参考资料") {
		t.Fatal("write_doc should not force 9-chapter skeleton")
	}
}

type scriptedClient struct {
	steps []llm.ChatResult
	i     int
}

func (c *scriptedClient) Model() string { return "scripted" }

func (c *scriptedClient) Chat(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (llm.ChatResult, error) {
	if c.i >= len(c.steps) {
		return llm.ChatResult{Content: "完成。"}, nil
	}
	r := c.steps[c.i]
	c.i++
	return r, nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func longWiki() string {
	var md strings.Builder
	md.WriteString("# 真实管线作品（游戏）Wiki\n\n> 说明：scripted 夹具，含剧透。\n\n")
	md.WriteString(":::epigraph\n银剑在雾里，他向北走。\n:::\n\n")
	for i := 1; i <= 8; i++ {
		md.WriteString(fmt.Sprintf("## %d. 章节\n\n", i))
		md.WriteString(strings.Repeat("本段写清手法、事实与可核查细节，避免空泛评价。", 40))
		md.WriteString("\n\n")
	}
	md.WriteString("## 9. 参考资料\n\n")
	for i := 1; i <= 5; i++ {
		md.WriteString(fmt.Sprintf("- 来源%d：https://example.com/%d\n", i, i))
	}
	return md.String()
}

func TestRealLoopWithScriptedClient(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	skillRoot := writeSkillRoot(t)
	med := domain.MediumGame
	w, err := st.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Medium: &med, Title: "真实管线作品",
	})
	if err != nil {
		t.Fatal(err)
	}

	client := &scriptedClient{steps: []llm.ChatResult{
		{ToolCalls: []llm.ToolCall{{
			ID: "c1", Type: "function",
			Function: llm.FunctionCall{Name: "narrative", Arguments: `{"text":"开始检索"}`},
		}}},
		{ToolCalls: []llm.ToolCall{{
			ID: "c2", Type: "function",
			Function: llm.FunctionCall{Name: "search_works", Arguments: `{"q":"真实管线"}`},
		}}},
		{ToolCalls: []llm.ToolCall{{
			ID: "c3", Type: "function",
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

	created, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent:  domain.RunIntentCreateWiki,
		Goal:    "为真实管线作品写 Wiki",
		Context: &runCtx{WorkID: &w.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, created.ID, domain.RunStatusCompleted)

	events, _ := st.ListRunEvents(created.ID, 0, 200)
	var sawStaging, sawCommitted, sawToolDone, sawReady bool
	for _, ev := range events {
		switch ev.Type {
		case "content.staging":
			sawStaging = true
		case "content.committed":
			sawCommitted = true
		case "tool.done":
			sawToolDone = true
		case "tree.updated":
			if works, ok := ev.Payload["works"].([]any); ok && len(works) > 0 {
				if m, ok := works[0].(map[string]any); ok && m["status"] == "ready" {
					sawReady = true
				}
			}
		}
	}
	if !sawStaging || !sawCommitted || !sawToolDone {
		t.Fatalf("missing events staging=%v committed=%v toolDone=%v", sawStaging, sawCommitted, sawToolDone)
	}
	if !sawReady {
		t.Fatal("expected quality gate to mark ready")
	}

	wd, _ := st.GetWork(w.ID)
	if wd.ContentVer < 1 {
		t.Fatalf("contentVer=%d", wd.ContentVer)
	}
	if wd.Status != domain.WorkStatusReady {
		t.Fatalf("status=%s want ready", wd.Status)
	}

	// checkpoint should hold messages
	rr, _ := st.GetRun(created.ID)
	if rr.Checkpoint["messages"] == nil {
		t.Fatal("expected checkpoint.messages")
	}
}

// 只读模式：模型即使尝试写正文，也必须被拦下，content_md 保持不变。
func TestReadModeBlocksWrites(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	w, err := st.CreateWork(domain.CreateWorkBody{
		Kind:  domain.WorkKindWork,
		Title: "只读模式样本",
	})
	if err != nil {
		t.Fatal(err)
	}
	original := "# 原始正文\n\n> 说明：原有内容\n\n## 1. 章节\n\n原始段落。\n"
	if _, err := st.PutWorkContent(w.ID, domain.PutContentBody{
		ContentMd: original,
		Author:    domain.AuthorHuman,
	}); err != nil {
		t.Fatal(err)
	}

	client := &scriptedClient{steps: []llm.ChatResult{
		{ToolCalls: []llm.ToolCall{{
			ID: "r1", Type: "function",
			Function: llm.FunctionCall{Name: "write_content", Arguments: mustJSON(map[string]any{
				"targetType": "work",
				"targetId":   w.ID,
				"contentMd":  "# 被篡改\n",
				"summary":    "只读模式下不该发生",
			})},
		}}},
		{ToolCalls: []llm.ToolCall{{
			ID: "r2", Type: "function",
			Function: llm.FunctionCall{Name: "answer", Arguments: `{"text":"已给出评估，未改动正文。"}`},
		}}},
	}}
	mgr := run.NewManager(st, client)
	mgr.SetSkillRoot(writeSkillRoot(t))
	t.Cleanup(func() { mgr.Stop() })

	mode := "read"
	run, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent: domain.RunIntentAnswer,
		Goal:   "帮我评估这篇稿子的质量",
		Context: &domain.RunContext{
			WorkID:  &w.ID,
			DocMode: &mode,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, run.ID, domain.RunStatusCompleted)

	after, err := st.GetWork(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ContentMd == nil || *after.ContentMd != original {
		t.Fatalf("只读模式不应改动正文，得到: %v", after.ContentMd)
	}
	// 版本号也不应增加
	if after.ContentVer != 1 {
		t.Fatalf("只读模式 contentVer = %d, want 1", after.ContentVer)
	}
}

func TestResumeFromCheckpoint(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := run.NewManager(st, nil)
	mgr.SetSkillRoot(writeSkillRoot(t))

	r, err := st.CreateRun(domain.RunIntentCreateWiki, "测试续跑", "echo", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InterruptRun(r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Resume(r.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, r.ID, domain.RunStatusCompleted)
}

func TestAnswerRunNoWrite(t *testing.T) {
	mgr, st := newManager(t)
	r, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent: domain.RunIntentAnswer,
		Goal:   "这个库现在有什么？",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, r.ID, domain.RunStatusCompleted)
}
