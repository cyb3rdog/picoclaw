package swl

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// stripToolHeader removes the metadata header lines that read_file prepends to its
// ForLLM output. Headers look like:
//
//	[file: foo.go | total: 4096 bytes | read: bytes 0-4095]
//	[END OF FILE - no further content.]
//
// These lines pollute content-hash checks (making paginated reads of unchanged
// content appear as changes) and regex-based symbol extraction.
// Only lines at the start of the result that begin with '[' are stripped.
func stripToolHeader(result string) string {
	i := 0
	for i < len(result) {
		if result[i] != '[' {
			break
		}
		nl := strings.IndexByte(result[i:], '\n')
		if nl < 0 {
			// entire remaining string is a header line
			return ""
		}
		i += nl + 1
	}
	return result[i:]
}

// normalizePath converts any path to a canonical workspace-relative form so that
// all references to the same file ("/abs/path/f.go", "./rel/f.go", "rel/f.go")
// produce the same entity ID.  Paths outside the workspace are returned as cleaned
// absolute paths.  An empty string is returned unchanged.
func (m *Manager) normalizePath(rawPath string) string {
	if rawPath == "" {
		return rawPath
	}
	p := filepath.Clean(rawPath)
	if !filepath.IsAbs(p) && m.workspace != "" {
		p = filepath.Join(m.workspace, p)
	}
	if m.workspace != "" {
		if rel, err := filepath.Rel(m.workspace, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return p
}

// PostHook runs the three-layer inference pipeline for a completed tool call.
// It is called from the agent SWLHook.AfterTool goroutine.
// sessionKey is the picoclaw session key; it is mapped to a SWL session UUID.
func (m *Manager) PostHook(sessionKey, toolName string, args map[string]any, result string) {
	sessionID := m.EnsureSession(sessionKey)
	m.runInference(sessionID, toolName, args, result)
	m.maybeDecay()
	m.maybePrune()
}

// PreHook runs pre-call guard checks. Currently a pass-through.
// Returns (shouldBlock bool, reason string).
func (m *Manager) PreHook(toolName string, args map[string]any) (bool, string) {
	// Future: constraint registry lookup here.
	return false, ""
}

// --- Three-layer inference ---

// customToolHandlers holds Layer 0 programmatic overrides.
var customToolHandlersMu sync.RWMutex
var customToolHandlers = map[string]func(m *Manager, sessionID string, args map[string]any, result string) *GraphDelta{}

// RegisterToolHandler registers a Layer 0 custom inference function for a named tool.
func RegisterToolHandler(toolName string, fn func(m *Manager, sessionID string, args map[string]any, result string) *GraphDelta) {
	customToolHandlersMu.Lock()
	customToolHandlers[toolName] = fn
	customToolHandlersMu.Unlock()
}

// declRule is a Layer 1 declarative inference rule.
type declRule struct {
	entityExpr string
	entityType EntityType
	rel        EdgeRel
	postApply  func(m *Manager, primaryID, sessionID string, args map[string]any, result string)
}

var toolMap = map[string]declRule{
	"write_file":  {entityExpr: "args.path", entityType: KnownTypeFile, rel: KnownRelWrittenIn, postApply: postApplyWriteFile},
	"edit_file":   {entityExpr: "args.path", entityType: KnownTypeFile, rel: KnownRelEditedIn, postApply: postApplyWriteFile},
	"append_file": {entityExpr: "args.path", entityType: KnownTypeFile, rel: KnownRelAppendedIn, postApply: postApplyAppendFile},
	"read_file":   {entityExpr: "args.path", entityType: KnownTypeFile, rel: KnownRelRead, postApply: postApplyReadFile},
	"delete_file": {entityExpr: "args.path", entityType: KnownTypeFile, rel: KnownRelDeleted, postApply: postApplyDeleteFile},
	"list_dir":    {entityExpr: "args.path", entityType: KnownTypeDirectory, rel: KnownRelListed, postApply: postApplyListDir},
	"exec":        {entityExpr: "args.command", entityType: KnownTypeCommand, rel: KnownRelExecuted, postApply: postApplyExec},
	"web_fetch":   {entityExpr: "args.url", entityType: KnownTypeURL, rel: KnownRelFetched, postApply: postApplyWebFetch},
}

func (m *Manager) runInference(sessionID, toolName string, args map[string]any, result string) {
	// Layer 0: custom handler
	customToolHandlersMu.RLock()
	customFn, hasCustom := customToolHandlers[toolName]
	customToolHandlersMu.RUnlock()
	if hasCustom {
		if delta := customFn(m, sessionID, args, result); delta != nil && !delta.IsEmpty() {
			_ = m.writer.applyDelta(delta, sessionID)
		}
		return
	}

	// Layer 1: declarative tool map
	if rule, ok := toolMap[toolName]; ok {
		primaryName := resolveEntityName(args, rule.entityExpr)
		// Normalize file/directory paths so that absolute vs relative forms of the
		// same path produce the same entity ID (prevents graph fragmentation).
		if (rule.entityType == KnownTypeFile || rule.entityType == KnownTypeDirectory) && primaryName != "" {
			primaryName = m.normalizePath(primaryName)
		}
		if primaryName != "" {
			primaryID := entityID(rule.entityType, primaryName)
			_ = m.writer.upsertEntity(EntityTuple{
				ID: primaryID, Type: rule.entityType, Name: primaryName,
				Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
			})
			_ = m.writer.upsertEdge(EdgeTuple{FromID: primaryID, Rel: rule.rel, ToID: sessionID, SessionID: sessionID})

			if rule.entityType == KnownTypeFile {
				dir := filepath.Dir(primaryName) // primaryName is normalized; dir is consistent
				dirID := entityID(KnownTypeDirectory, dir)
				_ = m.writer.upsertEntity(EntityTuple{
					ID: dirID, Type: KnownTypeDirectory, Name: dir,
					Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
				})
				_ = m.writer.upsertEdge(EdgeTuple{FromID: primaryID, Rel: KnownRelInDir, ToID: dirID})
			}
			if rule.postApply != nil {
				rule.postApply(m, primaryID, sessionID, args, result)
			}
		}
		return
	}

	// Layer 3 (catch-all): generic extraction on result text
	if result != "" {
		if delta := m.ExtractGeneric(toolName, result); delta != nil && !delta.IsEmpty() {
			_ = m.writer.applyDelta(delta, sessionID)
			m.logInferenceEvent(toolName, fmt.Sprintf("generic extracted %d entities", len(delta.Entities)))
		}
	}
}

// --- Layer 1 post-apply functions ---

func postApplyWriteFile(m *Manager, fileID, sessionID string, args map[string]any, result string) {
	content, _ := args["content"].(string)
	if content == "" {
		content = result
	}
	if content == "" {
		return
	}
	m.writer.mu.Lock()
	changed := m.writer.checkAndInvalidateLocked(fileID, content)
	if changed {
		m.writer.bumpKnowledgeDepthLocked(fileID, 3)
	}
	m.writer.mu.Unlock()

	if changed {
		filePath := m.normalizePath(argString(args, "path"))
		if delta := m.ExtractContent(fileID, filePath, content); delta != nil && !delta.IsEmpty() {
			_ = m.writer.applyDelta(delta, sessionID)
			m.logInferenceEvent("write_file", fmt.Sprintf("extracted %d entities from %s", len(delta.Entities), filePath))
		}
	}
	_ = m.writer.setFactStatus(fileID, FactVerified)
}

func postApplyAppendFile(m *Manager, fileID, sessionID string, args map[string]any, result string) {
	m.writer.mu.Lock()
	m.writer.nullContentHashLocked(fileID)
	m.writer.mu.Unlock()
}

func postApplyReadFile(m *Manager, fileID, sessionID string, args map[string]any, result string) {
	if result == "" {
		_ = m.writer.setFactStatus(fileID, FactStale)
		return
	}
	// Strip the [file: ...] / [END OF FILE] header added by read_file before
	// hashing or extracting — otherwise paginated reads of unchanged content
	// produce spurious cache misses and header text pollutes symbol extraction.
	content := stripToolHeader(result)
	if content == "" {
		_ = m.writer.setFactStatus(fileID, FactVerified)
		return
	}

	m.writer.mu.Lock()
	changed := m.writer.checkAndInvalidateLocked(fileID, content)
	if changed {
		m.writer.bumpKnowledgeDepthLocked(fileID, 3)
	}
	m.writer.mu.Unlock()

	// Only extract when content changed or entity is new — consistent with
	// postApplyWriteFile and avoids redundant re-extraction on repeated reads
	// of the same unchanged file (scanner already extracted on first index).
	if changed {
		filePath := m.normalizePath(argString(args, "path"))
		if delta := m.ExtractContent(fileID, filePath, content); delta != nil && !delta.IsEmpty() {
			_ = m.writer.applyDelta(delta, sessionID)
			m.logInferenceEvent("read_file", fmt.Sprintf("extracted %d entities from %s", len(delta.Entities), filePath))
		}
	}
	_ = m.writer.setFactStatus(fileID, FactVerified)
}

func postApplyDeleteFile(m *Manager, fileID, sessionID string, args map[string]any, result string) {
	_ = m.writer.setFactStatus(fileID, FactDeleted)
}

func postApplyListDir(m *Manager, dirID, sessionID string, args map[string]any, result string) {
	if result == "" {
		return
	}
	lines := strings.Split(result, "\n")
	entries := make([]string, 0, len(lines))
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			entries = append(entries, l)
		}
	}
	if delta := ExtractDirectory(dirID, entries); delta != nil && !delta.IsEmpty() {
		_ = m.writer.applyDelta(delta, sessionID)
	}
	_ = m.writer.setFactStatus(dirID, FactVerified)
}

func postApplyExec(m *Manager, cmdID, sessionID string, args map[string]any, result string) {
	command, _ := args["command"].(string)
	if command == "" {
		if action, ok := args["action"].(string); ok {
			command = action
		}
	}
	total := 0
	if delta := ExtractExec(sessionID, command, result, ""); delta != nil && !delta.IsEmpty() {
		_ = m.writer.applyDelta(delta, sessionID)
		total += len(delta.Entities)
	}
	// Secondary generic pass: picks up file paths and tasks that ExtractExec
	// doesn't cover (e.g. grep/cat/go doc output referencing workspace files).
	if result != "" {
		if delta := m.ExtractGeneric("exec:"+command, result); delta != nil && !delta.IsEmpty() {
			_ = m.writer.applyDelta(delta, sessionID)
			total += len(delta.Entities)
		}
	}
	if total > 0 {
		m.logInferenceEvent("exec", fmt.Sprintf("extracted %d entities from %q", total, truncate(command, 40)))
	}
}

func postApplyWebFetch(m *Manager, urlID, sessionID string, args map[string]any, result string) {
	if result == "" {
		_ = m.writer.setFactStatus(urlID, FactStale)
		return
	}
	if delta := ExtractWeb(urlID, result); delta != nil && !delta.IsEmpty() {
		_ = m.writer.applyDelta(delta, sessionID)
	}
	_ = m.writer.setFactStatus(urlID, FactVerified)
}

// --- expression resolvers ---

func resolveEntityName(args map[string]any, expr string) string {
	if !strings.HasPrefix(expr, "args.") {
		return ""
	}
	key := strings.TrimPrefix(expr, "args.")
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}
