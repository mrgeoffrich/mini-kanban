package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
	"github.com/mrgeoffrich/mini-kanban/internal/inputio"
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

func (d deps) handleIssueCreate(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueAddInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.Title == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "title is required", map[string]any{"field": "title"})
		return
	}
	state := model.StateBacklog
	if in.State != "" {
		st, err := model.ParseState(in.State)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "state"})
			return
		}
		state = st
	}
	var featureID *int64
	if in.FeatureSlug != "" {
		feat, err := d.store.GetFeatureBySlug(repo.ID, in.FeatureSlug)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, fmt.Sprintf("feature %q: %v", in.FeatureSlug, err), map[string]any{"field": "feature_slug"})
			return
		}
		featureID = &feat.ID
	}
	cleanTags, err := store.NormalizeTags(in.Tags)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "tags"})
		return
	}
	if isDryRun(r) {
		projected := &model.Issue{
			RepoID:      repo.ID,
			Number:      repo.NextIssueNumber,
			Key:         fmt.Sprintf("%s-%d", repo.Prefix, repo.NextIssueNumber),
			FeatureID:   featureID,
			FeatureSlug: in.FeatureSlug,
			Title:       in.Title,
			Description: in.Description,
			State:       state,
			Tags:        cleanTags,
		}
		if projected.Tags == nil {
			projected.Tags = []string{}
		}
		writeDryRun(w, http.StatusCreated, projected)
		return
	}
	iss, err := d.store.CreateIssue(repo.ID, featureID, in.Title, in.Description, state, cleanTags)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "issue.create",
		Kind:     "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: iss.Title,
	})
	writeJSON(w, http.StatusCreated, iss)
}

func (d deps) handleIssueState(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueStateInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if in.State == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "state is required", map[string]any{"field": "state"})
		return
	}
	st, err := model.ParseState(in.State)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "state"})
		return
	}
	if isDryRun(r) {
		projected := *iss
		projected.State = st
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	oldState := iss.State
	if err := d.store.SetIssueState(iss.ID, st); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	updated, err := d.store.GetIssueByID(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "issue.state",
		Kind:        "issue",
		TargetID:    &updated.ID,
		TargetLabel: updated.Key,
		Details:     fmt.Sprintf("%s → %s", oldState, st),
	})
	writeJSON(w, http.StatusOK, updated)
}

func (d deps) handleIssueAssign(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueAssignInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	name := strings.TrimSpace(in.Assignee)
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"assignee must be non-empty (use DELETE /issues/{key}/assignee to clear)",
			map[string]any{"field": "assignee"})
		return
	}
	if isDryRun(r) {
		projected := *iss
		projected.Assignee = name
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	old := iss.Assignee
	if err := d.store.SetIssueAssignee(iss.ID, name); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), map[string]any{"field": "assignee"})
		return
	}
	updated, err := d.store.GetIssueByID(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	details := "assigned to " + updated.Assignee
	if old != "" {
		details = fmt.Sprintf("%s → %s", old, updated.Assignee)
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "issue.assign",
		Kind:        "issue",
		TargetID:    &updated.ID,
		TargetLabel: updated.Key,
		Details:     details,
	})
	writeJSON(w, http.StatusOK, updated)
}

func (d deps) handleIssueUnassign(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if iss.Assignee == "" {
		// Matches CLI behaviour: no-op writes no audit row but still returns
		// the (unchanged) issue.
		writeJSON(w, http.StatusOK, iss)
		return
	}
	if isDryRun(r) {
		projected := *iss
		projected.Assignee = ""
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	old := iss.Assignee
	if err := d.store.SetIssueAssignee(iss.ID, ""); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	updated, err := d.store.GetIssueByID(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "issue.assign",
		Kind:        "issue",
		TargetID:    &updated.ID,
		TargetLabel: updated.Key,
		Details:     fmt.Sprintf("%s → (unassigned)", old),
	})
	writeJSON(w, http.StatusOK, updated)
}
