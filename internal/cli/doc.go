package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

var (
	docFilenameRe = regexp.MustCompile(`^[^/\\\x00]+$`)
	issueKeyShape = regexp.MustCompile(`^[A-Za-z0-9]{4}-\d+$`)
)

// isIssueKey reports whether s has the PREFIX-N shape, used to disambiguate
// "issue or feature?" positionals on commands like `mk doc link`.
func isIssueKey(s string) bool { return issueKeyShape.MatchString(s) }

func validateDocFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("filename is required")
	}
	if !docFilenameRe.MatchString(name) {
		return "", fmt.Errorf("filename must not contain '/', '\\\\', or NUL")
	}
	return name, nil
}

func newDocCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "doc", Short: "Manage per-repo text documents and their links to issues/features"}
	cmd.AddCommand(
		docAddCmd(), docListCmd(), docShowCmd(),
		docEditCmd(), docRmCmd(),
		docLinkCmd(), docUnlinkCmd(),
	)
	return cmd
}

func docAddCmd() *cobra.Command {
	var (
		typeStr, content, contentFile string
	)
	cmd := &cobra.Command{
		Use:   "add <filename>",
		Short: "Create a document in the current repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filename, err := validateDocFilename(args[0])
			if err != nil {
				return err
			}
			t, err := model.ParseDocumentType(typeStr)
			if err != nil {
				return err
			}
			body, err := readLongText(content, contentFile, true, "content")
			if err != nil {
				return err
			}
			if !utf8.ValidString(body) {
				return fmt.Errorf("document is not valid UTF-8 text; only text documents are supported")
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
			d, err := s.CreateDocument(repo.ID, filename, t, body)
			if err != nil {
				return err
			}
			d.Content = "" // don't echo body on add
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "document.create", Kind: "document",
				TargetID: &d.ID, TargetLabel: d.Filename,
				Details: "type=" + string(d.Type),
			})
			// JSON consumers (AI agents, scripts) want the full document
			// object. Text consumers want a clear success line.
			if opts.output == outputJSON {
				return emit(d)
			}
			return ok("Created %s in %s (type=%s, %d bytes)",
				d.Filename, repo.Prefix, d.Type, d.SizeBytes)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "document type (e.g. architecture, designs, user-docs)")
	cmd.Flags().StringVar(&content, "content", "", "content text or '-' for stdin")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "path to a markdown file")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

func docListCmd() *cobra.Command {
	var typeStr string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List documents in the current repo",
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
			f := store.DocumentFilter{RepoID: repo.ID}
			if typeStr != "" {
				t, err := model.ParseDocumentType(typeStr)
				if err != nil {
					return err
				}
				f.Type = &t
			}
			docs, err := s.ListDocuments(f)
			if err != nil {
				return err
			}
			return emit(docs)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "filter by type")
	return cmd
}

func docShowCmd() *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "show <filename>",
		Short: "Show a document's metadata, content, and links",
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
			d, err := s.GetDocumentByFilename(repo.ID, args[0], true)
			if err != nil {
				return err
			}
			if raw {
				_, err := os.Stdout.WriteString(d.Content)
				return err
			}
			links, err := s.ListDocumentLinks(d.ID)
			if err != nil {
				return err
			}
			return emit(&docView{Document: d, Links: links})
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "write content to stdout with no metadata (ignores --output)")
	return cmd
}

func docEditCmd() *cobra.Command {
	var (
		typeStr, content, contentFile string
	)
	cmd := &cobra.Command{
		Use:   "edit <filename>",
		Short: "Edit a document's type and/or content",
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
			d, err := s.GetDocumentByFilename(repo.ID, args[0], false)
			if err != nil {
				return err
			}
			var (
				newType    *model.DocumentType
				newContent *string
			)
			if typeStr != "" {
				t, err := model.ParseDocumentType(typeStr)
				if err != nil {
					return err
				}
				newType = &t
			}
			if content != "" || contentFile != "" {
				body, err := readLongText(content, contentFile, true, "content")
				if err != nil {
					return err
				}
				if !utf8.ValidString(body) {
					return fmt.Errorf("document is not valid UTF-8 text; only text documents are supported")
				}
				newContent = &body
			}
			if newType == nil && newContent == nil {
				return fmt.Errorf("nothing to update; pass --type and/or --content/--content-file")
			}
			if err := s.UpdateDocument(d.ID, newType, newContent); err != nil {
				return err
			}
			updated, err := s.GetDocumentByID(d.ID, false)
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "document.update", Kind: "document",
				TargetID: &updated.ID, TargetLabel: updated.Filename,
				Details: updatedFieldList(map[string]bool{
					"type":    newType != nil,
					"content": newContent != nil,
				}),
			})
			return emit(updated)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "new type")
	cmd.Flags().StringVar(&content, "content", "", "new content text or '-' for stdin")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "path to a markdown file")
	return cmd
}

func docRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <filename>",
		Short: "Delete a document (and its links)",
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
			d, err := s.GetDocumentByFilename(repo.ID, args[0], false)
			if err != nil {
				return err
			}
			if err := s.DeleteDocument(d.ID); err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "document.delete", Kind: "document",
				TargetID: &d.ID, TargetLabel: d.Filename,
				Details: "type=" + string(d.Type),
			})
			return ok("deleted document %s", d.Filename)
		},
	}
}

func docLinkCmd() *cobra.Command {
	var why string
	cmd := &cobra.Command{
		Use:   "link <filename> <ISSUE-KEY|feature-slug>",
		Short: "Link a document to an issue or feature (upsert; --why replaces description)",
		Args:  cobra.ExactArgs(2),
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
			d, err := s.GetDocumentByFilename(repo.ID, args[0], false)
			if err != nil {
				return err
			}
			target, err := resolveDocLinkTarget(s, repo.ID, args[1])
			if err != nil {
				return err
			}
			link, err := s.LinkDocument(d.ID, target, strings.TrimSpace(why))
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "document.link", Kind: "document",
				TargetID: &d.ID, TargetLabel: d.Filename,
				Details: "→ " + args[1],
			})
			return emit(link)
		},
	}
	cmd.Flags().StringVar(&why, "why", "", "description of why this document is linked (optional)")
	return cmd
}

func docUnlinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <filename> <ISSUE-KEY|feature-slug>",
		Short: "Remove a link between a document and an issue or feature",
		Args:  cobra.ExactArgs(2),
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
			d, err := s.GetDocumentByFilename(repo.ID, args[0], false)
			if err != nil {
				return err
			}
			target, err := resolveDocLinkTarget(s, repo.ID, args[1])
			if err != nil {
				return err
			}
			n, err := s.UnlinkDocument(d.ID, target)
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("no link from %s to %s", d.Filename, args[1])
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "document.unlink", Kind: "document",
				TargetID: &d.ID, TargetLabel: d.Filename,
				Details: "↛ " + args[1],
			})
			return ok("unlinked %s from %s", d.Filename, args[1])
		},
	}
}

// resolveDocLinkTarget interprets the second positional arg as either an
// issue key (e.g. MINI-42) or a feature slug in the given repo.
func resolveDocLinkTarget(s *store.Store, repoID int64, ref string) (store.LinkTarget, error) {
	ref = strings.TrimSpace(ref)
	if isIssueKey(ref) {
		iss, err := resolveIssueByKey(s, ref)
		if err != nil {
			return store.LinkTarget{}, err
		}
		return store.LinkTarget{IssueID: &iss.ID}, nil
	}
	feat, err := s.GetFeatureBySlug(repoID, ref)
	if err != nil {
		return store.LinkTarget{}, fmt.Errorf("%q is not an issue key or feature slug in this repo", ref)
	}
	return store.LinkTarget{FeatureID: &feat.ID}, nil
}
