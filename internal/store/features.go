package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/sync"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "feature"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func (s *Store) CreateFeature(repoID int64, slug, title, description string) (*model.Feature, error) {
	slug, err := ValidateSlug(slug)
	if err != nil {
		return nil, err
	}
	title, err = ValidateTitle(title, "title")
	if err != nil {
		return nil, err
	}
	description, err = ValidateBody(description, "description", false)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(
		`INSERT INTO features (uuid, repo_id, slug, title, description) VALUES (?, ?, ?, ?, ?)`,
		sync.New(), repoID, slug, title, description,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetFeatureByID(id)
}

const featureCols = `id, uuid, repo_id, slug, title, description, created_at, updated_at`

func (s *Store) GetFeatureByID(id int64) (*model.Feature, error) {
	return scanFeature(s.DB.QueryRow(`SELECT `+featureCols+` FROM features WHERE id = ?`, id))
}

func (s *Store) GetFeatureBySlug(repoID int64, slug string) (*model.Feature, error) {
	return scanFeature(s.DB.QueryRow(`SELECT `+featureCols+` FROM features WHERE repo_id = ? AND slug = ?`, repoID, slug))
}

// GetFeatureByUUID is the sync-side lookup: import maps the canonical
// uuid in feature.yaml back to a DB row, ignoring slug churn.
func (s *Store) GetFeatureByUUID(uuid string) (*model.Feature, error) {
	return scanFeature(s.DB.QueryRow(`SELECT `+featureCols+` FROM features WHERE uuid = ?`, uuid))
}

// ListFeatures returns every feature in the repo. When includeDescription is
// false the heavy `description` field is stripped post-scan — list contexts
// rarely want full bodies inlined, and dropping them keeps the JSON output
// small enough to fit comfortably into an agent's context window.
func (s *Store) ListFeatures(repoID int64, includeDescription bool) ([]*model.Feature, error) {
	rows, err := s.DB.Query(`SELECT `+featureCols+` FROM features WHERE repo_id = ? ORDER BY created_at`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Feature
	for rows.Next() {
		f, err := scanFeature(rows)
		if err != nil {
			return nil, err
		}
		if !includeDescription {
			f.Description = ""
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) UpdateFeature(id int64, title, description *string) error {
	sets := []string{}
	args := []any{}
	if title != nil {
		clean, err := ValidateTitle(*title, "title")
		if err != nil {
			return err
		}
		sets = append(sets, "title = ?")
		args = append(args, clean)
	}
	if description != nil {
		clean, err := ValidateBody(*description, "description", false)
		if err != nil {
			return err
		}
		sets = append(sets, "description = ?")
		args = append(args, clean)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	_, err := s.DB.Exec(fmt.Sprintf(`UPDATE features SET %s WHERE id = ?`, strings.Join(sets, ", ")), args...)
	return err
}

func (s *Store) DeleteFeature(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM features WHERE id = ?`, id)
	return err
}

func scanFeature(row rowScanner) (*model.Feature, error) {
	var f model.Feature
	err := row.Scan(&f.ID, &f.UUID, &f.RepoID, &f.Slug, &f.Title, &f.Description, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan feature: %w", err)
	}
	return &f, nil
}
