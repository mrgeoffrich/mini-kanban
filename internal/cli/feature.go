package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

func newFeatureCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "feature", Short: "Manage features (groups of issues)"}
	cmd.AddCommand(featureAddCmd(), featureListCmd(), featureShowCmd(), featureEditCmd(), featureRmCmd(), featurePlanCmd())
	return cmd
}

func featureAddCmd() *cobra.Command {
	var (
		slug, description, descriptionFile, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "add [title]",
		Short: "Create a feature in the current repo",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args,
					"slug", "description", "description-file"); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.FeatureAddInput](raw)
				if err != nil {
					return err
				}
				if in.Title == "" {
					return fmt.Errorf("title is required")
				}
				return createFeature(in.Title, in.Slug, in.Description)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <title> positional or --json")
			}
			desc, err := readLongText(description, descriptionFile, false, "description")
			if err != nil {
				return err
			}
			return createFeature(args[0], slug, desc)
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "explicit slug (default: derived from title)")
	cmd.Flags().StringVar(&description, "description", "", "description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func createFeature(title, slug, description string) error {
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
	if opts.dryRun {
		projected := &model.Feature{
			RepoID:      repo.ID,
			Slug:        slug,
			Title:       title,
			Description: description,
		}
		return emitDryRun(projected)
	}
	f, err := s.CreateFeature(repo.ID, slug, title, description)
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
}

func featureListCmd() *cobra.Command {
	var withDescription bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List features in the current repo (descriptions are stripped by default; pass --with-description to include them)",
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
			fs, err := s.ListFeatures(repo.ID, withDescription)
			if err != nil {
				return err
			}
			return emit(fs)
		},
	}
	cmd.Flags().BoolVar(&withDescription, "with-description", false, "include each feature's full description in JSON output (off by default to keep responses small)")
	return cmd
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
		title, description, descriptionFile, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "edit [slug]",
		Short: "Edit a feature's title or description",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args,
					"title", "description", "description-file"); err != nil {
					return err
				}
				in, present, err := decodeStrict[inputs.FeatureEditInput](raw)
				if err != nil {
					return err
				}
				if in.Slug == "" {
					return fmt.Errorf("slug is required")
				}
				var tPtr, dPtr *string
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
				return applyFeatureEdit(in.Slug, tPtr, dPtr)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <slug> positional or --json")
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
			return applyFeatureEdit(args[0], tPtr, dPtr)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&description, "description", "", "new description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func applyFeatureEdit(slug string, tPtr, dPtr *string) error {
	if tPtr == nil && dPtr == nil {
		return fmt.Errorf("nothing to update; pass title and/or description")
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
	f, err := s.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return err
	}
	if opts.dryRun {
		projected := *f
		if tPtr != nil {
			projected.Title = *tPtr
		}
		if dPtr != nil {
			projected.Description = *dPtr
		}
		return emitDryRun(&projected)
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
}

func featureRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [slug]",
		Short: "Delete a feature (issues are kept, unlinked from it)",
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
				in, _, err := decodeStrict[inputs.FeatureRmInput](raw)
				if err != nil {
					return err
				}
				if in.Slug == "" {
					return fmt.Errorf("slug is required")
				}
				return removeFeature(in.Slug)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <slug> positional or --json")
			}
			return removeFeature(args[0])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func removeFeature(slug string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	repo, err := resolveRepo(s)
	if err != nil {
		return err
	}
	f, err := s.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return err
	}
	if opts.dryRun {
		issues, err := s.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &f.ID})
		if err != nil {
			return err
		}
		docs, err := s.ListDocumentsLinkedToFeature(f.ID)
		if err != nil {
			return err
		}
		return emitDryRun(&featureDeletePreview{
			Feature:           f,
			WouldDelete:       true,
			IssuesUnlinked:    len(issues),
			DocumentLinks:     len(docs),
		})
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
}

// featureDeletePreview is the dry-run payload for `mk feature rm`.
// IssuesUnlinked counts issues that would have their feature_id set to NULL
// (the schema cascades via SET NULL, not DELETE); DocumentLinks counts
// document_links rows that would actually be removed.
type featureDeletePreview struct {
	Feature        *model.Feature `json:"feature"`
	WouldDelete    bool           `json:"would_delete"`
	IssuesUnlinked int            `json:"issues_unlinked"`
	DocumentLinks  int            `json:"document_links"`
}
