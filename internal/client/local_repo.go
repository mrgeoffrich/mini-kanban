package client

import (
	"context"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

func (c *localClient) ListRepos(ctx context.Context) ([]*model.Repo, error) {
	repos, err := c.store.ListRepos()
	if err != nil {
		return nil, err
	}
	if repos == nil {
		repos = []*model.Repo{}
	}
	return repos, nil
}

func (c *localClient) GetRepoByPrefix(ctx context.Context, prefix string) (*model.Repo, error) {
	return c.store.GetRepoByPrefix(strings.ToUpper(prefix))
}

func (c *localClient) GetRepoByPath(ctx context.Context, path string) (*model.Repo, error) {
	return c.store.GetRepoByPath(path)
}
