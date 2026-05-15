# SWL Roadmap — Intentional Gaps & Future Work

This document records features that are partially implemented, deferred, or
intentionally incomplete. Items here are **not bugs** — they are known gaps
with deliberate rationale.

---

## Schema Tables Not Yet Used

### `constraints` table

Defined in `db.go`. Intended to hold named SQL consistency checks that the
autonomous loop can evaluate. No writer or reader has been wired; the table
is empty in all deployments.

**Rationale for deferral**: The gap-analysis feedback loop (`query_gaps`) covers
the most critical case (missing intents). Constraint checking adds value only when
entities are numerous enough to have measurable quality variance.

### `events` table

Defined in `db.go`. Intended to store per-tool-call events (tool name, phase,
args hash) for replay and audit. Currently populated nowhere.

**Rationale for deferral**: The session and edges tables already record
which entities were touched per session. Full event replay is useful for
debugging extraction pipelines, not yet needed.

---

## Entity Types Without Full Implementation

### `KnownTypeIntent` (`"Intent"`)

Created by `swl_hook.go` at TurnStart via `upsertEntity`. The `intended_for`
edge is also written. However, Intent entities are never queried back — no
handler in `query.go` retrieves them, and they do not appear in graph views.

**Next step**: Add `askIntentHistory()` handler; surface in `SessionResume`.

### `KnownTypeSubAgent` (`"SubAgent"`)

Created by `swl_hook.go` at SubTurnSpawn. The `spawned_by` edge is written.
SubAgent entities appear in graphs but have no dedicated query handler.

**Next step**: Add `askSubAgentActivity()` handler to surface agent spawning
patterns.

---

## Edge Relations Without Full Implementation

### `KnownRelContextOf` (`"context_of"`)

Defined in `types.go`. Not written anywhere in the current codebase.

**Intended use**: A Session entity is `context_of` a workspace — connecting
the temporal graph to the structural graph. Currently sessions relate to
entities only via `source_session` on edge rows.

### `KnownRelReasoned` (`"reasoned"`)

Defined in `types.go`. Not written anywhere in the current codebase.

**Intended use**: When an LLM produces a reasoning step that leads to an entity
modification, a `reasoned` edge from the Intent to the affected entity documents
the causal chain. Requires Tier 2 extraction upgrade.

### `KnownRelCoOccursWith` (`"co_occurs_with"`)

Defined in `types.go`. The constant was added per the design spec (§7.3).
The edge generation loop — "entities co-occurring in ≥4 sessions → auto
`co_occurs_with` edge" — is not yet wired in `decay.go` or `feedback.go`.

**Next step**: Add a periodic SQL query in `maybeDecay()` (or a new
`runFeedbackLoop()`) that groups edges by `source_session` pair-frequency
and upserts `co_occurs_with` edges for qualifying pairs.

---

## Per-Model Quality Profiling (Plan Phase 8)

Depends on assertion linking (now complete). Implementation requires:

1. Add `source_agent TEXT`, `source_model TEXT` columns to `edges` via
   additive migration.
2. Propagate model ID from `pkg/agent/swl_hook.go` → `PostHook`.
3. Add `askModelReliability()` handler aggregating per-model assertion
   confirmation rates.
4. Expose `GET /api/swl/model-reliability` endpoint.
5. Add minimal model reliability display to `swl-stats.tsx`.
6. Weight assertion confidence by source model's historical reliability
   in the upsert pipeline.

---

## Workspace-Level Ontology Profile Entity

A `WorkspaceProfile` entity capturing overall project type (Go service,
firmware, documentation, etc.) would give LLMs an at-a-glance project
classification without manual tagging.

**Rationale for deferral**: Requires Tier 3 LLM indexing (opt-in) and is out
of scope for constrained hardware targets.

---

## Structured Anchor Document Content Extraction

Currently `snapshot.go` stores the first non-heading paragraph of the first
8KB as the anchor "description". Structured extraction (goals, stated
dependencies, section map) would make anchor content queryable.

**Next step**: Parse anchor content into `anchorExtract{Description, Goals,
Sections, KeyTerms}` and store as structured JSON in entity metadata.

---

## Per-Extension Extraction Overrides (Plan Phase 2.6)

Config has global extraction toggles. Per-extension overrides (e.g., disable
symbol extraction for `.md` files) require:

1. `extraction_overrides` field in `swl.rules.yaml` / `RulesEngine`.
2. Extension lookup in `ExtractContent()` before dispatching extractors.

This is a direct gap in the "adaptable and configurable" requirement.
