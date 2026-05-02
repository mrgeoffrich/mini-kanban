package store

import (
	"database/sql"
	"errors"
	"fmt"

	"mini-kanban/internal/model"
)

func (s *Store) CreateComment(issueID int64, author, body string) (*model.Comment, error) {
	res, err := s.DB.Exec(
		`INSERT INTO comments (issue_id, author, body) VALUES (?, ?, ?)`,
		issueID, author, body,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetCommentByID(id)
}

func (s *Store) GetCommentByID(id int64) (*model.Comment, error) {
	row := s.DB.QueryRow(`SELECT id, issue_id, author, body, created_at FROM comments WHERE id = ?`, id)
	return scanComment(row)
}

func (s *Store) ListComments(issueID int64) ([]*model.Comment, error) {
	rows, err := s.DB.Query(`SELECT id, issue_id, author, body, created_at FROM comments WHERE issue_id = ? ORDER BY created_at, id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanComment(row rowScanner) (*model.Comment, error) {
	var c model.Comment
	err := row.Scan(&c.ID, &c.IssueID, &c.Author, &c.Body, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan comment: %w", err)
	}
	return &c, nil
}
