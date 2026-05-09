package api

import (
	"net/http"

	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

func (d deps) handleFeaturesList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	withDesc := r.URL.Query().Get("with_description")
	includeDesc := withDesc == "true" || withDesc == "1"
	feats, err := d.store.ListFeatures(repo.ID, includeDesc)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if feats == nil {
		feats = []*model.Feature{}
	}
	writeJSON(w, http.StatusOK, feats)
}

func (d deps) handleFeatureShow(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	issues, err := d.store.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &feat.ID})
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if issues == nil {
		issues = []*model.Issue{}
	}
	docs, err := d.store.ListDocumentsLinkedToFeature(feat.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if docs == nil {
		docs = []*model.DocumentLink{}
	}
	writeJSON(w, http.StatusOK, &FeatureView{
		Feature:   feat,
		Issues:    issues,
		Documents: docs,
	})
}
