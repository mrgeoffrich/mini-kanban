package store

import (
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

// Generic per-repo KV used by the TUI. Keep keys namespaced (e.g.
// "board.hidden_states") so future toggles can share this table.

func (s *Store) GetTUISetting(repoID int64, key string) (string, error) {
	var v string
	err := s.DB.QueryRow(
		`SELECT value FROM tui_settings WHERE repo_id = ? AND key = ?`,
		repoID, key,
	).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetTUISetting(repoID int64, key, value string) error {
	_, err := s.DB.Exec(
		`INSERT INTO tui_settings (repo_id, key, value, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(repo_id, key) DO UPDATE SET
		   value      = excluded.value,
		   updated_at = CURRENT_TIMESTAMP`,
		repoID, key, value,
	)
	return err
}

const boardHiddenKey = "board.hidden_states"

// LoadHiddenStates returns the set of board states the user has hidden in
// this repo. Unknown state names are silently dropped so a future state
// rename doesn't break old saved settings.
func (s *Store) LoadHiddenStates(repoID int64) (map[model.State]bool, error) {
	raw, err := s.GetTUISetting(repoID, boardHiddenKey)
	if err != nil {
		return nil, err
	}
	out := map[model.State]bool{}
	if raw == "" {
		return out, nil
	}
	valid := map[model.State]bool{}
	for _, st := range model.AllStates() {
		valid[st] = true
	}
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		st := model.State(name)
		if valid[st] {
			out[st] = true
		}
	}
	return out, nil
}

// SaveHiddenStates persists the hidden set as a comma-separated list of
// canonical state names, sorted for stable storage.
func (s *Store) SaveHiddenStates(repoID int64, hidden map[model.State]bool) error {
	names := make([]string, 0, len(hidden))
	for st, on := range hidden {
		if on {
			names = append(names, string(st))
		}
	}
	sort.Strings(names)
	return s.SetTUISetting(repoID, boardHiddenKey, strings.Join(names, ","))
}
