package swl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ScanStats reports the outcome of a ScanWorkspace call.
type ScanStats struct {
	Scanned int
	New     int
	Changed int
	Deleted int
	Skipped int
}

var skipDirs = map[string]bool{
	".git": true, ".svn": true, ".hg": true,
	"node_modules": true, "vendor": true, ".venv": true,
	"venv": true, "__pycache__": true, ".tox": true,
	"dist": true, "build": true, ".build": true,
	".swl": true, // never index the SWL DB directory itself
}

var skipExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".bmp": true, ".ico": true, ".svg": true, ".webp": true,
	".pdf": true, ".doc": true, ".docx": true, ".xls": true,
	".xlsx": true, ".ppt": true, ".pptx": true,
	".zip": true, ".tar": true, ".gz": true, ".bz2": true,
	".xz": true, ".7z": true, ".rar": true,
	".so": true, ".a": true, ".o": true, ".dylib": true,
	".exe": true, ".dll": true, ".bin": true,
	".db": true, ".sqlite": true, ".sqlite3": true,
	".lock": true,
}

// ScanWorkspace does an incremental mtime-based walk of root, upserting
// File and Directory entities and extracting content for new/changed files.
// It also tombstones files that were previously indexed but no longer exist.
func (m *Manager) ScanWorkspace(root string) (ScanStats, error) {
	var stats ScanStats

	// Resolve root to absolute path, validate within workspace
	absRoot := root
	if !filepath.IsAbs(root) {
		if m.workspace != "" {
			absRoot = filepath.Join(m.workspace, root)
		} else {
			absRoot, _ = filepath.Abs(root)
		}
	}

	// Validate root is within workspace (prevent cross-workspace scans)
	if m.workspace != "" {
		absRoot, _ = filepath.Abs(absRoot)
		if !strings.HasPrefix(absRoot, m.workspace) {
			return stats, fmt.Errorf("scan root %q is outside workspace %q", root, m.workspace)
		}
		root = absRoot
	}

	maxSize := m.cfg.effectiveMaxFileSize()

	// Build map of all known file entity IDs → paths from DB.
	knownFiles, err := m.loadKnownFiles()
	if err != nil {
		return stats, err
	}
	visited := map[string]bool{}

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			stats.Skipped++
			return nil
		}

		name := d.Name()
		if d.IsDir() {
			if skipDirs[name] || strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			// Normalize to workspace-relative for consistent entity IDs.
			// Tool calls (via inference.go) use workspace-relative paths.
			// Scanner uses absolute paths from WalkDir. Normalizing here
			// ensures both paths to the same file produce the same entity ID.
			relPath := path
			if m.workspace != "" {
				if r, err := filepath.Rel(m.workspace, path); err == nil && !strings.HasPrefix(r, "..") {
					relPath = r
				}
			}
			dirID := entityID(KnownTypeDirectory, relPath)
			_ = m.writer.upsertEntity(EntityTuple{
				ID: dirID, Type: KnownTypeDirectory, Name: relPath,
				Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
			})
			return nil
		}

		ext := strings.ToLower(filepath.Ext(name))
		if skipExts[ext] {
			stats.Skipped++
			return nil
		}

		// Normalize to workspace-relative for consistent entity IDs.
		relPath := path
		if m.workspace != "" {
			if r, err := filepath.Rel(m.workspace, path); err == nil && !strings.HasPrefix(r, "..") {
				relPath = r
			}
		}

		info, err := d.Info()
		if err != nil {
			stats.Skipped++
			return nil
		}
		if info.Size() > maxSize {
			stats.Skipped++
			return nil
		}

		fileID := entityID(KnownTypeFile, relPath)
		visited[fileID] = true

		isKnown := knownFiles[fileID]
		if !isKnown {
			stats.New++
		}

		// Check mtime vs DB modified_at.
		changed := true
		if isKnown {
			var dbMtime string
			_ = m.db.QueryRow(
				"SELECT modified_at FROM entities WHERE id = ?", fileID,
			).Scan(&dbMtime)
			t := parseRFC3339(dbMtime)
			if !t.IsZero() && !info.ModTime().After(t) {
				changed = false
			}
		}

		if !changed {
			stats.Scanned++
			return nil
		}

		if isKnown {
			stats.Changed++
		}
		stats.Scanned++

		// Upsert the File entity (use relPath for consistent entity IDs).
		dirPath := filepath.Dir(relPath)
		dirID := entityID(KnownTypeDirectory, dirPath)
		_ = m.writer.upsertEntity(EntityTuple{
			ID: fileID, Type: KnownTypeFile, Name: relPath,
			Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
		})
		_ = m.writer.upsertEdge(EdgeTuple{FromID: fileID, Rel: KnownRelInDir, ToID: dirID})

		// Read and extract content.
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		content := string(raw)

		m.writer.mu.Lock()
		changed = m.writer.checkAndInvalidateLocked(fileID, content)
		m.writer.mu.Unlock()

		if changed {
			delta := m.ExtractContent(fileID, relPath, content)
			if delta != nil && !delta.IsEmpty() {
				_ = m.writer.applyDelta(delta, "")
			}
			_ = m.writer.setFactStatus(fileID, FactVerified)
		}

		return nil
	})

	if err != nil {
		return stats, err
	}

	// Tombstone files that are no longer present on disk.
	for fileID := range knownFiles {
		if !visited[fileID] {
			_ = m.SetFactStatus(fileID, FactDeleted)
			stats.Deleted++
		}
	}

	return stats, nil
}

// loadKnownFiles returns a set of entity IDs for all non-deleted File entities.
func (m *Manager) loadKnownFiles() (map[string]bool, error) {
	rows, err := m.db.Query(
		"SELECT id FROM entities WHERE type = ? AND fact_status != ?",
		KnownTypeFile, FactDeleted,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out, rows.Err()
}
