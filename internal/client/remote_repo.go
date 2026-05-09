package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/git"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

func (c *remoteClient) ListRepos(ctx context.Context) ([]*model.Repo, error) {
	var out []*model.Repo
	if err := c.do(ctx, http.MethodGet, "/repos", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.Repo{}
	}
	return out, nil
}

func (c *remoteClient) GetRepoByPrefix(ctx context.Context, prefix string) (*model.Repo, error) {
	var out model.Repo
	path := "/repos/" + strings.ToUpper(prefix)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRepoByPath has no direct REST endpoint; the API addresses repos by
// prefix only. The remote backend lists every repo and filters by path
// on the client side. ErrNotFound when no row matches.
func (c *remoteClient) GetRepoByPath(ctx context.Context, path string) (*model.Repo, error) {
	repos, err := c.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range repos {
		if r.Path == path {
			return r, nil
		}
	}
	return nil, store.ErrNotFound
}

// EnsureRepo for remote: detect locally, then look up by path, falling
// back to POST /repos. The server (not the client) writes the audit
// row when CreateRepo fires, matching the local backend's behaviour.
func (c *remoteClient) EnsureRepo(ctx context.Context, info *git.Info) (*model.Repo, bool, error) {
	repo, err := c.GetRepoByPath(ctx, info.Root)
	if err == nil {
		return repo, false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, false, err
	}
	body := map[string]any{
		"name":       info.Name,
		"path":       info.Root,
		"remote_url": info.RemoteURL,
	}
	var created model.Repo
	if err := c.do(ctx, http.MethodPost, "/repos", nil, body, &created); err != nil {
		return nil, false, fmt.Errorf("create repo: %w", err)
	}
	return &created, true, nil
}
