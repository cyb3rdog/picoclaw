package swl

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Manager is the top-level SWL object. One instance per workspace.
// All exported methods are safe for concurrent use.
type Manager struct {
	cfg       *Config
	workspace string // absolute workspace root
	dbPath    string
	db        *sql.DB
	mu        sync.Mutex // serializes all writes

	writer *entityWriter

	// rules drives label derivation from configuration (Phase B).
	// Initialized in NewManager from swl.rules.yaml; nil until then (falls back to package-level DeriveLabels).
	rules *RulesEngine

	// ignoreMatcher for .swlignore file
	ignore *ignoreMatcher

	// sessionsMu guards both activeSessions and syncedSessions under a single
	// lock so that EnsureSession can atomically check-then-create without a
	// separate read-lock → write-lock promotion step that could race.
	sessionsMu     sync.Mutex
	activeSessions map[string]string // picoclaw sessionKey → SWL session UUID
	syncedSessions map[string]bool

	// decayHandlers allow extensible per-type decay logic.
	decayMu       sync.RWMutex
	decayHandlers map[EntityType]DecayHandlerFunc

	// pendingHooks tracks async PostHook goroutines for graceful drain.
	wg sync.WaitGroup

	// compiledSymPatterns holds the compiled symbol extraction regexes.
	// Initialized once in NewManager from cfg.ExtractSymbolPatterns (or defaults).
	compiledSymPatterns []*regexp.Regexp

	// rulesLoadErr captures any error from loading workspace rules/query config.
	// Non-empty when swl.rules.yaml or swl.query.yaml failed to load/parse.
	rulesLoadErr string

	// inferenceLog is a fixed-size ring buffer of recent extraction events.
	// Useful for diagnosing why entities were or were not extracted.
	infLogMu  sync.Mutex
	infLog    [inferenceLogCap]inferenceEvent
	infLogIdx int
}

const inferenceLogCap = 64

type inferenceEvent struct {
	ts   time.Time
	tool string
	note string
}

// DecayHandlerFunc checks whether an entity is still valid. It should update
// fact_status via Manager.SetFactStatus if the entity has changed.
type DecayHandlerFunc func(m *Manager, entityID, name string) error

// NewManager creates a Manager backed by a SQLite database at dbPath.
// If dbPath is empty, it defaults to {workspace}/.swl/swl.db.
func NewManager(workspace string, cfg *Config) (*Manager, error) {
	dbPath := resolveDBPath(workspace, cfg)

	// Scaffold workspace config files on first init (before DB is created).
	_, statErr := os.Stat(dbPath)
	if os.IsNotExist(statErr) {
		scaffoldConfigFiles(workspace)
	}

	db, err := openDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("swl: open database: %w", err)
	}

	m := &Manager{
		cfg:            cfg,
		workspace:      workspace,
		dbPath:         dbPath,
		db:             db,
		activeSessions: make(map[string]string),
		syncedSessions: make(map[string]bool),
		decayHandlers:  make(map[EntityType]DecayHandlerFunc),
	}
	m.writer = &entityWriter{db: db, mu: &m.mu}

	// Load .swlignore file
	_ = m.loadSwlignore() // non-fatal if missing

	// Initialize rules engine from swl.rules.yaml (Phase B).
	// On failure, log a warning and record the error; fall back to nil
	// (package-level DeriveLabels used).
	if rules, rulesErr := LoadRules(workspace, ""); rulesErr != nil {
		fmt.Fprintf(os.Stderr, "[SWL] warning: failed to load swl.rules.yaml: %v\n", rulesErr)
		m.rulesLoadErr = "workspace rules: " + rulesErr.Error()
	} else {
		m.rules = rules

		// Also load query intents from swl.query.yaml (Phase B — query externalization).
		if qcfg, qcfgErr := LoadQueryConfig(workspace, ""); qcfgErr != nil {
			fmt.Fprintf(os.Stderr, "[SWL] warning: failed to load swl.query.yaml: %v\n", qcfgErr)
			m.rulesLoadErr += "; query config: " + qcfgErr.Error()
		} else {
			m.rules.QueryIntents = CompileQueryConfig(qcfg)
			m.rules.SQLTemplates = qcfg.SQLTmpls
		}
	}

	m.RegisterDecayHandler(KnownTypeFile, decayFile)
	m.RegisterDecayHandler(KnownTypeURL, decayURL)

	m.compiledSymPatterns = compileSymPatterns(cfg)

	return m, nil
}

// compileSymPatterns returns the compiled symbol extraction patterns.
// Uses cfg.ExtractSymbolPatterns if set and valid; falls back to package defaults.
// Invalid patterns in the custom list are silently skipped.
func compileSymPatterns(cfg *Config) []*regexp.Regexp {
	if cfg == nil || len(cfg.ExtractSymbolPatterns) == 0 {
		return symPatterns // package-level defaults
	}
	compiled := make([]*regexp.Regexp, 0, len(cfg.ExtractSymbolPatterns))
	for _, pat := range cfg.ExtractSymbolPatterns {
		if re, err := regexp.Compile(pat); err == nil {
			compiled = append(compiled, re)
		}
	}
	if len(compiled) == 0 {
		return symPatterns // all patterns were invalid; fall back to defaults
	}
	return compiled
}

// DBPath returns the filesystem path to the SQLite database.
func (m *Manager) DBPath() string { return m.dbPath }

// Workspace returns the absolute workspace root.
func (m *Manager) Workspace() string { return m.workspace }

// Config returns the Manager's configuration (never nil).
func (m *Manager) Config() *Config {
	if m.cfg == nil {
		return &Config{}
	}
	return m.cfg
}

// ReloadConfig re-reads swl.rules.yaml and swl.query.yaml from disk without
// restarting the agent. The new rules take effect immediately for all subsequent
// queries and extractions. Returns a status string suitable for LLM consumption.
func (m *Manager) ReloadConfig() string {
	rules, rulesErr := LoadRules(m.workspace, "")
	if rulesErr != nil {
		m.rulesLoadErr = "workspace rules: " + rulesErr.Error()
		return "[SWL] ⚠ Rules reload failed: " + rulesErr.Error() +
			"\n  Fix " + filepath.Join(m.workspace, ".swl", "swl.rules.yaml") + " and try again."
	}

	qcfg, qcfgErr := LoadQueryConfig(m.workspace, "")
	if qcfgErr != nil {
		m.rulesLoadErr = "query config: " + qcfgErr.Error()
		return "[SWL] ⚠ Query config reload failed: " + qcfgErr.Error() +
			"\n  Fix " + filepath.Join(m.workspace, ".swl", "swl.query.yaml") + " and try again."
	}

	rules.QueryIntents = CompileQueryConfig(qcfg)
	rules.SQLTemplates = qcfg.SQLTmpls
	m.mu.Lock()
	m.rules = rules
	m.rulesLoadErr = ""
	m.mu.Unlock()
	return "[SWL] Config reloaded. New rules and query intents are active."
}

// effectiveNoiseSymbols returns the noise symbol filter for extraction.
// When YAML rules define noise symbols, they replace the hardcoded defaults.
func (m *Manager) effectiveNoiseSymbols() map[string]bool {
	if m.rules != nil && len(m.rules.NoiseSymbols) > 0 {
		return m.rules.NoiseSymbols
	}
	return nil // callers fall back to package-level noiseSymbols
}

// effectiveSkipHosts returns the list of URL host fragments to suppress.
func (m *Manager) effectiveSkipHosts() []string {
	if m.rules != nil {
		return m.rules.SkipHosts
	}
	return nil
}

// RegisterDecayHandler registers a per-type decay handler.
// Custom handlers take precedence over built-ins if registered after NewManager.
func (m *Manager) RegisterDecayHandler(entityType EntityType, fn DecayHandlerFunc) {
	m.decayMu.Lock()
	m.decayHandlers[entityType] = fn
	m.decayMu.Unlock()
}

// SetFactStatus is the ONLY permitted path to change an entity's fact_status.
func (m *Manager) SetFactStatus(entityID string, status FactStatus) error {
	return m.writer.setFactStatus(entityID, status)
}

// UpsertEntity writes a single entity, enforcing all invariants.
func (m *Manager) UpsertEntity(e EntityTuple) error {
	return m.writer.upsertEntity(e)
}

// UpsertEdge writes a single edge.
func (m *Manager) UpsertEdge(e EdgeTuple) error {
	return m.writer.upsertEdge(e)
}

// ApplyDelta writes a GraphDelta atomically in a single transaction.
func (m *Manager) ApplyDelta(delta *GraphDelta, sessionID string) error {
	return m.writer.applyDelta(delta, sessionID)
}

// Close drains all pending async hooks, ends all active sessions,
// optionally VACUUMs when the DB is large, then closes the connection.
func (m *Manager) Close() error {
	m.wg.Wait()
	m.endAllSessions()
	m.maybeVacuum()
	return m.db.Close()
}

func (m *Manager) maybeVacuum() {
	info, err := os.Stat(m.dbPath)
	if err == nil && info.Size() > 50*1024*1024 {
		m.mu.Lock()
		m.db.Exec("VACUUM") //nolint:errcheck
		m.mu.Unlock()
	}
}

// logInferenceEvent appends a structured event to the in-memory ring buffer.
func (m *Manager) logInferenceEvent(tool, note string) {
	m.infLogMu.Lock()
	m.infLog[m.infLogIdx%inferenceLogCap] = inferenceEvent{ts: time.Now(), tool: tool, note: note}
	m.infLogIdx++
	m.infLogMu.Unlock()
}

// DebugInferenceLog returns the last N inference events as a formatted string.
func (m *Manager) DebugInferenceLog() string {
	m.infLogMu.Lock()
	total := m.infLogIdx
	log := m.infLog
	m.infLogMu.Unlock()

	start := 0
	if total > inferenceLogCap {
		start = total - inferenceLogCap
	}
	if total == 0 {
		return "[SWL] No inference events recorded yet."
	}

	lines := make([]string, 0, total-start)
	for i := start; i < total; i++ {
		ev := log[i%inferenceLogCap]
		lines = append(lines, fmt.Sprintf("  %s  [%s] %s", ev.ts.Format("15:04:05.000"), ev.tool, ev.note))
	}
	return fmt.Sprintf("[SWL] Last %d inference events:\n", len(lines)) + strings.Join(lines, "\n")
}

const scaffoldHeader = `# SWL Extraction Rules — Workspace Override
# This file was scaffolded automatically. Modify to customize extraction for this workspace.
# Reference: docs/swl/SWL-DESIGN.md
# After editing, reload without restarting: query_swl {"reload_config":true}

`

// scaffoldConfigFiles writes default swl.rules.yaml and swl.query.yaml into
// {workspace}/.swl/ on first init. Files are never overwritten if they already exist.
func scaffoldConfigFiles(workspace string) {
	swlDir := filepath.Join(workspace, ".swl")
	if mkErr := os.MkdirAll(swlDir, 0o755); mkErr != nil {
		return // non-fatal: DB creation will also attempt this
	}

	rulesData, err := defaultRulesFS.ReadFile("swl.rules.default.yaml")
	if err == nil {
		writeScaffold(filepath.Join(swlDir, "swl.rules.yaml"), rulesData)
	}

	queryData, err := defaultQueryFS.ReadFile("swl.query.default.yaml")
	if err == nil {
		writeScaffold(filepath.Join(swlDir, "swl.query.yaml"), queryData)
	}

	// Scaffold .swlignore if absent, listing common generated/noise paths.
	writeSwlignoreScaffold(filepath.Join(workspace, ".swlignore"))
}

const swlignoreScaffold = `# .swlignore — paths and patterns excluded from SWL indexing.
# Lines starting with # are comments. Patterns use gitignore syntax.
# This file was scaffolded automatically. Edit to add workspace-specific exclusions.

# Generated and vendored
go.sum
go.work.sum
pnpm-lock.yaml
package-lock.json
yarn.lock
*.pb.go
*_generated.go
*.gen.go
dist/
build/
target/
vendor/
`

func writeSwlignoreScaffold(path string) {
	if _, err := os.Stat(path); err == nil {
		return // already exists
	}
	_ = os.WriteFile(path, []byte(swlignoreScaffold), 0o644)
}

// writeScaffold writes header + content to path, but only if path does not yet exist.
func writeScaffold(path string, content []byte) {
	if _, err := os.Stat(path); err == nil {
		return // already exists — do not overwrite
	}
	data := append([]byte(scaffoldHeader), content...)
	_ = os.WriteFile(path, data, 0o644) // non-fatal
}

func resolveDBPath(workspace string, cfg *Config) string {
	if cfg != nil && cfg.DBPath != "" {
		return cfg.DBPath
	}
	return filepath.Join(workspace, ".swl", "swl.db")
}

// resolveSubjectEntity looks up an existing graph entity matching subject.
// Resolution order:
//  1. Exact entity ID match
//  2. File by exact normalized workspace-relative path
//  3. File by fuzzy name match (LIKE — picks highest knowledge_depth)
//  4. Symbol or Directory by exact name (case-insensitive)
//
// Returns ("", "") when no match is found — the caller is responsible for
// error handling. No fallback entity is created.
func (m *Manager) resolveSubjectEntity(subject string) (id string, entityType EntityType) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", ""
	}

	// 1. Exact entity ID match
	var existingType string
	if err := m.db.QueryRow(
		"SELECT type FROM entities WHERE id = ? AND fact_status != 'deleted' LIMIT 1",
		subject,
	).Scan(&existingType); err == nil {
		return subject, existingType
	}

	// 2. File by exact normalized workspace-relative path
	normalized := m.normalizePath(subject)
	fileID := entityID(KnownTypeFile, normalized)
	var foundID string
	if err := m.db.QueryRow(
		"SELECT id FROM entities WHERE id = ? AND fact_status != 'deleted'",
		fileID,
	).Scan(&foundID); err == nil {
		return foundID, KnownTypeFile
	}

	// 3. File by fuzzy name match
	if err := m.db.QueryRow(
		`SELECT id FROM entities WHERE type = ? AND LOWER(name) LIKE LOWER(?) AND fact_status != 'deleted'
		 ORDER BY knowledge_depth DESC LIMIT 1`,
		KnownTypeFile, "%"+subject+"%",
	).Scan(&foundID); err == nil {
		return foundID, KnownTypeFile
	}

	// 4. Symbol or Directory by exact name
	for _, t := range []EntityType{KnownTypeSymbol, KnownTypeDirectory} {
		if err := m.db.QueryRow(
			"SELECT id FROM entities WHERE type = ? AND LOWER(name) = LOWER(?) AND fact_status != 'deleted' LIMIT 1",
			t, subject,
		).Scan(&foundID); err == nil {
			return foundID, t
		}
	}

	return "", ""
}
