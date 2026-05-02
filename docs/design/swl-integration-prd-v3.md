# SWL Native Integration — PRD v3

## Retrospective Challenge of v2 Implementation

This document captures the deep architectural retrospective requested after completing v2 (Phases 1–10), identifies what is good, what is bad, what is missing, and defines the v3 corrective plan and execution.

---

## v2 Audit: What Is Good

### Open type system
`EntityType = string` and `EdgeRel = string` as Go type aliases rather than enum constants. The `KnownType*` and `KnownRel*` constants are **conventions**, not restrictions. Any tool, any domain, any operator can emit arbitrary types and relations without touching picoclaw source code. This is the correct architecture for universal applicability.

### Import cycle boundary
`SWLHook` lives in `pkg/agent/swl_hook.go`, not in `pkg/swl`. The `pkg/swl` package contains only the Manager — pure data, storage, query, inference logic. `pkg/agent` imports `pkg/swl`, never the reverse. This is the correct unidirectional dependency.

### Three-layer inference principle
Layer 0 (operator-registered custom handlers) → Layer 1 (declarative toolMap for known picoclaw tools) → Layer 3 (ExtractGeneric catch-all for all other tools). The principle is correct: known tools get deep extraction, unknown tools get best-effort generic extraction. Execution needs improvement.

### Async hooks with WaitGroup drain
AfterTool and AfterLLM fire goroutines. Manager.Close() calls wg.Wait(). Zero latency added to agent turns, no goroutine leaks. Correct.

### SQLite WAL + single writer mutex
WAL mode allows concurrent reads from the web backend while the gateway writes. A single sync.Mutex serializes writes. Correct architecture for SQLite under concurrent load.

### 15 invariant tests
All five upsert invariants (confidence MAX, knowledge_depth MAX, method priority, fact_status immutability, deleted-terminal) are tested and passing. These are the bedrock of SWL correctness.

### Session hint over SKILL.md injection
The original v1 PRD proposed injecting the full SKILL.md (~3,000 tokens) into every agent context. v2 replaced this with a 60-token session hint and a rich query_swl tool description. This is the right direction. The tool description IS the documentation for the LLM.

---

## v2 Audit: What Is Bad or Missing

### 1. Manager has no workspace field — fragile scan path
`Manager.workspace` does not exist. `tool.go` derives workspace from dbPath via string manipulation:
```go
root = m.dbPath[:strings.LastIndex(m.dbPath, "/.swl")]
```
This fails if the workspace path contains `/.swl/` elsewhere, and is simply wrong architecture. The workspace root must be a first-class field.

### 2. No workspace-level Manager singleton registry
If two AgentInstances share the same workspace, each creates its own `*swl.Manager`, opening two write connections to the same SQLite file. SQLite WAL handles this at the DB level, but it defeats the write-mutex design and creates redundant concurrent extraction work.
**Fix**: Package-level registry mapping `dbPath → *Manager` with reference counting.

### 3. ExtractGeneric is too shallow
`ExtractGeneric` only extracts URLs from unknown tool results. For MCP tools, operator-registered tools, or any tool not in the declarative toolMap, the result is almost entirely ignored. This defeats the "universal" claim.
**Improvements**:
- Extract file paths mentioned in result text (absolute paths, relative paths matching workspace)
- Attempt JSON parsing: if result is JSON object/array, extract string values that look like paths or URLs
- Extract task-like strings (TODO/FIXME patterns) from result text

### 4. ExtractLLMResponse extracts too little
`ExtractLLMResponse` extracts tasks and URLs from the response text and tasks/reasoning from the thinking block. It misses:
- File paths mentioned in the response (e.g., "I'll edit `pkg/foo/bar.go`")
- Symbol names referenced in context of files (e.g., "the `parseConfig` function in config.go")
- Structured data in code blocks the LLM emits

### 5. contextWithTimeout dead code in decay.go
`contextWithTimeout()` is defined but never called. It also discards the cancel function (resource leak if it were called). Dead code should be removed.

### 6. `var _ =` suppressors in web/backend/api/swl.go
`swlShortName` and `strings.Contains` are suppressed with `var _ = ...` instead of being fixed or removed. This is a code smell.

### 7. noiseSymbols are English/Go-centric
The noise filter (`main`, `test`, `setup`, `init`, `get`, `set`, `run`, `new`, `String`, `Error`, `Close`) is biased toward Go and English idioms. It will incorrectly suppress valid symbols in other languages/frameworks. This should be narrowed to a minimal, truly universal set.

### 8. ExtractGeneric uses hardcoded `"Tool"` string literal
```go
edges: []EdgeTuple{{FromID: entityID(tool), Rel: KnownRelExecuted, ToID: toolEntityID}}
```
The `EntityType` for tool entities is hardcoded as `"Tool"` (a string literal) rather than a `KnownTypeTool` constant. This is an oversight in the open type system.

### 9. Session hint may be unnecessary
The 60-token session hint injects a minimal instruction into every system prompt. But the `query_swl` tool description already explains when to use it, including the `{"resume": true}` form. LLMs already understand: "call this tool at session start." The hint adds tokens for no additional behavioral change.

### 10. Hook interceptor scope is picoclaw-tool-centric
The declarative `toolMap` covers picoclaw's built-in tools. When operators register custom tools via the tool registry, or when MCP tools are mounted, PostHook falls through to `ExtractGeneric` — which is too shallow. The hook should be more aggressive at extracting value from any tool result that contains text.

---

## v3 Architecture: What Changes

### Manager gains workspace field
```go
type Manager struct {
    cfg       *Config
    workspace string    // NEW: absolute workspace root
    dbPath    string
    // ...
}
```
`NewManager(workspace, cfg)` derives `dbPath` from `workspace` and cfg, then stores both. All callers that need the workspace root use `m.workspace` directly.

### Package-level Manager registry
```go
// pkg/swl/registry.go
var (
    regMu    sync.Mutex
    registry = map[string]*managerEntry{}
)

type managerEntry struct {
    mgr  *Manager
    refs int
}

func AcquireManager(workspace string, cfg *Config) (*Manager, error)
func ReleaseManager(workspace string)
```
`NewAgentInstance` calls `AcquireManager`, `Close` calls `ReleaseManager`. The last caller to release triggers `Manager.Close()`.

### Improved ExtractGeneric
```go
func ExtractGeneric(tool, result string, sessionID string) *GraphDelta {
    // 1. Extract URLs (existing)
    // 2. Extract absolute file paths via regex
    // 3. Extract relative paths that match workspace pattern
    // 4. Extract TODO/FIXME task patterns
    // 5. If result is JSON: walk string values for paths/URLs
    // 6. Create Tool entity for the unknown tool
    // 7. Create executed edges for extracted entities
}
```

### Improved ExtractLLMResponse
```go
func ExtractLLMResponse(content, thinking string, sessionID string) *GraphDelta {
    // Existing: tasks + URLs from content
    // NEW: file path mentions via backtick or code block extraction
    // NEW: "the X function in Y.go" pattern → Symbol mention edge
    // NEW: code block extraction (```lang\ncode\n```) → Symbol extraction from code
    // thinking: cap confidence at 0.75 (existing), also extract file paths
}
```

### KnownTypeTool constant
```go
const KnownTypeTool EntityType = "Tool"
```
Used in `ExtractGeneric` instead of the string literal `"Tool"`.

### Minimal noiseSymbols
Reduce to universally-noise names only:
```go
var noiseSymbols = map[string]bool{
    "main": true, "init": true, "test": true,
}
```
Language-specific noise (`get`, `set`, `run`, `new`, `String`, `Error`, `Close`, `open`, `close`, `read`, `write`) removed — these are valid symbols in many codebases.

### Remove session hint injection
The `query_swl` tool description is the documentation. Remove the 60-token session hint from `pkg/swl/hint.go` and the injection wiring in `pkg/agent/instance.go`. The `hint.go` file and `AddInlineSystemContent` can be removed if no other caller needs them.

### Remove dead code
- `contextWithTimeout()` removed from `decay.go`
- `swlShortName` and `var _ =` suppressors removed from `web/backend/api/swl.go`

---

## v3 Implementation Plan

### Change 1: Add workspace field to Manager + fix scan path
Files: `pkg/swl/manager.go`, `pkg/swl/tool.go`

### Change 2: Create Manager registry
Files: `pkg/swl/registry.go` (new), `pkg/agent/instance.go` (use Acquire/Release)

### Change 3: Add KnownTypeTool, improve ExtractGeneric
Files: `pkg/swl/types.go`, `pkg/swl/extractor.go`

### Change 4: Improve ExtractLLMResponse
Files: `pkg/swl/extractor.go`

### Change 5: Remove dead code and suppressors
Files: `pkg/swl/decay.go`, `web/backend/api/swl.go`

### Change 6: Narrow noiseSymbols
Files: `pkg/swl/extractor.go`

### Change 7: Remove session hint (optional — keep for now behind config default-off)
Decision: Keep `hint.go` but change default to `inject_session_hint: false`. The tool description is sufficient.

---

## What Does NOT Change in v3

- Open type system (`EntityType = string`, `EdgeRel = string`) — correct
- Import cycle boundary (hook in `pkg/agent`) — correct  
- Three-layer inference principle — correct, only execution improves
- Async hooks with WaitGroup drain — correct
- SQLite WAL + single writer mutex — correct
- All 15 invariant tests — must continue to pass
- 60-token hint constant — kept but disabled by default

---

## Summary

v2 built a correct, working semantic knowledge graph with solid foundations. v3 closes the gap between "works for picoclaw built-in tools" and "universal for any tool in any domain": by improving generic extraction depth, fixing the Manager's workspace field, adding a singleton registry, and removing dead code and code smells. The open type system, which was correct from v2, is now fully leveraged by adding `KnownTypeTool` and removing English/Go-centric noise filters.
