package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/mrgeoffrich/mini-kanban/internal/api"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

func seedDocument(t *testing.T, s *store.Store, repo *model.Repo, filename string, dt model.DocumentType, content string) *model.Document {
	t.Helper()
	d, err := s.CreateDocument(repo.ID, filename, dt, content, "")
	if err != nil {
		t.Fatalf("seed document %q: %v", filename, err)
	}
	return d
}

func TestDocumentsListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var docs []*model.Document
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected empty, got %d", len(docs))
	}
	// Empty array, not null:
	if string(raw) == "null\n" || string(raw) == "null" {
		t.Fatalf("expected []; got null")
	}
}

func TestDocumentsListLeanByDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "a.md", model.DocTypeArchitecture, "ALPHA")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var docs []*model.Document
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len: %d", len(docs))
	}
	if docs[0].Content != "" {
		t.Fatalf("expected lean (no content), got %q", docs[0].Content)
	}
	if docs[0].SizeBytes != int64(len("ALPHA")) {
		t.Fatalf("size_bytes: %d", docs[0].SizeBytes)
	}
}

func TestDocumentsListWithContent(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "a.md", model.DocTypeArchitecture, "ALPHA")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents?with_content=true")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var docs []*model.Document
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(docs) != 1 || docs[0].Content != "ALPHA" {
		t.Fatalf("got: %+v", docs)
	}
}

func TestDocumentsListTypeFilter(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "a.md", model.DocTypeArchitecture, "X")
	seedDocument(t, s, repo, "b.md", model.DocTypeDesigns, "Y")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents?type=architecture")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var docs []*model.Document
	_ = json.Unmarshal(raw, &docs)
	if len(docs) != 1 || docs[0].Filename != "a.md" {
		t.Fatalf("got: %+v", docs)
	}
}

func TestDocumentsListUnknownType(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents?type=nonsense")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentsListRepoNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/repos/NOPE/documents")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentShowHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	doc := seedDocument(t, s, repo, "design.md", model.DocTypeDesigns, "BODY")
	iss := seedIssue(t, s, repo, "an issue")
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &iss.ID}, "important context"); err != nil {
		t.Fatalf("link: %v", err)
	}

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/design.md")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var view struct {
		Document *model.Document         `json:"document"`
		Links    []*model.DocumentLink   `json:"links"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Document.Content != "BODY" {
		t.Fatalf("content: %q", view.Document.Content)
	}
	if len(view.Links) != 1 || view.Links[0].IssueKey == "" {
		t.Fatalf("links: %+v", view.Links)
	}
}

func TestDocumentShowSkipBody(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocument(t, s, repo, "design.md", model.DocTypeDesigns, "BODY")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/design.md?with_content=false")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var view struct {
		Document *model.Document `json:"document"`
	}
	_ = json.Unmarshal(raw, &view)
	if view.Document.Content != "" {
		t.Fatalf("expected empty content, got %q", view.Document.Content)
	}
	if view.Document.SizeBytes != int64(len("BODY")) {
		t.Fatalf("size_bytes: %d", view.Document.SizeBytes)
	}
}

func TestDocumentShowMissing(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/missing.md")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDocumentShowCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	seedDocument(t, s, repo2, "in-other.md", model.DocTypeDesigns, "X")

	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/documents/in-other.md")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// quietRead drains a response body so net/http can reuse the connection.
func quietRead(r io.Reader) { _, _ = io.Copy(io.Discard, r) }

func decodeNoBody(resp *http.Response) {
	quietRead(resp.Body)
	resp.Body.Close()
}
