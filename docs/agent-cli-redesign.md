# Designing `mk` for AI agents

Working notes distilled from Justin Poehnelt's [Rewrite Your CLI for AI Agents](https://justin.poehnelt.com/posts/rewrite-your-cli-for-ai-agents/). The article is written from the perspective of a Google Workspace CLI built for LLM-driven callers, but the principles map cleanly onto any CLI an agent has to drive — including `mk`.

This document captures the **takeaways only**. Each principle gets a separate follow-up pass where we judge how (or whether) it applies to `mk`.

## Key takeaways

### 1. Raw JSON payloads over custom flags
Accept full structured JSON as input rather than flattening every field into a bespoke `--flag`. Translating an API into flags is lossy, and LLMs are already fluent in JSON — let them speak it directly. Bespoke flags multiply over time and become their own maintenance burden.

### 2. Runtime schema introspection
Ship a command (e.g. `gws schema <method>`) that returns a machine-readable description of every command: parameters, types, required fields, scopes, examples. The CLI becomes the single source of truth for its own contract, so agents don't need bloated system prompts to know how to call it.

### 3. Context window discipline
Agents "pay per token and lose reasoning capacity with every irrelevant field." Provide field masks / projections to limit response size, support NDJSON-style streaming for paginated results, and avoid dumping verbose payloads by default. Output should be small and focused.

### 4. Defensive input validation
Treat agents as adversarial-by-accident. Reject hallucinated nonsense at the boundary: path traversal (`../../`), embedded query strings, control characters, double-encoded data, malformed identifiers. The CLI is the last line of defence — fail fast with a clear error rather than passing garbage downstream.

### 5. Dry-run mode for mutations
Every destructive or state-changing command should support `--dry-run` (or equivalent) so an agent can validate the request shape and resolved targets before anything is actually written. This is the single cheapest safety net against confidently-wrong agent calls.

### 6. Agent-specific documentation
Don't rely on `--help`. Ship a structured skill / instruction file aimed at agents that encodes invariants humans take for granted ("always pass `--user`", "use a field mask before listing", "confirm before deleting"). Agents lack intuition; spell the rules out.

### 7. Multi-surface architecture
The same underlying capability should be reachable via multiple agent-facing surfaces — CLI for shell-driving agents, MCP (JSON-RPC over stdio) for tool-calling clients, environment-based auth so headless callers don't have to interact. One implementation, several consumer paths.

### 8. Response sanitization
Data returned by the CLI may contain prompt-injection payloads (malicious emails, document bodies, ticket comments authored by third parties). Filter or fence untrusted text before handing it back to the agent so it can't be mistaken for instructions.

### 9. Incremental implementation
You don't need a rewrite. Start with `--output json` and input validation, then layer on schema introspection, field masks, dry-run, and MCP one at a time. Each step is independently useful.

## Next steps

Walk through each principle in order and decide:
- Does `mk` already do this? (Several of these are partially covered — e.g. `--output json` exists, `--user` is plumbed through.)
- If not, is it worth doing here? (`mk` is a local SQLite-backed tracker, not a cloud API — some principles apply differently.)
- What's the smallest concrete change?

---

## Principle #1 — JSON input on every mutating command

**Decision:** every mutating command gains a `--json` flag that accepts a JSON payload describing the entire operation. Existing positionals and typed flags stay (so humans keep their ergonomic surface), but `--json` is mutually exclusive with them — it's either the JSON path or the flag path, never a mix.

### Conventions

- **Flag name:** `--json` (alias `-j`). Value is either inline JSON or `-` for stdin. `--json @path/to.json` reads a file. (Cobra has no built-in `@` magic; we implement the same logic as `readLongText`.)
- **Globals stay as flags.** `--db`, `--user`, `--output` are transport/auth/format concerns, not part of the operation payload. Agents pass `--user` alongside `--json` exactly as they do alongside positionals today. We do *not* mirror them inside the JSON — keeping layers separated.
- **Strict decoding.** `json.Decoder.DisallowUnknownFields()` so a typo'd field is a hard error, not a silent no-op. Article principle #4 lands here cheap.
- **Output shape unchanged.** `--json` doesn't change what the command emits — the resulting object goes to stdout, formatted by `--output text|json`. (Realistically, agents driving `--json` will set `--output json`; we should consider auto-flipping the default when `--json` is present.)
- **Issue references.** JSON accepts the canonical `"MINI-42"` key only — no bare-number-relative-to-current-repo. Agents shouldn't be guessing prefixes; humans get that shortcut on the CLI flag path.
- **Long-text fields are inline strings.** No JSON-side equivalent of `--description-file`. If the agent wants the body in a file, it `cat`s it into the JSON it constructs. This collapses three flags (`--description`, `--description-file`, plus stdin sentinel) into one field.
- **Edit semantics — absent vs null vs empty:**
  - **Field absent** → don't change.
  - **Field present, non-empty** → set to that value.
  - **Field present, empty string `""`** → clear (where the model allows; otherwise reject).
  - **Field present, `null`** → clear (synonym for empty string; primarily for nullable references like `feature_slug`).
  - This requires distinguishing "absent" from "null/empty", so the decoder peeks at `map[string]json.RawMessage` for presence, then decodes into typed structs. Helper: `internal/cli/jsoninput.go`.

### Per-command JSON shapes

Read these as "what `--json` accepts". Required fields are marked `(req)`; everything else is optional.

#### Issues

```jsonc
// mk issue add --json -
{
  "title":          "Pin tab strip",  // (req)
  "feature_slug":   "tui-polish",      // optional
  "description":    "...markdown...",  // optional
  "state":          "todo",            // optional, default backlog
  "tags":           ["ui", "tui"]      // optional
}

// mk issue edit MINI-42 --json -   (positional KEY still required for ergonomics)
// or move the key into the payload:
// mk issue edit --json -
{
  "key":            "MINI-42",   // (req if no positional)
  "title":          "...",
  "description":    "...",       // "" to clear
  "feature_slug":   "auth"       // null to detach
}

// mk issue state --json -
{ "key": "MINI-42", "state": "in_progress" }

// mk issue assign --json -
{ "key": "MINI-42", "assignee": "agent-alice" }

// mk issue unassign --json -
{ "key": "MINI-42" }

// mk issue next --json -    (claim)
{ "feature_slug": "auth" }

// mk issue rm --json -
{ "key": "MINI-42" }
```

#### Features

```jsonc
// mk feature add
{ "title": "Auth rewrite", "slug": "auth", "description": "..." }

// mk feature edit
{ "slug": "auth", "title": "...", "description": "" }

// mk feature rm
{ "slug": "auth" }
```

#### Comments

```jsonc
// mk comment add
{ "issue_key": "MINI-42", "author": "agent-alice", "body": "..." }
```

#### Relations

```jsonc
// mk link
{ "from": "MINI-42", "type": "blocks", "to": "MINI-7" }

// mk unlink
{ "a": "MINI-42", "b": "MINI-7" }
```

#### Pull requests

```jsonc
// mk pr attach
{ "issue_key": "MINI-42", "url": "https://github.com/.../pull/123" }

// mk pr detach
{ "issue_key": "MINI-42", "url": "https://github.com/.../pull/123" }
```

#### Tags

```jsonc
// mk tag add
{ "issue_key": "MINI-42", "tags": ["ui", "tui"] }

// mk tag rm
{ "issue_key": "MINI-42", "tags": ["wip"] }
```

#### Documents

```jsonc
// mk doc add  (and `mk doc upsert` — same shape)
{
  "filename":    "auth-design.md",   // (req unless source_path is set)
  "type":        "architecture",     // (req unless inferable from source_path)
  "content":     "...markdown...",   // (req)
  "source_path": "docs/auth.md"      // optional
}

// mk doc edit
{ "filename": "auth-design.md", "type": "architecture", "content": "..." }

// mk doc rename
{ "old_filename": "auth.md", "new_filename": "auth-design.md", "type": "architecture" }

// mk doc rm
{ "filename": "auth-design.md" }

// mk doc link
{ "filename": "auth-design.md", "issue_key": "MINI-42", "description": "design ref" }
// or:
{ "filename": "auth-design.md", "feature_slug": "auth",  "description": "design ref" }

// mk doc unlink — same shape as link, no description
```

### Resolution rules

- `feature_slug`, `issue_key`, `filename` are resolved against the current repo (same `resolveRepo` path as today).
- Filename validation (`docFilenameRe`, no `/` `\` `\x00`), URL validation (`validatePRURL`), tag normalisation (`store.NormalizeTags`), state parsing (`model.ParseState`) are all reused — the JSON path enters the same validators after decoding. The "JSON path" is just a different *parser*, not a different *model*.

### Implementation skeleton

```go
// internal/cli/jsoninput.go
package cli

type jsonField[T any] struct {
    Set   bool
    Value T
}

func (f *jsonField[T]) UnmarshalJSON(b []byte) error {
    f.Set = true
    return json.Unmarshal(b, &f.Value)
}

// readJSONInput resolves --json to a []byte: inline JSON, "-" for stdin,
// or "@path" for a file. Returns nil when --json is unset.
func readJSONInput(value string) ([]byte, error) { ... }

// decodeStrict unmarshals with DisallowUnknownFields and
// returns (*T, presenceMap, error).
func decodeStrict[T any](raw []byte) (*T, map[string]struct{}, error) { ... }
```

Each command's `RunE` then becomes:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    if rawJSON != "" {
        return runIssueAddJSON(rawJSON)   // JSON path
    }
    return runIssueAddFlags(args, ...)    // existing path, untouched
}
```

So the existing flag handlers don't change at all — we add a parallel JSON path that funnels into the same `s.CreateIssue(...)` / `recordOp(...)` calls.

### What's intentionally *not* in this principle

- **Bulk / NDJSON apply** — that's a different shape (`mk apply -` with an op-discriminator field per line). Tracked separately under context-window discipline (#3) and the "what about batches" question.
- **Schema introspection** (`mk schema issue.add`) — covered by principle #2. Without it, agents have to read this doc; with it, they can `mk schema` at runtime.
- **Dry-run** — covered by principle #5.

### Rollout

One commit per command group keeps the diff reviewable: `issue`, then `feature`, `comment`, `link`/`unlink`, `pr`, `tag`, `doc`. Each commit adds the `--json` flag, the JSON path, and updates `SKILL.md` with the payload shape so `mk install-skill` distributes it.

---

## Principle #2 — Runtime schema introspection

**Decision:** add a `mk schema` command tree that emits JSON Schema for every mutating command's `--json` payload, derived from the same Go structs that decode the input. Agents discover shapes at runtime instead of memorising them from `SKILL.md`.

### Why pair with #1

Both passes touch the same code:
- The input structs from #1 *are* the source of truth for #2's schemas. Reflection over those structs gives us schema-decoder alignment for free — they can't drift.
- `SKILL.md` only needs to say "run `mk schema <command>`" once, instead of listing every payload shape inline. Cuts the doc churn in half.
- Whatever helper validates `--json` (strict decode, presence map, error wrapping) is shared with the schema generator's metadata.

### Surface

```
$ mk schema list
issue.add        Create an issue.
issue.edit       Update title / description / feature.
issue.state      Set issue state.
... (all mutating commands)

$ mk schema issue.add
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "mk://schema/issue.add",
  "title": "IssueAddInput",
  "description": "Create an issue in the current repo.",
  "type": "object",
  "properties": {
    "title":        { "type": "string", "description": "Issue title." },
    "feature_slug": { "type": "string", "description": "Feature to attach to." },
    "description":  { "type": "string", "description": "Markdown body." },
    "state":        { "type": "string", "enum": ["backlog","todo","in_progress","blocked","done","cancelled","duplicate"] },
    "tags":         { "type": "array", "items": { "type": "string" } }
  },
  "required": ["title"],
  "additionalProperties": false,
  "examples": [
    { "title": "Pin tab strip", "feature_slug": "tui-polish", "tags": ["ui"] }
  ]
}

$ mk schema --all
{ "issue.add": { ... }, "issue.edit": { ... }, ... }
```

### Design choices

- **Naming.** `mk schema issue.add` (dotted) refers to the cobra command `mk issue add`. The dot form is a single shell token, easy to template into agent prompts, and aligns with `recordOp` names that already use this convention (`issue.create`, `feature.update`).
- **JSON Schema 2020-12.** Off-the-shelf format LLMs are heavily trained on. No mk-specific wrapper around it.
- **Generation strategy.** Runtime reflection via [`github.com/invopop/jsonschema`](https://github.com/invopop/jsonschema) — reads `json:` and `jsonschema:` struct tags on the input types. Adds one go.mod dep but no codegen step. Per-field tags stay next to the field, which is where they want to live.
- **Examples are hand-curated.** Reflection won't invent useful examples. We attach one realistic `examples[0]` per command via either struct tags (`jsonschema:"example=..."` for primitives) or a `func (IssueAddInput) JSONSchemaExtend(s *jsonschema.Schema)` hook for complex shapes.
- **Strict additionalProperties.** Schemas declare `additionalProperties: false`, mirroring `DisallowUnknownFields()` at decode time. The schema and the decoder reject the same payloads.
- **`mk schema --all` for bulk fetch.** Some agents prefer one-shot ingestion. Pretty-print with `--output json` (default for this command anyway, since the output *is* JSON).
- **Reads excluded for now.** Only mutating commands have schemas. Read filters (`mk issue list --state ...`) stay flag-driven; their output types (`model.Issue` etc.) are already returned as JSON when `--output json` is set, and a future pass can publish their schemas if useful.
- **Empty `--json` discoverability.** When `--json` is supplied but parsing fails, the error message points at `mk schema <command>` so an agent that screws up gets a useful next step.

### Implementation skeleton

```go
// internal/cli/inputs/issue.go    (the structs from principle #1)
type IssueAddInput struct {
    Title        string   `json:"title"        jsonschema:"required,description=Issue title"`
    FeatureSlug  string   `json:"feature_slug,omitempty" jsonschema:"description=Feature to attach to"`
    Description  string   `json:"description,omitempty"  jsonschema:"description=Markdown body"`
    State        string   `json:"state,omitempty"        jsonschema:"enum=backlog,enum=todo,enum=in_progress,enum=blocked,enum=done,enum=cancelled,enum=duplicate"`
    Tags         []string `json:"tags,omitempty"`
}

// internal/cli/schema.go
type schemaEntry struct {
    Name        string                 // "issue.add"
    Short       string                 // one-liner
    InputType   reflect.Type           // for jsonschema.Reflect
    Example     any                    // hand-written
}

var schemaRegistry = []schemaEntry{
    {"issue.add",   "Create an issue.",            reflect.TypeOf(inputs.IssueAddInput{}),   inputs.ExampleIssueAdd},
    {"issue.edit",  "Update issue fields.",        reflect.TypeOf(inputs.IssueEditInput{}),  inputs.ExampleIssueEdit},
    // ...
}

func newSchemaCmd() *cobra.Command { /* list / by name / --all */ }
```

The same `inputs.IssueAddInput` struct is used by:
1. `runIssueAddJSON` — `json.Unmarshal` into it.
2. `mk schema issue.add` — `jsonschema.Reflect(reflect.New(t))`.

So the parsing path and the published schema can never disagree.

### Tradeoffs / pushback

- **One library dep** (`invopop/jsonschema`). Pure Go, no CGO, ~2 kloc. Acceptable for a tool that already pulls in cobra + bubbletea.
- **Reflection at runtime** vs. `go generate`d JSON files. Codegen would be more "rigorous" but adds a step that breaks if forgotten. Reflection is good enough for ~20 schemas and the cost is paid only when `mk schema` runs.
- **Hand-written examples.** Yes, this is duplication; no, autogenerated examples aren't worth shipping ("title": "string", "tags": ["string"]). The point of an example is realism. One example per schema, ~5 lines each, kept in `inputs/examples.go`.

---

## Combined plan: principles #1 + #2

Two phases, ordered. The whole thing fits in roughly the commit count of #1 alone — phase B reuses everything from phase A.

### Phase A — JSON input (principle #1)

1. **`internal/cli/inputs/`** — new package. One file per command group:
   - `issue.go`, `feature.go`, `comment.go`, `link.go`, `pr.go`, `tag.go`, `doc.go`
   - Each file defines the `*Input` structs with `json:` and `jsonschema:` tags.
2. **`internal/cli/jsoninput.go`** — shared helpers:
   - `readJSONInput(value string) ([]byte, error)` — handles inline / `-` / `@path`.
   - `decodeStrict[T any](raw []byte) (*T, presenceMap, error)` — strict decode + presence map for absent-vs-null edits.
   - Error wrapping that points at `mk schema <command>` on failure (forward reference to phase B; lands as a flat string in phase A and gets the real command name once schema lands).
3. **Per command group, in this order** (one commit each):
   - `issue` (the largest — adds `--json` to add, edit, state, assign, unassign, next, rm)
   - `feature` (add, edit, rm)
   - `comment` (add)
   - `link` / `unlink`
   - `pr` (attach, detach)
   - `tag` (add, rm)
   - `doc` (add, upsert, edit, rename, rm, link, unlink)

   Each commit:
   - Adds an `inputs/<group>.go` file (or extends it).
   - Adds `--json` flag to each command in the group.
   - Wires `if rawJSON != "" { return runXxxJSON(...) }` at the top of `RunE`.
   - Reuses the same store calls and `recordOp` for both paths.
   - Updates `SKILL.md` with a *placeholder* note ("payload shapes available via `mk schema <cmd>` once #2 lands").
4. **Vet / build / sanity check.** `go vet ./...`, `go build ./...`, manually run the new path against a scratch DB on at least one command per group.

### Phase B — Schema introspection (principle #2)

5. **Add `invopop/jsonschema` to `go.mod`.**
6. **`internal/cli/inputs/examples.go`** — one `ExampleXxx` value per input type. Hand-curated, ~5 lines each.
7. **`internal/cli/schema.go`** — schema registry, `mk schema list`, `mk schema <name>`, `mk schema --all`. Wired into `root.go`.
8. **Update phase A's error wrapping** to actually emit `mk schema <command>` paths now that the registry exists.
9. **`SKILL.md` rewrite for the agent path** — short section: "to drive mk from JSON, run `mk schema list` to discover commands, then `mk schema <name>` for the shape, then `mk <command> --json -`". Replaces any inline shape lists.
10. **`mk install-skill` redistributes the updated `SKILL.md`** automatically (no code change needed; the `embed.go` rebuild does it).

### Out of scope (deliberately)

- Output schemas (the shape of what `mk issue show` returns). Easy to add later by extending the registry; not on the critical path for agent usability since agents observe outputs directly.
- Bulk `mk apply` / NDJSON. Different concern (#3 territory).
- Dry-run validation of payloads (#5).
- Codegenning the schemas at build time. Reflection is fine until proven otherwise.

---

## Principle #3 — Context-window discipline

**Decision:** trim the verbose JSON outputs that bloat agent context, by switching list operations to lean-by-default and adding opt-in flags for callers that genuinely need the heavy fields. No new dep, no new output mode.

### Where mk currently bloats

A quick survey of JSON outputs against a fresh DB:

| Command | Heavy field | Default behaviour |
| --- | --- | --- |
| `mk issue list` | `description` | inlined in full on every row |
| `mk feature list` | `description` | inlined in full on every row |
| `mk doc list` | (none — already lean: emits `size_bytes`, no `content`) | OK |
| `mk doc show` | `content` | inlined in full (correct for `show`, but no metadata-only mode) |
| `mk issue brief` | linked-doc `content` × N | every linked doc body inlined |
| `mk history` | (none — `details` is already a short string) | OK |

`description` and `content` are the heavy hitters. A backlog of 50 issues with multi-paragraph descriptions easily produces a 50KB+ JSON blob from `mk issue list` — most of which the agent didn't ask for.

### What we're changing

**A. List operations become lean by default.**
- `mk issue list` JSON: drop `description`. Add `--with-description` to opt back in.
- `mk feature list` JSON: drop `description`. Add `--with-description` to opt back in.
- Text output is already terse for both — no change there. The bloat was JSON-only.
- Backwards-compat note: anyone parsing `description` out of list output today must add `--with-description`. mk is local-only and single-user; the migration is a one-line script change.

**B. Selective `mk doc show`.**
- Add `--metadata` to `mk doc show <name>`: emit type, size, links, timestamps — drop `content`. Default behaviour (full content) is unchanged for backwards compatibility.

**C. Selective `mk issue brief`.**
- Add `--no-doc-content` flag (joining the existing `--no-feature-docs` and `--no-comments`). Linked docs still appear with filename, type, source_path, linked_via, description — just not `content`. An agent inspects what's relevant, then `mk doc show` only the body it actually needs.

That's the whole change. No NDJSON, no `--field`/`--projection` mask, no streaming.

### Pushback / what we're *not* doing

- **NDJSON output** — the article recommends it. For mk it would be useful only on the rare command that returns 1000+ rows; today's largest realistic response is `mk issue brief` which is one object. Defer until evidence shows we need it.
- **Generic `--field a,b,c` projection** — clean in principle, but principle #1 already lets agents fetch via `--json` what they need; over-engineering a projection layer for the few list commands that bloat is poor return on engineering. Three opt-in flags cover the actual hot spots.
- **Removing `description` from the model entirely** — descriptions are still wanted on `show` and `brief`. The fix is "don't return them in *list* contexts", not "drop them from the type".

### Implementation outline

- `internal/store/issues.go` — `ListIssues` clears `Description` on each returned issue unless `IncludeDescription` is set on `IssueFilter`. Same approach for `ListFeatures` (add `IncludeDescription` parameter or option).
- `internal/cli/issue.go` and `feature.go` — add `--with-description` flag; thread through to the filter.
- `internal/cli/doc.go` — `docShowCmd` already builds a `docView` with the document inline; add `--metadata` that nils out `Content` before emitting.
- `internal/cli/issue.go` (`issueBriefCmd`) — add `--no-doc-content` boolean that empties each `briefDoc.Content` before emitting.
- `SKILL.md` — one paragraph: "lists are lean by default; pass `--with-description` if you actually want bodies inlined."

No schema-side changes (`mk schema` is for `--json` *inputs*, not output shapes).

---

## Principle #4 — Defensive input validation

**Decision:** centralise validation in `internal/store/validate.go` and call it from every mutation entry point in the store. Catches hallucinated nonsense before it lands in the DB or the audit log.

### Threat model

mk is local-only and single-user, so we're not defending against malicious actors. The realistic threats are:

1. **Display / audit-log corruption** — agents pasting control characters, NULs, ANSI escape sequences in titles, names, comments. Breaks `mk history` rendering, `mk issue show` output, and any future log-aggregation tooling.
2. **Filesystem escape** — `..`, absolute paths, or NUL bytes in document filenames or `--from-path`. Already mostly handled (`validateDocFilename`, `validateRelativePath`); tighten the gaps.
3. **Wrong-shape identifiers** — slugs with whitespace, tags with newlines, `--user` actor names with embedded `\n`. Today these flow straight into history details and break log lines.
4. **Resource exhaustion** — agents pasting 50MB markdown into a description. Local DB so not catastrophic; capping is cheap and avoids surprise.
5. **Invalid UTF-8** — already enforced for doc content; missing for everything else.

### What we already have

| Validator | Checks |
| --- | --- |
| `store.ValidatePrefix` | exactly 4 alnum chars |
| `store.ParseIssueKey` | `^[A-Za-z0-9]{4}-\d+$` |
| `model.ParseState` | closed enum |
| `model.ParseDocumentType` | closed enum |
| `store.NormalizeTag(s)` | strips whitespace, rejects empty, rejects internal whitespace |
| `cli.validateDocFilename` | rejects `/`, `\`, `\x00` |
| `cli.validateRelativePath` | rejects absolute, rejects `..` traversal |
| `cli.validatePRURL` | http/https scheme, host present |
| `decodeStrict` (principle #1) | unknown JSON fields → error |

### What we add

A new package-private validator suite in `internal/store/validate.go`:

| Helper | Purpose |
| --- | --- |
| `validateText(s, opts)` | core: required, max length, allow newlines, reject control chars, require valid UTF-8 |
| `validateTitle(s, field)` | single-line, max 200, required |
| `validateBody(s, field)` | multi-line, max 1 MiB, optional |
| `validateName(s, field)` | single-line, max 80, required (assignee, comment author) |
| `validateActor(s)` | single-line, max 80, required (`--user`) |
| `validateSlug(s)` | `^[a-z0-9][a-z0-9-]*$`, max 60 |
| `validateDocFilenameStrict` | tighten current rules: also reject control chars, `..`, leading/trailing whitespace, > 200 chars |
| `validatePRURLStrict` | tighten current rules: also reject control chars, > 2 KiB |

The "reject control chars" rule is:
- Single-line fields (title, name, slug, actor, filename, URL): reject all `\x00–\x1F` and `\x7F`.
- Multi-line fields (body, description, content): reject `\x00–\x08`, `\x0B`, `\x0C`, `\x0E–\x1F`, `\x7F`. Allow `\t` (`\x09`), `\n` (`\x0A`), `\r` (`\x0D`).

### Where validation runs

**Inside store mutations.** `CreateIssue`, `UpdateIssue`, `CreateFeature`, `UpdateFeature`, `SetIssueAssignee`, `CreateComment`, `CreateDocument`, `UpdateDocument`, `RenameDocument`, `AttachPR`. The CLI keeps its early validators (filename, prefix, PR URL) for fast-fail and good UX, but the store is the last line of defence — any future caller (TUI, tests, programmatic API) gets the same protection.

**At `--user` resolution.** `actor()` in `internal/cli/audit.go` runs `validateActor` once and errors at command start if `--user` was malformed.

### What we deliberately don't do

- **Unicode bidi-override / zero-width filtering.** Defensible for a public service displaying user-supplied text to other users; overkill for a single-user local tracker.
- **HTML/markdown sanitisation.** mk doesn't render HTML; markdown is rendered by glamour in the TUI which is its own escape boundary.
- **Output sanitisation.** Principle #8 territory; separate pass.
- **Idempotency keys / replay protection.** No remote API, no concurrent writers worth worrying about.
