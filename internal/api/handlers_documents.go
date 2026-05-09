package api

import (
	"errors"
	"net/http"

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
