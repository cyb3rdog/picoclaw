# SWL Agent Handoff

> Written at session close for the next Claude session.
> Read this before CLAUDE.md. Read CLAUDE.md before touching any code.
> Last updated: 2026-05-16. Branch: `claude/merge-swl-fixes-assessment-Y67DP`.

---

## What You Are Continuing

SWL (Semantic Workspace Layer) — a generic, universal, adaptable, self-improving knowledge graph for LLM agents. It provides verified workspace facts, prevents hallucinations and drift, runs on constrained hardware (RPi 0 2W). The verbatim requirement is in `docs/swl/swl-refactor-requirements.md`. Read it. It is the north star.

Current implementation is complete through phase N+2. See `docs/swl/ROADMAP.md` Resolved section for the full list.

---

## Current State — What Is Done and Verified

All phases A → D → N+1 → N+2 are implemented. Key capabilities:

- **YAML-driven extraction and query** — `swl.rules.default.yaml`, `swl.query.default.yaml`, workspace overrides deep-merged
- **Tier 1/2/3 query dispatch** — YAML intents → SQL templates → freetext
- **Autonomous feedback loop** — gap recording → rule suggestions → optional auto-apply (`auto_apply_suggestions: true`)
- **Per-model reliability** — assertions carry `model_id`, `GET /api/swl/model-reliability` endpoint, `query_swl {"question":"model reliability"}`
- **Ontological derivation** — `DeriveAreaRelations`, `DeriveSymbolUsage`, `DeriveSimilarSymbols`, all called at end of `ScanWorkspace`
- **Path queries** — `FindPath()` BFS + `askShortestPath()` handler, "path from X to Y"
- **SSE live graph stream** — two-watermark approach (nodes + edges), frontend deduplicates
- **Per-extension extraction overrides** — `ExtractionOverride` struct, `OverrideForExt()`, applied in `ExtractContent()`
- **Edge weights configurable** — `label_search.weights` in `swl.query.default.yaml`, applied in `labelSearch` scoring

---

## What Is NOT Done (Remaining Work)

### High Priority — Should Be Next

**1. `AssertNote` phantom node bug** (identified but never fixed)
`pkg/swl/query.go` — `AssertNote` creates a `Note` entity *named after the subject* rather than finding and linking to the real entity (File, Symbol, SemanticArea). If LLM asserts `subject:"pkg/auth/login.go"`, it creates a phantom Note named "pkg/auth/login.go" instead of linking to the actual File entity. The `describes` edge is therefore disconnected from real graph knowledge.
Fix: Before creating the Note, resolve the subject via `resolveEntityName()` (already exists in query.go). Link the Note to the resolved entity. If unresolvable, mark `fact_status: unknown`.
This is the most fundamental intelligence gap — assertions are currently decoration, not knowledge.

**2. No consumers for `similar_to` and `co_occurs_with` edges**
Both edge types are derived and written, but no query handler answers:
- "what symbols are similar to X?" → `similar_to`
- "what entities commonly appear together?" → `co_occurs_with`
Add `askSimilarSymbols(hint)` and `askCoOccurring(hint)` handlers. Register them. Add tier1 patterns.

**3. Frontend performance** (`web/frontend/src/components/swl/`)
- `swl-page.tsx`: `refetchInterval: 30_000` causes full graph reload every 30s — kills performance, defeats SSE. Remove it. SSE is the sole update mechanism after initial load.
- `swl-graph.tsx`: `warmupTicks={40}` runs 40 synchronous physics iterations on every data change. Reduce to 8.
- `swl-graph.tsx`: Neighborhood exit bug — `allNodesRef.current` not cleared when returning to overview, causing phantom nodes and layout instability.

**4. Config scaffolding on first init**
When `NewManager` creates `.swl/` for the first time, it should write scaffold copies of `swl.rules.yaml` and `swl.query.yaml` with comment headers. Currently the user must create these by hand to customize extraction. Implement `scaffoldConfigFiles(workspace string)` called from `NewManager` only when the DB is newly created.

**5. API correctness gaps** (`web/backend/api/swl.go`)
- NullString `.Valid` checks missing for session `ended_at` and `goal` fields (~line 727)
- DBPath opened fresh on every request instead of cached in Handler struct
- Session ID in graph query built via string concatenation — use parameterized query
- SSE stream: if DB is replaced on disk (rescan), the open connection goes stale with no recovery

### Lower Priority (See ROADMAP.md for rationale)

- Anchor document structured extraction (goals, sections, key terms from README/DESIGN docs)
- `WorkspaceProfile` entity (requires Tier 3 LLM indexing — opt-in, constrained hardware concern)
- `constraints` table wiring (useful only at >1000 entity scale)

---

## Critical Rules — Do Not Violate

**Build tags**: Always `go build -tags goolm,stdjson ./pkg/swl/...` — the package will not build without them.

**Launcher build**: `make build-launcher` requires `GOTOOLCHAIN=auto` (now set in root `Makefile`). If it fails with "running go 1.24.7", the env var has been reset to `local` somewhere. Also requires `pnpm install` in `web/frontend/` if `package.json` has changed since the last lock update.

**Upsert invariants** (enforced in `entity.go`):
- Confidence **never decreases** — `MAX(existing, new)`
- `knowledge_depth` **never decreases** — `MAX(existing, new)`
- `extraction_method` priority: `observed > stated > extracted > inferred`
- `fact_status` only via `Manager.SetFactStatus()` — never direct SQL

**Read before editing**: Never edit a file you haven't read. Never infer behavior from function names.

**Verify before planning**: Every plan item that claims something is "not implemented" must be verified by reading the actual code. This session discovered that per-extension overrides, gap suggestion surfacing, and events.model_id writes were all claimed as missing but were already present.

**Branch**: `claude/merge-swl-fixes-assessment-Y67DP`. Only this branch. The session system may assign a different branch name — ignore it, stay on Y67DP.

---

## Lessons From This Session — Do Not Repeat

**1. "Deferred" in ROADMAP was implementation-avoidance, not design rationale**
Three items were marked deferred with elaborate reasoning (Levenshtein noise, SQLite CTE syntax limits, auto-apply risk) when the real problem was choosing the wrong implementation. The pattern: encounter a flaw in the proposed approach → defer the entire capability instead of finding the correct approach. Fix: when something seems blocked, find the right implementation, don't defer.

**2. Plans written from summaries, not code**
Plan v2 had 9 phases and ~50 steps written from design documents and session summaries. Multiple steps described work that was already done or already broken in a different way than described. Before writing any plan, read the actual files.

**3. Summary ≠ reality**
Session compaction summaries describe intent, not ground truth. "Fixed in session X" means "an attempt was made in session X." Always verify against the code.

**4. Producer without consumer is waste**
`similar_to` and `co_occurs_with` edges are derived and written but never surface in any query response. Any derived entity or edge that has no query consumer provides zero value to the LLM. Build the consumer before or immediately after the producer.

**5. Test coverage is zero for N+2**
`DeriveSimilarSymbols`, `FindPath`, `askShortestPath`, `ApplyPendingSuggestions`, `LabelSearchWeights` — none have tests. The test suite passes because it doesn't cover these paths. Next session: write tests for these before adding more features.

**6. Bugs introduced in the session**
Two bugs found and fixed in retrospective:
- `labelSearch`: `m.rules.LabelSearchWeights` panicked when `m.rules == nil` — now guarded
- `askShortestPath`: tier1 pattern extracted only first endpoint from "path from X to Y" — pattern rewritten to capture full phrase as group 1

The pattern: added a feature, didn't test the error path (rules load failure), didn't verify hint extraction mechanics.

---

## Architecture Quick Reference

```
pkg/swl/
  manager.go      Top-level object; NewManager, Ask, PostHook, PreHook, ScanWorkspace
  query.go        All query handlers; handlerRegistry; tier1/2/3 dispatch; askShortestPath
  ontology.go     DeriveAreaRelations, DeriveSymbolUsage, DeriveSimilarSymbols, FindPath
  extractor.go    ExtractContent (per-extension overrides applied here), ExtractLLMResponse
  rules.go        RulesEngine, LoadRules (3-layer merge: defaults → workspace → auto)
  gap_analysis.go AnalyzeGaps, ApplyPendingSuggestions
  decay.go        MaybeDecay, deriveCoOccurrences
  scanner.go      ScanWorkspace (calls all Derive* at end)
  session.go      EnsureSession, SessionResume, askSessionActivity
  snapshot.go     BuildSnapshot (anchor docs, semantic areas)
  inference.go    PostHook tool event recording, recordToolEvent
  db.go           Schema, migrations
  types.go        All EntityType, EdgeRel, FactStatus constants
```

**Query dispatch path**: `Ask(q)` → YAML intents (`tryYAMLIntents`) → hardcoded patterns (`tryHardcodedPatterns`) → YAML SQL templates (`tryYAMLTier2`) → freetext (`tryTier3`) → gap recording + fallthrough

**Write path**: All writes go through `entityWriter` which holds the single write mutex. Never write directly to `m.db` for entity/edge upserts — use `m.writer.upsertEntity` / `m.writer.upsertEdge`.

**Rules loading order**: `swl.rules.default.yaml` (embedded) → `{workspace}/.swl/swl.rules.yaml` (user override) → `{workspace}/.swl/swl.rules.auto.yaml` (auto-applied gap suggestions). Each layer deep-merges onto the previous.

---

## Next Session — Suggested Starting Point

1. Read this document
2. Read `docs/swl/swl-refactor-requirements.md` (verbatim requirement)
3. Run `go test -tags goolm,stdjson ./pkg/swl/... -count=1` to confirm baseline
4. Fix `AssertNote` phantom node bug — it is the most impactful remaining gap
5. Add consumers for `similar_to` and `co_occurs_with` edges
6. Fix frontend performance (`refetchInterval`, `warmupTicks`, neighborhood exit)
7. Write tests for N+2 features before adding anything new
