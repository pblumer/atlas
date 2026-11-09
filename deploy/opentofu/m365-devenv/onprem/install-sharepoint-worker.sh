#!/usr/bin/env bash
# Starts the SharePoint Server worker on the farm server (onprem/sharepoint-worker.ps1)
# once the farm is ready, modelled on install-worker.sh for Exchange:
#   1. builds atlas.exe for Windows from the repository this script lives in,
#   2. offers it for 30 minutes from the Atlas VM inside the virtual network only,
#   3. mints a worker-scoped API token (7 days) in the Atlas server as its administrator,
#   4. runs sharepoint-worker.ps1 on the server through Azure's managed run command, with
#      the token and the service account's password as protected parameters.
# Nothing secret is printed. Run from deploy/opentofu/m365-devenv.
#
# Reads `tofu output -json sharepoint_server` (vm, resource_group, fqdn,
# web_application_url, runbook_account). The account's password comes from the output
# sharepoint_runbook_password or the variable SHAREPOINT_WORKER_PASSWORD. Not run yet:
# see the README, section SharePoint Server.
set -euo pipefail

cd "$(dirname "$0")/.."
repo="${ATLAS_REPO:-$(git rev-parse --show-toplevel)}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

info="$(tofu output -json sharepoint_server)"
get() { python3 -I -c 'import json,sys; print(json.loads(sys.argv[1]).get(sys.argv[2]) or "")' "$info" "$1"; }
[[ "$(get enabled)" == True ]] || { echo "install-sharepoint-worker: enable_sharepoint_server is off; nothing to do" >&2; exit 1; }
rg="$(get resource_group)"; vm="$(get vm)"; account="$(get runbook_account)"; web="$(get web_application_url)"

password="${SHAREPOINT_WORKER_PASSWORD:-$(tofu output -raw sharepoint_runbook_password 2>/dev/null || true)}"
[[ -n "$password" ]] || { echo "install-sharepoint-worker: no password for $account: set SHAREPOINT_WORKER_PASSWORD or add the output sharepoint_runbook_password" >&2; exit 1; }

url="$(tofu output -raw atlas_url)"
ssh_cmd="$(tofu output -raw atlas_ssh)"
atlas_vm="${rg%-rg}-atlas"
atlas_ip="$(az vm list-ip-addresses -g "$rg" -n "$atlas_vm" --query '[0].virtualMachine.network.privateIpAddresses[0]' -o tsv)"

echo "install-sharepoint-worker: building atlas.exe"
(cd "$repo" && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o "$work/atlas.exe" ./cmd/atlas)

echo "install-sharepoint-worker: offering it on http://$atlas_ip:8099 for 30 minutes"
key="$(awk '{print $3}' <<<"$ssh_cmd")"; host="$(awk '{print $4}' <<<"$ssh_cmd")"
scp -q -i "$key" "$work/atlas.exe" "$host:/tmp/atlas.exe"
$ssh_cmd "mkdir -p /tmp/atlas-dl && mv -f /tmp/atlas.exe /tmp/atlas-dl/ && { [ -f /tmp/atlas-dl/pid ] && kill \$(cat /tmp/atlas-dl/pid) 2>/dev/null; true; } && cd /tmp/atlas-dl && { setsid nohup timeout 1800 python3 -m http.server 8099 --bind $atlas_ip >/dev/null 2>&1 < /dev/null & echo \$! > /tmp/atlas-dl/pid; } && sleep 2 && curl -sf -o /dev/null -I http://$atlas_ip:8099/atlas.exe" \
  || { echo "install-sharepoint-worker: the download server on the Atlas VM does not answer" >&2; exit 1; }

echo "install-sharepoint-worker: minting a worker token"
token="$(python3 -I - "$url" <<'PY'
import json, subprocess, sys, urllib.request, http.cookiejar
url = sys.argv[1]
adm = json.loads(subprocess.run(["tofu", "output", "-json", "atlas_admin"], capture_output=True, text=True, check=True).stdout)
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(m, p, b=None):
    r = urllib.request.Request(url + p, data=json.dumps(b).encode() if b is not None else None, method=m,
                               headers={"Content-Type": "application/json"})
    return json.loads(op.open(r).read() or "null")
call("POST", "/api/v1/auth/login", {"username": adm["username"], "password": adm["password"]})
t = call("POST", "/api/v1/api-tokens", {"name": "sharepoint-server-worker", "scope": "worker", "expiresInDays": 7})
print(t.get("secret") or t.get("token"))
PY
)"

echo "install-sharepoint-worker: starting the worker on $vm"
az vm run-command create -g "$rg" --vm-name "$vm" --name atlas-sharepoint-worker \
  --script "@onprem/sharepoint-worker.ps1" --async-execution false --timeout-in-seconds 900 \
  --parameters "AtlasUrl=$url" "BinaryUrl=http://$atlas_ip:8099/atlas.exe" "ServiceAccount=$account" "WebApplicationUrl=$web" \
  --protected-parameters "Token=$token" "ServicePassword=$password" \
  --output none
result="$(az vm run-command show -g "$rg" --vm-name "$vm" --name atlas-sharepoint-worker --instance-view \
  --query 'instanceView.{state:executionState, output:output, error:error}' -o json)"
echo "$result"
az vm run-command delete -g "$rg" --vm-name "$vm" --name atlas-sharepoint-worker --yes
[[ "$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["state"])' "$result")" == Succeeded ]] \
  || { echo "install-sharepoint-worker: the run command failed" >&2; exit 1; }
echo "install-sharepoint-worker: done"
