package swl

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS entities (
    id                TEXT PRIMARY KEY,
    type              TEXT NOT NULL,
    name              TEXT NOT NULL,
    metadata          TEXT NOT NULL DEFAULT '{}',
    confidence        REAL NOT NULL DEFAULT 1.0,
    content_hash      TEXT,
    knowledge_depth   INTEGER NOT NULL DEFAULT 0,
    extraction_method TEXT NOT NULL DEFAULT 'observed',
    fact_status       TEXT NOT NULL DEFAULT 'unknown',
    created_at        TEXT NOT NULL,
    modified_at       TEXT NOT NULL,
    accessed_at       TEXT NOT NULL,
    access_count      INTEGER NOT NULL DEFAULT 0,
    last_checked      TEXT
);

CREATE TABLE IF NOT EXISTS edges (
    from_id        TEXT NOT NULL,
    rel            TEXT NOT NULL,
    to_id          TEXT NOT NULL,
    source_session TEXT,
    confirmed_at   TEXT NOT NULL,
    PRIMARY KEY (from_id, rel, to_id)
);

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    ended_at   TEXT,
    goal       TEXT,
    summary    TEXT
);

CREATE TABLE IF NOT EXISTS events (
    id         TEXT PRIMARY KEY,
    session_id TEXT,
    tool       TEXT,
    phase      TEXT,
    args_hash  TEXT,
    ts         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS constraints (
    name   TEXT PRIMARY KEY,
    query  TEXT NOT NULL,
    action TEXT NOT NULL DEFAULT 'WARN'
);

CREATE TABLE IF NOT EXISTS query_gaps (
    id          TEXT PRIMARY KEY,
    question    TEXT NOT NULL,
    terms       TEXT NOT NULL,
    count       INTEGER NOT NULL DEFAULT 1,
    first_at    TEXT NOT NULL,
    last_at     TEXT NOT NULL,
    suggestion  TEXT
);

CREATE INDEX IF NOT EXISTS idx_entities_type   ON entities(type);
CREATE INDEX IF NOT EXISTS idx_entities_status ON entities(fact_status);
CREATE INDEX IF NOT EXISTS idx_entities_depth  ON entities(knowledge_depth);
-- idx_entities_hash removed: content_hash is never filtered in queries
CREATE INDEX IF NOT EXISTS idx_edges_from      ON edges(from_id);
CREATE INDEX IF NOT EXISTS idx_edges_to        ON edges(to_id);
CREATE INDEX IF NOT EXISTS idx_edges_rel       ON edges(rel);
CREATE INDEX IF NOT EXISTS idx_edges_session   ON edges(source_session);
CREATE INDEX IF NOT EXISTS idx_events_session  ON events(session_id);
CREATE INDEX IF NOT EXISTS idx_events_ts       ON events(ts);
CREATE INDEX IF NOT EXISTS idx_query_gaps_count ON query_gaps(count);
`

func openDB(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("swl: create db dir: %w", err)
	}
	dsn := "file:" + dbPath +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(on)" +
		"&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("swl: open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := applySchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func applySchema(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("swl: apply schema: %w", err)
	}
	_ = migrateQueryGaps(db)
	_ = migrateEntityConsolidation(db)
	_ = migrateDropLegacySchema(db)
	return nil
}

// migrateDropLegacySchema removes obsolete schema elements from existing databases.
func migrateDropLegacySchema(db *sql.DB) error {
	_, _ = db.Exec(`DROP INDEX IF EXISTS idx_entities_hash`)
	_, _ = db.Exec(`ALTER TABLE sessions DROP COLUMN IF EXISTS workspace_state`)
	return nil
}

// migrateQueryGaps adds missing columns to query_gaps for existing databases.
func migrateQueryGaps(db *sql.DB) error {
	_, _ = db.Exec(`ALTER TABLE query_gaps ADD COLUMN suggestion TEXT`)
	return nil
}

// migrateEntityConsolidation marks deprecated AnchorDocument and SemanticArea
// entities as deleted. These types have been consolidated into File (with
// kind="anchor"/"manifest" metadata) and Directory (with is_semantic_area=true).
// The next ScanWorkspace call will re-emit the correct consolidated entities.
func migrateEntityConsolidation(db *sql.DB) error {
	_, _ = db.Exec(
		`UPDATE entities SET fact_status = 'deleted', modified_at = ?
		 WHERE type IN ('AnchorDocument','SemanticArea') AND fact_status != 'deleted'`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return nil
}

// nowSQLite returns the current UTC time in RFC3339 format used throughout the schema.
func nowSQLite() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// contentHash returns a short hex digest of content, used for change detection.
func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h[:8])
}

// newUUID generates a simple time-based pseudo-UUID sufficient for session IDs.
func newUUID() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// EntityIDFor is the exported alias for entityID — used by packages that create
// entity tuples outside pkg/swl (e.g. pkg/agent hook).
func EntityIDFor(entityType EntityType, name string) string {
	return entityID(entityType, name)
}

// entityID derives a stable deterministic ID from type and name.
func entityID(entityType EntityType, name string) string {
	h := sha256.Sum256([]byte(entityType + ":" + name))
	return fmt.Sprintf("%x", h[:12])
}
