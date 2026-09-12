package store

import (
	"database/sql"
	"strings"

	"wikiatlas/backend/internal/domain"
)

// targetOf 从会话 id 推出所属页面：会话 id 形如 "work:<id>"（默认会话）或
// "work:<id>~<后缀>"（同页面的后续会话）。
func targetOf(sessionID string) string {
	if i := strings.Index(sessionID, "~"); i > 0 {
		return sessionID[:i]
	}
	return sessionID
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// EnsureSession 保证会话行存在（建工单时调用）；标题为空时用首单目标回填。
func (s *Store) EnsureSession(id, goal string) error {
	if id == "" {
		return nil
	}
	now := Now()
	if _, err := s.DB.Exec(`INSERT OR IGNORE INTO sessions (id, target, title, created_at, updated_at)
		VALUES (?, ?, '', ?, ?)`, id, targetOf(id), now, now); err != nil {
		return err
	}
	if goal != "" {
		if _, err := s.DB.Exec(`UPDATE sessions SET title = ? WHERE id = ? AND title = ''`,
			truncateRunes(goal, 60), id); err != nil {
			return err
		}
	}
	_, err := s.DB.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, now, id)
	return err
}

const sessionSelect = `
SELECT s.id, s.target, s.title, s.created_at, s.updated_at,
  COALESCE((SELECT r.goal FROM runs r WHERE r.workspace = s.id
            ORDER BY r.started_at DESC LIMIT 1), ''),
  (SELECT COUNT(*) FROM runs WHERE workspace = s.id),
  COALESCE(
    (SELECT 'running' FROM runs WHERE workspace = s.id AND status = 'running' LIMIT 1),
    (SELECT status FROM runs WHERE workspace = s.id ORDER BY started_at DESC LIMIT 1),
    ''
  )
FROM sessions s`

func scanSession(row interface{ Scan(...any) error }) (*domain.Session, error) {
	var sess domain.Session
	err := row.Scan(&sess.ID, &sess.Target, &sess.Title, &sess.CreatedAt, &sess.UpdatedAt,
		&sess.LastGoal, &sess.RunCount, &sess.Status)
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// CreateSession 新建一段空白会话。页面下的第一个会话直接用 target 当 id
// （与建工单时的默认会话同一把钥匙，老数据天然归位）；之后的会话追加 "~<uuid>"。
func (s *Store) CreateSession(target, title string) (*domain.Session, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, ErrValidation{Message: "target is required"}
	}
	id := target
	var n int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE target = ?`, target).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		id = target + "~" + NewID()
	}
	now := Now()
	if _, err := s.DB.Exec(`INSERT INTO sessions (id, target, title, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, id, target, strings.TrimSpace(title), now, now); err != nil {
		return nil, err
	}
	return s.GetSession(id)
}

// GetSession returns one session with its run count and latest status.
func (s *Store) GetSession(id string) (*domain.Session, error) {
	sess, err := scanSession(s.DB.QueryRow(sessionSelect+` WHERE s.id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound{What: "session"}
	}
	return sess, err
}

// ListSessions 列出会话，最近活跃在前。
// target 为空 = 列出**全部**会话（这就是"工单列表"要的东西：可继续的对话，
// 而不是每次执行的一条记录）。
func (s *Store) ListSessions(target string) ([]domain.Session, error) {
	q := sessionSelect + ` ORDER BY s.updated_at DESC LIMIT 200`
	args := []any{}
	if target != "" {
		q = sessionSelect + ` WHERE s.target = ? ORDER BY s.updated_at DESC LIMIT 100`
		args = append(args, target)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Session, 0)
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

// RenameSession sets a session title.
func (s *Store) RenameSession(id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return ErrValidation{Message: "title is required"}
	}
	res, err := s.DB.Exec(`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`,
		truncateRunes(title, 120), Now(), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound{What: "session"}
	}
	return nil
}

// DeleteSession 删除会话及其全部工单（事件跟着删）。调用方应先停掉正在跑的工单。
func (s *Store) DeleteSession(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM run_events WHERE run_id IN (SELECT id FROM runs WHERE workspace = ?)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM session_messages WHERE session_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM runs WHERE workspace = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound{What: "session"}
	}
	return tx.Commit()
}

// touchSessionOfRun 工单有动态时刷新所属会话的排序时间（best-effort）。
func (s *Store) touchSessionOfRun(runID string) error {
	_, err := s.DB.Exec(`UPDATE sessions SET updated_at = ?
		WHERE id = (SELECT workspace FROM runs WHERE id = ?)`, Now(), runID)
	return err
}
