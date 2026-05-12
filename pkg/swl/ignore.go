package swl

import (
	"os"
	"path/filepath"
	"strings"
)

// ignorePattern represents a single pattern from a .swlignore file.
type ignorePattern struct {
	dirOnly  bool   // pattern ends with /
	negation bool   // pattern starts with !
	raw      string // original pattern text
}

// ignoreMatcher wraps parsed gitignore-compatible patterns.
type ignoreMatcher struct {
	patterns []ignorePattern
	root     string // absolute root path for relativization
}

// defaultIgnoreDirNames are always-ignored directory names (fallback when .swlignore missing).
var defaultIgnoreDirNames = map[string]bool{
	".git":             true,
	".svn":             true,
	".hg":              true,
	".bzr":             true,
	"CVS":              true,
	"node_modules":     true,
	"vendor":           true,
	"bower_components": true,
	"jspm_packages":    true,
	".venv":            true,
	"venv":             true,
	".env":             true,
	"ENV":              true,
	"__pycache__":      true,
	".tox":             true,
	".pytest_cache":    true,
	".mypy_cache":      true,
	".ruff_cache":      true,
	".hypothesis":      true,
	"dist":             true,
	"build":            true,
	".build":           true,
	"target":           true,
	".gradle":          true,
	".cargo":           true,
	".dart_tool":       true,
	".pub-cache":       true,
	".pub":             true,
	"bin":              true,
	"obj":              true,
	".idea":            true,
	".vscode":          true,
	".swl":             true, // never index the SWL DB directory itself
}

// defaultIgnoreExtensions are always-ignored file extensions.
var defaultIgnoreExtensions = map[string]bool{
	// Images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".bmp": true, ".ico": true, ".svg": true, ".webp": true,
	".avif": true,
	// Media
	".mp3": true, ".mp4": true, ".wav": true, ".flac": true,
	".ogg": true, ".webm": true, ".mov": true, ".avi": true,
	".mkv": true,
	// Documents
	".pdf": true, ".doc": true, ".docx": true, ".xls": true,
	".xlsx": true, ".ppt": true, ".pptx": true, ".odt": true,
	".ods": true, ".odp": true, ".rtf": true, ".epub": true,
	".mobi": true,
	// Archives
	".zip": true, ".tar": true, ".gz": true, ".bz2": true,
	".xz": true, ".7z": true, ".rar": true, ".tgz": true,
	// Binaries
	".so": true, ".a": true, ".o": true, ".dylib": true,
	".exe": true, ".dll": true, ".bin": true, ".class": true,
	".jar": true, ".war": true, ".ear": true, ".pdb": true,
	".ilk": true, ".obj": true, ".pch": true, ".pgc": true,
	".pgd": true, ".rsp": true, ".sbr": true, ".tlb": true,
	".tli": true, ".tlh": true, ".rlib": true,
	".whl": true, ".pyc": true, ".pyo": true, ".db": true,
	".sqlite": true, ".sqlite3": true,
	// Lock files
	".lock": true,
	// Logs & temp
	".log": true, ".tmp": true, ".bak": true, ".swp": true,
	".swo": true, "*~": true,
	// Security
	".pem": true, ".key": true, ".crt": true, ".p12": true,
	".pfx": true, ".jks": true,
}

// ParseIgnoreFile reads a .swlignore file and returns an ignoreMatcher.
// Returns nil if the file doesn't exist (caller should use defaults).
// Patterns are relative to the directory containing the file.
func ParseIgnoreFile(path string) (*ignoreMatcher, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	dir := filepath.Dir(path)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	patterns := parseIgnorePatterns(string(data))
	if len(patterns) == 0 {
		return nil, nil
	}

	return &ignoreMatcher{
		patterns: patterns,
		root:     absDir,
	}, nil
}

// parseIgnorePatterns converts a .swlignore file content into a list of patterns.
func parseIgnorePatterns(content string) []ignorePattern {
	splitLines := strings.Split(content, "\n")
	patterns := make([]ignorePattern, 0, len(splitLines))
	for _, line := range splitLines {
		line = strings.TrimRight(line, " \t\r")
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Handle Windows-style line endings
		line = strings.TrimSuffix(line, "\r")

		p := ignorePattern{raw: line}

		// Handle negation
		if strings.HasPrefix(line, "!") {
			p.negation = true
			line = line[1:]
		}

		// Directory-only pattern
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}

		p.raw = line
		patterns = append(patterns, p)
	}
	return patterns
}

// Matches returns true if path matches the ignore patterns.
// Patterns are evaluated in order; later patterns override earlier ones.
// dirOnly patterns match the directory itself and any file inside it.
func (m *ignoreMatcher) Matches(absPath string, isDir bool) bool {
	relPath := absPath
	if m.root != "" {
		if r, err := filepath.Rel(m.root, absPath); err == nil && !strings.HasPrefix(r, "..") {
			relPath = r
		}
	}
	relPath = filepath.ToSlash(relPath)

	ignored := false
	for _, p := range m.patterns {
		var matches bool
		if p.dirOnly && !isDir {
			// For files, check if any ancestor directory matches the pattern.
			matches = anyAncestorDirMatches(relPath, p.raw)
		} else {
			matches = matchPatternAnyDepth(relPath, p.raw)
		}
		if matches {
			if p.negation {
				ignored = false
			} else {
				ignored = true
			}
		}
	}
	return ignored
}

// matchPatternAnyDepth tries matchPattern from each starting path component.
// Non-anchored patterns (no leading /) can match at any directory level.
func matchPatternAnyDepth(path, pattern string) bool {
	if matchPattern(path, pattern) {
		return true
	}
	if !strings.HasPrefix(pattern, "/") {
		parts := strings.Split(path, "/")
		for start := 1; start < len(parts); start++ {
			if matchPattern(strings.Join(parts[start:], "/"), pattern) {
				return true
			}
		}
	}
	return false
}

// anyAncestorDirMatches returns true if any ancestor directory of path matches pattern.
func anyAncestorDirMatches(path, pattern string) bool {
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		dir := strings.Join(parts[:i], "/")
		if matchPatternAnyDepth(dir, pattern) {
			return true
		}
	}
	return false
}

// matchPattern returns true if path matches a gitignore-style pattern.
// Supports: *, **, ?, [abc], [!abc], leading / anchors to root.
func matchPattern(path, pattern string) bool {
	// Leading / anchors to root
	anchored := strings.HasPrefix(pattern, "/")
	if anchored {
		pattern = pattern[1:]
	}
	// trailing / means directory only (already handled by caller)

	// Normalize path separators
	path = filepath.ToSlash(path)
	pattern = filepath.ToSlash(pattern)

	// Handle **
	// Split on ** and match each segment
	parts := strings.Split(path, "/")
	pats := strings.Split(pattern, "/")

	pi := 0
	for i, pat := range pats {
		if pat == "**" {
			// ** matches zero or more directories
			if i == len(pats)-1 {
				// ** at end matches everything remaining
				return true
			}
			// Try matching at each position
			for j := pi; j <= len(parts); j++ {
				if matchSegments(parts[j:], pats[i+1:]) {
					return true
				}
			}
			return false
		} else if pat == "*" {
			// * matches anything except /
			if i == len(pats)-1 {
				// * at end matches without /
				for j := pi; j < len(parts); j++ {
					if !strings.Contains(parts[j], "/") {
						return true
					}
				}
			} else {
				for j := pi; j < len(parts); j++ {
					if !strings.Contains(parts[j], "/") {
						if matchSegments(parts[j+1:], pats[i+1:]) {
							return true
						}
					}
				}
			}
			return false
		} else if strings.HasPrefix(pat, "[") && strings.HasSuffix(pat, "]") {
			// Character class
			negated := strings.HasPrefix(pat, "[!")
			if negated {
				pat = "[!" + pat[2:len(pat)-1] + "]"
			}
			cls := pat[1 : len(pat)-1]
			if len(parts) <= pi {
				return false
			}
			matched := matchCharClass(parts[pi], cls)
			if negated {
				matched = !matched
			}
			if matched {
				return matchSegments(parts[pi+1:], pats[i+1:])
			}
			return false
		} else if pat == "?" {
			// ? matches single character
			if len(parts) <= pi {
				return false
			}
			return matchSegments(parts[pi+1:], pats[i+1:])
		} else {
			// Literal match
			if pat == "" {
				// Empty segment from trailing /: match anything remaining.
				return true
			}
			if len(parts) <= pi {
				return pat == "" && len(pats) == i+1
			}
			if !globMatch(parts[pi], pat) {
				return false
			}
			pi++
			if anchored && i == 0 && len(parts) > pi {
				// Anchored pattern - remaining path must match exactly
				return matchSegments(parts[pi:], pats[i+1:])
			}
		}
	}
	return pi >= len(parts)
}

// matchCharClass returns true if c matches the character class pattern.
func matchCharClass(c string, cls string) bool {
	if len(c) != 1 {
		return false
	}
	ch := c[0]
	// Handle ranges and negation
	inverted := strings.HasPrefix(cls, "!")
	if inverted {
		cls = cls[1:]
	}
	matched := false
	for i := 0; i < len(cls); i++ {
		if i+2 < len(cls) && cls[i+1] == '-' {
			// Range
			lo := cls[i]
			hi := cls[i+2]
			if ch >= lo && ch <= hi {
				matched = true
			}
			i += 2
		} else if cls[i] == '\\' && i+1 < len(cls) {
			if ch == cls[i+1] {
				matched = true
			}
			i++
		} else {
			if ch == cls[i] {
				matched = true
			}
		}
	}
	return matched
}

// globMatch performs glob matching with ? and [].
func globMatch(s, pattern string) bool {
	// Simple glob: * matches anything
	if pattern == "*" {
		return true
	}
	// Check for literal glob characters
	hasGlob := false
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*', '?', '[', ']':
			hasGlob = true
			break
		}
	}
	if !hasGlob {
		return s == pattern
	}
	// Fallback to simple byte matching (handles basic cases)
	return matchGlob([]byte(s), []byte(pattern))
}

func matchGlob(s, pat []byte) bool {
	sIdx := 0
	pIdx := 0
	savedS := -1
	savedP := -1

	for sIdx < len(s) && pIdx < len(pat) {
		if pat[pIdx] == '*' {
			savedS = sIdx
			savedP = pIdx
			pIdx++
		} else if pat[pIdx] == '[' {
			// Find closing ']'
			end := pIdx + 1
			for end < len(pat) && pat[end] != ']' {
				end++
			}
			if end < len(pat) {
				cls := string(pat[pIdx+1 : end])
				negated := strings.HasPrefix(cls, "!")
				if negated {
					cls = cls[1:]
				}
				matched := matchCharClass(string(s[sIdx:sIdx+1]), cls)
				if negated {
					matched = !matched
				}
				if matched {
					sIdx++
					pIdx = end + 1
				} else if savedS >= 0 {
					sIdx = savedS + 1
					pIdx = savedP + 1
					savedS++
				} else {
					return false
				}
			} else {
				// Malformed '[': treat as literal
				if s[sIdx] == '[' {
					sIdx++
					pIdx++
				} else if savedS >= 0 {
					sIdx = savedS + 1
					pIdx = savedP + 1
					savedS++
				} else {
					return false
				}
			}
		} else if pat[pIdx] == '?' || pat[pIdx] == s[sIdx] {
			sIdx++
			pIdx++
		} else if savedS >= 0 {
			sIdx = savedS + 1
			pIdx = savedP + 1
			savedS++
		} else {
			return false
		}
	}

	// Handle remaining pattern
	for pIdx < len(pat) && pat[pIdx] == '*' {
		pIdx++
	}
	return sIdx == len(s) && pIdx == len(pat)
}

// matchSegments matches path segments against pattern segments.
func matchSegments(pathParts, patParts []string) bool {
	if len(patParts) == 0 {
		return len(pathParts) == 0
	}
	for i, pat := range patParts {
		if pat == "**" {
			if i == len(patParts)-1 {
				return true
			}
			for j := 0; j <= len(pathParts); j++ {
				if matchSegments(pathParts[j:], patParts[i+1:]) {
					return true
				}
			}
			return false
		} else if pat == "*" {
			for j := 0; j <= len(pathParts); j++ {
				if matchSegments(pathParts[j:], patParts[i+1:]) {
					return true
				}
			}
			return false
		} else {
			if len(pathParts) == 0 || !globMatch(pathParts[0], pat) {
				return false
			}
			pathParts = pathParts[1:]
		}
	}
	return len(pathParts) == 0
}

// shouldIgnoreDir returns true if the directory name should be ignored.
// Checks both the ignoreMatcher (if loaded) and defaultIgnoreDirNames.
func (m *Manager) shouldIgnoreDir(name string) bool {
	// Always check defaults first
	if defaultIgnoreDirNames[name] {
		return true
	}
	// Check hidden directories (except workspace root ".")
	if strings.HasPrefix(name, ".") && name != "." {
		return true
	}
	return false
}

// shouldIgnoreFile returns true if the file should be ignored.
// Checks both the ignoreMatcher (if loaded) and defaultIgnoreExtensions.
func (m *Manager) shouldIgnoreFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if defaultIgnoreExtensions[ext] {
		return true
	}
	// Check full name patterns that are common
	baseName := strings.ToLower(name)
	if strings.HasSuffix(baseName, ".log") ||
		strings.HasSuffix(baseName, ".tmp") ||
		strings.HasSuffix(baseName, ".bak") ||
		strings.HasSuffix(baseName, ".swp") ||
		strings.HasSuffix(baseName, ".swo") {
		return true
	}
	return false
}

// ignoreFilePath checks if a file path should be ignored via .swlignore.
// Returns true if ignored, false otherwise.
func (m *Manager) ignoreFilePath(absPath string) bool {
	if m.ignore != nil {
		return m.ignore.Matches(absPath, false)
	}
	return false
}

// ignoreDirPath checks if a directory path should be ignored via .swlignore.
// Returns true if ignored, false otherwise.
func (m *Manager) ignoreDirPath(absPath string) bool {
	if m.ignore != nil {
		return m.ignore.Matches(absPath, true)
	}
	return false
}

// loadSwlignore loads the .swlignore file from the SWL directory.
// Returns nil if not found (use defaults).
func (m *Manager) loadSwlignore() error {
	swlDir := filepath.Join(m.workspace, ".swl")
	ignorePath := filepath.Join(swlDir, "swlignore")

	ignore, err := ParseIgnoreFile(ignorePath)
	if err != nil {
		return err
	}
	m.ignore = ignore
	return nil
}

// SwlignorePath returns the path to the .swlignore file.
func (m *Manager) SwlignorePath() string {
	return filepath.Join(m.workspace, ".swl", "swlignore")
}

// HasSwlignore returns true if a .swlignore file is loaded.
func (m *Manager) HasSwlignore() bool {
	return m.ignore != nil
}

// SwlignoreReload reloads the .swlignore file from disk.
func (m *Manager) SwlignoreReload() error {
	return m.loadSwlignore()
}
