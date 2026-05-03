package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

func parseRelType(s string) (model.RelationType, error) {
	switch strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(s))) {
	case "blocks":
		return model.RelBlocks, nil
	case "relates_to", "relates":
		return model.RelRelatesTo, nil
	case "duplicate_of", "duplicates":
		return model.RelDuplicateOf, nil
	default:
		return "", fmt.Errorf("unknown relation %q (valid: blocks, relates-to, duplicate-of)", s)
	}
}

func newLinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "link <FROM> <type> <TO>",
		Short: "Create a relation between two issues",
		Long:  "Types: blocks, relates-to, duplicate-of",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := parseRelType(args[1])
			if err != nil {
				return err
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			from, err := resolveIssueByKey(s, args[0])
			if err != nil {
				return err
			}
			to, err := resolveIssueByKey(s, args[2])
			if err != nil {
				return err
			}
			if from.ID == to.ID {
				return fmt.Errorf("an issue cannot be linked to itself")
			}
			if err := s.CreateRelation(from.ID, to.ID, t); err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &from.RepoID,
				Op:     "relation.create", Kind: "issue",
				TargetID: &from.ID, TargetLabel: from.Key,
				Details: fmt.Sprintf("%s %s", t, to.Key),
			})
			return ok("%s %s %s", from.Key, t, to.Key)
		},
	}
}

func newUnlinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <A> <B>",
		Short: "Remove all relations between two issues",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			a, err := resolveIssueByKey(s, args[0])
			if err != nil {
				return err
			}
			b, err := resolveIssueByKey(s, args[1])
			if err != nil {
				return err
			}
			n, err := s.DeleteRelation(a.ID, b.ID)
			if err != nil {
				return err
			}
			if n > 0 {
				recordOp(s, model.HistoryEntry{
					RepoID: &a.RepoID,
					Op:     "relation.delete", Kind: "issue",
					TargetID: &a.ID, TargetLabel: a.Key,
					Details: fmt.Sprintf("unlinked from %s (%d row(s))", b.Key, n),
				})
			}
			return ok("removed %d relation(s) between %s and %s", n, a.Key, b.Key)
		},
	}
}
