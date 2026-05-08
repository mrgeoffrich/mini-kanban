package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

func newPRCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pr", Short: "Attach pull requests to an issue"}
	cmd.AddCommand(prAttachCmd(), prDetachCmd(), prListCmd())
	return cmd
}

func validatePRURL(raw string) (string, error) {
	return store.ValidatePRURLStrict(raw)
}

func prAttachCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "attach [KEY] [URL]",
		Short: "Attach a pull request URL to an issue",
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
				in, _, err := decodeStrict[inputs.PRAttachInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" || in.URL == "" {
					return fmt.Errorf("issue_key and url are required")
				}
				return attachPR(in.IssueKey, in.URL, true)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <KEY> <URL> positionals or --json")
			}
			return attachPR(args[0], args[1], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func attachPR(key, prURL string, strict bool) error {
	pr, err := validatePRURL(prURL)
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
	created, err := s.AttachPR(iss.ID, pr)
	if err != nil {
		return err
	}
	recordOp(s, model.HistoryEntry{
		RepoID: &iss.RepoID,
		Op:     "pr.attach", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: pr,
	})
	return emit(created)
}

func prDetachCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "detach [KEY] [URL]",
		Short: "Detach a pull request URL from an issue",
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
				in, _, err := decodeStrict[inputs.PRDetachInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" || in.URL == "" {
					return fmt.Errorf("issue_key and url are required")
				}
				return detachPR(in.IssueKey, in.URL, true)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <KEY> <URL> positionals or --json")
			}
			return detachPR(args[0], args[1], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func detachPR(key, prURL string, strict bool) error {
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
	clean := strings.TrimSpace(prURL)
	n, err := s.DetachPR(iss.ID, clean)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no PR matching %q on %s", prURL, iss.Key)
	}
	recordOp(s, model.HistoryEntry{
		RepoID: &iss.RepoID,
		Op:     "pr.detach", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: clean,
	})
	return ok("detached %s from %s", prURL, iss.Key)
}

func prListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <KEY>",
		Short: "List pull requests attached to an issue",
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
			prs, err := s.ListPRs(iss.ID)
			if err != nil {
				return err
			}
			return emit(prs)
		},
	}
}
