package inputs

// PRAttachInput is the payload for `mk pr attach --json`.
type PRAttachInput struct {
	IssueKey string `json:"issue_key"`
	URL      string `json:"url"`
}

// PRDetachInput is the payload for `mk pr detach --json`.
type PRDetachInput struct {
	IssueKey string `json:"issue_key"`
	URL      string `json:"url"`
}
