package run

import (
	"fmt"
	"strings"

	"wikiatlas/backend/internal/domain"
)

// 会话转录：新工单开工前，把同一会话里前几单的「用户问 + Altas 答 + 写入回执」
// 作为真实对话消息喂给模型——这是"召回旧会话继续聊"的记忆。
// 规模控制：最多 maxTranscriptRuns 单，总字符超预算就从最老的开始丢。
const (
	maxTranscriptRuns    = 8
	transcriptBudgetChar = 16000
	turnAnswerCapRunes   = 400
)

type sessionTurn struct {
	Goal   string
	Answer string
	Writes []string
	Status domain.RunStatus
}

// sessionTranscript 读同会话最近的工单并压成对话轮。第二个返回值是被省略的更早单数。
func (m *Manager) sessionTranscript(sessionID string, maxRuns int) ([]sessionTurn, int) {
	if sessionID == "" || maxRuns <= 0 {
		return nil, 0
	}
	runs, err := m.store.ListRuns("", sessionID, maxRuns+1)
	if err != nil || len(runs) == 0 {
		return nil, 0
	}
	older := 0
	if len(runs) > maxRuns {
		older = len(runs) - maxRuns
		runs = runs[:maxRuns]
	}
	turns := make([]sessionTurn, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- { // ListRuns 是倒序，这里翻成时间顺序
		r := runs[i]
		if r.Status == domain.RunStatusRunning {
			continue // 还在跑的没有结论，不进转录
		}
		t := sessionTurn{Goal: r.Goal, Status: r.Status}
		if events, err := m.store.ListRunEventsPlain(r.ID, 0, 500); err == nil {
			for _, ev := range events {
				switch ev.Type {
				case EvItemDone:
					// Altas 说过的话 = 一条 message 输出项。宿主的播报不在其中
					// （它们走 wikiatlas.notice），所以这里不需要再按前缀排除固定句。
					if txt := messageItemText(ev.Payload["item"]); txt != "" {
						t.Answer = txt
					}
				case EvWACommit:
					if v, ok := ev.Payload["version"].(float64); ok {
						t.Writes = append(t.Writes, fmt.Sprintf("已写入正文 v%d", int(v)))
					}
				}
			}
		}
		turns = append(turns, t)
	}
	total := 0
	for _, t := range turns {
		total += len([]rune(t.Goal)) + len([]rune(t.Answer))
	}
	for len(turns) > 1 && total > transcriptBudgetChar {
		total -= len([]rune(turns[0].Goal)) + len([]rune(turns[0].Answer))
		turns = turns[1:]
		older++
	}
	return turns, older
}

// turnMessage 把一轮压成 assistant 侧的一条消息。
func turnMessage(t sessionTurn) string {
	var b strings.Builder
	answer := truncate(t.Answer, turnAnswerCapRunes)
	if answer == "" {
		answer = "（这单没有产出文字结论）"
	}
	b.WriteString(answer)
	if len(t.Writes) > 0 {
		b.WriteString("\n（" + strings.Join(t.Writes, "；") + "）")
	}
	switch t.Status {
	case domain.RunStatusFailed:
		b.WriteString("\n（这单最后失败了）")
	case domain.RunStatusInterrupted:
		b.WriteString("\n（这单被中断，可能没做完）")
	}
	return b.String()
}
