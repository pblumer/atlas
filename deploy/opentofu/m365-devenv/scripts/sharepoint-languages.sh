#!/usr/bin/env bash
# Installs the German, French and Italian language packs on the SharePoint Server farm
# (onprem/sharepoint-languages.ps1) and follows the installation to its end. The order form
# of the SharePoint Server team site offers 1031, 1036, 1040 and 1033; without the packs
# create-site answers "The language is not supported on the server."
#
#   ./scripts/sharepoint-languages.sh      # after scripts/wait-sharepoint.sh; WAIT_MINUTES=120
#
# The domain administrator's password goes to the VM as a protected parameter and is not
# printed. The server restarts at the end. Nothing is created in Azure or in the tenant.
set -euo pipefail

cd "$(dirname "$0")/.."
info="$(tofu output -json sharepoint_server)"
get() { python3 -I -c 'import json,sys; print(json.loads(sys.argv[1]).get(sys.argv[2]) or "")' "$info" "$1"; }
[[ "$(get enabled)" == True ]] || { echo "sharepoint-languages: enable_sharepoint_server is off" >&2; exit 1; }
rg="$(get resource_group)"; vm="$(get vm)"
netbios="$(echo 'var.ad_netbios_name' | tofu console | tr -d '"')"
admin_json="$(tofu output -json ad_admin)"
domain_user="$netbios\\$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["username"])' "$admin_json")"
domain_pass="$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["password"])' "$admin_json")"

echo "sharepoint-languages: starting on $vm"
az vm run-command create -g "$rg" --vm-name "$vm" --name atlas-sharepoint-languages \
  --script "@onprem/sharepoint-languages.ps1" --async-execution false --timeout-in-seconds 600 \
  --parameters "DomainUser=$domain_user" --protected-parameters "DomainPassword=$domain_pass" --output none
az vm run-command show -g "$rg" --vm-name "$vm" --name atlas-sharepoint-languages --instance-view \
  --query 'instanceView.output' -o tsv
az vm run-command delete -g "$rg" --vm-name "$vm" --name atlas-sharepoint-languages --yes

probe='$r="C:\AtlasSetup"; if (Test-Path "$r\sharepoint-languages-failed.txt") { "FAILED: " + (Get-Content "$r\sharepoint-languages-failed.txt"); Get-Content "$r\sharepoint-languages.log" -Tail 25 } elseif (Test-Path "$r\sharepoint-languages.done") { "DONE " + (Get-Content "$r\sharepoint-languages.done") } else { "running: " + ((Get-Content "$r\sharepoint-languages.log" -ErrorAction SilentlyContinue | Where-Object { $_ -match "installed|^> " }) -join " | ") }'
deadline=$(( $(date +%s) + ${WAIT_MINUTES:-120} * 60 ))
last=""
while :; do
  sleep 60
  out="$(az vm run-command invoke -g "$rg" -n "$vm" --command-id RunPowerShellScript --scripts "$probe" \
        --query 'value[0].message' -o tsv 2>/dev/null || echo "VM not reachable (restarting?)")"
  [[ "$out" != "$last" ]] && { echo "sharepoint-languages: $out" | cut -c1-600; last="$out"; }
  if [[ "$out" == DONE* ]]; then
    az vm run-command invoke -g "$rg" -n "$vm" --command-id RunPowerShellScript \
      --scripts 'Unregister-ScheduledTask -TaskName AtlasSharePointLanguages -Confirm:$false -ErrorAction SilentlyContinue' -o none || true
    echo "sharepoint-languages: done"; exit 0
  fi
  [[ "$out" == FAILED* ]] && exit 1
  (( $(date +%s) < deadline )) || { echo "sharepoint-languages: not done in time" >&2; exit 1; }
done
