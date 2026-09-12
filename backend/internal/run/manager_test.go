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
	"unicode/utf8"

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
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	joined := strings.Join(types, ",")
	// Responses 流事件 + wikiatlas.* 旁路：两条线都要在。
	for _, need := range []string{
		"response.created", "response.output_item.added", "response.output_text.delta",
		"response.function_call_arguments.done", "response.output_item.done", "response.completed",
		"wikiatlas.content.staging", "wikiatlas.content.committed", "wikiatlas.meta",
	} {
		if !strings.Contains(joined, need) {
			t.Fatalf("missing event %s in sequence: %s", need, joined)
		}
	}
	// 工具结果（含"已加载 skill"那句摘要）挂在 function_call 项上
	foundSkill := false
	for _, txt := range functionCallSummaries(events) {
		if strings.Contains(txt, "skill") || strings.Contains(txt, "SKILL") {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Fatalf("expected skill in tool result, got %v", functionCallSummaries(events))
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
		case "wikiatlas.content.staging":
			sawStaging = true
		case "wikiatlas.content.committed":
			sawCommitted = true
		case "response.output_item.done":
			if it, ok := ev.Payload["item"].(map[string]any); ok && it["type"] == "function_call" {
				sawToolDone = true
			}
		case "wikiatlas.tree":
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

	// 检查点只存执行进度，不存消息数组——消息的耐久副本是会话日志。
	// （本测试走 mock executor，没有模型往返，所以日志本身由
	//  TestFollowUpRunCarriesSessionTranscript 那条真实循环覆盖。）
	rr, _ := st.GetRun(created.ID)
	if rr.Checkpoint["messages"] != nil {
		t.Fatal("检查点不该再存消息数组（那是第二个事实源）")
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

// 同一会话第二单带上前几单对话（转录）的行为在 transcript_test.go 里用录制客户端验证。
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

// functionCallSummaries 收集所有工具项的结果摘要（挂在项的 wikiatlas 键下）。
func functionCallSummaries(events []domain.RunEvent) []string {
	out := []string{}
	for _, ev := range events {
		if ev.Type != "response.output_item.done" {
			continue
		}
		it, ok := ev.Payload["item"].(map[string]any)
		if !ok || it["type"] != "function_call" {
			continue
		}
		if extra, ok := it["wikiatlas"].(map[string]any); ok {
			if s, ok := extra["outputSummary"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// streamingScriptedClient 按脚本流式吐字：文本与工具参数分块喂进 onDelta，
// 模拟真实网关的增量到达（非流式的 scriptedClient 测不到转发环节）。
type streamingScriptedClient struct {
	steps []streamStep
	i     int
}

type streamStep struct {
	textDeltas []string
	argDeltas  []string
	toolName   string
	toolID     string
	result     llm.ChatResult
}

func (c *streamingScriptedClient) Model() string { return "scripted-stream" }

func (c *streamingScriptedClient) Chat(ctx context.Context, m []llm.Message, t []llm.ToolDef) (llm.ChatResult, error) {
	return c.ChatStream(ctx, m, t, nil)
}

func (c *streamingScriptedClient) ChatStream(_ context.Context, _ []llm.Message, _ []llm.ToolDef, onDelta func(llm.Delta)) (llm.ChatResult, error) {
	if c.i >= len(c.steps) {
		return llm.ChatResult{Content: "完成。"}, nil
	}
	s := c.steps[c.i]
	c.i++
	for _, txt := range s.textDeltas {
		if onDelta != nil {
			onDelta(llm.Delta{Kind: "text", Text: txt})
		}
	}
	for _, a := range s.argDeltas {
		if onDelta != nil {
			onDelta(llm.Delta{Kind: "tool", Text: a, ToolIndex: 0, ToolID: s.toolID, ToolName: s.toolName})
		}
	}
	return s.result, nil
}

// 工具参数增量必须**当场转发**。回归：曾经执行器把参数攒到流结束才一次性喂给
// 发射器——界面上"沉默 216 秒、然后 11752 字符砸下来"（真实事故：看门狗没杀它，
// 因为字节一直在到，是我们自己扣着不发）。文本尾巴同理，不能挂在节流缓冲里
// 等下一个文本增量。
func TestStreamingForwardsToolArgsIncrementally(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	med := domain.MediumGame
	w, err := st.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Medium: &med, Title: "流式增量样本",
	})
	if err != nil {
		t.Fatal(err)
	}

	full := mustJSON(map[string]any{
		"targetType": "work", "targetId": w.ID, "contentMd": longWiki(), "summary": "streamed",
	})
	// 按 **rune 边界**切分（真实网关按 token 边界切；切在多字节字符中间会
	// 产生非法 UTF-8，而事件落库时 json.Marshal 会把它替换成 U+FFFD）。
	c1, c2 := len(full)/3, 2*len(full)/3
	for c1 < len(full) && !utf8.RuneStart(full[c1]) {
		c1++
	}
	for c2 < len(full) && !utf8.RuneStart(full[c2]) {
		c2++
	}

	client := &streamingScriptedClient{steps: []streamStep{{
		textDeltas: []string{"先", "说两句话。", "然后把这一句话完整说完。"},
		argDeltas:  []string{full[:c1], full[c1:c2], full[c2:]},
		toolName:   "write_content",
		toolID:     "call_1",
		result: llm.ChatResult{ToolCalls: []llm.ToolCall{{
			ID: "call_1", Type: "function",
			Function: llm.FunctionCall{Name: "write_content", Arguments: full},
		}}},
	}, {result: llm.ChatResult{Content: "写完。"}}}}

	mgr := run.NewManager(st, client)
	mgr.SetSkillRoot(writeSkillRoot(t))
	created, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent:  domain.RunIntentCreateWiki,
		Goal:    "流式增量验证",
		Context: &runCtx{WorkID: &w.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, created.ID, domain.RunStatusCompleted)

	events, _ := st.ListRunEvents(created.ID, 0, 2000)
	var (
		argDeltas   int
		argDeltaLen int
		argSizes    []string
		argFirstSeq int64 = -1
		tailTextSeq int64 = -1
		textAll     strings.Builder
	)
	for _, ev := range events {
		switch ev.Type {
		case "response.output_text.delta":
			if s, ok := ev.Payload["delta"].(string); ok {
				textAll.WriteString(s)
				if strings.Contains(s, "完整说完") {
					tailTextSeq = ev.Seq
				}
			}
		case "response.function_call_arguments.delta":
			argDeltas++
			if s, ok := ev.Payload["delta"].(string); ok {
				argDeltaLen += len(s)
				argSizes = append(argSizes, fmt.Sprintf("%d:%q", len(s), s[:min(20, len(s))]))
			}
			if argFirstSeq < 0 {
				argFirstSeq = ev.Seq
			}
		}
	}
	if argDeltas < 3 {
		t.Fatalf("参数增量没有逐块转发：fnargs delta = %d 条（应为 3）——检查 onDelta 的 tool 分支是否当场 callArgsStream", argDeltas)
	}
	if argDeltaLen != len(full) {
		t.Fatalf("参数增量总长 %d != 参数全长 %d（丢了或重了）：%v", argDeltaLen, len(full), argSizes)
	}
	if tailTextSeq < 0 {
		t.Fatalf("文本尾巴丢在节流缓冲里（没等到下一个文本增量）：流出的文本 = %q", textAll.String())
	}
	if tailTextSeq > argFirstSeq {
		t.Fatalf("文本尾巴应排在参数增量之前（tail=%d argFirst=%d）——参数一到就该 flush 文本", tailTextSeq, argFirstSeq)
	}
}

// 复述抑制。事故复现：模型流式说了一段（自检语＋总结），又把总结原样走
// narrative 说一遍——界面上同一段出现两次。两条通道（扫尾 controlText、
// 工具执行时 Emit→emitNarrative）都要按"包含"判定挡住。
func TestNarrativeRepeatSuppressed(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	w, err := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "复述样本"})
	if err != nil {
		t.Fatal(err)
	}

	summary := "《复述样本》条目已建档完成，九章齐全，参考资料 5 条。"
	streamed := "自检完成：章名与题记都核过了。\n\n" + summary
	client := &streamingScriptedClient{steps: []streamStep{{
		textDeltas: []string{streamed[:12], streamed[12:]},
		result: llm.ChatResult{ToolCalls: []llm.ToolCall{{
			ID: "c1", Type: "function",
			Function: llm.FunctionCall{Name: "narrative", Arguments: mustJSON(map[string]any{"text": summary})},
		}}},
	}, {result: llm.ChatResult{Content: "完成。"}}}}

	mgr := run.NewManager(st, client)
	mgr.SetSkillRoot(writeSkillRoot(t))
	created, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent:  domain.RunIntentAnswer,
		Goal:    "复述抑制验证",
		Context: &runCtx{WorkID: &w.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, created.ID, domain.RunStatusCompleted)

	events, _ := st.ListRunEvents(created.ID, 0, 2000)
	var all strings.Builder
	for _, ev := range events {
		if ev.Type != "response.output_item.done" {
			continue
		}
		it, ok := ev.Payload["item"].(map[string]any)
		if !ok || it["type"] != "message" {
			continue
		}
		if parts, ok := it["content"].([]any); ok {
			for _, p := range parts {
				if pm, ok := p.(map[string]any); ok {
					if s, ok := pm["text"].(string); ok {
						all.WriteString(s)
					}
				}
			}
		}
	}
	if n := strings.Count(all.String(), summary); n != 1 {
		t.Fatalf("总结在消息里出现了 %d 次（应为 1 次）——复述没被抑制。全部消息文本：%q", n, all.String())
	}
}

// 任务清单新鲜度提醒。真实事故：龙与虎建档工单建完 5 项清单后，到写正文前
// 8 次检索一次没划勾（纪律就写在工具描述里，模型这一轮没遵守）。修法不是
// 加罚则，是把"该划勾了"搭在下一次工具结果里——这个测试钉住该行为：
// 清单建起后，第 4、5 次未更新的工具调用结果里应带上提醒；前 3 次不许催。
func TestStaleTodoReminder(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	searchCall := func(id, q string) llm.ToolCall {
		return llm.ToolCall{ID: id, Type: "function",
			Function: llm.FunctionCall{Name: "search_works", Arguments: mustJSON(map[string]any{"q": q})}}
	}
	client := &scriptedClient{steps: []llm.ChatResult{
		{ToolCalls: []llm.ToolCall{{ID: "c0", Type: "function",
			Function: llm.FunctionCall{Name: "todo_write", Arguments: `{"tasks":[` +
				`{"id":"1","content":"检索资料","activeForm":"正在检索资料","status":"in_progress"},` +
				`{"id":"2","content":"写作","activeForm":"正在写作","status":"pending"}]}`}}}},
		{ToolCalls: []llm.ToolCall{
			searchCall("c1", "甲"), searchCall("c2", "乙"), searchCall("c3", "丙"),
			searchCall("c4", "丁"), searchCall("c5", "戊"),
		}},
		{Content: "完成。"},
	}}

	mgr := run.NewManager(st, client)
	mgr.SetSkillRoot(writeSkillRoot(t))
	const sid = "test-stale-todo"
	created, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent:    domain.RunIntentAnswer,
		Goal:      "新鲜度提醒验证",
		Workspace: sid,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, created.ID, domain.RunStatusCompleted)

	msgs, err := st.ListSessionMessages(sid, false)
	if err != nil {
		t.Fatal(err)
	}
	var news []string
	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(m.Content, "任务清单已经") {
			news = append(news, m.ToolName)
		}
	}
	if len(news) != 2 {
		t.Fatalf("应恰好 2 条工具结果带清单提醒（第 4、5 次调用），实际 %d 条：%v", len(news), news)
	}
	// 前 3 次不许催
	for i, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "任务清单已经") {
			if i < 4 { // 会话里的前几条工具消息（todo 回执 + 前三次检索）
				t.Fatalf("过早提醒：第 %d 条消息就带了清单提醒", i+1)
			}
			break
		}
	}
}

// 工具名打错时（真实观察：模型把 search_works 写成 search_work），报错要顺手
// 给出最像的正确名字；完全不像的名字则不许乱指。
func TestUnknownToolSuggestsNearest(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	client := &scriptedClient{steps: []llm.ChatResult{
		{ToolCalls: []llm.ToolCall{
			{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "search_work", Arguments: `{"q":"龙与虎"}`}},
			{ID: "c2", Type: "function", Function: llm.FunctionCall{Name: "zzz_qq", Arguments: `{}`}},
		}},
		{Content: "好。"},
	}}
	mgr := run.NewManager(st, client)
	mgr.SetSkillRoot(writeSkillRoot(t))
	const sid = "test-unknown-tool"
	created, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent:    domain.RunIntentAnswer,
		Goal:      "工具名建议验证",
		Workspace: sid,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, created.ID, domain.RunStatusCompleted)

	msgs, err := st.ListSessionMessages(sid, false)
	if err != nil {
		t.Fatal(err)
	}
	var typoMsg, junkMsg string
	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		if m.ToolName == "search_work" {
			typoMsg = m.Content
		}
		if m.ToolName == "zzz_qq" {
			junkMsg = m.Content
		}
	}
	if !strings.Contains(typoMsg, "search_works") {
		t.Fatalf("打错的名字没有拿到建议：%q", typoMsg)
	}
	if strings.Contains(junkMsg, "你要找的可能是") {
		t.Fatalf("完全不像的名字不该编出建议：%q", junkMsg)
	}
}
