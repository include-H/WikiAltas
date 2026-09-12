package run

import (
	"context"
	"strings"
	"testing"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/llm"
	"wikiatlas/backend/internal/store"
)

func newTranscriptManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, nil)
	t.Cleanup(func() {
		mgr.Stop()
		st.Close()
	})
	return mgr, st
}

func TestSessionTranscript(t *testing.T) {
	mgr, st := newTranscriptManager(t)

	// 第一单：完整跑完（有结论 + 写入回执 + 固定播报句）
	r1, err := st.CreateRun("answer", "用一句话介绍这部作品", "m", "work:w1")
	if err != nil {
		t.Fatal(err)
	}
	// 一单的经过 = Responses 流事件：Altas 说的话是 message 输出项，
	// 写入回执是 wikiatlas.content.committed（宿主的播报不进转录）。
	appendMessageItem(t, st, r1.ID, "《巫师3》是一部开放世界动作角色扮演游戏。")
	if _, err := st.AppendRunEvent(r1.ID, EvWACommit, map[string]any{"version": float64(3)}); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteRun(r1.ID, map[string]any{"summary": "工单完成"}); err != nil {
		t.Fatal(err)
	}

	// 第二单：仍在跑——不应进转录
	r2, err := st.CreateRun("answer", "第二个问题", "m", "work:w1")
	if err != nil {
		t.Fatal(err)
	}
	appendMessageItem(t, st, r2.ID, "还在写")

	turns, older := mgr.sessionTranscript("work:w1", 8)
	if older != 0 {
		t.Fatalf("older = %d, want 0", older)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1 (running run must be skipped)", len(turns))
	}
	got := turns[0]
	if got.Goal != "用一句话介绍这部作品" {
		t.Fatalf("goal = %q", got.Goal)
	}
	if !strings.Contains(got.Answer, "巫师3") {
		t.Fatalf("answer = %q, want last real narrative", got.Answer)
	}
	if len(got.Writes) != 1 || !strings.Contains(got.Writes[0], "v3") {
		t.Fatalf("writes = %v, want 已写入正文 v3", got.Writes)
	}

	// 单轮压成一条 assistant 文案（给老会话补种日志时用）
	if got := turnMessage(got); !strings.Contains(got, "已写入正文 v3") {
		t.Fatalf("turn message = %q, want write receipt", got)
	}

	// 会话为空 / 不同会话：没有转录
	if turns, _ := mgr.sessionTranscript("work:none", 8); len(turns) != 0 {
		t.Fatalf("empty session should have no transcript, got %d", len(turns))
	}
}

func TestSessionTranscriptBudget(t *testing.T) {
	mgr, st := newTranscriptManager(t)
	long := strings.Repeat("长", 3000) // 每单 3000+ 字，预算 16000 只装得下最近几单
	for i := 0; i < 8; i++ {
		r, err := st.CreateRun("answer", "问题"+string(rune('A'+i)), "m", "work:w2")
		if err != nil {
			t.Fatal(err)
		}
		appendMessageItem(t, st, r.ID, long)
		if err := st.CompleteRun(r.ID, map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	turns, older := mgr.sessionTranscript("work:w2", 8)
	if older == 0 {
		t.Fatal("expected budget to drop old turns")
	}
	if len(turns) == 0 || !strings.HasPrefix(turns[len(turns)-1].Goal, "问题") {
		t.Fatalf("kept turns should end with the newest, got %+v", turns)
	}
	if len(turns)+older > 8 {
		t.Fatalf("kept+older = %d, want <= 8", len(turns)+older)
	}
}

// 首轮与延续轮的 user 文案：延续轮必须明说"上文就是完整经过"。
// （消息历史现在来自会话日志的投影，不再有"把前几单压成几行塞进提示"这回事。）
func TestBuildUserTurnMarksContinuation(t *testing.T) {
	first := buildUserTurn(domain.RunIntentContinueWiki, "再检查一遍全文",
		map[string]any{}, domain.MediumGame, "w1", "", "edit", "", 1)
	if strings.Contains(first, "这是本会话的延续") {
		t.Fatal("首轮不该说延续")
	}
	later := buildUserTurn(domain.RunIntentContinueWiki, "再检查一遍全文",
		map[string]any{}, domain.MediumGame, "w1", "", "edit", "", 3)
	if !strings.Contains(later, "这是本会话的延续") {
		t.Fatal("延续轮应说明上文是本会话的完整经过")
	}
	if !strings.Contains(later, "再检查一遍全文") || !strings.Contains(later, "w1") {
		t.Fatalf("工单信息不完整：%q", later)
	}
}

// recordingClient 记下每次请求的消息数组，用来断言"上一单的内容确实在输入里"。
type recordingClient struct {
	calls [][]llm.Message
	reply string
}

func (c *recordingClient) Model() string { return "recording" }

func (c *recordingClient) Chat(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (llm.ChatResult, error) {
	cp := make([]llm.Message, len(msgs))
	copy(cp, msgs)
	c.calls = append(c.calls, cp)
	return llm.ChatResult{Content: c.reply}, nil
}

func waitTerminal(t *testing.T, st *store.Store, runID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := st.GetRun(runID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != domain.RunStatusRunning {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s still running", runID)
}

// 同一会话的第二单，模型输入里必须带上前一单的问答（这就是"继续聊"的记忆）。
func TestFollowUpRunCarriesSessionTranscript(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	client := &recordingClient{reply: "看过了，第三章确实该补一句。"}
	mgr := NewManager(st, client)
	t.Cleanup(func() { mgr.Stop() })

	ws := "work:demo"
	first, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent: domain.RunIntentAnswer, Goal: "先看看这篇稿子", Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, st, first.ID)

	second, err := mgr.CreateAndStart(domain.CreateRunBody{
		Intent: domain.RunIntentAnswer, Goal: "那第三章再补一句", Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, st, second.ID)

	if len(client.calls) < 2 {
		t.Fatalf("calls = %d, want >= 2", len(client.calls))
	}
	last := client.calls[len(client.calls)-1]
	pairFound := false
	for i, m := range last {
		// user 文案是整段工单框架（目标嵌在里面），所以用包含判断
		if m.Role == "user" && strings.Contains(m.Content, "先看看这篇稿子") &&
			i+1 < len(last) && last[i+1].Role == "assistant" && strings.Contains(last[i+1].Content, "看过了") {
			pairFound = true
		}
	}
	if !pairFound {
		t.Fatalf("second run should carry first run's Q&A, got %+v", last)
	}
	if tail := last[len(last)-1]; tail.Role != "user" || !strings.Contains(tail.Content, "那第三章再补一句") {
		t.Fatalf("last message = %+v, want current task", tail)
	}

	// 记忆的载体是**会话日志**：两单的往返都在里面，第二单的请求是它的投影
	rows, err := st.ListSessionMessages(ws, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 4 { // system + user + assistant + 第二单的 user
		t.Fatalf("会话日志太短：%d 行", len(rows))
	}
	if rows[0].Role != "system" {
		t.Fatalf("投影的第一条应是 system：%+v", rows[0])
	}
	// 第二单的 user 行对同一个工具面，reason 必须是 continue（前缀是追加延长）
	var lastUser *store.SessionMessage
	for i := range rows {
		if rows[i].Role == "user" {
			lastUser = &rows[i]
		}
	}
	if lastUser == nil || lastUser.HeaderReason != "continue" {
		t.Fatalf("第二单应是同一会话的延续：%+v", lastUser)
	}
	// 两单共用同一条系统提示（内容没变 → 没有多余的 node 0 替换）
	sysRows := 0
	for _, r := range rows {
		if r.Role == "system" {
			sysRows++
		}
	}
	if sysRows != 1 {
		t.Fatalf("系统提示不该重复追加：%d 行", sysRows)
	}
}

// appendMessageItem 往 run 的事件流里追加一条"Altas 说的话"（Responses 的 message
// 输出项），形状与执行器发出的完全一致。
func appendMessageItem(t *testing.T, st *store.Store, runID, text string) {
	t.Helper()
	item := map[string]any{
		"type": "message", "id": "msg_0", "role": "assistant", "status": "completed",
		"content": []map[string]any{{"type": "output_text", "text": text}},
	}
	if _, err := st.AppendRunEvent(runID, EvItemDone, map[string]any{"output_index": 0, "item": item}); err != nil {
		t.Fatal(err)
	}
}
