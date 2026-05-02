# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Quick commands

- `go build ./...` — build everything.
- `go build -o ~/.local/bin/mk ./cmd/mk` — install the CLI/TUI binary on the developer machine (this is how the user runs `mk` and `mk tui`).
- `go vet ./...` — vet the codebase.
- `go test ./...` — currently a no-op; there are no `*_test.go` files yet.
- `mk <subcommand>` from inside any git working tree drives the CLI; `mk tui` launches the full-screen kanban.
- The SQLite database lives at `~/.mini-kanban/db.sqlite` by default. Override via `--db <path>`.

## Architecture in one screen

- Entry point: `cmd/mk/main.go` → `internal/cli.NewRoot()` (cobra).
- CLI commands: `internal/cli/*.go`, one file per command group (`issue`, `feature`, `repo`, `doc`, `link`, `pr`, `tag`, `comment`, `history`, `status`, `init`, `install_skill`, `tui`). Cross-cutting helpers live in `audit.go`, `context.go`, `output.go`, `output_flag.go`, `input.go`, `doc.go`.
- Persistence: `internal/store/` over SQLite (`modernc.org/sqlite`, pure-Go, no CGO). Schema is in `internal/store/schema.sql` and re-applied on every `Open` — adding a new table is a matter of appending another `CREATE TABLE IF NOT EXISTS …`. Schema changes that need real ALTERs go through `migrate()` in `internal/store/store.go`.
- Domain types: `internal/model/` — pure structs/enums, no DB.
- Git detection: `internal/git/detect.go` shells out to `git` for repo root + remote URL.
- TUI: `internal/tui/` — bubbletea v1.3.10 + lipgloss v1.1.0. Shell in `tui.go` owns the tab strip and routes keys; each tab implements the local `view` interface.

## Conventions that aren't obvious

- **`recordOp` after every mutation.** Every CLI command that writes to the database calls `recordOp(s, model.HistoryEntry{...})` from `internal/cli/audit.go`. History prune failures and audit-write failures log to stderr but never fail the user-visible command. New mutating commands MUST follow this pattern.
- **Repos auto-register.** `resolveRepo()` in `internal/cli/context.go` creates a `repos` row on first use from inside a git working tree. There's intentionally no separate `mk repo init`.
- **Actor (`--user`).** Every history row is stamped with an actor name. The default is the OS user; AI agents are expected to pass `--user <name>` explicitly so audits attribute work correctly. Plumbing lives in `audit.go`.
- **TUI view contract.** Each tab in `internal/tui/` is a struct implementing pointer-receiver `Update(msg tea.Msg) tea.Cmd`, `View(width, height int) string`, `Help() string`, and `HasOverlay() bool`. When `HasOverlay()` returns true the shell stops intercepting `q` / `esc` / digit / `tab`, so the overlay's own `esc` closes it instead of quitting the program.
- **Per-repo TUI state.** Generic key-value table `tui_settings(repo_id, key, value)` lives in `schema.sql`; helpers in `internal/store/tui_settings.go`. Used today for hidden columns; reuse for any future TUI preference instead of adding a typed table.
- **History retention.** `HistoryRetention` in `internal/store/store.go` is 60 days. `pruneHistory` runs on every `Open`. Adjust there if needed.

## `mk install-skill` and `embed.go`

- `embed.go` lives at the module root because `//go:embed` cannot traverse upward; it embeds `.claude/skills/mk/SKILL.md` into `embed.SkillMarkdown`.
- `mk install-skill` writes that markdown to `<git-root>/.claude/skills/mk/SKILL.md`, overwriting on every run so doc updates land in any repo that re-runs the command.
- `SKILL.md` is the canonical CLI reference for AI agents — keep it in sync when adding or changing commands.

## TUI cookbook

`docs/tui-cookbook.md` is a synthesised reference for bubbletea v1.3.10 + lipgloss v1.1.0 + bubbles. Read it before doing anything non-trivial in `internal/tui/`. The snippets are pinned to v1; upstream READMEs have already moved to v2 and will mislead you.
