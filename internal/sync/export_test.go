package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

// fakeStore implements StoreReader with hard-coded data. We deliberately
// don't reach for the real *store.Store here to keep the test self
// contained — Phase 2's correctness rule is "two exports of the same
// data produce identical bytes", which is exercised perfectly well
// against an in-memory fake.
type fakeStore struct {
	repos        []*model.Repo
	features     map[int64][]*model.Feature
	issues       map[int64][]*model.Issue
	allIssues    []*model.Issue
	comments     map[int64][]*model.Comment
	relations    map[int64]*IssueRelations
	prs          map[int64][]*model.PullRequest
	documents    map[int64][]*model.Document
	docsByID     map[int64]*model.Document
	docLinks     map[int64][]*model.DocumentLink
}

func (f *fakeStore) ListRepos() ([]*model.Repo, error) { return f.repos, nil }

func (f *fakeStore) ListFeatures(repoID int64, _ bool) ([]*model.Feature, error) {
	return f.features[repoID], nil
}

func (f *fakeStore) ListIssues(filter IssueFilterArg) ([]*model.Issue, error) {
	if filter.AllRepos {
		return f.allIssues, nil
	}
	if filter.RepoID != nil {
		out := append([]*model.Issue(nil), f.issues[*filter.RepoID]...)
		if !filter.IncludeDescription {
			for i := range out {
				clone := *out[i]
				clone.Description = ""
				out[i] = &clone
			}
		}
		return out, nil
	}
	return nil, nil
}

func (f *fakeStore) ListComments(issueID int64) ([]*model.Comment, error) {
	return f.comments[issueID], nil
}

func (f *fakeStore) ListIssueRelations(issueID int64) (*IssueRelations, error) {
	if r := f.relations[issueID]; r != nil {
		return r, nil
	}
	return &IssueRelations{}, nil
}

func (f *fakeStore) ListPRs(issueID int64) ([]*model.PullRequest, error) {
	return f.prs[issueID], nil
}

func (f *fakeStore) ListDocuments(filter DocumentFilterArg) ([]*model.Document, error) {
	return f.documents[filter.RepoID], nil
}

func (f *fakeStore) ListDocumentLinks(docID int64) ([]*model.DocumentLink, error) {
	return f.docLinks[docID], nil
}

func (f *fakeStore) GetDocumentByID(id int64, _ bool) (*model.Document, error) {
	return f.docsByID[id], nil
}

// makeFakeStore builds a small but realistic dataset that exercises:
//   - one repo with a feature
//   - two issues (one tied to the feature, one orphan)
//   - relations (blocks, relates_to)
//   - tags
//   - comments
//   - a PR
//   - a document linked to one of the issues
func makeFakeStore() *fakeStore {
	repo := &model.Repo{
		ID:              1,
		UUID:            "0190c2a3-7f3a-7b2c-8b21-aaaaaaaaaaaa",
		Prefix:          "MINI",
		Name:            "mini-kanban",
		Path:            "/local/path",
		RemoteURL:       "git@github.com:user/mini-kanban.git",
		NextIssueNumber: 3,
		CreatedAt:       time.Date(2025, 11, 1, 9, 0, 0, 0, time.UTC),
	}

	feat := &model.Feature{
		ID:          10,
		UUID:        "0190c2a3-7f3a-7b2c-8b21-dddddddddddd",
		RepoID:      1,
		Slug:        "auth-rewrite",
		Title:       "Rewrite auth",
		Description: "We need to rewrite the auth layer.\n",
		CreatedAt:   time.Date(2025, 11, 2, 9, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2025, 11, 2, 9, 0, 0, 0, time.UTC),
	}

	featID := feat.ID
	iss1 := &model.Issue{
		ID:          100,
		UUID:        "0190c2a3-7f3a-7b2c-8b21-bbbbbbbbbbbb",
		RepoID:      1,
		Number:      1,
		Key:         "MINI-1",
		FeatureID:   &featID,
		FeatureSlug: "auth-rewrite",
		Title:       "Add auth middleware 🔐",
		Description: "First the middleware, then the rest.\n",
		State:       model.StateInProgress,
		Assignee:    "geoff",
		Tags:        []string{"p1", "security"},
		CreatedAt:   time.Date(2026, 5, 1, 10, 14, 22, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 5, 9, 14, 22, 0, 0, time.UTC),
	}
	iss2 := &model.Issue{
		ID:          101,
		UUID:        "0190c2a3-7f3a-7b2c-8b21-cccccccccccc",
		RepoID:      1,
		Number:      2,
		Key:         "MINI-2",
		Title:       `Refactor "config" loader`, // exercises double-quote escaping
		Description: "",
		State:       model.StateBacklog,
		Assignee:    "", // edge case: empty assignee
		Tags:        []string{},
		CreatedAt:   time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC),
	}

	// Relations: iss1 blocks iss2.
	relsForIss1 := &IssueRelations{
		Outgoing: []model.Relation{
			{ID: 1000, FromIssue: "MINI-1", ToIssue: "MINI-2", Type: model.RelBlocks},
		},
	}
	// iss2 has incoming "blocked by" iss1; sync only emits outgoing.
	relsForIss2 := &IssueRelations{
		Incoming: []model.Relation{
			{ID: 1000, FromIssue: "MINI-1", ToIssue: "MINI-2", Type: model.RelBlocks},
		},
	}

	pr := &model.PullRequest{ID: 5000, IssueID: 100, URL: "https://github.com/x/y/pull/42"}

	c1 := &model.Comment{
		ID:        2000,
		UUID:      "0190c2a3-7f3a-7b2c-8b21-ffffffffffff",
		IssueID:   100,
		Author:    "geoff",
		Body:      "Looks good to me.\n",
		CreatedAt: time.Date(2026, 5, 9, 14, 22, 0, 0, time.UTC),
	}

	doc := &model.Document{
		ID:         3000,
		UUID:       "0190c2a3-7f3a-7b2c-8b21-eeeeeeeeeeee",
		RepoID:     1,
		Filename:   "auth-overview.md",
		Type:       model.DocTypeArchitecture,
		Content:    "# Auth overview\n\nNotes go here.\n",
		SizeBytes:  35,
		SourcePath: "docs/auth-overview.md",
		CreatedAt:  time.Date(2026, 4, 20, 11, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC),
	}

	issID := iss1.ID
	link := &model.DocumentLink{
		ID:               4000,
		DocumentID:       3000,
		DocumentFilename: "auth-overview.md",
		DocumentType:     model.DocTypeArchitecture,
		IssueID:          &issID,
		IssueKey:         "MINI-1",
		Description:      "design context",
		CreatedAt:        time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}

	return &fakeStore{
		repos:    []*model.Repo{repo},
		features: map[int64][]*model.Feature{1: {feat}},
		issues:   map[int64][]*model.Issue{1: {iss1, iss2}},
		allIssues: []*model.Issue{
			// the AllRepos list strips descriptions; the export pipeline
			// only uses it for key→uuid lookups so descriptions don't
			// matter here.
			{ID: 100, UUID: iss1.UUID, RepoID: 1, Number: 1, Key: "MINI-1"},
			{ID: 101, UUID: iss2.UUID, RepoID: 1, Number: 2, Key: "MINI-2"},
		},
		comments: map[int64][]*model.Comment{
			100: {c1},
		},
		relations: map[int64]*IssueRelations{
			100: relsForIss1,
			101: relsForIss2,
		},
		prs:       map[int64][]*model.PullRequest{100: {pr}},
		documents: map[int64][]*model.Document{1: {doc}},
		docsByID:  map[int64]*model.Document{3000: doc},
		docLinks:  map[int64][]*model.DocumentLink{3000: {link}},
	}
}

// TestExport_ByteIdenticalAcrossRuns is the headline Phase 2 invariant:
// two exports of the same DB produce byte-identical filesystem trees.
// Hash-stability of every individual YAML file is what makes this work,
// but the test asserts the union (every file path + every file body).
func TestExport_ByteIdenticalAcrossRuns(t *testing.T) {
	fs := makeFakeStore()
	eng := &Engine{Store: fs}

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	r1, err := eng.Export(context.Background(), dir1)
	if err != nil {
		t.Fatalf("first export: %v", err)
	}
	r2, err := eng.Export(context.Background(), dir2)
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if r1.Files != r2.Files {
		t.Fatalf("file counts differ: %d vs %d", r1.Files, r2.Files)
	}
	if r1.Files == 0 {
		t.Fatal("export produced zero files")
	}

	tree1 := readTree(t, dir1)
	tree2 := readTree(t, dir2)
	if len(tree1) != len(tree2) {
		t.Fatalf("file count mismatch: %d vs %d\nleft: %v\nright: %v",
			len(tree1), len(tree2), keys(tree1), keys(tree2))
	}
	for path, body1 := range tree1 {
		body2, ok := tree2[path]
		if !ok {
			t.Errorf("missing in second export: %s", path)
			continue
		}
		if string(body1) != string(body2) {
			t.Errorf("body differs for %s:\nleft:  %q\nright: %q", path, body1, body2)
		}
	}
}

// TestExport_ProducesExpectedTopLevelLayout asserts the directory shape
// matches the design doc — one file per record, named correctly.
func TestExport_ProducesExpectedTopLevelLayout(t *testing.T) {
	fs := makeFakeStore()
	eng := &Engine{Store: fs}
	dir := t.TempDir()
	if _, err := eng.Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	must := []string{
		"repos/MINI/repo.yaml",
		"repos/MINI/features/auth-rewrite/feature.yaml",
		"repos/MINI/features/auth-rewrite/description.md",
		"repos/MINI/issues/MINI-1/issue.yaml",
		"repos/MINI/issues/MINI-1/description.md",
		"repos/MINI/issues/MINI-2/issue.yaml",
		"repos/MINI/issues/MINI-2/description.md",
		"repos/MINI/docs/auth-overview.md/doc.yaml",
		"repos/MINI/docs/auth-overview.md/content.md",
	}
	for _, p := range must {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	// And one comment file should exist with the expected timestamp+uuid pattern.
	commentDir := filepath.Join(dir, "repos", "MINI", "issues", "MINI-1", "comments")
	entries, err := os.ReadDir(commentDir)
	if err != nil {
		t.Fatalf("read comments dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 comment files (.yaml + .md), got %d", len(entries))
	}
}

// TestExport_EmptyDB exercises the no-data path: an empty store must
// produce an empty target dir (or at least no files), not an error.
func TestExport_EmptyDB(t *testing.T) {
	fs := &fakeStore{}
	eng := &Engine{Store: fs}
	dir := t.TempDir()
	r, err := eng.Export(context.Background(), dir)
	if err != nil {
		t.Fatalf("export empty: %v", err)
	}
	if r.Files != 0 {
		t.Fatalf("expected 0 files, got %d", r.Files)
	}
	if r.Repos != 0 {
		t.Fatalf("expected 0 repos, got %d", r.Repos)
	}
}

// TestExport_DryRunWritesNothing checks that DryRun=true skips disk
// writes but still walks every record so the count is meaningful.
func TestExport_DryRunWritesNothing(t *testing.T) {
	fs := makeFakeStore()
	eng := &Engine{Store: fs, DryRun: true}
	dir := t.TempDir()
	r, err := eng.Export(context.Background(), dir)
	if err != nil {
		t.Fatalf("dry-run export: %v", err)
	}
	if r.Files == 0 {
		t.Fatal("dry-run still reports zero files; should count what it would write")
	}
	// No file should have actually been created.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("dry-run wrote %d entries to %s; expected none", len(entries), dir)
	}
}

// TestExport_IssueYAMLContent spot-checks one record's YAML contents
// against the expected schema, including the "always quoted strings"
// rule and the relations + tags shape.
func TestExport_IssueYAMLContent(t *testing.T) {
	fs := makeFakeStore()
	eng := &Engine{Store: fs}
	dir := t.TempDir()
	if _, err := eng.Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "repos/MINI/issues/MINI-1/issue.yaml"))
	if err != nil {
		t.Fatalf("read issue.yaml: %v", err)
	}
	body := string(got)
	checks := []string{
		// quoted user strings
		`assignee: "geoff"`,
		`state: "in_progress"`,
		`title: "Add auth middleware 🔐"`,
		// uuid present
		`uuid: "0190c2a3-7f3a-7b2c-8b21-bbbbbbbbbbbb"`,
		// number is unquoted int
		"number: 1",
		// timestamps quoted with ms precision
		`created_at: "2026-05-01T10:14:22.000Z"`,
		`updated_at: "2026-05-09T14:22:00.000Z"`,
		// feature reference
		`label: "auth-rewrite"`,
		`uuid: "0190c2a3-7f3a-7b2c-8b21-dddddddddddd"`,
		// tags sorted alphabetically
		`- "p1"`,
		`- "security"`,
		// relations -> blocks contains MINI-2
		`label: "MINI-2"`,
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	// And: must NOT contain CRLF.
	if strings.Contains(body, "\r") {
		t.Errorf("CR found in issue.yaml")
	}
}

// TestExport_DescriptionHashMatchesContent: the description_hash field
// in issue.yaml must be the sha256 of description.md's bytes.
func TestExport_DescriptionHashMatchesContent(t *testing.T) {
	fs := makeFakeStore()
	eng := &Engine{Store: fs}
	dir := t.TempDir()
	if _, err := eng.Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	desc, err := os.ReadFile(filepath.Join(dir, "repos/MINI/issues/MINI-1/description.md"))
	if err != nil {
		t.Fatalf("read description.md: %v", err)
	}
	wantHash := ContentHash(desc)
	yamlBytes, err := os.ReadFile(filepath.Join(dir, "repos/MINI/issues/MINI-1/issue.yaml"))
	if err != nil {
		t.Fatalf("read issue.yaml: %v", err)
	}
	if !strings.Contains(string(yamlBytes), `description_hash: "`+wantHash+`"`) {
		t.Errorf("description_hash missing or wrong; want %q\nyaml:\n%s", wantHash, yamlBytes)
	}
}

func readTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = body
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
