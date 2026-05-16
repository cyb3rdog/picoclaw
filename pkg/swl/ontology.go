package swl

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DeriveSymbolUsage derives File --uses--> Symbol edges from the existing import
// graph without any new extraction. The logic: if File A imports a Dependency whose
// import path ends with the directory of File B, and File B defines Symbol S, then
// File A likely uses Symbol S. Capped at 500 pairs per call.
//
// Precision filters applied to reduce noise:
//   - Only exported symbols (uppercase first letter) — unexported symbols cannot
//     cross package boundaries, so import-graph matching cannot apply to them.
//   - Only package directories with ≥2 path segments (e.g. "pkg/auth" not "auth")
//     — single-segment names are too ambiguous to match reliably.
//
// These edges are inferred (no session ID). When the extractor runs on the importing
// file and finds actual symbol occurrences, it writes a higher-confidence observed edge
// via the same (from_id, rel, to_id) primary key, which the upsert promotes.
func (m *Manager) DeriveSymbolUsage() {
	// 1. Load all import edges: file_id → []dependency_name
	impRows, err := m.db.Query(`
		SELECT f.id, d.name
		FROM edges e
		JOIN entities f ON f.id = e.from_id AND f.type = 'File' AND f.fact_status != 'deleted'
		JOIN entities d ON d.id = e.to_id AND d.type = 'Dependency' AND d.fact_status != 'deleted'
		WHERE e.rel = ?`, KnownRelImports)
	if err != nil {
		return
	}
	defer impRows.Close()

	// fileImports: file_id → set of dependency names
	fileImports := map[string][]string{}
	for impRows.Next() {
		var fid, dep string
		if impRows.Scan(&fid, &dep) == nil {
			fileImports[fid] = append(fileImports[fid], dep)
		}
	}
	_ = impRows.Err()
	impRows.Close()

	if len(fileImports) == 0 {
		return
	}

	// 2. Load all defines edges: symbol_id → (file_path, symbol_name) for exported symbols only.
	// Unexported symbols cannot cross package boundaries, so import matching cannot apply.
	defRows, err := m.db.Query(`
		SELECT s.id, s.name, f.name
		FROM edges e
		JOIN entities f ON f.id = e.from_id AND f.type = 'File' AND f.fact_status != 'deleted'
		JOIN entities s ON s.id = e.to_id AND s.type = 'Symbol' AND s.fact_status != 'deleted'
		WHERE e.rel = ?`, KnownRelDefines)
	if err != nil {
		return
	}
	defer defRows.Close()

	type symEntry struct {
		id     string
		pkgDir string
	}
	var syms []symEntry
	for defRows.Next() {
		var symID, symName, filePath string
		if defRows.Scan(&symID, &symName, &filePath) != nil || symName == "" {
			continue
		}
		// Skip unexported symbols — they cannot be used across packages.
		if symName[0] < 'A' || symName[0] > 'Z' {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(filePath))
		dir = strings.TrimPrefix(dir, "./")
		// Skip single-segment package dirs (too ambiguous for import path matching).
		if !strings.Contains(dir, "/") {
			continue
		}
		syms = append(syms, symEntry{id: symID, pkgDir: dir})
	}
	_ = defRows.Err()
	defRows.Close()

	if len(syms) == 0 {
		return
	}

	// 3. Match: dep ends with symPackageDir → upsert File --uses--> Symbol
	count := 0
	for fileID, deps := range fileImports {
		for _, sym := range syms {
			if count >= 500 {
				break
			}
			for _, dep := range deps {
				dep = filepath.ToSlash(dep)
				// Match: import path "github.com/foo/pkg/auth" ends with "pkg/auth"
				if strings.HasSuffix(dep, "/"+sym.pkgDir) || dep == sym.pkgDir {
					_ = m.writer.upsertEdge(EdgeTuple{
						FromID: fileID,
						Rel:    KnownRelUses,
						ToID:   sym.id,
					})
					count++
					break
				}
			}
		}
		if count >= 500 {
			break
		}
	}
}

// DeriveAreaRelations derives semantic relationships between Directory entities
// that are classified as semantic areas (is_semantic_area=true) from the existing
// entity graph. Called at the end of ScanWorkspace.
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
	rows, err := m.db.Query(`
		SELECT a1.id, a2.id, COUNT(DISTINCT e.to_id) AS imports
		FROM entities a1
		JOIN entities a2 ON a1.id != a2.id
		JOIN entities f  ON f.type = 'File' AND f.name LIKE (a1.name || '%') AND f.fact_status != 'deleted'
		JOIN edges e      ON e.from_id = f.id AND e.rel = ?
		JOIN entities d  ON d.id = e.to_id AND d.type = ? AND d.fact_status != 'deleted'
		                 AND d.name LIKE ('%' || TRIM(a2.name, '/') || '%')
		WHERE a1.type = 'Directory' AND json_extract(a1.metadata,'$.is_semantic_area') = 1
		  AND a1.fact_status != 'deleted'
		  AND a2.type = 'Directory' AND json_extract(a2.metadata,'$.is_semantic_area') = 1
		  AND a2.fact_status != 'deleted'
		GROUP BY a1.id, a2.id
		HAVING imports >= 2`,
		KnownRelImports, KnownTypeDependency,
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

// DeriveSimilarSymbols derives similar_to edges between Symbol entities that share a
// structural relationship: defined in the same file AND sharing a name prefix ≥4 chars.
// This captures same-family symbols like HandleGet/HandlePost, AuthService/AuthHandler,
// ParseJSON/ParseYAML — without Levenshtein noise or cross-file false positives.
// Capped at 50 pairs per file, 300 total per call.
func (m *Manager) DeriveSimilarSymbols() {
	rows, err := m.db.Query(`
		SELECT f.id AS file_id, s.id, s.name
		FROM edges e
		JOIN entities f ON f.id = e.from_id AND f.type = 'File' AND f.fact_status != 'deleted'
		JOIN entities s ON s.id = e.to_id AND s.type = 'Symbol' AND s.fact_status != 'deleted'
		WHERE e.rel = ?
		ORDER BY f.id, s.name`, KnownRelDefines)
	if err != nil {
		return
	}
	defer rows.Close()

	type symInfo struct{ id, name string }
	fileSyms := map[string][]symInfo{}
	for rows.Next() {
		var fid, sid, sname string
		if rows.Scan(&fid, &sid, &sname) == nil && sname != "" {
			fileSyms[fid] = append(fileSyms[fid], symInfo{sid, sname})
		}
	}
	_ = rows.Err()
	rows.Close()

	total := 0
	for _, syms := range fileSyms {
		if len(syms) < 2 {
			continue
		}
		count := 0
		// syms is sorted alphabetically (ORDER BY s.name); adjacent pairs are most likely
		// to share a prefix. We still check all pairs but skip when per-file cap is reached.
		for i := 0; i < len(syms) && count < 50 && total < 300; i++ {
			for j := i + 1; j < len(syms) && count < 50 && total < 300; j++ {
				if pfx := symbolCommonPrefix(syms[i].name, syms[j].name); len(pfx) >= 4 {
					_ = m.writer.upsertEdge(EdgeTuple{
						FromID: syms[i].id,
						Rel:    KnownRelSimilarTo,
						ToID:   syms[j].id,
					})
					count++
					total++
				}
			}
		}
	}
}

// symbolCommonPrefix returns the longest common prefix of a and b.
func symbolCommonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}

// FindPath performs a breadth-first search from fromID to toID over the edges table,
// returning the path as a slice of entity IDs. Returns nil if no path is found within
// maxDepth hops. The frontier is capped at 500 nodes per level to bound memory.
// Edges are traversed in both directions (from→to and to→from) so the graph is treated
// as undirected for path discovery purposes.
func (m *Manager) FindPath(fromID, toID string, maxDepth int) []string {
	if fromID == toID {
		return []string{fromID}
	}
	if maxDepth <= 0 {
		maxDepth = 8
	}

	// parent[node] = the node we arrived from in the BFS tree; "" marks the root.
	parent := map[string]string{fromID: ""}
	frontier := []string{fromID}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		if len(frontier) > 500 {
			frontier = frontier[:500]
		}

		placeholders := strings.Repeat("?,", len(frontier))
		placeholders = placeholders[:len(placeholders)-1] // trim trailing comma
		args := make([]any, len(frontier)*2)
		for i, id := range frontier {
			args[i] = id
			args[len(frontier)+i] = id
		}

		query := fmt.Sprintf(
			`SELECT from_id, to_id FROM edges WHERE from_id IN (%s) OR to_id IN (%s)`,
			placeholders, placeholders,
		)
		rows, err := m.db.Query(query, args...)
		if err != nil {
			return nil
		}

		type edge struct{ from, to string }
		var found []edge
		for rows.Next() {
			var f, t string
			if rows.Scan(&f, &t) == nil {
				found = append(found, edge{f, t})
			}
		}
		_ = rows.Err()
		rows.Close()

		var next []string
		for _, e := range found {
			// Try both directions as neighbors.
			for _, neighbor := range [2]string{e.to, e.from} {
				if _, visited := parent[neighbor]; visited {
					continue
				}
				// Record the node we came from (approximation for bidirectional traversal).
				via := e.from
				if neighbor == e.from {
					via = e.to
				}
				parent[neighbor] = via
				if neighbor == toID {
					return reconstructBFSPath(parent, fromID, toID)
				}
				next = append(next, neighbor)
			}
		}
		frontier = next
	}
	return nil
}

// reconstructBFSPath traces parent pointers back from dst to the BFS root.
func reconstructBFSPath(parent map[string]string, fromID, dst string) []string {
	var path []string
	for node := dst; ; {
		path = append([]string{node}, path...)
		p := parent[node]
		if p == "" || node == fromID {
			break
		}
		node = p
	}
	return path
}

// loadAreaPaths returns a map of semantic area Directory entity ID → directory path prefix.
func (m *Manager) loadAreaPaths() (map[string]string, error) {
	rows, err := m.db.Query(
		`SELECT id, name FROM entities
		 WHERE type = 'Directory' AND json_extract(metadata,'$.is_semantic_area') = 1
		   AND fact_status != 'deleted'`,
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
