# SWL Refactor: Comprehensive Plan v2

> Verbatim requirements: `docs/swl/swl-refactor-requirements.md`
> v1 (rejected as superficial): commit history
> This document confronts the actual conceptual tensions in the requirement set, derives the architectural model from first principles, and only then proposes phases.

---

## Part 1 — What Went Wrong with v1, and What the Real Problem Is

The v1 plan failed because it treated the five visible drifts (bloat, hardcoding, missing description, query gaps, no feedback) as five independent fixes. They are not. They are **symptoms** of three deeper architectural failures, and any quick-win plan must engage with the *failures*, not just patch the symptoms.

### The three architectural failures

**Failure 1 — No notion of *value***
The current extractor treats every regex match as equal. A file's role in the project (e.g., "main entry point", "configuration loader", "test harness") is the same kind of fact as a one-line `// TODO: refactor`. The graph cannot distinguish high-signal facts (the project's purpose) from low-signal ones (a function name buried in a generated file). Without a value gradient, "extract less" inevitably becomes "extract less of *everything*", which kills immediate intelligence — exactly the trap v1 fell into.

**Failure 2 — No notion of *this workspace***
Every workspace is treated as a generic blob of source files in known languages. The extractor doesn't know whether it's looking at a CLI tool, a library, a microservice, a research repo, an embedded firmware project, a documentation site. Yet the *meaning* of "important file" is entirely workspace-dependent. The hardcoded `projectTypeFiles` map is the degenerate, build-tool-only proxy for this.

**Failure 3 — No notion of *what the LLM is doing***
SWL extracts on tool call but never asks "is this extraction worth keeping?". It never observes whether what it stored was used, whether queries succeeded, whether the LLM had to re-derive facts SWL should already have known. The infrastructure for this exists (`access_count`, `events`, `constraints`, `infLog`) but is dormant, dead, or misused.

### Restating the goal in terms of the failures

A correct SWL must:
1. **Stratify knowledge by value**, so it can be cheap *and* immediately useful (failure 1)
2. **Adapt extraction to the workspace's actual shape**, not a fixed taxonomy (failure 2)
3. **Close the loop between what the LLM asks and what SWL extracts**, so it gets sharper over time without LLM calls (failure 3)

These three properties together produce: an *invisible brain* that gives the LLM verified, workspace-shaped semantic knowledge with bounded cost, on constrained hardware.

---

## Part 2 — Confronting the Tensions Directly

The v1 plan buried these. They must be solved on the surface, because the right answer is non-obvious in each case.

### Tension A — Immediate intelligence vs. avoiding bloat

**These are not opposed if extraction is stratified by value.** The 20k symbols that nobody asks about are a *quantity* problem; the LLM's need-to-know-immediately is a *quality* problem. Solving them with the same mechanism (extract everything / extract nothing) is wrong. The right resolution is a manifest-first model: scan-time extraction produces a small (~50–500 entity) **semantic profile** of the workspace — its purpose, its top-level structure, its key files, its language stack, its README content, its module manifest, its entry points. Per-file granular detail (every symbol, every import) is *not* part of immediate intelligence; it is detail that is extracted lazily, on demand, when the LLM actually touches the file.

This is the opposite of what scanning does today, which treats every `func` as equally important to extract upfront.

### Tension B — Generic vs. effective

A configuration system that lets the user define everything tends to require the user to *know* everything; a hardcoded system that knows everything tends to be useless outside the cases it was hardcoded for. Resolution: the system ships with a **default ontology and ruleset** that produces correct behavior on common workspaces with zero configuration; the workspace can **extend or override** any rule via a `swl.rules.yaml`; and the system can **propose new rules to the LLM** when it detects gaps (failed queries, repeated unknown patterns).

The key shift: rules are *data*, not code. A rule has a selector (when does it apply), an action (what entity / edge / metadata does it produce), a confidence weight, and a source (built-in / workspace / learned). The extractor becomes a small generic engine that walks rules. New languages and new semantics are added by writing rules, not by recompiling.

### Tension C — Self-improving vs. local + cheap

"Self-improving" must not mean "calls an LLM to improve itself" or "trains a model". On RPi-class hardware that's a non-starter. It means **deterministic feedback signals from observable events**:

- A query succeeded (returned ≥1 result the LLM didn't immediately re-query) → the rule that produced those entities gets a +1 useful signal
- A query failed (returned nothing, or LLM immediately re-asks variant) → the gap is recorded; if the same gap appears N times, SWL proposes a new rule
- An entity is *read* (returned by a query) → its `access_count` increments; entities with high read-count become "hot" and get prioritized for richer extraction
- A rule fires often but produces entities that are never queried → the rule is *cold*, demoted in priority and eventually pruned

No ML, no LLM calls. Just counters, ratios, and threshold-driven adjustments. The `events`, `access_count`, and `constraints` tables already exist for exactly this; they're just not wired up.

### Tension D — Quick win vs. fundamental redesign

The user explicitly asked for a quick-win refactoring that preserves strengths. Resolution: the plan refactors **internals** while preserving every public API, every existing invariant, and every integration surface. Nothing breaks for callers. The Manager / QuerySWLTool / hooks system stays. Inside, the extractor becomes a rules engine; the scanner becomes manifest+detail; the query system gets a multi-signal scorer.

---

## Part 3 — Architectural Model

The proposed architecture is six layers; each addressable independently; each with a clean interface.

```
┌─────────────────────────────────────────────────────────────────┐
│                     query_swl tool (preserved)                   │
└─────────────────────────────────────────────────────────────────┘
                                │
┌───────────────────────────────▼─────────────────────────────────┐
│  Layer 6 — Feedback Loop                                         │
│   • observes queries, results, rule firings, entity reads        │
│   • adjusts rule weights, entity hotness                         │
│   • detects gaps → proposes rules                                │
└───────────────────────────────┬─────────────────────────────────┘
                                │
┌───────────────────────────────▼─────────────────────────────────┐
│  Layer 5 — Query Engine (multi-signal)                           │
│   • intent decomposition (what is the LLM asking?)               │
│   • multi-signal scoring (name/location/imports/asserts/usage)   │
│   • returns ranked, confidence-weighted answers                  │
└───────────────────────────────┬─────────────────────────────────┘
                                │
┌───────────────────────────────▼─────────────────────────────────┐
│  Layer 4 — Ontology                                              │
│   • typed entity vocabulary (Project, Module, EntryPoint, …)    │
│   • typed relations (defines, depends_on, owns, implements, …)   │
│   • workspace-extensible; drives query templates                 │
└───────────────────────────────┬─────────────────────────────────┘
                                │
┌───────────────────────────────▼─────────────────────────────────┐
│  Layer 3 — Rules Engine (generic extractor)                      │
│   • selector → action rules, declarative                         │
│   • built-in defaults + workspace overrides + learned rules      │
│   • each rule has confidence weight, source, fire counter        │
└───────────────────────────────┬─────────────────────────────────┘
                                │
┌───────────────────────────────▼─────────────────────────────────┐
│  Layer 2 — Detail Extraction (lazy, on tool call)                │
│   • triggered by tool calls (read/write/list/exec)               │
│   • extraction depth proportional to entity hotness              │
│   • respects confidence-weighted budget                          │
└───────────────────────────────┬─────────────────────────────────┘
                                │
┌───────────────────────────────▼─────────────────────────────────┐
│  Layer 1 — Manifest Layer (immediate intelligence at scan)       │
│   • workspace identity (name, purpose, kind)                     │
│   • top-level structure (key dirs, their roles)                  │
│   • entry points, build manifest, README content                 │
│   • language/tech stack, conventions                             │
│   • bounded: 50–500 entities total, regardless of repo size      │
└─────────────────────────────────────────────────────────────────┘
                                │
                  Storage: existing entity/edge SQLite (preserved)
```

---

## Part 4 — Each Layer in Concrete Detail

### Layer 1 — Manifest Layer (NEW)

**Purpose:** Give the LLM immediately useful semantic intelligence on session start, with bounded entity count regardless of workspace size.

**What it produces (deterministic, no LLM):**

| Entity Type | How it's derived |
|-------------|------------------|
| `Project` | One per workspace. Name from manifest file (`go.mod` module path, `package.json` name, `Cargo.toml` package name, or directory basename). |
| `ProjectDescription` | From README's first paragraph after H1 + manifest description fields + comment header in main entry. Stored on `Project.metadata["description"]`. |
| `ProjectKind` | From signals: presence of cmd/, main.go, package.json bin → CLI / app; pkg/ + no main → library; firmware indicators → embedded; etc. Rule-driven, not hardcoded. |
| `LanguageStack` | Aggregated from extension counts across the workspace. Top 3 by file count. |
| `BuildManifest` | The actual go.mod / package.json / Cargo.toml / pyproject.toml / Makefile content as one entity with parsed metadata (deps, scripts, bin, version, license). |
| `EntryPoint` | Files matching configured patterns: `main.go`, `cmd/*/main.go`, `index.{ts,js}`, `__main__.py`, `bin/*` script files, files matching `package.json#bin`. |
| `KeyDirectory` | Top-level dirs with known semantic role: `pkg/`, `cmd/`, `src/`, `internal/`, `lib/`, `web/`, `docs/`, `tests/`, `examples/`. Rule defines role. |
| `Convention` | Detected from structure: "uses Go modules", "has Docker setup", "uses GitHub Actions", "has CI", etc. From file presence patterns. |
| `Document` | Top-level docs: README files, LICENSE, CONTRIBUTING, ARCHITECTURE.md, etc. Their first paragraph stored as description. |

**Bounded by design:**
- Max 1 Project, 1 ProjectKind, 1 LanguageStack, 1 BuildManifest
- Max 10 EntryPoint, 20 KeyDirectory, 30 Convention, 50 Document — configurable per workspace
- Total upper bound: ~100–200 entities for any workspace, no matter the size

**This is what enables `query_swl {question:"what is the project goal?"}` and `query_swl {resume:true}` to give immediate, useful answers without any per-file extraction.** It directly resolves the v1 conflict.

**Files:** new `pkg/swl/manifest.go`; refactor of `scanner.go` to call manifest builder before/instead of recursive content extraction; new section in `session.go SessionResume()` to surface manifest in the resume digest.

### Layer 2 — Detail Extraction (REFACTOR)

**Purpose:** Per-file granular knowledge (symbols, imports, tasks, sections), extracted only when an LLM tool call actually touches the file, with depth proportional to the file's measured importance.

**Triggers (not new — already exist):**
- `write_file` / `edit_file` → full extraction (highest priority — LLM is actively shaping this file)
- `read_file` → full extraction (LLM cares about this file *now*)
- `list_dir` → directory enumeration only (no per-file extraction)
- `exec` with file paths in stdout → opportunistic shallow indexing of mentioned files

**What changes from today:**

1. **Scanner no longer triggers detail extraction.** `ScanWorkspace()` runs only the manifest layer. (This is what kills the 20k-symbol bloat.)

2. **Detail extraction respects a per-file budget that scales with hotness.** Hotness = entity's `access_count` (which we will fix to actually mean read-count, see Layer 6). A cold file gets the default budget (say, 20 symbols); a hot file gets 5× the budget. Budgets are configurable per FileRule in `swl.rules.yaml`.

3. **Detail extraction is gated by the rules engine (Layer 3), not hardcoded patterns.** No more `symPatterns`, `importPatterns` arrays compiled into Go.

4. **The result of Layer 2 is always written through the same upsert invariants that exist today** (confidence monotonicity, method priority, fact_status discipline). No regression here.

**Files:** refactor `extractor.go` to consume rules from Layer 3; remove direct calls from `scanner.go`; preserve all `ExtractContent` / `ExtractDirectory` / `ExtractExec` / `ExtractWeb` / `ExtractLLMResponse` *signatures* but rewrite their internals to be rules-driven.

### Layer 3 — Rules Engine (NEW, replaces hardcoded extractor logic)

**Purpose:** Make extraction behavior fully configurable as data; eliminate the 123 hardcoded items audited.

**Rule schema (YAML for human authoring; JSON-equivalent on disk):**

```yaml
# swl.rules.yaml — workspace-level overrides; built-in defaults same shape
version: 1

ontology:
  entity_types:
    # Built-in inherited; workspace can add custom types here
    - name: Service
      parents: [Module]   # optional inheritance
      semantics: "deployable unit"
  relations:
    - name: deploys_to
      from: [Service]
      to: [Environment]

ignores:
  dirs: [".git", "node_modules", "vendor", "dist", "build"]
  extensions: [".png", ".pdf", ".so", ".exe"]
  patterns: ["*_generated.go", "*.pb.go"]
  # globs evaluated against workspace-relative path

manifest_rules:
  # Layer 1 rules
  - id: detect_project_kind_cli
    when:
      file_exists_any: ["cmd/*/main.go", "main.go", "package.json:bin"]
    produce:
      entity:
        type: ProjectKind
        name: cli
        confidence: 0.9
        method: extracted

  - id: project_description_from_readme
    when:
      file_match: "README.md"
    extract:
      description:
        from: first_paragraph_after_h1
        max_length: 280
    bind_to: Project

file_rules:
  # Layer 2 / per-file extraction
  - id: go_files
    when:
      extension: [".go"]
    extract:
      symbols:
        patterns:
          - 'func\s+(?:\(\w+\s+\*?\w+\)\s+)?(\w+)\s*\('
          - 'type\s+(\w+)\s+(?:struct|interface)'
        max: 60
        budget_multiplier_when_hot: 5
      imports:
        patterns: ['^\t"([^"]+)"']
        max: 40
      tasks:
        patterns: ['(?i)(TODO|FIXME|HACK)[:\s]+(.+)']
        max: 30

  - id: markdown_files
    when:
      extension: [".md"]
    extract:
      sections:
        patterns: ['^(#{1,3})\s+(.+)']
        max: 30
      description:
        from: first_paragraph_after_h1

tool_rules:
  # Layer 2 / tool interception
  - id: read_file_intercept
    when:
      tool_name: read_file
    actions:
      - upsert_entity:
          type: File
          name_from: args.path
      - extract_content:
          content_from: result
          strip_header_pattern: '^\['
          rule_set: file_rules

  - id: web_fetch_intercept
    when:
      tool_name: web_fetch
    actions:
      - upsert_entity:
          type: URL
          name_from: args.url
      - extract_content:
          content_from: result
          rule_set: web_rules

semantic_rules:
  # Layer 4 inference (ontology-driven derivations)
  - id: imports_imply_dep_on_module
    when:
      pattern: "(File)-[imports]->(Dependency)"
    derive:
      edge:
        from: $File
        rel: depends_on
        to: $Module(of $Dependency)

constraints:
  # Layer 6 — invariants and quality gates
  - name: every_service_has_owner
    query: "Service entities without 'owns' edge from User"
    action: WARN
```

**Rule application engine:**
- Rules are loaded once at `Manager` init: built-in defaults from embedded `rules_default.yaml`, then deep-merged with `{workspace}/.swl/rules.yaml` if present
- Each rule has a `fire_count`, `useful_count`, `confidence` tracked in a new `rule_stats` table
- Rule selection at extraction time is sorted by `(confidence × useful_ratio)` descending
- A rule with `useful_ratio < 0.05` after N>50 firings is *cold* and demoted; after 200 firings still cold, removed from active set (kept in DB for audit)

**Built-in defaults reproduce current behavior exactly** — so this is a strict refactor with no behavioral regression at the default config.

**Files:** new `pkg/swl/rules.go` (engine), `pkg/swl/rules_default.yaml` (embedded), `pkg/swl/rule_stats.go` (telemetry); rewrite `extractor.go` and `inference.go` to consume rules; new `rule_stats` table in `db.go`.

### Layer 4 — Ontology (NEW)

**Purpose:** Give entity types and relations *meaning*, so the query engine can reason across them rather than string-matching on names.

The current open-enum string types (`File`, `Symbol`, `Task`, `URL`, etc.) are kept for backward compatibility, but a typed registry is added on top:

```go
type EntityTypeDef struct {
    Name        string
    Parent      string         // inheritance: Service → Module → Entity
    Description string
    Required    []string       // metadata keys that must be present
    Indexable   []string       // metadata keys to create indexes on
}

type RelationDef struct {
    Name         string
    FromTypes    []string      // valid source types
    ToTypes      []string      // valid target types
    Symmetric    bool
    Transitive   bool          // for closure queries
    Inverse      string        // optional inverse relation name
}
```

The ontology is **advisory in v2 (logs warnings on violations) and enforceable in a future version (rejects writes that violate)**. Quick win: just having the types declared lets the query engine treat `is_a Module` (matching Service, Library, Package, etc.) as a single concept.

The ontology lives in the same `swl.rules.yaml` (as shown above). Built-in defaults declare the existing 15 entity types and 25 edge relations. Workspace can add domain types: `Service`, `APIEndpoint`, `Database`, `Migration`, `Test`, etc.

**Files:** new `pkg/swl/ontology.go`; loaded by `manager.go` at startup; consumed by Layers 5 and 6.

### Layer 5 — Query Engine (REWRITE of `query.go`)

**Purpose:** Answer the LLM's questions semantically — combining multiple signals — instead of regex-on-keywords + LIKE-on-name.

The current Tier 1/2/3 system stays as the *interface*; its internals are replaced by an **intent-driven, multi-signal scorer**.

**Intent decomposition:**

A question like "where is the file that does authentication?" decomposes to:
- Intent: `find_entity_by_purpose`
- Target type (constraint, from ontology): `File` (or any subtype)
- Purpose terms: `["authentication"]`
- Result count: top N

Intent recognition is done via a small set of declarative intent templates (also in `swl.rules.yaml`):

```yaml
query_intents:
  - id: find_by_purpose
    patterns:
      - "where is the (?P<type>\w+) that (?:does|handles|implements) (?P<purpose>.+)"
      - "find (?P<type>\w+s?) for (?P<purpose>.+)"
    handler: scoring.find_by_purpose

  - id: project_goal
    patterns:
      - "what is the (?:project )?(?:goal|purpose|aim)"
      - "what does this project do"
    handler: manifest.project_purpose

  - id: file_summary
    patterns:
      - "what does (?P<name>.+) do"
      - "describe (?P<name>.+)"
    handler: scoring.file_summary
```

**Scoring across multiple signals:**

For `find_by_purpose("authentication")`, the scorer queries the graph with weighted contributions from:

| Signal | Weight | What it does |
|--------|--------|--------------|
| Name match | 0.25 | File name / path contains "auth" |
| Path match | 0.15 | File is in a directory named `auth/` |
| Description match | 0.30 | `metadata["description"]` contains "authentication" — set by manifest extraction OR LLM's `assert` |
| Import signals | 0.15 | File imports things matching `*auth*`, `*jwt*`, `*oauth*`, `crypto/*` (configurable signal hints in rules.yaml) |
| Symbol match | 0.10 | File defines symbols matching the term |
| Hotness (read freq) | 0.05 | File has been read often when authentication topics were queried (Layer 6 feedback) |

Weights are **configurable** in `swl.rules.yaml`. The scorer returns top-K with their score breakdown so the LLM (and humans) can see why each result was chosen.

**This is what actually answers "where is the file that does X?" in a workspace-agnostic way** — and it does so without a description column, without LLM calls, and without any hardcoded language assumption. Multiple signals compensate for any single one being absent.

**Files:** rewrite `query.go`; new `pkg/swl/scorer.go`; intent patterns in `rules_default.yaml`; preserved query_swl tool surface.

### Layer 6 — Feedback Loop (NEW + activates dormant infra)

**Purpose:** Make SWL self-improving in a deterministic, local, cheap way.

**What gets observed:**

Three event streams, all written to existing `events` table (currently dormant):

1. `tool_call` events (already triggered, just need INSERT) — every tool call flows through `PostHook`; record it
2. `query` events — every `query_swl` invocation, with the question, intent matched, results count, top result IDs
3. `assert` events — every `query_swl {assert:...}` records a fact; capture this for confidence calibration

**What gets adjusted (deterministic rules, no ML):**

| Observation | Adjustment |
|-------------|------------|
| Query returned ≥1 result, LLM didn't immediately re-query the same thing | Mark top-K result entities as "useful" (+1 useful_count); rules that produced them get +1 useful firing |
| Query returned 0 results | Record gap: `(question, intent, terms)` in `query_gaps` table. If same gap appears N≥3 times, surface it in `query_swl {gaps:true}` as a candidate for `assert` or rule extension |
| Entity returned by ≥M queries in a session | Promote to "hot" — `hotness` field in metadata; future detail-extraction passes use larger budget |
| Rule fired N≥50 times with useful_ratio < 0.05 | Demote: drop from active rule set (remains in DB) |
| LLM `asserts` a description for entity X | Store as high-confidence fact; if X already had an extracted description, mark conflict for review |
| File's content_hash changed since last useful query | Re-trigger Layer 2 detail extraction proactively (only if hot) |

**No ML, no LLM calls.** Just counters in SQLite, threshold checks at query time, and adjustments at extraction time.

**Self-improvement materializes as:**
- The graph gets richer where the LLM is actively working (hot files), shallower where it isn't
- Rules that work get stronger; rules that don't get pruned
- Repeated query failures become explicit gaps the LLM can fill via `assert`
- The system shape *adapts to the workspace and the work* over time

**Files:** new `pkg/swl/feedback.go`; new `query_gaps` and `rule_stats` tables in `db.go`; INSERT calls in `inference.go` and `query.go`; periodic adjustment runs from `decay.go`-style background loops.

---

## Part 5 — Phased Execution

Three phases, each independently shippable. Order chosen so each phase delivers immediate user-visible value.

### Phase A — Manifest + Lazy Detail (the immediate quick win)

**Delivers:**
- Immediate semantic intelligence at session start (Layer 1)
- Eliminates the 20k-symbol bloat (Layer 2 lazy)
- These two are designed *together* because the v1 conflict only resolves when both ship at once

**Concrete changes:**
1. New `pkg/swl/manifest.go` with `BuildManifest(workspace)` returning a `GraphDelta` of ≤200 entities
2. Refactor `scanner.go ScanWorkspace()`: drop the per-file `ExtractContent` call inside the walk loop; replace with a manifest pass + structural file/dir indexing only
3. Preserve `ExtractContent` and keep its existing call sites in `inference.go` (`postApplyReadFile`, `postApplyWriteFile`) — detail still gets extracted on tool touch
4. Augment `session.go SessionResume()` to surface manifest entities in the resume digest
5. Add `query_swl {manifest:true}` mode that returns the manifest entities formatted for LLM consumption
6. Add Tier 1 patterns in `query.go` for "what is the project goal/purpose/about", "what kind of project is this", "what are the entry points" — backed by manifest entities

**What's preserved:** All existing entity types, all existing public APIs, all upsert invariants, all hooks integration. Detail extraction still happens — just on tool call, not on scan.

**Verification:**
- Scan picoclaw itself with `query_swl {scan:true}` → entity count ≤ 500 (vs. 20k+ today)
- `query_swl {question:"what is the project goal?"}` → returns project description
- `query_swl {question:"functions in pkg/swl/manager.go"}` after `read_file` on it → returns symbols
- Same query *before* `read_file` → returns "file not yet read in detail; ask the agent to read it" notice
- Performance: manifest build on a 1000-file workspace completes in < 2s on rpi 0 2w (target)

### Phase B — Rules Engine + Ontology (eliminates hardcoding)

**Delivers:**
- All extraction logic becomes data-driven (Layer 3)
- Workspace-level rules.yaml override
- Ontology declares typed vocabulary (Layer 4)
- 123 hardcoded items move to `rules_default.yaml`

**Concrete changes:**
1. New `pkg/swl/rules.go` engine: parses YAML, applies selectors, runs actions
2. New `pkg/swl/rules_default.yaml` embedded via `go:embed` — reproduces current behavior bit-for-bit
3. Workspace override: `{workspace}/.swl/rules.yaml` deep-merges with defaults
4. Rewrite `extractor.go` internals to consume rules; preserve all function signatures
5. Rewrite `inference.go toolMap` as a rules section (`tool_rules`); preserve `RegisterToolHandler` for programmatic Layer 0 escape hatch
6. Rewrite `scanner.go` skip logic to consume `ignores` section
7. New `pkg/swl/ontology.go` loaded from rules.yaml
8. New `rule_stats` table to track fire/useful counters

**What's preserved:** Default behavior identical to v1 SWL when no workspace rules.yaml present. All existing config.go fields continue to work (mapped to rules engine internally).

**Verification:**
- Diff of entities produced by current SWL vs. rules-engine SWL on the same scan → identical
- Add a `.tf` (Terraform) FileRule in workspace rules.yaml with `resource\s+"[^"]+"\s+"([^"]+)"` symbol pattern → scanner extracts Terraform resources without code change
- Add an ignore pattern `examples/**` → next scan respects it
- Define a new ontology type `APIEndpoint` with required `metadata.method` → assert one via `query_swl {assert:..., type:"APIEndpoint"}` → ontology validates

### Phase C — Multi-signal Query + Feedback (semantic + self-improving)

**Delivers:**
- Query engine answers semantic questions via signal scoring (Layer 5)
- Activates events, access tracking, rule stats for feedback (Layer 6)
- Gaps detection surfaces what SWL doesn't know

**Concrete changes:**
1. New `pkg/swl/scorer.go` implementing multi-signal scoring with configurable weights
2. Rewrite `query.go` Ask(): intent decomposition → scorer dispatch → ranked results
3. Preserve all existing Tier 1 patterns; they become intent templates
4. New `pkg/swl/feedback.go` observing queries, inserting events, adjusting hotness
5. Fix `access_count`: increment on every entity *returned by query* (not on upsert)
6. Activate `events` table writes from `inference.go PostHook`
7. New `query_gaps` table; surface gaps in `query_swl {gaps:true}` (replaces / extends current KnowledgeGaps)
8. Periodic adjustment: rule cold/hot promotion runs alongside `maybeDecay()`

**What's preserved:** All existing query_swl modes, all existing Tier 1/2/3 dispatch logic remains as fallback for unmatched intents.

**Verification:**
- `query_swl {question:"where is the file that does authentication?"}` returns scored results with breakdown, even when no file has "auth" literally in its name
- After 5 sessions of working in `pkg/swl/`, querying `query_swl {stats:true}` shows hot entities biased toward swl files
- Asking for a known-impossible thing 3 times → appears in `query_swl {gaps:true}` with suggestion to assert
- A custom rule that fires 100× but never produces queried entities is auto-demoted on next manager init

---

## Part 6 — What Is Explicitly NOT in Scope

These are deliberately deferred, even though they're tempting:

- LLM-call-at-scan-time for richer descriptions (violates "minimize LLM requests")
- Vector embeddings / semantic search beyond multi-signal scoring (RPi-class hardware budget)
- Constraint enforcement that blocks writes (Phase 4 ontology is advisory; enforcement is post-quick-win)
- Cross-workspace federation
- Rule-learning from LLM responses (would require an LLM); workspace authors and feedback-driven demotion are sufficient for v2
- Schema migration framework (manual upgrade is fine for now; tables are additive)

---

## Part 7 — Open Questions to Resolve Before Phase A

These need user input. Each affects design choices that ripple through the plan.

1. **Manifest depth:** Should the manifest layer parse README/markdown content into Document entities (richer description, but more entities), or only treat them as opaque files with extracted descriptions (leaner)?
2. **Rule format:** YAML or JSON for `swl.rules.yaml`? YAML is more authorable for humans/LLM; JSON is faster to parse and matches existing config style.
3. **Backward compatibility horizon:** Should the v1 `Config.ExtractSymbolPatterns` etc. continue to work as-is forever, or is there an opportunity to deprecate them in favour of pure rules.yaml after Phase B?
4. **Rule learning:** In Phase C feedback, when SWL detects a recurring gap, should it (a) just surface it for the LLM to handle via `assert`, or (b) propose a candidate rule (selector + action) for the LLM/user to accept? (b) is more powerful but more complex.
5. **Hotness reset:** Should hotness counters reset per session, decay over time, or persist forever? Decay matches the rest of SWL's philosophy.
6. **Ontology strictness:** Advisory (warn-only) for v2 is proposed; confirm this is acceptable, or do you want enforcement (reject writes) from the start?

---

## Summary of Why This Plan Is Different from v1

| Concern | v1 Approach | v2 Approach |
|---------|-------------|-------------|
| Bloat fix | "Don't extract on scan" → killed immediate intelligence | Manifest layer + lazy detail; both ship together |
| Hardcoding fix | "Add config fields" → still leaves regex/tool-map in code | Rules engine: behavior is data; rules.yaml drives everything |
| Description gap | "Add a description column, extract first comment" | Multi-signal scoring across name+path+imports+symbols+asserts+usage; description is one signal among many, never required |
| Query gap | "Add Tier 1 patterns for project goal" | Intent-driven decomposition + scoring; covers an open-ended class of questions, not a few new patterns |
| Self-improvement | "Fix access_count to count reads" | Full feedback loop: query/result/firing observations → rule weight + entity hotness → adaptive extraction depth + gap surfacing |
| Ontology | not addressed | Typed vocabulary (advisory v2, enforceable later); foundation for semantic reasoning |

The v1 plan was a list of patches; v2 confronts the architectural failures. Each layer is independently shippable, none breaks existing APIs, and the three phases together close the gap between current SWL and the original vision: a generic, configurable, self-improving invisible brain.
