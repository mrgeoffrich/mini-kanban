package store

import (
	"database/sql"
	"errors"
	"fmt"

	"mini-kanban/internal/model"
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
