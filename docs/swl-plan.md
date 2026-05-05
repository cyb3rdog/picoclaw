# SWL Development Plan
**Branch:** `claude/merge-swl-fixes-assessment-Y67DP`
**Based on:** `swl-fixes` merge + full codebase audit + optimization assessment review

---

## Scope & Non-Goals

This plan covers what to fix and build next in the Semantic Workspace Layer.
Items explicitly excluded are marked **[SKIP]** with rationale.
Items confirmed complete are marked **[DONE]**.
Items partially done are marked **[PARTIAL]**.

---

## Phase 0 — Completed (swl-fixes merge) [DONE]

- Path normalization for entity IDs and filesystem checks
- `relPath` passed correctly to `ExtractContent`; root boundary validation
- Phase 2–5 pipeline stability and data model corrections
- Revert of three regressions from feat/session-mode commit
- Launcher build errors

---

## Phase 1 — Activation & Cold-Start

### 1.1 Opt-in is intentional — keep it [DONE]
SWL requires `tools.swl.enabled: true` in config. Correct by design.

### 1.2 Cold-start bootstrap [DONE]
`SessionResume` now detects < 10 non-session entities and emits a guidance message
recommending `query_swl {"scan":true}`. Implemented in `session.go`.

### 1.3 Strip `read_file` header before extraction [DONE]
`stripToolHeader()` implemented in `inference.go`. Applied in `postApplyReadFile`
and `postApplyWriteFile`. Content hash is now header-free; paginated reads of
unchanged files no longer cause spurious cache misses.

---

## Phase 2 — Agent Awareness (Configurable, Experimental)

### 2.1 Agent awareness ping [TODO — last, experimental]
Short per-turn inline ping when `tools.swl.inject_awareness: true` (default false).
Deferred — needs stable graph quality first.

### 2.2 [SKIP] Richer SessionResume output
Intentional. Injecting entity lists expands context without guaranteed relevance.

### 2.3 [SKIP] PreHook constraint system
Deferred until 2.1 is proven stable.

### 2.4 Query freetext fallback — Tier 3 [DONE]
`Ask()` now dispatches through Tier 1 (18 regex patterns) → Tier 2 (named SQL
templates) → Tier 3 (freetext: multi-AND LIKE on entity names, stop-words filtered,
≤3 terms, 15 results). Implemented in `query.go`.

---

## Phase 3 — Data Quality

### 3.1 Path normalization end-to-end [DONE]
`TestPathNormalizationIdempotency` confirms all three path forms (absolute, `./rel`,
bare rel) produce the same entity ID. Scanner boundary check rejects out-of-workspace
roots.

### 3.2 Extraction uniformity [DONE]
- `postApplyExec`: secondary `ExtractGeneric` pass after `ExtractExec`
- `postApplyReadFile` / `postApplyWriteFile`: content extracted only on change
- `postApplyAppendFile`: content hash nulled (triggers re-extraction on next read)

### 3.3 Configurable symbol extraction patterns [DONE]
`Config.ExtractSymbolPatterns []string` added. `compileSymPatterns()` in `manager.go`
builds compiled pattern list from config; falls back to package defaults if empty or
all-invalid. `extractSymbols()` accepts the pattern slice as a parameter.

### 3.4 Generic extraction: URL bloat fix + unknown-tool coverage [DONE]
- `isNoisyURL()` filters localhost, 127.0.0.1, example.com, example.org, example.net,
  .local at all 5 extraction sites in `extractor.go`
- URL cap reduced 20→5 per tool call for Layer 3; confidence 0.8→0.75
- `filePathRE` and `backtickFileRE` extract file paths from exec/generic output

### 3.4a URL noise expansion [TODO — quick win from optimization assessment]
Current `noisyURLHosts` is missing several classes called out in the assessment:
- IP address ranges: `://0.`, `://1.`, `://10.`, `://192.168.`, `://172.`
- Non-HTTP schemes: `mailto:`, `tel:`, `data:`, `javascript:`
- Common CI/CD placeholder domains: `your-domain.com`, `yourdomain.`

**Fix:** Extend `noisyURLHosts` slice with the above. Also add a scheme prefix
check: reject any URL whose scheme is not `http://` or `https://` (unless it's
the explicitly fetched URL from `web_fetch`, which has already been validated).

### 3.5 Stale cascade [HOLD]
Conservative one-hop cascade retained intentionally. Do not widen until a batch
re-verification path exists. See open questions.

### 3.6 Relationship inference [TODO — P1 from optimization assessment]
Currently only `defines`, `imports`, `has_task`, `has_section`, `mentions`,
`in_dir`, and session-activity edges exist. Several valuable structural edges
are missing:

**A. Symbol usage (`uses` edge)**
When file content mentions a known symbol name followed by `(`, record a lightweight
`uses` edge from the file to the symbol entity. Cap at 20 uses-edges per file.
Add `KnownRelUses EdgeRel = "uses"` to `types.go`.

**B. Directory tree completeness**
Already have `in_dir` edges (file→dir). Add the inverse: `contains` edge from
dir to child (already implied; confirm `ExtractDirectory` emits `in_dir`).
**No new work — already correct.**

**C. `similar_to` / `related_to` edges [SKIP for now]**
Requires embedding or Levenshtein; too expensive at extraction time. Defer.

**D. Dependency chains (transitive imports) [SKIP for now]**
Tier 2 SQL template `dependency_chain` already computes this at query time with
a recursive CTE. No graph-level storage needed.

**Implementation scope for 3.6:** only the `uses` edge (A). Emit it in
`extractSymbols()` when a symbol name appears in content as `symbolName(`.

### 3.7 Confidence calibration [TODO — P1 from optimization assessment]
Current confidence is monotonically non-decreasing but static at insertion time.
The assessment correctly identifies that same-method re-observations should
average rather than just max.

**Fix:** In `upsertEntity` SQL (entity.go), change the `observed`/`stated`/
`extracted` confidence update rule:
- If incoming `extraction_method` priority > existing: replace confidence
- If same priority: `new_conf = (existing + incoming) / 2.0` (bounded at 1.0)
- If lower priority: no update (existing is better-sourced)

This requires a small extension to the conflict-resolution SQL in `entity.go`.

### 3.8 Context-aware extraction patterns [PARTIAL — TODO]
`extTopics` map already detects language from file extension and creates a `Topic`
entity. Symbol extraction uses a single multi-language pattern list (`symPatterns`).

**What's missing:** language-specific pattern tuning. The generic patterns extract
struct field names in Go as "symbols", and Python decorators create false positives.

**Design:** Add a `langSymPatterns map[string][]*regexp.Regexp` in `extractor.go`
keyed by lang string from `extTopics`. When present, use the lang-specific slice
instead of `m.compiledSymPatterns`. Falls back to generic if no lang-specific entry.
Ship with Go and Python overrides only (the two most common in picoclaw's own codebase).
Keep the existing `ExtractSymbolPatterns` config override as the operator escape hatch —
it takes precedence over both lang-specific and generic defaults.

**Do not add more than 2 lang-specific pattern sets initially.** Avoid the language
bloat the original plan warned about.

---

## Phase 4 — Observability [ALL DONE]

### 4.1 Inference event logging [DONE]
- 64-slot ring buffer (`infLog`) on `Manager`
- `logInferenceEvent()` called in `postApplyReadFile`, `postApplyWriteFile`,
  `postApplyExec`, and Layer 3 generic path
- `query_swl {"debug":true}` returns ring buffer as formatted text
- `recoverSWLHook` logs panics at WARN with `runtime.Stack` dump

### 4.2 [SKIP] Events table as inference audit log
Deferred. Adds write overhead without a clear consumer yet.

### 4.3 Graph health endpoint [DONE]
- `GET /api/swl/health` returns: score (0–1), level (empty/poor/fair/good/excellent),
  entity count, verified/stale %, edge count, DB size
- `HealthBadge` component in frontend SWLStats panel polls every 30s

### 4.4 Isolated node visibility [TODO — P2 from optimization assessment]
The assessment notes isolated nodes (no edges) are visually indistinguishable from
connected nodes. The health score currently uses verified/stale ratios only.

**Fix:**
- Backend: Add `isolated_count` to `/api/swl/health` (entities with 0 edges in either
  direction). Include in health score: penalise isolated nodes mildly
  (`isolated_frac * 0.15`).
- Frontend: No change needed to graph rendering (the 3D physics engine already pushes
  isolated nodes to the periphery). Add isolated count to `HealthBadge` tooltip.

---

## Phase 5 — Frontend & API Optimizations

*New phase added from optimization assessment.*

### 5.1 Combined overview endpoint — `/api/swl/overview` [TODO — P0]
Three separate React Query subscriptions (`/stats`, `/health`, `/sessions`) each open
a SQLite read connection and run separate queries every 15–30 seconds. On a workspace
with 20k+ entities this is three separate DB round-trips per poll cycle.

**Fix:** Add `GET /api/swl/overview` that returns all three payloads in a single
response and a single DB connection:
```json
{ "stats": {...}, "health": {...}, "sessions": [...] }
```
Replace the three separate `useQuery` calls in `swl-stats.tsx` with a single
`useQuery(["swl-overview"])` call with a 20s interval. Keep the individual endpoints
for backward compatibility (other consumers, curl diagnostics).

### 5.2 SSE polling interval backoff [TODO — P1]
The 2-second SSE poll is aggressive. Most workspaces have extraction bursts (during
active tool use) followed by quiet periods.

**Fix:** Implement exponential backoff in the SSE stream handler:
- When `maxModAt == lastModAt` (no change): double the sleep, capped at 10s
- When a change is detected: reset interval to 2s
- Signal the client of the current server-side interval via an SSE comment
  (`: poll-interval=N`) so the frontend can adjust its reconnect timer if needed.

This replaces the fixed `time.NewTicker(2 * time.Second)` with a dynamic interval.

### 5.3 Progressive graph loading [TODO — P0]
Large graphs (5000+ nodes) block the UI thread during the initial JSON parse and
3D layout computation. The topology endpoint already paginates server-side but the
frontend loads everything at once.

**Fix — phased load:**
- Phase 1 (immediate): Load top 500 highest-quality nodes (existing `/api/swl/graph`
  with `mode=overview` already does this for ~150 nodes; extend to 500 with a new
  `mode=fast` that includes symbols but caps at 500 nodes/1000 edges)
- Phase 2 (background, 1s delay): Upgrade to full `mode=map` silently, merge into
  existing graph data without re-rendering from scratch
- Keep `mode=overview` as the user-selectable structural view

This requires a new `SWLGraphMeta.isPartial: boolean` field to signal phase 1 state
to the frontend, and a `useEffect` in `swl-page.tsx` to trigger the phase 2 load.

### 5.4 Focus mode refresh [TODO — P2]
Focus mode (`neighborhood` query) currently disables auto-refresh entirely. A long-
running session that adds new edges to a focused node silently misses them.

**Fix:** Add a manual "Refresh" button to the focus mode toolbar that re-fetches the
neighborhood query for the current focus node. Auto-refresh remains off (correct —
continuous neighborhood re-fetches are expensive). The button appears only in focus
mode.

### 5.5 Refresh indicators [TODO — P2]
Graphs and stats update silently. On slow connections users see no indication.

**Fix:** Use React Query's `isFetching` state to show a subtle animated dot next to
the "Graph Health" section header while any SWL query is in flight. No spinners on
the 3D canvas itself (causes jarring re-renders).

### 5.6 [SKIP] Virtual rendering / WebGL instancing
The 3D graph already uses WebGL (Three.js via react-force-graph-3d). Adding separate
instancing logic duplicates work the library already does. The LOD tiers already
reduce geometry for large graphs. Skip.

### 5.7 [SKIP] Unified cache layer with ETag validation
SQLite's WAL mode and React Query's stale-while-revalidate already provide adequate
caching. Adding a custom `SWLCache` class would duplicate cache logic without
measurable benefit at current scale. Skip.

### 5.8 [SKIP] Offline support / localStorage fallback
The graph visualization requires live data to be useful (stale graph = misleading).
Showing a stale 3D graph offline adds complexity with negative user value. Skip.

---

## Phase 6 — LLM Context Quality

*New phase added from optimization assessment — filtered for viability.*

### 6.1 Intent entity from TurnStart [PARTIAL — DONE]
`swl_hook.go` already records user message as an `Intent` entity with a
`intended_for` edge to the session. Confirmed working.

### 6.2 Historical outcome tagging [TODO — P2]
When a session ends (`TurnEnd` with `status = error` or `status = aborted`), tag
the session entity with `fact_status = stale` and record the turn outcome in the
session's `summary` field. This surfaces failed attempts in `SessionResume`.

Currently `endAllSessions()` sets `summary = "entities=N edges=M"`. Enhance with
outcome: `"entities=N edges=M status=error reason=..."` where available.

**Implementation:** In `swl_hook.go`, handle `runtimeevents.KindAgentTurnEnd`
similarly to `KindAgentTurnStart`. Extract `TurnEndPayload.Status` and append to
the session record.

### 6.3 Internal reference mapping — doc sections [TODO — P2]
When a markdown file is extracted (`has_section` edges already exist from headings),
and another file `mentions` a heading string verbatim (e.g. a README heading that
matches a function name), add a `references` edge from the file to the section entity.

This is a lightweight post-extraction pass in `ExtractContent`: after extracting
sections from a `.md` file, check if any section heading matches an existing Symbol
or Task entity name (SQL lookup, max 10 matches). Emit `references` edge.

Cap at 5 cross-references per file. Only for markdown files.

### 6.4 [SKIP] Code flow / AST analysis
Requires per-language parsers. Too expensive, too language-specific. The generic
regex-based approach covers 80% of value at 5% of the cost.

### 6.5 [SKIP] Semantic clustering / embeddings
Requires embedding model inference at extraction time. Out of scope for a
lightweight knowledge graph. Defer indefinitely.

### 6.6 [SKIP] Cross-session entity merging
The assessment proposes linking same-named entities across sessions. The entity model
already deduplicates by `entityID(type, name)` — same name + type = same entity
regardless of session. Cross-session edges are unnecessary.

---

## Execution Order

```
# Quick wins first (from optimization assessment)
3.4a  URL noise expansion — extend noisyURLHosts (30 min)
3.6   Symbol usage edge — KnownRelUses in extractSymbols (2h)
3.7   Confidence calibration — same-priority averaging in upsertEntity SQL (1h)

# Frontend / API (P0 items from assessment)
5.1   Combined /api/swl/overview endpoint + frontend query consolidation (3h)
5.2   SSE adaptive polling backoff (1h)
5.3   Progressive graph loading (mode=fast + phase 2 background upgrade) (4h)

# Extraction quality
3.8   Context-aware patterns — Go + Python lang-specific symbol patterns (2h)
4.4   Isolated node count in health endpoint + badge tooltip (1h)

# UX & observability
5.4   Focus mode refresh button (1h)
5.5   Refresh indicators via isFetching (30 min)
6.2   Historical outcome tagging from TurnEnd events (2h)
6.3   Internal reference mapping for markdown sections (2h)

# Experimental, last
2.1   Agent awareness ping — inject_awareness flag (gated, experimental)
```

---

## Open Questions (carry forward)

- What is the right duplicate-suppression boundary for paginated reads of the same file?
  Currently each page re-extracts. Should extraction be gated on content-hash change
  per file entity, not per result string (which includes page-specific headers)?
- Is there a meaningful signal from `exec` results that warrants deep extraction, or
  does exec produce too much noise to be worth the effort?
- What does a "recovery path" for stale entities look like — lazy re-read on agent
  access, or a background scheduler? This unblocks 3.5.
- For 5.3 progressive loading: should `mode=fast` be a server-side concept or should
  the frontend simply call `mode=overview` first and `mode=map` second?
