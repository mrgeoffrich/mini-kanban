package store

import (
	"database/sql"
	"errors"
	"fmt"

	"mini-kanban/internal/model"
)

var ErrNotFound = errors.New("not found")

func (s *Store) CreateRepo(prefix, name, path, remoteURL string) (*model.Repo, error) {
	res, err := s.DB.Exec(
		`INSERT INTO repos (prefix, name, path, remote_url) VALUES (?, ?, ?, ?)`,
		prefix, name, path, remoteURL,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetRepoByID(id)
}

func (s *Store) GetRepoByID(id int64) (*model.Repo, error) {
	return scanRepo(s.DB.QueryRow(`SELECT id, prefix, name, path, remote_url, next_issue_number, created_at FROM repos WHERE id = ?`, id))
}

func (s *Store) GetRepoByPath(path string) (*model.Repo, error) {
	return scanRepo(s.DB.QueryRow(`SELECT id, prefix, name, path, remote_url, next_issue_number, created_at FROM repos WHERE path = ?`, path))
}

func (s *Store) GetRepoByPrefix(prefix string) (*model.Repo, error) {
	return scanRepo(s.DB.QueryRow(`SELECT id, prefix, name, path, remote_url, next_issue_number, created_at FROM repos WHERE prefix = ?`, prefix))
}

func (s *Store) ListRepos() ([]*model.Repo, error) {
	rows, err := s.DB.Query(`SELECT id, prefix, name, path, remote_url, next_issue_number, created_at FROM repos ORDER BY prefix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRepo(row rowScanner) (*model.Repo, error) {
	var r model.Repo
	err := row.Scan(&r.ID, &r.Prefix, &r.Name, &r.Path, &r.RemoteURL, &r.NextIssueNumber, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan repo: %w", err)
	}
	return &r, nil
}
