# SWL Native Integration — PRD v2
## Retrospective, Revised Architecture & Implementation Plan

---

## Part I: Retrospective — What v1 Got Right, Wrong, and Missed

### What v1 Got Right

- **Go-native approach** — correct. No Python, no subprocess wrappers, no daemons.
- **SQLite WAL + upsert invariants** — correct. All five invariants are the critical correctness axis.
- **Three-layer inference pipeline** — correct design.
- **Reactive maintenance** — correct. No cron.
- **ToolInterceptor as the integration hook** — correct. Fires for every tool call.
- **Async AfterTool** — correct. Never blocks the agent turn.
- **Session key → UUID mapping** — correct.
- **10-phase approach with checkpoints** — correct structure.
- **Claude Code exclusion** — absolutely correct.
- **v7 anti-drift rules** — correct and necessary.

---

### What v1 Got Wrong

#### Problem 1: Static Go const enums for EntityType and EdgeRel

v1 defines:
```go
type EntityType string
const (
    EntityFile      EntityType = "File"
    EntityDirectory EntityType = "Directory"
    // ... 10 more fixed types
)
```

**Why this is wrong:** It violates SWL's core design principle: *"Generic: no assumptions about workspace structure."* A closed enum creates a closed universe of knowledge. Real workspaces contain Docker images, database schemas, API endpoints, GitHub issues, Kubernetes manifests, CI/CD pipelines, Jira tickets, calendar events, IoT sensor readings — none of which fit the 12 predefined types.

The entity type system must be an **open string space**: any string is valid. Well-known types are *conventions*, not constraints. The extraction layer uses conventions by default. Custom tool handlers emit any type they choose. The query layer and visualization handle unknowns gracefully.

**Corrected design:** `type EntityType = string` — a type alias, not an enum. Constants become documentation:
```go
const (
    KnownTypeFile    = "File"
    KnownTypeSymbol  = "Symbol"
    // ... "known" prefix signals convention, not constraint
)
```
The schema accepts any string. The visualization assigns default styling to unknown types.

Same applies to `EdgeRel`: any string is a valid relation. Well-known edges are conventions.

---

#### Problem 2: Embedding SKILL.md (3,000+ tokens injected into every LLM call)

v1 embeds `pkg/swl/skill/SKILL.md` via `//go:embed` and injects it into every system prompt.

**Why this is wrong:**
- 3,000+ tokens injected every call is wasteful and bloats the context window
- It is *unnecessary*: the `query_swl` tool's parameter schema IS the documentation
- Modern LLMs understand a tool from its name, description, and parameter schema — no external documentation required
- The SKILL.md was designed for the Python subprocess-wrapper world where the tool schema wasn't always present. In picoclaw, the tool is always in the schema.

**Corrected design:** Three-tier prompt strategy:
1. **Tool schema** (always present): rich description + parameter documentation — the primary interface
2. **Session hint** (~80 tokens, `hint_enabled: true` by default): "Your workspace semantic graph is always current. Call query_swl with `{\"resume\":true}` at session start. Use it before re-reading files you've touched before."
3. **Full SKILL.md** (kept in repo as reference for humans/operators, never auto-injected): available for manual placement as a workspace skill if an operator wants full documentation inline

Config changes: Remove `inject_skill_prompt` (which implied full SKILL.md). Replace with `hint_enabled bool` (80-token hint only).

---

#### Problem 3: Static tool extraction map — incomplete, not universal

v1's `toolMap` covers 8 named picoclaw core tools. This means:
- MCP-connected tools (any number, dynamically registered) get **no extraction**
- Installed skill tools get **no extraction**
- Future tools added to picoclaw get **no extraction** without editing `toolMap`

**This is inconsistent with the "generic" principle.** The hook already fires for ALL tool calls — the extraction layer should match.

**Corrected design:** Two-track extraction:
1. **Specific rules** for named core tools (more accurate, keeps in place)
2. **Catch-all generic extractor** for any tool not in the specific-rules map:
   ```
   extractGeneric(tool, args, result):
       - Scan all string arg values for file paths (regex: looks like path)
       - Scan all string arg values for URLs (http/https)
       - Scan all string arg values for key named "command"/"cmd" → Command entity
       - Record tool call itself as a Command entity with tool name
       - Use confidence 0.8 (inferred — less precise than specific rules)
   ```
   This makes SWL *universally applicable* to any tool without code changes.

---

#### Problem 4: No LLM context interception — a large missed signal

v1 only hooks tool calls. The agent generates substantial semantic information *between* tool calls:
- The user's message contains file paths, URLs, goals, task descriptions
- The LLM's text response contains file mentions, plans, reasoning about code
- The LLM's *reasoning/thinking content* (`ReasoningContent`, present for thinking models) contains deep analytical insights
- SubTurn spawns create parent/child agent relationships worth recording

picoclaw exposes exactly the data needed:
- `EventKindTurnStart` → `TurnStartPayload.UserMessage string` — user's message text
- `LLMInterceptor.AfterLLM` → `LLMHookResponse.Response.Content string` — agent's response text
- `LLMHookResponse.Response.ReasoningContent string` — thinking/reasoning text
- `EventKindSubTurnSpawn` → `SubTurnSpawnPayload.{AgentID, Label, ParentTurnID}`

`SWLHook` must implement **three interfaces** simultaneously (picoclaw's HookManager dispatches to all applicable interfaces):
- `ToolInterceptor` — tool call interception (existing)
- `LLMInterceptor` — AfterLLM for response/reasoning extraction (new)
- `EventObserver` — TurnStart for user intent + SubTurn for provenance (new)

**What AfterLLM extraction adds:**
- File paths *mentioned* in LLM response text → entity at confidence 0.7 (weakly implied), depth bumped to 1
- URLs mentioned in text → URL entity
- LLM assertion of understanding (e.g. "This function handles authentication") → candidate for assert_note
- Reasoning text extraction: file paths and concepts mentioned in extended thinking (confidence 0.75 — agent is reasoning about them)
- All extraction is async, never blocks the LLM response pipeline

**What TurnStart extraction adds:**
- User mentions "update auth.py" → `auth.py` entity at confidence 0.8 (inferred from user context)
- User mentions a URL → URL entity
- First message of session → set `sessions.goal` field (first 200 chars of user message)

---

### What v1 Missed

#### Missed 5: SubTurn/multi-agent provenance

When picoclaw spawns a subagent (`EventKindSubTurnSpawn`), the child operates in the same workspace DB. The `SubTurnSpawnPayload` contains `AgentID`, `Label`, and `ParentTurnID`. SWL should:
- Record a `Session` entity for the subagent's work, linked to the parent session
- Tag entities touched during a subturn with the source agent ID (stored in edge `source_session` metadata)
- This enables queries like "what did the refactoring subagent touch?"

#### Missed 6: Session goal field always empty

v1 has a `sessions.goal` column but never populates it. The goal is highly valuable for `session_resume()` and the "bring me up to speed" query. Fix: extract from first user message of session via `EventKindTurnStart`.

#### Missed 7: Reasoning/thinking content as a knowledge signal

Thinking models (`ReasoningContent`, `Reasoning`) express the agent's understanding in detail: "I can see that auth.py uses RS256 JWT tokens with a 24h expiry..." This is *deep, reliable* knowledge. It should be processed at confidence 0.85 (stated — the agent is asserting understanding, same as `assert_note`). This is *automatically* a depth-3 knowledge signal for any entity mentioned.

This is not available via EventObserver (events don't carry content). It requires `LLMInterceptor.AfterLLM` which has `Response.ReasoningContent`.

#### Missed 8: Dynamic agent registration for multi-agent workspaces

v1 mounts SWL hooks in `mountAgentSWLHooks()` once at `Run()`. But picoclaw can theoretically add agents dynamically. The mounting should be idempotent and re-callable. Minor, but noted.

---

## Part II: Revised Design Decisions

### Decision 1: Open type system

`EntityType` and `EdgeRel` are `string` aliases. The schema accepts any value. Well-known constants are prefixed `Known*` to signal they are conventions, not constraints. The extraction layer uses well-known constants. Custom integrations emit any strings they need.

**Impact:** The graph is now truly universal. A custom tool handler for a Docker tool can emit `EntityType("DockerImage")` and `EdgeRel("depends_on_image")` without touching SWL core code.

### Decision 2: Prompt injection — hint only, no SKILL.md

The `query_swl` tool description is the documentation. A minimal session hint (~80 tokens) primes the LLM's behavior. Full SKILL.md is excluded from the runtime binary (stays as `docs/design/swl-skill-reference.md`).

Config: `hint_enabled: true` (default). The hint is a Go constant, not an embedded file.

### Decision 3: SWLHook implements three interfaces

```go
type SWLHook struct { manager *Manager; agentID string }

// Implements ToolInterceptor:
func (h *SWLHook) BeforeTool(...) (...)
func (h *SWLHook) AfterTool(...)  (...)

// Implements LLMInterceptor:
func (h *SWLHook) BeforeLLM(...) (...) // pass-through, no-op
func (h *SWLHook) AfterLLM(...)  (...) // extract from response + reasoning (async)

// Implements EventObserver:
func (h *SWLHook) OnEvent(ctx, evt Event) error // TurnStart + SubTurnSpawn
```

picoclaw's `HookManager` type-asserts each registered hook against all three interfaces independently. A single hook registration covers all three capabilities.

### Decision 4: Universal tool coverage via catch-all extractor

The `extractGeneric()` function runs for any tool name not found in the specific-rules map. It uses heuristic scanning of all arg values. Core tools use specific rules (better precision). Everything else uses the catch-all.

### Decision 5: Confidence scale for new signal sources

| Source | Confidence | Extraction Method | Depth impact |
|---|---|---|---|
| Tool arg (e.g. `args.path`) | 1.0 | observed | 0 → 1 |
| Content regex (symbols, imports) | 0.9 | extracted | → 1 |
| LLM response text mention | 0.7 | inferred | → 1 |
| LLM reasoning text mention | 0.75 | inferred | → 1 |
| LLM reasoning assertion | 0.85 | stated | → max(current, 2) |
| User message mention | 0.8 | inferred | → 1 |
| Directory listing inference | 0.8 | inferred | → 0 |
| Catch-all generic extractor | 0.8 | inferred | → 0 |

### Decision 6: Session goal from TurnStart

On `EventKindTurnStart`, if `manager.isFirstTurnOfSession(sessionKey)`:
- Set `sessions.goal = first 200 chars of UserMessage`
- Extract entities from UserMessage (paths, URLs)
- This makes `session_resume()` much more useful

---

## Part III: Revised Architecture

```
┌────────────────────────────────────────────────────────────────┐
│  picoclaw AgentInstance                                        │
│  ├── Tools (ToolRegistry)                                      │
│  │   └── QuerySWLTool ──────────────────────────────────┐     │
│  ├── SWLManager *swl.Manager ──────────────────────┐    │     │
│  └── ContextBuilder                                 │    │     │
│      └── AddSessionHint(swl.SessionHint())          │    │     │
└────────────────────────────────────────────────────-│----│-----┘
                                                       │    │
┌──────────────────────────────────────────────────────│----│-----┐
│  picoclaw AgentLoop                                  │    │     │
│  └── HookManager                                     │    │     │
│      └── SWLHook ────────────────────────────────────┘    │     │
│          implements THREE interfaces:                       │     │
│          ToolInterceptor  → tool call capture              │     │
│          LLMInterceptor   → response/reasoning extraction   │     │
│          EventObserver    → user intent + subturn provenance│     │
└─────────────────────────────────────────────────────────────│-----┘
                                                               │
┌─────────────────────────────────────────────────────────────▼-----┐
│  pkg/swl.Manager                                                   │
│                                                                    │
│  DB: {workspace}/.swl/swl.db (SQLite WAL)                         │
│  Type system: OPEN strings (EntityType = string, EdgeRel = string) │
│                                                                    │
│  PreHook(tool, args)           → guard + constraint check          │
│  PostHook(session, tool, args, result)                             │
│    ├── Layer 0: programmatic registry (custom handlers)            │
│    ├── Layer 1: specific rules (core tools)                        │
│    ├── Layer 2: semantic extraction (regex)                        │
│    └── Layer 3: catch-all generic (ALL unknown tools)              │
│                                                                    │
│  PostLLM(session, content, reasoning)  → async text extraction     │
│  OnTurnStart(session, userMessage)     → intent + goal extraction  │
│  OnSubTurnSpawn(parent, child, label)  → provenance recording      │
│                                                                    │
│  Ask(question) → Tier1/Tier2/Tier3 query                          │
│  ScanWorkspace(root) → incremental mtime scan                      │
└──────────────────────────────────────────────────────────────------┘
                         │
                         │ read-only SQLite (separate process, no IPC)
                         ▼
┌─────────────────────────────────────────────────────────────────────┐
│  web/backend/api/swl.go                                             │
│  GET /api/swl/graph    → nodes + links JSON (≤500 most active)      │
│  GET /api/swl/stats    → health stats                               │
│  GET /api/swl/sessions → session list with goals                    │
│  GET /api/swl/stream   → SSE: 2s poll on modified_at watermark      │
└────────────────────────────────────────────────────────────---------┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────────┐
│  web/frontend /swl route                                            │
│  3D force graph (react-force-graph-3d)                              │
│  ├── Nodes: entities — color by type, size by access_count         │
│  ├── Bloom: depth-3 nodes + recently-active nodes (emissive mat)   │
│  ├── Links: edges — color by rel type, directional arrows          │
│  ├── Stats panel: counts, session goals, stale/unknown breakdown   │
│  ├── Agent selector (multi-agent workspaces)                       │
│  └── SSE subscription → live graph updates as agent works          │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Part IV: Complete Data Model (v2)

### Open Type System

```go
// EntityType is an open string — any value is valid.
// The constants below are well-known conventions used by the extraction layer.
type EntityType = string

const (
    KnownTypeFile       = "File"
    KnownTypeDirectory  = "Directory"
    KnownTypeSymbol     = "Symbol"
    KnownTypeDependency = "Dependency"
    KnownTypeTask       = "Task"
    KnownTypeSection    = "Section"
    KnownTypeTopic      = "Topic"
    KnownTypeURL        = "URL"
    KnownTypeCommit     = "Commit"
    KnownTypeSession    = "Session"
    KnownTypeNote       = "Note"
    KnownTypeCommand    = "Command"
    // Operators / custom handlers may emit any string beyond these.
)

// EdgeRel is an open string — any relation is valid.
type EdgeRel = string

const (
    KnownRelDefines     = "defines"
    KnownRelImports     = "imports"
    KnownRelHasTask     = "has_task"
    KnownRelHasSection  = "has_section"
    KnownRelMentions    = "mentions"
    KnownRelDependsOn   = "depends_on"
    KnownRelTagged      = "tagged"
    KnownRelInDir       = "in_dir"
    KnownRelWrittenIn   = "written_in"
    KnownRelEditedIn    = "edited_in"
    KnownRelAppendedIn  = "appended_in"
    KnownRelRead        = "read"
    KnownRelFetched     = "fetched"
    KnownRelExecuted    = "executed"
    KnownRelDeleted     = "deleted"
    KnownRelDescribes   = "describes"
    KnownRelCommittedIn = "committed_in"
    KnownRelFound       = "found"
    KnownRelListed      = "listed"
    KnownRelSpawnedBy   = "spawned_by"  // subturn provenance
    // Operators / custom handlers may emit any string beyond these.
)
```

### GraphDelta (unchanged from v1 — correct design)

```go
type EntityTuple struct {
    ID               string
    Type             EntityType  // open string
    Name             string
    Metadata         map[string]any
    Confidence       float64
    ExtractionMethod ExtractionMethod
    KnowledgeDepth   int
}

type EdgeTuple struct {
    FromID string
    Rel    EdgeRel  // open string
    ToID   string
}

type GraphDelta struct {
    Entities []EntityTuple
    Edges    []EdgeTuple
}
```

### ExtractionMethod and FactStatus (remain as constrained strings — these ARE closed enums)

These two fields govern correctness invariants and MUST be constrained:

```go
type ExtractionMethod string
const (
    MethodObserved  ExtractionMethod = "observed"  // confidence 1.0  — arg directly names entity
    MethodExtracted ExtractionMethod = "extracted" // confidence 0.9  — regex from content
    MethodStated    ExtractionMethod = "stated"    // confidence 0.85 — LLM assertion / reasoning
    MethodInferred  ExtractionMethod = "inferred"  // confidence 0.8  — derived / catch-all
    MethodWeak      ExtractionMethod = "weak"      // confidence 0.7  — text mention
)

type FactStatus string
const (
    FactUnknown  FactStatus = "unknown"   // never verified
    FactVerified FactStatus = "verified"  // confirmed correct
    FactStale    FactStatus = "stale"     // known outdated
    FactDeleted  FactStatus = "deleted"   // terminal — entity gone
)
```

Rationale: EntityType/EdgeRel are domain vocabulary (open). FactStatus/ExtractionMethod are epistemic machinery (closed). This distinction is the key design insight v1 missed.

### SQLite Schema (updated)

The schema is identical to v1 except one addition:
```sql
-- entities.type and edges.rel accept ANY string (no CHECK constraint)
-- This enforces the open type system at the DB level.

-- New: tag edges with source agent for provenance
-- edges.source_session already exists; add source_agent:
ALTER TABLE edges ADD COLUMN source_agent TEXT;

-- sessions: ensure goal is always set
-- sessions.goal column already exists — set from first user message
```

---

## Part V: Package Structure (v2)

```
pkg/swl/
├── config.go        SWLConfig: hint_enabled (not inject_skill_prompt)
├── manager.go       Manager struct + public API (PostLLM, OnTurnStart, OnSubTurnSpawn added)
├── db.go            DB init, WAL, schema, migrations (open-type schema)
├── types.go         Open EntityType/EdgeRel, closed FactStatus/ExtractionMethod, GraphDelta
├── entity.go        UpsertEntity, SetFactStatus, ApplyDelta, InvalidateChildren
├── edge.go          UpsertEdge with optional source_agent field
├── session.go       StartSession, EndSession, SetGoal, SessionSync, WorkspaceSnapshot
├── extractor.go     ExtractContent, ExtractDirectory, ExtractExec, ExtractWeb,
│                    ExtractText (NEW: for LLM response/reasoning text),
│                    ExtractGeneric (NEW: catch-all for unknown tools)
├── scanner.go       ScanWorkspace — incremental mtime-based walker
├── hook.go          SWLHook — implements ToolInterceptor + LLMInterceptor + EventObserver
├── tool.go          QuerySWLTool — self-documenting via schema, no SKILL.md reference
├── hint.go          SessionHint() — 80-token system prompt hint constant (NEW, replaces SKILL.md embed)
├── query.go         Ask() — Tier1/Tier2/Tier3
└── decay.go         DecayCheck, MaybeDecay, MaybePrune, VACUUM
```

No `skill/SKILL.md` in the binary. The full SKILL.md lives as `docs/design/swl-skill-reference.md` (for humans/operators).

---

## Part VI: Hook Implementation (v2)

### SWLHook — three interfaces, one registration

```go
type SWLHook struct {
    manager  *Manager
    agentID  string
    wg       sync.WaitGroup // tracks async PostHook and PostLLM goroutines
}

// ToolInterceptor: BeforeTool
func (h *SWLHook) BeforeTool(ctx context.Context, call *ToolCallHookRequest) (
    *ToolCallHookRequest, HookDecision, error,
) {
    if !h.isMyAgent(call.Meta) { return call, Continue, nil }
    decision := h.manager.PreHook(call.Tool, call.Arguments)
    if decision == "abort" {
        return call, HookDecision{Action: HookActionDenyTool, Reason: decision.Reason}, nil
    }
    return call, Continue, nil
}

// ToolInterceptor: AfterTool
func (h *SWLHook) AfterTool(ctx context.Context, result *ToolResultHookResponse) (
    *ToolResultHookResponse, HookDecision, error,
) {
    if !h.isMyAgent(result.Meta) { return result, Continue, nil }
    sessionKey := h.sessionKey(result.Meta)
    tool := result.Tool
    args := result.Arguments
    res := result.Result
    h.wg.Add(1)
    go func() {
        defer h.wg.Done()
        defer recoverSWL("AfterTool", tool)
        h.manager.PostHook(sessionKey, tool, args, res)
    }()
    return result, Continue, nil
}

// LLMInterceptor: BeforeLLM — always pass through, no-op
func (h *SWLHook) BeforeLLM(ctx context.Context, req *LLMHookRequest) (
    *LLMHookRequest, HookDecision, error,
) {
    return req, Continue, nil
}

// LLMInterceptor: AfterLLM — extract from response text and reasoning (async)
func (h *SWLHook) AfterLLM(ctx context.Context, resp *LLMHookResponse) (
    *LLMHookResponse, HookDecision, error,
) {
    if !h.isMyAgent(resp.Meta) { return resp, Continue, nil }
    if resp.Response == nil   { return resp, Continue, nil }
    content   := resp.Response.Content
    reasoning := resp.Response.ReasoningContent
    if len(strings.TrimSpace(content)) == 0 && len(strings.TrimSpace(reasoning)) == 0 {
        return resp, Continue, nil
    }
    sessionKey := h.sessionKey(resp.Meta)
    h.wg.Add(1)
    go func() {
        defer h.wg.Done()
        defer recoverSWL("AfterLLM", "")
        h.manager.PostLLM(sessionKey, content, reasoning)
    }()
    return resp, Continue, nil
}

// EventObserver: OnEvent — TurnStart (user intent) + SubTurnSpawn (provenance)
func (h *SWLHook) OnEvent(ctx context.Context, evt Event) error {
    if evt.Meta.AgentID != h.agentID { return nil }
    switch evt.Kind {
    case EventKindTurnStart:
        if p, ok := evt.Payload.(TurnStartPayload); ok {
            sessionKey := evt.Meta.SessionKey
            go func() {
                defer recoverSWL("OnTurnStart", "")
                h.manager.OnTurnStart(sessionKey, evt.Meta.TurnID, p.UserMessage)
            }()
        }
    case EventKindSubTurnSpawn:
        if p, ok := evt.Payload.(SubTurnSpawnPayload); ok {
            go func() {
                defer recoverSWL("OnSubTurnSpawn", "")
                h.manager.OnSubTurnSpawn(
                    evt.Meta.SessionKey, evt.Meta.TurnID,
                    p.AgentID, p.Label, p.ParentTurnID,
                )
            }()
        }
    }
    return nil
}

// Close waits for all in-flight async operations before closing the manager
func (h *SWLHook) Close() error {
    h.wg.Wait()
    return h.manager.Close()
}
```

### isMyAgent — agent isolation in the global hook

```go
func (h *SWLHook) isMyAgent(meta EventMeta) bool {
    return meta.AgentID == h.agentID
}

func (h *SWLHook) sessionKey(meta EventMeta) string {
    return meta.SessionKey // picoclaw sets this from channel+chatID
}
```

---

## Part VII: Manager API (v2)

New methods beyond v1:

```go
// PostLLM processes LLM response text and reasoning for entity extraction.
// Called asynchronously by SWLHook.AfterLLM. Never blocks.
func (m *Manager) PostLLM(sessionKey, content, reasoning string)

// OnTurnStart processes the start of an agent turn:
// - Sets session goal (first turn only)
// - Extracts entities from user message text
func (m *Manager) OnTurnStart(sessionKey, turnID, userMessage string)

// OnSubTurnSpawn records parent→child turn relationship.
func (m *Manager) OnSubTurnSpawn(parentSessionKey, parentTurnID, childAgentID, label, picoParentTurnID string)

// RegisterTool registers a custom inference function for a named tool.
// Takes full precedence over Layer 1 and Layer 2 inference.
func (m *Manager) RegisterTool(toolName string, fn func(args, result map[string]any) *GraphDelta)

// RegisterDecayHandler registers a custom decay handler for an entity type.
func (m *Manager) RegisterDecayHandler(entityType EntityType, fn func(id string, meta map[string]any) FactStatus)

// AddGuard registers a call-time guard. fn returns false to block the tool call.
func (m *Manager) AddGuard(name string, fn func(tool string, args map[string]any) bool)

// AddConstraint registers an SQL-based graph-state invariant.
func (m *Manager) AddConstraint(name, selectCountSQL, action string)
```

---

## Part VIII: Extractor v2 (new functions)

### extractText — for LLM response content and reasoning

```go
// extractText performs a lightweight pass on text (LLM response or user message).
// Used for AfterLLM and OnTurnStart.
// Confidence is lower (0.7 for response text, 0.75 for reasoning, 0.8 for user message).
func extractText(sourceID, text string, conf float64, method ExtractionMethod) *GraphDelta {
    delta := &GraphDelta{}
    if len(text) == 0 { return delta }

    // Extract file paths mentioned in text
    // Pattern: sequences that look like paths (start with ./ ../ / ~/ or contain /)
    for _, path := range extractMentionedPaths(text) {
        id := normalizePath(path)
        delta.Entities = append(delta.Entities, EntityTuple{
            ID: id, Type: KnownTypeFile, Name: filepath.Base(id),
            Confidence: conf, ExtractionMethod: method, KnowledgeDepth: 0,
        })
        delta.Edges = append(delta.Edges, EdgeTuple{
            FromID: sourceID, Rel: KnownRelMentions, ToID: id,
        })
    }

    // Extract URLs
    for _, u := range urlRE.FindAllString(text, maxURLs) {
        delta.Entities = append(delta.Entities, EntityTuple{
            ID: u, Type: KnownTypeURL, Name: u,
            Confidence: conf, ExtractionMethod: method, KnowledgeDepth: 0,
        })
        delta.Edges = append(delta.Edges, EdgeTuple{
            FromID: sourceID, Rel: KnownRelMentions, ToID: u,
        })
    }

    return delta
}
```

For **reasoning content** (confidence 0.85, method `stated`): entities mentioned in the reasoning text are bumped to `MAX(depth, 2)` — the agent is explicitly reasoning about them, which constitutes active engagement.

### extractGeneric — catch-all for unknown tools

```go
// extractGeneric handles any tool not in the specific-rules map.
// Scans all arg values for recognizable patterns.
func extractGeneric(tool string, args map[string]any, result *tools.ToolResult) *GraphDelta {
    delta := &GraphDelta{}

    // Record the tool call as a Command entity
    cmdID := "cmd:" + shortHash(tool+jsonHash(args))
    delta.Entities = append(delta.Entities, EntityTuple{
        ID: cmdID, Type: KnownTypeCommand, Name: tool,
        Metadata: map[string]any{"tool": tool},
        Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 0,
    })

    // Scan all string arg values
    for _, v := range args {
        s, ok := v.(string)
        if !ok || len(s) == 0 { continue }

        // File path detection
        if looksLikePath(s) {
            id := normalizePath(s)
            delta.Entities = append(delta.Entities, EntityTuple{
                ID: id, Type: KnownTypeFile, Name: filepath.Base(id),
                Confidence: 0.8, ExtractionMethod: MethodInferred,
            })
            delta.Edges = append(delta.Edges, EdgeTuple{FromID: cmdID, Rel: KnownRelExecuted, ToID: id})
        }

        // URL detection
        if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
            delta.Entities = append(delta.Entities, EntityTuple{
                ID: s, Type: KnownTypeURL, Name: s,
                Confidence: 0.8, ExtractionMethod: MethodInferred,
            })
        }
    }

    return delta
}
```

---

## Part IX: QuerySWLTool v2 — Self-Documenting Interface

The tool description IS the documentation. No SKILL.md injection needed.

```go
func (t *QuerySWLTool) Description() string {
    return `Query your persistent semantic workspace knowledge graph.

The graph is built automatically from every tool call and LLM response in this
and all previous sessions. Use it to recall what you know without re-reading files.

Common queries:
  {"resume": true}                          — What did the last session do? (use at session start)
  {"question": "functions in auth.go"}      — Natural language questions about the workspace
  {"question": "what imports database/sql"} — Dependency queries
  {"question": "open todos"}                — Task queries
  {"gaps": true}                            — What files haven't been read yet?
  {"drift": true}                           — What knowledge may be outdated?
  {"assert": "auth.go uses RS256 JWT",
   "subject": "auth.go",
   "confidence": 0.9,
   "note_type": "behavioral"}              — Record your own understanding
  {"stats": true}                           — Graph health statistics
  {"schema": true}                          — Full schema for custom SQL queries
  {"sql": "SELECT id, type FROM entities WHERE type='File' LIMIT 20"}
  {"scan": true, "root": "."}              — Re-index workspace after external changes`
}
```

### Session hint (replaces SKILL.md injection)

```go
// pkg/swl/hint.go
const sessionHint = `Your workspace has a persistent semantic knowledge graph (SWL).
It updates automatically from all tool calls and session history.
Start each session with query_swl {"resume":true} to see where you left off.
Use query_swl before re-reading files — it may already know what you need.`

func SessionHint() string { return sessionHint }
```

Injected via `contextBuilder.AddInlineSystemContent(swl.SessionHint())` — approximately 60 tokens, not 3,000.

---

## Part X: Configuration (v2)

### SWLConfig (updated)

```go
type SWLConfig struct {
    Enabled          bool   `json:"enabled"`
    HintEnabled      bool   `json:"hint_enabled,omitempty"`      // default true (60-token hint)
    MaxFileSizeBytes int64  `json:"max_file_size_bytes,omitempty"` // default 524288
    DBPath           string `json:"db_path,omitempty"`            // default {workspace}/.swl/swl.db
    // Extraction feature flags (all default true)
    ExtractSymbols   bool   `json:"extract_symbols,omitempty"`
    ExtractImports   bool   `json:"extract_imports,omitempty"`
    ExtractTasks     bool   `json:"extract_tasks,omitempty"`
    ExtractSections  bool   `json:"extract_sections,omitempty"`
    ExtractURLs      bool   `json:"extract_urls,omitempty"`
    // New in v2: LLM context interception feature flags
    ExtractLLMText      bool `json:"extract_llm_text,omitempty"`      // default true
    ExtractLLMReasoning bool `json:"extract_llm_reasoning,omitempty"` // default true
    ExtractUserMessages bool `json:"extract_user_messages,omitempty"` // default true
}
```

Removed: `inject_skill_prompt` (replaced by `hint_enabled`).

### Example config.json
```json
{
  "tools": {
    "swl": {
      "enabled": true
    }
  }
}
```

All feature flags default to `true`. Minimal config to enable.

---

## Part XI: Files to Create and Modify (v2 delta from v1)

### Changed from v1

| File | v1 | v2 change |
|---|---|---|
| `pkg/swl/types.go` | EntityType/EdgeRel as const enums | Open string aliases; `Known*` prefix constants |
| `pkg/swl/hook.go` | ToolInterceptor only | +LLMInterceptor +EventObserver; Close() drains WaitGroup |
| `pkg/swl/extractor.go` | 8 specific tool rules | +extractText() +extractGeneric() catch-all |
| `pkg/swl/config.go` | inject_skill_prompt | hint_enabled; extract_llm_text; extract_llm_reasoning; extract_user_messages |
| `pkg/swl/manager.go` | no LLM context methods | +PostLLM, +OnTurnStart, +OnSubTurnSpawn |
| `pkg/swl/session.go` | goal never set | SetGoal() called from OnTurnStart on first turn |
| `pkg/swl/tool.go` | minimal description | Full self-documenting description with examples |
| `pkg/swl/hint.go` | (embedded SKILL.md) | New file: SessionHint() constant function (60 tokens) |
| `pkg/swl/skill/SKILL.md` | embedded in binary | REMOVED from binary; moved to docs/design/ |

### New files (v2 only)
```
pkg/swl/hint.go     — SessionHint() 60-token constant
```

### Removed from v2
```
pkg/swl/skill/SKILL.md   (moved to docs/design/swl-skill-reference.md)
```

### All other files same as v1 plan:
```
pkg/swl/db.go, entity.go, edge.go, scanner.go, query.go, decay.go
pkg/config/config_struct.go (SWLConfig + SWL field)
pkg/agent/instance.go (SWLManager, QuerySWLTool, Close)
pkg/agent/loop.go (mountAgentSWLHooks)
pkg/agent/context.go (AddInlineSystemContent, SessionHint injection)
web/backend/api/router.go, web/backend/api/swl.go
web/frontend/src/routes/swl.tsx
web/frontend/src/api/swl.ts
web/frontend/src/features/swl/{graph,stats,legend,hooks}.tsx
web/frontend/src/components/app-sidebar.tsx
web/frontend/package.json
```

---

## Part XII: Phased Implementation Plan (v2)

### Phase 1: Foundation — Open Type System, DB, CRUD
**Changes from v1:** Open string types instead of const enums  
**Files:** `types.go`, `db.go`, `entity.go`, `edge.go`, `config.go`  
**Checkpoint:** `go test ./pkg/swl/... -run TestUpsert`

- Define open `EntityType = string` and `EdgeRel = string`
- Define closed `FactStatus` and `ExtractionMethod` (these remain constrained)
- Implement `openDB()` with WAL, schema DDL (no CHECK constraints on type/rel columns)
- Implement `upsertEntity()` with all five invariants (SQL ON CONFLICT)
- Implement `SetFactStatus()` with deleted-is-terminal guard
- Implement `upsertEdge()`, `applyDelta()`
- Unit tests: open type acceptance, upsert invariants

### Phase 2: Session Management + Goal Extraction
**Changes from v1:** `SetGoal()` and `OnTurnStart()` wiring  
**Files:** `session.go`, partial `manager.go`  
**Checkpoint:** Sessions created with goals set; DB has correct rows

- Implement `startSession(sessionKey)` → UUID, `isFirstTurn()` detection
- Implement `SetGoal(sessionID, goal)` — called on first turn
- Implement `OnTurnStart(sessionKey, turnID, userMessage)` — sets goal, queues entity extraction
- Implement `endAllSessions()`, `sessionSync()`, `workspaceSnapshot()`
- Unit tests: goal extraction from user message, session deduplication

### Phase 3: Content Extraction (specific rules + text + catch-all)
**Changes from v1:** `extractText()` and `extractGeneric()` added  
**Files:** `extractor.go`  
**Checkpoint:** `go test ./pkg/swl/... -run TestExtract`

- Port all Go regex patterns (with lookahead rewrite where needed)
- Implement `extractContent()` (symbols, imports, tasks, sections, URLs, topics)
- Implement `extractDirectory()`, `extractExec()`, `extractWeb()`
- NEW: `extractText(sourceID, text, conf, method)` — for LLM response and user messages
- NEW: `extractGeneric(tool, args, result)` — catch-all for unknown tools
  - Path detection: `looksLikePath()` heuristic
  - URL detection: prefix check
  - Command entity recording
- Enforce anti-bloat limits throughout
- Unit tests: extraction per language, text extraction, catch-all on fake MCP tool

### Phase 4: Workspace Scanner
**Same as v1 Phase 4**  
**Files:** `scanner.go`

### Phase 5: Three-Layer Inference + ToolInterceptor
**Changes from v1:** Layer 3 (catch-all) added  
**Files:** `hook.go` (ToolInterceptor portion), partial `manager.go`  
**Checkpoint:** Integration test: write_file + unknown MCP tool → both in DB

- Implement `toolMap` for known core tools (Layer 1 declarative rules)
- Implement `_infer()` — Layers 0→1→2→3, where Layer 3 = `extractGeneric()`
- Implement `PostHook()` wiring all layers
- Implement `SWLHook.BeforeTool()` and `SWLHook.AfterTool()`
- Unit tests: known tool → specific extraction; unknown tool → catch-all extraction

### Phase 6: LLM Context Interception
**New in v2**  
**Files:** `hook.go` (LLMInterceptor + EventObserver portions), `manager.go`  
**Checkpoint:** LLM response mentioning a file path → entity in DB at conf=0.7

- Implement `SWLHook.BeforeLLM()` (pass-through)
- Implement `SWLHook.AfterLLM()` — async call to `manager.PostLLM()`
- Implement `manager.PostLLM(sessionKey, content, reasoning)`:
  - `extractText(sessionID, content, 0.7, MethodWeak)` for response text
  - `extractText(sessionID, reasoning, 0.75, MethodInferred)` for reasoning text
  - Assertions detected in reasoning text (pattern: "X uses Y", "X is a Z") → `assertNote()` with conf 0.85
- Implement `SWLHook.OnEvent()` for TurnStart and SubTurnSpawn
- Implement `manager.OnSubTurnSpawn()` — create session link edge with `KnownRelSpawnedBy`
- Unit tests: LLM response extraction, reasoning extraction, TurnStart goal setting

### Phase 7: Query Interface
**Same as v1 Phase 6**  
**Files:** `query.go`

### Phase 8: Decay System
**Same as v1 Phase 7**  
**Files:** `decay.go`

### Phase 9: QuerySWLTool + Hint + Config Integration
**Changes from v1:** Self-documenting tool, hint instead of SKILL.md  
**Files:** `tool.go`, `hint.go`, `config_struct.go`, `instance.go` changes  
**Checkpoint:** `query_swl` in tool list; hint in system prompt (~60 tokens not ~3000)

- Implement `QuerySWLTool` with full self-documenting description
- Create `hint.go` with `SessionHint()` constant
- Add `SWLConfig` to `pkg/config/config_struct.go`
- Wire `NewAgentInstance()`: SWLManager creation, QuerySWLTool registration, hint injection
- Verify: system prompt length increase = ~60 tokens (not 3000)
- Unit test: hint is ≤100 tokens

### Phase 10: AgentLoop Hook Mounting (all three interfaces)
**Changes from v1:** SWLHook implements all three interfaces  
**Files:** `loop.go` changes  
**Checkpoint:** AfterLLM fires and extracts entities from response text

- Verify `SWLHook` satisfies `ToolInterceptor`, `LLMInterceptor`, `EventObserver`
- Verify `HookManager` dispatches all three correctly (test with multi-interface hook)
- Integration test: full turn → tool call + LLM response → both in DB

### Phase 11: Frontend Visualization
**Same as v1 Phase 10 + agent selector dropdown**  
**Files:** all frontend + `web/backend/api/swl.go`  
**Checkpoint:** /swl shows live 3D graph with bloom; agent selector works; SSE updates live

---

## Part XIII: All Upsert Invariants (v2 — same as v1, restated for clarity)

CRITICAL — must be enforced in every code path:

1. **`fact_status` never reset by `upsertEntity()`** — ONLY `SetFactStatus()` changes it
2. **`confidence = MAX(existing, new)`** — never decreases on re-upsert
3. **`knowledge_depth = MAX(existing, new)`** — never decreases (resets to 1 only on content hash change)
4. **`extraction_method` priority: `observed > stated > extracted > inferred > weak`** — only upgrade
5. **Content hash change cascades**: file hash change → `knowledge_depth` reset to 1 → `InvalidateChildren()`
6. **`deleted` is terminal**: `SetFactStatus(id, FactDeleted)` cannot be undone; subsequent upserts do not change it back
7. **`append_file` nulls `content_hash`**: content after append is unknown; do not preserve old hash
8. **`assert_note` bumps depth to `MAX(current, 2)`**: not hardcoded 3

---

## Part XIV: Edge Cases & Risk Register (v2 additions to v1)

The 20 risks from v1 remain valid. Additional risks from v2 changes:

| # | Risk | Mitigation |
|---|---|---|
| 21 | Unknown entity type in visualization | Frontend: assign default color (#aaaaaa) and size for any type not in the known-type color map. Graph remains navigable. |
| 22 | LLM response content too large for text extraction | Cap `extractText()` input at 16KB. Beyond this, skip without error. |
| 23 | LLM reasoning content from extended thinking is very long | Same 16KB cap. Reasoning text tends to be denser but still bounded in practice. |
| 24 | Assertion patterns in reasoning text produce false positives | Only extract assertions matching high-confidence patterns ("X is a Y", "X handles Z"). Default to `assert_note` with conf 0.75 (below human-asserted 0.85). Flag for review via `drift_report`. |
| 25 | `extractGeneric` produces path false positives (e.g. JSON keys like "type", version strings like "1.2.3") | `looksLikePath()` requires: starts with `/`, `./`, `../`, `~/`, OR contains `/` AND ends with known file extension. String "1.2.3" → rejected. |
| 26 | `OnTurnStart` fires before session is started in DB | `OnTurnStart` calls `startSession()` internally if session not found. Order-independent. |
| 27 | SubTurn parent session key is different from child session key | The child has its own `meta.SessionKey`. Link them via edge `KnownRelSpawnedBy` in the sessions table, not by sharing session keys. |
| 28 | `HookManager` dispatches `BeforeLLM`/`AfterLLM` to SWLHook (which now implements LLMInterceptor) but BeforeLLM is a pass-through | BeforeLLM must return `(req, Continue, nil)` exactly. Any error or non-Continue decision would break LLM calls. Unit test: BeforeLLM never modifies request. |

---

## Part XV: Resumability Checkpoints (v2)

| After Phase | State |
|---|---|
| 1 | Open type upsert tests pass. DB accepts any string for entity type and rel. |
| 2 | Sessions created with goals set from user messages. |
| 3 | Text extraction + catch-all generic extraction tested. Unknown MCP-style tool → Command entity. |
| 4 | Workspace scanner indexes test repo correctly. |
| 5 | Core tool calls → correct entities. Unknown tool calls → catch-all entities. |
| 6 | LLM response mentioning "auth.go" → entity at conf 0.7. Reasoning text → entity at conf 0.75. |
| 7 | All 30+ query patterns return expected results. |
| 8 | Decay handlers correct. |
| 9 | `query_swl` in tool list; system prompt delta ≤100 tokens (hint only, no SKILL.md). |
| 10 | All three interfaces verified on SWLHook. Integration test passes. |
| 11 | Live 3D graph. Bloom. Real-time SSE. Agent selector. |

---

## Summary of v1 → v2 Changes

| Area | v1 | v2 |
|---|---|---|
| Entity/edge types | Go const enums (closed, static) | Open string aliases (any value valid) |
| Prompt injection | SKILL.md embed (~3000 tokens) | 60-token hint constant |
| Tool coverage | 8 core tools only | Core tools + catch-all for ALL unknown tools |
| LLM context | Not captured | AfterLLM: response + reasoning extraction |
| User messages | Not captured | OnTurnStart: intent + path/URL extraction + session goal |
| SubTurn provenance | Not tracked | OnSubTurnSpawn: parent→child session link |
| SWLHook interfaces | ToolInterceptor only | ToolInterceptor + LLMInterceptor + EventObserver |
| SKILL.md in binary | Embedded | Removed from binary; lives in docs/design/ |
| Config field | inject_skill_prompt | hint_enabled + extract_llm_text + extract_llm_reasoning |
| Implementation phases | 10 | 11 (Phase 6 split for LLM context) |

**Branch:** `claude/picoclaw-swl-analysis-XXpmh`  
**v1 tag:** commit `5bf08d2`  
**Commit granularity:** one commit per phase, tests passing at each commit
