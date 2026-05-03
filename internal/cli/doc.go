package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
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
		docAddCmd(), docUpsertCmd(), docListCmd(), docShowCmd(),
		docEditCmd(), docRenameCmd(), docExportCmd(), docRmCmd(),
		docLinkCmd(), docUnlinkCmd(),
	)
	return cmd
}

// docInputs is the resolved (filename, type, body, source_path) tuple that
// `add` and `upsert` operate on, after merging positional / --from-path /
// explicit flags. SourcePath is the value that should be stored on the row;
// it's set when --from-path was used and empty otherwise.
type docInputs struct {
	Filename   string
	Type       model.DocumentType
	Body       string
	SourcePath string
}

// resolveDocInputs derives the filename, type, and content body from the CLI
// surface shared by `mk doc add` and `mk doc upsert`. Explicit flags always
// win over values derived from --from-path; --from-path supplies sensible
// defaults so skills don't have to do path-to-filename translation by hand.
func resolveDocInputs(positional, fromPath, typeStr, content, contentFile string) (*docInputs, error) {
	if positional != "" && fromPath != "" {
		return nil, fmt.Errorf("provide a filename positional OR --from-path, not both")
	}

	var (
		filename string
		err      error
	)
	switch {
	case positional != "":
		filename, err = validateDocFilename(positional)
	case fromPath != "":
		filename, err = validateDocFilename(canonicalDocFilename(fromPath))
	default:
		return nil, fmt.Errorf("provide a filename positional or --from-path")
	}
	if err != nil {
		return nil, err
	}

	var t model.DocumentType
	switch {
	case typeStr != "":
		t, err = model.ParseDocumentType(typeStr)
		if err != nil {
			return nil, err
		}
	case fromPath != "":
		derived, ok := deriveDocTypeFromPath(fromPath)
		if !ok {
			return nil, fmt.Errorf("--type required: cannot derive document type from path %q", fromPath)
		}
		t = derived
	default:
		return nil, fmt.Errorf("--type is required")
	}

	contentFileEffective := contentFile
	if content == "" && contentFile == "" {
		if fromPath == "" {
			return nil, fmt.Errorf("provide --content - (stdin) or --content-file <path>")
		}
		contentFileEffective = fromPath
	}
	body, err := readLongText(content, contentFileEffective, true, "content")
	if err != nil {
		return nil, err
	}
	if !utf8.ValidString(body) {
		return nil, fmt.Errorf("document is not valid UTF-8 text; only text documents are supported")
	}

	sourcePath := ""
	if fromPath != "" {
		sourcePath = canonicalSourcePath(fromPath)
		if err := validateRelativePath(sourcePath); err != nil {
			return nil, fmt.Errorf("--from-path: %w", err)
		}
	}
	return &docInputs{Filename: filename, Type: t, Body: body, SourcePath: sourcePath}, nil
}

// canonicalSourcePath normalises a --from-path value to its repo-relative
// form: trims whitespace, leading "./" / "/", and forward-slash separators.
func canonicalSourcePath(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return p
}

// validateRelativePath rejects paths that escape the repo root or are
// absolute. Used both for --from-path on import and for the resolved
// destination on `mk doc export`.
func validateRelativePath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return fmt.Errorf("path %q must be relative", p)
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("path %q escapes the repo root", p)
	}
	return nil
}

// canonicalDocFilename converts a repo-relative path like
// "docs/planning/not-shipped/foo.md" into "docs-planning-not-shipped-foo.md".
func canonicalDocFilename(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return strings.ReplaceAll(p, "/", "-")
}

// deriveDocTypeFromPath maps a small set of directory conventions to a
// document type. Returns (_, false) when no convention matches and the caller
// must require an explicit --type.
func deriveDocTypeFromPath(p string) (model.DocumentType, bool) {
	p = filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(p), "./"))
	switch {
	case strings.HasPrefix(p, "docs/planning/not-shipped/"):
		return model.DocTypeProjectInPlanning, true
	case strings.HasPrefix(p, "docs/planning/in-progress/"):
		return model.DocTypeProjectInProgress, true
	case strings.HasPrefix(p, "docs/planning/shipped/"):
		return model.DocTypeProjectComplete, true
	}
	return "", false
}

func docAddCmd() *cobra.Command {
	var typeStr, content, contentFile, fromPath string
	cmd := &cobra.Command{
		Use:   "add [filename]",
		Short: "Create a document in the current repo",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pos := ""
			if len(args) == 1 {
				pos = args[0]
			}
			in, err := resolveDocInputs(pos, fromPath, typeStr, content, contentFile)
			if err != nil {
				return err
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
			d, err := s.CreateDocument(repo.ID, in.Filename, in.Type, in.Body, in.SourcePath)
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
	cmd.Flags().StringVar(&fromPath, "from-path", "", "derive filename (and optionally type+content) from a repo-relative path")
	return cmd
}

func docUpsertCmd() *cobra.Command {
	var typeStr, content, contentFile, fromPath string
	cmd := &cobra.Command{
		Use:   "upsert [filename]",
		Short: "Create or update a document (same flag surface as `add`)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pos := ""
			if len(args) == 1 {
				pos = args[0]
			}
			in, err := resolveDocInputs(pos, fromPath, typeStr, content, contentFile)
			if err != nil {
				return err
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
			existing, err := s.GetDocumentByFilename(repo.ID, in.Filename, false)
			if errors.Is(err, store.ErrNotFound) {
				d, err := s.CreateDocument(repo.ID, in.Filename, in.Type, in.Body, in.SourcePath)
				if err != nil {
					return err
				}
				d.Content = ""
				recordOp(s, model.HistoryEntry{
					RepoID: &repo.ID, RepoPrefix: repo.Prefix,
					Op: "document.create", Kind: "document",
					TargetID: &d.ID, TargetLabel: d.Filename,
					Details: "type=" + string(d.Type),
				})
				if opts.output == outputJSON {
					return emit(d)
				}
				return ok("Created %s in %s (type=%s, %d bytes)",
					d.Filename, repo.Prefix, d.Type, d.SizeBytes)
			}
			if err != nil {
				return err
			}
			var newType *model.DocumentType
			if in.Type != existing.Type {
				t := in.Type
				newType = &t
			}
			body := in.Body
			// Refresh source_path on every --from-path upsert so it tracks the
			// most recent import location. Skip when no --from-path was given,
			// to avoid clobbering an existing recorded path.
			var newSource *string
			if in.SourcePath != "" && in.SourcePath != existing.SourcePath {
				sp := in.SourcePath
				newSource = &sp
			}
			if err := s.UpdateDocument(existing.ID, newType, &body, newSource); err != nil {
				return err
			}
			updated, err := s.GetDocumentByID(existing.ID, false)
			if err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "document.update", Kind: "document",
				TargetID: &updated.ID, TargetLabel: updated.Filename,
				Details: updatedFieldList(map[string]bool{
					"type":        newType != nil,
					"content":     true,
					"source_path": newSource != nil,
				}),
			})
			if opts.output == outputJSON {
				return emit(updated)
			}
			return ok("Updated %s in %s (type=%s, %d bytes)",
				updated.Filename, repo.Prefix, updated.Type, updated.SizeBytes)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "document type (e.g. architecture, designs, user-docs)")
	cmd.Flags().StringVar(&content, "content", "", "content text or '-' for stdin")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&fromPath, "from-path", "", "derive filename (and optionally type+content) from a repo-relative path")
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
			if err := s.UpdateDocument(d.ID, newType, newContent, nil); err != nil {
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

func docRenameCmd() *cobra.Command {
	var typeStr string
	cmd := &cobra.Command{
		Use:   "rename <old-filename> <new-filename>",
		Short: "Rename a document, preserving its links (and optionally update its type)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldName, err := validateDocFilename(args[0])
			if err != nil {
				return err
			}
			newName, err := validateDocFilename(args[1])
			if err != nil {
				return err
			}
			if oldName == newName && typeStr == "" {
				return fmt.Errorf("nothing to rename: old and new filenames are identical")
			}
			var newType *model.DocumentType
			if typeStr != "" {
				t, err := model.ParseDocumentType(typeStr)
				if err != nil {
					return err
				}
				newType = &t
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
			d, err := s.GetDocumentByFilename(repo.ID, oldName, false)
			if err != nil {
				return err
			}
			if err := s.RenameDocument(d.ID, newName, newType); err != nil {
				return err
			}
			updated, err := s.GetDocumentByID(d.ID, false)
			if err != nil {
				return err
			}
			details := fmt.Sprintf("%s → %s", oldName, newName)
			if newType != nil {
				details += " type=" + string(*newType)
			}
			recordOp(s, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "document.rename", Kind: "document",
				TargetID: &updated.ID, TargetLabel: updated.Filename,
				Details: details,
			})
			if opts.output == outputJSON {
				return emit(updated)
			}
			return ok("Renamed %s → %s in %s", oldName, newName, repo.Prefix)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "optionally also update the document type")
	return cmd
}

func docExportCmd() *cobra.Command {
	var (
		toPath     bool
		explicitTo string
	)
	cmd := &cobra.Command{
		Use:   "export <filename>",
		Short: "Write a document's content to disk",
		Long: `Write a document's content to disk.

Pass --to-path to use the source path the doc was imported from
(via --from-path on add/upsert), or --to <path> to write to an
explicit path. Parent directories are created as needed and an
existing file at the destination is overwritten.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if toPath && explicitTo != "" {
				return fmt.Errorf("--to-path and --to are mutually exclusive")
			}
			if !toPath && explicitTo == "" {
				return fmt.Errorf("provide --to-path (use stored source_path) or --to <path>")
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
			d, err := s.GetDocumentByFilename(repo.ID, args[0], true)
			if err != nil {
				return err
			}
			var dest string
			if toPath {
				if d.SourcePath == "" {
					return fmt.Errorf("document %s has no source_path; pass --to <path> or re-import via `mk doc upsert --from-path`", d.Filename)
				}
				dest = d.SourcePath
			} else {
				dest = explicitTo
			}
			if err := validateRelativePath(dest); err != nil {
				return err
			}
			repoRoot := repo.Path
			if repoRoot == "" {
				return fmt.Errorf("repo path is unset; cannot resolve export destination")
			}
			absDest := filepath.Join(repoRoot, filepath.FromSlash(dest))
			if err := os.MkdirAll(filepath.Dir(absDest), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(absDest, []byte(d.Content), 0o644); err != nil {
				return err
			}
			return ok("wrote %s (%d bytes) to %s", d.Filename, len(d.Content), absDest)
		},
	}
	cmd.Flags().BoolVar(&toPath, "to-path", false, "write to the doc's stored source_path (the path used with --from-path)")
	cmd.Flags().StringVar(&explicitTo, "to", "", "write to this repo-relative path")
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
