#!/usr/bin/env python3
"""Tests the MS SQL worker against AtlasTest_HR: deploys examples/mssql-eintrittsmeldung.bpmn
and onprem/mssql-smoke.bpmn to the Atlas server of this environment and runs one instance
per case, as its administrator (password from `tofu output -json atlas_admin`, never
printed).

    python3 -I onprem/run-mssql-smoke.py

Run from deploy/opentofu/m365-devenv after scripts/mssql-testdb.sh, which resets the
table: the example marks cbr as active, so a second run without the reset still passes
(the UPDATE changes the row again) but reads aktiv = 1 before it. A case that expects a
failed job passes when the instance stops at an incident on that task; the instance is
then cancelled. Exit 0 when every case came out as expected.
"""
import http.cookiejar
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", "..", "..", ".."))


def tofu(name):
    return json.loads(subprocess.run(["tofu", "output", "-json", name], capture_output=True, text=True,
                                     check=True, cwd=os.path.dirname(HERE)).stdout)


# (case, process, start variables, expectation): "completed" with a check on the variables,
# or "incident" at the named task.
CASES = [
    ("Beispiel: Eintritt vermerkt", "mssql-eintrittsmeldung", {"kuerzel": "cbr"},
     ("completed", lambda v: v.get("vermerkt") == 1 and (v.get("person") or {}).get("abteilung") == "GL")),
    ("Beispiel: nicht in HR", "mssql-eintrittsmeldung", {"kuerzel": "zzz"},
     ("completed", lambda v: v.get("person") is None and "vermerkt" not in v)),
    ("query mit maxRows", "proc_mssql_smoke", {"fall": "liste"},
     ("completed", lambda v: [r.get("kuerzel") for r in v.get("rows") or []] == ["lbe", "nfr", "sme"])),
    ("query-one ohne Treffer", "proc_mssql_smoke", {"fall": "keiner"},
     ("completed", lambda v: "person" in v and v["person"] is None)),
    ("query-one mit benannter Bindung", "proc_mssql_smoke", {"fall": "benannt"},
     ("completed", lambda v: (v.get("person") or {}).get("kuerzel") == "mhu")),
    ("query-one mit zwei Treffern", "proc_mssql_smoke", {"fall": "doppelt"}, ("incident", "q_doppelt")),
    ("query ueber maxRows", "proc_mssql_smoke", {"fall": "zuviele"}, ("incident", "q_zuviele")),
]


def main():
    url = tofu("atlas_url")
    admin = tofu("atlas_admin")
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(method, path, body=None, ctype="application/json"):
        data = body if isinstance(body, bytes) else (json.dumps(body).encode() if body is not None else None)
        req = urllib.request.Request(url + path, data=data, method=method, headers={"Content-Type": ctype})
        try:
            return json.loads(op.open(req, timeout=120).read() or "null")
        except urllib.error.HTTPError as e:
            sys.exit(f"{method} {path}: HTTP {e.code} {e.read().decode()[:400]}")

    def variables(key):
        v = call("GET", f"/api/v1/instances/{key}/variables")
        if isinstance(v, dict) and isinstance(v.get("variables"), (dict, list)):
            v = v["variables"]
        if isinstance(v, list):
            v = {x.get("name"): x.get("value") for x in v if isinstance(x, dict)}
        return {k: (json.loads(x) if isinstance(x, str) and x[:1] in "[{" else x) for k, x in (v or {}).items()}

    call("POST", "/api/v1/auth/login", {"username": admin["username"], "password": admin["password"]})
    for path in (os.path.join(REPO, "examples", "mssql-eintrittsmeldung.bpmn"), os.path.join(HERE, "mssql-smoke.bpmn")):
        dep = call("POST", "/api/v1/deployments", open(path, "rb").read(), "application/xml")
        for w in (dep or {}).get("warnings", []) or []:
            print(f"run-mssql-smoke: deploy warning ({os.path.basename(path)}): {w}")

    failed = 0
    for name, process, start, (want, check) in CASES:
        inst = call("POST", "/api/v1/instances", {"processId": process, "variables": start})
        key = inst.get("instanceKey") or inst.get("key")
        outcome, detail = "timeout", ""
        deadline = time.time() + 300
        while time.time() < deadline:
            listed = call("GET", "/api/v1/incidents?state=open") or []
            if isinstance(listed, dict):
                listed = listed.get("items") or listed.get("incidents") or []
            inc = [i for i in listed if isinstance(i, dict) and i.get("processInstanceKey") == key]
            if inc:
                outcome, detail = "incident", f"{inc[0]['elementId']}: {inc[0]['message'].replace(chr(0), '')[:200]}"
                break
            if call("GET", f"/api/v1/instances/{key}/timeline").get("state") == "completed":
                outcome = "completed"
                break
            time.sleep(3)
        if want == "completed":
            ok = outcome == "completed" and check(variables(key))
            if outcome == "completed" and not ok:
                detail = json.dumps({k: v for k, v in variables(key).items() if k not in start}, ensure_ascii=False)[:300]
        else:
            ok = outcome == "incident" and detail.startswith(check + ":")
            if outcome == "incident":
                call("DELETE", f"/api/v1/instances/{key}")
        failed += not ok
        print(f"run-mssql-smoke: {'ok  ' if ok else 'FAIL'} {name}: {outcome} {detail}".rstrip())
    print(f"run-mssql-smoke: {len(CASES) - failed} of {len(CASES)} cases as expected")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
