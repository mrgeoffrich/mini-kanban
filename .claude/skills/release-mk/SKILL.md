---
name: release-mk
description: Cut a new release of mini-kanban (mk). Cross-compiles binaries for Windows amd64, Linux amd64+arm64, and macOS amd64+arm64; packages them into per-platform archives with a sha256 checksums file; tags the commit; and publishes a GitHub release with the assets attached. Use this whenever the user says "release mk", "cut a release", "publish a new version of mk", "ship vX.Y.Z", "make a release", "tag and release", or asks to bump mk's version. Also use it for pre-release dry runs that build artefacts but skip the publish step ("dry run", "snapshot build", "test the matrix").
---

# Release mk

This skill cuts a release of mini-kanban end-to-end: build → package → tag → publish. It's organised so every irreversible step (tagging, pushing, creating the GitHub release) is gated on explicit confirmation, and so you can stop halfway with `dist/` artefacts intact for inspection.

The build matrix and packaging live in `scripts/release.sh` at the repo root. The skill orchestrates around it. Keeping the matrix in a script (rather than re-deriving it each release) means the platform set stays stable across versions — that matters because anyone scripting a curl-install relies on the filenames not drifting.

## Workflow

### 1. Pre-flight checks

Run these and stop on any failure. The point is to fail fast before doing anything you'd have to undo:

```bash
# Right repo?
[ -f cmd/mk/main.go ] || { echo "not in mini-kanban repo"; exit 1; }

# Clean tree?
[ -z "$(git status --porcelain)" ] || { echo "working tree dirty; commit/stash first"; exit 1; }

# On main? (Releasing off a feature branch is almost always wrong.)
branch="$(git rev-parse --abbrev-ref HEAD)"
[ "$branch" = "main" ] || echo "warning: releasing from $branch; confirm with the user before proceeding"

# Up to date with origin?
git fetch --quiet origin
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/$branch)" ] || \
    echo "warning: local $branch differs from origin/$branch"

# Tooling present?
gh auth status >/dev/null 2>&1 || { echo "gh not logged in; run 'gh auth login'"; exit 1; }
go version >/dev/null
command -v zip >/dev/null || { echo "zip not on PATH (needed for the windows archive)"; exit 1; }
```

If anything failed, surface the specific reason and stop. Don't try to auto-fix dirty trees or rebase — that's the user's call.

### 2. Determine the version

Look up the most recent tag and propose the next semver:

```bash
last="$(git describe --tags --abbrev=0 2>/dev/null || echo none)"
echo "last tag: $last"
```

Suggest a bump based on what's changed since `$last`:

- **Patch** (`v0.1.0` → `v0.1.1`) — bug fixes only, no new behaviour.
- **Minor** (`v0.1.0` → `v0.2.0`) — new features, backwards-compatible.
- **Major** (`v0.1.0` → `v1.0.0`) — breaking changes (CLI surface, JSON shape, on-disk format).

For pre-1.0 projects, treat minor bumps as the place to land breaking changes too — that's the convention before the first stable release.

Confirm the chosen version with the user before tagging. The version must match `vMAJOR.MINOR.PATCH` (with an optional `-prerelease` suffix like `-rc.1`); the build script enforces this.

### 3. Build artefacts

```bash
bash scripts/release.sh v0.1.0
```

This populates `dist/` with five archives plus `checksums.txt`. Output lives only in `dist/` — no other files are touched.

### 4. Smoke-test the host binary

Quick sanity check that the artefact for the current machine actually runs. Catches "I broke main without realising" before tagging:

```bash
host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
host="${host_os}-${host_arch}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [ "$host_os" = "windows" ]; then
    unzip -q "dist/mk-v"*"-${host}.zip" -d "$tmp"
else
    tar -xzf "dist/mk-v"*"-${host}.tar.gz" -C "$tmp"
fi

"$tmp"/mk-v*-${host}/mk --help >/dev/null && echo "host binary OK"
```

If this fails, do not tag — investigate the build first.

### 5. Compose release notes

Read the commits since the last tag and write notes. The model can do this better than a one-shot bash command — group by intent, not by file order.

```bash
last="$(git describe --tags --abbrev=0 2>/dev/null || echo)"
if [ -n "$last" ]; then
    git log "${last}..HEAD" --pretty=format:'%h %s'
else
    git log --pretty=format:'%h %s'
fi
```

Group the commits into sections (skip empty ones):

- **Features** — new capabilities, new commands, new flags
- **Fixes** — bug fixes, regression fixes
- **Performance** — measurable perf wins
- **Docs** — README, SKILL.md, examples
- **Chore** — refactors, dep bumps, gofmt cleanups, CI

Show the draft to the user and let them edit before publishing. Save the final notes to `/tmp/release-notes.md` for the next step.

If the user prefers GitHub's auto-generated changelog (commit list with PR refs), drop the `--notes-file` flag in step 6 and use `--generate-notes` instead.

### 6. Tag, push, publish — irreversible. Confirm first.

These three steps create durable, externally-visible state. Surface them to the user as a single confirmation gate ("about to tag v0.1.0, push to origin, and create a GitHub release with 5 archives — proceed?").

```bash
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0

gh release create v0.1.0 \
    --title "v0.1.0" \
    --notes-file /tmp/release-notes.md \
    dist/mk-v0.1.0-*.tar.gz \
    dist/mk-v0.1.0-*.zip \
    dist/checksums.txt
```

Order matters: the tag must exist on the remote before `gh release create`, otherwise GitHub creates a release pointing at a tag-that-doesn't-yet-exist (it works, but it's confusing if anything goes wrong mid-flight).

### 7. Show the result

```bash
gh release view v0.1.0
```

Print the release URL so the user can click through to verify. Don't auto-open a browser — surface the URL and let them.

## Dry runs

If the user says "dry run", "snapshot", "build only", "test the matrix", or similar — run **steps 1–4 only** and stop. Report `dist/`'s contents and the checksums. Don't tag, don't push, don't publish.

This is the right mode for:
- Verifying the build matrix after a dependency bump.
- Producing local binaries to share with one tester before a real release.
- Sanity-checking after editing `scripts/release.sh`.

## Customising the matrix

The `PLATFORMS` array in `scripts/release.sh` is the single source of truth for which binaries get built. Edit it for one-off cuts (e.g. dropping windows for an internal release), but treat changes as deliberate — release consumers expect a stable platform set across versions, and dropping one mid-stream breaks anyone who scripted a download URL.

If the user asks to add a platform Go supports but the matrix doesn't (e.g. `linux/arm`, `freebsd/amd64`), that's a one-line addition. Discuss with the user whether it's a permanent change or a one-off.

## Future: GitHub Actions

The current workflow builds on the developer's machine. That's fine for a small CLI with no CGO and no signing requirements. If you ever want reproducible CI builds (e.g. for SLSA provenance, signed binaries via cosign, or Linux builds without a local Go toolchain), the natural extension is a `.github/workflows/release.yml` triggered by tag pushes that runs the same `scripts/release.sh` and uses `gh release create` with `${{ github.ref_name }}`. Out of scope for this skill — this is the local-builds version.

## Troubleshooting

- **`gh auth status` fails** — run `gh auth login` and retry. The skill won't proceed without an authenticated `gh`.
- **A single platform build fails** — the script aborts at the first failure. Read the Go error; common causes are a CGO-using dependency sneaking in (mk avoids these) or a build tag missing. Fix and re-run.
- **`shasum: command not found`** — the script tries `sha256sum` first and falls back to `shasum`. If neither is present, install one (`brew install coreutils` on macOS, `apt install perl` or `apt install coreutils` on Debian-likes).
- **`gh release create` fails after the tag was pushed** — the tag is durable but the release isn't. Either retry `gh release create` (it'll error if a release already exists for the tag — use `gh release upload` instead to add assets to a partially-created one), or `gh release delete v0.1.0` and start over.
- **You tagged the wrong commit** — `git tag -d v0.1.0 && git push origin :refs/tags/v0.1.0` removes both local and remote tags. Then re-run from step 6 with the right HEAD checked out. Don't do this if the release was already announced/consumed.
