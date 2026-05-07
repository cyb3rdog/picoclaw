-- SWL SYNTH-CORE Extensions Schema
-- Adds tables for entropy monitoring, conflict tracking, and goal management

-- Goals table: tracks agent objectives
CREATE TABLE IF NOT EXISTS goals (
    id TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    constraints TEXT,  -- JSON array of constraint strings
    sub_goals TEXT,   -- JSON array of sub-goal IDs
    progress REAL DEFAULT 0.0,
    status TEXT DEFAULT 'active',  -- active, completed, blocked
    created_at TEXT NOT NULL,
    modified_at TEXT,
    UNIQUE(id)
);

CREATE INDEX IF NOT EXISTS idx_goals_status ON goals(status);
CREATE INDEX IF NOT EXISTS idx_goals_progress ON goals(progress);

-- Temporal edges: for Chronos vector ordering
CREATE TABLE IF NOT EXISTS temporal_edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    relation TEXT NOT NULL,  -- precedes, follows, concurrent
    session_id TEXT,
    timestamp TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_temporal_from ON temporal_edges(from_id);
CREATE INDEX IF NOT EXISTS idx_temporal_to ON temporal_edges(to_id);
CREATE INDEX IF NOT EXISTS idx_temporal_session ON temporal_edges(session_id);

-- Conflicts table: tracks saddle points (contradictions)
CREATE TABLE IF NOT EXISTS conflicts (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,  -- mutual_exclusion, temporal_ordering, redundant_extract, confidence_mismatch
    entity_a TEXT NOT NULL,
    entity_b TEXT NOT NULL,
    status TEXT DEFAULT 'pending',  -- pending, resolved, dismissed
    resolution TEXT,
    reasoning TEXT,
    detected_at TEXT NOT NULL,
    resolved_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_conflicts_status ON conflicts(status);
CREATE INDEX IF NOT EXISTS idx_conflicts_type ON conflicts(type);

-- Entropy budget tracking: per-session inference cost
CREATE TABLE IF NOT EXISTS entropy_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    entropy_spent INTEGER DEFAULT 0,
    max_depth INTEGER DEFAULT 5,
    result_count INTEGER DEFAULT 0,
    termination TEXT,  -- depth_limit, iteration_cap, entropy_exhausted, stability_reached
    timestamp TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_entropy_session ON entropy_log(session_id);

-- Cross-domain grafts: functional mappings
CREATE TABLE IF NOT EXISTS grafts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    domain_a TEXT NOT NULL,
    domain_b TEXT NOT NULL,
    invariant TEXT NOT NULL,
    description TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_grafts_domains ON grafts(domain_a, domain_b);
