package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

// remoteClient drives a *mk api* server over HTTP. The server stamps
// audit-log rows itself, so this client never calls RecordHistory.
type remoteClient struct {
	httpClient *http.Client
	base       *url.URL
	token      string
	actor      string
}

func newRemoteClient(opts Options) (*remoteClient, error) {
	raw := strings.TrimSpace(opts.Remote)
	if raw == "" {
		return nil, errors.New("remote URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid remote URL %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid remote URL %q: must include scheme and host", raw)
	}
	return &remoteClient{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		base:       u,
		token:      opts.Token,
		actor:      opts.Actor,
	}, nil
}

func (c *remoteClient) Mode() string { return ModeRemote }
func (c *remoteClient) Close() error { return nil }

// errorBody is the API error envelope shape.
type errorBody struct {
	Error   string         `json:"error"`
	Code    string         `json:"code"`
	Details map[string]any `json:"details,omitempty"`
}

// HTTPError wraps a non-2xx response. Status preserves the HTTP code so
// callers can branch on 404 vs 409 etc.
type HTTPError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// IsNotFound reports whether err wraps an HTTP 404. Used by the local
// auto-register flow's remote analogue.
func IsNotFound(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusNotFound
	}
	return false
}

// do issues a request, applies auth + actor headers, and parses the
// envelope into either body (on 2xx) or *HTTPError (otherwise). The
// caller writes the body to a streaming response by passing a non-nil
// rawSink instead of body.
func (c *remoteClient) do(ctx context.Context, method, path string, query url.Values, in any, out any) error {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	if query != nil {
		u.RawQuery = query.Encode()
	}
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
		// Allow assigning the raw bytes directly when out is *[]byte.
		if sink, ok := out.(*[]byte); ok {
			*sink = raw
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

// wrapStoreError keeps callers compatible with errors.Is(err, store.ErrNotFound)
// even when the underlying error is an HTTPError. Returned error wraps the
// HTTPError so callers that branch on HTTP status still can.
func wrapStoreError(he *HTTPError) error {
	if he.Status == http.StatusNotFound {
		return &notFoundError{HTTPError: *he}
	}
	return he
}

type notFoundError struct {
	HTTPError
}

func (e *notFoundError) Unwrap() error { return store.ErrNotFound }

// Stub implementations follow. Real bodies arrive verb-by-verb in
// later commits.

func (c *remoteClient) ResolveIssueKey(ctx context.Context, repo *model.Repo, key string) (string, error) {
	return "", errors.New("not implemented")
}
func (c *remoteClient) ListIssues(ctx context.Context, f IssueFilter) ([]*model.Issue, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) GetIssueByKey(ctx context.Context, repo *model.Repo, key string) (*model.Issue, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) ShowIssue(ctx context.Context, repo *model.Repo, key string) (*IssueView, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) BriefIssue(ctx context.Context, repo *model.Repo, key string, opts BriefOptions) (*IssueBrief, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) CreateIssue(ctx context.Context, repo *model.Repo, in inputs.IssueAddInput, dryRun bool) (*model.Issue, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) UpdateIssue(ctx context.Context, repo *model.Repo, key string, edit IssueEdit, dryRun bool) (*model.Issue, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) SetIssueState(ctx context.Context, repo *model.Repo, key string, state model.State, dryRun bool) (*model.Issue, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) AssignIssue(ctx context.Context, repo *model.Repo, key, assignee string, dryRun bool) (*model.Issue, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) UnassignIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) DeleteIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, *IssueDeletePreview, error) {
	return nil, nil, errors.New("not implemented")
}
func (c *remoteClient) PeekNextIssue(ctx context.Context, repo *model.Repo, slug string) (*model.Issue, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) ClaimNextIssue(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Issue, error) {
	return nil, errors.New("not implemented")
}

func (c *remoteClient) ListComments(ctx context.Context, repo *model.Repo, key string) ([]*model.Comment, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) AddComment(ctx context.Context, repo *model.Repo, in inputs.CommentAddInput, dryRun bool) (*model.Comment, error) {
	return nil, errors.New("not implemented")
}

func (c *remoteClient) LinkRelation(ctx context.Context, repo *model.Repo, in inputs.LinkInput, dryRun bool) (*model.Relation, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) UnlinkRelation(ctx context.Context, repo *model.Repo, in inputs.UnlinkInput, dryRun bool) (*RelationDeletePreview, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (c *remoteClient) ListPRs(ctx context.Context, repo *model.Repo, key string) ([]*model.PullRequest, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) AttachPR(ctx context.Context, repo *model.Repo, key, url string, dryRun bool) (*model.PullRequest, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) DetachPR(ctx context.Context, repo *model.Repo, key, url string, dryRun bool) (*PRDetachPreview, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (c *remoteClient) AddTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) RemoveTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error) {
	return nil, errors.New("not implemented")
}

func (c *remoteClient) ListDocuments(ctx context.Context, repo *model.Repo, typeStr string) ([]*model.Document, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) ShowDocument(ctx context.Context, repo *model.Repo, filename string, withContent bool) (*DocView, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) GetDocumentRaw(ctx context.Context, repo *model.Repo, filename string) (*model.Document, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) DownloadDocument(ctx context.Context, repo *model.Repo, filename string) ([]byte, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) CreateDocument(ctx context.Context, repo *model.Repo, in DocCreateInput, dryRun bool) (*model.Document, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) UpsertDocument(ctx context.Context, repo *model.Repo, in DocCreateInput, dryRun bool) (*model.Document, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) EditDocument(ctx context.Context, repo *model.Repo, filename string, newType *string, newContent *string, dryRun bool) (*model.Document, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) RenameDocument(ctx context.Context, repo *model.Repo, oldName, newName, typeStr string, dryRun bool) (*model.Document, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) DeleteDocument(ctx context.Context, repo *model.Repo, filename string, dryRun bool) (*model.Document, *DocumentDeletePreview, error) {
	return nil, nil, errors.New("not implemented")
}
func (c *remoteClient) LinkDocument(ctx context.Context, repo *model.Repo, in inputs.DocLinkInput, dryRun bool) (*model.DocumentLink, error) {
	return nil, errors.New("not implemented")
}
func (c *remoteClient) UnlinkDocument(ctx context.Context, repo *model.Repo, in inputs.DocUnlinkInput, dryRun bool) (*DocumentUnlinkPreview, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (c *remoteClient) ListHistory(ctx context.Context, f store.HistoryFilter) ([]*model.HistoryEntry, error) {
	return nil, errors.New("not implemented")
}
