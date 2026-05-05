# SWL Development Plan
**Branch:** `claude/merge-swl-fixes-assessment-Y67DP`
**Based on:** `swl-fixes` merge + full codebase audit + optimization assessment review
**Last updated:** 2026-05-05

---

## Scope & Non-Goals

SWL is a **lightweight SQLite knowledge graph** that extracts entities and edges
from agent tool calls to give the agent spatial awareness of the workspace.
It is **not** a search engine, embedding store, AST parser, or UI dashboard.

All plan items are evaluated against that purpose. Over-engineered or
architecturally invasive items are skipped with explicit rationale.

Items confirmed complete are marked **[DONE]**.
Items partially done are marked **[PARTIAL]**.
Items excluded are marked **[SKIP]** with rationale.

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

### 2.1 Agent awareness ping [TODO — experimental, last]
Short per-turn inline ping injected when `tools.swl.inject_awareness: true`
(default false). **Do not implement until graph quality is stable.**

**Concrete scope when ready:**
- In `session.go:SessionResume()`, after updating the session row, if the config
  flag is on, return a 1–2 sentence hint string (top 3 stale files + entity count)
  to be prepended to the system prompt. No new DB tables.
- Gate entirely behind `InjectSessionHint *bool` (already in `SWLToolConfig`).

### 2.2 [SKIP] Richer SessionResume output
Injecting entity lists expands context without guaranteed relevance. Rejected.

### 2.3 [SKIP] PreHook constraint system
Depends on 2.1 being proven useful first. Deferred indefinitely.

### 2.4 Query freetext fallback — Tier 3 [DONE]
`Ask()` dispatches Tier 1 (18 regex patterns) → Tier 2 (named SQL templates)
→ Tier 3 (multi-AND LIKE on entity names, ≤3 terms, 15 results). In `query.go`.

---

## Phase 3 — Data Quality

### 3.1 Path normalization end-to-end [DONE]
`TestPathNormalizationIdempotency` confirms all three path forms produce the same
entity ID. Scanner boundary check rejects out-of-workspace roots.

### 3.2 Extraction uniformity [DONE]
- `postApplyExec`: secondary `ExtractGeneric` pass after `ExtractExec`
- `postApplyReadFile` / `postApplyWriteFile`: content extracted only on change
- `postApplyAppendFile`: content hash nulled (triggers re-extraction on next read)

### 3.3 Configurable symbol extraction patterns [DONE]
`Config.ExtractSymbolPatterns []string` → `compileSymPatterns()` in `manager.go`.
Falls back to package defaults if empty or all-invalid. Pattern slice threaded
through to `extractSymbols()`.

### 3.4 Generic extraction: URL bloat fix + unknown-tool coverage [DONE]
- `isNoisyURL()` filters localhost, private IP ranges (10., 192.168., 172.,
  169.254., 0.0.0.0), [::1], example.com/org/net, placeholder domains (.local,
  your-domain, yourdomain, mydomain, acme.com) at all 5 extraction sites
- URL cap reduced 20→5 per tool call; confidence 0.8→0.75
- `filePathRE` and `backtickFileRE` extract file paths from exec/generic output

### 3.5 Stale cascade [HOLD]
Conservative one-hop cascade retained. Do not widen until a batch re-verification
path exists (requires 2.1 or a background scheduler — neither is implemented).

### 3.6 Symbol usage edge [DONE]
`KnownRelUses EdgeRel = "uses"` added to `types.go`.
`extractSymbols()` emits a `uses` edge (file→symbol) when `symbolName(` appears
in the file content more than once (definition + at least one call site). Capped
at 20 uses-edges per file to prevent hub inflation.

### 3.7 Confidence calibration [DONE]
`upsertEntitySQL` in `entity.go` now uses a 3-way CASE for confidence updates:
- Higher-priority incoming method → adopt incoming confidence
- Same method → average the two (bounded at 1.0)
- Lower-priority incoming method → keep higher of the two
Tests: `TestConfidenceCalibration` covers all three branches.

### 3.8 Context-aware symbol patterns — Go only [TODO — P2]
The generic `symPatterns` list matches too broadly: in Go files it captures struct
field names (which look like `FieldName Type`) and in Python it matches decorator
names (e.g. `property`, `staticmethod`) as symbols.

**Concrete fix (Go only — the most common language in picoclaw's own codebase):**

In `extractor.go`, add a `goSymPatterns []*regexp.Regexp` package-level var:
```go
var goSymPatterns = []*regexp.Regexp{
    regexp.MustCompile(`(?m)^func\s+(?:\([^)]+\)\s+)?([A-Z][A-Za-z0-9_]*)\s*\(`),  // exported func/method
    regexp.MustCompile(`(?m)^func\s+(?:\([^)]+\)\s+)?([a-z][A-Za-z0-9_]*)\s*\(`),  // unexported func/method
    regexp.MustCompile(`(?m)^type\s+([A-Za-z][A-Za-z0-9_]*)\s+(?:struct|interface)\b`), // named types only
    regexp.MustCompile(`(?m)^const\s+([A-Z][A-Za-z0-9_]*)\s*=`),                   // exported const
}
```

In `extractSymbols()`, check if `lang == "go"` (from `extTopics` detection on
file extension `.go`) and use `goSymPatterns` instead of the generic list.
If `m.compiledSymPatterns` is non-empty (operator override), it always takes
precedence over both lang-specific and generic defaults — no change there.

**Do not add Python patterns yet.** Python's decorator and indentation-based
scoping make patterns more fragile; add only after Go is verified.

**No new config fields.** The existing `ExtractSymbolPatterns` override is the
operator escape hatch.

---

## Phase 4 — Observability

### 4.1 Inference event logging [DONE]
64-slot ring buffer (`infLog`) on `Manager`. `logInferenceEvent()` called in
`postApplyReadFile`, `postApplyWriteFile`, `postApplyExec`, Layer 3 generic.
`query_swl {"debug":true}` returns ring buffer. `recoverSWLHook` logs panics at
WARN with stack dump.

### 4.2 [SKIP] Events table as inference audit log
Adds write overhead without a clear consumer. Rejected.

### 4.3 Graph health endpoint [DONE]
`GET /api/swl/health` returns score, level, entityCount, verified/stale %,
edgeCount, dbSizeBytes, message. `HealthBadge` in frontend SWLStats.

### 4.4 Isolated node count in health [TODO — P2]
Isolated nodes (entities with zero edges in either direction) inflate entity
counts without contributing to graph connectivity. Currently invisible in health.

**Concrete fix:**

Backend — add `IsolatedCount int` to `swlHealthData` (in `swl.go`). Populate it
with:
```sql
SELECT COUNT(*) FROM entities
WHERE fact_status != 'deleted'
  AND id NOT IN (SELECT DISTINCT from_id FROM edges
                 UNION SELECT DISTINCT to_id FROM edges)
```
Add to health score: `isolatedFrac * 0.15` penalty (alongside existing stale and
unknown penalties). Update this query in **both** `handleSWLHealth` and
`handleSWLOverview` (they share the same logic).

Frontend — in `HealthBadge`, add a second line:
```
{health.isolatedCount > 0 && (
  <span className="opacity-50">{health.isolatedCount} isolated</span>
)}
```
No tooltip required. No graph rendering change needed (3D physics already pushes
isolated nodes to the periphery).

---

## Phase 5 — Frontend & API Optimizations

### 5.1 Combined overview endpoint [DONE]
`GET /api/swl/overview` returns `{ stats, health, sessions }` in one DB
connection. `swl-stats.tsx` uses a single `useQuery(["swl-overview"])` at 20s.
Individual endpoints (`/stats`, `/health`, `/sessions`) kept for backward compat.

### 5.2 SSE adaptive polling backoff [DONE]
Fixed `time.NewTicker(2s)` replaced with `time.After(interval)` adaptive loop:
- No change: interval doubles (2s → 4s → 8s → 10s cap)
- Change detected: interval resets to 2s

### 5.3 Two-phase graph loading [TODO — P3, frontend-only]
For workspaces with 5000+ entities, the initial graph load can be slow. This is
a frontend-only change using **existing backend modes** — no new backend mode.

**Concrete fix (swl-page.tsx only):**
1. On mount, fetch `mode=overview` first (10k nodes max, no Symbol/Section,
   already exists). Render immediately.
2. After 1 second (`setTimeout`), fetch `mode=map` in background. On success,
   replace the graph data wholesale (`setGraphData(mapData)`).
3. Show a subtle "Loading full graph…" indicator (text, not spinner) during
   the background fetch, using React Query's `isFetching` on the map query.
4. No new server-side `mode=fast`. No partial-merge logic. Full replacement only.

**Skip if:** the typical workspace stays under 3000 entities. Re-evaluate when
there is evidence of actual rendering lag complaints.

### 5.4 Focus mode manual refresh [TODO — P2]
In focus (neighborhood) mode, new edges to the focused node are missed because
auto-refresh is off. A manual button fixes this without adding continuous polling.

**Concrete fix:**
- In `swl-page.tsx`, expose `refetch` from the `useQuery` call for the
  neighborhood graph.
- Add a `<button onClick={refetch}>↻</button>` to the focus mode toolbar —
  visible only when `viewMode === 'neighborhood'`.
- No auto-refresh. No polling timer. No state beyond what `useQuery` already
  provides (`isFetching` for loading state on the button).

### 5.5 Fetch-in-progress indicator [TODO — P2]
Silent updates confuse users on slow connections.

**Concrete fix:** In `swl-stats.tsx`, destructure `isFetching` from the
`useQuery(["swl-overview"])` call. Append a pulsing `●` to the "Graph Health"
section header while `isFetching` is true:
```tsx
<span className={isFetching ? "animate-pulse opacity-50 ml-1" : "hidden"}>●</span>
```
No other change. No canvas-level indicators (causes re-renders).

### 5.6 [SKIP] Virtual rendering / WebGL instancing
Three.js already handles instancing. Duplicating this adds complexity with no
measurable gain. Rejected.

### 5.7 [SKIP] Unified cache layer with ETag validation
React Query's stale-while-revalidate + SQLite WAL mode already provides adequate
caching. A custom `SWLCache` class duplicates that without benefit. Rejected.

### 5.8 [SKIP] Offline support / localStorage fallback
A stale graph is misleading. Showing it offline has negative user value. Rejected.

---

## Phase 6 — LLM Context Quality

### 6.1 Intent entity from TurnStart [DONE]
`swl_hook.go` records user message as an `Intent` entity with an `intended_for`
edge to the session on `KindAgentTurnStart`.

### 6.2 Session outcome tagging from TurnEnd [TODO — P2]
Currently `endAllSessions()` is called only from `Manager.Close()` and writes a
generic `"entities=N edges=M"` summary. The hook never handles `KindAgentTurnEnd`,
so error/abort outcomes are invisible in the sessions table.

**Concrete fix:**

`TurnEndPayload` (in `pkg/agent/event_payloads.go`) has `Status TurnEndStatus`
(completed/error/aborted) and `Iterations int`. `KindAgentTurnEnd` exists in
`pkg/events/kind.go`. Both are usable.

1. Add `EndSession(sessionKey, outcome string)` to `pkg/swl/session.go`:
   ```go
   func (m *Manager) EndSession(sessionKey, outcome string) {
       m.sessionsMu.Lock()
       id := m.activeSessions[sessionKey]
       delete(m.activeSessions, sessionKey)
       m.sessionsMu.Unlock()
       if id == "" { return }
       now := nowSQLite()
       summary := m.autoSummary() + " status=" + outcome
       m.mu.Lock()
       m.db.Exec("UPDATE sessions SET ended_at=?, summary=? WHERE id=? AND ended_at IS NULL",
           now, summary, id)
       m.mu.Unlock()
   }
   ```
2. In `swl_hook.go`, add a `case runtimeevents.KindAgentTurnEnd:` branch:
   ```go
   case runtimeevents.KindAgentTurnEnd:
       payload, ok := evt.Payload.(TurnEndPayload)
       if !ok { return nil }
       h.manager.EndSession(evt.Scope.SessionKey, string(payload.Status))
   ```
3. `endAllSessions()` on `Close()` stays as the catch-all for sessions not yet
   ended by the hook (e.g. process kill).

**Only** `Status` is recorded. `Iterations` and `Duration` are not stored —
they'd require schema changes.

### 6.3 [SKIP] Markdown section → symbol cross-reference edges
The plan was: after extracting sections from a `.md` file, SQL-lookup existing
Symbols with the same name and emit `references` edges.

**Reason for skip:** `ExtractContent` is a pure function with no DB access. To
query existing symbols during extraction requires either (a) passing a DB handle
into the extractor (breaks the pure-function model) or (b) a post-apply pass in
the Manager (requires the delta to be committed first, then a second write
transaction). Both approaches add architectural complexity for a feature that
primarily benefits markdown-heavy workspaces, which are not the primary use case
for picoclaw. The cross-reference value is marginal compared to the cost.

Rejected.

### 6.4 [SKIP] Code flow / AST analysis
Per-language parsers are too expensive and language-specific. Rejected.

### 6.5 [SKIP] Semantic clustering / embeddings
Requires embedding model inference at extraction time. Out of scope. Rejected.

### 6.6 [SKIP] Cross-session entity merging
The entity model already deduplicates by `entityID(type, name)` — same name +
type = same entity regardless of session. Cross-session edges are unnecessary.
Rejected.

---

## Execution Order (Remaining Items)

```
# Straightforward, bounded, high-signal
4.4   Isolated node count in health + overview (45 min)
       — SQL query + IsolatedCount field + HealthBadge line
6.2   Session outcome tagging from TurnEnd (45 min)
       — EndSession() method + case in swl_hook.go OnRuntimeEvent

# Extraction quality (low risk, bounded patterns)
3.8   Go-specific symbol patterns in extractor.go (1h)
       — goSymPatterns var + lang check in extractSymbols(); no new config

# UI polish (trivial, isolated to single component)
5.5   isFetching indicator in swl-stats.tsx (15 min)
5.4   Focus mode refresh button in swl-page.tsx (30 min)

# Larger, conditional on evidence of need
5.3   Two-phase graph load (frontend-only, mode=overview then mode=map) (2h)
       — Only if workspace grows past ~3k entities with visible lag

# Experimental — gates on graph quality
2.1   Agent awareness ping (inject_awareness flag, default off) (2h)
       — Blocked: needs proven graph quality + a design spike first
```

---

## Open Questions (carry forward)

- Paginated reads re-extract on each page. Should extraction be gated on
  content-hash change per file entity, not per result string?
- Is there a meaningful signal from `exec` results that warrants deep extraction,
  or does exec produce too much noise?
- What does a recovery path for stale entities look like — lazy re-read on agent
  access, or a background scheduler? Unblocks 3.5.
- For 5.3: measure actual rendering time on a 3k-entity workspace before
  implementing. It may be a non-problem.
