# SWL Phase A Audit — 2026-05-06

## Purpose

Systematically verify what Phase A was supposed to deliver vs. what was actually
implemented. Every TODO, unresolved issue, and gap in the KG is evidence that Phase A
was vague. This document catalogs what was promised, what shipped, what was missed,
and what the consequences are.

---

## 1. What Phase A Was Supposed to Deliver

From `swl-refactor-plan.md` §7, Phase A had:

### 1.1 File Changes

| File | Promised Change | Status |
|------|----------------|--------|
| `snapshot.go` (new) | `BuildSnapshot(workspace)` → bounded semantic snapshot | ✅ Shipped |
| `scanner.go` | Replace per-file `ExtractContent()` in walk loop with `BuildSnapshot()` + structural only | ✅ Shipped |
| `inference.go` | Preserve postApply call sites; activate events table | ✅ Shipped |
| `session.go` | Augment `SessionResume()` to surface snapshot entities | ⚠️ Partial |
| `query.go` | Add Tier 1 intent patterns; "not yet read" notice for unread files | ⚠️ Partial |
| `entity.go` | Fix `access_count`: increment on **reads**, not writes | ❌ Wrong |
| `inference.go` | Activate `events` table INSERT | ✅ Shipped |
| `db.go` | Add `query_gaps` table | ✅ Shipped |

### 1.2 Verification Criteria

The plan defined 5 concrete checks:

```
1. Scan → entity count ≤ 300
2. query_swl {question:"what is this workspace for?"} → returns description from README
3. askFileDetail before read_file → "not yet read" notice
4. askFileDetail after read_file → returns symbols and content
5. query_swl {stats:true} → access_count on returned entities > 0
6. query_swl {snapshot:true} → structured semantic overview
```

**Findings:**
- Criterion 1: **Unclear** — the 300 limit is ambiguous. Is it the snapshot entities only
  (currently 43: 29 SemanticAreas + 14 AnchorDocuments — ✅ within limit), or the total
  graph (currently 3,300 — ❌ way over)? The plan does not specify.
- Criterion 2: ✅ Works — `askWorkspacePurpose()` queries AnchorDocument entities and
  returns descriptions.
- Criterion 3: ✅ Works — `askFileDetail()` checks `knowledge_depth <= 1` and returns the
  "structurally indexed" warning.
- Criterion 4: ✅ Works — after `read_file`, `postApplyReadFile` calls `ExtractContent`
  which bumps `knowledge_depth` to 3, then symbols/tasks are visible in `askFileDetail`.
- Criterion 5: ⚠️ Partially broken — see Finding #1 below.
- Criterion 6: ❌ Not implemented — no `snapshot` query mode exists in query.go.

---

## 2. Concrete Findings

### Finding 1 — `access_count` double-increment (Bug)

**Location:** `entity.go` + `entityWriter.upsertEntity()`

The `upsertEntitySQL` statement includes `access_count = access_count + 1` on every write.
The Phase A requirement was: *"access_count: increment on entity **reads** (returned by
query), not on upsert."*

The plan correctly called for `BumpAccessCount` to handle reads, and `BumpAccessCount`
IS called by query handlers (e.g. `askWorkspacePurpose`, `askFileDetail`). But the
upsert SQL also increments on every write, meaning:
- A `write_file` call upserts a File entity with `access_count + 1` — **incorrect**
- A `query_swl` call returns entities and calls `BumpAccessCount` — **correct**
- Net effect: writes inflate the count; the count no longer reflects read frequency

**Verification:** `query_swl {sql:"SELECT name, access_count FROM entities WHERE type='File' ORDER BY access_count DESC LIMIT 5"}`
```
cron/jobs.json     access_count=6    (read via query_swl)
state/state.json   access_count=5    (read via query_swl)
pkg/swl/manager.go access_count=5    (read via query_swl)
```

The counts are being bumped by read queries, but also being bumped by the upserts
themselves. The two contributions cannot be separated.

**Fix:** Remove `access_count = access_count + 1` from `upsertEntitySQL`. Keep it only
in `BumpAccessCount` (which is the correct path).

---

### Finding 2 — `query_swl {snapshot:true}` not implemented

**Location:** `query.go` — `Ask()` function and tier1Patterns

The verification criterion explicitly requires `query_swl {snapshot:true}` to return a
structured semantic overview. No such pattern exists in `tier1Patterns` and no handler
method `askSnapshot` exists.

**Current workaround:** `query_swl {question:"what is this workspace for?"}` returns
AnchorDocument descriptions and SemanticArea names via `askWorkspacePurpose` and
`askSemanticAreas`. This covers most of the snapshot intent, but the explicit
`snapshot` mode was promised and not delivered.

**Fix:** Add `snapshot` handler to `tier1Patterns` + `AskSnapshot()` method that returns
a combined structured view (anchors + areas + content profile).

---

### Finding 3 — SessionResume surfacing of snapshot is partial

**Location:** `session.go` — `SessionResume()`

`SessionResume` does surface AnchorDocument rows (via anchorRows query) and SemanticArea
rows (via areaRows query). This partially satisfies the Phase A requirement. However:

- The cold-start threshold (`nonSessionCount < 10`) is arbitrary and not validated
- The output format is ad-hoc strings, not structured data
- No `workspace_type` or `content_profile` summary from the snapshot is surfaced

---

### Finding 4 — `KnowledgeGaps` threshold is wrong

**Location:** `query.go` — `KnowledgeGaps()`

```go
WHERE (confidence < 0.85 OR fact_status = 'unknown') AND fact_status != 'deleted'
```

The threshold `< 0.85` includes:
- All `MethodInferred` entities (confidence = 0.8) — these are the LLM's inferred facts,
  the primary value of SWL
- All entities with `fact_status = 'unknown'` — default state for any upserted entity
  before first decay check

The query returns virtually every non-observed entity as a "gap," which is meaningless.
The KG confirms this: `KnowledgeGaps()` returns nothing because there are no entities
with confidence < 0.85 that aren't stale or deleted — but that's because the threshold
is so low it rarely fires. A workspace with heavy LLM inference activity (MethodInferred
entities) would show massive "gap" output that is actually useful knowledge.

**Fix:** Change threshold to `< 0.75` OR add `fact_status = 'unknown'` as a separate
category, not co-mingled with confidence. Alternatively, surface `unknown` status
entities as a distinct section ("entities needing verification").

---

### Finding 5 — Phase A scope was defined by changes, not outcomes

**Location:** `swl-refactor-plan.md` §7

Phase A was scoped as "fix extraction and query so the system works correctly" with
file-change checklist items. This is a build清单, not a definition of done. The plan
did not define:

- What "works correctly" means in observable terms
- What the entity count behavior should be (snapshot vs. total graph)
- What the query coverage requirements are (how many intent patterns, what accuracy)
- What the feedback signal requirements are (which decay loops run, which are stubs)

Consequently, what shipped was: "build snapshot, remove scan-time extraction,
add event recording" — and the verification was left implicit.

**Fix (for Phase B planning):** Define Phase B outcomes as measurable predicates, not
file-change lists.

---

### Finding 6 — No observable evidence Phase A improved anything

**Location:** KG current state

The KG contains:
- 3,300+ entities (mostly from lazy extraction during real tool use — not from snapshot)
- 29 SemanticAreas (from BuildSnapshot, correctly bounded)
- 14 AnchorDocuments (from BuildSnapshot, correctly bounded)
- 10 stale entities (mostly memory file Sections from the agent's own MEMORY.md)
- 0 query_gaps records (gap recording fires but no unanswered questions have been repeated 3+ times yet)

The snapshot is working — entity count from scan is bounded. But the graph is still
being populated by lazy extraction from tool calls (read_file, write_file, exec),
which means the 3,300 count reflects real usage, not scan bloat. The Phase A fix
(targeting scan-time bloat) worked, but the graph is still large because tool calls
continue to extract content. This is correct behavior — lazy extraction is the design.

The staleness in Section entities (MEMORY.md sections like "Quick Reference", "Build
Pipeline Knowledge") is expected — those files change frequently and decay correctly
marks them stale. The 2 stale File entities (cron/jobs.json, state/state.json) are
likely intentional state files that decay detects as changed.

---

### Finding 7 — Missing: explicit "confirm before read" for unknown files

**Location:** `query.go` — `askFileDetail()`

The Phase A plan mentioned: *"If knowledge_depth == 1, content has never been extracted.
... silent empty answers are eliminated."* The `askFileDetail` warning exists and works.

However, the plan also said the response should include a notice before suggesting the
agent read the file. Currently it just says "Read it with read_file to populate..." with
no mechanism to confirm this is what the agent wants. This is minor but was in scope.

---

## 3. Hardcoded Values Audit

### 3.1 snapshot.go — Hardcoded Constants

| Constant | Value | Should be configurable? | Phase B target |
|----------|-------|------------------------|----------------|
| `anchorNames` (set of 11 filenames) | `README`, `OVERVIEW`, etc. | Yes | `swl.rules.yaml` `anchor_patterns` |
| `manifestNames` (set of 13 filenames) | `go.mod`, `package.json`, etc. | Yes | Same |
| `snapshotMaxDepth` | 3 | Yes | `limits.max_semantic_areas` depth |
| `snapshotMaxAnchorBytes` | 8192 | Yes | `limits.max_anchor_doc_bytes` |
| `anchorH1Skip` + `paragraphLength` logic | inline in `extractDescription` | Yes | Config rule |

### 3.2 extractor.go — Hardcoded Limits and Patterns

| Item | Value | Should be configurable? | Phase B target |
|------|-------|--------------------------|----------------|
| `maxSymbols` | 60 | Yes | `limits.detail_budget_cold` |
| `maxImports` | 40 | Yes | Same |
| `maxTasks` | 30 | Yes | Same |
| `maxSections` | 20 | Yes | Same |
| `maxURLs` | 20 | Yes | Same |
| `maxTopics` | 10 | Yes | Same |
| `symPatterns` (10 regexes, 8 languages) | hardcoded | Yes | `file_rules[].extract.symbols.patterns` |
| `importPatterns` (7 regexes, 7 languages) | hardcoded | Yes | `file_rules[].extract.imports.patterns` |
| `noiseSymbols` | 4 names | Yes | `file_rules[].noise_symbols` |
| `noisyURLHosts` | inline array | Yes | `ignores.urls` |
| `projectTypeFiles` | 21 entries | Yes | `file_rules` auto-detect |
| `extTopics` | 23 extension→topic mappings | Yes | `file_rules` auto-detect |
| `isNoisyURL` | inline string contains check | Yes | `ignores.urls` |

### 3.3 scanner.go — Hardcoded Skip Lists

| Item | Value | Should be configurable? | Phase B target |
|------|-------|------------------------|----------------|
| `skipDirs` (9 entries) | `.git`, `node_modules`, etc. | Yes | `ignores.dirs` |
| `skipExts` (21 extensions) | `.png`, `.pdf`, etc. | Yes | `ignores.extensions` |

### 3.4 query.go — Hardcoded Patterns and Handlers

| Item | Count | Should be configurable? | Phase B target |
|------|-------|-------------------------|----------------|
| `tier1Patterns` (intent → handler) | 22 patterns | Yes | `swl.query.yaml` `intents` |
| `sqlTemplates` (named SQL queries) | 3 templates | Yes | `swl.query.yaml` `templates` |
| `label_weights` | None — not implemented | Yes | `swl.query.yaml` `label_weights` |
| `tryTier3` term cap | 3 terms max | Yes | Config |
| `stop words` for tier3Terms | 15 words hardcoded | Yes | Config |

### 3.5 inference.go — Hardcoded Tool Map

| Item | Count | Should be configurable? | Phase B target |
|------|-------|-------------------------|----------------|
| `toolMap` (toolName → declRule) | 8 tools | Yes | `swl.rules.yaml` `tool_rules` |
| `postApply` functions | 8 functions | Partial (Layer 0 escape hatch exists) | Partially externalizable |
| Custom handlers via `RegisterToolHandler` | Layer 0 override exists | ✅ Already externalizable | N/A |

### 3.6 decay.go — Hardcoded Thresholds

| Item | Value | Should be configurable? | Phase B target |
|------|-------|-------------------------|----------------|
| Decay probability | 5% | Yes | `feedback_thresholds.decay_probability` |
| Age threshold for decay candidates | 24 hours | Yes | `feedback_thresholds.decay_age_hours` |
| Entities checked per decay call | 2 | Yes | `feedback_thresholds.decay_batch_size` |
| Prune threshold | 10,000 events | Yes | `feedback_thresholds.prune_threshold` |
| Prune cutoff | 30 days | Yes | `feedback_thresholds.prune_cutoff_days` |

---

## 4. Phase B Risks If Not Addressed

1. **Externalizing the hardcoded values without understanding current behavior** —
   There are 80+ hardcoded values across 6 files. Blindly externalizing them without
   behavioral verification risks subtle breakage. Each group needs a behavioral test
   before externalization.

2. **Confidence averaging may not be the right merge strategy** — The current upsert
   logic averages confidence when methods match. This is mathematically sound but may
   not reflect actual trust: two MethodObserved observations of the same fact should
   reinforce each other (average toward 1.0), not average to 0.5. The current logic
   does MAX for different methods and average for same methods, which is correct.

3. **The 3-tier query engine is coupled to hardcoded patterns** — Phase B needs to
   extract patterns from `swl.query.yaml` and dispatch to the same handler methods.
   The handlers are well-structured; the coupling is only in the `Ask()` dispatch
   loop. This is manageable but needs careful refactoring.

4. **Tier 2 (SQL templates) has no externalization path** — The SQL templates in
   `sqlTemplates` map are not described in the plan. They need to be added to
   `swl.query.yaml` as named queries with parameter extraction.

---

## 5. Summary Scores

| Dimension | Status | Notes |
|-----------|--------|-------|
| BuildSnapshot delivered | ✅ Complete | 43 snapshot entities, bounded |
| Scan-time extraction removed | ✅ Complete | Walk loop no longer calls ExtractContent |
| Lazy extraction on tool call | ✅ Complete | postApplyReadFile/WriteFile trigger extraction |
| events table activated | ✅ Complete | recordToolEvent fires on every PostHook |
| query_gaps table created | ✅ Complete | recordQueryGap called on unmatched queries |
| access_count fix (reads not writes) | ❌ Bug | Double-increment on writes |
| snapshot query mode | ❌ Missing | Not implemented |
| Tier 1 intent patterns | ✅ Complete | 22 patterns covering major query types |
| "not yet read" notice | ✅ Complete | askFileDetail shows warning for depth ≤ 1 |
| Semantic labels | ⏳ Partial | metadata used for some queries but no label weights |
| Hardcoded values externalized | ❌ Not done | 80+ values across 6 files — all Phase B scope |
| Behavioral tests / verification | ❌ Not done | No automated verification of Phase A criteria |

---

## 6. Recommended Actions Before Proceeding to Phase B

1. **Fix access_count bug** — remove `access_count + 1` from upsertEntitySQL; keep it only in BumpAccessCount
2. **Implement `snapshot` query mode** — add to tier1Patterns + AskSnapshot() handler
3. **Fix KnowledgeGaps threshold** — separate unknown status from confidence threshold
4. **Add behavioral verification tests** — at minimum, the 6 criteria from §1.2 should be testable
5. **Resolve the 300 entity limit ambiguity** — document whether it applies to snapshot only or total graph
6. **Phase B audit** — read every file listed in §3, produce a spec document before writing rules.go and query_engine.go

---

*Generated from source audit of: snapshot.go, scanner.go, inference.go, extractor.go, query.go, session.go, decay.go, manager.go, entity.go, types.go, db.go + KG live state query.*