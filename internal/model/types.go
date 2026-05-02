package model

import "time"

type Repo struct {
	ID              int64     `json:"id"`
	Prefix          string    `json:"prefix"`
	Name            string    `json:"name"`
	Path            string    `json:"path"`
	RemoteURL       string    `json:"remote_url,omitempty"`
	NextIssueNumber int64     `json:"next_issue_number"`
	CreatedAt       time.Time `json:"created_at"`
}

type Feature struct {
	ID          int64     `json:"id"`
	RepoID      int64     `json:"repo_id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Issue struct {
	ID          int64     `json:"id"`
	RepoID      int64     `json:"repo_id"`
	Number      int64     `json:"number"`
	Key         string    `json:"key"` // e.g. "MINI-42"
	FeatureID   *int64    `json:"feature_id,omitempty"`
	FeatureSlug string    `json:"feature_slug,omitempty"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	State       State     `json:"state"`
	Tags        []string  `json:"tags"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Comment struct {
	ID        int64     `json:"id"`
	IssueID   int64     `json:"issue_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type RelationType string

const (
	RelBlocks      RelationType = "blocks"
	RelRelatesTo   RelationType = "relates_to"
	RelDuplicateOf RelationType = "duplicate_of"
)

type Relation struct {
	ID         int64        `json:"id"`
	FromIssue  string       `json:"from_issue"` // key form
	ToIssue    string       `json:"to_issue"`
	Type       RelationType `json:"type"`
	CreatedAt  time.Time    `json:"created_at"`
}

type PullRequest struct {
	ID        int64     `json:"id"`
	IssueID   int64     `json:"issue_id"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

type Attachment struct {
	ID        int64     `json:"id"`
	IssueID   *int64    `json:"issue_id,omitempty"`
	FeatureID *int64    `json:"feature_id,omitempty"`
	Filename  string    `json:"filename"`
	SizeBytes int64     `json:"size_bytes"`
	Content   string    `json:"content,omitempty"` // populated only on show
	CreatedAt time.Time `json:"created_at"`
}
