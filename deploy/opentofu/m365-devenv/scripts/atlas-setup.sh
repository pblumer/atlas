#!/usr/bin/env bash
# First-boot setup of the Atlas server (run once by cloud-init, log in
# /var/log/atlas-setup.log). Reads /etc/atlas/setup.env, written by OpenTofu.
#
#   1. packages: Caddy (TLS in front of Atlas), Python 3 (bootstrap), and - for the
#      Teams Phone worker - PowerShell 7 with the MicrosoftTeams module
#   2. the domain controller: its private name in /etc/hosts, its LDAPS certificate
#      in the system trust store (the AD worker verifies against it)
#   3. Atlas: a release checked against SHA256SUMS, or a build from source
#   4. the atlas service, then Caddy
#   5. the bootstrap: workers, vault bundles and an API token in the Console
#
# Re-running it is safe; a finished run leaves /var/lib/atlas-setup.done.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
echo "== atlas-setup $(date -Is)"

# shellcheck disable=SC1091
. /etc/atlas/setup.env

retry() { local n=0; until "$@"; do n=$((n + 1)); [ "$n" -ge 5 ] && return 1; sleep $((n * 10)); done; }

# --- 1. packages -------------------------------------------------------------
retry apt-get update -q
retry apt-get install -yq --no-install-recommends ca-certificates curl python3 caddy

if [ "${WITH_TEAMS}" = "1" ]; then
  . /etc/os-release
  curl -fsSL -o /tmp/packages-microsoft-prod.deb \
    "https://packages.microsoft.com/config/ubuntu/${VERSION_ID}/packages-microsoft-prod.deb"
  dpkg -i /tmp/packages-microsoft-prod.deb
  retry apt-get update -q
  retry apt-get install -yq powershell
  # AllUsers, so the atlas service account finds it.
  retry pwsh -NoProfile -NonInteractive -Command \
    "Set-PSRepository -Name PSGallery -InstallationPolicy Trusted; Install-Module -Name MicrosoftTeams -Scope AllUsers -Force -ErrorAction Stop"
fi

# --- 2. domain controller -----------------------------------------------------
if [ -n "${AD_HOSTS_LINE}" ]; then
  grep -qxF "${AD_HOSTS_LINE}" /etc/hosts || echo "${AD_HOSTS_LINE}" >>/etc/hosts
  update-ca-certificates
fi

# --- 3. Atlas -----------------------------------------------------------------
if [ ! -x /usr/local/bin/atlas ]; then
  work=$(mktemp -d)
  if [ "${ATLAS_INSTALL}" = "release" ]; then
    base="https://github.com/pblumer/atlas/releases/download/v${ATLAS_VERSION}"
    archive="atlas_${ATLAS_VERSION}_linux_amd64.tar.gz"
    (cd "$work" &&
      retry curl -fsSLO "${base}/${archive}" &&
      retry curl -fsSLO "${base}/SHA256SUMS" &&
      sha256sum --check --ignore-missing SHA256SUMS &&
      tar -xzf "${archive}")
    install -m 0755 "$work/atlas_${ATLAS_VERSION}_linux_amd64/atlas" /usr/local/bin/atlas
  else
    retry apt-get install -yq --no-install-recommends git golang-go
    retry git clone --depth 1 --branch "${ATLAS_SOURCE_REF}" "${ATLAS_SOURCE_REPO}" "$work/src"
    # golang-go of Ubuntu 24.04 is older than go.mod asks for; GOTOOLCHAIN=auto
    # fetches the required toolchain from the Go module proxy.
    (cd "$work/src" &&
      HOME=/root GOTOOLCHAIN=auto GOPATH=/root/go CGO_ENABLED=0 \
        go build -trimpath -ldflags="-s -w" -o /usr/local/bin/atlas ./cmd/atlas)
  fi
  rm -rf "$work"
fi
/usr/local/bin/atlas version || true

# --- 4. services -------------------------------------------------------------
id atlas >/dev/null 2>&1 || useradd --system --home-dir /var/lib/atlas --shell /usr/sbin/nologin atlas
install -d -o atlas -g atlas -m 0750 /var/lib/atlas
systemctl daemon-reload
systemctl enable --now atlas

install -m 0644 /etc/atlas/Caddyfile /etc/caddy/Caddyfile
systemctl enable caddy
systemctl restart caddy

# --- 5. bootstrap ------------------------------------------------------------
# The settings file is removed once the bootstrap succeeded; a re-run skips it.
if [ -f /etc/atlas/bootstrap.json ]; then
  python3 -I /usr/local/sbin/atlas-bootstrap.py /etc/atlas/bootstrap.json
fi

touch /var/lib/atlas-setup.done
echo "== atlas-setup done $(date -Is)"
