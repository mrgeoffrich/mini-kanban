#!/usr/bin/env bash
# scripts/render-tap-formula.sh — Emit the Homebrew formula for the
# given mk version. Reads sha256sum-format lines from stdin (the same
# layout as dist/checksums.txt that scripts/release.sh produces) and
# writes the rendered formula to stdout.
#
# Usage:
#   scripts/render-tap-formula.sh <version-without-leading-v> < checksums.txt > Formula/mk.rb
#
# Example: scripts/render-tap-formula.sh 0.2.1 < dist/checksums.txt

set -euo pipefail

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
    echo "usage: $0 <version>   e.g. $0 0.2.1" >&2
    exit 1
fi
# Refuse a leading "v" so generated `version` field is right.
if [[ "$VERSION" == v* ]]; then
    echo "version must not include a leading 'v' (got $VERSION)" >&2
    exit 1
fi

CHECKSUMS="$(cat)"

sha_for() {
    local suffix="$1"
    local match
    match="$(printf '%s\n' "$CHECKSUMS" | grep -E "[[:space:]]mk-v${VERSION}-${suffix}(\\.tar\\.gz|\\.zip)\$" || true)"
    if [[ -z "$match" ]]; then
        echo "missing checksum line for ${suffix} (looked for mk-v${VERSION}-${suffix}.tar.gz/.zip)" >&2
        exit 1
    fi
    awk '{print $1}' <<< "$match"
}

DARWIN_ARM="$(sha_for darwin-arm64)"
DARWIN_AMD="$(sha_for darwin-amd64)"
LINUX_ARM="$(sha_for linux-arm64)"
LINUX_AMD="$(sha_for linux-amd64)"

URL_BASE="https://github.com/mrgeoffrich/mini-kanban/releases/download/v${VERSION}"

cat <<EOF
class Mk < Formula
  desc "Local-first issue tracker for AI agents, with CLI and TUI"
  homepage "https://github.com/mrgeoffrich/mini-kanban"
  version "${VERSION}"
  license "MIT"

  on_macos do
    on_arm do
      url "${URL_BASE}/mk-v${VERSION}-darwin-arm64.tar.gz"
      sha256 "${DARWIN_ARM}"
    end
    on_intel do
      url "${URL_BASE}/mk-v${VERSION}-darwin-amd64.tar.gz"
      sha256 "${DARWIN_AMD}"
    end
  end

  on_linux do
    on_arm do
      url "${URL_BASE}/mk-v${VERSION}-linux-arm64.tar.gz"
      sha256 "${LINUX_ARM}"
    end
    on_intel do
      url "${URL_BASE}/mk-v${VERSION}-linux-amd64.tar.gz"
      sha256 "${LINUX_AMD}"
    end
  end

  def install
    bin.install "mk"
  end

  test do
    assert_match "mini-kanban", shell_output("#{bin}/mk --help")
  end
end
EOF
