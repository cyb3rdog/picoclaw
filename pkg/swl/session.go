package swl

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
)

// EnsureSession returns the SWL session UUID for the given picoclaw session key,
// creating a new session row if none exists yet.
func (m *Manager) EnsureSession(sessionKey string) string {
	if sessionKey == "" {
		return ""
	}
	m.sessionsMu.RLock()
	id, ok := m.activeSessions[sessionKey]
	m.sessionsMu.RUnlock()
	if ok {
		return id
	}

	id = newUUID()
	m.mu.Lock()
	_, _ = m.db.Exec(
		`INSERT OR IGNORE INTO sessions (id, started_at, workspace_state) VALUES (?, ?, '{}')`,
		id, nowSQLite(),
	)
	m.mu.Unlock()

	m.sessionsMu.Lock()
	// Double-check in case another goroutine raced us.
	if existing, raced := m.activeSessions[sessionKey]; raced {
		m.sessionsMu.Unlock()
		return existing
	}
	m.activeSessions[sessionKey] = id
	m.sessionsMu.Unlock()

	// Session entity for graph visibility
	_ = m.writer.upsertEntity(EntityTuple{
		ID:               id,
		Type:             KnownTypeSession,
		Name:             sessionKey,
		Confidence:       1.0,
		ExtractionMethod: MethodObserved,
		KnowledgeDepth:   1,
	})

	return id
}

// SetSessionGoal records the user's stated intent for a session.
func (m *Manager) SetSessionGoal(sessionID, goal string) {
	if sessionID == "" || goal == "" {
		return
	}
	m.mu.Lock()
	m.db.Exec( //nolint:errcheck
		"UPDATE sessions SET goal = ? WHERE id = ?", goal, sessionID,
	)
	m.mu.Unlock()
}

// SessionSync checks all verified File entities for external changes.
// Runs at most once per session UUID.
func (m *Manager) SessionSync(sessionID string) {
	if sessionID == "" {
		return
	}
	m.syncedMu.Lock()
	if m.syncedSessions[sessionID] {
		m.syncedMu.Unlock()
		return
	}
	m.syncedSessions[sessionID] = true
	m.syncedMu.Unlock()

	rows, err := m.db.Query(
		`SELECT id, name FROM entities WHERE type = ? AND fact_status = ?`,
		KnownTypeFile, FactVerified,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	type fileRow struct{ id, name string }
	var files []fileRow
	for rows.Next() {
		var r fileRow
		if rows.Scan(&r.id, &r.name) == nil {
			files = append(files, r)
		}
	}

	for _, f := range files {
		info, err := os.Stat(f.name)
		if err != nil {
			if os.IsNotExist(err) {
				_ = m.SetFactStatus(f.id, FactDeleted)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		// Re-read mtime from DB and compare.
		var modifiedAt string
		_ = m.db.QueryRow(
			"SELECT modified_at FROM entities WHERE id = ?", f.id,
		).Scan(&modifiedAt)

		dbMtime := parseRFC3339(modifiedAt)
		if !dbMtime.IsZero() && info.ModTime().After(dbMtime) {
			_ = m.SetFactStatus(f.id, FactStale)
		}
	}
}

// endAllSessions closes all active session rows with a summary.
func (m *Manager) endAllSessions() {
	m.sessionsMu.Lock()
	keys := make([]string, 0, len(m.activeSessions))
	ids := make([]string, 0, len(m.activeSessions))
	for k, id := range m.activeSessions {
		keys = append(keys, k)
		ids = append(ids, id)
	}
	m.sessionsMu.Unlock()

	now := nowSQLite()
	summary := m.autoSummary()

	m.mu.Lock()
	for _, id := range ids {
		m.db.Exec( //nolint:errcheck
			"UPDATE sessions SET ended_at = ?, summary = ? WHERE id = ? AND ended_at IS NULL",
			now, summary, id,
		)
	}
	m.mu.Unlock()

	_ = keys // suppress unused warning; key list used for logging if needed
}

// autoSummary produces a short stats string for the session summary field.
func (m *Manager) autoSummary() string {
	var entityCount, edgeCount int
	m.db.QueryRow("SELECT COUNT(*) FROM entities").Scan(&entityCount) //nolint:errcheck
	m.db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&edgeCount)      //nolint:errcheck
	return fmt.Sprintf("entities=%d edges=%d", entityCount, edgeCount)
}

// SessionResume returns a brief "bring me up to speed" digest.
func (m *Manager) SessionResume(sessionKey string) string {
	sessionID := m.EnsureSession(sessionKey)
	go m.SessionSync(sessionID)

	type stat struct {
		entityType string
		count      int
	}
	rows, err := m.db.Query(`
		SELECT type, COUNT(*) as c FROM entities
		WHERE fact_status != 'deleted'
		GROUP BY type ORDER BY c DESC LIMIT 10
	`)
	if err != nil {
		return "[SWL] No knowledge yet — start working to build the graph."
	}
	defer rows.Close()

	var stats []stat
	for rows.Next() {
		var s stat
		if rows.Scan(&s.entityType, &s.count) == nil {
			stats = append(stats, s)
		}
	}

	if len(stats) == 0 {
		return "[SWL] Graph is empty. Knowledge will build as you work."
	}

	var staleCount int
	m.db.QueryRow( //nolint:errcheck
		"SELECT COUNT(*) FROM entities WHERE fact_status = 'stale'",
	).Scan(&staleCount)

	out := "[SWL] Session resumed.\nKnowledge graph:"
	for _, s := range stats {
		out += fmt.Sprintf("\n  %-14s %d", s.entityType, s.count)
	}
	if staleCount > 0 {
		out += fmt.Sprintf("\n  ⚠ %d stale entities (files may have changed)", staleCount)
	}

	// Last session goal
	var goal sql.NullString
	m.db.QueryRow( //nolint:errcheck
		`SELECT goal FROM sessions WHERE id != ? AND goal IS NOT NULL
		 ORDER BY started_at DESC LIMIT 1`, sessionID,
	).Scan(&goal)
	if goal.Valid && goal.String != "" {
		out += "\nPrevious goal: " + goal.String
	}

	return out
}

// WorkspaceSnapshot returns a JSON object summarising the workspace for
// storage in sessions.workspace_state.
func (m *Manager) WorkspaceSnapshot() string {
	snapshot := map[string]any{}

	var fileCount, symCount int
	m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ?", KnownTypeFile).Scan(&fileCount)           //nolint:errcheck
	m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ?", KnownTypeSymbol).Scan(&symCount)          //nolint:errcheck
	snapshot["files"] = fileCount
	snapshot["symbols"] = symCount

	b, _ := json.Marshal(snapshot)
	return string(b)
}
