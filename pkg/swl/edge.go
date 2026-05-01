package swl

const upsertEdgeTxSQL = `
INSERT INTO edges (from_id, rel, to_id, source_session, confirmed_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(from_id, rel, to_id) DO UPDATE SET
    confirmed_at   = excluded.confirmed_at,
    source_session = excluded.source_session
`

// upsertEdge inserts or confirms an edge between two entities.
func (w *entityWriter) upsertEdge(e EdgeTuple) error {
	if e.FromID == "" || e.ToID == "" || e.Rel == "" {
		return nil
	}
	now := nowSQLite()
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.db.Exec(upsertEdgeTxSQL, e.FromID, e.Rel, e.ToID, e.SessionID, now)
	return err
}
