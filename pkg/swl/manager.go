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
	// Silently falls back to nil (package-level DeriveLabels used) if YAML load fails.
	if rules, err := LoadRules(workspace, ""); err == nil {
		m.rules = rules

		// Also load query intents from swl.query.yaml (Phase B — query externalization).
		if qcfg, err := LoadQueryConfig(workspace, ""); err == nil {
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

func resolveDBPath(workspace string, cfg *Config) string {
	if cfg != nil && cfg.DBPath != "" {
		return cfg.DBPath
	}
	return filepath.Join(workspace, ".swl", "swl.db")
}
