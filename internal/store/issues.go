package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"mini-kanban/internal/model"
)

var issueKeyRe = regexp.MustCompile(`^([A-Za-z0-9]{4})-(\d+)$`)

// ParseIssueKey splits "MINI-42" into prefix and number.
func ParseIssueKey(key string) (prefix string, number int64, err error) {
	m := issueKeyRe.FindStringSubmatch(strings.TrimSpace(key))
	if m == nil {
		return "", 0, fmt.Errorf("invalid issue key %q (expected like MINI-42)", key)
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return "", 0, err
	}
	return strings.ToUpper(m[1]), n, nil
}

func (s *Store) CreateIssue(repoID int64, featureID *int64, title, description string, state model.State) (*model.Issue, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	num, err := s.AllocateIssueNumber(tx, repoID)
	if err != nil {
		return nil, err
	}
	res, err := tx.Exec(
		`INSERT INTO issues (repo_id, number, feature_id, title, description, state) VALUES (?, ?, ?, ?, ?, ?)`,
		repoID, num, nullableInt(featureID), title, description, string(state),
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetIssueByID(id)
}

func (s *Store) GetIssueByID(id int64) (*model.Issue, error) {
	return scanIssue(s.DB.QueryRow(issueSelect+` WHERE i.id = ?`, id))
}

func (s *Store) GetIssueByKey(prefix string, number int64) (*model.Issue, error) {
	return scanIssue(s.DB.QueryRow(issueSelect+` WHERE r.prefix = ? AND i.number = ?`, prefix, number))
}

type IssueFilter struct {
	RepoID    *int64
	States    []model.State
	FeatureID *int64
	AllRepos  bool
}

func (s *Store) ListIssues(f IssueFilter) ([]*model.Issue, error) {
	var (
		where []string
		args  []any
	)
	if !f.AllRepos && f.RepoID != nil {
		where = append(where, "i.repo_id = ?")
		args = append(args, *f.RepoID)
	}
	if f.FeatureID != nil {
		where = append(where, "i.feature_id = ?")
		args = append(args, *f.FeatureID)
	}
	if len(f.States) > 0 {
		ph := make([]string, len(f.States))
		for i, st := range f.States {
			ph[i] = "?"
			args = append(args, string(st))
		}
		where = append(where, "i.state IN ("+strings.Join(ph, ",")+")")
	}
	q := issueSelect
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY r.prefix, i.number"
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Issue
	for rows.Next() {
		iss, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, iss)
	}
	return out, rows.Err()
}

func (s *Store) UpdateIssue(id int64, title, description *string, featureID **int64) error {
	sets := []string{}
	args := []any{}
	if title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *title)
	}
	if description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *description)
	}
	if featureID != nil {
		sets = append(sets, "feature_id = ?")
		args = append(args, nullableInt(*featureID))
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	_, err := s.DB.Exec(fmt.Sprintf(`UPDATE issues SET %s WHERE id = ?`, strings.Join(sets, ", ")), args...)
	return err
}

func (s *Store) SetIssueState(id int64, state model.State) error {
	_, err := s.DB.Exec(`UPDATE issues SET state = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(state), id)
	return err
}

func (s *Store) DeleteIssue(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM issues WHERE id = ?`, id)
	return err
}

const issueSelect = `
SELECT i.id, i.repo_id, i.number, r.prefix, i.feature_id, COALESCE(f.slug, ''),
       i.title, i.description, i.state, i.created_at, i.updated_at
FROM issues i
JOIN repos r ON r.id = i.repo_id
LEFT JOIN features f ON f.id = i.feature_id`

func scanIssue(row rowScanner) (*model.Issue, error) {
	var (
		i         model.Issue
		prefix    string
		featureID sql.NullInt64
		featSlug  string
		state     string
	)
	err := row.Scan(&i.ID, &i.RepoID, &i.Number, &prefix, &featureID, &featSlug,
		&i.Title, &i.Description, &state, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan issue: %w", err)
	}
	i.Key = fmt.Sprintf("%s-%d", prefix, i.Number)
	i.State = model.State(state)
	if featureID.Valid {
		v := featureID.Int64
		i.FeatureID = &v
		i.FeatureSlug = featSlug
	}
	return &i, nil
}

func nullableInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
