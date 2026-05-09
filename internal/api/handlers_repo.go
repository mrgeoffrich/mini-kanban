package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
	"github.com/mrgeoffrich/mini-kanban/internal/inputio"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

func (d deps) handleReposList(w http.ResponseWriter, r *http.Request) {
	repos, err := d.store.ListRepos()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if repos == nil {
		repos = []*model.Repo{}
	}
	writeJSON(w, http.StatusOK, repos)
}

func (d deps) handleReposShow(w http.ResponseWriter, r *http.Request) {
	prefix := strings.ToUpper(r.PathValue("prefix"))
	repo, err := d.store.GetRepoByPrefix(prefix)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (d deps) handleReposCreate(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.RepoCreateInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "name is required", map[string]any{"field": "name"})
		return
	}
	if strings.TrimSpace(in.Path) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "path is required", map[string]any{"field": "path"})
		return
	}

	var prefix string
	if in.Prefix != "" {
		p, err := store.ValidatePrefix(in.Prefix)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "prefix"})
			return
		}
		prefix = p
	}

	if existing, err := d.store.GetRepoByPath(in.Path); err == nil {
		writeError(w, http.StatusConflict, "conflict",
			"repo already registered for this path",
			map[string]any{"prefix": existing.Prefix, "path": existing.Path})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}

	if prefix != "" {
		if existing, err := d.store.GetRepoByPrefix(prefix); err == nil {
			writeError(w, http.StatusConflict, "conflict",
				"prefix already in use",
				map[string]any{"prefix": existing.Prefix, "path": existing.Path})
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
	} else {
		p, err := d.store.AllocatePrefix(in.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
			return
		}
		prefix = p
	}

	repo, err := d.store.CreateRepo(prefix, in.Name, in.Path, in.RemoteURL)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "repo.create",
		Kind:     "repo",
		TargetID: &repo.ID, TargetLabel: repo.Prefix,
		Details: "api init (" + repo.Name + ")",
	})
	writeJSON(w, http.StatusCreated, repo)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}
