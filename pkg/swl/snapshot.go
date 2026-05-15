package swl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// anchorNames is the set of uppercase base-names (without extension) that
// qualify a file as an anchor document — a file that states the purpose,
// goals, or structure of the directory it lives in.
// Phase B moves this to swl.rules.yaml anchor_patterns.
var anchorNames = map[string]bool{
	"README": true, "OVERVIEW": true, "ARCHITECTURE": true, "CONTRIBUTING": true,
	"CHANGELOG": true, "CHANGES": true, "HISTORY": true, "HACKING": true,
	"DESIGN": true, "GOALS": true, "VISION": true, "ABOUT": true,
}

// manifestNames are files that carry project identity and dependency metadata.
var manifestNames = map[string]bool{
	"go.mod": true, "package.json": true, "Cargo.toml": true,
	"pyproject.toml": true, "setup.py": true, "requirements.txt": true,
	"pom.xml": true, "build.gradle": true, "Gemfile": true,
	"Makefile": true, "CMakeLists.txt": true, "Dockerfile": true,
	"docker-compose.yml": true,
}

// snapshotMaxDepth is how deep into the workspace we walk looking for
// anchor docs and semantic areas during BuildSnapshot.
const snapshotMaxDepth = 3

// snapshotMaxAnchorBytes caps how many bytes of an anchor document we read;
// we only need the opening paragraphs for description extraction.
const snapshotMaxAnchorBytes = 8192

// BuildSnapshot produces a bounded workspace semantic snapshot by walking up
// to snapshotMaxDepth levels of the workspace.  It emits:
//   - File entities (kind="anchor" or kind="manifest") for README-class and
//     manifest files, enriching the existing File entity with extracted
//     description stored in metadata["description"]
//   - Directory entities (is_semantic_area=true) for directories that contain
//     anchor documents or have a recognizable content profile
//
// Entity types are File and Directory — not the deprecated AnchorDocument and
// SemanticArea types — so snapshot entities merge cleanly with scanner entities.
func (m *Manager) BuildSnapshot(root string) *GraphDelta {
	// Resolve root to absolute before any path operations.
	// Consistent with ScanWorkspace's normalization so that snapshot
	// entity IDs match scanner entity IDs for the same file.
	absRoot := root
	if !filepath.IsAbs(root) {
		if m.workspace != "" {
			absRoot = filepath.Join(m.workspace, root)
		} else {
			absRoot, _ = filepath.Abs(root)
		}
	}

	delta := &GraphDelta{}
	m.snapshotDir(absRoot, absRoot, 0, delta)
	return delta
}

// effectiveSnapshotMaxDepth returns the configured max depth, falling back to
// the package constant when rules are not loaded.
func (m *Manager) effectiveSnapshotMaxDepth() int {
	if m.rules != nil && m.rules.SnapshotMaxDepth > 0 {
		return m.rules.SnapshotMaxDepth
	}
	return snapshotMaxDepth
}

// effectiveAreaMinExtensionPct returns the configured area extension threshold
// as a float64 ratio (0–1), falling back to 0.6 when rules are not loaded.
func (m *Manager) effectiveAreaMinExtensionPct() float64 {
	if m.rules != nil && m.rules.AreaMinExtensionPct > 0 {
		return float64(m.rules.AreaMinExtensionPct) / 100.0
	}
	return 0.6
}

// isAnchorName returns true when the base file name (without extension, uppercased)
// matches a configured anchor pattern.  Falls back to the hardcoded anchorNames map
// when no rules are loaded or the rules list is empty.
func (m *Manager) isAnchorName(baseName string) bool {
	upper := strings.ToUpper(strings.TrimSuffix(baseName, filepath.Ext(baseName)))
	if m.rules != nil && len(m.rules.AnchorPatterns) > 0 {
		for _, p := range m.rules.AnchorPatterns {
			if strings.ToUpper(p) == upper {
				return true
			}
		}
		return false
	}
	return anchorNames[upper]
}

// snapshotDir recursively walks path up to snapshotMaxDepth, collecting
// anchor documents and classifying semantic areas.
func (m *Manager) snapshotDir(absRoot, path string, depth int, delta *GraphDelta) {
	if depth > m.effectiveSnapshotMaxDepth() {
		return
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}

	relPath := m.snapshotRelPath(absRoot, path)
	dirID := entityID(KnownTypeDirectory, relPath)

	extCounts := map[string]int{}
	totalFiles := 0
	anchorIDs := make([]string, 0, len(entries))

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			// Check .swlignore patterns first
			if m.ignoreDirPath(filepath.Join(path, name)) {
				continue
			}
			// Then check default skip dirs
			if m.shouldIgnoreDir(name) {
				continue
			}
			m.snapshotDir(absRoot, filepath.Join(path, name), depth+1, delta)
			continue
		}

		ext := strings.ToLower(filepath.Ext(name))
		// Check .swlignore patterns first
		if m.ignoreFilePath(filepath.Join(path, name)) {
			continue
		}
		// Then check default skip extensions
		if m.shouldIgnoreFile(name) {
			continue
		}
		totalFiles++
		if ext != "" {
			extCounts[ext]++
		}

		if !m.isAnchorFileByName(name) {
			continue
		}

		absFile := filepath.Join(path, name)
		relFile := m.snapshotRelPath(absRoot, absFile)

		content := readTruncated(absFile, snapshotMaxAnchorBytes)

		meta := map[string]any{"kind": "anchor"}
		if manifestNames[name] {
			meta["kind"] = "manifest"
			for k, v := range extractManifestMeta(name, content) {
				meta[k] = v
			}
		} else {
			// Use enriched metadata extraction for non-manifest anchor docs.
			for k, v := range extractAnchorMeta(content) {
				meta[k] = v
			}
		}

		// Emit as File entity (enriching the existing scanner File entity with
		// anchor metadata). KnownTypeAnchorDocument is deprecated.
		anchorID := entityID(KnownTypeFile, relFile)
		anchorIDs = append(anchorIDs, anchorID)
		delta.Entities = append(delta.Entities, EntityTuple{
			ID: anchorID, Type: KnownTypeFile, Name: relFile,
			Metadata:         meta,
			Confidence:       1.0,
			ExtractionMethod: MethodExtracted,
			KnowledgeDepth:   2,
		})
		delta.Edges = append(delta.Edges, EdgeTuple{
			FromID: anchorID, Rel: KnownRelDocuments, ToID: dirID,
		})
	}

	// Classify directory as a semantic area when it has anchor documents or a
	// strong enough content profile (≥3 files with ≥AreaMinExtensionPct% sharing one extension).
	dominantExt, dominantRatio := dominantExtension(extCounts, totalFiles)
	hasStrongProfile := totalFiles >= 3 && dominantRatio >= m.effectiveAreaMinExtensionPct()
	if len(anchorIDs) == 0 && !hasStrongProfile {
		return
	}

	meta := map[string]any{}
	if len(anchorIDs) > 0 {
		meta["documented"] = true
	}
	if dominantExt != "" {
		meta["content_type"] = dominantExt
	}

	// Pull description from the first anchor doc found in this dir.
	if len(anchorIDs) > 0 {
		var desc string
		m.db.QueryRow( //nolint:errcheck
			`SELECT json_extract(metadata,'$.description') FROM entities WHERE id = ? AND type = 'File'`,
			anchorIDs[0],
		).Scan(&desc)
		if desc != "" {
			meta["description"] = desc
		}
	}

	// Emit as Directory entity enriched with is_semantic_area=true.
	// KnownTypeSemanticArea is deprecated — Directory is the canonical type.
	meta["is_semantic_area"] = true
	areaID := entityID(KnownTypeDirectory, relPath)
	// Derive semantic labels for the area from its path (Phase A.2).
	areaLabels := m.DeriveLabels(KnownTypeDirectory, relPath)
	areaMeta := meta
	for k, v := range areaLabels.ToMetadata() {
		if _, exists := areaMeta[k]; !exists {
			areaMeta[k] = v
		}
	}
	delta.Entities = append(delta.Entities, EntityTuple{
		ID: areaID, Type: KnownTypeDirectory, Name: relPath,
		Metadata:         areaMeta,
		Confidence:       0.9,
		ExtractionMethod: MethodExtracted,
		KnowledgeDepth:   1,
	})

	if depth > 0 {
		parentRel := m.snapshotRelPath(absRoot, filepath.Dir(path))
		parentDirID := entityID(KnownTypeDirectory, parentRel)
		delta.Edges = append(delta.Edges, EdgeTuple{
			FromID: parentDirID, Rel: KnownRelHasArea, ToID: areaID,
		})
	}
}

// snapshotRelPath returns path relative to absRoot, or path unchanged if
// relativisation fails (e.g. path == absRoot returns ".").
func (m *Manager) snapshotRelPath(absRoot, path string) string {
	if m.workspace != "" {
		if rel, err := filepath.Rel(m.workspace, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if rel, err := filepath.Rel(absRoot, path); err == nil {
		if rel == "" {
			return "."
		}
		return rel
	}
	return path
}

// isAnchorFileByName is the Manager method equivalent of isAnchorFile.
// It uses m.isAnchorName (rules-driven) for anchor name matching.
func (m *Manager) isAnchorFileByName(name string) bool {
	if manifestNames[name] {
		return true
	}
	return m.isAnchorName(name)
}

// isAnchorFile returns true when name (case-insensitive base without extension)
// matches an anchor document name, or when name is a known manifest file.
func isAnchorFile(name string) bool {
	if manifestNames[name] {
		return true
	}
	base := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	return anchorNames[base]
}

// extractAnchorMeta parses the first snapshotMaxAnchorBytes of an anchor doc
// and returns enriched metadata with description, title, goals, and sections.
// Uses headingRE (from extractor.go) for consistent heading detection.
func extractAnchorMeta(content string) map[string]any {
	meta := map[string]any{}

	// Extract all headings in one pass using the shared regex.
	var firstHeading string
	var sections []string
	for _, m := range headingRE.FindAllStringSubmatch(content, -1) {
		level, text := m[1], strings.TrimSpace(m[2])
		if level == "#" {
			if firstHeading == "" {
				firstHeading = text
			}
		} else {
			sections = append(sections, text)
			if firstHeading == "" {
				firstHeading = text
			}
		}
	}

	// Extract first paragraph (non-heading, non-code prose).
	goalKeywords := []string{"goal", "purpose", "designed to", "aims to", "intended to", "mission", "vision"}
	var description string
	var goals, paraLines []string
	inPara := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if inPara && description == "" && len(paraLines) > 0 {
				description = strings.Join(paraLines, " ")
			}
			inPara = false
			paraLines = nil
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		inPara = true
		paraLines = append(paraLines, trimmed)
		lower := strings.ToLower(trimmed)
		for _, kw := range goalKeywords {
			if strings.Contains(lower, kw) {
				goals = append(goals, trimmed)
				break
			}
		}
	}
	if description == "" && len(paraLines) > 0 {
		description = strings.Join(paraLines, " ")
	}
	if len(description) > 300 {
		description = description[:300]
	}

	if description != "" {
		meta["description"] = description
	}
	if firstHeading != "" {
		meta["title"] = firstHeading
	}
	if len(goals) > 5 {
		goals = goals[:5]
	}
	if len(goals) > 0 {
		meta["goals"] = goals
	}
	if len(sections) > 10 {
		sections = sections[:10]
	}
	if len(sections) > 0 {
		meta["sections"] = sections
	}
	return meta
}

// extractDescription extracts a short human-readable description from file
// content.  It understands Markdown (extracts first paragraph after H1) and
// falls back to the first non-trivial text line for other formats.
func extractDescription(filename, content string) string {
	if content == "" {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".md", ".markdown", ".rst":
		return extractMarkdownDescription(content)
	default:
		// For go.mod, package.json, etc. the caller uses extractManifestMeta.
		// For unlabelled text use first non-empty, non-comment line.
		return extractFirstLine(content)
	}
}

// extractMarkdownDescription returns the first non-heading paragraph from
// Markdown content (≤280 chars).  If an H1 is present it is skipped.
func extractMarkdownDescription(content string) string {
	lines := strings.Split(content, "\n")
	inParagraph := false
	para := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if inParagraph {
				break
			}
			continue // skip headings before paragraph starts
		}
		if trimmed == "" {
			if inParagraph {
				break
			}
			continue
		}
		inParagraph = true
		para = append(para, trimmed)
		if len(strings.Join(para, " ")) > 280 {
			break
		}
	}
	result := strings.Join(para, " ")
	if len(result) > 280 {
		result = result[:277] + "..."
	}
	return result
}

// extractFirstLine returns the first non-empty, non-comment line of content
// (≤200 chars), used as a fallback description for non-Markdown files.
func extractFirstLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip comment-only lines and shebangs.
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") ||
			strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
			continue
		}
		if len(line) > 200 {
			line = line[:197] + "..."
		}
		return line
	}
	return ""
}

// extractManifestMeta parses known manifest formats and returns key metadata
// fields (name, description, version, module) as a string map.
func extractManifestMeta(filename, content string) map[string]string {
	meta := map[string]string{}
	switch filename {
	case "go.mod":
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				meta["module"] = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			}
			if strings.HasPrefix(line, "go ") {
				meta["go_version"] = strings.TrimSpace(strings.TrimPrefix(line, "go "))
			}
		}
	case "package.json":
		var obj map[string]any
		if json.Unmarshal([]byte(content), &obj) == nil {
			for _, key := range []string{"name", "description", "version"} {
				if v, ok := obj[key].(string); ok && v != "" {
					meta[key] = v
				}
			}
		}
	case "Cargo.toml":
		inPackage := false
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "[package]" {
				inPackage = true
				continue
			}
			if strings.HasPrefix(line, "[") {
				inPackage = false
			}
			if !inPackage {
				continue
			}
			for _, key := range []string{"name", "description", "version"} {
				prefix := key + " = "
				if strings.HasPrefix(line, prefix) {
					val := strings.Trim(strings.TrimPrefix(line, prefix), `"`)
					if val != "" {
						meta[key] = val
					}
				}
			}
		}
	}
	return meta
}

// dominantExtension returns the most common extension and its ratio of total
// files. Returns ("", 0) when extCounts is empty or totalFiles is zero.
func dominantExtension(extCounts map[string]int, totalFiles int) (string, float64) {
	if totalFiles == 0 || len(extCounts) == 0 {
		return "", 0
	}
	best, bestCount := "", 0
	for ext, count := range extCounts {
		if count > bestCount {
			best, bestCount = ext, count
		}
	}
	return best, float64(bestCount) / float64(totalFiles)
}

// readTruncated reads up to maxBytes from a file, stopping at a valid UTF-8
// boundary. Returns empty string on any error.
func readTruncated(path string, maxBytes int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, maxBytes)
	n, _ := f.Read(buf)
	if n == 0 {
		return ""
	}
	s := string(buf[:n])
	// Trim to valid UTF-8 boundary.
	for !strings.HasSuffix(s, string([]rune(s)[len([]rune(s))-1:])) {
		s = s[:len(s)-1]
	}
	return s
}
