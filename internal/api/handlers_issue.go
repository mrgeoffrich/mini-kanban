package api

import (
	"net/http"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

func (d deps) handleIssuesList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	q := r.URL.Query()
	withDesc := q.Get("with_description")
	f := store.IssueFilter{
		RepoID:             &repo.ID,
		IncludeDescription: withDesc == "true" || withDesc == "1",
	}
	if featureSlug := q.Get("feature"); featureSlug != "" {
		feat, err := d.store.GetFeatureBySlug(repo.ID, featureSlug)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), map[string]any{"field": "feature"})
			return
		}
		f.FeatureID = &feat.ID
	}
	if stateCSV := q.Get("state"); stateCSV != "" {
		for _, raw := range strings.Split(stateCSV, ",") {
			st, err := model.ParseState(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "state"})
				return
			}
			f.States = append(f.States, st)
		}
	}
	if tagCSV := q.Get("tag"); tagCSV != "" {
		clean, err := store.NormalizeTags(strings.Split(tagCSV, ","))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "tag"})
			return
		}
		f.Tags = clean
	}
	issues, err := d.store.ListIssues(f)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if issues == nil {
		issues = []*model.Issue{}
	}
	writeJSON(w, http.StatusOK, issues)
}

func (d deps) handleIssueShow(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	comments, err := d.store.ListComments(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if comments == nil {
		comments = []*model.Comment{}
	}
	rels, err := d.store.ListIssueRelations(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	prs, err := d.store.ListPRs(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if prs == nil {
		prs = []*model.PullRequest{}
	}
	docs, err := d.store.ListDocumentsLinkedToIssue(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if docs == nil {
		docs = []*model.DocumentLink{}
	}
	writeJSON(w, http.StatusOK, &IssueView{
		Issue:        iss,
		Comments:     comments,
		Relations:    rels,
		PullRequests: prs,
		Documents:    docs,
	})
}
