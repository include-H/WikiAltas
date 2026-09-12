package run

import (
	"strings"
	"testing"

	"wikiatlas/backend/internal/store"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, nil)
	t.Cleanup(func() {
		m.Stop()
		st.Close()
	})
	return m
}

// logRows 往会话日志里灌一段超预算的历史：system + 首条 user + N 对 assistant/tool。
func logRows(t *testing.T, m *Manager, session string, pairs int) {
	t.Helper()
	put := func(row store.SessionMessage) {
		row.SessionID = session
		if _, err := m.store.AppendSessionMessage(row); err != nil {
			t.Fatal(err)
		}
	}
	put(store.SessionMessage{Role: "system", Content: "S"})
	put(store.SessionMessage{Role: "user", Content: "首条用户消息"})
	big := strings.Repeat("a", 4000)
	for i := 0; i < pairs; i++ {
		put(store.SessionMessage{Role: "assistant", Content: big,
			ToolCallsJSON: `[{"id":"c","type":"function"}]`})
		put(store.SessionMessage{Role: "tool", Content: big, ToolCallID: "c"})
	}
}

// 压缩 = 会话日志上的**一次替换**，不是把数组拼短：
// 被替换的行留在日志里（只是不在投影上），摘要占被替换区间的起始位置。
func TestCompactSessionReplacesRangeInLog(t *testing.T) {
	m := newTestManager(t)
	const sess = "work:x"
	logRows(t, m, sess, 30)

	before, _, err := m.deriveSession(sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 62 {
		t.Fatalf("准备数据不对：%d 条", len(before))
	}

	did, err := m.compactSession(sess, "run1", 1)
	if err != nil || !did {
		t.Fatalf("应当压缩：did=%v err=%v", did, err)
	}

	after, _, err := m.deriveSession(sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) >= len(before) {
		t.Fatalf("压缩后没变短：%d → %d", len(before), len(after))
	}
	// system 永远在最前，首条 user 保留
	if after[0].Role != "system" || after[1].Role != "user" {
		t.Fatalf("头两条应保留：%+v %+v", after[0], after[1])
	}
	// 摘要占被替换区间的起始位置（第 3 条），角色是 user（对齐 dsh）
	if after[2].Role != "user" || !strings.Contains(after[2].Content, "上下文压缩") {
		t.Fatalf("摘要位置/内容不对：%+v", after[2])
	}
	// 压缩后第一条中间消息不能是 tool 回复（序列会非法）
	if after[2].Role == "tool" {
		t.Fatal("压缩后第一条中间消息不能是 tool")
	}
	// 最近若干条保留
	if after[len(after)-1].Role != "tool" {
		t.Fatalf("尾部应保留最近消息：%+v", after[len(after)-1])
	}
	total := 0
	for _, x := range after {
		total += len(x.Content)
	}
	// 压完的体量要明显下来（用同一套估算口径比）
	if got, before := m.sessionTokens(sess, 0), total; got <= 0 || got >= 60_000 {
		t.Fatalf("压缩后估算仍然过大：%d token（原始 %d 字节）", got, before)
	}
	// 被遮蔽的行**不能删**：日志是审计与回放的依据
	all, err := m.store.ListSessionMessages(sess, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) <= len(after) {
		t.Fatalf("被遮蔽的行不该消失：日志 %d 行、投影 %d 条", len(all), len(after))
	}
	// 再压一次：预算已经下来了，应当不再动手
	if did, err := m.compactSession(sess, "run1", 1); err != nil || did {
		t.Fatalf("刚压过不该再压：did=%v err=%v", did, err)
	}
}

func TestCompactSessionNoopWhenUnderBudget(t *testing.T) {
	m := newTestManager(t)
	const sess = "work:y"
	logRows(t, m, sess, 1)
	if did, err := m.compactSession(sess, "r", 1); err != nil || did {
		t.Fatalf("预算内不该压缩：did=%v err=%v", did, err)
	}
}

// 投影是纯函数：同一份日志折两次必须一模一样（重建不变量的前提）。
func TestProjectionIsDeterministic(t *testing.T) {
	m := newTestManager(t)
	const sess = "work:z"
	logRows(t, m, sess, 3)
	a, _, err := m.deriveSession(sess)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := m.deriveSession(sess)
	if err != nil {
		t.Fatal(err)
	}
	if !messagesEqual(a, b) {
		t.Fatalf("同一份日志折出两次不一样：\n%+v\n%+v", a, b)
	}
}

// 上下文窗口只有一个来源：设置页。没设时回默认值。
// （以前是"展示用窗口"和"实际压缩用的字节预算"两个数，于是 262k 的模型上
// 用了四分之一就开始压缩。）
func TestContextWindowComesFromSettings(t *testing.T) {
	m := newTestManager(t)
	if got := m.contextWindow(); got != defaultContextWindow {
		t.Fatalf("没设时应回默认窗口：%d", got)
	}
	st, err := m.store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	st.LLM.ContextWindow = 32000
	if err := m.store.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if got := m.contextWindow(); got != 32000 {
		t.Fatalf("应读设置里的窗口：%d", got)
	}
}

// 估算口径：有实测就用实测（provider 报的 promptTokens），没有才按字节估。
func TestSessionTokensPrefersMeasured(t *testing.T) {
	m := newTestManager(t)
	const sess = "work:tokens"
	logRows(t, m, sess, 2)
	if got := m.sessionTokens(sess, 12345); got != 12345 {
		t.Fatalf("有实测就该用实测：%d", got)
	}
	if got := m.sessionTokens(sess, 0); got <= 0 {
		t.Fatalf("没实测时应按字节估出正数：%d", got)
	}
}
