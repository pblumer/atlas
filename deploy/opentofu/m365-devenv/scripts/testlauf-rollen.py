#!/usr/bin/env python3
"""Separates the test persons' roles for the catalogue test run (TESTLAUF-KATALOG.md).

    python3 scripts/testlauf-rollen.py [--dry-run] [--cafile out/caddy-root.crt]

The installers put every test person who has signed in into every group they create, so
anybody may approve anything and an approval path proves nothing. This script sets the
Atlas group memberships of the four test persons to the roles of the test run:

  Anna Muster     employee who orders: the catalogues' audiences
  Beat Beispiel   employee, recipient of orders placed for him: the M365 audiences
  Carla Probe     every specialist office: facility, telephony, administration, security,
                  AD, identity, mainframe and licence management
  Max Vorgesetzt  manager of the three (Entra manager): the M365 audiences, Lizenz-Chef

Only these four persons' memberships change; the administrator and anybody else keep
theirs. A group that does not exist is reported and skipped. Re-run it after every run of
an installer. It changes nothing in Azure or in the tenant: Atlas groups only.
"""
import argparse
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
m365 = __import__("install-m365-dienste")

AUDIENCES_M365 = ["M365-Mitarbeitende", "M365-UCC"]
# Audiences of Informationstransfer (Identity Management) and of the licence contracts
# (Lizenz-Integrations-Manager): Anna orders there, so she must see them.
AUDIENCES_ORDERER = ["Identity Management", "Lizenz-Integrations-Manager"]
OFFICES = ["M365-Facility", "M365-Telefonie", "M365-Administration", "Informationssicherheit",
           "AD-Administration", "Identity Management", "Mainframe", "Lizenzmanagement",
           "Lizenz-Tenantverantwortung", "Lizenz-Finanzen", "Lizenz-Integrations-Manager"]

ROLES = {
    "anna.muster": AUDIENCES_M365 + AUDIENCES_ORDERER,
    "beat.beispiel": AUDIENCES_M365,
    "carla.probe": AUDIENCES_M365 + OFFICES,
    "manager": AUDIENCES_M365 + ["Lizenz-Chef"],
}

def log(msg):
    print(f"testlauf-rollen: {msg}", flush=True)


MANAGED = sorted(set(AUDIENCES_M365 + AUDIENCES_ORDERER + OFFICES + ["Lizenz-Chef"]))


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--tofu-dir", default=os.path.dirname(HERE))
    ap.add_argument("--cafile", help="Caddy's root certificate (atlas_tls = internal)")
    ap.add_argument("--dry-run", action="store_true", help="say what would change, change nothing")
    args = ap.parse_args()
    tofu_dir = os.path.abspath(args.tofu_dir)

    url = m365.tofu_output(tofu_dir, "atlas_url")
    admin = m365.tofu_output(tofu_dir, "atlas_admin")
    atlas = m365.Atlas(url, args.cafile)
    atlas.call("POST", "/api/v1/auth/login", {"username": admin["username"], "password": admin["password"]})

    _, users = atlas.call("GET", "/api/v1/users")
    users = m365.items(users, "users")
    person = {}
    for key in ROLES:
        hit = [u for u in users if u.get("username", "").endswith("-" + key)]
        if len(hit) != 1:
            sys.exit(f"test person {key}: {len(hit)} Atlas accounts match; sign in once, or check the names")
        person[key] = hit[0]["id"]

    _, groups = atlas.call("GET", "/api/v1/groups")
    groups = {g["name"]: g for g in m365.items(groups, "groups")}
    changes = 0
    for name in MANAGED:
        g = groups.get(name)
        if not g:
            log(f"group {name} does not exist; skipped")
            continue
        members = set(g.get("members") or [])
        for key, uid in person.items():
            want = name in ROLES[key]
            if want and uid not in members:
                verb, method = "joins", "PUT"
            elif not want and uid in members:
                verb, method = "leaves", "DELETE"
            else:
                continue
            log(f"{key} {verb} {name}" + (" (dry run)" if args.dry_run else ""))
            changes += 1
            if not args.dry_run:
                atlas.call(method, f"/api/v1/groups/{g['id']}/members/{uid}")
    log(f"{changes} membership(s) {'to change' if args.dry_run else 'changed'}")


if __name__ == "__main__":
    main()
