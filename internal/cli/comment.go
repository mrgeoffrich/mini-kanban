package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
	"github.com/mrgeoffrich/mini-kanban/internal/inputio"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

func newCommentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "comment", Short: "Manage issue comments"}
	cmd.AddCommand(commentAddCmd(), commentListCmd())
	return cmd
}

func commentAddCmd() *cobra.Command {
	var (
		author, body, bodyFile, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "add [KEY]",
		Short: "Add a comment to an issue",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args, "as", "body", "body-file"); err != nil {
					return err
				}
				in, _, err := inputio.DecodeStrict[inputs.CommentAddInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" || in.Author == "" || in.Body == "" {
					return fmt.Errorf("issue_key, author, and body are required")
				}
				return addComment(in.IssueKey, in.Author, in.Body, true)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <KEY> positional or --json")
			}
			text, err := readLongText(body, bodyFile, true, "body")
			if err != nil {
				return err
			}
			if author == "" {
				return fmt.Errorf("--as is required")
			}
			return addComment(args[0], author, text, false)
		},
	}
	cmd.Flags().StringVar(&author, "as", "", "comment author name (required when not using --json)")
	cmd.Flags().StringVar(&body, "body", "", "comment body or '-' for stdin")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "path to a markdown file")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func addComment(key, author, body string, strict bool) error {
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
		return emitDryRun(&model.Comment{
			IssueID: iss.ID,
			Author:  author,
			Body:    body,
		})
	}
	c, err := s.CreateComment(iss.ID, author, body)
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
