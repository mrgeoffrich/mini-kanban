package sync

import "github.com/mrgeoffrich/mini-kanban/internal/model"

// Engine is the top-level coordinator for the sync layer. Phase 2 only
// uses Export — Import / Run / Verify land in later phases. The struct
// is deliberately named "Engine" rather than "Exporter" so the later
// phases don't have to break the public type.
//
// Actor is recorded against any audit op the sync engine writes (none
// in Phase 2, but the field is here so callers don't have to plumb
// it in twice).
//
// DryRun is honoured (where it makes sense) by every method on Engine.
// Phase 2's Export honours it by short-circuiting before any
// filesystem write.
type Engine struct {
	Store  StoreReader
	Actor  string
	DryRun bool
}

// StoreReader is the read-only slice of *store.Store that the sync
// engine needs. We name it as an interface here (rather than importing
// *store.Store directly) to break what would otherwise be an import
// cycle — store already depends on this package for the UUIDv7 helper
// from Phase 1, so sync can't depend on store at the package level.
//
// Callers in the cli package satisfy this interface via a thin
// adapter (see internal/cli/sync.go's storeAdapter), which converts
// store.IssueFilter / store.IssueRelations into the equivalent
// sync-side shapes. Tests can pass an in-memory fake.
type StoreReader interface {
	ListRepos() ([]*model.Repo, error)
	ListFeatures(repoID int64, includeDescription bool) ([]*model.Feature, error)
	ListIssues(filter IssueFilterArg) ([]*model.Issue, error)
	ListComments(issueID int64) ([]*model.Comment, error)
	ListIssueRelations(issueID int64) (*IssueRelations, error)
	ListPRs(issueID int64) ([]*model.PullRequest, error)
	ListDocuments(filter DocumentFilterArg) ([]*model.Document, error)
	ListDocumentLinks(documentID int64) ([]*model.DocumentLink, error)
	GetDocumentByID(id int64, withContent bool) (*model.Document, error)
}

// IssueFilterArg mirrors store.IssueFilter for the fields the sync
// engine actually uses. Field names match the store's so a tiny
// adapter at the cli boundary can copy fields by name.
type IssueFilterArg struct {
	RepoID             *int64
	AllRepos           bool
	IncludeDescription bool
}

// DocumentFilterArg mirrors store.DocumentFilter.
type DocumentFilterArg struct {
	RepoID int64
}

// IssueRelations mirrors store.IssueRelations. The relation slice
// itself is on model.Relation (already in the model package), so only
// the wrapper struct needs duplicating here.
type IssueRelations struct {
	Outgoing []model.Relation
	Incoming []model.Relation
}
