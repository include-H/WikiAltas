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
  slug          TEXT NOT NULL UNIQUE,
  aliases_json  TEXT NOT NULL DEFAULT '[]',
  content_md    TEXT,
  content_ver   INTEGER NOT NULL DEFAULT 0,
  status        TEXT NOT NULL DEFAULT 'stub',
  sort_order    INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS docs (
  id            TEXT PRIMARY KEY,
  folder_of     TEXT NOT NULL REFERENCES works(id),
  title         TEXT NOT NULL,
  slug          TEXT NOT NULL UNIQUE,
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
  plan_json     TEXT NOT NULL DEFAULT '[]',
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

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_works_parent ON works(parent_id);
CREATE INDEX IF NOT EXISTS idx_docs_folder ON docs(folder_of);
CREATE INDEX IF NOT EXISTS idx_revisions_target ON revisions(target_type, target_id, version);
CREATE INDEX IF NOT EXISTS idx_relations_from ON relations(from_id);
CREATE INDEX IF NOT EXISTS idx_relations_to ON relations(to_id);
CREATE INDEX IF NOT EXISTS idx_library_work ON library_links(work_id);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);
CREATE INDEX IF NOT EXISTS idx_run_events_run ON run_events(run_id, seq);
`
	if _, err := s.DB.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}

	// FTS5 virtual tables. trigram tokenizer supports CJK without external segmenters.
	// External-content pattern keeps a single source of truth in works/docs.
	fts := `
CREATE VIRTUAL TABLE IF NOT EXISTS works_fts USING fts5(
  title, content, content='works', content_rowid='rowid', tokenize='trigram'
);

CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
  title, content, content='docs', content_rowid='rowid', tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS works_ai AFTER INSERT ON works BEGIN
  INSERT INTO works_fts(rowid, title, content) VALUES (new.rowid, new.title, COALESCE(new.content_md,''));
END;
CREATE TRIGGER IF NOT EXISTS works_ad AFTER DELETE ON works BEGIN
  INSERT INTO works_fts(works_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, COALESCE(old.content_md,''));
END;
CREATE TRIGGER IF NOT EXISTS works_au AFTER UPDATE ON works BEGIN
  INSERT INTO works_fts(works_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, COALESCE(old.content_md,''));
  INSERT INTO works_fts(rowid, title, content) VALUES (new.rowid, new.title, COALESCE(new.content_md,''));
END;

CREATE TRIGGER IF NOT EXISTS docs_ai AFTER INSERT ON docs BEGIN
  INSERT INTO docs_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content_md);
END;
CREATE TRIGGER IF NOT EXISTS docs_ad AFTER DELETE ON docs BEGIN
  INSERT INTO docs_fts(docs_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, old.content_md);
END;
CREATE TRIGGER IF NOT EXISTS docs_au AFTER UPDATE ON docs BEGIN
  INSERT INTO docs_fts(docs_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, old.content_md);
  INSERT INTO docs_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content_md);
END;
`
	if _, err := s.DB.Exec(fts); err != nil {
		// FTS5 trigram may be unavailable on very old SQLite; fall back to unicode61.
		ftsFallback := strings.ReplaceAll(fts, ", tokenize='trigram'", "")
		if _, err2 := s.DB.Exec(ftsFallback); err2 != nil {
			return fmt.Errorf("apply fts: %v (fallback: %w)", err, err2)
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

// slugify generates a URL-safe slug; falls back to id-suffix if empty.
func slugify(s string) string {
	var b strings.Builder
	s = strings.ToLower(strings.TrimSpace(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		default:
			// keep CJK and other letters as-is for readable slugs
			if r > 127 {
				b.WriteRune(r)
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if out == "" {
		out = "item-" + NewID()[:8]
	}
	return out
}
