# SWL Refactor Plan v4 — Requirements-Grounded

> Status: **DRAFT** | Date: 2026-05-07
> Replaces: SWL-REFACTOR.md (v3), SWL-PHASE1-AUDIT.md
> Grounded in: `swl-refactor-requirements.md` (verbatim requirements)
> Supersedes prior plans that audited against implementation rather than requirements

---

## What Changed and Why

The previous plans audited against implementation ("was Phase A built?") instead of against requirements ("does the system answer the questions it should?"). This created false confidence: Phase A built infrastructure, but the most critical aspect — **SWL actually answering questions** — was never addressed.

This plan is rebuilt from the requirements upward. The sequence is determined by what must be true for SWL to be useful, not by what was easy to build.

---

## The Five Requirements (source of truth)

From `swl-refactor-requirements.md`:

| # | Requirement | Verbatim key phrase |
|---|-------------|-------------------|
| R1 | Immediate intelligence at boot | "provide immediate intelligence for SWL to start working in the codebase" |
| R2 | Answer real questions | "where is the file that does X", "what is the project Y goal" |
| R3 | Generic, configurable, adaptable | "configuration based generic rule based extractor...cover any languages, ignore patterns, rules, logics, semantic logic" |
| R4 | Self-improving | "self learning and self-improvement" |
| R5 | Multi-agent aware | "different agents/LLM models working in same workspace might have different tentions...drifts and halucinations" |

---

## Root Cause: The Cold-Start Semantic Gap

The system currently has a cold-start gap that Phase A did not address:

```
What Phase A produces:
  File: pkg/auth/middleware.go  (structural, unlabeled)
  Directory: pkg/auth/          (structural, unlabeled)
  AnchorDocument: README.md     (has description)
  SemanticArea: pkg/auth/       (has content_type)

What is MISSING:
  File: pkg/auth/middleware.go → role: authentication    ← NO LABEL
  File: pkg/auth/middleware.go → domain: security        ← NO LABEL
  Directory: pkg/auth/          → role: authentication   ← NO LABEL
```

Without semantic labels on File entities, the query engine cannot answer: *"where is the file that handles authentication?"*

This is the core gap. Everything else — YAML externalization, feedback loop, per-agent profiles — depends on having labels to operate on.

**The fix is not adding more code. The fix is deriving labels from structural signals that already exist.**

---

## Revised Phase Sequence

### Phase A — ✅ Done
Scan-time bloat fix. BuildSnapshot, lazy extraction, events table, gap recording.

**What it delivers:** Structural understanding. The graph knows what files exist and what README says. The LLM boots with structure, not semantics.

---

### Phase A.2 — Semantic Bootstrap (NEW — HIGHEST PRIORITY)

**What it must deliver:** Immediate semantic intelligence at boot without LLM calls.

**What the requirement says:** "provide immediate intelligence for SWL to start working in the codebase... even on constrained hardware."

**How it works:** Path pattern → semantic label derivation (Tier 1 ontological inference).

The concept: workspace structure already encodes semantic meaning. `pkg/auth/` means authentication. `cmd/` means entry points. `*_test.go` means tests. This meaning can be derived from structural signals — no LLM needed.

**Implementation:**

In `scanner.go`, after upserting a File entity, derive labels from:

| Signal | Derivation | Result |
|--------|-----------|--------|
| Path prefix | `pkg/auth/` → `role: authentication` | Every file in that dir gets the label |
| Path prefix | `pkg/db/`, `pkg/data/` → `domain: data-access` | Data layer files |
| Path prefix | `pkg/api/`, `pkg/http/` → `domain: networking` | API layer files |
| Path prefix | `cmd/` → `kind: entry-point` | CLI entry points |
| Path prefix | `internal/` → `visibility: internal` | Internal packages |
| File name suffix | `*_test.go` → `kind: test` | Test files |
| File name suffix | `*_mock.go`, `*_fake.go` → `kind: mock` | Mock implementations |
| File name pattern | `middleware*.go` → `role: middleware` | Middleware files |
| File name pattern | `config*.go`, `config*.yaml` → `role: configuration` | Config files |
| Directory with anchor doc | `pkg/foo/README.md` → `role: documented` | Well-documented areas |
| Dominant extension | `*.sql` files → `content_type: sql` | Database code |
| Dominant extension | `*.tf` files → `content_type: hcl` | Terraform IaC |
| Dominant extension | `*.proto` → `content_type: protobuf` | Protocol buffers |

This is SQL-only derivation — runs at scan time, costs nothing, produces labeled entities immediately.

**Behavioral changes:**
- Every File entity in `pkg/auth/` gains `role: authentication` in metadata
- Every File entity in `cmd/` gains `kind: entry-point` in metadata
- SemanticArea entities gain `role` and `domain` labels derived from their contained files
- The graph is semantically meaningful at boot — no LLM reading required

**Files to modify:**
- `pkg/swl/scanner.go` — add label derivation step after File entity upsert
- `pkg/swl/types.go` — add KnownLabel* constants if needed
- `pkg/swl/inference.go` — document Tier 1 inference is active here (not in a separate file)

**Verification:**
- `query_swl {question:"where is authentication code"}` → returns files from `pkg/auth/` directories
- `query_swl {question:"what is the entry point"}` → returns files from `cmd/`
- KG: File entities have `role` and `domain` in metadata at boot

**Scope:** Label derivation only. No YAML externalization yet. No new tables. No query engine changes.

---

### Phase A.3 — Query Capability (NEW — HIGH PRIORITY)

**What it must deliver:** Answer "where is the file that does X" and "what is the project goal."

**What the requirement says:** "where is the file that does X", "what is the project Y goal."

**How it works:** Add `label_search` handler to the query engine.

**Implementation:**

1. **Add Tier 1 intent patterns to `query.go`:**

```go
// find_by_purpose: "where is the authentication code?"
{regexp.MustCompile(`(?i)where\s+(?:is|are)\s+(?:the\s+)?(.+?)\s+(?:that\s+)?(?:does|handles|implements|for|logic)`), 
  func(m *Manager, hint string) string { return m.labelSearch(hint) }},

// find_by_kind: "where are the test files?"
{regexp.MustCompile(`(?i)where\s+(?:are|is)\s+(?:the\s+)?(.+?)\s+(files?|tests?)`),
  func(m *Manager, hint string) string { return m.labelSearch(hint) }},
```

2. **Implement `labelSearch()` in `query.go`:**

```go
// labelSearch finds files matching role/domain/kind labels.
// Searches metadata['role'], metadata['domain'], metadata['kind'], 
// metadata['content_type'] fields. Returns top results with scores.
func (m *Manager) labelSearch(query string) string { ... }
```

The handler:
- Extracts search terms from the query
- Matches against File entity metadata (role, domain, kind, content_type)
- Scores by label match weight (exact match > partial match)
- Returns ranked results with labels shown
- Falls back to directory name search if no label match

3. **Ensure `askWorkspacePurpose()` is working** (it already is — AnchorDocument descriptions returned)

**Behavioral changes:**
- "where is the authentication logic?" → returns `pkg/auth/` files with `role: authentication`
- "what is the project goal?" → returns AnchorDocument descriptions
- "what does auth/middleware.go do?" → returns file detail (already working)

**Files to modify:**
- `pkg/swl/query.go` — add labelSearch handler and Tier 1 intent patterns

**Verification:**
- `query_swl {question:"where is authentication code"}` → labeled results
- `query_swl {question:"where are the entry points"}` → `cmd/` files
- `query_swl {question:"what is this workspace for"}` → anchor doc descriptions

---

### Phase B — Externalization (REVISED SCOPE)

**What it must deliver:** All hardcoded patterns, rules, and query intents moved to YAML config files.

**What the requirement says:** "swl configuration file would be able to cover any languages, ignore patterns, rules, logics, semantic logic, ontology rules."

**What changes relative to prior Phase B plan:**

Phase B now MUST include the semantic bootstrap rules (Phase A.2) as configurable patterns in `swl.rules.yaml`. The patterns are not just "what to skip" — they are "how to derive semantic meaning from structure."

**New files:**

| File | Purpose |
|------|---------|
| `pkg/swl/rules.go` | Rules engine: loads `swl.rules.yaml`, deep-merges overrides |
| `pkg/swl/rules_default.yaml` | Embedded defaults (path→label mappings from Phase A.2) |
| `pkg/swl/query_engine.go` | Intent dispatcher consuming `swl.query.yaml` |
| `pkg/swl/swl_query_default.yaml` | Embedded query patterns (label_search + Phase A.3 intents) |

**`swl.rules.yaml` structure (key additions):**

```yaml
# Semantic label derivation rules
label_rules:
  path_prefixes:
    - prefix: "pkg/auth/"
      role: authentication
      domain: security
    - prefix: "pkg/db/"
      domain: data-access
    - prefix: "cmd/"
      kind: entry-point
    - prefix: "internal/"
      visibility: internal

  name_patterns:
    - pattern: ".*_test\\.go$"
      kind: test
    - pattern: ".*_mock\\.go$"
      kind: mock
    - pattern: "middleware.*\\.go$"
      role: middleware

  content_types:
    - extension: ".sql"
      content_type: sql
    - extension: ".tf"
      content_type: hcl
    - extension: ".proto"
      content_type: protobuf

# Extraction rules
file_rules:
  symbols:
    max_per_file: 60
    patterns: [go-func-pattern, js-func-pattern, ...]
  ignores:
    dirs: [".git", "node_modules", ...]
    extensions: [".png", ".pdf", ...]

# Query intent patterns
intents:
  - id: find_by_purpose
    patterns: [...]
    handler: label_search
    search_on: [metadata.role, metadata.domain, metadata.kind]
```

**Files to modify:**
- `pkg/swl/extractor.go` — consume `file_rules` from rules engine
- `pkg/swl/scanner.go` — consume `label_rules` from rules engine
- `pkg/swl/query.go` — consume `intents` from query engine
- `pkg/swl/inference.go` — consume `tool_rules` from rules engine
- `pkg/config/swl.go` — add `RulesPath`, `QueryPath` fields

**Verification:**
- Workspace with `.tf` files → label `content_type: hcl` derived from extension rule
- Workspace with `srv/` directory → `role: service` derived from path prefix rule
- Add custom `role: database` for `pkg/data/` → label applied to all files in that dir
- Intent pattern `find_by_purpose` dispatched through YAML config

---

### Phase C — Feedback Loop (REVISED SCOPE)

**What it must deliver:** Self-improvement through workspace action signals. Gap → candidate rule generation.

**What the requirement says:** "self learning and self-improvement...different agents/LLM models...different tentions and keen to different drifts."

**What changes relative to prior Phase C plan:**

1. **Gap → candidate rule generation** (new, critical):
   - If `query_swl {question:"where is X"}` returns 0 results ≥3 times
   - SWL surfaces the gap AND proposes a candidate `label_rule` suggestion
   - Example: Query "where is the billing logic" returns nothing 3 times
   - SWL suggests: "No files labeled 'billing'. Consider adding: `{prefix: 'pkg/billing/', role: billing}`"
   - This closes the loop: missing knowledge → candidate rule → user/LLM approves → rule active

2. **Cross-agent convergence** (existing, needs implementation):
   - Track which sessions/agents assert which facts
   - When ≥3 distinct agents assert the same label for the same entity → promote to `verified`
   - When ≥2 agents contradict → flag as conflict in gaps

**Files to create/modify:**
- `pkg/swl/feedback.go` — feedback loop (existing plan)
- `pkg/swl/entity.go` — label confidence per agent (new: track agent_id on assertions)
- `pkg/swl/db.go` — `agent_stats` table (existing plan)
- `pkg/swl/query.go` — gap → candidate rule suggestion (new in handler)

**Verification:**
- Repeated unanswered query → gap + candidate rule suggestion
- Three agents asserting same role on same file → label promoted
- Two agents asserting different roles on same file → conflict surfaced

---

## Phase Priority Rationale

```
Phase A.2 (Semantic Bootstrap)  ← MUST come first
Phase A.3 (Query Capability)    ← MUST come second  
Phase B (Externalization)       ← Infrastructure for maintainability
Phase C (Feedback Loop)         ← Self-improvement after foundation is solid
```

**Why A.2 before B:** YAML externalization (Phase B) externalizes the patterns that Phase A.2 discovers. You cannot externalize what you haven't first proven works in hardcoded form.

**Why A.3 before B:** The label_search handler needs to exist before it can be configured via YAML. Build it first, make it work, then make it configurable.

**Why A.2/A.3 before C:** The feedback loop adjusts entity weights and surfaces gaps. It works best when entities already have labels to adjust. Running the feedback loop on unlabeled entities produces no useful signal.

---

## Verification Criteria by Phase

### Phase A.2
| Criterion | Method |
|-----------|--------|
| File in `pkg/auth/` has `role: authentication` | `query_swl {sql:"SELECT metadata FROM entities WHERE name LIKE '%auth%' AND type='File' LIMIT 1"}` |
| File in `cmd/` has `kind: entry-point` | `query_swl {sql:"SELECT metadata FROM entities WHERE name LIKE 'cmd/%' LIMIT 1"}` |
| SemanticArea `pkg/` has derived `role` | `query_swl {sql:"SELECT metadata FROM entities WHERE type='SemanticArea' AND name='pkg'"}` |
| Derivation costs nothing at scan time | Scan stats show no LLM calls; profiler shows <10ms overhead |
| Works on new workspace without prior reads | Cold workspace boots with labeled entities |

### Phase A.3
| Criterion | Method |
|-----------|--------|
| "where is authentication code" → labeled results | `query_swl {question:"where is authentication code"}` |
| "what is this workspace for" → anchor descriptions | `query_swl {question:"what is this workspace for"}` |
| "what does auth/middleware.go do" → file detail | `query_swl {question:"what does pkg/auth/middleware.go do"}` |
| Query returns within 200ms | Timing measurement on KG with 3,000+ entities |
| No false positives on "where is X" queries | Test with queries for non-existent concepts |

### Phase B
| Criterion | Method |
|-----------|--------|
| Entity diff: Phase A.3 vs Phase B → identical output | Automated diff test |
| Add `.tf` file → HCL label applied | Functional test |
| Custom `pkg/billing/` → `role: billing` applied | Functional test |
| Add intent pattern via YAML → dispatched | Functional test |
| Workspace override deep-merges with defaults | Test with partial override |

### Phase C
| Criterion | Method |
|-----------|--------|
| Repeated unanswered query → gap + rule suggestion | Functional test |
| 3 agents assert same label → verified | Multi-session test |
| 2 agents assert different labels → conflict in gaps | Multi-session test |
| Rule removed after 200 firings with <5% useful ratio | Load test with synthetic queries |

---

## What Phase A Got Right (Keep)

- Lazy extraction on tool call (don't extract symbols at scan time)
- BuildSnapshot (bounded semantic snapshot at scan time)
- Events table recording
- mtime-based incremental scan
- Directory entity creation with parent edges
- Session management with hint injection

These are correct infrastructure decisions. Phase A.2/A.3 build semantic capability on top of this infrastructure.

---

## What Phase A Got Wrong (Fix)

| Issue | Fix |
|-------|-----|
| `access_count` double-incremented on writes AND reads | Remove `access_count + 1` from `upsertEntitySQL`; keep only in `BumpAccessCount` |
| `query_swl {snapshot:true}` not implemented | Add `snapshot` mode or document it as `askSemanticAreas` + `askAnchorDocuments` |
| `KnowledgeGaps` threshold `< 0.85` captures inferred entities as gaps | Change threshold to `< 0.75`; separate `unknown` status into distinct category |
| Tier 1 (Ontological Inference) never built | Build in Phase A.2 as path→label derivation |
| `find_by_purpose` handler never built | Build in Phase A.3 as `labelSearch` |

---

## Document Changelog

| Date | Change |
|------|--------|
| 2026-05-07 | v4: Rebuilt from requirements upward. Added Phase A.2 (Semantic Bootstrap) and A.3 (Query Capability) as higher priority than YAML externalization. Fixed priority ordering. |
