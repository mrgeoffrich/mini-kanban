package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

func newIssueCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "issue", Short: "Manage issues"}
	cmd.AddCommand(
		issueAddCmd(),
		issueListCmd(),
		issueShowCmd(),
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
			iss, err := s.CreateIssue(repo.ID, featureID, title, desc, state)
			if err != nil {
				return err
			}
			return emit(iss)
		},
	}
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "feature slug to attach to")
	cmd.Flags().StringVar(&description, "description", "", "description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&stateStr, "state", "", "initial state (default: backlog)")
	return cmd
}

func issueListCmd() *cobra.Command {
	var (
		stateCSV    string
		featureSlug string
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
			issues, err := s.ListIssues(f)
			if err != nil {
				return err
			}
			return emit(issues)
		},
	}
	cmd.Flags().StringVar(&stateCSV, "state", "", "comma-separated states to filter (e.g. todo,in_progress)")
	cmd.Flags().StringVarP(&featureSlug, "feature", "f", "", "limit to a feature")
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
			atts, err := s.ListAttachments(store.AttachmentTarget{IssueID: &iss.ID})
			if err != nil {
				return err
			}
			return emit(&issueView{
				Issue: iss, Comments: comments, Relations: rels,
				PullRequests: prs, Attachments: atts,
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
			if err := s.SetIssueState(iss.ID, st); err != nil {
				return err
			}
			updated, err := s.GetIssueByID(iss.ID)
			if err != nil {
				return err
			}
			return emit(updated)
		},
	}
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
			return ok("issue %s deleted", iss.Key)
		},
	}
}
