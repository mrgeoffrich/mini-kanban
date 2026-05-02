package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"mini-kanban/internal/model"
)

var ErrAttachmentExists = errors.New("attachment with that filename already exists on this target")

// AttachmentTarget identifies what an attachment is bound to. Exactly one of
// IssueID or FeatureID must be set.
type AttachmentTarget struct {
	IssueID   *int64
	FeatureID *int64
}

func (t AttachmentTarget) check() error {
	if (t.IssueID == nil) == (t.FeatureID == nil) {
		return errors.New("attachment target must specify exactly one of issue or feature")
	}
	return nil
}

func (s *Store) CreateAttachment(t AttachmentTarget, filename, content string) (*model.Attachment, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(
		`INSERT INTO attachments (issue_id, feature_id, filename, content, size_bytes) VALUES (?, ?, ?, ?, ?)`,
		nullableInt(t.IssueID), nullableInt(t.FeatureID), filename, content, len(content),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrAttachmentExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetAttachmentByID(id, true)
}

func (s *Store) GetAttachmentByID(id int64, withContent bool) (*model.Attachment, error) {
	cols := "id, issue_id, feature_id, filename, size_bytes, created_at"
	if withContent {
		cols += ", content"
	}
	row := s.DB.QueryRow(`SELECT `+cols+` FROM attachments WHERE id = ?`, id)
	return scanAttachment(row, withContent)
}

func (s *Store) GetAttachmentByName(t AttachmentTarget, filename string, withContent bool) (*model.Attachment, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	cols := "id, issue_id, feature_id, filename, size_bytes, created_at"
	if withContent {
		cols += ", content"
	}
	var (
		row *sql.Row
	)
	switch {
	case t.IssueID != nil:
		row = s.DB.QueryRow(`SELECT `+cols+` FROM attachments WHERE issue_id = ? AND filename = ?`, *t.IssueID, filename)
	case t.FeatureID != nil:
		row = s.DB.QueryRow(`SELECT `+cols+` FROM attachments WHERE feature_id = ? AND filename = ?`, *t.FeatureID, filename)
	}
	return scanAttachment(row, withContent)
}

func (s *Store) ListAttachments(t AttachmentTarget) ([]*model.Attachment, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	const cols = "id, issue_id, feature_id, filename, size_bytes, created_at"
	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case t.IssueID != nil:
		rows, err = s.DB.Query(`SELECT `+cols+` FROM attachments WHERE issue_id = ? ORDER BY filename`, *t.IssueID)
	case t.FeatureID != nil:
		rows, err = s.DB.Query(`SELECT `+cols+` FROM attachments WHERE feature_id = ? ORDER BY filename`, *t.FeatureID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Attachment
	for rows.Next() {
		a, err := scanAttachment(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAttachment(t AttachmentTarget, filename string) (int64, error) {
	if err := t.check(); err != nil {
		return 0, err
	}
	var (
		res sql.Result
		err error
	)
	switch {
	case t.IssueID != nil:
		res, err = s.DB.Exec(`DELETE FROM attachments WHERE issue_id = ? AND filename = ?`, *t.IssueID, filename)
	case t.FeatureID != nil:
		res, err = s.DB.Exec(`DELETE FROM attachments WHERE feature_id = ? AND filename = ?`, *t.FeatureID, filename)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanAttachment(row rowScanner, withContent bool) (*model.Attachment, error) {
	var (
		a         model.Attachment
		issueID   sql.NullInt64
		featureID sql.NullInt64
		err       error
	)
	if withContent {
		err = row.Scan(&a.ID, &issueID, &featureID, &a.Filename, &a.SizeBytes, &a.CreatedAt, &a.Content)
	} else {
		err = row.Scan(&a.ID, &issueID, &featureID, &a.Filename, &a.SizeBytes, &a.CreatedAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan attachment: %w", err)
	}
	if issueID.Valid {
		v := issueID.Int64
		a.IssueID = &v
	}
	if featureID.Valid {
		v := featureID.Int64
		a.FeatureID = &v
	}
	return &a, nil
}
