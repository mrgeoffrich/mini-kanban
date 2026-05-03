package cli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

func newPRCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pr", Short: "Attach pull requests to an issue"}
	cmd.AddCommand(prAttachCmd(), prDetachCmd(), prListCmd())
	return cmd
}

func validatePRURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("URL must use http or https scheme")
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL must include a host")
	}
	return raw, nil
}

func prAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <KEY> <URL>",
		Short: "Attach a pull request URL to an issue",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pr, err := validatePRURL(args[1])
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
		},
	}
}

func prDetachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detach <KEY> <URL>",
		Short: "Detach a pull request URL from an issue",
		Args:  cobra.ExactArgs(2),
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
			url := strings.TrimSpace(args[1])
			n, err := s.DetachPR(iss.ID, url)
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("no PR matching %q on %s", args[1], iss.Key)
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &iss.RepoID,
				Op:     "pr.detach", Kind: "issue",
				TargetID: &iss.ID, TargetLabel: iss.Key,
				Details: url,
			})
			return ok("detached %s from %s", args[1], iss.Key)
		},
	}
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
