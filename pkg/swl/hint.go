package swl

// SessionHint is injected into the system prompt when InjectSessionHint is true.
// Keep it dense but actionable — it runs on every turn and must pay for itself.
const sessionHint = `## Semantic Workspace Layer (SWL) — always active

A persistent knowledge graph records every file read/write, exec, and web fetch
automatically. It survives across sessions and accumulates depth over time.

Mandatory habits:
1. ALWAYS open sessions with: query_swl {"resume":true}
   → <50 tokens instead of re-reading stale context.
2. ALWAYS query_swl BEFORE reading any file:
   query_swl {"question":"symbols in pkg/foo/bar.go"}
   query_swl {"question":"open TODOs in src/"}
   → Saves ~3 000 tokens per file if knowledge is already indexed.
3. Check indexing coverage before deep-diving a directory:
   query_swl {"index_status":true}
   query_swl {"scan":true,"root":"."}
4. Capture insights that won't appear in file content:
   query_swl {"assert":"<fact>","subject":"<topic>","confidence":0.9}
   Assertions link to real workspace entities (File, Symbol, Directory) — not phantom notes.

Useful queries:
  query_swl {"question":"what imports <pkg>"}
  query_swl {"question":"files in <dir>"}
  query_swl {"gaps":true}          → knowledge gaps to fill
  query_swl {"stale":true}         → stale / changed files
  query_swl {"stats":true}         → graph health
  query_swl {"sql":"SELECT ..."}   → raw graph query (SELECT only)

Config self-improvement (no restart required):
  Edit {workspace}/.swl/swl.rules.yaml to tune extraction patterns.
  Then: query_swl {"reload_config":true}  → new rules active immediately.
  If query_swl returns ⚠ configuration warnings, fix the named file before continuing.`

// SessionHint returns the constant hint string.
func SessionHint() string { return sessionHint }
