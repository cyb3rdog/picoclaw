package swl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ScanStats reports the outcome of a ScanWorkspace call.
type ScanStats struct {
	Scanned int
	New     int
	Changed int
	Deleted int
	Skipped int
}

// ScanWorkspace does an incremental mtime-based walk of root, upserting
// File and Directory entities and extracting content for new/changed files.
// It also tombstones files that were previously indexed but no longer exist.
// sessionKey, if non-empty, is used as the session context for all edges written
// during this scan so they are visible in session-scoped queries.
func (m *Manager) ScanWorkspace(root string, sessionKey ...string) (ScanStats, error) {
	sk := ""
	if len(sessionKey) > 0 {
		sk = sessionKey[0]
	}
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

	// Phase A: build and apply the workspace semantic snapshot before the
	// structural walk.  This produces AnchorDocument and SemanticArea entities
	// (bounded to ~100–300 total) and replaces the previous per-file
	// ExtractContent call that generated tens of thousands of Symbol entities.
	if snapshotDelta := m.BuildSnapshot(root); snapshotDelta != nil && !snapshotDelta.IsEmpty() {
		_ = m.writer.applyDelta(snapshotDelta, sk)
	}

	// Build map of known file entity IDs → paths from DB, scoped to the
	// scan root.  We only tombstone files that were previously indexed under
	// this root — files outside the scan scope must never be touched.
	relRoot := ""
	if m.workspace != "" {
		if r, err := filepath.Rel(m.workspace, root); err == nil && !strings.HasPrefix(r, "..") {
			relRoot = r
		}
	}
	if relRoot == "" {
		relRoot = root
	}
	knownFiles, err := m.loadKnownFilesUnderRoot(relRoot)
	if err != nil {
		return stats, err
	}
	visited := map[string]bool{}

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			stats.Skipped++
			return nil //nolint:nilerr // skip inaccessible entries; nil tells WalkDir to continue
		}

		name := d.Name()
		if d.IsDir() {
			// Check .swlignore patterns first
			if m.ignoreDirPath(path) {
				return filepath.SkipDir
			}
			// Then check default skip dirs
			if m.shouldIgnoreDir(name) {
				return filepath.SkipDir
			}
			// Normalize to workspace-relative for consistent entity IDs.
			// Tool calls (via inference.go) use workspace-relative paths.
			// Scanner uses absolute paths from WalkDir. Normalizing here
			// ensures both paths to the same file produce the same entity ID.
			relPath := path
			if m.workspace != "" {
				if r, relErr := filepath.Rel(m.workspace, path); relErr == nil && !strings.HasPrefix(r, "..") {
					relPath = r
				}
			}
			dirID := entityID(KnownTypeDirectory, relPath)
			// Derive semantic labels for directory (Phase A.2 — semantic bootstrap).
			dirLabels := m.DeriveLabels(KnownTypeDirectory, relPath)
			dirMeta := dirLabels.ToMetadata()
			_ = m.writer.upsertEntity(EntityTuple{
				ID: dirID, Type: KnownTypeDirectory, Name: relPath,
				Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
				Metadata: dirMeta,
			})
			// Create parent directory edge (skip workspace root which has no parent).
			// filepath.Dir returns "" for single-component paths like "pkg",
			// so we map "" to "." for the workspace root parent.
			if relPath != "." {
				parentRelPath := filepath.Dir(relPath)
				if parentRelPath == "" {
					parentRelPath = "."
				}
				parentDirID := entityID(KnownTypeDirectory, parentRelPath)
				_ = m.writer.upsertEdge(EdgeTuple{FromID: dirID, Rel: KnownRelInDir, ToID: parentDirID})
			}
			return nil
		}

		// Check .swlignore patterns first
		if m.ignoreFilePath(path) {
			stats.Skipped++
			return nil
		}
		// Then check default skip extensions
		if m.shouldIgnoreFile(name) {
			stats.Skipped++
			return nil
		}

		// Normalize to workspace-relative for consistent entity IDs.
		relPath := path
		if m.workspace != "" {
			if r, relErr := filepath.Rel(m.workspace, path); relErr == nil && !strings.HasPrefix(r, "..") {
				relPath = r
			}
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			stats.Skipped++
			return nil //nolint:nilerr // skip unreadable file entries; nil tells WalkDir to continue
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

		// Structural indexing + semantic labels: upsert the File entity and derive
		// role/domain/kind labels from its path. This is Tier 1 ontological inference —
		// no LLM needed, runs at scan time, produces semantically meaningful entities.
		dirPath := filepath.Dir(relPath)
		dirID := entityID(KnownTypeDirectory, dirPath)

		modTime := info.ModTime().UTC().Format(time.RFC3339Nano)

		// Derive semantic labels from file path (Phase A.2 — semantic bootstrap).
		lr := m.DeriveLabels(KnownTypeFile, relPath)
		lrMeta := lr.ToMetadata()

		// Check mtime vs DB modified_at to detect changes without reading content.
		mtimeChanged := true
		if isKnown {
			var dbMtime string
			_ = m.db.QueryRow(
				"SELECT modified_at FROM entities WHERE id = ?", fileID,
			).Scan(&dbMtime)
			t := parseRFC3339(dbMtime)
			if !t.IsZero() && !info.ModTime().After(t) {
				mtimeChanged = false
			}
		}

		if !mtimeChanged {
			stats.Scanned++
			return nil
		}

		if isKnown {
			stats.Changed++
			// File changed on disk — cascade any previously extracted children
			// (symbols, tasks, etc. from prior lazy extraction) to stale so the
			// next read_file triggers fresh extraction.
			m.writer.mu.Lock()
			m.writer.invalidateChildrenLocked(fileID, modTime)
			m.writer.mu.Unlock()
		}
		stats.Scanned++

		_ = m.writer.upsertEntity(EntityTuple{
			ID: fileID, Type: KnownTypeFile, Name: relPath,
			Confidence: 1.0, ExtractionMethod: MethodObserved, KnowledgeDepth: 1,
			Metadata: lrMeta,
		})
		_ = m.writer.upsertEdge(EdgeTuple{FromID: fileID, Rel: KnownRelInDir, ToID: dirID})
		_ = m.writer.setFactStatus(fileID, FactVerified)

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

	// Derive semantic area relationships and cross-file symbol usage from the import graph.
	m.DeriveAreaRelations()
	m.DeriveSymbolUsage()

	return stats, nil
}

// loadKnownFilesUnderRoot returns a set of entity IDs for all non-deleted
// File entities whose workspace-relative path starts with rootPrefix.
// This scopes the tombstone phase so it can only delete files that were
// previously indexed within the same scan root — never files outside it.
func (m *Manager) loadKnownFilesUnderRoot(rootPrefix string) (map[string]bool, error) {
	// When rootPrefix is "." (workspace root), load all file entities — the
	// path filter "name LIKE './%'" would not match workspace-relative names
	// like "hello.go" that lack the leading "./" prefix.
	if rootPrefix == "." || rootPrefix == "" {
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

	// Ensure the prefix ends with / so that "foo/bar" matches "foo/bar/*"
	// but not "foo/barbaz/*".
	prefix := rootPrefix
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	rows, err := m.db.Query(
		"SELECT id FROM entities WHERE type = ? AND fact_status != ? AND (name = ? OR name LIKE ?)",
		KnownTypeFile, FactDeleted, rootPrefix, prefix+"%",
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
