# Getting started with `mk`

`mk` is an issue tracker for a single user (or a small team), built so Claude Code can drive it. The standard workflow is:

1. You ask Claude Code something — *"file an issue for the Safari bug"*, *"what's on my plate?"*, *"plan out the auth rewrite"*.
2. Claude reads the relevant state from `mk`, performs writes if needed, and tells you what it did.
3. You read the result in your editor, in `mk tui`, or on the CLI.

You **don't have to memorise mk's commands**. Claude has a copy of the canonical skill (installed once per repo) and knows the surface. You can drop into `mk --help` if you want — but the design assumes you mostly won't.

This guide walks you through:

- Setting up `mk` in a repo (no sync — quickest path to value).
- A first session driving `mk` through Claude Code.
- Optional: install sample skills for common flows.
- Optional: share the board between machines via git-backed sync.

---

## 1. One-time setup

Install the binary (pure-Go SQLite, no CGO):

```bash
go install github.com/mrgeoffrich/mini-kanban/cmd/mk@latest
```

Pick a project. From inside its working tree:

```bash
cd ~/Repos/your-project
mk init                # bind this repo to a 4-letter prefix (e.g. YOUR)
mk install-skill       # write .claude/skills/mk/SKILL.md
```

Restart Claude Code in this repo so the new skill loads. That's it — you're done.

The SQLite database lives at `~/.mini-kanban/db.sqlite`. Move it, back it up, or override with `--db <path>` per command. There's nothing else to configure.

> **Note.** `mk init` is optional — running any `mk` command inside a fresh git repo auto-creates the row and allocates a prefix. Calling `init` explicitly just lets you choose the prefix (`--prefix AUTH`) and confirms the binding.

---

## 2. Your first session

Open Claude Code in the project and try this conversation.

### File an issue

> **You**: We've got a bug — login 500s on Safari when the password contains a `&`. Can you log it?
>
> **Claude**: *(calls `mk issue add` with title, description, tag `bug`)*
> Filed as `YOUR-1`.

Claude doesn't ask you to confirm a flag dump — it composes a JSON payload, optionally rehearses it with `--dry-run`, then commits and tells you the issue key. The audit log records the action against `--user Claude` (or whatever name your agent identifies as) so you can later see who wrote what.

### Ask what's on your plate

> **You**: What's in progress?
>
> **Claude**: *(calls `mk issue list --state in_progress -o json`)*
> Two items: `YOUR-3` (auth rewrite) and `YOUR-7` (flaky deploy test). Want details on either?

Lists are read-only and return JSON. Claude summarises so you don't have to skim raw output.

### Look at the board yourself

When you want to *see* the board, not chat about it:

```bash
mk tui
```

This is the full-screen kanban — read-first. You can move cards by hand from here, but most users find the muscle memory that sticks is to keep using Claude for writes and use the TUI as a window into the state.

### Take a closer look at one issue

> **You**: Tell me about YOUR-3.
>
> **Claude**: *(calls `mk issue brief YOUR-3`)*
> Here's the gist: …

`mk issue brief` is the bulk-context call. Claude pulls the issue, parent feature, comments, relations, attached PRs, and any linked design documents in one read. Good for *"catch me up"* questions.

### Other things you can ask

The flexibility comes from Claude knowing the full surface, not from memorised command names:

- *"Move YOUR-3 to in review."*
- *"Tag YOUR-12 as P1 and attach it to the auth-rewrite feature."*
- *"What's blocked, and by what?"*
- *"Add a comment on YOUR-7 saying I tried clearing the cookie and it didn't help."*
- *"Show me everything Claude did yesterday."* (audit log)

If a request maps to something `mk` exposes, Claude will pick it up.

---

## 3. Sample skills (optional)

The canonical `mk` skill teaches Claude **how** to call the CLI. The sample skills bundled with `mk` teach Claude **when** and **why** for common flows — they're shortcuts you can drop into your repo when the bare canonical skill isn't picking up your phrasing.

Install all four into the current repo:

```bash
mk install-sample-skills
```

Or install only the ones you want:

```bash
mk install-sample-skills triage stand-up
```

Each lands at `<repo-root>/.claude/skills/<name>/SKILL.md` and is overwritten on every run, so re-running picks up updates from a newer build of `mk`. The file is checked into your repo alongside everything else, so the rest of your team gets the same shortcuts.

Restart Claude Code so the new skills load, then:

| Skill | Trigger phrase | What it does |
|---|---|---|
| `file-issue` | *"file an issue"*, *"log this"*, *"add a ticket"* | Takes a one-line description, writes a clean title, body, and tags; attaches to a feature if obvious. |
| `triage` | *"triage the backlog"*, *"groom the board"*, *"what should I look at"* | Sweeps backlog issues, proposes tags / priorities / feature groupings — asks before writing. |
| `stand-up` | *"stand-up"*, *"daily summary"*, *"what changed yesterday"* | Pure-read summary of `in_progress`, blocked items, and the last 24h of audit history. |
| `plan-feature` | *"plan the auth rewrite"*, *"break this down"* | Creates a feature, child issues, blocks/blocked-by edges, and (optionally) a linked design doc. |

These are templates — open them, tweak the trigger phrases or the procedural steps to match your workflow, and commit the changes. Re-running `mk install-sample-skills` will overwrite your edits with the bundled version, so once you've customised, leave the install command alone.

---

## 4. The CLI as a fallback

You don't need to drive `mk` directly, but you can. Useful for tab-completion-friendly commands, scripts, and getting a quick read on something without opening Claude:

```bash
mk status                      # repo + issue counts at a glance
mk issue list --state todo
mk issue show YOUR-3
mk feature plan auth-rewrite   # topo-sorted execution plan
mk history --since 1d          # last day's mutations
```

For the full surface, `mk --help` and `mk <subcommand> --help` cover everything. The exhaustive reference is `.claude/skills/mk/SKILL.md` (the same file Claude reads) — open it any time you want to know what's possible.

---

## 5. Sync across machines (when you're ready)

Single-machine `mk` is the fast path. If you want the same board on a laptop and a desktop, or to share a board with a teammate, set up git-backed sync.

The model: `mk sync` mirrors the SQLite DB to a checked-in folder of YAML + markdown in a separate git repo (the *sync repo*). You push and pull through normal git; conflicts are resolved last-writer-wins per record, with already-in-git winning label collisions.

### First-time setup

From inside your project repo:

```bash
mk sync init ~/sync/your-project --remote git@github.com:you/your-project-mk-sync.git
```

This creates the sync repo at `~/sync/your-project`, exports the project's data, commits, and pushes. It also writes `.mk/config.yaml` in your project (checked in) so other clones know which sync remote to use.

### Joining the sync repo from another machine

After cloning your project on machine 2:

```bash
cd ~/Repos/your-project
mk sync clone           # uses .mk/config.yaml to find the remote
```

This clones the sync repo and imports its contents into the local SQLite DB. If the local DB already has issues for this prefix, `mk sync clone` will refuse unless you pass `--allow-renumber`.

### Steady-state

```bash
mk sync                 # pull → import → export → commit → push
```

Run it whenever you want to push your local writes upstream and pull anyone else's. Most users find on-demand sufficient — wire it into a cron or git hook only if you want continuous mirroring.

For collisions, conflict semantics, redirect chains, and the verify-and-inspect commands, see the `Git-backed sync` section in `.claude/skills/mk/SKILL.md`.

---

## What to read next

- **`.claude/skills/mk/SKILL.md`** — exhaustive reference for AI agents. Also the right place to look if you want to know exactly what `mk` exposes.
- **`docs/agent-cli-principles.md`** — the design rules `mk` follows so agents can drive it reliably.
- **`mk --help`** — every CLI command with one-line summaries.

If something in this guide is wrong or unclear, file an issue — *"hey Claude, file an issue against mk: …"*.
