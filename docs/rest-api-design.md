# mk REST API — design plan

Status: **proposal / planning** (no code yet)
Owner: this branch (`claude/add-rest-api-mode-R4zJ3`)
Companion docs: [agent-cli-principles.md](agent-cli-principles.md), [agent-cli-redesign.md](agent-cli-redesign.md)

## 1. Goal

Add a second surface — a local HTTP REST API — that exposes the same operations the CLI already supports, backed by the same SQLite store, validators, audit log, and JSON input/output schemas. Launch it via:

```
mk api [--addr 127.0.0.1:7777] [--token <secret>] [--db <path>]
```

The CLI stays the primary surface; the API is for callers that aren't a child process (web UIs, IDE plugins, long-running agents, future MCP server bridge). Both surfaces MUST stay functionally equivalent — anything you can do via `mk issue add --json` you can do via `POST /repos/{prefix}/issues`, and vice versa.

This is the implementation of pending principle **#7 Multi-surface architecture** in `docs/agent-cli-principles.md`.

## 2. Non-goals

- **Not a public/internet-facing service.** Default bind is `127.0.0.1`; auth model assumes loopback or trusted LAN. No TLS termination, no rate limiting, no CORS gymnastics in v1.
- **No new domain features.** This is a transport layer. New verbs land in the CLI first (where the schema/dry-run/audit conventions live) and the API picks them up automatically when we follow §6.
- **No streaming / NDJSON / WebSockets.** Same rationale as the CLI rule #20: largest realistic response is one `issue brief`. Long-poll for history changes is a v2+ idea.
- **No multi-tenant auth.** One process, one token, one operator. Per-user accounts are out of scope.
- **Not a backwards-compat shim for some other tracker's API.** mk's verbs are mk's verbs.

## 3. Guiding principles

The same six rules that govern the CLI apply, with HTTP-shaped corollaries:

| CLI rule | API expression |
|---|---|
| 1. Mutations accept JSON via `--json` | All mutating HTTP methods take `Content-Type: application/json` bodies that decode into the **same `inputs.*Input` structs** the CLI uses. Strict-decode (`DisallowUnknownFields`) is preserved. |
| 2. Schemas published at runtime | `GET /schema`, `GET /schema/{name}` mirror `mk schema all` / `mk schema show`. Same `schemaRegistry`, same JSON Schema draft 2020-12 output. |
| 3. Lists lean by default | List endpoints strip `description` / `content` unless `?with_description=true` (issues/features) or `?with_content=true` (docs). Identical to CLI flags. |
| 4. Validators run at the store boundary | API handlers call the same store methods. No re-implemented validation in the HTTP layer. |
| 5. Every mutation has `--dry-run` | Every mutating endpoint accepts `?dry_run=true` (or `X-Dry-Run: 1` header) and returns the projected entity with `X-Dry-Run: applied` on the response. No history entry. |
| 6. SKILL.md is the agent reference | Add an "HTTP API" section to `SKILL.md` mirroring the CLI quick-start: discover via `/schema`, compose via JSON body, rehearse via `?dry_run`, execute with `X-Actor: <name>`, query lean. |

## 4. Architecture

### 4.1 Process model

`mk api` is a single foreground process. SIGINT/SIGTERM trigger graceful shutdown via `http.Server.Shutdown` with a 5s deadline. The store is opened once at boot and shared by all handlers (SQLite WAL handles concurrent reads; the existing store API is already goroutine-safe via `database/sql`'s connection pool).

```
cmd/mk/main.go
  └─ internal/cli/api.go              # cobra wiring for `mk api`
       └─ internal/api/server.go       # NewServer(store, opts) -> *http.Server
            ├─ internal/api/router.go  # routes(mux, deps)
            ├─ internal/api/middleware.go
            ├─ internal/api/handlers_issue.go
            ├─ internal/api/handlers_feature.go
            ├─ internal/api/handlers_doc.go
            ├─ internal/api/handlers_repo.go
            ├─ internal/api/handlers_history.go
            ├─ internal/api/handlers_schema.go
            ├─ internal/api/handlers_health.go
            ├─ internal/api/audit.go    # mirror of cli/audit.go (recordOp, actor)
            └─ internal/api/errors.go   # error → status mapping
```

Reusing as much CLI plumbing as possible:

- `internal/cli/inputs/*` — used **directly** by API handlers. No duplication.
- `internal/store` — used directly. The validators that already run at the store boundary are exactly what we want.
- `internal/cli.schemaRegistry` — exposed via a small package-level getter so the API can serve the same schemas the CLI does. *Refactor note: pull `schemaRegistry` + `buildSchema` out of `internal/cli/schema.go` into a new `internal/schema` package so neither surface depends on the other.* See §11.
- `recordOp` / `actor()` from `internal/cli/audit.go` — replicated in `internal/api/audit.go` rather than imported, because the CLI version reads `opts.user` (a CLI global). The API version reads from per-request context. Both call `s.RecordHistory(...)` the same way.

### 4.2 Routing

Standard library `net/http` with Go 1.22's pattern matching (`mux.HandleFunc("POST /path/{var}", ...)`). No third-party router; the codebase already has zero non-CLI runtime deps it doesn't need.

### 4.3 Auth

- **Loopback-only by default.** `--addr` defaults to `127.0.0.1:7777`. Binding to `0.0.0.0` requires the operator to opt in by passing the explicit address.
- **Optional bearer token.** `--token <secret>` (or env `MK_API_TOKEN`) enables `Authorization: Bearer <token>` checks. When unset, requests are accepted without an Authorization header (matches the "trust the loopback" posture). When set, missing/wrong token → `401`.
- **No cookies, no sessions, no refresh.** Bearer is the only mechanism. Easy to integrate from `curl`, `httpx`, or a browser fetch.
- The token is compared with `subtle.ConstantTimeCompare`.

### 4.4 Actor identity

The CLI stamps every history row with `--user <name>`. The API needs the same:

1. `X-Actor: <name>` request header — primary mechanism, validated with `store.ValidateActor` before any work.
2. Falls back to `"api"` if absent (analogous to the CLI's "OS user" fallback). **Not** the OS username of the server process — that would be misleading.
3. Reject malformed `X-Actor` early (in middleware) with `400` and an error body, same as the CLI's `validateActorFlag`.

Tokens and actor are independent: one shared token can serve many distinct actors. (We're not building IAM; we're building "who do I write in the audit row".)

### 4.5 Repo selection

The CLI auto-detects the repo from CWD. The API can't — there's no implicit working directory per request. Two options were considered:

- **A. Path-prefix routing (`/repos/{prefix}/...`)** — explicit, RESTful, makes "current repo" impossible to forget.
- **B. Header (`X-Repo: MINI`)** — flatter URLs but easy to omit.

**Decision: A.** Every repo-scoped resource lives under `/repos/{prefix}/...`. Cross-repo reads (history, repo list) live at the top level. Flat URLs aren't worth the foot-gun.

`mk init`'s auto-create-on-first-use behaviour cannot apply here (no CWD). The API requires repos to already exist; create them via `POST /repos` with an explicit `{prefix, name, path, remote_url?}` body, or by running `mk init` once in the relevant working tree.

## 5. URL surface (v1)

All bodies + responses are `application/json`. `{prefix}` is the 4-char repo prefix uppercased. `{key}` is the canonical `PREFIX-N` issue key. Mutating endpoints accept `?dry_run=true`.

### 5.1 Meta

| Method | Path | Maps to |
|---|---|---|
| `GET`  | `/healthz`              | (new) — returns `{ok:true,version}` |
| `GET`  | `/schema`               | `mk schema all` |
| `GET`  | `/schema/{name}`        | `mk schema show <name>` |
| `GET`  | `/schema/list`          | `mk schema list` |

### 5.2 Repos

| Method | Path | Maps to |
|---|---|---|
| `GET`    | `/repos`              | `mk repo list` |
| `POST`   | `/repos`              | `mk init` (explicit form) — body `{prefix?, name, path, remote_url?}` |
| `GET`    | `/repos/{prefix}`     | `mk repo show <PREFIX>` |

### 5.3 Features

| Method | Path | Maps to |
|---|---|---|
| `GET`    | `/repos/{prefix}/features`              | `mk feature list` (`?with_description`) |
| `POST`   | `/repos/{prefix}/features`              | `mk feature add` (`inputs.FeatureAddInput`) |
| `GET`    | `/repos/{prefix}/features/{slug}`       | `mk feature show <slug>` |
| `PATCH`  | `/repos/{prefix}/features/{slug}`       | `mk feature edit` (`inputs.FeatureEditInput`; `slug` from URL) |
| `DELETE` | `/repos/{prefix}/features/{slug}`       | `mk feature rm` |
| `POST`   | `/repos/{prefix}/features/{slug}/plan`  | `mk feature plan <slug>` (read-only; POST kept to mirror the existing CLI command's compute-not-pure semantics — alternatively `GET`) |
| `POST`   | `/repos/{prefix}/features/{slug}/next`  | `mk issue next --feature <slug>` (mutating: claims) |
| `GET`    | `/repos/{prefix}/features/{slug}/next`  | `mk issue peek --feature <slug>` (read-only) |

### 5.4 Issues

| Method | Path | Maps to |
|---|---|---|
| `GET`    | `/repos/{prefix}/issues`                  | `mk issue list` (`?state`,`?feature`,`?tag`,`?with_description`) |
| `POST`   | `/repos/{prefix}/issues`                  | `mk issue add` (`inputs.IssueAddInput`) |
| `GET`    | `/repos/{prefix}/issues/{key}`            | `mk issue show <KEY>` |
| `GET`    | `/repos/{prefix}/issues/{key}/brief`      | `mk issue brief <KEY>` (`?no_feature_docs`,`?no_comments`,`?no_doc_content`) |
| `PATCH`  | `/repos/{prefix}/issues/{key}`            | `mk issue edit` (`inputs.IssueEditInput`; key from URL takes precedence) |
| `DELETE` | `/repos/{prefix}/issues/{key}`            | `mk issue rm` |
| `PUT`    | `/repos/{prefix}/issues/{key}/state`      | `mk issue state` — body `{state}` |
| `PUT`    | `/repos/{prefix}/issues/{key}/assignee`   | `mk issue assign` — body `{assignee}` |
| `DELETE` | `/repos/{prefix}/issues/{key}/assignee`   | `mk issue unassign` |

### 5.5 Comments / relations / PRs / tags

| Method | Path | Maps to |
|---|---|---|
| `GET`    | `/repos/{prefix}/issues/{key}/comments`         | `mk comment list <KEY>` |
| `POST`   | `/repos/{prefix}/issues/{key}/comments`         | `mk comment add` (`inputs.CommentAddInput`) |
| `POST`   | `/repos/{prefix}/relations`                     | `mk link` (`inputs.LinkInput`) |
| `DELETE` | `/repos/{prefix}/relations`                     | `mk unlink` (`inputs.UnlinkInput` via body — a/b not in URL) |
| `POST`   | `/repos/{prefix}/issues/{key}/pull-requests`    | `mk pr attach` |
| `DELETE` | `/repos/{prefix}/issues/{key}/pull-requests`    | `mk pr detach` (URL via body or `?url=`) |
| `POST`   | `/repos/{prefix}/issues/{key}/tags`             | `mk tag add` |
| `DELETE` | `/repos/{prefix}/issues/{key}/tags`             | `mk tag rm` (tags via body) |

### 5.6 Documents

| Method | Path | Maps to |
|---|---|---|
| `GET`    | `/repos/{prefix}/documents`                                  | `mk doc list` |
| `POST`   | `/repos/{prefix}/documents`                                  | `mk doc add` |
| `PUT`    | `/repos/{prefix}/documents/{filename}`                       | `mk doc upsert` (idempotent create-or-replace) |
| `GET`    | `/repos/{prefix}/documents/{filename}`                       | `mk doc show` (`?with_content=false` to drop body) |
| `PATCH`  | `/repos/{prefix}/documents/{filename}`                       | `mk doc edit` |
| `DELETE` | `/repos/{prefix}/documents/{filename}`                       | `mk doc rm` |
| `POST`   | `/repos/{prefix}/documents/{filename}/rename`                | `mk doc rename` |
| `POST`   | `/repos/{prefix}/documents/{filename}/links`                 | `mk doc link` (issue or feature in body) |
| `DELETE` | `/repos/{prefix}/documents/{filename}/links`                 | `mk doc unlink` |
| `POST`   | `/repos/{prefix}/documents/{filename}/export`                | `mk doc export` (writes to disk on the server) |

`mk doc export` is unusual because it writes to the server's filesystem. v1 keeps it (it works for local-only use), but the API explicitly documents that the path is server-side. Future remote deploy would gate this behind a config flag.

### 5.7 History

| Method | Path | Maps to |
|---|---|---|
| `GET`  | `/repos/{prefix}/history`  | `mk history` (single repo) |
| `GET`  | `/history`                 | `mk history --all-repos` |

Filters: `?limit=`, `?offset=`, `?op=`, `?kind=`, `?actor=`, `?since=`, `?from=`, `?to=`, `?oldest_first=true`. Same parsing as the CLI (reuse `parseLookback` / `parseTimestamp`, also moved into a shared helper pkg per §11).

## 6. Request / response shape

### 6.1 Mutations: same input structs as the CLI

```http
POST /repos/MINI/issues
Content-Type: application/json
Authorization: Bearer <token>            # if --token configured
X-Actor: agent-alice

{
  "title": "Pin tab strip in place",
  "feature_slug": "tui-polish",
  "description": "Body height should clip the tab strip…",
  "state": "todo",
  "tags": ["ui","tui"]
}
```

Decode path:

```go
var in inputs.IssueAddInput
dec := json.NewDecoder(r.Body)
dec.DisallowUnknownFields()
if err := dec.Decode(&in); err != nil { ...400 }
// from here on, identical to runIssueAddJSON in internal/cli/issue.go
```

PATCH endpoints reuse the same pointer-field-with-presence-map technique the CLI's `decodeStrict` provides. Pull `decodeStrict` out of `internal/cli/jsoninput.go` and into `internal/inputio` (or similar) so both surfaces share it.

### 6.2 Reads

Match what `emit()` returns in JSON mode today, byte-for-byte. The existing models (`*model.Issue`, `*model.Feature`, `*issueView`, `*featureView`, `*briefDoc`, etc.) already carry the correct `json:` tags. No reshaping.

Lists return JSON arrays — **not** `{items: [...]}` wrappers. This matches what `mk issue list -o json` already emits today.

### 6.3 Errors

Single shape, modelled on RFC 7807-lite:

```json
{
  "error": "title is required",
  "code": "invalid_input",
  "details": {"field":"title"}
}
```

Status mapping:

| Condition | Status | Code |
|---|---|---|
| Malformed JSON, unknown field, validator failure | `400` | `invalid_input` |
| Missing/invalid bearer | `401` | `unauthorized` |
| Authenticated but op forbidden (future) | `403` | `forbidden` |
| `store.ErrNotFound` | `404` | `not_found` |
| Conflict (duplicate slug, prefix, link) | `409` | `conflict` |
| Internal | `500` | `internal` |

Conflict detection: catch the `UNIQUE` constraint violation pattern that surfaces from `modernc.org/sqlite` and translate to 409. Implement once in `internal/api/errors.go`.

### 6.4 Dry-run

`POST /repos/MINI/issues?dry_run=true` returns the same body shape the real call would, but no row is written and no history entry is recorded. Response carries `X-Dry-Run: applied`. The CLI's `emitDryRun` writes to stderr; the API surfaces the same signal via response header rather than a side channel.

### 6.5 Pagination

History is the only realistic candidate for v1. Use `?limit=` + `?offset=` matching the CLI. No cursor pagination yet. Issue list pagination can come later if a real consumer asks for it.

## 7. Concurrency & transactions

- The store is goroutine-safe via the existing `database/sql` pool.
- `CreateIssue` already uses a transaction internally (number bump + insert). Nothing to change there.
- HTTP handlers are stateless — no per-request store handles, no caches.
- Server enforces `ReadHeaderTimeout` (5s), `ReadTimeout` (15s), `WriteTimeout` (30s — `issue brief` with large doc bodies is the slowest legitimate read), `IdleTimeout` (60s). Body size capped via `http.MaxBytesReader(r, 4 MiB)` on every mutating handler — generous enough for the 1 MiB body cap in `validate.go` plus envelope.

## 8. Observability

- Structured request log to stderr: `method path status duration actor`. Drop-in with `log/slog` (stdlib).
- No metrics endpoint in v1. (`/metrics` Prometheus would be a v2 add.)
- The existing audit log IS the user-visible activity log for mutations. Reads aren't audited (matches CLI behaviour).

## 9. Tests

The repo currently has zero `*_test.go` files outside one in `internal/store`. v1 adds:

- `internal/api/server_test.go` — table-driven HTTP-level tests using `httptest.NewServer` against an in-memory SQLite (`?file=:memory:&cache=shared`).
- One round-trip test per CLI verb: same payload via `--json` and via the API yields the same store mutation + same audit row.
- Auth path tests: missing token, wrong token, valid token.
- Dry-run tests: response shape matches the real call; no `history` row written.

## 10. Phased delivery

The CLI surface is ~30 verbs. Doing them all in one PR is too much to review well. Suggested split:

1. **PR 1 — scaffolding.** `mk api` command, server boot/shutdown, `/healthz`, `/schema*`, `/repos`, auth middleware, error model, request log, tests. **No** mutation endpoints yet.
2. **PR 2 — issues + comments.** `/repos/{prefix}/issues*`, `/comments`, `/tags`, `/relations`, `/pull-requests`. The bulk of agent-driving traffic.
3. **PR 3 — features + brief + claim.** `/features*`, `/issues/{key}/brief`, claim/peek.
4. **PR 4 — documents + history.** `/documents*`, `/history`.
5. **PR 5 — SKILL.md update + agent quick-start examples.** Documents the API surface, mirrors the CLI's "discover/compose/rehearse/execute/query" rhythm. Update README.md.

Each PR is independently shippable — partial coverage is fine because the CLI still covers everything.

## 11. Refactors prerequisite to PR 1

To avoid duplicating logic, move these out of `internal/cli` into shared packages first:

- `schemaRegistry`, `buildSchema`, `findSchema` → `internal/schema`. `internal/cli/schema.go` becomes a thin cobra wrapper.
- `decodeStrict` → `internal/inputio`. Used by both surfaces.
- `parseLookback`, `parseTimestamp` → `internal/timeparse`. Used by `mk history` and `GET /history`.
- `recordOp` and the `model.HistoryEntry` builder helpers → keep in `internal/cli/audit.go`, **and** add a sibling `internal/api/audit.go` that diverges only in how the actor is resolved. (The two are short enough that aliasing them isn't worth the indirection.)

These are mechanical moves. They land in PR 1 alongside the scaffolding so the API package can `import "github.com/mrgeoffrich/mini-kanban/internal/schema"` cleanly.

## 12. Open questions

1. **Default port.** 7777 is unassigned and memorable. Confirm or pick another (8087, 9876).
2. **Persistent token store.** If the operator wants the token to survive restarts, do we read it from `~/.mini-kanban/api-token` on first boot? v1 says no — operators set `MK_API_TOKEN` themselves or pass `--token`. Revisit if friction shows up.
3. **Auto-start on `mk tui`.** Out of scope. Future work where the TUI can talk to a remote `mk api` instead of a local SQLite file is possible but is a much bigger refactor.
4. **OpenAPI doc generation.** We already publish JSON Schemas per input; full OpenAPI 3.1 is reflectable from the same registry plus the route table. Defer to a later PR — `mk schema all` already covers the agent use case.
5. **Filesystem-touching endpoints (`doc export`, `doc add --from-path`).** Keep server-local v1; revisit if a remote-deploy use case appears.

## 13. Definition of done (for the whole effort, not PR 1)

- `mk api --addr 127.0.0.1:7777 --token T` boots, accepts the full route table in §5, and survives a SIGTERM cleanly.
- For every mutating CLI verb there is an equivalent HTTP endpoint that takes the same `inputs.*Input` JSON and produces the same store row + history entry.
- `GET /schema/<name>` returns byte-identical JSON to `mk schema show <name>`.
- `?dry_run=true` writes nothing to the DB on every mutating endpoint.
- `SKILL.md` has an "HTTP API" section that is self-sufficient for an agent that has never touched the CLI.
- `go test ./...` passes including the new HTTP-level tests.
- README.md mentions `mk api` in the "Quick start" / "AI-agent integration" sections.
