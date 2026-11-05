#!/usr/bin/env python3
"""Installs examples/lizenzmanagement into the Atlas server of this environment.

    python3 scripts/install-lizenzmanagement.py --cafile out/caddy-root.crt

Run from deploy/opentofu/m365-devenv after scripts/install-m365-dienste.py (the licence order
of m365-dienste calls the quota check of this package). It

  1. signs in as the local administrator (password from `tofu output -json atlas_admin`,
     never printed);
  2. creates the Atlas groups the package addresses (Lizenz-Tenantverantwortung, Lizenz-Chef,
     Lizenzmanagement, Lizenz-Finanzen) and the audience of the customers' catalogue
     (Lizenz-Integrations-Manager); test persons who have signed in join them, as in
     install-m365-dienste.py;
  3. copies the package into a temporary directory and adapts it to this tenant: LzSku maps
     the three variants and the tenant's SKU to the first SKU of license_sku_ids, the same SKU
     install-m365-dienste.py gives M365Lizenzprofil (a test tenant rarely holds E3, E5 and F3;
     every licence then counts as E3 in the contracts);
  4. does what the package's installieren.sh does (decision models, application with its
     information model and architecture, release, catalogue), in the administrator's session;
     the capability map's gaps are adopted from the package's proposals through the API and
     named in the log (ADR-0528);
  5. sets the application constants: ATLAS_API_BASE, the four groups, EA_JAHRESTAG and EA_ENDE
     (next 1 July and three years on, unless --ea-jahrestag / --ea-ende say otherwise);
  6. starts the tenant's quota book (proc_lz_tenantbuch) through its idempotent trigger;
  7. with --vertrag-pruefen, switches the licence order of beispiel-m365-dienste to the
     contracts (LIZENZ_VERTRAG_PRUEFEN = ein, LIZENZ_VERTRAG_STANDARD), keeping its other
     constants.

Re-running is safe: groups are kept, the application and the catalogue are updated, the tenant
book is not started twice. Needs python3 and tofu; it changes nothing in Azure or the tenant.
"""
import argparse
import datetime
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
# The session, the listing helper and the capability-map decision are those of the m365 installer.
m365 = __import__("install-m365-dienste")

APP_KEY = "beispiel-lizenzmanagement"
M365_KEY = "beispiel-m365-dienste"
MODELS = ["lz-abgleich", "lz-entzug-standard", "lz-export-form", "lz-kontingent", "lz-kunde-zuordnung",
          "lz-nachkauf-schwelle", "lz-preise", "lz-psp-pruefung", "lz-sku"]
GROUPS = {
    # answer in fragen.json -> Atlas group
    "zielgruppe": "Lizenz-Integrations-Manager",
    "tenant-verantwortung": "Lizenz-Tenantverantwortung",
}
CONSTANT_GROUPS = {"GRUPPE_TENANT": "Lizenz-Tenantverantwortung", "GRUPPE_CHEF": "Lizenz-Chef",
                   "GRUPPE_LIZENZMANAGEMENT": "Lizenzmanagement", "GRUPPE_FINANZEN": "Lizenz-Finanzen"}
LZ_SKUS = ("05e9a617-0261-4cee-bb44-138d3ef5d965", "06ebc4ee-1bb5-47dd-8120-11324bc54e06",
           "66b55226-6b4f-492c-910c-a3b7a3c9d993")


def log(msg):
    print(f"install-lizenzmanagement: {msg}", flush=True)


def adapt(src, dst, sku):
    """Copies the package and points LzSku at the tenant's SKU."""
    shutil.copytree(src, dst)
    p = os.path.join(dst, "lz-sku.dmn")
    s = open(p, encoding="utf-8").read()
    for old in LZ_SKUS:
        if s.count(old) != 2:
            sys.exit(f"lz-sku.dmn: SKU {old} not found twice; has the package changed?")
        s = s.replace(old, sku)
    open(p, "w", encoding="utf-8").write(s)


def source_tree(pkg):
    man = json.load(open(os.path.join(pkg, "atlas.json"), encoding="utf-8"))
    files = ["atlas.json"] + [p["path"] for p in man["processes"]] + [f["path"] for f in man["forms"]] + \
        sorted({d["path"] for d in man["decisions"] if d.get("path")}) + \
        [m["path"] for m in man.get("informationModels", [])] + ([man["architecture"]] if man.get("architecture") else [])
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for f in files:
            tar.add(os.path.join(pkg, f), arcname=f)
    return buf.getvalue()


def install(atlas, pkg):
    for m in MODELS:
        atlas.call("POST", f"/api/v1/dmn-models?handle={m}", raw=open(os.path.join(pkg, m + ".dmn"), "rb").read(),
                   ctype="application/xml")
    log("decision models uploaded")
    tree = source_tree(pkg)
    _, plan = atlas.call("POST", "/api/v1/applications/source?dryRun=true", raw=tree, ctype="application/gzip")
    _, res = atlas.call("POST", "/api/v1/applications/source" + m365.gap_decisions(plan), raw=tree, ctype="application/gzip")
    app_id = res["applicationId"]
    _, pub = atlas.call("POST", f"/api/v1/applications/{app_id}/publish", {"note": "install-lizenzmanagement.py"})
    log(f"application {app_id} imported and published, release {(pub.get('release') or {}).get('version')}")

    _, principals = atlas.call("GET", "/api/v1/principals")
    fragen = json.load(open(os.path.join(pkg, "fragen.json"), encoding="utf-8"))
    raw = open(os.path.join(pkg, "katalog.json"), encoding="utf-8").read()

    def fill(m):
        name = m.group(1)
        kind = fragen[name]["kind"]
        hit = [p["id"] for p in m365.items(principals, "principals") if p.get("type") == kind and p.get("name") == GROUPS[name]]
        if len(hit) != 1:
            sys.exit(f"{{{{{name}}}}}: group {GROUPS[name]} found {len(hit)} times")
        return hit[0]
    atlas.call("POST", "/api/v1/catalogs/import", raw=re.sub(r"\{\{([a-z0-9-]+)\}\}", fill, raw).encode("utf-8"))
    log("catalogue imported and published")
    return app_id


def next_first_of_july(today):
    d = datetime.date(today.year, 7, 1)
    return d if d > today else datetime.date(today.year + 1, 7, 1)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--tofu-dir", default=os.path.dirname(HERE))
    ap.add_argument("--example", default=os.path.join(HERE, "..", "..", "..", "..", "examples", "lizenzmanagement"))
    ap.add_argument("--cafile", help="Caddy's root certificate (atlas_tls = internal)")
    ap.add_argument("--sku", help="the tenant's licence for E3, E5 and F3 (default: first of license_sku_ids)")
    ap.add_argument("--ea-jahrestag", help="next EA anniversary, YYYY-MM-DD (default: the next 1 July)")
    ap.add_argument("--ea-ende", help="end of the EA term, YYYY-MM-DD (default: three years after the anniversary, minus a day)")
    ap.add_argument("--vertrag-pruefen", action="store_true",
                    help="switch the licence order of beispiel-m365-dienste to the contracts (LIZENZ_VERTRAG_PRUEFEN = ein)")
    ap.add_argument("--standardvertrag", default="",
                    help="LIZENZ_VERTRAG_STANDARD for m365-dienste: the contract of orders that name none (e.g. LV-…)")
    args = ap.parse_args()
    tofu_dir = os.path.abspath(args.tofu_dir)

    url = m365.tofu_output(tofu_dir, "atlas_url")
    admin = m365.tofu_output(tofu_dir, "atlas_admin")
    shared = m365.tofu_output(tofu_dir, "shared_tenant")
    sku = args.sku
    if not sku:
        out = subprocess.run(["tofu", "console"], input="jsonencode(var.license_sku_ids)\n", cwd=tofu_dir,
                             check=True, capture_output=True, text=True).stdout.strip()
        skus = json.loads(json.loads(out))
        sku = skus[0] if skus else None
    if not sku:
        sys.exit("No licence SKU: set license_sku_ids in terraform.tfvars or pass --sku.")
    prefix = (shared or {}).get("prefix", "")
    prefix = prefix + "-" if prefix else ""
    jahrestag = args.ea_jahrestag or next_first_of_july(datetime.date.today()).isoformat()
    ende = args.ea_ende or (datetime.date.fromisoformat(jahrestag).replace(year=datetime.date.fromisoformat(jahrestag).year + 3)
                            - datetime.timedelta(days=1)).isoformat()

    atlas = m365.Atlas(url, args.cafile)
    atlas.call("POST", "/api/v1/auth/login", {"username": admin["username"], "password": admin["password"]})
    log(f"signed in at {url} as {admin['username']}")

    names = sorted(set(GROUPS.values()) | set(CONSTANT_GROUPS.values()))
    _, listing = atlas.call("GET", "/api/v1/groups")
    have = {g["name"]: g["id"] for g in m365.items(listing, "groups")}
    for name in names:
        if name not in have:
            _, g = atlas.call("POST", "/api/v1/groups", {"name": name})
            have[name] = g["id"]
            log(f"group {name} created")
    _, users = atlas.call("GET", "/api/v1/users")
    for u in m365.items(users, "users"):
        if prefix and (u.get("username") or "").startswith(prefix) and u.get("email"):
            for name in names:
                atlas.call("PUT", f"/api/v1/groups/{have[name]}/members/{u['id']}")
            log(f"{u['username']} joins the licence management groups")

    work = tempfile.mkdtemp(prefix="lizenzmanagement-")
    try:
        pkg = os.path.join(work, "lizenzmanagement")
        adapt(os.path.abspath(args.example), pkg, sku)
        app_id = install(atlas, pkg)
    finally:
        shutil.rmtree(work, ignore_errors=True)

    constants = [{"name": "ATLAS_API_BASE", "type": "string", "value": "http://127.0.0.1:8080"},
                 {"name": "EA_JAHRESTAG", "type": "string", "value": jahrestag},
                 {"name": "EA_ENDE", "type": "string", "value": ende}] + \
                [{"name": n, "type": "string", "value": g} for n, g in sorted(CONSTANT_GROUPS.items())]
    atlas.call("PUT", f"/api/v1/applications/{app_id}/constants", {"constants": constants})
    log(f"constants set: EA_JAHRESTAG={jahrestag}, EA_ENDE={ende}, groups {', '.join(sorted(CONSTANT_GROUPS.values()))}")

    # After the constants: the tenant book reads EA_JAHRESTAG and EA_ENDE when it starts.
    code, _ = atlas.call("POST", "/api/v1/processes/proc_lz_tenantbuch/triggers/lz.tenantbuch.eroeffnen",
                         {"triggerId": "tenant", "source": "install-lizenzmanagement.py", "variables": {}})
    log("tenant quota book started" if code == 201 else "tenant quota book was already running")

    if args.vertrag_pruefen:
        _, apps = atlas.call("GET", "/api/v1/applications")
        app = next((a for a in m365.items(apps, "applications") if a.get("key") == M365_KEY), None)
        if not app:
            sys.exit(f"--vertrag-pruefen: {M365_KEY} is not installed; run install-m365-dienste.py first")
        _, held = atlas.call("GET", f"/api/v1/applications/{app['id']}/constants")
        keep = [{"name": c["name"], "type": c["type"], "value": c["value"]} for c in (held or {}).get("constants", [])
                if c.get("name") not in ("LIZENZ_VERTRAG_PRUEFEN", "LIZENZ_VERTRAG_STANDARD")]
        keep.append({"name": "LIZENZ_VERTRAG_PRUEFEN", "type": "string", "value": "ein"})
        # An empty string cannot be held (install-m365-dienste.py, NAME_PREFIX); without a
        # standard contract the package's fallback stays.
        if args.standardvertrag:
            keep.append({"name": "LIZENZ_VERTRAG_STANDARD", "type": "string", "value": args.standardvertrag})
        atlas.call("PUT", f"/api/v1/applications/{app['id']}/constants", {"constants": keep})
        log(f"{M365_KEY}: LIZENZ_VERTRAG_PRUEFEN=ein, LIZENZ_VERTRAG_STANDARD={args.standardvertrag!r}")
    log("done")


if __name__ == "__main__":
    main()
