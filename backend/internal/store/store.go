// Package store provides SQLite persistence for WikiAltas v2.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database.
type Store struct {
	DB *sql.DB
}

// Open opens (creating if needed) the SQLite database and runs migrations.
func Open(dbPath string) (*Store, error) {
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	// WAL + busy_timeout for concurrent readers during SSE.
	dsn := "file:" + dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// modernc sqlite is single-writer; limit connections.
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenMemory opens an in-memory database (tests).
func OpenMemory() (*Store, error) {
	// unique name per open so tests don't share state
	name := "file:memdb_" + strings.ReplaceAll(uuid.NewString(), "-", "") + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", name)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.DB.Close()
}

// Now returns the current time as ISO8601 UTC string used throughout the store.
func Now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// NewID returns a new UUIDv7 string.
func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// fallback extremely unlikely
		return uuid.NewString()
	}
	return id.String()
}

// tableHasColumn 检查表里是否有某一列（迁移用）。
func (s *Store) tableHasColumn(table, column string) (bool, error) {
	rows, err := s.DB.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Migrate creates tables and FTS if missing.
func (s *Store) Migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version    INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS works (
  id            TEXT PRIMARY KEY,
  parent_id     TEXT REFERENCES works(id),
  kind          TEXT NOT NULL,
  medium        TEXT,
  title         TEXT NOT NULL,
  aliases_json  TEXT NOT NULL DEFAULT '[]',
  content_md    TEXT,
  content_ver   INTEGER NOT NULL DEFAULT 0,
  status        TEXT NOT NULL DEFAULT 'stub',
  visibility    TEXT NOT NULL DEFAULT 'private',
  sort_order    INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS docs (
  id            TEXT PRIMARY KEY,
  folder_of     TEXT NOT NULL REFERENCES works(id),
  title         TEXT NOT NULL,
  content_md    TEXT NOT NULL DEFAULT '',
  content_ver   INTEGER NOT NULL DEFAULT 0,
  links_json    TEXT NOT NULL DEFAULT '[]',
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS revisions (
  id            TEXT PRIMARY KEY,
  target_type   TEXT NOT NULL,
  target_id     TEXT NOT NULL,
  version       INTEGER NOT NULL,
  content_md    TEXT NOT NULL,
  author        TEXT NOT NULL,
  run_id        TEXT,
  summary       TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  UNIQUE(target_type, target_id, version)
);

CREATE TABLE IF NOT EXISTS relations (
  id            TEXT PRIMARY KEY,
  from_id       TEXT NOT NULL REFERENCES works(id),
  to_id         TEXT NOT NULL REFERENCES works(id),
  type          TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  UNIQUE(from_id, to_id, type)
);

CREATE TABLE IF NOT EXISTS library_links (
  id            TEXT PRIMARY KEY,
  work_id       TEXT NOT NULL REFERENCES works(id),
  source        TEXT NOT NULL,
  external_id   TEXT NOT NULL,
  url           TEXT,
  title_hint    TEXT,
  created_at    TEXT NOT NULL,
  UNIQUE(source, external_id)
);

CREATE TABLE IF NOT EXISTS runs (
  id            TEXT PRIMARY KEY,
  workspace     TEXT NOT NULL DEFAULT 'default',
  intent        TEXT NOT NULL,
  goal          TEXT NOT NULL,
  status        TEXT NOT NULL,
  checkpoint    TEXT NOT NULL DEFAULT '{}',
  tool_cache    TEXT NOT NULL DEFAULT '{}',
  result_json   TEXT,
  error_json    TEXT,
  model         TEXT NOT NULL DEFAULT '',
  started_at    TEXT NOT NULL,
  last_active   TEXT NOT NULL,
  expires_at    TEXT,
  completed_at  TEXT
);

CREATE TABLE IF NOT EXISTS run_events (
  id            TEXT PRIMARY KEY,
  run_id        TEXT NOT NULL REFERENCES runs(id),
  seq           INTEGER NOT NULL,
  type          TEXT NOT NULL,
  payload       TEXT NOT NULL DEFAULT '{}',
  created_at    TEXT NOT NULL,
  UNIQUE(run_id, seq)
);

CREATE TABLE IF NOT EXISTS sessions (
  id         TEXT PRIMARY KEY,
  target     TEXT NOT NULL DEFAULT '',
  title      TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- 会话消息日志：一条会话 = 一条追加式日志，模型的消息历史是它的**投影**，不单独存。
-- 这是 dsh「模型可见 ⟺ 有日志」的落地：新工单从这条日志派生上下文，而不是重建。
-- tool_calls_json 存原始 JSON（store 不 import llm，按不透明字符串处理）。
-- shadowed_by 非空 = 该行被一次替换（压缩）遮蔽：仍在日志里，但不在投影上。
-- replaces_seq 是"替换行"的落位：0 = 普通追加（按 seq 排）；
--   >0 = 本行替换掉从 replaces_seq 起的区间，投影时**占那个位置**。
--   （dsh 的 surface replace：新节点出现在被替换节点的位置上，而不是日志末尾。）
-- header_hash / header_reason 记该轮请求的"非历史状态"（工具面 + 调用配置）指纹，
-- 变了就是一次带原因的请求重塑（对应 dsh 的 request/header）。
CREATE TABLE IF NOT EXISTS session_messages (
  id              TEXT PRIMARY KEY,
  session_id      TEXT NOT NULL,
  seq             INTEGER NOT NULL,
  replaces_seq    INTEGER NOT NULL DEFAULT 0,
  run_id          TEXT NOT NULL DEFAULT '',
  turn            INTEGER NOT NULL DEFAULT 1,
  role            TEXT NOT NULL,
  content         TEXT NOT NULL DEFAULT '',
  tool_calls_json TEXT NOT NULL DEFAULT '',
  tool_call_id    TEXT NOT NULL DEFAULT '',
  tool_name       TEXT NOT NULL DEFAULT '',
  header_hash     TEXT NOT NULL DEFAULT '',
  header_reason   TEXT NOT NULL DEFAULT '',
  shadowed_by     TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  UNIQUE(session_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_works_parent ON works(parent_id);
CREATE INDEX IF NOT EXISTS idx_docs_folder ON docs(folder_of);
CREATE INDEX IF NOT EXISTS idx_revisions_target ON revisions(target_type, target_id, version);
CREATE INDEX IF NOT EXISTS idx_relations_from ON relations(from_id);
CREATE INDEX IF NOT EXISTS idx_relations_to ON relations(to_id);
CREATE INDEX IF NOT EXISTS idx_library_work ON library_links(work_id);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);
CREATE INDEX IF NOT EXISTS idx_run_events_run ON run_events(run_id, seq);
CREATE INDEX IF NOT EXISTS idx_sessions_target ON sessions(target, updated_at DESC);
`
	if _, err := s.DB.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	// runs.plan_json 是已移除的"计划任务"遗留列（2026-09-11 连根拔除）——
	// 老库里有就删掉，新库不再建。
	if hasColumn, err := s.tableHasColumn("runs", "plan_json"); err != nil {
		return fmt.Errorf("check runs.plan_json: %w", err)
	} else if hasColumn {
		if _, err := s.DB.Exec(`ALTER TABLE runs DROP COLUMN plan_json`); err != nil {
			return fmt.Errorf("drop runs.plan_json: %w", err)
		}
	}
	// 会话表回填（幂等）：老库里的工单按 workspace 归组成会话，
	// 标题取该会话最早一单的目标——历史的"一问一答"升级成可召回的会话。
	if _, err := s.DB.Exec(`
		INSERT OR IGNORE INTO sessions (id, target, title, created_at, updated_at)
		SELECT r.workspace,
		       CASE WHEN instr(r.workspace, '~') > 0
		            THEN substr(r.workspace, 1, instr(r.workspace, '~') - 1)
		            ELSE r.workspace END,
		       COALESCE((SELECT r2.goal FROM runs r2 WHERE r2.workspace = r.workspace
		                 ORDER BY r2.started_at ASC LIMIT 1), ''),
		       MIN(r.started_at), MAX(r.last_active)
		FROM runs r
		WHERE r.workspace != ''
		GROUP BY r.workspace`); err != nil {
		return fmt.Errorf("backfill sessions: %w", err)
	}
	// 简易用户系统的隐私列（2026-09-10，见 DESIGN_V2 §3.6）
	if err := s.migrateVisibility(); err != nil {
		return fmt.Errorf("migrate visibility: %w", err)
	}
	// 出厂访问密码：保证"有库就能进管理态"，否则第一次没人能登录（DESIGN_V2 §3.6）
	if err := s.ensureDefaultAdminPassword(); err != nil {
		return fmt.Errorf("default admin password: %w", err)
	}

	// FTS5 virtual tables. trigram tokenizer supports CJK without external segmenters.
	// External-content pattern keeps a single source of truth in works/docs.
	//
	// 迁移：早期版本只索引 title/content，别名（aliases_json）没进索引，
	// 导致 search_works 声称"按别名检索"却搜不到（如 FF7R）。这里检测表定义，
	// 缺 aliases 列就重建 FTS 表与触发器并 rebuild（FTS 是派生数据，重建安全）。
	var ftsSQL string
	if err := s.DB.QueryRow(
		`SELECT COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name = 'works_fts'`,
	).Scan(&ftsSQL); err != nil {
		ftsSQL = ""
	}
	ftsExisted := ftsSQL != ""
	// 早期版本把 FTS 列名写成 content / aliases，而外部内容表要求列名与 works 一致
	// （works.content_md / works.aliases_json），导致 FTS 查询报错并静默回退到 LIKE。
	migrated := ftsExisted && !strings.Contains(ftsSQL, "aliases_json")
	if migrated {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS works_fts`,
			`DROP TABLE IF EXISTS docs_fts`,
			`DROP TRIGGER IF EXISTS works_ai`,
			`DROP TRIGGER IF EXISTS works_ad`,
			`DROP TRIGGER IF EXISTS works_au`,
			`DROP TRIGGER IF EXISTS docs_ai`,
			`DROP TRIGGER IF EXISTS docs_ad`,
			`DROP TRIGGER IF EXISTS docs_au`,
		} {
			if _, err := s.DB.Exec(stmt); err != nil {
				return fmt.Errorf("migrate fts: %w", err)
			}
		}
	}
	fts := `
CREATE VIRTUAL TABLE IF NOT EXISTS works_fts USING fts5(
  title, content_md, aliases_json, content='works', content_rowid='rowid', tokenize='trigram'
);

CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
  title, content_md, content='docs', content_rowid='rowid', tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS works_ai AFTER INSERT ON works BEGIN
  INSERT INTO works_fts(rowid, title, content_md, aliases_json)
    VALUES (new.rowid, new.title, COALESCE(new.content_md,''), COALESCE(new.aliases_json,'[]'));
END;
CREATE TRIGGER IF NOT EXISTS works_ad AFTER DELETE ON works BEGIN
  INSERT INTO works_fts(works_fts, rowid, title, content_md, aliases_json)
    VALUES ('delete', old.rowid, old.title, COALESCE(old.content_md,''), COALESCE(old.aliases_json,'[]'));
END;
CREATE TRIGGER IF NOT EXISTS works_au AFTER UPDATE ON works BEGIN
  INSERT INTO works_fts(works_fts, rowid, title, content_md, aliases_json)
    VALUES ('delete', old.rowid, old.title, COALESCE(old.content_md,''), COALESCE(old.aliases_json,'[]'));
  INSERT INTO works_fts(rowid, title, content_md, aliases_json)
    VALUES (new.rowid, new.title, COALESCE(new.content_md,''), COALESCE(new.aliases_json,'[]'));
END;

CREATE TRIGGER IF NOT EXISTS docs_ai AFTER INSERT ON docs BEGIN
  INSERT INTO docs_fts(rowid, title, content_md) VALUES (new.rowid, new.title, new.content_md);
END;
CREATE TRIGGER IF NOT EXISTS docs_ad AFTER DELETE ON docs BEGIN
  INSERT INTO docs_fts(docs_fts, rowid, title, content_md) VALUES ('delete', old.rowid, old.title, old.content_md);
END;
CREATE TRIGGER IF NOT EXISTS docs_au AFTER UPDATE ON docs BEGIN
  INSERT INTO docs_fts(docs_fts, rowid, title, content_md) VALUES ('delete', old.rowid, old.title, old.content_md);
  INSERT INTO docs_fts(rowid, title, content_md) VALUES (new.rowid, new.title, new.content_md);
END;
`
	if _, err := s.DB.Exec(fts); err != nil {
		// FTS5 trigram may be unavailable on very old SQLite; fall back to unicode61.
		ftsFallback := strings.ReplaceAll(fts, ", tokenize='trigram'", "")
		if _, err2 := s.DB.Exec(ftsFallback); err2 != nil {
			return fmt.Errorf("apply fts: %v (fallback: %w)", err, err2)
		}
	}
	// 首次建表或刚做过 aliases 迁移时灌一次数据。
	// 注意不能用 FTS5 的 'rebuild'（外部内容表要求列名与 works 相同，我们用的是 content_md/aliases_json）。
	if !ftsExisted || migrated {
		if _, err := s.DB.Exec(`
			INSERT INTO works_fts(rowid, title, content_md, aliases_json)
			SELECT rowid, title, COALESCE(content_md, ''), COALESCE(aliases_json, '[]') FROM works`); err != nil {
			return fmt.Errorf("index works_fts: %w", err)
		}
		if _, err := s.DB.Exec(`
			INSERT INTO docs_fts(rowid, title, content_md)
			SELECT rowid, title, COALESCE(content_md, '') FROM docs`); err != nil {
			return fmt.Errorf("index docs_fts: %w", err)
		}
	}

	// record migration
	_, _ = s.DB.Exec(`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, ?)`, Now())
	return nil
}

// ErrNotFound is returned when a row is missing.
type ErrNotFound struct{ What string }

func (e ErrNotFound) Error() string { return e.What + " not found" }

// ErrConflict is returned on version/content conflicts.
type ErrConflict struct{ Message string }

func (e ErrConflict) Error() string { return e.Message }

// ErrValidation is returned on bad input.
type ErrValidation struct{ Message string }

func (e ErrValidation) Error() string { return e.Message }
