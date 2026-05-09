# Git-backed sync for `mk`

Working notes for adding a sync layer that mirrors mk's SQLite database to a
checked-in folder structure inside a git repository. The goal is to let multiple
machines (and multiple humans) collaborate on the same mk dataset without
introducing a server, while keeping mk's local-first, offline-friendly DX.

This doc captures the design decisions, the reasoning behind them, and the
edge cases worth knowing about before implementation. Nothing here is
shipped yet.

## Goal and non-goals

**Goal.** Let mk's per-repo state — issues, features, documents, comments,
tags, links — be persisted in a human-readable folder structure inside a git
repository, and synchronised both ways with the local SQLite database. Two
people on two machines should be able to do `git pull && mk sync` and end up
with the same data, with conflicts surfaced (and mostly resolved
automatically) along the way.

**Non-goals.**

- Real-time collaboration. Sync is explicit, user-triggered (or git-hook
  triggered), not a background daemon.
- Server-side coordination. There is no mk server; git is the only shared
  substrate.
- CRDT-grade merge correctness. Last-writer-wins per record is good enough
  at the scale mk targets, and the implementation cost of CRDTs would dwarf
  the benefit.
- Field-level merging of YAML bodies. If two clients edit the same record's
  YAML simultaneously, git's text merge handles it; mk re-parses after.
- Syncing `tui_settings` (local UI prefs) or `history` v1 (audit log).
  Both can be revisited later if there's demand.

## Why git, why files

A few existing trackers store everything in markdown / YAML in git
(see: `tasks.md` workflows, `git-bug`, etc.). The reasons that apply to mk:

- **mk already lives in a git repo by design.** Every `mk` invocation
  resolves a repo via `internal/git/detect.go`. Putting sync state in the
  same git tree is conceptually free.
- **Git already solves the hardest part: distributed conflict detection.**
  We're not reimplementing concurrency; we're using git's existing model.
- **The data is reviewable.** `git diff` on a folder of YAML files is
  legible to humans in a way that diffing SQLite isn't.
- **Backups and history come along for free.** Every commit is a snapshot.

The cost is that we now have two stores (DB and files) that can drift, and
we have to design the sync algorithm with that drift in mind. The rest of
this doc is about that.

## Layout

One folder per repo, keyed by repo prefix. Inside each repo folder, one
folder per record. Folder names use **labels** (numbers, slugs, filenames)
so the working tree is browsable; **identity** is carried separately as a
UUID inside each YAML file (see [Identity model](#identity-model)).

```
repos/
  <prefix>/
    repo.yaml                    # name, remote_url, next_issue_number (advisory)
    redirects.yaml               # label history: old → new mappings + uuids
    issues/
      MINI-1/
        issue.yaml               # uuid, number, state, assignee, feature ref,
                                 # tags, relations, prs, created_at, updated_at
        description.md
        comments/
          2026-05-09T14-22-00Z--<full-uuid>.yaml
          2026-05-09T14-22-00Z--<full-uuid>.md
    features/
      auth-rewrite/
        feature.yaml             # uuid, slug, title, timestamps
        description.md
    docs/
      auth-overview/             # folder named by filename (no type in path)
        doc.yaml                 # uuid, filename, type, source_path, links[], timestamps
        content.md
```

Notes on the layout:

- **One folder per record** rather than one file. This is the layout where
  per-resource subdata (comments on an issue, content + meta on a doc) has
  somewhere natural to live, and where merge conflicts stay scoped to the
  record they affect.
- **Folders are named by current label.** Renaming a label is a folder
  rename. Git handles it as a rename, not delete+add, as long as the
  contents move with it. The label inside the YAML stays in sync.
- **Comments use timestamp-prefixed filenames, not sequential numbers.**
  Sequential numbering means two clients adding "comment 0001" simultaneously
  produce a real conflict on the filename. Timestamp + short uuid suffix
  makes concurrent appends collision-free at the filesystem layer. We lose
  the friendly numbering — worth it for the merge ergonomics.
- **Doc folders are named by `filename`, not `type`.** `type` is mutable
  via `mk doc edit --type` and is not part of the schema's unique key
  (`UNIQUE(repo_id, filename)`). Putting it in the path would turn a
  type-change into a folder move and would forbid two docs sharing a
  filename across types. `type` lives inside `doc.yaml`.
- **`redirects.yaml` is per-repo and covers all renamable kinds.** It
  records every label rename and every collision-driven renumber/rename
  for issues, features, *and* documents (each entry carries a `kind`
  discriminator), so old references can still resolve via
  `mk issue show MINI-7`, `mk feature show old-slug`,
  `mk doc show old-filename.md`.

## Sync repo identity and mode switching

mk's sync data lives in a **dedicated git repository**, separate from any
of the project repos whose data it stores. One sync repo can hold the
data for many projects — one folder per prefix under `repos/`. There is
no "same-repo" mode where sync data lives inside a project's tree; the
sync repo is always its own thing.

This needs two bits of infrastructure: mk has to recognise a directory as
a sync repo (so it doesn't accidentally treat it as a project to track),
and project repos need a way to point at the sync repo.

### The sentinel: `mk-sync.yaml`

A file at the root of the sync repo, present iff this is a sync repo:

```yaml
# mk-sync.yaml (at the root of the sync repo)
schema_version: 1
created_at: 2026-05-09T14:22:00Z
```

When mk resolves a working tree (`internal/git/detect.go` plus the
`resolveRepo()` step), it checks for `mk-sync.yaml` at the root. If
present, mk switches to **sync-repo mode**:

- **No auto-registration.** `resolveRepo()` does not insert a row into
  `repos` for this directory. The sync repo is the storage layer, not a
  project being tracked.
- **Tracking commands fail with a helpful error.** Running
  `mk issue create` from inside the sync repo prints something like:
  *"this is an mk sync repo (`mk-sync.yaml` at root); cd to your project
  repo to track issues."*
- **A small set of sync-admin commands work here.** `mk sync verify`
  (check on-disk consistency — uuids unique, redirects resolve, no
  orphaned label references) and `mk sync inspect <prefix>` (read-only
  browsing) are the obvious ones. Anything beyond that and you're
  reinventing project commands in the wrong context.

### The reverse pointer: project → sync repo

Each project repo needs to know which sync repo to talk to. Stored as
`.mk/config.yaml`, checked into the project's git history so every
collaborator agrees on the destination:

```yaml
# .mk/config.yaml (in the project repo, checked in)
sync:
  remote: git@github.com:user/mk-data.git
```

This file carries **only the canonical remote URL**. Each user's local
clone location is tracked separately in the local mk DB (different
machines, different paths, possibly different cloning conventions). On
first sync setup, mk reads `.mk/config.yaml`, clones the remote into a
user-chosen local path, and records the `(remote → local path)` mapping
in DB.

### Bootstrap workflow

Creating a sync repo for the first time, from inside a project:

```
mk sync init <local-path> [--remote <git-url>]
    # creates the sync repo at <local-path>
    # writes mk-sync.yaml at its root
    # if --remote given, sets it as the git remote and writes
    #   .mk/config.yaml in the project repo
    # runs first export of the project's data
```

Joining a project that already has sync configured:

```
git clone <project>
mk sync clone [<local-path>]
    # reads .mk/config.yaml from the project repo
    # clones the sync remote to <local-path> (or a default location)
    # records (remote → local path) in DB
    # runs first import
```

### Edge case: nested checkout

If a user clones the sync repo *inside* a project working tree (e.g.,
just as a sibling subdirectory rather than at a fully separate path),
`git rev-parse --show-toplevel` follows the innermost `.git`, so running
`mk` from inside the sync subdirectory correctly resolves the sync repo
and the sentinel detection fires. Running `mk` from elsewhere in the
project sees only the project repo. This works, but it's not the
recommended layout — keep the sync repo somewhere outside any project
tree to avoid confusion.

### Decided: sync repos are non-bare

A bare git repo would dodge the mode-switching question entirely (no
working tree, nothing for mk to wander into). It also forfeits the main
reason we chose this design: that the data is browsable as a checked-out
folder of YAML and markdown. Sync repos are non-bare.

### What's in `issue.yaml`

Approximate shape, not a finalised schema:

```yaml
uuid: 0190c2a3-7f3a-7b2c-8b21-...   # UUIDv7, immutable
number: 7
state: in_progress
assignee: "geoff"                    # always quoted (see "YAML emit rules")
feature:
  label: auth-rewrite                # readability hint, refreshed on export
  uuid: 0190c2a3-7f3a-7b2c-8b21-bbbbbbbbbbbb   # canonical
tags: ["security", "p1"]
relations:
  blocks:
    - {label: MINI-12, uuid: 0190c2a3-7f3a-7b2c-8b21-cccccccccccc}
  relates_to:
    - {label: MINI-3,  uuid: 0190c2a3-7f3a-7b2c-8b21-dddddddddddd}
  duplicate_of: []
prs:
  - https://github.com/.../pull/42
created_at: 2026-05-01T10:14:22Z
updated_at: 2026-05-09T14:22:00Z
```

`description.md` holds the body so it's reviewable as a normal markdown
file.

## Identity model

The schema currently identifies records by composite natural keys —
issues by `(repo_prefix, number)`, features by `(repo_prefix, slug)`,
documents by `(repo_prefix, filename)`. These are great for humans but
fragile under collaborative edit: renaming a slug is a delete + create,
collisions on numbers force one client to give up "their" number, and
external references (Slack, GitHub) break on rename.

The fix is to add a **UUID per record**, separate from the label.

- **uuid** = identity. Immutable. Lives in YAML and in a new DB column.
  Match by uuid in sync; never by label.
- **label** = presentation. Mutable. The number for an issue, the slug for
  a feature, the filename for a doc. What humans type and read.
- **file path** = current label. Folder structure is named by label so
  `ls` is meaningful.

### UUID format

UUIDv7. Time-ordered prefix is useful for:

- Roughly-chronological directory listings without a separate sort step.
- Stable ordering of comment filenames (which embed a uuid suffix).
- Mild aid in debugging — the prefix tells you when a record was created.

Pure-Go libraries are available; no CGO concerns.

### Cross-references

Cross-references in YAML carry **both label and uuid**:

```yaml
blocks:
  - {label: MINI-7, uuid: 0190c2a3-7f3a-7b2c-8b21-...}
feature:
  label: auth-rewrite
  uuid: 0190c2a3-7f3a-7b2c-8b21-...
```

- **uuid is canonical.** Import resolves references by uuid; the label
  is purely a human-readability hint. This sidesteps every ordering
  problem during a renumber pass: an incoming `blocks: [{label: MINI-7,
  uuid: X}]` reference unambiguously means "the record with uuid X",
  regardless of what label MINI-7 maps to right now.
- **The label gets refreshed on export.** If the target's current label
  has changed (rename, renumber), the next `mk sync export` rewrites
  the label hint to match. Stale labels on disk are harmless — they're
  ignored on import.
- **If the uuid isn't found locally**, the reference is dangling: log a
  warning, leave the YAML as-is, do not insert a phantom DB row. This
  can happen when one user receives a reference to a record that
  another user hasn't pushed yet.

The DB stores foreign keys as integer ids (current behaviour); on export
we look up `(integer-fk → uuid + current label)` and write the pair; on
import we look up `(uuid → integer-fk)` and ignore the label except for
the export-side refresh logic. The label-as-hint is purely cosmetic.

### Schema impact

- Add `uuid TEXT NOT NULL UNIQUE` to `issues`, `features`, `documents`,
  `comments`. Generate at create time, never mutate. Backfill existing
  rows via `migrate()` with freshly-generated UUIDv7s.
- `tags`, `issue_relations`, `issue_pull_requests` don't need UUIDs —
  they're either pure values or composite relationships where the natural
  key is fine.
- `repos` gets a `uuid` too, used by `repo.yaml` so we can detect
  catastrophic wrong-repo merges (two users with the same prefix
  pointing at the same sync repo).
- **Relax `repos.path` UNIQUE.** Today `path` is `NOT NULL UNIQUE`;
  with whole-repo sync (see [Whole-repo sync](#whole-repo-sync)) we
  auto-register **phantom repos** for prefixes that exist on disk but
  have no local working tree on this machine. Those rows store
  `path = ''`. Replace the column-level UNIQUE with a partial index:
  `CREATE UNIQUE INDEX uniq_repos_path ON repos(path) WHERE path != '';`
  so multiple phantoms can coexist while real working trees still get
  the dedup guarantee.

### What's local-only and never synced

To pre-empt confusion: these `repos` columns are **client-state**, never
written to or read from `repo.yaml`:

- `repos.id` — autoincrement PK, diverges across machines.
- `repos.path` — local working tree location.
- `repos.created_at` — local DB row creation, not the project's true age.

What `repo.yaml` *does* carry: `uuid`, `prefix`, `name`, `remote_url`,
`next_issue_number` (advisory). The local row's `id` is determined at
import time by uuid lookup; `path` is whatever the user has locally
(or `''` for a phantom).

## Sync algorithm

### Mental model

- DB and disk are peers, both writeable.
- Match by uuid, not label.
- Last-writer-wins per record by `updated_at`.
- One asymmetric tiebreaker: **already-in-git beats local-only.**

### Bookkeeping: the `sync_state` table

```
sync_state(
  uuid TEXT PRIMARY KEY,
  kind TEXT,                  -- 'issue' | 'feature' | 'document' | 'comment'
  last_synced_at DATETIME,    -- last successful export OR import touched this uuid
  last_synced_hash TEXT       -- content hash at that sync
)
```

The **presence of a row** in `sync_state` is the "this record has been
synced before" flag. Absence means "local-only, never participated in a
sync". This is what makes deletion detection unambiguous:

| DB row exists? | sync_state row? | uuid seen on disk this import? | Meaning |
| --- | --- | --- | --- |
| Yes | No  | No  | New local, awaiting first export. **Leave.** |
| Yes | Yes | No  | Previously synced, now deleted remotely. **Propagate delete.** |
| Yes | Yes | Yes | Tracked record, normal compare-and-merge. |
| No  | No  | Yes | New record from elsewhere. **Insert.** |
| No  | Yes | Yes | Resurrection: previously deleted locally, came back from remote. **Re-insert.** |
| No  | Yes | No  | Cleanup: was deleted both sides. **Drop the sync_state row.** |

There is no per-pass boolean (no `last_seen_in_files`). The "did I see
this uuid on this run?" question is answered by an in-memory set built
during the import walk; it doesn't need to live in the DB.

### `mk sync import` (files → DB)

Phase 1 — **scan**: walk `repos/<prefix>/` and read every record folder
into memory. For each: extract uuid, label, `updated_at`, content hash,
and the raw YAML/markdown payloads. Build:

- `seen_uuids[kind]` — set of all uuids found on disk this pass.
- `incoming_labels[kind][label]` — uuid that owns each label on disk.

Phase 2 — **resolve label collisions**: for each kind, find DB rows
using a label that's also in `incoming_labels` but with a *different*
uuid. Those DB rows are local-only (otherwise git would have produced
a folder conflict). Renumber them: allocate `max(number)+1` for issues;
for features and documents, suffix the slug/filename (e.g., `slug-2`,
`filename-2.md`). Add an entry to `redirects.yaml` with the kind
discriminator. Log a `renumber` (or `rename`) audit op.

Phase 3 — **apply**: for each scanned record, look up by uuid in DB:

- **Not in DB, no `sync_state` row** — new record from elsewhere. Insert.
  Cross-references resolve by uuid; any uuid not yet present is dangling
  and gets recorded but not enforced.
- **Not in DB, `sync_state` row present** — resurrection. Re-insert.
- **In DB, content hash matches** — no-op.
- **In DB, content differs** — compare `updated_at`. File newer →
  overwrite DB. DB newer → leave (export will rewrite the file).
- **In DB, label differs** — label rename on the git side. Update DB
  label; add `redirects.yaml` entry.

After Phase 3, write `sync_state(uuid, kind, last_synced_at = NOW,
last_synced_hash)` for every uuid touched.

Phase 4 — **detect deletions**: for each DB row with a `sync_state` row
whose uuid is **not** in `seen_uuids`, propagate the delete. Drop the
`sync_state` row alongside.

### `mk sync export` (DB → files)

### `mk sync export` (DB → files)

For each record in DB:

1. Compute target path from current label.
2. If a different folder exists for this uuid (label changed in DB since
   last export) → `git mv` the folder, don't rewrite-and-delete.
3. Write content **only if hash differs**. This is what keeps git diffs
   surgical — re-running `mk sync` on an unchanged DB produces zero file
   modifications.
4. Refresh label hints inside cross-references: any `{label, uuid}` pair
   pointing at this record gets its `label` field updated to the current
   value if it's stale. Pure cosmetic on-disk; uuid is canonical.
5. Upsert `sync_state(uuid, kind, last_synced_at = NOW, last_synced_hash)`.

After writing, walk on-disk folders. Any folder whose uuid is no longer
in DB is a local deletion — with `--prune` propagate it (and drop the
matching `sync_state` row); otherwise warn.

### Order of operations

```
mk sync   ≡   git pull
              mk sync import
              mk sync export
              git add . && git commit
              git push
```

`import` runs before `export` deliberately. Imports might trigger
renumbers (collision resolution); we want those reflected in the same
`export` so the working tree converges in one commit. If `export` ran
first, we'd push stale numbering and immediately have to push again.

A single `mk sync` should produce **at most one commit**. Partial failure
should not leave a half-staged tree — easiest path is to stage everything
in a temp working area and only `git add` on full success.

`mk sync` should be process-locked: only one `mk sync` runs against a
given DB at a time, and concurrent `mk issue create` (etc.) calls block
on a short advisory lock for the duration of the sync. Otherwise an
in-flight create can allocate a `next_issue_number` that import is
about to assign to a remote uuid.

### Whole-repo sync

`mk sync` always processes the **entire** sync repo's content, not just
the prefix matching the current working tree. Selective sync turns out
to be painful in practice: stale-data-in-working-tree, partial pushes,
and "I have to remember to sync project B too" ergonomics.

Concretely:

- `git pull` brings in changes for every prefix in the sync repo.
- `import` walks every `repos/<prefix>/` folder, not just the local
  project's.
- `export` walks every locally-known repo, not just the active one.
- One `mk sync`, one commit (when there's anything to commit), all
  prefixes covered.

**Phantom repos.** A prefix can exist in the sync repo without a local
working tree on this machine (because the user only checked out some
subset of the projects mk tracks). On import, those rows still get
written to DB — with `path = ''` (see [Schema impact](#schema-impact)
on the `path` UNIQUE relaxation). They're queryable via `mk` (e.g.,
`mk issue list -R OTHER` works), but `resolveRepo()` will not pick a
phantom as "the current repo"; it requires a real working tree path.

Phantom repos are upgraded to real ones automatically the first time
the user runs `mk` from inside the matching project's working tree:
`resolveRepo()` finds an existing row by uuid (read from `repo.yaml`),
populates `path`, and the row stops being phantom.

### Bootstrap and first sync

The first time data flows between a DB and a sync repo is the most
dangerous moment for the algorithm — naïvely applying the regular
`import → renumber-on-collision → export` flow can silently shift a
collaborator's entire issue stream. Bootstrap is therefore split into
two non-overlapping commands.

**`mk sync init <local-path> [--remote <git-url>]`** — export-only.

- Runs from inside a project repo.
- Creates the sync repo at `<local-path>` if it doesn't exist
  (initialises `mk-sync.yaml`, sets remote if `--remote` given).
- **Refuses to proceed if the remote already has any data.** Bootstrap
  via `init` is for "I'm setting up sync for the first time and the
  remote is empty"; everything else uses `clone`.
- Writes `.mk/config.yaml` in the project repo with the remote URL.
- Performs an initial `export` of the project's data, commits, pushes.

**`mk sync clone [<local-path>]`** — import-only with explicit handling
for pre-existing local data.

- Runs from inside a project repo (reads `.mk/config.yaml` for the
  remote URL).
- Clones the sync repo to `<local-path>` (or a default).
- Records `(remote → local path)` in DB.
- **If local DB has any rows for this prefix:** refuses to proceed
  unless `--allow-renumber` is passed. With `--allow-renumber`, prints
  a preview ("the following local issues will be renumbered: …")
  before applying. Without `--allow-renumber`, exits with an error
  pointing at the preview command.
- If local DB is empty for this prefix, imports normally.

**`mk sync clone --dry-run`** prints the preview unconditionally — what
would be imported, what would be renumbered — without touching DB or
disk.

These two commands are the only entry points where collisions on
"first contact" need special framing; once a project has both a DB
and a synced state, normal `mk sync` is the steady-state path and
its collision policy is the regular `already-in-git wins` rule.

### Collision policy in detail

The same rule applies across all renamable kinds — issues, features,
documents — wherever a label is used as the folder name:

- The uuid present in the **just-imported file** keeps the label. It's
  already in git history; rewriting it would surprise everyone who's
  pulled.
- The uuid present **only in DB** (no file on disk yet) is given a new
  label deterministically:
  - **Issues:** renumber to `max(number) + 1` for the repo.
  - **Features:** suffix the slug — `auth-rewrite` → `auth-rewrite-2`,
    incrementing until unique.
  - **Documents:** suffix the filename — `auth-overview.md` →
    `auth-overview-2.md`, incrementing until unique.
- A `redirects.yaml` entry records the move, with a `kind` discriminator:

```yaml
- kind: issue
  old: MINI-7
  new: MINI-12
  uuid: 0190c2a3-...
  changed_at: 2026-05-09T14:30:00Z
  reason: label_collision

- kind: document
  old: auth-overview.md
  new: auth-overview-2.md
  uuid: 0190c2a3-...
  changed_at: 2026-05-09T14:30:00Z
  reason: label_collision

- kind: feature
  old: auth-rewrite
  new: auth-rewrite-2
  uuid: 0190c2a3-...
  changed_at: 2026-05-09T14:30:00Z
  reason: label_rename
```

- The next `mk sync export` writes out the renumbered/renamed folder.

This rule is what makes the algorithm **consistent across machines**:
every client running `import` against the same git state arrives at the
same outcome. "Already-in-git wins" is a deterministic, observable
state, unlike "earlier `created_at` wins" which depends on details
neither client can verify.

### What references get rewritten on renumber/rename?

With label+uuid cross-references, the answer is largely **nothing has
to be rewritten for correctness**. Imports resolve by uuid; stale
`label` hints are ignored. Two practical points:

- **Refresh on export, not on import.** The next `mk sync export` walks
  cross-references for any record that touched DB this run and refreshes
  the `label` field of each `{label, uuid}` pair to match the current
  label of the target uuid. This keeps the on-disk view tidy without
  introducing a "rewrite the whole tree on every renumber" pass.
- **Free-text mentions of the old label** inside markdown bodies
  (`description.md`, comment bodies) are not rewritten. Regex-replacing
  `\bMINI-7\b` is tempting but unsafe — a comment might be quoting an
  old discussion of the original MINI-7. Emit a warning listing every
  body that contains the renumbered token; humans can decide.
- **External references** (commit messages, PR descriptions, Slack) are
  out of reach. `redirects.yaml` plus `mk issue show MINI-7` resolving
  via the redirect chain is what makes these tolerable.

### `next_issue_number` becomes advisory

With collision handling real, the `next_issue_number` column on `repos`
is just a cache for `max(number) + 1`. Keep it for the fast path (avoids
a `MAX()` query on every issue create), but **always recompute on import**
so a remotely-created MINI-50 doesn't get re-used locally as MINI-50.

## Edge cases

### YAML body merge conflicts

If two clients edit `issue.yaml` for the same uuid, git produces a
standard text merge conflict. Don't try to be clever: punt to git's
conflict markers and human resolution. After the human resolves,
`mk sync import` re-parses and proceeds normally. The cost of an
occasional manual merge is much lower than the cost of an algorithm
that does field-level YAML merging.

### Concurrent comment additions

Per-comment files with timestamp-prefixed filenames means concurrent
appends produce *different* filenames, not conflicts. Git merges by
adding both files. mk's view sorts comments by `created_at`, so
ordering is preserved.

### Concurrent label renames

Alice renames `MINI-7` → `MINI-7-renamed-by-alice`. Bob renames
`MINI-7` → `MINI-7-renamed-by-bob`. They sync. Git sees two folder
renames of the same source — this is a merge conflict on the directory
tree. Punt to human resolution. Rare.

### Deletion vs rename

Without uuid, `MINI-7/` disappearing and `MINI-12/` appearing looks
like delete + create. With uuid, if both folders carry the same uuid,
sync recognises it as a rename and preserves history (`updated_at`,
relation references, etc.). This is one of the main reasons we want
uuids.

### Half-synced state from a crash

If `mk sync` crashes mid-export (some files written, others not), the
working tree may be in a state that doesn't reflect any consistent DB
snapshot. Mitigation: stage the export in a scratch area and only swap
into the working tree atomically when the full export is computed. Worst
case: re-running `mk sync` recovers, since import-then-export is
idempotent against a clean DB.

### Repo prefix collision in shared git repo

If two machines initialise mk repos with the same prefix (`MINI`) for
two unrelated working trees, and both push to the same git sync repo,
their data would merge into the same `repos/MINI/` folder and chaos
would ensue. Mitigation: `repo.yaml` carries the repo's own uuid, and
import refuses to merge a folder whose `repo.yaml` uuid doesn't match
the local DB's. User has to either delete one side or rename the prefix.

### Case-insensitive filesystems

macOS (APFS, HFS+ default) and Windows (NTFS default) are
case-insensitive. Document filenames allow any UTF-8 except `/\NUL` and
control chars (`ValidateDocFilenameStrict`), so `auth-overview.md` and
`Auth-Overview.md` are different DB rows but the same folder on those
filesystems. The import path must canonicalise:

- **NFC normalise** every disk-derived label and DB-side label before
  comparison (avoids precomposed vs. decomposed Unicode forms).
- **Detect case-insensitive collisions on import** between distinct
  uuids and refuse to merge them silently. Treat as a label-collision
  case (loser gets renamed via the standard mechanism).

Same applies to feature slugs in principle, though the validator
already constrains them to lowercase kebab-case so case collisions are
unlikely.

### YAML emit rules

`assignee` and `name` are free-form strings; values like `assignee: on`
or `assignee: no` round-trip through YAML 1.1 as booleans. Tag values
similarly. The emit path must:

- **Always quote user-supplied scalar strings** in YAML output
  (`assignee: "geoff"` rather than `assignee: geoff`). This is cheap
  and removes an entire class of round-trip surprises.
- **Refuse to import a value whose YAML-decoded type doesn't match the
  schema's expected type** for that field. e.g., if `assignee` decodes
  to a bool, fail loudly rather than coercing.

### Concurrent `mk sync` invocations across machines

Two clients run `mk sync` against the same remote at the same time.
Both compute renumbers from their local view, both push. The first
push wins; the second gets a `non-fast-forward` rejection. The losing
client must `git pull`, which surfaces conflicts (or merges cleanly),
then re-run `mk sync import` to re-resolve and re-export. The
"one commit per `mk sync`" promise holds within a single invocation;
across two contended invocations it becomes "one commit per successful
sync, possibly preceded by a re-run". Documenting this so users aren't
surprised when a contended sync needs a retry.

### Half-synced state from a crash

If `mk sync` crashes mid-export (some files written, others not), the
working tree may be in a state that doesn't reflect any consistent DB
snapshot. Mitigation: stage the export in a scratch area and only swap
into the working tree atomically when the full export is computed. Worst
case: re-running `mk sync` recovers, since import-then-export is
idempotent against a clean DB.

### Threat model: hand-edited working tree

`mk sync` treats files-newer-than-DB as authoritative. That means a
user (or anything with FS write access) can hand-edit `issue.yaml`
between sync runs and `mk sync import` will accept the edit as truth.
This is by design — it's how a user could fix a YAML conflict by
hand — but it means the sync repo working tree is **not** a tamper-proof
log. Anyone who can write the working tree can rewrite history (in the
sense of "future state of the DB"). Combine with normal git protections
on the remote.

## Out of scope (deliberately)

- **CRDTs.** Last-writer-wins per record is enough.
- **Field-level YAML merging.** Git text merge + human resolution is fine.
- **A background sync daemon.** Sync is explicit, user-triggered, or
  optionally wired to a git hook by the user.
- **Partial sync (`mk sync issues/MINI-7`).** Always whole-repo. Easier
  to reason about, and the data volumes are small.
- **History (`history` table) sync.** v1 keeps audit log local. If we
  later want cross-machine audit, append-only `history.jsonl` is the
  obvious shape (append-only files merge cleanly in git).
- **`tui_settings` sync.** Local UI prefs don't belong in git.
- **Real-time merge resolution UI.** If git produces a conflict, the
  user resolves it the way they resolve any other git conflict.
- **In-repo sync mode (deliberately v2, not permanently out).** Some
  projects will want their mk data to ship alongside the code (open-source
  libraries where issues should be visible to anyone who clones; small
  personal projects where setting up a separate sync repo is overkill).
  In that mode, sync data would live at `.mk/data/` inside the project's
  own working tree, and `mk sync` would commit/push against the project
  repo rather than a dedicated sync repo. The core import/export
  algorithm doesn't change — only where it reads and writes. The
  `.mk/config.yaml` shape is already forward-compatible: a future
  `sync.mode: in-repo` value (alongside today's implicit `external`)
  flips the behaviour. Deferred to v2 to keep the v1 mental model
  single-track.

## Open questions to validate before implementation

1. **Is `mk sync` one command or split (`mk sync pull` / `mk sync push`)?**
   One command is simpler and matches how most users think. Split gives
   power users the ability to import without exporting (e.g., to inspect
   a colleague's state without committing local changes). Lean towards
   one command with `--no-export` / `--no-import` / `--no-push` flags
   for the fine-grained cases.
2. **Should `mk init` set up the sync repo path?** Or is it a separate
   `mk sync init` step? The latter keeps `mk init` lightweight and lets
   sync be opt-in. Strong lean towards keeping them separate.
3. **Comment filename scheme: `<timestamp>--<uuid>.yaml` vs `<uuid>.yaml` only.**
   UUIDv7 already encodes the timestamp, so the prefix is redundant for
   ordering — but the human-readable date in the filename is a real
   usability win (`ls comments/` is meaningful at a glance). Lean
   towards keeping the timestamp prefix even though it's technically
   duplicated information.
4. **Should `redirects.yaml` ever be pruned?** Redirect entries
   accumulate forever. After N years, very old redirects may not be
   worth resolving. Probably never prune in v1; revisit if it becomes
   a problem.
5. **What command surface for the audit op?** `mk sync` writes a
   `history` row of op `sync` per run, plus per-renumber/rename
   sub-ops. Per the existing pattern in `internal/cli/audit.go`, this
   is one `recordOp` call per logical action.
6. **What goes in `mk-sync.yaml` beyond `schema_version` and `created_at`?**
   Probably nothing in v1. Candidates for later: advisory list of
   expected prefixes, originating mk version. The repo's own uuid
   already lives in `repo.yaml` per prefix.
7. **Phantom-repo lifecycle.** When the user runs `mk` from inside a
   project working tree whose prefix is a phantom in DB, do we:
   (a) auto-upgrade the phantom by populating `path`,
   (b) prompt the user, or
   (c) require an explicit `mk sync attach` command?
   Option (a) is the principle-of-least-surprise default but means a
   `cd` into a project's directory silently mutates DB state. Probably
   fine, but worth deciding before shipping.

## Implementation phasing

The work is naturally staged. Each step is independently shippable.

1. **Add `uuid` columns + backfill migration.** Issues, features,
   documents, comments, repos all get a UUIDv7 column. Generated at
   create time, never mutated. `migrate()` backfills existing rows.
   Relax `repos.path` UNIQUE to a partial index so phantoms can exist
   later. Low risk, no user-visible change.
2. **Build `mk sync export`.** One-way DB → files. Useful immediately as
   a backup / inspection tool, even with no import. Establishes the
   YAML emit rules (always quote user strings) and the
   write-only-if-hash-differs discipline.
3. **Build `mk sync import`.** One-way files → DB. Exercises the
   `sync_state` state machine, label-collision resolution across all
   three kinds (issues, features, documents), and the phantom-repo
   creation path. Adds `redirects.yaml` plumbing.
4. **Wire them together as `mk sync`,** with the pull / commit / push
   shell, the process lock, and the whole-repo (multi-prefix)
   walking. Sentinel detection (`mk-sync.yaml`) and project-side
   `.mk/config.yaml` reverse pointer land here, plus the
   `mk sync init` / `mk sync clone` bootstrap commands with their
   `--allow-renumber` / `--dry-run` flags.
5. **Comment filename scheme + timestamps.** Could land in step 2 or 3,
   but easier to validate the layout against real data first.
6. **Documentation pass.** Update `SKILL.md` so AI agents know how to
   drive `mk sync`, what `mk sync init` vs. `mk sync clone` mean, and
   what to expect on collision.

Steps 1 and 2 can be merged independently. Steps 3 and 4 are tightly
coupled and probably want to land together.
