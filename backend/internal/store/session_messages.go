package store

import (
	"database/sql"
	"strings"
)

// 会话消息日志（session_messages）：一条会话 = 一条追加式日志，
// 模型的消息历史是它的**投影**——不单独存一份数组，就不会出现
// "存下来的和模型看到的不一致"。
//
// 这是 dsh「模型可见 ⟺ 有日志」在 WikiAltas 的落地。两条硬规矩：
//   1. 只追加，不修改历史行；要"改"就追加一行并遮蔽旧行（shadowed_by）。
//   2. 投影 = 未遮蔽的行按 seq 升序，原样映射成消息，不做二次加工。

// SessionMessage 是会话日志的一行。
type SessionMessage struct {
	ID        string
	SessionID string
	Seq       int64
	// ReplacesSeq 标记这是一行"替换"：它占据从 ReplacesSeq 起的区间的位置。
	// 0 表示普通追加。（对应 dsh surface 的 replace{startSeq,endSeq}。）
	ReplacesSeq int64
	RunID       string
	Turn        int
	Role        string // system | user | assistant | tool
	Content     string
	// ToolCallsJSON 是 assistant 行上的工具调用原始 JSON；
	// store 不 import llm，所以按不透明字符串存取。
	ToolCallsJSON string
	ToolCallID    string
	ToolName      string
	// HeaderHash 是该行"身份相关状态"的指纹：system 行上=系统提示的哈希，
	// user 行上=本轮工具面+调用配置的哈希。变了就是一次请求重塑。
	HeaderHash string
	// HeaderReason 只写在 user 行上：initial | continue | change（对照 dsh 的 request/header）。
	HeaderReason string
	CreatedAt    string
}

const sessionMessageCols = `id, session_id, seq, replaces_seq, run_id, turn, role, content,
	tool_calls_json, tool_call_id, tool_name, header_hash, header_reason, created_at`

// orderByPosition 让替换行占被替换节点的位置：这是"投影"而不是"追加顺序"。
const orderByPosition = ` ORDER BY (CASE WHEN replaces_seq > 0 THEN replaces_seq ELSE seq END)`

func scanSessionMessage(row interface{ Scan(...any) error }) (*SessionMessage, error) {
	var m SessionMessage
	err := row.Scan(&m.ID, &m.SessionID, &m.Seq, &m.ReplacesSeq, &m.RunID, &m.Turn, &m.Role, &m.Content,
		&m.ToolCallsJSON, &m.ToolCallID, &m.ToolName, &m.HeaderHash, &m.HeaderReason, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// nextSessionSeq 在事务里分配会话内下一个序号。
func nextSessionSeq(tx *sql.Tx, sessionID string) (int64, error) {
	var next int64
	err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM session_messages WHERE session_id = ?`,
		sessionID).Scan(&next)
	return next, err
}

func insertSessionMessage(tx *sql.Tx, m *SessionMessage) error {
	if _, err := tx.Exec(`INSERT INTO session_messages (`+sessionMessageCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.SessionID, m.Seq, m.ReplacesSeq, m.RunID, m.Turn, m.Role, m.Content,
		m.ToolCallsJSON, m.ToolCallID, m.ToolName, m.HeaderHash, m.HeaderReason, m.CreatedAt); err != nil {
		return err
	}
	return nil
}

// AppendSessionMessage 往会话日志追加一行，序号在事务里分配（会话内单调）。
func (s *Store) AppendSessionMessage(m SessionMessage) (*SessionMessage, error) {
	if m.SessionID == "" {
		return nil, ErrValidation{Message: "sessionId is required"}
	}
	if m.Role == "" {
		return nil, ErrValidation{Message: "role is required"}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	seq, err := nextSessionSeq(tx, m.SessionID)
	if err != nil {
		return nil, err
	}
	m.ID = NewID()
	m.Seq = seq
	if m.Turn <= 0 {
		m.Turn = 1
	}
	if m.CreatedAt == "" {
		m.CreatedAt = Now()
	}
	if err := insertSessionMessage(tx, &m); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ReplaceSessionRange 一次替换：追加一行"替换行"（投影时占 startSeq 的位置），
// 并把 [startSeq, endSeq] 里尚未遮蔽的行标记为被它遮蔽。
//
// 两件事在同一个事务里，所以不会出现"遮蔽了却没有替换行"或反过来的中间态——
// 对应 dsh 里压缩的 start → summary → end 括号（这里靠事务而不是显式锁）。
func (s *Store) ReplaceSessionRange(m SessionMessage, startSeq, endSeq int64) (*SessionMessage, error) {
	if m.SessionID == "" || m.Role == "" {
		return nil, ErrValidation{Message: "sessionId and role are required"}
	}
	if startSeq <= 0 || endSeq < startSeq {
		return nil, ErrValidation{Message: "invalid replace range"}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	seq, err := nextSessionSeq(tx, m.SessionID)
	if err != nil {
		return nil, err
	}
	m.ID = NewID()
	m.Seq = seq
	m.ReplacesSeq = startSeq
	if m.Turn <= 0 {
		m.Turn = 1
	}
	if m.CreatedAt == "" {
		m.CreatedAt = Now()
	}
	if err := insertSessionMessage(tx, &m); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE session_messages SET shadowed_by = ?
		WHERE session_id = ? AND seq >= ? AND seq <= ? AND shadowed_by = ''`,
		m.ID, m.SessionID, startSeq, endSeq); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ListSessionMessages 按 seq 升序返回会话日志。
// includeShadowed=false 时只返回投影上的行（被压缩遮蔽的留在库里但不投影）。
func (s *Store) ListSessionMessages(sessionID string, includeShadowed bool) ([]SessionMessage, error) {
	if sessionID == "" {
		return nil, nil
	}
	q := `SELECT ` + sessionMessageCols + ` FROM session_messages WHERE session_id = ?`
	if !includeShadowed {
		q += ` AND shadowed_by = ''`
	}
	q += orderByPosition
	rows, err := s.DB.Query(q, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SessionMessage, 0)
	for rows.Next() {
		m, err := scanSessionMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// LastSessionMessage 取投影上位置最靠后的指定 role（找不到返回 nil）。
func (s *Store) LastSessionMessage(sessionID, role string) (*SessionMessage, error) {
	m, err := scanSessionMessage(s.DB.QueryRow(
		`SELECT `+sessionMessageCols+` FROM session_messages
		 WHERE session_id = ? AND role = ? AND shadowed_by = ''`+orderByPosition+` DESC LIMIT 1`,
		sessionID, role))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// LastSessionUserTurn 取最后一条**真正的用户轮**（排除压缩摘要那种替换行）。
// 摘要骑在 user 角色上（对齐 dsh），所以靠 replaces_seq 区分：
// 0 = 这一轮是用户发起的对话；>0 = 这是替换/摘要。
func (s *Store) LastSessionUserTurn(sessionID string) (*SessionMessage, error) {
	q := `SELECT ` + sessionMessageCols + ` FROM session_messages
		WHERE session_id = ? AND role = 'user' AND shadowed_by = '' AND replaces_seq = 0` +
		orderByPosition + ` DESC LIMIT 1`
	m, err := scanSessionMessage(s.DB.QueryRow(q, sessionID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// SearchSessionMessages 跨会话检索日志正文（排除 excludeSession，通常是当前会话——
// 自己的历史已经在上下文里了，再搜一遍只会刷屏）。
// 只搜投影上的非 system 行：system 是每次注入的模板，搜它等于搜噪声。
func (s *Store) SearchSessionMessages(q, excludeSession string, limit int) ([]SessionMessage, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	// LIKE 的通配符要转义，否则用户搜 "100%" 会被当成模式
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	rows, err := s.DB.Query(
		`SELECT `+sessionMessageCols+` FROM session_messages
		 WHERE shadowed_by = '' AND role != 'system' AND session_id != ?
		   AND content LIKE ? ESCAPE '\'
		 ORDER BY seq DESC LIMIT ?`,
		excludeSession, "%"+esc+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SessionMessage, 0)
	for rows.Next() {
		m, err := scanSessionMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// CountSessionMessages 返回投影上的行数。
func (s *Store) CountSessionMessages(sessionID string) (int, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM session_messages WHERE session_id = ? AND shadowed_by = ''`,
		sessionID).Scan(&n)
	return n, err
}
