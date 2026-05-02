package swl

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

	// activeSessions maps a picoclaw session key to a SWL session UUID.
	sessionsMu     sync.RWMutex
	activeSessions map[string]string // picoclaw sessionKey → SWL session UUID

	// syncedSessions tracks which session UUIDs have already run sessionSync.
	syncedMu      sync.Mutex
	syncedSessions map[string]bool

	// decayHandlers allow extensible per-type decay logic.
	decayMu      sync.RWMutex
	decayHandlers map[EntityType]DecayHandlerFunc

	// pendingHooks tracks async PostHook goroutines for graceful drain.
	wg sync.WaitGroup
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

	m.RegisterDecayHandler(KnownTypeFile, decayFile)
	m.RegisterDecayHandler(KnownTypeURL, decayURL)

	return m, nil
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

func resolveDBPath(workspace string, cfg *Config) string {
	if cfg != nil && cfg.DBPath != "" {
		return cfg.DBPath
	}
	return filepath.Join(workspace, ".swl", "swl.db")
}
