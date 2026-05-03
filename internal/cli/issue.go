package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

func newIssueCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "issue", Short: "Manage issues"}
	cmd.AddCommand(
		issueAddCmd(),
		issueListCmd(),
		issueShowCmd(),
		issueBriefCmd(),
		issueEditCmd(),
		issueStateCmd(),
		issueRmCmd(),
	)
	return cmd
}

// resolveIssueByKey resolves "MINI-42" or just "42" (relative to current repo).
func resolveIssueByKey(s *store.Store, key string) (*model.Issue, error) {
	key = strings.TrimSpace(key)
	if !strings.Contains(key, "-") {
		repo, err := resolveRepo(s)
		if err != nil {
			return nil, err
		}
		var n int64
		if _, err := fmt.Sscanf(key, "%d", &n); err != nil {
			return nil, fmt.Errorf("invalid issue reference %q", key)
		}
		return s.GetIssueByKey(repo.Prefix, n)
	}
	prefix, num, err := store.ParseIssueKey(key)
	if err != nil {
		return nil, err
	}
	return s.GetIssueByKey(prefix, num)
}

func issueAddCmd() *cobra.Command {
	var (
		featureSlug, description, descriptionFile, stateStr string
		tags                                                []string
	)
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Create an issue in the current repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := args[0]
			desc, err := readLongText(description, descriptionFile, false, "description")
			if err != nil {
				return err
			}
			state := model.StateBacklog
			if stateStr != "" {
				state, err = model.ParseState(stateStr)
				if err != nil {
					return err
				}
			}
			cleanTags, err := store.NormalizeTags(tags)
			if err != nil {
				return err
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			repo, err := resolveRepo(s)
			if err != nil {
				return err
			}
			var featureID *int64
			if featureSlug != "" {
				f, err := s.GetFeatureBySlug(repo.ID, featureSlug)
				if err != nil {
					return fmt.Errorf("feature %q: %w", featureSlug, err)
				}
				featureID = &f.ID
			}
			iss, err := s.CreateIssue(repo.ID, featureID, title, desc, state, cleanTags)
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "issue.create", Kind: "issue",
				TargetID: &iss.ID, TargetLabel: iss.Key,
				Details: iss.Title,
			})
			return emit(iss)
		},
	}
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "feature slug to attach to")
	cmd.Flags().StringVar(&description, "description", "", "description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&stateStr, "state", "", "initial state (default: backlog)")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tag to attach (repeatable)")
	return cmd
}

func issueListCmd() *cobra.Command {
	var (
		stateCSV    string
		featureSlug string
		tags        []string
		allRepos    bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List issues",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			f := store.IssueFilter{AllRepos: allRepos}
			if !allRepos {
				repo, err := resolveRepo(s)
				if err != nil {
					return err
				}
				f.RepoID = &repo.ID
				if featureSlug != "" {
					feat, err := s.GetFeatureBySlug(repo.ID, featureSlug)
					if err != nil {
						return fmt.Errorf("feature %q: %w", featureSlug, err)
					}
					f.FeatureID = &feat.ID
				}
			}
			if stateCSV != "" {
				for _, raw := range strings.Split(stateCSV, ",") {
					st, err := model.ParseState(raw)
					if err != nil {
						return err
					}
					f.States = append(f.States, st)
				}
			}
			if len(tags) > 0 {
				cleanTags, err := store.NormalizeTags(tags)
				if err != nil {
					return err
				}
				f.Tags = cleanTags
			}
			issues, err := s.ListIssues(f)
			if err != nil {
				return err
			}
			return emit(issues)
		},
	}
	cmd.Flags().StringVar(&stateCSV, "state", "", "comma-separated states to filter (e.g. todo,in_progress)")
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "limit to a feature")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "require this tag (repeatable; AND semantics)")
	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "search across all tracked repos")
	return cmd
}

func issueShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <KEY>",
		Short: "Show an issue with comments and relations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			iss, err := resolveIssueByKey(s, args[0])
			if err != nil {
				return err
			}
			comments, err := s.ListComments(iss.ID)
			if err != nil {
				return err
			}
			rels, err := s.ListIssueRelations(iss.ID)
			if err != nil {
				return err
			}
			prs, err := s.ListPRs(iss.ID)
			if err != nil {
				return err
			}
			docs, err := s.ListDocumentsLinkedToIssue(iss.ID)
			if err != nil {
				return err
			}
			return emit(&issueView{
				Issue: iss, Comments: comments, Relations: rels,
				PullRequests: prs, Documents: docs,
			})
		},
	}
}

func issueEditCmd() *cobra.Command {
	var (
		title, description, descriptionFile, featureSlug string
		clearFeature                                     bool
	)
	cmd := &cobra.Command{
		Use:   "edit <KEY>",
		Short: "Edit an issue's title, description, or feature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			iss, err := resolveIssueByKey(s, args[0])
			if err != nil {
				return err
			}
			var tPtr, dPtr *string
			var fPtr **int64
			if cmd.Flags().Changed("title") {
				tPtr = &title
			}
			if description != "" || descriptionFile != "" {
				d, err := readLongText(description, descriptionFile, true, "description")
				if err != nil {
					return err
				}
				dPtr = &d
			}
			if clearFeature && featureSlug != "" {
				return fmt.Errorf("--feature and --no-feature are mutually exclusive")
			}
			if clearFeature {
				var none *int64
				fPtr = &none
			} else if featureSlug != "" {
				feat, err := s.GetFeatureBySlug(iss.RepoID, featureSlug)
				if err != nil {
					return fmt.Errorf("feature %q: %w", featureSlug, err)
				}
				p := &feat.ID
				fPtr = &p
			}
			if tPtr == nil && dPtr == nil && fPtr == nil {
				return fmt.Errorf("nothing to update")
			}
			if err := s.UpdateIssue(iss.ID, tPtr, dPtr, fPtr); err != nil {
				return err
			}
			updated, err := s.GetIssueByID(iss.ID)
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &iss.RepoID,
				Op:     "issue.update", Kind: "issue",
				TargetID: &updated.ID, TargetLabel: updated.Key,
				Details: updatedFieldList(map[string]bool{
					"title":       tPtr != nil,
					"description": dPtr != nil,
					"feature":     fPtr != nil,
				}),
			})
			return emit(updated)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&description, "description", "", "new description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "move to a feature slug")
	cmd.Flags().BoolVar(&clearFeature, "no-feature", false, "detach from any feature")
	return cmd
}

func issueStateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "state <KEY> <state>",
		Short: "Set issue state",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := model.ParseState(args[1])
			if err != nil {
				return err
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			iss, err := resolveIssueByKey(s, args[0])
			if err != nil {
				return err
			}
			oldState := iss.State
			if err := s.SetIssueState(iss.ID, st); err != nil {
				return err
			}
			updated, err := s.GetIssueByID(iss.ID)
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &iss.RepoID,
				Op:     "issue.state", Kind: "issue",
				TargetID: &updated.ID, TargetLabel: updated.Key,
				Details: fmt.Sprintf("%s → %s", oldState, st),
			})
			return emit(updated)
		},
	}
}

// issueBrief is the bulk-context payload returned by `mk issue brief`. It's
// designed for skill / LLM consumption: one read, one structured object,
// every doc body inlined so the consumer can reason about the issue without
// chasing N+1 follow-up reads.
type issueBrief struct {
	Issue        *model.Issue          `json:"issue"`
	Feature      *model.Feature        `json:"feature,omitempty"`
	Relations    *store.IssueRelations `json:"relations"`
	PullRequests []*model.PullRequest  `json:"pull_requests"`
	Documents    []*briefDoc           `json:"documents"`
	Comments     []*model.Comment      `json:"comments"`
	Warnings     []string              `json:"warnings"`
}

// briefDoc is a single linked document with its full content inlined and
// attribution path captured in LinkedVia (e.g. ["issue"], or
// ["issue", "feature/auth-rewrite"] when the same doc is reachable via
// both the issue and its parent feature).
type briefDoc struct {
	Filename    string             `json:"filename"`
	Type        model.DocumentType `json:"type"`
	Description string             `json:"description,omitempty"`
	SourcePath  string             `json:"source_path,omitempty"`
	LinkedVia   []string           `json:"linked_via"`
	Content     string             `json:"content"`
}

func issueBriefCmd() *cobra.Command {
	var (
		noFeatureDocs bool
		noComments    bool
	)
	cmd := &cobra.Command{
		Use:   "brief <KEY>",
		Short: "Bulk JSON context for an issue (issue + feature + linked docs with content + comments + relations + PRs)",
		Long: `Single bulk-context fetch for an issue. Always emits JSON regardless of
--output, since this verb is structured-data-by-design — it exists to
collapse the issue + feature + linked-docs + content + comments dance
that every skill was open-coding into one read.

Linked docs from the parent feature are included by default (use
--no-feature-docs to skip). Each doc carries a "linked_via" array that
records every attribution path (e.g. ["issue"] or
["issue", "feature/auth-rewrite"]).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			iss, err := resolveIssueByKey(s, args[0])
			if err != nil {
				return err
			}

			var feat *model.Feature
			if iss.FeatureID != nil {
				feat, err = s.GetFeatureByID(*iss.FeatureID)
				if err != nil {
					return err
				}
			}

			rels, err := s.ListIssueRelations(iss.ID)
			if err != nil {
				return err
			}
			prs, err := s.ListPRs(iss.ID)
			if err != nil {
				return err
			}
			if prs == nil {
				prs = []*model.PullRequest{}
			}

			docs, warnings, err := collectBriefDocs(s, iss.ID, feat, !noFeatureDocs)
			if err != nil {
				return err
			}

			var comments []*model.Comment
			if !noComments {
				comments, err = s.ListComments(iss.ID)
				if err != nil {
					return err
				}
			}
			if comments == nil {
				comments = []*model.Comment{}
			}

			brief := &issueBrief{
				Issue:        iss,
				Feature:      feat,
				Relations:    rels,
				PullRequests: prs,
				Documents:    docs,
				Comments:     comments,
				Warnings:     warnings,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(brief)
		},
	}
	cmd.Flags().BoolVar(&noFeatureDocs, "no-feature-docs", false, "skip docs linked to the parent feature")
	cmd.Flags().BoolVar(&noComments, "no-comments", false, "skip the comments section")
	return cmd
}

// collectBriefDocs assembles the deduped document list for an issue brief.
// Issue links come first; feature links append to existing entries (extending
// linked_via) or create new ones. When both link rows have differing --why
// descriptions, the issue's wins and a warning is appended so nothing is
// silently dropped.
func collectBriefDocs(s *store.Store, issueID int64, feat *model.Feature, includeFeature bool) ([]*briefDoc, []string, error) {
	warnings := []string{}
	out := []*briefDoc{}
	byDocID := map[int64]*briefDoc{}

	issueLinks, err := s.ListDocumentsLinkedToIssue(issueID)
	if err != nil {
		return nil, nil, err
	}
	for _, l := range issueLinks {
		d, err := s.GetDocumentByID(l.DocumentID, true)
		if err != nil {
			return nil, nil, err
		}
		entry := &briefDoc{
			Filename:    d.Filename,
			Type:        d.Type,
			Description: l.Description,
			SourcePath:  d.SourcePath,
			LinkedVia:   []string{"issue"},
			Content:     d.Content,
		}
		out = append(out, entry)
		byDocID[d.ID] = entry
	}

	if includeFeature && feat != nil {
		featLinks, err := s.ListDocumentsLinkedToFeature(feat.ID)
		if err != nil {
			return nil, nil, err
		}
		via := "feature/" + feat.Slug
		for _, l := range featLinks {
			if existing, ok := byDocID[l.DocumentID]; ok {
				existing.LinkedVia = append(existing.LinkedVia, via)
				if l.Description != "" && l.Description != existing.Description {
					if existing.Description == "" {
						existing.Description = l.Description
					} else {
						warnings = append(warnings, fmt.Sprintf(
							"document %s: feature link description differs from issue link; using issue's. Feature said: %q",
							existing.Filename, l.Description))
					}
				}
				continue
			}
			d, err := s.GetDocumentByID(l.DocumentID, true)
			if err != nil {
				return nil, nil, err
			}
			entry := &briefDoc{
				Filename:    d.Filename,
				Type:        d.Type,
				Description: l.Description,
				SourcePath:  d.SourcePath,
				LinkedVia:   []string{via},
				Content:     d.Content,
			}
			out = append(out, entry)
			byDocID[d.ID] = entry
		}
	}

	return out, warnings, nil
}

func issueRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <KEY>",
		Short: "Delete an issue (and its comments)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			iss, err := resolveIssueByKey(s, args[0])
			if err != nil {
				return err
			}
			if err := s.DeleteIssue(iss.ID); err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &iss.RepoID,
				Op:     "issue.delete", Kind: "issue",
				TargetID: &iss.ID, TargetLabel: iss.Key,
				Details: iss.Title,
			})
			return ok("issue %s deleted", iss.Key)
		},
	}
}
