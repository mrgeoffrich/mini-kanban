package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mrgeoffrich/mini-kanban/internal/identity"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

func (s *Store) CreateComment(issueID int64, author, body string) (*model.Comment, error) {
	author, err := ValidateName(author, "author")
	if err != nil {
		return nil, err
	}
	body, err = ValidateBody(body, "body", true)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(
		`INSERT INTO comments (uuid, issue_id, author, body) VALUES (?, ?, ?, ?)`,
		identity.New(), issueID, author, body,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetCommentByID(id)
}

const commentCols = `id, uuid, issue_id, author, body, created_at`

func (s *Store) GetCommentByID(id int64) (*model.Comment, error) {
	row := s.DB.QueryRow(`SELECT `+commentCols+` FROM comments WHERE id = ?`, id)
	return scanComment(row)
}

// GetCommentByUUID is the sync-side lookup. Comments live in their own
// timestamped files under each issue folder, so an importer needs to
// match incoming comment uuids against the DB.
func (s *Store) GetCommentByUUID(uuid string) (*model.Comment, error) {
	row := s.DB.QueryRow(`SELECT `+commentCols+` FROM comments WHERE uuid = ?`, uuid)
	return scanComment(row)
}

func (s *Store) ListComments(issueID int64) ([]*model.Comment, error) {
	rows, err := s.DB.Query(`SELECT `+commentCols+` FROM comments WHERE issue_id = ? ORDER BY created_at, id`, issueID)
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
	err := row.Scan(&c.ID, &c.UUID, &c.IssueID, &c.Author, &c.Body, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan comment: %w", err)
	}
	return &c, nil
}
