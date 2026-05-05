# SWL Development Plan
**Branch:** `claude/merge-swl-fixes-assessment-Y67DP`
**Based on:** `swl-fixes` merge + full codebase audit + optimization assessment
**Last updated:** 2026-05-05

---

## Scope & Non-Goals

SWL is a **lightweight SQLite knowledge graph** that extracts entities and edges
from agent tool calls to give the agent spatial awareness of the workspace.
It is **not** a search engine, embedding store, AST parser, or UI dashboard.

Extraction patterns are intentionally generic — language-specific specialisation
is an explicit non-goal.

---

## Phase 0 — swl-fixes merge [DONE]

- Path normalization for entity IDs and filesystem checks
- `relPath` passed correctly to `ExtractContent`; root boundary validation
- Phase 2–5 pipeline stability and data model corrections
- Revert of three regressions from feat/session-mode commit
- Launcher build errors

---

## Phase 1 — Activation & Cold-Start [DONE]

### 1.1 Opt-in is intentional
SWL requires `tools.swl.enabled: true`. Correct by design.

### 1.2 Cold-start bootstrap
`SessionResume` detects < 10 non-session entities and emits a guidance message
recommending `query_swl {"scan":true}`. Implemented in `session.go`.

### 1.3 Strip `read_file` header before extraction
`stripToolHeader()` in `inference.go`. Applied in `postApplyReadFile` and
`postApplyWriteFile`. Content hash is header-free; paginated reads of unchanged
files no longer cause spurious cache misses.

---

## Phase 2 — Agent Awareness

### 2.1 Agent awareness ping [DEFERRED — experimental]
Short per-turn inline hint injected when `tools.swl.inject_session_hint: true`
(default false). Gate already exists in `SWLToolConfig`.

**Do not implement until graph quality is proven stable in production.**

When ready: in `session.go:SessionResume()`, return a 1–2 sentence hint string
(top 3 stale files + entity count) for the caller to prepend to the system
prompt. No new DB tables, no schema changes.

### 2.2 [SKIP] Richer SessionResume output
Injecting entity lists expands context without guaranteed relevance.

### 2.3 [SKIP] PreHook constraint system
Depends on 2.1 being proven useful. Deferred indefinitely.

### 2.4 Query freetext fallback — Tier 3 [DONE]
`Ask()` dispatches Tier 1 (18 regex patterns) → Tier 2 (named SQL templates)
→ Tier 3 (multi-AND LIKE on entity names, ≤3 terms, 15 results). In `query.go`.

---

## Phase 3 — Data Quality [DONE]

### 3.1 Path normalization end-to-end
`TestPathNormalizationIdempotency` confirms all three path forms produce the same
entity ID. Scanner boundary check rejects out-of-workspace roots.

### 3.2 Extraction uniformity
- `postApplyExec`: secondary `ExtractGeneric` pass after `ExtractExec`
- `postApplyReadFile` / `postApplyWriteFile`: content extracted only on change
- `postApplyAppendFile`: content hash nulled (triggers re-extraction on next read)

### 3.3 Configurable symbol extraction patterns
`Config.ExtractSymbolPatterns []string` → `compileSymPatterns()` in `manager.go`.
Falls back to package defaults if empty or all-invalid.

### 3.4 URL bloat fix + unknown-tool coverage
- `isNoisyURL()` filters localhost, private IP ranges (10., 192.168., 172.,
  169.254., 0.0.0.0), [::1], example.com/org/net, placeholder domains (.local,
  your-domain, yourdomain, mydomain, acme.com) at all 5 extraction sites
- URL cap reduced 20→5 per tool call; confidence 0.8→0.75
- `filePathRE` and `backtickFileRE` extract file paths from exec/generic output

### 3.5 Stale cascade [HOLD]
Conservative one-hop cascade retained. Do not widen until a batch re-verification
path exists.

### 3.6 Symbol usage edge
`KnownRelUses EdgeRel = "uses"` in `types.go`. `extractSymbols()` emits a `uses`
edge (file→symbol) when `symbolName(` appears more than once in the file (one
definition + at least one call site). Capped at 20 per file.

### 3.7 Confidence calibration
`upsertEntitySQL` in `entity.go` — 3-way CASE:
- Higher-priority method incoming → adopt incoming confidence
- Same method → average the two (bounded at 1.0)
- Lower-priority incoming → keep the higher of the two

`TestConfidenceCalibration` covers all three branches.

### 3.8 [SKIP] Language-specific symbol patterns
Generic patterns are intentional. Specialisation per language adds maintenance
surface without clear benefit for a tool that must work across any workspace.

---

## Phase 4 — Observability [DONE]

### 4.1 Inference event logging
64-slot ring buffer (`infLog`) on `Manager`. `query_swl {"debug":true}` returns
the buffer. `recoverSWLHook` logs panics at WARN with stack dump.

### 4.2 [SKIP] Events table as inference audit log
Adds write overhead without a clear consumer.

### 4.3 Graph health endpoint
`GET /api/swl/health` (and `/api/swl/overview`) return: score (0–1), level,
entityCount, verifiedPct, stalePct, edgeCount, isolatedCount, dbSizeBytes,
message. `HealthBadge` in frontend SWLStats displays all fields.

Health score formula: `1.0 − (staleFrac×0.5) − (unknownFrac×0.2) − (isolatedFrac×0.15)`

### 4.4 Isolated node count
Entities with zero edges in either direction counted via:
```sql
SELECT COUNT(*) FROM entities WHERE fact_status != 'deleted'
  AND id NOT IN (SELECT DISTINCT from_id FROM edges
                 UNION SELECT DISTINCT to_id FROM edges)
```
`computeHealth()` helper shared by both `/health` and `/overview` — no duplicate
logic. `HealthBadge` shows isolated count when > 0.

---

## Phase 5 — Frontend & API [DONE]

### 5.1 Combined overview endpoint
`GET /api/swl/overview` → `{ stats, health, sessions }` in one DB connection.
`swl-stats.tsx` uses a single `useQuery(["swl-overview"])` at 20s.
Individual endpoints kept for backward compat.

### 5.2 SSE adaptive polling backoff
`time.After(interval)` replaces fixed `time.NewTicker(2s)`:
- No change detected: interval doubles (2s → 4s → 8s → 10s cap)
- Change detected: resets to 2s

### 5.3 Two-phase graph loading [DEFERRED — conditional]
Only warranted if workspaces grow past ~3000 entities with observable rendering
lag. Frontend-only change: load `mode=overview` first, upgrade to `mode=map`
after 1s. No new backend mode. No action until lag is reported.

### 5.4 [SKIP] Focus mode manual refresh
Not needed.

### 5.5 Fetch-in-progress indicator
`isFetching` from `useQuery(["swl-overview"])` drives an `animate-pulse` dot
next to the "Graph Health" header in `HealthBadge`.

### 5.6 [SKIP] Virtual rendering / WebGL instancing
Three.js already handles this.

### 5.7 [SKIP] Unified cache layer with ETag validation
React Query + SQLite WAL already covers this.

### 5.8 [SKIP] Offline support / localStorage fallback
A stale graph is misleading. Negative user value.

---

## Phase 6 — LLM Context Quality

### 6.1 Intent entity from TurnStart [DONE]
`swl_hook.go` records user message as an `Intent` entity with an `intended_for`
edge to the session on `KindAgentTurnStart`.

### 6.2 [SKIP] Session outcome tagging from TurnEnd
Writing turn status (completed/error/aborted) to the sessions table does not
improve graph quality or entity accuracy. Out of scope for the knowledge graph.

### 6.3 [SKIP] Markdown section → symbol cross-reference edges
`ExtractContent` is a pure function with no DB access. Adding SQL lookups during
extraction requires either breaking that model or a two-pass write transaction.
Architectural cost exceeds the marginal value.

### 6.4 [SKIP] Code flow / AST analysis
Per-language parsers, out of scope.

### 6.5 [SKIP] Semantic clustering / embeddings
Out of scope.

### 6.6 [SKIP] Cross-session entity merging
Entity ID is `type+name` — same entity already deduplicates across sessions.

---

## Status Summary

Everything from the optimization assessment is resolved. Two items remain open:

| Item | Status | Condition |
|---|---|---|
| 5.3 Two-phase graph load | Deferred | Only if >3k entity lag observed |
| 2.1 Agent awareness ping | Deferred | Only after graph quality verified in prod |

---

## Open Questions

- Is there a meaningful signal from `exec` results that warrants deep extraction,
  or does exec produce too much noise?
- What does a recovery path for stale entities look like — lazy re-read on agent
  access, or a background scheduler? Unblocks 3.5.
