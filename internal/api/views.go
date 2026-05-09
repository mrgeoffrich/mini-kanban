// Package api transport-only composite types. The CLI keeps its own
// equivalents (in internal/cli/output.go and the *_preview structs in
// the relevant cli/*.go files) for text rendering; both surfaces emit
// the same JSON shape so consumers don't need a per-surface parser.
package api

import (
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

type IssueView struct {
	Issue        *model.Issue          `json:"issue"`
	Comments     []*model.Comment      `json:"comments"`
	Relations    *store.IssueRelations `json:"relations"`
	PullRequests []*model.PullRequest  `json:"pull_requests"`
	Documents    []*model.DocumentLink `json:"documents"`
}

type CascadeCount struct {
	Comments      int `json:"comments"`
	Relations     int `json:"relations"`
	PullRequests  int `json:"pull_requests"`
	DocumentLinks int `json:"document_links"`
	Tags          int `json:"tags"`
}

type IssueDeletePreview struct {
	Issue       *model.Issue `json:"issue"`
	Cascade     CascadeCount `json:"cascade"`
	WouldDelete bool         `json:"would_delete"`
}

type RelationDeletePreview struct {
	A           string `json:"a"`
	B           string `json:"b"`
	WouldRemove int    `json:"would_remove"`
}

type PRDetachPreview struct {
	IssueKey    string `json:"issue_key"`
	URL         string `json:"url"`
	WouldRemove int    `json:"would_remove"`
}
