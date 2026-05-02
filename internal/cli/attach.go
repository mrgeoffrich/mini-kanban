package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"mini-kanban/internal/store"
)

func newAttachCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "attach", Short: "Manage text attachments on issues or features"}
	cmd.AddCommand(attachAddCmd(), attachListCmd(), attachShowCmd(), attachRmCmd())
	return cmd
}

// resolveAttachTarget interprets a positional arg as either an issue key
// (e.g. MINI-42) or a feature slug in the current repo.
func resolveAttachTarget(s *store.Store, ref string) (store.AttachmentTarget, string, error) {
	ref = strings.TrimSpace(ref)
	if isIssueKey(ref) {
		iss, err := resolveIssueByKey(s, ref)
		if err != nil {
			return store.AttachmentTarget{}, "", err
		}
		return store.AttachmentTarget{IssueID: &iss.ID}, iss.Key, nil
	}
	repo, err := resolveRepo(s)
	if err != nil {
		return store.AttachmentTarget{}, "", err
	}
	feat, err := s.GetFeatureBySlug(repo.ID, ref)
	if err != nil {
		return store.AttachmentTarget{}, "", fmt.Errorf("%q is not an issue key or feature slug in this repo", ref)
	}
	return store.AttachmentTarget{FeatureID: &feat.ID}, feat.Slug, nil
}

var issueKeyShape = regexp.MustCompile(`^[A-Za-z0-9]{4}-\d+$`)

func isIssueKey(s string) bool { return issueKeyShape.MatchString(s) }

var filenameRe = regexp.MustCompile(`^[^/\\\x00]+$`)

func validateFilename(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	if !filenameRe.MatchString(name) {
		return fmt.Errorf("filename must not contain '/', '\\\\', or NUL")
	}
	return nil
}

func attachAddCmd() *cobra.Command {
	var (
		name, file string
	)
	cmd := &cobra.Command{
		Use:   "add <ISSUE-KEY|feature-slug>",
		Short: "Attach a text file to an issue or feature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFilename(name); err != nil {
				return err
			}
			if file == "" {
				return fmt.Errorf("--file is required (use '-' to read stdin)")
			}
			content, err := readFile(file)
			if err != nil {
				return fmt.Errorf("read attachment: %w", err)
			}
			if !utf8.ValidString(content) {
				return fmt.Errorf("attachment is not valid UTF-8 text; only text attachments are supported")
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			target, _, err := resolveAttachTarget(s, args[0])
			if err != nil {
				return err
			}
			a, err := s.CreateAttachment(target, name, content)
			if err != nil {
				return err
			}
			a.Content = "" // don't echo body on add
			return emit(a)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "logical filename to store as (e.g. spec.md)")
	cmd.Flags().StringVar(&file, "file", "", "path to local file, or '-' for stdin")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func attachListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <ISSUE-KEY|feature-slug>",
		Short: "List attachments on an issue or feature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			target, _, err := resolveAttachTarget(s, args[0])
			if err != nil {
				return err
			}
			as, err := s.ListAttachments(target)
			if err != nil {
				return err
			}
			return emit(as)
		},
	}
}

func attachShowCmd() *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "show <ISSUE-KEY|feature-slug> <filename>",
		Short: "Show an attachment's content",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			target, _, err := resolveAttachTarget(s, args[0])
			if err != nil {
				return err
			}
			a, err := s.GetAttachmentByName(target, args[1], true)
			if err != nil {
				return err
			}
			if raw {
				_, err := os.Stdout.WriteString(a.Content)
				return err
			}
			return emit(a)
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "write the file content to stdout with no metadata (ignores --output)")
	return cmd
}

func attachRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <ISSUE-KEY|feature-slug> <filename>",
		Short: "Remove an attachment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			target, label, err := resolveAttachTarget(s, args[0])
			if err != nil {
				return err
			}
			n, err := s.DeleteAttachment(target, args[1])
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("no attachment named %q on %s", args[1], label)
			}
			return ok("removed %s from %s", args[1], label)
		},
	}
}
