package api

import (
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
	"github.com/mrgeoffrich/mini-kanban/internal/inputio"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

// The CLI's filesystem-touching verbs (`mk doc add --from-path`, `mk doc
// export --to-path`) are deliberately not exposed: HTTP can't safely read
// the developer's working tree. Use POST /documents with inline content
// for create-from-path, and GET /documents/{filename}/download for export.

func (d deps) handleDocumentsList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	q := r.URL.Query()
	withContent := q.Get("with_content") == "true" || q.Get("with_content") == "1"
	f := store.DocumentFilter{RepoID: repo.ID}
	if t := q.Get("type"); t != "" {
		dt, err := model.ParseDocumentType(t)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "type"})
			return
		}
		f.Type = &dt
	}
	docs, err := d.store.ListDocuments(f)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if docs == nil {
		docs = []*model.Document{}
	}
	if withContent {
		// ListDocuments deliberately drops content for the list shape; the
		// caller asked for it, so re-fetch each row's body. Mirrors the CLI's
		// `mk doc list -o json` deliberate-lean default.
		for i, row := range docs {
			full, err := d.store.GetDocumentByID(row.ID, true)
			if err != nil {
				status, code := statusForError(err)
				writeError(w, status, code, err.Error(), nil)
				return
			}
			docs[i] = full
		}
	}
	writeJSON(w, http.StatusOK, docs)
}

func (d deps) handleDocumentShow(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	doc, ok := resolveDocumentOnRepo(w, r, d.store, repo, true)
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("with_content") == "false" || q.Get("with_content") == "0" {
		doc.Content = ""
	}
	links, err := d.store.ListDocumentLinks(doc.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if links == nil {
		links = []*model.DocumentLink{}
	}
	writeJSON(w, http.StatusOK, &DocView{Document: doc, Links: links})
}

func (d deps) handleDocumentCreate(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.DocAddInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	resolved, status, code, msg, field := resolveDocCreateInput(*in)
	if msg != "" {
		writeError(w, status, code, msg, fieldDetail(field))
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusCreated, &model.Document{
			RepoID:     repo.ID,
			Filename:   resolved.Filename,
			Type:       resolved.Type,
			SizeBytes:  int64(len(resolved.Body)),
			SourcePath: resolved.SourcePath,
		})
		return
	}
	doc, err := d.store.CreateDocument(repo.ID, resolved.Filename, resolved.Type, resolved.Body, resolved.SourcePath)
	if err != nil {
		if errors.Is(err, store.ErrDocumentExists) {
			writeError(w, http.StatusConflict, "conflict", err.Error(), nil)
			return
		}
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	doc.Content = "" // mirror CLI's `mk doc add`: don't echo body on create.
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "document.create",
		Kind:     "document",
		TargetID: &doc.ID, TargetLabel: doc.Filename,
		Details: "type=" + string(doc.Type),
	})
	writeJSON(w, http.StatusCreated, doc)
}

func (d deps) handleDocumentUpsert(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.DocAddInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	// URL filename always wins on PUT — overwrite whatever the body claimed.
	in.Filename = r.PathValue("filename")
	in.SourcePath = ""
	resolved, status, code, msg, field := resolveDocCreateInput(*in)
	if msg != "" {
		writeError(w, status, code, msg, fieldDetail(field))
		return
	}
	existing, err := d.store.GetDocumentByFilename(repo.ID, resolved.Filename, false)
	if errors.Is(err, store.ErrNotFound) {
		if isDryRun(r) {
			writeDryRun(w, http.StatusOK, &model.Document{
				RepoID:    repo.ID,
				Filename:  resolved.Filename,
				Type:      resolved.Type,
				SizeBytes: int64(len(resolved.Body)),
			})
			return
		}
		doc, err := d.store.CreateDocument(repo.ID, resolved.Filename, resolved.Type, resolved.Body, "")
		if err != nil {
			s, c := statusForError(err)
			writeError(w, s, c, err.Error(), nil)
			return
		}
		recordOp(d.store, d.logger, model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Actor:    ActorFromContext(r.Context()),
			Op:       "document.create",
			Kind:     "document",
			TargetID: &doc.ID, TargetLabel: doc.Filename,
			Details: "type=" + string(doc.Type),
		})
		writeJSON(w, http.StatusOK, doc)
		return
	}
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	var newType *model.DocumentType
	if resolved.Type != existing.Type {
		t := resolved.Type
		newType = &t
	}
	body := resolved.Body
	if isDryRun(r) {
		projected := *existing
		projected.Type = resolved.Type
		projected.SizeBytes = int64(len(body))
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.UpdateDocument(existing.ID, newType, &body, nil); err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	updated, err := d.store.GetDocumentByID(existing.ID, false)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "document.update",
		Kind:     "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
		Details: updatedFieldList(map[string]bool{
			"type":    newType != nil,
			"content": true,
		}),
	})
	writeJSON(w, http.StatusOK, updated)
}

func (d deps) handleDocumentEdit(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	doc, ok := resolveDocumentOnRepo(w, r, d.store, repo, false)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, present, err := inputio.DecodeStrict[inputs.DocEditInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	_ = in.Filename // URL filename always wins; ignore body
	var (
		newType    *model.DocumentType
		newContent *string
	)
	if _, ok := present["type"]; ok {
		if in.Type == nil || *in.Type == "" {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"type cannot be empty or null; omit the field to leave it unchanged",
				map[string]any{"field": "type"})
			return
		}
		t, err := model.ParseDocumentType(*in.Type)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "type"})
			return
		}
		newType = &t
	}
	if _, ok := present["content"]; ok {
		body := ""
		if in.Content != nil {
			body = *in.Content
		}
		if !utf8.ValidString(body) {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"document is not valid UTF-8 text; only text documents are supported",
				map[string]any{"field": "content"})
			return
		}
		newContent = &body
	}
	if newType == nil && newContent == nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "nothing to update", nil)
		return
	}
	if isDryRun(r) {
		projected := *doc
		if newType != nil {
			projected.Type = *newType
		}
		if newContent != nil {
			projected.SizeBytes = int64(len(*newContent))
		}
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.UpdateDocument(doc.ID, newType, newContent, nil); err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	updated, err := d.store.GetDocumentByID(doc.ID, false)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "document.update",
		Kind:     "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
		Details: updatedFieldList(map[string]bool{
			"type":    newType != nil,
			"content": newContent != nil,
		}),
	})
	writeJSON(w, http.StatusOK, updated)
}

func (d deps) handleDocumentRename(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.DocRenameInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	in.OldFilename = r.PathValue("filename") // URL wins
	if in.NewFilename == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "new_filename is required", map[string]any{"field": "new_filename"})
		return
	}
	oldName, err := store.ValidateDocFilenameStrict(in.OldFilename)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "filename"})
		return
	}
	newName, err := store.ValidateDocFilenameStrict(in.NewFilename)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "new_filename"})
		return
	}
	if oldName == newName && in.Type == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "nothing to rename: old and new filenames are identical", nil)
		return
	}
	var newType *model.DocumentType
	if in.Type != "" {
		t, err := model.ParseDocumentType(in.Type)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "type"})
			return
		}
		newType = &t
	}
	doc, err := d.store.GetDocumentByFilename(repo.ID, oldName, false)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "document not found", nil)
			return
		}
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		projected := *doc
		projected.Filename = newName
		if newType != nil {
			projected.Type = *newType
		}
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.RenameDocument(doc.ID, newName, newType); err != nil {
		if errors.Is(err, store.ErrDocumentExists) {
			writeError(w, http.StatusConflict, "conflict", err.Error(), nil)
			return
		}
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	updated, err := d.store.GetDocumentByID(doc.ID, false)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	details := oldName + " → " + newName
	if newType != nil {
		details += " type=" + string(*newType)
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "document.rename",
		Kind:     "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
		Details: details,
	})
	writeJSON(w, http.StatusOK, updated)
}

// resolvedDocCreate is the validated tuple that document create/upsert
// hand off to the store.
type resolvedDocCreate struct {
	Filename   string
	Type       model.DocumentType
	Body       string
	SourcePath string
}

// resolveDocCreateInput validates the JSON payload for create/upsert. Returns
// (resolved, status, code, msg, field). msg is non-empty on failure; the
// caller writes the error envelope so this helper stays HTTP-agnostic.
func resolveDocCreateInput(in inputs.DocAddInput) (*resolvedDocCreate, int, string, string, string) {
	if in.Filename == "" {
		return nil, http.StatusBadRequest, "invalid_input", "filename is required", "filename"
	}
	clean, err := store.ValidateDocFilenameStrict(in.Filename)
	if err != nil {
		return nil, http.StatusBadRequest, "invalid_input", err.Error(), "filename"
	}
	if in.Type == "" {
		return nil, http.StatusBadRequest, "invalid_input", "type is required", "type"
	}
	t, err := model.ParseDocumentType(in.Type)
	if err != nil {
		return nil, http.StatusBadRequest, "invalid_input", err.Error(), "type"
	}
	if in.Content == "" {
		return nil, http.StatusBadRequest, "invalid_input", "content is required", "content"
	}
	if !utf8.ValidString(in.Content) {
		return nil, http.StatusBadRequest, "invalid_input", "document is not valid UTF-8 text; only text documents are supported", "content"
	}
	return &resolvedDocCreate{
		Filename:   clean,
		Type:       t,
		Body:       in.Content,
		SourcePath: in.SourcePath,
	}, 0, "", "", ""
}

func fieldDetail(field string) map[string]any {
	if field == "" {
		return nil
	}
	return map[string]any{"field": field}
}

// resolveDocumentOnRepo pulls {filename} from the URL, validates it, and
// fetches the row scoped to repo. 404 on miss; 400 on a validation
// failure since the URL value can't satisfy any document at that point.
func resolveDocumentOnRepo(w http.ResponseWriter, r *http.Request, s *store.Store, repo *model.Repo, withContent bool) (*model.Document, bool) {
	name := r.PathValue("filename")
	clean, err := store.ValidateDocFilenameStrict(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "filename"})
		return nil, false
	}
	doc, err := s.GetDocumentByFilename(repo.ID, clean, withContent)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "document not found", nil)
			return nil, false
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return nil, false
	}
	return doc, true
}
