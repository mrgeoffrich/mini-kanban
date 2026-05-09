package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mrgeoffrich/mini-kanban/internal/sync"
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
	hasSrc, err := columnExists(db, "documents", "source_path")
	if err != nil {
		return err
	}
	if !hasSrc {
		if _, err := db.Exec(`ALTER TABLE documents ADD COLUMN source_path TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	hasAssignee, err := columnExists(db, "issues", "assignee")
	if err != nil {
		return err
	}
	if !hasAssignee {
		if _, err := db.Exec(`ALTER TABLE issues ADD COLUMN assignee TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_assignee ON issues(assignee)`); err != nil {
		return err
	}
	if err := migrateUUIDs(db); err != nil {
		return err
	}
	return nil
}

// migrateUUIDs adds nullable `uuid` columns to issues, features,
// documents, comments, and repos on databases that pre-date them, then
// backfills each row with a freshly-generated UUIDv7. Once every row is
// populated, a unique index is created so the column matches the
// schema.sql declaration on fresh DBs.
//
// SQLite's ALTER TABLE forbids non-constant defaults on ADD COLUMN, so
// the column is added nullable and backfilled in Go (uuid7 is not a
// SQLite built-in). The whole sequence is idempotent: if the column
// already exists (fresh DB), the ALTER is skipped; if no rows are NULL,
// the backfill loop is a no-op; CREATE UNIQUE INDEX IF NOT EXISTS
// papers over re-runs.
func migrateUUIDs(db *sql.DB) error {
	for _, t := range []string{"issues", "features", "documents", "comments", "repos"} {
		has, err := columnExists(db, t, "uuid")
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN uuid TEXT`, t)); err != nil {
				return fmt.Errorf("add uuid to %s: %w", t, err)
			}
		}
		if err := backfillUUIDs(db, t); err != nil {
			return fmt.Errorf("backfill uuid on %s: %w", t, err)
		}
		idx := "uniq_" + t + "_uuid"
		if _, err := db.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s(uuid)`, idx, t)); err != nil {
			return fmt.Errorf("index %s: %w", idx, err)
		}
	}
	return nil
}

// backfillUUIDs assigns a fresh UUIDv7 to every row whose uuid is NULL
// (or empty, just in case a partial older migration ran). Row-by-row
// updates are fine for mk's data sizes (thousands of rows at most).
func backfillUUIDs(db *sql.DB, table string) error {
	rows, err := db.Query(fmt.Sprintf(`SELECT id FROM %s WHERE uuid IS NULL OR uuid = ''`, table))
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(ids) == 0 {
		return nil
	}
	stmt, err := db.Prepare(fmt.Sprintf(`UPDATE %s SET uuid = ? WHERE id = ?`, table))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.Exec(sync.New(), id); err != nil {
			return err
		}
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
