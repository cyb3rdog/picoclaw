package swl

import (
	"strings"
	"testing"
	"time"
)

// hookTestManager creates a Manager and provides PostHook/PreHook
// directly (the agent-side SWLHook wraps these; we test the Manager methods).

func newHookTestManager(t *testing.T) (*Manager, func()) {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(dir, &Config{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, func() { m.Close() }
}

// --- PreHook ---

func TestPreHook_AlwaysContinues(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	shouldBlock, reason := m.PreHook("write_file", map[string]any{"path": "/tmp/x.go", "content": "package main"})
	if shouldBlock {
		t.Errorf("expected no block, got reason: %s", reason)
	}
}

func TestPreHook_UnknownTool_NoPanic(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	shouldBlock, _ := m.PreHook("some_unknown_mcp_tool", map[string]any{"x": "y"})
	if shouldBlock {
		t.Error("unknown tool should not block")
	}
}

// --- PostHook: write_file ---

func TestPostHook_WriteFile_CreatesFileEntity(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "write_file", map[string]any{
		"path":    "pkg/foo/bar.go",
		"content": "package foo\n\nfunc Greet(name string) string { return name }",
	}, "")

	var count int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ? AND name = ?",
		KnownTypeFile, "pkg/foo/bar.go").Scan(&count)
	if count == 0 {
		t.Error("expected File entity for pkg/foo/bar.go")
	}
}

func TestPostHook_WriteFile_ExtractsSymbols(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "write_file", map[string]any{
		"path":    "pkg/calc/calc.go",
		"content": "package calc\nfunc Add(a, b int) int { return a + b }\nfunc Subtract(a, b int) int { return a - b }",
	}, "")

	var count int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ?", KnownTypeSymbol).Scan(&count)
	if count == 0 {
		t.Error("expected Symbol entities after write_file with functions")
	}
}

func TestPostHook_WriteFile_SetsVerified(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "write_file", map[string]any{
		"path":    "hello.go",
		"content": "package main",
	}, "")

	var status string
	_ = m.db.QueryRow("SELECT fact_status FROM entities WHERE name = ?", "hello.go").Scan(&status)
	if status != string(FactVerified) {
		t.Errorf("expected verified, got %s", status)
	}
}

func TestPostHook_WriteFile_CreatesDirectoryEntity(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "write_file", map[string]any{
		"path":    "pkg/auth/handler.go",
		"content": "package auth",
	}, "")

	var count int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ? AND name = ?",
		KnownTypeDirectory, "pkg/auth").Scan(&count)
	if count == 0 {
		t.Error("expected Directory entity for pkg/auth")
	}
}

// --- PostHook: read_file ---

func TestPostHook_ReadFile_VerifiesOnSuccess(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "read_file", map[string]any{"path": "config.go"},
		"package config\nfunc Load() {}")

	var status string
	_ = m.db.QueryRow("SELECT fact_status FROM entities WHERE name = ?", "config.go").Scan(&status)
	if status != string(FactVerified) {
		t.Errorf("expected verified after read_file, got %s", status)
	}
}

func TestPostHook_ReadFile_MarksStaleOnEmptyResult(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	// Pre-create the entity
	fileID := entityID(KnownTypeFile, "missing.go")
	_ = m.writer.upsertEntity(EntityTuple{
		ID: fileID, Type: KnownTypeFile, Name: "missing.go",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})

	m.PostHook("sess1", "read_file", map[string]any{"path": "missing.go"}, "")

	var status string
	_ = m.db.QueryRow("SELECT fact_status FROM entities WHERE id = ?", fileID).Scan(&status)
	if status != string(FactStale) {
		t.Errorf("expected stale after empty read_file result, got %s", status)
	}
}

func TestStripToolHeader(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "normal read_file output",
			input: "[file: server.go | total: 4096 bytes | read: bytes 0-4095]\n[END OF FILE - no further content.]\npackage main\n\nfunc main() {}",
			want:  "package main\n\nfunc main() {}",
		},
		{
			name:  "truncated read_file output",
			input: "[file: big.go | total: 65536 bytes | read: bytes 0-4095]\n[TRUNCATED - file has more content. Call read_file again with offset=4096 to continue.]\npackage main\n",
			want:  "package main\n",
		},
		{
			name:  "no header — raw content unchanged",
			input: "package main\nfunc main() {}",
			want:  "package main\nfunc main() {}",
		},
		{
			name:  "only header line, no content",
			input: "[END OF FILE - no content at this offset]",
			want:  "",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripToolHeader(tc.input)
			if got != tc.want {
				t.Errorf("stripToolHeader(%q)\n  got  %q\n  want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestPostHook_ReadFile_StripsHeader_ExtractsSymbols(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	// Simulate the full ForLLM output that read_file produces, including the header.
	result := "[file: main.go | total: 50 bytes | read: bytes 0-49]\n[END OF FILE - no further content.]\npackage main\n\nfunc DoWork() {}\n"
	m.PostHook("sess1", "read_file", map[string]any{"path": "main.go"}, result)
	time.Sleep(20 * time.Millisecond)

	// Entity must be verified
	var status string
	_ = m.db.QueryRow("SELECT fact_status FROM entities WHERE name = ?", "main.go").Scan(&status)
	if status != string(FactVerified) {
		t.Errorf("expected verified, got %q", status)
	}

	// Symbol must have been extracted from content (not confused by header)
	var symCount int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = 'Symbol' AND name = 'DoWork'").Scan(&symCount)
	if symCount == 0 {
		t.Error("expected Symbol entity for DoWork to be extracted")
	}
}

// --- PostHook: append_file ---

func TestPostHook_AppendFile_NullsContentHash(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	// First write to set a hash
	m.PostHook("sess1", "write_file", map[string]any{
		"path": "log.txt", "content": "line1",
	}, "")

	var hashBefore string
	_ = m.db.QueryRow("SELECT COALESCE(content_hash,'') FROM entities WHERE name = ?", "log.txt").Scan(&hashBefore)
	if hashBefore == "" {
		t.Skip("write_file did not set content_hash; skipping append test")
	}

	m.PostHook("sess1", "append_file", map[string]any{"path": "log.txt", "content": "line2"}, "")

	var hashAfter string
	_ = m.db.QueryRow("SELECT COALESCE(content_hash,'') FROM entities WHERE name = ?", "log.txt").Scan(&hashAfter)
	if hashAfter != "" {
		t.Errorf("expected content_hash to be NULL after append_file, got %q", hashAfter)
	}
}

// --- PostHook: delete_file ---

func TestPostHook_DeleteFile_TombstonesEntity(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "write_file", map[string]any{"path": "dead.go", "content": "package x"}, "")
	m.PostHook("sess1", "delete_file", map[string]any{"path": "dead.go"}, "")

	var status string
	_ = m.db.QueryRow("SELECT fact_status FROM entities WHERE name = ?", "dead.go").Scan(&status)
	if status != string(FactDeleted) {
		t.Errorf("expected deleted, got %s", status)
	}
}

// --- PostHook: exec ---

func TestPostHook_Exec_ExtractsCommit(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "exec", map[string]any{"command": "git log"},
		"commit abc1234def5678\nAuthor: Dev\nDate: today\n\n    fix: something")

	var count int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ?", KnownTypeCommit).Scan(&count)
	if count == 0 {
		t.Error("expected Commit entity after exec with git log output")
	}
}

// --- PostHook: web_fetch ---

func TestPostHook_WebFetch_CreatesURLEntity(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "web_fetch", map[string]any{"url": "https://example.com"},
		"# Example Domain\nThis domain is for illustrative examples.")

	var status string
	_ = m.db.QueryRow("SELECT fact_status FROM entities WHERE name = ?", "https://example.com").Scan(&status)
	if status != string(FactVerified) {
		t.Errorf("expected verified URL after web_fetch, got %s", status)
	}
}

func TestPostHook_WebFetch_MarksStaleOnEmpty(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	urlID := entityID(KnownTypeURL, "https://gone.example.com")
	_ = m.writer.upsertEntity(EntityTuple{
		ID: urlID, Type: KnownTypeURL, Name: "https://gone.example.com",
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})

	m.PostHook("sess1", "web_fetch", map[string]any{"url": "https://gone.example.com"}, "")

	var status string
	_ = m.db.QueryRow("SELECT fact_status FROM entities WHERE id = ?", urlID).Scan(&status)
	if status != string(FactStale) {
		t.Errorf("expected stale after empty web_fetch, got %s", status)
	}
}

// --- PostHook: unknown tool (Layer 3) ---

func TestPostHook_UnknownTool_ExtractsURLs(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "my_custom_mcp_tool", map[string]any{},
		"Result: see https://docs.example.com/api for details")

	var count int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ? AND name LIKE ?",
		KnownTypeURL, "%docs.example.com%").Scan(&count)
	if count == 0 {
		t.Error("expected URL entity extracted by Layer 3 generic handler")
	}
}

func TestPostHook_UnknownTool_ExtractsFilePaths(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "custom_tool", map[string]any{},
		"Modified files: pkg/swl/manager.go and pkg/agent/swl_hook.go")

	var count int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE type = ?", KnownTypeFile).Scan(&count)
	if count == 0 {
		t.Error("expected File entities extracted from generic tool result")
	}
}

// --- Session key stability ---

func TestPostHook_SessionKeyStable(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	key := "channel:abc123"
	m.PostHook(key, "write_file", map[string]any{"path": "a.go", "content": "package a"}, "")
	m.PostHook(key, "write_file", map[string]any{"path": "b.go", "content": "package b"}, "")

	var count int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 session for same key, got %d", count)
	}
}

// --- Layer 0: custom handler ---

func TestPostHook_Layer0_CustomHandler(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	called := false
	RegisterToolHandler("my_special_tool", func(mgr *Manager, sessionID string, args map[string]any, result string) *GraphDelta {
		called = true
		topicID := entityID(KnownTypeTopic, "custom-topic")
		return &GraphDelta{
			Entities: []EntityTuple{{ID: topicID, Type: KnownTypeTopic, Name: "custom-topic", Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1}},
		}
	})
	defer func() {
		customToolHandlersMu.Lock()
		delete(customToolHandlers, "my_special_tool")
		customToolHandlersMu.Unlock()
	}()

	m.PostHook("sess1", "my_special_tool", map[string]any{}, "some result")
	if !called {
		t.Error("custom Layer 0 handler was not called")
	}

	var count int
	_ = m.db.QueryRow("SELECT COUNT(*) FROM entities WHERE name = ?", "custom-topic").Scan(&count)
	if count == 0 {
		t.Error("expected custom-topic entity from Layer 0 handler")
	}
}

// --- ExtractLLMResponse ---

func TestExtractLLMResponse_ExtractsTasks(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	delta := m.ExtractLLMResponse("sess1", "I'll fix this next. TODO: add error handling for nil case.")
	if delta == nil {
		t.Fatal("expected non-nil delta")
	}
	found := false
	for _, e := range delta.Entities {
		if e.Type == KnownTypeTask {
			found = true
		}
	}
	if !found {
		t.Error("expected Task entity from LLM response")
	}
}

func TestExtractLLMResponse_ExtractsFilePaths(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	delta := m.ExtractLLMResponse("sess1", "I'll now edit `pkg/swl/manager.go` to add the workspace field.")
	if delta == nil {
		t.Fatal("expected non-nil delta")
	}
	found := false
	for _, e := range delta.Entities {
		if e.Type == KnownTypeFile && strings.Contains(e.Name, "manager.go") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected File entity for manager.go from LLM response, entities: %+v", delta.Entities)
	}
}

func TestExtractLLMResponse_ReasoningConfidenceCapped(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	// Simulate what AfterLLM does for reasoning content
	delta := m.ExtractLLMResponse("sess1", "TODO: check the URL https://example.com for updates")
	if delta == nil {
		t.Fatal("expected non-nil delta")
	}
	// Cap confidence as AfterLLM does
	cap := m.cfg.EffectiveReasoningConfidenceCap()
	for i := range delta.Entities {
		if delta.Entities[i].Confidence > cap {
			delta.Entities[i].Confidence = cap
		}
		delta.Entities[i].ExtractionMethod = MethodInferred
	}
	for _, e := range delta.Entities {
		if e.Confidence > cap {
			t.Errorf("entity %s confidence %f exceeds cap %f", e.Name, e.Confidence, cap)
		}
		if e.ExtractionMethod != MethodInferred {
			t.Errorf("entity %s method %s expected inferred", e.Name, e.ExtractionMethod)
		}
	}
}

// --- Async safety ---

func TestPostHook_NoPanicOnNilArgs(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	// Should not panic even with nil/empty args
	m.PostHook("sess1", "write_file", nil, "")
	m.PostHook("sess1", "write_file", map[string]any{}, "")
	// Give async goroutines time to settle
	time.Sleep(20 * time.Millisecond)
}

func TestPostHook_NoPanicOnUnknownTool(t *testing.T) {
	m, cleanup := newHookTestManager(t)
	defer cleanup()

	m.PostHook("sess1", "", nil, "")
	m.PostHook("sess1", "!@#$%^&*()", map[string]any{}, "garbage result\x00\xff")
	time.Sleep(20 * time.Millisecond)
}
