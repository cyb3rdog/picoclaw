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

// ReleaseManager decrements the reference count for mgr.
// When the count reaches zero, Manager.Close() is called and the entry is
// removed from the registry.  Using the Manager's own DBPath() as the key
// avoids re-resolving from config, which could diverge if the config pointer
// is reused across different Manager lifetimes.
func ReleaseManager(mgr *Manager) {
	if mgr == nil {
		return
	}
	dbPath := mgr.DBPath()

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
