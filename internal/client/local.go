package client

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mrgeoffrich/mini-kanban/internal/git"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

// localClient is the in-process backend wrapping a *store.Store. It
// owns the store's lifecycle (Open in newLocalClient, Close on Close).
// Audit-log writes happen here, mirroring what cli handlers used to do
// inline.
type localClient struct {
	store *store.Store
	actor string
}

func newLocalClient(opts Options) (*localClient, error) {
	path := opts.DBPath
	if path == "" {
		p, err := store.DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	s, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	return &localClient{store: s, actor: opts.Actor}, nil
}

func (c *localClient) Mode() string  { return ModeLocal }
func (c *localClient) Close() error  { return c.store.Close() }

// Store exposes the underlying *store.Store. Used by CLI verbs that
// keep some local-only computation (e.g. mk status's filesystem-aware
// stats). Callers in remote mode must NOT hit this — the caller is
// responsible for branching on Mode() first.
func (c *localClient) Store() *store.Store { return c.store }

// recordOp writes an audit-log entry. Failures are logged to stderr
// but never fail the user-visible command — losing one history row is
// preferable to rolling back the work the user just asked for.
func (c *localClient) recordOp(e model.HistoryEntry) {
	if e.Actor == "" {
		e.Actor = c.actor
	}
	if e.Actor == "" {
		e.Actor = "unknown"
	}
	if err := c.store.RecordHistory(e); err != nil {
		fmt.Fprintln(os.Stderr, "mk: warning: failed to record history:", err)
	}
}

// updatedFieldList mirrors internal/cli/audit.go:updatedFieldList. Same
// audit-log Details text on both backends.
func updatedFieldList(fields map[string]bool) string {
	var parts []string
	for name, touched := range fields {
		if touched {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "updated " + joinCSV(parts)
}

func joinCSV(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "," + p
	}
	return out
}

// EnsureRepo replicates the CLI's resolveRepo() behaviour: look up by
// path, auto-create on miss, write the repo.create audit row.
func (c *localClient) EnsureRepo(ctx context.Context, info *git.Info) (*model.Repo, bool, error) {
	repo, err := c.store.GetRepoByPath(info.Root)
	if err == nil {
		return repo, false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, false, err
	}
	prefix, err := c.store.AllocatePrefix(info.Name)
	if err != nil {
		return nil, false, fmt.Errorf("allocate prefix: %w", err)
	}
	created, err := c.store.CreateRepo(prefix, info.Name, info.Root, info.RemoteURL)
	if err != nil {
		return nil, false, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &created.ID, RepoPrefix: created.Prefix,
		Op: "repo.create", Kind: "repo",
		TargetID: &created.ID, TargetLabel: created.Prefix,
		Details: "auto-registered (" + created.Name + ")",
	})
	return created, true, nil
}

func (c *localClient) ListHistory(ctx context.Context, repo *model.Repo, f store.HistoryFilter) ([]*model.HistoryEntry, error) {
	if repo != nil {
		f.RepoID = &repo.ID
	}
	rows, err := c.store.ListHistory(f)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*model.HistoryEntry{}
	}
	return rows, nil
}
