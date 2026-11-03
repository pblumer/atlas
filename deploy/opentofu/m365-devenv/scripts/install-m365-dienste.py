#!/usr/bin/env python3
"""Installs examples/m365-dienste into the Atlas server of this environment, ready to order.

    python3 scripts/install-m365-dienste.py --cafile out/caddy-root.crt

Run from deploy/opentofu/m365-devenv after `tofu apply` and scripts/wait-atlas.sh. It

  1. signs in as the local administrator (password from `tofu output -json atlas_admin`,
     never printed);
  2. creates the Atlas groups the package addresses (M365-Administration, M365-Telefonie,
     Informationssicherheit) and the audiences and approvers it asks for;
  3. copies the package into a temporary directory and adapts it to this tenant:
       - M365Lizenzprofil: the variants standard, erweitert and frontline become the first
         SKU of license_sku_ids (a test tenant rarely holds E3, E5 and F3);
       - without the Teams Phone worker (workers.teamsphone = false) the products
         m365-telefonie, m365-warteschlange and m365-telefonzentrale leave both catalogues;
       - m365-engage and m365-aufbewahrung always leave them (see UNSERVED), and
         m365-sharepoint-site unless --site-inventory names the inventory site;
       - the site order form's SharePoint host becomes the tenant's;
  4. does what the package's installieren.sh does (decision models, application, release,
     catalogue), in the administrator's session rather than with a token (see install());
  5. sets the application constants: NAME_PREFIX (the environment's prefix, so everything
     the catalogue creates lies in the shared tenant's Exchange scope and is removed by
     the cleanup scripts), MAIL_DOMAIN, USAGE_LOCATION, PARTNER_UPN_DOMAIN, ATLAS_API_BASE.

Re-running is safe: existing groups are kept, the application and the catalogue are updated.
Test persons join the approver and audience groups only once they have signed in to Atlas;
whoever signs in for the first time after the installation sees no catalogue until this is
run again (or they are added to the groups by hand).
Needs python3 and tofu. --cafile is Caddy's root certificate
(/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt on the server) with
atlas_tls = "internal"; with atlas_tls = "letsencrypt" leave it out.
"""
import argparse
import io
import re
import tarfile
import http.cookiejar
import json
import os
import shutil
import ssl
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
TELEPHONY = {"m365-telefonie", "m365-warteschlange", "m365-telefonzentrale"}
# Products this environment cannot serve, whatever is configured: a Viva Engage community
# needs Community.ReadWrite.All on the whole tenant and a network in native mode, which a
# shared tenant should not grant to a test app; Litigation Hold needs Exchange Online
# Plan 2, which Business Basic does not include.
UNSERVED = {"m365-engage", "m365-aufbewahrung"}
SITE = "m365-sharepoint-site"
GROUPS = {
    # answer in fragen.json -> Atlas group
    "zielgruppe": "M365-Mitarbeitende",
    "zielgruppe-ucc": "M365-UCC",
    "telefonie-team": "M365-Telefonie",
    "facility-management": "M365-Facility",
    "compliance": "Informationssicherheit",
}
NAMED_GROUPS = ["M365-Administration", "M365-Telefonie", "Informationssicherheit"]


def log(msg):
    print(f"install-m365-dienste: {msg}", flush=True)


def tofu_output(tofu_dir, name):
    out = subprocess.run(["tofu", "output", "-json", name], cwd=tofu_dir, check=True,
                         capture_output=True, text=True).stdout
    return json.loads(out)


class Atlas:
    def __init__(self, base, cafile):
        self.base = base.rstrip("/")
        ctx = ssl.create_default_context(cafile=cafile) if cafile else ssl.create_default_context()
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=ctx),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(self, method, path, body=None, ok=(200, 201, 204), raw=None, ctype="application/json"):
        data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
        req = urllib.request.Request(self.base + path, data=data, method=method)
        if data is not None:
            req.add_header("Content-Type", ctype)
        try:
            with self.opener.open(req, timeout=120) as r:
                text = r.read().decode() or "null"
                return r.status, json.loads(text) if text.strip()[:1] in "[{n" else text
        except urllib.error.HTTPError as e:
            detail = e.read().decode(errors="replace")[:500]
            if e.code in ok:
                return e.code, detail
            raise RuntimeError(f"{method} {path}: HTTP {e.code} {detail}") from None


def items(listing, key):
    if isinstance(listing, dict):
        for k in (key, "items", "data"):
            if isinstance(listing.get(k), list):
                return listing[k]
    return listing or []


def adapt(src, dst, sku, with_telephony, department, sharepoint_host, with_site):
    shutil.copytree(src, dst)
    # Directory fields: in a shared tenant a search must offer this environment's test
    # users and nobody else, so every field gets a fixed filter on their department
    # (the environment id, entra.tf), which the server applies whatever a request says.
    if department:
        for name in sorted(os.listdir(dst)):
            if not name.endswith(".form.json"):
                continue
            p = os.path.join(dst, name)
            doc = json.load(open(p, encoding="utf-8"))
            changed = False

            def walk(c):
                nonlocal changed
                for x in c.get("components", []):
                    src_ = x.get("atlasDataSource")
                    if src_ and src_.get("resource") == "users":
                        src_["filters"] = [f for f in src_.get("filters", []) if f.get("property") != "department"] + \
                            [{"property": "department", "value": department}]
                        changed = True
                    walk(x)
            walk(doc.get("schema", doc))
            if changed:
                json.dump(doc, open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
    # Licence profile: the three mailbox variants become the tenant's licence.
    p = os.path.join(dst, "m365-lizenzprofil.dmn")
    s = open(p, encoding="utf-8").read()
    for old in ("05e9a617-0261-4cee-bb44-138d3ef5d965", "06ebc4ee-1bb5-47dd-8120-11324bc54e06",
                "66b55226-6b4f-492c-910c-a3b7a3c9d993"):
        if s.count(old) != 1:
            sys.exit(f"m365-lizenzprofil.dmn: SKU {old} not found once; has the package changed?")
        s = s.replace(old, sku)
    for name in ("Microsoft 365 E3", "Microsoft 365 E5", "Microsoft 365 F3"):
        s = s.replace(f'"{name}"', f'"{name} (Testumgebung: {sku})"')
    open(p, "w", encoding="utf-8").write(s)

    # The site's order form checks the URL against the tenant's SharePoint host, which
    # the package names as contoso.sharepoint.com (README, Voraussetzungen 7).
    p = os.path.join(dst, "m365-sharepoint-site-bestellung.form.json")
    if os.path.exists(p):
        s = open(p, encoding="utf-8").read()
        open(p, "w", encoding="utf-8").write(s.replace("contoso.sharepoint.com", sharepoint_host))

    drop = set(UNSERVED)
    if not with_telephony:
        drop |= TELEPHONY
    if not with_site:
        drop.add(SITE)
    p = os.path.join(dst, "katalog.json")
    doc = json.load(open(p, encoding="utf-8"))
    doc["products"] = [x for x in doc["products"] if x["id"] not in drop]
    for c in doc["catalogs"]:
        c["items"] = [i for i in c["items"] if i not in drop]
        c["edges"] = [e for e in c.get("edges", []) if e["from"] not in drop and e["to"] not in drop]
    json.dump(doc, open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
    log("products left out here: " + ", ".join(sorted(drop)))


APP_KEY = "beispiel-m365-dienste"
MODELS = ["m365-lizenzprofil", "m365-namensregel", "m365-postfachkontingent", "m365-postfachzugriff",
          "m365-site-kontingent", "m365-telefonie", "m365-verteiler-zuordnung"]


def install(atlas, pkg):
    """The steps of the package's installieren.sh, in the administrator's session.

    installieren.sh authenticates with an API token, and the application then belongs to
    that token: a second run with another token is refused ("no application with that
    key"), and a token never carries productmanager for the catalogue. The administrator's
    session owns the application and may import the catalogue, so a re-run updates both.
    """
    for m in MODELS:
        atlas.call("POST", f"/api/v1/dmn-models?handle={m}", raw=open(os.path.join(pkg, m + ".dmn"), "rb").read(),
                   ctype="application/xml")
    log("decision models uploaded")

    man = json.load(open(os.path.join(pkg, "atlas.json"), encoding="utf-8"))
    files = ["atlas.json"] + [p["path"] for p in man["processes"]] + [f["path"] for f in man["forms"]] + \
        sorted({d["path"] for d in man["decisions"] if d.get("path")})
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for f in files:
            tar.add(os.path.join(pkg, f), arcname=f)
    _, res = atlas.call("POST", "/api/v1/applications/source", raw=buf.getvalue(), ctype="application/gzip")
    app_id = res["applicationId"]
    _, pub = atlas.call("POST", f"/api/v1/applications/{app_id}/publish", {"note": "install-m365-dienste.py"})
    log(f"application {app_id} imported and published, release {(pub.get('release') or {}).get('version')}")

    _, principals = atlas.call("GET", "/api/v1/principals")
    fragen = json.load(open(os.path.join(pkg, "fragen.json"), encoding="utf-8"))
    raw = open(os.path.join(pkg, "katalog.json"), encoding="utf-8").read()

    def fill(m):
        name = m.group(1)
        kind = fragen[name]["kind"]
        hit = [p["id"] for p in items(principals, "principals") if p.get("type") == kind and p.get("name") == GROUPS[name]]
        if len(hit) != 1:
            sys.exit(f"{{{{{name}}}}}: group {GROUPS[name]} found {len(hit)} times")
        return hit[0]
    doc = re.sub(r"\{\{([a-z0-9-]+)\}\}", fill, raw)
    atlas.call("POST", "/api/v1/catalogs/import", raw=doc.encode("utf-8"))
    log("catalogue imported and published")


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--tofu-dir", default=os.path.dirname(HERE))
    ap.add_argument("--example", default=os.path.join(HERE, "..", "..", "..", "..", "examples", "m365-dienste"))
    ap.add_argument("--cafile", help="Caddy's root certificate (atlas_tls = internal)")
    ap.add_argument("--sku", help="licence for the mailbox variants (default: first of license_sku_ids)")
    ap.add_argument("--mail-domain", help="domain of the catalogue's addresses (default: the initial domain)")
    ap.add_argument("--site-inventory", help="SITE_INVENTAR, the site carrying the list «Site-Inventar» "
                    "(host:/sites/path); without it the SharePoint team site is left out")
    args = ap.parse_args()
    tofu_dir = os.path.abspath(args.tofu_dir)

    url = tofu_output(tofu_dir, "atlas_url")
    admin = tofu_output(tofu_dir, "atlas_admin")
    shared = tofu_output(tofu_dir, "shared_tenant")
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
    domain = args.mail_domain or tofu_output(tofu_dir, "tenant")["initial_domain"]
    with_telephony = bool({"teams", "teamsphone"} & set(tofu_output(tofu_dir, "atlas_workers").values()))
    # The tenant's SharePoint host follows its initial domain: <name>.onmicrosoft.com
    # serves <name>.sharepoint.com.
    initial = tofu_output(tofu_dir, "tenant")["initial_domain"]
    sharepoint_host = initial.split(".", 1)[0] + ".sharepoint.com"

    atlas = Atlas(url, args.cafile)
    atlas.call("POST", "/api/v1/auth/login", {"username": admin["username"], "password": admin["password"]})
    log(f"signed in at {url} as {admin['username']}")

    _, listing = atlas.call("GET", "/api/v1/groups")
    have = {g["name"]: g["id"] for g in items(listing, "groups")}
    for name in sorted(set(GROUPS.values()) | set(NAMED_GROUPS)):
        if name not in have:
            _, g = atlas.call("POST", "/api/v1/groups", {"name": name})
            have[name] = g["id"]
            log(f"group {name} created")

    # Approvers need an address: a group whose members have none resolves to no
    # recipient, and the approval mail fails. The test users' Atlas accounts carry their
    # Entra mail once they have signed in; they join the approver and audience groups.
    _, users = atlas.call("GET", "/api/v1/users")
    for u in items(users, "users"):
        if prefix and (u.get("username") or "").startswith(prefix) and u.get("email"):
            for name in sorted(set(GROUPS.values()) | set(NAMED_GROUPS)):
                atlas.call("PUT", f"/api/v1/groups/{have[name]}/members/{u['id']}")
            log(f"{u['username']} joins the approver and audience groups")

    work = tempfile.mkdtemp(prefix="m365-dienste-")
    try:
        pkg = os.path.join(work, "m365-dienste")
        adapt(os.path.abspath(args.example), pkg, sku, with_telephony, prefix.rstrip("-") if prefix else "",
              sharepoint_host, bool(args.site_inventory))
        install(atlas, pkg)
    finally:
        shutil.rmtree(work, ignore_errors=True)

    _, apps = atlas.call("GET", "/api/v1/applications")
    app = next((a for a in items(apps, "applications") if a.get("key") == APP_KEY), None)
    if not app:
        sys.exit(f"the application {APP_KEY} is not listed after the import")
    constants = [
        # json, not string: an empty string cannot be a bound constant's fallback (the
        # compiler reads an empty value as "none"), so the package declares the fallback
        # "" as JSON, and the value held here has the same type.
        {"name": "NAME_PREFIX", "type": "json", "value": json.dumps(prefix)},
        {"name": "MAIL_DOMAIN", "type": "string", "value": domain},
        {"name": "USAGE_LOCATION", "type": "string", "value": os.environ.get("M365_USAGE_LOCATION", "CH")},
        {"name": "PARTNER_UPN_DOMAIN", "type": "string", "value": domain},
        {"name": "ATLAS_API_BASE", "type": "string", "value": "http://127.0.0.1:8080"},
        {"name": "SHAREPOINT_HOST", "type": "string", "value": sharepoint_host},
    ]
    if args.site_inventory:
        constants.append({"name": "SITE_INVENTAR", "type": "string", "value": args.site_inventory})
    atlas.call("PUT", f"/api/v1/applications/{app['id']}/constants", {"constants": constants})
    log(f"constants set: NAME_PREFIX={prefix!r}, MAIL_DOMAIN={domain}")
    log("done")


if __name__ == "__main__":
    main()
