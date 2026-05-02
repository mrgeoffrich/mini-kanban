package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"mini-kanban/internal/model"
)

// RecordHistory inserts an audit-log row. Fields beyond Actor and Op are
// optional and stored as snapshots — the caller passes the labels they
// want preserved so entries remain meaningful after entities get deleted.
func (s *Store) RecordHistory(e model.HistoryEntry) error {
	if e.Actor == "" || e.Op == "" {
		return errors.New("history entry requires actor and op")
	}
	_, err := s.DB.Exec(
		`INSERT INTO history
		   (repo_id, repo_prefix, actor, op, kind, target_id, target_label, details)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullableInt(e.RepoID), e.RepoPrefix, e.Actor, e.Op, e.Kind,
		nullableInt(e.TargetID), e.TargetLabel, e.Details,
	)
	return err
}

type HistoryFilter struct {
	RepoID *int64
	Actor  string
	Op     string
	Since  *time.Time
	Limit  int
}

func (s *Store) ListHistory(f HistoryFilter) ([]*model.HistoryEntry, error) {
	q := `SELECT id, repo_id, repo_prefix, actor, op, kind,
	             target_id, target_label, details, created_at
	      FROM history`
	var (
		where []string
		args  []any
	)
	if f.RepoID != nil {
		where = append(where, "repo_id = ?")
		args = append(args, *f.RepoID)
	}
	if f.Actor != "" {
		where = append(where, "actor = ?")
		args = append(args, f.Actor)
	}
	if f.Op != "" {
		where = append(where, "op = ?")
		args = append(args, f.Op)
	}
	if f.Since != nil {
		where = append(where, "created_at >= ?")
		args = append(args, f.Since.UTC().Format("2006-01-02 15:04:05"))
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC, id DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.HistoryEntry
	for rows.Next() {
		e, err := scanHistory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanHistory(row rowScanner) (*model.HistoryEntry, error) {
	var (
		e        model.HistoryEntry
		repoID   sql.NullInt64
		targetID sql.NullInt64
	)
	err := row.Scan(&e.ID, &repoID, &e.RepoPrefix, &e.Actor, &e.Op, &e.Kind,
		&targetID, &e.TargetLabel, &e.Details, &e.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan history: %w", err)
	}
	if repoID.Valid {
		v := repoID.Int64
		e.RepoID = &v
	}
	if targetID.Valid {
		v := targetID.Int64
		e.TargetID = &v
	}
	return &e, nil
}
