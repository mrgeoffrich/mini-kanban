# mini-kanban (`mk`)

A local-first issue tracker designed to be **driven by an AI agent**.

You talk to Claude Code; Claude files issues, updates state, breaks features into tasks, and answers questions about your board. As a human, you mostly *read* — in your editor, on the CLI (`mk issue list`), or in the full-screen TUI (`mk tui`).

It's a single binary on top of one SQLite file at `~/.mini-kanban/db.sqlite`. No server, no account, no setup beyond `mk init` and `mk install-skill`.

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
mk init                # bind this repo to a 4-letter prefix
mk install-skill       # teach Claude Code how to drive mk
```

Now open Claude Code and say:

> File an issue: the login page 500s on Safari when the password contains a `&`.

Claude does the rest.

For the full walk-through — first session, sample skills, multi-machine sync — see **[docs/getting-started.md](docs/getting-started.md)**.

## Why mk

- **AI-first.** Every read returns JSON, every mutation accepts a JSON payload, every payload schema is published at runtime via `mk schema`. The bundled Claude Code skill (`mk install-skill`) is the single source of truth for agents.
- **Local-first.** One SQLite file, one git working tree per project. No sync until you want it.
- **Auditable.** Every mutation records who did it, when, and what changed. Pass `--user Claude` so audits attribute correctly.
- **Optional sync.** When you want the same board on a second machine or another teammate, `mk sync init` mirrors the DB to a checked-in YAML repo over plain git.
- **Optional REST API.** `mk api` exposes the same operations over HTTP for non-shell callers (web UIs, IDE plugins, long-running agents). Same SQLite file, same JSON shapes, same audit log. See `docs/rest-api-design.md`.

## Project status

Solo-maintained, used in anger by its author. Contributions welcome — see `CLAUDE.md` for development conventions, and `docs/tui-cookbook.md` for the bubbletea/lipgloss patterns the TUI relies on.

## License

MIT — see [LICENSE](LICENSE).
