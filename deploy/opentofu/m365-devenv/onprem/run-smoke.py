#!/usr/bin/env python3
"""Deploys onprem/exchange-onprem-smoke.bpmn to the Atlas server of this environment and
runs it once against the on-premises Exchange server, as its administrator (password from
`tofu output -json atlas_admin`, never printed).

    python3 -I onprem/run-smoke.py [--person <upn>]

Run from deploy/opentofu/m365-devenv after onprem/install-worker.sh. The person is a
test user with a mailbox on exch01 (OU=AtlasTest, mailbox enabled by exchange-setup.ps1);
by default the first one. Prints each step the instance takes and the incident it stops
at, if any; exit 0 when the instance completed.
"""
import argparse
import http.cookiejar
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))


def tofu(name):
    return json.loads(subprocess.run(["tofu", "output", "-json", name], capture_output=True, text=True,
                                     check=True, cwd=os.path.dirname(HERE)).stdout)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--person")
    args = ap.parse_args()

    url = tofu("atlas_url")
    admin = tofu("atlas_admin")
    env = tofu("environment_id")
    ex = tofu("exchange_onprem")
    domain = ex["fqdn"].split(".", 1)[1]
    person = args.person
    if not person:
        users = tofu("test_users")
        first = sorted(users)[0] if isinstance(users, dict) else None
        if not first:
            sys.exit("no test user; pass --person")
        person = f"{env}-{first}@{domain}"

    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(method, path, body=None, ctype="application/json"):
        data = body if isinstance(body, bytes) else (json.dumps(body).encode() if body is not None else None)
        req = urllib.request.Request(url + path, data=data, method=method, headers={"Content-Type": ctype})
        try:
            return json.loads(op.open(req).read() or "null")
        except urllib.error.HTTPError as e:
            sys.exit(f"{method} {path}: HTTP {e.code} {e.read().decode()[:400]}")

    call("POST", "/api/v1/auth/login", {"username": admin["username"], "password": admin["password"]})
    dep = call("POST", "/api/v1/deployments", open(os.path.join(HERE, "exchange-onprem-smoke.bpmn"), "rb").read(),
               "application/xml")
    for w in (dep or {}).get("warnings", []) or []:
        print(f"run-smoke: deploy warning: {w}")
    variables = {"name": f"{env}-Smoke OnPrem", "alias": f"{env}-smoke-onprem", "domain": domain, "person": person}
    inst = call("POST", "/api/v1/instances", {"processId": "proc_exchange_onprem_smoke", "variables": variables})
    key = inst.get("instanceKey") or inst.get("key")
    print(f"run-smoke: instance {key}, person {person}")

    seen = 0
    deadline = time.time() + 900
    while time.time() < deadline:
        t = call("GET", f"/api/v1/instances/{key}/timeline")
        steps = t.get("steps", [])
        for s in steps[seen:]:
            print(f"run-smoke:   {s['elementId']}")
        seen = len(steps)
        listed = call("GET", "/api/v1/incidents?state=open") or []
        if isinstance(listed, dict):
            listed = listed.get("items") or listed.get("incidents") or []
        incidents = [i for i in listed if isinstance(i, dict) and i.get("processInstanceKey") == key]
        if incidents:
            for i in incidents:
                print(f"run-smoke: INCIDENT at {i['elementId']}: {i['message'].replace(chr(0), '')[:600]}")
            return 1
        if t.get("state") == "completed":
            print("run-smoke: completed")
            return 0
        time.sleep(5)
    print("run-smoke: not completed within 15 minutes")
    return 1


if __name__ == "__main__":
    sys.exit(main())
