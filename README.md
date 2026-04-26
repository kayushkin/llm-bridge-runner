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
curl -fsSL https://raw.githubusercontent.com/kayushkin/llm-bridge-runner/main/scripts/install.sh | bash
```

The installer prompts for two things:

1. **bridge endpoint** — either a public URL (`https://bridge.example.com`) or an SSH target (`user@host[:remote-port]`). For SSH targets, the installer also installs a persistent `ssh -NL` tunnel as a systemd user service so the runner can reach `localhost:8160` without a public DNS name.
2. **enrollment passphrase** — single-use, minted on the server with `llm-bridge-server mint-enroll`. Trades for a durable per-machine token stored at `~/.config/llm-bridge-runner/config.json`.

The installer downloads a prebuilt binary (linux/{amd64,arm64}, darwin/{amd64,arm64}) from the bridge server's `/api/runner/binary` endpoint, enrolls the machine, and starts a systemd user unit (`llm-bridge-runner.service`).

## Mint a passphrase (server side)

On the host running llm-bridge-server:

```bash
llm-bridge-server mint-enroll --ttl 30m
```

That prints an 8-character passphrase. Enter it when the installer asks. The passphrase is single-use; once redeemed, future reconnects use the per-machine token instead.

## Subcommands

```
llm-bridge-runner enroll --server URL --passphrase XXXX [--name LABEL]
    One-time exchange: trade a passphrase for a per-machine token.
    Writes ~/.config/llm-bridge-runner/config.json.

llm-bridge-runner [run]
    Long-lived service. Reads the saved config and dials the server.
    Default subcommand.

llm-bridge-runner -version
    Print version.
```

## Service management

```bash
journalctl --user -u llm-bridge-runner -f      # logs
systemctl --user restart llm-bridge-runner     # restart
sudo loginctl enable-linger $USER              # survive logout
```

When the bridge endpoint is an SSH target, a second unit `llm-bridge-tunnel.service` keeps the SSH tunnel alive; the runner has `Requires=llm-bridge-tunnel.service`.

## Part of the llm-bridge ecosystem

See [llm-bridge](https://github.com/kayushkin/llm-bridge) for the canonical message types and the full architecture.

## License

Apache 2.0. See [LICENSE](./LICENSE).
