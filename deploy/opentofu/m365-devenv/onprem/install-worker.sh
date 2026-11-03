#!/usr/bin/env bash
# Starts the on-premises Exchange worker on exch01 (onprem/exchange-worker.ps1) once the
# Exchange server is ready (scripts/wait-exchange.sh):
#   1. builds atlas.exe for Windows from the repository this script lives in,
#   2. offers it for ten minutes from the Atlas VM inside the virtual network only,
#   3. mints a worker-scoped API token (7 days) in the Atlas server as its administrator,
#   4. runs exchange-worker.ps1 on exch01 through Azure's managed run command, with the
#      token and the service account's password as protected parameters.
# Nothing secret is printed. Run from deploy/opentofu/m365-devenv.
set -euo pipefail

cd "$(dirname "$0")/.."
repo="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

info="$(tofu output -json exchange_onprem)"
get() { python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])[sys.argv[2]])' "$info" "$1"; }
rg="$(get resource_group)"; vm="$(get vm)"; fqdn="$(get fqdn)"; account="$(get service_account)"
url="$(tofu output -raw atlas_url)"
ssh_cmd="$(tofu output -raw atlas_ssh)"
atlas_vm="${rg%-rg}-atlas"
atlas_ip="$(az vm list-ip-addresses -g "$rg" -n "$atlas_vm" --query '[0].virtualMachine.network.privateIpAddresses[0]' -o tsv)"

echo "install-worker: building atlas.exe"
(cd "$repo" && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o "$work/atlas.exe" ./cmd/atlas)

echo "install-worker: offering it on http://$atlas_ip:8099 for ten minutes"
key="$(awk '{print $3}' <<<"$ssh_cmd")"; host="$(awk '{print $4}' <<<"$ssh_cmd")"
scp -q -i "$key" "$work/atlas.exe" "$host:/tmp/atlas.exe"
$ssh_cmd "mkdir -p /tmp/atlas-dl && mv /tmp/atlas.exe /tmp/atlas-dl/ && (cd /tmp/atlas-dl && nohup timeout 600 python3 -m http.server 8099 --bind $atlas_ip >/dev/null 2>&1 &)"

echo "install-worker: minting a worker token"
token="$(python3 -I - "$url" <<'EOF'
import json, subprocess, sys, urllib.request, http.cookiejar
url = sys.argv[1]
adm = json.loads(subprocess.run(["tofu", "output", "-json", "atlas_admin"], capture_output=True, text=True, check=True).stdout)
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(m, p, b=None):
    r = urllib.request.Request(url + p, data=json.dumps(b).encode() if b is not None else None, method=m,
                               headers={"Content-Type": "application/json"})
    return json.loads(op.open(r).read() or "null")
call("POST", "/api/v1/auth/login", {"username": adm["username"], "password": adm["password"]})
t = call("POST", "/api/v1/api-tokens", {"name": "exchange-onprem-worker", "scope": "worker", "expiresInDays": 7})
print(t.get("secret") or t.get("token"))
EOF
)"

echo "install-worker: starting the worker on $vm"
az vm run-command create -g "$rg" --vm-name "$vm" --name atlas-exchange-worker \
  --script "@onprem/exchange-worker.ps1" --async-execution false --timeout-in-seconds 900 \
  --parameters "AtlasUrl=$url" "BinaryUrl=http://$atlas_ip:8099/atlas.exe" "ServiceAccount=$account" "ExchangeFqdn=$fqdn" \
  --protected-parameters "Token=$token" "ServicePassword=$(tofu output -raw exchange_service_password)" \
  --output none
az vm run-command show -g "$rg" --vm-name "$vm" --name atlas-exchange-worker --instance-view \
  --query 'instanceView.{state:executionState, output:output, error:error}' -o json
az vm run-command delete -g "$rg" --vm-name "$vm" --name atlas-exchange-worker --yes
echo "install-worker: done"
