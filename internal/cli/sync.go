package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
	"github.com/mrgeoffrich/mini-kanban/internal/sync"
)

// newSyncCmd registers the `mk sync` command group. Phase 2 only ships
// `mk sync export <path>` and the parent group itself is hidden — this
// is a developer/debugging surface; the user-facing `mk sync` (full
// pull/import/export/commit/push) lands in Phase 4 and that's when
// we'll un-hide it and document it in SKILL.md.
func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "sync",
		Short:  "Git-backed sync (developer preview)",
		Hidden: true,
	}
	cmd.AddCommand(newSyncExportCmd())
	return cmd
}

func newSyncExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "export <path>",
		Short:  "Export the local DB to a folder as YAML + markdown",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("mk sync export: not supported in remote mode (operates on the local DB only)")
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			eng := &sync.Engine{
				Store:  storeAdapter{s: s},
				Actor:  actor(),
				DryRun: opts.dryRun,
			}
			res, err := eng.Export(context.Background(), args[0])
			if err != nil {
				return err
			}
			out := exportResult{ExportResult: res}
			if opts.dryRun {
				return emitDryRun(out)
			}
			return emit(out)
		},
	}
	return cmd
}

// exportResult wraps *sync.ExportResult so we can register a text
// renderer in renderText without leaking sync types into the output
// package's switch. JSON output flows through ExportResult's own
// fields untouched (the embedded pointer's tags do the work).
type exportResult struct {
	*sync.ExportResult
}

// storeAdapter wraps *store.Store to satisfy sync.StoreReader. The
// engine package can't import store directly (the store package
// already depends on internal/sync for the UUIDv7 helper, so a back
// edge would create a cycle), and the type-conversion shims live here
// because this is the only place that owns both ends.
//
// Translations are mechanical: store.IssueFilter ↔ sync.IssueFilterArg,
// store.IssueRelations ↔ sync.IssueRelations, etc. If either side
// grows new fields, this adapter's where they get plumbed.
type storeAdapter struct {
	s *store.Store
}

func (a storeAdapter) ListRepos() ([]*model.Repo, error) { return a.s.ListRepos() }

func (a storeAdapter) ListFeatures(repoID int64, includeDescription bool) ([]*model.Feature, error) {
	return a.s.ListFeatures(repoID, includeDescription)
}

func (a storeAdapter) ListIssues(filter sync.IssueFilterArg) ([]*model.Issue, error) {
	return a.s.ListIssues(store.IssueFilter{
		RepoID:             filter.RepoID,
		AllRepos:           filter.AllRepos,
		IncludeDescription: filter.IncludeDescription,
	})
}

func (a storeAdapter) ListComments(issueID int64) ([]*model.Comment, error) {
	return a.s.ListComments(issueID)
}

func (a storeAdapter) ListIssueRelations(issueID int64) (*sync.IssueRelations, error) {
	r, err := a.s.ListIssueRelations(issueID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return &sync.IssueRelations{}, nil
	}
	return &sync.IssueRelations{Outgoing: r.Outgoing, Incoming: r.Incoming}, nil
}

func (a storeAdapter) ListPRs(issueID int64) ([]*model.PullRequest, error) {
	return a.s.ListPRs(issueID)
}

func (a storeAdapter) ListDocuments(filter sync.DocumentFilterArg) ([]*model.Document, error) {
	return a.s.ListDocuments(store.DocumentFilter{RepoID: filter.RepoID})
}

func (a storeAdapter) ListDocumentLinks(documentID int64) ([]*model.DocumentLink, error) {
	return a.s.ListDocumentLinks(documentID)
}

func (a storeAdapter) GetDocumentByID(id int64, withContent bool) (*model.Document, error) {
	return a.s.GetDocumentByID(id, withContent)
}
