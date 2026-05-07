package swl

import (
	"testing"
)

// === SYNTH-CORE TESTS ===

func TestEntropyMonitorCreation(t *testing.T) {
	em := NewEntropyMonitor(1000, 5, 50)

	if em.Budget != 1000 {
		t.Errorf("expected Budget=1000, got %d", em.Budget)
	}
	if em.MaxDepth != 5 {
		t.Errorf("expected MaxDepth=5, got %d", em.MaxDepth)
	}
	if em.MaxIter != 50 {
		t.Errorf("expected MaxIter=50, got %d", em.MaxIter)
	}
	if em.Spent != 0 {
		t.Errorf("expected Spent=0, got %d", em.Spent)
	}
}

func TestEntropyMonitorRecord(t *testing.T) {
	em := NewEntropyMonitor(100, 5, 50)

	// Record within budget
	exhausted := em.Record(50)
	if exhausted {
		t.Error("expected not exhausted at 50, got exhausted")
	}
	if em.Spent != 50 {
		t.Errorf("expected Spent=50, got %d", em.Spent)
	}

	// Record reaching budget
	exhausted = em.Record(50)
	if !exhausted {
		t.Error("expected exhausted at 100, got not exhausted")
	}
}

func TestEntropyMonitorDepthCheck(t *testing.T) {
	em := NewEntropyMonitor(1000, 5, 50)

	if em.CheckDepth() {
		t.Error("depth should not be exceeded initially")
	}

	em.Depth = 6
	if !em.CheckDepth() {
		t.Error("depth should be exceeded at 6 > 5")
	}
}

func TestEntropyMonitorIterationsCheck(t *testing.T) {
	em := NewEntropyMonitor(1000, 5, 50)

	if em.CheckIterations(49) {
		t.Error("iterations should not be exceeded at 49 < 50")
	}

	if !em.CheckIterations(50) {
		t.Error("iterations should be exceeded at 50 >= 50")
	}
}

func TestEntropyMonitorStability(t *testing.T) {
	em := NewEntropyMonitor(1000, 5, 50)

	// No stability with empty results
	if em.CheckStability(0) {
		t.Error("should not detect stability with 0 results")
	}

	// Stability when no new results
	em.PrevCount = 10
	if !em.CheckStability(10) {
		t.Error("should detect stability when count unchanged at 10")
	}

	// No stability when results grow
	em.PrevCount = 10
	if em.CheckStability(11) {
		t.Error("should not detect stability when results grew")
	}
}

func TestEntropyMonitorIsExhausted(t *testing.T) {
	em := NewEntropyMonitor(100, 5, 50)

	// Not exhausted initially
	exhausted, reason := em.IsExhausted(0, 5)
	if exhausted {
		t.Error("should not be exhausted initially")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %s", reason)
	}

	// Exhaustive by entropy
	em.Spent = 100
	exhausted, reason = em.IsExhausted(0, 5)
	if !exhausted {
		t.Error("should be exhausted by entropy")
	}
	if reason != "entropy_exhausted" {
		t.Errorf("expected entropy_exhausted, got %s", reason)
	}
}

func TestEntropyMonitorReset(t *testing.T) {
	em := NewEntropyMonitor(1000, 5, 50)
	em.Spent = 500
	em.Depth = 3
	em.PrevCount = 10
	em.LastDelta = 5

	em.Reset()

	if em.Spent != 0 {
		t.Errorf("expected Spent=0 after reset, got %d", em.Spent)
	}
	if em.Depth != 0 {
		t.Errorf("expected Depth=0 after reset, got %d", em.Depth)
	}
	if em.PrevCount != 0 {
		t.Errorf("expected PrevCount=0 after reset, got %d", em.PrevCount)
	}
}

// === GOAL TRACKER TESTS ===

func TestGoalCreation(t *testing.T) {
	gt := NewGoalTracker(nil) // nil db for unit test

	err := gt.SetGoal("test_goal", "Complete integration", []string{"constraint1"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	goals := gt.GetActiveGoals()
	if len(goals) != 1 {
		t.Errorf("expected 1 goal, got %d", len(goals))
	}

	if goals[0].ID != "test_goal" {
		t.Errorf("expected goal ID 'test_goal', got %s", goals[0].ID)
	}
	if goals[0].Description != "Complete integration" {
		t.Errorf("expected description 'Complete integration', got %s", goals[0].Description)
	}
	if goals[0].Progress != 0.0 {
		t.Errorf("expected progress 0.0, got %f", goals[0].Progress)
	}
	if goals[0].Status != "active" {
		t.Errorf("expected status 'active', got %s", goals[0].Status)
	}
}

func TestGoalProgressUpdate(t *testing.T) {
	gt := NewGoalTracker(nil)

	gt.SetGoal("test_goal", "Complete integration", nil)

	// Partial progress
	gt.UpdateProgress("test_goal", 0.5)
	goal := gt.GetActiveGoals()[0]
	if goal.Progress != 0.5 {
		t.Errorf("expected progress 0.5, got %f", goal.Progress)
	}
	if goal.Status != "active" {
		t.Errorf("expected status 'active' at 50%%, got %s", goal.Status)
	}

	// Complete
	gt.UpdateProgress("test_goal", 1.0)
	goal = gt.GetActiveGoals()[0]
	if goal.Progress != 1.0 {
		t.Errorf("expected progress 1.0, got %f", goal.Progress)
	}
	if goal.Status != "completed" {
		t.Errorf("expected status 'completed' at 100%%, got %s", goal.Status)
	}
}

func TestGoalNotFound(t *testing.T) {
	gt := NewGoalTracker(nil)

	err := gt.UpdateProgress("nonexistent", 0.5)
	if err == nil {
		t.Error("expected error for nonexistent goal")
	}
}

// === GRAFT ENGINE TESTS ===

func TestGraftEngineCreation(t *testing.T) {
	ge := NewGraftEngine()

	invariants := ge.GetInvariants()
	if len(invariants) != 3 {
		t.Errorf("expected 3 invariants, got %d", len(invariants))
	}
}

func TestGraftEngineApply(t *testing.T) {
	ge := NewGraftEngine()

	// Valid graft
	result, ok := ge.ApplyGraft("entropy", "information_gain", 100)
	if !ok {
		t.Error("expected graft to succeed")
	}
	if result != 100 {
		t.Errorf("expected result 100, got %v", result)
	}

	// Invalid graft
	result, ok = ge.ApplyGraft("unknown", "domain", 100)
	if ok {
		t.Error("expected graft to fail for unknown domains")
	}
	if result != nil {
		t.Errorf("expected nil result for failed graft, got %v", result)
	}
}

func TestGraftInvariantStrings(t *testing.T) {
	ge := NewGraftEngine()

	invariants := ge.GetInvariants()

	expected := []string{
		"ΔS = k · log(N_states)",
		"∇P = ρ · g · h",
		"Local rule → Global coherence",
	}

	for i, inv := range invariants {
		if inv != expected[i] {
			t.Errorf("expected invariant %d to be %q, got %q", i, expected[i], inv)
		}
	}
}

// === HELPER FUNCTION TESTS ===

func TestContains(t *testing.T) {
	tests := []struct {
		s, substr string
		expected bool
	}{
		{"hello world", "world", true},
		{"hello world", "hello", true},
		{"hello world", "xyz", false},
		{"hello", "hello", true},
		{"", "", true},
		{"hello", "", true},
		{"", "hello", false},
	}

	for _, tc := range tests {
		result := contains(tc.s, tc.substr)
		if result != tc.expected {
			t.Errorf("contains(%q, %q) = %v, expected %v", tc.s, tc.substr, result, tc.expected)
		}
	}
}

func TestStripQuotes(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{`"hello"`, "hello"},
		{"'world'", "world"},
		{"noquotes", "noquotes"},
		{`"`, "\""},
		{"''", ""},
		{`"`, "\""},
	}

	for _, tc := range tests {
		result := stripQuotes(tc.input)
		if result != tc.expected {
			t.Errorf("stripQuotes(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}

func TestJoin(t *testing.T) {
	lines := []string{"a", "b", "c"}
	result := join(lines)
	expected := "a\nb\nc"

	if result != expected {
		t.Errorf("join() = %q, expected %q", result, expected)
	}

	// Empty case
	empty := join([]string{})
	if empty != "" {
		t.Errorf("join([]) = %q, expected empty", empty)
	}
}

// === MARSHAL TESTS ===

func TestMarshalConflicts(t *testing.T) {
	conflicts := []ConflictRecord{
		{
			ID:      "test_conflict",
			Type:    ConflictMutualExclusion,
			EntityA: "entity_a",
			EntityB: "entity_b",
			Status:  "pending",
		},
	}

	json := MarshalConflicts(conflicts)
	if json == "" {
		t.Error("expected non-empty JSON")
	}
	// Basic JSON check
	if json[0] != '[' {
		t.Errorf("expected JSON array, got %c", json[0])
	}
}

func TestMarshalGoals(t *testing.T) {
	goals := []*Goal{
		{
			ID:          "goal_1",
			Description: "Test goal",
			Progress:    0.5,
			Status:      "active",
		},
	}

	json := MarshalGoals(goals)
	if json == "" {
		t.Error("expected non-empty JSON")
	}
	// Basic JSON check
	if json[0] != '[' {
		t.Errorf("expected JSON array, got %c", json[0])
	}
}

// === CONFLICT TYPE CONSTANTS ===

func TestConflictTypes(t *testing.T) {
	if ConflictMutualExclusion != "mutual_exclusion" {
		t.Errorf("expected mutual_exclusion, got %s", ConflictMutualExclusion)
	}
	if ConflictTemporalOrdering != "temporal_ordering" {
		t.Errorf("expected temporal_ordering, got %s", ConflictTemporalOrdering)
	}
	if ConflictRedundantExtract != "redundant_extract" {
		t.Errorf("expected redundant_extract, got %s", ConflictRedundantExtract)
	}
	if ConflictConfidenceMismatch != "confidence_mismatch" {
		t.Errorf("expected confidence_mismatch, got %s", ConflictConfidenceMismatch)
	}
}

// === CHRONOS VECTOR CONSTANTS ===

func TestChronosTokens(t *testing.T) {
	if StartToken != "S" {
		t.Errorf("expected StartToken='S', got %s", StartToken)
	}
	if EndToken != "E" {
		t.Errorf("expected EndToken='E', got %s", EndToken)
	}
}
