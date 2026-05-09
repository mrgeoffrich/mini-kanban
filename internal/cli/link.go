package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
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
	var rawInput string
	cmd := &cobra.Command{
		Use:   "link [FROM] [type] [TO]",
		Short: "Create a relation between two issues",
		Long:  "Types: blocks, relates-to, duplicate-of",
		Args:  cobra.RangeArgs(0, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.LinkInput](raw)
				if err != nil {
					return err
				}
				if in.From == "" || in.Type == "" || in.To == "" {
					return fmt.Errorf("from, type, and to are required")
				}
				return createRelation(in.From, in.Type, in.To, true)
			}
			if len(args) != 3 {
				return fmt.Errorf("requires <FROM> <type> <TO> positionals or --json")
			}
			return createRelation(args[0], args[1], args[2], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func createRelation(fromKey, typeStr, toKey string, strict bool) error {
	t, err := parseRelType(typeStr)
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
	from, err := resolve(s, fromKey)
	if err != nil {
		return err
	}
	to, err := resolve(s, toKey)
	if err != nil {
		return err
	}
	if from.ID == to.ID {
		return fmt.Errorf("an issue cannot be linked to itself")
	}
	if opts.dryRun {
		return emitDryRun(&model.Relation{
			FromIssue: from.Key,
			ToIssue:   to.Key,
			Type:      t,
		})
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
}

func newUnlinkCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "unlink [A] [B]",
		Short: "Remove all relations between two issues",
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
				in, _, err := decodeStrict[inputs.UnlinkInput](raw)
				if err != nil {
					return err
				}
				if in.A == "" || in.B == "" {
					return fmt.Errorf("a and b are required")
				}
				return removeRelation(in.A, in.B, true)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <A> <B> positionals or --json")
			}
			return removeRelation(args[0], args[1], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func removeRelation(aKey, bKey string, strict bool) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	resolve := resolveIssueByKey
	if strict {
		resolve = resolveIssueKeyStrict
	}
	a, err := resolve(s, aKey)
	if err != nil {
		return err
	}
	b, err := resolve(s, bKey)
	if err != nil {
		return err
	}
	if opts.dryRun {
		// Count relations in both directions without deleting them.
		rels, err := s.ListIssueRelations(a.ID)
		if err != nil {
			return err
		}
		matched := 0
		if rels != nil {
			for _, r := range rels.Outgoing {
				if r.ToIssue == b.Key {
					matched++
				}
			}
			for _, r := range rels.Incoming {
				if r.FromIssue == b.Key {
					matched++
				}
			}
		}
		return emitDryRun(&relationDeletePreview{
			A:           a.Key,
			B:           b.Key,
			WouldRemove: matched,
		})
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
}

// relationDeletePreview is the dry-run payload for `mk unlink`.
type relationDeletePreview struct {
	A           string `json:"a"`
	B           string `json:"b"`
	WouldRemove int    `json:"would_remove"`
}
