# SWL Phase B — Rules Engine Specification

> Status: **DRAFT** | Date: 2026-05-07
> Based on: SWL-DESIGN.md + user alignment on "dynamic engine not pattern catalog"
> Replaces: prior Phase B plan (pattern catalog approach)

---

## Core Concept: Rules Engine vs. Pattern Catalog

### What it's NOT

A pattern catalog — explicit mappings for every possible path/pattern/language:

```
❌ pkg/auth/    → role: authentication
   pkg/db/      → domain: data-access
   pkg/api/     → role: api
   src/billing/ → role: billing
   
   Problem: Must catalog every language/framework. Scales infinitely.
```

### What it IS

A dynamic derivation engine — generic framework with configurable signals:

```
✅ The engine applies derivation methods to workspace signals.
   The workspace configures WHICH signals to look for.
   The engine determines WHAT those signals mean — generically.
   
   Works for: Go (pkg/), Python (src/), Rust (src/), Java (src/main/), etc.
   Without: separate explicit mappings for each.
```

---

## Architecture

```
WORKSPACE SCAN
     │
     ▼
┌─────────────────────────┐
│   SIGNAL EXTRACTION     │  ← Observables from file/directory
│   ─────────────────     │
│   - file_extension      │
│   - naming_pattern      │
│   - directory_profile   │
│   - anchor_doc_present  │
│   - import_patterns     │
└──────────┬──────────────┘
           │ signals
           ▼
┌─────────────────────────┐
│   DERIVATION ENGINE      │  ← Generic derivation methods
│   ─────────────────     │
│   - match_dominant      │     "derive role from dominant convention"
│   - cluster_by_role     │     "group files by naming cluster"
│   - aggregate_profile   │     "derive area role from file distribution"
│   - propagate_down      │     "files inherit directory role"
└──────────┬──────────────┘
           │ labels + confidence
           ▼
┌─────────────────────────┐
│   ENTITY ANNOTATION     │  ← Labels stored in entity metadata
│   ─────────────────     │
│   role: "authentication"│
│   domain: "security"     │
│   kind: "test"          │
│   _confidence: 0.85      │
└─────────────────────────┘
```

---

## Signal Types (Fixed Set)

The engine has a fixed set of signal types. This set never grows:

| Signal | Observable | Confidence | Notes |
|--------|-----------|------------|-------|
| `file_extension` | `.go`, `.py`, `.sql` | 0.9 | Direct mapping to content_type |
| `naming_pattern` | `*_test.go`, `*middleware*` | 0.8 | Workspace-configured patterns |
| `directory_role` | Files in dir share convention | 0.85 | Derived, not hardcoded |
| `anchor_doc_present` | README.md in dir | 0.7 | Documented area signal |
| `import_pattern` | Imports from `pkg/auth/` | 0.75 | Dependency-based role inference |
| `path_depth` | `cmd/`, `pkg/`, depth | 0.6 | Structural role signal |

---

## Derivation Methods (Fixed Set)

These methods apply to any signal. This set never grows:

| Method | Input | Output | Example |
|--------|-------|--------|---------|
| `match_extension` | extension signal | content_type label | `.sql` → `content_type: sql` |
| `match_pattern` | naming_pattern signal | kind label | `*_test.go` → `kind: test` |
| `derive_from_directory` | directory_role signal | propagate role to files | `pkg/auth/` files → `role: auth-related` |
| `aggregate_profile` | directory file distribution | SemanticArea role | Dir with mostly `.tf` → `domain: infrastructure` |
| `cluster_naming` | multiple files same pattern | role cluster | `*middleware*` files cluster → `role: cross-cutting` |

---

## Config Structure

### `swl.rules.yaml` — Signal Definitions (what to look for)

```yaml
meta:
  version: "1.0"
  workspace_type: software  # software | research | config | mixed

# ─── Signal Definitions ───
# These are SWITCHES — turn on/off, configure patterns.
# The engine applies GENERIC derivation methods to these signals.

signals:
  # Built-in: always active
  file_extension:
    enabled: true
    # No config needed — extension → content_type is universal
    
  naming_pattern:
    enabled: true
    patterns:
      # Format: {pattern} → {label}
      - pattern: ".*_test\\.(go|py|js|ts|rs|java)$"
        labels:
          kind: test
      - pattern: ".*_(mock|fake|stub)\\.(go|py|js)$"
        labels:
          kind: mock
      - pattern: ".*middleware.*\\.(go|js|ts)$"
        labels:
          role: middleware
      # Workspace can add more patterns here
      - pattern: ".*config.*\\.(yaml|yml|toml|json)$"
        labels:
          role: configuration
          kind: config-file
      
  directory_role:
    enabled: true
    strategy: dominant_convention  # dominant_convention | anchor_doc | hybrid
    # Strategy: derive directory role from:
    # - dominant_convention: files in dir sharing a naming pattern
    # - anchor_doc: README/OVERVIEW present
    # - hybrid: both, weighted

  anchor_doc:
    enabled: true
    patterns:
      - "README*"
      - "OVERVIEW*"
      - "ARCHITECTURE*"
      - "CHANGELOG*"
    confidence_boost: 0.1  # Add to any derived labels if anchor present
      
  path_depth:
    enabled: true
    depth_roles:
      1:  # top-level dirs
        "cmd":    { kind: entry-point, role: cli }
        "pkg":    { domain: code }
        "src":    { domain: code }
        "lib":    { domain: library }
        "internal": { visibility: internal }
      2:
        "auth":   { role: authentication, domain: security }
        "api":    { role: api, domain: networking }
        "db":     { role: persistence, domain: data-access }
        "data":   { role: persistence, domain: data-access }
        "web":    { role: ui, domain: presentation }
        "ui":     { role: ui, domain: presentation }
    # NOTE: This IS a mapping, but it's ONLY for top structural roles.
    # Deep paths derive from convention, not this map.

# ─── Ignores ───
ignores:
  dirs: [".git", "node_modules", "vendor", ".venv", "__pycache__", ".venv", "dist", "build"]
  extensions: [".png", ".jpg", ".gif", ".ico", ".pdf", ".zip", ".tar", ".gz", ".exe", ".bin"]
  patterns: ["*.log", "*.tmp", "*.lock", ".DS_Store", "Thumbs.db"]

# ─── Extraction Limits ───
extraction:
  max_file_size_bytes: 524288  # 512KB
  symbols:
    max_per_file: 60
    patterns:
      go: "\\bfunc\\s+(\\w+)"
      py: "\\bdef\\s+(\\w+)"
      js: "(?:function\\s+(\\w+)|const\\s+(\\w+)\\s*=)"
  imports:
    max_per_file: 40
  tasks:
    max_per_file: 30
  sections:
    max_per_file: 20
  urls:
    max_per_file: 20
```

### Why path_depth uses a small mapping

It's NOT a pattern catalog. It's a **structural convention** lookup:
- `cmd/` is a universal Go convention for entry points
- This is not "cataloguing every language" — it's ONE convention for ONE structural depth level
- Deep paths (e.g., `pkg/auth/middleware/`) derive from the naming convention inside, not from an explicit mapping

---

## How Derivation Works (Step by Step)

### Step 1: Signal Extraction (scan-time)

For each file/directory, extract signals:

```
File: pkg/auth/middleware.go
  signals:
    file_extension:   [".go"]
    naming_pattern:    ["middleware", "*middleware*"]
    path:              "pkg/auth/middleware.go"
    path_depth:        3  (pkg/auth/middleware.go)
    parent_dirs:       ["pkg", "pkg/auth"]
    anchor_doc_in_dir: false  (no README in pkg/auth/)
```

```
Directory: pkg/auth/
  signals:
    file_extensions:   [".go"] (dominant: 100%)
    naming_patterns:   ["middleware", "auth"]
    path_depth:        2
    child_count:       5
    anchor_doc_present: false
```

### Step 2: Apply Derivation Methods

The engine applies generic methods to the signals:

```go
// match_extension: .go → content_type: go-code
derive: {content_type: "go-code", confidence: 0.9}

// match_pattern: "*middleware*" → kind: middleware, role: cross-cutting
derive: {role: "middleware", kind: "cross-cutting", confidence: 0.8}

// derive_from_directory:
//   Parent dir "pkg/auth/" has role "auth" (derived from directory_role strategy)
//   File inherits: role: "auth-middleware"
derive: {role: "auth-middleware", confidence: 0.85}

// aggregate_profile (for SemanticArea):
//   Dir "pkg/auth/" has 5 .go files with naming pattern "auth/middleware"
//   Dominant convention: auth + middleware → derive role and domain
derive: {role: "authentication", domain: "security", confidence: 0.85}
```

### Step 3: Annotate Entity

```sql
-- Entity metadata (JSONB)
{
  "role": "auth-middleware",
  "role_origins": ["name_pattern:middleware", "directory_inherit:pkg/auth"],
  "domain": "security",
  "domain_origins": ["directory_profile:pkg/auth"],
  "kind": "cross-cutting",
  "content_type": "go-code",
  "_confidence": 0.85
}
```

---

## Query Engine Integration

### `swl.query.yaml` — Intent Patterns

```yaml
meta:
  version: "1.0"

intents:
  - id: find_by_purpose
    description: "Where is the file/component that does X"
    patterns:
      - "(?i)where\\s+(?:is|are)\\s+(?:the\\s+)?(.+?)\\s+(?:that\\s+)?(?:does|handles|implements|for|logic|code)"
      - "(?i)find\\s+(?:the\\s+)?(.+?)\\s+(?:file|code|logic|handler)"
      - "(?i)where\\s+(?:is|are)\\s+(?:the\\s+)?(.+?)\\s+(files?|tests?)"
    search:
      strategy: label_match  # label_match | name_search | freetext
      search_fields:
        - metadata.role
        - metadata.domain
        - metadata.kind
      weights:
        exact_match: 1.0
        partial_match: 0.6
        name_fallback: 0.4
    response:
      format: "labeled_results"
      max_results: 10
      show_labels: true
      
  - id: find_by_kind
    description: "Find files by kind (test, mock, config, etc.)"
    patterns:
      - "(?i)where\\s+(?:are|is)\\s+(?:the\\s+)?(.+?)\\s+(files?|tests?)"
      - "(?i)find\\s+(?:all\\s+)?(.+?)\\s+(files?|tests?)"
    search:
      strategy: label_match
      search_fields:
        - metadata.kind
      weights:
        exact_match: 1.0
        
  - id: workspace_purpose
    description: "What is this workspace/project for"
    patterns:
      - "(?i)what\\s+(?:is|are)\\s+(?:this\\s+)?(?:workspace|project|codebase)\\s+(?:for|about|goal)"
      - "(?i)describe\\s+(?:this\\s+)?(?:workspace|project|codebase)"
    search:
      strategy: anchor_doc
      entity_types: [AnchorDocument]
    response:
      format: "descriptions"
      max_results: 3
      
  - id: area_contents
    description: "What is in area X"
    patterns:
      - "(?i)what\\s+(?:is|are)\\s+(?:in|inside)\\s+(?:the\\s+)?(.+?)\\s+(area|dir|directory|folder)"
      - "(?i)show\\s+(?:me\\s+)?(?:the\\s+)?contents\\s+(?:of\\s+)?(?:the\\s+)?(.+?)\\s+(area|dir|directory)"
    search:
      strategy: semantic_area
      search_fields:
        - name
        - metadata.role
        - metadata.domain
    response:
      format: "area_tree"
```

---

## Phase B vs. Phase A.2 — What's Different

| Aspect | Phase A.2 (hardcoded) | Phase B (externalized) |
|--------|---------------------|------------------------|
| Signal definitions | Go patterns hardcoded in scanner.go | `swl.rules.yaml` signals section |
| Derivation methods | Hardcoded in scanner.go | Fixed methods in rules.go engine |
| Naming patterns | `*_test.go`, `*middleware*` hardcoded | YAML config, workspace-extendable |
| Directory role strategy | Hardcoded logic | YAML configurable (`dominant_convention` vs `anchor_doc`) |
| Path depth mapping | Hardcoded path → role | YAML `path_depth.depth_roles` section |
| Ignores | Hardcoded in scanner.go | YAML `ignores` section |
| Query patterns | Hardcoded regex in query.go | `swl.query.yaml` patterns section |
| Workspace override | None | Deep-merge workspace YAML with embedded defaults |

**Phase A.2 proves it works.** Phase B makes it configurable and maintainable.

---

## New Files

| File | Purpose |
|------|---------|
| `pkg/swl/rules.go` | Rules engine: loads YAML, applies derivation |
| `pkg/swl/derivation.go` | Generic derivation methods (match_extension, derive_from_directory, aggregate_profile, cluster_naming) |
| `pkg/swl/rules_default.yaml` | Embedded defaults (Go software project signals) |
| `pkg/swl/query_engine.go` | Intent dispatcher reading `swl.query.yaml` |
| `pkg/swl/query_default.yaml` | Embedded defaults (Phase A.3 query patterns) |
| `pkg/swl/signal.go` | Signal extraction (scan-time observables) |

---

## Modified Files

| File | Change |
|------|--------|
| `pkg/swl/scanner.go` | Use rules engine for label derivation; remove hardcoded patterns |
| `pkg/swl/query.go` | Use query engine for intent dispatch; remove hardcoded patterns |
| `pkg/swl/manager.go` | Wire rules engine and query engine on init |
| `pkg/swl/config.go` | Add `RulesPath`, `QueryPath` fields |
| `pkg/config/swl.go` | Add YAML path fields to SWLToolConfig |

---

## Verification

| Criterion | Method |
|-----------|--------|
| Entity output identical: Phase A.2 vs Phase B | Automated diff test |
| Add custom pattern in YAML → applied | Add `*-handler.*\.go$` → `role: handler` |
| `.tf` files → `content_type: hcl` | Add terraform pattern to rules.yaml |
| Custom path_depth rule → applied | Add `srv/` → `role: service` |
| Intent pattern via YAML → dispatched | Add custom intent → query dispatched |
| Workspace override deep-merges | Partial override + verify merged config |
| Zero behavioral change without override | Default config matches Phase A.2 output |

---

## The Key Principle

> **The rules engine is a framework, not a dictionary.**
> It has a fixed set of signal types and derivation methods.
> It has a configurable set of signal definitions.
> This design works for any language/framework without growing the code.
