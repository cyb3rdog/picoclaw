package swl

import "sync"

// Global registry maps resolved dbPath → *managerEntry so multiple
// AgentInstances sharing the same workspace share one Manager and one
// SQLite write connection.
var (
	regMu    sync.Mutex
	registry = map[string]*managerEntry{}
)

type managerEntry struct {
	mgr  *Manager
	refs int
}

// AcquireManager returns the existing Manager for workspace (if any) or
// creates a new one, incrementing the reference count either way.
// Callers must call ReleaseManager when done (typically in Close()).
func AcquireManager(workspace string, cfg *Config) (*Manager, error) {
	dbPath := resolveDBPath(workspace, cfg)

	regMu.Lock()
	defer regMu.Unlock()

	if e, ok := registry[dbPath]; ok {
		e.refs++
		return e.mgr, nil
	}

	mgr, err := NewManager(workspace, cfg)
	if err != nil {
		return nil, err
	}
	registry[dbPath] = &managerEntry{mgr: mgr, refs: 1}
	return mgr, nil
}

// ReleaseManager decrements the reference count for the Manager associated
// with workspace. When the count reaches zero, Manager.Close() is called and
// the entry is removed from the registry.
func ReleaseManager(workspace string, cfg *Config) {
	dbPath := resolveDBPath(workspace, cfg)

	regMu.Lock()
	defer regMu.Unlock()

	e, ok := registry[dbPath]
	if !ok {
		return
	}
	e.refs--
	if e.refs <= 0 {
		e.mgr.Close() //nolint:errcheck
		delete(registry, dbPath)
	}
}
