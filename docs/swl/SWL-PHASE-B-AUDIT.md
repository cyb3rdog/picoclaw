# SWL Phase B Audit & Refactor Plan v5

> Status: **DRAFT** | Date: 2026-05-08
> Supersedes: SWL-REFACTOR.md (v4), SWL-PHASE1-AUDIT.md
> Grounded in: `swl-refactor-requirements.md` (verbatim requirements)

---

## 1. What Actually Exists vs What Was Claimed

### Delivered ✅

| Component | Status | Evidence |
|-----------|--------|----------|
| `BuildSnapshot` + lazy extraction | ✅ Real | `scanner.go` calls `ExtractContent` on tool call, not at scan time. 1,414 File entities, 43 snapshot entities (29 SemanticAreas + 14 AnchorDocs). |
| Semantic labels on entities | ✅ Real | `scanner.go:231` passes `lrMeta` (from `DeriveLabels`) to upsertEntity. File metadata contains `role`, `domain`, `kind`, `content_type`. Confirmed by `labelSearch` reading them via `json_extract`. |
| `labelSearch()` query handler | ✅ Real | `query.go:206` — searches metadata role/domain/kind with weighted scoring, falls back to directory path match. |
| YAML externalization (Phase B) | ✅ Real | `swl.rules.default.yaml` (30 path prefix rules, 18 name patterns, 35 content types, extraction limits). `swl.query.default.yaml` (18 Tier 1 intents + 3 Tier 2 templates). `rules.go` with `LoadRules`, `LoadQueryConfig`, deep-merge overrides. |
| Feedback loop (Phase C) | ✅ Real | `gap_analysis.go` — `AnalyzeGaps`, `SuggestRules`, `RuleSuggestion`. `query_gaps.suggestion` column. `KnowledgeGaps` shows both entity gaps and query gaps with rule candidates. |
| `access_count` fix | ✅ Fixed | `entity.go:65` — `access_count = access_count` (preserved on writes). Only `BumpAccessCount` increments it (on reads). |

### Actually Missing / Incomplete

| # | Gap | Impact | Evidence |
|---|-----|--------|-----------|
| B1 | **Phase A.2 path rules NOT externalized** | HIGH | `labels.go` has 59 hardcoded `{prefix: "pkg/auth/", role: "..."}` rules. `rules.go:RulesEngine.DeriveLabels` does exist but `scanner.go` calls `DeriveLabels` from `labels.go` (the standalone function), NOT through the rules engine. So YAML config is loaded but unused for label derivation. |
| B2 | **Phase A.2 name/content-type rules NOT externalized** | HIGH | Same as B1 — `labels.go` has hardcoded `namePatternRules` and `contentTypeRules`, never replaced by YAML values. |
| B3 | **Extractor limits NOT externalized** | MEDIUM | `extractor.go:14-19` has hardcoded limits (60/40/30/20/20). These are NOT consumed from `swl.rules.yaml`. YAML has `file_rules` with the correct values but extractor never reads them. |
| B4 | **Noise symbols NOT externalized** | MEDIUM | `extractor.go:528` hardcodes `maxUses = 20`. YAML has `noise` list but it's never loaded into extractor. |
| B5 | **`query_swl {snapshot:true}` not implemented** | MEDIUM | Verification criterion from Phase A audit. Currently covered indirectly by `askWorkspacePurpose` + `askSemanticAreas`, but the explicit `{snapshot:true}` mode is missing. |
| B6 | **`swlignore` not in config** | LOW | `ignore.go` and `swlignore` support exists but is not exposed in `swl.rules.yaml` — the place where workspace configuration lives. |
| B7 | **Tier 1 ontological derivation (SQL-based)** | LOW | Design doc specifies a 4-tier model. Tier 1 (derived from graph structure) was conflated with Tier 0 (path→label rules). True SQL-based inference (e.g., "if entity A has `in_dir` edge to SemanticArea X, and X has `role: api`, then A has `domain: networking`) was never built. Not critical for current use, but a spec gap. |

---

## 2. Root Cause

Phase B was implemented as *YAML files created*, not *YAML values consumed*. The infrastructure exists — `RulesEngine` loads, `rules.go` parses, deep-merge works — but **none of the Go code reads from it**. The labels and extractors still use their hardcoded originals.

```
What was built:
  swl.rules.default.yaml  (59 path rules + 18 name patterns + 35 content types)
  rules.go                (LoadRules, DeriveLabels, deep-merge)

What was NOT connected:
  scanner.go → labels.go (hardcoded)    ← path rules bypass YAML entirely
  extractor.go → hardcoded limits       ← extractor limits bypass YAML entirely

What works:
  query.go → rules.QueryIntents         ← query intents ARE wired
  query.go → rules.SQLTemplates         ← Tier 2 SQL templates ARE wired
  gap_analysis.go                       ← feedback loop IS wired
```

**The YAML is a dead store for labels and extraction rules.** The query engine is the only part that actually uses the config.

---

## 3. Unified Phase Plan

All remaining work is scoped as **Phase B completion** — wiring the YAML config into the code that already exists but isn't using it. No new functionality, just connection.

### Phase B.1 — Wire label rules through RulesEngine

**Files:** `scanner.go`, `snapshot.go`, `labels.go`, `rules.go`

**Changes:**
1. Remove `pathPrefixRules`, `namePatternRules`, `contentTypeRules` from `labels.go` — they're duplicated in `swl.rules.default.yaml` already
2. Make `RulesEngine` the single source of truth for label derivation — `rules.go` loads `PathPrefixRules`, `NamePatternRules`, `ContentTypeRules` from YAML
3. Update `Manager.DeriveLabels` (which calls `m.rules.DeriveLabels`) — it's already correct but needs the `pathPrefixRules` from YAML loaded
4. Update `scanner.go` to call `m.DeriveLabels()` (already does) — no change needed there
5. Update `snapshot.go` — already calls `m.DeriveLabels()` via rules engine — no change needed

**Result:** `swl.rules.yaml` path prefix rules, name patterns, and content types are consumed by the scanner. Workspace overrides via deep-merge work.

**Verification:**
```bash
# Before: labels come from hardcoded labels.go
query_swl {sql:"SELECT name, json_extract(metadata,'$.role') FROM entities WHERE type='File' AND name LIKE '%auth%'"}

# After: same result, but driven by YAML
# Add custom rule in ~/.swl/swl.rules.yaml:
#   - prefix: "pkg/billing/"
#     role: billing
#     domain: payments
# Run scan → files in pkg/billing/ get role:billing from YAML, not hardcoded
```

### Phase B.2 — Wire extraction limits through RulesEngine

**Files:** `extractor.go`, `rules.go`

**Changes:**
1. Add `EffectiveMaxSymbols()`, `EffectiveMaxImports()`, `EffectiveMaxTasks()`, `EffectiveMaxURLs()` to `Config` or `RulesEngine`
2. `extractor.go` — replace hardcoded `maxSymbols`, `maxImports`, etc. with calls to `m.cfg.effectiveMax...()` or read from `m.rules` if available
3. Load extraction limits from `m.rules` at extractor init

**Current hardcoded values to externalize:**
```
extractor.go:14-19  — maxSymbols=60, maxImports=40, maxTasks=30, maxSections=20, maxURLs=20
extractor.go:528    — maxUses=20 (noise symbol threshold)
extractor.go:630    — check truncation at 512 chars
extractor.go:137    — maxSize via m.cfg.effectiveMaxFileSize() ✅ already wired
query.go:1149       — SQL row cap at 200 ✅ already wired
session.go:207       — cold-start threshold <10 ✅ hardcoded but not critical
```

**Result:** All anti-bloat extraction limits configurable via `swl.rules.yaml`. Workspace can override without code changes.

### Phase B.3 — `swlignore` in config + `snapshot` mode

**Files:** `swl.rules.yaml`, `ignore.go`, `query.go`

**Changes:**
1. Add `ignore_patterns` section to `swl.rules.yaml` — currently `ignore.go` has hardcoded defaults but they should be configurable
2. Add `{snapshot:true}` query mode to `query.go` — returns combined view: anchor documents, semantic areas, content profile, explicit goals. Implementation: combine `askWorkspacePurpose` + `askSemanticAreas` + content type distribution into structured output.

### Phase B.4 — Cleanup dead code

**Files:** `labels.go`

**Changes:**
1. Remove `pathPrefixRules`, `namePatternRules`, `contentTypeRules` hardcoded vars from `labels.go` — they're now in YAML
2. Keep `DeriveLabels()` as thin wrapper that delegates to `rules.go`
3. Clean up `applyPathPrefix()`, `applyNamePattern()`, `applyContentType()` — they currently read hardcoded vars that should come from rules engine
4. Move `RulesEngine` label derivation logic from `labels.go` into `rules.go` (it's already partially there)

**Alternative:** Merge `labels.go` into `rules.go` entirely — `DeriveLabels` lives in `RulesEngine`, `labels.go` becomes empty or minimal forwarding. Cleaner separation: `rules.go` owns all config, `labels.go` is just types.

---

## 4. Scope Summary

### Phase B Completion (this work)

| Task | Effort | Priority | Verification |
|------|--------|----------|-------------|
| B1: Wire path/name/content rules through RulesEngine | Low | P1 | Custom YAML rule → applied to scanned files |
| B2: Wire extraction limits through RulesEngine | Low | P1 | Override maxSymbols in YAML → respected at extract time |
| B3: `swlignore` in config + `{snapshot:true}` mode | Low | P2 | `query_swl {snapshot:true}` → structured overview |
| B4: Cleanup dead hardcoded rules from `labels.go` | Low | P2 | `labels.go` path rules removed, YAML is source of truth |

### Deferred Items (backlog, not blocking)

| Task | Reason | Priority |
|------|--------|----------|
| B7: Tier 1 SQL-based ontological inference | Nice-to-have; path→label (Tier 0) covers current needs | P3 |
| Verification tests | No automated tests for Phase A criteria | P3 |
| Per-agent reliability profiling | Requires cross-agent evidence accumulation | P3 |

---

## 5. Behavioral Change

After Phase B completion:
- `labels.go` is driven by `swl.rules.yaml` — workspace can add custom path rules, name patterns, content types without touching Go code
- Extractor limits configurable — workspace can increase symbol budget for large files, decrease for constrained hardware
- `swlignore` patterns configurable — workspace can exclude custom directories
- `query_swl {snapshot:true}` works as explicit mode

**Zero behavioral change** for default configs (embedded YAML values match hardcoded originals). All improvement is in configurability and maintainability.

---

## 6. Document Changelog

| Date | Change |
|------|--------|
| 2026-05-08 | v5: Complete codebase audit reveals Phase B YAML was built but not wired. Root cause: YAML files created + loaded, but Go code still reads hardcoded originals. Phase B.1-B.4 now scoped as connection work. Gap G1 (labels never extracted) — confirmed NOT a bug, labels ARE extracted and persisted. Gap G6 (hardcoded values) — confirmed, quantified, scoped to 6 locations. |