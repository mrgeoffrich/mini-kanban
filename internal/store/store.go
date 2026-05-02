package store

import (
	_ "embed"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// HistoryRetention bounds how long audit-log entries are kept. Anything
// older is dropped on every Open() — keeps the table from growing without
// bound. Tweak here if a longer or shorter retention window is wanted.
const HistoryRetention = 60 * 24 * time.Hour

//go:embed schema.sql
var schemaSQL string

type Store struct {
	DB *sql.DB
}

// DefaultPath returns ~/.mini-kanban/db.sqlite
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mini-kanban", "db.sqlite"), nil
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// History prune runs on every Open. Failures here aren't fatal — the
	// caller's user-visible work shouldn't fail because retention housekeeping
	// hit a snag — so we surface a warning and carry on.
	if err := pruneHistory(db, HistoryRetention); err != nil {
		fmt.Fprintln(os.Stderr, "mk: warning: history prune failed:", err)
	}
	return &Store{DB: db}, nil
}

// pruneHistory deletes audit-log rows older than retention.
func pruneHistory(db *sql.DB, retention time.Duration) error {
	cutoff := time.Now().Add(-retention).UTC().Format("2006-01-02 15:04:05")
	_, err := db.Exec(`DELETE FROM history WHERE created_at < ?`, cutoff)
	return err
}

// migrate brings older databases up to the current schema. SQLite's ALTER
// TABLE doesn't support IF NOT EXISTS, so we check column presence first.
// Once the schema gets more complex, swap this for a real migration tool.
func migrate(db *sql.DB) error {
	has, err := columnExists(db, "features", "updated_at")
	if err != nil {
		return err
	}
	if !has {
		// SQLite forbids non-constant defaults on ALTER TABLE ADD COLUMN, so
		// add it nullable then backfill from created_at.
		if _, err := db.Exec(`ALTER TABLE features ADD COLUMN updated_at DATETIME`); err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE features SET updated_at = created_at WHERE updated_at IS NULL`); err != nil {
			return err
		}
	}
	// The attachments feature was removed in favour of documents; drop the
	// table from any pre-existing DB. Historical history.attachment.* rows
	// stay put since the audit log is append-only.
	if _, err := db.Exec(`DROP TABLE IF EXISTS attachments`); err != nil {
		return err
	}
	return nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) Close() error { return s.DB.Close() }
