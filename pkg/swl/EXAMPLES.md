# SYNTH-CORE Usage Examples

## Basic Usage

### 1. Creating a SynthCoreManager

```go
package main

import (
    "github.com/sipeed/picoclaw/pkg/swl"
)

func main() {
    // Create manager with SYNTH-CORE layer
    sc, err := swl.NewSynthCoreManager(
        "/path/to/workspace",
        &swl.Config{},
        1000,  // entropyBudget: max inference iterations
        5,     // maxDepth: BFS depth limit
        50,    // maxIter: hard iteration cap
    )
    if err != nil {
        panic(err)
    }
    defer sc.Close()
}
```

### 2. Bounded Query with Termination

```go
// Query with explicit termination
results, reason, err := sc.BoundedQuery("user_query", 10)

fmt.Printf("Results: %d items\n", len(results))
fmt.Printf("Termination: %s\n", reason)
// Possible reasons: "pattern_match", "depth_limit", "iteration_cap", 
//                   "entropy_exhausted", "stability_reached", "iteration_complete"
```

### 3. Conflict Detection

```go
// Add entity and check for conflicts
entity := swl.EntityTuple{
    ID:       "file_main",
    Type:     swl.KnownTypeFile,
    Name:     "main.go",
    Confidence: 0.9,
}

// Detect saddle point
conflict := sc.Conflicts.DetectConflict(entity, sessionID)
if conflict != nil {
    fmt.Printf("Conflict detected: %s\n", conflict.Type)
    fmt.Printf("Entities: %s <-> %s\n", conflict.EntityA, conflict.EntityB)
    
    // Resolve: dismiss, keep_newer, keep_older, or merge
    err := sc.Conflicts.ResolveConflict(conflict.ID, "dismiss", "", "User confirmed")
}
```

### 4. Goal Tracking

```go
// Create goal
sc.Goals.SetGoal("integrate_swl", "Complete SWL integration", []string{
    "entropy must work",
    "conflicts must detect",
})

// Update progress
sc.Goals.UpdateProgress("integrate_swl", 0.25) // 25%
sc.Goals.UpdateProgress("integrate_swl", 0.50) // 50%
sc.Goals.UpdateProgress("integrate_swl", 1.00) // Complete

// Query active goals
goals := sc.Goals.GetActiveGoals()
for _, g := range goals {
    fmt.Printf("[%s] %s (%.0f%%)\n", g.ID, g.Description, g.Progress*100)
}
```

### 5. Cross-Domain Grafting

```go
// Create graft engine
ge := swl.NewGraftEngine()

// Apply graft: entropy ↔ information_gain
result, ok := ge.ApplyGraft("entropy", "information_gain", 100)
if ok {
    fmt.Printf("Entropy mapped to info gain: %v\n", result)
}

// Get all invariants
invariants := ge.GetInvariants()
for _, inv := range invariants {
    fmt.Printf("Invariant: %s\n", inv)
}
```

### 6. Chronos Vector (Temporal Symmetry)

```go
// Create chronos vector
cv := swl.NewChronosVector(db)

// Record temporal edge: A precedes B
cv.RecordTemporalEdge("event_start", "event_end", "precedes", sessionID)

// Query temporal chain
chain, err := cv.QueryTemporalChain("event_start", 10)
fmt.Printf("Chain: %v\n", chain)

// Use constants
fmt.Printf("Start token: %s, End token: %s\n", swl.StartToken, swl.EndToken)
```

### 7. Entropy Monitor Standalone

```go
// Create entropy monitor
em := swl.NewEntropyMonitor(1000, 5, 50)

// Record iterations
for i := 0; i < 100; i++ {
    em.Record(10)
    exhausted, reason := em.IsExhausted(i, i*5)
    if exhausted {
        fmt.Printf("Terminated at iteration %d: %s\n", i, reason)
        break
    }
}

// Reset for new query
em.Reset()
```

## Integration with SWL Hook

```go
// In agent initialization
func initAgent(workspace string) *swl.SynthCoreManager {
    sc, err := swl.NewSynthCoreManager(workspace, nil, 1000, 5, 50)
    if err != nil {
        log.Fatal(err)
    }
    
    // Run migration
    if err := swl.EnsureSynthCoreTables(sc.Manager.DB()); err != nil {
        log.Printf("Migration warning: %v", err)
    }
    
    return sc
}

// In tool call PostHook
func onToolCall(sc *swl.SynthCoreManager, toolName string, args map[string]any, result string) {
    sessionID := sc.Manager.EnsureSession(sessionKey)
    
    // Run inference
    sc.Manager.PostHook(sessionKey, toolName, args, result)
    
    // Check for conflicts
    // (Conflict detection integrated into upsertEntity)
    
    // Update entropy
    // (Entropy tracking integrated into BoundedQuery)
}
```

## Pattern Matching Queries

```go
// These are handled automatically by BoundedQuery's Tier 1
sc.BoundedQuery("stats", 10)      // Returns sc.Stats()
sc.BoundedQuery("gaps", 10)       // Returns sc.KnowledgeGaps()
sc.BoundedQuery("schema", 10)     // Returns sc.Schema()
sc.BoundedQuery("goals", 10)      // Returns active goals
```

---

*Generated: 2026-05-08*  
*SYNTH-CORE v10 Compliant*
