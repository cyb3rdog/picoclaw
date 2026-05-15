package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sipeed/picoclaw/pkg/config"
)

func (h *Handler) registerSWLRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/swl/graph", h.handleSWLGraph)
	mux.HandleFunc("GET /api/swl/graph/neighborhood", h.handleSWLNeighborhood)
	mux.HandleFunc("GET /api/swl/stats", h.handleSWLStats)
	mux.HandleFunc("GET /api/swl/health", h.handleSWLHealth)
	mux.HandleFunc("GET /api/swl/sessions", h.handleSWLSessions)
	mux.HandleFunc("GET /api/swl/overview", h.handleSWLOverview)
	mux.HandleFunc("GET /api/swl/stream", h.handleSWLStream)
}

// swlDBPath resolves the SWL database path from config, caching the result.
func (h *Handler) swlDBPath() (string, error) {
	h.swlDBPathMu.RLock()
	cached := h.swlCachedDBPath
	h.swlDBPathMu.RUnlock()
	if cached != "" {
		return cached, nil
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	var resolved string
	if cfg.Tools.SWL != nil && cfg.Tools.SWL.DBPath != "" {
		resolved = cfg.Tools.SWL.DBPath
	} else {
		resolved = filepath.Join(cfg.WorkspacePath(), ".swl", "swl.db")
	}

	h.swlDBPathMu.Lock()
	h.swlCachedDBPath = resolved
	h.swlDBPathMu.Unlock()
	return resolved, nil
}

func openSWLReadOnly(dbPath string) (*sql.DB, error) {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("SWL database not found at %s", dbPath)
	}
	dsn := "file:" + dbPath + "?mode=ro&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	return db, nil
}

// --- Graph endpoint ---

type swlNode struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Name           string         `json:"name"`
	Confidence     float64        `json:"confidence"`
	FactStatus     string         `json:"factStatus"`
	KnowledgeDepth int            `json:"knowledgeDepth"`
	AccessCount    int            `json:"accessCount"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type swlLink struct {
	Source    string `json:"source"`
	Target    string `json:"target"`
	Rel       string `json:"rel"`
	SessionID string `json:"sessionId,omitempty"`
}

type swlGraphMeta struct {
	NodeCount  int    `json:"nodeCount"`
	LinkCount  int    `json:"linkCount"`
	TotalNodes int    `json:"totalNodes"`
	TotalEdges int    `json:"totalEdges"`
	BuildTime  string `json:"buildTime"`
	Mode       string `json:"mode"`
}

type swlGraphData struct {
	Nodes []swlNode    `json:"nodes"`
	Links []swlLink    `json:"links"`
	Meta  swlGraphMeta `json:"meta"`
}

func (h *Handler) handleSWLGraph(w http.ResponseWriter, r *http.Request) {
	dbPath, err := h.swlDBPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	db, err := openSWLReadOnly(dbPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer db.Close()

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		// legacy ?view= parameter maps to new mode names
		switch r.URL.Query().Get("view") {
		case "overview":
			mode = "overview"
		default:
			mode = "map"
		}
	}

	// Parameters vary by mode:
	//   map      — 20000 nodes, 40000 edges, 500 edges/hub  — general exploration
	//   overview — 10000 nodes, 20000 edges, 250 edges/hub  — structural snapshot
	//              Symbol/Section excluded (they dominate at 14k+ rows but add noise)
	//   session  — 5000 nodes, 10000 edges, 150 edges/hub  — scoped to recent sessions
	maxNodes := 20000
	maxEdges := 40000
	maxEdgesPerNode := 500
	var typeFilter string

	switch mode {
	case "overview":
		maxNodes = 10000
		maxEdges = 20000
		maxEdgesPerNode = 250
		typeFilter = `AND n1.type NOT IN ('Symbol','Section')
		          AND n2.type NOT IN ('Symbol','Section')`
	case "session":
		maxNodes = 5000
		maxEdges = 10000
		maxEdgesPerNode = 150
	}

	// For session mode: resolve the set of session IDs to scope by.
	var (
		sessionEdgeFilter string
		edgeArgs          []any
	)
	if mode == "session" {
		sessionIDs := swlRecentSessionIDs(r, db, 5)
		if len(sessionIDs) > 0 {
			placeholders := strings.Repeat("?,", len(sessionIDs))
			placeholders = placeholders[:len(placeholders)-1]
			sessionEdgeFilter = `AND (e.source_session IN (` + placeholders + `) OR n1.type = 'Session' OR n2.type = 'Session')`
			for _, id := range sessionIDs {
				edgeArgs = append(edgeArgs, id)
			}
		}
		// Fall back to map behavior if no sessions found.
	}

	// Phase 1: Select the highest-quality edges (both endpoints non-deleted).
	// We scan 3× the edge budget to compensate for the per-node degree cap.
	edgeQuery := `
		SELECT e.from_id, e.rel, e.to_id, COALESCE(e.source_session,'')
		FROM edges e
		JOIN entities n1 ON n1.id = e.from_id AND n1.fact_status != 'deleted'
		JOIN entities n2 ON n2.id = e.to_id   AND n2.fact_status != 'deleted'
		` + typeFilter + sessionEdgeFilter + `
		ORDER BY (n1.knowledge_depth + n2.knowledge_depth +
		          MIN(n1.access_count,50) + MIN(n2.access_count,50)) DESC
		LIMIT ?`
	edgeArgs = append(edgeArgs, maxEdges*3)
	edgeRows, err := db.QueryContext(r.Context(), edgeQuery, edgeArgs...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer edgeRows.Close()

	links := make([]swlLink, 0, maxEdges)
	neededIDs := map[string]bool{}
	nodeDegree := map[string]int{}
	for edgeRows.Next() {
		var l swlLink
		if err := edgeRows.Scan(&l.Source, &l.Rel, &l.Target, &l.SessionID); err != nil {
			continue
		}
		if nodeDegree[l.Source] >= maxEdgesPerNode || nodeDegree[l.Target] >= maxEdgesPerNode {
			continue
		}
		links = append(links, l)
		neededIDs[l.Source] = true
		neededIDs[l.Target] = true
		nodeDegree[l.Source]++
		nodeDegree[l.Target]++
		if len(links) >= maxEdges {
			break
		}
	}
	_ = edgeRows.Err()

	// Phase 2: Fetch entity details for all edge-endpoint IDs in batches of 400
	// (SQLite 999-parameter limit).
	nodes := make([]swlNode, 0, len(neededIDs))
	nodeIDs := map[string]bool{}

	needList := make([]string, 0, len(neededIDs))
	for id := range neededIDs {
		needList = append(needList, id)
	}

	const batchSize = 400
	scanNode := func(rows *sql.Rows) {
		for rows.Next() {
			var n swlNode
			var metaStr string
			if rows.Scan(&n.ID, &n.Type, &n.Name, &n.Confidence,
				&n.FactStatus, &n.KnowledgeDepth, &n.AccessCount, &metaStr) != nil {
				continue
			}
			if nodeIDs[n.ID] {
				continue
			}
			if metaStr != "" && metaStr != "{}" {
				_ = json.Unmarshal([]byte(metaStr), &n.Metadata)
			}
			n.Name = swlShortName(n.Name)
			nodes = append(nodes, n)
			nodeIDs[n.ID] = true
		}
	}

	for i := 0; i < len(needList); i += batchSize {
		end := i + batchSize
		if end > len(needList) {
			end = len(needList)
		}
		batch := needList[i:end]
		ph := strings.Repeat("?,", len(batch))
		ph = ph[:len(ph)-1]
		args := make([]any, len(batch))
		for j, id := range batch {
			args[j] = id
		}
		brows, berr := db.QueryContext(r.Context(),
			"SELECT id,type,name,confidence,fact_status,knowledge_depth,access_count,metadata"+
				" FROM entities WHERE id IN ("+ph+")", args...)
		if berr != nil {
			// Retry once on failure
			brows, berr = db.QueryContext(r.Context(),
				"SELECT id,type,name,confidence,fact_status,knowledge_depth,access_count,metadata"+
					" FROM entities WHERE id IN ("+ph+")", args...)
			if berr != nil {
				continue
			}
		}
		scanNode(brows)
		_ = brows.Err()
		brows.Close()
	}

	// Phase 3: Fill remaining node budget.
	// overview/session: exclude Symbol/Section (noise).
	// map: exclude only trivial leaves (depth≤1, access≤1).
	if len(nodes) < maxNodes {
		remaining := maxNodes - len(nodes)
		var fillFilter string
		switch mode {
		case "overview", "session":
			fillFilter = `AND type NOT IN ('Symbol','Section')`
		default:
			fillFilter = `AND NOT (type IN ('Symbol','Section') AND knowledge_depth <= 1 AND access_count <= 1)`
		}
		fillRows, ferr := db.QueryContext(r.Context(), `
			SELECT id,type,name,confidence,fact_status,knowledge_depth,access_count,metadata
			FROM entities
			WHERE fact_status != 'deleted'
			  `+fillFilter+`
			ORDER BY knowledge_depth DESC, access_count DESC, accessed_at DESC
			LIMIT ?
		`, remaining*3)
		if ferr == nil {
			scanNode(fillRows)
			_ = fillRows.Err()
			fillRows.Close() //nolint:sqlclosecheck // explicitly closed after use
		}
		if len(nodes) > maxNodes {
			nodes = nodes[:maxNodes]
		}
	}

	// Re-filter edges: drop any whose endpoints weren't included in the final node set.
	validLinks := links[:0]
	for _, l := range links {
		if nodeIDs[l.Source] && nodeIDs[l.Target] {
			validLinks = append(validLinks, l)
		}
	}

	// Collect DB-wide totals for the frontend scale indicator.
	var totalNodes, totalEdges int
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM entities WHERE fact_status != 'deleted'").
		Scan(&totalNodes)
		//nolint:errcheck
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM edges").
		Scan(&totalEdges)
		//nolint:errcheck

	data := swlGraphData{Nodes: nodes, Links: validLinks}
	data.Meta.NodeCount = len(nodes)
	data.Meta.LinkCount = len(validLinks)
	data.Meta.TotalNodes = totalNodes
	data.Meta.TotalEdges = totalEdges
	data.Meta.BuildTime = time.Now().UTC().Format(time.RFC3339)
	data.Meta.Mode = mode

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data) //nolint:errcheck
}

// handleSWLNeighborhood returns the 2-hop subgraph around a given node ID.
// It is the backend for the "focus" mode: click a node → see what it connects to.
func (h *Handler) handleSWLNeighborhood(w http.ResponseWriter, r *http.Request) {
	focusID := strings.TrimSpace(r.URL.Query().Get("id"))
	if focusID == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	dbPath, err := h.swlDBPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	db, err := openSWLReadOnly(dbPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer db.Close()

	const maxNodes = 120
	const maxEdges = 400
	const maxEdgesPerNode = 30

	// Depth-1 pass: all edges incident on focusID.
	hop1Rows, err := db.QueryContext(r.Context(), `
		SELECT e.from_id, e.rel, e.to_id, COALESCE(e.source_session,'')
		FROM edges e
		JOIN entities n1 ON n1.id = e.from_id AND n1.fact_status != 'deleted'
		JOIN entities n2 ON n2.id = e.to_id   AND n2.fact_status != 'deleted'
		WHERE e.from_id = ? OR e.to_id = ?
		ORDER BY (n1.knowledge_depth + n2.knowledge_depth +
		          MIN(n1.access_count,50) + MIN(n2.access_count,50)) DESC
		LIMIT ?`, focusID, focusID, maxEdges)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	links := make([]swlLink, 0, maxEdges)
	neededIDs := map[string]bool{focusID: true}
	nodeDegree := map[string]int{}
	hop1Neighbors := map[string]bool{}

	defer hop1Rows.Close()
	for hop1Rows.Next() {
		var l swlLink
		if hop1Rows.Scan(&l.Source, &l.Rel, &l.Target, &l.SessionID) != nil {
			continue
		}
		if nodeDegree[l.Source] >= maxEdgesPerNode || nodeDegree[l.Target] >= maxEdgesPerNode {
			continue
		}
		links = append(links, l)
		neededIDs[l.Source] = true
		neededIDs[l.Target] = true
		hop1Neighbors[l.Source] = true
		hop1Neighbors[l.Target] = true
		nodeDegree[l.Source]++
		nodeDegree[l.Target]++
	}
	_ = hop1Rows.Err()

	// Depth-2 pass: edges between any two hop-1 neighbors (cross-links only;
	// skip edges that expand to unknown nodes to keep the graph focused).
	if len(neededIDs) < maxNodes {
		neighborList := make([]string, 0, len(hop1Neighbors))
		for id := range hop1Neighbors {
			if id != focusID {
				neighborList = append(neighborList, id)
			}
		}
		for i := 0; i < len(neighborList) && len(links) < maxEdges; i += 200 {
			end := i + 200
			if end > len(neighborList) {
				end = len(neighborList)
			}
			batch := neighborList[i:end]
			ph := strings.Repeat("?,", len(batch))
			ph = ph[:len(ph)-1]
			// Build args twice (from_id IN (...) AND to_id IN (...))
			args := make([]any, 0, len(batch)*2+1)
			for _, id := range batch {
				args = append(args, id)
			}
			for _, id := range batch {
				args = append(args, id)
			}
			args = append(args, maxEdges-len(links))
			hop2Rows, err := db.QueryContext(r.Context(), `
				SELECT e.from_id, e.rel, e.to_id, COALESCE(e.source_session,'')
				FROM edges e
				JOIN entities n1 ON n1.id = e.from_id AND n1.fact_status != 'deleted'
				JOIN entities n2 ON n2.id = e.to_id   AND n2.fact_status != 'deleted'
				WHERE e.from_id IN (`+ph+`) AND e.to_id IN (`+ph+`)
				LIMIT ?`, args...)
			if err == nil {
				for hop2Rows.Next() {
					var l swlLink
					if hop2Rows.Scan(&l.Source, &l.Rel, &l.Target, &l.SessionID) != nil {
						continue
					}
					if nodeDegree[l.Source] >= maxEdgesPerNode || nodeDegree[l.Target] >= maxEdgesPerNode {
						continue
					}
					links = append(links, l)
					nodeDegree[l.Source]++
					nodeDegree[l.Target]++
				}
				_ = hop2Rows.Err()
				hop2Rows.Close() //nolint:sqlclosecheck // explicitly closed per batch iteration
			}
		}
	}

	// Fetch entity details.
	nodes := make([]swlNode, 0, len(neededIDs))
	nodeIDs := map[string]bool{}
	needList := make([]string, 0, len(neededIDs))
	for id := range neededIDs {
		needList = append(needList, id)
	}

	scanNode := func(rows *sql.Rows) {
		for rows.Next() {
			var n swlNode
			var metaStr string
			if rows.Scan(&n.ID, &n.Type, &n.Name, &n.Confidence,
				&n.FactStatus, &n.KnowledgeDepth, &n.AccessCount, &metaStr) != nil {
				continue
			}
			if nodeIDs[n.ID] {
				continue
			}
			if metaStr != "" && metaStr != "{}" {
				_ = json.Unmarshal([]byte(metaStr), &n.Metadata)
			}
			n.Name = swlShortName(n.Name)
			nodes = append(nodes, n)
			nodeIDs[n.ID] = true
		}
	}

	const batchSize = 400
	for i := 0; i < len(needList); i += batchSize {
		end := i + batchSize
		if end > len(needList) {
			end = len(needList)
		}
		batch := needList[i:end]
		ph := strings.Repeat("?,", len(batch))
		ph = ph[:len(ph)-1]
		args := make([]any, len(batch))
		for j, id := range batch {
			args[j] = id
		}
		brows, berr := db.QueryContext(r.Context(),
			"SELECT id,type,name,confidence,fact_status,knowledge_depth,access_count,metadata"+
				" FROM entities WHERE id IN ("+ph+")", args...)
		if berr != nil {
			continue
		}
		scanNode(brows)
		_ = brows.Err()
		brows.Close() //nolint:sqlclosecheck // explicitly closed per batch iteration
	}

	// Re-filter edges.
	validLinks := links[:0]
	for _, l := range links {
		if nodeIDs[l.Source] && nodeIDs[l.Target] {
			validLinks = append(validLinks, l)
		}
	}

	var totalNodes, totalEdges int
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM entities WHERE fact_status != 'deleted'").
		Scan(&totalNodes)
		//nolint:errcheck
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM edges").
		Scan(&totalEdges)
		//nolint:errcheck

	data := swlGraphData{Nodes: nodes, Links: validLinks}
	data.Meta.NodeCount = len(nodes)
	data.Meta.LinkCount = len(validLinks)
	data.Meta.TotalNodes = totalNodes
	data.Meta.TotalEdges = totalEdges
	data.Meta.BuildTime = time.Now().UTC().Format(time.RFC3339)
	data.Meta.Mode = "neighborhood"

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data) //nolint:errcheck
}

// swlRecentSessionIDs returns the IDs of the N most recently started sessions.
func swlRecentSessionIDs(r *http.Request, db *sql.DB, n int) []string {
	rows, err := db.QueryContext(r.Context(),
		"SELECT id FROM sessions ORDER BY started_at DESC LIMIT ?", n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Err()
	return ids
}

// --- Stats endpoint ---

type swlStatRow struct {
	Type     string `json:"type"`
	Total    int    `json:"total"`
	Verified int    `json:"verified"`
	Stale    int    `json:"stale"`
	Unknown  int    `json:"unknown"`
}

type swlStatsData struct {
	Rows      []swlStatRow `json:"rows"`
	EdgeCount int          `json:"edgeCount"`
	DBPath    string       `json:"dbPath"`
}

func (h *Handler) handleSWLStats(w http.ResponseWriter, r *http.Request) {
	dbPath, err := h.swlDBPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	db, err := openSWLReadOnly(dbPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer db.Close()

	rows, err := db.QueryContext(r.Context(), `
		SELECT type,
		       COUNT(*),
		       SUM(CASE WHEN fact_status='verified' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN fact_status='stale'    THEN 1 ELSE 0 END),
		       SUM(CASE WHEN fact_status='unknown'  THEN 1 ELSE 0 END)
		FROM entities GROUP BY type ORDER BY COUNT(*) DESC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	data := swlStatsData{DBPath: dbPath, Rows: make([]swlStatRow, 0, 16)}
	for rows.Next() {
		var sr swlStatRow
		if rows.Scan(&sr.Type, &sr.Total, &sr.Verified, &sr.Stale, &sr.Unknown) == nil {
			data.Rows = append(data.Rows, sr)
		}
	}
	_ = rows.Err()
	db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&data.EdgeCount) //nolint:errcheck

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data) //nolint:errcheck
}

// --- Health endpoint ---

type swlHealthData struct {
	Score         float64 `json:"score"`
	Level         string  `json:"level"`
	EntityCount   int     `json:"entityCount"`
	VerifiedPct   float64 `json:"verifiedPct"`
	StalePct      float64 `json:"stalePct"`
	EdgeCount     int     `json:"edgeCount"`
	IsolatedCount int     `json:"isolatedCount"`
	DBSizeBytes   int64   `json:"dbSizeBytes"`
	Message       string  `json:"message"`
}

// computeHealth runs all health queries against an already-open DB and returns the result.
func computeHealth(ctx context.Context, db *sql.DB, dbPath string) swlHealthData {
	var totalEntities, verified, stale, unknown int
	db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			SUM(CASE WHEN fact_status='verified' THEN 1 ELSE 0 END),
			SUM(CASE WHEN fact_status='stale'    THEN 1 ELSE 0 END),
			SUM(CASE WHEN fact_status='unknown'  THEN 1 ELSE 0 END)
		FROM entities WHERE fact_status != 'deleted'
	`).Scan(&totalEntities, &verified, &stale, &unknown) //nolint:errcheck

	var edgeCount, isolatedCount int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM edges").Scan(&edgeCount) //nolint:errcheck
	db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM entities
		WHERE fact_status != 'deleted'
		  AND id NOT IN (SELECT DISTINCT from_id FROM edges
		                 UNION SELECT DISTINCT to_id FROM edges)
	`).Scan(&isolatedCount) //nolint:errcheck

	var dbSize int64
	if info, err := os.Stat(dbPath); err == nil {
		dbSize = info.Size()
	}

	var verifiedPct, stalePct float64
	if totalEntities > 0 {
		verifiedPct = float64(verified) / float64(totalEntities) * 100
		stalePct = float64(stale) / float64(totalEntities) * 100
	}

	score := 0.0
	if totalEntities > 0 {
		staleFrac := float64(stale) / float64(totalEntities)
		unknownFrac := float64(unknown) / float64(totalEntities)
		isolatedFrac := float64(isolatedCount) / float64(totalEntities)
		score = 1.0 - (staleFrac * 0.5) - (unknownFrac * 0.2) - (isolatedFrac * 0.15)
		if score < 0 {
			score = 0
		}
	}

	var level string
	switch {
	case totalEntities == 0:
		level = "empty"
	case score < 0.5:
		level = "poor"
	case score < 0.75:
		level = "fair"
	case score < 0.9:
		level = "good"
	default:
		level = "excellent"
	}

	return swlHealthData{
		Score:         score,
		Level:         level,
		EntityCount:   totalEntities,
		VerifiedPct:   verifiedPct,
		StalePct:      stalePct,
		EdgeCount:     edgeCount,
		IsolatedCount: isolatedCount,
		DBSizeBytes:   dbSize,
		Message:       fmt.Sprintf("%d entities, %.0f%% verified, %.0f%% stale", totalEntities, verifiedPct, stalePct),
	}
}

func (h *Handler) handleSWLHealth(w http.ResponseWriter, r *http.Request) {
	dbPath, err := h.swlDBPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	db, err := openSWLReadOnly(dbPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(swlHealthData{ //nolint:errcheck
			Score: 0, Level: "empty", Message: "SWL database not found.",
		})
		return
	}
	defer db.Close()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(computeHealth(r.Context(), db, dbPath)) //nolint:errcheck
}

// --- Sessions endpoint ---

type swlSessionRow struct {
	ID        string `json:"id"`
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt,omitempty"`
	Goal      string `json:"goal,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

func (h *Handler) handleSWLSessions(w http.ResponseWriter, r *http.Request) {
	dbPath, err := h.swlDBPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	db, err := openSWLReadOnly(dbPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer db.Close()

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err2 := strconv.Atoi(lStr); err2 == nil && n >= 1 && n <= 200 {
			limit = n
		}
	}

	rows, err := db.QueryContext(r.Context(), `
		SELECT id, started_at, ended_at, goal, summary
		FROM sessions ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	sessions := make([]swlSessionRow, 0, 16)
	for rows.Next() {
		var s swlSessionRow
		var endedAt, goal, summary sql.NullString
		if rows.Scan(&s.ID, &s.StartedAt, &endedAt, &goal, &summary) != nil {
			continue
		}
		s.EndedAt  = nullStr(endedAt)
		s.Goal     = nullStr(goal)
		s.Summary  = nullStr(summary)
		sessions = append(sessions, s)
	}
	_ = rows.Err()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions) //nolint:errcheck
}

// --- Overview endpoint (combined stats + health + sessions in one DB connection) ---

type swlOverviewData struct {
	Stats    swlStatsData    `json:"stats"`
	Health   swlHealthData   `json:"health"`
	Sessions []swlSessionRow `json:"sessions"`
}

func (h *Handler) handleSWLOverview(w http.ResponseWriter, r *http.Request) {
	dbPath, err := h.swlDBPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	out := swlOverviewData{
		Stats:    swlStatsData{DBPath: dbPath, Rows: make([]swlStatRow, 0)},
		Health:   swlHealthData{Level: "empty", Message: "SWL database not found."},
		Sessions: make([]swlSessionRow, 0),
	}

	db, err := openSWLReadOnly(dbPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out) //nolint:errcheck
		return
	}
	defer db.Close()

	// Stats
	srows, serr := db.QueryContext(r.Context(), `
		SELECT type,
		       COUNT(*),
		       SUM(CASE WHEN fact_status='verified' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN fact_status='stale'    THEN 1 ELSE 0 END),
		       SUM(CASE WHEN fact_status='unknown'  THEN 1 ELSE 0 END)
		FROM entities GROUP BY type ORDER BY COUNT(*) DESC
	`)
	if serr == nil {
		defer srows.Close()
		for srows.Next() {
			var sr swlStatRow
			if srows.Scan(&sr.Type, &sr.Total, &sr.Verified, &sr.Stale, &sr.Unknown) == nil {
				out.Stats.Rows = append(out.Stats.Rows, sr)
			}
		}
		_ = srows.Err()
	}
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM edges").Scan(&out.Stats.EdgeCount) //nolint:errcheck

	// Health
	out.Health = computeHealth(r.Context(), db, dbPath)

	// Sessions
	rrows, rerr := db.QueryContext(r.Context(), `
		SELECT id, started_at, ended_at, goal, summary
		FROM sessions ORDER BY started_at DESC LIMIT 50
	`)
	if rerr == nil {
		defer rrows.Close()
		for rrows.Next() {
			var s swlSessionRow
			var endedAt, goal, summary sql.NullString
			if rrows.Scan(&s.ID, &s.StartedAt, &endedAt, &goal, &summary) != nil {
				continue
			}
			if endedAt.Valid { s.EndedAt = endedAt.String }
			if goal.Valid { s.Goal = goal.String }
			if summary.Valid { s.Summary = summary.String }
			out.Sessions = append(out.Sessions, s)
		}
		_ = rrows.Err()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out) //nolint:errcheck
}

// --- SSE stream endpoint ---

func (h *Handler) handleSWLStream(w http.ResponseWriter, r *http.Request) {
	dbPath, err := h.swlDBPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	db, _ := openSWLReadOnly(dbPath)
	// Single defer closes whatever db is at function return — avoids defer-in-loop
	// accumulation when the stream reconnects to a newly-available DB.
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	if db == nil {
		fmt.Fprintf(w, ": swl not ready\n\n")
		flusher.Flush()
	}

	// Initialize watermarks at connect time so we only stream changes from now on.
	// This prevents re-flooding data that the initial REST call already delivered.
	var lastModAt, lastEdgeAt string
	if db != nil {
		db.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(modified_at),'') FROM entities"). //nolint:errcheck
			Scan(&lastModAt)
		db.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(confirmed_at),'') FROM edges"). //nolint:errcheck
			Scan(&lastEdgeAt)
	}

	const (
		minInterval = 2 * time.Second
		maxInterval = 10 * time.Second
	)
	interval := minInterval

	for {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(interval):
		}

		// Reconnect if DB became available after initial connection attempt.
		if db == nil {
			if db2, _ := openSWLReadOnly(dbPath); db2 != nil {
				db = db2
				db.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(modified_at),'') FROM entities"). //nolint:errcheck
					Scan(&lastModAt)
				db.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(confirmed_at),'') FROM edges"). //nolint:errcheck
					Scan(&lastEdgeAt)
			}
			continue
		}

		var maxModAt string
		if err := db.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(modified_at),'') FROM entities").
			Scan(&maxModAt); err != nil {
			// DB connection stale — reset to trigger reconnect on next iteration.
			db.Close()
			db = nil
			continue
		}
		var maxEdgeAt string
		db.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(confirmed_at),'') FROM edges"). //nolint:errcheck
			Scan(&maxEdgeAt)

		noEntityChange := maxModAt == "" || maxModAt == lastModAt
		noEdgeChange := maxEdgeAt == "" || maxEdgeAt == lastEdgeAt
		if noEntityChange && noEdgeChange {
			// No change — back off up to maxInterval
			if interval < maxInterval {
				interval *= 2
				if interval > maxInterval {
					interval = maxInterval
				}
			}
			continue
		}

		// Cursor-based pagination — advance watermark only after query succeeds.
		// Query entities modified SINCE the previous watermark (not AT the new one).
		rows, err := db.QueryContext(r.Context(), `
			SELECT id, type, name, confidence, fact_status, knowledge_depth, access_count, metadata, modified_at
			FROM entities WHERE modified_at > ? ORDER BY modified_at ASC LIMIT 100`,
			lastModAt,
		)
		if err != nil {
			// DB connection stale — reset to trigger reconnect on next iteration.
			db.Close()
			db = nil
			continue
		}

		var updates []swlNode
		var lastProcessedModAt string
		for rows.Next() {
			var n swlNode
			var metaStr string
			var rowModAt string
			if rows.Scan(&n.ID, &n.Type, &n.Name, &n.Confidence,
				&n.FactStatus, &n.KnowledgeDepth, &n.AccessCount, &metaStr, &rowModAt) == nil {
				n.Name = swlShortName(n.Name)
				if metaStr != "" && metaStr != "{}" {
					_ = json.Unmarshal([]byte(metaStr), &n.Metadata)
				}
				if rowModAt != "" {
					lastProcessedModAt = rowModAt
				}
				updates = append(updates, n)
			}
		}
		_ = rows.Err()
		rows.Close() //nolint:sqlclosecheck // explicitly closed per poll iteration

		if len(updates) > 0 && lastProcessedModAt != "" {
			lastModAt = lastProcessedModAt
		}

		// Poll new edges — cursor-based on confirmed_at.
		var newEdges []swlLink
		var lastProcessedEdgeAt string
		edgeRows, err := db.QueryContext(r.Context(), `
			SELECT from_id, rel, to_id, COALESCE(source_session,''), confirmed_at
			FROM edges WHERE confirmed_at > ? ORDER BY confirmed_at ASC LIMIT 200`,
			lastEdgeAt,
		)
		if err == nil {
			for edgeRows.Next() {
				var l swlLink
				var edgeConfAt string
				if edgeRows.Scan(&l.Source, &l.Rel, &l.Target, &l.SessionID, &edgeConfAt) == nil {
					newEdges = append(newEdges, l)
					if edgeConfAt != "" {
						lastProcessedEdgeAt = edgeConfAt
					}
				}
			}
			_ = edgeRows.Err()
			edgeRows.Close() //nolint:sqlclosecheck
			if len(newEdges) > 0 && lastProcessedEdgeAt != "" {
				lastEdgeAt = lastProcessedEdgeAt
			}
		}

		if len(updates) == 0 && len(newEdges) == 0 {
			continue
		}

		// Activity detected — reset to minimum interval.
		interval = minInterval

		delta := map[string]any{"type": "delta"}
		if len(updates) > 0 {
			delta["nodes"] = updates
		}
		if len(newEdges) > 0 {
			delta["links"] = newEdges
		}
		payload, _ := json.Marshal(delta)
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}
}

func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func swlShortName(name string) string {
	if len(name) > 50 {
		return "..." + name[len(name)-47:]
	}
	return name
}

