# SWL Roadmap — Deferred Features

This document records features that are intentionally incomplete or deferred.
Items here are **not bugs** — they are known gaps with deliberate rationale.

Items completed since initial deferral are moved to the "Resolved" section at the bottom.

---

## Deferred: Semantic Similarity Edges (`similar_to`)

`DeriveSimilarSymbols()` — not built. Design proposed Levenshtein distance ≤3
on symbol names to derive `similar_to` edges.

**Why deferred**: Name-distance similarity without type context produces systematic
noise. `handleRequest` and `handleResponse`, `parseJSON` and `parseYAML`, `newClient`
and `newServer` — all match by name but may be completely unrelated. Without qualifier
resolution (requires a type checker, not regex), occurrence-based cross-file matching
creates a high-noise, low-signal edge class. Reopen if a type-context approach becomes
available (e.g., AST-based matching using tree-sitter).

---

## Deferred: Path Queries (`shortestPath`)

No `shortestPath()` implementation. Design proposed recursive CTE in SQLite.

**Why deferred**: SQLite recursive CTEs with depth ≥10 generate millions of intermediate
rows on graphs with 1000+ nodes. The cost-based termination proposed in design documents
(`WHERE p.cost < 20`) is not valid SQLite syntax inside a recursive CTE's WHERE clause.
A correct implementation requires application-level BFS with a hard node frontier limit
(≤500 nodes, depth cap ≤8). Design and implement before building.

---

## Deferred: M8 — Autonomous Rule Auto-Apply

Gap analysis (`gap_analysis.go`) generates candidate YAML rule suggestions and surfaces
them inline in `query_swl {"gaps":true}`. The suggestions are ready-to-paste YAML.
However, no automatic write path exists from suggestion → `swl.rules.yaml`.

**Why deferred**: Auto-applying YAML rules without dry-run oversight risks breaking
workspace-specific extraction configuration. The correct sequence is:
1. Build a `dry_run` mode that logs what a suggestion *would* change
2. Gate auto-apply on a config opt-in (`auto_apply_rule_suggestions: true`)
3. Only apply suggestions above a confidence threshold (e.g. ≥5 query misses, ≥0.8 suggestion confidence)

Until dry-run mode is designed, auto-apply is too risky.

---

## Deferred: Per-Extension Extraction Overrides

Global extraction toggles exist (`extract_symbols`, `extract_imports`, etc.) but no
per-extension overrides. A documentation workspace cannot disable symbol extraction
only for `.md` files.

**Why deferred**: Medium priority. Requires extending `RulesEngine` with an
`ExtractionOverride` struct, adding `extraction_overrides` to `swl.rules.yaml`
schema, and a lookup in `ExtractContent()` per file extension. The infrastructure
is in place; the YAML schema needs extending.

**Design sketch**:
```yaml
extraction_overrides:
  - extensions: [".md", ".txt", ".rst"]
    extract_symbols: false
    extract_imports: false
    extract_sections: true
    extract_urls: true
```

---

## Deferred: Anchor Document Structured Content Extraction

`snapshot.go` stores the first non-heading paragraph as the anchor "description".
Structured extraction (goals, stated dependencies, section headings) would make
anchor content queryable.

**Why deferred**: Requires a new `anchorExtract{Description, Goals, Sections, KeyTerms}`
struct and parser in `snapshot.go`. No query handler currently consumes structured
anchor metadata — build the consumer before the producer.

---

## Deferred: WorkspaceProfile Entity

A `WorkspaceProfile` entity capturing overall project type (Go service, firmware,
documentation, etc.) would give LLMs at-a-glance project classification.

**Why deferred**: Requires Tier 3 LLM indexing (opt-in) to be meaningful. Out of
scope for constrained hardware targets (RPi 0 2W).

---

## Deferred: ToolMap Configurable via YAML

`inference.go`'s `toolMap` handles 8 standard picoclaw-native tools with complex
post-apply logic (content hashing, symbol/import extraction, depth bumping, edge
creation). Reducing this to a simple YAML schema loses the specialized post-apply
intelligence.

**Why deferred**: The Layer 0 `RegisterToolHandler()` escape hatch already handles
custom tools. Making the standard 8 configurable requires either a rich DSL (complex)
or accepting that YAML-registered tools get no post-apply intelligence (degraded).
Wait until a use case appears that cannot be served by Layer 0.

---

## Deferred: Edge Weights as DB Schema Column

Design documents proposed `ALTER TABLE edges ADD COLUMN weight REAL DEFAULT 1.0`.

**Why deferred**: A schema column freezes weights at edge creation time. Changing
`swl.rules.yaml` edge weights would not affect existing edges that can persist for
months. The correct approach is config-driven weights applied at query time via a
CASE expression, requiring no schema change. Implement as a query-time CASE when
a consumer that needs weight-ordered traversal is built.

---

## Deferred: `constraints` Table

Defined in `db.go`. Intended for named SQL consistency checks in the autonomous loop.
Table exists but no writer or reader has been wired.

**Why deferred**: The `query_gaps` feedback loop covers the most critical case
(missing intents). Constraint checking adds value only when entities are numerous
enough to have measurable quality variance. Revisit at >1000 entity deployments.

---

## Resolved (previously deferred, now done)

| Item | When resolved | What was done |
|------|--------------|---------------|
| `events` table empty | N+1 (2026-05-16) | `recordToolEvent` writes model_id + argsHash; `ExtractLLMResponse` writes `context_of` edges |
| `KnownRelContextOf` not written | N+1 (2026-05-16) | Written by `ExtractLLMResponse` for all extracted entities (Tasks, URLs, Files) |
| `KnownRelCoOccursWith` loop not wired | N+1 (2026-05-16) | `decay.go` runs co-occurrence query; upserts edges for entity pairs in ≥4 sessions |
| Intent/SubAgent write-only | D (2026-05-16) | `askSessionActivity` and `SessionResume` now read Intent and SubAgent via edge traversal |
| Assertion orphaning (phantom nodes) | D (2026-05-16) | Assertions moved to `metadata.assertions[]` on subject entity; no separate entity or edge |
| Per-model reliability profiling | N+1 (2026-05-16) | `assertionEntry.ModelID`, `sessionModels` map, `askModelReliability()`, `GET /api/swl/model-reliability` |
| DeriveAreaRelations | N+1 (2026-05-16) | `ontology.go` derives `depends_on` edges between SemanticAreas from import graph |
| DeriveSymbolUsage | N+1 (2026-05-16) | `ontology.go` derives `uses` edges (exported symbols, multi-segment paths only) from import graph |
| SSE edge delivery | D (2026-05-16) | Two-watermark approach; `links[]` in SSE delta payload; frontend deduplicates by composite key |
| Decay ordering probabilistic | N+1 (2026-05-16) | `ORDER BY confidence ASC, access_count ASC, last_checked ASC` — evidence-based, not random |
