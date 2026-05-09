package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mrgeoffrich/mini-kanban/internal/client"
	"github.com/mrgeoffrich/mini-kanban/internal/git"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
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

// remoteURL returns the configured remote URL, falling back to
// MK_REMOTE so callers can switch backends per-shell without retyping
// the flag on every command.
func remoteURL() string {
	if opts.remote != "" {
		return opts.remote
	}
	return os.Getenv("MK_REMOTE")
}

// apiToken returns the bearer token for the remote API, falling back
// to MK_API_TOKEN.
func apiToken() string {
	if opts.token != "" {
		return opts.token
	}
	return os.Getenv("MK_API_TOKEN")
}

// openClient wires up the right backend (local SQLite or remote HTTP).
// Replaces openStore() at every CLI handler call site as Phase 6
// migrates them. Defer c.Close() in the same way you would the store.
func openClient() (client.Client, error) {
	return client.Open(context.Background(), client.Options{
		DBPath: opts.dbPath,
		Remote: remoteURL(),
		Token:  apiToken(),
		Actor:  actor(),
	})
}

// inRemoteMode reports whether the CLI is configured to talk to a
// remote `mk api` server. Used by local-only verbs to short-circuit
// with a clear error.
func inRemoteMode() bool { return remoteURL() != "" }

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
