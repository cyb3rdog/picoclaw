# SYNTH-CORE Integration Guide

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        PICOCLAW AGENT                            │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────────────┐ │
│  │   Python    │    │     Go      │    │    SWL (Go)          │ │
│  │ brain_engine│◄──►│brain_engine │◄──►│   pkg/swl/           │ │
│  │    .py      │    │    .go      │    │                      │ │
│  └─────────────┘    └─────────────┘    │  • manager.go       │ │
│         │                   │            │  • synth_core.go     │ │
│         │                   │            │  • query.go          │ │
│         ▼                   ▼            │  • inference.go      │ │
│  ┌──────────────────────────────────┐    │  • scanner.go        │ │
│  │     SYNTH-CORE Framework         │    └─────────────────────┘ │
│  │                                    │              │            │
│  │  • EntropyBudget (ΔS ≤ C)        │              ▼            │
│  │  • FixedPoint (‖ψ_t+1 - ψ_t‖ < δ) │    ┌─────────────────┐    │
│  │  • GoalTracking (backward chain)  │    │   SQLite DB     │    │
│  │  • ConflictDetect (saddles)       │    │   .swl/swl.db   │    │
│  │  • ChronosVector (S/E sync)       │    └─────────────────┘    │
│  └──────────────────────────────────┘                             │
└─────────────────────────────────────────────────────────────────┘
```

## Two Integration Paths

### Path A: Python Brain Engine (memory/brain_engine.py)

**Use when:**
- Running picoclaw agent loop
- Need LLM-agnostic KG operations
- JSON persistence is acceptable
- Lower latency preferred (no DB overhead)

**Location:** `memory/brain_engine.py`

**Usage:**
```python
from brain_engine import BrainEngine

be = BrainEngine()
be.assert("user", "name", "Alice", confidence=1.0)
results = be.query("user")
```

### Path B: SWL Go Layer (pkg/swl/)

**Use when:**
- Integrating with picoclaw source code
- Need SQLite persistence
- Complex entity extraction (symbols, imports, tasks)
- Hook-based inference pipeline

**Location:** `projects/cyb3rclaw/src/pkg/swl/`

**Usage:**
```go
mgr, _ := swl.NewManager("/workspace", nil)
mgr.PostHook(sessionKey, "read_file", args, result)
```

## SYNTH-CORE v10 Principles

| Principle | Python (`brain_engine.py`) | Go (`pkg/swl/synth_core.go`) |
|-----------|---------------------------|------------------------------|
| **Entropy Budget** | `entropy_budget`, `max_iterations` | `EntropyMonitor` struct |
| **Fixed Point** | `check_fixed_point()` | `CheckStability()` |
| **Goal Tracking** | `goals` dict + `update_goal()` | `GoalTracker` struct |
| **Conflict Detection** | `conflicts` list + `resolve()` | `ConflictDetector` struct |
| **Chronos Vector** | `chronos` dict | `ChronosVector` struct |
| **Graft Engine** | Manual cross-domain | `GraftEngine` struct |

## Integration Points

### 1. Agent Loop Integration (Pre-Response)

```python
# Before LLM call - inject KG context
be = BrainEngine()
context = be.format_for_prompt()
prompt = f"{context}\n\nUser: {user_input}"
```

### 2. Tool Result Processing (Post-Tool)

```go
// In SWL hook PostHook
mgr.PostHook(sessionKey, toolName, args, result)

// Check for conflicts
conflict := synthCore.Conflicts.DetectConflict(entity, sessionID)
if conflict != nil {
    // Flag for resolution
}
```

### 3. Session Start/End (Chronos Vector)

```python
# Session start
be.assert(f"session_{session_id}", "status", "S")  # Start token

# Session end  
be.assert(f"session_{session_id}", "status", "E")  # End token
```

## Database Schema (SWL + SYNTH-CORE)

```sql
-- Core SWL tables
CREATE TABLE entities (...);
CREATE TABLE edges (...);
CREATE TABLE sessions (...);

-- SYNTH-CORE extensions
CREATE TABLE goals (...);           -- Goal tracking
CREATE TABLE temporal_edges (...);  -- Chronos vector
CREATE TABLE conflicts (...);       -- Saddle points
CREATE TABLE entropy_log (...);     -- Budget tracking
CREATE TABLE grafts (...);          -- Cross-domain mappings
```

## SWL Tool Map (Declarative Inference)

```go
var toolMap = map[string]declRule{
    "write_file":  {entityType: KnownTypeFile, rel: KnownRelWrittenIn},
    "edit_file":   {entityType: KnownTypeFile, rel: KnownRelEditedIn},
    "read_file":   {entityType: KnownTypeFile, rel: KnownRelRead},
    "delete_file": {entityType: KnownTypeFile, rel: KnownRelDeleted},
    "exec":        {entityType: KnownTypeCommand, rel: KnownRelExecuted},
    "web_fetch":   {entityType: KnownTypeURL, rel: KnownRelFetched},
}
```

## Graft Engine Invariants

```
Thermodynamics          ↔  Knowledge Inference
  ΔS = k · log(N_states)     ΔS_KB = Σ p_i · log(1/p_i)

Fluid Dynamics          ↔  Context Propagation
  ∇P = ρ · g · h             ∇Attention = Σ w_i · context_i

Cellular Automata        ↔  KB Evolution
  Rule 110 universality     Graph propagation
```

## Testing

```bash
# Python Brain Engine
cd memory
python test_brain_engine.py

# Go SWL SYNTH-CORE
cd projects/cyb3rclaw/src
go test ./pkg/swl/... -v
```

## Files Reference

| File | Location | Purpose |
|------|----------|---------|
| `brain_engine.py` | `memory/` | Python KG engine |
| `brain_engine.go` | `workspace/` | Go wrapper for Python |
| `synth_core.go` | `pkg/swl/` | SYNTH-CORE Go layer |
| `synth_core_schema.sql` | `pkg/swl/` | DB migration |
| `synth_core_test.go` | `pkg/swl/` | Unit tests |
| `EXAMPLES.md` | `pkg/swl/` | Usage examples |

---

*Generated: 2026-05-08*  
*SYNTH-CORE v10 Compliant*
