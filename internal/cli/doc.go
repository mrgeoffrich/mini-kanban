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

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

var issueKeyShape = regexp.MustCompile(`^[A-Za-z0-9]{4}-\d+$`)

// isIssueKey reports whether s has the PREFIX-N shape, used to disambiguate
// "issue or feature?" positionals on commands like `mk doc link`.
func isIssueKey(s string) bool { return issueKeyShape.MatchString(s) }

// validateDocFilename runs the strict store-layer validator. Kept as a
// CLI-side wrapper so every entry point (positional flag path, JSON path,
// rename) goes through the same rules and surfaces errors at parse time
// instead of waiting for the SQL write. No silent trimming — leading or
// trailing whitespace is a hard error so an agent that fat-fingered a
// payload sees the problem instead of having it normalised away.
func validateDocFilename(name string) (string, error) {
	return store.ValidateDocFilenameStrict(name)
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

// resolveDocInputsJSON is the JSON-side counterpart of resolveDocInputs.
// Unlike the flag path, it never reads from disk: content must be supplied
// inline. Either filename or source_path is required (not both); type is
// required unless source_path's directory implies one.
func resolveDocInputsJSON(filename, sourcePath, typeStr, content string) (*docInputs, error) {
	if filename != "" && sourcePath != "" {
		return nil, fmt.Errorf("provide filename OR source_path, not both")
	}
	var (
		fname string
		err   error
	)
	switch {
	case filename != "":
		fname, err = validateDocFilename(filename)
	case sourcePath != "":
		fname, err = validateDocFilename(canonicalDocFilename(sourcePath))
	default:
		return nil, fmt.Errorf("provide filename or source_path")
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
	case sourcePath != "":
		derived, ok := deriveDocTypeFromPath(sourcePath)
		if !ok {
			return nil, fmt.Errorf("type is required: cannot derive document type from source_path %q", sourcePath)
		}
		t = derived
	default:
		return nil, fmt.Errorf("type is required")
	}

	if content == "" {
		return nil, fmt.Errorf("content is required")
	}
	if !utf8.ValidString(content) {
		return nil, fmt.Errorf("document is not valid UTF-8 text; only text documents are supported")
	}

	sp := ""
	if sourcePath != "" {
		sp = canonicalSourcePath(sourcePath)
		if err := validateRelativePath(sp); err != nil {
			return nil, fmt.Errorf("source_path: %w", err)
		}
	}
	return &docInputs{Filename: fname, Type: t, Body: content, SourcePath: sp}, nil
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
	var typeStr, content, contentFile, fromPath, rawInput string
	cmd := &cobra.Command{
		Use:   "add [filename]",
		Short: "Create a document in the current repo",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args,
					"type", "content", "content-file", "from-path"); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.DocAddInput](raw)
				if err != nil {
					return err
				}
				resolved, err := resolveDocInputsJSON(in.Filename, in.SourcePath, in.Type, in.Content)
				if err != nil {
					return err
				}
				return createDocument(resolved)
			}
			pos := ""
			if len(args) == 1 {
				pos = args[0]
			}
			resolved, err := resolveDocInputs(pos, fromPath, typeStr, content, contentFile)
			if err != nil {
				return err
			}
			return createDocument(resolved)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "document type (e.g. architecture, designs, user-docs)")
	cmd.Flags().StringVar(&content, "content", "", "content text or '-' for stdin")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&fromPath, "from-path", "", "derive filename (and optionally type+content) from a repo-relative path")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func createDocument(in *docInputs) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	repo, err := resolveRepo(s)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(&model.Document{
			RepoID:     repo.ID,
			Filename:   in.Filename,
			Type:       in.Type,
			SizeBytes:  int64(len(in.Body)),
			SourcePath: in.SourcePath,
		})
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
}

func docUpsertCmd() *cobra.Command {
	var typeStr, content, contentFile, fromPath, rawInput string
	cmd := &cobra.Command{
		Use:   "upsert [filename]",
		Short: "Create or update a document (same flag surface as `add`)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args,
					"type", "content", "content-file", "from-path"); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.DocAddInput](raw)
				if err != nil {
					return err
				}
				resolved, err := resolveDocInputsJSON(in.Filename, in.SourcePath, in.Type, in.Content)
				if err != nil {
					return err
				}
				return upsertDocument(resolved)
			}
			pos := ""
			if len(args) == 1 {
				pos = args[0]
			}
			resolved, err := resolveDocInputs(pos, fromPath, typeStr, content, contentFile)
			if err != nil {
				return err
			}
			return upsertDocument(resolved)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "document type (e.g. architecture, designs, user-docs)")
	cmd.Flags().StringVar(&content, "content", "", "content text or '-' for stdin")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&fromPath, "from-path", "", "derive filename (and optionally type+content) from a repo-relative path")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func upsertDocument(in *docInputs) error {
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
		if opts.dryRun {
			return emitDryRun(&model.Document{
				RepoID:     repo.ID,
				Filename:   in.Filename,
				Type:       in.Type,
				SizeBytes:  int64(len(in.Body)),
				SourcePath: in.SourcePath,
			})
		}
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
	var newSource *string
	if in.SourcePath != "" && in.SourcePath != existing.SourcePath {
		sp := in.SourcePath
		newSource = &sp
	}
	if opts.dryRun {
		projected := *existing
		projected.Type = in.Type
		projected.SizeBytes = int64(len(body))
		if newSource != nil {
			projected.SourcePath = *newSource
		}
		return emitDryRun(&projected)
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
	var raw, metadata bool
	cmd := &cobra.Command{
		Use:   "show <filename>",
		Short: "Show a document's metadata, content, and links",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if raw && metadata {
				return fmt.Errorf("--raw and --metadata are mutually exclusive")
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
			d, err := s.GetDocumentByFilename(repo.ID, args[0], !metadata)
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
	cmd.Flags().BoolVar(&metadata, "metadata", false, "skip the document body (returns type, size, links, and timestamps only)")
	return cmd
}

func docEditCmd() *cobra.Command {
	var (
		typeStr, content, contentFile, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "edit [filename]",
		Short: "Edit a document's type and/or content",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args,
					"type", "content", "content-file"); err != nil {
					return err
				}
				in, present, err := decodeStrict[inputs.DocEditInput](raw)
				if err != nil {
					return err
				}
				if in.Filename == "" {
					return fmt.Errorf("filename is required")
				}
				var (
					newType    *model.DocumentType
					newContent *string
				)
				if _, ok := present["type"]; ok {
					if in.Type == nil || *in.Type == "" {
						return fmt.Errorf("type cannot be empty or null; omit the field to leave it unchanged")
					}
					t, err := model.ParseDocumentType(*in.Type)
					if err != nil {
						return err
					}
					newType = &t
				}
				if _, ok := present["content"]; ok {
					body := ""
					if in.Content != nil {
						body = *in.Content
					}
					if !utf8.ValidString(body) {
						return fmt.Errorf("document is not valid UTF-8 text; only text documents are supported")
					}
					newContent = &body
				}
				return applyDocEdit(in.Filename, newType, newContent)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <filename> positional or --json")
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
			return applyDocEdit(args[0], newType, newContent)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "new type")
	cmd.Flags().StringVar(&content, "content", "", "new content text or '-' for stdin")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "path to a markdown file")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func applyDocEdit(filename string, newType *model.DocumentType, newContent *string) error {
	if newType == nil && newContent == nil {
		return fmt.Errorf("nothing to update; pass type and/or content")
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
	d, err := s.GetDocumentByFilename(repo.ID, filename, false)
	if err != nil {
		return err
	}
	if opts.dryRun {
		projected := *d
		if newType != nil {
			projected.Type = *newType
		}
		if newContent != nil {
			projected.SizeBytes = int64(len(*newContent))
		}
		return emitDryRun(&projected)
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
}

func docRenameCmd() *cobra.Command {
	var typeStr, rawInput string
	cmd := &cobra.Command{
		Use:   "rename [old-filename] [new-filename]",
		Short: "Rename a document, preserving its links (and optionally update its type)",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args, "type"); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.DocRenameInput](raw)
				if err != nil {
					return err
				}
				if in.OldFilename == "" || in.NewFilename == "" {
					return fmt.Errorf("old_filename and new_filename are required")
				}
				return renameDocument(in.OldFilename, in.NewFilename, in.Type)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <old-filename> <new-filename> positionals or --json")
			}
			return renameDocument(args[0], args[1], typeStr)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "optionally also update the document type")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func renameDocument(oldArg, newArg, typeStr string) error {
	oldName, err := validateDocFilename(oldArg)
	if err != nil {
		return err
	}
	newName, err := validateDocFilename(newArg)
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
	if opts.dryRun {
		projected := *d
		projected.Filename = newName
		if newType != nil {
			projected.Type = *newType
		}
		return emitDryRun(&projected)
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
}

func docExportCmd() *cobra.Command {
	var (
		toPath     bool
		explicitTo string
		rawInput   string
	)
	cmd := &cobra.Command{
		Use:   "export [filename]",
		Short: "Write a document's content to disk",
		Long: `Write a document's content to disk.

Pass --to-path to use the source path the doc was imported from
(via --from-path on add/upsert), or --to <path> to write to an
explicit path. Parent directories are created as needed and an
existing file at the destination is overwritten.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args, "to", "to-path"); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.DocExportInput](raw)
				if err != nil {
					return err
				}
				if in.Filename == "" {
					return fmt.Errorf("filename is required")
				}
				if in.ToPath && in.To != "" {
					return fmt.Errorf("provide to OR to_path, not both")
				}
				if !in.ToPath && in.To == "" {
					return fmt.Errorf("provide to (path) or to_path=true (use stored source_path)")
				}
				return exportDocument(in.Filename, in.To, in.ToPath)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <filename> positional or --json")
			}
			if toPath && explicitTo != "" {
				return fmt.Errorf("--to-path and --to are mutually exclusive")
			}
			if !toPath && explicitTo == "" {
				return fmt.Errorf("provide --to-path (use stored source_path) or --to <path>")
			}
			return exportDocument(args[0], explicitTo, toPath)
		},
	}
	cmd.Flags().BoolVar(&toPath, "to-path", false, "write to the doc's stored source_path (the path used with --from-path)")
	cmd.Flags().StringVar(&explicitTo, "to", "", "write to this repo-relative path")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func exportDocument(filename, explicitTo string, toPath bool) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	repo, err := resolveRepo(s)
	if err != nil {
		return err
	}
	d, err := s.GetDocumentByFilename(repo.ID, filename, true)
	if err != nil {
		return err
	}
	var dest string
	if toPath {
		if d.SourcePath == "" {
			return fmt.Errorf("document %s has no source_path; pass an explicit destination or re-import via `mk doc upsert --from-path`", d.Filename)
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
	if opts.dryRun {
		return emitDryRun(&docExportPreview{
			Filename:    d.Filename,
			Destination: absDest,
			Bytes:       len(d.Content),
		})
	}
	if err := os.MkdirAll(filepath.Dir(absDest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(absDest, []byte(d.Content), 0o644); err != nil {
		return err
	}
	return ok("wrote %s (%d bytes) to %s", d.Filename, len(d.Content), absDest)
}

// docExportPreview is the dry-run payload for `mk doc export`. It reports
// the absolute path the body would be written to, plus the byte count, so
// an agent can see whether the destination path is what it expected
// without actually creating any files on disk.
type docExportPreview struct {
	Filename    string `json:"filename"`
	Destination string `json:"destination"`
	Bytes       int    `json:"bytes"`
}

func docRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [filename]",
		Short: "Delete a document (and its links)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.DocRmInput](raw)
				if err != nil {
					return err
				}
				if in.Filename == "" {
					return fmt.Errorf("filename is required")
				}
				return removeDocument(in.Filename)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <filename> positional or --json")
			}
			return removeDocument(args[0])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func removeDocument(filename string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	repo, err := resolveRepo(s)
	if err != nil {
		return err
	}
	d, err := s.GetDocumentByFilename(repo.ID, filename, false)
	if err != nil {
		return err
	}
	if opts.dryRun {
		links, err := s.ListDocumentLinks(d.ID)
		if err != nil {
			return err
		}
		return emitDryRun(&docDeletePreview{
			Document:    d,
			WouldDelete: true,
			LinksRemoved: len(links),
		})
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
}

func docLinkCmd() *cobra.Command {
	var why, rawInput string
	cmd := &cobra.Command{
		Use:   "link [filename] [ISSUE-KEY|feature-slug]",
		Short: "Link a document to an issue or feature (upsert; --why replaces description)",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args, "why"); err != nil {
					return err
				}
				in, _, err := decodeStrict[inputs.DocLinkInput](raw)
				if err != nil {
					return err
				}
				if in.Filename == "" {
					return fmt.Errorf("filename is required")
				}
				ref, err := docLinkTargetFromJSON(in.IssueKey, in.FeatureSlug)
				if err != nil {
					return err
				}
				return linkDocument(in.Filename, ref, in.Description)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <filename> <ISSUE-KEY|feature-slug> positionals or --json")
			}
			return linkDocument(args[0], args[1], why)
		},
	}
	cmd.Flags().StringVar(&why, "why", "", "description of why this document is linked (optional)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func linkDocument(filename, ref, why string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	repo, err := resolveRepo(s)
	if err != nil {
		return err
	}
	d, err := s.GetDocumentByFilename(repo.ID, filename, false)
	if err != nil {
		return err
	}
	target, err := resolveDocLinkTarget(s, repo.ID, ref)
	if err != nil {
		return err
	}
	if opts.dryRun {
		preview := &model.DocumentLink{
			DocumentID:       d.ID,
			DocumentFilename: d.Filename,
			DocumentType:     d.Type,
			Description:      strings.TrimSpace(why),
		}
		if target.IssueID != nil {
			preview.IssueID = target.IssueID
		}
		if target.FeatureID != nil {
			preview.FeatureID = target.FeatureID
		}
		return emitDryRun(preview)
	}
	link, err := s.LinkDocument(d.ID, target, strings.TrimSpace(why))
	if err != nil {
		return err
	}
	recordOp(s, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "document.link", Kind: "document",
		TargetID: &d.ID, TargetLabel: d.Filename,
		Details: "→ " + ref,
	})
	return emit(link)
}

func docUnlinkCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "unlink [filename] [ISSUE-KEY|feature-slug]",
		Short: "Remove a link between a document and an issue or feature",
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
				in, _, err := decodeStrict[inputs.DocUnlinkInput](raw)
				if err != nil {
					return err
				}
				if in.Filename == "" {
					return fmt.Errorf("filename is required")
				}
				ref, err := docLinkTargetFromJSON(in.IssueKey, in.FeatureSlug)
				if err != nil {
					return err
				}
				return unlinkDocument(in.Filename, ref)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <filename> <ISSUE-KEY|feature-slug> positionals or --json")
			}
			return unlinkDocument(args[0], args[1])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func unlinkDocument(filename, ref string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	repo, err := resolveRepo(s)
	if err != nil {
		return err
	}
	d, err := s.GetDocumentByFilename(repo.ID, filename, false)
	if err != nil {
		return err
	}
	target, err := resolveDocLinkTarget(s, repo.ID, ref)
	if err != nil {
		return err
	}
	if opts.dryRun {
		links, err := s.ListDocumentLinks(d.ID)
		if err != nil {
			return err
		}
		matched := 0
		for _, l := range links {
			if target.IssueID != nil && l.IssueID != nil && *l.IssueID == *target.IssueID {
				matched++
			}
			if target.FeatureID != nil && l.FeatureID != nil && *l.FeatureID == *target.FeatureID {
				matched++
			}
		}
		if matched == 0 {
			return fmt.Errorf("no link from %s to %s", d.Filename, ref)
		}
		return emitDryRun(&docUnlinkPreview{
			Filename:    d.Filename,
			Target:      ref,
			WouldRemove: matched,
		})
	}
	n, err := s.UnlinkDocument(d.ID, target)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no link from %s to %s", d.Filename, ref)
	}
	recordOp(s, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "document.unlink", Kind: "document",
		TargetID: &d.ID, TargetLabel: d.Filename,
		Details: "↛ " + ref,
	})
	return ok("unlinked %s from %s", d.Filename, ref)
}

// docDeletePreview is the dry-run payload for `mk doc rm`. LinksRemoved
// counts how many document_links rows would cascade-delete alongside the
// document row itself.
type docDeletePreview struct {
	Document     *model.Document `json:"document"`
	WouldDelete  bool            `json:"would_delete"`
	LinksRemoved int             `json:"links_removed"`
}

// docUnlinkPreview is the dry-run payload for `mk doc unlink`.
type docUnlinkPreview struct {
	Filename    string `json:"filename"`
	Target      string `json:"target"`
	WouldRemove int    `json:"would_remove"`
}

// docLinkTargetFromJSON converts a JSON link payload's (issue_key,
// feature_slug) pair into the single-string reference resolveDocLinkTarget
// expects. Exactly one must be set; the JSON path doesn't accept the
// "shape-discriminated string" the CLI positional uses.
func docLinkTargetFromJSON(issueKey, featureSlug string) (string, error) {
	switch {
	case issueKey != "" && featureSlug != "":
		return "", fmt.Errorf("provide issue_key OR feature_slug, not both")
	case issueKey != "":
		if !isIssueKey(strings.TrimSpace(issueKey)) {
			return "", fmt.Errorf("issue_key %q must be canonical (e.g. \"MINI-42\")", issueKey)
		}
		return strings.TrimSpace(issueKey), nil
	case featureSlug != "":
		return strings.TrimSpace(featureSlug), nil
	default:
		return "", fmt.Errorf("issue_key or feature_slug is required")
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
