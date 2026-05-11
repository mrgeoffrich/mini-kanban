# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Required reading before planning or implementing

Two docs in `docs/` carry the non-obvious conventions this codebase relies on. Read whichever is relevant **before** you start planning a change — they'll shape the design, not just check it after the fact.

- **[`docs/agent-cli-principles.md`](docs/agent-cli-principles.md)** — the six rules every mutating CLI command honours (JSON in via `--json`, schema reachable via `mk schema`, lean output by default, validation at the store boundary, `--dry-run` support, documented in `SKILL.md`) plus the explicit "deliberately don't do" list. Read before adding or changing any CLI command. The longer working notes (threat models, alternatives) live in `docs/agent-cli-redesign.md`.
- **[`docs/tui-cookbook.md`](docs/tui-cookbook.md)** — bubbletea v1.3.10 + lipgloss v1.1.0 + bubbles patterns, pinned to v1. Upstream READMEs have moved to v2 and will mislead you. Read before any non-trivial work in `internal/tui/`.

The deeper context for both lives in the topic sections below (`## Agent-CLI principles` and `## TUI cookbook`).

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

## Agent-CLI principles (read before planning a feature)

`docs/agent-cli-principles.md` is the durable reference for the conventions mk adopted from Justin Poehnelt's "Rewrite Your CLI for AI Agents". Every mutating command accepts JSON via `--json`, publishes its schema via `mk schema`, returns lean output by default, validates input at the store boundary, supports `--dry-run`, and is documented in `SKILL.md`. New CLI work should honour those six rules and the explicit "deliberately don't do" list (no NDJSON, no `--field` projection, no silent input normalisation, etc.). The longer working notes that produced the rules — threat models, alternatives considered — are in `docs/agent-cli-redesign.md`.

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

## Text panels

Fullscreen card / feature / document overlays share a single shape — bordered, padded, with a vertical scrollbar — implemented in `internal/tui/panel.go`. Use `markdownPanel(width, height, body, *scroll)` for a stand-alone bordered overlay; use `scrollableBlock(width, height, body, *scroll)` when the surrounding layout already owns the border (e.g. the top half of the card overlay's split). Both helpers strict-clip to the requested size, so a too-long body can't stretch the frame and a too-short body is padded — that's how we avoid the "panel grows with content" bug. `*scroll` is clamped in place.

## Runes vs cells (Unicode width)

A *rune* is one Unicode code point; a *cell* is one column of terminal grid. ASCII is 1:1, but emoji and CJK are 1 rune / 2 cells, combining marks are N runes / 1 cell, and ZWJ sequences (e.g. 👨‍👩‍👧) collapse many runes into one glyph. `internal/tui/helpers.go`'s `truncate` measures *runes* — fine for ASCII filenames and keys, but if a user-supplied string (issue title, comment body, document name) contains emoji, the truncated line will be shorter cell-wise than expected. lipgloss's own `Width()` is cell-aware (via go-runewidth) and pads correctly, so the symptom is usually a small visual gap, not overflow. Swap `truncate` to `runewidth.Truncate` if a panel ever shows visible emoji-driven misalignment.

## TUI snapshots (visual debugging)

`mk tui --snapshot <target>` renders one TUI view to stdout non-interactively, so you can inspect layout bugs without a real terminal. Targets: `board`, `features`, `docs`, `history`, `card-overlay`, `doc-overlay`, `feature-overlay`, `picker`. Use `--width`/`--height` to fix the viewport and `--issue MINI-1` to focus a specific card before opening `card-overlay` (or to position the cursor on `board`). Implementation is in `internal/tui/snapshot.go`; reproducing a visual bug is usually one command, e.g. `mk tui --snapshot card-overlay --issue MINI-1 --width 140 --height 50 > /tmp/x.ansi`.
