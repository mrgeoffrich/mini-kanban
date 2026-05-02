package cli

import (
	"fmt"
	"os"
	osuser "os/user"
	"strings"

	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

// actor returns the resolved name for who is performing the operation.
// Resolution order: --user flag → OS username → "unknown".
func actor() string {
	if opts.user != "" {
		return opts.user
	}
	if u, err := osuser.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}

// recordOp writes an audit-log entry. Failures are reported on stderr but
// never fail the user-visible command — losing a log row is preferable to
// rolling back the work the user just asked for.
func recordOp(s *store.Store, e model.HistoryEntry) {
	if e.Actor == "" {
		e.Actor = actor()
	}
	if err := s.RecordHistory(e); err != nil {
		fmt.Fprintln(os.Stderr, "mk: warning: failed to record history:", err)
	}
}

// repoSnapshot fills the repo-related history fields from a model.Repo,
// returning a partially-populated entry the caller can extend.
func repoSnapshot(repo *model.Repo) model.HistoryEntry {
	if repo == nil {
		return model.HistoryEntry{}
	}
	id := repo.ID
	return model.HistoryEntry{
		RepoID:     &id,
		RepoPrefix: repo.Prefix,
	}
}

// updatedFieldList returns "updated <a>,<b>" for a fixed set of "did this
// field get touched?" booleans. Used to summarise patch-style mutations
// in audit entries.
func updatedFieldList(fields map[string]bool) string {
	var parts []string
	for name, touched := range fields {
		if touched {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "updated " + strings.Join(parts, ",")
}
