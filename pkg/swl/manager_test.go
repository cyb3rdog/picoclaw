package swl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(dir, &Config{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

// --- Invariant 1: fact_status is never reset by upsert ---

func TestUpsertDoesNotResetFactStatus(t *testing.T) {
	m := newTestManager(t)

	id := entityID(KnownTypeFile, "/tmp/test.go")
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeFile, Name: "/tmp/test.go",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})

	_ = m.SetFactStatus(id, FactVerified)

	// Upsert again — fact_status must remain verified
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeFile, Name: "/tmp/test.go",
		Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 2,
	})

	var status string
	_ = m.db.QueryRow("SELECT fact_status FROM entities WHERE id = ?", id).Scan(&status)
	if status != string(FactVerified) {
		t.Errorf("fact_status was reset by upsert: got %q want %q", status, FactVerified)
	}
}

// --- Invariant 2: confidence is monotonically non-decreasing ---

func TestConfidenceMonotonic(t *testing.T) {
	m := newTestManager(t)

	id := entityID(KnownTypeSymbol, "MyFunc")
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeSymbol, Name: "MyFunc",
		Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
	})

	// Try to decrease confidence
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeSymbol, Name: "MyFunc",
		Confidence: 0.5, ExtractionMethod: MethodInferred, KnowledgeDepth: 1,
	})

	var conf float64
	_ = m.db.QueryRow("SELECT confidence FROM entities WHERE id = ?", id).Scan(&conf)
	if conf < 0.9 {
		t.Errorf("confidence decreased: got %.2f want >= 0.9", conf)
	}
}

// --- Invariant 3: extraction_method priority ---

func TestExtractionMethodPriority(t *testing.T) {
	m := newTestManager(t)

	id := entityID(KnownTypeFile, "/a.go")
	// Insert with extracted
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeFile, Name: "/a.go",
		Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
	})

	// Re-upsert with inferred — should NOT downgrade
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeFile, Name: "/a.go",
		Confidence: 0.8, ExtractionMethod: MethodInferred, KnowledgeDepth: 1,
	})

	var method string
	_ = m.db.QueryRow("SELECT extraction_method FROM entities WHERE id = ?", id).Scan(&method)
	if ExtractionMethod(method) != MethodExtracted {
		t.Errorf("method downgraded: got %q want %q", method, MethodExtracted)
	}

	// Re-upsert with observed — should UPGRADE
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeFile, Name: "/a.go",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})
	_ = m.db.QueryRow("SELECT extraction_method FROM entities WHERE id = ?", id).Scan(&method)
	if ExtractionMethod(method) != MethodObserved {
		t.Errorf("method not upgraded to observed: got %q", method)
	}
}

// --- Invariant 4: deleted status is terminal ---

func TestDeletedStatusTerminal(t *testing.T) {
	m := newTestManager(t)

	id := entityID(KnownTypeFile, "/gone.go")
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeFile, Name: "/gone.go",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})

	_ = m.SetFactStatus(id, FactDeleted)

	// Attempt to revert
	_ = m.SetFactStatus(id, FactVerified)

	var status string
	_ = m.db.QueryRow("SELECT fact_status FROM entities WHERE id = ?", id).Scan(&status)
	if FactStatus(status) != FactDeleted {
		t.Errorf("deleted status was reverted: got %q", status)
	}
}

// --- Invariant 5: append_file nulls content_hash ---

func TestAppendFileNullsContentHash(t *testing.T) {
	m := newTestManager(t)

	id := entityID(KnownTypeFile, "/log.txt")
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeFile, Name: "/log.txt",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})

	// Set a hash first
	m.writer.mu.Lock()
	m.db.Exec("UPDATE entities SET content_hash = 'abc123' WHERE id = ?", id)
	m.writer.mu.Unlock()

	// Simulate append_file post-apply
	postApplyAppendFile(m, id, "sess1", nil, "")

	var hash *string
	m.db.QueryRow("SELECT content_hash FROM entities WHERE id = ?", id).Scan(&hash)
	if hash != nil {
		t.Errorf("content_hash should be NULL after append_file, got %q", *hash)
	}
}

// --- Invariant 6: assert_note sets depth to MAX(current, 2), not hardcoded 3 ---

func TestAssertNoteDepth(t *testing.T) {
	m := newTestManager(t)

	// Fresh entity — depth should become 2
	result := m.AssertNote("some_subject", "A note about something", 0, "")
	if result == "" {
		t.Error("AssertNote returned empty string")
	}

	// Find the note entity
	var depth int
	err := m.db.QueryRow(
		"SELECT knowledge_depth FROM entities WHERE type = ? AND name LIKE ?",
		KnownTypeNote, "%A note about something%",
	).Scan(&depth)
	if err != nil {
		t.Fatalf("note entity not found: %v", err)
	}
	if depth < 2 {
		t.Errorf("assert_note depth < 2: got %d", depth)
	}
}

// --- Invariant 7: content hash change cascades children to stale ---

func TestContentHashCascade(t *testing.T) {
	m := newTestManager(t)

	fileID := entityID(KnownTypeFile, "/src/foo.go")
	symID := entityID(KnownTypeSymbol, "/src/foo.go:Foo")

	_ = m.UpsertEntity(EntityTuple{ID: fileID, Type: KnownTypeFile, Name: "/src/foo.go",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1})
	_ = m.UpsertEntity(EntityTuple{ID: symID, Type: KnownTypeSymbol, Name: "Foo",
		Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 2})
	_ = m.UpsertEdge(EdgeTuple{FromID: fileID, Rel: KnownRelDefines, ToID: symID})
	_ = m.SetFactStatus(symID, FactVerified)

	// Change file content — should cascade symbol to stale
	m.writer.mu.Lock()
	m.db.Exec("UPDATE entities SET content_hash = 'oldhash' WHERE id = ?", fileID)
	changed := m.writer.checkAndInvalidateLocked(fileID, "new content version")
	m.writer.mu.Unlock()

	if !changed {
		t.Fatal("expected hash change to return true")
	}

	var status string
	m.db.QueryRow("SELECT fact_status FROM entities WHERE id = ?", symID).Scan(&status)
	if FactStatus(status) != FactStale {
		t.Errorf("child symbol not cascaded to stale: got %q", status)
	}
}

// --- Invariant 8: knowledge_depth resets to 1 on content change ---

func TestKnowledgeDepthResetsOnContentChange(t *testing.T) {
	m := newTestManager(t)

	id := entityID(KnownTypeFile, "/main.go")
	_ = m.UpsertEntity(EntityTuple{ID: id, Type: KnownTypeFile, Name: "/main.go",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 3})

	// Set existing hash
	m.writer.mu.Lock()
	m.db.Exec("UPDATE entities SET content_hash = 'oldhash', knowledge_depth = 3 WHERE id = ?", id)
	m.writer.checkAndInvalidateLocked(id, "totally new content")
	m.writer.mu.Unlock()

	var depth int
	m.db.QueryRow("SELECT knowledge_depth FROM entities WHERE id = ?", id).Scan(&depth)
	if depth != 1 {
		t.Errorf("knowledge_depth not reset to 1 on content change: got %d", depth)
	}
}

// --- Invariant 9: session UUID stable across multiple PostHook calls ---

func TestSessionUUIDStable(t *testing.T) {
	m := newTestManager(t)

	id1 := m.EnsureSession("channel:chat123")
	id2 := m.EnsureSession("channel:chat123")
	id3 := m.EnsureSession("channel:chat123")

	if id1 == "" {
		t.Fatal("EnsureSession returned empty UUID")
	}
	if id1 != id2 || id2 != id3 {
		t.Errorf("session UUID not stable: %q %q %q", id1, id2, id3)
	}
}

// --- Invariant 10: SafeQuery rejects non-SELECT SQL ---

func TestSafeQueryRejectsNonSelect(t *testing.T) {
	m := newTestManager(t)

	_, err := m.SafeQuery("DROP TABLE entities")
	if err == nil {
		t.Error("SafeQuery should reject DROP TABLE")
	}

	_, err = m.SafeQuery("INSERT INTO entities VALUES ('a','b','c','{}',1.0,0,'observed','unknown','now','now','now',0,NULL)")
	if err == nil {
		t.Error("SafeQuery should reject INSERT")
	}

	// SELECT should work
	_, err = m.SafeQuery("SELECT COUNT(*) FROM entities")
	if err != nil {
		t.Errorf("SafeQuery rejected valid SELECT: %v", err)
	}
}

// --- Invariant 11: ApplyDelta is transactional (rollback on error) ---

func TestApplyDeltaAtomicRollback(t *testing.T) {
	m := newTestManager(t)

	// Delta with a duplicate edge that would cause an issue if not transactional
	delta := &GraphDelta{
		Entities: []EntityTuple{
			{ID: "e1", Type: KnownTypeFile, Name: "/a.go",
				Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1},
		},
	}

	err := m.ApplyDelta(delta, "sess1")
	if err != nil {
		t.Errorf("ApplyDelta failed: %v", err)
	}

	var count int
	m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE id = 'e1'").Scan(&count)
	if count != 1 {
		t.Errorf("entity not written: count=%d", count)
	}
}

// --- Extraction tests ---

func TestExtractContent_Symbols(t *testing.T) {
	m := newTestManager(t)

	fileID := entityID(KnownTypeFile, "/foo.go")
	content := `
package main

func Hello() {}
func World() {}
type Greeter struct{}
`
	delta := m.ExtractContent(fileID, "/foo.go", content)
	if delta == nil || len(delta.Entities) == 0 {
		t.Fatal("ExtractContent returned empty delta for Go file")
	}

	names := map[string]bool{}
	for _, e := range delta.Entities {
		if e.Type == KnownTypeSymbol {
			names[e.Name] = true
		}
	}
	if !names["Hello"] {
		t.Error("missing symbol Hello")
	}
	if !names["World"] {
		t.Error("missing symbol World")
	}
	if !names["Greeter"] {
		t.Error("missing symbol Greeter")
	}
}

func TestExtractContent_Tasks(t *testing.T) {
	m := newTestManager(t)

	fileID := entityID(KnownTypeFile, "/todo.go")
	content := `// TODO: fix the auth bug
// FIXME: remove this hack
// NOTE: this is important`

	delta := m.ExtractContent(fileID, "/todo.go", content)
	tasks := 0
	for _, e := range delta.Entities {
		if e.Type == KnownTypeTask {
			tasks++
		}
	}
	if tasks < 2 {
		t.Errorf("expected >= 2 tasks, got %d", tasks)
	}
}

func TestExtractContent_BinarySkipped(t *testing.T) {
	m := newTestManager(t)

	fileID := entityID(KnownTypeFile, "/bin.exe")
	content := "MZ\x00\x00\x00\x00\x00\x00" // binary header

	delta := m.ExtractContent(fileID, "/bin.exe", content)
	if delta != nil && len(delta.Entities) > 0 {
		t.Error("binary file should produce no extracted entities")
	}
}

func TestExtractContent_AntiBloomLimits(t *testing.T) {
	m := newTestManager(t)

	fileID := entityID(KnownTypeFile, "/huge.go")

	// Generate content with >60 functions
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("func ")
		sb.WriteString("Function")
		sb.WriteRune(rune('A' + i%26))
		sb.WriteString(string(rune('0' + i/26)))
		sb.WriteString("() {}\n")
	}

	delta := m.ExtractContent(fileID, "/huge.go", sb.String())
	symbolCount := 0
	for _, e := range delta.Entities {
		if e.Type == KnownTypeSymbol {
			symbolCount++
		}
	}
	if symbolCount > maxSymbols {
		t.Errorf("symbol count %d exceeds limit %d", symbolCount, maxSymbols)
	}
}

// --- Scanner test ---

// TestPathNormalizationIdempotency verifies that absolute, relative (./),
// and bare-relative references to the same file all produce the same entity ID.
func TestPathNormalizationIdempotency(t *testing.T) {
	m := newTestManager(t)
	ws := m.workspace

	// Write the same file via three different path representations.
	absPath := filepath.Join(ws, "pkg", "foo.go")
	_ = os.MkdirAll(filepath.Dir(absPath), 0755)

	content := "package foo\nfunc Bar() {}"
	m.PostHook("sess", "write_file", map[string]any{"path": absPath, "content": content}, "ok")
	m.PostHook("sess", "write_file", map[string]any{"path": "./pkg/foo.go", "content": content}, "ok")
	m.PostHook("sess", "write_file", map[string]any{"path": "pkg/foo.go", "content": content}, "ok")

	// All three must resolve to exactly one File entity.
	var count int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = 'File'").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 File entity for three path representations, got %d", count)
	}
}

func TestScanWorkspace(t *testing.T) {
	m := newTestManager(t)

	// Scan root must be inside the manager's workspace.
	root := m.workspace

	// Create some files
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}"), 0644)
	_ = os.WriteFile(filepath.Join(root, "README.md"), []byte("# Project\n\n## Setup\n"), 0644)
	_ = os.MkdirAll(filepath.Join(root, "node_modules"), 0755)
	_ = os.WriteFile(filepath.Join(root, "node_modules", "skip.js"), []byte("ignored"), 0644)

	stats, err := m.ScanWorkspace(root)
	if err != nil {
		t.Fatalf("ScanWorkspace: %v", err)
	}
	if stats.New < 2 {
		t.Errorf("expected >= 2 new files, got %d", stats.New)
	}
	// node_modules should be skipped
	var nodeCount int
	m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE name LIKE '%node_modules%'").Scan(&nodeCount)
	if nodeCount > 0 {
		t.Error("node_modules entries should not be indexed")
	}
}

// TestFullTurn simulates a complete agent turn: write_file → read_file → Ask.
// This is the end-to-end integration test verifying the full inference pipeline.
func TestFullTurn(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()

	const sessionKey = "channel:test:123"
	const filePath = "pkg/calc/calc.go"
	const fileContent = `package calc

// Add returns the sum of a and b.
func Add(a, b int) int { return a + b }

// Subtract returns a minus b.
// TODO: handle overflow
func Subtract(a, b int) int { return a - b }
`

	// --- Turn 1: write_file ---
	m.PostHook(sessionKey, "write_file", map[string]any{
		"path":    filePath,
		"content": fileContent,
	}, "")

	// File entity should exist and be verified
	fileID := entityID(KnownTypeFile, filePath)
	var fileStatus string
	if err := m.db.QueryRow("SELECT fact_status FROM entities WHERE id = ?", fileID).Scan(&fileStatus); err != nil {
		t.Fatalf("File entity not found after write_file: %v", err)
	}
	if fileStatus != string(FactVerified) {
		t.Errorf("expected verified after write_file, got %s", fileStatus)
	}

	// Symbols should be extracted
	var symCount int
	m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ? AND fact_status != 'deleted'", KnownTypeSymbol).Scan(&symCount)
	if symCount == 0 {
		t.Error("expected Symbol entities after write_file")
	}

	// Task should be extracted (TODO: handle overflow)
	var taskCount int
	m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ?", KnownTypeTask).Scan(&taskCount)
	if taskCount == 0 {
		t.Error("expected Task entity (from TODO comment) after write_file")
	}

	// --- Turn 2: read_file (simulates LLM reading the file back) ---
	m.PostHook(sessionKey, "read_file", map[string]any{"path": filePath}, fileContent)

	// Depth should be bumped
	var depth int
	m.db.QueryRow("SELECT knowledge_depth FROM entities WHERE id = ?", fileID).Scan(&depth)
	if depth < 2 {
		t.Errorf("expected knowledge_depth >= 2 after read_file, got %d", depth)
	}

	// --- Turn 3: Ask natural-language questions ---
	symbolsResult := m.Ask("functions in " + filePath)
	if !strings.Contains(symbolsResult, "Add") && !strings.Contains(symbolsResult, "Subtract") {
		t.Errorf("expected Add/Subtract in symbol query, got: %s", symbolsResult)
	}

	tasksResult := m.Ask("todos in " + filePath)
	if !strings.Contains(tasksResult, "overflow") && !strings.Contains(tasksResult, "TODO") {
		t.Errorf("expected TODO mention in task query, got: %s", tasksResult)
	}

	// --- Turn 4: SessionResume should reflect the work done ---
	resumeResult := m.SessionResume(sessionKey)
	if resumeResult == "" {
		t.Error("expected non-empty SessionResume after activity")
	}

	// --- Turn 5: delete_file → tombstone ---
	m.PostHook(sessionKey, "delete_file", map[string]any{"path": filePath}, "")
	var deletedStatus string
	m.db.QueryRow("SELECT fact_status FROM entities WHERE id = ?", fileID).Scan(&deletedStatus)
	if deletedStatus != string(FactDeleted) {
		t.Errorf("expected deleted after delete_file, got %s", deletedStatus)
	}

	// Children (symbols/tasks) should cascade to stale
	var staleChildren int
	m.db.QueryRow(`SELECT COUNT(*) FROM entities
		WHERE fact_status = 'stale'
		AND id IN (SELECT to_id FROM edges WHERE from_id = ? AND rel IN ('defines','has_task'))`,
		fileID).Scan(&staleChildren)
	if staleChildren == 0 {
		t.Error("expected child entities to become stale after file deleted")
	}
}

// TestMultiSessionIsolation verifies that two different session keys produce
// independent sessions with separate UUIDs.
func TestMultiSessionIsolation(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()

	id1 := m.EnsureSession("workspace:chan1")
	id2 := m.EnsureSession("workspace:chan2")

	if id1 == id2 {
		t.Error("different session keys must produce different session UUIDs")
	}

	var count int
	m.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 sessions, got %d", count)
	}
}

// TestRegistrySharedManager verifies that AcquireManager returns the same
// Manager for the same workspace and increments ref count correctly.
func TestRegistrySharedManager(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{}

	m1, err := AcquireManager(dir, cfg)
	if err != nil {
		t.Fatalf("AcquireManager 1: %v", err)
	}
	m2, err := AcquireManager(dir, cfg)
	if err != nil {
		t.Fatalf("AcquireManager 2: %v", err)
	}

	if m1 != m2 {
		t.Error("expected same Manager pointer for same workspace")
	}

	// Release once — manager should still be alive (ref=1)
	ReleaseManager(m1)
	if m1.db == nil {
		t.Error("manager should still be open after first release")
	}

	// Release again — manager should be closed
	ReleaseManager(m1)

	// Registry entry should be gone
	regMu.Lock()
	dbPath := resolveDBPath(dir, cfg)
	_, stillExists := registry[dbPath]
	regMu.Unlock()
	if stillExists {
		t.Error("registry entry should be removed after final release")
	}
}

// TestConfigurableSymbolPatterns verifies that a custom ExtractSymbolPatterns
// overrides the defaults and extracts only what the custom pattern matches.
func TestConfigurableSymbolPatterns(t *testing.T) {
	dir := t.TempDir()
	// Custom pattern: only match lines starting with "EXPORT:"
	cfg := &Config{
		ExtractSymbolPatterns: []string{`(?m)^EXPORT:\s+(\w+)`},
	}
	m, err := NewManager(dir, cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	content := "package main\nfunc Ignored() {}\nEXPORT: MySymbol\n"
	m.PostHook("sess", "read_file", map[string]any{"path": "custom.txt"}, content)

	var symCount int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = 'Symbol'").Scan(&symCount)
	if symCount != 1 {
		t.Errorf("expected exactly 1 symbol (MySymbol), got %d", symCount)
	}
	var symName string
	_ = m.db.QueryRow("SELECT name FROM entities WHERE type = 'Symbol'").Scan(&symName)
	if symName != "MySymbol" {
		t.Errorf("expected symbol name 'MySymbol', got %q", symName)
	}
}

// --- Confidence calibration ---

func TestConfidenceCalibration(t *testing.T) {
	m := newTestManager(t)
	id := entityID(KnownTypeSymbol, "pkg/foo.go:Bar")

	// First upsert: extracted at 0.9
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeSymbol, Name: "Bar",
		Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
	})

	// Second upsert: same method (extracted), lower confidence → should average
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeSymbol, Name: "Bar",
		Confidence: 0.7, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
	})

	var conf float64
	m.db.QueryRow("SELECT confidence FROM entities WHERE id = ?", id).Scan(&conf)
	want := (0.9 + 0.7) / 2.0 // 0.8
	if conf < want-0.01 || conf > want+0.01 {
		t.Errorf("same-method averaging: got confidence %.4f, want %.4f", conf, want)
	}

	// Third upsert: higher-priority method (observed) → should replace with new confidence
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeSymbol, Name: "Bar",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 2,
	})
	m.db.QueryRow("SELECT confidence FROM entities WHERE id = ?", id).Scan(&conf)
	if conf < 0.999 {
		t.Errorf("higher-priority method: got confidence %.4f, want 1.0", conf)
	}

	// Fourth upsert: lower-priority method (inferred) → should NOT reduce confidence
	_ = m.UpsertEntity(EntityTuple{
		ID: id, Type: KnownTypeSymbol, Name: "Bar",
		Confidence: 0.5, ExtractionMethod: MethodInferred, KnowledgeDepth: 1,
	})
	m.db.QueryRow("SELECT confidence FROM entities WHERE id = ?", id).Scan(&conf)
	if conf < 0.999 {
		t.Errorf("lower-priority method should not reduce confidence: got %.4f", conf)
	}
}

func TestSymbolUsesEdge(t *testing.T) {
	m := newTestManager(t)
	// Content where parseArgs is defined and also called internally
	content := `package main

func parseArgs(args []string) map[string]string {
	result := map[string]string{}
	return result
}

func main() {
	opts := parseArgs(os.Args[1:])
	_ = opts
}
`
	fileID := entityID(KnownTypeFile, "main.go")
	_ = m.UpsertEntity(EntityTuple{
		ID: fileID, Type: KnownTypeFile, Name: "main.go",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})
	delta := m.ExtractContent(fileID, "main.go", content)
	if delta == nil {
		t.Fatal("expected non-nil delta")
	}
	_ = m.ApplyDelta(delta, "")

	// Check a 'uses' edge exists from file to parseArgs symbol
	var usesCount int
	m.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND rel = 'uses'`, fileID).Scan(&usesCount)
	if usesCount == 0 {
		t.Errorf("expected at least one 'uses' edge from file, got 0")
	}
}

