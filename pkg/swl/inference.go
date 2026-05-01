package swl

import (
	"path/filepath"
	"strings"
	"sync"
)

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
		primaryID := resolveEntityID(rule.entityType, args, rule.entityExpr)
		if primaryID != "" {
			primaryName := resolveEntityName(args, rule.entityExpr)
			_ = m.writer.upsertEntity(EntityTuple{
				ID: primaryID, Type: rule.entityType, Name: primaryName,
				Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
			})
			_ = m.writer.upsertEdge(EdgeTuple{FromID: primaryID, Rel: rule.rel, ToID: sessionID, SessionID: sessionID})

			if rule.entityType == KnownTypeFile {
				dir := filepath.Dir(primaryName)
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
		filePath, _ := args["path"].(string)
		if delta := m.ExtractContent(fileID, filePath, content); delta != nil && !delta.IsEmpty() {
			_ = m.writer.applyDelta(delta, sessionID)
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
	m.writer.mu.Lock()
	changed := m.writer.checkAndInvalidateLocked(fileID, result)
	if changed {
		m.writer.bumpKnowledgeDepthLocked(fileID, 3)
	}
	m.writer.mu.Unlock()

	filePath, _ := args["path"].(string)
	if delta := m.ExtractContent(fileID, filePath, result); delta != nil && !delta.IsEmpty() {
		_ = m.writer.applyDelta(delta, sessionID)
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
	if delta := ExtractExec(sessionID, command, result, ""); delta != nil && !delta.IsEmpty() {
		_ = m.writer.applyDelta(delta, sessionID)
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

func resolveEntityID(entityType EntityType, args map[string]any, expr string) string {
	name := resolveEntityName(args, expr)
	if name == "" {
		return ""
	}
	return entityID(entityType, name)
}

func resolveEntityName(args map[string]any, expr string) string {
	if !strings.HasPrefix(expr, "args.") {
		return ""
	}
	key := strings.TrimPrefix(expr, "args.")
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}
