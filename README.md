# llm-bridge-runner

Long-lived daemon that registers a remote machine with an [llm-bridge-server](https://github.com/kayushkin/llm-bridge-server) and accepts harness-spawn requests over a single outbound WebSocket. Lets the server drive harness subprocesses on machines behind NAT without inbound SSH or a VPN.

## How it works

```
  Your llm-bridge-server                Remote machine
  ┌────────────────────────┐             ┌────────────────────────────┐
  │                        │  outbound   │                            │
  │  spawn harness X here  │◀────WS─────▶│  llm-bridge-runner daemon  │
  │                        │             │            │               │
  └────────────────────────┘             │            ▼               │
                                         │   harness subprocess(es)   │
                                         │   (claudecode, codex, …)   │
                                         └────────────────────────────┘
```

The runner connects out to the server and stays connected. The server sends spawn requests over that connection; the runner forks the harness binary locally and pipes its stdin/stdout/stderr back. Closes the loop without exposing any inbound port on the remote.

## Install

```bash
go install github.com/kayushkin/llm-bridge-runner@latest
```

Or via the bundled install script (also installs a systemd user unit):

```bash
curl -fsSL https://raw.githubusercontent.com/kayushkin/llm-bridge-runner/main/scripts/install.sh | bash
```

## Run

```bash
llm-bridge-runner \
  -server https://bridge.example.com \
  -token  $LLMBRIDGE_TOKEN \
  -name   $(hostname)
```

All flags accept env-var fallbacks:

| Flag | Env var | Description |
|------|---------|-------------|
| `-server` | `LLMBRIDGE_SERVER` | llm-bridge-server base URL (`http`/`https`/`ws`/`wss`) |
| `-token` | `LLMBRIDGE_TOKEN` | bearer token for runner auth |
| `-name` | `LLMBRIDGE_MACHINE_NAME` | human label for this machine |
| `-workdir` | `LLMBRIDGE_WORKING_DIR` | default cwd for spawned harnesses (defaults to `$HOME`) |

## Part of the llm-bridge ecosystem

See [llm-bridge](https://github.com/kayushkin/llm-bridge) for the full picture.

## License

Apache 2.0. See [LICENSE](./LICENSE).
