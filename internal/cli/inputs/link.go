package inputs

// LinkInput is the payload for `mk link --json`.
type LinkInput struct {
	From string `json:"from"`
	Type string `json:"type"`
	To   string `json:"to"`
}

// UnlinkInput is the payload for `mk unlink --json`.
type UnlinkInput struct {
	A string `json:"a"`
	B string `json:"b"`
}
