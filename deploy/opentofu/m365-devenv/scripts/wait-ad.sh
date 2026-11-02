#!/usr/bin/env bash
# Waits until the test domain controller answers LDAPS with the certificate OpenTofu
# wrote to out/ad-ldaps.pem, and - when ldapwhoami is installed - until the worker's
# bind account can bind. Promotion, reboot and phase 2 take some 10-20 minutes.
#
#   ./scripts/wait-ad.sh            # reads host and CA from tofu output
#   ./scripts/wait-ad.sh <fqdn> <ca.pem>
set -euo pipefail

cd "$(dirname "$0")/.."
host="${1:-$(tofu output -raw ad_public_fqdn)}"
ca="${2:-$(tofu output -raw ad_ldaps_certificate_file)}"
timeout_min="${WAIT_MINUTES:-30}"

command -v openssl >/dev/null || { echo "wait-ad: openssl is required" >&2; exit 1; }

deadline=$(( $(date +%s) + timeout_min * 60 ))
echo "wait-ad: waiting for ldaps://$host:636 (up to $timeout_min minutes)"
until echo | openssl s_client -connect "$host:636" -servername "$host" \
        -CAfile "$ca" -verify_return_error -verify_hostname "$host" >/dev/null 2>&1; do
  (( $(date +%s) < deadline )) || { echo "wait-ad: LDAPS did not come up in time" >&2; exit 1; }
  sleep 20
done
echo "wait-ad: LDAPS answers with the expected certificate"

if command -v ldapwhoami >/dev/null; then
  bind_dn="$(tofu output -raw ad_bind_dn)"
  pw="$(tofu output -raw ad_bind_password)"
  until LDAPTLS_CACERT="$ca" ldapwhoami -x -H "ldaps://$host:636" -D "$bind_dn" -w "$pw" >/dev/null 2>&1; do
    (( $(date +%s) < deadline )) || { echo "wait-ad: the bind account cannot bind yet (phase 2 not done?)" >&2; exit 1; }
    sleep 20
  done
  echo "wait-ad: $bind_dn binds - the domain controller is ready"
else
  echo "wait-ad: ldapwhoami not installed; bind not checked (phase 2 may still be running)"
fi
