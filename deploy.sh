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

for t in "${TARGETS[@]}"; do
  read -r OS ARCH <<< "$t"
  OUT="$TMPDIR_BUILD/$NAME-$OS-$ARCH"
  echo "==> Building $OS/$ARCH..."
  GOOS="$OS" GOARCH="$ARCH" go build \
    -ldflags "-X main.Version=$VERSION" \
    -o "$OUT" .
  echo "    $(ls -lh "$OUT" | awk '{print $5}')  $OUT"
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
