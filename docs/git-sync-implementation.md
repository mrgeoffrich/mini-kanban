# Git sync — technical design

Companion to [`git-sync-design.md`](./git-sync-design.md). That doc is the
*what* and *why* of mk's git-backed sync layer. This doc is the *how*:
exact DB schema deltas, new file/package layout, sync-engine internals,
canonical file formats, and a phased plan that lists the files each
phase touches.

Decisions that are settled in the planning doc are not re-litigated
here — if a section seems thin, it's because the design doc already
covered it.

## Overview

The work breaks into five components, each owned by a clear part of the
codebase:

| Component | Lives in | Owns |
| --- | --- | --- |
| Identity | `internal/store/`, `internal/model/` | UUIDv7 columns + backfill |
| File layout | `internal/sync/paths.go` | Folder-name generation, NFC normalisation, case-insensitive collision detection |
| YAML I/O | `internal/sync/yaml_*.go` | Emit (always-quote-user-strings, hash-stable output), parse (strict typing), canonical hash |
| Sync engine | `internal/sync/engine.go` (+ `import.go`, `export.go`, `state.go`, `redirects.go`) | The import/export pipelines, `sync_state` bookkeeping, redirects |
| CLI / git plumbing | `internal/cli/sync.go`, `internal/git/sync_ops.go` | `mk sync` subcommands, sentinel detection, `git clone/pull/push/mv` |

Nothing in the existing code's contract changes for non-sync users:
mk-without-sync continues to work exactly as today. The new tables and
columns are additive; `migrate()` brings older DBs forward.

## DB schema changes

All deltas applied via `internal/store/schema.sql` (for fresh DBs) and
mirrored in `migrate()` (`internal/store/store.go`) for existing DBs.
SQLite's ALTER TABLE limitations apply — adding a `NOT NULL` column
needs an intermediate nullable add + backfill + index, the same pattern
already used for `features.updated_at`.

### New columns

```sql
-- issues, features, documents, comments, repos all gain a uuid.
-- Generated UUIDv7 at create time, never mutated afterwards.
ALTER TABLE issues     ADD COLUMN uuid TEXT;  -- becomes NOT NULL after backfill
ALTER TABLE features   ADD COLUMN uuid TEXT;
ALTER TABLE documents  ADD COLUMN uuid TEXT;
ALTER TABLE comments   ADD COLUMN uuid TEXT;
ALTER TABLE repos      ADD COLUMN uuid TEXT;

-- After migrate() backfills NULLs with fresh UUIDv7s, enforce uniqueness:
CREATE UNIQUE INDEX uniq_issues_uuid    ON issues(uuid);
CREATE UNIQUE INDEX uniq_features_uuid  ON features(uuid);
CREATE UNIQUE INDEX uniq_documents_uuid ON documents(uuid);
CREATE UNIQUE INDEX uniq_comments_uuid  ON comments(uuid);
CREATE UNIQUE INDEX uniq_repos_uuid     ON repos(uuid);
```

`schema.sql` for fresh DBs declares `uuid TEXT NOT NULL UNIQUE` directly.

### Relax `repos.path` UNIQUE

The current `repos.path TEXT NOT NULL UNIQUE` blocks phantom repos
(rows with `path = ''` representing prefixes that exist in the sync
repo but have no local working tree on this machine). Replace with a
partial unique index:

```sql
-- In a fresh schema: drop the column-level UNIQUE constraint and add:
CREATE UNIQUE INDEX uniq_repos_path ON repos(path) WHERE path != '';
```

In `migrate()`, dropping a column constraint requires the SQLite
table-rebuild dance: create a new `repos_new` with the relaxed schema,
copy rows, drop old, rename. This is a one-shot in `migrate()` keyed by
the existence of the `uniq_repos_path` index.

### New `sync_state` table

```sql
CREATE TABLE IF NOT EXISTS sync_state (
    uuid             TEXT    NOT NULL PRIMARY KEY,
    kind             TEXT    NOT NULL CHECK (kind IN
                       ('issue','feature','document','comment','repo')),
    last_synced_at   DATETIME NOT NULL,
    last_synced_hash TEXT     NOT NULL
);

CREATE INDEX idx_sync_state_kind ON sync_state(kind);
```

Presence-of-row is the "previously synced" flag (per the design doc's
case table). No per-pass boolean; the import walk builds an in-memory
`seen_uuids` set and uses it to detect deletions before persisting any
state.

### Backfill UUIDv7s for existing rows

In `migrate()`, after each `ALTER TABLE … ADD COLUMN uuid TEXT`:

```sql
-- Pseudocode; actual implementation does this row-by-row in Go because
-- SQLite has no built-in uuid7 function.
UPDATE issues    SET uuid = ? WHERE id = ?;  -- bound from Go-generated UUIDv7
-- Then once all rows are populated:
ALTER TABLE issues ADD CONSTRAINT — (or table rebuild) to enforce NOT NULL.
```

Implementation: `migrate()` runs a small loop per table:

```go
rows, _ := tx.Query(`SELECT id FROM issues WHERE uuid IS NULL`)
for each id:
    tx.Exec(`UPDATE issues SET uuid = ? WHERE id = ?`, uuid7.New(), id)
```

This is fine for the dataset sizes mk targets (thousands of issues, not
millions). The backfill is idempotent — running migrate twice is a
no-op on the second pass.

### Recompute `next_issue_number` on import

Per the design, it's now advisory. Add a helper in
`internal/store/repos.go`:

```go
func (s *Store) RecomputeNextIssueNumber(repoID int64) error
```

called once per repo at the end of `mk sync import` so a remotely
imported MINI-50 doesn't get re-used locally.

## File format reference

All on-disk YAML uses a strict emitter that:

1. **Quotes every user-supplied string scalar** unconditionally.
2. **Emits timestamps as RFC3339 UTC** with millisecond precision.
3. **Sorts map keys alphabetically** (deterministic for hash stability).
4. **Uses `LF` line endings** regardless of platform.
5. **Ends with a single trailing newline.**

These rules together let us treat the YAML output as content-addressable
(SHA-256 of the bytes is the `last_synced_hash`).

### `mk-sync.yaml` (root of sync repo)

```yaml
created_at: "2026-05-09T14:22:00.000Z"
schema_version: 1
```

Presence at working-tree root → mk is in sync-repo mode (see
[Sentinel detection](#sentinel-detection)).

### `.mk/config.yaml` (in each project repo, checked in)

```yaml
sync:
  remote: "git@github.com:user/mk-data.git"
```

Optional. When absent, `mk sync` errors with a setup hint pointing at
`mk sync init`.

### `repos/<prefix>/repo.yaml`

```yaml
created_at: "2025-11-01T09:00:00.000Z"
name: "mini-kanban"
next_issue_number: 47
prefix: "MINI"
remote_url: "git@github.com:mrgeoffrich/mini-kanban.git"
updated_at: "2026-05-09T14:22:00.000Z"
uuid: "0190c2a3-7f3a-7b2c-8b21-aaaaaaaaaaaa"
```

Never carries `id`, `path`, or any other client-state field.

### `repos/<prefix>/redirects.yaml`

Append-only list, sorted by `changed_at`:

```yaml
- changed_at: "2026-05-09T14:30:00.000Z"
  kind: "issue"
  new: "MINI-12"
  old: "MINI-7"
  reason: "label_collision"
  uuid: "0190c2a3-7f3a-7b2c-8b21-bbbbbbbbbbbb"

- changed_at: "2026-05-10T08:15:00.000Z"
  kind: "document"
  new: "auth-overview-2.md"
  old: "auth-overview.md"
  reason: "label_collision"
  uuid: "0190c2a3-7f3a-7b2c-8b21-cccccccccccc"

- changed_at: "2026-05-12T11:00:00.000Z"
  kind: "feature"
  new: "auth-rewrite-v2"
  old: "auth-rewrite"
  reason: "manual_rename"
  uuid: "0190c2a3-7f3a-7b2c-8b21-dddddddddddd"
```

`reason` is one of `label_collision`, `manual_rename`,
`label_changed_remotely`. `kind` is one of `issue`, `feature`,
`document`. The file is never pruned in v1.

### `repos/<prefix>/issues/<label>/issue.yaml`

```yaml
assignee: "geoff"
created_at: "2026-05-01T10:14:22.000Z"
description_hash: "sha256:abc123..."   # of description.md, for change detection
feature:
  label: "auth-rewrite"
  uuid: "0190c2a3-7f3a-7b2c-8b21-dddddddddddd"
number: 7
prs:
  - "https://github.com/.../pull/42"
relations:
  blocks:
    - {label: "MINI-12", uuid: "0190c2a3-7f3a-7b2c-8b21-cccccccccccc"}
  duplicate_of: []
  relates_to:
    - {label: "MINI-3", uuid: "0190c2a3-7f3a-7b2c-8b21-eeeeeeeeeeee"}
state: "in_progress"
tags:
  - "p1"
  - "security"
title: "Add auth middleware"
updated_at: "2026-05-09T14:22:00.000Z"
uuid: "0190c2a3-7f3a-7b2c-8b21-bbbbbbbbbbbb"
```

`description.md` holds the body. The `description_hash` field in YAML
is what makes a body-only edit detectable in import without re-reading
both files (tradeoff: redundant info, but cheap and worth it).

### `repos/<prefix>/issues/<label>/comments/<filename>`

Filename: `<RFC3339 timestamp with `:` → `-`>--<full uuid>.yaml` plus a
sibling `.md` for the body.

```yaml
# 2026-05-09T14-22-00.000Z--0190c2a3-7f3a-7b2c-8b21-ffffffffffff.yaml
author: "geoff"
body_hash: "sha256:def456..."
created_at: "2026-05-09T14:22:00.000Z"
uuid: "0190c2a3-7f3a-7b2c-8b21-ffffffffffff"
```

### `repos/<prefix>/features/<slug>/feature.yaml` + `description.md`

```yaml
created_at: "2026-04-15T09:00:00.000Z"
description_hash: "sha256:..."
slug: "auth-rewrite"
title: "Rewrite the auth middleware"
updated_at: "2026-05-09T14:22:00.000Z"
uuid: "0190c2a3-7f3a-7b2c-8b21-dddddddddddd"
```

### `repos/<prefix>/docs/<filename>/doc.yaml` + `content.md`

```yaml
content_hash: "sha256:..."
created_at: "2026-04-20T11:00:00.000Z"
filename: "auth-overview.md"
links:
  - {kind: "issue",   target_label: "MINI-7",  target_uuid: "..."}
  - {kind: "feature", target_label: "auth-rewrite", target_uuid: "..."}
source_path: "docs/auth-overview.md"
type: "architecture"
updated_at: "2026-05-09T14:22:00.000Z"
uuid: "0190c2a3-7f3a-7b2c-8b21-aaaaaaaaaaaa"
```

`type` lives only inside `doc.yaml`, never in the path.

## Code layout

### New package: `internal/sync/`

```
internal/sync/
    engine.go        # orchestration: Sync(), Import(), Export()
    import.go        # the four-phase import pipeline
    export.go        # the export pipeline + atomic staging
    state.go         # sync_state CRUD on top of *store.Store
    redirects.go     # redirects.yaml read/append, kind-aware lookup
    paths.go         # label → path generation, NFC, case collision
    yaml_emit.go     # strict deterministic emitter
    yaml_parse.go    # strict parser (rejects type mismatches)
    hash.go          # canonical content hash
    sentinel.go      # mk-sync.yaml detection, mode flip
    config.go        # .mk/config.yaml read/write
    bootstrap.go     # init / clone command implementations
    identity.go      # UUIDv7 generation helper (wraps google/uuid)
```

### Additions to `internal/store/`

| File | Additions |
| --- | --- |
| `schema.sql` | uuid columns, sync_state table, partial path index |
| `store.go` | uuid backfill in `migrate()`; table rebuild for path index |
| `issues.go`, `features.go`, `documents.go`, `comments.go`, `repos.go` | uuid generation at create; `GetByUUID` lookup; `Update*ByUUID` helpers |
| `validate.go` | NFC normalisation helper for any string used as a label |
| `sync_state.go` | new file — sync_state CRUD: `MarkSynced`, `GetSyncState`, `DeleteSyncState`, `ListSyncedUUIDs(kind)` |

### Additions to `internal/git/`

`detect.go` is unchanged. New file `internal/git/sync_ops.go` wraps the
git operations sync needs:

```go
type Repo struct{ Root string }

func Open(path string) (*Repo, error)        // checks .git exists
func Clone(remote, dest string) error
func (r *Repo) Pull() error                   // fast-forward or fail
func (r *Repo) Add(paths ...string) error
func (r *Repo) Mv(from, to string) error
func (r *Repo) Commit(message string) (sha string, err error)
func (r *Repo) Push() error
func (r *Repo) HasUncommittedChanges() (bool, error)
func (r *Repo) HasUnpulledChanges() (bool, error)
```

Each shells out to `git` via `os/exec`, matching `detect.go`'s
existing pattern. No new third-party dependency for git itself.

### Additions to `internal/cli/`

New file `internal/cli/sync.go` adds the cobra commands:

```go
func newSyncCmd() *cobra.Command {
    cmd := &cobra.Command{Use: "sync", Short: "..."}
    cmd.AddCommand(
        newSyncInitCmd(),    // mk sync init <path> [--remote URL]
        newSyncCloneCmd(),   // mk sync clone [<path>] [--allow-renumber] [--dry-run]
        newSyncRunCmd(),     // mk sync (the steady-state command)
        newSyncVerifyCmd(),  // mk sync verify (consistency check)
        newSyncInspectCmd(), // mk sync inspect <prefix> [--issue MINI-7]
    )
    return cmd
}
```

`mk sync` (no subcommand) maps to `newSyncRunCmd()` — the common
case is one word.

`context.go`'s `resolveRepo()` learns the sentinel check:

```go
func resolveRepo(s *store.Store) (*model.Repo, error) {
    cwd, _ := os.Getwd()
    info, err := git.Detect(cwd)
    if err != nil { return nil, err }

    // NEW: bail before auto-register if this is a sync repo.
    if sync.IsSyncRepo(info.Root) {
        return nil, errSyncRepoMode  // commands handle this gracefully
    }

    // ... existing GetRepoByPath / CreateRepo flow unchanged
}
```

`errSyncRepoMode` is a sentinel error type so each command can decide
whether to fail or do something useful (e.g., `mk sync verify` accepts
sync-repo mode; `mk issue create` rejects it).

`root.go` registers the new command:

```go
root.AddCommand(
    // ... existing ...
    newSyncCmd(),
)
```

### Additions to `internal/model/`

Each domain type gains a `UUID string` field:

```go
type Issue struct {
    ID    int64  `json:"id"`
    UUID  string `json:"uuid"`           // NEW
    // ... existing fields ...
}
// Repeat for Feature, Document, Comment, Repo.
```

JSON output of existing CLI commands gains a `uuid` field — additive,
backward-compatible for consumers ignoring unknown fields.

### Module-root files

`embed.go` is unchanged. `go.mod` promotes existing transitive deps to
direct:

```
github.com/google/uuid v1.6.0   // for UUIDv7 (uuid.NewV7())
go.yaml.in/yaml/v4              // already pulled transitively; promote
```

Both are already present transitively; no new external deps.

## Sync engine internals

### Engine entry points

```go
package sync

type Engine struct {
    Store     *store.Store
    SyncRepo  *git.Repo        // working tree of the sync repo
    Actor     string
    DryRun    bool
}

func (e *Engine) Run(ctx context.Context) (*Result, error)        // full pull→import→export→commit→push
func (e *Engine) Import(ctx context.Context) (*ImportResult, error)
func (e *Engine) Export(ctx context.Context) (*ExportResult, error)
func (e *Engine) Verify(ctx context.Context) (*VerifyResult, error)
```

### Import pipeline (`internal/sync/import.go`)

Implements the four phases from the design doc:

```go
func (e *Engine) Import(ctx context.Context) (*ImportResult, error) {
    // Phase 1: scan
    scan, err := e.scanWorkingTree(ctx)
    // scan.records[uuid] = parsed record + content hash
    // scan.byLabel[kind][label] = uuid

    // Phase 2: resolve label collisions
    renames, err := e.resolveCollisions(scan)
    // For each (kind, label) where DB has uuid X and scan has uuid Y (X != Y),
    // renumber DB row X. Append to redirects.yaml. Log audit.

    // Phase 3: apply per-record
    for _, rec := range scan.records {
        e.applyRecord(rec)  // dispatches on the case-table
    }

    // Phase 4: detect deletions
    e.propagateDeletes(scan.seenUUIDs)

    // Recompute next_issue_number per repo
    for repoID := range scan.touchedRepos {
        e.Store.RecomputeNextIssueNumber(repoID)
    }
    return result, nil
}
```

Each phase is a method on `*Engine` so they're individually testable
with a constructed in-memory store.

### Export pipeline (`internal/sync/export.go`)

Atomic staging is the key correctness requirement:

```go
func (e *Engine) Export(ctx context.Context) (*ExportResult, error) {
    staging, err := e.makeStagingDir()  // tmpdir, sibling of working tree
    defer os.RemoveAll(staging)

    for repo := range e.Store.ListReposWithLocalData() {
        e.exportRepo(repo, staging)
    }

    // Compare staging against working tree; compute file ops:
    ops := computeFileOps(workingTree, staging)
    // ops = [{type: rename, from, to}, {type: write, path, bytes}, {type: delete, path}]

    // Apply all ops atomically (or as close as Linux/macOS will allow:
    // os.Rename for files, then os.Rename of dirs).
    return e.applyOps(ops)
}
```

`computeFileOps` is the heart — it produces the *minimum* set of
filesystem ops to converge the working tree to the staging directory's
shape. This is what keeps git diffs surgical: unchanged files are
never touched.

### `sync_state` semantics (`internal/sync/state.go`)

Wraps `*store.Store` with sync-aware helpers:

```go
func (e *Engine) markSynced(uuid, kind, hash string) error
// upsert sync_state(uuid, kind, last_synced_at = NOW, last_synced_hash = hash)

func (e *Engine) wasPreviouslySynced(uuid string) (bool, error)
// SELECT 1 FROM sync_state WHERE uuid = ?

func (e *Engine) listPreviouslySynced(kind string) ([]string, error)
// returns all uuids of this kind with a sync_state row
// used in deletion-detection phase
```

### Hash stability (`internal/sync/hash.go`)

```go
func ContentHash(yamlBytes []byte) string
// returns "sha256:<hex>" of the bytes; assumes emitter has produced
// canonical (sorted-keys, LF-only, single trailing newline) output
```

The emitter is responsible for canonicalisation; the hash function is
a thin wrapper.

### Path generation (`internal/sync/paths.go`)

```go
func IssueFolder(prefix string, number int64) string
// "repos/MINI/issues/MINI-7"

func FeatureFolder(prefix, slug string) string
// "repos/MINI/features/auth-rewrite"

func DocumentFolder(prefix, filename string) string
// "repos/MINI/docs/auth-overview.md"
// filename is NFC-normalised; case-sensitive on FS that supports it

func CommentFile(issueFolder string, createdAt time.Time, uuid string) (yaml, md string)
// "<issue>/comments/2026-05-09T14-22-00.000Z--<uuid>.yaml"

func DetectCaseInsensitiveCollisions(folders []string) map[string][]string
// returns groups of folder names that case-fold to the same string
```

NFC normalisation uses `golang.org/x/text/unicode/norm` (already in
the dependency tree as transitive of go-yaml).

### Sentinel detection (`internal/sync/sentinel.go`)

```go
func IsSyncRepo(workingTreeRoot string) bool
// checks whether <root>/mk-sync.yaml exists and parses

func ReadSentinel(path string) (*Sentinel, error)
type Sentinel struct {
    SchemaVersion int
    CreatedAt     time.Time
}
```

Called from `internal/cli/context.go`'s `resolveRepo`.

### Process lock

To prevent `mk sync` racing with concurrent `mk issue create` (etc.)
on the same DB:

```go
// internal/sync/lock.go
func AcquireSyncLock(dbPath string) (release func(), err error)
// flock-style file lock at <dbPath>.sync.lock
// blocks for up to 30s; errors out otherwise
```

`mk sync` acquires for the full duration. Other mutating commands
acquire briefly per-transaction (they already use SQLite transactions;
the file lock is an additional outer guard for the sync window
specifically).

Implementation: `golang.org/x/sys/unix.Flock` on Unix, equivalent on
Windows. Both already pulled transitively.

### Redirects (`internal/sync/redirects.go`)

```go
type Redirect struct {
    Kind      string    // "issue" | "feature" | "document"
    Old       string    // old label
    New       string    // new label
    UUID      string
    ChangedAt time.Time
    Reason    string    // "label_collision" | "manual_rename" | "label_changed_remotely"
}

func LoadRedirects(repoFolder string) ([]Redirect, error)
func AppendRedirect(repoFolder string, r Redirect) error
func ResolveLabel(redirects []Redirect, kind, label string) (newLabel string, ok bool)
// chases the redirect chain forward to the current label
```

`ResolveLabel` is what makes `mk issue show MINI-7` work after a
renumber to MINI-12.

## CLI surface

### `mk sync init <path> [--remote URL]`

Pre-flight:
- CWD must be inside a project repo (not a sync repo).
- `<path>` either doesn't exist, or exists and is empty.
- If `--remote` given, validate URL syntactically.

Effects:
- `git init` at `<path>`.
- Write `<path>/mk-sync.yaml`.
- If `--remote`, `git remote add origin <url>` and `git push --set-upstream`.
- Write `.mk/config.yaml` in the project repo with `sync.remote`.
- Run `Engine.Export()` against the new sync repo.
- Commit and push.

Error if remote already has commits → "use `mk sync clone` to join an
existing sync repo."

### `mk sync clone [<path>] [--allow-renumber] [--dry-run]`

Pre-flight:
- CWD must be inside a project repo.
- `.mk/config.yaml` must exist and have `sync.remote`.
- `<path>` either doesn't exist, or exists and is empty (default:
  `~/.mini-kanban/sync/<basename-of-remote>`).

Effects with `--dry-run`:
- Clone (or read existing) sync repo to a temp dir.
- Run import in dry-run mode (no DB writes); emit a preview report:
  imports per kind, renumbers required, dangling references.
- Cleanup.

Effects without `--dry-run`:
- Clone the sync repo.
- Record `(remote → local path)` in DB.
- If local DB has any rows for the project's prefix and `--allow-renumber`
  is not set: print preview and exit non-zero.
- Otherwise: run `Engine.Import()`. Commit nothing; first export happens
  on the next `mk sync`.

### `mk sync` (steady-state)

Pre-flight:
- CWD must be inside a project repo.
- `.mk/config.yaml` exists with `sync.remote`.
- DB has a row recording the local clone path for that remote.
- Acquire sync lock.

Pipeline:
1. `git pull` the sync repo.
2. `Engine.Import()`.
3. `Engine.Export()`.
4. If staging produced any changes: `git add . && git commit -m "<auto>"`,
   then `git push`. If push fails non-fast-forward: pull, re-import,
   re-export, retry once. Bail with a clear message if the retry also
   fails — user has to investigate.
5. Release sync lock.

Output (text mode): one-line summary per kind plus warnings.
Output (`--output json`): structured `Result`.

### `mk sync verify`

Run from inside a sync repo (the only command that *requires* sync-repo
mode). Walks every `repos/<prefix>/` and checks:

- Every `<record>.yaml` parses cleanly under strict typing.
- UUIDs are unique across all records.
- All cross-reference uuids resolve to present records.
- No case-insensitive folder collisions.
- `redirects.yaml` chains terminate (no cycles).
- `repo.yaml` uuid matches across runs (warns on first verify after
  uuid was set).

Exits non-zero on any failure; prints all problems to stdout.

### `mk sync inspect <prefix> [--issue MINI-7]`

Read-only. Run from a sync repo. Prints either a summary of one prefix
or the parsed YAML for one record. Useful for debugging sync without
needing the project repo's DB.

### Audit ops

Per `internal/cli/audit.go` conventions, `mk sync` records:

| `op` | `kind` | When |
| --- | --- | --- |
| `sync.run` | `repo` | Once per `mk sync` invocation, with details on counts |
| `sync.import` | `repo` | Once per Import phase |
| `sync.export` | `repo` | Once per Export phase |
| `sync.renumber` | `issue` | Per renumber, target_label = new label |
| `sync.rename` | `feature` or `document` | Per rename |
| `sync.delete` | varies | Per propagated remote deletion |

All via `recordOp(s, model.HistoryEntry{...})` — the existing pattern.

## Phased implementation plan

Each phase is independently shippable and lands as its own PR. Within
a phase, files can be split across smaller PRs if useful — the listed
groupings are the natural shape, not a hard rule.

### Phase 1 — UUID columns + backfill

**Goal:** every record has a UUIDv7. No sync yet, no behaviour change.

**Files:**
- `internal/store/schema.sql` — add `uuid TEXT NOT NULL UNIQUE` to
  issues, features, documents, comments, repos. Fresh DBs only.
- `internal/store/store.go` — `migrate()`: add `uuid` columns nullable
  to existing tables, backfill with UUIDv7s (Go-side loop), then add
  unique indexes.
- `internal/store/issues.go`, `features.go`, `documents.go`, `comments.go`,
  `repos.go` — generate UUIDv7 in every `Create*` path; add
  `GetByUUID(uuid string)` lookups; expose `UUID` in returned models.
- `internal/store/sync_state.go` — new file. Schema-only this phase
  (table + index in schema.sql); CRUD lands in Phase 3.
- `internal/model/types.go`, `document.go` — add `UUID string` to
  `Issue`, `Feature`, `Document`, `Comment`, `Repo`.
- `internal/sync/identity.go` — new file. `New() string` returning
  UUIDv7 string via `google/uuid`. Promotion of `google/uuid` to
  direct dependency in `go.mod`.
- `cmd/mk` — none.
- `internal/cli/output.go` — add `uuid` to JSON outputs of every
  `*Show` and list response.
- `SKILL.md` — update output schema examples to mention `uuid`.

**Test:** verify a fresh DB and a migrated DB both end with the same
schema; verify create-then-fetch round-trips a UUIDv7; verify two
creates produce different uuids.

**Ship criterion:** existing CLI/TUI tests all pass; no user-visible
behaviour change.

### Phase 2 — Export-only

**Goal:** `mk sync export <path>` writes the DB to a folder. One-way;
useful as a backup tool even with no import. No sentinel, no commits,
no remote.

**Files:**
- `internal/sync/yaml_emit.go` — strict deterministic emitter.
- `internal/sync/paths.go` — folder-name generators with NFC
  normalisation.
- `internal/sync/hash.go` — content hash.
- `internal/sync/export.go` — single-pass export to a target folder.
  No staging yet; full overwrite is acceptable in this phase.
- `internal/sync/engine.go` — minimal `Engine` with just `Export`.
- `internal/cli/sync.go` — new file. `newSyncCmd()` with one
  subcommand: `sync export <path>` (hidden / undocumented; this is a
  Phase-2 development tool).
- `internal/cli/root.go` — register `newSyncCmd()`.

**Test:** export a populated DB, diff the output across two runs
(should be byte-identical for an unchanged DB). Exercise the YAML
emitter against records with edge-case strings (`assignee: "on"`,
emoji titles, etc.).

**Ship criterion:** exporting a clean DB twice produces zero diff;
exporting a hand-modified DB produces only the expected diff.

### Phase 3 — Import-only

**Goal:** `mk sync import <path>` reads a folder into the DB. One-way.
Implements the four-phase import, sync_state table, redirects.

**Files:**
- `internal/sync/yaml_parse.go` — strict parser; refuses type
  mismatches; uses `golang.org/x/text/unicode/norm` for NFC.
- `internal/sync/state.go` — `sync_state` CRUD wrapping `*store.Store`.
- `internal/store/sync_state.go` — flesh out the CRUD started in Phase 1.
- `internal/sync/redirects.go` — load/append/resolve.
- `internal/sync/import.go` — the four-phase pipeline.
- `internal/sync/engine.go` — extend `Engine` with `Import`.
- `internal/store/repos.go` — `RecomputeNextIssueNumber`,
  phantom-repo support (`CreatePhantomRepo`, `UpgradePhantomRepo`).
- `internal/store/issues.go`, etc. — `Update*ByUUID`, `Renumber*`
  helpers used by collision resolution.
- `internal/cli/sync.go` — add `sync import <path>` subcommand.
- `internal/cli/context.go` — no change yet (sentinel detection is
  Phase 4).

**Test:** export from one DB, import into a fresh DB, verify
equivalence. Trigger label collisions deliberately (two DBs with same
issue number, different uuids) and verify the renumber + redirects.
Trigger deletions, resurrections, dangling refs.

**Ship criterion:** export-then-import is a faithful round-trip.
Collision resolution is observable through `mk history` audit ops.

### Phase 4 — Wire as `mk sync` with bootstrap

**Goal:** the user-facing commands. End-to-end multi-machine usage.

**Files:**
- `internal/git/sync_ops.go` — new file. `Open`, `Clone`, `Pull`,
  `Add`, `Mv`, `Commit`, `Push`, `HasUncommittedChanges`,
  `HasUnpulledChanges`. All shell out to `git`.
- `internal/sync/sentinel.go` — `IsSyncRepo`, `ReadSentinel`,
  `WriteSentinel`.
- `internal/sync/config.go` — `.mk/config.yaml` read/write.
- `internal/sync/bootstrap.go` — `Init(...)`, `Clone(...)` flows.
- `internal/sync/lock.go` — process-level file lock.
- `internal/sync/engine.go` — `Run()` (full pipeline) and atomic
  staging in `Export` (replace the simple overwrite from Phase 2).
- `internal/cli/sync.go` — add `init`, `clone`, top-level `sync`,
  with all flags (`--allow-renumber`, `--dry-run`).
- `internal/cli/context.go` — `resolveRepo()` checks `IsSyncRepo`;
  return `errSyncRepoMode` and let each command handle it.

**Test:** spin up two temp DBs and a temp sync repo on disk; round-trip
data between them. Simulate a push race by interleaving pulls.
Run `mk sync init` against an empty remote, then a non-empty remote
(should error). `mk sync clone` against a populated DB without
`--allow-renumber` (should error with preview).

**Ship criterion:** two collaborator DBs reach consistency through a
shared sync repo with a single `mk sync` per user.

### Phase 5 — Verify, inspect, polish

**Goal:** the diagnostic commands and the documentation pass.

**Files:**
- `internal/sync/verify.go` — consistency checks for sync repo content.
- `internal/cli/sync.go` — add `verify` and `inspect`.
- `SKILL.md` — document `mk sync*` commands, the bootstrap workflows,
  what to expect on collisions.
- `docs/git-sync-design.md` — strike or update sections that the
  implementation chose differently from.

**Ship criterion:** `mk sync verify` catches a deliberately-corrupted
sync repo; SKILL.md round-trips through `mk install-skill`.

## Open implementation questions

1. **YAML library.** `go.yaml.in/yaml/v4` is already transitive
   (likely via glamour). It's the maintained successor to
   `gopkg.in/yaml.v3`. Promote it to direct, or use v3 explicitly?
   v4 is rc-tagged but stable enough; lean towards v4.
2. **Process lock on Windows.** `golang.org/x/sys/windows` has
   `LockFileEx`. Mk hasn't been tested on Windows in earnest; v1 might
   accept "no lock on Windows" with a warning. Decide before Phase 4.
3. **Should `mk sync` be transactional with the DB?** A failure
   mid-import should leave the DB in a consistent state. Wrapping the
   whole import in a single SQLite transaction is feasible (mk's
   imports are bounded in size), but the audit-log writes traditionally
   happen outside the main transaction so they survive rollbacks.
   Decide whether sync's audit writes follow the same pattern or live
   inside the import txn.
4. **Phantom-repo `path = ''` invariant.** Does `resolveRepoC` (the
   client-abstraction variant) need updating to never resolve a phantom
   as the active repo? Probably yes — only real working trees with
   matching `path` should win.
5. **Concurrent `mk sync` retries.** The design says "retry once on
   non-fast-forward." Should it be configurable? Probably hardcoded
   to 1 retry for v1; revisit only if observed in practice.
6. **`mk sync verify` running from a project repo.** Should it work
   from outside the sync repo too (auto-locating it via
   `.mk/config.yaml`)? Convenience, not correctness — Phase 5 decision.

## What's *not* in this doc

- TUI integration. `mk sync` is CLI-only initially; the TUI gains a
  status indicator later. Out of scope for this design.
- `mk api` (remote mode) interaction with sync. Sync is local-DB only;
  in remote mode, the *server* is the source of truth and sync is its
  problem. The CLI's `inRemoteMode()` short-circuits sync commands
  with a clear error.
- `mk install-skill` updates beyond a SKILL.md content refresh — the
  embed mechanism doesn't change.
