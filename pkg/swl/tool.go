package swl

import (
	"context"
	"fmt"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// QuerySWLTool implements tools.Tool — exposed to the LLM as "query_swl".
type QuerySWLTool struct {
	manager *Manager
}

// NewQuerySWLTool creates a tool backed by the given Manager.
func NewQuerySWLTool(m *Manager) *QuerySWLTool {
	return &QuerySWLTool{manager: m}
}

func (t *QuerySWLTool) Name() string { return "query_swl" }

func (t *QuerySWLTool) Description() string {
	return `Query the persistent semantic knowledge graph (SWL) for this workspace.

The graph is built automatically from every tool call and LLM response — no setup required.
It knows about files, symbols, imports, tasks, URLs, sessions, and more.

Input formats (pick one):
  {"resume":true}                          — bring me up to speed on this workspace
  {"question":"functions in main.go"}      — natural-language query (Tier 1/2/3)
  {"gaps":true}                            — entities with low confidence or unknown status
  {"drift":true}                           — stale/outdated entities
  {"assert":"note text","subject":"x"}     — record a free-form fact
  {"stats":true}                           — entity/edge counts by type
  {"schema":true}                          — DB schema and known types
  {"sql":"SELECT ..."}                     — raw read-only SQL (200-row cap)
  {"scan":true,"root":"/path"}             — incremental workspace index scan

Returns a text summary. Always consult SWL before re-reading files.`
}

func (t *QuerySWLTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{"type": "string", "description": "Natural-language question about the workspace"},
			"resume":   map[string]any{"type": "boolean", "description": "If true, return session resume digest"},
			"gaps":     map[string]any{"type": "boolean", "description": "If true, return knowledge gaps"},
			"drift":    map[string]any{"type": "boolean", "description": "If true, return stale entities"},
			"assert":   map[string]any{"type": "string", "description": "Free-form note to record"},
			"subject":  map[string]any{"type": "string", "description": "Subject entity for assert"},
			"confidence": map[string]any{"type": "number", "description": "Confidence for assert (default 0.85)"},
			"type":     map[string]any{"type": "string", "description": "Entity type for assert (default Note)"},
			"stats":    map[string]any{"type": "boolean", "description": "If true, return graph statistics"},
			"schema":   map[string]any{"type": "boolean", "description": "If true, return DB schema"},
			"sql":      map[string]any{"type": "string", "description": "Read-only SQL query (SELECT/WITH/EXPLAIN)"},
			"scan":     map[string]any{"type": "boolean", "description": "If true, run incremental workspace scan"},
			"root":     map[string]any{"type": "string", "description": "Root path for scan (default: workspace root)"},
			"decay":    map[string]any{"type": "boolean", "description": "If true, run decay check"},
			"entity_id": map[string]any{"type": "string", "description": "Entity ID for targeted decay check"},
		},
	}
}

func (t *QuerySWLTool) Execute(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	m := t.manager
	sessionKey := toolshared.ToolSessionKey(ctx)

	// resume
	if v, _ := args["resume"].(bool); v {
		return toolshared.SilentResult(m.SessionResume(sessionKey))
	}

	// gaps
	if v, _ := args["gaps"].(bool); v {
		return toolshared.SilentResult(m.KnowledgeGaps())
	}

	// drift
	if v, _ := args["drift"].(bool); v {
		return toolshared.SilentResult(m.DriftReport())
	}

	// stats
	if v, _ := args["stats"].(bool); v {
		return toolshared.SilentResult(m.Stats())
	}

	// schema
	if v, _ := args["schema"].(bool); v {
		return toolshared.SilentResult(m.Schema())
	}

	// assert
	if note, _ := args["assert"].(string); note != "" {
		subject, _ := args["subject"].(string)
		confidence, _ := args["confidence"].(float64)
		entityType, _ := args["type"].(string)
		return toolshared.SilentResult(m.AssertNote(subject, note, confidence, entityType))
	}

	// sql
	if sqlStr, _ := args["sql"].(string); sqlStr != "" {
		result, err := m.SafeQuery(sqlStr)
		if err != nil {
			return toolshared.SilentResult("[SWL] SQL error: " + err.Error())
		}
		return toolshared.SilentResult(result)
	}

	// scan
	if v, _ := args["scan"].(bool); v {
		root, _ := args["root"].(string)
		if root == "" {
			root = m.workspace
		}
		stats, err := m.ScanWorkspace(root)
		if err != nil {
			return toolshared.SilentResult("[SWL] Scan error: " + err.Error())
		}
		return toolshared.SilentResult(fmt.Sprintf(
			"[SWL] Scan complete: scanned=%d new=%d changed=%d deleted=%d skipped=%d",
			stats.Scanned, stats.New, stats.Changed, stats.Deleted, stats.Skipped,
		))
	}

	// decay
	if v, _ := args["decay"].(bool); v {
		eid, _ := args["entity_id"].(string)
		m.DecayCheck(eid, 5)
		return toolshared.SilentResult("[SWL] Decay check complete.")
	}

	// question (natural language or fallthrough)
	question, _ := args["question"].(string)
	if question == "" {
		return toolshared.SilentResult("[SWL] Provide a question, resume:true, stats:true, or another operation.")
	}
	return toolshared.SilentResult(m.Ask(question))
}
