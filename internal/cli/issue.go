package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
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
		issueAssignCmd(),
		issueUnassignCmd(),
		issueNextCmd(),
		issuePeekCmd(),
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

// resolveIssueKeyStrict is the JSON-path equivalent of resolveIssueByKey: it
// requires the canonical "PREFIX-N" form and refuses bare-number references.
// Agents shouldn't be guessing the current repo's prefix.
func resolveIssueKeyStrict(s *store.Store, key string) (*model.Issue, error) {
	key = strings.TrimSpace(key)
	if !strings.Contains(key, "-") {
		return nil, fmt.Errorf("issue key %q must be canonical (e.g. \"MINI-42\")", key)
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
		rawInput                                            string
	)
	cmd := &cobra.Command{
		Use:   "add [title]",
		Short: "Create an issue in the current repo",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args,
					"feature", "description", "description-file", "state", "tag"); err != nil {
					return err
				}
				return runIssueAddJSON(raw)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <title> positional or --json")
			}
			return runIssueAdd(args[0], featureSlug, description, descriptionFile, stateStr, tags)
		},
	}
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "feature slug to attach to")
	cmd.Flags().StringVar(&description, "description", "", "description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&stateStr, "state", "", "initial state (default: backlog)")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tag to attach (repeatable)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runIssueAdd(title, featureSlug, description, descriptionFile, stateStr string, tags []string) error {
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
	return createIssue(title, featureSlug, desc, state, cleanTags)
}

func runIssueAddJSON(raw []byte) error {
	in, _, err := decodeStrict[inputs.IssueAddInput](raw)
	if err != nil {
		return err
	}
	if in.Title == "" {
		return fmt.Errorf("title is required")
	}
	state := model.StateBacklog
	if in.State != "" {
		st, err := model.ParseState(in.State)
		if err != nil {
			return err
		}
		state = st
	}
	cleanTags, err := store.NormalizeTags(in.Tags)
	if err != nil {
		return err
	}
	return createIssue(in.Title, in.FeatureSlug, in.Description, state, cleanTags)
}

func createIssue(title, featureSlug, description string, state model.State, tags []string) error {
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
	if opts.dryRun {
		projected := &model.Issue{
			RepoID:      repo.ID,
			Number:      repo.NextIssueNumber,
			Key:         fmt.Sprintf("%s-%d", repo.Prefix, repo.NextIssueNumber),
			FeatureID:   featureID,
			FeatureSlug: featureSlug,
			Title:       title,
			Description: description,
			State:       state,
			Tags:        tags,
		}
		if projected.Tags == nil {
			projected.Tags = []string{}
		}
		return emitDryRun(projected)
	}
	iss, err := s.CreateIssue(repo.ID, featureID, title, description, state, tags)
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
}

func issueListCmd() *cobra.Command {
	var (
		stateCSV        string
		featureSlug     string
		tags            []string
		allRepos        bool
		withDescription bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List issues (descriptions are stripped by default; pass --with-description to include them)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			f := store.IssueFilter{AllRepos: allRepos, IncludeDescription: withDescription}
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
	cmd.Flags().BoolVar(&withDescription, "with-description", false, "include each issue's full description in JSON output (off by default to keep responses small)")
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
		rawInput                                         string
	)
	cmd := &cobra.Command{
		Use:   "edit [KEY]",
		Short: "Edit an issue's title, description, or feature",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args,
					"title", "description", "description-file", "feature", "no-feature"); err != nil {
					return err
				}
				return runIssueEditJSON(raw)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <KEY> positional or --json")
			}
			return runIssueEdit(cmd, args[0], title, description, descriptionFile, featureSlug, clearFeature)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&description, "description", "", "new description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "move to a feature slug")
	cmd.Flags().BoolVar(&clearFeature, "no-feature", false, "detach from any feature")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runIssueEdit(cmd *cobra.Command, key, title, description, descriptionFile, featureSlug string, clearFeature bool) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	iss, err := resolveIssueByKey(s, key)
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
	return applyIssueEdit(s, iss, tPtr, dPtr, fPtr)
}

func runIssueEditJSON(raw []byte) error {
	in, present, err := decodeStrict[inputs.IssueEditInput](raw)
	if err != nil {
		return err
	}
	if in.Key == "" {
		return fmt.Errorf("key is required")
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	iss, err := resolveIssueKeyStrict(s, in.Key)
	if err != nil {
		return err
	}
	var tPtr, dPtr *string
	var fPtr **int64
	if _, ok := present["title"]; ok {
		if in.Title == nil || *in.Title == "" {
			return fmt.Errorf("title cannot be empty or null; omit the field to leave it unchanged")
		}
		tPtr = in.Title
	}
	if _, ok := present["description"]; ok {
		if in.Description == nil {
			empty := ""
			dPtr = &empty
		} else {
			dPtr = in.Description
		}
	}
	if _, ok := present["feature_slug"]; ok {
		if in.FeatureSlug == nil || *in.FeatureSlug == "" {
			var none *int64
			fPtr = &none
		} else {
			feat, err := s.GetFeatureBySlug(iss.RepoID, *in.FeatureSlug)
			if err != nil {
				return fmt.Errorf("feature %q: %w", *in.FeatureSlug, err)
			}
			p := &feat.ID
			fPtr = &p
		}
	}
	return applyIssueEdit(s, iss, tPtr, dPtr, fPtr)
}

func applyIssueEdit(s *store.Store, iss *model.Issue, tPtr, dPtr *string, fPtr **int64) error {
	if tPtr == nil && dPtr == nil && fPtr == nil {
		return fmt.Errorf("nothing to update")
	}
	if opts.dryRun {
		projected := *iss
		if tPtr != nil {
			projected.Title = *tPtr
		}
		if dPtr != nil {
			projected.Description = *dPtr
		}
		if fPtr != nil {
			projected.FeatureID = *fPtr
			if *fPtr == nil {
				projected.FeatureSlug = ""
			} else {
				feat, err := s.GetFeatureByID(**fPtr)
				if err != nil {
					return err
				}
				projected.FeatureSlug = feat.Slug
			}
		}
		return emitDryRun(&projected)
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
}

func issueStateCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "state [KEY] [state]",
		Short: "Set issue state",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.IssueStateInput](raw)
				if err != nil {
					return err
				}
				if in.Key == "" || in.State == "" {
					return fmt.Errorf("key and state are required")
				}
				return setIssueState(in.Key, in.State, true)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <KEY> <state> positionals or --json")
			}
			return setIssueState(args[0], args[1], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func setIssueState(key, stateStr string, strict bool) error {
	st, err := model.ParseState(stateStr)
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	resolve := resolveIssueByKey
	if strict {
		resolve = resolveIssueKeyStrict
	}
	iss, err := resolve(s, key)
	if err != nil {
		return err
	}
	if opts.dryRun {
		projected := *iss
		projected.State = st
		return emitDryRun(&projected)
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
		noDocContent  bool
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
["issue", "feature/auth-rewrite"]).

Pass --no-doc-content to keep doc metadata (filename, type, source_path,
linked_via, description) but drop the bodies — useful when you want the
shape of an issue's context without paying for every linked doc's full
text. Fetch specific bodies later via mk doc show.`,
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
			if noDocContent {
				for _, d := range docs {
					d.Content = ""
				}
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
	cmd.Flags().BoolVar(&noDocContent, "no-doc-content", false, "keep linked-doc metadata but drop their bodies (fetch via `mk doc show`)")
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

func issueAssignCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "assign [KEY] [name]",
		Short: "Assign an issue to a person or agent",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.IssueAssignInput](raw)
				if err != nil {
					return err
				}
				if in.Key == "" || strings.TrimSpace(in.Assignee) == "" {
					return fmt.Errorf("key and non-empty assignee are required")
				}
				return assignIssue(in.Key, in.Assignee, true)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <KEY> <name> positionals or --json")
			}
			return assignIssue(args[0], args[1], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func assignIssue(key, name string, strict bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("assignee name must be non-empty (use `mk issue unassign` to clear)")
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	resolve := resolveIssueByKey
	if strict {
		resolve = resolveIssueKeyStrict
	}
	iss, err := resolve(s, key)
	if err != nil {
		return err
	}
	if opts.dryRun {
		projected := *iss
		projected.Assignee = name
		return emitDryRun(&projected)
	}
	old := iss.Assignee
	if err := s.SetIssueAssignee(iss.ID, name); err != nil {
		return err
	}
	updated, err := s.GetIssueByID(iss.ID)
	if err != nil {
		return err
	}
	details := "assigned to " + name
	if old != "" {
		details = fmt.Sprintf("%s → %s", old, name)
	}
	recordOp(s, model.HistoryEntry{
		RepoID: &iss.RepoID,
		Op:     "issue.assign", Kind: "issue",
		TargetID: &updated.ID, TargetLabel: updated.Key,
		Details: details,
	})
	return emit(updated)
}

func issueUnassignCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "unassign [KEY]",
		Short: "Clear the assignee on an issue",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.IssueUnassignInput](raw)
				if err != nil {
					return err
				}
				if in.Key == "" {
					return fmt.Errorf("key is required")
				}
				return unassignIssue(in.Key, true)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <KEY> positional or --json")
			}
			return unassignIssue(args[0], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func unassignIssue(key string, strict bool) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	resolve := resolveIssueByKey
	if strict {
		resolve = resolveIssueKeyStrict
	}
	iss, err := resolve(s, key)
	if err != nil {
		return err
	}
	if iss.Assignee == "" {
		return emit(iss)
	}
	if opts.dryRun {
		projected := *iss
		projected.Assignee = ""
		return emitDryRun(&projected)
	}
	old := iss.Assignee
	if err := s.SetIssueAssignee(iss.ID, ""); err != nil {
		return err
	}
	updated, err := s.GetIssueByID(iss.ID)
	if err != nil {
		return err
	}
	recordOp(s, model.HistoryEntry{
		RepoID: &iss.RepoID,
		Op:     "issue.assign", Kind: "issue",
		TargetID: &updated.ID, TargetLabel: updated.Key,
		Details: fmt.Sprintf("%s → (unassigned)", old),
	})
	return emit(updated)
}

// claimResult is the structured payload `mk issue next` emits. Issue is nil
// when no work is currently claimable (so JSON consumers see {"issue": null}
// and can poll without parsing errors).
type claimResult struct {
	Issue *model.Issue `json:"issue"`
}

func issueNextCmd() *cobra.Command {
	var featureSlug, rawInput string
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Atomically claim the next ready issue in a feature",
		Long: `Picks the lowest-numbered todo issue in --feature whose blockers are all
done/cancelled/duplicate and whose assignee is empty, flips it to
in_progress, and stamps the assignee with --user.

Designed for agent loops: call repeatedly to walk through a feature in
dependency order. When nothing is currently claimable (everything is
either claimed, done, or still blocked) the command emits an empty
result with exit code 0 — the caller should wait and retry rather than
treat it as an error.

Pass --user explicitly so the audit log and assignee reflect the agent's
identity instead of the OS username.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args, "feature"); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.IssueNextInput](raw)
				if err != nil {
					return err
				}
				if in.FeatureSlug == "" {
					return fmt.Errorf("feature_slug is required")
				}
				return claimNextIssue(in.FeatureSlug)
			}
			if featureSlug == "" {
				return fmt.Errorf("--feature is required")
			}
			return claimNextIssue(featureSlug)
		},
	}
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "feature slug to pull from (required)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func claimNextIssue(featureSlug string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	repo, err := resolveRepo(s)
	if err != nil {
		return err
	}
	feat, err := s.GetFeatureBySlug(repo.ID, featureSlug)
	if err != nil {
		return fmt.Errorf("feature %q: %w", featureSlug, err)
	}
	if opts.dryRun {
		// Equivalent to `mk issue peek`: report what would be claimed
		// without flipping state or stamping the assignee.
		iss, err := s.PeekNextIssue(repo.ID, feat.ID)
		if err != nil {
			return err
		}
		return emitDryRun(&claimResult{Issue: iss})
	}
	who := actor()
	iss, err := s.ClaimNextIssue(repo.ID, feat.ID, who)
	if err != nil {
		return err
	}
	if iss != nil {
		recordOp(s, model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Op: "issue.claim", Kind: "issue",
			TargetID: &iss.ID, TargetLabel: iss.Key,
			Details: fmt.Sprintf("claimed by %s (todo → in_progress)", who),
		})
	}
	return emit(&claimResult{Issue: iss})
}

func issuePeekCmd() *cobra.Command {
	var featureSlug string
	cmd := &cobra.Command{
		Use:   "peek",
		Short: "Show the next ready issue in a feature without claiming it",
		Long: `Read-only counterpart to ` + "`mk issue next`" + `: returns the same issue
the claim would pick (lowest-numbered todo with all blockers
done/cancelled/duplicate and no assignee) but does not mutate state.

Emits an empty result with exit code 0 when nothing is currently
claimable, matching the shape of ` + "`mk issue next`" + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if featureSlug == "" {
				return fmt.Errorf("--feature is required")
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
			feat, err := s.GetFeatureBySlug(repo.ID, featureSlug)
			if err != nil {
				return fmt.Errorf("feature %q: %w", featureSlug, err)
			}
			iss, err := s.PeekNextIssue(repo.ID, feat.ID)
			if err != nil {
				return err
			}
			return emit(&claimResult{Issue: iss})
		},
	}
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "feature slug to peek into (required)")
	return cmd
}

func issueRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [KEY]",
		Short: "Delete an issue (and its comments)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.IssueRmInput](raw)
				if err != nil {
					return err
				}
				if in.Key == "" {
					return fmt.Errorf("key is required")
				}
				return removeIssue(in.Key, true)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <KEY> positional or --json")
			}
			return removeIssue(args[0], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func removeIssue(key string, strict bool) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	resolve := resolveIssueByKey
	if strict {
		resolve = resolveIssueKeyStrict
	}
	iss, err := resolve(s, key)
	if err != nil {
		return err
	}
	if opts.dryRun {
		preview, err := previewIssueDelete(s, iss)
		if err != nil {
			return err
		}
		return emitDryRun(preview)
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
}

// issueDeletePreview is the dry-run payload for `mk issue rm`. It records
// the row that would be deleted plus how many dependent rows would be
// cascade-removed alongside it.
type issueDeletePreview struct {
	Issue          *model.Issue `json:"issue"`
	Cascade        cascadeCount `json:"cascade"`
	WouldDelete    bool         `json:"would_delete"`
}

// cascadeCount summarises how many dependent rows ride along with a
// destructive operation. Zeros are kept (not omitempty) so the JSON shape
// is stable for downstream parsers.
type cascadeCount struct {
	Comments      int `json:"comments"`
	Relations     int `json:"relations"`
	PullRequests  int `json:"pull_requests"`
	DocumentLinks int `json:"document_links"`
	Tags          int `json:"tags"`
}

func previewIssueDelete(s *store.Store, iss *model.Issue) (*issueDeletePreview, error) {
	comments, err := s.ListComments(iss.ID)
	if err != nil {
		return nil, err
	}
	relations, err := s.ListIssueRelations(iss.ID)
	if err != nil {
		return nil, err
	}
	prs, err := s.ListPRs(iss.ID)
	if err != nil {
		return nil, err
	}
	docs, err := s.ListDocumentsLinkedToIssue(iss.ID)
	if err != nil {
		return nil, err
	}
	relCount := 0
	if relations != nil {
		relCount = len(relations.Outgoing) + len(relations.Incoming)
	}
	return &issueDeletePreview{
		Issue:       iss,
		WouldDelete: true,
		Cascade: cascadeCount{
			Comments:      len(comments),
			Relations:     relCount,
			PullRequests:  len(prs),
			DocumentLinks: len(docs),
			Tags:          len(iss.Tags),
		},
	}, nil
}
