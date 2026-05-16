package swl

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// handlerRegistry maps handler names (as referenced in swl.query.yaml) to
// type-safe method calls. All handlers conform to func(*Manager, string) string.
// Handlers that take no hint ignore the hint parameter. Handlers that take
// extra fixed arguments (e.g. askSymbols with symType) use a wrapping closure.
// This replaces the previous reflect-based dispatch in dispatchHandler.
//
//nolint:gochecknoglobals
var handlerRegistry = map[string]func(*Manager, string) string{
	// Session
	"SessionResume": func(m *Manager, hint string) string { return m.SessionResume(hint) },

	// Workspace / areas
	"askWorkspacePurpose": func(m *Manager, _ string) string { return m.askWorkspacePurpose() },
	"askSemanticAreas":    func(m *Manager, _ string) string { return m.askSemanticAreas() },
	"askAnchorDocuments":  func(m *Manager, _ string) string { return m.askAnchorDocuments() },

	// File detail
	"askFileDetail": func(m *Manager, hint string) string { return m.askFileDetail(hint) },

	// Label search (find-by-purpose)
	"labelSearch": func(m *Manager, hint string) string { return m.labelSearch(hint) },

	// Symbols — hint contains file name; symType not specified by YAML (defaults to "")
	"askSymbols": func(m *Manager, hint string) string { return m.askSymbols(hint, "") },

	// Tasks
	"askTasks":    func(m *Manager, hint string) string { return m.askTasks(hint) },
	"askAllTasks": func(m *Manager, _ string) string { return m.askAllTasks() },

	// Imports / dependencies
	"askImports": func(m *Manager, hint string) string { return m.askImports(hint) },

	// Files
	"askFilesIn":     func(m *Manager, hint string) string { return m.askFilesIn(hint) },
	"askRecentFiles": func(m *Manager, _ string) string { return m.askRecentFiles() },

	// Quality / metrics
	"askStale":       func(m *Manager, _ string) string { return m.askStale() },
	"askProjectType": func(m *Manager, _ string) string { return m.askProjectType() },
	"askComplexity":  func(m *Manager, _ string) string { return m.askComplexity() },
	"askTopDeps":     func(m *Manager, _ string) string { return m.askTopDeps() },

	// URLs / web
	"askURLs": func(m *Manager, _ string) string { return m.askURLs() },

	// Operational
	"askSessions":          func(m *Manager, _ string) string { return m.askSessions() },
	"askSessionActivity":   func(m *Manager, _ string) string { return m.askSessionActivity() },
	"askIndexStatus":       func(m *Manager, _ string) string { return m.IndexStatus() },
	"askModelReliability":  func(m *Manager, _ string) string { return m.askModelReliability() },
	"Stats":                func(m *Manager, _ string) string { return m.Stats() },
	"KnowledgeGaps":        func(m *Manager, _ string) string { return m.KnowledgeGaps() },
	"Schema":               func(m *Manager, _ string) string { return m.Schema() },
}

// ValidateHandler reports whether name is a registered handler.
// Used by CompileQueryConfig to catch YAML typos at load time.
func ValidateHandler(name string) bool {
	_, ok := handlerRegistry[name]
	return ok
}

// tier1Pattern maps a compiled regexp to a handler method name + hint extraction group.
type tier1Pattern struct {
	re      *regexp.Regexp
	handler func(m *Manager, hint string) string
}

var tier1Patterns []tier1Pattern

func init() {
	tier1Patterns = []tier1Pattern{
		// Session / resume
		{
			regexp.MustCompile(`(?i)(?:resume|bring me up to speed|what was i doing|where did we leave)`),
			func(m *Manager, hint string) string { return m.SessionResume("") },
		},

		// Workspace purpose / goals — backed by snapshot AnchorDocument entities
		{
			regexp.MustCompile(
				`(?i)what\s+(?:is\s+)?this\s+(?:workspace|project|repo(?:sitory)?)\s+(?:for|about|doing|used\s+for)`,
			),
			func(m *Manager, hint string) string { return m.askWorkspacePurpose() },
		},
		{
			regexp.MustCompile(
				`(?i)what\s+(?:does\s+this|is\s+the)\s+(?:project|workspace|repo(?:sitory)?)\s+(?:do|goal|purpose|aim|about)`,
			),
			func(m *Manager, hint string) string { return m.askWorkspacePurpose() },
		},
		{
			regexp.MustCompile(
				`(?i)(?:describe|summaris[e]?|summarize)\s+(?:this\s+)?(?:workspace|project|repo(?:sitory)?)`,
			),
			func(m *Manager, hint string) string { return m.askWorkspacePurpose() },
		},
		{
			regexp.MustCompile(`(?i)(?:project|workspace)\s+(?:goal|purpose|aim|description|overview)`),
			func(m *Manager, hint string) string { return m.askWorkspacePurpose() },
		},

		// Semantic areas
		{
			regexp.MustCompile(
				`(?i)(?:what\s+areas?|semantic\s+areas?|workspace\s+areas?|areas?\s+of\s+(?:the\s+)?workspace)`,
			),
			func(m *Manager, hint string) string { return m.askSemanticAreas() },
		},
		{
			regexp.MustCompile(`(?i)(?:key|main|important)\s+(?:documents?|files?|areas?)`),
			func(m *Manager, hint string) string { return m.askAnchorDocuments() },
		},
		{
			regexp.MustCompile(`(?i)(?:anchor|readme|overview)\s+(?:docs?|documents?|files?)`),
			func(m *Manager, hint string) string { return m.askAnchorDocuments() },
		},

		// File detail — what does a file do?
		{
			regexp.MustCompile(`(?i)what\s+does\s+(.+?)\s+do`),
			func(m *Manager, hint string) string { return m.askFileDetail(hint) },
		},
		{
			regexp.MustCompile(`(?i)describe\s+(?:file\s+)?(.+)`),
			func(m *Manager, hint string) string { return m.askFileDetail(hint) },
		},
		{
			regexp.MustCompile(`(?i)explain\s+(?:file\s+)?(.+)`),
			func(m *Manager, hint string) string { return m.askFileDetail(hint) },
		},

		// Find-by-purpose: where is the file that does X? (Phase A.3)
		{
			regexp.MustCompile(
				`(?i)where\s+(?:is|are)\s+(?:the\s+)?(.+?)\s+(?:that\s+)?(?:does|handles|implements|manages|provides)`,
			),
			func(m *Manager, hint string) string { return m.labelSearch(hint) },
		},
		{
			regexp.MustCompile(`(?i)where\s+(?:is|are)\s+(?:the\s+)?(.+?)\s+(?:code|logic|handler|service|middleware)`),
			func(m *Manager, hint string) string { return m.labelSearch(hint) },
		},
		{
			regexp.MustCompile(`(?i)find\s+(?:the\s+)?(.+?)\s+(?:files?|code|impl(?:ementation)?)`),
			func(m *Manager, hint string) string { return m.labelSearch(hint) },
		},
		{
			regexp.MustCompile(`(?i)which\s+(?:file|files)\s+(?:does|handles|implements|handles)\s+(.+)`),
			func(m *Manager, hint string) string { return m.labelSearch(hint) },
		},
		{
			regexp.MustCompile(`(?i)where\s+(?:are\s+)?(?:the\s+)?(?:entry\s+points?|tests?|config(?:uration)?s?)`),
			func(m *Manager, hint string) string { return m.labelSearch(hint) },
		},

		// Existing patterns
		{
			regexp.MustCompile(`(?i)functions?\s+in\s+(.+)`),
			func(m *Manager, hint string) string { return m.askSymbols(hint, "function") },
		},
		{
			regexp.MustCompile(`(?i)symbols?\s+in\s+(.+)`),
			func(m *Manager, hint string) string { return m.askSymbols(hint, "") },
		},
		{
			regexp.MustCompile(`(?i)classes?\s+in\s+(.+)`),
			func(m *Manager, hint string) string { return m.askSymbols(hint, "class") },
		},
		{
			regexp.MustCompile(`(?i)(?:todos?|fixmes?|tasks?)\s+in\s+(.+)`),
			func(m *Manager, hint string) string { return m.askTasks(hint) },
		},
		{
			regexp.MustCompile(`(?i)(?:todos?|fixmes?|tasks?|open\s+tasks?|pending)`),
			func(m *Manager, hint string) string { return m.askAllTasks() },
		},
		{
			regexp.MustCompile(`(?i)(?:imports?|depends?\s+on|dependencies)\s+(?:in|of|for)\s+(.+)`),
			func(m *Manager, hint string) string { return m.askImports(hint) },
		},
		{
			regexp.MustCompile(`(?i)files?\s+in\s+(.+)`),
			func(m *Manager, hint string) string { return m.askFilesIn(hint) },
		},
		{
			regexp.MustCompile(`(?i)(?:stale|drift|outdated|changed)`),
			func(m *Manager, hint string) string { return m.askStale() },
		},
		{regexp.MustCompile(`(?i)project\s+type`), func(m *Manager, hint string) string { return m.askProjectType() }},
		{
			regexp.MustCompile(`(?i)(?:most\s+complex|complexity|biggest\s+files?)`),
			func(m *Manager, hint string) string { return m.askComplexity() },
		},
		{
			regexp.MustCompile(`(?i)(?:top\s+deps?|most\s+imported|popular\s+deps?)`),
			func(m *Manager, hint string) string { return m.askTopDeps() },
		},
		{
			regexp.MustCompile(`(?i)(?:recent\s+(?:files?|changes?|writes?))`),
			func(m *Manager, hint string) string { return m.askRecentFiles() },
		},
		{
			regexp.MustCompile(`(?i)(?:urls?|links?|web\s+(?:pages?|sites?))`),
			func(m *Manager, hint string) string { return m.askURLs() },
		},
		{regexp.MustCompile(`(?i)sessions?`), func(m *Manager, hint string) string { return m.askSessions() }},
		{
			regexp.MustCompile(`(?i)(?:model\s+reliability|per.?model|llm\s+quality|which\s+(?:model|llm)|model\s+(?:accuracy|stats?|performance))`),
			func(m *Manager, _ string) string { return m.askModelReliability() },
		},
		{regexp.MustCompile(`(?i)stats?`), func(m *Manager, hint string) string { return m.Stats() }},
		{regexp.MustCompile(`(?i)gaps?`), func(m *Manager, hint string) string { return m.KnowledgeGaps() }},
		{regexp.MustCompile(`(?i)schema`), func(m *Manager, hint string) string { return m.Schema() }},
	}
}

// Ask dispatches a natural-language question through Tier 1 → Tier 2 → Tier 3.
// Unmatched questions are recorded as query gaps after 3 repetitions.
// Phase B: If Manager has compiled query intents from swl.query.yaml, those are tried
// first. Falls back to hardcoded tier1Patterns for forward compatibility.
func (m *Manager) Ask(question string) string {
	q := strings.TrimSpace(question)
	if q == "" {
		return "[SWL] Empty question."
	}

	// Prepend a warning when workspace rules/query config failed to load.
	rulesWarning := ""
	if m.rulesLoadErr != "" {
		rulesWarning = fmt.Sprintf(
			"⚠ SWL workspace rules failed to load: %s\n  Workspace file: %s/.swl/swl.rules.yaml\n  Using embedded defaults. Fix the file and rescan to apply custom rules.\n\n",
			m.rulesLoadErr, m.workspace,
		)
	}

	// Tier 1: try YAML intents if loaded, otherwise hardcoded patterns
	var tier1Result string
	if len(m.rules.QueryIntents) > 0 {
		tier1Result = m.tryYAMLIntents(q)
	}
	if tier1Result == "" {
		tier1Result = m.tryHardcodedPatterns(q)
	}
	if tier1Result != "" {
		return rulesWarning + tier1Result
	}

	// Tier 2: SQL templates
	var tier2Result string
	if len(m.rules.SQLTemplates) > 0 {
		tier2Result = m.tryYAMLTier2(q)
	} else {
		tier2Result = m.tryTier2(q)
	}
	if tier2Result != "" {
		return rulesWarning + tier2Result
	}

	// Tier 3: freetext
	if result := m.tryTier3(q); result != "" {
		return rulesWarning + result
	}

	isNewGap := m.recordQueryGap(q)
	return rulesWarning + m.fallthroughResponse(q, isNewGap)
}

// tryYAMLIntents matches the question against compiled query intents from swl.query.yaml.
// Returns the handler result, or "" if no intent matched.
func (m *Manager) tryYAMLIntents(question string) string {
	for _, intent := range m.rules.QueryIntents {
		matches := intent.RE.FindStringSubmatch(question)
		if matches == nil {
			continue
		}
		hint := ""
		if intent.HintGroup > 0 && len(matches) > intent.HintGroup {
			hint = strings.TrimSpace(matches[intent.HintGroup])
		}
		return m.dispatchHandler(intent.Handler, hint)
	}
	return ""
}

// dispatchHandler calls a registered handler by name with the extracted hint.
// If the name is not found in handlerRegistry, it logs a warning and returns "".
// This replaces the previous reflect.MethodByName approach which silently
// swallowed typos in handler names.
func (m *Manager) dispatchHandler(handlerName, hint string) string {
	fn, ok := handlerRegistry[handlerName]
	if !ok {
		m.logInferenceEvent("dispatch", "unknown handler: "+handlerName)
		return "" // unknown handler → fall through to Tier 2
	}
	return fn(m, hint)
}

// tryHardcodedPatterns is the original Tier 1 pattern matching loop.
// Kept as fallback when YAML intents are not loaded.
func (m *Manager) tryHardcodedPatterns(question string) string {
	for _, p := range tier1Patterns {
		matches := p.re.FindStringSubmatch(question)
		if matches == nil {
			continue
		}
		hint := ""
		if len(matches) > 1 {
			hint = strings.TrimSpace(matches[1])
		}
		return p.handler(m, hint)
	}
	return ""
}

// tryYAMLTier2 tries YAML-defined SQL templates (Phase B query externalization).
// Falls back to hardcoded sqlTemplates if none match.
func (m *Manager) tryYAMLTier2(question string) string {
	q := strings.ToLower(question)
	for _, tmpl := range m.rules.SQLTemplates {
		for _, kw := range tmpl.Keywords {
			if !strings.Contains(q, kw) {
				continue
			}
			// Substitute placeholders from the question (derived from tmpl.query)
			args := extractTier2Args(tmpl.Query, question)
			rows, err := m.db.Query(tmpl.Query, args...)
			if err != nil {
				continue
			}
			defer rows.Close()
			return collectRowsGeneric(rows)
		}
	}
	// Fall back to hardcoded templates
	return m.tryTier2(question)
}

// extractTier2Args extracts positional SQL arguments from the question string.
// If the SQL template contains no "?" placeholders, nil is returned immediately
// (the query takes no parameters). Otherwise, the first meaningful term is
// extracted from the question by stripping common stop words and returning the
// first remaining token wrapped in SQL LIKE wildcards ("%term%").
//
// For the current built-in templates only one argument is ever needed
// (dependency_chain: a file/package name). Additional placeholder slots beyond
// the first are filled with the same term so the query still executes.
func extractTier2Args(query, question string) []any {
	if !strings.Contains(query, "?") {
		return nil
	}

	// Count how many "?" placeholders the query has.
	placeholderCount := strings.Count(query, "?")

	// Extract the first meaningful term from the question.
	term := firstMeaningfulTerm(question)
	if term == "" {
		// No term found; supply empty LIKE wildcard so the query still runs.
		term = "%"
	} else {
		term = "%" + term + "%"
	}

	args := make([]any, placeholderCount)
	for i := range args {
		args[i] = term
	}
	return args
}

// firstMeaningfulTerm returns the first non-stop-word token from s, lowercased.
// Returns "" if all tokens are stop words or s is empty.
func firstMeaningfulTerm(s string) string {
	stop := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "in": true, "of": true,
		"for": true, "to": true, "and": true, "or": true, "what": true, "how": true,
		"where": true, "find": true, "show": true, "list": true, "get": true,
		"that": true, "which": true, "are": true, "chain": true, "transitive": true,
		"recursive": true, "count": true, "by": true, "type": true, "breakdown": true,
		"orphan": true, "unreferenced": true, "entity": true, "entities": true,
	}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, `.,;:!?'"`)
		if w == "" || len(w) < 2 || stop[w] {
			continue
		}
		return w
	}
	return ""
}

// --- Tier 1 handlers ---

// labelSearch finds files matching semantic labels derived from path patterns.
// This is the core "where is the file that does X?" handler (Phase A.3).
// It matches the query hint against role, domain, kind, and content_type labels
// stored in entity metadata at scan time (Phase A.2 semantic bootstrap).
func (m *Manager) labelSearch(hint string) string {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return "[SWL] Please specify what you're looking for (e.g. 'authentication', 'config', 'tests')."
	}

	// Extract search terms from the hint. Normalize to lowercase.
	terms := extractSearchTerms(hint)
	if len(terms) == 0 {
		return fmt.Sprintf("[SWL] Could not extract search terms from %q.", hint)
	}

	// Build a SQL query that searches role, domain, kind, content_type, and visibility
	// fields in entity metadata. We use a weighted scoring approach:
	//   exact label match on role (weight 10) > domain (weight 5) > kind (weight 3) > content_type (weight 2)
	//   + name substring match (weight 1)
	//   + directory path match (weight 1)
	// Files with higher total scores ranked first.
	scoreExpr := "0"
	args := []any{}
	for _, term := range terms {
		termLower := strings.ToLower(term)
		// Role match: exact or substring (highest weight)
		scoreExpr += " + CASE WHEN json_extract(metadata,'$.role') LIKE ? THEN 10 ELSE 0 END"
		args = append(args, "%"+termLower+"%")
		// Domain match
		scoreExpr += " + CASE WHEN json_extract(metadata,'$.domain') LIKE ? THEN 5 ELSE 0 END"
		args = append(args, "%"+termLower+"%")
		// Kind match
		scoreExpr += " + CASE WHEN json_extract(metadata,'$.kind') LIKE ? THEN 3 ELSE 0 END"
		args = append(args, "%"+termLower+"%")
		// Content type match
		scoreExpr += " + CASE WHEN json_extract(metadata,'$.content_type') LIKE ? THEN 2 ELSE 0 END"
		args = append(args, "%"+termLower+"%")
		// Name substring match
		scoreExpr += " + CASE WHEN LOWER(name) LIKE ? THEN 1 ELSE 0 END"
		args = append(args, "%"+termLower+"%")
		// Directory path match (for directory queries)
		scoreExpr += " + CASE WHEN LOWER(name) LIKE ? THEN 1 ELSE 0 END"
		args = append(args, "%/"+termLower+"/%")
	}

	// Also try directory-based search: if hint matches a known directory name,
	// return files in that directory.
	dirQuery := `SELECT id, name, type,
		json_extract(metadata,'$.role') as role,
		json_extract(metadata,'$.domain') as domain,
		json_extract(metadata,'$.kind') as kind,
		json_extract(metadata,'$.content_type') as content_type,
		access_count, knowledge_depth
		FROM entities
		WHERE type = 'File' AND fact_status != 'deleted' AND name LIKE ? LIMIT 20`
	dirArgs := []any{"%/" + strings.ToLower(hint) + "/%"}

	query := fmt.Sprintf(`
		SELECT id, name, type,
		       json_extract(metadata,'$.role') as role,
		       json_extract(metadata,'$.domain') as domain,
		       json_extract(metadata,'$.kind') as kind,
		       json_extract(metadata,'$.content_type') as content_type,
		       access_count, knowledge_depth,
		       (%s) as score
		FROM entities
		WHERE type IN ('File', 'Directory') AND fact_status != 'deleted'
		HAVING score > 0
		ORDER BY score DESC, knowledge_depth DESC, access_count DESC
		LIMIT 20`, scoreExpr)

	rows, err := m.db.Query(query, args...)
	if err != nil {
		return "[SWL] Label search error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	var ids []string
	scoredRows := 0
	for rows.Next() {
		var id, name, etype string
		var role, domain, kind, ct sql.NullString
		var accessCount, depth int
		var score int
		if err := rows.Scan(&id, &name, &etype, &role, &domain, &kind, &ct, &accessCount, &depth, &score); err != nil {
			continue
		}
		if score == 0 {
			continue
		}
		scoredRows++
		ids = append(ids, id)
		line := fmt.Sprintf("  [%s] %s", etype, name)
		var tags []string
		if role.Valid && role.String != "" {
			tags = append(tags, "role:"+role.String)
		}
		if domain.Valid && domain.String != "" {
			tags = append(tags, "domain:"+domain.String)
		}
		if kind.Valid && kind.String != "" {
			tags = append(tags, "kind:"+kind.String)
		}
		if ct.Valid && ct.String != "" {
			tags = append(tags, "type:"+ct.String)
		}
		if len(tags) > 0 {
			line += "  (" + strings.Join(tags, ", ") + ")"
		}
		out = append(out, line)
	}
	_ = rows.Err()

	// Fallback: directory-based search if no label matches
	if scoredRows == 0 {
		dirRows, dirErr := m.db.Query(dirQuery, dirArgs...)
		if dirErr == nil {
			defer dirRows.Close()
			for dirRows.Next() {
				var id, name, etype string
				var role, domain, kind, ct sql.NullString
				var accessCount, depth int
				if dirRows.Scan(&id, &name, &etype, &role, &domain, &kind, &ct, &accessCount, &depth) == nil {
					ids = append(ids, id)
					line := "  [File] " + name
					var tags []string
					if role.Valid && role.String != "" {
						tags = append(tags, "role:"+role.String)
					}
					if domain.Valid && domain.String != "" {
						tags = append(tags, "domain:"+domain.String)
					}
					if ct.Valid && ct.String != "" {
						tags = append(tags, "type:"+ct.String)
					}
					if len(tags) > 0 {
						line += "  (" + strings.Join(tags, ", ") + ")"
					}
					out = append(out, line)
				}
			}
			_ = dirRows.Err()
		}
	}

	if len(out) == 0 {
		// Suggest creating a label rule for the query
		return fmt.Sprintf("[SWL] No files found matching %q.\n"+
			"Hint: The graph knows about these roles: authentication, api, service, configuration, "+
			"database, middleware, test, logging, metrics, and more.\n"+
			"Try: 'where is authentication code' or 'where are the entry points'.",
			hint)
	}
	m.BumpAccessCount(ids)
	return "[SWL] Files matching '" + hint + "':\n" + strings.Join(out, "\n")
}

// extractSearchTerms splits a query hint into significant search terms.
// Strips stop words and normalizes to lowercase.
func extractSearchTerms(hint string) []string {
	stop := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "in": true, "of": true,
		"for": true, "to": true, "and": true, "or": true, "what": true, "how": true,
		"where": true, "find": true, "show": true, "list": true, "get": true,
		"that": true, "which": true, "are": true, "file": true, "files": true,
		"code": true, "logic": true, "my": true, "this": true, "all": true,
		"some": true, "any": true, "do": true, "does": true,
	}
	words := strings.Fields(strings.ToLower(hint))
	terms := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" || len(w) < 2 {
			continue
		}
		if stop[w] {
			continue
		}
		terms = append(terms, w)
	}
	return terms
}

// askWorkspacePurpose returns descriptions from anchor File entities and
// any session goals, giving the LLM immediate context about the workspace.
func (m *Manager) askWorkspacePurpose() string {
	rows, err := m.db.Query(`
		SELECT name, json_extract(metadata,'$.description'), json_extract(metadata,'$.module'),
		       json_extract(metadata,'$.name'), json_extract(metadata,'$.kind')
		FROM entities
		WHERE type = 'File' AND json_extract(metadata,'$.kind') IN ('anchor','manifest')
		  AND fact_status != 'deleted'
		ORDER BY knowledge_depth DESC, access_count DESC LIMIT 10`,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	var ids []string
	for rows.Next() {
		var name string
		var desc, module, pkgName, kind sql.NullString
		if rows.Scan(&name, &desc, &module, &pkgName, &kind) != nil {
			continue
		}
		ids = append(ids, entityID(KnownTypeFile, name))
		line := "  " + name
		if module.Valid && module.String != "" {
			line += " [module: " + module.String + "]"
		} else if pkgName.Valid && pkgName.String != "" {
			line += " [" + pkgName.String + "]"
		}
		if desc.Valid && desc.String != "" {
			line += "\n    " + truncate(desc.String, 200)
		}
		out = append(out, line)
	}
	_ = rows.Err()

	// Also pull the most recent session goal.
	var goal sql.NullString
	m.db.QueryRow( //nolint:errcheck
		`SELECT goal FROM sessions WHERE goal IS NOT NULL ORDER BY started_at DESC LIMIT 1`,
	).Scan(&goal)
	if goal.Valid && goal.String != "" {
		out = append(out, "  Last session goal: "+goal.String)
	}

	if len(out) == 0 {
		return "[SWL] No workspace purpose information yet.\n" +
			"Run query_swl {\"scan\":true} to index the workspace, or use\n" +
			"query_swl {\"assert\":\"<purpose>\", \"subject\":\"workspace\"} to record it."
	}
	m.BumpAccessCount(ids)
	return "[SWL] Workspace purpose:\n" + strings.Join(out, "\n")
}

// askSemanticAreas returns the classified semantic areas of the workspace.
// After entity consolidation, semantic areas are Directory entities with is_semantic_area=true.
func (m *Manager) askSemanticAreas() string {
	rows, err := m.db.Query(`
		SELECT name, json_extract(metadata,'$.content_type'), json_extract(metadata,'$.documented'),
		       json_extract(metadata,'$.description')
		FROM entities
		WHERE type = 'Directory' AND json_extract(metadata,'$.is_semantic_area') = 1
		  AND fact_status != 'deleted'
		ORDER BY name LIMIT 30`,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	var ids []string
	for rows.Next() {
		var name string
		var contentType, documented, desc sql.NullString
		if rows.Scan(&name, &contentType, &documented, &desc) != nil {
			continue
		}
		ids = append(ids, entityID(KnownTypeDirectory, name))
		line := "  " + name
		if contentType.Valid && contentType.String != "" {
			line += " [" + contentType.String + "]"
		}
		if documented.Valid && documented.String == "1" {
			line += " ✓"
		}
		if desc.Valid && desc.String != "" {
			line += "\n    " + truncate(desc.String, 120)
		}
		out = append(out, line)
	}
	_ = rows.Err()
	if len(out) == 0 {
		return "[SWL] No semantic areas indexed yet. Run query_swl {\"scan\":true}."
	}
	m.BumpAccessCount(ids)
	return "[SWL] Semantic areas:\n" + strings.Join(out, "\n")
}

// askAnchorDocuments returns known anchor documents with their descriptions.
// After entity consolidation, anchor docs are File entities with kind="anchor" or "manifest".
func (m *Manager) askAnchorDocuments() string {
	rows, err := m.db.Query(`
		SELECT name, json_extract(metadata,'$.description'), json_extract(metadata,'$.kind')
		FROM entities
		WHERE type = 'File' AND json_extract(metadata,'$.kind') IN ('anchor','manifest')
		  AND fact_status != 'deleted'
		ORDER BY knowledge_depth DESC, name LIMIT 20`,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	var ids []string
	for rows.Next() {
		var name string
		var desc, kind sql.NullString
		if rows.Scan(&name, &desc, &kind) != nil {
			continue
		}
		ids = append(ids, entityID(KnownTypeFile, name))
		line := "  " + name
		if kind.Valid && kind.String != "" {
			line += " [" + kind.String + "]"
		}
		if desc.Valid && desc.String != "" {
			line += ": " + truncate(desc.String, 120)
		}
		out = append(out, line)
	}
	_ = rows.Err()
	if len(out) == 0 {
		return "[SWL] No anchor documents indexed yet. Run query_swl {\"scan\":true}."
	}
	m.BumpAccessCount(ids)
	return "[SWL] Anchor documents:\n" + strings.Join(out, "\n")
}

// askFileDetail returns what is known about a specific file: its description,
// symbols, and tasks.  If the file has only been structurally indexed (not yet
// read by a tool call), it says so explicitly rather than returning empty results.
func (m *Manager) askFileDetail(hint string) string {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return "[SWL] Please specify a file name."
	}

	// Find the file entity.
	var fileID, fileName string
	var depth int
	var desc sql.NullString
	err := m.db.QueryRow(`
		SELECT id, name, knowledge_depth, json_extract(metadata,'$.description')
		FROM entities
		WHERE type = ? AND fact_status != 'deleted' AND name LIKE ?
		ORDER BY knowledge_depth DESC LIMIT 1`,
		KnownTypeFile, "%"+hint+"%",
	).Scan(&fileID, &fileName, &depth, &desc)
	if err != nil {
		return fmt.Sprintf("[SWL] No file found matching %q.", hint)
	}

	m.BumpAccessCount([]string{fileID})

	out := fmt.Sprintf("[SWL] %s (depth %d):", fileName, depth)
	if desc.Valid && desc.String != "" {
		out += "\n  Description: " + desc.String
	}

	// If knowledge_depth == 1, content has never been extracted.
	if depth <= 1 {
		out += "\n  ⚠ This file has only been structurally indexed." +
			"\n    Read it with read_file to populate symbols, imports, and tasks."
		return out
	}

	// Symbols.
	symRows, err := m.db.Query(`
		SELECT e.name FROM entities e
		JOIN edges ed ON ed.to_id = e.id AND ed.rel = 'defines'
		WHERE e.type = ? AND e.fact_status != 'deleted' AND ed.from_id = ?
		ORDER BY e.access_count DESC LIMIT 15`, KnownTypeSymbol, fileID)
	if err == nil {
		defer symRows.Close()
		var syms []string
		for symRows.Next() {
			var s string
			if symRows.Scan(&s) == nil {
				syms = append(syms, s)
			}
		}
		_ = symRows.Err()
		if len(syms) > 0 {
			out += "\n  Symbols: " + strings.Join(syms, ", ")
		}
	}

	// Tasks.
	taskRows, err := m.db.Query(`
		SELECT e.name FROM entities e
		JOIN edges ed ON ed.to_id = e.id AND ed.rel = 'has_task'
		WHERE e.type = ? AND e.fact_status != 'deleted' AND ed.from_id = ?
		ORDER BY e.modified_at DESC LIMIT 5`, KnownTypeTask, fileID)
	if err == nil {
		defer taskRows.Close()
		var tasks []string
		for taskRows.Next() {
			var t string
			if taskRows.Scan(&t) == nil {
				tasks = append(tasks, truncate(t, 80))
			}
		}
		_ = taskRows.Err()
		if len(tasks) > 0 {
			out += "\n  Tasks: " + strings.Join(tasks, "; ")
		}
	}

	// Assertions recorded directly in entity metadata.
	var metaStr string
	_ = m.db.QueryRow("SELECT metadata FROM entities WHERE id = ?", fileID).Scan(&metaStr)
	if metaStr != "" && metaStr != "{}" {
		var meta map[string]any
		if json.Unmarshal([]byte(metaStr), &meta) == nil {
			if raw, ok := meta["assertions"]; ok {
				if b, err2 := json.Marshal(raw); err2 == nil {
					var entries []assertionEntry
					if json.Unmarshal(b, &entries) == nil && len(entries) > 0 {
						var notes []string
						for _, e := range entries {
							notes = append(notes, fmt.Sprintf("%s (%.2f)", truncate(e.Text, 100), e.Conf))
						}
						out += "\n  Notes: " + strings.Join(notes, "; ")
					}
				}
			}
		}
	}

	return out
}

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
	_ = rows.Err()
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
		`SELECT t.name, f.name FROM entities t
		 LEFT JOIN edges e ON e.to_id = t.id AND e.rel = 'has_task'
		 LEFT JOIN entities f ON f.id = e.from_id AND f.type = 'File'
		 WHERE t.type = ? AND t.fact_status != 'deleted'
		 ORDER BY t.modified_at DESC LIMIT 30`, KnownTypeTask,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var task string
		var file sql.NullString
		if rows.Scan(&task, &file) == nil {
			line := "  " + truncate(task, 80)
			if file.Valid && file.String != "" {
				line += " [" + file.String + "]"
			}
			out = append(out, line)
		}
	}
	_ = rows.Err()
	if len(out) == 0 {
		return "[SWL] No open tasks found."
	}
	return "[SWL] Open tasks:\n" + strings.Join(out, "\n")
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
	_ = rows.Err()
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
	_ = rows.Err()
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
	_ = rows.Err()
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

// askSessionActivity returns a summary of activity in the most recent session:
// which files were touched, what operations were performed, and any Assertions recorded.
func (m *Manager) askSessionActivity() string {
	var sessionID string
	err := m.db.QueryRow(
		`SELECT id FROM sessions ORDER BY started_at DESC LIMIT 1`,
	).Scan(&sessionID)
	if err != nil {
		return "[SWL] No sessions recorded yet."
	}

	actRows, err := m.db.Query(`
		SELECT e.rel, en.name, en.type FROM edges e
		JOIN entities en ON en.id = e.from_id
		WHERE e.rel IN ('written_in','edited_in','appended_in','read','fetched','executed','deleted','listed')
		  AND e.source_session = ?
		ORDER BY e.rel, en.name LIMIT 50`, sessionID)
	if err != nil {
		return "[SWL] Activity query error: " + err.Error()
	}
	defer actRows.Close()

	byRel := map[string][]string{}
	for actRows.Next() {
		var rel, name, typ string
		if actRows.Scan(&rel, &name, &typ) == nil {
			byRel[rel] = append(byRel[rel], truncate(name, 60))
		}
	}
	_ = actRows.Err()

	if len(byRel) == 0 {
		return "[SWL] No activity recorded in the most recent session yet."
	}

	out := "[SWL] Last session activity:\n"
	for _, rel := range []string{"written_in", "edited_in", "read", "fetched", "executed", "listed", "deleted"} {
		if items, ok := byRel[rel]; ok {
			out += fmt.Sprintf("  %-12s %s\n", rel+":", strings.Join(items, ", "))
		}
	}

	// Intent(s) the session was pursuing — via Session --intended_for--> Intent edges.
	intentRows, err := m.db.Query(`
		SELECT en.name FROM edges e
		JOIN entities en ON en.id = e.to_id AND en.type = 'Intent'
		WHERE e.from_id = ? AND e.rel = 'intended_for'
		ORDER BY en.modified_at DESC LIMIT 3`, sessionID)
	if err == nil {
		defer intentRows.Close()
		var intents []string
		for intentRows.Next() {
			var name string
			if intentRows.Scan(&name) == nil {
				intents = append(intents, truncate(name, 80))
			}
		}
		_ = intentRows.Err()
		if len(intents) > 0 {
			out += "\n  Pursuing: " + strings.Join(intents, "; ")
		}
	}

	// SubAgents spawned in this session — via SubAgent --spawned_by--> Session edges.
	subRows, err := m.db.Query(`
		SELECT en.name FROM edges e
		JOIN entities en ON en.id = e.from_id AND en.type = 'SubAgent'
		WHERE e.to_id = ? AND e.rel = 'spawned_by'
		ORDER BY en.modified_at DESC LIMIT 5`, sessionID)
	if err == nil {
		defer subRows.Close()
		var subs []string
		for subRows.Next() {
			var name string
			if subRows.Scan(&name) == nil {
				subs = append(subs, truncate(name, 60))
			}
		}
		_ = subRows.Err()
		if len(subs) > 0 {
			out += "\n  SubAgents: " + strings.Join(subs, ", ")
		}
	}

	// Entities with recorded insights (assertions in metadata).
	insightRows, err := m.db.Query(`
		SELECT name FROM entities
		WHERE json_extract(metadata,'$.assertions') IS NOT NULL
		  AND fact_status != 'deleted'
		ORDER BY modified_at DESC LIMIT 5`)
	if err == nil {
		defer insightRows.Close()
		var insights []string
		for insightRows.Next() {
			var name string
			if insightRows.Scan(&name) == nil {
				insights = append(insights, truncate(name, 60))
			}
		}
		_ = insightRows.Err()
		if len(insights) > 0 {
			out += "\n  Insights on: " + strings.Join(insights, ", ")
		}
	}

	return strings.TrimRight(out, "\n")
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
	_ = rows.Err()
	if len(out) == 0 {
		return "[SWL] No sessions recorded yet."
	}
	return "[SWL] Sessions:\n" + strings.Join(out, "\n")
}

// askModelReliability aggregates per-model assertion statistics from entity metadata.
// It counts assertions by model_id, how many were confirmed (≥2 distinct sessions),
// and the average confidence — giving a rough per-model reliability signal.
func (m *Manager) askModelReliability() string {
	rows, err := m.db.Query(
		`SELECT metadata FROM entities WHERE metadata LIKE '%"assertions"%' AND fact_status != 'deleted'`,
	)
	if err != nil {
		return "[SWL] Query error: " + err.Error()
	}
	defer rows.Close()

	type modelStats struct {
		total     int
		confirmed int
		confSum   float64
	}
	stats := map[string]*modelStats{}

	for rows.Next() {
		var metaStr string
		if rows.Scan(&metaStr) != nil {
			continue
		}
		var meta map[string]any
		if json.Unmarshal([]byte(metaStr), &meta) != nil {
			continue
		}
		raw, ok := meta["assertions"]
		if !ok {
			continue
		}
		b, _ := json.Marshal(raw)
		var entries []assertionEntry
		if json.Unmarshal(b, &entries) != nil {
			continue
		}
		// Count distinct sessions per entity to detect confirmed assertions.
		sessionsSeen := map[string]bool{}
		for _, e := range entries {
			sessionsSeen[e.Sid] = true
		}
		confirmed := len(sessionsSeen) >= 2
		for _, e := range entries {
			mid := e.ModelID
			if mid == "" {
				mid = "(unknown)"
			}
			if stats[mid] == nil {
				stats[mid] = &modelStats{}
			}
			stats[mid].total++
			stats[mid].confSum += e.Conf
			if confirmed {
				stats[mid].confirmed++
			}
		}
	}
	_ = rows.Err()

	if len(stats) == 0 {
		return "[SWL] No per-model assertion data yet. Assertions recorded after this version will carry model identifiers."
	}

	var lines []string
	lines = append(lines, "[SWL] Per-model assertion reliability:")
	lines = append(lines, fmt.Sprintf("  %-40s  %6s  %9s  %8s", "Model", "Total", "Confirmed", "Avg Conf"))
	lines = append(lines, "  "+strings.Repeat("-", 70))
	for mid, s := range stats {
		rate := 0.0
		if s.total > 0 {
			rate = s.confSum / float64(s.total)
		}
		lines = append(lines, fmt.Sprintf("  %-40s  %6d  %9d  %8.2f",
			truncate(mid, 40), s.total, s.confirmed, rate))
	}
	return strings.Join(lines, "\n")
}

// --- Tier 2: named SQL templates ---

type tier2Template struct {
	keywords []string
	query    string
	// argFn returns the SQL arguments for this specific template.
	// nil means the query takes no parameters.
	argFn func(question string) []any
}

var sqlTemplates = map[string]tier2Template{
	"dependency_chain": {
		keywords: []string{"chain", "transitive", "recursive"},
		query: `WITH RECURSIVE chain(id, depth) AS (
			SELECT to_id, 0 FROM edges WHERE from_id = (SELECT id FROM entities WHERE name LIKE ? LIMIT 1) AND rel = 'imports'
			UNION ALL
			SELECT e.to_id, c.depth+1 FROM edges e JOIN chain c ON e.from_id = c.id WHERE c.depth < 8
		) SELECT DISTINCT e.name, c.depth FROM chain c JOIN entities e ON e.id = c.id ORDER BY c.depth LIMIT 50`,
		argFn: func(question string) []any {
			terms := tier3Terms(question)
			if len(terms) > 0 {
				return []any{"%" + terms[0] + "%"}
			}
			return []any{"%" + question + "%"}
		},
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

func (m *Manager) tryTier3(question string) string {
	terms := tier3Terms(question)
	// cap at 3 for query specificity
	if len(terms) > 3 {
		terms = terms[:3]
	}
	if len(terms) == 0 {
		return ""
	}

	// Each term matches against name OR metadata (description, module, etc.)
	q := `SELECT type, name, fact_status FROM entities WHERE fact_status != 'deleted'`
	args := make([]any, 0, len(terms)*2)
	for _, t := range terms {
		q += ` AND (name LIKE ? OR metadata LIKE ?)`
		args = append(args, "%"+t+"%", "%"+t+"%")
	}
	q += " ORDER BY knowledge_depth DESC, access_count DESC LIMIT 15"

	rows, err := m.db.Query(q, args...)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var etype, name, status string
		if rows.Scan(&etype, &name, &status) == nil {
			out = append(out, fmt.Sprintf("  [%s] %s (%s)", etype, name, status))
		}
	}
	_ = rows.Err()
	if len(out) == 0 {
		return ""
	}
	return "[SWL] Freetext matches for " + strings.Join(terms, "+") + ":\n" + strings.Join(out, "\n")
}

// recordQueryGap upserts a question into query_gaps, incrementing count on
// repeat. Returns true if this is the first occurrence of this question.
func (m *Manager) recordQueryGap(question string) bool {
	terms := tier3Terms(question)
	id := contentHash("gap:" + strings.ToLower(strings.TrimSpace(question)))
	now := nowSQLite()
	m.writer.mu.Lock()
	m.db.Exec( //nolint:errcheck
		`INSERT INTO query_gaps (id, question, terms, count, first_at, last_at)
		 VALUES (?, ?, ?, 1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET count = count + 1, last_at = excluded.last_at`,
		id, question, strings.Join(terms, ","), now, now,
	)
	m.writer.mu.Unlock()
	var count int
	_ = m.db.QueryRow("SELECT count FROM query_gaps WHERE id = ?", id).Scan(&count)
	return count == 1
}

// fallthroughResponse returns the message shown when all tiers miss.
// On repeat miss, surfaces any generated rule suggestion inline.
func (m *Manager) fallthroughResponse(question string, isNewGap bool) string {
	if isNewGap {
		return fmt.Sprintf(
			"[SWL] Query not yet indexed: %q\n"+
				"Tried: Tier 1 (intent patterns), Tier 2 (SQL templates), Tier 3 (freetext) — no match.\n"+
				"Next steps:\n"+
				"  query_swl {\"scan\":true}  — index the workspace\n"+
				"  query_swl {\"help\":true}  — see full query syntax\n"+
				"  query_swl {\"gaps\":true}  — view recurring misses\n"+
				"(Query recorded — repeated misses generate candidate rules automatically.)",
			question,
		)
	}

	// On repeat miss, look up existing suggestion to surface inline.
	gapID := contentHash("gap:" + strings.ToLower(strings.TrimSpace(question)))
	var suggestion string
	m.db.QueryRow("SELECT suggestion FROM query_gaps WHERE id = ?", gapID).Scan(&suggestion) //nolint:errcheck

	base := fmt.Sprintf("[SWL] Still no match for %q — workspace may not be indexed yet. Try query_swl {\"scan\":true}.", question)
	if suggestion != "" {
		return base + "\n\n[SWL] Suggested rule (paste into {workspace}/.swl/swl.rules.yaml):\n" + suggestion +
			"\nThen: query_swl {\"reload_config\":true}"
	}
	return base
}

// tier3Terms extracts significant words from a question (shared by tryTier3
// and recordQueryGap).
func tier3Terms(question string) []string {
	stop := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "in": true, "of": true,
		"for": true, "to": true, "and": true, "or": true, "what": true, "how": true,
		"where": true, "find": true, "show": true, "list": true, "get": true,
	}
	var terms []string
	for _, w := range strings.Fields(strings.ToLower(question)) {
		if !stop[w] && len(w) > 2 {
			terms = append(terms, w)
		}
		if len(terms) == 5 {
			break
		}
	}
	return terms
}

func (m *Manager) tryTier2(question string) string {
	q := strings.ToLower(question)
	for _, tmpl := range sqlTemplates {
		for _, kw := range tmpl.keywords {
			if strings.Contains(q, kw) {
				var args []any
				if tmpl.argFn != nil {
					args = tmpl.argFn(question)
				}
				rows, err := m.db.Query(tmpl.query, args...)
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
	_ = rows.Err()
	var edgeCount int
	m.db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&edgeCount) //nolint:errcheck
	out = append(out, fmt.Sprintf("\n  Edges: %d", edgeCount))
	return "[SWL] Graph stats:\n" + strings.Join(out, "\n")
}

// IndexStatus returns a summary of indexing coverage: how many files are indexed,
// how many are unindexed (no knowledge_depth), and which directories have gaps.
func (m *Manager) IndexStatus() string {
	var totalFiles, indexedFiles int
	m.db.QueryRow(`SELECT COUNT(*) FROM entities WHERE type='File' AND fact_status!='deleted'`).Scan(&totalFiles)                              //nolint:errcheck
	m.db.QueryRow(`SELECT COUNT(*) FROM entities WHERE type='File' AND fact_status!='deleted' AND knowledge_depth>0`).Scan(&indexedFiles)     //nolint:errcheck

	var staleFiles int
	m.db.QueryRow(`SELECT COUNT(*) FROM entities WHERE type='File' AND fact_status='stale'`).Scan(&staleFiles) //nolint:errcheck

	var symbolCount, taskCount, sectionCount int
	m.db.QueryRow(`SELECT COUNT(*) FROM entities WHERE type='Symbol' AND fact_status!='deleted'`).Scan(&symbolCount)  //nolint:errcheck
	m.db.QueryRow(`SELECT COUNT(*) FROM entities WHERE type='Task' AND fact_status!='deleted'`).Scan(&taskCount)      //nolint:errcheck
	m.db.QueryRow(`SELECT COUNT(*) FROM entities WHERE type='Section' AND fact_status!='deleted'`).Scan(&sectionCount) //nolint:errcheck

	// Top directories by unindexed file count.
	gapRows, err := m.db.Query(`
		SELECT d.name, COUNT(f.id) as gap
		FROM entities f
		JOIN edges e ON e.from_id = f.id AND e.rel = 'in_dir'
		JOIN entities d ON d.id = e.to_id AND d.type = 'Directory'
		WHERE f.type = 'File' AND f.fact_status != 'deleted' AND f.knowledge_depth = 0
		GROUP BY d.name ORDER BY gap DESC LIMIT 5`)
	var gapLines []string
	if err == nil {
		defer gapRows.Close()
		for gapRows.Next() {
			var dir string
			var cnt int
			if gapRows.Scan(&dir, &cnt) == nil {
				gapLines = append(gapLines, fmt.Sprintf("  %s (%d unindexed)", dir, cnt))
			}
		}
	}

	lines := []string{
		fmt.Sprintf("[SWL] Index coverage: %d/%d files indexed (%.0f%%)", indexedFiles, totalFiles,
			pct(indexedFiles, totalFiles)),
		fmt.Sprintf("  Stale: %d  Symbols: %d  Tasks: %d  Sections: %d",
			staleFiles, symbolCount, taskCount, sectionCount),
	}
	if len(gapLines) > 0 {
		lines = append(lines, "  Dirs with unindexed files:")
		lines = append(lines, gapLines...)
		lines = append(lines, `  → run query_swl {"scan":true} to index`)
	} else {
		lines = append(lines, "  All known files indexed.")
	}
	return strings.Join(lines, "\n")
}

func pct(n, total int) float64 {
	if total == 0 {
		return 100
	}
	return float64(n) / float64(total) * 100
}

// HelpText returns a concise query syntax reference for LLMs operating under
// compressed context that may have lost the tool description.
func (m *Manager) HelpText() string {
	return `[SWL] Query syntax reference (query_swl):
  {"resume":true}                          — session resume digest
  {"question":"what does foo.go do?"}      — natural-language (Tier 1/2/3)
  {"stats":true}                           — entity/edge counts by type
  {"gaps":true}                            — unknown/low-confidence entities
  {"stale":true}                           — stale/outdated entities
  {"scan":true}                            — incremental workspace index
  {"scan":true,"root":"pkg/swl"}           — scan a subdirectory
  {"index_status":true}                    — indexing coverage (files indexed vs. missing)
  {"sql":"SELECT name FROM entities ..."}  — raw read-only SQL (200-row cap)
  {"assert":"fact","subject":"X"}          — record a verified fact
  {"suggest":true}                         — rule suggestions from recurring misses
  {"reload_config":true}                   — reload swl.rules.yaml/swl.query.yaml in-process
  {"schema":true}                          — DB schema and entity types
  {"debug":true}                           — last 64 inference events
  {"help":true}                            — this reference

Tip: always call query_swl before re-reading files — the graph may already know.`
}

// KnowledgeGaps returns entities with low confidence or unknown status,
// plus recurring missed queries and their rule suggestions (Phase C).
func (m *Manager) KnowledgeGaps() string {
	// Entity gaps.
	rows, err := m.db.Query(
		`SELECT type, name, confidence, fact_status FROM entities
		 WHERE (confidence < 0.85 OR fact_status = 'unknown') AND fact_status != 'deleted'
		 ORDER BY confidence ASC LIMIT 20`,
	)
	if err != nil {
		return "[SWL] Gaps error: " + err.Error()
	}
	defer rows.Close()

	var entityGaps []string
	for rows.Next() {
		var t, name, status string
		var conf float64
		if rows.Scan(&t, &name, &conf, &status) == nil {
			entityGaps = append(entityGaps, fmt.Sprintf("  [%s] %.2f %s  %s", status, conf, t, name))
		}
	}
	_ = rows.Err()

	// Query gaps with suggestions (Phase C).
	analysis := m.AnalyzeGaps(3)

	var sections []string

	if len(entityGaps) > 0 {
		sections = append(
			sections,
			"[SWL] Entity gaps (low confidence / unknown status):\n"+strings.Join(entityGaps, "\n"),
		)
	}

	if len(analysis.MissedQuestions) > 0 {
		var qgaps []string
		for _, g := range analysis.MissedQuestions {
			flag := ""
			if g.Suggestion != "" && g.RuleKind != "none" {
				flag = " → rule candidate"
			}
			qgaps = append(qgaps, fmt.Sprintf("  [%dx] %s%s", g.Count, g.Question, flag))
		}
		sections = append(sections, "[SWL] Missed queries (repeated ≥3×):\n"+strings.Join(qgaps, "\n"))

		// Include ready-to-paste YAML rule suggestions inline so the LLM can act immediately.
		if len(analysis.RuleSuggestions) > 0 {
			yamlBlocks := make([]string, 0, len(analysis.RuleSuggestions))
			for i, s := range analysis.RuleSuggestions {
				yamlBlocks = append(yamlBlocks, fmt.Sprintf(
					"# Suggestion %d (%s) — %s\n%s", i+1, s.Kind, s.Why, s.YAML,
				))
			}
			sections = append(sections,
				"[SWL] Rule suggestions (paste into {workspace}/.swl/swl.rules.yaml):\n"+
					strings.Join(yamlBlocks, "\n\n"),
			)
		}
	}

	if len(sections) == 0 {
		return "[SWL] No significant knowledge gaps."
	}
	return strings.Join(sections, "\n\n")
}

// SuggestRules returns actionable rule suggestions derived from recurring query gaps.
// Each suggestion is a ready-to-paste YAML snippet for swl.rules.yaml.
// Call this to see what rules would improve the query engine.
func (m *Manager) SuggestRules() string {
	analysis := m.AnalyzeGaps(3)
	if len(analysis.RuleSuggestions) == 0 {
		return "[SWL] No rule suggestions — no recurring query gaps (≥3 misses) yet."
	}
	out := make([]string, 0, len(analysis.RuleSuggestions)+1)
	out = append(out, "[SWL] Rule suggestions (add to {workspace}/.swl/swl.rules.yaml):\n")
	for i, s := range analysis.RuleSuggestions {
		out = append(out, fmt.Sprintf("\n--- Suggestion %d (%s) ---\n  Why: %s\n  %s",
			i+1, s.Kind, s.Why, s.YAML))
	}
	return strings.Join(out, "\n")
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

// assertionEntry is one fact recorded against a workspace entity.
type assertionEntry struct {
	Text    string  `json:"text"`
	Conf    float64 `json:"conf"`
	Sid     string  `json:"sid"`               // SWL session UUID
	At      string  `json:"at"`                // RFC3339 timestamp
	ModelID string  `json:"model_id,omitempty"` // LLM model that stated this fact
}

// appendAssertionToMeta appends or updates a fact in the entity's metadata.assertions
// array. It is the sole write path for LLM-stated facts.
//
//   - Deduplication: same session (Sid) → overwrite existing entry, do not duplicate.
//   - Contradiction: different session with different text → returns true; caller
//     halves the confidence.
//   - Cap: the array is capped at 10 entries; lowest-confidence entry is evicted.
//   - Side effects: boosts entity confidence by conf*0.05; if ≥2 distinct sessions
//     have asserted about this entity, promotes fact_status to verified.
func (m *Manager) appendAssertionToMeta(subjectID, text, sessionID string, conf float64) (contradicted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var metaStr string
	_ = m.db.QueryRow("SELECT metadata FROM entities WHERE id = ?", subjectID).Scan(&metaStr)

	var meta map[string]any
	if metaStr != "" {
		_ = json.Unmarshal([]byte(metaStr), &meta)
	}
	if meta == nil {
		meta = map[string]any{}
	}

	var existing []assertionEntry
	if raw, ok := meta["assertions"]; ok {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &existing)
		}
	}

	// Contradiction: different session, different text.
	for _, e := range existing {
		if e.Sid != sessionID && e.Text != text {
			contradicted = true
			conf /= 2
			break
		}
	}

	now := nowSQLite()

	// Resolve model ID for this session (set by AfterLLM hook).
	var modelID string
	if v, ok := m.sessionModels.Load(sessionID); ok {
		modelID, _ = v.(string)
	}

	entry := assertionEntry{Text: text, Conf: conf, Sid: sessionID, At: now, ModelID: modelID}

	// Deduplication: same session → update existing entry.
	found := false
	for i, e := range existing {
		if e.Sid == sessionID {
			existing[i] = entry
			found = true
			break
		}
	}
	if !found {
		// Prepend so newest is first.
		existing = append([]assertionEntry{entry}, existing...)
	}

	// Cap at 10: evict lowest-confidence entry.
	const maxAssertions = 10
	if len(existing) > maxAssertions {
		minIdx, minConf := 0, existing[0].Conf
		for i, e := range existing {
			if e.Conf < minConf {
				minConf = e.Conf
				minIdx = i
			}
		}
		existing = append(existing[:minIdx], existing[minIdx+1:]...)
	}

	meta["assertions"] = existing
	b, _ := json.Marshal(meta)

	// Count distinct sessions to decide on verification.
	sessionsSeen := map[string]bool{}
	for _, e := range existing {
		if e.Sid != "" {
			sessionsSeen[e.Sid] = true
		}
	}

	boost := conf * 0.05
	_, _ = m.db.Exec(
		`UPDATE entities SET metadata = ?, modified_at = ?, confidence = MIN(confidence + ?, 1.0) WHERE id = ?`,
		string(b), now, boost, subjectID,
	)
	if len(sessionsSeen) >= 2 {
		_, _ = m.db.Exec(
			"UPDATE entities SET fact_status = ? WHERE id = ? AND fact_status != 'deleted'",
			FactVerified, subjectID,
		)
	}
	return contradicted
}

// AssertNote records a free-form fact about a workspace entity directly in that
// entity's metadata.assertions array. Unlike the old Assertion-entity approach,
// no separate graph node or edge is created — the subject entity itself carries
// the knowledge, and its modified_at timestamp advances so the SSE stream delivers
// the enriched entity to all connected clients immediately.
//
// subject is resolved via resolveSubjectEntity (exact ID → exact path → fuzzy
// file name → exact symbol/directory name). If no entity is found, an error
// is returned — no phantom nodes are created.
//
// sessionKey is the caller's picoclaw session key (may be empty for programmatic
// callers); it is mapped to a SWL session UUID for cross-session tracking.
func (m *Manager) AssertNote(subject, content string, confidence float64, sessionKey string) string {
	if subject == "" || content == "" {
		return "[SWL] assert requires subject and content."
	}
	if confidence <= 0 {
		confidence = 0.85
	}

	subjectID, resolvedType := m.resolveSubjectEntity(subject)
	if subjectID == "" {
		return fmt.Sprintf(
			`[SWL] Subject %q not found in graph.`+"\n"+
				`  Use: query_swl {"question":"%s"} to find the exact entity name, then retry.`,
			subject, subject,
		)
	}

	sessionID := m.EnsureSession(sessionKey)
	contradicted := m.appendAssertionToMeta(subjectID, content, sessionID, confidence)

	shortID := subjectID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	note := ""
	if contradicted {
		note = " ⚠ Contradicts existing assertion — confidence halved."
	}
	return fmt.Sprintf(
		"[SWL] Recorded on %s %q [id:%s] at confidence %.2f.%s",
		resolvedType, subject, shortID, confidence, note,
	)
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
	_ = rows.Err()
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
