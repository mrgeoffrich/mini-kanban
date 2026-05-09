package api

import (
	"log/slog"
	"net/http"

	"github.com/mrgeoffrich/mini-kanban/internal/store"
)

// deps is the unexported handler-context bag. Each handler is a method on
// deps so it can reach the store and config without pulling globals.
type deps struct {
	store  *store.Store
	opts   Options
	logger *slog.Logger
}

func newRouter(d deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", d.handleHealthz)

	mux.HandleFunc("GET /schema", d.handleSchemaAll)
	mux.HandleFunc("GET /schema/list", d.handleSchemaList)
	mux.HandleFunc("GET /schema/{name}", d.handleSchemaShow)

	mux.HandleFunc("GET /repos", d.handleReposList)
	mux.HandleFunc("POST /repos", d.handleReposCreate)
	mux.HandleFunc("GET /repos/{prefix}", d.handleReposShow)

	mux.HandleFunc("GET /repos/{prefix}/issues", d.handleIssuesList)
	mux.HandleFunc("POST /repos/{prefix}/issues", d.handleIssueCreate)
	mux.HandleFunc("GET /repos/{prefix}/issues/{key}", d.handleIssueShow)
	mux.HandleFunc("PUT /repos/{prefix}/issues/{key}/state", d.handleIssueState)
	mux.HandleFunc("PUT /repos/{prefix}/issues/{key}/assignee", d.handleIssueAssign)
	mux.HandleFunc("DELETE /repos/{prefix}/issues/{key}/assignee", d.handleIssueUnassign)

	// Outermost first: panic recovery wraps everything so a bug in any
	// later layer still returns a 500 envelope.
	var h http.Handler = mux
	h = bodyCap(h, 4<<20)
	h = auth(h, d.opts.Token)
	h = actorMiddleware(h)
	h = requestLog(h, d.logger)
	h = recoverPanic(h, d.logger)
	return h
}
