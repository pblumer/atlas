#!/usr/bin/env bash
# Follows the unattended Exchange installation (scripts/exchange-setup.ps1) on the VM
# through Azure's run command, which needs no inbound port: prints the phase markers
# and stops when C:\AtlasSetup\exchange.done exists, or with the last log lines when
# a phase wrote exchange-failed.txt. The whole installation takes one to two hours.
#
#   ./scripts/wait-exchange.sh          # WAIT_MINUTES=150 by default
set -euo pipefail

cd "$(dirname "$0")/.."
info="$(tofu output -json exchange_onprem)"
rg="$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["resource_group"])' "$info")"
vm="$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["vm"])' "$info")"
timeout_min="${WAIT_MINUTES:-150}"
deadline=$(( $(date +%s) + timeout_min * 60 ))

probe='$r="C:\AtlasSetup"; if (Test-Path "$r\exchange-failed.txt") { "FAILED: " + (Get-Content "$r\exchange-failed.txt"); Get-ChildItem "$r\exchange-phase*.log" | Sort-Object LastWriteTime | Select-Object -Last 1 | Get-Content -Tail 25 } elseif (Test-Path "$r\exchange.done") { "DONE" } else { "phases done: " + ((Get-ChildItem "$r\exchange-phase*.done" -ErrorAction SilentlyContinue | ForEach-Object { $_.BaseName -replace "exchange-phase","" -replace "\.done","" }) -join ",") }'

echo "wait-exchange: following $vm (up to $timeout_min minutes)"
last=""
while :; do
  out="$(az vm run-command invoke -g "$rg" -n "$vm" --command-id RunPowerShellScript --scripts "$probe" \
        --query 'value[0].message' -o tsv 2>/dev/null || echo "VM not reachable yet")"
  [[ "$out" != "$last" ]] && { echo "wait-exchange: $out"; last="$out"; }
  [[ "$out" == DONE* ]] && { echo "wait-exchange: Exchange Server is ready"; exit 0; }
  [[ "$out" == FAILED* ]] && exit 1
  (( $(date +%s) < deadline )) || { echo "wait-exchange: not ready in time" >&2; exit 1; }
  sleep 120
done
