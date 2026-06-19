# citadel logs-daemon as a native systemd service — design

**Date:** 2026-06-19
**Status:** Approved, pending implementation plan

## Problem

The `citadel-logs` daemon today runs only as a Docker container via
`docker-compose.logs.yml`. To start it you must `cd` into the citadel repo and
run `docker compose -f docker-compose.logs.yml up -d`. There is no way to start
it from an arbitrary directory, and reboot survival depends on Docker's own
boot configuration rather than anything citadel controls.

The user wants to run it as a managed background service:

> "even if my computer shut off I do not need to go to that folder, I can just
> run `citadel logs-daemon start`"

## Goal

Add lifecycle subcommands to `citadel logs-daemon` that manage the
`citadel-logs` binary as a **systemd `--user` service**:

- Startable from any directory.
- Auto-starts on boot (via user lingering), before login.
- No Docker dependency.

The existing registry subcommands (`register`, `list`, `unregister`) are
unchanged.

## Decisions (from brainstorming)

- **Run model:** native binary + systemd `--user` service. No Docker.
- **Command surface:** full set — `start`, `stop`, `restart`, `status`, `logs`.
  `start` is idempotent and also performs the install.
- **AWS env:** captured at `start` time from the current shell, overridable via
  `--profile` / `--region` flags, baked into the unit's `Environment=` lines.
- **stop** leaves the unit enabled (reboot still brings it back); `--disable`
  flag fully turns it off.
- **Daemon binds to loopback only** (`127.0.0.1:5500`).

## The systemd unit

`start` renders and installs `~/.config/systemd/user/citadel-logs.service`:

```ini
[Unit]
Description=citadel-logs daemon
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/abs/path/to/citadel-logs \
  --registry %h/.citadel/registry.yml \
  --db %h/.local/share/citadel/citadel-logs.db \
  --addr 127.0.0.1:5500
Environment=AWS_PROFILE=<captured>
Environment=AWS_REGION=<captured>
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
```

`%h` is systemd's specifier for the user's home directory. If a captured AWS
value is empty, its `Environment=` line is omitted entirely.

### Path changes from the Docker setup

All of these simplify things when running natively:

| Concern   | Docker                          | Native service                              |
|-----------|---------------------------------|---------------------------------------------|
| DB        | `/data/citadel-logs.db` (volume)| `~/.local/share/citadel/citadel-logs.db` (XDG) |
| Registry  | `/etc/citadel/registry.yml` (mount) | `~/.citadel/registry.yml` (direct)      |
| Repos     | `/repos` read-only mount        | Host paths resolve directly — no remapping  |
| Listen    | `127.0.0.1:5500` (port binding) | `127.0.0.1:5500` (`--addr`)                 |

Because the registry already stores host repo paths, running natively removes
the `/repos` mount remapping and the hardcoded `clusterbox/backend` path from
the compose file — a class of path-mismatch bugs disappears.

## `start` flow (idempotent — install + enable + start)

1. Guard `runtime.GOOS == "linux"`. On other platforms, error with a pointer to
   `docker compose -f docker-compose.logs.yml up -d`.
2. Locate the `citadel-logs` binary: prefer the same directory as the running
   `citadel` executable (`os.Executable`), else fall back to `$PATH`. If not
   found, error with a hint to run `make install`.
3. Resolve AWS env: `--profile` / `--region` flags override; else inherit the
   current `AWS_PROFILE` / `AWS_REGION`; else omit.
4. `mkdir -p ~/.local/share/citadel` and `~/.config/systemd/user`.
5. Render the unit file to
   `~/.config/systemd/user/citadel-logs.service`.
6. `systemctl --user daemon-reload`, then
   `systemctl --user enable --now citadel-logs.service`.
7. `loginctl enable-linger $USER` — best-effort; warn but do not fail if denied.
   This lets the service start at boot before the user logs in.
8. Print the active state and `http://localhost:5500/logs`.

## Other commands

- **stop** — `systemctl --user stop citadel-logs.service`. The unit stays
  enabled so the next boot restarts it. `--disable` also runs
  `systemctl --user disable` to fully turn it off.
- **restart** — re-render the unit file (picks up any new `--profile`,
  `--region`, or `--addr`), `daemon-reload`, then
  `systemctl --user restart citadel-logs.service`.
- **status** — `systemctl --user is-active`, plus the dashboard URL and the
  count of registered services (read from the registry).
- **logs** — `journalctl --user -u citadel-logs -f`. Flags: `-n <N>` for line
  count, `--no-follow` to print and exit.

## Code structure

- New file `cmd/citadel/logsdaemon_service.go` holding the lifecycle
  subcommands, wired into the existing `newLogsDaemonCmd()` in
  `cmd/citadel/logsdaemon.go`.
- A pure `renderUnit(opts)` function building the unit-file string from a small
  options struct (binary path, registry path, db path, addr, profile, region).
- A small `systemctlRunner` interface wrapping `exec.Command` for the
  `systemctl` / `loginctl` / `journalctl` invocations.
- `make install` updated to install **both** `citadel` and `citadel-logs`.

## Testing

- `renderUnit` is pure → table-driven unit tests asserting the rendered unit
  contents, including the omit-empty-env behavior.
- Lifecycle commands run against a fake `systemctlRunner` that records the
  command sequence, so `start` / `stop` / `restart` are tested without touching
  real systemd. Assert the exact ordered command list (e.g. start →
  daemon-reload, enable --now, enable-linger).
- Binary-resolution helper tested via a temp dir + `$PATH` manipulation.

## Out of scope (YAGNI)

- macOS `launchd` support.
- System-wide (non-`--user`) units.
- Any change to how `register` / `list` / `unregister` work.
