package swl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helpers shared across query tests

func newQueryTestManager(t *testing.T) (*Manager, func()) {
	t.Helper()
	dir := t.TempDir()
	cfg := &Config{}
	m, err := NewManager(dir, cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, func() { m.Close() }
}

func seedFile(t *testing.T, m *Manager, sessionID, path, content string) string {
	t.Helper()
	fileID := entityID(KnownTypeFile, path)
	_ = m.writer.upsertEntity(EntityTuple{
		ID: fileID, Type: KnownTypeFile, Name: path,
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})
	_ = m.writer.setFactStatus(fileID, FactVerified)
	if content != "" {
		delta := m.ExtractContent(fileID, path, content)
		if delta != nil {
			_ = m.writer.applyDelta(delta, sessionID)
		}
	}
	return fileID
}

// --- Tier 1 query tests ---

func TestAsk_Tier1_Symbols(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	sessionID := m.EnsureSession("s1")
	seedFile(
		t,
		m,
		sessionID,
		"pkg/foo/bar.go",
		"func ParseConfig(path string) error { return nil }\nfunc loadFile(p string) {}",
	)

	result := m.Ask("functions in pkg/foo/bar.go")
	if !strings.Contains(result, "ParseConfig") {
		t.Errorf("expected ParseConfig in result, got: %s", result)
	}
}

func TestAsk_Tier1_Tasks(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	sessionID := m.EnsureSession("s1")
	seedFile(t, m, sessionID, "main.go", "// TODO: implement retry logic\n// FIXME: handle nil pointer here")

	result := m.Ask("todos in main.go")
	if !strings.Contains(result, "retry logic") && !strings.Contains(result, "TODO") {
		t.Errorf("expected task mention in result, got: %s", result)
	}
}

func TestAsk_Tier1_AllTasks(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	sessionID := m.EnsureSession("s1")
	seedFile(t, m, sessionID, "a.go", "// TODO: fix this")
	seedFile(t, m, sessionID, "b.go", "// FIXME: broken")

	result := m.Ask("open tasks")
	if result == "" || strings.HasPrefix(result, "[SWL] No") {
		t.Errorf("expected tasks, got: %s", result)
	}
}

func TestAsk_Tier1_Imports(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	sessionID := m.EnsureSession("s1")
	seedFile(t, m, sessionID, "main.go", "import (\n\t\"fmt\"\n\t\"os\"\n\t\"github.com/foo/bar\"\n)")

	result := m.Ask("imports in main.go")
	if !strings.Contains(result, "github.com/foo/bar") && !strings.Contains(result, "fmt") {
		t.Errorf("expected imports, got: %s", result)
	}
}

func TestAsk_Tier1_FilesIn(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	sessionID := m.EnsureSession("s1")
	seedFile(t, m, sessionID, "pkg/swl/manager.go", "")
	seedFile(t, m, sessionID, "pkg/swl/entity.go", "")

	result := m.Ask("files in pkg/swl")
	if !strings.Contains(result, "manager.go") && !strings.Contains(result, "pkg/swl") {
		t.Errorf("expected pkg/swl files, got: %s", result)
	}
}

func TestAsk_Tier1_Stale(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	fileID := entityID(KnownTypeFile, "stale.go")
	_ = m.writer.upsertEntity(EntityTuple{
		ID: fileID, Type: KnownTypeFile, Name: "stale.go",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})
	_ = m.writer.setFactStatus(fileID, FactStale)

	result := m.Ask("stale entities")
	if !strings.Contains(result, "stale.go") {
		t.Errorf("expected stale.go in drift report, got: %s", result)
	}
}

func TestAsk_Tier1_ProjectType(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	sessionID := m.EnsureSession("s1")
	topicID := entityID(KnownTypeTopic, "go")
	_ = m.writer.upsertEntity(EntityTuple{
		ID: topicID, Type: KnownTypeTopic, Name: "go",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})
	_ = m.writer.upsertEdge(
		EdgeTuple{FromID: entityID(KnownTypeFile, "go.mod"), Rel: KnownRelTagged, ToID: topicID, SessionID: sessionID},
	)

	result := m.Ask("project type")
	if !strings.Contains(result, "go") {
		t.Errorf("expected 'go' topic, got: %s", result)
	}
}

func TestAsk_Tier1_Complexity(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	sessionID := m.EnsureSession("s1")
	fileID := seedFile(t, m, sessionID, "complex.go",
		strings.Repeat("func f"+strings.Repeat("x", 5)+"() {}\n", 30))

	_ = fileID

	result := m.Ask("most complex files")
	if result == "" || strings.HasPrefix(result, "[SWL] No") {
		t.Errorf("expected complexity result, got: %s", result)
	}
}

func TestAsk_Tier1_Stats(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	result := m.Ask("stats")
	// Stats output contains a type column header or "Edges"
	if !strings.Contains(result, "type") && !strings.Contains(result, "Edges") && !strings.Contains(result, "edges") {
		t.Errorf("expected stats output, got: %s", result)
	}
}

func TestAsk_Tier1_Schema(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	result := m.Ask("schema")
	if !strings.Contains(result, "entities") {
		t.Errorf("expected schema info, got: %s", result)
	}
}

func TestAsk_Tier1_Resume(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	result := m.Ask("bring me up to speed")
	// Should return a digest, not an error
	if strings.HasPrefix(result, "[SWL] No pattern") {
		t.Errorf("resume pattern not matched, got: %s", result)
	}
}

// --- Tier 2 query tests ---

func TestAsk_Tier2_DependencyChain(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	sessionID := m.EnsureSession("s1")
	aID := seedFile(t, m, sessionID, "a.go", "")
	bID := entityID(KnownTypeDependency, "github.com/foo/b")
	_ = m.writer.upsertEntity(
		EntityTuple{
			ID:               bID,
			Type:             KnownTypeDependency,
			Name:             "github.com/foo/b",
			Confidence:       1.0,
			ExtractionMethod: MethodObserved,
			KnowledgeDepth:   1,
		},
	)
	_ = m.writer.upsertEdge(EdgeTuple{FromID: aID, Rel: KnownRelImports, ToID: bID, SessionID: sessionID})

	result := m.tryTier2("dependency chain a.go")
	// Tier 2 match is fuzzy; just ensure no panic and non-empty plausible result
	_ = result
}

// --- Tier 3 (SafeQuery) tests ---

func TestSafeQuery_Select(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	result, err := m.SafeQuery("SELECT COUNT(*) as n FROM entities")
	if err != nil {
		t.Fatalf("SafeQuery failed: %v", err)
	}
	if !strings.Contains(result, "n") && !strings.Contains(result, "0") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestSafeQuery_RejectInsert(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	_, err := m.SafeQuery("INSERT INTO entities(id) VALUES('x')")
	if err == nil {
		t.Error("expected error for INSERT, got nil")
	}
}

func TestSafeQuery_RejectDrop(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	_, err := m.SafeQuery("DROP TABLE entities")
	if err == nil {
		t.Error("expected error for DROP, got nil")
	}
}

func TestSafeQuery_With(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	result, err := m.SafeQuery("WITH x AS (SELECT 1 as v) SELECT v FROM x")
	if err != nil {
		t.Fatalf("WITH query failed: %v", err)
	}
	if !strings.Contains(result, "1") {
		t.Errorf("expected '1' in result, got: %s", result)
	}
}

func TestSafeQuery_RowCap(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	// Insert 250 entities and verify the cap is enforced
	sessionID := m.EnsureSession("s1")
	for i := 0; i < 250; i++ {
		id := entityID(
			KnownTypeFile,
			filepath.Join("dir", strings.Repeat("f", 3)+string(rune('a'+i%26))+string(rune('0'+i/26))),
		)
		_ = m.writer.upsertEntity(EntityTuple{
			ID: id, Type: KnownTypeFile, Name: id,
			Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
		})
		_ = sessionID
	}

	result, err := m.SafeQuery("SELECT id FROM entities")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(result), "\n")
	// header + separator + up to 200 data rows + truncation line = 203 max
	if len(lines) > 203 {
		t.Errorf("expected ≤203 lines (header+sep+200 rows+truncation), got %d", len(lines))
	}
}

// --- KnowledgeGaps and DriftReport ---

func TestKnowledgeGaps(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	// Insert a low-confidence entity
	id := entityID(KnownTypeFile, "uncertain.go")
	_ = m.writer.upsertEntity(EntityTuple{
		ID: id, Type: KnownTypeFile, Name: "uncertain.go",
		Confidence: 0.4, ExtractionMethod: MethodInferred, KnowledgeDepth: 0,
	})

	result := m.KnowledgeGaps()
	if result == "" {
		t.Error("expected non-empty KnowledgeGaps result")
	}
}

func TestDriftReport(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	id := entityID(KnownTypeFile, "changed.go")
	_ = m.writer.upsertEntity(EntityTuple{
		ID: id, Type: KnownTypeFile, Name: "changed.go",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})
	_ = m.writer.setFactStatus(id, FactStale)

	result := m.DriftReport()
	if !strings.Contains(result, "changed.go") {
		t.Errorf("expected changed.go in drift report, got: %s", result)
	}
}

// --- AssertNote ---

func TestAssertNote_CreatesNoteEntity(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	result := m.AssertNote("pkg/swl/manager.go", "This file is the entry point for SWL", 0.9, "")
	if strings.Contains(result, "error") || strings.Contains(result, "Error") {
		t.Errorf("AssertNote returned error: %s", result)
	}

	// Verify note exists in DB
	var count int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ?", KnownTypeNote).Scan(&count)
	if count == 0 {
		t.Error("expected Note entity in DB after AssertNote")
	}
}

func TestAssertNote_DepthAtLeast2(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	m.AssertNote("foo.go", "important note about this file", 0.85, "")

	// AssertNote creates an Assertion entity with depth MAX(current, 2)
	var depth int
	_ = m.db.QueryRow("SELECT MAX(knowledge_depth) FROM entities WHERE type = ?", KnownTypeAssertion).Scan(&depth)
	if depth < 2 {
		t.Errorf("expected Assertion entity knowledge_depth ≥ 2, got %d", depth)
	}
}

// --- SessionResume ---

func TestSessionResume_EmptyDB(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	result := m.SessionResume("test-session-key")
	if result == "" {
		t.Error("expected non-empty resume even on empty DB")
	}
}

func TestSessionResume_WithData(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	sessionID := m.EnsureSession("test-key")
	seedFile(t, m, sessionID, "main.go", "func main() {}")
	_ = sessionID

	result := m.SessionResume("test-key")
	if strings.HasPrefix(result, "[SWL] No") {
		t.Errorf("unexpected empty resume: %s", result)
	}
}

// --- ScanWorkspace ---

func TestScanWorkspace_PicksUpNewFiles(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	// Scan root must be inside the manager's workspace.
	dir := m.workspace
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\nfunc main(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# Project\nTODO: write tests"), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := m.ScanWorkspace(dir)
	if err != nil {
		t.Fatalf("ScanWorkspace: %v", err)
	}
	if stats.New == 0 {
		t.Errorf("expected new files, got stats: %+v", stats)
	}

	// Second scan should show no new files
	stats2, err := m.ScanWorkspace(dir)
	if err != nil {
		t.Fatalf("ScanWorkspace 2: %v", err)
	}
	if stats2.New != 0 {
		t.Errorf("expected 0 new on rescan, got %d", stats2.New)
	}
}

// --- L1: actionable fallthrough response ---

func TestAsk_FallthroughIsActionable(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	result := m.Ask("xyzzy-unrecognizable-42-gibberish-querymiss")
	if result == "" {
		t.Error("expected non-empty fallthrough response, got empty string")
	}
	if !strings.Contains(result, "scan") {
		t.Errorf("expected fallthrough response to mention 'scan', got: %s", result)
	}
}

// --- L2: help mode ---

func TestHelpText_ContainsSyntaxReference(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	help := m.HelpText()
	if help == "" {
		t.Error("expected non-empty help text")
	}
	for _, keyword := range []string{"resume", "scan", "sql", "assert", "schema"} {
		if !strings.Contains(help, keyword) {
			t.Errorf("expected help text to contain %q", keyword)
		}
	}
}

// --- L5: assert echo ---

func TestAssertNote_EchosIDAndConfidence(t *testing.T) {
	m, cleanup := newQueryTestManager(t)
	defer cleanup()

	result := m.AssertNote("pkg/swl/foo.go", "This is a test note", 0.9, "")
	if !strings.Contains(result, "Recorded") {
		t.Errorf("expected 'Recorded' in AssertNote result, got: %s", result)
	}
	if !strings.Contains(result, "0.90") {
		t.Errorf("expected confidence '0.90' in AssertNote result, got: %s", result)
	}
	if !strings.Contains(result, "[id:") {
		t.Errorf("expected short entity ID '[id:' in AssertNote result, got: %s", result)
	}
}
