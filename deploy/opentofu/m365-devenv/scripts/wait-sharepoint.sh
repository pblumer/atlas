#!/usr/bin/env bash
# Follows the unattended SharePoint installation (scripts/sharepoint-setup.ps1) on the VM
# through Azure's run command, which needs no inbound port: prints the phase markers
# and stops when C:\AtlasSetup\sharepoint.done exists, or with the last log lines when
# a phase wrote sharepoint-failed.txt.
#
#   ./scripts/wait-sharepoint.sh          # WAIT_MINUTES=240 by default
set -euo pipefail

cd "$(dirname "$0")/.."
info="$(tofu output -json sharepoint_server)"
enabled="$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["enabled"])' "$info")"
[[ "$enabled" == True ]] || { echo "wait-sharepoint: no SharePoint server in this environment (enable_sharepoint_server)" >&2; exit 1; }
rg="$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["resource_group"])' "$info")"
vm="$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["vm"])' "$info")"
timeout_min="${WAIT_MINUTES:-240}"
deadline=$(( $(date +%s) + timeout_min * 60 ))

probe='$r="C:\AtlasSetup"; if (Test-Path "$r\sharepoint-failed.txt") { "FAILED: " + (Get-Content "$r\sharepoint-failed.txt"); Get-ChildItem "$r\sharepoint-phase*.log" | Sort-Object LastWriteTime | Select-Object -Last 1 | Get-Content -Tail 25 } elseif (Test-Path "$r\sharepoint.done") { "DONE" } else { "phases done: " + ((Get-ChildItem "$r\sharepoint-phase*.done" -ErrorAction SilentlyContinue | ForEach-Object { $_.BaseName -replace "sharepoint-phase","" -replace "\.done","" }) -join ",") }'

echo "wait-sharepoint: following $vm (up to $timeout_min minutes)"
last=""
while :; do
  out="$(az vm run-command invoke -g "$rg" -n "$vm" --command-id RunPowerShellScript --scripts "$probe" \
        --query 'value[0].message' -o tsv 2>/dev/null || echo "VM not reachable yet")"
  [[ "$out" != "$last" ]] && { echo "wait-sharepoint: $out"; last="$out"; }
  [[ "$out" == DONE* ]] && { echo "wait-sharepoint: SharePoint Server is ready"; exit 0; }
  [[ "$out" == FAILED* ]] && exit 1
  (( $(date +%s) < deadline )) || { echo "wait-sharepoint: not ready in time" >&2; exit 1; }
  sleep 120
done
