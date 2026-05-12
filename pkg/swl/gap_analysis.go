package swl

import (
	"strings"
)

// GapAnalysis holds the result of gap analysis, including rule suggestions.
type GapAnalysis struct {
	MissedQuestions []GapEntry
	RuleSuggestions []RuleSuggestion
}

// GapEntry is a single missed query with its analysis.
type GapEntry struct {
	Question   string
	Terms      []string
	Count      int
	Suggestion string
	RuleKind   string // "path_prefix" | "name_pattern" | "new_intent" | "label_match"
}

// RuleSuggestion is a concrete candidate rule to add to swl.rules.yaml.
type RuleSuggestion struct {
	Kind    string // "path_prefix" | "name_pattern" | "content_type" | "query_intent"
	Prefix  string // for path_prefix
	Pattern string // for name_pattern
	Role    string
	Domain  string
	KindLbl string // for kind label
	YAML    string // ready-to-paste YAML snippet
	Why     string // human-readable reason
}

// AnalyzeGaps reads all query_gaps with count >= threshold and produces
// rule suggestions for the workspace-level swl.rules.yaml.
// threshold controls minimum miss count before suggesting a rule (default: 3).
func (m *Manager) AnalyzeGaps(threshold int) GapAnalysis {
	if threshold <= 0 {
		threshold = 3
	}

	rows, err := m.db.Query(`
		SELECT id, question, terms, count, suggestion
		FROM query_gaps
		WHERE count >= ?
		ORDER BY count DESC, last_at DESC
		LIMIT 50`, threshold)
	if err != nil {
		return GapAnalysis{}
	}
	defer rows.Close()

	var gaps []GapEntry
	for rows.Next() {
		var id, question, terms, suggestion string
		var count int
		if rows.Scan(&id, &question, &terms, &count, &suggestion) == nil {
			gaps = append(gaps, GapEntry{
				Question:   question,
				Terms:      strings.Split(terms, ","),
				Count:      count,
				Suggestion: suggestion,
			})
		}
	}
	_ = rows.Err()

	// Generate suggestions for gaps that don't have one yet.
	suggestions := make([]RuleSuggestion, 0)
	for i := range gaps {
		if gaps[i].Suggestion == "" {
			s := suggestForGap(gaps[i])
			gaps[i].Suggestion = s.YAML
			gaps[i].RuleKind = s.Kind
			suggestions = append(suggestions, s)
			// Persist suggestion back to DB.
			m.persistSuggestion(gaps[i].Question, s.YAML)
		} else {
			// Already has a suggestion; decode rule kind.
			gaps[i].RuleKind = inferRuleKind(gaps[i].Suggestion)
		}
	}

	return GapAnalysis{
		MissedQuestions: gaps,
		RuleSuggestions: suggestions,
	}
}

// persistSuggestion writes a generated suggestion back to query_gaps.
func (m *Manager) persistSuggestion(question, suggestion string) {
	id := contentHash("gap:" + strings.ToLower(strings.TrimSpace(question)))
	m.writer.mu.Lock()
	m.db.Exec( //nolint:errcheck
		`UPDATE query_gaps SET suggestion = ? WHERE id = ?`,
		suggestion, id,
	)
	m.writer.mu.Unlock()
}

// suggestForGap derives a concrete rule suggestion from a gap entry.
// It analyses the question terms and existing graph to produce actionable YAML.
func suggestForGap(gap GapEntry) RuleSuggestion {
	terms := gap.Terms

	// Filter out stop words.
	stop := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "in": true, "of": true,
		"for": true, "to": true, "and": true, "or": true, "what": true, "how": true,
		"where": true, "find": true, "show": true, "list": true, "get": true,
		"file": true, "files": true, "code": true, "logic": true, "this": true,
	}
	var clean []string
	for _, t := range terms {
		if !stop[t] && len(t) > 2 {
			clean = append(clean, t)
		}
	}
	if len(clean) == 0 {
		clean = terms
	}

	// Look for "where is X that handles Y" pattern → path_prefix suggestion.
	if suggest := suggestPathPrefix(clean, gap.Question); suggest.Kind != "" {
		return suggest
	}

	// Look for "which files do X" → name_pattern suggestion.
	if suggest := suggestNamePattern(clean); suggest.Kind != "" {
		return suggest
	}

	// Look for "find X by purpose" → label search refinement (content_type hint).
	if suggest := suggestContentType(clean); suggest.Kind != "" {
		return suggest
	}

	// No actionable rule → return a note.
	return RuleSuggestion{
		Kind: "none",
		YAML: "# No automatic rule possible for: " + strings.Join(clean, " "),
		Why:  "terms do not map to a path or naming convention",
	}
}

// suggestPathPrefix looks for a domain/concept that maps to a directory path.
// e.g. "authentication" → suggest adding path_prefix for "pkg/auth/"
func suggestPathPrefix(terms []string, question string) RuleSuggestion {
	// Domain keyword → conventional path mapping.
	domainToPath := map[string][]string{
		"auth":           {"pkg/auth/", "auth/"},
		"authentication": {"pkg/auth/", "auth/"},
		"security":       {"security/", "pkg/security/"},
		"database":       {"pkg/db/", "pkg/database/"},
		"data":           {"pkg/data/", "pkg/db/"},
		"api":            {"pkg/api/", "pkg/rest/"},
		"http":           {"pkg/http/"},
		"grpc":           {"pkg/grpc/"},
		"config":         {"config/", "configs/"},
		"deploy":         {"pkg/deploy/", "deploy/"},
		"logging":        {"pkg/logging/"},
		"metrics":        {"pkg/metrics/"},
		"telemetry":      {"pkg/telemetry/"},
		"cache":          {"pkg/cache/"},
		"queue":          {"pkg/queue/"},
		"messaging":      {"pkg/msg/", "pkg/events/"},
		"middleware":     {"middleware/", "pkg/middleware/"},
		"model":          {"pkg/model/", "pkg/models/"},
		"schema":         {"pkg/schema/"},
		"validation":     {"pkg/valid/", "pkg/validator/"},
		"test":           {"test/", "tests/"},
		"docs":           {"docs/", "doc/"},
		"script":         {"scripts/"},
		"tool":           {"tools/"},
		"frontend":       {"frontend/", "web/"},
		"ui":             {"ui/", "web/"},
	}

	q := strings.ToLower(question)

	for term, paths := range domainToPath {
		if strings.Contains(q, term) || sliceContains(terms, term) {
			prefix := paths[0]
			role := term
			if term == "auth" || term == "authentication" {
				role = "authentication"
			} else if term == "config" {
				role = "configuration"
			}
			domain := domainForTerm(term)

			suggestion := RuleSuggestion{
				Kind:   "path_prefix",
				Prefix: prefix,
				Role:   role,
				Domain: domain,
				YAML: `  # Added from gap analysis (query: "` + q + `"):
  - prefix: "` + prefix + `"
    role: ` + role + `
    domain: ` + domain,
				Why: "query `" + q + "` missed; " + prefix + " is the conventional path for " + term,
			}
			return suggestion
		}
	}

	// Try to infer from file type keywords → suggest a content_type or name_pattern.
	if suggest := suggestFromFileType(terms); suggest.Kind != "" {
		return suggest
	}

	return RuleSuggestion{}
}

// suggestNamePattern suggests a name_pattern rule from query terms.
// e.g. "config files" → suggest *.config.* pattern.
func suggestNamePattern(terms []string) RuleSuggestion {
	typeToExt := map[string]string{
		"config":  "yaml",
		"configs": "yaml",
		"test":    "go",
		"mock":    "go",
		"sql":     "sql",
		"schema":  "sql",
		"proto":   "proto",
		"script":  "sh",
		"docker":  "dockerfile",
		"make":    "makefile",
	}

	for _, term := range terms {
		if ext, ok := typeToExt[term]; ok {
			role := term
			if term == "config" || term == "configs" {
				role = "configuration"
			}

			pattern := "*." + ext
			if ext == "dockerfile" {
				pattern = "Dockerfile"
			} else if ext == "makefile" {
				pattern = "Makefile"
			}

			return RuleSuggestion{
				Kind:    "name_pattern",
				Pattern: pattern,
				Role:    role,
				KindLbl: "configuration",
				Domain:  "infrastructure",
				YAML: `  # Added from gap analysis:
  - pattern: "` + pattern + `"
    role: ` + role + `
    kind: ` + role + `
    domain: infrastructure`,
				Why: "query mentions `" + term + "`; pattern `" + pattern + "` matches related files",
			}
		}
	}

	return RuleSuggestion{}
}

// suggestContentType suggests a content_type rule from query terms.
func suggestContentType(terms []string) RuleSuggestion {
	typeToExt := map[string]string{
		"go":         "go",
		"python":     "py",
		"javascript": "js",
		"typescript": "ts",
		"rust":       "rs",
		"sql":        "sql",
		"shell":      "sh",
		"yaml":       "yaml",
		"json":       "json",
		"html":       "html",
		"css":        "css",
		"markdown":   "md",
	}

	for _, term := range terms {
		if ext, ok := typeToExt[term]; ok {
			return RuleSuggestion{
				Kind:    "content_type",
				Pattern: "." + ext,
				KindLbl: ext,
				YAML: `  # Added from gap analysis:
  - extension: ".` + ext + `"
    content_type: ` + ext,
				Why: "query mentions `" + term + "`; extension ." + ext + " maps to content_type",
			}
		}
	}

	return RuleSuggestion{}
}

// suggestFromFileType tries to derive a path_prefix from file type keywords.
func suggestFromFileType(terms []string) RuleSuggestion {
	// Check if any term looks like a concept that has a conventional path.
	conceptToPath := map[string]struct{ prefix, role, domain string }{
		"handler":    {prefix: "pkg/handler/", role: "handler", domain: "business"},
		"service":    {prefix: "pkg/service/", role: "service", domain: "business"},
		"controller": {prefix: "pkg/controller/", role: "controller", domain: "networking"},
		"model":      {prefix: "pkg/model/", role: "model", domain: "data-access"},
		"view":       {prefix: "pkg/view/", role: "view", domain: "frontend"},
		"migration":  {prefix: "migrations/", role: "migration", domain: "data-access"},
		"migrate":    {prefix: "pkg/migrate/", role: "migration", domain: "data-access"},
		"fixture":    {prefix: "fixtures/", role: "fixture", domain: "testing"},
		"factory":    {prefix: "pkg/factory/", role: "factory", domain: "business"},
		"plugin":     {prefix: "plugins/", role: "plugin", domain: "infrastructure"},
		"extension":  {prefix: "extensions/", role: "extension", domain: "infrastructure"},
	}

	for _, term := range terms {
		if m, ok := conceptToPath[term]; ok {
			return RuleSuggestion{
				Kind:   "path_prefix",
				Prefix: m.prefix,
				Role:   m.role,
				Domain: m.domain,
				YAML: `  # Added from gap analysis:
  - prefix: "` + m.prefix + `"
    role: ` + m.role + `
    domain: ` + m.domain,
				Why: "query mentions `" + term + "`; conventional path is " + m.prefix,
			}
		}
	}

	return RuleSuggestion{}
}

// inferRuleKind determines the rule kind from a suggestion YAML string.
func inferRuleKind(suggestion string) string {
	if strings.Contains(suggestion, "prefix:") {
		return "path_prefix"
	}
	if strings.Contains(suggestion, "pattern:") {
		return "name_pattern"
	}
	if strings.Contains(suggestion, "extension:") {
		return "content_type"
	}
	if strings.Contains(suggestion, "intent:") {
		return "query_intent"
	}
	return "none"
}

// domainForTerm maps a term to its domain label.
func domainForTerm(term string) string {
	domainMap := map[string]string{
		"auth":           "security",
		"authentication": "security",
		"security":       "security",
		"database":       "data-access",
		"data":           "data-access",
		"api":            "networking",
		"http":           "networking",
		"grpc":           "networking",
		"config":         "infrastructure",
		"deploy":         "infrastructure",
		"logging":        "observability",
		"metrics":        "observability",
		"telemetry":      "observability",
		"cache":          "data-access",
		"queue":          "messaging",
		"messaging":      "messaging",
		"middleware":     "networking",
		"model":          "data-access",
		"schema":         "data-access",
		"validation":     "security",
		"test":           "testing",
		"docs":           "meta",
		"script":         "infrastructure",
		"tool":           "infrastructure",
		"frontend":       "frontend",
		"ui":             "frontend",
	}
	if d, ok := domainMap[term]; ok {
		return d
	}
	return "business"
}

// sliceContains checks if a slice contains a string (case-insensitive).
func sliceContains(slice []string, s string) bool {
	ls := strings.ToLower(s)
	for _, v := range slice {
		if strings.ToLower(v) == ls {
			return true
		}
	}
	return false
}
