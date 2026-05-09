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
that aren't a child process). See `docs/rest-api-design.md` for the full
route table; Phase 1 covers `/healthz`, `/schema*`, and `/repos*`.

## AI-agent integration

`mk` ships a Claude Code skill that documents the full surface for agents. To install it into another repo, run from anywhere inside that repo:

```bash
mk install-skill
```

This writes `.claude/skills/mk/SKILL.md` so Claude Code's project-skill auto-discovery picks it up. Re-run after upgrading `mk` to refresh the docs.

For one-shot LLM context on a single issue:

```bash
mk issue brief MINI-42 | tee /tmp/ctx.json
```

Returns a single JSON object with the issue, parent feature, deduped linked documents (with full content inlined), comments, relations, PRs, and a warnings array.

## Project status

Solo-maintained, used in anger by its author. Contributions welcome — see `CLAUDE.md` for development conventions, and `docs/tui-cookbook.md` for the bubbletea/lipgloss patterns the TUI relies on.

## License

MIT — see [LICENSE](LICENSE).
