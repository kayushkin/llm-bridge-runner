#!/usr/bin/env bash
set -euo pipefail

# Cross-builds the runner for every (linux|darwin)/(amd64|arm64) target and
# drops the binaries into LLMBRIDGE_RUNNER_ASSETS_DIR (default
# /usr/local/lib/llm-bridge-runner-binaries). llm-bridge-server serves these
# from /api/runner/binary, so newly-deployed binaries become available to
# every enrolled remote runner on its next reconnect / fetchHarnessBinary.
#
# This is purely a build+publish pass — there is no service to restart on
# this host (the runner runs on remote machines).

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$REPO_DIR"

ASSETS_DIR="${LLMBRIDGE_RUNNER_ASSETS_DIR:-/usr/local/lib/llm-bridge-runner-binaries}"
NAME="llm-bridge-runner"
VERSION="$(git rev-parse --short HEAD 2>/dev/null || echo dev)"

export PATH="$HOME/.local/share/mise/shims:$PATH"

if [ ! -d "$ASSETS_DIR" ]; then
  echo "==> Creating $ASSETS_DIR (sudo)..."
  sudo mkdir -p "$ASSETS_DIR"
fi

TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
)

TMPDIR_BUILD="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_BUILD"' EXIT

# Refuses a binary that carries no vcs.revision, and warns when it was built from a
# dirty tree. Every target is checked the moment it is built, so nothing reaches the
# publish loop below unidentified: these binaries are served to remote machines from
# /api/runner/binary, and once a runner has fetched one, the only record of what
# source it runs is the stamp inside it.
#
# go build writes no VCS information when it cannot find a .git DIRECTORY, and it does
# not fail when that happens -- not even with -buildvcs=true, and not even with no
# repository at all (measured on go1.26.0). A git worktree's .git is a pointer file,
# so a deploy run from one publishes binaries stamped (devel) while the log reads
# clean. A provenance stamp is the one property whose absence looks exactly like
# success, so assert it rather than trusting the build.
check_provenance() {
  local binary="$1" target="$2" buildinfo vcs_revision vcs_modified
  buildinfo="$(go version -m "$binary")"
  vcs_revision="$(printf '%s\n' "$buildinfo" | awk -F= '$1 ~ /[[:space:]]vcs\.revision$/ {print $2}')"
  vcs_modified="$(printf '%s\n' "$buildinfo" | awk -F= '$1 ~ /[[:space:]]vcs\.modified$/ {print $2}')"
  if [ -z "$vcs_revision" ]; then
    echo "    REFUSING TO PUBLISH: the $target binary carries no vcs.revision, so nothing can" >&2
    echo "    tie it back to a commit. 'go build' writes no VCS stamp when it cannot find a" >&2
    echo "    .git DIRECTORY, and it does not fail when that happens -- not even with" >&2
    echo "    -buildvcs=true. The usual cause is building from a git worktree, whose .git is" >&2
    echo "    a pointer file. Build from a real clone or checkout instead." >&2
    exit 1
  fi
  echo "    vcs.revision=$vcs_revision"
  if [ "$vcs_modified" = "true" ]; then
    echo "    WARNING: the $target binary was built from a DIRTY tree (vcs.modified=true)." >&2
    echo "    $vcs_revision names the commit it was built NEAR, not the source it was built" >&2
    echo "    FROM, and that source is not recoverable from any commit. Commit first for a" >&2
    echo "    reproducible build." >&2
  fi
}

for t in "${TARGETS[@]}"; do
  read -r OS ARCH <<< "$t"
  OUT="$TMPDIR_BUILD/$NAME-$OS-$ARCH"
  echo "==> Building $OS/$ARCH..."
  GOOS="$OS" GOARCH="$ARCH" go build \
    -ldflags "-X main.Version=$VERSION" \
    -o "$OUT" .
  echo "    $(ls -lh "$OUT" | awk '{print $5}')  $OUT"
  # Cross-compiled binaries carry the same stamp as a native build, and 'go version -m'
  # reads a foreign GOOS/GOARCH file without executing it -- both measured here on
  # go1.26.0 against darwin/arm64 from this linux host. So each target is checked for
  # real, not assumed to inherit the host build's provenance.
  check_provenance "$OUT" "$OS/$ARCH"
done

echo "==> Publishing to $ASSETS_DIR..."
for t in "${TARGETS[@]}"; do
  read -r OS ARCH <<< "$t"
  SRC="$TMPDIR_BUILD/$NAME-$OS-$ARCH"
  DST="$ASSETS_DIR/$NAME-$OS-$ARCH"
  sudo install -m 0755 "$SRC" "$DST"
  echo "    -> $DST"
done

echo "==> Done. version=$VERSION"
echo "    runners auto-fetch via /api/runner/binary on next reconnect."
