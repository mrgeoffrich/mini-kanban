package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

func (s *Store) CreateRelation(fromID, toID int64, t model.RelationType) error {
	_, err := s.DB.Exec(
		`INSERT INTO issue_relations (from_issue_id, to_issue_id, type) VALUES (?, ?, ?)`,
		fromID, toID, string(t),
	)
	return err
}

func (s *Store) DeleteRelation(fromID, toID int64) (int64, error) {
	res, err := s.DB.Exec(
		`DELETE FROM issue_relations
		   WHERE (from_issue_id = ? AND to_issue_id = ?)
		      OR (from_issue_id = ? AND to_issue_id = ?)`,
		fromID, toID, toID, fromID,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// IssueRelations describes the relations involving an issue, with the issue
// itself as the implicit "self" — outgoing means self -> other.
type IssueRelations struct {
	Outgoing []model.Relation `json:"outgoing"`
	Incoming []model.Relation `json:"incoming"`
}

func (s *Store) ListIssueRelations(issueID int64) (*IssueRelations, error) {
	out := &IssueRelations{}

	rows, err := s.DB.Query(`
		SELECT ir.id,
		       rf.prefix || '-' || self.number AS from_key,
		       rt.prefix || '-' || other.number AS to_key,
		       ir.type, ir.created_at
		FROM issue_relations ir
		JOIN issues self  ON self.id  = ir.from_issue_id
		JOIN issues other ON other.id = ir.to_issue_id
		JOIN repos rf ON rf.id = self.repo_id
		JOIN repos rt ON rt.id = other.repo_id
		WHERE ir.from_issue_id = ?
		ORDER BY ir.created_at`, issueID)
	if err != nil {
		return nil, err
	}
	out.Outgoing, err = scanRelations(rows)
	if err != nil {
		return nil, err
	}

	rows, err = s.DB.Query(`
		SELECT ir.id,
		       rf.prefix || '-' || other.number AS from_key,
		       rt.prefix || '-' || self.number AS to_key,
		       ir.type, ir.created_at
		FROM issue_relations ir
		JOIN issues other ON other.id = ir.from_issue_id
		JOIN issues self  ON self.id  = ir.to_issue_id
		JOIN repos rf ON rf.id = other.repo_id
		JOIN repos rt ON rt.id = self.repo_id
		WHERE ir.to_issue_id = ?
		ORDER BY ir.created_at`, issueID)
	if err != nil {
		return nil, err
	}
	out.Incoming, err = scanRelations(rows)
	return out, err
}

func scanRelations(rows *sql.Rows) ([]model.Relation, error) {
	defer rows.Close()
	var out []model.Relation
	for rows.Next() {
		var r model.Relation
		var t string
		if err := rows.Scan(&r.ID, &r.FromIssue, &r.ToIssue, &t, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan relation: %w", err)
		}
		r.Type = model.RelationType(t)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Sentinel for callers that want to differentiate "not found" deletions.
var ErrNoRelation = errors.New("no relation between issues")

// IssueBlocker describes one `blocks` edge pointing AT a given blocked issue,
// with enough info about the blocker (key + state) for callers to render
// dependency hints without N+1 lookups.
type IssueBlocker struct {
	BlockedID    int64
	BlockerID    int64
	BlockerKey   string
	BlockerState model.State
}

// BlockersFor returns, keyed by blocked-issue id, every `blocks` edge whose
// "to" side (the blocked issue) is in the given id set. Blockers may live in
// any repo / feature — callers decide how to interpret cross-feature blockers.
func (s *Store) BlockersFor(ids []int64) (map[int64][]IssueBlocker, error) {
	out := map[int64][]IssueBlocker{}
	if len(ids) == 0 {
		return out, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf(`
		SELECT ir.to_issue_id,
		       ir.from_issue_id,
		       r.prefix || '-' || src.number AS blocker_key,
		       src.state
		FROM issue_relations ir
		JOIN issues src ON src.id = ir.from_issue_id
		JOIN repos  r   ON r.id   = src.repo_id
		WHERE ir.type = 'blocks'
		  AND ir.to_issue_id IN (%s)`, strings.Join(ph, ","))
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b IssueBlocker
		var st string
		if err := rows.Scan(&b.BlockedID, &b.BlockerID, &b.BlockerKey, &st); err != nil {
			return nil, fmt.Errorf("scan blocker: %w", err)
		}
		b.BlockerState = model.State(st)
		out[b.BlockedID] = append(out[b.BlockedID], b)
	}
	return out, rows.Err()
}
