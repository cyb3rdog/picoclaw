# SWL Refactor: Comprehensive Plan

> Verbatim requirements: `docs/swl/swl-refactor-requirements.md`
> Codebase analysis: `/root/.claude/plans/great-now-please-read-lovely-codd.md`

---

## Context

SWL was designed as a *generic, workspace-agnostic, self-improving knowledge graph* that gives LLMs verified facts about the workspace — preventing hallucination, drift, and unnecessary rediscovery. The current implementation drifted from that vision in five concrete ways:

| # | Drift | Symptom |
|---|-------|---------|
| 1 | **Bulk upfront extraction** | ~20k entities after scanning a small codebase; mostly never queried |
| 2 | **Hardcoded language/tool specifics** | Go/Python/JS/Rust patterns, tool names, project file names, command patterns — all compiled in |
| 3 | **No semantic description field** | Graph stores names and relationships, but can't answer "what does this file do?" |
| 4 | **Weak query system** | "Where is the file that does X?", "what is the project goal?" — both fail or return nonsense |
| 5 | **No real feedback loop** | LLM queries don't inform what gets extracted; `access_count` counts writes not reads; events table never written |

The goal is a **quick-win refactoring** that preserves the proven strengths (entity graph, upsert invariants, session continuity, Tier 1/2/3 query dispatch, decay checking) while fixing the five drifts above. The result must run on constrained hardware (RPi 0 2W: single-core ARMv6, 512 MB RAM).

---

## Guiding Principles

1. **Pull over Push** — extract knowledge when the LLM *touches* a file, not when SWL is initialized
2. **Config over Code** — extraction rules, ignore patterns, and semantic ontologies live in a config file the LLM can inspect and improve; no hardcoded language names in production paths
3. **Depth over Breadth** — fewer, richer, more verified entities beat thousands of shallow ones
4. **Feedback closes the loop** — what the LLM actually queries determines what gets extracted next
5. **Additive refactoring** — preserve all existing public APIs, invariants, and integration points; change the internals

---

## Phase 1 — Extraction Discipline (Critical, Quick Win)

**Problem:** `ScanWorkspace()` extracts every symbol from every file upfront. For a 100-file Go project this produces ~8,000–20,000 entities, most never touched by the LLM.

**Fix: Structural scan only; content on demand.**

### 1a. Split ScanWorkspace into two modes

`scanner.go` — change `ScanWorkspace()` to accept a `depth` parameter (or separate method):

```
ScanStructure(root)   → indexes only File + Directory entities (no content extraction)
ScanContent(filePath) → extracts symbols/imports/tasks/sections for one specific file
```

`ScanWorkspace()` (public API, preserved) becomes an alias for `ScanStructure`.

**What changes:**
- Remove the `ExtractContent()` call from the walk loop in `scanner.go` (lines ~128–191)
- Keep only: File entity upsert, Directory entity upsert, mtime + content hash storage, `in_dir` edge
- `ExtractContent()` is then called only from `inference.go` Layer 1 handlers (`postApplyReadFile`, `postApplyWriteFile`) — which already call it; nothing else needed there

**Impact:** A 100-file scan produces ~115 entities (100 files + 15 dirs) instead of ~8,000+. Content is still extracted — just lazily, when the LLM actually reads a file.

### 1b. Add lazy-extraction tracking

Add a boolean column `content_extracted` to the entities table (or use `knowledge_depth == 1` as the proxy — it already means "surface observation").

When the LLM queries `query_swl {question: "functions in foo.go"}` and the file has never been read by a tool call, `askSymbols()` should return a note: *"foo.go has not been read yet; use read_file to populate its symbols."*

This prevents silent empty answers and teaches the LLM the pull model.

**Files:** `scanner.go`, `db.go` (optional column), `query.go` (askSymbols, askImports, askTasks)

---

## Phase 2 — Config-Driven Rule System

**Problem:** Skip dirs, skip extensions, symbol patterns, import patterns, project-type detection, tool-name mappings — all hardcoded in `scanner.go` and `extractor.go`. SWL cannot be adapted to new languages or workspace types without recompiling.

**Fix: A `swl-rules.json` config file per workspace (or embedded in main SWL config).**

### 2a. Define the Rules schema

New file: `pkg/swl/rules.go` — defines `SWLRules` struct:

```go
type SWLRules struct {
    // Scanning
    IgnoreDirs       []string            // default: .git, node_modules, vendor, ...
    IgnoreExtensions []string            // default: .png, .exe, .zip, ...
    IgnorePatterns   []string            // glob patterns: *_generated.go, *.pb.go

    // Per-extension extraction rules
    FileRules        map[string]FileRule  // key: ".go", ".py", ".ts", ...
    DefaultFileRule  FileRule            // fallback for unknown extensions

    // Tool interception rules
    ToolRules        []ToolRule          // replaces hardcoded toolMap

    // Project type detection
    ProjectTypeMarkers map[string]string  // filename → project type: "go.mod" → "go"
}

type FileRule struct {
    ExtractSymbols   bool
    ExtractImports   bool
    ExtractTasks     bool
    ExtractSections  bool
    ExtractURLs      bool
    SymbolPatterns   []string   // RE2 patterns, capture group 1 = symbol name
    ImportPatterns   []string   // RE2 patterns, capture group 1 = import path
    MaxSymbols       int
    MaxImports       int
    MaxTasks         int
}

type ToolRule struct {
    ToolName    string     // exact match or glob
    FileArgKey  string     // which arg key holds the file path ("path", "file_path", ...)
    ContentKey  string     // which key holds content ("content", "result", ...)
    ExtractMode string     // "file_write" | "file_read" | "exec" | "web" | "generic"
}
```

### 2b. Built-in rules as embedded JSON

Ship a `rules_default.json` embedded via `go:embed`. This contains exactly what is currently hardcoded. No behavior change at default config.

### 2c. Workspace-level override

SWL looks for `{workspace}/.swl/rules.json`. If present, deep-merges with defaults (user rules extend, not replace, built-ins — unless `override: true` is set at the top level).

### 2d. LLM can inspect and propose rule changes

`query_swl {question: "schema"}` (already exists) should also return the active rules summary. `query_swl {assert: ..., type: "Rule"}` could be the hook for future self-improvement (Phase 5).

**Files:** `pkg/swl/rules.go` (new), `scanner.go`, `extractor.go`, `inference.go`, `config.go`

**Migration:** Replace all `skipDirs`, `skipExts`, hardcoded symbol/import regexes, `toolMap`, and `projectTypeMarkers` with reads from the active `SWLRules`. Existing `Config.ExtractSymbolPatterns` becomes `Config.RulesPath` (path to override file) plus the per-`FileRule.SymbolPatterns` mechanism.

---

## Phase 3 — Semantic Description Field

**Problem:** The entity graph stores names and relationships but has no "what does this do?" field. Queries like "where is the file that does X?" fall through to Tier 3 name-matching, which is useless unless the file name hints at its purpose.

**Fix: Store a `description` in entity metadata; extract it from source on first read; make it searchable.**

### 3a. Description extraction

In `extractor.go`, `ExtractContent()`:
- For each file, attempt to extract a *one-line description*:
  - **Go**: first `// Package ...` comment or first exported function/type's doc comment
  - **Python**: module-level docstring (first `"""` or `'''`)
  - **Markdown**: first paragraph after `# Heading`
  - **Generic fallback**: first non-empty comment line that isn't a license header
- Store as `metadata["description"]` on the File entity

### 3b. AssertNote becomes the primary improvement path

The LLM can already call `query_swl {assert: "Handles JWT auth", subject: "auth.go"}`. Surface this more prominently in the `query_swl` tool description so the LLM knows to do it when it reads a file and discovers its purpose.

### 3c. Tier 3 searches description

In `query.go` `tryTier3()`, extend the SQL to also match `metadata` JSON:

```sql
SELECT type, name, fact_status, metadata FROM entities
WHERE fact_status != 'deleted'
  AND (name LIKE '%term%' OR metadata LIKE '%term%')
ORDER BY knowledge_depth DESC, access_count DESC LIMIT 15
```

This makes "where is the file that does JWT auth?" match files where the LLM (or extractor) stored "JWT auth" in the description.

**Files:** `extractor.go`, `query.go`, `rules.go` (description extraction config per FileRule)

---

## Phase 4 — Query Gap Fixes

**Problem:** Several high-value queries return nothing or wrong answers. The query system is powerful but under-connected to the data that exists.

### 4a. Fix "project goal" query

Add Tier 1 pattern to `query.go`:
```go
`(?i)(?:project\s+goal|what\s+is\s+(?:the\s+)?(?:goal|purpose|aim|objective))` →
```
Handler: query `sessions` table for the most recent non-null `goal`, plus any `Note` entities with `type=Intent`. Return formatted result.

### 4b. Fix "what does file X do?" query

Add Tier 1 pattern:
```go
`(?i)(?:what\s+does\s+(.+?)\s+do|describe\s+(.+?)|purpose\s+of\s+(.+?))`
```
Handler: look up the named file entity, return its `metadata["description"]` + top 5 symbols + top 3 tasks.

### 4c. Promote "assert" as a teaching tool

When `query_swl` returns an entity with no description and `knowledge_depth == 1`, append a suggestion: *"Use `{assert: \"...\", subject: \"filename\"}` to record what this file does."*

### 4d. Fix SessionResume() usefulness

Add to the resume digest:
- Top 5 recently modified files (by `modified_at`)
- Top 3 open tasks (by access_count)
- Files with `knowledge_depth == 1` count (how many files have never been read)

**Files:** `query.go`, `session.go`

---

## Phase 5 — Feedback Loop & Self-Improvement Foundation

**Problem:** The LLM's queries don't feed back into extraction priorities. `access_count` counts writes, not reads. The events and constraints tables are never used.

### 5a. Fix access_count semantics

In `query.go`, after every query that returns entity results, increment `accessed_at` and `access_count` for those entities:

```sql
UPDATE entities SET access_count = access_count + 1, accessed_at = ? WHERE id IN (...)
```

This makes `access_count` mean "times this entity was actually returned to the LLM" — a real signal.

### 5b. Activate the events table

In `inference.go` `PostHook()`, insert a row into `events` for each tool call:
```go
INSERT OR IGNORE INTO events (id, session_id, tool, phase, args_hash, ts)
VALUES (?, ?, ?, 'post', ?, ?)
```
This enables `maybePrune()` to actually function, and enables future analytics: "which tools are called most?", "what files are accessed most?"

### 5c. Add "most queried" analytics to Stats()

In `query.go` `Stats()`, add a section:
```
Most accessed entities (top 5 by access_count):
  pkg/auth/jwt.go          [File] accessed 12x
  ParseConfig              [Symbol] accessed 8x
  ...
```

This gives the LLM signal about what's worth knowing.

### 5d. Constraints table: foundation only (no-op in Phase 5)

Document the intended use in a comment in `db.go`. Add a `LoadConstraints()` stub in `manager.go` that reads from the table but does nothing yet. This sets up Phase 6 (future: rule-driven auto-extraction triggers).

**Files:** `query.go`, `inference.go`, `manager.go`, `db.go`

---

## What is NOT in scope for this refactoring

These are future phases, deliberately excluded to keep scope manageable:

- Full NLP/embedding-based semantic search ("find files semantically similar to X")
- LLM-in-the-loop extraction (calling an LLM to generate descriptions at scan time)
- Distributed / multi-workspace graph federation
- Schema versioning / migrations
- Real-time constraint enforcement (Phase 5d is foundation only)
- Auto-generated rules from usage patterns (self-learning Phase 2)

---

## Execution Order

| Phase | Files Changed | Effort | Impact |
|-------|--------------|--------|--------|
| **1a** Structural-only scan | `scanner.go` | S | ★★★★★ Eliminates 95% of entity bloat |
| **1b** Lazy-extraction notice | `query.go` | XS | ★★★ Teaches LLM the pull model |
| **4a** Project goal query | `query.go` | XS | ★★★ Fills obvious gap |
| **4d** Better SessionResume | `session.go` | S | ★★★ More useful context for LLM |
| **3a** Description extraction | `extractor.go` | S | ★★★★ Enables semantic queries |
| **3c** Tier 3 searches metadata | `query.go` | XS | ★★★ Makes description searchable |
| **4b** "What does X do?" query | `query.go` | XS | ★★★ Uses description field |
| **5a** Fix access_count | `query.go`, `entity.go` | S | ★★★ Real read signal |
| **5b** Activate events | `inference.go` | XS | ★★ Enables prune + analytics |
| **5c** Most queried in Stats | `query.go` | XS | ★★ LLM sees what's hot |
| **2a–2d** Config-driven rules | `rules.go` (new), `scanner.go`, `extractor.go`, `inference.go` | L | ★★★★★ Makes SWL truly generic |

Start with Phase 1a + 4a + 4d (structural scan + query fixes) as the immediate quick win. These are independent of each other and of the rules system, and together address the most painful issues.

---

## Verification

After Phase 1a:
- Index a known small Go project (`query_swl {scan: true}`) — entity count should be ~files + dirs, not 20k
- Trigger a read_file on one file — entity count should increase by ~symbols in that file
- `query_swl {stats: true}` should show low entity counts and `knowledge_depth=1` for unread files

After Phase 3 + 4:
- `query_swl {question: "what is the project goal?"}` should return the session goal if set
- `query_swl {question: "where is the file that does authentication?"}` should return files with "authentication" in their description after those files have been read
- `query_swl {question: "what does auth.go do?"}` should return description + symbols

After Phase 2 (rules):
- Add `.tf` files to `FileRule` in `rules.json` with Terraform symbol patterns — should extract `resource` blocks without any code change
- Add a custom ignore dir — should be respected on next scan

After Phase 5:
- Call `query_swl {stats: true}` after querying several symbols — `access_count` on those entities should be > 0
- `query_swl {debug: true}` should show recent events from the events table
