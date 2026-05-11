# mini-kanban (`mk`)

A local-first kanban for developers. **claude does the work, you orchestrate it.**

You talk to Claude Code; claude does the typing — files issues, updates state, breaks features into tasks, answers questions about your board. You mostly *read* — in your editor, on the CLI (`mk issue list`), or in `mk tui`.

Your board is a SQLite file at `~/.mini-kanban/db.sqlite`. `mk init` and `mk install-skill` are the whole setup.

<p align="center">
  <img src="docs/screenshots/01.png" alt="The mk TUI board" width="80%" />
</p>

## Install

**Homebrew** (macOS and Linux, prebuilt binaries):

```bash
brew tap mrgeoffrich/mk
brew install mk
```

**`go install`** (pure-Go SQLite via modernc, no CGO required):

```bash
go install github.com/mrgeoffrich/mini-kanban/cmd/mk@latest
```

**From a checkout:**

```bash
go build -o ~/.local/bin/mk ./cmd/mk
```

Either way, `mk --version` prints the version (release tag for prebuilt or `go install`-ed binaries; commit hash for `go build`).

## Quick start

```bash
cd ~/Repos/your-project
mk init                # bind this repo to a 4-letter prefix
mk install-skill       # teach Claude Code how to drive mk
```

Now open Claude Code and say:

> File an issue: the login page 500s on Safari when the password contains a `&`.

claude does the rest — picks a title, picks tags, files the ticket, hands you back the key.

For the full walk-through — first session, sample skills, multi-machine sync — see **[docs/getting-started.md](docs/getting-started.md)**.

## Read the board

```bash
cd ~/Repos/your-project
mk tui
```

A full-screen kanban with four tabs — Board (above), Features, Docs, History — all keyboard. `?` shows the bindings for the focused tab, `q` (or `ctrl+c`) exits. Open any card for the full description and comments, or jump to the other tabs:

<p align="center">
  <img src="docs/screenshots/02.png" alt="Card overlay" width="49%" />
  <img src="docs/screenshots/03.png" alt="Features tab" width="49%" />
  <img src="docs/screenshots/04.png" alt="Documents tab" width="49%" />
  <img src="docs/screenshots/05.png" alt="History tab" width="49%" />
</p>

## Sync the board to git

`mk sync` mirrors the SQLite DB to a folder of YAML + markdown in a separate git repo — handy for browsing the board in your editor, diffing changes over time, or sharing a board across machines.

1. **Create an empty git repo for the sync data.** On GitHub:

   ```bash
   gh repo create your-project-mk-sync --private
   ```

   Any empty git remote works (GitLab, Gitea, a bare repo on a server you control); the contents are plain text.

2. **From inside your project, seed it:**

   ```bash
   mk sync init ~/sync/your-project --remote git@github.com:you/your-project-mk-sync.git
   ```

   This creates `~/sync/your-project` with one file per issue, feature, and document, commits, and pushes. It also writes `.mk/config.yaml` inside your project (check it in) so future `mk sync` calls — and other machines via `mk sync clone` — know which remote to use.

3. **Keep it in sync as you work:**

   ```bash
   mk sync                # pull → import → export → commit → push
   ```

   Run it whenever — pushes your writes, pulls anyone else's. Multi-machine setup, conflict semantics, and the inspect/verify tools live in [docs/getting-started.md](docs/getting-started.md#5-sync-across-machines-when-youre-ready).

## Why mk

- **Built for claude.** Reads return JSON. Mutations take JSON. Every payload schema is reachable at runtime via `mk schema`. The bundled Claude Code skill (`mk install-skill`) is the single source of truth for agents.
- **Local-first.** Your board is a SQLite file. Nothing leaves the laptop until you run `mk sync`.
- **Auditable.** Every mutation records who, when, and what changed. (claude knows to pass `--user claude` so the log attributes correctly.)
- **Optional sync.** Want the same board on a laptop and a desktop? `mk sync init`, plain git underneath.
- **Optional REST API.** `mk api` puts the CLI behind HTTP — handy for web UIs, IDE plugins, long-running agents. See `docs/rest-api-design.md`.

## Project status

Solo-maintained, used in anger by its author. Contributions welcome — see `CLAUDE.md` for development conventions, and `docs/tui-cookbook.md` for the bubbletea/lipgloss patterns the TUI relies on.

## License

MIT — see [LICENSE](LICENSE).
