package swl

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// tier1Pattern maps a compiled regexp to a handler method name + hint extraction group.
type tier1Pattern struct {
	re      *regexp.Regexp
	handler func(m *Manager, hint string) string
}

var tier1Patterns []tier1Pattern

func init() {
	tier1Patterns = []tier1Pattern{
		{regexp.MustCompile(`(?i)(?:resume|bring me up to speed|what was i doing|where did we leave)`), func(m *Manager, hint string) string { return m.SessionResume("") }},
		{regexp.MustCompile(`(?i)functions?\s+in\s+(.+)`), func(m *Manager, hint string) string { return m.askSymbols(hint, "function") }},
		{regexp.MustCompile(`(?i)symbols?\s+in\s+(.+)`), func(m *Manager, hint string) string { return m.askSymbols(hint, "") }},
		{regexp.MustCompile(`(?i)classes?\s+in\s+(.+)`), func(m *Manager, hint string) string { return m.askSymbols(hint, "class") }},
		{regexp.MustCompile(`(?i)(?:todos?|fixmes?|tasks?)\s+in\s+(.+)`), func(m *Manager, hint string) string { return m.askTasks(hint) }},
		{regexp.MustCompile(`(?i)(?:todos?|fixmes?|tasks?|open\s+tasks?|pending)`), func(m *Manager, hint string) string { return m.askAllTasks() }},
		{regexp.MustCompile(`(?i)(?:imports?|depends?\s+on|dependencies)\s+(?:in|of|for)\s+(.+)`), func(m *Manager, hint string) string { return m.askImports(hint) }},
		{regexp.MustCompile(`(?i)files?\s+in\s+(.+)`), func(m *Manager, hint string) string { return m.askFilesIn(hint) }},
		{regexp.MustCompile(`(?i)(?:stale|drift|outdated|changed)`), func(m *Manager, hint string) string { return m.askStale() }},
		{regexp.MustCompile(`(?i)project\s+type`), func(m *Manager, hint string) string { return m.askProjectType() }},
		{regexp.MustCompile(`(?i)(?:most\s+complex|complexity|biggest\s+files?)`), func(m *Manager, hint string) string { return m.askComplexity() }},
		{regexp.MustCompile(`(?i)(?:top\s+deps?|most\s+imported|popular\s+deps?)`), func(m *Manager, hint string) string { return m.askTopDeps() }},
		{regexp.MustCompile(`(?i)(?:recent\s+(?:files?|changes?|writes?))`), func(m *Manager, hint string) string { return m.askRecentFiles() }},
		{regexp.MustCompile(`(?i)(?:urls?|links?|web\s+(?:pages?|sites?))`), func(m *Manager, hint string) string { return m.askURLs() }},
		{regexp.MustCompile(`(?i)sessions?`), func(m *Manager, hint string) string { return m.askSessions() }},
		{regexp.MustCompile(`(?i)stats?`), func(m *Manager, hint string) string { return m.Stats() }},
		{regexp.MustCompile(`(?i)gaps?`), func(m *Manager, hint string) string { return m.KnowledgeGaps() }},
		{regexp.MustCompile(`(?i)schema`), func(m *Manager, hint string) string { return m.Schema() }},
	}
}

// Ask dispatches a natural-language question through Tier 1 → Tier 2 → Tier 3.
func (m *Manager) Ask(question string) string {
	q := strings.TrimSpace(question)
	if q == "" {
		return "[SWL] Empty question."
	}

	// Tier 1: pattern matching
	for _, p := range tier1Patterns {
		matches := p.re.FindStringSubmatch(q)
		if matches == nil {
			continue
		}
		hint := ""
		if len(matches) > 1 {
			hint = strings.TrimSpace(matches[1])
		}
		return p.handler(m, hint)
	}

	// Tier 2: named SQL templates
	if result := m.tryTier2(q); result != "" {
		return result
	}

	return fmt.Sprintf("[SWL] No pattern matched %q. Try: stats, gaps, resume, or sql:SELECT ...", q)
}

// --- Tier 1 handlers ---

func (m *Manager) askSymbols(hint, symType string) string {
	hint = strings.TrimSpace(hint)
	query := `SELECT e.name, e.knowledge_depth FROM entities e
		JOIN edges ed ON ed.to_id = e.id AND ed.rel = 'defines'
		WHERE e.type = ? AND e.fact_status != 'deleted'`
	args := []any{KnownTypeSymbol}

	if hint != "" {
		query += ` AND ed.from_id IN (SELECT id FROM entities WHERE name LIKE ? AND type = ?)`
		args = append(args, "%"+hint+"%", KnownTypeFile)
	}
	query += " ORDER BY e.knowledge_depth DESC, e.access_count DESC LIMIT 40"

	rows, err := m.db.Query(query, args...)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		var depth int
		if rows.Scan(&name, &depth) == nil {
			out = append(out, fmt.Sprintf("  %s (depth %d)", name, depth))
		}
	}
	if len(out) == 0 {
		return "[SWL] No symbols found" + suffix(hint)
	}
	return fmt.Sprintf("[SWL] Symbols%s:\n%s", suffix(hint), strings.Join(out, "\n"))
}

func (m *Manager) askTasks(hint string) string {
	hint = strings.TrimSpace(hint)
	query := `SELECT e.name FROM entities e
		JOIN edges ed ON ed.to_id = e.id AND ed.rel = 'has_task'
		WHERE e.type = ? AND e.fact_status != 'deleted'`
	args := []any{KnownTypeTask}

	if hint != "" {
		query += ` AND ed.from_id IN (SELECT id FROM entities WHERE name LIKE ?)`
		args = append(args, "%"+hint+"%")
	}
	query += " ORDER BY e.modified_at DESC LIMIT 30"

	return m.runQueryList("Tasks"+suffix(hint), query, args...)
}

func (m *Manager) askAllTasks() string {
	rows, err := m.db.Query(
		`SELECT name FROM entities WHERE type = ? AND fact_status != 'deleted'
		 ORDER BY modified_at DESC LIMIT 30`, KnownTypeTask,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()
	return collectRows("[SWL] Open tasks", rows)
}

func (m *Manager) askImports(hint string) string {
	query := `SELECT e.name FROM entities e
		JOIN edges ed ON ed.to_id = e.id AND ed.rel = 'imports'
		WHERE e.type = ? AND e.fact_status != 'deleted'`
	args := []any{KnownTypeDependency}
	if hint != "" {
		query += ` AND ed.from_id IN (SELECT id FROM entities WHERE name LIKE ?)`
		args = append(args, "%"+hint+"%")
	}
	query += " ORDER BY e.name LIMIT 40"
	return m.runQueryList("Imports"+suffix(hint), query, args...)
}

func (m *Manager) askFilesIn(hint string) string {
	query := `SELECT name FROM entities WHERE type = ? AND fact_status != 'deleted'`
	args := []any{KnownTypeFile}
	if hint != "" {
		query += ` AND name LIKE ?`
		args = append(args, "%"+hint+"%")
	}
	query += " ORDER BY modified_at DESC LIMIT 30"
	return m.runQueryList("Files"+suffix(hint), query, args...)
}

func (m *Manager) askStale() string {
	rows, err := m.db.Query(
		`SELECT name, type FROM entities WHERE fact_status = 'stale' ORDER BY modified_at DESC LIMIT 20`,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name, t string
		if rows.Scan(&name, &t) == nil {
			out = append(out, fmt.Sprintf("  [%s] %s", t, name))
		}
	}
	if len(out) == 0 {
		return "[SWL] No stale entities — knowledge graph is current."
	}
	return "[SWL] Stale entities:\n" + strings.Join(out, "\n")
}

func (m *Manager) askProjectType() string {
	rows, err := m.db.Query(
		`SELECT name FROM entities WHERE type = ? AND fact_status != 'deleted'
		 ORDER BY access_count DESC LIMIT 10`, KnownTypeTopic,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()
	return collectRows("[SWL] Project topics", rows)
}

func (m *Manager) askComplexity() string {
	rows, err := m.db.Query(`
		SELECT e.name, COUNT(ed.to_id) as sym_count
		FROM entities e
		JOIN edges ed ON ed.from_id = e.id AND ed.rel = 'defines'
		WHERE e.type = ? AND e.fact_status != 'deleted'
		GROUP BY e.id ORDER BY sym_count DESC LIMIT 15`, KnownTypeFile,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		var count int
		if rows.Scan(&name, &count) == nil {
			out = append(out, fmt.Sprintf("  %3d symbols  %s", count, name))
		}
	}
	if len(out) == 0 {
		return "[SWL] No complexity data yet."
	}
	return "[SWL] Files by symbol count:\n" + strings.Join(out, "\n")
}

func (m *Manager) askTopDeps() string {
	rows, err := m.db.Query(`
		SELECT e.name, COUNT(DISTINCT ed.from_id) as file_count
		FROM entities e
		JOIN edges ed ON ed.to_id = e.id AND ed.rel = 'imports'
		WHERE e.type = ? GROUP BY e.id ORDER BY file_count DESC LIMIT 20`, KnownTypeDependency,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		var count int
		if rows.Scan(&name, &count) == nil {
			out = append(out, fmt.Sprintf("  %3d files  %s", count, name))
		}
	}
	if len(out) == 0 {
		return "[SWL] No dependency data yet."
	}
	return "[SWL] Most imported dependencies:\n" + strings.Join(out, "\n")
}

func (m *Manager) askRecentFiles() string {
	rows, err := m.db.Query(
		`SELECT name FROM entities WHERE type = ? AND fact_status != 'deleted'
		 ORDER BY modified_at DESC LIMIT 15`, KnownTypeFile,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()
	return collectRows("[SWL] Recently modified files", rows)
}

func (m *Manager) askURLs() string {
	rows, err := m.db.Query(
		`SELECT name FROM entities WHERE type = ? AND fact_status != 'deleted'
		 ORDER BY access_count DESC, modified_at DESC LIMIT 20`, KnownTypeURL,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()
	return collectRows("[SWL] Known URLs", rows)
}

func (m *Manager) askSessions() string {
	rows, err := m.db.Query(
		`SELECT id, started_at, ended_at, goal FROM sessions ORDER BY started_at DESC LIMIT 10`,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id, startedAt string
		var endedAt, goal sql.NullString
		if rows.Scan(&id, &startedAt, &endedAt, &goal) != nil {
			continue
		}
		line := fmt.Sprintf("  %s  started %s", id[:8], startedAt[:10])
		if goal.Valid && goal.String != "" {
			line += "  goal: " + truncate(goal.String, 60)
		}
		if !endedAt.Valid {
			line += "  [active]"
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return "[SWL] No sessions recorded yet."
	}
	return "[SWL] Sessions:\n" + strings.Join(out, "\n")
}

// --- Tier 2: named SQL templates ---

var sqlTemplates = map[string]struct {
	keywords []string
	query    string
}{
	"dependency_chain": {
		keywords: []string{"chain", "transitive", "recursive"},
		query: `WITH RECURSIVE chain(id, depth) AS (
			SELECT to_id, 0 FROM edges WHERE from_id = (SELECT id FROM entities WHERE name LIKE ? LIMIT 1) AND rel = 'imports'
			UNION ALL
			SELECT e.to_id, c.depth+1 FROM edges e JOIN chain c ON e.from_id = c.id WHERE c.depth < 8
		) SELECT DISTINCT e.name, c.depth FROM chain c JOIN entities e ON e.id = c.id ORDER BY c.depth LIMIT 50`,
	},
	"files_by_type": {
		keywords: []string{"count by type", "entity types", "breakdown"},
		query:    `SELECT type, COUNT(*) as c FROM entities WHERE fact_status != 'deleted' GROUP BY type ORDER BY c DESC`,
	},
	"orphan_symbols": {
		keywords: []string{"orphan", "unreferenced"},
		query:    `SELECT e.name FROM entities e WHERE e.type = 'Symbol' AND NOT EXISTS (SELECT 1 FROM edges WHERE to_id = e.id OR from_id = e.id) LIMIT 20`,
	},
}

func (m *Manager) tryTier2(question string) string {
	q := strings.ToLower(question)
	for _, tmpl := range sqlTemplates {
		for _, kw := range tmpl.keywords {
			if strings.Contains(q, kw) {
				rows, err := m.db.Query(tmpl.query, "%"+question+"%")
				if err != nil {
					continue
				}
				defer rows.Close()
				return collectRowsGeneric(rows)
			}
		}
	}
	return ""
}

// --- Public utility methods ---

// Stats returns a summary of graph contents.
func (m *Manager) Stats() string {
	rows, err := m.db.Query(
		`SELECT type, COUNT(*), SUM(CASE WHEN fact_status='verified' THEN 1 ELSE 0 END),
		        SUM(CASE WHEN fact_status='stale' THEN 1 ELSE 0 END)
		 FROM entities GROUP BY type ORDER BY COUNT(*) DESC`,
	)
	if err != nil {
		return "[SWL] Stats error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	out = append(out, fmt.Sprintf("  %-14s %6s %8s %6s", "type", "total", "verified", "stale"))
	out = append(out, "  "+strings.Repeat("-", 38))
	for rows.Next() {
		var t string
		var total, verified, stale int
		if rows.Scan(&t, &total, &verified, &stale) == nil {
			out = append(out, fmt.Sprintf("  %-14s %6d %8d %6d", t, total, verified, stale))
		}
	}
	var edgeCount int
	m.db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&edgeCount) //nolint:errcheck
	out = append(out, fmt.Sprintf("\n  Edges: %d", edgeCount))
	return "[SWL] Graph stats:\n" + strings.Join(out, "\n")
}

// KnowledgeGaps returns entities with low confidence or unknown status.
func (m *Manager) KnowledgeGaps() string {
	rows, err := m.db.Query(
		`SELECT type, name, confidence, fact_status FROM entities
		 WHERE (confidence < 0.85 OR fact_status = 'unknown') AND fact_status != 'deleted'
		 ORDER BY confidence ASC LIMIT 20`,
	)
	if err != nil {
		return "[SWL] Gaps error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var t, name, status string
		var conf float64
		if rows.Scan(&t, &name, &conf, &status) == nil {
			out = append(out, fmt.Sprintf("  [%s] %.2f %s  %s", status, conf, t, name))
		}
	}
	if len(out) == 0 {
		return "[SWL] No significant knowledge gaps."
	}
	return "[SWL] Knowledge gaps (low confidence or unknown status):\n" + strings.Join(out, "\n")
}

// DriftReport returns all stale entities.
func (m *Manager) DriftReport() string {
	return m.askStale()
}

// Schema returns a description of the DB tables.
func (m *Manager) Schema() string {
	return `[SWL] Schema:
  entities(id, type, name, metadata, confidence, content_hash, knowledge_depth,
           extraction_method, fact_status, created_at, modified_at, accessed_at, access_count, last_checked)
  edges(from_id, rel, to_id, source_session, confirmed_at)
  sessions(id, started_at, ended_at, goal, summary, workspace_state)
  events(id, session_id, tool, phase, args_hash, ts)
  constraints(name, query, action)

Entity types (open — any string valid): File, Directory, Symbol, Dependency, Task, Section,
  Topic, URL, Commit, Session, Note, Command, Intent, SubAgent, + custom
Edge relations (open — any string valid): defines, imports, has_task, has_section, mentions,
  depends_on, tagged, in_dir, written_in, edited_in, appended_in, read, fetched, executed,
  deleted, describes, committed_in, found, listed, spawned_by, context_of, reasoned, + custom`
}

// AssertNote records a free-form note about a subject entity.
// depth is set to MAX(current_depth, 2) — never hardcoded to 3.
func (m *Manager) AssertNote(subject, content string, confidence float64, entityType EntityType) string {
	if subject == "" || content == "" {
		return "[SWL] assert requires subject and content."
	}
	if confidence <= 0 {
		confidence = 0.85
	}
	if entityType == "" {
		entityType = KnownTypeNote
	}

	noteID := entityID(entityType, subject+":"+content[:min(40, len(content))])

	// Read current depth
	var currentDepth int
	m.db.QueryRow("SELECT knowledge_depth FROM entities WHERE id = ?", noteID).Scan(&currentDepth) //nolint:errcheck
	depth := currentDepth
	if depth < 2 {
		depth = 2
	}

	_ = m.writer.upsertEntity(EntityTuple{
		ID: noteID, Type: entityType, Name: content,
		Confidence: confidence, ExtractionMethod: MethodStated, KnowledgeDepth: depth,
		Metadata: map[string]any{"subject": subject},
	})

	subjectID := entityID(KnownTypeNote, subject)
	_ = m.writer.upsertEntity(EntityTuple{
		ID: subjectID, Type: KnownTypeNote, Name: subject,
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})
	_ = m.writer.upsertEdge(EdgeTuple{FromID: noteID, Rel: KnownRelDescribes, ToID: subjectID})

	return fmt.Sprintf("[SWL] Note recorded about %q (depth %d, conf %.2f).", subject, depth, confidence)
}

// SafeQuery executes a read-only SQL query with a 200-row cap.
func (m *Manager) SafeQuery(sqlStr string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToUpper(sqlStr))
	if !strings.HasPrefix(trimmed, "SELECT") &&
		!strings.HasPrefix(trimmed, "WITH") &&
		!strings.HasPrefix(trimmed, "EXPLAIN") {
		return "", fmt.Errorf("only SELECT/WITH/EXPLAIN queries are allowed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := m.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	header := strings.Join(cols, "\t")
	var lines []string
	lines = append(lines, header)
	lines = append(lines, strings.Repeat("-", len(header)))

	count := 0
	for rows.Next() {
		if count >= 200 {
			lines = append(lines, "(truncated at 200 rows)")
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if rows.Scan(ptrs...) != nil {
			continue
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
		}
		lines = append(lines, strings.Join(parts, "\t"))
		count++
	}
	return strings.Join(lines, "\n"), nil
}

// --- helpers ---

func (m *Manager) runQueryList(label, query string, args ...any) string {
	rows, err := m.db.Query(query, args...)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()
	return collectRows("[SWL] "+label, rows)
}

func collectRows(label string, rows *sql.Rows) string {
	var out []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && name != "" {
			out = append(out, "  "+name)
		}
	}
	if len(out) == 0 {
		return label + ": (none)"
	}
	return label + ":\n" + strings.Join(out, "\n")
}

func collectRowsGeneric(rows *sql.Rows) string {
	cols, _ := rows.Columns()
	if len(cols) == 0 {
		return ""
	}
	var lines []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if rows.Scan(ptrs...) != nil {
			continue
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
		}
		lines = append(lines, "  "+strings.Join(parts, "  "))
	}
	if len(lines) == 0 {
		return ""
	}
	return "[SWL] Results:\n" + strings.Join(lines, "\n")
}

func suffix(hint string) string {
	if hint == "" {
		return ""
	}
	return " in " + hint
}
