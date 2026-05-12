package swl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseIgnoreFile(t *testing.T) {
	// Create a temp directory with test files
	tmpDir := t.TempDir()
	ignorePath := filepath.Join(tmpDir, "swlignore")

	// Test 1: File doesn't exist
	m, err := ParseIgnoreFile("/nonexistent/path/swlignore")
	if err != nil {
		t.Fatalf("ParseIgnoreFile on nonexistent file should not error, got: %v", err)
	}
	if m != nil {
		t.Fatal("ParseIgnoreFile on nonexistent file should return nil")
	}

	// Test 2: Empty file
	if writeErr := os.WriteFile(ignorePath, []byte(""), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	m, err = ParseIgnoreFile(ignorePath)
	if err != nil {
		t.Fatalf("ParseIgnoreFile on empty file should not error, got: %v", err)
	}
	if m != nil {
		t.Fatal("ParseIgnoreFile on empty file should return nil")
	}

	// Test 3: Basic patterns
	content := `# Comment
node_modules/
*.log
.git/
vendor/
!important.txt
`
	if writeErr := os.WriteFile(ignorePath, []byte(content), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	m, err = ParseIgnoreFile(ignorePath)
	if err != nil {
		t.Fatalf("ParseIgnoreFile failed: %v", err)
	}
	if m == nil {
		t.Fatal("ParseIgnoreFile should not return nil for valid file")
	}
	if len(m.patterns) != 5 {
		t.Fatalf("Expected 5 patterns, got %d", len(m.patterns))
	}
}

func TestIgnoreMatcher_Matches(t *testing.T) {
	tmpDir := t.TempDir()
	ignorePath := filepath.Join(tmpDir, "swlignore")

	// Create ignore file with patterns
	content := `node_modules/
*.log
.git/
build/
*.exe
!important.exe
`
	if err := os.WriteFile(ignorePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseIgnoreFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path     string
		isDir    bool
		expected bool
	}{
		// Should be ignored
		{filepath.Join(tmpDir, "node_modules", "pkg", "index.js"), false, true},
		{filepath.Join(tmpDir, "some", "node_modules"), true, true},
		{filepath.Join(tmpDir, "debug.log"), false, true},
		{filepath.Join(tmpDir, ".git", "config"), false, true},
		{filepath.Join(tmpDir, "build", "output.js"), false, true},
		{filepath.Join(tmpDir, "app.exe"), false, true},

		// Should NOT be ignored
		{filepath.Join(tmpDir, "src", "main.go"), false, false},
		{filepath.Join(tmpDir, "important.exe"), false, false}, // negated
		{filepath.Join(tmpDir, "readme.md"), false, false},
	}

	for _, tt := range tests {
		got := m.Matches(tt.path, tt.isDir)
		if got != tt.expected {
			t.Errorf("Matches(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.expected)
		}
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		path     string
		pattern  string
		expected bool
	}{
		// Simple glob
		{"foo.txt", "*.txt", true},
		{"bar.txt", "*.txt", true},
		{"foo.md", "*.txt", false},

		// Double glob **
		{"foo/bar/baz.go", "**/*.go", true},
		{"foo/bar/baz.ts", "**/*.go", false},
		{"src/foo.go", "**/*.go", true},

		// Directory patterns
		{"node_modules/foo", "node_modules/", true},
		{"vendor/foo", "vendor/", true},
		{"src/foo", "vendor/", false},

		// Character classes
		{"foo.go", "[f]*.go", true},
		{"bar.go", "[f]*.go", false},

		// Question mark (? matches exactly one character)
		{"foo.go", "f???.go", false}, // f+oo+.go=6 chars < f+???+.go=7 required
		{"fooo.go", "f???.go", true}, // f+ooo+.go=7 chars matches exactly
	}

	for _, tt := range tests {
		got := matchPattern(tt.path, tt.pattern)
		if got != tt.expected {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.expected)
		}
	}
}

func TestDefaultIgnoreLists(t *testing.T) {
	// Test defaultIgnoreDirNames
	dirs := []string{
		".git", ".svn", ".hg", "node_modules", "vendor",
		"__pycache__", ".venv", "build", "dist", "target",
		".gradle", ".cargo", ".idea", ".vscode", ".swl",
	}
	for _, dir := range dirs {
		if !defaultIgnoreDirNames[dir] {
			t.Errorf("defaultIgnoreDirNames should contain %q", dir)
		}
	}

	// Test defaultIgnoreExtensions
	exts := []string{
		".png", ".jpg", ".gif", ".pdf", ".zip",
		".exe", ".dll", ".so", ".class", ".jar",
		".log", ".tmp", ".lock", ".pem", ".key",
	}
	for _, ext := range exts {
		if !defaultIgnoreExtensions[ext] {
			t.Errorf("defaultIgnoreExtensions should contain %q", ext)
		}
	}
}

func TestManager_shouldIgnoreDir(t *testing.T) {
	// Create a minimal manager for testing
	m := &Manager{}
	m.ignore = nil // No .swlignore loaded

	tests := []struct {
		name     string
		expected bool
	}{
		{".git", true},
		{"node_modules", true},
		{"vendor", true},
		{"__pycache__", true},
		{"build", true},
		{".hidden", true},
		{"src", false},
		{"pkg", false},
		{".", false}, // workspace root
	}

	for _, tt := range tests {
		got := m.shouldIgnoreDir(tt.name)
		if got != tt.expected {
			t.Errorf("shouldIgnoreDir(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestManager_shouldIgnoreFile(t *testing.T) {
	m := &Manager{}

	tests := []struct {
		name     string
		expected bool
	}{
		{"image.png", true},
		{"data.pdf", true},
		{"app.exe", true},
		{"debug.log", true},
		{"backup.bak", true},
		{"main.go", false},
		{"readme.md", false},
	}

	for _, tt := range tests {
		got := m.shouldIgnoreFile(tt.name)
		if got != tt.expected {
			t.Errorf("shouldIgnoreFile(%q) = %v, want %v", tt.name, got, tt.expected)
		}
	}
}
