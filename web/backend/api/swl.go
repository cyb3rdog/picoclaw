package api

import (
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sipeed/picoclaw/pkg/config"
)

func (h *Handler) registerSWLRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/swl/graph", h.handleSWLGraph)
	mux.HandleFunc("GET /api/swl/graph/neighborhood", h.handleSWLNeighborhood)
	mux.HandleFunc("GET /api/swl/graph/topology", h.handleSWLTopology)
	mux.HandleFunc("GET /api/swl/stats", h.handleSWLStats)
	mux.HandleFunc("GET /api/swl/health", h.handleSWLHealth)
	mux.HandleFunc("GET /api/swl/sessions", h.handleSWLSessions)
	mux.HandleFunc("GET /api/swl/overview", h.handleSWLOverview)
	mux.HandleFunc("GET /api/swl/stream", h.handleSWLStream)
}

// swlDBPath resolves the SWL database path from config.
func (h *Handler) swlDBPath() (string, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	if cfg.Tools.SWL != nil && cfg.Tools.SWL.DBPath != "" {
		return cfg.Tools.SWL.DBPath, nil
	}
	workspace := cfg.WorkspacePath()
	return filepath.Join(workspace, ".swl", "swl.db"), nil
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
	var sessionEdgeFilter string
	if mode == "session" {
		sessionIDs := swlRecentSessionIDs(r, db, 5)
		if len(sessionIDs) > 0 {
			ph := "'" + strings.Join(sessionIDs, "','") + "'"
			sessionEdgeFilter = `AND (e.source_session IN (` + ph + `) OR n1.type = 'Session' OR n2.type = 'Session')`
		}
		// Fall back to map behaviour if no sessions found.
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
	edgeRows, err := db.QueryContext(r.Context(), edgeQuery, maxEdges*3)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

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
	edgeRows.Close()

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
			fillRows.Close()
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
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM entities WHERE fact_status != 'deleted'").Scan(&totalNodes) //nolint:errcheck
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM edges").Scan(&totalEdges)                                  //nolint:errcheck

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
	hop1Rows.Close()

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
			args := make([]any, len(batch)*2)
			for j, id := range batch {
				args[j] = id
				args[len(batch)+j] = id
			}
			hop2Rows, err := db.QueryContext(r.Context(), `
				SELECT e.from_id, e.rel, e.to_id, COALESCE(e.source_session,'')
				FROM edges e
				JOIN entities n1 ON n1.id = e.from_id AND n1.fact_status != 'deleted'
				JOIN entities n2 ON n2.id = e.to_id   AND n2.fact_status != 'deleted'
				WHERE e.from_id IN (`+ph+`) AND e.to_id IN (`+ph+`)
				LIMIT ?`, append(args, maxEdges-len(links))...)
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
				hop2Rows.Close()
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
		brows.Close()
	}

	// Re-filter edges.
	validLinks := links[:0]
	for _, l := range links {
		if nodeIDs[l.Source] && nodeIDs[l.Target] {
			validLinks = append(validLinks, l)
		}
	}

	var totalNodes, totalEdges int
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM entities WHERE fact_status != 'deleted'").Scan(&totalNodes) //nolint:errcheck
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM edges").Scan(&totalEdges)                                  //nolint:errcheck

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
	db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&data.EdgeCount) //nolint:errcheck

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data) //nolint:errcheck
}

// --- Health endpoint ---

type swlHealthData struct {
	Score       float64 `json:"score"`
	Level       string  `json:"level"`
	EntityCount int     `json:"entityCount"`
	VerifiedPct float64 `json:"verifiedPct"`
	StalePct    float64 `json:"stalePct"`
	EdgeCount   int     `json:"edgeCount"`
	DBSizeBytes int64   `json:"dbSizeBytes"`
	Message     string  `json:"message"`
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

	var totalEntities, verified, stale, unknown int
	db.QueryRowContext(r.Context(), `
		SELECT
			COUNT(*),
			SUM(CASE WHEN fact_status='verified' THEN 1 ELSE 0 END),
			SUM(CASE WHEN fact_status='stale'    THEN 1 ELSE 0 END),
			SUM(CASE WHEN fact_status='unknown'  THEN 1 ELSE 0 END)
		FROM entities WHERE fact_status != 'deleted'
	`).Scan(&totalEntities, &verified, &stale, &unknown) //nolint:errcheck

	var edgeCount int
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM edges").Scan(&edgeCount) //nolint:errcheck

	var dbSize int64
	if info, statErr := os.Stat(dbPath); statErr == nil {
		dbSize = info.Size()
	}

	var verifiedPct, stalePct float64
	if totalEntities > 0 {
		verifiedPct = float64(verified) / float64(totalEntities) * 100
		stalePct = float64(stale) / float64(totalEntities) * 100
	}

	// Health score: 1.0 baseline, penalised by stale (−0.5×fraction) and unknown (−0.2×fraction).
	score := 0.0
	if totalEntities > 0 {
		staleFrac := float64(stale) / float64(totalEntities)
		unknownFrac := float64(unknown) / float64(totalEntities)
		score = 1.0 - (staleFrac * 0.5) - (unknownFrac * 0.2)
		if score < 0 {
			score = 0
		}
	}

	level := "good"
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

	data := swlHealthData{
		Score:       score,
		Level:       level,
		EntityCount: totalEntities,
		VerifiedPct: verifiedPct,
		StalePct:    stalePct,
		EdgeCount:   edgeCount,
		DBSizeBytes: dbSize,
		Message:     fmt.Sprintf("%d entities, %.0f%% verified, %.0f%% stale", totalEntities, verifiedPct, stalePct),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data) //nolint:errcheck
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

	rows, err := db.QueryContext(r.Context(), `
		SELECT id, started_at, ended_at, goal, summary
		FROM sessions ORDER BY started_at DESC LIMIT 50
	`)
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
		s.EndedAt = endedAt.String
		s.Goal = goal.String
		s.Summary = summary.String
		sessions = append(sessions, s)
	}

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
	}
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM edges").Scan(&out.Stats.EdgeCount) //nolint:errcheck

	// Health
	var totalEntities, verified, stale, unknown int
	db.QueryRowContext(r.Context(), `
		SELECT
			COUNT(*),
			SUM(CASE WHEN fact_status='verified' THEN 1 ELSE 0 END),
			SUM(CASE WHEN fact_status='stale'    THEN 1 ELSE 0 END),
			SUM(CASE WHEN fact_status='unknown'  THEN 1 ELSE 0 END)
		FROM entities WHERE fact_status != 'deleted'
	`).Scan(&totalEntities, &verified, &stale, &unknown) //nolint:errcheck

	var edgeCount int
	db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM edges").Scan(&edgeCount) //nolint:errcheck

	var dbSize int64
	if info, statErr := os.Stat(dbPath); statErr == nil {
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
		score = 1.0 - (staleFrac * 0.5) - (unknownFrac * 0.2)
		if score < 0 {
			score = 0
		}
	}

	level := "good"
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

	out.Health = swlHealthData{
		Score:       score,
		Level:       level,
		EntityCount: totalEntities,
		VerifiedPct: verifiedPct,
		StalePct:    stalePct,
		EdgeCount:   edgeCount,
		DBSizeBytes: dbSize,
		Message:     fmt.Sprintf("%d entities, %.0f%% verified, %.0f%% stale", totalEntities, verifiedPct, stalePct),
	}

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
			s.EndedAt = endedAt.String
			s.Goal = goal.String
			s.Summary = summary.String
			out.Sessions = append(out.Sessions, s)
		}
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

	// Initialise watermark at connect time so we only stream changes from now on.
	// This prevents re-flooding data that the initial REST call already delivered.
	var lastModAt string
	if db != nil {
		db.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(modified_at),'') FROM entities").Scan(&lastModAt) //nolint:errcheck
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
				db.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(modified_at),'') FROM entities").Scan(&lastModAt) //nolint:errcheck
			}
			continue
		}

		var maxModAt string
		db.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(modified_at),'') FROM entities").Scan(&maxModAt) //nolint:errcheck

		if maxModAt == "" || maxModAt == lastModAt {
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
		rows.Close()

		if len(updates) > 0 && lastProcessedModAt != "" {
			lastModAt = lastProcessedModAt
		}

		if len(updates) == 0 {
			continue
		}

		// Activity detected — reset to minimum interval
		interval = minInterval

		payload, _ := json.Marshal(map[string]any{
			"type":  "delta",
			"nodes": updates,
		})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}
}

func swlShortName(name string) string {
	if len(name) > 50 {
		return "..." + name[len(name)-47:]
	}
	return name
}

// handleSWLTopology returns the full graph topology (lightweight nodes/edges only).
// This is used for initial load where we need all nodes at once for physics simulation.
// FIX: Added pagination for large graphs to prevent memory exhaustion.
func (h *Handler) handleSWLTopology(w http.ResponseWriter, r *http.Request) {
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

	// FIX: Paginate results to handle large graphs (10k nodes per page max)
	const nodesPerPage = 10000
	const edgesPerPage = 20000

	type topologyNode struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Name string `json:"name"`
	}
	type topologyLink struct {
		Source string `json:"source"`
		Target string `json:"target"`
		Rel    string `json:"rel"`
	}

	// Fetch nodes in pages
	allNodes := make([]topologyNode, 0)
	nodeIDs := make(map[string]bool)
	offset := 0
	for {
		nodeRows, err := db.QueryContext(r.Context(), `
			SELECT id, type, name FROM entities 
			WHERE fact_status != 'deleted' 
			ORDER BY id LIMIT ? OFFSET ?`, nodesPerPage, offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		
		count := 0
		for nodeRows.Next() {
			var n topologyNode
			if nodeRows.Scan(&n.ID, &n.Type, &n.Name) == nil {
				allNodes = append(allNodes, n)
				nodeIDs[n.ID] = true
				count++
			}
		}
		nodeRows.Close()
		
		if count < nodesPerPage {
			break
		}
		offset += nodesPerPage
		
		// Safety limit: max 50k nodes
		if len(allNodes) >= 50000 {
			break
		}
	}

	// Fetch edges in pages
	allLinks := make([]topologyLink, 0)
	edgeOffset := 0
	for {
		edgeRows, err := db.QueryContext(r.Context(), `
			SELECT from_id, rel, to_id FROM edges
			ORDER BY from_id, rel, to_id LIMIT ? OFFSET ?`, edgesPerPage, edgeOffset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		
		count := 0
		for edgeRows.Next() {
			var l topologyLink
			if edgeRows.Scan(&l.Source, &l.Rel, &l.Target) == nil {
				// Only include edges where both endpoints exist
				if nodeIDs[l.Source] && nodeIDs[l.Target] {
					allLinks = append(allLinks, l)
				}
				count++
			}
		}
		edgeRows.Close()
		
		if count < edgesPerPage {
			break
		}
		edgeOffset += edgesPerPage
		
		// Safety limit: max 100k edges
		if len(allLinks) >= 100000 {
			break
		}
	}

	// Wrap in gzip
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Type", "application/json")
	gz := gzip.NewWriter(w)
	defer gz.Close()
	json.NewEncoder(gz).Encode(struct {
		Nodes []topologyNode `json:"nodes"`
		Links []topologyLink `json:"links"`
		Meta  struct {
			TotalNodes int `json:"totalNodes"`
			TotalEdges int `json:"totalEdges"`
		} `json:"meta"`
	}{
		Nodes: allNodes,
		Links: allLinks,
		Meta: struct {
			TotalNodes int `json:"totalNodes"`
			TotalEdges int `json:"totalEdges"`
		}{len(allNodes), len(allLinks)},
	})
}
