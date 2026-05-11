package swl

import (
	"context"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
)

// MaybeDecay runs decay checks with 5% probability — called from PostHook.
func (m *Manager) MaybeDecay() { m.maybeDecay() }

// maybeDecay runs decay checks with 5% probability per PostHook call.
func (m *Manager) maybeDecay() {
	if rand.Float64() > 0.05 {
		return
	}
	m.DecayCheck("", 2)
}

// DecayCheck verifies the factual status of up to limit entities.
// If entityID is non-empty, only that entity is checked.
func (m *Manager) DecayCheck(entityID string, limit int) {
	if limit <= 0 {
		limit = 2
	}

	var rows []struct{ id, entityType, name string }

	if entityID != "" {
		var t, n string
		err := m.db.QueryRow(
			"SELECT type, name FROM entities WHERE id = ? AND fact_status IN ('unknown','verified')",
			entityID,
		).Scan(&t, &n)
		if err == nil {
			rows = append(rows, struct{ id, entityType, name string }{entityID, t, n})
		}
	} else {
		qrows, err := m.db.Query(`
			SELECT id, type, name FROM entities
			WHERE fact_status IN ('unknown','verified')
			  AND (last_checked IS NULL OR last_checked < ?)
			ORDER BY last_checked ASC LIMIT ?`,
			timeAgo(24*time.Hour), limit,
		)
		if err != nil {
			return
		}
		defer qrows.Close()
		for qrows.Next() {
			var r struct{ id, entityType, name string }
			if qrows.Scan(&r.id, &r.entityType, &r.name) == nil {
				rows = append(rows, r)
			}
		}
	}

	for _, r := range rows {
		m.decayMu.RLock()
		handler, ok := m.decayHandlers[r.entityType]
		m.decayMu.RUnlock()

		if ok {
			_ = handler(m, r.id, r.name)
		}
		m.mu.Lock()
		m.db.Exec( //nolint:errcheck
			"UPDATE entities SET last_checked = ? WHERE id = ?",
			nowSQLite(), r.id,
		)
		m.mu.Unlock()
	}
}

// maybePrune deletes old events when the table exceeds 10k rows.
func (m *Manager) maybePrune() {
	var count int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count); err != nil || count <= 10000 {
		return
	}
	cutoff := timeAgo(30 * 24 * time.Hour)
	m.mu.Lock()
	m.db.Exec("DELETE FROM events WHERE ts < ?", cutoff) //nolint:errcheck
	m.mu.Unlock()
}

// --- built-in decay handlers ---

func decayFile(m *Manager, entityID, name string) error {
	// Resolve workspace-relative paths before os.Stat.
	// The name field may store relative paths (e.g., "src/foo.go")
	// but os.Stat requires absolute paths unless CWD is correct.
	absPath := name
	if !filepath.IsAbs(name) && m.workspace != "" {
		absPath = filepath.Join(m.workspace, name)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return m.SetFactStatus(entityID, FactDeleted)
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil
	}

	var dbMtime string
	_ = m.db.QueryRow("SELECT modified_at FROM entities WHERE id = ?", entityID).Scan(&dbMtime)
	t := parseRFC3339(dbMtime)
	if !t.IsZero() && info.ModTime().After(t) {
		return m.SetFactStatus(entityID, FactStale)
	}
	return nil
}

func decayURL(m *Manager, entityID, name string) error {
	// Enforce minimum 24h recheck interval — checked in DecayCheck via last_checked query.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, name, nil)
	if err != nil {
		return nil // not a valid URL, ignore
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return m.SetFactStatus(entityID, FactStale)
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		return m.SetFactStatus(entityID, FactStale)
	}
	return m.SetFactStatus(entityID, FactVerified)
}

// --- time helpers ---

func timeAgo(d time.Duration) string {
	return time.Now().UTC().Add(-d).Format(time.RFC3339Nano)
}

func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	log.Warn().Str("value", s).Msg("swl: malformed modified_at timestamp — decay check skipped")
	return time.Time{}
}
