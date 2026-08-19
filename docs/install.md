# Installing Atlas

How to get the `atlas` binary onto a machine and keep it running. This is the
operator's guide — for setting up a *development* checkout see
[`DEVELOPMENT.md`](../DEVELOPMENT.md), and for containers see
[`deploy/`](../deploy/).

| I want to… | Go to |
|------------|-------|
| Run the binary on a Linux server | [Linux, step by step](#linux-step-by-step) |
| Run it on Windows Server | [Windows Server](#windows-server) |
| Run it on macOS | [macOS](#macos) |
| Try it out for five minutes | [Quick try](#quick-try) |
| Run it in Docker or Kubernetes | [`deploy/README.md`](../deploy/README.md) |
| Look up a flag or environment variable | [Configuration reference](#configuration-reference) |
| Build it myself | [Building from source](#building-from-source) |

> 🚧 Atlas is a **developer preview (`0.x`)**. The on-disk format is not stable
> between releases yet — treat an upgrade as a migration, keep backups, and do
> not put irreplaceable data in it. See [`CHANGELOG.md`](../CHANGELOG.md).

## What you need

Almost nothing, and that is deliberate. Atlas is one self-contained binary
([ADR-0011](adr/0011-single-binary-distribution-and-web-ui.md)): the engine, the
HTTP API, and the whole web UI are compiled into it.

- **No database.** Atlas embeds its own write-ahead log and state store.
- **No message broker, no external services.**
- **No runtime dependencies.** The builds are `CGO_ENABLED=0`, statically linked.
- **One CPU core and a few hundred MB of RAM** are enough to start; the engine is
  a single-writer design, so it wants fast local disk more than many cores.

Optional extras, each off unless you ask for it:

| If you want | You also need |
|-------------|---------------|
| Script tasks in Python / JavaScript / PowerShell | `python3` / `node` / `pwsh` on `PATH` |
| The event log mirrored for search and reporting | An OpenSearch cluster ([ADR-0114](adr/0114-opensearch-event-exporter.md)) |
| DMN models served centrally instead of from disk | A temis endpoint (`ATLAS_DMN_RESOLVER_URL`) |

A missing script interpreter does not stop the server from booting — it logs a
warning and the tasks in that language park until you install it.

## Two rules to read before anything else

**One process per data directory.** Atlas is a single-writer engine: exactly one
`atlas serve` may own a data directory at a time. Two processes pointed at the
same directory will corrupt it. This is also why the Helm chart is a one-replica
StatefulSet and must never become a Deployment.

**Atlas speaks plain HTTP, never TLS.** The binary has no certificate handling at
all. Put a TLS-terminating reverse proxy in front of it before anyone outside the
host can reach it — see [Reverse proxy and TLS](#8-reverse-proxy-and-tls).

## Quick try

If you only want to see it run, and will throw the directory away afterwards:

```bash
curl -fsSLO https://github.com/pblumer/atlas/releases/latest/download/atlas_0.2.0_linux_amd64.tar.gz
tar -xzf atlas_0.2.0_linux_amd64.tar.gz
./atlas_0.2.0_linux_amd64/atlas serve
```

Then open <http://127.0.0.1:8080/>. Authentication is off, so there is no login —
which is exactly why this is a "try it" recipe and not an install. For anything
that outlives the afternoon, follow the steps below.

## Linux, step by step

### 1. Download the release and check it

Releases are at <https://github.com/pblumer/atlas/releases>. Each one carries a
`SHA256SUMS` asset covering every archive; verify against it rather than trusting
the transfer. Substitute the version and architecture you want — `linux_amd64`,
`linux_arm64`, or `linux_arm` (ARMv6, for a 32-bit Raspberry Pi OS).

```bash
VERSION=0.2.0
ARCH=linux_amd64
BASE=https://github.com/pblumer/atlas/releases/download/v${VERSION}

curl -fsSLO ${BASE}/atlas_${VERSION}_${ARCH}.tar.gz
curl -fsSLO ${BASE}/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
```

`sha256sum` must print `atlas_0.2.0_linux_amd64.tar.gz: OK`. If it prints
anything else, stop — do not unpack the archive.

### 2. Put the binary on the system

```bash
tar -xzf atlas_${VERSION}_${ARCH}.tar.gz
sudo install -m 0755 atlas_${VERSION}_${ARCH}/atlas /usr/local/bin/atlas
atlas version
```

The archive also contains `LICENSE`, `README.md`, and `CHANGELOG.md`. `atlas
version` prints the release plus the git commit it was built from, and that same
string is what the UI and `GET /api/v1/info` report — handy when you are asked
"which build is that box running?".

### 3. Create a service user and a data directory

Atlas needs no privileges beyond its own data directory, so do not run it as
root.

```bash
sudo useradd --system --home-dir /var/lib/atlas --shell /usr/sbin/nologin atlas
sudo mkdir -p /var/lib/atlas
sudo chown atlas:atlas /var/lib/atlas
sudo chmod 0750 /var/lib/atlas
```

Put this directory on local disk, not on NFS or a network share. Every commit
ends in an `fsync`, and the durability guarantee is only as good as that call.

### 4. Start it once by hand

Before wiring up a service, confirm it comes up:

```bash
sudo -u atlas atlas serve --addr 127.0.0.1:8080 --data-dir /var/lib/atlas
```

You should see it open the data directory and then:

```
listening on 127.0.0.1:8080 (UI at http://127.0.0.1:8080, MCP at http://127.0.0.1:8080/mcp)
```

Check it from another shell, then stop it with `Ctrl-C`:

```bash
curl -fsS http://127.0.0.1:8080/healthz
```

Binding to `127.0.0.1` on the first run is on purpose: authentication is still
off, and until step 6 anyone who can reach the port is an administrator.

### 5. Run it as a systemd service

Write `/etc/systemd/system/atlas.service`:

```ini
[Unit]
Description=Atlas BPMN workflow engine
Documentation=https://github.com/pblumer/atlas
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=atlas
Group=atlas
ExecStart=/usr/local/bin/atlas serve --addr 127.0.0.1:8080 --data-dir /var/lib/atlas --auth
Restart=on-failure
RestartSec=5s

# Atlas shuts down cleanly on SIGTERM; give it a little longer than its own
# --shutdown-timeout (10s by default) so in-flight requests finish first.
KillSignal=SIGTERM
TimeoutStopSec=30s

# Credentials, read from a root-owned file rather than the unit, so they stay out
# of `systemctl show` and the journal.
EnvironmentFile=-/etc/atlas/atlas.env

# It only ever needs its own data directory.
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/atlas

[Install]
WantedBy=multi-user.target
```

Then:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now atlas
sudo systemctl status atlas
journalctl -u atlas -f
```

> If you enable script tasks, `ProtectSystem=strict` and `PrivateTmp=true` also
> apply to the interpreters Atlas spawns. Loosen them only as far as your scripts
> actually need.

### 6. Turn on authentication

The unit above already passes `--auth`. On the **first** start with `--auth` and
an empty user store, Atlas seeds one administrator:

```bash
sudo mkdir -p /etc/atlas
sudo tee /etc/atlas/atlas.env >/dev/null <<'EOF'
ATLAS_ADMIN_USERNAME=admin
ATLAS_ADMIN_PASSWORD=change-me-to-something-long
EOF
sudo chmod 0600 /etc/atlas/atlas.env
sudo systemctl restart atlas
```

If you leave `ATLAS_ADMIN_PASSWORD` unset, Atlas generates a password and logs it
**once** at startup — read it out of `journalctl -u atlas` and change it. Either
way, remove the password from `atlas.env` after the first successful login; it is
only consulted while the user store is empty.

Locked out later? Recovery runs against the data directory, with or without the
server running:

```bash
sudo -u atlas atlas reset-password --data-dir /var/lib/atlas admin
# or, to create an admin that does not exist yet:
sudo -u atlas atlas reset-password --data-dir /var/lib/atlas --create-admin patrick
# or to supply the password instead of having one generated:
printf '%s' 'a-strong-password' | sudo -u atlas atlas reset-password \
  --data-dir /var/lib/atlas --password-stdin admin
```

### 7. Back up the vault key

With the vault enabled (the default), the first start generates a master key at
`<data-dir>/vault.key` and encrypts every stored connector secret with it. **A
data directory without its key is not recoverable** — the secrets in it cannot be
read back.

Two ways to handle it, pick one:

- **Back up `vault.key` separately** from the data directory, so a stolen backup
  of one is not enough to read the other.
- **Supply the key yourself** via `ATLAS_VAULT_KEY` (32 bytes as 64 hex chars or
  base64) or `ATLAS_VAULT_KEY_FILE`. An operator-provided key is never written to
  disk, which is the stronger posture — see
  [ADR-0070](adr/0070-vault-on-by-default-with-generated-key.md).

### 8. Reverse proxy and TLS

Atlas serves plain HTTP. Terminate TLS in front of it. An nginx sketch:

```nginx
server {
    listen 443 ssl;
    server_name atlas.example.com;

    ssl_certificate     /etc/letsencrypt/live/atlas.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/atlas.example.com/privkey.pem;

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;

        # Long-poll and streaming endpoints (collaboration, logs) outlive the
        # default 60s read timeout.
        proxy_read_timeout 300s;
        proxy_buffering    off;
    }
}
```

One endpoint needs a decision from you: **`/mcp` is not authenticated at the
transport level**, by design ([ADR-0016](adr/0016-mcp-server-over-http-api.md)). It is how an
AI agent drives the server. If you do not want that exposed, block it at the
proxy:

```nginx
location /mcp { deny all; }
```

## Windows Server

The release includes a `windows_amd64` build. Note that this is the **binary** on
Windows — Windows *containers* are not supported.

### 1. Download and verify

In an elevated PowerShell:

```powershell
$Version = '0.2.0'
$Base = "https://github.com/pblumer/atlas/releases/download/v$Version"
Invoke-WebRequest "$Base/atlas_${Version}_windows_amd64.zip" -OutFile atlas.zip
Invoke-WebRequest "$Base/SHA256SUMS" -OutFile SHA256SUMS

$expected = (Select-String -Path SHA256SUMS -Pattern "atlas_${Version}_windows_amd64.zip").Line.Split(' ')[0]
$actual   = (Get-FileHash atlas.zip -Algorithm SHA256).Hash.ToLower()
if ($expected -ne $actual) { throw "checksum mismatch - do not use this download" }

Expand-Archive atlas.zip -DestinationPath C:\Atlas
New-Item -ItemType Directory -Force -Path C:\Atlas\data | Out-Null
```

### 2. Try it in the foreground

```powershell
C:\Atlas\atlas_0.2.0_windows_amd64\atlas.exe serve --addr 127.0.0.1:8080 --data-dir C:\Atlas\data
```

### 3. Run it as a Windows service

`atlas.exe` is a plain console program — it does not implement the Windows
Service Control Manager protocol, so `sc.exe create` pointed straight at it will
register but fail to start ("did not respond in a timely fashion"). Use a service
wrapper. With [WinSW](https://github.com/winsw/winsw), place `atlas-service.xml`
next to the wrapper executable:

```xml
<service>
  <id>atlas</id>
  <name>Atlas BPMN workflow engine</name>
  <description>Durable BPMN 2.x workflow engine.</description>
  <executable>C:\Atlas\atlas_0.2.0_windows_amd64\atlas.exe</executable>
  <arguments>serve --addr 127.0.0.1:8080 --data-dir C:\Atlas\data --auth</arguments>
  <workingdirectory>C:\Atlas</workingdirectory>
  <onfailure action="restart" delay="5 sec"/>
  <log mode="roll-by-size"/>
  <stoptimeout>30 sec</stoptimeout>
</service>
```

```powershell
C:\Atlas\atlas-service.exe install
Start-Service atlas
```

[NSSM](https://nssm.cc/) works equally well if you prefer it. Whichever you pick,
set the service to stop with `SIGTERM`-equivalent behaviour and allow ~30 seconds
so the engine finishes in-flight work.

Then, as on Linux: grant the service account write access to `C:\Atlas\data`
only, put IIS or another TLS terminator in front, and set
`ATLAS_ADMIN_USERNAME` / `ATLAS_ADMIN_PASSWORD` as machine-level environment
variables before the first start with `--auth`.

**PowerShell script tasks** run through `pwsh` — that is PowerShell **Core** (7+),
not the Windows PowerShell 5.1 that ships with the OS. Install it separately if
you need them, or start with `--powershell=false`.

## macOS

`darwin_amd64` and `darwin_arm64` builds are published. Installation is the same
as Linux (download, verify against `SHA256SUMS`, `install` the binary). The
binaries are **not notarized**, so Gatekeeper will quarantine a downloaded
archive; clear it explicitly once you have verified the checksum:

```bash
xattr -d com.apple.quarantine ./atlas
```

For a background service, write a `launchd` plist under
`~/Library/LaunchAgents/` (per-user) or `/Library/LaunchDaemons/` (system-wide)
with `ProgramArguments` set to the same `serve` invocation.

## Configuration reference

Everything is optional — `atlas serve` with no arguments starts a working server.
Flags are listed with their defaults; `atlas serve -h` prints the same list.

### `atlas serve`

| Flag | Default | What it does |
|------|---------|--------------|
| `--addr` | `:8080` | HTTP listen address |
| `--data-dir` | `atlas-data` | WAL, state store, and every other durable file |
| `--auth` | `false` | Require login for the API and UI |
| `--shutdown-timeout` | `10s` | Grace period for in-flight requests on shutdown |
| `--docs` | `true` | Serve `/api/docs` and `/api/v1/openapi.json` |
| `--vault` | `true` | Encrypted secret vault for connector credentials |
| `--user-provisioning` | `true` | Let the system project's approved processes manage Atlas logins |
| `--powershell` | `true` | Run PowerShell script tasks via `pwsh` |
| `--python` | `true` | Run Python script tasks via `python3` |
| `--javascript` | `true` | Run JavaScript script tasks via `node` |
| `--script-timeout` | `30s` | Wall-clock limit for one script task |
| `--checkpoint-interval` | `5m` | How often to snapshot applied state so restarts replay less log; `0` disables |
| `--checkpoint-keep` | `3` | How many checkpoints to retain |
| `--opensearch-url` | `$ATLAS_OPENSEARCH_URL` | Mirror the event log into OpenSearch; empty disables |
| `--opensearch-index` | `$ATLAS_OPENSEARCH_INDEX` | Index the exporter writes to |
| `--retention-max-age` | `$ATLAS_RETENTION_MAX_AGE` | Hard-delete finished instances older than this once exported, e.g. `720h`; `0` disables. A process may override it with its own `atlas:historyTtl` (ADR-0144) |
| `--retention-interval` | `1m` | How often the retention sweep runs |
| `--retention-batch` | `1000` | Finished instances one sweep evaluates; with `--retention-interval` this bounds how fast a backlog drains |

The boolean flags above are all **on by default**, so they are turned off with an
explicit `=false` — `--vault=false`, not `--no-vault`.

### Other subcommands

| Command | Purpose |
|---------|---------|
| `atlas serve [flags]` | Run the engine, API, and UI. This is the default when no subcommand is given. |
| `atlas mcp [--server URL]` | Model Context Protocol adapter on stdio, proxying to a running server (default `http://localhost:8080`) |
| `atlas reset-password [--data-dir DIR] [--create-admin] [--password-stdin] USERNAME` | Reset a local user's password straight against the data directory |
| `atlas version` | Version, git revision, and Go toolchain |
| `atlas help` | Usage |

### Environment variables

Secrets live here rather than in flags, so they never show up in `ps` or shell
history.

| Variable | Used for |
|----------|----------|
| `ATLAS_ADMIN_USERNAME` | Bootstrap admin name (default `admin`); only read while the user store is empty and `--auth` is on |
| `ATLAS_ADMIN_PASSWORD` | Bootstrap admin password; if unset, one is generated and logged once |
| `ATLAS_VAULT_KEY` | Vault master key, 64 hex chars or base64; never written to disk |
| `ATLAS_VAULT_KEY_FILE` | Path to a file holding that key |
| `ATLAS_OPENSEARCH_URL` | Default for `--opensearch-url` |
| `ATLAS_OPENSEARCH_INDEX` | Default for `--opensearch-index` |
| `ATLAS_OPENSEARCH_USERNAME`, `ATLAS_OPENSEARCH_PASSWORD` | Exporter credentials (env-only) |
| `ATLAS_RETENTION_MAX_AGE` | Default for `--retention-max-age` |
| `ATLAS_RETENTION_INTERVAL` | Default for `--retention-interval` |
| `ATLAS_RETENTION_BATCH` | Default for `--retention-batch` |
| `ATLAS_DMN_RESOLVER_URL`, `ATLAS_DMN_RESOLVER_TOKEN` | Resolve DMN models from a remote service instead of `<data-dir>/dmn-models` |
| `ATLAS_TEMIS_CONNECTORS` | Comma-separated connector names, each configured by `ATLAS_TEMIS_<NAME>_URL` and `ATLAS_TEMIS_<NAME>_TOKEN` |
| `ATLAS_CONNECTOR_<REF>_TOKEN` | Bearer token for the REST connector named `<REF>` |

### Endpoints

| Path | What it is |
|------|------------|
| `/` | Web UI — modeler, operations, tasks, and the in-app handbook |
| `/api/v1/…` | JSON API |
| `/api/docs` | API explorer (disable with `--docs=false`) |
| `/healthz` | Liveness — is the process alive. Unconditional; never gated by `--auth` |
| `/readyz` | Readiness — should this instance be routed traffic. Never gated by `--auth` |
| `/mcp` | Model Context Protocol endpoint — **not authenticated at the transport level** |

#### Health and readiness

`/healthz` and `/readyz` answer different questions and are not interchangeable.

`GET /healthz` always returns `200 ok`. That is the point: the only remedy a liveness
probe has is a restart, so it must not fail for anything a restart would not fix. Point
a liveness probe here.

`GET /readyz` returns `200 ok` only when this instance can actually serve, and `503`
with a one-line reason otherwise:

| Reason | What it means |
|--------|---------------|
| `server is shutting down` | Shutdown has begun; work handed to it now would be dropped |
| `startup recovery has not finished` | The log has not been replayed, so state does not yet describe reality |
| `state store is not readable: …` | A point read of the state store failed |
| `the partition writer is not responding` | The goroutine that owns the engine did not answer within two seconds — a blocked fsync on a hung volume looks like this |

Point a readiness probe (and, in Kubernetes, the startup probe) here. Neither endpoint is
gated by `--auth`, because a kubelet carries no session.

Note that the server does not open its listening port until startup recovery has
finished, so a probe that gets a connection refused during a restart is seeing that
replay. Give the startup probe enough budget to wait it out rather than restarting into
a replay that then starts over — the bundled Helm chart allows ten minutes.

## The data directory

Everything durable lives under `--data-dir`. Back up the whole directory as a
unit; the parts are not independently consistent.

| Path | Contents |
|------|----------|
| `wal/` | The write-ahead log — the source of truth |
| `state/` | Materialized state (embedded LSM store), rebuildable from the WAL |
| `checkpoints/` | Recovery checkpoints, so a restart replays only the log after the newest one |
| `vault.key` | Vault master key, mode `0600`, only when generated rather than supplied |
| `vault/` | Encrypted connector secrets |
| `deployments/`, `drafts/`, `forms/`, `projects/`, `releases/`, `users/`, `connectors/`, `settings/`, … | Design-time and administrative stores |
| `dmn-models/` | DMN models, unless resolved remotely |
| `exporter/` | OpenSearch export position, when the exporter is on |

### Backups

The engine holds these files open, so the safe order is:

1. `systemctl stop atlas`
2. Archive the whole `--data-dir` (plus `vault.key`, if you keep it elsewhere)
3. `systemctl start atlas`

A filesystem or volume snapshot works without stopping, provided it is atomic
across the entire directory. Copying the tree file-by-file on a running server
does **not** — you will capture a WAL and a state store from different moments.

Atlas can also export a whole-instance snapshot through the API and stage a
restore that is applied on the next start; see
[ADR-0109](adr/0109-full-instance-snapshot.md).

## Upgrading

While Atlas is `0.x`, on-disk formats can change between releases. Read
[`CHANGELOG.md`](../CHANGELOG.md) first, then:

```bash
sudo systemctl stop atlas
# back up /var/lib/atlas here
sudo install -m 0755 ./atlas /usr/local/bin/atlas
sudo systemctl start atlas
journalctl -u atlas -n 50
```

Downgrading is not supported: a newer build may have written state an older one
cannot read. Restore the backup instead.

## Uninstalling

```bash
sudo systemctl disable --now atlas
sudo rm /etc/systemd/system/atlas.service /usr/local/bin/atlas
sudo systemctl daemon-reload
sudo rm -rf /var/lib/atlas /etc/atlas   # deletes all process data
```

## Building from source

You do not need this to run Atlas, but it is two commands. Go 1.26+ and no CGO:

```bash
git clone https://github.com/pblumer/atlas.git
cd atlas
go build -o atlas ./cmd/atlas
```

`make build` does the same with the release flags. See
[`DEVELOPMENT.md`](../DEVELOPMENT.md) for the test and lint sequence.

## Troubleshooting

**`bind: address already in use`** — something else holds the port. Pick another
with `--addr :9090`, or find the holder: `sudo ss -lptn 'sport = :8080'`.

**`permission denied` on the data directory** — the service user does not own it:
`sudo chown -R atlas:atlas /var/lib/atlas`.

**The server hangs on start, or the log stops after "opening data directory"** —
another `atlas serve` already owns that directory. Only one may.

**`WARNING: python script worker enabled but "python3" was not found on PATH`** —
exactly what it says. Install the interpreter, or start with `--python=false`.
Tasks in that language park until it is available; nothing is lost.

**No login prompt appears** — `--auth` is off. Nothing is protected in that mode;
add the flag and restart.

**You cannot log in** — use `atlas reset-password` against the data directory
(see [step 6](#6-turn-on-authentication)). It works whether or not the server is
running, because login re-reads the user store on every attempt.

**Recovery takes a long time after a crash** — the WAL is replayed from the
newest usable checkpoint. If checkpointing was disabled (`--checkpoint-interval
0`), replay starts at genesis. Re-enable it.

## Where to go next

- The **in-app handbook** at `/` in the UI — onboarding, BPMN and DMN from
  scratch, designing and running processes, testing and simulation
- **[Postman onboarding kit](../postman/)** — drive the HTTP API end to end
- **[Deploying Atlas](../deploy/README.md)** — container image and Helm chart
- **[Architecture overview](ARCHITECTURE.md)** — how the engine works inside
