# mini-kanban (`mk`)

A local-first, single-binary issue tracker designed to be driven equally well by humans and AI agents.

`mk` is a CLI and TUI on top of a single SQLite file at `~/.mini-kanban/db.sqlite`. It auto-detects which repo you're in, gives every issue a `PREFIX-N` key (e.g. `MINI-42`), and exposes every read in JSON so scripts and skills can drive it without parsing text. Long-text fields (descriptions, comments, doc bodies) are supplied via files or stdin, never inline flags — friendly to multi-line input from agents.

## Features

- **Issues, features, comments, relations, tags, PR attachments** — the boring kanban basics, fully scriptable.
- **Per-repo "documents"** — typed text blobs (architecture / designs / planning / etc.) that link to issues and features. Useful as a stable place to park specs the LLM should always have at hand.
- **`mk doc add --from-path` / `mk doc export --to-path`** — round-trip a doc to/from its on-disk source location, no shell-glue regex needed.
- **`mk issue brief <KEY>`** — bulk-context JSON: issue + parent feature + all linked doc bodies + comments + relations + PRs, in one read. Designed to be piped straight into an LLM prompt.
- **TUI (`mk tui`)** — bubbletea-based full-screen kanban for human review.
- **Audit log** — every mutation is recorded with an actor name (`--user <name>`); 60-day retention.
- **REST API (`mk api`)** — same operations over HTTP for non-shell callers (web UIs, IDE plugins, long-running agents).
- **CLI client mode (`--remote` / `MK_REMOTE`)** — point the CLI at an `mk api` server instead of the local DB; every retargetable verb works against either backend.

## Install

Pure-Go SQLite (modernc), no CGO required:

```bash
go install github.com/mrgeoffrich/mini-kanban/cmd/mk@latest
```

Or build from a checkout:

```bash
go build -o ~/.local/bin/mk ./cmd/mk
```

## Quick start

```bash
cd ~/Repos/your-project
mk init                     # binds the cwd to a 4-letter prefix derived from the repo name
mk issue add "Login broken on Safari" --description-file /tmp/repro.md --tag bug
mk issue list
mk tui                      # interactive board
mk api                      # local REST API on 127.0.0.1:5320 (same DB)
```

To file work on behalf of an AI agent, always pass `--user <agent-name>` so audits attribute correctly:

```bash
mk issue add "Refactor auth middleware" --user Claude
```

## Output format

Every read command supports `-o json` (alias `--output json`):

```bash
mk issue list --state in_progress -o json | jq '.[] | .key'
```

JSON is the contract. Text mode is for humans and may shift between releases.

## REST API

`mk api` exposes the same operations over HTTP, backed by the same SQLite
file. Defaults to `127.0.0.1:5320`. Set `MK_API_TOKEN` (or pass `--token`)
to require `Authorization: Bearer <token>`.

    mk api &
    curl http://127.0.0.1:5320/healthz

The API is intended for local-only callers (web UIs, IDE plugins, agents
that aren't a child process). Exposes `/healthz`, `/schema*`, `/repos*`,
the full issue / comment / relation / pull-request / tag surface under
`/repos/{prefix}/issues*`, the feature surface under
`/repos/{prefix}/features*` including bulk-context (`/issues/{key}/brief`),
plan view (`/features/{slug}/plan`), and atomic claim
(`POST /features/{slug}/next`), the document surface under
`/repos/{prefix}/documents*` (CRUD + rename + link/unlink), and an audit
log under `/history` (cross-repo) and `/repos/{prefix}/history` (scoped).

Documents support a non-JSON streaming download endpoint —
`GET /repos/{prefix}/documents/{filename}/download` returns the raw
markdown body so `curl -O` saves a file directly. This replaces the
CLI's `mk doc export --to-path`; the API never reads or writes the
server filesystem.

Every mutation accepts `?dry_run=true` (or `X-Dry-Run: 1`); set
`X-Actor: <name>` so audit rows attribute correctly (and to claim
work — `POST /features/{slug}/next` requires it). See
`docs/rest-api-design.md` for the full route table.

### CLI client mode

The same `mk` binary can drive a remote `mk api` server instead of the
local DB. Set `--remote <url>` (or `MK_REMOTE`) and `--token <secret>`
(or `MK_API_TOKEN`):

    MK_REMOTE=http://team-mk:5320 MK_API_TOKEN=$T mk issue list
    mk --remote http://team-mk:5320 issue add "Login broken" --feature auth

Every read and mutating verb works in remote mode with the same flags,
positionals, and JSON output as local mode. Audit rows are written by
the API server, not the client. A small set of filesystem-touching
verbs stay local-only and error clearly when `MK_REMOTE` is set:
`mk init`, `mk install-skill`, `mk doc add --from-path`, `mk doc upsert
--from-path`, `mk doc export`, and `mk tui`. Use the new
`mk doc download <filename>` (writes to stdout, or `--to <path>`) when
you need a doc body locally in remote mode. See
`docs/cli-client-mode.md` for the full design.

## AI-agent integration

`mk` ships a Claude Code skill that documents the full surface for agents. To install it into another repo, run from anywhere inside that repo:

```bash
mk install-skill
```

This writes `.claude/skills/mk/SKILL.md` so Claude Code's project-skill auto-discovery picks it up. Re-run after upgrading `mk` to refresh the docs.

The skill at `.claude/skills/mk/SKILL.md` documents both the CLI and HTTP API surfaces; agents discover endpoints at runtime via `GET /schema` (mirrors `mk schema all`) and use `?dry_run=true` + `X-Actor: <name>` to rehearse and attribute work.

For one-shot LLM context on a single issue:

```bash
mk issue brief MINI-42 | tee /tmp/ctx.json
```

Returns a single JSON object with the issue, parent feature, deduped linked documents (with full content inlined), comments, relations, PRs, and a warnings array.

## Project status

Solo-maintained, used in anger by its author. Contributions welcome — see `CLAUDE.md` for development conventions, and `docs/tui-cookbook.md` for the bubbletea/lipgloss patterns the TUI relies on.

## License

MIT — see [LICENSE](LICENSE).
