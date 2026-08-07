package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at dbPath and verifies
// the connection is usable. The caller MUST Close it.
func Open(dbPath string) (*Store, error) {
	// DSN "pragmas" configure SQLite: enforce foreign keys, use WAL journaling
	// (better concurrency), and wait up to 5s on locked DB before erroring.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dbPath, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect sqlite %s: %w", dbPath, err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", dbPath, err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
