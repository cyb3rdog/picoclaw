package swl

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureSession returns the SWL session UUID for the given picoclaw session key,
// creating a new session row if none exists yet.
func (m *Manager) EnsureSession(sessionKey string) string {
	if sessionKey == "" {
		return ""
	}

	m.sessionsMu.Lock()
	if id, ok := m.activeSessions[sessionKey]; ok {
		m.sessionsMu.Unlock()
		return id
	}
	id := newUUID()
	m.activeSessions[sessionKey] = id
	m.sessionsMu.Unlock()

	m.mu.Lock()
	_, _ = m.db.Exec(
		`INSERT OR IGNORE INTO sessions (id, started_at, workspace_state) VALUES (?, ?, '{}')`,
		id, nowSQLite(),
	)
	m.mu.Unlock()

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
	m.sessionsMu.Lock()
	if m.syncedSessions[sessionID] {
		m.sessionsMu.Unlock()
		return
	}
	m.syncedSessions[sessionID] = true
	m.sessionsMu.Unlock()

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
	_ = rows.Err()

	for _, f := range files {
		// Resolve workspace-relative paths before os.Stat.
		absPath := f.name
		if !filepath.IsAbs(f.name) && m.workspace != "" {
			absPath = filepath.Join(m.workspace, f.name)
		}
		info, err := os.Stat(absPath)
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
	ids := make([]string, 0, len(m.activeSessions))
	for _, id := range m.activeSessions {
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
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.SessionSync(sessionID)
	}()

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
	_ = rows.Err()

	if len(stats) == 0 {
		return "[SWL] Graph is empty — cold start.\n" +
			"Use query_swl {\"scan\":true} to index the workspace, then work normally.\n" +
			"The graph will grow automatically as you read, write, and execute tools."
	}

	// Cold-start threshold: fewer than 10 non-session entities means the graph
	// is essentially fresh. Give a short bootstrap hint.
	var nonSessionCount int
	for _, s := range stats {
		if s.entityType != KnownTypeSession {
			nonSessionCount += s.count
		}
	}
	if nonSessionCount < 10 {
		return "[SWL] Knowledge graph is nearly empty (" + fmt.Sprintf("%d", nonSessionCount) + " entities).\n" +
			"Run query_swl {\"scan\":true} to index the workspace, then proceed normally.\n" +
			"Entities accumulate automatically from tool calls."
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

	// Workspace purpose — AnchorDocument descriptions and manifest module names.
	anchorRows, err := m.db.Query(`
		SELECT name, json_extract(metadata,'$.description'), json_extract(metadata,'$.module'),
		       json_extract(metadata,'$.kind')
		FROM entities
		WHERE type = ? AND fact_status != 'deleted'
		ORDER BY knowledge_depth DESC, access_count DESC LIMIT 5`,
		KnownTypeAnchorDocument,
	)
	if err == nil {
		defer anchorRows.Close()
		var anchors []string
		for anchorRows.Next() {
			var name string
			var desc, module, kind sql.NullString
			if anchorRows.Scan(&name, &desc, &module, &kind) != nil {
				continue
			}
			line := "  " + name
			if module.Valid && module.String != "" {
				line += " [" + module.String + "]"
			}
			if desc.Valid && desc.String != "" {
				line += ": " + truncate(desc.String, 120)
			}
			anchors = append(anchors, line)
		}
		_ = anchorRows.Err()
		if len(anchors) > 0 {
			out += "\nWorkspace anchors:\n" + strings.Join(anchors, "\n")
		}
	}

	// Semantic areas — directory-level content profile.
	areaRows, err := m.db.Query(`
		SELECT name, json_extract(metadata,'$.content_type')
		FROM entities
		WHERE type = ? AND fact_status != 'deleted'
		ORDER BY name LIMIT 10`,
		KnownTypeSemanticArea,
	)
	if err == nil {
		defer areaRows.Close()
		var areas []string
		for areaRows.Next() {
			var name string
			var ct sql.NullString
			if areaRows.Scan(&name, &ct) != nil {
				continue
			}
			line := "  " + name
			if ct.Valid && ct.String != "" {
				line += " [" + ct.String + "]"
			}
			areas = append(areas, line)
		}
		_ = areaRows.Err()
		if len(areas) > 0 {
			out += "\nSemantic areas:\n" + strings.Join(areas, "\n")
		}
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

// WorkspaceSnapshot returns a JSON object summarizing the workspace for
// storage in sessions.workspace_state.
func (m *Manager) WorkspaceSnapshot() string {
	snapshot := map[string]any{}

	var fileCount, symCount int
	m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ?", KnownTypeFile).Scan(&fileCount)  //nolint:errcheck
	m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ?", KnownTypeSymbol).Scan(&symCount) //nolint:errcheck
	snapshot["files"] = fileCount
	snapshot["symbols"] = symCount

	b, _ := json.Marshal(snapshot)
	return string(b)
}
