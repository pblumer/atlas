#!/usr/bin/env bash
# Sets up the database the MS SQL worker is tested against, on the SharePoint VM's SQL
# Server, and the configured Worker hr-db in Atlas that reaches it (ADR-0173, ADR-0188):
#   1. generates a new password for the SQL login atlas_hr,
#   2. runs onprem/mssql-testdb.ps1 on the VM through Azure's managed run command (mixed
#      mode, TCP 1433 from the Atlas subnet only, AtlasTest_HR with dbo.personen reset to
#      the test data, the login with read and write on that database only),
#   3. creates the configured Worker hr-db (kind mssql) in Atlas with the connection
#      string sealed in the vault, or re-keys the vault entry of the one that exists,
#   4. checks the connection through Atlas, and that a wrong password is refused.
# Nothing secret is printed. Re-running is safe. Run from deploy/opentofu/m365-devenv
# after scripts/wait-sharepoint.sh. It creates nothing in Azure or in the tenant.
set -euo pipefail

cd "$(dirname "$0")/.."

info="$(tofu output -json sharepoint_server)"
get() { python3 -I -c 'import json,sys; print(json.loads(sys.argv[1]).get(sys.argv[2]) or "")' "$info" "$1"; }
[[ "$(get enabled)" == True ]] || { echo "mssql-testdb: enable_sharepoint_server is off; no SQL Server here" >&2; exit 1; }
rg="$(get resource_group)"; vm="$(get vm)"
vnet="$(az network vnet list -g "$rg" --query '[0].name' -o tsv)"
allow="$(az network vnet subnet show -g "$rg" --vnet-name "$vnet" -n app --query addressPrefix -o tsv)"
sql_ip="$(az vm list-ip-addresses -g "$rg" -n "$vm" --query '[0].virtualMachine.network.privateIpAddresses[0]' -o tsv)"
netbios="$(echo 'var.ad_netbios_name' | tofu console | tr -d '"')"

admin_json="$(tofu output -json ad_admin)"
domain_user="$netbios\\$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["username"])' "$admin_json")"
domain_pass="$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["password"])' "$admin_json")"
login_pass="$(python3 -I -c 'import secrets,string; a=string.ascii_letters+string.digits; print("".join(secrets.choice(a) for _ in range(28)) + "a7Q")')"

echo "mssql-testdb: preparing AtlasTest_HR on $vm ($sql_ip), TCP 1433 from $allow"
az vm run-command create -g "$rg" --vm-name "$vm" --name atlas-mssql-testdb \
  --script "@onprem/mssql-testdb.ps1" --async-execution false --timeout-in-seconds 900 \
  --parameters "AllowFrom=$allow" "DomainUser=$domain_user" \
  --protected-parameters "DomainPassword=$domain_pass" "LoginPassword=$login_pass" \
  --output none
result="$(az vm run-command show -g "$rg" --vm-name "$vm" --name atlas-mssql-testdb --instance-view \
  --query 'instanceView.{state:executionState, output:output, error:error}' -o json)"
az vm run-command delete -g "$rg" --vm-name "$vm" --name atlas-mssql-testdb --yes
python3 -I -c 'import json,sys; r=json.loads(sys.argv[1]); print(r.get("output") or ""); print(r.get("error") or "", file=sys.stderr)' "$result"
[[ "$(python3 -I -c 'import json,sys; print(json.loads(sys.argv[1])["state"])' "$result")" == Succeeded ]] \
  || { echo "mssql-testdb: the run command failed" >&2; exit 1; }

echo "mssql-testdb: configured Worker hr-db in Atlas"
DSN="sqlserver://atlas_hr:$login_pass@$sql_ip:1433?database=AtlasTest_HR&encrypt=true&TrustServerCertificate=true" \
WRONG="sqlserver://atlas_hr:wrong-$RANDOM@$sql_ip:1433?database=AtlasTest_HR&encrypt=true&TrustServerCertificate=true" \
python3 -I - "$(tofu output -raw atlas_url)" "$(tofu output -json atlas_admin)" <<'PY'
import json, os, sys, urllib.error, urllib.parse, urllib.request, http.cookiejar
url, adm = sys.argv[1], json.loads(sys.argv[2])
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(m, p, b=None):
    r = urllib.request.Request(url + p, data=json.dumps(b).encode() if b is not None else None, method=m,
                               headers={"Content-Type": "application/json"})
    try:
        return json.loads(op.open(r, timeout=120).read() or "null")
    except urllib.error.HTTPError as e:
        sys.exit(f"mssql-testdb: {m} {p}: HTTP {e.code} {e.read().decode(errors='replace')[:300]}")
call("POST", "/api/v1/auth/login", {"username": adm["username"], "password": adm["password"]})
listing = call("GET", "/api/v1/configured-workers")
rows = listing.get("items", listing.get("connectors", [])) if isinstance(listing, dict) else listing
hit = [c for c in rows if c.get("name") == "hr-db"]
if hit:
    ref = hit[0].get("credentialsRef") or f"sql/{hit[0]['id']}/dsn"
    call("PUT", "/api/v1/secrets/" + urllib.parse.quote(ref, safe=""), {"value": os.environ["DSN"]})
    print(f"mssql-testdb: hr-db exists ({hit[0]['id']}); its connection string is re-keyed")
else:
    c = call("POST", "/api/v1/configured-workers", {"name": "hr-db", "kind": "mssql", "connectionString": os.environ["DSN"]})
    ref = c.get("credentialsRef")
    print(f"mssql-testdb: hr-db created ({c.get('id')}), endpoint {c.get('endpoint')}")
ok = call("POST", "/api/v1/connectors/test", {"kind": "mssql", "credentialsRef": ref})
print(f"mssql-testdb: check with the stored connection string: ok={ok.get('ok')} {ok.get('message') or ok.get('detail') or ''}")
bad = call("POST", "/api/v1/connectors/test", {"kind": "mssql", "connectionString": os.environ["WRONG"]})
print(f"mssql-testdb: check with a wrong password: ok={bad.get('ok')} (expected False) {bad.get('message') or bad.get('detail') or ''}"[:300])
if not ok.get("ok") or bad.get("ok"):
    sys.exit("mssql-testdb: the checks did not come out as expected")
PY
echo "mssql-testdb: done"
