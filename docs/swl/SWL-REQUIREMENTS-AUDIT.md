# SWL Refactor Plan — Requirements Audit

> Date: 2026-05-06
> Purpose: Re-examine the refactor plan against the actual requirements, not implementation

---

## 1. The Requirements (what we are testing against)

From `swl-refactor-requirements.md` (verbatim):

> **Primary intent:** Create a quick win refactoring solution, preserving the strengths, and fixing the drifts, wrong design decisions, and gaps. Starting with the most critical aspects, which is it's use.

> **Problem statement:** "The indexing / scanning process now prepoulates the huge amount of data, that will likely never be used, where it was required to provide immediate intelligence for SWL to start working in the codebase."

> **Goal:** "ontological, semantic intelligence even on constrained hardware (i.e rpi02w), so that when LLM asks the SWL it will actually be able to answer questions (such as—only as illustrative examples from many—'where is the file that does X', 'what is the project Y goal' etc etc etc.)"

> **Conceptual solution:** "a configuration based generic rule based extractor, which would be adaptable and capable of self learning and self-improvement, and with swl configuration file would be able to cover any languages, ignore patterns, rules, logics, semantic logic, ontology rules. Mainly however, it would shift from current 'hardcoded' and presumed intelligence to the tiered extensible configurable semantic intelligence extraction **defined by what the LLM would actually need, ask, and work with**."

> **Addendum:** "design should understand the reality of how different LLMs can behave. it should systematically cover the understanding that different agents/LLM models working in same workspace might have different tentions and keen to different drifts and halucinations and actions, and outcomes."

---

## 2. Requirement Decomposition

The requirements contain 5 distinct claims. Each claim must be tested against the plan:

| # | Claim | From requirement |
|---|-------|-----------------|
| R1 | SWL provides immediate intelligence at boot, without LLM having to read files first | Primary problem statement |
| R2 | SWL answers questions like "where is the file that does X" and "what is the project goal" | Goal statement |
| R3 | Extractor is generic (any language), configurable (YAML), and adaptable | Conceptual solution |
| R4 | Extractor is self-learning and self-improving | Conceptual solution |
| R5 | Design accounts for different LLM/agent behaviors and drifts | Addendum |

---

## 3. Test: Does the Plan Satisfy Each Requirement?

### R1 — Immediate Intelligence at Boot

**What the requirement says:** SWL provides immediate intelligence at boot. The LLM can ask meaningful questions the moment SWL boots without having to read files first.

**What Phase A delivers:**
- `BuildSnapshot` produces ~100-300 entities (SemanticAreas, AnchorDocuments, directory structure)
- The snapshot is indexed during `ScanWorkspace`
- `SessionResume` surfaces anchor documents and semantic areas in the digest

**What is missing:**

The snapshot produces entities but does NOT produce meaningful labels. Consider these questions an LLM might ask at boot:

- "Where is the authentication logic?" → requires `role: "authentication"` label on files
- "Where is the database layer?" → requires `domain: "data-access"` label on files  
- "What is the entry point?" → requires `kind: "entry-point"` label on files

The snapshot does NOT attach semantic role labels to files. It only produces:
- AnchorDocument entities (readme/overview files and their descriptions)
- SemanticArea entities (directories with anchor docs or strong content profile)
- File entities (raw, unlabeled)

The query engine (`askFileDetail`, `askWorkspacePurpose`) can answer "what is this workspace for?" — but cannot answer "where is the file that handles X?" because no role/domain labels exist on File entities.

**Claim R1 verdict: PARTIAL** — SWL boots with structural understanding (what files/dirs exist, what the README says) but NOT semantic understanding (what each file does). The gap between "structural" and "semantic" is exactly what the user described in the original problem.

---

### R2 — Answer "where is the file that does X" and "what is the project goal"

**What the requirement says:** SWL answers questions like "where is the file that does X" and "what is the project goal".

**What Phase A/B deliver:**
- "what is the project goal?" → ✅ `askWorkspacePurpose()` returns descriptions from AnchorDocuments (README/go.mod etc.)
- "where is the file that does X?" → ❌ NO mechanism exists

**Why "where is the file that does X" fails:**

The question requires:
1. The LLM to express intent about a purpose (e.g., "authentication", "data persistence", "API routing")
2. SWL to map that purpose to a specific file

Current implementation:
- `askFileDetail("what does pkg/auth/middleware.go do")` → works if the file has been read
- `askWorkspacePurpose()` → returns project-level description from README
- `askSemanticAreas()` → returns directory names with content_type metadata
- NO handler for: "find the file that handles authentication"

The `swl.query.yaml` schema in the plan includes a `find_by_purpose` intent pattern:

```yaml
- id: find_by_purpose
  patterns:
    - "where is (?:the )?(?P<type>\\w+) that (?:does|handles|implements|manages) (?P<purpose>.+)"
    - "find (?P<type>\\w+s?) (?:for|that) (?P<purpose>.+)"
  handler: label_search
  search_on: [metadata.role, metadata.domain, metadata.description, name, path]
```

This handler does not exist in the current `query.go` implementation. It was specified in the plan's YAML schema but never built.

**Claim R2 verdict: PARTIAL** — "what is the project goal" works. "where is the file that does X" does not. The plan specifies the handler in YAML schema but does not implement it in code.

---

### R3 — Generic, Configurable, Adaptable Extractor

**What the requirement says:** A config-based rule extractor covering any languages, ignore patterns, rules, logics, semantic logic, ontology rules. Tiered, extensible, driven by what the LLM actually needs.

**What Phase A delivers:** Phase A does NOT externalize anything. It builds `BuildSnapshot` and lazy extraction — both hardcoded.

**What Phase B promises:** Externalization to `swl.rules.yaml` and `swl.query.yaml`. Rules engine, query engine, embedded defaults.

**What Phase B actually does:** Moves hardcoded values from Go code to YAML files. The intelligence doesn't change — it relocates.

**The gap:** The plan does not address *"driven by what the LLM actually needs"*. The extraction rules are static — they do not adapt based on what the LLM queries, asserts, or ignores. The feedback loop (Phase C) adjusts entity weights but does not modify extraction rules.

**Example:** If an LLM repeatedly asks about "environment variables" but SWL never extracts `env` variable references from files, there is no mechanism for the system to notice this and suggest a new extraction rule. The current loop only marks existing entities as verified/stale — it does not discover missing entity types or patterns.

**Claim R3 verdict: PARTIAL** — Generic and configurable via YAML (Phase B). Not yet adaptable in the sense of learning what the LLM needs. Tiered — yes (Tier 0-3 defined).

---

### R4 — Self-Learning and Self-Improving

**What the requirement says:** "capable of self learning and self-improvement".

**What Phase C delivers:** The feedback loop adjusts:
- Entity confidence based on cross-agent confirmation
- Label promotion when ≥3 agents agree
- Label demotion when contradictions detected
- Rule removal after 200 firings with useful_ratio < 0.05

**What is missing:** The loop only adjusts weights on existing entities and rules. It does not:

1. **Discover new patterns** — If the LLM repeatedly mentions a concept that doesn't exist in the graph (e.g., "OAuth flow"), the system records a gap but does not generate a candidate extraction rule.
2. **Learn from query patterns** — If `find_by_purpose` queries consistently return zero results, the system should surface this as a gap and suggest adding role/domain labels to relevant files.
3. **Adapt extraction depth** — If the LLM reads the same file 10 times, should extraction depth increase? The current system does not track re-read frequency as a signal.
4. **Handle the "cold start" problem for new workspaces** — A fresh workspace has 0 labels, 0 verified facts. How does SWL bootstrap semantic understanding from zero? Phase A's snapshot provides structure but not semantics (see R1 gap).

**Claim R4 verdict: WEAK** — The feedback loop exists but is limited to weight adjustment on existing entities. True self-learning would require the system to generate candidate rules from observed patterns. The plan explicitly defers this ("SWL surfaces gaps; a human or the LLM writes new rules") — which is a conscious design decision, not a missing feature. But the user's requirement says "self learning and self-improvement", which this is not.

---

### R5 — Account for Different LLM/Agent Behaviors and Drifts

**What the requirement says:** "design should understand the reality of how different LLMs can behave. it should systematically cover the understanding that different agents/LLM models working in same workspace might have different tentions and keen to different drifts and halucinations and actions, and outcomes."

**What the plan delivers:** Multi-agent convergence signal (≥3 agents confirm → verified). Per-agent session tracking in the events table.

**What is missing:**

1. **Per-agent reliability weighting** — The plan acknowledges this in "Next Iteration Remarks": "per-agent reliability profiles — accumulate assertion confirmation/contradiction evidence to build per-model confidence weights." It is deferred, not implemented.

2. **Behavioral drift detection** — If Agent A consistently asserts X and Agent B consistently contradicts X, this is a behavioral drift pattern. The current events table records tool calls but does not analyze behavioral sequences across sessions.

3. **Different extraction effectiveness per agent** — Some agents read files carefully and extract rich metadata. Others skip directly to exec and produce sparse graphs. The system does not track per-agent extraction quality.

4. **Query pattern variation by agent type** — Different LLMs ask questions differently. A Claude-style agent might phrase questions differently from a GPT-style agent. The current Tier 1 patterns are hardcoded for English. If a Japanese LLM uses Japanese queries, they won't match.

**Claim R5 verdict: NOT ADDRESSED** — Multi-agent convergence exists as a theoretical signal but no implementation produces it. The plan defers to "next iteration."

---

## 4. Cross-Cutting Gaps

### Gap G1 — Semantic Labels Are Not Extracted

The plan's central concept is that semantic labels (role, domain, content_type) bridge extraction and query. But Phase A does not extract labels — it only extracts structural entities (File, Directory, Symbol) and snapshot-level entities (SemanticArea, AnchorDocument).

**Example:** For a file `pkg/auth/middleware.go`, the system creates:
- Entity: `File`, name: `pkg/auth/middleware.go`
- Edge: `in_dir` → `Directory(pkg/auth)`

The system does NOT create:
- `role: authentication` in metadata
- `domain: security` in metadata
- `content_type: go` in metadata (this IS created by `extractor.go` via `extTopics`)

**Consequence:** The `label_search` handler specified in the plan's `swl.query.yaml` has nothing to search. "where is the file that handles authentication?" → no results.

**Fix needed:** Phase B must extract semantic labels as a Tier 1 inference step, not just store structural entities.

### Gap G2 — Tier 1 (Ontological Inference) Is Not Implemented

The plan specifies 4 extraction tiers:
- Tier 0: Structural/derivative (scan-time)
- Tier 1: Ontological inference (SQL-only, from existing graph facts)
- Tier 2: Passive LLM capture (AfterLLM hook)
- Tier 3: Active LLM indexing (off by default)

**Current implementation:**
- Tier 0: ✅ BuildSnapshot (structural)
- Tier 2: ✅ ExtractLLMResponse (passive capture)
- Tier 3: ✅ Configurable (off by default)

**Tier 1 is missing entirely.** There is no SQL-based ontological inference layer. The plan says: "rule-driven derivations from existing graph facts — 'if entity A has label X and relates to entity B, derive label Y on B'."

This is exactly what Gap G1 needs. Without Tier 1, the system has no way to derive that `pkg/auth/` contains "authentication" files — it must be manually labeled or the LLM must read the files first.

### Gap G3 — The "Cold Start" Problem Is Understated

Phase A's BuildSnapshot produces structural entities (files, directories, anchor docs). But the snapshot is bounded to ~100-300 entities and contains no semantic labels. For a new workspace with no prior knowledge, the LLM boots into a graph that:
- Knows which files/directories exist
- Knows what the README says
- Does NOT know what any file does

The gap between "structural knowledge" and "semantic knowledge" is large. The plan does not specify how this gap is bridged for a cold-start workspace.

### Gap G4 — Phase A Is Not the "Quick Win" the User Wanted

The user said: "Starting with the most critical aspects, which is it's use."

Phase A focused on fixing scan-time bloat (correct) and adding events table (correct). But it did NOT deliver R1 (immediate semantic intelligence) or R2 ("where is the file that does X"). The most critical aspect — the actual usability of SWL for answering real questions — was not addressed in Phase A.

---

## 5. Revised Recommendations

### For the Plan

The plan is well-structured for a long-term refactor but has these gaps relative to the requirements:

1. **Tier 1 (Ontological Inference) must be added to Phase B scope** — Without it, semantic labels never appear on entities. Without labels, label_search doesn't work. Without label_search, "where is the file that does X?" never works.

2. **Semantic label extraction must be a first-class Phase B deliverable** — Not just the rules engine and query engine. The actual extraction of role/domain labels from file content and directory context.

3. **Phase A should have addressed immediate semantic intelligence** — BuildSnapshot should produce labeled entities, not just structural ones. A file under `pkg/auth/` should have `role: authentication` derived from its path pattern.

4. **Phase C needs a "gap → candidate rule" signal** — Currently, gaps only surface to the user. The feedback loop should be able to propose candidate extraction rules based on repeated unanswered queries.

### Suggested Phase Sequence (Revised)

| Phase | Focus | Deliverable |
|-------|-------|-------------|
| **A** | Scan-time bloat fix | BuildSnapshot, lazy extraction, events table — ✅ Done |
| **A.2** (new) | Semantic bootstrap | Path-pattern → label derivation, Tier 1 inference, semantic labels on entities |
| **A.3** (new) | Query capability | label_search handler, find_by_purpose queries working |
| **B** | Externalization | swl.rules.yaml, swl.query.yaml, rules engine, query engine |
| **C** | Feedback loop | Self-improvement, cross-agent convergence, gap → rule generation |

### For Phase A.2 Specifics

To bootstrap semantic labels without LLM calls:

1. **Path pattern → label derivation** (Tier 1):
   - `pkg/auth/` → `role: authentication`
   - `pkg/db/` → `domain: data-access`
   - `pkg/api/` → `role: api`, `domain: networking`
   - `cmd/` → `kind: entry-point`
   - `internal/` → `visibility: internal`

2. **File naming → label derivation** (Tier 1):
   - `*_test.go` → `kind: test`
   - `*_mock.go` → `kind: mock`
   - `*.sql` → `content_type: sql`
   - `Makefile` → `kind: build-target`
   - `docker-compose*.yml` → `kind: infrastructure`

3. **Directory role inference** (Tier 1):
   - Directory containing files with `authentication` in path → `role: authentication`
   - Directory containing files with `auth`, `login`, `oauth`, `jwt` in names → `role: authentication`
   - Directory with dominant `.sql` extension → `domain: data-access`

This produces labeled entities from structural signals alone — no LLM needed.

---

*Generated: Requirements audit against the refactor plan, identifying what's missing vs. what the user actually needs.*