# SWL Development Plan
**Branch:** `claude/merge-swl-fixes-assessment-Y67DP`
**Based on:** `swl-fixes` merge + full codebase audit

---

## Scope & Non-Goals

This plan covers what to fix and build next in the Semantic Workspace Layer.
Items explicitly excluded are marked **[SKIP]** with rationale.

---

## Phase 0 — Completed (swl-fixes merge)

Seven commits merged from `swl-fixes`:
- Path normalization for entity IDs and filesystem checks
- `relPath` passed correctly to `ExtractContent`; root boundary validation
- Phase 2–5 pipeline stability and data model corrections
- Revert of three regressions from feat/session-mode commit
- Launcher build errors

---

## Phase 1 — Activation & Cold-Start

### 1.1 Opt-in is intentional — keep it
SWL requires `tools.swl.enabled: true` in config. This is correct. The "automatic side
effect" language in the overview describes behaviour once enabled, not default-on. No
change needed. Operator documentation should make the enablement prerequisite explicit.

### 1.2 Cold-start bootstrap [TODO]
When SWL is first enabled on a workspace with no prior graph, `SessionResume` returns
zero entities. The agent sees an empty graph with no guidance on how to populate it.

**Fix:** In `SessionResume`, detect entity count < threshold (e.g. 10) and append:
> "Graph is empty — call `query_swl {\"scan\":true}` to index the workspace."

Optionally trigger a background scan automatically on the first session for a workspace
(session count = 0). Keep it non-blocking.

### 1.3 Strip `read_file` header before extraction [TODO]
`read_file` prepends `[file: foo.go | total: N bytes | read: bytes 0-N]\n[END OF FILE...]`
to `ForLLM`. This string flows into `postApplyReadFile` → `checkAndInvalidateLocked` →
`ExtractContent`.

Two concrete bugs:
1. **Spurious cache misses:** the hash includes the header, so paginated reads of the
   same unchanged file show as "changed" on each page.
2. **Noise in extraction:** header text passes through all regex scanners.

**Fix:** In `postApplyReadFile` (and analogously `postApplyWriteFile` fallback path),
strip lines matching `^\[file:` and `^\[` prefixes before hashing and before passing to
`ExtractContent`.

---

## Phase 2 — Agent Awareness (Configurable, Experimental)

### 2.1 Agent and subagent SWL awareness [TODO — experimental, opt-in]
Agents and subagents should be aware that a knowledge graph exists and is available.
However, injecting a full session-resume at every session start would:
- Cause drift from the user's passed intent
- Create potential infinite-loop patterns in subagents
- Add latency on every turn

**Design:** Add a configurable flag `tools.swl.inject_awareness: bool` (default `false`,
experimental). When enabled, at `TurnStart`, inject a single short inline message (< 20
tokens) summarising that SWL is active and graph has N entities — not a full resume.
Full resume remains a tool call only.

The existing `InjectSessionHint` (hint.go) covers the static instruction. This would be
a dynamic per-turn awareness ping, gated separately.

### 2.2 [SKIP] Richer SessionResume output
Current stats digest is intentional. Injecting entity lists into the resume would expand
context without guaranteed relevance to the current task.

### 2.3 [SKIP] PreHook constraint system
Not yet. The constraint table exists; implementation deferred until agent awareness
(2.1) is proven stable. A premature constraint that blocks re-reads can break legitimate
workflows.

### 2.4 Query fallback: freetext search for unmatched questions [TODO]
When a natural language question doesn't match any Tier 1 pattern (18 regex) or Tier 2
template, `Ask` currently returns a generic error. An agent asking "where is X defined?"
gets nothing useful.

**Fix:** Add a Tier 3 freetext fallback in `Ask`:
```sql
SELECT name, type, confidence, fact_status
FROM entities
WHERE name LIKE ? OR metadata LIKE ?
ORDER BY knowledge_depth DESC, access_count DESC
LIMIT 10
```

Also: log unmatched questions (session_id + question text) to a lightweight in-memory
ring buffer (not persisted — skip the events table for now) for pattern analysis.
This guides future Tier 1 pattern additions.

---

## Phase 3 — Data Quality

### 3.1 Verify path normalization end-to-end [TODO]
The swl-fixes commits normalized paths. Before building on this, verify empirically:
read the same file as `/abs/path/file.go`, `./file.go`, and `file.go` in three
consecutive tool calls, then confirm all three map to the same entity ID.

Write a test if one doesn't exist: `TestPathNormalizationIdempotency`.

### 3.2 Extraction uniformity across all tool types [TODO]
Currently `postApplyReadFile` and `postApplyWriteFile` extract content; `postApplyExec`,
`postApplyAppendFile`, and `postApplyWebFetch` have partial or no content extraction.

Problems:
- `exec` results (e.g. `go doc`, `grep`, `cat`) contain file content but extraction only
  looks for commits/test results — symbols in exec output are ignored.
- `append_file` nulls the content hash (correct) but doesn't extract from the appended
  content at all. If an agent appends a function definition, the graph never learns it.

**Fix:** Route all tool results through a common content extraction path. The key is
**deduplication**: if a file entity already has `knowledge_depth >= 2` and
`fact_status = verified`, skip re-extraction. Extract only on: first observe, content
change, or explicit scan. This prevents redundant extraction while covering all tool
paths uniformly.

### 3.3 Symbol extraction: generic, configurable, not language-specific bloat [TODO]
Current implementation has per-language regex patterns. Concerns:
- Performance: running 10+ regex patterns against every file line
- Bloat: extracting hundreds of symbols from large files, many of low value
- Duplicates: same symbol extracted multiple times across paginated reads

**Design goals:** generic, universal, adjustable via config, bounded.

**Plan:**
1. Replace per-language pattern lists with a single configurable regex list in
   `swl.Config` (`ExtractSymbolPatterns []string`). Ship sensible defaults; operator
   can override or extend without code changes.
2. Add a per-file symbol cap (already exists: `maxSymbols=60`) and per-session
   duplicate guard: skip extraction if entity ID already exists with `confidence >= 0.9`.
3. Add `ExtractSymbolsEnabled` toggle (already exists in config) as the kill switch.
4. For deduplication across paginated reads: check entity existence before insertion,
   not after. The current upsert is correct but wastes extraction work.

Performance target: extraction for a 512KB file must complete within 200ms on
reference hardware before the approach is considered viable.

### 3.4 Generic extraction: fix URL bloat, improve unknown-tool coverage [TODO]
`ExtractGeneric` (Layer 3 catch-all) currently extracts URLs from unknown tool results.
This produces noise: `http://example.com`, localhost URLs, documentation links, and
test fixture URLs flood the graph with low-value URL entities.

**Fix — URL filtering:**
- Skip URLs matching: `localhost`, `127.0.0.1`, `example.com`, `test.`, `*.local`
- Skip URLs that are schema-only or path-only (no meaningful host)
- Apply minimum confidence of `0.75` for URL entities from generic extraction (currently
  `0.9` is too generous for noise-heavy sources)
- Add URL entity cap: max 5 URLs per tool call for Layer 3 extraction

**Fix — unknown tool coverage:**
- Extract absolute file paths (`/[a-zA-Z0-9/_.-]+\.[a-z]{1,6}`)
- Extract workspace-relative paths (starts with `./` or matches known workspace root)
- Extract task-like strings (TODO/FIXME lines)
- Create a `Tool` entity for each unknown tool call (records "this tool was used" even
  without content extraction)

### 3.5 Stale cascade: do NOT make it more aggressive [HOLD]
The current one-hop cascade (children only) is the result of a painful lesson: a single
README change previously caused 15k+ entities to go stale in a cascade, with no
efficient recovery path. The system was computationally broken by overly aggressive
propagation.

Current behaviour (cascade to direct children only) is intentionally conservative.
Do not change the cascade depth until a batch re-verification mechanism exists that
can efficiently restore stale entities without full re-reads. Mark this as a future
item contingent on a recovery path being designed first.

---

## Phase 4 — Observability

### 4.1 Inference event logging [TODO]
No current way to verify that extraction is firing or what it produced. The panic
shield in `SWLHook` silently discards errors.

**Fix:**
- Add DEBUG-level structured log entries in `postApplyReadFile`, `postApplyWriteFile`,
  `postApplyExec`, and `ExtractContent`: "Extracted N symbols, M imports from X"
- Log panic recovery (currently `_ = r; _ = label`) at WARN level with tool name and
  stack summary
- Add `query_swl {"debug":true}` operation returning last N inference events from an
  in-memory ring buffer (not persisted)

### 4.2 [SKIP] Events table as inference audit log
Deferred. The events table schema exists; populating it adds write overhead and
storage growth without a clear consumer yet. Revisit when debug logging (4.1) reveals
patterns worth persisting.

### 4.3 Graph health endpoint and frontend indicator [TODO]
Operators cannot tell from the 3D graph whether extraction is working or whether the
graph is full of isolated File nodes with no Symbol children.

**Fix:**
- Add `GET /api/swl/health` returning: total entities, entities with ≥1 outbound edge,
  average symbols per File entity, last extraction timestamp
- Add a "Graph Health" score to the frontend stats panel:
  `(entities_with_edges / total_entities) * 100%`
- Visual indicator in the graph: isolated nodes (no edges) rendered as smaller/dimmer
  to distinguish them from structurally connected nodes at a glance

---

## Phase 5 — [SKIP] Roadmap Features

Cross-session aggregate queries, automated NL summarisation, adaptive decay, constraint
registry, custom decay handlers — all deferred. The system must be reliably correct at
its current scope before expanding it.

---

## Execution Order

```
1.3  Strip read_file header (unblocks correct hashing + extraction)
3.1  Verify path normalization end-to-end (validates swl-fixes work)
3.2  Extraction uniformity (exec/append/others through common path)
3.4  URL bloat fix + unknown-tool coverage in ExtractGeneric
3.3  Configurable symbol extraction patterns + dedup
4.1  Inference event logging
2.4  Query freetext fallback
1.2  Cold-start bootstrap message in SessionResume
4.3  Health endpoint + frontend indicator
2.1  Agent awareness ping (experimental, last — needs stable graph first)
```

---

## Open Questions (carry forward to next audit)

- What is the right duplicate-suppression boundary for paginated reads of the same file?
  Currently each page re-extracts. Should extraction be gated on content-hash change
  per file entity, not per result string (which includes page-specific headers)?
- Is there a meaningful signal from `exec` results that warrants deep extraction, or
  does exec produce too much noise to be worth the effort?
- What does a "recovery path" for stale entities look like — lazy re-read on agent
  access, or a background scheduler? This unblocks 3.5.
