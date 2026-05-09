# mk CLI client mode (Phase 6)

Status: **in progress** — Phase 6 of the REST API addition.
Branch: `claude/add-rest-api-mode-R4zJ3`.
Companion docs: [rest-api-design.md](rest-api-design.md), [agent-cli-principles.md](agent-cli-principles.md).

## 1. Goal

The CLI today talks to a local SQLite file via `internal/store`. Phase 6
re-targets every retargetable verb through a `Client` interface with two
backends — **local** (SQLite, what we ship today) and **remote** (HTTP
against `mk api`) — selected per invocation:

```
mk issue add "Fix login" --feature auth                                   # local SQLite (today)
MK_REMOTE=http://team-mk:5320 mk issue add "Fix login" --feature auth     # POSTs to /repos/{prefix}/issues
mk --remote http://team-mk:5320 issue list                                # GETs /repos/{prefix}/issues
```

Default behaviour is unchanged — no `--remote` / `MK_REMOTE` ⇒ local mode.
The local-first promise stays intact for solo users.

## 2. Architecture

```
┌──────────────────────────────────────────────────────┐
│                  cmd/mk/main.go                       │
│                       ↓                               │
│              internal/cli (cobra)                     │
│                       ↓                               │
│      cli.openClient()  ─────► reads opts.remote,      │
│                                MK_REMOTE, --token,    │
│                                MK_API_TOKEN           │
│                       ↓                               │
│              client.Open(ctx, Options) ─►   ┌─────────┐
│                                              │ Client  │
│                                              │interface│
│                                              └────┬────┘
│                                                   │
│                          ┌────────────────────────┼─────────────────────────┐
│                          ↓                        ↓                          │
│                   localClient                remoteClient                    │
│                  (in-process)               (over net/http)                  │
│                          │                        │                          │
│                  ┌───────┴────────┐               ↓                          │
│                  ↓                ↓        ┌─────────────┐                   │
│           internal/store    internal/cli   │   mk api    │                   │
│                              audit.go      │  (server)   │                   │
│           SQLite WAL                       └──────┬──────┘                   │
│                                                   ↓                          │
│                                            internal/api ─► internal/store    │
└──────────────────────────────────────────────────────────────────────────────┘
```

Both backends ultimately mutate the same kind of `*store.Store`. The local
one talks to it directly; the remote one delegates to a server which is
itself a `*store.Store` consumer. Audit-log writes happen inside the
local backend (mirroring what CLI handlers do today inline) and inside
the API server (where `internal/api/audit.go` already does this); the
remote client never writes audit rows itself.

## 3. The `Client` interface

```go
package client

type Options struct {
    DBPath string  // local backend: ~/.mini-kanban/db.sqlite by default
    Remote string  // empty → local; "http://..." → remote
    Token  string  // bearer for remote backend
    Actor  string  // resolved actor name; same value local + remote
}

func Open(ctx context.Context, opts Options) (Client, error)

type Client interface {
    Close() error
    Mode() string                          // "local" or "remote"

    // Repos
    ListRepos(ctx) ([]*model.Repo, error)
    GetRepoByPrefix(ctx, prefix string) (*model.Repo, error)
    GetRepoByPath(ctx, path string) (*model.Repo, error)
    CreateRepo(ctx, prefix, name, path, remoteURL string, dryRun bool) (*model.Repo, error)
    AllocatePrefix(ctx, candidate string) (string, error)
    EnsureRepo(ctx, info *git.Info) (repo *model.Repo, created bool, err error)  // auto-register helper

    // Features
    ListFeatures(ctx, repoID int64, withDescription bool) ([]*model.Feature, error)
    GetFeatureBySlug(ctx, repoID int64, slug string) (*model.Feature, error)
    GetFeatureByID(ctx, id int64) (*model.Feature, error)
    CreateFeature(ctx, repo *model.Repo, in inputs.FeatureAddInput, dryRun bool) (*model.Feature, error)
    UpdateFeature(ctx, repo *model.Repo, slug string, title, description *string, dryRun bool) (*model.Feature, error)
    DeleteFeature(ctx, repo *model.Repo, slug string, dryRun bool) (deleted *model.Feature, preview *FeatureDeletePreview, err error)
    PlanFeature(ctx, repo *model.Repo, slug string) (*PlanView, error)

    // Issues
    ListIssues(ctx, f IssueFilter) ([]*model.Issue, error)
    GetIssueByKey(ctx, prefix string, n int64) (*model.Issue, error)
    GetIssueByID(ctx, id int64) (*model.Issue, error)
    ShowIssue(ctx, repo *model.Repo, key string) (*IssueView, error)
    BriefIssue(ctx, repo *model.Repo, key string, opts BriefOptions) (*IssueBrief, error)
    CreateIssue(ctx, repo *model.Repo, in inputs.IssueAddInput, dryRun bool) (*model.Issue, error)
    UpdateIssue(ctx, repo *model.Repo, key string, edit IssueEdit, dryRun bool) (*model.Issue, error)
    SetIssueState(ctx, repo *model.Repo, key string, state model.State, dryRun bool) (*model.Issue, error)
    AssignIssue(ctx, repo *model.Repo, key, name string, dryRun bool) (*model.Issue, error)
    UnassignIssue(ctx, repo *model.Repo, key string, dryRun bool) (*model.Issue, error)
    DeleteIssue(ctx, repo *model.Repo, key string, dryRun bool) (deleted *model.Issue, preview *IssueDeletePreview, err error)
    PeekNextIssue(ctx, repo *model.Repo, slug string) (*model.Issue, error)
    ClaimNextIssue(ctx, repo *model.Repo, slug string, dryRun bool) (*model.Issue, error)

    // Comments
    ListComments(ctx, repo *model.Repo, key string) ([]*model.Comment, error)
    AddComment(ctx, repo *model.Repo, in inputs.CommentAddInput, dryRun bool) (*model.Comment, error)

    // Relations
    LinkRelation(ctx, repo *model.Repo, in inputs.LinkInput, dryRun bool) (*model.Relation, error)
    UnlinkRelation(ctx, repo *model.Repo, in inputs.UnlinkInput, dryRun bool) (preview *RelationDeletePreview, removed int64, err error)

    // Pull requests
    ListPRs(ctx, repo *model.Repo, key string) ([]*model.PullRequest, error)
    AttachPR(ctx, repo *model.Repo, key, url string, dryRun bool) (*model.PullRequest, error)
    DetachPR(ctx, repo *model.Repo, key, url string, dryRun bool) (preview *PRDetachPreview, removed int64, err error)

    // Tags
    AddTags(ctx, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error)
    RemoveTags(ctx, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error)

    // Documents
    ListDocuments(ctx, repo *model.Repo, typeStr string) ([]*model.Document, error)
    GetDocument(ctx, repo *model.Repo, filename string, withContent bool) (*DocView, error)
    DownloadDocument(ctx, repo *model.Repo, filename string) (filename string, body []byte, err error)
    CreateDocument(ctx, repo *model.Repo, filename, typeStr, content, sourcePath string, dryRun bool) (*model.Document, error)
    UpsertDocument(ctx, repo *model.Repo, filename, typeStr, content, sourcePath string, dryRun bool) (*model.Document, error)
    EditDocument(ctx, repo *model.Repo, filename string, newType *string, newContent *string, dryRun bool) (*model.Document, error)
    RenameDocument(ctx, repo *model.Repo, oldName, newName, typeStr string, dryRun bool) (*model.Document, error)
    DeleteDocument(ctx, repo *model.Repo, filename string, dryRun bool) (preview *DocumentDeletePreview, err error)
    LinkDocument(ctx, repo *model.Repo, in inputs.DocLinkInput, dryRun bool) (*model.DocumentLink, error)
    UnlinkDocument(ctx, repo *model.Repo, in inputs.DocUnlinkInput, dryRun bool) (preview *DocumentUnlinkPreview, removed int64, err error)

    // History
    ListHistory(ctx, f HistoryFilter) ([]*model.HistoryEntry, error)
}
```

The `Client` interface mirrors the CLI's needs, not `internal/store`'s
exact surface. It hides:

- audit-log stamping (local backend writes; remote backend doesn't)
- dry-run projection (local replicates the existing in-memory copy logic;
  remote sets `?dry_run=true` and trusts the server's projection)
- HTTP error envelope translation into Go errors

Where the interface needs view shapes the API server already publishes
(`PlanView`, `IssueBrief`, `IssueView`, `FeatureDeletePreview`,
`IssueDeletePreview`, `RelationDeletePreview`, `PRDetachPreview`, `DocView`,
`DocumentDeletePreview`, `DocumentUnlinkPreview`), the `internal/client`
package re-defines them to avoid an `internal/cli` ⇒ `internal/api`
import cycle. They are JSON-byte-for-byte identical so the CLI's
existing rendering logic (which expects the `internal/cli` types) keeps
working — the CLI rebinds its existing struct definitions to the client
package's via simple value copy.

## 4. Verb mapping

`X-Actor`, `Authorization`, and `?dry_run=true` are added by the remote
client when relevant; the CLI handler doesn't think about them.

Bare-number issue references (`mk issue state 42 todo`) and `--feature`
auto-detect-current-repo flows are all resolved on the CLI side using
the resolved repo from `EnsureRepo` / `GetRepoByPath`. Once a repo is in
hand, the canonical `PREFIX-N` form is sent everywhere.

| CLI verb | Local client method | HTTP verb + path | Audit op | Local-only flags |
|---|---|---|---|---|
| `mk repo list` | `ListRepos` | `GET /repos` | (read) | — |
| `mk repo show` | `GetRepoByPrefix` / `EnsureRepo` | `GET /repos/{prefix}` | (read) | — |
| `mk init` | LOCAL ONLY | n/a | — | whole verb |
| `mk install-skill` | LOCAL ONLY | n/a | — | whole verb |
| `mk feature add` | `CreateFeature` | `POST /repos/{prefix}/features` | `feature.create` | — |
| `mk feature list` | `ListFeatures` | `GET /repos/{prefix}/features` | (read) | — |
| `mk feature show` | local: store-direct join; remote: `GET /features/{slug}` | `GET /repos/{prefix}/features/{slug}` | (read) | — |
| `mk feature edit` | `UpdateFeature` | `PATCH /repos/{prefix}/features/{slug}` | `feature.update` | — |
| `mk feature rm` | `DeleteFeature` | `DELETE /repos/{prefix}/features/{slug}` | `feature.delete` | — |
| `mk feature plan` | `PlanFeature` | `GET /repos/{prefix}/features/{slug}/plan` | (read) | — |
| `mk issue add` | `CreateIssue` | `POST /repos/{prefix}/issues` | `issue.create` | — |
| `mk issue list` | `ListIssues` | `GET /repos/{prefix}/issues` | (read) | `--all-repos` (uses every-repo lookup, works in both modes) |
| `mk issue show` | `ShowIssue` | `GET /repos/{prefix}/issues/{key}` | (read) | — |
| `mk issue brief` | `BriefIssue` | `GET /repos/{prefix}/issues/{key}/brief` | (read) | — |
| `mk issue edit` | `UpdateIssue` | `PATCH /repos/{prefix}/issues/{key}` | `issue.update` | — |
| `mk issue state` | `SetIssueState` | `PUT /repos/{prefix}/issues/{key}/state` | `issue.state` | — |
| `mk issue assign` | `AssignIssue` | `PUT /repos/{prefix}/issues/{key}/assignee` | `issue.assign` | — |
| `mk issue unassign` | `UnassignIssue` | `DELETE /repos/{prefix}/issues/{key}/assignee` | `issue.assign` | — |
| `mk issue rm` | `DeleteIssue` | `DELETE /repos/{prefix}/issues/{key}` | `issue.delete` | — |
| `mk issue next` | `ClaimNextIssue` | `POST /repos/{prefix}/features/{slug}/next` | `issue.claim` | — |
| `mk issue peek` | `PeekNextIssue` | `GET /repos/{prefix}/features/{slug}/next` | (read) | — |
| `mk comment add` | `AddComment` | `POST /repos/{prefix}/issues/{key}/comments` | `comment.add` | — |
| `mk comment list` | `ListComments` | `GET /repos/{prefix}/issues/{key}/comments` | (read) | — |
| `mk link` | `LinkRelation` | `POST /repos/{prefix}/relations` | `relation.create` | — |
| `mk unlink` | `UnlinkRelation` | `DELETE /repos/{prefix}/relations` | `relation.delete` | — |
| `mk pr attach` | `AttachPR` | `POST /repos/{prefix}/issues/{key}/pull-requests` | `pr.attach` | — |
| `mk pr detach` | `DetachPR` | `DELETE /repos/{prefix}/issues/{key}/pull-requests` | `pr.detach` | — |
| `mk pr list` | `ListPRs` | `GET /repos/{prefix}/issues/{key}/pull-requests` | (read) | — |
| `mk tag add` | `AddTags` | `POST /repos/{prefix}/issues/{key}/tags` | `tag.add` | — |
| `mk tag rm` | `RemoveTags` | `DELETE /repos/{prefix}/issues/{key}/tags` | `tag.remove` | — |
| `mk doc add` | `CreateDocument` | `POST /repos/{prefix}/documents` | `document.create` | `--from-path` (local only — body upload is the remote alternative) |
| `mk doc upsert` | `UpsertDocument` | `PUT /repos/{prefix}/documents/{filename}` | `document.create`/`update` | `--from-path` (local only) |
| `mk doc list` | `ListDocuments` | `GET /repos/{prefix}/documents` | (read) | — |
| `mk doc show` | `GetDocument` | `GET /repos/{prefix}/documents/{filename}` | (read) | — |
| `mk doc edit` | `EditDocument` | `PATCH /repos/{prefix}/documents/{filename}` | `document.update` | — |
| `mk doc rename` | `RenameDocument` | `POST /repos/{prefix}/documents/{filename}/rename` | `document.rename` | — |
| `mk doc rm` | `DeleteDocument` | `DELETE /repos/{prefix}/documents/{filename}` | `document.delete` | — |
| `mk doc link` | `LinkDocument` | `POST /repos/{prefix}/documents/{filename}/links` | `document.link` | — |
| `mk doc unlink` | `UnlinkDocument` | `DELETE /repos/{prefix}/documents/{filename}/links` | `document.unlink` | — |
| `mk doc export --to-path` | LOCAL ONLY (writes to filesystem) | n/a | — | `--to-path`, `--to` (server file) |
| `mk doc download` (NEW) | `DownloadDocument` | `GET /repos/{prefix}/documents/{filename}/download` | (read) | — |
| `mk history` | `ListHistory` | `GET /repos/{prefix}/history` or `GET /history` | (read) | — |
| `mk status` | local-direct (uses `EnsureRepo` + counts) | n/a (no /status) | — | uses `--db`; remote is currently not redirected (kept local-direct since stats endpoints aren't published) |
| `mk schema *` | local registry | n/a | — | local-only by design (registry doesn't depend on backend) |
| `mk api` | local store | n/a | — | local-only (it IS the server) |
| `mk tui` | LOCAL ONLY | n/a | — | whole verb |

## 5. Local-only error contract

When `--remote` / `MK_REMOTE` is set, the following CLI verbs error
clearly without making any HTTP calls:

| Verb | Error |
|---|---|
| `mk init` | `mk init: not supported in remote mode (this verb reads CWD git state); use \`POST /repos\` against the API or run \`mk init\` against the local DB instead` |
| `mk install-skill` | `mk install-skill: not supported in remote mode (writes the skill file to the local repo); run this verb against the local DB instead` |
| `mk doc add --from-path` | `--from-path is not supported in remote mode (the API cannot read the client's filesystem); pass --content/--content-file with --filename instead` |
| `mk doc upsert --from-path` | (same as above) |
| `mk doc export --to-path` | `mk doc export --to-path is not supported in remote mode; use \`mk doc download\` to fetch the body via HTTP and write it yourself` |
| `mk doc export --to <path>` | `mk doc export --to is not supported in remote mode (would write to the API server's filesystem); use \`mk doc download\` to fetch the body and pipe to disk` |
| `mk tui` | `mk tui: not supported in remote mode (TUI talks to SQLite directly); start the TUI against a local DB` |

`mk status` and `mk schema *` are kept local-direct in remote mode (no
remote endpoints exist for them in v1; they don't mutate, so the operator
just sees their local copy).

## 6. Repo auto-register in remote mode

Every CLI verb that takes the repo from CWD ends up calling
`resolveRepo()`. In remote mode this becomes:

1. `git.Detect(cwd)` — done locally; CLI knows its own working tree.
2. `client.GetRepoByPath(info.Root)` — resolved against the *server's*
   DB. (Remote backend hits `GET /repos` and filters by path on the
   client side, since there's no `GET /repos?path=...` endpoint
   currently and `GET /repos` is small.)
3. On miss: `client.AllocatePrefix(info.Name)` (local-only operation,
   short-circuited in remote mode by deriving a candidate via
   `store.DerivePrefix` and letting the server's `POST /repos` allocate
   if absent), then `POST /repos {name, path, remote_url}`.
4. The local backend keeps doing exactly what it does today (auto-create
   + audit row inline).

This puts the repo-auto-create in the same place for both backends.
`mk init` itself stays local-only (it's the explicit-form companion).

## 7. Issue key resolution

`resolveIssueByKey` accepts `MINI-42` or just `42`. In both modes:

1. If the key already contains `-`, parse as `PREFIX-N` directly.
2. Otherwise resolve the current repo (via `EnsureRepo`) and prepend
   its prefix.
3. Send `PREFIX-N` to the backend.

Both modes thus use canonical `PREFIX-N` from the client edge, matching
the API's URL shape.

## 8. Migration order

Each step ends with a clean commit + push.

1. Design pass — this doc.
2. `internal/client` package skeleton (`client.go`, `local.go`, `remote.go`, types).
3. Add `cli.openClient()` + global `--remote` / `--token` flags. Handlers untouched.
4. Migrate **repos** (`mk repo *`) and `resolveRepo()`'s auto-create.
5. Migrate **features** (`mk feature *`).
6. Migrate **issues — list/show/add**.
7. Migrate **issues — state/assign/unassign/edit/rm**.
8. Migrate **issues — next/peek/brief**.
9. Migrate **comments + relations + PRs + tags**.
10. Migrate **documents** (including new `mk doc download`).
11. Migrate **history**.
12. Local-only verb error paths.
13. Round-trip tests.
14. README + SKILL.md updates.
15. Final sweep + smoke tests.

## 9. Tests

- `internal/client/client_test.go` — interface compiles, factory picks
  the right backend.
- `internal/client/roundtrip_test.go` — for each resource group, drive
  the same operation through `localClient` against a temp SQLite DB and
  through `remoteClient` against `httptest.NewServer(api.New(...))`
  using the same temp SQLite DB; assert returned entities and audit
  rows match.
- All existing `internal/api/...` tests continue to pass unchanged.
- `internal/store/issues_test.go` continues to pass unchanged.

## 10. Output rendering parity

Both backends end up handing the CLI a `*model.Issue` (or `[]*model.Issue`,
or `*briefDoc`, etc). The CLI's existing JSON / text rendering paths in
`internal/cli/output.go` and friends operate on those types and emit
identical output regardless of backend. The remote backend round-trips
through JSON (`Go struct → JSON → Go struct`) which is byte-equivalent
for the same model definitions. View shapes (e.g. `issueView`,
`featureView`, `issueBrief`) live in `internal/cli` for text rendering
and are constructed in two places:

- Local backend: build the view struct from the store calls inline,
  matching what the CLI does today.
- Remote backend: decode the API's matching view shape (e.g. `IssueView`
  in `internal/api/views.go` → `*client.IssueView` → CLI wraps to
  `*cli.issueView` via field-by-field copy).

To avoid duplicating the struct definitions a third time, the CLI's
existing types (`issueView`, `featureView`, etc) are the authoritative
ones for client-internal use; the client package just re-uses them
verbatim by importing `internal/cli/inputs` (already shared) and
defining its own equivalents only for the API-shape types
(`api.IssueView` etc) — which are minor rebinds to the CLI types via
field-for-field copy. Net: the CLI's text renderer doesn't change.

## 11. Things that surprised us during planning

- `mk doc download` is a small new CLI verb that wraps the existing
  `GET /documents/{filename}/download`. It exists so remote-mode users
  have a way to fetch a doc body without `--to-path`. Local mode uses
  the same client method to read the doc and writes it out.
- `mk status` doesn't have a remote endpoint in v1. Decision: keep it
  local-direct (talks to the local DB). If you set `MK_REMOTE` and run
  `mk status`, you see the local DB's stats — which is the right thing
  to do because `mk status` is fundamentally about "what does THIS
  machine have?".
- The audit-log message text is identical across backends because both
  the local backend and `internal/api/audit.go` use the same
  `model.HistoryEntry` shape and matching `Details` strings. Round-trip
  tests assert this byte-for-byte.
