#!/usr/bin/env python3
"""Installs examples/informationstransfer into the Atlas server of this environment, ready to order.

    python3 scripts/install-informationstransfer.py --cafile out/caddy-root.crt
    python3 scripts/install-informationstransfer.py --nach-hub --cafile out/caddy-root.crt
    python3 scripts/install-informationstransfer.py --dry-run [--test-ou OU=AtlasTest,DC=atlastest,DC=internal]

With --nach-hub the application must already have been imported from the atlas hub (the
Console's hub dialog, or atlas_hub_import_release as in TESTLAUF-KATALOG.md, Phase 0). Steps 3
and the import of step 5 are then left out: the hub's package is installed as published, and
every constant the processes read is bound to the application, so step 6 is what gives them
this environment's values. A hub import neither publishes nor imports the catalogue; this does
both, and the rest as below.

Run from deploy/opentofu/m365-devenv after `tofu apply` (with enable_ad_dc) and
scripts/wait-atlas.sh, ideally after install-ad-dienste.py (whose groups the transfer of access
rights copies). It

  1. derives the directory as install-ad-dienste.py does: the accounts live in the test OU
     (`tofu output -json ad_test_ou`), the manual groups in OU=Gruppen,OU=Atlas,<test OU> (the
     AD_GRUPPEN_OU of ad-dienste);
  2. picks the mail system: exchange-server when the environment has built the Exchange Server
     (`tofu output -json exchange_onprem`, worker ucc_exchange_onprem), else exchange-online
     (worker ucc_exchange); --postfach overrides it;
  3. copies the package into a temporary directory and gives the constants' fallbacks in the
     processes this environment's values; a contoso DN left in anything it uploads stops it;
  4. signs in as the local administrator (password from `tofu output -json atlas_admin`, never
     printed) and creates the Atlas groups the package addresses (Identity Management,
     Informationssicherheit, M365-Administration, M365-Telefonie); test persons who have signed
     in join them, as in install-m365-dienste.py;
  5. uploads the decision model, imports and publishes the application and imports the
     catalogue with its audience (Identity Management);
  6. sets the application constants, keeping any other constant held for the application.
     ENTRA_TRANSFER is always set to `aus`: this environment has no Entra Connect, so the hard
     match cannot complete, and the transfer deletes a cloud account for good. Whoever wants to
     walk it anyway sets ENTRA_TRANSFER = ein in the Console, by hand, for one test, and resets it
     (examples/informationstransfer/README.md, «In der Testumgebung»);
  7. switches «Forms may search» on at ad_verzeichnis (the order form's person search).

--dry-run adapts the package and prints what would be uploaded, without signing in. Re-running is
safe. Needs python3 and tofu; it changes nothing in Azure, the tenant or the directory.
"""
import argparse
import io
import json
import os
import re
import shutil
import sys
import tarfile
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
# The session, the listing helper and the capability-map decision are those of the m365 installer;
# the DN helpers those of the AD one.
m365 = __import__("install-m365-dienste")
ad = __import__("install-ad-dienste")

APP_KEY = "beispiel-informationstransfer"
AD_WORKER = "ad_verzeichnis"
MODELS = ["it-optionen"]
CONTOSO_DN = re.compile(r"DC=contoso", re.IGNORECASE)
AUDIENCE = {"zielgruppe": "Identity Management"}
GROUPS = ["Identity Management", "Informationssicherheit", "M365-Administration", "M365-Telefonie"]
SYSTEMS = ("exchange-server", "exchange-online")


def log(msg):
    print(f"install-informationstransfer: {msg}", flush=True)


def constants_for(test_ou, postfach, prefix):
    """The application constants for this environment. ENTRA_TRANSFER is never switched on here."""
    if postfach not in SYSTEMS:
        sys.exit(f"--postfach {postfach!r}: want one of {', '.join(SYSTEMS)}")
    return {
        "ATLAS_API_BASE": "http://127.0.0.1:8080",
        "AD_PERSONEN_OU": test_ou,
        "AD_GRUPPEN_OU": f"OU=Gruppen,OU=Atlas,{test_ou}",
        "POSTFACH_SYSTEM": postfach,
        "ENTRA_TRANSFER": "aus",
        "KUERZEL_HERKUNFT": "generiert",
        "NAME_PREFIX": prefix,
        "GRUPPE_IDENTITY_MANAGEMENT": "Identity Management",
        "GRUPPE_INFORMATIONSSICHERHEIT": "Informationssicherheit",
        "GRUPPE_EXCHANGE": "M365-Administration",
        "GRUPPE_TELEFONIE": "M365-Telefonie",
        "WIEDERHERSTELLUNG_INTERVALL": "PT10M",
        "WIEDERHERSTELLUNG_RUNDEN": "72",
    }


def uploaded_files(pkg):
    """Every file of the package this installer sends: the source tree, the model, the catalogue."""
    man = json.load(open(os.path.join(pkg, "atlas.json"), encoding="utf-8"))
    tree = ["atlas.json"] + [p["path"] for p in man["processes"]] + [f["path"] for f in man["forms"]] + \
        sorted({d["path"] for d in man["decisions"] if d.get("path")}) + \
        [m["path"] for m in man.get("informationModels", [])] + ([man["architecture"]] if man.get("architecture") else [])
    return tree, sorted(set(tree) | {m + ".dmn" for m in MODELS} | {"katalog.json"})


def adapt(src, dst, constants):
    """Copies the package and gives the constants' fallbacks in the processes this environment's values."""
    shutil.copytree(src, dst)
    pat = re.compile(r'(<atlas:constant name="([A-Z_]+)"[^>]*?\bvalue=")([^"]*)(")')
    _, files = uploaded_files(dst)
    for f in files:
        if not f.endswith(".bpmn"):
            continue
        p = os.path.join(dst, f)
        s = open(p, encoding="utf-8").read()
        s = pat.sub(lambda m: m.group(1) + constants.get(m.group(2), m.group(3)) + m.group(4), s)
        open(p, "w", encoding="utf-8").write(s)
    left = []
    for f in files:
        for n, line in enumerate(open(os.path.join(dst, f), encoding="utf-8"), 1):
            if CONTOSO_DN.search(line):
                left.append(f"{f}:{n}: {line.strip()[:160]}")
    if left:
        sys.exit("a contoso DN is left in what would be uploaded:\n  " + "\n  ".join(left))


def install(atlas, pkg, from_hub=False):
    if from_hub:
        _, apps = atlas.call("GET", "/api/v1/applications")
        app = next((a for a in m365.items(apps, "applications") if a.get("key") == APP_KEY), None)
        if not app:
            sys.exit(f"--nach-hub: the application {APP_KEY} is not on this server; import it from the "
                     "atlas hub first (Console, atlas hub, or atlas_hub_import_release)")
        return publish_with_catalogue(atlas, pkg, app["id"], "imported from the atlas hub")
    for m in MODELS:
        atlas.call("POST", f"/api/v1/dmn-models?handle={m}", raw=open(os.path.join(pkg, m + ".dmn"), "rb").read(),
                   ctype="application/xml")
    log("decision model uploaded: " + ", ".join(MODELS))
    tree_files, _ = uploaded_files(pkg)
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for f in tree_files:
            tar.add(os.path.join(pkg, f), arcname=f)
    tree = buf.getvalue()
    _, plan = atlas.call("POST", "/api/v1/applications/source?dryRun=true", raw=tree, ctype="application/gzip")
    _, res = atlas.call("POST", "/api/v1/applications/source" + m365.gap_decisions(plan), raw=tree, ctype="application/gzip")
    return publish_with_catalogue(atlas, pkg, res["applicationId"], "imported")


def publish_with_catalogue(atlas, pkg, app_id, how):
    _, pub = atlas.call("POST", f"/api/v1/applications/{app_id}/publish", {"note": "install-informationstransfer.py"})
    log(f"application {app_id} {how} and published, release {(pub.get('release') or {}).get('version')}")

    _, principals = atlas.call("GET", "/api/v1/principals")
    fragen = json.load(open(os.path.join(pkg, "fragen.json"), encoding="utf-8"))
    raw = open(os.path.join(pkg, "katalog.json"), encoding="utf-8").read()

    def fill(m):
        name = m.group(1)
        if name not in AUDIENCE or name not in fragen:
            sys.exit(f"{{{{{name}}}}}: no answer in this installer or no question in fragen.json; has the package changed?")
        kind = fragen[name]["kind"]
        hit = [p["id"] for p in m365.items(principals, "principals") if p.get("type") == kind and p.get("name") == AUDIENCE[name]]
        if len(hit) != 1:
            sys.exit(f"{{{{{name}}}}}: group {AUDIENCE[name]} found {len(hit)} times")
        return hit[0]
    atlas.call("POST", "/api/v1/catalogs/import", raw=re.sub(r"\{\{([a-z0-9-]+)\}\}", fill, raw).encode("utf-8"))
    log("catalogue imported and published")
    return app_id


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--tofu-dir", default=os.path.dirname(HERE))
    ap.add_argument("--example", default=os.path.join(HERE, "..", "..", "..", "..", "examples", "informationstransfer"))
    ap.add_argument("--cafile", help="Caddy's root certificate (atlas_tls = internal)")
    ap.add_argument("--test-ou", help="the test OU (default: tofu output ad_test_ou)")
    ap.add_argument("--postfach", choices=SYSTEMS,
                    help="POSTFACH_SYSTEM (default: exchange-server when exchange_onprem is built, else exchange-online)")
    ap.add_argument("--dry-run", action="store_true", help="adapt the package and print what would be uploaded; sign in nowhere")
    ap.add_argument("--nach-hub", action="store_true",
                    help="the application was imported from the atlas hub: publish it, import the catalogue, "
                         "set constants, groups and the form search; upload no package")
    args = ap.parse_args()
    tofu_dir = os.path.abspath(args.tofu_dir)

    test_ou = args.test_ou or m365.tofu_output(tofu_dir, "ad_test_ou")
    if not test_ou:
        sys.exit("No domain controller (ad_test_ou is null): the package needs enable_ad_dc = true.")
    ad.domain_dn_of(test_ou)  # refuses an OU that does not end in DC= components
    postfach = args.postfach
    if not postfach:
        postfach = "exchange-server" if (args.dry_run and args.test_ou) or m365.tofu_output(tofu_dir, "exchange_onprem") else "exchange-online"
    prefix = ""
    if not (args.dry_run and args.test_ou):
        shared = m365.tofu_output(tofu_dir, "shared_tenant")
        prefix = (shared or {}).get("prefix", "")
    # NAME_PREFIX carries the trailing dash, as install-m365-dienste.py sets it: names are
    # built as prefix + rest, and the account searches compare with startswith.
    constants = constants_for(test_ou, postfach, prefix + "-" if prefix else "")

    work = tempfile.mkdtemp(prefix="informationstransfer-")
    try:
        pkg = os.path.join(work, "informationstransfer")
        adapt(os.path.abspath(args.example), pkg, constants)
        log(f"accounts under {test_ou}, manual groups under {constants['AD_GRUPPEN_OU']}, mailboxes on {postfach}")
        if args.dry_run:
            _, files = uploaded_files(pkg)
            print("would upload: " + ", ".join(files))
            print("constants:")
            for k, v in constants.items():
                print(f"  {k} = {v}")
            print(f"groups: {', '.join(GROUPS)}")
            print(f"worker: formLookups on at {AD_WORKER}, if it exists")
            return

        url = m365.tofu_output(tofu_dir, "atlas_url")
        admin = m365.tofu_output(tofu_dir, "atlas_admin")
        atlas = m365.Atlas(url, args.cafile)
        atlas.call("POST", "/api/v1/auth/login", {"username": admin["username"], "password": admin["password"]})
        log(f"signed in at {url} as {admin['username']}")

        _, listing = atlas.call("GET", "/api/v1/groups")
        have = {g["name"]: g["id"] for g in m365.items(listing, "groups")}
        for name in GROUPS:
            if name not in have:
                _, g = atlas.call("POST", "/api/v1/groups", {"name": name})
                have[name] = g["id"]
                log(f"group {name} created")
        user_prefix = prefix + "-" if prefix else ""
        _, users = atlas.call("GET", "/api/v1/users")
        for u in m365.items(users, "users"):
            if user_prefix and (u.get("username") or "").startswith(user_prefix) and u.get("email"):
                for name in GROUPS:
                    atlas.call("PUT", f"/api/v1/groups/{have[name]}/members/{u['id']}")
                log(f"{u['username']} joins the transfer groups")

        app_id = install(atlas, pkg, from_hub=args.nach_hub)
    finally:
        shutil.rmtree(work, ignore_errors=True)

    _, held = atlas.call("GET", f"/api/v1/applications/{app_id}/constants")
    keep = [{"name": c["name"], "type": c["type"], "value": c["value"]} for c in (held or {}).get("constants", [])
            if c.get("name") not in constants]
    atlas.call("PUT", f"/api/v1/applications/{app_id}/constants", {"constants": keep + [
        {"name": k, "type": "string", "value": v} for k, v in constants.items()]})
    log(f"constants set: POSTFACH_SYSTEM={postfach}, ENTRA_TRANSFER=aus, NAME_PREFIX={prefix or '(none)'}; "
        f"{len(keep)} other constant(s) kept")

    _, workers = atlas.call("GET", "/api/v1/connectors")
    w = next((c for c in m365.items(workers, "connectors") if (c.get("name") or "").lower() == AD_WORKER), None)
    if not w:
        log(f"worker {AD_WORKER} not found: the order form searches through it; set worker_names = "
            f"{{ ad = \"{AD_WORKER}\" }} or create it in the Console, then run this again")
    elif not w.get("formLookups"):
        atlas.call("PATCH", f"/api/v1/connectors/{w['id']}", {"formLookups": True})
        log(f"worker {AD_WORKER}: «Forms may search» switched on")
    log("done")


if __name__ == "__main__":
    main()
