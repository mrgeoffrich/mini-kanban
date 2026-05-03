package cli

import (
	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

func newCommentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "comment", Short: "Manage issue comments"}
	cmd.AddCommand(commentAddCmd(), commentListCmd())
	return cmd
}

func commentAddCmd() *cobra.Command {
	var (
		author, body, bodyFile string
	)
	cmd := &cobra.Command{
		Use:   "add <KEY>",
		Short: "Add a comment to an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := readLongText(body, bodyFile, true, "body")
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
			c, err := s.CreateComment(iss.ID, author, text)
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &iss.RepoID,
				Op:     "comment.add", Kind: "issue",
				TargetID: &iss.ID, TargetLabel: iss.Key,
				Details: "by " + author,
			})
			return emit(c)
		},
	}
	cmd.Flags().StringVar(&author, "as", "", "comment author name (required)")
	cmd.Flags().StringVar(&body, "body", "", "comment body or '-' for stdin")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "path to a markdown file")
	_ = cmd.MarkFlagRequired("as")
	return cmd
}

func commentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <KEY>",
		Short: "List comments on an issue",
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
			cs, err := s.ListComments(iss.ID)
			if err != nil {
				return err
			}
			return emit(cs)
		},
	}
}
