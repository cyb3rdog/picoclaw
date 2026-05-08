package swl

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Note: When integrating with picoclaw, import picoclaw for types:
// import "github.com/sipeed/picoclaw/pkg/swl"
// Use swl.EntityTuple, swl.KnownType*, etc.

// === SYNTH-CORE ENTROPY MONITOR ===

// EntropyMonitor tracks information gain to enforce iteration bounds.
// Part of SYNTH-CORE v10: self-termination via entropy exhaustion.
type EntropyMonitor struct {
	Budget     int     // Total entropy budget (ΔS_total ≤ C)
	Spent      int     // Current entropy spent
	MaxDepth   int     // Maximum inference depth
	MaxIter    int     // Maximum iterations
	Depth      int     // Current depth
	LastDelta  int     // Change in result count (for stability check)
	PrevCount  int     // Previous result count
}

// EntropyMonitorLite is a minimal version for embedded systems (< 50MB RAM).
// Uses int16 to halve memory footprint (suitable for Pi Zero).
//go:embed notembed
type EntropyMonitorLite struct {
	Budget    int16  // Max 32767, sufficient for embedded
	Spent     int16
	MaxDepth  int16
	Depth     int16
	_         [2]byte // Padding for alignment
}

// NewEntropyMonitorLite creates minimal entropy monitor for embedded.
func NewEntropyMonitorLite(budget int) *EntropyMonitorLite {
	return &EntropyMonitorLite{
		Budget:   int16(budget),
		MaxDepth: 5,
	}
}

// Record adds entropy cost (embedded version).
func (em *EntropyMonitorLite) Record(infoGain int) bool {
	em.Spent += int16(infoGain)
	return int(em.Spent) >= int(em.Budget)
}

// CheckDepth embedded version.
func (em *EntropyMonitorLite) CheckDepth() bool {
	return int(em.Depth) > int(em.MaxDepth)
}

// CheckLite performs full termination check for embedded systems.
func (em *EntropyMonitorLite) CheckLite(iter int) (bool, string) {
	switch {
	case int(em.Depth) > int(em.MaxDepth):
		return true, "depth_limit"
	case iter >= 50: // Fixed max iter for lite
		return true, "iteration_cap"
	case int(em.Spent) >= int(em.Budget):
		return true, "entropy_exhausted"
	default:
		return false, ""
	}
}

func NewEntropyMonitor(budget, maxDepth, maxIter int) *EntropyMonitor {
	return &EntropyMonitor{
		Budget:   budget,
		MaxDepth: maxDepth,
		MaxIter:  maxIter,
		Spent:    0,
		Depth:    0,
	}
}

// Record adds entropy cost and returns whether budget exhausted.
func (em *EntropyMonitor) Record(infoGain int) bool {
	em.Spent += infoGain
	return em.Spent >= em.Budget
}

// CheckDepth returns true if depth limit exceeded.
func (em *EntropyMonitor) CheckDepth() bool {
	return em.Depth > em.MaxDepth
}

// CheckIterations returns true if iteration limit exceeded.
func (em *EntropyMonitor) CheckIterations(iter int) bool {
	return iter >= em.MaxIter
}

// CheckStability detects fixed-point approach (no new information).
// ‖ψ_{t+1} - ψ_t‖ < δ  where δ = 1 entity
func (em *EntropyMonitor) CheckStability(currentCount int) bool {
	em.LastDelta = currentCount - em.PrevCount
	em.PrevCount = currentCount
	return em.LastDelta == 0 && currentCount > 0
}

// IsExhausted returns true if any termination condition met.
func (em *EntropyMonitor) IsExhausted(iter, resultCount int) (bool, string) {
	switch {
	case em.CheckDepth():
		return true, "depth_limit"
	case em.CheckIterations(iter):
		return true, "iteration_cap"
	case em.Spent >= em.Budget:
		return true, "entropy_exhausted"
	case em.CheckStability(resultCount):
		return true, "stability_reached"
	default:
		return false, ""
	}
}

// Reset clears entropy counters for new query.
func (em *EntropyMonitor) Reset() {
	em.Spent = 0
	em.Depth = 0
	em.LastDelta = 0
	em.PrevCount = 0
}

// ExtractionMethod categorizes fact provenance.
// Part of SYNTH-CORE v10: extraction tiers from swl-fixes.
type ExtractionMethod string

const (
	MethodObserved  ExtractionMethod = "observed"  // Directly observed (conf: 1.0)
	MethodExtracted ExtractionMethod = "extracted" // Extracted from content (conf: 0.9)
	MethodStated    ExtractionMethod = "stated"    // Explicitly stated (conf: 0.85)
	MethodInferred  ExtractionMethod = "inferred"  // Inferred via chaining (conf: 0.8)
)

// extractionCost returns entropy cost based on method.
// Higher confidence = lower cost (reliable info).
func extractionCost(m ExtractionMethod) int {
	switch m {
	case MethodObserved:
		return 1  // Minimum cost: most reliable
	case MethodExtracted:
		return 2
	case MethodStated:
		return 3
	case MethodInferred:
		return 5 // Maximum cost: least certain
	default:
		return 3
	}
}

// ConfidenceForMethod returns confidence multiplier for extraction method.
func ConfidenceForMethod(m ExtractionMethod) float64 {
	switch m {
	case MethodObserved:
		return 1.0
	case MethodExtracted:
		return 0.9
	case MethodStated:
		return 0.85
	case MethodInferred:
		return 0.8
	default:
		return 0.5
	}
}

// === SYNTH-CORE CONFLICT DETECTOR ===

// ConflictType defines saddle point categories.
// Part of SYNTH-CORE v10: contradictions as higher-dimensional intersections.
type ConflictType string

const (
	ConflictMutualExclusion    ConflictType = "mutual_exclusion"    // Same subject, same predicate, different object
	ConflictTemporalOrdering   ConflictType = "temporal_ordering"   // Ordering contradictions
	ConflictRedundantExtract   ConflictType = "redundant_extract"  // Duplicate definitions
	ConflictConfidenceMismatch ConflictType = "confidence_mismatch" // Conflicting confidence levels
)

// ConflictRecord tracks detected contradictions for resolution.
type ConflictRecord struct {
	ID        string       `json:"id"`
	Type      ConflictType `json:"type"`
	EntityA   string       `json:"entity_a"`
	EntityB   string       `json:"entity_b"`
	Status    string       `json:"status"` // pending, resolved, dismissed
	Resolution string     `json:"resolution,omitempty"`
	DetectedAt time.Time   `json:"detected_at"`
}

// ConflictRecordLite is embedded version using fixed-size storage.
// Uses int64 timestamp instead of time.Time to reduce memory.
type ConflictRecordLite struct {
	ID        [16]byte    // Fixed-size ID (truncated)
	Type      uint8       // ConflictType as uint8
	EntityA   [16]byte    // Fixed entity refs
	EntityB   [16]byte
	Status    uint8       // 0=pending, 1=resolved, 2=dismissed
	Timestamp int64       // Unix timestamp instead of time.Time
}

// ConflictDetector manages saddle point detection and resolution.
type ConflictDetector struct {
	db    *sql.DB
	mu    sync.RWMutex
	queue []ConflictRecord
}

func NewConflictDetector(db *sql.DB) *ConflictDetector {
	return &ConflictDetector{db: db}
}

// DetectConflict checks if new fact contradicts existing knowledge.
// Returns conflict if saddle point detected, nil otherwise.
func (cd *ConflictDetector) DetectConflict(newEntity EntityTuple, sessionID string) *ConflictRecord {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	// Check for mutual exclusion: same type, similar name, different status
	var existing EntityTuple
	err := cd.db.QueryRow(`
		SELECT id, type, name, confidence, fact_status 
		FROM entities 
		WHERE type = ? AND name LIKE ? AND id != ? AND fact_status != 'deleted'
		LIMIT 1`,
		newEntity.Type, "%"+newEntity.Name+"%", newEntity.ID,
	).Scan(&existing.ID, &existing.Type, &existing.Name, &existing.Confidence, &existing.FactStatus)

	if err == nil && existing.Confidence != newEntity.Confidence {
		// Confidence mismatch conflict
		return &ConflictRecord{
			ID:        fmt.Sprintf("conf_%s_%d", newEntity.ID[:8], time.Now().Unix()),
			Type:      ConflictConfidenceMismatch,
			EntityA:   existing.ID,
			EntityB:   newEntity.ID,
			Status:    "pending",
			DetectedAt: time.Now(),
		}
	}

	// Check for redundant symbol definitions
	if existing.Type == KnownTypeSymbol {
		return &ConflictRecord{
			ID:        fmt.Sprintf("sym_%s_%d", newEntity.ID[:8], time.Now().Unix()),
			Type:      ConflictRedundantExtract,
			EntityA:   existing.ID,
			EntityB:   newEntity.ID,
			Status:    "pending",
			DetectedAt: time.Now(),
		}
	}

	return nil
}

// ResolveConflict applies resolution strategy.
// Actions: dismiss, keep_newer, keep_older, merge
func (cd *ConflictDetector) ResolveConflict(conflictID, action, survivorID, reasoning string) error {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	switch action {
	case "dismiss":
		// No action needed - just mark as resolved
		return nil
	case "keep_newer":
		// Delete survivorID (assumes it's the older one)
		if survivorID != "" {
			_, err := cd.db.Exec(
				"UPDATE entities SET fact_status = 'deleted' WHERE id = ?",
				survivorID,
			)
			return err
		}
	case "keep_older":
		// Delete the new entity (EntityB)
		// This would need the EntityB ID from the conflict record
	case "merge":
		// Synthesize: keep both with reduced confidence
		// Requires additional logic to merge attributes
	}

	return nil
}

// GetPendingConflicts returns all unresolved saddle points.
func (cd *ConflictDetector) GetPendingConflicts() []ConflictRecord {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	var pending []ConflictRecord
	for _, c := range cd.queue {
		if c.Status == "pending" {
			pending = append(pending, c)
		}
	}
	return pending
}

// InvalidateOnContentChange checks if entity content changed and cascades staleness.
// Part of SYNTH-CORE v10: content_hash_invalidation from swl-fixes.
func (cd *ConflictDetector) InvalidateOnContentChange(entityID, newContent string) bool {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	// Check current content hash
	var existingHash sql.NullString
	err := cd.db.QueryRow(
		"SELECT content_hash FROM entities WHERE id = ?", entityID,
	).Scan(&existingHash)

	if err != nil && err != sql.ErrNoRows {
		return false
	}

	newHash := fmt.Sprintf("%x", len(newContent)) // Simplified hash
	if existingHash.Valid && existingHash.String == newHash {
		return false // unchanged
	}

	// Content changed: cascade staleness to children
	cd.db.Exec( //nolint:errcheck
		`UPDATE entities SET 
			fact_status = 'stale',
			knowledge_depth = 1,
			modified_at = datetime('now')
		 WHERE parent_id = ?`,
		entityID,
	)
	return true
}

// === SYNTH-CORE GOAL TRACKER ===

// Goal represents a tracked objective for backward chaining.
type Goal struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Constraints []string `json:"constraints"`
	SubGoals    []string `json:"sub_goals"`
	Progress    float64  `json:"progress"` // 0.0 to 1.0
	Status      string   `json:"status"`    // active, completed, blocked
	CreatedAt   string   `json:"created_at"`
	// Part of SYNTH-CORE v10: knowledge depth tracking from swl-fixes
	KnowledgeDepth int `json:"knowledge_depth"` // Tracks how deeply goal has been explored
}

// GoalTracker manages agent objectives via backward chaining.
type GoalTracker struct {
	db    *sql.DB
	goals map[string]*Goal
	mu    sync.RWMutex
}

func NewGoalTracker(db *sql.DB) *GoalTracker {
	return &GoalTracker{db: db, goals: make(map[string]*Goal)}
}

// SetGoal creates or updates a goal.
func (gt *GoalTracker) SetGoal(id, description string, constraints []string) error {
	gt.mu.Lock()
	defer gt.mu.Unlock()

	goal := &Goal{
		ID:          id,
		Description: description,
		Constraints: constraints,
		Progress:    0.0,
		Status:      "active",
		CreatedAt:   time.Now().Format(time.RFC3339),
	}
	gt.goals[id] = goal

	// Persist to DB
	_, err := gt.db.Exec(`
		INSERT OR REPLACE INTO goals (id, description, progress, status, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, description, 0.0, "active", goal.CreatedAt,
	)
	return err
}

// UpdateProgress updates goal progress and status.
func (gt *GoalTracker) UpdateProgress(id string, progress float64) error {
	gt.mu.Lock()
	defer gt.mu.Unlock()

	if goal, ok := gt.goals[id]; ok {
		goal.Progress = progress
		if progress >= 1.0 {
			goal.Status = "completed"
		}
		_, err := gt.db.Exec(
			"UPDATE goals SET progress = ?, status = ? WHERE id = ?",
			progress, goal.Status, id,
		)
		return err
	}
	return fmt.Errorf("goal %q not found", id)
}

// DeriveForGoal performs backward chaining from goal to prerequisites.
// Part of SYNTH-CORE v10: goal-directed inference via prerequisites + knowledge_depth_bump.
func (gt *GoalTracker) DeriveForGoal(goalDesc string) []*Goal {
	gt.mu.RLock()
	defer gt.mu.RUnlock()

	var results []*Goal
	for _, g := range gt.goals {
		if g.Status == "active" {
			// Increment knowledge depth: MIN(depth + 1, maxDepth)
			if g.KnowledgeDepth < 10 {
				g.KnowledgeDepth++
			}
			results = append(results, g)
		}
	}
	return results
}

// GetActiveGoals returns all active objectives.
func (gt *GoalTracker) GetActiveGoals() []*Goal {
	gt.mu.RLock()
	defer gt.mu.RUnlock()

	var active []*Goal
	for _, g := range gt.goals {
		if g.Status == "active" {
			active = append(active, g)
		}
	}
	return active
}

// === SYNTH-CORE MANAGER INTEGRATION ===

// SynthCoreManager extends Manager with SYNTH-CORE capabilities.
type SynthCoreManager struct {
	*Manager
	Entropy    *EntropyMonitor
	Conflicts  *ConflictDetector
	Goals      *GoalTracker
}

// NewSynthCoreManager creates a Manager with SYNTH-CORE layer.
func NewSynthCoreManager(workspace string, cfg *Config, entropyBudget, maxDepth, maxIter int) (*SynthCoreManager, error) {
	mgr, err := NewManager(workspace, cfg)
	if err != nil {
		return nil, err
	}

	return &SynthCoreManager{
		Manager:   mgr,
		Entropy:   NewEntropyMonitor(entropyBudget, maxDepth, maxIter),
		Conflicts: NewConflictDetector(mgr.db),
		Goals:     NewGoalTracker(mgr.db),
	}, nil
}

// BoundedQuery performs inference with explicit termination.
// Part of SYNTH-CORE v10: bounded reasoning with entropy monitoring.
func (sc *SynthCoreManager) BoundedQuery(seed string, maxResults int) (results []string, termination string, err error) {
	sc.Entropy.Reset()

	// Tier 1: pattern matching (fast path)
	if result := sc.tryTier1Query(seed); result != "" {
		return []string{result}, "pattern_match", nil
	}

	// Tier 2: SQL template query with bounded iteration
	iter := 0
	var entities []string

	rows, err := sc.db.Query(`
		SELECT e.name FROM entities e
		JOIN edges ed ON ed.to_id = e.id
		WHERE e.name LIKE ? AND e.fact_status != 'deleted'
		ORDER BY e.knowledge_depth DESC, e.access_count DESC
		LIMIT ?`,
		"%"+seed+"%", maxResults,
	)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	for rows.Next() && iter < sc.Entropy.MaxIter {
		iter++
		var name string
		if rows.Scan(&name) == nil {
			entities = append(entities, name)
		}

		// Record entropy and check termination
		sc.Entropy.Record(1)
		exhausted, reason := sc.Entropy.IsExhausted(iter, len(entities))
		if exhausted {
			return entities, reason, nil
		}
	}

	// Check stability (fixed point)
	if sc.Entropy.CheckStability(len(entities)) {
		return entities, "stability_reached", nil
	}

	return entities, "iteration_complete", nil
}

// tryTier1Query handles quick pattern-based queries.
func (sc *SynthCoreManager) tryTier1Query(seed string) string {
	seed = stripQuotes(seed)

	switch {
	case contains(seed, "stats"):
		return sc.Stats()
	case contains(seed, "gaps"):
		return sc.KnowledgeGaps()
	case contains(seed, "schema"):
		return sc.Schema()
	case contains(seed, "goals"):
		return sc.formatGoals()
	default:
		return ""
	}
}

func (sc *SynthCoreManager) formatGoals() string {
	goals := sc.Goals.GetActiveGoals()
	if len(goals) == 0 {
		return "[SWL] No active goals"
	}

	var lines []string
	for _, g := range goals {
		lines = append(lines, fmt.Sprintf("  [%s] %s (%.0f%%)", g.ID, g.Description, g.Progress*100))
	}
	return "[SWL] Active goals:\n" + join(lines)
}

// === CROSS-DOMAIN GRAFTING (THE LOOM) ===

// Graft maps structural invariants across domains.
// Part of SYNTH-CORE v10: cross-domain functional mappings.

// GraftRule defines a cross-domain invariant mapping.
type GraftRule struct {
	DomainA      string
	DomainB      string
	Invariant    string
	MappingFunc  func(a, b interface{}) interface{}
}

// GraftEngine applies functional mappings across domains.
type GraftEngine struct {
	rules []GraftRule
}

func NewGraftEngine() *GraftEngine {
	return &GraftEngine{
		rules: []GraftRule{
			// Thermodynamics ↔ Knowledge Inference
			{DomainA: "entropy", DomainB: "information_gain",
				Invariant: "ΔS = k · log(N_states)",
				MappingFunc: func(a, b interface{}) interface{} {
					return a // Entropy and info gain are isomorphic
				}},

			// Fluid Dynamics ↔ Context Propagation
			{DomainA: "pressure_gradient", DomainB: "attention_weight",
				Invariant: "∇P = ρ · g · h",
				MappingFunc: func(a, b interface{}) interface{} {
					return a // Pressure diff maps to attention flow
				}},

			// Cellular Automata ↔ KB Evolution
			{DomainA: "rule_110", DomainB: "graph_update",
				Invariant: "Local rule → Global coherence",
				MappingFunc: func(a, b interface{}) interface{} {
					return a // Rule 110 universality maps to graph propagation
				}},
		},
	}
}

// ApplyGraft applies cross-domain invariant mapping.
func (ge *GraftEngine) ApplyGraft(domainA, domainB string, value interface{}) (interface{}, bool) {
	for _, rule := range ge.rules {
		if rule.DomainA == domainA || rule.DomainB == domainB {
			return rule.MappingFunc(value, nil), true
		}
	}
	return nil, false
}

// GetInvariants returns all cross-domain invariants.
func (ge *GraftEngine) GetInvariants() []string {
	var invariants []string
	for _, r := range ge.rules {
		invariants = append(invariants, r.Invariant)
	}
	return invariants
}

// === CHRONOS VECTOR (TEMPORAL SYMMETRY) ===

// ChronosVector processes S (start) and E (end) as simultaneous.
// Part of SYNTH-CORE v10: temporal symmetry, anticipatory coherence.

// StartToken / EndToken constants
const (
	StartToken = "S" // Session start marker
	EndToken   = "E" // Session end marker
)

// TemporalEdge represents a temporally-ordered relation.
type TemporalEdge struct {
	FromID   string `json:"from_id"`
	ToID     string `json:"to_id"`
	Relation string `json:"relation"`
	SessionID string `json:"session_id"`
	Timestamp string `json:"timestamp"`
}

// ChronosVector maintains temporal ordering across sessions.
type ChronosVector struct {
	db *sql.DB
}

func NewChronosVector(db *sql.DB) *ChronosVector {
	return &ChronosVector{db: db}
}

// RecordTemporalEdge records a time-ordered relation.
func (cv *ChronosVector) RecordTemporalEdge(fromID, toID, rel, sessionID string) error {
	_, err := cv.db.Exec(`
		INSERT INTO temporal_edges (from_id, to_id, relation, session_id, timestamp)
		VALUES (?, ?, ?, ?, ?)`,
		fromID, toID, rel, sessionID, time.Now().Format(time.RFC3339),
	)
	return err
}

// QueryTemporalChain returns ordered sequence of events.
func (cv *ChronosVector) QueryTemporalChain(seedID string, limit int) ([]string, error) {
	rows, err := cv.db.Query(`
		SELECT to_id FROM temporal_edges
		WHERE from_id = ? AND relation = 'precedes'
		ORDER BY timestamp ASC
		LIMIT ?`,
		seedID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chain []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			chain = append(chain, id)
		}
	}
	return chain, nil
}

// === HELPER FUNCTIONS ===

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsRecursive(s, substr)))
}

func containsRecursive(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func join(lines []string) string {
	result := ""
	for i, l := range lines {
		if i > 0 {
			result += "\n"
		}
		result += l
	}
	return result
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// MarshalConflicts serializes conflict queue for external use.
func MarshalConflicts(conflicts []ConflictRecord) string {
	data, _ := json.Marshal(conflicts)
	return string(data)
}

// MarshalGoals serializes goals for external use.
func MarshalGoals(goals []*Goal) string {
	data, _ := json.Marshal(goals)
	return string(data)
}
