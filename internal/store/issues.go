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

// CreateIssue is fully atomic: the counter peek, INSERT, tag writes, and
// counter bump all live in a single transaction. We deliberately bump the
// counter LAST (right before Commit) so that any failure in the issue or
// tag inserts means the counter never even gets touched on disk — that
// way a phantom-number gap requires a Commit to actually succeed, which
// only happens when every preceding step succeeded.
func (s *Store) CreateIssue(repoID int64, featureID *int64, title, description string, state model.State, tags []string) (*model.Issue, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var num int64
	if err := tx.QueryRow(`SELECT next_issue_number FROM repos WHERE id = ?`, repoID).Scan(&num); err != nil {
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
	if err := s.addTagsTx(tx, id, tags); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`UPDATE repos SET next_issue_number = next_issue_number + 1 WHERE id = ?`, repoID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetIssueByID(id)
}

func (s *Store) GetIssueByID(id int64) (*model.Issue, error) {
	iss, err := scanIssue(s.DB.QueryRow(issueSelect+` WHERE i.id = ?`, id))
	if err != nil {
		return nil, err
	}
	return s.attachTags(iss)
}

func (s *Store) GetIssueByKey(prefix string, number int64) (*model.Issue, error) {
	iss, err := scanIssue(s.DB.QueryRow(issueSelect+` WHERE r.prefix = ? AND i.number = ?`, prefix, number))
	if err != nil {
		return nil, err
	}
	return s.attachTags(iss)
}

func (s *Store) attachTags(iss *model.Issue) (*model.Issue, error) {
	tagMap, err := s.loadTagsForIssues([]int64{iss.ID})
	if err != nil {
		return nil, err
	}
	iss.Tags = tagMap[iss.ID]
	if iss.Tags == nil {
		iss.Tags = []string{}
	}
	return iss, nil
}

type IssueFilter struct {
	RepoID    *int64
	States    []model.State
	FeatureID *int64
	Tags      []string // AND semantics: issue must have ALL of these tags
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
	if len(f.Tags) > 0 {
		ph := make([]string, len(f.Tags))
		for i, t := range f.Tags {
			ph[i] = "?"
			args = append(args, t)
		}
		// All requested tags must be present on the issue (AND semantics).
		where = append(where, fmt.Sprintf(
			`i.id IN (SELECT issue_id FROM issue_tags WHERE tag IN (%s) GROUP BY issue_id HAVING COUNT(DISTINCT tag) = %d)`,
			strings.Join(ph, ","), len(f.Tags),
		))
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
	var ids []int64
	for rows.Next() {
		iss, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, iss)
		ids = append(ids, iss.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tagMap, err := s.loadTagsForIssues(ids)
	if err != nil {
		return nil, err
	}
	for _, iss := range out {
		iss.Tags = tagMap[iss.ID]
		if iss.Tags == nil {
			iss.Tags = []string{}
		}
	}
	return out, nil
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

// IssueStateCounts returns a map of state → count for issues in a repo, or
// across every repo if repoID is nil.
func (s *Store) IssueStateCounts(repoID *int64) (map[string]int, error) {
	q := `SELECT state, COUNT(*) FROM issues`
	args := []any{}
	if repoID != nil {
		q += ` WHERE repo_id = ?`
		args = append(args, *repoID)
	}
	q += ` GROUP BY state`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[state] = n
	}
	return out, rows.Err()
}

// CountFeatures returns the feature count for a repo.
func (s *Store) CountFeatures(repoID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM features WHERE repo_id = ?`, repoID).Scan(&n)
	return n, err
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
