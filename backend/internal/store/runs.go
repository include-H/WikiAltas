package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"wikiatlas/backend/internal/domain"
)

func scanRun(row interface{ Scan(...any) error }) (*domain.Run, error) {
	var (
		r           domain.Run
		checkpoint  string
		toolCache   string
		resultJSON  sql.NullString
		errorJSON   sql.NullString
		expiresAt   sql.NullString
		completedAt sql.NullString
	)
	err := row.Scan(&r.ID, &r.Workspace, &r.Intent, &r.Goal, &r.Status,
		&checkpoint, &toolCache, &resultJSON, &errorJSON,
		&r.Model, &r.StartedAt, &r.LastActive, &expiresAt, &completedAt)
	if err != nil {
		return nil, err
	}
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

const runCols = `id, workspace, intent, goal, status, checkpoint, tool_cache, result_json, error_json, model, started_at, last_active, expires_at, completed_at`

// responseEventPrefix 是 Responses 流事件的类型前缀（见 internal/run/responses.go）。
// 只有它们占用 sequence_number。
const responseEventPrefix = "response."

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
	_, err := s.DB.Exec(`INSERT INTO runs (id, workspace, intent, goal, status, checkpoint, tool_cache, model, started_at, last_active)
		VALUES (?, ?, ?, ?, 'running', '{}', '{}', ?, ?, ?)`,
		id, workspace, string(intent), goal, model, now, now)
	if err != nil {
		return nil, err
	}
	// 会话行随第一单建出（标题用首单目标回填），之后的调用只刷新排序时间。
	if err := s.EnsureSession(workspace, goal); err != nil {
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
	if _, err := s.DB.Exec(`UPDATE runs SET status = 'completed', result_json = ?, last_active = ?, completed_at = ? WHERE id = ?`,
		string(b), now, now, id); err != nil {
		return err
	}
	return s.touchSessionOfRun(id)
}

// FailRun marks failed with error JSON.
func (s *Store) FailRun(id string, errObj map[string]any) error {
	b, _ := json.Marshal(errObj)
	now := Now()
	if _, err := s.DB.Exec(`UPDATE runs SET status = 'failed', error_json = ?, last_active = ?, completed_at = ? WHERE id = ?`,
		string(b), now, now, id); err != nil {
		return err
	}
	return s.touchSessionOfRun(id)
}

// InterruptRun marks interrupted (checkpoint kept).
func (s *Store) InterruptRun(id string) error {
	if err := s.UpdateRunStatus(id, domain.RunStatusInterrupted); err != nil {
		return err
	}
	return s.touchSessionOfRun(id)
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
//
// 事件信封的两个字段在这里一次写死，不让调用方各写各的：
//
//   - `type`：事件类型也进 payload。Responses 规范里每个流事件的 type 既是
//     SSE 的 event 名、也是 data 里的判别式（前端归约器按它分派）。
//   - `sequence_number`：**Responses 流事件专属**，从 0 起、**连续 +1**。
//     规范要求每个流事件带它，前端的归约器更是按它增量推进（只在连续序号上
//     走，跳号要攒到 gap > 10 才敢跳）——所以它必须
//     (a) 与落库顺序同源，不能由执行器自己数（重启/续跑会重号）；
//     (b) **不**被 wikiatlas.* 旁路事件占用：
//     那是"同流不同类"的另一条通道，它一吃号，归约器看到的序号就到处
//     是洞，一路丢块到 gap > 10 才恢复。旁路事件只带 type、不带号。
func (s *Store) AppendRunEvent(runID, eventType string, payload map[string]any) (*domain.RunEvent, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	// 取号与写入必须同一个事务：seq 与 sequence_number 都是**读-算-写**，而
	// 取消/续跑的生命周期播报走 HTTP goroutine，与执行器 goroutine 并发。
	// 两条语句分开做时它们会交错、算出同一个号——(run_id, seq) 有唯一约束，
	// 后到的那条直接写不进去，事件被**丢掉**（调用方多半不会为此炸出来）：
	// 轻则少一条播报，重则少一个 response.* 流事件，前端序号出现空洞，
	// 归约器要攒到 gap > 10 才肯跳过。
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var maxSeq sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(seq) FROM run_events WHERE run_id = ?`, runID).Scan(&maxSeq); err != nil {
		return nil, err
	}
	seq := int64(1)
	if maxSeq.Valid {
		seq = maxSeq.Int64 + 1
	}
	payload["type"] = eventType
	if strings.HasPrefix(eventType, responseEventPrefix) {
		// 只数 Responses 流事件，数出来的就是它们在本次响应里的第几个。
		var n int
		if err := tx.QueryRow(
			`SELECT COUNT(*) FROM run_events WHERE run_id = ? AND type LIKE ?`,
			runID, responseEventPrefix+"%").Scan(&n); err != nil {
			return nil, err
		}
		payload["sequence_number"] = n
	}
	b, _ := json.Marshal(payload)
	id := NewID()
	now := Now()
	if _, err := tx.Exec(`INSERT INTO run_events (id, run_id, seq, type, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, runID, seq, eventType, string(b), now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// bump last_active —— 放在事务**外**：连接只有一条（SetMaxOpenConns(1)），
	// 事务里再取连接会自锁。
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

// ListRunEventsPlain 同上，但跳过流式增量。
// 用途：面板/工单页重建历史时只要"权威事件"，否则长工单会被增量挤满窗口，
// 前面的章节叙事全被截掉（引入流式之后真实踩到）。
func (s *Store) ListRunEventsPlain(runID string, afterSeq int64, limit int) ([]domain.RunEvent, error) {
	return s.listRunEvents(runID, afterSeq, limit, true)
}

// deltaTypePattern 是"流式增量"的**命名约定**：一律以 .delta 结尾。
//
// 这里按约定过滤，而不是维护一张硬编码清单——清单会漂移：当初列的是当时那两三种，
// 后来新增的增量没人补，结果一个工单的历史回放里 530 条有 478 条是半句话碎片。
// 约定式过滤让以后任何 *.delta 自动是"只走实时流、不进历史"。
//
// Responses 的三种增量（output_text / reasoning_summary_text /
// function_call_arguments）都符合这条约定；而每个增量的**归约结果**都有对应的
// 权威事件（.done / output_item.done），所以去掉增量不丢信息，只丢"打字过程"。
const deltaTypePattern = `%.delta`

func (s *Store) listRunEvents(runID string, afterSeq int64, limit int, plain bool) ([]domain.RunEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	query := `SELECT id, run_id, seq, type, payload, created_at FROM run_events
		WHERE run_id = ? AND seq > ?`
	if plain {
		query += ` AND type NOT LIKE '` + deltaTypePattern + `'`
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

var _ = fmt.Sprintf
