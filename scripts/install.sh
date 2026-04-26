#!/usr/bin/env bash
#
# llm-bridge-runner install script.
#
# Installs the runner binary, writes a systemd user unit, and starts the
# service. Connection details (server URL, token, machine name) come
# from flags, env vars, or interactive prompt — in that order.
#
# Usage:
#   curl -fsSL https://your-bridge-server/install.sh | bash
#   curl -fsSL <url>/install.sh | bash -s -- --server URL --token TOKEN --name LABEL
#   LLMBRIDGE_SERVER=… LLMBRIDGE_TOKEN=… LLMBRIDGE_MACHINE_NAME=… curl … | bash
#
# The script never echoes the token to the terminal or to logs.

set -euo pipefail

# Pre-baked default — when the script is served by an llm-bridge-server
# instance, that server templates this line with its own URL. Direct
# downloads from the source repo leave it empty so the prompt asks.
LLMBRIDGE_SERVER_DEFAULT="${LLMBRIDGE_SERVER:-}"

# Where the prebuilt runner binary lives. When the script is served by
# an llm-bridge-server, this is the absolute URL of /api/runner/binary
# for the matching OS+arch. Empty disables the download path; the
# script then falls back to `go install`.
RUNNER_BINARY_URL_DEFAULT="${LLMBRIDGE_RUNNER_BINARY_URL:-}"

# ──────────────────────────────────────────────────────────────────────
# Argument parsing
# ──────────────────────────────────────────────────────────────────────

server="${LLMBRIDGE_SERVER:-$LLMBRIDGE_SERVER_DEFAULT}"
token="${LLMBRIDGE_TOKEN:-}"
machine_name="${LLMBRIDGE_MACHINE_NAME:-}"
working_dir="${LLMBRIDGE_WORKING_DIR:-}"
binary_url="${LLMBRIDGE_RUNNER_BINARY_URL:-$RUNNER_BINARY_URL_DEFAULT}"
non_interactive=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --server)        server="$2"; shift 2 ;;
    --token)         token="$2"; shift 2 ;;
    --name)          machine_name="$2"; shift 2 ;;
    --workdir)       working_dir="$2"; shift 2 ;;
    --binary-url)    binary_url="$2"; shift 2 ;;
    --yes|-y)        non_interactive=1; shift ;;
    -h|--help)
      sed -n '2,15p' "$0"; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

# ──────────────────────────────────────────────────────────────────────
# Environment detection
# ──────────────────────────────────────────────────────────────────────

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

is_wsl=0
if grep -qEi '(microsoft|wsl)' /proc/version 2>/dev/null; then
  is_wsl=1
fi

run_user="$(id -un)"
home_dir="${HOME:-$(getent passwd "$run_user" | cut -d: -f6)}"

# ──────────────────────────────────────────────────────────────────────
# Interactive prompts (read from /dev/tty so it works under curl|bash)
# ──────────────────────────────────────────────────────────────────────

prompt_value() {
  # $1 = label, $2 = current value, $3 = "secret" if input should be hidden
  local label="$1" current="$2" secret="${3:-}" value
  if [[ -n "$current" ]]; then
    echo "$current"
    return
  fi
  if [[ ! -t 0 ]] && [[ ! -e /dev/tty ]]; then
    echo "missing required value: $label (no tty available for prompt)" >&2
    exit 2
  fi
  if [[ "$secret" == "secret" ]]; then
    printf "%s: " "$label" >/dev/tty
    IFS= read -rs value </dev/tty
    printf "\n" >/dev/tty
  else
    printf "%s: " "$label" >/dev/tty
    IFS= read -r value </dev/tty
  fi
  echo "$value"
}

if [[ "$non_interactive" -eq 1 ]]; then
  for var in server token machine_name; do
    if [[ -z "${!var}" ]]; then
      echo "missing required value in non-interactive mode: --$var" >&2
      exit 2
    fi
  done
else
  server="$(prompt_value "llm-bridge-server URL" "$server")"
  token="$(prompt_value "runner token" "$token" secret)"
  default_name="$(hostname -s 2>/dev/null || hostname)"
  if [[ "$is_wsl" -eq 1 ]]; then
    default_name="wsl-${default_name}"
  fi
  machine_name="${machine_name:-$default_name}"
  machine_name="$(prompt_value "machine label [$machine_name]" "" )"
  machine_name="${machine_name:-$default_name}"
fi

if [[ -z "$server" || -z "$token" || -z "$machine_name" ]]; then
  echo "server URL, token, and machine name are all required" >&2
  exit 2
fi

# ──────────────────────────────────────────────────────────────────────
# Pre-flight checks
# ──────────────────────────────────────────────────────────────────────

if ! command -v claude >/dev/null 2>&1; then
  echo "warning: 'claude' not found on PATH — Claude Code sessions will fail to spawn" >&2
  echo "         install with: npm install -g @anthropic-ai/claude-code" >&2
  echo "         (continuing — you can install it later)" >&2
fi

# ──────────────────────────────────────────────────────────────────────
# Install the binary
# ──────────────────────────────────────────────────────────────────────

bin_dir="$home_dir/.local/bin"
mkdir -p "$bin_dir"
runner_bin="$bin_dir/llm-bridge-runner"

if [[ -n "$binary_url" ]]; then
  echo "downloading llm-bridge-runner from $binary_url"
  tmp="$(mktemp)"
  curl -fsSL "$binary_url" -o "$tmp"
  chmod +x "$tmp"
  mv "$tmp" "$runner_bin"
elif command -v go >/dev/null 2>&1; then
  echo "no --binary-url provided; building from source via 'go install'"
  GOBIN="$bin_dir" go install github.com/kayushkin/llm-bridge-runner@latest
else
  echo "error: neither --binary-url nor 'go' is available; cannot install runner" >&2
  exit 1
fi

if [[ ! -x "$runner_bin" ]]; then
  echo "install failed: $runner_bin is missing or not executable" >&2
  exit 1
fi

echo "installed $runner_bin"
"$runner_bin" -version || true

# ──────────────────────────────────────────────────────────────────────
# Configure systemd user unit
# ──────────────────────────────────────────────────────────────────────

if [[ "$os" != "linux" ]]; then
  echo "non-linux OS: systemd setup skipped. Run manually: $runner_bin -server '$server' -token <TOKEN> -name '$machine_name'"
  exit 0
fi

if ! command -v systemctl >/dev/null 2>&1; then
  echo "systemctl not found; cannot install service. Run the runner manually." >&2
  exit 0
fi

unit_dir="$home_dir/.config/systemd/user"
mkdir -p "$unit_dir"
unit_file="$unit_dir/llm-bridge-runner.service"

env_file="$home_dir/.config/llm-bridge-runner/env"
mkdir -p "$(dirname "$env_file")"
umask_prev="$(umask)"
umask 077
cat >"$env_file" <<EOF
LLMBRIDGE_SERVER=$server
LLMBRIDGE_TOKEN=$token
LLMBRIDGE_MACHINE_NAME=$machine_name
LLMBRIDGE_WORKING_DIR=${working_dir}
EOF
umask "$umask_prev"

cat >"$unit_file" <<EOF
[Unit]
Description=llm-bridge-runner ($machine_name)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$env_file
ExecStart=$runner_bin
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now llm-bridge-runner.service

echo
echo "service started. Tail logs with:"
echo "  journalctl --user -u llm-bridge-runner -f"
echo
echo "To survive logout (keep runner alive after WSL terminal closes):"
echo "  sudo loginctl enable-linger $run_user"
