#!/usr/bin/env bash
# Waits until the Atlas server answers and its first-boot setup has finished, then
# prints the last lines of the setup log. Setup takes some 10-20 minutes (packages,
# PowerShell and the MicrosoftTeams module, Atlas, bootstrap).
#
#   ./scripts/wait-atlas.sh
set -euo pipefail

cd "$(dirname "$0")/.."
url="$(tofu output -raw atlas_url)"
ssh_cmd="$(tofu output -raw atlas_ssh)"
timeout_min="${WAIT_MINUTES:-30}"
deadline=$(( $(date +%s) + timeout_min * 60 ))

# -k: with atlas_tls = internal the certificate comes from Caddy's own CA.
echo "wait-atlas: waiting for $url (up to $timeout_min minutes)"
until curl -ksf -o /dev/null "$url/healthz"; do
  (( $(date +%s) < deadline )) || { echo "wait-atlas: no answer in time; look at /var/log/atlas-setup.log ($ssh_cmd)" >&2; exit 1; }
  sleep 20
done
echo "wait-atlas: Atlas answers"

# shellcheck disable=SC2086
until $ssh_cmd -o StrictHostKeyChecking=accept-new -o BatchMode=yes test -f /var/lib/atlas-setup.done 2>/dev/null; do
  (( $(date +%s) < deadline )) || { echo "wait-atlas: setup not finished in time" >&2; break; }
  sleep 20
done
# shellcheck disable=SC2086
$ssh_cmd -o BatchMode=yes sudo tail -n 15 /var/log/atlas-setup.log || true
echo "wait-atlas: sign in at $url (tofu output -json atlas_admin)"
