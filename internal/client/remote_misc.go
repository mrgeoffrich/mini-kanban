package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

// ----- Comments -----

func (c *remoteClient) ListComments(ctx context.Context, repo *model.Repo, key string) ([]*model.Comment, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	var out []*model.Comment
	if err := c.do(ctx, http.MethodGet, "/repos/"+prefix+"/issues/"+canonical+"/comments", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.Comment{}
	}
	return out, nil
}

func (c *remoteClient) AddComment(ctx context.Context, repo *model.Repo, in inputs.CommentAddInput, dryRun bool) (*model.Comment, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, in.IssueKey)
	if err != nil {
		return nil, err
	}
	in.IssueKey = canonical
	prefix := strings.SplitN(canonical, "-", 2)[0]
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Comment
	if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/issues/"+canonical+"/comments", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ----- Relations -----

func (c *remoteClient) LinkRelation(ctx context.Context, repo *model.Repo, in inputs.LinkInput, dryRun bool) (*model.Relation, error) {
	from, err := c.ResolveIssueKey(ctx, repo, in.From)
	if err != nil {
		return nil, err
	}
	to, err := c.ResolveIssueKey(ctx, repo, in.To)
	if err != nil {
		return nil, err
	}
	in.From = from
	in.To = to
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Relation
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/relations", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) UnlinkRelation(ctx context.Context, repo *model.Repo, in inputs.UnlinkInput, dryRun bool) (*RelationDeletePreview, int64, error) {
	a, err := c.ResolveIssueKey(ctx, repo, in.A)
	if err != nil {
		return nil, 0, err
	}
	b, err := c.ResolveIssueKey(ctx, repo, in.B)
	if err != nil {
		return nil, 0, err
	}
	in.A = a
	in.B = b
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
		var preview RelationDeletePreview
		if err := c.doBody(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/relations", q, in, &preview); err != nil {
			return nil, 0, err
		}
		return &preview, 0, nil
	}
	var resp struct {
		Removed int64 `json:"removed"`
	}
	if err := c.doBody(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/relations", nil, in, &resp); err != nil {
		return nil, 0, err
	}
	return nil, resp.Removed, nil
}

// doBody is like do() but always sends a body even for DELETE — net/http
// handles that fine but the c.do() helper picks Method and body handling
// based on whether `in` is nil. The relation/tag delete endpoints take
// JSON bodies, so we can't just call c.do with in=nil.
func (c *remoteClient) doBody(ctx context.Context, method, path string, query url.Values, in any, out any) error {
	u := *c.base
	setURLPath(&u, path)
	if query != nil {
		u.RawQuery = query.Encode()
	}
	buf, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.actor != "" {
		req.Header.Set("X-Actor", c.actor)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil || len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		return nil
	}
	herr := &HTTPError{Status: resp.StatusCode}
	if len(raw) > 0 {
		var env errorBody
		if jerr := json.Unmarshal(raw, &env); jerr == nil {
			herr.Code = env.Code
			herr.Message = env.Error
			herr.Details = env.Details
		} else {
			herr.Message = strings.TrimSpace(string(raw))
		}
	}
	return wrapStoreError(herr)
}

// ----- PRs -----

func (c *remoteClient) ListPRs(ctx context.Context, repo *model.Repo, key string) ([]*model.PullRequest, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	var out []*model.PullRequest
	if err := c.do(ctx, http.MethodGet, "/repos/"+prefix+"/issues/"+canonical+"/pull-requests", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.PullRequest{}
	}
	return out, nil
}

func (c *remoteClient) AttachPR(ctx context.Context, repo *model.Repo, key, prURL string, dryRun bool) (*model.PullRequest, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	body := inputs.PRAttachInput{IssueKey: canonical, URL: prURL}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.PullRequest
	if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/issues/"+canonical+"/pull-requests", q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) DetachPR(ctx context.Context, repo *model.Repo, key, prURL string, dryRun bool) (*PRDetachPreview, int64, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, 0, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	body := inputs.PRDetachInput{IssueKey: canonical, URL: prURL}
	if dryRun {
		q := url.Values{}
		q.Set("dry_run", "true")
		var preview PRDetachPreview
		if err := c.doBody(ctx, http.MethodDelete, "/repos/"+prefix+"/issues/"+canonical+"/pull-requests", q, body, &preview); err != nil {
			return nil, 0, err
		}
		return &preview, 0, nil
	}
	if err := c.doBody(ctx, http.MethodDelete, "/repos/"+prefix+"/issues/"+canonical+"/pull-requests", nil, body, nil); err != nil {
		return nil, 0, err
	}
	return nil, 1, nil
}

// ----- Tags -----

func (c *remoteClient) AddTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error) {
	return c.tagMutate(ctx, repo, key, tags, true, dryRun)
}

func (c *remoteClient) RemoveTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error) {
	return c.tagMutate(ctx, repo, key, tags, false, dryRun)
}

func (c *remoteClient) tagMutate(ctx context.Context, repo *model.Repo, key string, tags []string, add, dryRun bool) (*model.Issue, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Issue
	if add {
		body := inputs.TagAddInput{IssueKey: canonical, Tags: tags}
		if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/issues/"+canonical+"/tags", q, body, &out); err != nil {
			return nil, err
		}
	} else {
		body := inputs.TagRmInput{IssueKey: canonical, Tags: tags}
		if err := c.doBody(ctx, http.MethodDelete, "/repos/"+prefix+"/issues/"+canonical+"/tags", q, body, &out); err != nil {
			return nil, err
		}
	}
	return &out, nil
}
