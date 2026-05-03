package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

	// Limit to 500 most-recently-active entities for performance.
	rows, err := db.QueryContext(r.Context(), `
		SELECT id, type, name, confidence, fact_status, knowledge_depth, access_count, metadata
		FROM entities
		WHERE fact_status != 'deleted'
		ORDER BY accessed_at DESC
		LIMIT 500
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	nodes := make([]swlNode, 0, 64)
	nodeIDs := map[string]bool{}
	for rows.Next() {
		var n swlNode
		var metaStr string
		if err := rows.Scan(&n.ID, &n.Type, &n.Name, &n.Confidence,
			&n.FactStatus, &n.KnowledgeDepth, &n.AccessCount, &metaStr); err != nil {
			continue
		}
		if metaStr != "" && metaStr != "{}" {
			_ = json.Unmarshal([]byte(metaStr), &n.Metadata)
		}
		n.Name = swlShortName(n.Name)
		nodes = append(nodes, n)
		nodeIDs[n.ID] = true
	}

	// Only return edges where both endpoints are in our node set.
	edgeRows, err := db.QueryContext(r.Context(), `
		SELECT from_id, rel, to_id, source_session FROM edges LIMIT 2000
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer edgeRows.Close()

	links := make([]swlLink, 0, 64)
	for edgeRows.Next() {
		var l swlLink
		var sess sql.NullString
		if err := edgeRows.Scan(&l.Source, &l.Rel, &l.Target, &sess); err != nil {
			continue
		}
		if !nodeIDs[l.Source] || !nodeIDs[l.Target] {
			continue
		}
		l.SessionID = sess.String
		links = append(links, l)
	}

	data := swlGraphData{Nodes: nodes, Links: links}
	data.Meta.NodeCount = len(nodes)
	data.Meta.LinkCount = len(links)
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
	if db != nil {
		defer db.Close()
	} else {
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
					defer db.Close() //nolint:gocritic
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
				SELECT id, type, name, confidence, fact_status, knowledge_depth, access_count
				FROM entities WHERE modified_at > ? ORDER BY modified_at ASC LIMIT 100`,
				prevModAt,
			)
			if err != nil {
				continue
			}

			var updates []swlNode
			for rows.Next() {
				var n swlNode
				if rows.Scan(&n.ID, &n.Type, &n.Name, &n.Confidence,
					&n.FactStatus, &n.KnowledgeDepth, &n.AccessCount) == nil {
					n.Name = swlShortName(n.Name)
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
