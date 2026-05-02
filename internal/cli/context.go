package cli

import (
	"errors"
	"fmt"
	"os"

	"mini-kanban/internal/git"
	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

// auto-register a repo on first use. Each call site records its own
// history once we get a repo back.

// openStore opens the configured database.
func openStore() (*store.Store, error) {
	path := opts.dbPath
	if path == "" {
		p, err := store.DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	return store.Open(path)
}

// resolveRepo finds the repo row for the current working directory, creating
// it on first use. Errors out if not inside a git repo.
func resolveRepo(s *store.Store) (*model.Repo, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	info, err := git.Detect(cwd)
	if err != nil {
		return nil, err
	}
	repo, err := s.GetRepoByPath(info.Root)
	if err == nil {
		return repo, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	prefix, err := s.AllocatePrefix(info.Name)
	if err != nil {
		return nil, fmt.Errorf("allocate prefix: %w", err)
	}
	created, err := s.CreateRepo(prefix, info.Name, info.Root, info.RemoteURL)
	if err != nil {
		return nil, err
	}
	recordOp(s, model.HistoryEntry{
		RepoID: &created.ID, RepoPrefix: created.Prefix,
		Op: "repo.create", Kind: "repo",
		TargetID: &created.ID, TargetLabel: created.Prefix,
		Details: "auto-registered (" + created.Name + ")",
	})
	return created, nil
}
