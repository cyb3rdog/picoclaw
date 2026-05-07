package swl

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
)

//go:embed synth_core_schema.sql
var synthCoreSchema string

// MigrateSynthCore applies SYNTH-CORE schema extensions to the SWL database.
// This migration adds tables for entropy tracking, conflict detection, and goal management.
func MigrateSynthCore(db *sql.DB) error {
	// Execute schema migration
	_, err := db.Exec(synthCoreSchema)
	if err != nil {
		return fmt.Errorf("synth-core schema migration failed: %w", err)
	}

	log.Println("[SWL] SYNTH-CORE schema migration complete")
	return nil
}

// EnsureSynthCoreTables checks if SYNTH-CORE tables exist, creates if not.
func EnsureSynthCoreTables(db *sql.DB) error {
	tables := []string{
		"goals",
		"temporal_edges",
		"conflicts",
		"entropy_log",
		"grafts",
	}

	for _, table := range tables {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name = ?",
			table,
		).Scan(&name)

		if err == sql.ErrNoRows {
			// Table doesn't exist, run migration
			return MigrateSynthCore(db)
		} else if err != nil {
			return fmt.Errorf("check table %q: %w", table, err)
		}
	}

	return nil
}
