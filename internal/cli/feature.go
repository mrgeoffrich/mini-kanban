package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

func newFeatureCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "feature", Short: "Manage features (groups of issues)"}
	cmd.AddCommand(featureAddCmd(), featureListCmd(), featureShowCmd(), featureEditCmd(), featureRmCmd())
	return cmd
}

func featureAddCmd() *cobra.Command {
	var (
		slug, description, descriptionFile string
	)
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Create a feature in the current repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := args[0]
			desc, err := readLongText(description, descriptionFile, false, "description")
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
			if slug == "" {
				slug = store.Slugify(title)
			}
			f, err := s.CreateFeature(repo.ID, slug, title, desc)
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "feature.create", Kind: "feature",
				TargetID: &f.ID, TargetLabel: f.Slug,
				Details: f.Title,
			})
			return emit(f)
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "explicit slug (default: derived from title)")
	cmd.Flags().StringVar(&description, "description", "", "description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	return cmd
}

func featureListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List features in the current repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			repo, err := resolveRepo(s)
			if err != nil {
				return err
			}
			fs, err := s.ListFeatures(repo.ID)
			if err != nil {
				return err
			}
			return emit(fs)
		},
	}
}

func featureShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <slug>",
		Short: "Show a feature with its issues, attachments, and linked documents",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			repo, err := resolveRepo(s)
			if err != nil {
				return err
			}
			f, err := s.GetFeatureBySlug(repo.ID, args[0])
			if err != nil {
				return err
			}
			issues, err := s.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &f.ID})
			if err != nil {
				return err
			}
			docs, err := s.ListDocumentsLinkedToFeature(f.ID)
			if err != nil {
				return err
			}
			return emit(&featureView{Feature: f, Issues: issues, Documents: docs})
		},
	}
}

func featureEditCmd() *cobra.Command {
	var (
		title, description, descriptionFile string
	)
	cmd := &cobra.Command{
		Use:   "edit <slug>",
		Short: "Edit a feature's title or description",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			repo, err := resolveRepo(s)
			if err != nil {
				return err
			}
			f, err := s.GetFeatureBySlug(repo.ID, args[0])
			if err != nil {
				return err
			}
			var tPtr, dPtr *string
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
			if tPtr == nil && dPtr == nil {
				return fmt.Errorf("nothing to update; pass --title and/or --description")
			}
			if err := s.UpdateFeature(f.ID, tPtr, dPtr); err != nil {
				return err
			}
			updated, err := s.GetFeatureByID(f.ID)
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "feature.update", Kind: "feature",
				TargetID: &updated.ID, TargetLabel: updated.Slug,
				Details: updatedFieldList(map[string]bool{
					"title":       tPtr != nil,
					"description": dPtr != nil,
				}),
			})
			return emit(updated)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&description, "description", "", "new description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	return cmd
}

func featureRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <slug>",
		Short: "Delete a feature (issues are kept, unlinked from it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			repo, err := resolveRepo(s)
			if err != nil {
				return err
			}
			f, err := s.GetFeatureBySlug(repo.ID, args[0])
			if err != nil {
				return err
			}
			if err := s.DeleteFeature(f.ID); err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "feature.delete", Kind: "feature",
				TargetID: &f.ID, TargetLabel: f.Slug,
				Details: f.Title,
			})
			return ok("feature %s deleted", f.Slug)
		},
	}
}
