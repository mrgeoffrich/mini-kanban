package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

func newTagCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tag", Short: "Manage tags on issues"}
	cmd.AddCommand(tagAddCmd(), tagRmCmd())
	return cmd
}

func tagAddCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "add [KEY] [tag...]",
		Short: "Add one or more tags to an issue (idempotent)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.TagAddInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" || len(in.Tags) == 0 {
					return fmt.Errorf("issue_key and a non-empty tags array are required")
				}
				return mutateTags(in.IssueKey, in.Tags, true, true)
			}
			if len(args) < 2 {
				return fmt.Errorf("requires <KEY> <tag> [<tag>...] positionals or --json")
			}
			return mutateTags(args[0], args[1:], true, false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func tagRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [KEY] [tag...]",
		Short: "Remove one or more tags from an issue",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.TagRmInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" || len(in.Tags) == 0 {
					return fmt.Errorf("issue_key and a non-empty tags array are required")
				}
				return mutateTags(in.IssueKey, in.Tags, false, true)
			}
			if len(args) < 2 {
				return fmt.Errorf("requires <KEY> <tag> [<tag>...] positionals or --json")
			}
			return mutateTags(args[0], args[1:], false, false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func mutateTags(key string, rawTags []string, add, strict bool) error {
	tags, err := store.NormalizeTags(rawTags)
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
	op := "tag.add"
	if add {
		err = s.AddTagsToIssue(iss.ID, tags)
	} else {
		err = s.RemoveTagsFromIssue(iss.ID, tags)
		op = "tag.remove"
	}
	if err != nil {
		return err
	}
	updated, err := s.GetIssueByID(iss.ID)
	if err != nil {
		return err
	}
	recordOp(s, model.HistoryEntry{
		RepoID: &iss.RepoID,
		Op:     op, Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: strings.Join(tags, ","),
	})
	return emit(updated)
}
