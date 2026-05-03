package api

import (
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
	mux.HandleFunc("GET /api/swl/stats", h.handleSWLStats)
	mux.HandleFunc("GET /api/swl/sessions", h.handleSWLSessions)
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
	ID               string         `json:"id"`
	Type             string         `json:"type"`
	Name             string         `json:"name"`
	Confidence       float64        `json:"confidence"`
	FactStatus       string         `json:"factStatus"`
	KnowledgeDepth   int            `json:"knowledgeDepth"`
	AccessCount      int            `json:"accessCount"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type swlLink struct {
	Source    string `json:"source"`
	Target    string `json:"target"`
	Rel       string `json:"rel"`
	SessionID string `json:"sessionId,omitempty"`
}

type swlGraphData struct {
	Nodes []swlNode `json:"nodes"`
	Links []swlLink `json:"links"`
	Meta  struct {
		NodeCount  int    `json:"nodeCount"`
		LinkCount  int    `json:"linkCount"`
		BuildTime  string `json:"buildTime"`
	} `json:"meta"`
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

	const maxEdges        = 2000
	const maxNodes        = 500
	const maxEdgesPerNode = 50 // prevents hub nodes (700+ degree) from flooding the edge budget

	// Phase 1: Select the highest-quality edges (both endpoints non-deleted).
	// Ordering by combined depth+access prioritises structural hub edges over
	// recently-touched leaf edges, ensuring parent→child connections survive.
	// We scan more than maxEdges so the per-node cap doesn't starve the budget.
	edgeRows, err := db.QueryContext(r.Context(), `
		SELECT e.from_id, e.rel, e.to_id, COALESCE(e.source_session,'')
		FROM edges e
		JOIN entities n1 ON n1.id = e.from_id AND n1.fact_status != 'deleted'
		JOIN entities n2 ON n2.id = e.to_id   AND n2.fact_status != 'deleted'
		ORDER BY (n1.knowledge_depth + n2.knowledge_depth +
		          MIN(n1.access_count,50) + MIN(n2.access_count,50)) DESC
		LIMIT ?
	`, maxEdges*3)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	links := make([]swlLink, 0, maxEdges)
	neededIDs := map[string]bool{}
	nodeDegree := map[string]int{} // tracks edges-per-node; enforces per-hub cap
	for edgeRows.Next() {
		var l swlLink
		if err := edgeRows.Scan(&l.Source, &l.Rel, &l.Target, &l.SessionID); err != nil {
			continue
		}
		if nodeDegree[l.Source] >= maxEdgesPerNode || nodeDegree[l.Target] >= maxEdgesPerNode {
			continue // skip: this endpoint has reached its display budget
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

	// Phase 2: Fetch entity details for all edge-endpoint IDs in batches.
	// SQLite has a 999-parameter limit; use batches of 400.
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
			continue
		}
		scanNode(brows)
		brows.Close()
	}

	// Phase 3: Fill remaining capacity with high-value isolated nodes.
	// Exclude trivial leaf nodes (Symbol/Section at depth≤1 with a single access)
	// since 10k+ of them dominate the DB but add noise to overview renders.
	// Files, Directories, Tasks, Topics, Dependencies, etc. always qualify.
	if len(nodes) < maxNodes {
		remaining := maxNodes - len(nodes)
		fillRows, ferr := db.QueryContext(r.Context(), `
			SELECT id,type,name,confidence,fact_status,knowledge_depth,access_count,metadata
			FROM entities
			WHERE fact_status != 'deleted'
			  AND NOT (type IN ('Symbol','Section') AND knowledge_depth <= 1 AND access_count <= 1)
			ORDER BY knowledge_depth DESC, access_count DESC, accessed_at DESC
			LIMIT ?
		`, remaining*3) // over-select to account for already-included rows
		if ferr == nil {
			scanNode(fillRows)
			fillRows.Close()
		}
		// Trim to maxNodes if over-selected
		if len(nodes) > maxNodes {
			nodes = nodes[:maxNodes]
		}
	}

	// Re-filter edges: drop any whose endpoints weren't included in the final set.
	validLinks := links[:0]
	for _, l := range links {
		if nodeIDs[l.Source] && nodeIDs[l.Target] {
			validLinks = append(validLinks, l)
		}
	}

	data := swlGraphData{Nodes: nodes, Links: validLinks}
	data.Meta.NodeCount = len(nodes)
	data.Meta.LinkCount = len(validLinks)
	data.Meta.BuildTime = time.Now().UTC().Format(time.RFC3339)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data) //nolint:errcheck
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

// --- Sessions endpoint ---

type swlSessionRow struct {
	ID         string `json:"id"`
	StartedAt  string `json:"startedAt"`
	EndedAt    string `json:"endedAt,omitempty"`
	Goal       string `json:"goal,omitempty"`
	Summary    string `json:"summary,omitempty"`
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

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
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
				continue
			}

			// Capture previous watermark before advancing it.
			prevModAt := lastModAt
			lastModAt = maxModAt

			// Query all entities modified SINCE the previous watermark (not AT the new one).
			// The old code used `modified_at >= currentModAt` which returned only the single
			// most-recent entity instead of everything changed since the last check.
			rows, err := db.QueryContext(r.Context(), `
				SELECT id, type, name, confidence, fact_status, knowledge_depth, access_count, metadata
				FROM entities WHERE modified_at > ? ORDER BY modified_at ASC LIMIT 100`,
				prevModAt,
			)
			if err != nil {
				continue
			}

			var updates []swlNode
			for rows.Next() {
				var n swlNode
				var metaStr string
				if rows.Scan(&n.ID, &n.Type, &n.Name, &n.Confidence,
					&n.FactStatus, &n.KnowledgeDepth, &n.AccessCount, &metaStr) == nil {
					n.Name = swlShortName(n.Name)
					if metaStr != "" && metaStr != "{}" {
						_ = json.Unmarshal([]byte(metaStr), &n.Metadata)
					}
					updates = append(updates, n)
				}
			}
			rows.Close()

			if len(updates) == 0 {
				continue
			}

			payload, _ := json.Marshal(map[string]any{
				"type":  "delta",
				"nodes": updates,
				"modAt": maxModAt,
			})
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func swlShortName(name string) string {
	if len(name) > 50 {
		return "..." + name[len(name)-47:]
	}
	return name
}
