package store

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrate applies any embedded migrations not yet recored in schmea_migrations
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
       version			TEXT PRIMARY KEY,
       applied_at		TIMESTAMP NOT NULL
)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names) // filename order = apply order (0001_, 0002_, etc...)

	for _, name := range names {
		var exists bool
		if err := s.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)`, name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check migrations %s: %w", name, err)
		}
		if exists {
			continue // already applied
		}

		script, err := migrationsFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migrations %s: %w", name, err)
		}

		if err := s.applyMigration(name, string(script)); err != nil {
			return err
		}
	}
	return nil
}

// applyMigrations runs one migration and records it - atomically.
func (s *Store) applyMigration(name, script string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin %s: %w", name, err)
	}
	defer tx.Rollback() // harmless no-op if we Commit first

	if _, err := tx.Exec(script); err != nil {
		return fmt.Errorf("apply %s: %w", name, err)
	}

	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))`, name,
	); err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}

	return tx.Commit()
}
