# SWL Native Integration into picoclaw — PRD & Implementation Plan

## Context

SWL (Semantic Workspace Layer) v8.0 is a persistent, self-maintaining semantic knowledge graph that hooks into LLM agent tool calls and builds a second brain that survives indefinitely across sessions. It was authored with picoclaw integration in mind (the v8 ZIP ships Python subprocess wrappers for picoclaw tools), but the Python wrapper approach conflicts with picoclaw's core design: pure Go, single binary, zero external runtimes, targets resource-constrained hardware (MIPS, RISC-V, ARM, Raspberry Pi Zero).

This plan describes a **Go-native port** of SWL v8.0 embedded directly into picoclaw — no Python, no subprocess wrappers, no Claude Code hooks, no daemons. The result is a config-driven, transparent, always-on semantic memory layer available to every picoclaw agent.

Additionally, the plan covers a **real-time 3D knowledge graph visualization** in the picoclaw web frontend — a new sidebar page powered by a bloom-enhanced 3D force graph, reflecting the live SWL SQLite graph as it grows during agent sessions.

---

## Vision

When SWL is enabled in picoclaw:
- Every tool call (file read/write, exec, list_dir, web fetch) automatically enriches a persistent SQLite knowledge graph stored at `{workspace}/.swl/swl.db`
- The agent can query the graph in ~50 tokens instead of re-reading files at ~3,000 tokens each
- Knowledge persists across sessions, channel restarts, and binary upgrades
- Facts auto-invalidate when source files change (content-hash cascade)
- The agent resumes intelligently via `{"resume": true}` on `query_swl`
- Operators view the live knowledge graph in the web frontend as a bloom-lit 3D force graph
- Zero operator setup: drop config entry, restart, done

---

## Non-Goals

- **No Python dependency** — Go-native only; no subprocess wrappers, no shelling out
- **No Claude Code integration** — all `_CC_TOOL_MAP`, `_normalise_cc_tool()`, `_cli_hook()`, `_cli_install()`, `.claude/settings.json` references are EXCLUDED from the Go port
- **No cron or daemon** — all maintenance is reactive (session-start sync, probabilistic decay, event pruning)
- **No vector database** — relational SQLite only, regex-based extraction
- **No AST parsing** — 85–90% precision regex is sufficient and far simpler
- **No v7 patterns** — must implement v8.0 semantics only; see anti-drift section
- **No cross-workspace sharing** — DB is strictly workspace-local
- **Not a replacement for session history** — complementary to the existing JSONL session store

---

## Critical v7 Anti-Drift Rules (What Must NOT Be Ported)

The SWL ZIP dev/PRD.md documents what v7 got wrong. The Go port must implement v8 semantics exclusively:

| v7 Pattern (BANNED) | v8 Replacement (REQUIRED) |
|---|---|
| Separate session per subprocess | One session per picoclaw session key, shared across all turns |
| Cron-based maintenance | Reactive only: session-start sync, 5% decay probability per hook call, prune at 10k events, VACUUM on close >50MB |
| DB in skill directory | `{workspace}/.swl/swl.db` (never in pkg/swl/) |
| Nine source files | Single `Manager` struct in `pkg/swl/`, clean internal separation |
| `Channel` edge orphans | Only create `Channel` entity when content is non-empty |
| `searched` edge orphans | Only create `searched` edge when result is non-empty |
| `append_file` preserves content_hash | `append_file` NULLS content_hash (content unknown after append) |
| `assert_note` hardcodes depth=3 | `assert_note` sets depth to `MAX(current_depth, 2)` |
| Directory entity without verification | Only upsert Directory entity if path is confirmed directory |
| `fact_status` reset by upsert | `fact_status` NEVER reset by upsert — only via `SetFactStatus()` |
| `confidence` decreases on re-upsert | `confidence = MAX(existing, new)` always |

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│  picoclaw AgentInstance                                     │
│  ├── Tools (ToolRegistry)                                   │
│  │   └── QuerySWLTool ─────────────────────────────┐       │
│  ├── SWLManager *swl.Manager ───────────────────┐  │       │
│  └── ContextBuilder (with SWL SKILL.md inject)  │  │       │
└─────────────────────────────────────────────────│──│───────┘
                                                   │  │
┌─────────────────────────────────────────────────│──│───────┐
│  picoclaw AgentLoop                             │  │       │
│  └── HookManager                               │  │       │
│      └── SWLHook (ToolInterceptor) ────────────┘  │       │
│          mounted per-agent from instance           │       │
└────────────────────────────────────────────────────│───────┘
                                                     │
┌────────────────────────────────────────────────────▼───────┐
│  pkg/swl.Manager                                           │
│  ├── DB: {workspace}/.swl/swl.db (SQLite WAL)             │
│  ├── Sessions: sessionKey → SWL session UUID mapping       │
│  ├── Extractor: compiled Go regex patterns                 │
│  ├── pre_hook(tool, args) → guards + constraint checks     │
│  ├── post_hook(tool, args, result) → infer + apply_delta   │
│  ├── Ask(question) → Tier1/Tier2/Tier3 query              │
│  └── ScanWorkspace(root) → incremental mtime scan         │
└─────────────────────────────────────────────────────────────┘
                         │
                         │ read-only SQLite (no IPC needed)
                         ▼
┌─────────────────────────────────────────────────────────────┐
│  web/backend/api/swl.go                                    │
│  ├── GET /api/swl/graph   → nodes + links JSON             │
│  ├── GET /api/swl/stats   → health stats                   │
│  ├── GET /api/swl/sessions → session list                  │
│  └── GET /api/swl/stream  → SSE real-time updates         │
└─────────────────────────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│  web/frontend: /swl route (new sidebar item)               │
│  └── 3D Force Graph (react-force-graph-3d / vasturiano)   │
│      ├── Nodes: entities (color by type, size by depth)   │
│      ├── Links: edges (color by rel type)                 │
│      ├── Bloom post-processing on depth-3 nodes           │
│      ├── Stats panel (entity counts, session info)        │
│      └── SSE subscription for real-time updates          │
└─────────────────────────────────────────────────────────────┘
```

---

## Data Model (Go Types)

### Entity Types
```go
type EntityType string
const (
    EntityFile       EntityType = "File"
    EntityDirectory  EntityType = "Directory"
    EntitySymbol     EntityType = "Symbol"
    EntityDependency EntityType = "Dependency"
    EntityTask       EntityType = "Task"
    EntitySection    EntityType = "Section"
    EntityTopic      EntityType = "Topic"
    EntityURL        EntityType = "URL"
    EntityCommit     EntityType = "Commit"
    EntitySession    EntityType = "Session"
    EntityNote       EntityType = "Note"
    EntityCommand    EntityType = "Command"
)
```

### Edge Relations
```go
type EdgeRel string
const (
    RelDefines     EdgeRel = "defines"
    RelImports     EdgeRel = "imports"
    RelHasTask     EdgeRel = "has_task"
    RelHasSection  EdgeRel = "has_section"
    RelMentions    EdgeRel = "mentions"
    RelDependsOn   EdgeRel = "depends_on"
    RelTagged      EdgeRel = "tagged"
    RelInDir       EdgeRel = "in_dir"
    RelWrittenIn   EdgeRel = "written_in"
    RelEditedIn    EdgeRel = "edited_in"
    RelAppendedIn  EdgeRel = "appended_in"
    RelRead        EdgeRel = "read"
    RelFetched     EdgeRel = "fetched"
    RelExecuted    EdgeRel = "executed"
    RelDeleted     EdgeRel = "deleted"
    RelDescribes   EdgeRel = "describes"
    RelCommittedIn EdgeRel = "committed_in"
    RelFound       EdgeRel = "found"
    RelListed      EdgeRel = "listed"
)
```

### Fact Status & Extraction Method
```go
type FactStatus string
const (
    FactUnknown  FactStatus = "unknown"
    FactVerified FactStatus = "verified"
    FactStale    FactStatus = "stale"
    FactDeleted  FactStatus = "deleted"
)

type ExtractionMethod string
const (
    MethodObserved ExtractionMethod = "observed"  // confidence 1.0
    MethodStated   ExtractionMethod = "stated"    // confidence 0.85
    MethodExtracted ExtractionMethod = "extracted" // confidence 0.9
    MethodInferred ExtractionMethod = "inferred"  // confidence 0.8
)
```

### GraphDelta (atomic write unit)
```go
type EntityTuple struct {
    ID               string
    Type             EntityType
    Name             string
    Metadata         map[string]any
    Confidence       float64
    ExtractionMethod ExtractionMethod
    KnowledgeDepth   int
}

type EdgeTuple struct {
    FromID string
    Rel    EdgeRel
    ToID   string
}

type GraphDelta struct {
    Entities []EntityTuple
    Edges    []EdgeTuple
}

func (d *GraphDelta) Merge(other *GraphDelta) { ... }
func (d *GraphDelta) IsEmpty() bool { ... }
```

### SQLite Schema (DDL)
```sql
CREATE TABLE IF NOT EXISTS entities (
    id                TEXT PRIMARY KEY,
    type              TEXT NOT NULL,
    name              TEXT NOT NULL,
    metadata          TEXT DEFAULT '{}',
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
    id              TEXT PRIMARY KEY,
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    goal            TEXT,
    summary         TEXT,
    workspace_state TEXT DEFAULT '{}'
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

-- Indexes
CREATE INDEX IF NOT EXISTS idx_entities_type       ON entities(type);
CREATE INDEX IF NOT EXISTS idx_entities_status     ON entities(fact_status);
CREATE INDEX IF NOT EXISTS idx_entities_depth      ON entities(knowledge_depth);
CREATE INDEX IF NOT EXISTS idx_entities_hash       ON entities(content_hash);
CREATE INDEX IF NOT EXISTS idx_edges_from          ON edges(from_id);
CREATE INDEX IF NOT EXISTS idx_edges_to            ON edges(to_id);
CREATE INDEX IF NOT EXISTS idx_edges_rel           ON edges(rel);
CREATE INDEX IF NOT EXISTS idx_events_session      ON events(session_id);
CREATE INDEX IF NOT EXISTS idx_events_ts           ON events(ts);
```

---

## Package Structure

```
pkg/swl/
├── config.go       SWLConfig struct (decoded from ToolsConfig.SWL JSON)
├── manager.go      Manager struct — main object, lifecycle, public API
├── db.go           DB init, schema DDL, migrations, WAL, connection management
├── types.go        EntityType, EdgeRel, FactStatus, ExtractionMethod, GraphDelta
├── entity.go       UpsertEntity, SetFactStatus, ApplyDelta, invalidateChildren
├── edge.go         UpsertEdge, confirmEdge
├── session.go      StartSession, EndSession, SessionSync, WorkspaceSnapshot
├── extractor.go    ExtractContent, ExtractDirectory, ExtractExec, ExtractWeb
│                   Go regex patterns for 14-language symbol/import extraction
├── scanner.go      ScanWorkspace — incremental mtime-based directory walker
├── hook.go         SWLHook implementing agent.ToolInterceptor
│                   BeforeTool: guard checks, constraint validation
│                   AfterTool:  three-layer inference + apply_delta
├── tool.go         QuerySWLTool implementing tools.Tool
│                   Dispatches all query input formats to Manager methods
├── query.go        Ask() — Tier1 pattern matching, Tier2 SQL templates, Tier3 raw SQL
├── decay.go        DecayCheck, MaybeDecay, MaybePrune, VACUUM-on-close
└── skill/
    └── SKILL.md    Embedded runtime manual (picoclaw-only, zero Claude refs)
```

---

## Configuration

### New field in `pkg/config/config_struct.go` — `ToolsConfig`
```go
type SWLConfig struct {
    Enabled           bool   `json:"enabled"`
    InjectSkillPrompt bool   `json:"inject_skill_prompt,omitempty"` // default true
    MaxFileSizeBytes  int64  `json:"max_file_size_bytes,omitempty"` // default 524288
    DBPath            string `json:"db_path,omitempty"`             // default {workspace}/.swl/swl.db
    ExtractSymbols    bool   `json:"extract_symbols,omitempty"`     // default true
    ExtractImports    bool   `json:"extract_imports,omitempty"`     // default true
    ExtractTasks      bool   `json:"extract_tasks,omitempty"`       // default true
    ExtractSections   bool   `json:"extract_sections,omitempty"`    // default true
    ExtractURLs       bool   `json:"extract_urls,omitempty"`        // default true
}
```

Add to `ToolsConfig`:
```go
SWL *SWLConfig `json:"swl,omitempty"`
```

### Example `config.json` entry
```json
{
  "tools": {
    "swl": {
      "enabled": true,
      "inject_skill_prompt": true
    }
  }
}
```

---

## Integration Points in Existing Code

### 1. `pkg/agent/instance.go` — `NewAgentInstance()`
Add after tool registration block:
```go
var swlManager *swl.Manager
if cfg.Tools.SWL != nil && cfg.Tools.SWL.Enabled {
    mgr, err := swl.NewManager(workspace, cfg.Tools.SWL)
    if err != nil {
        logger.WarnCF("agent", "SWL init failed; continuing without SWL",
            map[string]any{"error": err.Error()})
    } else {
        swlManager = mgr
        toolsRegistry.Register(swl.NewQuerySWLTool(mgr))
    }
}
```

Add `SWLManager *swl.Manager` field to `AgentInstance` struct.

### 2. `pkg/agent/loop.go` — `Run()`
Add after `ensureHooksInitialized`:
```go
al.mountAgentSWLHooks()
```

New method:
```go
func (al *AgentLoop) mountAgentSWLHooks() {
    for _, agentID := range al.registry.ListAgentIDs() {
        agent, ok := al.registry.GetAgent(agentID)
        if !ok || agent.SWLManager == nil {
            continue
        }
        hookName := "swl_" + agentID
        if err := al.MountHook(HookRegistration{
            Name:     hookName,
            Source:   HookSourceInProcess,
            Priority: 10, // low priority — runs after security hooks
            Hook:     agent.SWLManager.Hook(agentID),
        }); err != nil {
            logger.WarnCF("agent", "Failed to mount SWL hook",
                map[string]any{"agent_id": agentID, "error": err.Error()})
        }
    }
}
```

### 3. `pkg/agent/context.go` — `ContextBuilder`
Add `extraSystemContent []string` field.

In `NewContextBuilder()`, wire up SWL skill prompt injection:
```go
// Called from NewAgentInstance when SWL enabled:
func (cb *ContextBuilder) AddInlineSystemContent(content string) {
    cb.mu.Lock()
    cb.extraSystemContent = append(cb.extraSystemContent, content)
    cb.mu.Unlock()
}
```

In `BuildSystemPrompt()`, append inline content at the end.

In `NewAgentInstance()`, when SWL enabled and `cfg.Tools.SWL.InjectSkillPrompt`:
```go
contextBuilder.AddInlineSystemContent(swl.SkillContent())
```

### 4. `pkg/agent/instance.go` — `Close()`
Add SWL cleanup:
```go
if a.SWLManager != nil {
    a.SWLManager.Close()
}
```

### 5. `web/backend/api/router.go` — `RegisterRoutes()`
Add:
```go
h.registerSWLRoutes(mux)
```

---

## SWL Hook Implementation Detail

### `pkg/swl/hook.go`

```go
type SWLHook struct {
    manager *Manager
    agentID string
}

func (h *SWLHook) BeforeTool(
    ctx context.Context,
    call *agent.ToolCallHookRequest,
) (*agent.ToolCallHookRequest, agent.HookDecision, error) {
    // 1. Filter to only this agent's calls
    if !h.matchesAgent(call) {
        return call, agent.HookDecision{Action: agent.HookActionContinue}, nil
    }
    // 2. Run guards and constraint checks (non-blocking, panic-safe)
    decision := h.manager.PreHook(call.Tool, call.Arguments)
    if decision.Action == "abort" {
        return call, agent.HookDecision{Action: agent.HookActionDenyTool, Reason: decision.Reason}, nil
    }
    return call, agent.HookDecision{Action: agent.HookActionContinue}, nil
}

func (h *SWLHook) AfterTool(
    ctx context.Context,
    result *agent.ToolResultHookResponse,
) (*agent.ToolResultHookResponse, agent.HookDecision, error) {
    // 1. Filter to only this agent's calls
    if !h.matchesAgent(result) {
        return result, agent.HookDecision{Action: agent.HookActionContinue}, nil
    }
    // 2. Run inference + apply_delta (panic-safe, never blocks)
    sessionKey := extractSessionKey(result)
    go func() {
        // Async: AfterTool must not block the agent turn
        h.manager.PostHook(sessionKey, result.Tool, result.Arguments, result.Result)
    }()
    return result, agent.HookDecision{Action: agent.HookActionContinue}, nil
}
```

**Agent filtering**: The global hook manager calls all hooks for all tool calls. The SWL hook filters using `call.Context.Route.AgentID == h.agentID`. This ensures workspace isolation when multiple agents coexist.

**Session key derivation**: Use `call.Channel + ":" + call.ChatID` as the picoclaw session key, mapped to a stable SWL session UUID stored in `Manager.activeSessions`.

**Panic safety**: All `PreHook` and `PostHook` calls wrapped in `defer recover()`. SWL failure is logged and silently ignored — never crashes the agent turn.

**AfterTool is async**: Run in a goroutine. This keeps AfterTool latency to zero for the agent. The inference pipeline (<50ms for typical files) runs in background.

---

## Three-Layer Inference Pipeline (Go port of Python `_infer`)

### Layer 0: Programmatic registry (via `Manager.RegisterTool(name, fn)`)
Custom tool inference functions registered by operators. Take full precedence.

### Layer 1: Declarative tool map (Go equivalent of Python `_TOOL_MAP`)
```go
var toolMap = map[string]declRule{
    "write_file": {
        entityExpr: "args.path",
        entityType: EntityFile,
        edges:      []edgeRule{{rel: RelInDir, targetExpr: "parent(args.path)"}},
    },
    "edit_file":   { /* same as write_file + edited_in rel */ },
    "append_file": { /* same but rel = appended_in */ },
    "read_file":   { /* file entity + read rel */ },
    "delete_file": { /* tombstone entity */ },
    "list_dir":    { /* directory entity + listed rel */ },
    "exec":        { /* command entity + executed rel */ },
    "web_fetch":   { /* URL entity + fetched rel */ },
}
```

Expressions resolved from `args` and `result` maps. `parent(path)` returns the directory.

### Layer 2: Semantic extraction (via `extractor.go`)
Called after Layer 1. Content-based extraction using compiled Go regex patterns.

| Tool | Extraction |
|---|---|
| `write_file`, `edit_file` | File entity + symbols, imports, tasks, sections, URLs, topics from args.content |
| `read_file` | Same from result.content (result.ForLLM parsed) |
| `append_file` | Null out content_hash (content now unknown) |
| `delete_file` | Tombstone: SetFactStatus(path, FactDeleted) |
| `list_dir` | Directory entity + project-type topics from special files |
| `exec` | Git commits, pytest/go test results, pip/npm packages, URLs from stdout |
| `web_fetch` | Title, sections, linked URLs, topics from page content |

---

## Go Regex Patterns (`extractor.go`)

All patterns compiled once at `NewManager()` time. Key porting notes:

**Go RE2 does NOT support lookahead/lookbehind.** Patterns using `(?<=...)` or `(?=...)` must be rewritten as capture groups.

### Symbol patterns (Go syntax)
```go
var symPatterns = []*regexp.Regexp{
    regexp.MustCompile(`(?m)^func\s+(?:\(\w+\s+\*?\w+\)\s+)?(\w+)\s*\(`),       // Go
    regexp.MustCompile(`(?m)^def\s+(\w+)\s*\(`),                                  // Python
    regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?function\s+(\w+)\s*\(`), // JS/TS
    regexp.MustCompile(`(?m)^\s*(?:pub\s+)?fn\s+(\w+)\s*[(<]`),                  // Rust
    regexp.MustCompile(`(?m)^\s*(?:public|private|protected)?\s*(?:static\s+)?\w+\s+(\w+)\s*\(`), // Java/C#
    regexp.MustCompile(`(?m)^\s*class\s+(\w+)\s*[({:]`),                         // multi-lang
    regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:interface|type)\s+(\w+)\s*[={<]`), // TS
    regexp.MustCompile(`(?m)^\s*(?:pub\s+)?(?:struct|enum|trait)\s+(\w+)\b`),    // Rust
    regexp.MustCompile(`(?m)^\s*(?:const|var|let)\s+([A-Z][A-Z0-9_]{2,})\s*=`), // constants
}
```

### Import patterns (Go syntax)
```go
var importPatterns = []*regexp.Regexp{
    regexp.MustCompile(`(?m)^import\s+(\S+)`),                            // Python bare
    regexp.MustCompile(`(?m)^from\s+(\S+)\s+import`),                    // Python from
    regexp.MustCompile(`(?m)^\t"([^"]+)"`),                               // Go (tab-indented strings)
    regexp.MustCompile(`(?m)^import\s+(?:\{[^}]+\}|\S+)\s+from\s+"([^"]+)"`), // ES6
    regexp.MustCompile(`(?m)^\s*use\s+([\w:]+)`),                        // Rust
    regexp.MustCompile(`(?m)^#include\s+[<"]([^>"]+)[>"]`),              // C/C++
    regexp.MustCompile(`(?m)^\s*require\s*['"]([^'"]+)['"]`),            // Ruby/Node.js
}
```

### Task pattern
```go
var taskRE = regexp.MustCompile(
    `(?i)(?:TODO|FIXME|HACK|NOTE|BUG|XXX|OPTIMIZE|REFACTOR|REVIEW|DEPRECATED)[:\s]+(.+)`)
```

### Section/heading pattern
```go
var headingRE = regexp.MustCompile(`(?m)^(#{1,3})\s+(.+)`)
```

### URL pattern
```go
var urlRE = regexp.MustCompile(`https?://[^\s"'<>)\]]+`)
```

---

## Anti-Bloat Limits (enforced in extractor.go)
```go
const (
    maxSymbols  = 60
    maxImports  = 40
    maxTasks    = 30
    maxSections = 20
    maxURLs     = 20
    maxTopics   = 10
    maxFileSize = 512 * 1024 // 512KB
)
```

### Noise symbols (ignored during extraction)
```go
var noiseSymbols = map[string]bool{
    "main": true, "test": true, "setup": true, "init": true,
    "get": true, "set": true, "run": true, "new": true,
    "String": true, "Error": true, "Close": true,
}
```

---

## Session Management (`session.go`)

### Session key → SWL session UUID mapping
```go
type Manager struct {
    // ...
    sessionsMu     sync.RWMutex
    activeSessions map[string]string // picoclaw sessionKey → SWL session UUID
}
```

### Session lifecycle
1. `PreHook` or `PostHook` with unknown session key → `startSession(sessionKey)` → new UUID
2. Session UUID stored in `activeSessions` map
3. Sessions table row inserted with `started_at`
4. On `Manager.Close()` → `endAllSessions()` with auto-generated summary from stats

### SessionSync (called at first `PostHook` of a new session key)
```go
func (m *Manager) sessionSync(sessionID string) {
    // Check all known verified File entities for external changes
    // Compare stored mtime against os.Stat()
    // Mark changed files stale; deleted files FactDeleted
    // Runs at most once per session (tracked by synced map)
}
```

---

## QuerySWLTool (`tool.go`)

```go
type QuerySWLTool struct { manager *Manager }

func (t *QuerySWLTool) Name()        string         { return "query_swl" }
func (t *QuerySWLTool) Description() string         { return "Query the SWL semantic workspace knowledge graph..." }
func (t *QuerySWLTool) Parameters()  map[string]any { /* JSON schema */ }

func (t *QuerySWLTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
    // Dispatch on input shape:
    // {"question": "..."}               → t.manager.Ask(question)
    // {"resume": true}                  → t.manager.SessionResume(sessionKey)
    // {"gaps": true}                    → t.manager.KnowledgeGaps()
    // {"drift": true}                   → t.manager.DriftReport()
    // {"assert": "...", "subject": ..., "confidence": ..., "type": ...} → t.manager.AssertNote(...)
    // {"stats": true}                   → t.manager.Stats()
    // {"decay": true, "entity_id": ...} → t.manager.DecayCheck(entityID)
    // {"schema": true}                  → t.manager.Schema()
    // {"sql": "SELECT ..."}             → t.manager.SafeQuery(sql)
    // {"scan": true, "root": "..."}     → t.manager.ScanWorkspace(root)
}
```

Tool result: `SilentResult("[SWL] " + answer)` — LLM sees it, user does not.

---

## Query Interface (`query.go`)

### Tier 1: Natural language pattern matching
30+ compiled regex patterns matching question strings to handler methods. Examples:

| Pattern | Handler |
|---|---|
| `(?i)functions? in (.+)` | `askSymbols(hint, "function")` |
| `(?i)todos? in (.+)` | `askTasks(hint)` |
| `(?i)what.*(import|depend).*(.+)` | `askImports(hint)` |
| `(?i)files? in (.+)` | `askFilesIn(hint)` |
| `(?i)(stale|drift|outdated)` | `askStale()` |
| `(?i)project type` | `askProjectType()` |
| `(?i)most complex` | `askComplexity()` |
| `(?i)bring me up to speed` | `SessionResume()` |

### Tier 2: Named SQL templates
```go
var sqlTemplates = map[string]string{
    "dependency_chain": `
        WITH RECURSIVE chain(id, depth) AS (
            SELECT to_id, 0 FROM edges WHERE from_id = ? AND rel = 'imports'
            UNION ALL
            SELECT e.to_id, c.depth + 1 FROM edges e
            JOIN chain c ON e.from_id = c.id WHERE c.depth < 10
        )
        SELECT DISTINCT id, depth FROM chain ORDER BY depth`,
    "files_by_complexity": `
        SELECT e.from_id, COUNT(*) as sym_count
        FROM edges e WHERE e.rel = 'defines'
        GROUP BY e.from_id ORDER BY sym_count DESC LIMIT 20`,
    "top_dependencies": `
        SELECT to_id, COUNT(DISTINCT from_id) as file_count
        FROM edges WHERE rel = 'imports'
        GROUP BY to_id ORDER BY file_count DESC LIMIT 20`,
    // ... 20+ templates
}
```

### Tier 3: Raw SQL escape hatch
```go
func (m *Manager) SafeQuery(sql string) (string, error) {
    // Validate: must start with SELECT, WITH, or EXPLAIN
    // Row limit: 200
    // Timeout: 5 seconds
    // Returns formatted table string
}
```

---

## Reactive Maintenance (`decay.go`)

All maintenance is event-triggered. No goroutines that run independently.

### MaybeDecay (called at end of every PostHook)
```go
func (m *Manager) maybeDecay() {
    if rand.Float64() > 0.05 { return } // 5% chance
    m.decayCheck("", 2) // Check 2 random eligible entities
}
```

### Decay handlers
- **File**: `os.Stat(path)` — verify existence, update `fact_status`
- **URL**: HTTP HEAD with 24h minimum between rechecks
- Custom handlers registered via `RegisterDecayHandler(entityType, fn)`

### MaybePrune
```go
func (m *Manager) maybePrune() {
    var count int
    m.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
    if count > 10000 {
        m.db.Exec("DELETE FROM events WHERE ts < ?", cutoff30Days)
    }
}
```

### VACUUM on close
```go
func (m *Manager) Close() error {
    m.endAllSessions()
    var size int64
    os.Stat(m.dbPath, &info); size = info.Size()
    if size > 50*1024*1024 {
        m.db.Exec("VACUUM")
    }
    return m.db.Close()
}
```

---

## Upsert Invariants (entity.go)

CRITICAL — these invariants must be enforced in every code path:

```go
func (m *Manager) upsertEntity(e EntityTuple) error {
    // INSERT OR IGNORE first
    // Then UPDATE with invariants:
    // 1. confidence = MAX(existing, new)         — NEVER decrease
    // 2. knowledge_depth = MAX(existing, new)    — NEVER decrease (resets to 1 on hash change)
    // 3. fact_status NEVER touched here          — only SetFactStatus() changes it
    // 4. extraction_method priority:
    //    observed > stated > extracted > inferred
    //    Only upgrade, never downgrade
    const sql = `
        INSERT INTO entities (id, type, name, metadata, confidence, knowledge_depth,
                              extraction_method, fact_status, created_at, modified_at,
                              accessed_at, access_count)
        VALUES (?, ?, ?, ?, ?, ?, ?, 'unknown', ?, ?, ?, 1)
        ON CONFLICT(id) DO UPDATE SET
            confidence = MAX(confidence, excluded.confidence),
            knowledge_depth = MAX(knowledge_depth, excluded.knowledge_depth),
            extraction_method = CASE
                WHEN extraction_method = 'observed' THEN 'observed'
                WHEN excluded.extraction_method = 'observed' THEN 'observed'
                WHEN extraction_method = 'stated' THEN 'stated'
                WHEN excluded.extraction_method = 'stated' THEN 'stated'
                WHEN extraction_method = 'extracted' THEN 'extracted'
                WHEN excluded.extraction_method = 'extracted' THEN 'extracted'
                ELSE 'inferred'
            END,
            modified_at = excluded.modified_at,
            accessed_at = excluded.accessed_at,
            access_count = access_count + 1
    `
}
```

### SetFactStatus — ONLY way to change fact_status
```go
func (m *Manager) SetFactStatus(entityID string, status FactStatus) error {
    // Validate status is one of: unknown, verified, stale, deleted
    // 'deleted' is terminal — cannot be changed back
    if status == FactDeleted {
        // Cascade: mark all derived entities stale
        m.invalidateChildren(entityID)
    }
    _, err := m.db.Exec(
        "UPDATE entities SET fact_status = ?, modified_at = ? WHERE id = ?",
        status, nowSQLite(), entityID)
    return err
}
```

### CheckAndInvalidate — content hash cascade
```go
func (m *Manager) checkAndInvalidate(entityID, content string) bool {
    newHash := contentHash(content)
    var existingHash sql.NullString
    m.db.QueryRow("SELECT content_hash FROM entities WHERE id = ?", entityID).Scan(&existingHash)
    if existingHash.Valid && existingHash.String == newHash {
        return false // unchanged
    }
    m.db.Exec("UPDATE entities SET content_hash = ?, knowledge_depth = 1 WHERE id = ?", newHash, entityID)
    if existingHash.Valid && existingHash.String != "" {
        m.invalidateChildren(entityID)
    }
    return true // changed
}
```

### InvalidateChildren
```go
func (m *Manager) invalidateChildren(fileID string) {
    // Mark all entities derived from fileID as stale:
    // - Symbols, tasks, sections, imports (via edges: defines, has_task, has_section, imports)
    // - Notes about the file (via edges: describes)
    const sql = `
        UPDATE entities SET fact_status = 'stale', modified_at = ?
        WHERE id IN (
            SELECT to_id FROM edges
            WHERE from_id = ? AND rel IN ('defines','has_task','has_section','mentions')
            UNION
            SELECT from_id FROM edges
            WHERE to_id = ? AND rel = 'describes'
        )`
    m.db.Exec(sql, nowSQLite(), fileID, fileID)
}
```

---

## DB Connection Management (`db.go`)

```go
type Manager struct {
    dbPath string
    db     *sql.DB  // single connection, MaxOpenConns(1), WAL handles concurrency
    mu     sync.Mutex // write serialization
    // ...
}

func openDB(dbPath string) (*sql.DB, error) {
    db, err := sql.Open("sqlite",
        "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(on)")
    if err != nil { return nil, err }
    db.SetMaxOpenConns(1)    // SQLite: one writer at a time
    db.SetMaxIdleConns(1)
    db.SetConnMaxLifetime(0)
    return db, initSchema(db)
}
```

Writes use `m.mu.Lock()`. Reads use `m.db.QueryRow()` directly (WAL allows concurrent reads).

---

## SKILL.md (embedded resource)

File: `pkg/swl/skill/SKILL.md`

Content: Adapted from SWL v8.0 SKILL.md with ALL of the following removed:
- "Setup for Claude Code" section
- All `swl.py --install` references
- All `.claude/settings.json` references
- All `--hook pre|post` CLI references  
- All `SWL_CONVERSATION_ID` env var references
- "Setup for Picoclaw" section (no longer needed — it's built in)

Retained and adapted:
- "First thing every session" (query_swl `{"resume":true}`)
- Full query reference (all 30+ question patterns)
- Confidence and knowledge depth tables
- "What builds automatically" table (adapted for picoclaw tool names)
- `assert`, `gaps`, `drift`, `stats`, `schema`, `sql`, `scan` query formats

Loaded via:
```go
//go:embed skill/SKILL.md
var skillContent string

func SkillContent() string { return skillContent }
```

---

## Frontend: SWL Knowledge Graph Visualization

### New dependency
```
pnpm add react-force-graph-3d
```
(`react-force-graph-3d` wraps vasturiano's `3d-force-graph` with React bindings and built-in Three.js bloom post-processing support)

### New files

#### `web/frontend/src/routes/swl.tsx` (TanStack Router file-based route)
```tsx
export const Route = createFileRoute('/swl')({ component: SWLPage })

function SWLPage() {
    return (
        <div className="flex h-full">
            <div className="flex-1"><SWLGraph /></div>
            <div className="w-72 border-l"><SWLStats /><SWLLegend /></div>
        </div>
    )
}
```

#### `web/frontend/src/api/swl.ts`
```ts
export const swlApi = {
    getGraph:    (agentId = 'main') => httpClient.get<SWLGraphData>(`/api/swl/graph?agent_id=${agentId}`),
    getStats:    (agentId = 'main') => httpClient.get<SWLStats>(`/api/swl/stats?agent_id=${agentId}`),
    getSessions: (agentId = 'main') => httpClient.get<SWLSession[]>(`/api/swl/sessions?agent_id=${agentId}`),
    streamUrl:   (agentId = 'main') => `/api/swl/stream?agent_id=${agentId}`, // SSE
}
```

#### `web/frontend/src/features/swl/graph.tsx`
```tsx
import ForceGraph3D from 'react-force-graph-3d'

const NODE_COLORS: Record<EntityType, string> = {
    File: '#4488ff', Directory: '#888888', Symbol: '#44ff88',
    Task: '#ffcc44', URL: '#44ffff', Session: '#cc44ff',
    Note: '#ff8844', Topic: '#ff4488', Dependency: '#88ff44',
    Command: '#ffff44', Commit: '#ff4444', Section: '#44ccff',
}

export function SWLGraph() {
    const { data, isLoading } = useQuery({ queryKey: ['swl-graph'], queryFn: swlApi.getGraph })
    const graphRef = useRef()

    // SSE real-time updates
    useEffect(() => {
        const es = new EventSource(swlApi.streamUrl())
        es.onmessage = (e) => {
            const update = JSON.parse(e.data)
            // Merge delta into existing graph data
            // Debounce: 500ms
        }
        return () => es.close()
    }, [])

    return (
        <ForceGraph3D
            ref={graphRef}
            graphData={data}
            nodeColor={node => NODE_COLORS[node.type] ?? '#ffffff'}
            nodeVal={node => Math.log1p(node.accessCount) + 1}
            nodeOpacity={node => node.confidence}
            linkColor={link => linkColors[link.rel] ?? '#666666'}
            linkWidth={1}
            linkDirectionalArrowLength={4}
            linkDirectionalArrowRelPos={1}
            // Bloom post-processing (built into react-force-graph-3d via Three.js)
            enableNodeDrag
            nodeThreeObject={node => buildBloomNode(node)}
            backgroundColor="#0a0a0f"
        />
    )
}
```

**Bloom encoding**: Nodes with `knowledge_depth >= 2` get emissive material; `depth === 3` gets UnrealBloomPass glow. Stale nodes get wireframe rendering.

#### `web/frontend/src/features/swl/stats.tsx`
Entity counts by type, session info, stale/unknown/deleted breakdown, knowledge depth distribution.

#### `web/frontend/src/features/swl/legend.tsx`
Color legend for node types and edge relation types.

### Sidebar item (`web/frontend/src/components/app-sidebar.tsx`)
Add to the "Agent" nav group:
```ts
{
    title: "navigation.swl",
    url: "/swl",
    icon: IconBrain, // @tabler/icons-react
    translateTitle: true,
}
```

Add i18n key `navigation.swl` = "Knowledge Graph" (en), "知识图谱" (zh-CN), etc.

### Backend API (`web/backend/api/swl.go`)

The web backend (launcher process) reads the SWL SQLite DB **directly** (read-only). No IPC needed — it reads the same `.swl/swl.db` file the gateway writes. SQLite WAL handles concurrent access cleanly.

```go
func (h *Handler) registerSWLRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /api/swl/graph",    h.handleSWLGraph)
    mux.HandleFunc("GET /api/swl/stats",    h.handleSWLStats)
    mux.HandleFunc("GET /api/swl/sessions", h.handleSWLSessions)
    mux.HandleFunc("GET /api/swl/stream",   h.handleSWLStream)  // SSE
}

func (h *Handler) swlDBPath() (string, error) {
    cfg, err := h.loadConfig()
    if err != nil { return "", err }
    workspace := cfg.Agents.Defaults.Workspace
    // expand ~ same as picoclaw does
    return filepath.Join(expandHome(workspace), ".swl", "swl.db"), nil
}

func (h *Handler) handleSWLGraph(w http.ResponseWriter, r *http.Request) {
    dbPath, err := h.swlDBPath()
    // Open read-only: "file:{dbPath}?mode=ro"
    // Query entities + edges
    // Return JSON: {nodes: [...], links: [...], meta: {...}}
}

func (h *Handler) handleSWLStream(w http.ResponseWriter, r *http.Request) {
    // SSE: set headers, poll DB every 2 seconds for changes (compare modified_at watermark)
    // Send "data: {...}\n\n" on change
    // Flush on each event
}
```

**SSE polling strategy**: The SSE handler polls `SELECT MAX(modified_at) FROM entities` every 2 seconds. If changed, re-queries and sends delta. Lightweight — no file watching needed.

---

## Tool Argument Normalization

picoclaw tool arg names map directly to SWL expected names — no normalization needed since both use the same conventions:

| picoclaw arg | SWL expected |
|---|---|
| `path` | `path` ✓ |
| `content` | `content` ✓ |
| `old_text`/`new_text` | `old_text`/`new_text` ✓ |
| `command` | `command` ✓ |
| `url` | `url` ✓ |

The exec tool in picoclaw uses `action` + `command` args. PostHook normalizes: extract `command` from args, fallback to `action` field if needed.

---

## Files to Create

```
pkg/swl/config.go
pkg/swl/manager.go
pkg/swl/db.go
pkg/swl/types.go
pkg/swl/entity.go
pkg/swl/edge.go
pkg/swl/session.go
pkg/swl/extractor.go
pkg/swl/scanner.go
pkg/swl/hook.go
pkg/swl/tool.go
pkg/swl/query.go
pkg/swl/decay.go
pkg/swl/skill/SKILL.md
pkg/swl/manager_test.go
pkg/swl/extractor_test.go
pkg/swl/hook_test.go
pkg/swl/query_test.go
web/backend/api/swl.go
web/frontend/src/routes/swl.tsx
web/frontend/src/api/swl.ts
web/frontend/src/features/swl/graph.tsx
web/frontend/src/features/swl/stats.tsx
web/frontend/src/features/swl/legend.tsx
web/frontend/src/features/swl/hooks.ts
```

## Files to Modify

```
pkg/config/config_struct.go          add SWLConfig + SWL field to ToolsConfig
pkg/agent/instance.go                NewAgentInstance: create SWLManager, register QuerySWLTool
                                     AgentInstance: add SWLManager field
                                     Close(): call SWLManager.Close()
pkg/agent/loop.go                    Run(): call mountAgentSWLHooks()
                                     new method: mountAgentSWLHooks()
pkg/agent/context.go                 ContextBuilder: add extraSystemContent field
                                     AddInlineSystemContent(), BuildSystemPrompt() amendment
web/backend/api/router.go            RegisterRoutes(): add h.registerSWLRoutes(mux)
web/frontend/src/components/app-sidebar.tsx   add SWL nav item to Agent group
web/frontend/package.json            add react-force-graph-3d dependency
web/frontend/src/routes/__root.tsx   (check if any route tree update needed for /swl)
```

---

## Phased Implementation Plan

### Phase 1: Foundation — Types, DB, Entity/Edge CRUD
**Goal**: Working SQLite graph with correct upsert invariants  
**Files**: `types.go`, `db.go`, `entity.go`, `edge.go`, `config.go`  
**Checkpoint**: `go test ./pkg/swl/... -run TestUpsert`  

- Define all Go types (EntityType, EdgeRel, FactStatus, GraphDelta)
- Implement `openDB()` with WAL, schema DDL, migration scaffold
- Implement `upsertEntity()` with all five invariants (SQL ON CONFLICT clause)
- Implement `SetFactStatus()` with deleted-is-terminal guard
- Implement `upsertEdge()`, `confirmEdge()`
- Implement `applyDelta()` — transactional batch write
- Unit tests: upsert invariants (fact_status immutability, confidence monotonic, method priority)

### Phase 2: Session Management
**Goal**: Correct session lifecycle tied to picoclaw session keys  
**Files**: `session.go`, additions to `manager.go`  
**Checkpoint**: Session rows in DB with correct started_at/ended_at  

- Implement `startSession(sessionKey)` → UUID mapping
- Implement `endSession(sessionID, summary)` with auto-summary from stats
- Implement `sessionSync(sessionID)` — mtime-based file change detection
- Implement `workspaceSnapshot()` for session.workspace_state JSON
- Implement `activeSessions` map with thread-safe access

### Phase 3: Content Extraction
**Goal**: Accurate extraction from file content, exec output, web content  
**Files**: `extractor.go`  
**Checkpoint**: `go test ./pkg/swl/... -run TestExtract`  

- Compile all regex patterns once at init
- Implement `extractContent(fileID, content)` — symbols, imports, tasks, sections, URLs, topics
- Implement `extractDirectory(dirID, entries)` — topics from special files
- Implement `extractExec(command, stdout, stderr)` — git commits, test results, packages
- Implement `extractWeb(url, content)` — title, sections, linked URLs
- Enforce all anti-bloat limits
- Port noise symbol filter
- Port topic detection (special files → project-type, ext → language)
- Unit tests: extraction correctness, limit enforcement, edge cases (binary files, empty content)

### Phase 4: Workspace Scanner
**Goal**: Incremental mtime-based workspace indexing  
**Files**: `scanner.go`  
**Checkpoint**: `ScanWorkspace()` correctly detects new/changed/deleted files  

- Walk workspace directory respecting skip dirs and skip extensions
- Mtime comparison: skip unchanged files
- Detect deleted files: mark as FactDeleted, cascade children stale
- Stats return: {scanned, new, changed, deleted, skipped}
- 512KB file size limit
- Symlink handling: follow once, don't recurse through loops

### Phase 5: Three-Layer Inference + Hook
**Goal**: SWLHook intercepts picoclaw tool calls correctly  
**Files**: `hook.go`, additions to `manager.go`  
**Checkpoint**: Integration test: write_file tool call → entities in DB  

- Implement declarative tool map (`toolMap` var)
- Implement `_infer(tool, args, result)` — three layers in priority order
- Implement `PreHook(tool, args)` — guard registry + constraint checks
- Implement `PostHook(sessionKey, tool, args, result)` — infer + apply_delta + post-apply updates
- Wire all post-apply updates per tool:
  - write_file/edit_file: checkAndInvalidate + bump depth
  - append_file: null content_hash
  - read_file: checkAndInvalidate + bump depth + set verified (or stale on error)
  - delete_file: tombstone
  - web_fetch: set stale if error
- Implement `SWLHook` struct implementing `agent.ToolInterceptor`
- Agent filtering by agentID from TurnContext.Route
- Async AfterTool execution (goroutine, panic-safe)
- Unit tests: each tool's inference path, agent filter, panic recovery

### Phase 6: Query Interface
**Goal**: Full Tier 1/2/3 query capability  
**Files**: `query.go`  
**Checkpoint**: `manager.Ask("functions in main.go")` returns accurate results  

- Compile 30+ Tier 1 patterns
- Implement all `ask*` handler methods
- Implement `SessionResume()`, `KnowledgeGaps()`, `DriftReport()`
- Implement all 20+ Tier 2 SQL templates
- Implement `SafeQuery()` with SELECT-only validation and row limit
- Implement `Schema()` and `Stats()` methods
- Unit tests: each query pattern, SQL template, edge cases (empty DB, no matches)

### Phase 7: Decay System
**Goal**: Reactive maintenance with correct probabilistic decay  
**Files**: `decay.go`  
**Checkpoint**: `decayCheck()` correctly marks stale files/URLs  

- Implement `DecayCheck(entityID, limit)` — run decay handlers on eligible entities
- Implement File decay handler: `os.Stat(path)` check
- Implement URL decay handler: HTTP HEAD with 24h minimum between rechecks
- Implement `RegisterDecayHandler(entityType, fn)` for extensibility
- Implement `maybeDecay()` — 5% probability, 2 entities max
- Implement `maybePrune()` — prune events at 10k rows
- Implement VACUUM logic in `Close()`
- Unit tests: decay handler invocation, probability enforcement, 24h URL recheck guard

### Phase 8: QuerySWLTool + Config + AgentInstance Integration
**Goal**: End-to-end picoclaw integration, config-driven activation  
**Files**: `tool.go`, config_struct.go, instance.go modifications  
**Checkpoint**: `query_swl` appears in LLM tool list when SWL enabled in config  

- Implement `QuerySWLTool` with all input format dispatch
- Add `SWLConfig` to `pkg/config/config_struct.go`
- Add `SWL *SWLConfig` to `ToolsConfig`
- Implement helper: `cfg.Tools.IsSWLEnabled()`
- Wire `NewAgentInstance()`:  create Manager, register QuerySWLTool, store on instance
- Wire `AgentInstance.Close()` to call `SWLManager.Close()`
- Embed `skill/SKILL.md` via `//go:embed`
- Implement `SkillContent()` function
- Wire `ContextBuilder.AddInlineSystemContent()` + `BuildSystemPrompt()` amendment
- Call `contextBuilder.AddInlineSystemContent(swl.SkillContent())` in NewAgentInstance when enabled

### Phase 9: AgentLoop Hook Mounting
**Goal**: SWL hook active in running agent loop  
**Files**: loop.go modifications  
**Checkpoint**: AfterTool fires for actual agent tool calls and writes to DB  

- Add `mountAgentSWLHooks()` to AgentLoop
- Call from `Run()` after `ensureHooksInitialized()`
- Use `al.registry.ListAgentIDs()` + `al.registry.GetAgent()` to iterate
- Integration test: full turn with tool call → SWL entities written

### Phase 10: Frontend Visualization
**Goal**: Real-time 3D knowledge graph in web frontend  
**Files**: all frontend + web/backend/api/swl.go  
**Checkpoint**: Navigate to /swl, see live graph with bloom nodes  

- Install `react-force-graph-3d` via pnpm
- Implement `web/backend/api/swl.go`:
  - Read-only SQLite open to workspace DB
  - `/api/swl/graph` endpoint
  - `/api/swl/stats` endpoint  
  - `/api/swl/sessions` endpoint
  - `/api/swl/stream` SSE endpoint (2s poll, modified_at watermark)
- Register routes in `web/backend/api/router.go`
- Implement frontend components (graph.tsx, stats.tsx, legend.tsx, hooks.ts)
- Add `/swl` route file
- Add SWL TypeScript types (SWLNode, SWLLink, SWLGraphData, SWLStats)
- Add API client (`src/api/swl.ts`)
- Add sidebar nav item (app-sidebar.tsx)
- Add i18n key for navigation label
- Test: blank graph → run agent task → graph populates live

---

## Testing Strategy

### Unit tests per package (Go)
- `pkg/swl/manager_test.go` — full lifecycle: init → write_file → query → close
- `pkg/swl/extractor_test.go` — extraction correctness per language, limit enforcement
- `pkg/swl/hook_test.go` — BeforeTool/AfterTool mechanics, agent filter, panic recovery
- `pkg/swl/query_test.go` — all 30+ Tier 1 patterns, Tier 2 templates, Tier 3 SQL validation

### Invariant tests (must all pass)
Port the 15 most critical invariant tests from `dev/tests/test_swl.py`:
1. `fact_status` not reset by upsert
2. `confidence` monotonically increases
3. `extraction_method` priority (observed > stated > extracted > inferred)
4. Content hash change cascades children to stale
5. `knowledge_depth` resets to 1 on content change
6. `assert_note` sets depth to MAX(current, 2), not hardcoded 3
7. `append_file` nulls content_hash
8. Deleted status is terminal (cannot be reverted)
9. Session UUID stable across multiple PostHook calls with same session key
10. SessionSync correctly marks externally modified files stale
11. Decay handlers respect 24h recheck minimum for URLs
12. Events pruned at 10k rows
13. SafeQuery rejects non-SELECT SQL
14. Anti-bloat limits enforced (60 symbols, 40 imports, etc.)
15. Graph delta applied atomically (transaction rollback on error)

### Integration test
```go
func TestFullTurn(t *testing.T) {
    // Create Manager with temp workspace
    // Mount SWLHook
    // Simulate: write_file tool call → PreHook → PostHook
    // Assert: File entity in DB with correct metadata
    // Simulate: read_file → PostHook
    // Assert: depth bumped to 2, fact_status = verified
    // Call Ask("functions in main.go")
    // Assert: returns extracted symbols
}
```

### Frontend tests
- Component render test for SWLGraph (mock data)
- API client test (mock fetch)
- SSE subscription test (mock EventSource)

---

## Edge Cases & Risk Register

| # | Risk | Mitigation |
|---|---|---|
| 1 | SWL DB on read-only filesystem | Catch `openDB()` error, log warning, disable SWL for this session. Never crash agent. |
| 2 | Binary file sent to extractor | Check first 512 bytes for null bytes OR use `utf8.Valid()`. Skip binary files entirely. |
| 3 | Files >512KB sent to extractor | Enforce limit before regex: truncate to 512KB, still extract from truncated portion. |
| 4 | Concurrent writes from multiple agent turns | Single `db.SetMaxOpenConns(1)` + write mutex. SQLite WAL handles concurrent readers. |
| 5 | AfterTool goroutine outlives Manager.Close() | Add `sync.WaitGroup` for pending PostHook goroutines. `Close()` waits for all to drain. |
| 6 | Regex catastrophic backtracking on adversarial content | Use `context.WithTimeout(2s)` wrapping extraction. Cancel and skip on timeout. |
| 7 | Go regex no lookahead/lookbehind | All patterns rewritten as capture groups. Tested against SWL Python output. |
| 8 | SSE endpoint holds connection forever | Client disconnect detection: `r.Context().Done()`. Cleanup on disconnect. |
| 9 | Web backend reads DB while gateway is mid-transaction | SQLite WAL mode: readers never block on writers. Read-only connection is safe. |
| 10 | workspace path not set in config | Fallback: use `os.Getwd()`. Log warning if workspace undefined. |
| 11 | Multiple AgentInstances share same workspace | Both create Manager for same dbPath. Use package-level manager registry (workspace→Manager) to return the same instance. |
| 12 | Session key changes format across picoclaw versions | Session UUID stored in DB by session key hash. Old sessions remain but don't affect new ones. |
| 13 | SWL DB grows without bound | VACUUM on Close() when >50MB. Event pruning at 10k rows. Entities never deleted (only tombstoned). Operators can manually delete DB to reset. |
| 14 | `query_swl` tool conflicts with existing tool name | Check registry before registration. If conflict, log warning and use `swl_query` as fallback name. |
| 15 | Agent filter race (TurnContext.Route is nil) | Guard: `if call.Context == nil || call.Context.Route == nil { return Continue }`. |
| 16 | InjectSkillPrompt bloats context window | SKILL.md ~3,000 tokens. Default: inject. Config `inject_skill_prompt: false` to disable for small context models. |
| 17 | react-force-graph-3d large graph performance | Limit nodes to 500 most-recently-active entities in graph API response. Operator can increase via query param. |
| 18 | SSE updates cause graph thrashing | Debounce frontend updates: 500ms. Merge deltas before re-rendering. |
| 19 | `assert_note` depth bug reintroduction | Unit test: `assert_note` on depth-0 entity → verify depth becomes 2, not 3. |
| 20 | append_file hash bug reintroduction | Unit test: `append_file` → verify `content_hash` becomes NULL in DB. |

---

## Resumability Checkpoints

Each phase produces independently testable artifacts. Development can pause and resume at any phase boundary:

| After Phase | Resumability State |
|---|---|
| 1 | `pkg/swl/` package compiles. Upsert invariant tests pass. |
| 2 | Sessions created and closed correctly. DB has session rows. |
| 3 | Extraction unit tests pass for all languages. |
| 4 | `ScanWorkspace()` correctly indexes test workspace. |
| 5 | Simulated tool calls write correct entities to DB. |
| 6 | All query patterns return expected results. |
| 7 | Decay handlers mark stale entities correctly. |
| 8 | `query_swl` appears in agent tool list. Config controls activation. |
| 9 | Full agent turn → SWL DB populated. End-to-end integration test passes. |
| 10 | Frontend /swl route shows live 3D graph. Bloom effects visible. |

**Branch**: `claude/picoclaw-swl-analysis-XXpmh`  
**Commit granularity**: One commit per phase (atomic, passing tests at each commit)

---

## Summary of All Changes

### New Go package: `pkg/swl/` (13 source files + SKILL.md + tests)
### Modified Go files (5)
- `pkg/config/config_struct.go` — SWLConfig type, SWL field
- `pkg/agent/instance.go` — SWLManager init, QuerySWLTool registration, Close()
- `pkg/agent/loop.go` — mountAgentSWLHooks()
- `pkg/agent/context.go` — AddInlineSystemContent(), BuildSystemPrompt() amendment
- `web/backend/api/router.go` — registerSWLRoutes()

### New web backend file (1)
- `web/backend/api/swl.go` — 4 HTTP handlers, read-only DB access

### New frontend files (6)
- `web/frontend/src/routes/swl.tsx`
- `web/frontend/src/api/swl.ts`
- `web/frontend/src/features/swl/graph.tsx`
- `web/frontend/src/features/swl/stats.tsx`
- `web/frontend/src/features/swl/legend.tsx`
- `web/frontend/src/features/swl/hooks.ts`

### Modified frontend files (3)
- `web/frontend/package.json` — add react-force-graph-3d
- `web/frontend/src/components/app-sidebar.tsx` — SWL nav item
- i18n locale files — navigation.swl key
