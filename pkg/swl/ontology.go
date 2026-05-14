package swl

import (
	"database/sql"
	"strings"
)

// DeriveAreaRelations derives semantic relationships between SemanticArea entities
// from the existing entity graph. Called at the end of ScanWorkspace.
//
// Currently derives:
//   - depends_on: if files in area A import packages from area B (≥2 cross-area imports)
func (m *Manager) DeriveAreaRelations() {
	// Find all SemanticAreas and their parent directory paths
	areas, err := m.loadAreaPaths()
	if err != nil || len(areas) < 2 {
		return
	}

	// For each area pair, count cross-area import edges:
	// File in area A imports Dependency whose name starts with area B's path prefix
	const minCrossImports = 2
	for areaID, areaPath := range areas {
		for otherID, otherPath := range areas {
			if areaID == otherID || otherPath == "" {
				continue
			}
			count := m.countCrossImports(areaPath, otherPath)
			if count >= minCrossImports {
				_ = m.writer.upsertEdge(EdgeTuple{
					FromID: areaID,
					Rel:    KnownRelDependsOn,
					ToID:   otherID,
				})
			}
		}
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

// countCrossImports counts import edges from files under areaPath to dependencies
// whose name starts with a prefix derivable from otherAreaPath.
func (m *Manager) countCrossImports(areaPath, otherAreaPath string) int {
	// Files in areaPath: entities of type File with name starting with areaPath
	// Their imports: edges with rel=imports pointing to Dependency entities
	// whose name contains otherAreaPath as a substring
	if areaPath == "" || otherAreaPath == "" {
		return 0
	}
	// Normalize: treat otherAreaPath as a path fragment to search for
	searchFrag := strings.Trim(otherAreaPath, "/")
	if searchFrag == "" {
		return 0
	}

	var count int
	err := m.db.QueryRow(`
        SELECT COUNT(DISTINCT e.to_id)
        FROM edges e
        JOIN entities f ON f.id = e.from_id AND f.type = ? AND f.name LIKE ? AND f.fact_status != 'deleted'
        JOIN entities d ON d.id = e.to_id   AND d.type = ? AND d.name LIKE ? AND d.fact_status != 'deleted'
        WHERE e.rel = ?`,
		KnownTypeFile, areaPath+"%",
		KnownTypeDependency, "%"+searchFrag+"%",
		KnownRelImports,
	).Scan(&count)
	if err != nil && err != sql.ErrNoRows {
		return 0
	}
	return count
}
