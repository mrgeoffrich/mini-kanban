package inputs

// RepoCreateInput is the payload for `POST /repos`. There is no CLI
// equivalent — `mk init` infers path from CWD, which has no analogue on
// an HTTP server.
type RepoCreateInput struct {
	Prefix    string `json:"prefix,omitempty"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	RemoteURL string `json:"remote_url,omitempty"`
}
