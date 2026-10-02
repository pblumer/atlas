#!/usr/bin/env bash
# Run the Postman collection (postman/) end to end against a throwaway Atlas.
#
# The collection is published for people to import, and every request in it
# asserts its status and the shape of its answer. Those assertions only say
# something when somebody runs them: this script builds the server, starts it on an
# empty data directory with a known admin password, runs the whole collection with
# Newman in collection order, and stops the server again. A green run means every
# request, chained id and saved example in the kit still matches the live API.
#
# The Go tests in api/postman_internal_test.go guard the collection statically
# (every request names a served, non-deprecated route; every request is documented
# and asserts something). This is the half they cannot do: talking to a server.
#
# Usage: scripts/postman-smoke.sh
#   ATLAS_SMOKE_ADDR   listen address of the throwaway server (default 127.0.0.1:18080)
#   NEWMAN             Newman command (default: npx --yes newman@6.2.2)
#
# Needs Go and Node.js. Newman is fetched by npx on first use.
set -euo pipefail

cd "$(dirname "$0")/.."

addr="${ATLAS_SMOKE_ADDR:-127.0.0.1:18080}"
base="http://${addr}"
newman="${NEWMAN:-npx --yes newman@6.2.2}"

work="$(mktemp -d)"
server_pid=""
cleanup() {
	if [ -n "${server_pid}" ] && kill -0 "${server_pid}" 2>/dev/null; then
		kill "${server_pid}"
		wait "${server_pid}" 2>/dev/null || true
	fi
	rm -rf "${work}"
}
trap cleanup EXIT

if curl -s -o /dev/null "${base}/healthz"; then
	echo "something is already listening on ${addr}; set ATLAS_SMOKE_ADDR to a free address" >&2
	exit 1
fi

echo "building atlas…"
go build -o "${work}/atlas" ./cmd/atlas

# A fresh data directory seeds exactly one admin from these two variables
# (ADR-0195), which is the account the collection signs in with.
# od reads exactly what it prints; `tr </dev/urandom | head` would die of SIGPIPE
# under pipefail.
password="$(od -An -N18 -tx1 /dev/urandom | tr -d ' \n')"
ATLAS_ADMIN_USERNAME=admin ATLAS_ADMIN_PASSWORD="${password}" \
	"${work}/atlas" serve --addr "${addr}" --data-dir "${work}/data" >"${work}/server.log" 2>&1 &
server_pid=$!

echo "waiting for ${base}/readyz…"
for _ in $(seq 1 120); do
	if curl -sf -o /dev/null "${base}/readyz"; then
		break
	fi
	if ! kill -0 "${server_pid}" 2>/dev/null; then
		echo "atlas exited before it was ready:" >&2
		cat "${work}/server.log" >&2
		exit 1
	fi
	sleep 0.5
done
curl -sf -o /dev/null "${base}/readyz" || {
	echo "atlas did not become ready within 60s:" >&2
	tail -n 50 "${work}/server.log" >&2
	exit 1
}

# shellcheck disable=SC2086 # $newman is a command line on purpose.
${newman} run postman/Atlas.postman_collection.json \
	--environment postman/Atlas.postman_environment.json \
	--env-var "baseUrl=${base}" \
	--env-var "username=admin" \
	--env-var "password=${password}"
