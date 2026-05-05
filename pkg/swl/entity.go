package swl

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// entityWriter serializes all writes through a single mutex to respect SQLite's
// single-writer constraint (the DB is opened with MaxOpenConns(1)).
type entityWriter struct {
	db *sql.DB
	mu *sync.Mutex
}

// cascadeRelIn is the SQL IN fragment built once from CascadeRels.
// Example: ('defines','has_task','has_section','mentions')
var cascadeRelIn string

func init() {
	parts := make([]string, len(CascadeRels))
	for i, r := range CascadeRels {
		parts[i] = "'" + string(r) + "'"
	}
	cascadeRelIn = "(" + strings.Join(parts, ",") + ")"
}

const upsertEntitySQL = `
INSERT INTO entities (
    id, type, name, metadata, confidence, knowledge_depth,
    extraction_method, fact_status, created_at, modified_at,
    accessed_at, access_count
) VALUES (?, ?, ?, ?, ?, ?, ?, 'unknown', ?, ?, ?, 1)
ON CONFLICT(id) DO UPDATE SET
    name = CASE WHEN excluded.name != '' THEN excluded.name ELSE name END,
    metadata = CASE WHEN excluded.metadata != '{}' AND excluded.metadata != '' THEN excluded.metadata ELSE metadata END,
    confidence = MAX(confidence, excluded.confidence),
    knowledge_depth = MAX(knowledge_depth, excluded.knowledge_depth),
    extraction_method = CASE
        WHEN extraction_method = 'observed'  THEN 'observed'
        WHEN excluded.extraction_method = 'observed'  THEN 'observed'
        WHEN extraction_method = 'stated'    THEN 'stated'
        WHEN excluded.extraction_method = 'stated'    THEN 'stated'
        WHEN extraction_method = 'extracted' THEN 'extracted'
        WHEN excluded.extraction_method = 'extracted' THEN 'extracted'
        ELSE 'inferred'
    END,
    modified_at  = excluded.modified_at,
    accessed_at  = excluded.accessed_at,
    access_count = access_count + 1
`

// upsertEntity inserts or updates an entity, enforcing all invariants:
//   - confidence: MAX(existing, new) — never decreases
//   - knowledge_depth: MAX(existing, new) — never decreases
//   - extraction_method: priority order (observed > stated > extracted > inferred)
//   - fact_status: NEVER touched here — only SetFactStatus() may change it
func (w *entityWriter) upsertEntity(e EntityTuple) error {
	if e.ID == "" {
		return fmt.Errorf("swl: upsert entity: ID is required")
	}
	if e.Type == "" {
		return fmt.Errorf("swl: upsert entity %q: type is required", e.ID)
	}
	if e.Confidence <= 0 {
		e.Confidence = confidenceForMethod(e.ExtractionMethod)
	}
	if e.ExtractionMethod == "" {
		e.ExtractionMethod = MethodObserved
	}

	meta, err := marshalMetadata(e.Metadata)
	if err != nil {
		return fmt.Errorf("swl: marshal metadata for %q: %w", e.ID, err)
	}

	now := nowSQLite()
	w.mu.Lock()
	defer w.mu.Unlock()

	_, err = w.db.Exec(upsertEntitySQL,
		e.ID, e.Type, e.Name, meta,
		e.Confidence, e.KnowledgeDepth,
		e.ExtractionMethod,
		now, now, now,
	)
	return err
}

// setFactStatus is the ONLY permitted path to change fact_status.
// Deleted status is terminal — once set it cannot be reverted.
// Cascade: setting FactDeleted also marks all derived entities stale.
func (w *entityWriter) setFactStatus(entityID string, status FactStatus) error {
	switch status {
	case FactUnknown, FactVerified, FactStale, FactDeleted:
	default:
		return fmt.Errorf("swl: invalid fact_status %q", status)
	}

	now := nowSQLite()
	w.mu.Lock()
	defer w.mu.Unlock()

	// Guard: deleted is terminal
	var current string
	if err := w.db.QueryRow(
		"SELECT fact_status FROM entities WHERE id = ?", entityID,
	).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return nil // entity doesn't exist; silently ignore
		}
		return err
	}
	if current == string(FactDeleted) {
		return nil // terminal — no change
	}

	if _, err := w.db.Exec(
		"UPDATE entities SET fact_status = ?, modified_at = ? WHERE id = ?",
		status, now, entityID,
	); err != nil {
		return err
	}

	if status == FactDeleted {
		w.invalidateChildrenLocked(entityID, now)
	}
	return nil
}

// invalidateChildrenLocked marks direct child entities derived from fileID as stale.
// Must be called with w.mu held.
// Limits cascade to direct outgoing edges only (not transitive via UNION).
// Skips already-stale entities to prevent re-cascading.
func (w *entityWriter) invalidateChildrenLocked(fileID, now string) {
	// cascadeRelIn is built from CascadeRels at package init — add new ownership
	// relations to types.go:CascadeRels, not here.
	invalidateSQL := `
		UPDATE entities SET fact_status = 'stale', modified_at = ?
		WHERE id IN (
			SELECT DISTINCT to_id FROM edges
			WHERE from_id = ? AND rel IN ` + cascadeRelIn + `
		) AND fact_status NOT IN ('deleted','stale')`
	w.db.Exec(invalidateSQL, now, fileID) //nolint:errcheck — best-effort cascade
}

// checkAndInvalidate compares the current content_hash of entityID with a
// hash of content. Returns true if changed (or entity is new).
// On change: resets knowledge_depth to 1 and cascades children to stale.
// Must be called with w.mu held.
func (w *entityWriter) checkAndInvalidateLocked(entityID, content string) bool {
	newHash := contentHash(content)
	var existingHash sql.NullString
	_ = w.db.QueryRow(
		"SELECT content_hash FROM entities WHERE id = ?", entityID,
	).Scan(&existingHash)

	if existingHash.Valid && existingHash.String == newHash {
		return false // unchanged
	}

	now := nowSQLite()
	w.db.Exec( //nolint:errcheck
		"UPDATE entities SET content_hash = ?, knowledge_depth = 1, modified_at = ? WHERE id = ?",
		newHash, now, entityID,
	)
	if existingHash.Valid && existingHash.String != "" {
		// File previously indexed and content changed — cascade
		w.invalidateChildrenLocked(entityID, now)
	}
	return true
}

// applyDelta writes all entities and edges in a single transaction.
func (w *entityWriter) applyDelta(delta *GraphDelta, sessionID string) error {
	if delta == nil || delta.IsEmpty() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	tx, err := w.db.Begin()
	if err != nil {
		return fmt.Errorf("swl: begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback() //nolint:errcheck
		}
	}()

	now := nowSQLite()

	for _, e := range delta.Entities {
		if e.ID == "" || e.Type == "" {
			continue
		}
		if e.Confidence <= 0 {
			e.Confidence = confidenceForMethod(e.ExtractionMethod)
		}
		if e.ExtractionMethod == "" {
			e.ExtractionMethod = MethodObserved
		}
		meta, merr := marshalMetadata(e.Metadata)
		if merr != nil {
			meta = "{}"
		}
		if _, err = tx.Exec(upsertEntitySQL,
			e.ID, e.Type, e.Name, meta,
			e.Confidence, e.KnowledgeDepth,
			e.ExtractionMethod,
			now, now, now,
		); err != nil {
			return fmt.Errorf("swl: upsert entity %q: %w", e.ID, err)
		}
	}

	for _, ed := range delta.Edges {
		if ed.FromID == "" || ed.ToID == "" || ed.Rel == "" {
			continue
		}
		sid := ed.SessionID
		if sid == "" {
			sid = sessionID
		}
		if _, err = tx.Exec(upsertEdgeTxSQL, ed.FromID, ed.Rel, ed.ToID, sid, now); err != nil {
			return fmt.Errorf("swl: upsert edge %s-[%s]->%s: %w", ed.FromID, ed.Rel, ed.ToID, err)
		}
	}

	return tx.Commit()
}

// bumpKnowledgeDepth increments knowledge_depth by 1, capped at maxDepth.
// Must be called with w.mu held.
func (w *entityWriter) bumpKnowledgeDepthLocked(entityID string, maxDepth int) {
	w.db.Exec( //nolint:errcheck
		`UPDATE entities SET knowledge_depth = MIN(knowledge_depth + 1, ?), modified_at = ?
		 WHERE id = ?`,
		maxDepth, nowSQLite(), entityID,
	)
}

// nullContentHash sets content_hash to NULL (used after append_file where
// the full content is unknown).
// Must be called with w.mu held.
func (w *entityWriter) nullContentHashLocked(entityID string) {
	w.db.Exec( //nolint:errcheck
		"UPDATE entities SET content_hash = NULL, modified_at = ? WHERE id = ?",
		nowSQLite(), entityID,
	)
}

func marshalMetadata(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}", err
	}
	return string(b), nil
}

func confidenceForMethod(m ExtractionMethod) float64 {
	switch m {
	case MethodObserved:
		return 1.0
	case MethodExtracted:
		return 0.9
	case MethodStated:
		return 0.85
	case MethodInferred:
		return 0.8
	default:
		return 1.0
	}
}
