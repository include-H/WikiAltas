package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"wikiatlas/backend/internal/domain"
)

func scanRun(row interface{ Scan(...any) error }) (*domain.Run, error) {
	var (
		r           domain.Run
		planJSON    string
		checkpoint  string
		toolCache   string
		resultJSON  sql.NullString
		errorJSON   sql.NullString
		expiresAt   sql.NullString
		completedAt sql.NullString
	)
	err := row.Scan(&r.ID, &r.Workspace, &r.Intent, &r.Goal, &r.Status,
		&planJSON, &checkpoint, &toolCache, &resultJSON, &errorJSON,
		&r.Model, &r.StartedAt, &r.LastActive, &expiresAt, &completedAt)
	if err != nil {
		return nil, err
	}
	r.Plan = []domain.RunTask{}
	_ = json.Unmarshal([]byte(planJSON), &r.Plan)
	r.Checkpoint = map[string]any{}
	_ = json.Unmarshal([]byte(checkpoint), &r.Checkpoint)
	if ctx, ok := r.Checkpoint["context"].(map[string]any); ok {
		r.Context = ctx
	}
	r.ToolCache = map[string]any{}
	_ = json.Unmarshal([]byte(toolCache), &r.ToolCache)
	if resultJSON.Valid && resultJSON.String != "" {
		r.Result = map[string]any{}
		_ = json.Unmarshal([]byte(resultJSON.String), &r.Result)
	}
	if errorJSON.Valid && errorJSON.String != "" {
		r.Error = map[string]any{}
		_ = json.Unmarshal([]byte(errorJSON.String), &r.Error)
	}
	if expiresAt.Valid {
		r.ExpiresAt = &expiresAt.String
	}
	if completedAt.Valid {
		r.CompletedAt = &completedAt.String
	}
	return &r, nil
}

const runCols = `id, workspace, intent, goal, status, plan_json, checkpoint, tool_cache, result_json, error_json, model, started_at, last_active, expires_at, completed_at`

// CreateRun inserts a new run in running status.
func (s *Store) CreateRun(intent domain.RunIntent, goal, model, workspace string) (*domain.Run, error) {
	if goal == "" {
		return nil, ErrValidation{Message: "goal is required"}
	}
	if workspace == "" {
		workspace = "default"
	}
	id := NewID()
	now := Now()
	_, err := s.DB.Exec(`INSERT INTO runs (id, workspace, intent, goal, status, plan_json, checkpoint, tool_cache, model, started_at, last_active)
		VALUES (?, ?, ?, ?, 'running', '[]', '{}', '{}', ?, ?, ?)`,
		id, workspace, string(intent), goal, model, now, now)
	if err != nil {
		return nil, err
	}
	return s.GetRun(id)
}

// GetRun returns one run.
func (s *Store) GetRun(id string) (*domain.Run, error) {
	r, err := scanRun(s.DB.QueryRow(`SELECT `+runCols+` FROM runs WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound{What: "run"}
	}
	return r, err
}

// ListRuns returns runs optionally filtered by status and workspace (conversation key).
func (s *Store) ListRuns(status, workspace string, limit int) ([]domain.Run, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT ` + runCols + ` FROM runs WHERE 1 = 1`
	args := []any{}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	if workspace != "" {
		query += ` AND workspace = ?`
		args = append(args, workspace)
	}
	query += ` ORDER BY started_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Run, 0)
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// UpdateRunStatus sets status and timestamps.
func (s *Store) UpdateRunStatus(id string, status domain.RunStatus) error {
	now := Now()
	var completed any
	switch status {
	case domain.RunStatusCompleted, domain.RunStatusFailed, domain.RunStatusExpired:
		completed = now
	}
	_, err := s.DB.Exec(`UPDATE runs SET status = ?, last_active = ?, completed_at = COALESCE(?, completed_at) WHERE id = ?`,
		string(status), now, completed, id)
	return err
}

// UpdateRunPlan stores plan_json and bumps last_active.
func (s *Store) UpdateRunPlan(id string, plan []domain.RunTask) error {
	if plan == nil {
		plan = []domain.RunTask{}
	}
	b, _ := json.Marshal(plan)
	now := Now()
	_, err := s.DB.Exec(`UPDATE runs SET plan_json = ?, last_active = ? WHERE id = ?`, string(b), now, id)
	return err
}

// UpdateRunCheckpoint stores checkpoint and tool_cache.
func (s *Store) UpdateRunCheckpoint(id string, checkpoint, toolCache map[string]any) error {
	now := Now()
	cb, _ := json.Marshal(checkpoint)
	tc, _ := json.Marshal(toolCache)
	_, err := s.DB.Exec(`UPDATE runs SET checkpoint = ?, tool_cache = ?, last_active = ? WHERE id = ?`,
		string(cb), string(tc), now, id)
	return err
}

// CompleteRun marks completed with result JSON.
func (s *Store) CompleteRun(id string, result map[string]any) error {
	b, _ := json.Marshal(result)
	now := Now()
	_, err := s.DB.Exec(`UPDATE runs SET status = 'completed', result_json = ?, last_active = ?, completed_at = ? WHERE id = ?`,
		string(b), now, now, id)
	return err
}

// FailRun marks failed with error JSON.
func (s *Store) FailRun(id string, errObj map[string]any) error {
	b, _ := json.Marshal(errObj)
	now := Now()
	_, err := s.DB.Exec(`UPDATE runs SET status = 'failed', error_json = ?, last_active = ?, completed_at = ? WHERE id = ?`,
		string(b), now, now, id)
	return err
}

// InterruptRun marks interrupted (checkpoint kept).
func (s *Store) InterruptRun(id string) error {
	return s.UpdateRunStatus(id, domain.RunStatusInterrupted)
}

// ClearRunCache clears tool_cache and optionally checkpoint (used on expiry).
func (s *Store) ClearRunCache(id string, clearCheckpoint bool) error {
	now := Now()
	if clearCheckpoint {
		_, err := s.DB.Exec(`UPDATE runs SET tool_cache = '{}', checkpoint = '{}', last_active = ? WHERE id = ?`, now, id)
		return err
	}
	_, err := s.DB.Exec(`UPDATE runs SET tool_cache = '{}', last_active = ? WHERE id = ?`, now, id)
	return err
}

// ExpireStaleRuns applies the retention policy:
// running/interrupted > expireDays → expired (clear cache)
// completed > 48h → expired (clear tool_cache)
func (s *Store) ExpireStaleRuns(expireDays int) (int64, error) {
	if expireDays <= 0 {
		expireDays = 7
	}
	now := time.Now().UTC()
	activeCutoff := now.AddDate(0, 0, -expireDays).Format(time.RFC3339Nano)
	completedCutoff := now.Add(-48 * time.Hour).Format(time.RFC3339Nano)

	res1, err := s.DB.Exec(`
		UPDATE runs SET status = 'expired', tool_cache = '{}', checkpoint = '{}'
		WHERE status IN ('running','interrupted') AND last_active < ?`,
		activeCutoff)
	if err != nil {
		return 0, err
	}
	n1, _ := res1.RowsAffected()

	res2, err := s.DB.Exec(`
		UPDATE runs SET status = 'expired', tool_cache = '{}'
		WHERE status IN ('completed','failed') AND completed_at IS NOT NULL AND completed_at < ?`,
		completedCutoff)
	if err != nil {
		return n1, err
	}
	n2, _ := res2.RowsAffected()
	return n1 + n2, nil
}

// DeleteOldRunEvents removes events older than keepDays.
func (s *Store) DeleteOldRunEvents(keepDays int) (int64, error) {
	if keepDays <= 0 {
		keepDays = 90
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -keepDays).Format(time.RFC3339Nano)
	res, err := s.DB.Exec(`DELETE FROM run_events WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteRun 手工删除一条工单：事件跟着一起删（run_events.run_id 有外键）。
func (s *Store) DeleteRun(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM run_events WHERE run_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM runs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound{What: "run"}
	}
	return tx.Commit()
}

// AppendRunEvent inserts the next event for a run and returns it.
func (s *Store) AppendRunEvent(runID, eventType string, payload map[string]any) (*domain.RunEvent, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	b, _ := json.Marshal(payload)
	var maxSeq sql.NullInt64
	if err := s.DB.QueryRow(`SELECT MAX(seq) FROM run_events WHERE run_id = ?`, runID).Scan(&maxSeq); err != nil {
		return nil, err
	}
	seq := int64(1)
	if maxSeq.Valid {
		seq = maxSeq.Int64 + 1
	}
	id := NewID()
	now := Now()
	_, err := s.DB.Exec(`INSERT INTO run_events (id, run_id, seq, type, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, runID, seq, eventType, string(b), now)
	if err != nil {
		return nil, err
	}
	// bump last_active
	_, _ = s.DB.Exec(`UPDATE runs SET last_active = ? WHERE id = ?`, now, runID)
	return &domain.RunEvent{
		ID: id, RunID: runID, Seq: seq, Type: eventType,
		Payload: payload, CreatedAt: now,
	}, nil
}

// ListRunEvents returns events after lastSeq (exclusive), limited.
func (s *Store) ListRunEvents(runID string, afterSeq int64, limit int) ([]domain.RunEvent, error) {
	return s.listRunEvents(runID, afterSeq, limit, false)
}

// ListRunEventsPlain 同上，但跳过流式增量（narrative.delta / tool.delta）。
// 用途：面板/工单页重建历史时只要"权威事件"，否则长工单会被增量挤满 200 条窗口，
// 前面的章节叙事全被截掉（引入流式之后真实踩到）。
func (s *Store) ListRunEventsPlain(runID string, afterSeq int64, limit int) ([]domain.RunEvent, error) {
	return s.listRunEvents(runID, afterSeq, limit, true)
}

func (s *Store) listRunEvents(runID string, afterSeq int64, limit int, plain bool) ([]domain.RunEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	query := `SELECT id, run_id, seq, type, payload, created_at FROM run_events
		WHERE run_id = ? AND seq > ?`
	if plain {
		query += ` AND type NOT IN ('narrative.delta','tool.delta')`
	}
	query += ` ORDER BY seq ASC LIMIT ?`
	rows, err := s.DB.Query(query, runID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RunEvent, 0)
	for rows.Next() {
		var (
			e       domain.RunEvent
			payload string
		)
		if err := rows.Scan(&e.ID, &e.RunID, &e.Seq, &e.Type, &payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Payload = map[string]any{}
		_ = json.Unmarshal([]byte(payload), &e.Payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountRunEvents returns total events for a run.
func (s *Store) CountRunEvents(runID string) (int64, error) {
	var n int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM run_events WHERE run_id = ?`, runID).Scan(&n)
	return n, err
}

// SetRunModel updates the model field.
func (s *Store) SetRunModel(id, model string) error {
	_, err := s.DB.Exec(`UPDATE runs SET model = ? WHERE id = ?`, model, id)
	return err
}

var _ = fmt.Sprintf
