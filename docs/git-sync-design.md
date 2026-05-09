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
          2026-05-09T14-22-00Z--<uuid8>.yaml
          2026-05-09T14-22-00Z--<uuid8>.md
    features/
      auth-rewrite/
        feature.yaml             # uuid, slug, title, timestamps
        description.md
    docs/
      architecture/
        overview/
          doc.yaml               # uuid, type, source_path, links[], timestamps
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
- **`redirects.yaml` is per-repo.** It records every label rename and
  every collision-driven renumber, so old references (in PR descriptions,
  Slack, etc.) can still resolve via `mk issue show MINI-7`.

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
assignee: geoff
feature: auth-rewrite               # slug reference, resolved at import
tags: [security, p1]
relations:
  blocks: [MINI-12]                 # label references, resolved at import
  relates_to: [MINI-3]
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

Cross-references in YAML use **labels** (`blocks: [MINI-7]`), not UUIDs,
for human readability. They're resolved to uuid at import time. If a
label is ambiguous because of an unresolved collision, the uuid embedded
in the colliding `issue.yaml` is the tiebreaker; if a label is missing
because of a renumber, `redirects.yaml` provides the lookup.

The DB stores foreign keys as integer ids (current behaviour); on export
we write labels for readability; on import we resolve labels back to uuid
and then to integer fk. Three layers, but each translation is local and
mechanical.

### Schema impact

- Add `uuid TEXT NOT NULL UNIQUE` to `issues`, `features`, `documents`,
  `comments`. Generate at create time, never mutate. Backfill existing
  rows via `migrate()` with freshly-generated UUIDv7s.
- `tags`, `issue_relations`, `issue_pull_requests` don't need UUIDs —
  they're either pure values or composite relationships where the natural
  key is fine.
- `repos` could get a uuid eventually if repo rename becomes a feature,
  but `prefix` is fine as the stable key for now.

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
  kind TEXT,                  -- 'issue', 'feature', 'document', 'comment'
  last_synced_at DATETIME,
  last_synced_hash TEXT,      -- content hash at last sync
  last_seen_in_files BOOL     -- true if record was on disk last sync
)
```

This is what makes deletion detection possible. Without it, "record exists
in DB but not on disk" is ambiguous — could be a remote delete, could be
local-only-not-yet-exported. With it, we know.

### `mk sync import` (files → DB)

For each record folder under `repos/<prefix>/`:

1. Read uuid, label, `updated_at`, content from YAML + body files.
2. Look up by **uuid** in DB:
   - **Not in DB** — new record from elsewhere. Insert.
   - **In DB, content hash matches** — no-op. Stamp `last_seen_in_files = true`.
   - **In DB, content differs** — compare `updated_at`. File newer →
     overwrite DB. DB newer → leave (export will rewrite the file).
   - **In DB, label differs from file's label** — label rename happened on
     the git side. Update DB label, append a `redirects.yaml` entry.
3. Check label collision: does another DB row use this label with a
   different uuid? If so, that other row is local-only (otherwise git
   would have flagged a folder conflict). **Renumber it** to
   `max(number)+1`, add a `redirects.yaml` entry, log a `renumber`
   audit op.

After walking the disk, scan DB rows whose uuid was *not* seen this pass:

- `last_seen_in_files = true` previously → deleted in git, propagate the delete.
- `last_seen_in_files = false` (never synced) → local-only awaiting export. Leave.

### `mk sync export` (DB → files)

For each record in DB:

1. Compute target path from current label.
2. If a different folder exists for this uuid (label changed in DB since
   last export) → `git mv` the folder, don't rewrite-and-delete.
3. Write content **only if hash differs**. This is what keeps git diffs
   surgical — re-running `mk sync` on an unchanged DB produces zero file
   modifications.
4. Update `sync_state.last_synced_at` and `last_synced_hash`.

After writing, walk on-disk folders. Any folder whose uuid is no longer
in DB is a local deletion — with `--prune` propagate it; otherwise warn.

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

### Collision policy in detail

When import finds the same label used by two different uuids:

- The uuid present in the **just-imported file** keeps the label. It's
  already in git history; rewriting it would surprise everyone who's
  pulled.
- The uuid present **only in DB** (no file on disk yet) is renumbered to
  `max(number) + 1` for the repo.
- A `redirects.yaml` entry records the move:

```yaml
- old: MINI-7
  new: MINI-12
  uuid: 0190c2a3-...
  renumbered_at: 2026-05-09T14:30:00Z
  reason: label_collision
```

- The next `mk sync export` writes out the renumbered folder.

This rule is what makes the algorithm **consistent across machines**:
every client running `import` against the same git state arrives at the
same renumbering. "Already-in-git wins" is a deterministic, observable
state, unlike "earlier `created_at` wins" which depends on details neither
client can verify.

### What references get rewritten on renumber?

Mechanical:

- The folder name itself.
- The `number` field in `issue.yaml`.
- `relates_to` / `blocks` / `duplicate_of` lists in *other* `issue.yaml`
  files that pointed at the loser. (Lists are label refs; the import
  pass resolves through `redirects.yaml`, so technically these don't
  need to be rewritten in place — old labels still resolve. We can
  rewrite opportunistically on next export to keep the on-disk state
  tidy, but it's not required for correctness.)
- `document_links` references.

Not rewritten:

- Free-text mentions of the old label inside markdown bodies (`description.md`,
  comment bodies). Regex-replacing `\bMINI-7\b` is tempting but unsafe — a
  comment might be quoting an old discussion of the original MINI-7. Emit
  a warning listing every body that contains the renumbered token; humans
  can decide.
- External references (commit messages, PR descriptions, Slack). Out of
  reach. The `redirects.yaml` entry plus `mk issue show MINI-7` resolving
  via redirect is what makes these tolerable.

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
their data merges into the same `repos/MINI/` folder and chaos ensues.
Mitigation: `repo.yaml` stores the original mk DB's uuid for the repo
itself (this is the one place we'd use a uuid for `repos`), and import
refuses to merge a folder whose `repo.yaml` uuid doesn't match the
local DB's. User has to either delete one side or rename the prefix.

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
3. **Comment filename scheme: timestamp + uuid8, or just uuid7?** UUIDv7
   already encodes the timestamp, so `uuidv7.yaml` would be self-sorting
   and shorter. Tradeoff: less human-readable. The timestamp prefix
   is a usability vs. compactness call.
4. **Should `redirects.yaml` ever be pruned?** Redirect entries
   accumulate forever. After N years, very old redirects may not be
   worth resolving. Probably never prune in v1; revisit if it becomes
   a problem.
5. **What command surface for the audit op?** `mk sync` writes a
   `history` row of op `sync` per run, plus per-renumber `renumber` ops.
   Per the existing pattern in `internal/cli/audit.go`, this is one
   `recordOp` call per logical action.
6. **What goes in `mk-sync.yaml` beyond schema version?** Could carry an
   advisory list of expected prefixes, the originating mk version, or a
   uuid of the sync repo itself (handy for ruling out catastrophic
   wrong-repo mistakes). v1 can ship with just `schema_version` and
   `created_at` and grow from there.

## Implementation phasing

The work is naturally staged. Each step is independently shippable.

1. **Add `uuid` columns + backfill migration.** No sync yet. Just makes
   identity stable for future work, and starts populating UUIDs on every
   new record. Low risk, no user-visible change.
2. **Build `mk sync export`.** One-way DB → files. Useful immediately as
   a backup / inspection tool, even with no import.
3. **Build `mk sync import`.** One-way files → DB. Exercises the
   collision detection and label-resolution paths.
4. **Wire them together as `mk sync`,** with the pull / commit / push
   shell. Add `redirects.yaml` plumbing. This is also where the sentinel
   detection (`mk-sync.yaml`) and project-side `.mk/config.yaml` reverse
   pointer land, plus the `mk sync init` / `mk sync clone` bootstrap
   commands.
5. **Comment filename scheme + timestamps.** Could land in step 2 or 3,
   but easier to validate the layout against real data first.
6. **Documentation pass.** Update `SKILL.md` so AI agents know how to
   drive `mk sync` and what to expect on collision.

Steps 1 and 2 can be merged independently. Steps 3 and 4 are tightly
coupled and probably want to land together.
