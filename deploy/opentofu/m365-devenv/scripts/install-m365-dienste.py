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
       - m365-engage, m365-aufbewahrung and the two hybrid-identity products always leave
         them (see UNSERVED), and a catalogue left without products is left out too, and
         m365-sharepoint-site unless --site-inventory names the inventory site;
       - the site order form's SharePoint host becomes the tenant's;
  4. does what the package's installieren.sh does (decision models, application, release,
     catalogue), in the administrator's session rather than with a token (see install());
  5. brings the architecture of examples/m365-dienste/architektur with the application:
     the source tree (format 4, ADR-0528) carries the
     information model and the realizations, and the import files them before the release
     is published, so its architecture sketch (ADR-0471, ADR-0495) records classes,
     stores, capabilities and value streams. The capability map is the server's: what it
     lacks is a gap the import asks about, and this installer decides it through the API
     — it adopts the package's proposals, says which, and writes nothing over a record
     the map already has. A hub import through the Console asks the same in its dialog;
  6. sets the application constants: NAME_PREFIX (the environment's prefix, so everything
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
import urllib.parse
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
TELEPHONY = {"m365-telefonie", "m365-warteschlange", "m365-telefonzentrale"}
# Products this environment cannot serve, whatever is configured: a Viva Engage community
# needs Community.ReadWrite.All on the whole tenant and a network in native mode, which a
# shared tenant should not grant to a test app; Litigation Hold needs Exchange Online
# Plan 2, which Business Basic does not include.
#
# The hybrid-identity products (m365-konto-hybrid, m365-konto-cloud) need Entra Connect
# between the domain controller and the tenant, and this environment synchronises
# nothing: its AD and its Entra ID are two separate directories whose test users merely
# share a UPN. Entra Connect must not be added to the shared tenant either, because it
# would soft-match the AD test users onto the existing cloud users by that UPN and turn
# them into synchronised users for good (examples/m365-dienste/README.md, «Hybride
# Identität (Entra Connect)»).
UNSERVED = {"m365-engage", "m365-aufbewahrung", "m365-konto-hybrid", "m365-konto-cloud"}
SITE = "m365-sharepoint-site"
GROUPS = {
    # answer in fragen.json -> Atlas group
    "zielgruppe": "M365-Mitarbeitende",
    "zielgruppe-ucc": "M365-UCC",
    "telefonie-team": "M365-Telefonie",
    "facility-management": "M365-Facility",
    "compliance": "Informationssicherheit",
    "zielgruppe-identitaet": "M365-Administration",
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
    # A catalogue whose every product is left out here (cat-m365-identitaet) would publish
    # empty; it goes too, with the audience it asks for.
    doc["catalogs"] = [c for c in doc["catalogs"] if c["items"]]
    json.dump(doc, open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
    log("products left out here: " + ", ".join(sorted(drop)))


# The on-premises variant (exchange.tf): the Exchange products once more, in a catalogue
# of their own, served by the Exchange worker on the Exchange server.
ONPREM_PRODUCTS = ["m365-funktionspostfach", "m365-verteiler", "m365-postfachzugriff", "m365-kalender", "m365-raum", "m365-geraet"]
ONPREM_WORKER = "ucc_exchange_onprem"
ONLINE_STORE = "Exchange Online: Empfaenger"
ONPREM_STORE = "Exchange Server: Empfaenger"


def build_onprem(pkg, dst):
    """Writes the on-premises variant of the adapted package pkg into dst.

    Every process the Exchange products run, every form they show and every message
    they start by is copied under a name of its own (process ids and form ids get
    _onprem / -onprem, messages m365onprem.*), so the variant neither replaces the
    online catalogue's definitions nor starts its processes; the copies name the
    on-premises worker. The approval stays the online one (proc_m365_genehmigung_
    vorgesetzter, which reads the manager in Entra), and so do the decisions, which belong
    to the online application.
    The people the forms find in Entra are the same people to Exchange Server because
    the test domain's users carry the tenant's UPN (exchange-setup.ps1, phase 4).
    """
    os.makedirs(dst)
    man = json.load(open(os.path.join(pkg, "atlas.json"), encoding="utf-8"))
    cat = json.load(open(os.path.join(pkg, "katalog.json"), encoding="utf-8"))
    path_of = {x["id"]: x["path"] for x in man["processes"]}
    form_path = {x["id"]: x["path"] for x in man["forms"]}
    products = [x for x in cat["products"] if x["id"] in ONPREM_PRODUCTS]
    procs, forms, messages = set(), set(), set()
    for x in products:
        for k in ("lifecycleProcess", "provisionProcess", "deprovisionProcess"):
            if x.get(k):
                procs.add(x[k])
        forms.add(x.get("configForm"))
        for a in x.get("actions", []):
            if a.get("form"):
                forms.add(a["form"])
            if a.get("message"):
                messages.add(a["message"])
    # A process a copied process calls (raum-einrichten calls proc_m365_raumliste) is
    # copied too, and the call names the copy: the online process would run its tasks on
    # the online worker.
    todo = list(procs)
    while todo:
        src = open(os.path.join(pkg, path_of[todo.pop()]), encoding="utf-8").read()
        for called in re.findall(r'<zeebe:calledElement processId="([^"]+)"', src):
            if called in path_of and called not in procs:
                procs.add(called)
                todo.append(called)
    for pid in procs:
        forms |= set(re.findall(r'formId="([^"]+)"', open(os.path.join(pkg, path_of[pid]), encoding="utf-8").read()))
    forms.discard(None)

    def proc(i): return i + "_onprem"
    def form(i): return i + "-onprem"
    def message(m): return m.replace("m365.", "m365onprem.", 1)

    out_procs = []
    for pid in sorted(procs):
        src = open(os.path.join(pkg, path_of[pid]), encoding="utf-8").read()
        src = src.replace('connector="ucc_exchange"', f'connector="{ONPREM_WORKER}"')
        # The data store the processes name follows the worker (architektur/informationsmodell.json).
        src = src.replace(f'name="{ONLINE_STORE}"', f'name="{ONPREM_STORE}"')
        # The process id is an attribute value wherever it occurs (the process, its
        # diagram plane): replaced as a whole quoted value.
        src = src.replace(f'"{pid}"', f'"{proc(pid)}"')
        for other in procs:
            src = src.replace(f'processId="{other}"', f'processId="{proc(other)}"')
        src = re.sub(r'(<process id="[^"]+" name=")([^"]*)"', r'\g<1>\g<2> (Exchange Server)"', src, count=1)
        for m in messages:
            src = src.replace(f'name="{m}"', f'name="{message(m)}"')
        for f in forms:
            src = src.replace(f'formId="{f}"', f'formId="{form(f)}"')
        name = path_of[pid].replace(".bpmn", "-onprem.bpmn")
        open(os.path.join(dst, name), "w", encoding="utf-8").write(src)
        out_procs.append({"id": proc(pid), "name": proc(pid), "path": name})
    out_forms = []
    for fid in sorted(forms):
        doc = json.load(open(os.path.join(pkg, form_path[fid]), encoding="utf-8"))
        doc["id"] = form(fid)
        name = form_path[fid].replace(".form.json", "-onprem.form.json")
        json.dump(doc, open(os.path.join(dst, name), "w", encoding="utf-8"), ensure_ascii=False, indent=2)
        out_forms.append({"id": form(fid), "name": form(fid), "path": name})
    for m in MODELS:
        shutil.copy(os.path.join(pkg, m + ".dmn"), os.path.join(dst, m + ".dmn"))
    shutil.copy(os.path.join(pkg, "fragen.json"), os.path.join(dst, "fragen.json"))
    # The variant's architecture (ADR-0528): its own copy
    # of the information model, whose Exchange store is Exchange Server and its worker, and
    # the online realizations of the processes it copies, under their copies' ids. The
    # proposals are the online package's: installed after it, the variant finds every key
    # in the map and only links.
    os.makedirs(os.path.join(dst, "architektur"))
    model = json.load(open(os.path.join(pkg, man["informationModels"][0]["path"]), encoding="utf-8"))
    for st in model["stores"]:
        if st["name"] == ONLINE_STORE:
            st["name"], st["worker"] = ONPREM_STORE, ONPREM_WORKER
    model["name"] += " (Exchange Server)"
    json.dump(model, open(os.path.join(dst, "architektur", "informationsmodell.json"), "w", encoding="utf-8"),
              ensure_ascii=False, indent=2)
    arch = json.load(open(os.path.join(pkg, man["architecture"]), encoding="utf-8"))
    arch["realizations"] = [dict(r, processId=proc(r["processId"])) for r in arch["realizations"]
                            if r.get("kind") == "process" and r.get("processId") in procs]
    json.dump(arch, open(os.path.join(dst, "architektur", "capabilities.json"), "w", encoding="utf-8"),
              ensure_ascii=False, indent=2)
    json.dump({"formatVersion": man["formatVersion"], "key": APP_KEY + "-onprem",
               "name": man["name"] + " (Exchange Server)", "processes": out_procs, "forms": out_forms,
               # A decision belongs to one application: the variant's business rule
               # tasks call the online application's M365Namensregel and
               # M365PostfachzugriffFreigabe by their decision ids.
               "decisions": [],
               "informationModels": [{"name": model["name"], "path": "architektur/informationsmodell.json"}],
               "architecture": "architektur/capabilities.json"},
              open(os.path.join(dst, "atlas.json"), "w", encoding="utf-8"), ensure_ascii=False, indent=2)

    out_products = []
    for x in products:
        y = json.loads(json.dumps(x))
        y["id"] = x["id"] + "-onprem"
        y["homeCatalog"] = "cat-m365-onprem"
        for k in ("lifecycleProcess", "provisionProcess", "deprovisionProcess"):
            if y.get(k):
                y[k] = proc(y[k])
        if y.get("configForm"):
            y["configForm"] = form(y["configForm"])
        for a in y.get("actions", []):
            if a.get("form"):
                a["form"] = form(a["form"])
            if a.get("message"):
                a["message"] = message(a["message"])
        y["texts"] = {k: v + " (Exchange Server)" for k, v in x["texts"].items()}
        out_products.append(y)
    # A person sees every catalogue they are the audience for (ADR-0522), so the
    # variant's catalogue, with the staff audience, is offered beside the online ones.
    doc = {"publish": cat.get("publish", True),
           "catalogs": [{"id": "cat-m365-onprem",
                         "texts": {"de": "Exchange Server (OnPrem)", "en": "Exchange Server (on-premises)"},
                         "rank": 400, "languages": ["de", "en"], "items": [y["id"] for y in out_products],
                         "groups": ["{{zielgruppe}}"], "edges": []}],
           "products": out_products}
    json.dump(doc, open(os.path.join(dst, "katalog.json"), "w", encoding="utf-8"), ensure_ascii=False, indent=2)


APP_KEY = "beispiel-m365-dienste"
MODELS = ["m365-lizenzprofil", "m365-namensregel", "m365-postfachkontingent", "m365-postfachzugriff",
          "m365-site-kontingent", "m365-telefonie", "m365-verteiler-zuordnung"]


def gap_decisions(plan):
    """The query that settles the capability map's gaps of a planned source import: every
    gap the package proposes a record for is adopted, and each is named in the log. A gap
    without a proposal stops the installer — mapping it onto another key is a person's
    decision, made in the Console."""
    gaps = [a for a in plan.get("artifacts", []) if a.get("status") == "gap"]
    if not gaps:
        return ""
    bare = [a["ref"] for a in gaps if not a.get("proposed")]
    if bare:
        sys.exit(f"the capability map lacks {', '.join(bare)} and the package proposes nothing for it: decide in the Console")
    for a in gaps:
        log(f"capability map: adopting the package's proposal {a['ref']} ({a.get('name', '')})")
    return "?" + urllib.parse.urlencode({"adopt": ",".join(a["ref"] for a in gaps)})


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
        sorted({d["path"] for d in man["decisions"] if d.get("path")}) + \
        [m["path"] for m in man.get("informationModels", [])] + ([man["architecture"]] if man.get("architecture") else [])
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for f in files:
            tar.add(os.path.join(pkg, f), arcname=f)
    tree = buf.getvalue()
    # The plan first: it names the capability map's gaps before anything is written.
    _, plan = atlas.call("POST", "/api/v1/applications/source?dryRun=true", raw=tree, ctype="application/gzip")
    _, res = atlas.call("POST", "/api/v1/applications/source" + gap_decisions(plan), raw=tree, ctype="application/gzip")
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
    ap.add_argument("--no-onprem", action="store_true",
                    help="leave out the Exchange Server variant even when exchange_onprem is built")
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

    example = os.path.abspath(args.example)
    onprem = tofu_output(tofu_dir, "exchange_onprem")
    with_onprem = bool(onprem) and not args.no_onprem

    work = tempfile.mkdtemp(prefix="m365-dienste-")
    try:
        pkg = os.path.join(work, "m365-dienste")
        adapt(example, pkg, sku, with_telephony, prefix.rstrip("-") if prefix else "",
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

    if with_onprem:
        work = tempfile.mkdtemp(prefix="m365-dienste-onprem-")
        try:
            pkg = os.path.join(work, "m365-dienste")
            adapt(os.path.abspath(args.example), pkg, sku, with_telephony, prefix.rstrip("-") if prefix else "",
                  sharepoint_host, bool(args.site_inventory))
            var = os.path.join(work, "onprem")
            build_onprem(pkg, var)
            install(atlas, var)
        finally:
            shutil.rmtree(work, ignore_errors=True)
        _, apps = atlas.call("GET", "/api/v1/applications")
        app = next((a for a in items(apps, "applications") if a.get("key") == APP_KEY + "-onprem"), None)
        if not app:
            sys.exit(f"the application {APP_KEY}-onprem is not listed after the import")
        onprem_domain = onprem["fqdn"].split(".", 1)[1]
        atlas.call("PUT", f"/api/v1/applications/{app['id']}/constants", {"constants": [
            {"name": "NAME_PREFIX", "type": "json", "value": json.dumps(prefix)},
            {"name": "MAIL_DOMAIN", "type": "string", "value": onprem_domain},
            {"name": "ATLAS_API_BASE", "type": "string", "value": "http://127.0.0.1:8080"},
            # Set-Place exists in Exchange Online only; the Exchange worker refuses it
            # against the Exchange Server, so the room processes skip it (raumliste.bpmn).
            {"name": "ORTSANGABEN", "type": "json", "value": "false"},
        ]})
        log(f"on-premises variant installed: catalogue cat-m365-onprem, worker {ONPREM_WORKER}, MAIL_DOMAIN={onprem_domain}")
    log("done")


if __name__ == "__main__":
    main()
