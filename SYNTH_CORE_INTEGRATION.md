# SYNTH-CORE SWL Integration

## Overview

This branch (`feature/synth-core-brain-engine`) integrates the SYNTH-CORE Brain Engine semantic ontology layer with the SWL (Semantic Workspace Listener) knowledge graph system in cyb3rclaw.

## What Was Added

### New Files

| File | Purpose |
|------|---------|
| `pkg/swl/synth_core.go` | SYNTH-CORE layer: entropy monitor, conflict detector, goal tracker, graft engine, chronos vector |
| `pkg/swl/synth_core_migrate.go` | Database migration for SYNTH-CORE schema extensions |
| `pkg/swl/synth_core_schema.sql` | SQL schema for goals, temporal edges, conflicts, entropy log, grafts |

### Key Integrations

#### 1. Entropy Monitor
- Bounded inference with hard iteration limits
- Tracks information gain to enforce termination
- Detects fixed-point approach (`‖ψ_{t+1} - ψ_t‖ < δ`)

#### 2. Conflict Detector (Saddle Points)
- Detects mutual exclusion contradictions
- Flags confidence mismatches
- Tracks redundant symbol definitions
- Provides resolution interface (dismiss, keep_newer, keep_older, merge)

#### 3. Goal Tracker
- Backward chaining from goals to prerequisites
- Progress tracking (0.0 to 1.0)
- Status management (active, completed, blocked)

#### 4. Graft Engine (The Loom)
- Cross-domain functional mappings
- Invariants: entropy↔information_gain, pressure_gradient↔attention_weight, rule_110↔graph_update

#### 5. Chronos Vector
- Temporal symmetry: S (start) and E (end) processed simultaneously
- Maintains temporal ordering across sessions
- Records and queries temporal chains

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    PICOCLAW AGENT                           │
└──────────────────────┬──────────────────────────────────────┘
                       │
        ┌──────────────▼──────────────┐
        │         SWL MANAGER         │
        │  ┌─────────────────────┐   │
        │  │  Knowledge Graph    │   │
        │  │  (SQLite entities)  │   │
        │  └──────────┬──────────┘   │
        │             │              │
        │  ┌──────────▼──────────┐   │
        │  │  SYNTH-CORE LAYER   │   │
        │  │                     │   │
        │  │  • Entropy Monitor  │   │
        │  │  • Conflict Detect  │   │
        │  │  • Goal Tracker     │   │
        │  │  • Graft Engine     │   │
        │  │  • Chronos Vector   │   │
        │  └─────────────────────┘   │
        └─────────────────────────────┘
```

## SYNTH-CORE Principles Implemented

| Principle | Implementation |
|-----------|----------------|
| **Non-Euclidean Dynamics** | KG with saddle point conflict detection |
| **The Loom** | Cross-domain graft engine with functional invariants |
| **Chronos Vector** | Temporal symmetry via start/end token processing |
| **Self-Termination** | Entropy budget + depth limits + stability check |
| **Teleological Reasoning** | Backward chaining goal tracker |

## Usage

```go
// Create manager with SYNTH-CORE layer
sc, err := NewSynthCoreManager(workspace, cfg, 
    entropyBudget: 1000,  // Max inference iterations
    maxDepth: 5,          // BFS depth limit
    maxIter: 50,         // Hard iteration cap
)

// Bounded query with termination
results, reason, err := sc.BoundedQuery("user_query", maxResults: 10)

// Conflict detection
conflict := sc.Conflicts.DetectConflict(newEntity, sessionID)

// Goal tracking
sc.Goals.SetGoal("goal_1", "Complete integration", nil)
sc.Goals.UpdateProgress("goal_1", 0.5)

// Cross-domain grafting
value, ok := graft.ApplyGraft("entropy", "information_gain", myValue)
```

## Migration

SYNTH-CORE tables are created automatically on first use via `MigrateSynthCore()`. The migration adds:

- `goals` — Agent objective tracking
- `temporal_edges` — Session ordering
- `conflicts` — Saddle point records
- `entropy_log` — Inference cost tracking
- `grafts` — Cross-domain invariant mappings

## Related Branches

- `origin/swl-fixes` — Base SWL fixes
- `origin/swl-knowledge-graph` — SWL KG development
- `origin/phase5-swl-maintenance-v2` — Phase 5 maintenance

## Status

```
Branch: feature/synth-core-brain-engine
Base: main (cyb3rclaw fork)
Upstream: sipeed/picoclaw
```

---

*Generated: 2026-05-08*  
*SYNTH-CORE v10 Compliant*
