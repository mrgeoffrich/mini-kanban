package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

var ErrDocumentExists = errors.New("a document with that filename already exists in this repo")

func (s *Store) CreateDocument(repoID int64, filename string, t model.DocumentType, content, sourcePath string) (*model.Document, error) {
	filename, err := ValidateDocFilenameStrict(filename)
	if err != nil {
		return nil, err
	}
	content, err = ValidateBody(content, "content", false)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(
		`INSERT INTO documents (repo_id, filename, type, content, size_bytes, source_path) VALUES (?, ?, ?, ?, ?, ?)`,
		repoID, filename, string(t), content, len(content), sourcePath,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrDocumentExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetDocumentByID(id, true)
}

func (s *Store) GetDocumentByID(id int64, withContent bool) (*model.Document, error) {
	cols := docCols(withContent)
	row := s.DB.QueryRow(`SELECT `+cols+` FROM documents WHERE id = ?`, id)
	return scanDocument(row, withContent)
}

func (s *Store) GetDocumentByFilename(repoID int64, filename string, withContent bool) (*model.Document, error) {
	cols := docCols(withContent)
	row := s.DB.QueryRow(`SELECT `+cols+` FROM documents WHERE repo_id = ? AND filename = ?`, repoID, filename)
	return scanDocument(row, withContent)
}

type DocumentFilter struct {
	RepoID int64
	Type   *model.DocumentType
}

func (s *Store) ListDocuments(f DocumentFilter) ([]*model.Document, error) {
	cols := docCols(false)
	q := `SELECT ` + cols + ` FROM documents WHERE repo_id = ?`
	args := []any{f.RepoID}
	if f.Type != nil {
		q += ` AND type = ?`
		args = append(args, string(*f.Type))
	}
	q += ` ORDER BY filename`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Document
	for rows.Next() {
		d, err := scanDocument(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDocument patches the type, content, and/or source_path. Pass nil for
// fields you don't want to change. Bumps updated_at when something actually
// changed.
func (s *Store) UpdateDocument(id int64, newType *model.DocumentType, newContent, newSourcePath *string) error {
	sets := []string{}
	args := []any{}
	if newType != nil {
		sets = append(sets, "type = ?")
		args = append(args, string(*newType))
	}
	if newContent != nil {
		clean, err := ValidateBody(*newContent, "content", false)
		if err != nil {
			return err
		}
		sets = append(sets, "content = ?", "size_bytes = ?")
		args = append(args, clean, len(clean))
	}
	if newSourcePath != nil {
		sets = append(sets, "source_path = ?")
		args = append(args, *newSourcePath)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	_, err := s.DB.Exec(fmt.Sprintf(`UPDATE documents SET %s WHERE id = ?`, strings.Join(sets, ", ")), args...)
	return err
}

// RenameDocument changes a document's filename in place — the row id is
// preserved, so document_links rows stay intact. Optionally updates the
// type at the same time. Returns ErrDocumentExists on filename collision.
func (s *Store) RenameDocument(id int64, newFilename string, newType *model.DocumentType) error {
	newFilename, err := ValidateDocFilenameStrict(newFilename)
	if err != nil {
		return err
	}
	sets := []string{"filename = ?"}
	args := []any{newFilename}
	if newType != nil {
		sets = append(sets, "type = ?")
		args = append(args, string(*newType))
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	_, err = s.DB.Exec(
		fmt.Sprintf(`UPDATE documents SET %s WHERE id = ?`, strings.Join(sets, ", ")),
		args...,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrDocumentExists
		}
		return err
	}
	return nil
}

func (s *Store) DeleteDocument(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM documents WHERE id = ?`, id)
	return err
}

func docCols(withContent bool) string {
	c := "id, repo_id, filename, type, size_bytes, source_path, created_at, updated_at"
	if withContent {
		c += ", content"
	}
	return c
}

func scanDocument(row rowScanner, withContent bool) (*model.Document, error) {
	var (
		d   model.Document
		typ string
		err error
	)
	if withContent {
		err = row.Scan(&d.ID, &d.RepoID, &d.Filename, &typ, &d.SizeBytes, &d.SourcePath, &d.CreatedAt, &d.UpdatedAt, &d.Content)
	} else {
		err = row.Scan(&d.ID, &d.RepoID, &d.Filename, &typ, &d.SizeBytes, &d.SourcePath, &d.CreatedAt, &d.UpdatedAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan document: %w", err)
	}
	d.Type = model.DocumentType(typ)
	return &d, nil
}
