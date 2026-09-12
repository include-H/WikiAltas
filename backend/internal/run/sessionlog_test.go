package run

import (
	"testing"

	"wikiatlas/backend/internal/store"
)

// 回归：首轮开工时系统提示必须真的进了消息数组。
//
// 曾经只把 system 行写进日志、忘了加进内存数组，结果是**第一轮请求里根本没有
// 系统提示**，而日志投影却有——重建不变量当场喊了出来（rebuildOK=false）。
func TestOpenSessionSendsSystemPromptOnFirstTurn(t *testing.T) {
	m := newTestManager(t)
	const sess = "work:first"
	msgs, turn, reason, err := m.openSession(sess, "run1", "SYSTEM_PROMPT_MARKER", "read_work,edit",
		func(int) string { return "用户的第一句话" })
	if err != nil {
		t.Fatal(err)
	}
	if turn != 1 || reason != headerInitial {
		t.Fatalf("turn=%d reason=%s，want 1/initial", turn, reason)
	}
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[0].Content != "SYSTEM_PROMPT_MARKER" {
		t.Fatalf("首轮必须带系统提示：%+v", msgs)
	}
	// 内存这份必须和日志投影逐条一致——重建不变量的硬要求
	rebuilt, err := m.deriveSessionMessages(sess)
	if err != nil {
		t.Fatal(err)
	}
	if !messagesEqual(rebuilt, msgs) {
		t.Fatalf("内存与日志不一致：\n内存 %+v\n日志 %+v", msgs, rebuilt)
	}
}

// 同一会话第二轮：历史接着上一轮，工具面没变 → continue。
func TestOpenSessionContinuesSameSession(t *testing.T) {
	m := newTestManager(t)
	const sess = "work:second"
	if _, _, _, err := m.openSession(sess, "r1", "SYS", "tools", func(int) string { return "第一句" }); err != nil {
		t.Fatal(err)
	}
	msgs, turn, reason, err := m.openSession(sess, "r2", "SYS", "tools", func(int) string { return "第二句" })
	if err != nil {
		t.Fatal(err)
	}
	if turn != 2 || reason != headerContinue {
		t.Fatalf("turn=%d reason=%s，want 2/continue", turn, reason)
	}
	if len(msgs) != 3 { // system + 第一句 + 第二句
		t.Fatalf("第二轮应接着历史：%+v", msgs)
	}
	if msgs[len(msgs)-1].Content != "第二句" {
		t.Fatalf("末条应是本轮 user：%+v", msgs[len(msgs)-1])
	}
	n := 0
	for _, x := range msgs {
		if x.Role == "system" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("系统提示重复了 %d 次", n)
	}
}

// 系统提示/工具面变了 → change，且系统提示是**替换**而不是再追加一条。
func TestOpenSessionReportsHeaderChange(t *testing.T) {
	m := newTestManager(t)
	const sess = "work:third"
	if _, _, _, err := m.openSession(sess, "r1", "SYS-A", "tools", func(int) string { return "第一句" }); err != nil {
		t.Fatal(err)
	}
	msgs, _, reason, err := m.openSession(sess, "r2", "SYS-B", "tools", func(int) string { return "第二句" })
	if err != nil {
		t.Fatal(err)
	}
	if reason != headerChange {
		t.Fatalf("系统提示变了应为 change，得到 %s", reason)
	}
	var sys []int
	for i, x := range msgs {
		if x.Role == "system" {
			sys = append(sys, i)
		}
	}
	if len(sys) != 1 || msgs[sys[0]].Content != "SYS-B" {
		t.Fatalf("系统提示应被替换成新的且只有一条：%+v", msgs)
	}
	if msgs[0].Role != "system" {
		t.Fatalf("替换后 system 仍在最前：%+v", msgs[0])
	}
}

// 崩在半截工具调用上：续跑前要补出收尾，否则序列非法（provider 直接 400）。
func TestCloseOpenTurnAddsMissingToolResults(t *testing.T) {
	m := newTestManager(t)
	const sess = "work:crash"
	put := func(row store.SessionMessage) {
		row.SessionID = sess
		if _, err := m.store.AppendSessionMessage(row); err != nil {
			t.Fatal(err)
		}
	}
	put(store.SessionMessage{Role: "system", Content: "S"})
	put(store.SessionMessage{Role: "user", Content: "U"})
	put(store.SessionMessage{Role: "assistant",
		ToolCallsJSON: `[{"id":"c1","type":"function"},{"id":"c2","type":"function"}]`})
	put(store.SessionMessage{Role: "tool", ToolCallID: "c1", Content: "已经回来了"})

	closed, err := m.closeOpenTurn(sess)
	if err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("只该补 c2 一条：%d", closed)
	}
	msgs, err := m.deriveSessionMessages(sess)
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || last.ToolCallID != "c2" {
		t.Fatalf("末尾应补上 c2 的结果：%+v", last)
	}
	// 已经补过的不能再补
	if again, _ := m.closeOpenTurn(sess); again != 0 {
		t.Fatalf("补过就不该再补：%d", again)
	}
}
