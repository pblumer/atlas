#!/usr/bin/env bash
# Fills a freshly started Atlas server with what the video is supposed to show.
# An empty instance is a poor subject: the dashboard counts nothing, and the audit
# log says "no access-control changes recorded yet".
#
#   ATLAS_BASE   server address        (default http://127.0.0.1:8080)
#   ATLAS_USER   administrator         (default admin)
#   ATLAS_PASS   their password        (default atlas-demo-2026)
set -euo pipefail
BASE="${ATLAS_BASE:-http://127.0.0.1:8080}"
USER="${ATLAS_USER:-admin}"
PASS="${ATLAS_PASS:-atlas-demo-2026}"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
JAR="$(mktemp)"; trap 'rm -f "$JAR"' EXIT
api() { curl -s -b "$JAR" -c "$JAR" "$@"; }

echo "==> signing in to $BASE"
api -X POST "$BASE/api/v1/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" -o /dev/null -w '    %{http_code}\n'

echo "==> deploying processes"
for f in pruefe-auftrag order-to-cash-live order-fulfillment cart-total order-to-cash; do
  api -X POST "$BASE/api/v1/deployments" -H 'Content-Type: application/xml' \
      --data-binary @"$REPO/examples/$f.bpmn" -o /dev/null -w "    $f %{http_code}\n"
done

echo "==> starting instances"
# Starting an instance addresses the definition by its numeric key, not its process id.
KEYS=$(api "$BASE/api/v1/processes" | python3 -c "
import sys, json
want = {'order-to-cash-live': 7, 'proc_check_order': 5, 'order-to-cash': 3, 'order-fulfillment': 2}
for p in json.load(sys.stdin):
    if p['processId'] in want: print(p['key'], want[p['processId']])
")
while read -r key n; do
  for _ in $(seq "$n"); do
    api -X POST "$BASE/api/v1/processes/$key/instances" -H 'Content-Type: application/json' \
        -d '{"variables":{}}' -o /dev/null
  done
  echo "    definition $key: $n instances"
done <<< "$KEYS"

echo "==> users and groups"
mkuser() { api -X POST "$BASE/api/v1/users" -H 'Content-Type: application/json' -d "$1" -o /dev/null -w "    %{http_code}\n"; }
mkuser '{"username":"m.keller","displayName":"Marc Keller","password":"Demo-Passwort-1","roles":["modeler","operator","user"]}'
mkuser '{"username":"s.arnold","displayName":"Sara Arnold","password":"Demo-Passwort-1","roles":["operator","user"]}'
mkuser '{"username":"t.brunner","displayName":"Tim Brunner","password":"Demo-Passwort-1","roles":["user"]}'
mkuser '{"username":"l.frei","displayName":"Lea Frei","password":"Demo-Passwort-1","roles":["modeler","user"]}'
for g in einkauf buchhaltung lager; do
  api -X POST "$BASE/api/v1/groups" -H 'Content-Type: application/json' -d "{\"name\":\"$g\"}" -o /dev/null
done

echo "==> applications and shares (what the audit log records)"
APPS=$(for n in Order-to-Cash Benutzerverwaltung Beschaffung; do
  api -X POST "$BASE/api/v1/applications" -H 'Content-Type: application/json' \
      -d "{\"name\":\"$n\"}" | python3 -c "import sys,json;print(json.load(sys.stdin)['id'])"
done)
USERS=$(api "$BASE/api/v1/users" | python3 -c "
import sys, json
print(' '.join(u['id'] for u in json.load(sys.stdin) if u['username'] != 'admin'))")
set -- $APPS; APP1=$1; APP2=$2; APP3=$3
set -- $USERS; U1=$1; U2=$2; U3=$3; U4=$4
share() { api -X PUT "$BASE/api/v1/applications/$1/members/$2" -H 'Content-Type: application/json' \
          -d "{\"role\":\"$3\"}" -o /dev/null -w "    share %{http_code}\n"; }
share "$APP1" "$U1" editor; share "$APP1" "$U2" editor
share "$APP1" "$U3" viewer; share "$APP1" "$U4" viewer
share "$APP2" "$U1" viewer; share "$APP3" "$U3" editor
api -X DELETE "$BASE/api/v1/applications/$APP1/members/$U4" -o /dev/null -w "    unshare %{http_code}\n"

echo "==> state"
api "$BASE/api/v1/stats"; echo
