# SWL Refactor: Implementation Plan v3

> Requirements: `docs/swl/swl-refactor-requirements.md`
> Refined through collaborative dialogue: 2026-05-05
> Previous versions (v1, v2) rejected as symptom-patching and overengineered respectively.

---

## Part 1 — Root Causes

Previous plans addressed symptoms. This plan addresses the three architectural failures they revealed.

**Failure 1 — No value gradient**
Every extracted fact is treated as equally important. The graph cannot distinguish "this workspace exists to do X" (high signal, needed immediately) from "this file has a TODO on line 47" (low signal, only useful if asked). Without a value gradient, extraction is either everything-or-nothing. Current result: 20k entities on a small codebase, none answering "what is this workspace for?".

**Failure 2 — No workspace identity**
Every workspace is treated as a generic blob of source files. The extractor hardcodes meaning for software project shapes (go.mod → "go project", `cmd/` → "entry points") and is useless for everything else: research datasets, legal document collections, firmware, documentation sites, config management repos. SWL must be workspace-content agnostic.

**Failure 3 — No feedback loop**
The infrastructure for self-improvement exists (`access_count`, `events`, `constraints`, inference ring buffer) but is dormant, misused, or never written to. SWL never observes whether what it stored was useful, never detects which agent produced which facts, never converges across multiple agents. `access_count` counts *writes*, not reads. The events table is never written. Constraints are never enforced.

---

## Part 2 — Design Principles

1. **Generic over specific** — the workspace can be anything. Every design decision holds for code, docs, research, config, firmware, or mixed content.
2. **Cheap before expensive** — extraction runs in cost tiers; expensive tiers are opt-in and off by default.
3. **LLM-agnostic signals** — self-improvement is grounded in workspace actions (tool calls, assertions), never in inferences about LLM behavior. Different LLMs and agents exhibit different tendencies, drifts, and hallucinations; SWL cannot depend on any of them behaving predictably.
4. **Multi-agent neutral** — multiple different LLMs may work the same workspace. SWL is a shared semantic layer; convergence across agents is stronger signal than any single agent's output.
5. **Autonomous** — the feedback loop runs without human or LLM intervention. It adjusts weights, promotes/demotes facts, surfaces gaps. It does not rewrite rules automatically.
6. **Additive** — all existing public APIs, upsert invariants, and integration surfaces (Manager, QuerySWLTool, hooks) are preserved throughout.

---

## Part 3 — Conceptual Model

Two moving parts, both driven by configuration:

```
  swl.rules.yaml                    swl.query.yaml
  ──────────────                    ──────────────
  what to extract                   how to answer
  when, how, cost tier              intent patterns
  semantic label rules              label weights
  tool interception                 graph traversal templates
  feedback thresholds               (NOTE: revisit unification in v4)
         │                                  │
         ▼                                  ▼
    EXTRACTION                        QUERY ENGINE
    ──────────                        ────────────
    scan → semantic snapshot          intent decomposition
    (bounded, generic)                label-weighted scoring
    + lazy per-file detail            graph traversal
    on tool call                      + feedback observation
         │                                  │
         └──────────────┬───────────────────┘
                        ▼
               Shared Entity Graph
               (SQLite — preserved)
```

**Semantic labels** are the bridge. A label is a structured tag a rule assigns to an entity and stores in `metadata`: `role: "authentication"`, `domain: "data-access"`, `kind: "anchor-document"`, `content_type: "sql"`. Labels are what make queries answerable — "where is the file that handles authentication?" becomes a label search, not a name match.

---

## Part 4 — Extraction Model

### 4.1 Workspace Semantic Snapshot (scan-time, bounded)

The scanner no longer extracts per-file symbols. Instead it produces a **workspace semantic snapshot**: a bounded set of high-signal entities that answer the LLM's session-start questions before any tool call has happened.

The snapshot answers: *"what is this workspace for?"*, *"what areas does it have and what do they do?"*, *"what are the key documents?"*, *"what goals are stated?"*, *"what kinds of content exist here?"*

**What the snapshot contains:**

| Entity | Derived from | How |
|--------|-------------|-----|
| Semantic areas | Significant directories | Rules classify by: anchor document presence and content, file type distribution, naming signals, child relationships. A directory becomes a semantic area only when a rule fires — empty dirs and generated-file dirs produce nothing. |
| Anchor documents | Purpose-stating files | Rules define patterns: `README*`, `OVERVIEW*`, `ARCHITECTURE*`, manifest files, files with structured header comments. Extracted: first paragraph, section headings, stated goals. |
| Content profile | Extension distribution | Aggregated counts → dominant content types. Not hardcoded language names — generic content-type labels. |
| Key relations | Top-level connections | Rules define signals: cross-reference patterns, import conventions, naming proximity. |
| Explicit goals | Human-stated intent | Extracted from anchor documents wherever present; stored as entity metadata. |

**Bounded by design:** configurable max per entity class in `swl.rules.yaml`. Default upper bound: ~100–300 entities for any workspace, regardless of size.

### 4.2 Four Extraction Tiers (cost-ordered)

| Tier | Name | Cost | Default | Runs when |
|------|------|------|---------|-----------|
| 0 | Structural / derivative | Near-zero | Always on | Scan-time: file names, dir structure, extension distribution, naming patterns, anchor document presence |
| 1 | Ontological inference | Cheap (SQL only) | Always on | Scan + after writes: rule-driven derivations from existing graph facts — "if entity A has label X and relates to entity B, derive label Y on B" |
| 2 | Passive LLM capture | Free (happens anyway) | Always on | During tool use: existing `AfterLLM` hook captures semantic facts from LLM reasoning and responses. This is the *expected* deep learning channel during real work. |
| 3 | Active LLM indexing | Expensive | Off by default | Scan-time only, if configured: SWL asks a configured LLM to summarize/classify anchor documents. Configurable model, token budget, which file classes. Never expected on constrained hardware (RPi 0 2W). |

Tier 2 requires no additional infrastructure — it is the existing `AfterLLM` / `ExtractLLMResponse` path. The LLM's deep understanding of files it works with flows into SWL naturally during real sessions, for free. Active LLM indexing (Tier 3) is the explicit opt-in version of this, paid for upfront.

### 4.3 Lazy Per-File Detail

Per-file granular knowledge (symbols, imports, tasks, sections) is extracted only when a tool call touches the file. The scanner's walk loop no longer calls `ExtractContent()`.

Extraction depth scales with **entity hotness** (read frequency, cross-agent access count). Cold files get a minimum symbol budget; hot files get a configurable multiplier.

When a query asks for per-file detail on an unread file, the response includes an explicit notice: *"[filename] has not been read in detail — use read_file to populate its contents."* Silent empty answers are eliminated.

### 4.4 `swl.rules.yaml` Schema

```yaml
version: 1

limits:
  max_semantic_areas: 50
  max_anchor_documents: 30
  max_entities_per_scan: 300
  detail_budget_cold: 20          # symbols/imports extracted for unread files
  detail_budget_hot_multiplier: 5 # multiplier when entity is hot

tiers:
  structural: true
  ontological: true
  passive_llm: true
  active_llm:
    enabled: false
    model: ""                     # e.g. "claude-haiku-4-5-20251001"
    token_budget_per_file: 200
    apply_to: ["anchor_documents"]

ignores:
  dirs: [".git", "node_modules", "vendor", "dist", "build"]
  extensions: [".png", ".pdf", ".so", ".exe", ".db", ".sqlite"]
  patterns: []                    # glob patterns, workspace-relative

anchor_patterns:
  - "README*"
  - "OVERVIEW*"
  - "ARCHITECTURE*"
  - "CONTRIBUTING*"
  - "*.meta.md"

area_signals:
  - id: dir_has_anchor
    when:
      contains_file_matching: anchor_patterns
    produce:
      label: {kind: "documented-area"}
      confidence: 0.9

  - id: dir_dominant_content_type
    when:
      dominant_extension_ratio: 0.6
    produce:
      label: {content_type: "$dominant_extension"}
      confidence: 0.7

file_rules:
  - id: go_files
    when:
      extension: [".go"]
    extract:
      symbols:
        patterns: ['(?m)^func\s+(?:\(\w+\s+\*?\w+\)\s+)?(\w+)\s*\(']
        max: 20
      imports:
        patterns: ['(?m)^\t"([^"]+)"']
        max: 20
      tasks:
        patterns: ['(?i)(TODO|FIXME|HACK)[:\s]+(.+)']
        max: 10

  - id: markdown_files
    when:
      extension: [".md"]
    extract:
      sections:
        patterns: ['(?m)^(#{1,3})\s+(.+)']
        max: 20
      description:
        from: first_paragraph_after_h1

  # workspace adds its own file_rules here for custom types

tool_rules:
  - id: read_file
    when: {tool_name: "read_file"}
    actions:
      - upsert_entity: {type: File, name_from: "args.path"}
      - extract_content: {content_from: result, rule_set: file_rules, strip_header: true}
      - record_event: {kind: file_read}

  - id: write_file
    when: {tool_name: "write_file"}
    actions:
      - upsert_entity: {type: File, name_from: "args.path"}
      - extract_content: {content_from: "args.content", rule_set: file_rules}
      - record_event: {kind: file_write}

  - id: exec
    when: {tool_name: "exec"}
    actions:
      - upsert_entity: {type: Command, name_from: "args.command"}
      - extract_generic: {content_from: result}
      - record_event: {kind: exec}

  - id: web_fetch
    when: {tool_name: "web_fetch"}
    actions:
      - upsert_entity: {type: URL, name_from: "args.url"}
      - extract_web: {content_from: result}
      - record_event: {kind: web_fetch}

  # workspace adds custom tool_rules for domain-specific tools

semantic_rules:
  # Tier 1: ontological inference from existing graph facts
  - id: imports_signal_domain
    when:
      pattern: "(File)-[imports]->(Dependency)"
      dependency_label_matches: "domain:*"
    derive:
      entity_label: {domain: "$matched_domain", confidence: 0.6}

feedback_thresholds:
  promote_after_agent_count: 3
  demote_after_contradiction_count: 2
  cluster_after_session_count: 4
  prune_cold_rule_after_firings: 200
  prune_cold_rule_useful_ratio: 0.05
  gap_surface_after_repeat_count: 3
  hotness_decay_after_sessions: 10

---

## Part 5 — Query Model

### 5.1 How queries work

1. **Intent recognition** — match the question against intent patterns in `swl.query.yaml`
2. **Label-weighted scoring** — score entities by signal strength: exact label match > partial label match > name match > path match > session co-occurrence. Weights configurable per intent.
3. **Graph traversal** — for relational questions ("what is in the auth area?", "what depends on X?"), walk edges from matched entities
4. **Fallback** — existing Tier 3 text search on entity names for unmatched intents

The key insight: **queries become answerable because extraction was semantic, not because the query engine is smart.** A well-labelled graph with a simple scorer beats a clever scorer on a name-only graph every time.

### 5.2 `swl.query.yaml` Schema

```yaml
version: 1

# NOTE: consider unifying with swl.rules.yaml in next version/iteration

intents:
  - id: workspace_purpose
    patterns:
      - "what is this (?:workspace|project|repo) (?:for|about|doing)"
      - "what (?:does this|is the) (?:project|workspace) (?:do|goal|purpose|aim)"
      - "(?:describe|summarise|summarize) (?:this|the) workspace"
    handler: manifest_summary
    entities: [AnchorDocument, SemanticArea]
    labels: [kind: "workspace-root", kind: "documented-area"]

  - id: find_by_purpose
    patterns:
      - "where is (?:the )?(?P<type>\\w+) that (?:does|handles|implements|manages) (?P<purpose>.+)"
      - "find (?P<type>\\w+s?) (?:for|that) (?P<purpose>.+)"
      - "which (?P<type>\\w+) (?:handles|does|is responsible for) (?P<purpose>.+)"
    handler: label_search
    search_on: [metadata.role, metadata.domain, metadata.description, name, path]

  - id: area_contents
    patterns:
      - "what is in (?:the )?(?P<area>.+?)(?:\\s+area)?"
      - "show (?:me )?(?:the )?(?P<area>.+?) (?:area|section|directory|folder)"
      - "what files are in (?P<area>.+)"
    handler: area_traverse
    traverse: {from: SemanticArea, match_name: "$area", depth: 1}

  - id: file_detail
    patterns:
      - "what does (?P<name>.+?) do"
      - "describe (?P<name>.+)"
      - "explain (?P<name>.+)"
    handler: file_summary
    includes: [symbols, tasks, metadata.description, metadata.role, metadata.domain]

  - id: workspace_goals
    patterns:
      - "what (?:are the |is the )?goals?"
      - "what (?:are we|is this) trying to (?:do|achieve|build)"
    handler: goals_summary
    entities: [AnchorDocument, Session]
    fields: [metadata.goals, goal]

  - id: content_type_distribution
    patterns:
      - "what (?:kind of|types? of) (?:content|files?) (?:are|is) (?:here|in this workspace)"
      - "what (?:language|tech|stack|technology) (?:is|does) this"
    handler: content_profile

  # workspace adds custom intents here

label_weights:
  exact_label_match: 1.0
  partial_label_match: 0.6
  name_match: 0.4
  path_match: 0.3
  session_co_occurrence: 0.2
  cross_agent_confirmation: 0.5   # bonus for cross-agent confirmed labels
```

---

## Part 6 — Autonomous Feedback Loop

### 6.1 Why signals must be LLM-agnostic

Different LLMs and agents working the same workspace exhibit different tendencies: some re-ask when results are poor, some accept wrong answers silently, some hallucinate on top of stale SWL data, some ignore SWL entirely. Any signal derived from LLM behavioral inference ("it didn't re-ask, so the result was useful") is unreliable across the agent population and would corrupt the feedback loop.

All signals are grounded in **workspace actions** — what actually happened to files and entities — not in observations of LLM decision-making.

### 6.2 Observable signals

| Signal | Observable fact | How captured |
|--------|----------------|--------------|
| Tool follow-through | After SWL returns entity X, agent calls `read_file(X)` or `write_file(X)` | `PostHook` correlates prior query results with subsequent tool calls in the same session |
| Assertion event | Agent calls `query_swl {assert:...}` | Explicit fact injection; tagged with source agent session ID |
| Assertion confirmation | Later agent asserts same fact independently | Confidence strengthened; source agents recorded |
| Contradiction | Agent B asserts fact conflicting with Agent A's assertion | Both recorded with agent ID; confidence of conflicting fact halved; surfaced in gaps |
| Cross-agent convergence | ≥N distinct agents touch same entity under same semantic context | Label promoted toward `verified` |
| Decay signal | Entity not touched by any agent in M sessions | Label confidence decays; candidate for gap surfacing |

### 6.3 Autonomous adjustment loop

Runs on the same probabilistic cadence as existing `maybeDecay()`. No human or LLM intervention required.

| Trigger | Action |
|---------|--------|
| Entity touched by ≥3 distinct agents with same label | Label confidence → `verified` |
| Assertion contradicted by ≥2 subsequent agents | Confidence halved; `fact_status: stale`; surfaced in `query_swl {gaps:true}` |
| Entities co-occurring in ≥4 sessions | Auto `co_occurs_with` edge recorded (relationship noted, no label forced) |
| Rule fired ≥200× with useful_ratio < 0.05 | Rule removed from active set; retained in DB for audit |
| Query returning 0 results, repeated ≥3× | Appears in `query_swl {gaps:true}` with suggestion to assert or add rule |
| Entity hotness not renewed for M sessions | Hotness decays; detail extraction budget returns to cold baseline |

All thresholds are configurable under `feedback_thresholds` in `swl.rules.yaml`.

### 6.4 Multi-agent awareness

Each tool call, query, and assertion is tagged with the source agent session ID. The graph accumulates per-agent evidence. Convergence across agents — not any single agent's confidence — is the primary signal of verified semantic knowledge.

> **Next iteration remark:** per-agent reliability profiles. SWL accumulates enough cross-agent evidence (assertion confirmation rates, contradiction frequency, hallucination vs. verified-fact alignment) to build per-model quality scores over time. This enables weighted initial confidence for assertions (higher-reliability agent's facts start higher) and surfaces as a standalone LLM quality diagnostic capability.

---

## Part 7 — Phase Sequencing

### Phase A — Correct behavior, hardcoded internally
**Goal:** immediate value. Fix extraction and query so the system works correctly. Logic is still in code; rules are not yet externalized.

**Changes:**

| File | Change |
|------|--------|
| `pkg/swl/snapshot.go` (new) | `BuildSnapshot(workspace) *GraphDelta`: produces bounded semantic snapshot — semantic areas, anchor documents, content profile, key relations, explicit goals |
| `pkg/swl/scanner.go` | Replace per-file `ExtractContent()` call in walk loop with `BuildSnapshot()` + structural file/dir indexing only |
| `pkg/swl/inference.go` | Preserve all existing `postApplyReadFile`, `postApplyWriteFile` etc. call sites — detail still extracted on tool touch |
| `pkg/swl/session.go` | Augment `SessionResume()` to surface snapshot entities in the digest |
| `pkg/swl/query.go` | Add intent-aware Tier 1 patterns: workspace purpose, area contents, file detail, stated goals, content profile. Fix `askSymbols` etc. to return "not yet read" notice for unread files |
| `pkg/swl/entity.go` | Fix `access_count`: increment on entity *reads* (returned by query), not on upsert |
| `pkg/swl/inference.go` | Activate `events` table INSERT from `PostHook` for every tool call |
| `pkg/swl/db.go` | Add `query_gaps` table |

**Preserved:** all public APIs, all upsert invariants, all hooks integration, all existing `query_swl` modes.

**Verification:**
- Scan the picoclaw workspace → entity count ≤ 300
- `query_swl {question:"what is this workspace for?"}` → returns description from README
- `query_swl {question:"what does pkg/swl/manager.go do?"}` before `read_file` → "not yet read in detail" notice
- Same query after `read_file` on that file → returns symbols and content
- `query_swl {stats:true}` after querying several entities → `access_count` on returned entities > 0
- `query_swl {snapshot:true}` → structured semantic overview

---

### Phase B — Configurable (generic)
**Goal:** move all hardcoded extraction and query logic to `swl.rules.yaml` and `swl.query.yaml`. Zero behavioral change when no workspace overrides present.

**Changes:**

| File | Change |
|------|--------|
| `pkg/swl/rules.go` (new) | Rules engine: loads `swl.rules.yaml`, deep-merges workspace `{workspace}/.swl/rules.yaml`, applies selectors and actions |
| `pkg/swl/rules_default.yaml` (new, embedded) | Built-in defaults reproducing Phase A behavior exactly |
| `pkg/swl/query_engine.go` (new) | Intent dispatcher consuming `swl.query.yaml`; replaces hardcoded Tier 1 pattern slice |
| `pkg/swl/swl_query_default.yaml` (new, embedded) | Built-in query intents reproducing Phase A query behavior |
| `pkg/swl/extractor.go` | Rewrite internals to consume file_rules from rules engine; all function signatures preserved |
| `pkg/swl/scanner.go` | Rewrite skip logic, anchor detection, area classification to consume rules |
| `pkg/swl/inference.go` | Rewrite `toolMap` as `tool_rules` consumed from rules engine; preserve `RegisterToolHandler` as Layer 0 escape hatch |
| `pkg/swl/db.go` | Add `rule_stats` table: `rule_id`, `fire_count`, `useful_count` |
| `pkg/config/swl.go` | Add `RulesPath`, `QueryPath` fields; existing Config fields mapped to rules engine internally |

**Verification:**
- Entity diff: Phase A vs Phase B on same workspace scan → identical output
- Add `.tf` `file_rule` with Terraform `resource\s+"[^"]+"\s+"([^"]+)"` symbol pattern → extracts Terraform resources, no code change
- Add `ignores.patterns: ["examples/**"]` → respected on next scan
- Add new intent pattern in `swl.query.yaml` → dispatched correctly
- Add workspace-specific semantic area signal → fires on matching directories

---

### Phase C — Self-improving
**Goal:** activate the autonomous feedback loop; make SWL improve with use across sessions and agents.

**Changes:**

| File | Change |
|------|--------|
| `pkg/swl/feedback.go` (new) | Observes tool follow-through, records signals, runs adjust loop alongside `maybeDecay()` |
| `pkg/swl/session.go` | Tag each tool call and assertion with source agent session ID |
| `pkg/swl/entity.go` | Contradiction detection in `UpsertEntity`: flag conflicting assertions from different agents |
| `pkg/swl/query.go` | Gap recording: failed queries → `query_gaps` table; `query_swl {gaps:true}` surfaces them |
| `pkg/swl/db.go` | Add `agent_stats` table for per-agent fact tracking (foundation for next-iteration quality profiles) |
| `pkg/swl/tool.go` | Add `query_swl {convergence:true}` mode: shows cross-agent confirmed facts |

**Verification:**
- Work in the same file across 3 sessions with different agent configs → labels promoted to `verified`
- Assert contradictory facts from two sessions → conflict appears in `query_swl {gaps:true}`
- Query something SWL doesn't know 3× → surfaces as gap with suggestion
- Cold rule after 200 firings → removed from active rule set

---

## Part 8 — Not In Scope (this refactoring)

- LLM calls at scan time for descriptions (Tier 2 passive capture covers this for free during real work)
- Vector embeddings / semantic similarity (RPi memory budget)
- Strict ontology enforcement with write rejection (advisory labelling is sufficient for v2)
- Cross-workspace federation
- Schema migration framework (tables are additive; manual upgrade is fine)
- Automatic rule generation (SWL surfaces gaps and suggestions; a human or the LLM writes new rules)

---

## Part 9 — Next Iteration Remarks

> These are design-level decisions deferred to the next version, not forgotten.

1. **Unify `swl.rules.yaml` and `swl.query.yaml`** into a single configuration file once the separation has proven itself in practice
2. **Per-agent reliability profiles** — use accumulated assertion confirmation/contradiction evidence to build per-model confidence weights; surfaces as standalone LLM quality diagnostic
3. **Ontology enforcement** — promote from advisory label warnings to write-rejection for declared type/relation constraints
4. **Pattern learning** — promote frequently-useful LLM assertions into candidate rules; surface for human/LLM review and promotion
5. **Active LLM indexing on capable hardware** — expose Tier 3 configuration properly; test on workspaces where pre-indexing pays off
