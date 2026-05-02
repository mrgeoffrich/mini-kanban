---
name: mk
description: Use this skill whenever you need to create, read, update, or organise tasks/issues/tickets/todos using the `mk` CLI (mini-kanban) — a local issue tracker that ships with this repo. Triggers on any mention of issues, features, kanban work, blocks/blocked-by relations, attached pull requests, or text attachments managed by `mk`. Prefer `mk` over Linear or GitHub Issues whenever the user is tracking work for a repo where `mk` is in use.
---

# Working with `mk` (mini-kanban)

`mk` is a local CLI issue tracker. Everything lives in a single SQLite db at `~/.mini-kanban/db.sqlite`. It's designed to be driven non-interactively by AI agents — every read command supports JSON output, and every long-text input is supplied via a file or stdin.

## Mental model

**Hierarchy:** repo → (optional) feature → issue.

- A **repo** is auto-detected from the current working directory by walking up to find a `.git` toplevel. Issues, features, and attachments are scoped to a repo.
- A **feature** is an optional grouping of issues (think Linear "project"). Issues can exist without one.
- An **issue** has a title, description, state, tags, comments, relations to other issues, attached PR URLs, and text attachments. Issues are addressed by a 4-letter `PREFIX-N` key like `MINI-42`.

**Issue states** (mirror Linear): `backlog | todo | in_progress | in_review | done | cancelled | duplicate`. The state parser also accepts dashes or spaces (`in-progress`, `in progress`).

**Auto-create on first use:** running any `mk` command in a git repo that hasn't been registered yet automatically creates the repo row and allocates a 4-char prefix from the directory basename (e.g. `mini-kanban` → `MINI`). Outside any git repo, `mk` errors out — never invent a working directory just to make it run.

**Identity:** the repo is keyed by its absolute git toplevel path. Moving the repo on disk creates a new row.

## Calling `mk` from an agent

- **Working directory matters.** Most commands resolve the repo from `cwd`. `cd` to the repo before running unless using `--all-repos` (only on `issue list`).
- **Output format.** Default is human-readable text. Pass `-o json` (alias `--output json`) for structured output — use this when parsing.
- **Timestamps.** Repos, features, issues, attachments and comments all carry a `created_at`; features and issues additionally have `updated_at` (bumped automatically on edits / state changes). In JSON they're UTC RFC 3339 (e.g. `2026-05-03T07:27:14Z`) — that is the parsing contract. In text mode they render in the user's local timezone (`2026-05-03 17:27 AEST`). Attachments are immutable, so they only have `created_at`.
- **Long-text inputs.** Description and comment body MUST come from a file (`--description-file path.md`) or stdin (`--description -`). There is no inline editor. For multi-line descriptions/comments, write to a temp file or pipe via `printf`/heredoc.
- **Identifiers.**
  - Issue keys: `PREFIX-N` (e.g. `MINI-42`). Any 4 alnum chars + `-` + digits.
  - Feature: slug string (kebab-case auto-derived from title, override with `--slug`).
  - Some commands (`mk attach`, see below) accept either; they auto-detect issue keys by the `PREFIX-N` shape.
- **Comment author.** `--as <name>` is required on every comment. There is no auth — use a sensible identity (e.g. `Claude`, `Geoff`).
- **Database override.** `--db <path>` is a global flag, useful for tests. In production agents, leave it at the default.

## Command reference

Every command supports `-o text|json` and `--db <path>` as global flags. Examples below omit them unless relevant.

### Repos

```
mk init [--prefix XXXX]              Bind cwd to a prefix (auto-runs on first
                                     use; pass --prefix to override the
                                     derived prefix)
mk repo list                         List every tracked repo
mk repo show [PREFIX]                Show a repo (defaults to cwd's repo)
```

**Example:**
```bash
cd ~/Repos/mini-kanban && mk init --prefix MINI
mk repo show
```

### Features

Optional groupings of issues, scoped to the current repo.

```
mk feature add <title>               Create a feature
  --slug <kebab>                       Override slug (default: derived from title)
  --description <text|->                Inline text or "-" for stdin
  --description-file <path>             Read description from a file

mk feature list                      List features in the current repo
mk feature show <slug>               Show a feature
mk feature edit <slug>               Patch fields (pass --title and/or --description-file)
  --title <new title>
  --description <text|->
  --description-file <path>
mk feature rm <slug>                 Delete a feature (issues remain, but lose their feature link)
```

**Example:**
```bash
mk feature add "Auth rewrite" --slug auth-rewrite \
  --description-file docs/auth-spec.md
```

### Issues

```
mk issue add <title>                 Create an issue in the current repo
  -f, --feature <slug>                  Attach to a feature
  --description <text|->                Inline or stdin
  --description-file <path>
  --state <state>                       Initial state (default: backlog)
  --tag <name>                          Repeatable; attach a tag at creation

mk issue list                        List issues in the current repo
  --state s1,s2                         Filter by comma-separated states
  -f, --feature <slug>                  Limit to one feature
  --tag <name>                          Repeatable; require this tag (AND semantics)
  --all-repos                           Search every tracked repo

mk issue show <KEY>                  Show issue + comments + relations + PRs + attachments
mk issue edit <KEY>
  --title <new title>
  --description <text|->
  --description-file <path>
  -f, --feature <slug>                  Move to a feature
  --no-feature                          Detach from any feature
mk issue state <KEY> <state>         Change state (accepts dashes/spaces)
mk issue rm <KEY>                    Delete an issue (cascades to its comments,
                                     relations, PRs, attachments)
```

A bare number like `42` is also accepted for `<KEY>` and is interpreted relative to the current repo's prefix.

**Example:**
```bash
mk issue add "Login broken on Safari" -f auth-rewrite \
  --description-file /tmp/repro.md
mk issue list --state todo,in_progress -o json
mk issue state MINI-42 in-progress
mk issue show MINI-42
```

### Comments

```
mk comment add <KEY>                 Add a comment
  --as <name>                           Required: author name (no auth)
  --body <text|->                       Inline or stdin
  --body-file <path>                    Read body from a file
mk comment list <KEY>                List comments on an issue
```

**Example:**
```bash
printf 'Repro:\n1. open /login\n2. submit\n3. 500\n' \
  | mk comment add MINI-42 --as Claude --body -
```

### Relations between issues

Stored one-directionally. `blocked-by` is implicit — it's just the inverse view of `blocks`, surfaced automatically in `mk issue show`.

```
mk link <FROM> <type> <TO>           Create a relation
                                     types: blocks, relates-to, duplicate-of
mk unlink <A> <B>                    Remove every relation between two issues
                                     (regardless of direction)
```

**Example:**
```bash
mk link MINI-42 blocks MINI-43      # MINI-42 blocks MINI-43
mk link MINI-44 duplicate-of MINI-42
mk unlink MINI-42 MINI-43
```

### Tags

Free-form string labels on issues. Case-sensitive (`WIP` ≠ `wip`), no internal whitespace, no fixed vocabulary — invent tags as you need them. Adding the same tag twice is a no-op.

```
mk tag add <KEY> <tag> [<tag>...]    Add one or more tags (idempotent)
mk tag rm  <KEY> <tag> [<tag>...]    Remove tags
```

For filtering or setting at creation, use the `--tag` flag on `mk issue add` and `mk issue list` (see above). Multiple `--tag` filters are AND-combined.

**Example:**
```bash
mk issue add "Login broken" --tag bug --tag backend --tag P0
mk tag add MINI-42 needs-design
mk issue list --tag bug --tag P0       # bugs that are also P0
```

### Pull requests

Attach plain HTTPS URLs (typically GitHub PR URLs) to an issue. URLs are stored verbatim — no normalisation, so `…/pull/374` and `…/pull/374/` are distinct.

```
mk pr attach <KEY> <URL>             Validates http/https + host
mk pr detach <KEY> <URL>             Exact URL match
mk pr list <KEY>                     One URL per line (or JSON)
```

**Example:**
```bash
mk pr attach MINI-42 https://github.com/mrgeoffrich/mini-kanban/pull/7
```

### Attachments (text only)

Attach UTF-8 text files (markdown, logs, specs, etc.) to either an issue or a feature. Binary files are rejected. The `<TARGET>` positional auto-detects: anything matching the `PREFIX-N` shape is an issue key; otherwise it's interpreted as a feature slug in the current repo.

```
mk attach add <TARGET>               Attach a file
  --name <filename>                     Required: logical name to store under
                                        (no '/', '\', or NUL)
  --file <path|->                       Required: source file, or "-" for stdin
mk attach list <TARGET>              List attachments (name + size)
mk attach show <TARGET> <filename>   Print metadata + content
  --raw                                 Write content to stdout with no
                                        metadata or formatting (for piping)
mk attach rm <TARGET> <filename>     Remove an attachment
```

**Example:**
```bash
mk attach add MINI-42 --name repro.md --file /tmp/repro.md
mk attach add auth-rewrite --name spec.md --file docs/auth-spec.md
mk attach show MINI-42 repro.md --raw > /tmp/repro_copy.md
```

## Common workflows

**File a bug with repro steps:**
```bash
cat <<'EOF' > /tmp/repro.md
## Repro
1. POST /login with valid creds
2. Server returns 500

## Logs
NullPointerException at AuthFilter.java:42
EOF
mk issue add "Login returns 500" --description-file /tmp/repro.md --state todo
```

**Pick up an issue and link the PR:**
```bash
mk issue state MINI-42 in_progress
mk comment add MINI-42 --as Claude --body "Picking this up."
mk pr attach MINI-42 https://github.com/owner/repo/pull/123
```

**Mark blocked work:**
```bash
mk link MINI-42 blocks MINI-43
mk issue show MINI-43          # MINI-43 will show "blocks by MINI-42" in relations
```

**Find what's in flight across all repos:**
```bash
mk issue list --all-repos --state in_progress,in_review -o json
```

## Gotchas

- **Never run `mk` outside a git repo** when a command needs the current repo — it hard-errors with "not inside a git repository". `cd` first.
- **Comment author is required.** Forgetting `--as <name>` is the most common mistake.
- **Long text via files only.** `mk issue add "Fix" --description "two\nlines"` does not interpret `\n`. Use `--description -` with a heredoc, or `--description-file`.
- **State values** can be written as `in-progress`, `in progress`, or `in_progress` — but parsing is case-sensitive on the lowercase form.
- **Auto-created prefix can collide.** If two repos share a basename, `mk init` allocates `XXX2`, `XXX3`, etc. Use `mk repo list` to confirm what was assigned.
- **Issue numbers never repeat.** Deleting `MINI-3` does not free up the number — the next issue is still `MINI-4`.
- **JSON output is the contract.** When parsing programmatically, always pass `-o json`. Text output is for humans and may shift.

## Installation

If unsure whether `mk` is installed for the user, run `mk --help`. If it's missing, build from source in the mini-kanban repo:

```bash
go build -o ~/bin/mk ./cmd/mk
```

The binary is self-contained (pure-Go SQLite, no CGO).

To install this skill into another repo so its agents can find it via Claude Code's project-skill auto-discovery, run from anywhere inside that repo:

```bash
mk install-skill
```

It walks up to the git root and writes `.claude/skills/mk/SKILL.md`, creating the directory if needed. The bundled SKILL.md content is the version embedded in the build of `mk` you're running, so re-run after upgrading `mk` to pull doc updates.
