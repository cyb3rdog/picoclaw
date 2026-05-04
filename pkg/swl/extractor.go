package swl

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// --- Anti-bloat limits ---
const (
	maxSymbols  = 60
	maxImports  = 40
	maxTasks    = 30
	maxSections = 20
	maxURLs     = 20
	maxTopics   = 10
)

// noiseSymbols are universally noisy names discarded during symbol extraction.
// Kept minimal to avoid suppressing valid symbols in non-Go/non-English codebases.
var noiseSymbols = map[string]bool{
	"main": true, "init": true, "test": true,
}

// --- Compiled patterns (initialised once at package load) ---

var (
	symPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^func\s+(?:\(\w+\s+\*?\w+\)\s+)?(\w+)\s*\(`),                          // Go
		regexp.MustCompile(`(?m)^def\s+(\w+)\s*\(`),                                                    // Python
		regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?function\s+(\w+)\s*\(`),                 // JS/TS function
		regexp.MustCompile(`(?m)^\s*(?:pub\s+)?fn\s+(\w+)\s*[(<]`),                                    // Rust fn
		regexp.MustCompile(`(?m)^\s*(?:public|private|protected)?\s*(?:static\s+)?\w[\w<>\[\]]+\s+(\w+)\s*\(`), // Java/C#
		regexp.MustCompile(`(?m)^\s*class\s+(\w+)\s*[({:]`),                                            // multi-lang class
		regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:interface|type)\s+(\w+)\s*[={<]`),                 // TS interface/type
		regexp.MustCompile(`(?m)^\s*(?:pub\s+)?(?:struct|enum|trait)\s+(\w+)\b`),                      // Rust struct/enum/trait
		regexp.MustCompile(`(?m)^type\s+(\w+)\s+(?:struct|interface|func|map|chan|slice|\[)`),          // Go type declarations
		regexp.MustCompile(`(?m)^\s*(?:const|var|let)\s+([A-Z][A-Z0-9_]{2,})\s*=`),                   // exported constants
	}

	importPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^import\s+"?([^\s"]+)"?`),                              // Python bare / generic
		regexp.MustCompile(`(?m)^from\s+(\S+)\s+import`),                               // Python from
		regexp.MustCompile(`(?m)^\t"([^"]+)"`),                                         // Go (tab-indented in import block)
		regexp.MustCompile(`(?m)^import\s+(?:\{[^}]+\}|\*\s+as\s+\w+|\w+)\s+from\s+"([^"]+)"`), // ES6 named/star/default
		regexp.MustCompile(`(?m)^\s*use\s+([\w:]+)`),                                  // Rust use
		regexp.MustCompile(`(?m)^#include\s+[<"]([^>"]+)[>"]`),                        // C/C++
		regexp.MustCompile(`(?m)^\s*require\s*\(?['"]([^'"]+)['"]\)?`),                // Ruby/Node.js require
	}

	taskRE    = regexp.MustCompile(`(?i)(?:TODO|FIXME|HACK|NOTE|BUG|XXX|OPTIMIZE|REFACTOR|REVIEW|DEPRECATED)[:\s]+(.+)`)
	headingRE = regexp.MustCompile(`(?m)^(#{1,3})\s+(.+)`)
	urlRE     = regexp.MustCompile(`https?://[^\s"'<>)\]]+`)

	// filePathRE matches absolute Unix/Windows paths and relative paths that
	// look like workspace files (contain a slash and end with a known extension).
	filePathRE = regexp.MustCompile(
		`(?:^|[\s'"` + "`" + `(])` +
			`(/[\w./-]+\.\w+|` + // absolute: /foo/bar.go
			`[\w][\w.-]*/[\w./-]+\.\w+)` + // relative: pkg/foo/bar.go
			`(?:$|[\s'"` + "`" + `):,])`)

	// backtickFileRE matches filenames inside backticks: `pkg/foo/bar.go`
	backtickFileRE = regexp.MustCompile("`([^`]+\\.\\w+)`")

	// exec output patterns
	gitCommitRE  = regexp.MustCompile(`(?m)^commit\s+([0-9a-f]{7,40})`)
	testPassRE   = regexp.MustCompile(`(?i)(?:PASS|ok)\s+([\w./]+)`)
	testFailRE   = regexp.MustCompile(`(?i)(?:FAIL|--- FAIL)\s+([\w./]+)`)
	pipPkgRE     = regexp.MustCompile(`(?m)^Successfully installed\s+(.+)`)
	npmPkgRE     = regexp.MustCompile(`(?m)^\+\s+([\w@/-]+)`)
	goPkgRE      = regexp.MustCompile(`(?m)^go:\s+(?:downloading|adding)\s+([\w./]+)`)

	// topic detection: special filenames → project type
	projectTypeFiles = map[string]string{
		"go.mod": "go", "go.sum": "go",
		"package.json": "nodejs", "package-lock.json": "nodejs", "yarn.lock": "nodejs", "pnpm-lock.yaml": "nodejs",
		"requirements.txt": "python", "setup.py": "python", "pyproject.toml": "python", "Pipfile": "python",
		"Cargo.toml": "rust", "Cargo.lock": "rust",
		"pom.xml": "java", "build.gradle": "java",
		"Gemfile": "ruby",
		"CMakeLists.txt": "cmake",
		"Makefile": "make",
		"Dockerfile": "docker", "docker-compose.yml": "docker",
		".github": "github-actions",
	}

	// file extension → language topic
	extTopics = map[string]string{
		".go": "go", ".py": "python", ".js": "javascript", ".ts": "typescript",
		".tsx": "typescript", ".jsx": "javascript", ".rs": "rust",
		".java": "java", ".cs": "csharp", ".cpp": "cpp", ".c": "c",
		".h": "c", ".rb": "ruby", ".php": "php", ".swift": "swift",
		".kt": "kotlin", ".md": "markdown", ".yaml": "yaml", ".yml": "yaml",
		".json": "json", ".toml": "toml", ".sh": "shell", ".bash": "shell",
	}
)

// --- Public extraction functions ---

// ExtractContent extracts knowledge from file content into a GraphDelta.
// fileID and filePath identify the entity; content is the raw file text.
func (m *Manager) ExtractContent(fileID, filePath, content string) *GraphDelta {
	if isBinary(content) {
		return nil
	}
	maxSize := m.cfg.effectiveMaxFileSize()
	if int64(len(content)) > maxSize {
		// Truncate to a valid UTF-8 boundary to avoid splitting multi-byte sequences.
		content = strings.ToValidUTF8(content[:maxSize], "")
	}

	// Normalize filePath so Symbol entity IDs are consistent whether the file
	// was indexed via scanner (absolute path) or inference (relative path).
	normFilePath := m.normalizePath(filePath)

	// Run with a 2-second timeout to guard against catastrophic backtracking.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	delta := &GraphDelta{}

	// Language topic from extension
	ext := strings.ToLower(filepath.Ext(filePath))
	if lang, ok := extTopics[ext]; ok {
		topicID := entityID(KnownTypeTopic, lang)
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: topicID, Type: KnownTypeTopic, Name: lang,
			Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
		})
		delta.Edges = append(delta.Edges, EdgeTuple{FromID: fileID, Rel: KnownRelTagged, ToID: topicID})
	}

	if m.cfg.effectiveExtractSymbols() {
		extractSymbols(ctx, fileID, normFilePath, content, delta)
	}
	if m.cfg.effectiveExtractImports() {
		extractImports(ctx, fileID, content, delta)
	}
	if m.cfg.effectiveExtractTasks() {
		extractTasks(ctx, fileID, content, delta)
	}
	if m.cfg.effectiveExtractSections() {
		extractSections(ctx, fileID, content, delta)
	}
	if m.cfg.effectiveExtractURLs() {
		extractURLs(ctx, fileID, content, delta)
	}

	return delta
}

// ExtractDirectory extracts topics from a directory listing.
// entries is a list of filenames (not full paths) present in the directory.
func ExtractDirectory(dirID string, entries []string) *GraphDelta {
	delta := &GraphDelta{}
	topicsSeen := map[string]bool{}

	for _, name := range entries {
		base := filepath.Base(name)
		if pt, ok := projectTypeFiles[base]; ok && !topicsSeen[pt] {
			topicsSeen[pt] = true
			topicID := entityID(KnownTypeTopic, pt)
			delta.Entities = append(delta.Entities, EntityTuple{
				ID: topicID, Type: KnownTypeTopic, Name: pt,
				Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
			})
			delta.Edges = append(delta.Edges, EdgeTuple{FromID: dirID, Rel: KnownRelTagged, ToID: topicID})
		}
	}
	return delta
}

// ExtractExec extracts knowledge from exec tool output.
func ExtractExec(sessionID, command, stdout, stderr string) *GraphDelta {
	if command == "" && stdout == "" {
		return nil
	}
	delta := &GraphDelta{}

	cmdID := entityID(KnownTypeCommand, command)
	delta.Entities = append(delta.Entities, EntityTuple{
		ID: cmdID, Type: KnownTypeCommand, Name: command,
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})

	combined := stdout + "\n" + stderr

	// Git commits
	for _, m := range gitCommitRE.FindAllStringSubmatch(combined, 5) {
		commitID := entityID(KnownTypeCommit, m[1])
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: commitID, Type: KnownTypeCommit, Name: m[1],
			Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
		})
		delta.Edges = append(delta.Edges, EdgeTuple{FromID: cmdID, Rel: KnownRelCommittedIn, ToID: commitID, SessionID: sessionID})
	}

	// Test results
	for _, m := range testPassRE.FindAllStringSubmatch(combined, 20) {
		pkgID := entityID(KnownTypeDependency, m[1])
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: pkgID, Type: KnownTypeDependency, Name: m[1],
			Confidence: 0.9, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
			Metadata: map[string]any{"test_status": "pass"},
		})
	}
	for _, m := range testFailRE.FindAllStringSubmatch(combined, 20) {
		pkgID := entityID(KnownTypeDependency, m[1])
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: pkgID, Type: KnownTypeDependency, Name: m[1],
			Confidence: 0.9, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
			Metadata: map[string]any{"test_status": "fail"},
		})
	}

	// Package installs
	for _, re := range []*regexp.Regexp{pipPkgRE, npmPkgRE, goPkgRE} {
		for _, m := range re.FindAllStringSubmatch(combined, 10) {
			name := strings.TrimSpace(m[1])
			if name == "" {
				continue
			}
			depID := entityID(KnownTypeDependency, name)
			delta.Entities = append(delta.Entities, EntityTuple{
				ID: depID, Type: KnownTypeDependency, Name: name,
				Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
			})
		}
	}

	// URLs in output
	urls := urlRE.FindAllString(combined, maxURLs)
	for _, u := range urls {
		urlID := entityID(KnownTypeURL, u)
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: urlID, Type: KnownTypeURL, Name: u,
			Confidence: 0.9, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
		})
		delta.Edges = append(delta.Edges, EdgeTuple{FromID: cmdID, Rel: KnownRelFound, ToID: urlID, SessionID: sessionID})
	}

	return delta
}

// ExtractWeb extracts knowledge from web_fetch result content.
func ExtractWeb(urlID, pageContent string) *GraphDelta {
	if pageContent == "" {
		return nil
	}
	delta := &GraphDelta{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Sections / headings from page
	count := 0
	for _, m := range headingRE.FindAllStringSubmatch(pageContent, -1) {
		if isDone(ctx) || count >= maxSections {
			break
		}
		title := strings.TrimSpace(m[2])
		if title == "" {
			continue
		}
		secID := entityID(KnownTypeSection, urlID+":"+title)
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: secID, Type: KnownTypeSection, Name: title,
			Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
		})
		delta.Edges = append(delta.Edges, EdgeTuple{FromID: urlID, Rel: KnownRelHasSection, ToID: secID})
		count++
	}

	// Linked URLs
	urls := urlRE.FindAllString(pageContent, maxURLs)
	for _, u := range urls {
		if u == "" {
			continue
		}
		linkedID := entityID(KnownTypeURL, u)
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: linkedID, Type: KnownTypeURL, Name: u,
			Confidence: 0.8, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
		})
		delta.Edges = append(delta.Edges, EdgeTuple{FromID: urlID, Rel: KnownRelFound, ToID: linkedID})
	}

	return delta
}

// ExtractLLMResponse extracts knowledge from an LLM response text.
// Used by SWLHook.AfterLLM to capture what the LLM stated/inferred.
func (m *Manager) ExtractLLMResponse(sessionID, content string) *GraphDelta {
	if content == "" || !m.cfg.effectiveExtractLLMContent() {
		return nil
	}
	if int64(len(content)) > m.cfg.effectiveMaxFileSize() {
		content = content[:m.cfg.effectiveMaxFileSize()]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	delta := &GraphDelta{}

	// Tasks/TODOs the LLM mentions
	count := 0
	for _, match := range taskRE.FindAllStringSubmatch(content, -1) {
		if isDone(ctx) || count >= maxTasks {
			break
		}
		text := strings.TrimSpace(match[1])
		if text == "" {
			continue
		}
		taskID := entityID(KnownTypeTask, text)
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: taskID, Type: KnownTypeTask, Name: text,
			Confidence: 0.7, ExtractionMethod: MethodInferred, KnowledgeDepth: 1,
		})
		count++
	}

	// URLs the LLM mentions
	if !isDone(ctx) {
		for _, u := range urlRE.FindAllString(content, maxURLs) {
			urlID := entityID(KnownTypeURL, u)
			delta.Entities = append(delta.Entities, EntityTuple{
				ID: urlID, Type: KnownTypeURL, Name: u,
				Confidence: 0.7, ExtractionMethod: MethodInferred, KnowledgeDepth: 1,
			})
		}
	}

	// File paths mentioned in the response (e.g. "I'll edit `pkg/foo/bar.go`")
	if !isDone(ctx) {
		seen := map[string]bool{}
		pathCount := 0
		for _, paths := range [][][]string{
			backtickFileRE.FindAllStringSubmatch(content, -1),
			filePathRE.FindAllStringSubmatch(content, -1),
		} {
			for _, match := range paths {
				if isDone(ctx) || pathCount >= 20 {
					break
				}
				p := strings.TrimSpace(match[1])
				if p == "" {
					continue
				}
				// Normalize so inferred paths unify with observed ones.
				p = m.normalizePath(p)
				if seen[p] {
					continue
				}
				seen[p] = true
				fileID := entityID(KnownTypeFile, p)
				delta.Entities = append(delta.Entities, EntityTuple{
					ID: fileID, Type: KnownTypeFile, Name: p,
					Confidence: 0.6, ExtractionMethod: MethodInferred, KnowledgeDepth: 1,
				})
				pathCount++
			}
		}
	}

	return delta
}

// ExtractGeneric is the catch-all Layer 3 extractor for unknown tools.
// Applied when no specific extractor matches — extracts URLs, file paths, and tasks.
func (m *Manager) ExtractGeneric(toolName, result string) *GraphDelta {
	if result == "" {
		return nil
	}
	if int64(len(result)) > m.cfg.effectiveMaxFileSize() {
		result = result[:m.cfg.effectiveMaxFileSize()]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	delta := &GraphDelta{}

	// Tool entity — use KnownTypeTool constant
	toolID := entityID(KnownTypeTool, toolName)
	delta.Entities = append(delta.Entities, EntityTuple{
		ID: toolID, Type: KnownTypeTool, Name: toolName,
		Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
	})

	// URLs in result
	for _, u := range urlRE.FindAllString(result, maxURLs) {
		if isDone(ctx) {
			break
		}
		urlID := entityID(KnownTypeURL, u)
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: urlID, Type: KnownTypeURL, Name: u,
			Confidence: 0.8, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
		})
		delta.Edges = append(delta.Edges, EdgeTuple{FromID: toolID, Rel: KnownRelFound, ToID: urlID})
	}

	// File paths referenced in result (e.g. MCP tools returning file listings)
	if !isDone(ctx) {
		seen := map[string]bool{}
		pathCount := 0
		for _, paths := range [][][]string{
			backtickFileRE.FindAllStringSubmatch(result, -1),
			filePathRE.FindAllStringSubmatch(result, -1),
		} {
			for _, match := range paths {
				if isDone(ctx) || pathCount >= 20 {
					break
				}
				p := strings.TrimSpace(match[1])
				if p == "" || seen[p] {
					continue
				}
				seen[p] = true
				fileID := entityID(KnownTypeFile, p)
				delta.Entities = append(delta.Entities, EntityTuple{
					ID: fileID, Type: KnownTypeFile, Name: p,
					Confidence: 0.6, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
				})
				delta.Edges = append(delta.Edges, EdgeTuple{FromID: toolID, Rel: KnownRelFound, ToID: fileID})
				pathCount++
			}
		}
	}

	// Task/TODO patterns in result text
	if !isDone(ctx) {
		taskCount := 0
		for _, match := range taskRE.FindAllStringSubmatch(result, -1) {
			if isDone(ctx) || taskCount >= maxTasks {
				break
			}
			text := strings.TrimSpace(match[1])
			if text == "" {
				continue
			}
			taskID := entityID(KnownTypeTask, text)
			delta.Entities = append(delta.Entities, EntityTuple{
				ID: taskID, Type: KnownTypeTask, Name: text,
				Confidence: 0.7, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
			})
			delta.Edges = append(delta.Edges, EdgeTuple{FromID: toolID, Rel: KnownRelFound, ToID: taskID})
			taskCount++
		}
	}

	return delta
}

// --- internal helpers ---

func extractSymbols(ctx context.Context, fileID, filePath, content string, delta *GraphDelta) {
	seen := map[string]bool{}
	count := 0
	for _, re := range symPatterns {
		for _, m := range re.FindAllStringSubmatch(content, -1) {
			if isDone(ctx) || count >= maxSymbols {
				return
			}
			name := strings.TrimSpace(m[1])
			if name == "" || noiseSymbols[name] || seen[name] {
				continue
			}
			seen[name] = true
			symID := entityID(KnownTypeSymbol, filePath+":"+name)
			delta.Entities = append(delta.Entities, EntityTuple{
				ID: symID, Type: KnownTypeSymbol, Name: name,
				Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 2,
			})
			delta.Edges = append(delta.Edges, EdgeTuple{FromID: fileID, Rel: KnownRelDefines, ToID: symID})
			count++
		}
	}
}

func extractImports(ctx context.Context, fileID, content string, delta *GraphDelta) {
	seen := map[string]bool{}
	count := 0
	for _, re := range importPatterns {
		for _, m := range re.FindAllStringSubmatch(content, -1) {
			if isDone(ctx) || count >= maxImports {
				return
			}
			name := strings.TrimSpace(m[1])
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			depID := entityID(KnownTypeDependency, name)
			delta.Entities = append(delta.Entities, EntityTuple{
				ID: depID, Type: KnownTypeDependency, Name: name,
				Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
			})
			delta.Edges = append(delta.Edges, EdgeTuple{FromID: fileID, Rel: KnownRelImports, ToID: depID})
			count++
		}
	}
}

func extractTasks(ctx context.Context, fileID, content string, delta *GraphDelta) {
	count := 0
	for _, m := range taskRE.FindAllStringSubmatch(content, -1) {
		if isDone(ctx) || count >= maxTasks {
			return
		}
		text := strings.TrimSpace(m[1])
		if text == "" {
			continue
		}
		taskID := entityID(KnownTypeTask, fileID+":"+text)
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: taskID, Type: KnownTypeTask, Name: text,
			Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 2,
		})
		delta.Edges = append(delta.Edges, EdgeTuple{FromID: fileID, Rel: KnownRelHasTask, ToID: taskID})
		count++
	}
}

func extractSections(ctx context.Context, fileID, content string, delta *GraphDelta) {
	count := 0
	for _, m := range headingRE.FindAllStringSubmatch(content, -1) {
		if isDone(ctx) || count >= maxSections {
			return
		}
		title := strings.TrimSpace(m[2])
		if title == "" {
			continue
		}
		secID := entityID(KnownTypeSection, fileID+":"+title)
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: secID, Type: KnownTypeSection, Name: title,
			Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 2,
		})
		delta.Edges = append(delta.Edges, EdgeTuple{FromID: fileID, Rel: KnownRelHasSection, ToID: secID})
		count++
	}
}

func extractURLs(ctx context.Context, fileID, content string, delta *GraphDelta) {
	count := 0
	for _, u := range urlRE.FindAllString(content, -1) {
		if isDone(ctx) || count >= maxURLs {
			return
		}
		urlID := entityID(KnownTypeURL, u)
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: urlID, Type: KnownTypeURL, Name: u,
			Confidence: 0.9, ExtractionMethod: MethodExtracted, KnowledgeDepth: 1,
		})
		delta.Edges = append(delta.Edges, EdgeTuple{FromID: fileID, Rel: KnownRelMentions, ToID: urlID})
		count++
	}
}

// isBinary returns true if the content appears to be binary data.
func isBinary(content string) bool {
	if !utf8.ValidString(content) {
		return true
	}
	check := content
	if len(check) > 512 {
		check = check[:512]
	}
	for _, b := range []byte(check) {
		if b == 0 {
			return true
		}
	}
	return false
}

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
