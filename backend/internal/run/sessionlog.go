package run

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"wikiatlas/backend/internal/llm"
	"wikiatlas/backend/internal/store"
)

// 会话日志在 run 层的门面。
//
// 一条会话 = 一条追加式日志，模型的消息历史是它的**投影**，从不单独存一份数组。
// 这样做买到两件事：
//   1. 新工单接着上一次的日志往下走 —— 前几单查过的来源、做过的判断、改过哪一段，
//      全都在上下文里，不需要"召回"，因为从来没丢过。
//   2. 前缀天然稳定（dsh：stability is emergent, not managed）：
//      请求永远是上一次的追加延长，除非真的发生了系统提示变化或压缩。
//
// 两条硬规矩：
//   - 只追加，不改历史行；要"改"就追加一行并遮蔽旧行。
//   - 投影是纯函数：同一份日志永远折出同一组消息。

// hashText 给"请求的非历史状态"（系统提示、工具面）算短指纹。
func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// projectMessages 把日志行投影成模型消息。
//
// 排序规则一条：**活的 system 行永远在最前**（它本来就该是第一条消息），
// 其余按 store 排好的位置序（替换行占被替换节点的位置）。
// 替换过的 system 行会被遮蔽，所以活的只有一行。
func projectMessages(rows []store.SessionMessage) []llm.Message {
	toMsg := func(r store.SessionMessage) llm.Message {
		msg := llm.Message{Role: r.Role, Content: r.Content, ToolCallID: r.ToolCallID, Name: r.ToolName}
		if r.ToolCallsJSON != "" {
			var calls []llm.ToolCall
			if err := json.Unmarshal([]byte(r.ToolCallsJSON), &calls); err == nil {
				msg.ToolCalls = calls
			}
		}
		return msg
	}
	out := make([]llm.Message, 0, len(rows))
	for _, r := range rows {
		if r.Role == "system" {
			continue // 稍后统一放最前
		}
		out = append(out, toMsg(r))
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Role == "system" {
			out = append([]llm.Message{toMsg(rows[i])}, out...)
			break
		}
	}
	return out
}

// logMessage 把一条消息追加进会话日志。sessionID 为空时静默跳过
// （无会话的老路径与单测走内存数组即可）。
func (m *Manager) logMessage(sessionID, runID string, turn int, msg llm.Message) {
	if sessionID == "" {
		return
	}
	row := store.SessionMessage{
		SessionID: sessionID, RunID: runID, Turn: turn,
		Role: msg.Role, Content: msg.Content,
		ToolCallID: msg.ToolCallID, ToolName: msg.Name,
	}
	if len(msg.ToolCalls) > 0 {
		b, err := json.Marshal(msg.ToolCalls)
		if err != nil {
			return
		}
		row.ToolCallsJSON = string(b)
	}
	_, _ = m.store.AppendSessionMessage(row)
}

// deriveSession 读会话日志并投影成消息（同时把原始行返回，压缩要用 seq）。
func (m *Manager) deriveSession(sessionID string) ([]llm.Message, []store.SessionMessage, error) {
	rows, err := m.store.ListSessionMessages(sessionID, false)
	if err != nil {
		return nil, nil, err
	}
	return projectMessages(rows), rows, nil
}

// headerReason 描述这一轮相对上一轮的请求头变化（对照 dsh 的 request/header.reason）。
const (
	headerInitial  = "initial"  // 本会话第一轮
	headerContinue = "continue" // 头没变：这一轮是上一轮的追加延长
	headerChange   = "change"   // 头变了（工具面/配置不同）：这一次要付全价
)

// openSession 是本轮开工的入口：上下文从会话日志**派生**，不重建。
//
// 返回 (messages, turn, headerReason, err)。三件事：
//  1. 历史 = 日志投影（前几单的工具往返与结论都在里面，这就是"记得住"）
//  2. 系统提示变了 → 追加一行替换掉旧的 system（dsh：系统提示是派生历史，不是 header）
//  3. 追加本轮 user 行，并把请求头指纹与原因记进日志
//
// buildUser 拿得到本轮轮次，因为 user 文案里要区分"首轮/延续"。
func (m *Manager) openSession(sessionID, runID, sysPrompt, toolsSig string, buildUser func(turn int) string) ([]llm.Message, int, string, error) {
	lastUser, err := m.store.LastSessionUserTurn(sessionID)
	if err != nil {
		return nil, 0, "", err
	}
	turn := 1
	if lastUser != nil {
		turn = lastUser.Turn + 1
	}
	userTurn := buildUser(turn)

	if sessionID == "" {
		return []llm.Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userTurn},
		}, turn, headerInitial, nil
	}

	// 系统提示：内容变了就是一次 node 0 替换（会打断缓存，所以留痕）
	sysHash := hashText(sysPrompt)
	lastSys, err := m.store.LastSessionMessage(sessionID, "system")
	if err != nil {
		return nil, 0, "", err
	}
	switch {
	case lastSys == nil:
		if _, err := m.store.AppendSessionMessage(store.SessionMessage{
			SessionID: sessionID, RunID: runID, Role: "system",
			Content: sysPrompt, HeaderHash: sysHash,
		}); err != nil {
			return nil, 0, "", err
		}
	case lastSys.HeaderHash != sysHash:
		if _, err := m.store.ReplaceSessionRange(store.SessionMessage{
			SessionID: sessionID, RunID: runID, Role: "system",
			Content: sysPrompt, HeaderHash: sysHash,
		}, lastSys.Seq, lastSys.Seq); err != nil {
			return nil, 0, "", err
		}
	}

	// 本轮 user：请求头指纹 = 系统提示 + 工具面
	headerHash := hashText(sysPrompt + "\x00" + toolsSig)
	reason := headerInitial
	if lastUser != nil {
		if lastUser.HeaderHash == headerHash {
			reason = headerContinue
		} else {
			reason = headerChange
		}
	}
	if _, err := m.store.AppendSessionMessage(store.SessionMessage{
		SessionID: sessionID, RunID: runID, Turn: turn, Role: "user",
		Content: userTurn, HeaderHash: headerHash, HeaderReason: reason,
	}); err != nil {
		return nil, 0, "", err
	}

	// 写完日志再投影一次，拿到的就是权威上下文。
	// 不要在这里手工往数组里补消息——新增 system 行时漏补过一次，
	// 结果是系统提示根本没发出去（重建不变量当场喊了出来）。
	msgs, err := m.deriveSessionMessages(sessionID)
	if err != nil {
		return nil, 0, "", err
	}
	return msgs, turn, reason, nil
}

// seedSessionFromRuns 给老会话补种一次日志。
//
// 这次改造之前的会话只有工单、没有消息日志，直接用会"失忆"。所以开工前
// 用旧的转录（目标 + 最后一条叙事 + 写入回执）折成 user/assistant 行补进去；
// 只种一次，之后这个会话就走新的日志路径。取证式的历史补不回来（老的检查点是截断过的），
// 但至少不再是空白。
func (m *Manager) seedSessionFromRuns(sessionID, currentRunID string) error {
	if sessionID == "" {
		return nil
	}
	n, err := m.store.CountSessionMessages(sessionID)
	if err != nil || n > 0 {
		return err
	}
	turns, _ := m.sessionTranscript(sessionID, maxTranscriptRuns)
	for i, t := range turns {
		m.logMessage(sessionID, currentRunID, i+1, llm.Message{Role: "user", Content: t.Goal})
		m.logMessage(sessionID, currentRunID, i+1, llm.Message{Role: "assistant", Content: turnMessage(t)})
	}
	return nil
}

func (m *Manager) deriveSessionMessages(sessionID string) ([]llm.Message, error) {
	msgs, _, err := m.deriveSession(sessionID)
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

// closeOpenTurn 给中断的半截 turn 补收尾。
//
// 崩在工具执行中间时，日志末尾会留一条带 tool_calls 却没有对应 tool 结果的
// assistant 行——直接拿去请求会被 provider 拒（消息序列非法）。
// 补上和调用一一对应的错误结果即可，和 dsh 崩溃修复里补 closers 是同一件事。
func (m *Manager) closeOpenTurn(sessionID string) (int, error) {
	if sessionID == "" {
		return 0, nil
	}
	rows, err := m.store.ListSessionMessages(sessionID, false)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	// 末尾往回扫：连续的 tool 结果算"已经回答过的"
	i := len(rows) - 1
	for i >= 0 && rows[i].Role == "tool" {
		i--
	}
	if i < 0 || rows[i].Role != "assistant" || rows[i].ToolCallsJSON == "" {
		return 0, nil
	}
	var calls []llm.ToolCall
	if err := json.Unmarshal([]byte(rows[i].ToolCallsJSON), &calls); err != nil {
		return 0, err
	}
	answered := map[string]bool{}
	for _, r := range rows[i+1:] {
		if r.ToolCallID != "" {
			answered[r.ToolCallID] = true
		}
	}
	closed := 0
	for _, c := range calls {
		if answered[c.ID] {
			continue
		}
		m.logMessage(sessionID, rows[i].RunID, rows[i].Turn, llm.Message{
			Role: "tool", ToolCallID: c.ID, Name: c.Function.Name,
			Content: `{"ok":false,"message":"本单被中断，这次工具调用没有返回结果。"}`,
		})
		closed++
	}
	return closed, nil
}

// contextWindow 是**这一个数**：模型一次能装多少 token。
// 来自设置页（换模型就换窗口）；没设时用默认值。
func (m *Manager) contextWindow() int {
	if st, err := m.store.GetSettings(); err == nil && st.LLM.ContextWindow > 0 {
		return st.LLM.ContextWindow
	}
	return defaultContextWindow
}

// sessionTokens 估当前投影占多少 token：**优先用实测**（上一轮 provider 报的
// promptTokens，那就是"现在窗口里装了多少"），没有实测时按字节估（中文约 3 字节/token）。
func (m *Manager) sessionTokens(sessionID string, measured int) int {
	if measured > 0 || sessionID == "" {
		return measured
	}
	rows, err := m.store.ListSessionMessages(sessionID, false)
	if err != nil {
		return 0
	}
	bytes := 0
	for _, r := range rows {
		bytes += len(r.Content)
	}
	return bytes / bytesPerToken
}

// compactSession 是"压缩 = 日志上的一次显式替换"。
//
// 触发点 = 模型窗口 × compactRatio，用实测用量判断（测不到才估）。
// 注意基准是**窗口本身**，不是另拍一个预算数字——预算和窗口是两个数的时候，
// 就会出现"在 262k 的模型上用了四分之一就开始压缩"这种事。
//
// 返回是否真的压了。
func (m *Manager) compactSession(sessionID, runID string, turn int) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	rows, err := m.store.ListSessionMessages(sessionID, false)
	if err != nil {
		return false, err
	}
	if len(rows) <= contextKeepRecent+3 {
		return false, nil
	}
	// 保留：system（投影里恒在最前）+ 首条 user + 最近 contextKeepRecent 行
	start := 2 // rows[0] 是 system、rows[1] 是首条 user（投影序）
	if start >= len(rows)-contextKeepRecent {
		return false, nil
	}
	end := len(rows) - contextKeepRecent
	// 起点不能落在 tool 结果上：丢掉 assistant 却留下它的结果会让序列非法
	for start < end && rows[start].Role == "tool" {
		start++
	}
	if start >= end {
		return false, nil
	}

	summary := fmt.Sprintf(
		"（上下文压缩：中间 %d 条工具调用与返回已省略，因为窗口放不下。需要时重新调用工具读取，不要凭记忆写。）",
		end-start)
	if _, err := m.store.ReplaceSessionRange(store.SessionMessage{
		SessionID: sessionID, RunID: runID, Turn: turn, Role: "user", Content: summary,
	}, rows[start].Seq, rows[end-1].Seq); err != nil {
		return false, err
	}
	return true, nil
}

// sessionsEqual 比对两组消息是否逐条相同（重建不变量用）。
func messagesEqual(a, b []llm.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameMessage(a[i], b[i]) {
			return false
		}
	}
	return true
}

// summarizeToolNames 把工具面压成一行指纹串（请求头的一部分）。
func summarizeToolNames(defs []llm.ToolDef) string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Function.Name)
	}
	return strings.Join(names, ",")
}
