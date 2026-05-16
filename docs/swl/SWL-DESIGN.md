# SWL — Semantic Workspace Layer: Design Document

> Status: **v1.4** | Date: 2026-05-16
> Replaces and consolidates all prior SWL design notes.
> Aligned with implementation as of `claude/merge-swl-fixes-assessment-Y67DP`.

---

## 1. Purpose & Vision

SWL (Semantic Workspace Layer) is a persistent, generic, self-improving semantic brain that gives every LLM agent working in a workspace real knowledge of that workspace — verified facts, structural understanding, semantic relationships — without re-learning or rediscovering on every request.

It is the invisible layer between an LLM and the workspace it operates in.

### What it replaces

Previously, an LLM arriving at a workspace has zero context. It must:
- Ask exploratory questions
- Read every file it needs to understand what it works with
- Re-discover directory structure, purpose, and key components on every session
- Make assumptions that risk drift and hallucinations

SWL eliminates all of this.

### What it provides

A sufficiently-indexed workspace answers questions before the LLM asks:
- "What is this workspace for?" → from README/anchor documents
- "Where is the file that does X?" → from symbol labels, domain metadata
- "What goals is this project pursuing?" → from stated goals in anchor docs
- "What areas exist and what do they contain?" → from semantic area snapshot
- "What imports does the auth package have?" → from import graph traversal

The LLM gets intelligence instead of needing to create it.

---

## 2. Mission Statement

> **SWL is generic, universal, adaptable, self-improving, and workspace-content agnostic.**
> It works for Go projects, Python research repos, firmware, legal document collections, config management, or mixed content.
> It runs on constrained hardware (RPi 0 2W — ~80MB RAM budget).
> It self-improves with use, accumulating verified facts across sessions and agents.
> It prevents LLM drift, assumptions, and hallucinations by providing ground-truth intelligence about the workspace.

---

## 3. Core Design Principles

### 3.1 Generic over specific
Every design decision holds for any workspace content type. No hardcoded assumptions about "source code project" as the only use case.

### 3.2 Cheap before expensive
Extraction runs in cost tiers. Expensive tiers (LLM-powered indexing) are opt-in and off by default. Tier 0–2 cost nothing or near-nothing.

### 3.3 LLM-agnostic signals
Self-improvement is grounded in **workspace actions** — tool calls, file modifications, assertions — never in inferences about LLM behavior. Different LLMs drift differently; SWL cannot depend on predictable LLM behavior.

### 3.4 Multi-agent neutral
Multiple LLMs may work the same workspace. SWL is a shared semantic layer. Convergence across agents — not any single agent's output — is the signal for verified knowledge.

### 3.5 Autonomous
The feedback loop runs without human or LLM intervention. It adjusts weights, promotes/demotes facts, surfaces gaps. It does not auto-rewrite rules.

### 3.6 Additive evolution
All existing public APIs, upsert invariants, and integration surfaces are preserved throughout all refactoring phases.

---

## 4. Conceptual Model

Two configurable layers, driven by YAML config files:

```
  swl.rules.yaml                    swl.query.yaml
  ──────────────                    ──────────────
  what to extract                   how to answer
  when, how, cost tier              intent patterns
  semantic label rules              label weights
  tool interception                 graph traversal templates
  feedback thresholds               
         │                                  
         ▼                                  
    EXTRACTION                        QUERY ENGINE
    ──────────                        ────────────
    scan → semantic snapshot          intent decomposition
    (bounded, generic)                label-weighted scoring
    + lazy per-file detail            graph traversal
    on tool call                      + feedback observation
         │                                  
         └──────────────┬───────────────┘
                        ▼
               Shared Entity Graph
               (SQLite — preserved)
```

### Semantic Labels
The bridge between extraction and query. A label is a structured tag stored in entity metadata: `role: "authentication"`, `domain: "data-access"`, `kind: "anchor-document"`, `content_type: "sql"`.

Queries become answerable because extraction was semantic, not because the query engine is clever. A well-labelled graph with a simple scorer beats a clever scorer on a name-only graph every time.

---

## 5. Extraction Model

### 5.1 Four Extraction Tiers (cost-ordered)

| Tier | Name | Cost | Default | Runs when |
|------|------|------|---------|-----------|
| 0 | Structural / derivative | Near-zero | Always on | Scan-time: file names, dir structure, extension distribution, naming patterns, anchor document presence |
| 1 | Ontological inference | Cheap (SQL only) | Always on | Scan + after writes: rule-driven derivations from existing graph facts |
| 2 | Passive LLM capture | Free | Always on | During tool use: `AfterLLM` hook captures semantic facts from LLM reasoning and responses — flows naturally during real sessions |
| 3 | Active LLM indexing | Expensive | Off by default | Scan-time only, if configured: SWL asks a configured LLM to summarize/classify anchor documents. Never on constrained hardware. |

### 5.2 Workspace Semantic Snapshot (scan-time, bounded)

The snapshot answers: *"what is this workspace for?"*, *"what areas does it have and what do they do?"*, *"what are the key documents?"*, *"what goals are stated?"*, *"what kinds of content exist here?"*

| Entity | Derived from | How |
|--------|-------------|-----|
| Semantic areas | Significant directories | Rules classify by: anchor document presence and content, file type distribution, naming signals, child relationships |
| Anchor documents | Purpose-stating files | Patterns: `README*`, `OVERVIEW*`, `ARCHITECTURE*`, manifest files, structured header comments |
| Content profile | Extension distribution | Aggregated counts → generic content-type labels |
| Key relations | Top-level connections | Cross-reference patterns, import conventions, naming proximity |
| Explicit goals | Human-stated intent | Extracted from anchor documents wherever present |

**Bounded by design:** configurable max per entity class. Default upper bound: ~100–300 entities for any workspace, regardless of size.

### 5.3 Lazy Per-File Detail

Per-file granular knowledge (symbols, imports, tasks, sections) is extracted only when a tool call touches the file — NOT at scan time. This eliminates the previous problem of generating 40K+ Symbol entities per scan.

Extraction depth scales with entity hotness (read frequency, cross-agent access count). Cold files get a minimum symbol budget; hot files get a configurable multiplier.

When a query asks for per-file detail on an unread file, the response includes an explicit notice: *"[filename] has not been read in detail — use read_file to populate its contents."*

---

## 6. Query Model

### 6.1 How queries work

1. **Intent recognition** — match the question against intent patterns in `swl.query.yaml`
2. **Label-weighted scoring** — score entities by signal strength: exact label match > partial label match > name match > path match > session co-occurrence
3. **Graph traversal** — for relational questions, walk edges from matched entities
4. **Fallback** — freetext name+metadata search for unmatched intents

### 6.2 Built-in intent patterns

| Intent | Example question | Handler |
|--------|-----------------|---------|
| workspace_purpose | "what is this workspace for?" | manifest_summary |
| find_by_purpose | "where is the file that handles auth?" | label_search |
| area_contents | "what is in the web area?" | area_traverse |
| file_detail | "what does manager.go do?" | file_summary |
| workspace_goals | "what are the goals?" | goals_summary |
| content_type_distribution | "what kind of content is here?" | content_profile |

### 6.3 Label weights

| Signal | Weight |
|--------|--------|
| Exact label match | 1.0 |
| Partial label match | 0.6 |
| Name match | 0.4 |
| Path match | 0.3 |
| Session co-occurrence | 0.2 |
| Cross-agent confirmation | 0.5 |

---

## 7. Autonomous Feedback Loop

### 7.1 Why signals must be LLM-agnostic

Different LLMs working the same workspace exhibit different tendencies: some re-ask when results are poor, some accept wrong answers silently, some hallucinate on top of stale SWL data. Any signal derived from LLM behavioral inference is unreliable.

All signals are grounded in **workspace actions** — what actually happened to files and entities.

### 7.2 Observable signals

| Signal | Observable fact | How captured |
|--------|----------------|--------------|
| Tool follow-through | After SWL returns entity X, agent calls `read_file(X)` | `PostHook` correlates prior query results with subsequent tool calls |
| Assertion event | Agent calls `query_swl {assert:...}` | Explicit fact injection tagged with source agent session ID |
| Assertion confirmation | Later agent asserts same fact independently | Confidence strengthened; source agents recorded |
| Contradiction | Agent B asserts fact conflicting with Agent A's assertion | Confidence halved; surfaced in gaps |
| Cross-agent convergence | ≥N distinct agents touch same entity under same semantic context | Label promoted toward `verified` |
| Decay signal | Entity not touched by any agent in M sessions | Label confidence decays |

### 7.3 Autonomous adjustment loop

Runs on the same probabilistic cadence as `maybeDecay()`. No human or LLM intervention.

| Trigger | Action |
|---------|--------|
| Entity touched by ≥3 distinct agents with same label | Label confidence → `verified` |
| Assertion contradicted by ≥2 subsequent agents | Confidence halved; `fact_status: stale`; surfaced in `query_swl {gaps:true}` |
| Entities co-occurring in ≥4 sessions | Auto `co_occurs_with` edge recorded (`decay.go`) |
| Rule fired ≥200× with useful_ratio < 0.05 | Rule removed from active set |
| Query returning 0 results, repeated ≥3× | Appears in `query_swl {gaps:true}` with inline YAML suggestion |
| Decay check runs | `ORDER BY confidence ASC, access_count ASC, last_checked ASC` — lowest-confidence, least-accessed entities checked first |

### 7.4 Ontological derivation (scan-time, Tier 1)

After `ScanWorkspace`, two derivation passes run:

- **`DeriveAreaRelations()`** (`ontology.go`) — derives `depends_on` edges between SemanticArea (Directory) entities where files in area A import ≥2 dependencies that fall under area B's path prefix. Gives LLMs "what does pkg/auth depend on?" without any file reads.
- **`DeriveSymbolUsage()`** (`ontology.go`) — derives `File --uses--> Symbol` edges from the import graph. If File A imports a dependency whose path ends with the directory of File B, and File B defines exported Symbol S, → `File A uses Symbol S`. Precision filters: exported symbols only (uppercase first letter); multi-segment package paths only (e.g. `pkg/auth`, not `auth`). Capped at 500 pairs per scan. These edges are inferred (no session ID); extractor-observed edges from actual file reads take precedence via upsert.

### 7.5 Per-model reliability profiling (ADDENDUM 1)

Each `assertionEntry` in `metadata.assertions[]` carries a `model_id` field populated
from `sessionModels` (set by `AfterLLM` via `SetSessionModel`). Each event row in the
`events` table carries `model_id` similarly.

`askModelReliability()` aggregates per-model assertion stats: total assertions,
confirmed count (≥2 distinct sessions), average confidence. Queryable via
`query_swl {"question":"model reliability"}` and `GET /api/swl/model-reliability`.

---

## 8. System Architecture

### 8.1 Go package structure (`pkg/swl/`)

```
pkg/swl/
├── manager.go       Core: NewManager, SetSessionModel, scaffoldConfigFiles, ref-counted registry
├── db.go            SQLite schema, WAL, single-writer connection, DB operations + migrations
├── entity.go        Entity CRUD, upsert invariants, decay, access_count (reads not writes)
├── edge.go          Edge CRUD, relation types, graph traversal
├── inference.go     3-layer inference pipeline, toolMap, RegisterToolHandler, recordToolEvent
├── extractor.go     Regex extractors: symbols, imports, tasks, sections, URLs, LLM response
├── scanner.go       Workspace walk, mtime-based incremental scan, ignore patterns
├── snapshot.go      BuildSnapshot: workspace semantic snapshot (Tier 0 bounded)
├── ontology.go      DeriveAreaRelations, DeriveSymbolUsage (Tier 1 derivation post-scan)
├── session.go       Session management, cold-start detection, hint injection, SessionResume
├── registry.go      Global map[dbPath → managerEntry] with ref-counting
├── decay.go         Evidence-based decay (confidence ASC), co_occurs_with loop, re-verification
├── gap_analysis.go  AnalyzeGaps, SuggestRules, inline YAML rule suggestions
├── query.go         3-tier query engine + askModelReliability + appendAssertionToMeta
├── tool.go          query_swl tool registration, PreHook blocking, rulesLoadErr surfacing
├── hint.go          Session hint constant injected into system prompt
├── config.go        SWL configuration, effective value helpers
├── rules.go         RulesEngine, QueryConfig, CompileQueryConfig, workspace override merge
├── labels.go        LabelResult, DeriveLabels, path/name/extension pattern matching
├── ignore.go        .swlignore parser, gitignore-compatible pattern matching
├── types.go         All public types, constants (KnownType*, KnownRel*, Method*)
└── util.go          Path normalization, JSON helpers, truncate
```

**22 source files total.**

### 8.2 Agent integration (`pkg/agent/swl_hook.go`)

`SWLHook` implements three interfaces:
- **RuntimeEventObserver** — `OnRuntimeEvent`: TurnStart → Intent entity + session goal; SubTurnSpawn → SubAgent entity
- **LLMInterceptor** — `AfterLLM`: calls `SetSessionModel(sessionID, resp.Model)` for per-model tracking; fires `ExtractLLMResponse` async (extracts Tasks, URLs, file paths + wires `context_of` edges); reasoning content capped at `EffectiveReasoningConfidenceCap()`
- **ToolInterceptor** — `AfterTool`: fires `PostHook` async; `BeforeTool`: calls `PreHook` for blocking

Mounted per-agent via `mountAgentSWLHooks()` with priority 10. One SWLHook instance per agent, sharing the Manager via the agent registry.

### 8.3 Query engine (3-tier)

| Tier | Mechanism | Examples |
|------|-----------|---------|
| 1 | 30+ hardcoded regex patterns | askWorkspacePurpose, askSemanticAreas, askSymbols, askImports |
| 2 | SQL templates | dependency_chain, files_by_type, orphan_symbols |
| 3 | Freetext name+metadata search | unmatched intents (capped at 3 terms) |
| Fallthrough | query_gaps | records missed queries after 3 failures |

### 8.4 Inference pipeline (3-layer)

| Layer | Mechanism | Priority |
|-------|-----------|---------|
| 0 | Custom handlers via `RegisterToolHandler()` | Highest — escape hatch |
| 1 | Declarative `toolMap` (write_file, edit_file, read_file, exec, web_fetch) | Default |
| 3 | `ExtractGeneric` for unknown tools | Lowest — catch-all |

Path normalization: all entity IDs derived from workspace-relative normalized paths.

### 8.5 Extraction limits

| Extractor | Limit |
|-----------|-------|
| Symbols | 60 per file |
| Imports | 40 per file |
| Tasks | 30 per file |
| Sections | 20 per file |
| URLs | 20 per file |
| Max file size | 512KB (configurable) |
| Context timeout | 2s per extractor |
| Noise filter | Skips localhost, 10.x, 192.168.x, 172.x, 169.254.x, example.com, .local |

### 8.6 Web API (`web/backend/api/swl.go`)

8 REST endpoints + SSE stream:

| Endpoint | Mode | Max nodes | Max edges | Per-node cap |
|----------|------|-----------|-----------|-------------|
| GET /graph | map | 20,000 | 40,000 | 500 |
| GET /graph | overview | 10,000 | 20,000 | 250 |
| GET /graph | session | 5,000 | 10,000 | 150 |
| GET /graph/neighborhood | — | 120 | 400 | 30 |

Plus: GET /stats, GET /health, GET /sessions, GET /overview (combined), GET /model-reliability (per-model assertion stats), GET /stream (SSE delta stream — delivers both node updates and new edges).

**Neighborhood model**: depth-1 all incident edges → depth-2 cross-links only between hop-1 neighbors (no expansion). Focus mode without graph explosion.

**Health scoring**: `score = 1.0 - (stale*0.5) - (unknown*0.2) - (isolated*0.15)`. Levels: empty/poor/fair/good/excellent.

### 8.7 Web UI (`web/frontend`)

| Component | Role |
|-----------|------|
| SWLPage | Container: view mode selector, focus breadcrumb, graph + stats layout |
| SWLGraph | 3D force-directed: ForceGraph3D + Three.js + UnrealBloomPass |
| SWLStats | Side panel: NodeInspector, HealthBadge, type filter, sessions |

**Graph specifics:**
- In-place SSE mutation: node objects mutated directly, `updateNodeMaterial` reads props every frame, `setGraphState` only called for new nodes (avoids simulation reheat)
- LOD tiers: <100 nodes → 14×10 sphere segs, 100-300 → 8×6, >300 → 5×4
- Focus node: 1.5x radius, badge, neighborhood mode suppresses unrelated SSE arrivals
- Auto-orbit, pauses 20s on interaction, zoom-to-fit after 800ms
- No auto-refetch: SSE stream is the sole update mechanism after initial load; manual Reload button available
- 400ms debounced SSE batch; SSE delivers both node updates (`nodes[]`) and new edges (`links[]`)

**Color palette** (type → hex):
File=blue, Symbol=green, Task=amber, URL=cyan, Session=purple, Note=orange, Intent=lilac, SubAgent=teal, Dependency=lime, Command=gold.

### 8.8 Session management

- Cold-start detection: <10 non-session entities → full digest
- Session hint constant: 60-token prompt injected into system prompt
- `autoSummary`: "entities=N edges=N"
- `SessionSync`: async mtime check on all verified files at session start
- Session edge tracking: every edge tagged with `source_session`

---

## 9. Configuration

### 9.1 Config files

| Layer | File | Purpose |
|-------|------|---------|
| SWL runtime | `pkg/swl/config.go` | Internal defaults and effective values |
| Tools config | `pkg/config/swl.go` | JSON-decoded from tools config |
| Extraction rules | `swl.rules.yaml` | Tier 0–3 extraction rules (Phase B) |
| Query patterns | `swl.query.yaml` | Intent patterns and label weights (Phase B) |

### 9.2 SWL config fields

```go
type SWLToolConfig struct {
    Enabled                bool
    DBPath                 string   // default: {workspace}/.swl/swl.db
    MaxFileSizeBytes       int64    // default: 512KB
    InjectSessionHint      *bool   // default: true
    ExtractSymbols         *bool    // default: true
    ExtractImports         *bool    // default: true
    ExtractTasks           *bool    // default: true
    ExtractSections        *bool    // default: true
    ExtractURLs            *bool    // default: true
    ExtractLLMContent      *bool    // default: true
    ReasoningConfidenceCap *float64 // default: 0.75
}
```

---

## 10. Refactor Phases

### Phase A — ✅ Done (2026-05-06)
BuildSnapshot, lazy per-file extraction, events table activated, access_count fixed (reads not writes), gap recording, `query_swl` for gaps and drift detection.

**Verification:**
- Scan picoclaw workspace → entity count ≤ 300 at scan time ✅ (1414 verified Files, 29 SemanticAreas, 14 AnchorDocuments — extracted lazily on tool call, not at scan)
- `query_swl {"question":"what is this workspace for?"}` → returns anchor doc descriptions ✅
- `query_swl {"question":"what does pkg/swl/manager.go do?"}` before `read_file` → "not yet read in detail" notice ✅
- `query_swl {"stats":true}` → access_count tracked on returned entities ✅
- `query_swl {"gaps":true}` → knowledge gaps surfaced ✅

### Phase A.2 — ✅ Done (2026-05-08)
Path pattern → semantic label derivation. `labels.go` + `scanner.go` + `snapshot.go` derive `role`, `domain`, `kind`, `visibility`, `content_type` labels from structural signals (path prefixes, name patterns, content types) at scan time. No LLM needed.

**New files:** `pkg/swl/labels.go` (LabelResult, DeriveLabels, path/name pattern matching), `pkg/swl/ignore.go` + `ignore_test.go` (noise symbols, ignore dirs/exts).
**Modified:** `scanner.go` — calls `m.DeriveLabels()` after File entity upsert; `snapshot.go` — uses `m.DeriveLabels()` for SemanticArea labels; `manager.go` — initializes RulesEngine for label derivation.
**Embedded config:** `swl.rules.default.yaml` (30 path prefix rules, 18 name patterns, 35 content types).

### Phase A.3 — ✅ Done (2026-05-08)
`label_search` handler + Tier 1 intent patterns for "where is the file that does X?" questions. Answerable because Phase A.2 produced labeled entities.

**New files:** `pkg/swl/query.go` refactored with `labelSearch()` handler. 30+ Tier 1 patterns matching workspace purpose, semantic areas, file detail, find-by-purpose, symbols, tasks, imports, files, stale, complexity, deps, recent, URLs, sessions, stats, gaps, schema.
**Modified:** `query.go` — `Ask()` dispatches via tryYAMLIntents → dispatchHandler → tryHardcodedPatterns → tryTier2 → tryTier3 pipeline; `manager.go` — loads query intents from `swl.query.default.yaml` at init.

### Phase B — ✅ Done (2026-05-12)
Externalize extraction and query logic to `swl.rules.yaml` and `swl.query.yaml`. YAML intents tried first; hardcoded fallbacks preserved for forward compatibility. Zero behavioral change when no workspace overrides present.

**New files:** `pkg/swl/rules.go` (RulesEngine, QueryConfig, CompiledIntent, LoadRules, LoadQueryConfig, CompileQueryConfig); `pkg/swl/swl.rules.default.yaml` (embedded, 30 path prefix rules, 18 name patterns, 35 content types, extraction limits, noise symbols); `pkg/swl/swl.query.default.yaml` (embedded, 18 Tier 1 intents, 3 Tier 2 SQL templates, label search weights); `pkg/swl/gap_analysis.go` (AnalyzeGaps, SuggestRules, RuleSuggestion, GapEntry).
**Modified:** `manager.go` (loads rules + query intents at init, `m.rules` field); `query.go` (tryYAMLIntents, tryYAMLTier2, tryHardcodedPatterns, dispatchHandler pipeline); `scanner.go`, `snapshot.go` (use `m.DeriveLabels()` via RulesEngine when available).
**New schema:** `query_gaps.suggestion TEXT` column (migration on open). Deep-merge workspace-level `{workspace}/.swl/swl.rules.yaml` / `{workspace}/.swl/swl.query.yaml` overrides.

**B1 (wired):** `Manager.DeriveLabels()` delegates to `m.rules.DeriveLabels()`; workspace label rule overrides in `.swl/swl.rules.yaml` are honoured at scan time.
**B3 (wired):** `ExtractContent()` receives limits from `m.MaxSymbols()`, `m.MaxImports()`, etc.; workspace limit overrides (important for constrained hardware) are honoured.

### Phase C — ✅ Done (2026-05-07)
Activate autonomous feedback loop. Self-improvement with use across sessions and agents. Gap → candidate rule generation.

**New file:** `gap_analysis.go` — `AnalyzeGaps()`, `SuggestRules()`, `RuleSuggestion`, `GapEntry`
**New column:** `query_gaps.suggestion TEXT` (migration on open via `ALTER TABLE`)
**Modified:** `query.go` (`KnowledgeGaps()` now includes query gaps + candidates), `tool.go` (new `suggest` arg), `db.go` (migration + schema)

---

## 11. Known Gaps & Design Boundaries

### Resolved (as of 2026-05-16)

| ID | Gap | Resolution |
|----|-----|-----------|
| B1 | Label rules not wired | `Manager.DeriveLabels()` delegates to `m.rules.DeriveLabels()`; workspace overrides honoured |
| B3 | Extraction limits not wired | `ExtractContent()` receives limits from `m.MaxSymbols()` etc.; workspace overrides honoured |
| LLM-1 | Silent query fallthrough | `fallthroughResponse()` returns actionable next-step hint on every miss |
| LLM-2 | No inline help mode | `HelpText()` + `{"help":true}` operation added |
| LLM-3 | No staleness signal at use | `PreHook` checks `fact_status`; prepends stale notice to tool result in `AfterTool` |
| LLM-4 | Gap recording silent | First-miss response includes "(Query recorded — repeated misses generate candidate rules automatically.)" |
| LLM-5 | Assert no confirmation echo | `AssertNote()` returns `"Recorded: '<fact>' on entity '<name>' [id: <short-id>] at confidence <n>"` |
| D-1 | Assertion orphaning | Assertions moved to `metadata.assertions[]`; no phantom nodes |
| D-2 | Intent/SubAgent write-only | Wired in `askSessionActivity` and `SessionResume` |
| D-3 | SSE edges missing | Two-watermark approach; `links[]` in SSE delta payload |
| N-1 | LLM extraction orphans | `context_of` edges from extracted entities to session |
| N-2 | Decay ordering probabilistic | `ORDER BY confidence ASC, access_count ASC, last_checked ASC` |
| N-3 | ADDENDUM 1 unimplemented | `model_id` in assertions + events; `askModelReliability`; `/api/swl/model-reliability` |

### Intentionally deferred

See [ROADMAP.md](./ROADMAP.md) for the full list with rationale:
- Semantic similarity edges (`similar_to`) — noise without type context
- Path queries (`shortestPath`) — SQLite CTE limitations; needs application-level BFS
- M8 autonomous rule auto-apply — needs dry-run design first
- Per-extension extraction overrides — infrastructure ready; YAML schema not extended
- Anchor document structured extraction — no consumer yet
- WorkspaceProfile entity — requires Tier 3 LLM indexing (opt-in)
- Edge weights as DB column — correct approach is config-driven at query time

---

## 12. Not In Scope

- LLM calls at scan time for descriptions (Tier 2 passive capture covers this for free during real work)
- Vector embeddings / semantic similarity (RPi memory budget)
- Strict ontology enforcement with write rejection (advisory labelling is sufficient)
- Cross-workspace federation
- Schema migration framework (tables are additive; manual upgrade is fine)
- Automatic rule generation (Phase C: `SuggestRules()` surfaces candidate rules; human/LLM applies via swl.rules.yaml)

---

## 13. Known Behaviors (Not Bugs)

| Observation | Explanation |
|-------------|-------------|
| 20 SWL source files show as "unknown" | Lazy extraction — content extracted only on tool call. Subagent reads bypass the hook. By design. |
| Symbol entities have 0 verified | Symbols extracted via tool call hooks; fact_status defaults to "unknown" until verified through decay or assertion. Normal. |
| ~1,460 Files but only 411 Symbols | Symbol extraction happens lazily on `read_file`/`write_file` tool calls — not all files have been read yet. Expected. |
| 4 stale files (cron, sessions, state) | Workspace operational files change between sessions. Expected, not SWL source files. |
| Session edges only 5 | Session edge insertion may be gated by session key availability in runtime events. Investigate if needed. |

---

## 14. .swlignore File

The SWL uses a `.swlignore` file (gitignore-compatible) to exclude directories and files from scanning. Located at `{workspace}/.swl/swlignore`.

### Features
- **Gitignore-compatible patterns**: `*`, `**`, `?`, `[abc]`, `!` negation
- **Default ignore lists**: Common directories (`.git`, `node_modules`, etc.) and extensions (`.png`, `.pdf`, etc.) are always skipped
- **Load order**: `.swlignore` patterns first (user-defined), then defaults as fallback
- **Non-fatal**: Missing `.swlignore` file doesn't cause errors — uses defaults only
- **Cached in memory**: Patterns loaded once at Manager initialization

### Default Ignored Directories
`.git`, `node_modules`, `.svn`, `.hg`, `vendor`, `deps`, `.idea`, `.vscode`, `.DS_Store`, `__pycache__`, `.pytest_cache`, `dist`, `build`, `target`, `.cache`

### Default Ignored Extensions
`.png`, `.jpg`, `.jpeg`, `.gif`, `.bmp`, `.svg`, `.ico`, `.pdf`, `.zip`, `.tar`, `.gz`, `.rar`, `.7z`, `.exe`, `.dll`, `.so`, `.dylib`, `.o`, `.a`, `.pyc`, `.class`

### Integration Points
- `scanner.go`: `ScanWorkspace()` checks `.swlignore` first, then defaults
- `snapshot.go`: `BuildSnapshot()` checks `.swlignore` first, then defaults
- `manager.go`: `NewManager()` loads `.swlignore` at initialization

---

## 15. Abbreviations

| Abbreviation | Meaning |
|-------------|---------|
| SWL | Semantic Workspace Layer |
| KG | Knowledge Graph (SWL's SQLite-backed graph) |
| SSE | Server-Sent Events |
| LOD | Level of Detail |
| Tier 0–3 | Extraction cost tiers |
| Tier 1–3 | Query engine tiers |
