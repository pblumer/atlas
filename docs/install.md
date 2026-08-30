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
curl -fsSLO https://github.com/pblumer/atlas/releases/download/v0.4.0/atlas_0.4.0_linux_amd64.tar.gz
tar -xzf atlas_0.4.0_linux_amd64.tar.gz
./atlas_0.4.0_linux_amd64/atlas serve
```

Then open <http://127.0.0.1:8080/>. Authentication is off, so there is no login —
which is exactly why this is a "try it" recipe and not an install. For anything
that outlives the afternoon, follow the steps below.

The URL names the version rather than using `/releases/latest/`: while Atlas is
pre-1.0 every release is published as a **prerelease**, and GitHub's "latest"
never points at one — so a `/latest/` link would 404. Bump the version here when
you cut a release, as in the steps below.

## Linux, step by step

### 1. Download the release and check it

Releases are at <https://github.com/pblumer/atlas/releases>. Each one carries a
`SHA256SUMS` asset covering every archive; verify against it rather than trusting
the transfer. Substitute the version and architecture you want — `linux_amd64`,
`linux_arm64`, or `linux_arm` (ARMv6, for a 32-bit Raspberry Pi OS).

```bash
VERSION=0.4.0
ARCH=linux_amd64
BASE=https://github.com/pblumer/atlas/releases/download/v${VERSION}

curl -fsSLO ${BASE}/atlas_${VERSION}_${ARCH}.tar.gz
curl -fsSLO ${BASE}/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
```

`sha256sum` must print `atlas_0.4.0_linux_amd64.tar.gz: OK`. If it prints
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
time=2026-01-31T09:14:02.117Z level=INFO msg="listening; recovery is complete and this instance is ready" event=server.listening addr=127.0.0.1:8080 ui=http://127.0.0.1:8080/ mcp=http://127.0.0.1:8080/mcp
```

Check it from another shell, then stop it with `Ctrl-C`:

```bash
curl -fsS http://127.0.0.1:8080/healthz
```

Binding to `127.0.0.1` on the first run is on purpose. A login is required by
default, so the first start also seeds an administrator and logs a generated
password once (step 6) — binding to loopback means that window is not reachable
from anywhere else while you collect it.

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

### 6. The administrator account

Authentication is **on by default**; the unit above passes `--auth` explicitly so
the file says what it relies on. On the **first** start with an empty user store,
Atlas seeds one administrator:

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

### Roles: who may do what

Being signed in is one question; what that account may do is another. Every
endpoint names the role it needs, and the server checks it before the request
reaches anything — for a browser session, an API token, a deploy token, an OAuth
grant and every MCP tool call alike.

| Role | May |
|---|---|
| `admin` | everything, including accounts, credentials, secrets, settings, backup and restore |
| `modeler` | author drafts, forms and decisions — and **deploy** them |
| `operator` | start, cancel, terminate and repair instances; read runtime data |
| `user` | work on tasks and read what they are given |

An account carries **several** roles, not one: they are a list, not a ladder, so a
modeller who also starts test instances holds `modeler` *and* `operator`. An
administrator reaches everything.

A few runtime operations stay with `admin` because they were admin-only before and
this release takes nothing away from anybody: rewriting a running instance's
variables, migrating instances onto another version, and reading a worker's job
history.

Grant them in the Console under **Organization → Users**, where each role is listed
with what it lets the person do. The navigation then offers only the apps and
screens that person can actually use.

Two things to know when you upgrade an installation that predates this:

- **Every existing account keeps what it could do** — `modeler`, `operator` and
  `user`, so everything except administration. Nothing stops working, and nothing is
  narrower than it was until you narrow it. The startup log says how many accounts
  were carried over (`event=auth.roles_upgraded`).
- **What you narrow, stays narrow.** The carry-over runs once per account, so an
  account you set back to `user` is still `user` after the next restart.

New accounts get `user`. An API token carries the roles of the account that minted
it and is **never** an administrator, whoever mints it. With `--auth=false` there is
no account and none of this is enforced.

### Single sign-on with an identity provider

Optional, and off unless you configure it. With no provider set, Atlas
authenticates people exactly as it did before — a local password, no outbound
connection, no dependency on anybody else being up.

Point it at an OpenID Connect provider (Entra ID, Keycloak, Google, Auth0, any
compliant one) with three settings:

```bash
sudo tee -a /etc/atlas/atlas.env >/dev/null <<'EOF'
ATLAS_OIDC_ISSUER=https://login.example.com/realms/atlas
ATLAS_OIDC_CLIENT_ID=atlas
ATLAS_OIDC_CLIENT_SECRET=the-secret-the-provider-issued
ATLAS_EXTERNAL_URL=https://atlas.example.com
EOF
sudo systemctl restart atlas
```

`ATLAS_EXTERNAL_URL` matters here: the redirect URI is built from it, so it has to
be the origin people actually reach. Register **`<external-url>/auth/oidc/callback`**
as the redirect URI at your provider — that exact string, or the provider will
refuse the login.

Two more settings if you need them: `ATLAS_OIDC_SCOPES` (default
`openid profile email`) and `ATLAS_OIDC_NAME`, which is what the button on the
login screen says. The client secret may be omitted for a provider that registered
Atlas as a public client; the flow uses PKCE either way.

What to expect once it is on:

- The login screen gains a **Sign in with …** button and keeps the password form.
- A first sign-in **creates an account** — source `oidc`, linked to the provider's
  subject — with the `user` role and nothing else. Grant more under
  **Organization → Users**, as for any account.
- The link is the provider's subject, not the email address. A person whose name
  or address changes at the provider keeps their account; a matching local account
  is *not* adopted, so somebody with both has two records until you remove one.
- Sign-in is refused, with the reason in the server log and not on screen, when the
  token is for another audience, has expired, carries the wrong nonce, or is signed
  by a key the provider does not publish.

**Keep one local administrator.** A provider that is unreachable — an expired
certificate, a moved discovery document, a closed network path — takes federated
sign-in with it. The local password remains the way back in, and Atlas refuses to
leave an instance without an enabled administrator.

#### Letting the provider's groups decide roles

Optional, and off until you turn it on. Under **Organization → Single sign-on** you
name one claim in the provider's token and a list of exact values it may carry, and
each value names the Atlas roles it grants and the groups it puts a person in. From
that moment, onboarding and offboarding are a group membership somebody already
maintains: the role and the shared projects arrive at the next sign-in, and go away
at the sign-in after the membership does.

The claim is whatever your provider emits — `groups` for many, `roles`, or a dotted
path like `realm_access.roles` for Keycloak. Values are compared exactly; Atlas does
not interpret them, so a group name, an object id and a role name all work as long
as the token carries that string.

Four things worth knowing before switching it on:

- **Roles become the provider's to decide.** While the mapping is on, a role granted
  by hand under **Organization → Users** is replaced at that person's next sign-in.
  If you want to grant roles here, leave the mapping off.
- **Group membership follows only for the groups your rules name.** A group no rule
  mentions is left alone, so a membership you added by hand there survives.
- **Nothing is granted by absence.** Somebody the provider says nothing about
  matches no rule and gets `user` — which everybody who can sign in holds, mapping
  or not.
- **A rule that cannot work is refused when you save it**, not silently ignored at
  every login: a role Atlas does not enforce, or a group that no longer exists.

The mapping cannot lock you out of the local administrator account, which is not
federated. If a mapping does leave the instance without a federated administrator,
sign in locally — or reset that password on the host with
`atlas reset-password --data-dir <dir> <username>` — and fix the rules.

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

`/mcp` — the endpoint an AI agent drives the server through — is gated by
`--auth` like the rest of the API
([ADR-0196](adr/0196-authenticated-mcp-transport.md)).
A request that carries no credential is answered with `401`, and a tool call acts
as whoever made it, with exactly their permissions. It used to be open at the
transport level whatever `--auth` said, so a proxy rule was the only thing in
front of it; if you are upgrading, that rule is now belt and braces rather than
the protection itself.

If you do not want an agent surface at all, block it at the proxy:

```nginx
location /mcp { deny all; }
```

### Who may configure a connector

A connector has an **owner**: whoever created it. They, whoever they share it with,
and administrators can see its endpoint and credential reference, change it, delete
it, or give it an inbound subscription
([ADR-0205](adr/0205-connector-ownership-and-event-delivery.md)). Everybody else
still sees that it exists — its name, kind and whether it is usable — because that
is what the modeler needs to author a task against it.

Sharing is in **Console → Connectors**, beside each connector: add a person or a
whole group as *may see it* or *may change it*, and withdraw either at any time.
Ownership can be handed on, which is how a connector survives the person who made
it — an ownerless connector is administrators-only.

**Its events reach only the processes you allow.** An inbound subscription claims
the message name it publishes under: a process deployed by somebody who cannot
reach the connector will not be delivered those events, and pointing a connector at
a name somebody else's process already listens for is refused rather than silently
forwarding your post to them. Both refusals name the message, never the other
party. If a deploy or a subscription is refused this way, rename the message in
your model — or ask whoever owns the connector to share it with you.

One thing this does **not** do: it does not reach the runtime. A deployed process
resolves its connector by name whoever started it, and while a message correlates
the engine still matches on name and key alone. This is a gate at the two points
where a model and a connector meet, not isolation inside the engine.

**Upgrading:** connectors stored before this carry no owner and become
administrators-only until one is assigned. An administrator can hand each to its
real owner from the same page. Processes deployed before this carry no deployer
either, so they keep any message name they already listen for until they are
redeployed — deploy them again to bring them under the claim.

### Connecting a hosted AI connector

A connector that runs on somebody else's infrastructure — claude.ai's, for
instance — has nowhere for you to paste a token. Those connect over OAuth
([ADR-0200](adr/0200-mcp-oauth-resource-server.md)): you register the application
once, and each person who uses it approves it in their own browser and gets a
token that carries *them*.

One thing has to be right first: set `--external-url` to the address people reach
Atlas at. Behind a TLS proxy Atlas cannot work it out, and every URL it publishes
would name `http://`, which no hosted connector can use.

Then do it in the Console: **Console → AI access → Connect an assistant**. Pick the
application, press Create, and the page hands you the three values the connector's
own dialog is asking for — MCP server URL, client id, and a **secret shown exactly
once** — each with a copy button. (It also checks the published address for you and
says so if it is not one a connector could reach.) The same page lists what is
registered and every approval given, and is where an approval is withdrawn.

For scripting, the endpoint behind that form is
`POST /api/v1/oauth-clients` with `{"name": …, "redirectUris": [ … ]}`; only the
fingerprint of the secret is stored, so a lost one is reissued rather than looked
up. The redirect URI must match what the connector uses, character for character:
it is where the authorization code is sent, so it is matched whole and never by
prefix.

What each person then gets is confined to what they approved. A connector set up
against `/mcp` drives the MCP tools and is refused at `/api/v1`; the token expires
in two hours and renews itself silently. Anyone can see their own approvals at
`GET /api/v1/oauth-grants` and withdraw one with `DELETE`; an administrator sees
and can withdraw everyone's, and deleting the client withdraws all of them at
once — all of it on the same **AI access** page, for people who do not use the API.
Disabling an account revokes its approvals with it.

Registering a client is not the same as being able to use it: an application may
*ask*, and only a person can say yes.

#### Letting connectors register themselves

If entering each client by hand is more ceremony than you want, `--oauth-dynamic-registration`
(or `ATLAS_OAUTH_DYNAMIC_REGISTRATION=1`) opens [RFC 7591](https://www.rfc-editor.org/rfc/rfc7591.html)
self-registration. A connector then needs nothing but the MCP URL: it registers
itself, and the person approves it as usual.

**It is off by default, and it is worth understanding what turning it on means.**
It is the only unauthenticated endpoint in Atlas that writes durable state:
anyone who can reach the port may create a client record and appear on your
people's consent screens under a name they chose. What makes that liveable is
that the consent screen **says so** — a self-registered application is labelled
there, in as many words, so nobody mistakes it for one you vetted. Nothing is
reached until a person approves, and what they approve is bounded by their own
account.

The number of self-registered clients is capped; past the cap, registering evicts
the oldest one nobody approved. An approved client is never evicted, so a flood
cannot take somebody's access away — but a client that has registered and is
still waiting for its person to approve can be, and would have to register again.
The AI access page marks which clients registered themselves, and
`auth.oauth_client_self_registered` records each one as it happens.

Leave it off if your connectors are few and known; an administrator registering
them by hand is the stronger posture, and it is why that is the default.

## Windows Server

The release includes a `windows_amd64` build. Note that this is the **binary** on
Windows — Windows *containers* are not supported.

### 1. Download and verify

In an elevated PowerShell:

```powershell
$Version = '0.4.0'
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
C:\Atlas\atlas_0.4.0_windows_amd64\atlas.exe serve --addr 127.0.0.1:8080 --data-dir C:\Atlas\data
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
  <executable>C:\Atlas\atlas_0.4.0_windows_amd64\atlas.exe</executable>
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
| `--auth` | `true` | Require login for the API, the UI and `/mcp`. `--auth=false` runs the server open — development and demos only; it logs a warning (`auth.disabled`) at startup. Sign-in attempts are throttled per address and per account, and every one is recorded (see [Logs](#logs)) |
| `--oauth-dynamic-registration` | `false` | Let an OAuth client register itself ([RFC 7591](https://www.rfc-editor.org/rfc/rfc7591.html)), so a hosted MCP connector can be connected with nothing but this server's URL. Off by default: it is the only unauthenticated endpoint that writes durable state, and anyone who can reach the port could then appear on your people's consent screens under a name they chose — where such a client is labelled as self-registered ([ADR-0200](adr/0200-mcp-oauth-resource-server.md)). Also `ATLAS_OAUTH_DYNAMIC_REGISTRATION=1` |
| `--external-url` | *(derived)* | Public origin this server is reachable under, e.g. `https://atlas.example.com`. **Set this behind a reverse proxy:** Atlas terminates no TLS, so the origin it derives from a request is `http://…`, and every absolute URL it publishes — the OAuth discovery documents, the `WWW-Authenticate` challenge, the authorization and token endpoints — would name something no client can use ([ADR-0200](adr/0200-mcp-oauth-resource-server.md)). Also `ATLAS_EXTERNAL_URL` |
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
| `--compact-wal` | `false` | Delete WAL segments already covered by a checkpoint and every consumer watermark. Irreversible, so opt-in; requires checkpointing |
| `--metrics` | `true` | Serve the Prometheus exposition at `/metrics`. Gated by `--auth` like every other route — give the scraper an API token scoped `metrics` (see [Credentials for machines](#credentials-for-machines)) |
| `--log-format` | `text` | `text` for a terminal, `json` for a log shipper — see [Logs](#logs) |
| `--trace-endpoint` | `$OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/HTTP collector base URL to export request traces to; empty disables tracing — see [Traces](#traces) |
| `--trace-sample-ratio` | `0.1` | Fraction of traces to record, `0` to `1` |
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
| `atlas mcp [--server URL] [--token TOKEN]` | Model Context Protocol adapter on stdio, proxying to a running server (default `http://localhost:8080`). `--token` (or `ATLAS_TOKEN`) is what it authenticates with against a server running `--auth` |
| `atlas reset-password [--data-dir DIR] [--create-admin] [--password-stdin] USERNAME` | Reset a local user's password straight against the data directory |
| `atlas version` | Version, git revision, and Go toolchain |
| `atlas help` | Usage |

### Environment variables

Secrets live here rather than in flags, so they never show up in `ps` or shell
history.

| Variable | Used for |
|----------|----------|
| `ATLAS_TOKEN` | The credential `atlas worker` and `atlas mcp` authenticate with, and what supervised workers are given if you set it on the server. It must be an **API token** the server accepts (see below); an arbitrary value is refused, and the server warns at startup if you set one |
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
| `ATLAS_AD_MOCK`, `ATLAS_AD_MOCK_SEED` | Serve Active Directory tasks against a mock directory in the worker's memory, optionally seeded from an LDIF or DSML file ([ADR-0181](adr/0181-ad-connector-mock-mode.md)). For a worker Atlas supervises, prefer the switch in Console → Connectors → Active Directory: it needs no restart. These variables remain the way to configure a worker you start yourself, and the way a server decides before anyone has used that switch |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Default for `--trace-endpoint`; the standard OpenTelemetry variable, honored so a deployment that already sets it needs no Atlas-specific flag |
| `OTEL_SERVICE_NAME` | Name this process reports on exported traces (default `atlas`) |

### Endpoints

| Path | What it is |
|------|------------|
| `/` | Web UI — modeler, operations, tasks, and the in-app handbook |
| `/api/v1/…` | JSON API |
| `/api/docs` | API explorer (disable with `--docs=false`) |
| `/metrics` | Prometheus exposition — gated by `--auth`; scrape with a token scoped `metrics` |
| `/healthz` | Liveness — is the process alive. Unconditional; never gated by `--auth` |
| `/readyz` | Readiness — should this instance be routed traffic. Never gated by `--auth` |
| `/mcp` | Model Context Protocol endpoint — gated by `--auth`; a tool call acts as the caller |
| `/api/v1/openapi.json`, `/api/docs` | The API description and explorer — served with `--docs`, and gated by `--auth` like the rest of the API |

An external worker leases a job with `POST /api/v1/jobs/{key}/activate` and reports the
outcome with `.../complete` or `.../fail` ([ADR-0007](adr/0007-job-worker-protocol.md)).
A lease holds the job for `leaseMs` (5 minutes by default, 24 hours at most) and takes it
off the list other workers are offered; when it elapses the job is offered again, so a
worker that crashes does not park its work forever. Leasing a job someone else holds is a
`409`. Pulling by job *type* — "give me the next `send-email` job" — is not available yet;
the ADR records why.

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

### Logs

Every line Atlas writes carries a stable **`event=` name** beside the sentence. The
sentence explains, the name identifies — so an alert matches on `event=checkpoint.failed`
and keeps working when the wording changes, and values arrive as fields instead of buried
in English.

```
time=2026-01-31T09:19:02.884Z level=INFO msg="published a recovery checkpoint; recovery replays only past it" event=checkpoint.published position=48213
time=2026-01-31T09:19:02.885Z level=WARN msg="wal compaction failed; will retry next tick" event=wal_compaction.failed error="no space left on device"
```

`--log-format=json` emits the same records as one JSON object per line, for a shipper
that would otherwise need a parsing rule of its own. Text is the default because a
terminal is the audience Atlas has always had.

```json
{"time":"2026-01-31T09:19:02.884Z","level":"INFO","msg":"published a recovery checkpoint; recovery replays only past it","event":"checkpoint.published","position":48213}
```

Everything goes to **stderr**, including lines from libraries, so `journalctl -u atlas`
and `docker logs` see one stream in one shape. The most recent lines are also readable
over the API at `GET /api/v1/logs` and in the UI, which is a diagnostic tail rather than
storage — it is bounded and does not survive a restart.

Event names an operator is most likely to alert on:

| Event | Level | Meaning |
|-------|-------|---------|
| `server.listening` | INFO | The listener is up. It comes *after* recovery, so this is also "this instance finished starting" |
| `server.shutting_down` | INFO | SIGTERM received; in-flight requests are being drained |
| `command.failed` | ERROR | A command exited non-zero |
| `checkpoint.published` | INFO | A recovery checkpoint was captured, with the log `position` it covers |
| `checkpoint.failed` | WARN | The checkpoint pass failed; the next tick retries. Persistent failures mean restarts replay more log |
| `wal_compaction.failed` | WARN | Compaction failed; segments are kept. Safe, but the log keeps growing |
| `wal_compaction.inert` | WARN | `--compact-wal` without `--checkpoint-interval` — it is doing nothing |
| `exporter.tick_failed` | WARN | An OpenSearch export tick failed; lag grows until it recovers |
| `retention.purged` | INFO | Finished instances were hard-deleted, with how many |
| `script_worker.binary_missing` | WARN | A script language is enabled but its interpreter is absent; those tasks park |
| `auth.admin_seeded` | WARN | The bootstrap administrator was created with a generated password |
| `auth.disabled` | WARN | The server was started with `--auth=false` and requires no login for anything |

**The security audit trail.** Every line below carries the acting principal
(`actor`, `actor_id`) where the request has one, and the `client_ip` always. None
of them carries a password, a hash or a token. Ship them with `--log-format=json`.

| Event | Level | Meaning |
|-------|-------|---------|
| `auth.login` | INFO | A successful sign-in, with `username` and `user_id` |
| `auth.login_failed` | WARN | A refused sign-in, with the `username` attempted and a `reason` (`no such account`, `account disabled`, `wrong password`). The response says only "invalid credentials" — the reason is for you, not for the caller |
| `auth.login_throttled` | WARN | An attempt refused before any password check, because the address or the account had spent its budget |
| `auth.logout` | INFO | A session was ended by its owner |
| `auth.denied` | WARN | A signed-in caller was refused for lacking the admin role, with the `method` and `path`. Anonymous `401`s are deliberately *not* logged — they would bury this under every probe that finds the port |
| `auth.user_created`, `auth.user_updated`, `auth.user_deleted` | INFO | The account lifecycle, naming both the actor and the subject; the update line carries the `roles` and `disabled` state that resulted |
| `auth.password_set` | INFO | An administrator replaced a user's password (that it happened and for whom — never the password) |
| `auth.token_minted`, `auth.token_revoked` | INFO | A machine credential — an API token or a deploy token — was issued or revoked, by `token_id` and `token_name`; a mint also records its `scope` and `expires_at` |
| `auth.worker_token_unknown` | WARN | `ATLAS_TOKEN` is set to a value this server does not accept. Supervised workers are handed it instead of the server's own token and will be refused at every poll — mint an API token with scope `worker`, or unset the variable |

Event names are treated as an API: renaming one is a breaking change and appears under
_Changed_ in the [changelog](../CHANGELOG.md). Secrets never become fields — the seeded
admin password stays inside the message text precisely because a field is what a log
shipper extracts and keeps.

### Credentials for machines

A person signs in; a machine presents an **API token**. Mint one as an
administrator — the secret comes back exactly once, because the server stores only
its SHA-256:

```bash
curl -sS -X POST http://127.0.0.1:8080/api/v1/api-tokens \
  -b cookies.txt -H 'Content-Type: application/json' \
  -d '{"name":"worker on host-b","scope":"worker","expiresInDays":90}'
# {"id":"…","name":"worker on host-b","scope":"worker","expiresAt":…,"token":"atlasat_…"}
```

| Scope | Reaches |
|-------|---------|
| `worker` | Only what `atlas worker` does: lease a batch of jobs, settle each one, and post a preview mail back to the outbox. Nothing else — the right scope for a worker running in another network zone |
| `metrics` | Only `GET /metrics`. The narrowest scope there is, for a Prometheus scraper |
| `full` | Everything a signed-in non-admin reaches, for a CI job or an MCP adapter whose calls cannot be enumerated in advance. Broad by design, and never an admin: user management, secrets and backups stay refused |

Then hand it over as `--token` or `ATLAS_TOKEN`:

```bash
atlas worker --server https://atlas.example.com --token "$ATLAS_TOKEN" --connector script
atlas mcp    --server https://atlas.example.com --token "$ATLAS_TOKEN"
```

For Prometheus, that is two lines in the scrape config:

```yaml
scrape_configs:
  - job_name: atlas
    authorization:
      credentials: atlasat_…      # a token scoped "metrics"
    static_configs:
      - targets: ['atlas.example.com:8080']
```

`GET /api/v1/api-tokens` lists what exists (identity, scope, lifetime — never a
secret) and `DELETE /api/v1/api-tokens/{id}` revokes one, effective on the next
request. An expired token is refused exactly like an unknown one, and its record
stays listed so you can see what needs reissuing.

A **deploy token** (`atlasat_` vs `atlasdt_`) is the separate, narrower credential
a peer Atlas uses to publish a bundle here; see
[ADR-0129](adr/0129-remote-deployment-targets.md).

### Traces

Off unless you point Atlas at a collector. `--trace-endpoint=http://collector:4318`
(or the standard `OTEL_EXPORTER_OTLP_ENDPOINT`) turns on OpenTelemetry tracing for the
`/api/v1` surface; Atlas posts to `<endpoint>/v1/traces` in OTLP's JSON encoding, which
every OTLP/HTTP receiver accepts.

```bash
atlas serve --trace-endpoint http://collector:4318 --trace-sample-ratio 0.1
```

What you get, and what you deliberately do not:

| | |
|---|---|
| **Traced** | Every `/api/v1` request, as a server span named for its route |
| **Not traced** | `/healthz`, `/readyz`, `/metrics`, and the static UI — they run on a timer forever and would bury the spans you are looking for |
| **Not traced** | The engine's batch loop. A span costs an allocation, a clock read and a lock; the single writer takes none of them, and a test enforces it |

A span is named for the **route pattern** — `GET /api/v1/instances/{key}` — never the URL
that matched it, so the number of distinct span names is fixed by the code rather than
growing with your instances. The attributes are the method, the route and the response
status; the raw target and query string are not recorded.

Incoming **W3C `traceparent`** headers are honored, so a request that arrives from
another traced service continues that trace instead of starting a new one. A caller that
already decided to sample is always respected, whatever `--trace-sample-ratio` says — a
half-recorded distributed trace is worse than none.

Sampling applies to traces Atlas starts. `0.1` records one in ten; `0` records nothing;
`1` records everything, which is fine for a quiet server and expensive for a busy one.

If the collector is down, slow, or rejecting payloads, requests are unaffected: export
happens on its own goroutine after the response is sent. That makes a collector outage an
observability problem rather than an availability one.

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

**No login prompt appears** — the server was started with `--auth=false`. Nothing is protected in that mode;
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
