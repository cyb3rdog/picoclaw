# SWL Native Integration into picoclaw — Consolidated PRD

**Branch**: `claude/picoclaw-swl-analysis-XXpmh`  
**Status**: Implementation complete. All 10 phases shipped. v3 architecture improvements applied. Upstream merge (`a94ba821`) resolved.  
**Last updated**: 2026-05-02

---

## Document History

| Version | Summary |
|---|---|
| v1 | Original plan: Go-native port, 10-phase implementation, 3D frontend |
| v2 | Retrospective: open type system, drop SKILL.md injection, generic extraction improvements |
| v3 | Post-implementation audit: workspace field, manager registry, extraction depth, dead code cleanup |
| **v4 (this)** | Consolidated. Reflects actual shipped code. Tracks open items. |

---

## Vision

SWL (Semantic Workspace Layer) is a persistent, self-maintaining semantic knowledge graph embedded directly into picoclaw. It hooks into every tool call and LLM response, building a second brain that survives across sessions, channel restarts, and binary upgrades.

When SWL is enabled:
- Every tool call (read, write, exec, web fetch, MCP tools) automatically enriches a SQLite knowledge graph at `{workspace}/.swl/swl.db`
- The agent queries the graph in ~50 tokens instead of re-reading files at ~3,000 tokens each
- Facts auto-invalidate when source files change (content-hash cascade)
- Operators see the live graph at `/swl` in the web frontend — a bloom-lit 3D force graph
- Zero operator setup: one config entry, restart, done

---

## Non-Goals (permanent)

- No Python dependency — Go-native only, single binary
- No Claude Code integration — no `_CC_TOOL_MAP`, `.claude/settings.json`, or CLI hooks
- No cron or daemon — all maintenance is reactive
- No vector database — relational SQLite + regex extraction
- No AST parsing — 85–90% regex precision is sufficient
- No cross-workspace sharing — DB is strictly workspace-local
- Not a replacement for session history — complementary to the JSONL session store

---

## Architecture (as shipped)

```
┌─────────────────────────────────────────────────────────────┐
│  picoclaw AgentInstance                                     │
│  ├── Tools: query_swl (QuerySWLTool)                        │
│  ├── SWLManager *swl.Manager (via AcquireManager)          │
│  └── ContextBuilder (optional 60-token hint, default off)  │
└───────────────────────────────────────┬─────────────────────┘
                                        │
┌───────────────────────────────────────▼─────────────────────┐
│  pkg/agent/swl_hook.go — SWLHook                           │
│  pkg/agent/swl_mount.go — mountAgentSWLHooks()             │
│  (mounted into HookManager from agent.go Run())            │
└───────────────────────────────────────┬─────────────────────┘
                                        │
┌───────────────────────────────────────▼─────────────────────┐
│  pkg/swl.Manager                                           │
│  ├── workspace string (first-class field)                  │
│  ├── DB: {workspace}/.swl/swl.db (SQLite WAL)             │
│  ├── sessions: sessionKey → UUID map                       │
│  ├── inference.go: three-layer PostHook pipeline           │
│  ├── extractor.go: compiled regex patterns (14 languages)  │
│  ├── query.go: Tier1 NL / Tier2 templates / Tier3 SQL      │
│  └── scanner.go: incremental mtime workspace scan          │
│                                                             │
│  pkg/swl/registry.go — workspace-keyed Manager singleton   │
│  AcquireManager / ReleaseManager (ref-counted)             │
└───────────────────────────────────────┬─────────────────────┘
                                        │ read-only SQLite
                                        ▼
┌─────────────────────────────────────────────────────────────┐
│  web/backend/api/swl.go                                    │
│  GET /api/swl/graph    — nodes + links JSON (≤500 nodes)   │
│  GET /api/swl/stats    — entity counts by type             │
│  GET /api/swl/sessions — last 50 sessions                  │
│  GET /api/swl/stream   — SSE, 2s poll, modified_at mark    │
└───────────────────────────────────────┬─────────────────────┘
                                        ▼
┌─────────────────────────────────────────────────────────────┐
│  web/frontend /swl route                                   │
│  react-force-graph-3d: bloom nodes, colored by type        │
│  Stats panel + legend. SSE for live updates.               │
└─────────────────────────────────────────────────────────────┘
```

---

## Data Model

### Entity type system (open string space)
`EntityType = string` — any string is valid. Known types are conventions:

```go
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
    KnownTypeTool       = "Tool"    // added v3
    KnownTypeIntent     = "Intent"
    KnownTypeSubAgent   = "SubAgent"
)
```

### Edge relation system (open string space)
`EdgeRel = string` — any string is valid. Known relations are conventions.

### Upsert invariants (bedrock of correctness — all tested)
1. `confidence = MAX(existing, new)` — never decreases
2. `knowledge_depth = MAX(existing, new)` — never decreases (resets to 1 on content hash change)
3. `fact_status` never touched by upsert — only `SetFactStatus()` changes it
4. `extraction_method` priority: `observed > stated > extracted > inferred` — only upgrades
5. Deleted status is terminal — cannot be reverted

### SQLite schema

```sql
CREATE TABLE entities (
    id TEXT PRIMARY KEY, type TEXT NOT NULL, name TEXT NOT NULL,
    metadata TEXT DEFAULT '{}', confidence REAL NOT NULL DEFAULT 1.0,
    content_hash TEXT, knowledge_depth INTEGER NOT NULL DEFAULT 0,
    extraction_method TEXT NOT NULL DEFAULT 'observed',
    fact_status TEXT NOT NULL DEFAULT 'unknown',
    created_at TEXT NOT NULL, modified_at TEXT NOT NULL,
    accessed_at TEXT NOT NULL, access_count INTEGER NOT NULL DEFAULT 0,
    last_checked TEXT
);
CREATE TABLE edges (
    from_id TEXT NOT NULL, rel TEXT NOT NULL, to_id TEXT NOT NULL,
    source_session TEXT, confirmed_at TEXT NOT NULL,
    PRIMARY KEY (from_id, rel, to_id)
);
CREATE TABLE sessions (
    id TEXT PRIMARY KEY, started_at TEXT NOT NULL, ended_at TEXT,
    goal TEXT, summary TEXT, workspace_state TEXT DEFAULT '{}'
);
CREATE TABLE events (
    id TEXT PRIMARY KEY, session_id TEXT, tool TEXT,
    phase TEXT, args_hash TEXT, ts TEXT NOT NULL
);
CREATE TABLE constraints (
    name TEXT PRIMARY KEY, query TEXT NOT NULL, action TEXT NOT NULL DEFAULT 'WARN'
);
```

---

## Package Structure (as shipped)

```
pkg/swl/
├── config.go        SWLConfig — extract flags, db_path, max_file_size, hint toggle
├── manager.go       Manager struct, lifecycle, public API surface
├── db.go            DB init, WAL, schema DDL, migrations
├── types.go         EntityType/EdgeRel (string aliases), FactStatus, GraphDelta
├── entity.go        upsertEntity, SetFactStatus, applyDelta, invalidateChildren
├── edge.go          upsertEdge, confirmEdge
├── session.go       startSession, endSession, sessionSync, workspaceSnapshot
├── extractor.go     ExtractContent, ExtractDirectory, ExtractExec, ExtractWeb,
│                    ExtractGeneric, ExtractLLMResponse, compiled regex patterns
├── inference.go     three-layer PostHook pipeline (Layer0/Layer1/Generic)
├── scanner.go       ScanWorkspace — incremental mtime walker
├── query.go         Ask() → Tier1 NL / Tier2 SQL templates / Tier3 SafeQuery
├── decay.go         DecayCheck, maybeDecay (5%), maybePrune (10k events),
│                    VACUUM on close >50MB
├── hint.go          SessionHint() — 60-token prompt fragment (off by default)
├── tool.go          QuerySWLTool (query_swl) — dispatches all input shapes
├── registry.go      AcquireManager / ReleaseManager — workspace-keyed singleton
├── util.go          entityID, nowSQLite, contentHash, helpers
└── skill/SKILL.md   Operator reference doc (never auto-injected)

pkg/agent/
├── swl_hook.go      SWLHook: ToolInterceptor + LLMInterceptor + EventObserver
└── swl_mount.go     mountAgentSWLHooks() — wired into agent.go Run()
```

---

## Configuration (as shipped)

```json
{
  "tools": {
    "swl": {
      "enabled": true,
      "db_path": "",
      "max_file_size_bytes": 524288,
      "inject_session_hint": false,
      "extract_symbols": true,
      "extract_imports": true,
      "extract_tasks": true,
      "extract_sections": true,
      "extract_urls": true,
      "extract_llm_content": true,
      "reasoning_confidence_cap": 0.75
    }
  }
}
```

Config type: `config.SWLToolConfig` in `pkg/config/config.go`, field `ToolsConfig.SWL *SWLToolConfig`.

---

## Three-Layer Inference Pipeline

### Layer 0: Operator-registered custom handlers
`Manager.RegisterToolHandler(name, fn)` — takes full precedence.

### Layer 1: Declarative tool map (known picoclaw built-ins)
| Tool | Entities created | Post-apply |
|---|---|---|
| `write_file`, `edit_file` | File + symbols, imports, tasks, sections, URLs, topics | checkAndInvalidate, depth bump, FactVerified |
| `append_file` | File | NULL content_hash (content now unknown) |
| `read_file` | File + extracted content | depth bump, FactVerified (or FactStale on empty) |
| `delete_file` | File | FactDeleted tombstone, cascade children stale |
| `list_dir` | Directory + topics from special files | FactVerified |
| `exec` | Command + git commits, test results, packages | — |
| `web_fetch` | URL + title, sections, linked URLs | FactStale on empty result |

### Layer 2 (Generic): All other tools (MCP, operator skills, unknown)
- Extract absolute/relative file paths from result text
- Extract URLs
- Extract TODO/FIXME task patterns
- Attempt JSON walk for string values that look like paths or URLs
- Create `KnownTypeTool` entity for the tool itself
- Create `KnownRelExecuted` edge

### LLM response extraction (AfterLLM hook)
- Tasks and URLs from assistant content
- File path mentions (backtick spans, path-like strings)
- Symbol→file mentions ("the `parseConfig` function in config.go")
- Reasoning/thinking blocks: same extraction but confidence capped at `reasoning_confidence_cap` (default 0.75) and `MethodInferred`

---

## Query Interface

### Tier 1: Natural language (30+ compiled regex patterns)
Examples: `"functions in pkg/foo/bar.go"`, `"todos in main.go"`, `"imports in main.go"`, `"files in pkg/swl"`, `"stale entities"`, `"project type"`, `"most complex files"`, `"open tasks"`, `"bring me up to speed"`.

### Tier 2: Named SQL templates (20+ templates)
`dependency_chain`, `files_by_complexity`, `top_dependencies`, and others. Selected by fuzzy matching against template names.

### Tier 3: Raw SQL escape hatch
`SafeQuery(sql)` — SELECT/WITH/EXPLAIN only, 200-row cap, 5s timeout.

### Convenience methods
- `SessionResume(sessionKey)` — structured digest of last session
- `KnowledgeGaps()` — low-confidence or zero-depth entities
- `DriftReport()` — stale entities
- `AssertNote(subject, note, confidence, entityType)` — manual annotation, depth MAX(current, 2)
- `Stats()` — entity counts by type
- `Schema()` — table structure summary
- `ScanWorkspace(root)` — incremental filesystem scan

---

## Reactive Maintenance

| Trigger | Action | Probability/Threshold |
|---|---|---|
| Every PostHook | `maybeDecay()` — check 2 random entities | 5% per call |
| Every PostHook | `maybePrune()` — delete old events | Only if events > 10,000 |
| File decay handler | `os.Stat(path)` — mark stale/deleted if gone | On each decay check |
| URL decay handler | HTTP HEAD check | 24h minimum between rechecks |
| `Manager.Close()` | `VACUUM` if DB > 50MB | Always |

---

## v7 Anti-Drift Rules (permanent constraints)

| v7 Pattern (BANNED) | v8/picoclaw Requirement |
|---|---|
| Separate session per subprocess | One session per picoclaw session key |
| Cron-based maintenance | Reactive only |
| DB in skill directory | `{workspace}/.swl/swl.db` |
| Closed entity/edge type enums | Open string spaces with `KnownType*` conventions |
| `Channel` edge orphans | Only create when content is non-empty |
| `append_file` preserves content_hash | `append_file` NULLs content_hash |
| `assert_note` hardcodes depth=3 | `assert_note` sets depth to MAX(current, 2) |
| `fact_status` reset by upsert | `fact_status` NEVER touched by upsert |
| `confidence` decreases on re-upsert | `confidence = MAX(existing, new)` |
| Full SKILL.md injected into every prompt | 60-token hint only (off by default); tool description is the doc |
| String literal `"Tool"` in type position | `KnownTypeTool` constant |
| Broad noiseSymbols (English/Go-centric) | Minimal: `main`, `init`, `test` only |

---

## Implementation Status

### Phases (all complete)

| Phase | Description | Status | Commit |
|---|---|---|---|
| 1 | Types, DB schema, entity/edge CRUD, upsert invariants | ✅ Done | `87aacf6` |
| 2 | Session management, lifecycle, sessionSync | ✅ Done | `87aacf6` |
| 3 | Content extraction — 14 languages, all patterns | ✅ Done | `87aacf6` |
| 4 | Workspace scanner — incremental mtime walk | ✅ Done | `87aacf6` |
| 5 | Three-layer inference + SWLHook integration | ✅ Done | `87aacf6` |
| 6 | Query interface — Tier1/2/3, all patterns | ✅ Done | `87aacf6` |
| 7 | Decay system — probabilistic, URL recheck, VACUUM | ✅ Done | `87aacf6` |
| 8 | QuerySWLTool + config wiring + AgentInstance integration | ✅ Done | `87aacf6` |
| 9 | AgentLoop hook mounting (swl_mount.go, swl_hook.go) | ✅ Done | `87aacf6` |
| 10 | Frontend 3D graph + backend SSE API | ✅ Done | `15c5e17` |

### v3 Architecture Improvements (all complete)

| Item | Description | Status | Commit |
|---|---|---|---|
| v3-1 | `Manager.workspace` field, fix fragile scan path in tool.go | ✅ Done | `6faf602` |
| v3-2 | `pkg/swl/registry.go` — workspace-keyed Manager singleton | ✅ Done | `6faf602` |
| v3-3 | `KnownTypeTool` constant, improved `ExtractGeneric` (paths, tasks, JSON walk) | ✅ Done | `6faf602` |
| v3-4 | Improved `ExtractLLMResponse` (file paths, symbol mentions) | ✅ Done | `6faf602` |
| v3-5 | Remove dead code: `contextWithTimeout` in decay.go | ✅ Done | `6faf602` |
| v3-6 | Remove `var _ =` suppressors in web/backend/api/swl.go | ✅ Done | `6faf602` |
| v3-7 | Narrow `noiseSymbols` to `{main, init, test}` | ✅ Done | `6faf602` |
| v3-8 | Session hint: kept in `hint.go`, default off (`inject_session_hint: false`) | ✅ Done | `6faf602` |

### Post-merge fixes (upstream `a94ba821`)

| Item | Description | Status | Commit |
|---|---|---|---|
| M-1 | Remove old `loop_*.go` files (renamed to `agent_*.go` upstream) | ✅ Done | `0668b0b9` |
| M-2 | Re-wire `mountAgentSWLHooks()` call into `agent.go Run()` | ✅ Done | `34b0c10d` |
| M-3 | Re-add `SWLToolConfig` + `SWL *SWLToolConfig` to `ToolsConfig` (lost in merge) | ✅ Done | `34b0c10d` |
| M-4 | Restore `SetPreferredWebSearchLanguage` / `GetPreferredWebSearchLanguage` (lost in merge) | ✅ Done | `34b0c10d` |

### Test coverage

| Test file | Tests | Status |
|---|---|---|
| `pkg/swl/manager_test.go` | 15 invariant tests + lifecycle | ✅ All passing |
| `pkg/swl/hook_test.go` | 17 hook tests (all tools, panic recovery, agent filter) | ✅ All passing |
| `pkg/swl/query_test.go` | 20 query tests (Tier1/2/3, KnowledgeGaps, DriftReport, AssertNote, SessionResume, ScanWorkspace) | ✅ All passing |

---

## Open Items

| # | Item | Priority | Notes |
|---|---|---|---|
| O-1 | Frontend: `nodeOpacity` per-node confidence rendering | Low | Currently fixed at 0.85; `react-force-graph-3d` `nodeOpacity` prop is `number`, not function. Would need `nodeThreeObject` override to vary per-node. |
| O-2 | Frontend: bloom effect on depth ≥ 3 nodes | Low | `nodeThreeObject` with emissive Three.js material; deferred to avoid bundle size increase. |
| O-3 | `ScanWorkspace` symlink loop detection | Low | Current walker uses `filepath.WalkDir` which follows symlinks once; deeper loop detection not implemented. |
| O-4 | URL decay handler live | Low | Decay handler framework exists; URL HTTP HEAD rechecks wired but 24h guard test coverage is thin. |
| O-5 | Integration test: full agent turn → DB populated | Medium | Unit tests cover PostHook in isolation. A full `AgentLoop` integration test with mock tool calls would close the gap. |
| O-6 | config.example.json update | Low | `inject_session_hint` field name in the example still shows the old v1 name. Should reflect v3 field names. |
| O-7 | i18n: add `navigation.swl` key to non-en/zh locales | Low | Only `en.json` and `zh.json` have the "Knowledge Graph" / "知识图谱" key. Other locales fall back to key name. |

---

## Files Changed (complete inventory)

### New — `pkg/swl/` package (20 files)
`config.go`, `db.go`, `decay.go`, `edge.go`, `entity.go`, `extractor.go`, `hint.go`, `inference.go`, `manager.go`, `query.go`, `registry.go`, `scanner.go`, `session.go`, `tool.go`, `types.go`, `util.go`, `manager_test.go`, `hook_test.go`, `query_test.go`, `skill/SKILL.md`

### New — agent SWL integration (2 files)
`pkg/agent/swl_hook.go`, `pkg/agent/swl_mount.go`

### New — web backend (1 file)
`web/backend/api/swl.go` — 4 HTTP handlers, read-only SQLite

### New — web frontend (6 files)
`web/frontend/src/routes/swl.tsx`, `web/frontend/src/api/swl.ts`, `web/frontend/src/features/swl/graph.tsx`, `web/frontend/src/features/swl/stats.tsx`, `web/frontend/src/features/swl/legend.tsx`, `web/frontend/src/features/swl/hooks.ts`

### Modified — Go
| File | Change |
|---|---|
| `pkg/config/config.go` | `SWLToolConfig` type + `SWL *SWLToolConfig` field on `ToolsConfig` |
| `pkg/agent/instance.go` | `SWLManager` field, `swlRelease`, `AcquireManager`, `QuerySWLTool` registration, `Close()` |
| `pkg/agent/agent.go` | `mountAgentSWLHooks()` call in `Run()` |
| `pkg/agent/context.go` | `extraSystemContent []string`, `AddInlineSystemContent()`, `BuildSystemPromptParts()` injection |
| `pkg/tools/integration/web.go` | `SetPreferredWebSearchLanguage` / `GetPreferredWebSearchLanguage` (restored after merge) |
| `pkg/tools/integration_facade.go` | Re-export of language functions at `pkg/tools` level |
| `web/backend/api/router.go` | `h.registerSWLRoutes(mux)` |

### Modified — Frontend
| File | Change |
|---|---|
| `web/frontend/package.json` | `react-force-graph-3d ^1.29.1` |
| `web/frontend/src/components/app-sidebar.tsx` | SWL nav item (Knowledge Graph, IconBrain) |
| `web/frontend/src/i18n/locales/en.json` | `navigation.swl: "Knowledge Graph"` |
| `web/frontend/src/i18n/locales/zh.json` | `navigation.swl: "知识图谱"` |
| `web/frontend/src/routes/routeTree.gen.ts` | Regenerated to include `/swl` route |

---

## Edge Cases & Mitigations

| Risk | Mitigation | Status |
|---|---|---|
| SWL DB on read-only filesystem | `openDB()` error → log warning, agent continues without SWL | ✅ Implemented |
| Binary file sent to extractor | `utf8.Valid()` check before regex; skip binary files | ✅ Implemented |
| Files >512KB | Enforce limit before extraction; truncate to cap | ✅ Implemented |
| Concurrent writes from multiple turns | Single `MaxOpenConns(1)` + write mutex | ✅ Implemented |
| AfterTool goroutine outlives Close() | `sync.WaitGroup` in `SWLHook`; `Close()` drains | ✅ Implemented |
| Regex catastrophic backtracking | `context.WithTimeout(2s)` around extraction | ✅ Implemented |
| SSE holds connection forever | `r.Context().Done()` teardown | ✅ Implemented |
| Web backend reads during gateway write | SQLite WAL: readers never block writers | ✅ By design |
| Multiple AgentInstances same workspace | `registry.go` ref-counted singleton | ✅ Implemented |
| `assert_note` depth hardcoded (v7 bug) | `MAX(current, 2)` enforced; invariant tested | ✅ Tested |
| `append_file` hash preserved (v7 bug) | NULL on append enforced; invariant tested | ✅ Tested |
| react-force-graph-3d large graph | ≤500 most-recently-active nodes in API response | ✅ Implemented |
