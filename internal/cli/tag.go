package cli

import (
	"github.com/spf13/cobra"

	"mini-kanban/internal/store"
)

func newTagCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tag", Short: "Manage tags on issues"}
	cmd.AddCommand(tagAddCmd(), tagRmCmd())
	return cmd
}

func tagAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <KEY> <tag> [<tag>...]",
		Short: "Add one or more tags to an issue (idempotent)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			tags, err := store.NormalizeTags(args[1:])
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
			if err := s.AddTagsToIssue(iss.ID, tags); err != nil {
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

func tagRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <KEY> <tag> [<tag>...]",
		Short: "Remove one or more tags from an issue",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			tags, err := store.NormalizeTags(args[1:])
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
			if err := s.RemoveTagsFromIssue(iss.ID, tags); err != nil {
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
