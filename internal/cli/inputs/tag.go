package inputs

// TagAddInput is the payload for `mk tag add --json`.
type TagAddInput struct {
	IssueKey string   `json:"issue_key"`
	Tags     []string `json:"tags"`
}

// TagRmInput is the payload for `mk tag rm --json`.
type TagRmInput struct {
	IssueKey string   `json:"issue_key"`
	Tags     []string `json:"tags"`
}
