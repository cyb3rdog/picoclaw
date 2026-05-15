package swl

// DeriveAreaRelations derives semantic relationships between SemanticArea entities
// from the existing entity graph. Called at the end of ScanWorkspace.
//
// Derives depends_on: if files in area A import dependencies that fall under area B
// (≥2 cross-area import edges), upsert a depends_on edge from area A to area B.
func (m *Manager) DeriveAreaRelations() {
	areas, err := m.loadAreaPaths()
	if err != nil || len(areas) < 2 {
		return
	}

	// Single aggregated query: find all (areaID, otherAreaID) pairs where files under
	// one area import dependencies that match another area's path prefix.
	// Uses a single join rather than O(A²) individual queries.
	rows, err := m.db.Query(`
		SELECT a1.id, a2.id, COUNT(DISTINCT e.to_id) AS imports
		FROM entities a1
		JOIN entities a2 ON a1.id != a2.id
		JOIN entities f  ON f.type = ? AND f.name LIKE (a1.name || '%') AND f.fact_status != 'deleted'
		JOIN edges e      ON e.from_id = f.id AND e.rel = ?
		JOIN entities d  ON d.id = e.to_id AND d.type = ? AND d.fact_status != 'deleted'
		                 AND d.name LIKE ('%' || TRIM(a2.name, '/') || '%')
		WHERE a1.type = ? AND a1.fact_status != 'deleted'
		  AND a2.type = ? AND a2.fact_status != 'deleted'
		GROUP BY a1.id, a2.id
		HAVING imports >= 2`,
		KnownTypeFile, KnownRelImports, KnownTypeDependency,
		KnownTypeSemanticArea, KnownTypeSemanticArea,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	type pair struct{ from, to string }
	var pairs []pair
	for rows.Next() {
		var from, to string
		var count int
		if rows.Scan(&from, &to, &count) == nil {
			pairs = append(pairs, pair{from, to})
		}
	}
	_ = rows.Err()

	for _, p := range pairs {
		_ = m.writer.upsertEdge(EdgeTuple{
			FromID: p.from,
			Rel:    KnownRelDependsOn,
			ToID:   p.to,
		})
	}
}

// loadAreaPaths returns a map of SemanticArea entity ID → directory path prefix.
func (m *Manager) loadAreaPaths() (map[string]string, error) {
	rows, err := m.db.Query(
		`SELECT id, name FROM entities WHERE type = ? AND fact_status != 'deleted'`,
		KnownTypeSemanticArea,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	areas := make(map[string]string)
	for rows.Next() {
		var id, name string
		if rows.Scan(&id, &name) == nil {
			areas[id] = name
		}
	}
	return areas, rows.Err()
}
