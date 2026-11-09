#!/usr/bin/env python3
"""Installs examples/ad-dienste into the Atlas server of this environment, ready to order.

    python3 scripts/install-ad-dienste.py --cafile out/caddy-root.crt
    python3 scripts/install-ad-dienste.py --dry-run [--test-ou OU=AtlasTest,DC=atlastest,DC=internal]

Run from deploy/opentofu/m365-devenv after `tofu apply` (with enable_ad_dc) and
scripts/wait-atlas.sh. It

  1. derives the directory from the environment as the bootstrap does: the test OU is
     `tofu output -json ad_test_ou` (OU=AtlasTest under the DN of ad_domain_name, ad.tf),
     the domain DN is its DC= part, the UPN suffix the tenant's initial domain (as
     MAIL_DOMAIN of install-m365-dienste.py);
  2. copies the package into a temporary directory and adapts it: every DC=contoso,DC=local
     of the tables AdBerechtigungen and AdOe (and of the constants' fallbacks in the
     processes) becomes the test OU, because the bind account may write there only and a DN
     outside the forest is answered with an LDAP referral; a contoso DN left in anything it
     uploads stops it before anything is uploaded;
  3. signs in as the local administrator (password from `tofu output -json atlas_admin`,
     never printed) and creates the Atlas groups the package addresses (M365-Mitarbeitende,
     Informationssicherheit, AD-Administration, Identity Management, Mainframe); test persons
     who have signed in join them, as in install-m365-dienste.py;
  4. uploads the three decision models, imports and publishes the application and imports
     the catalogue with its three group placeholders filled;
  5. sets the application constants AD_* and ATLAS_API_BASE, keeping any other constant
     held for the application;
  6. switches «Forms may search» (formLookups) on at the worker ad_verzeichnis, if it exists.

The OUs and groups the tables and constants name are created by scripts/ad-setup.ps1
(phase 2, or phase 3 on a forest built before). --dry-run adapts the package and prints what
would be uploaded, without signing in. Re-running is safe. Needs python3 and tofu; it changes
nothing in Azure, the tenant or the directory.
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
# The session, the listing helper and the capability-map decision are those of the m365 installer.
m365 = __import__("install-m365-dienste")

APP_KEY = "beispiel-ad-dienste"
WORKER = "ad_verzeichnis"
MODELS = ["ad-berechtigungen", "ad-namensregel", "ad-oe"]
# Tables whose DNs are examples of the package (README, Voraussetzungen 7).
TABLES = ["ad-berechtigungen", "ad-oe"]
EXAMPLE_BASE = "DC=contoso,DC=local"
CONTOSO_DN = re.compile(r"DC=contoso", re.IGNORECASE)
GROUPS = {
    # answer in fragen.json -> Atlas group
    "zielgruppe-ad": "M365-Mitarbeitende",
    "informationssicherheit": "Informationssicherheit",
    "ad-administration": "AD-Administration",
}
IM_GROUP = "Identity Management"
MAINFRAME_GROUP = "Mainframe"
# Constant -> OU below OU=Atlas,<test OU> (scripts/ad-setup.ps1 creates them).
ATLAS_OUS = {
    "AD_GRUPPEN_OU": "Gruppen", "AD_ADMIN_OU": "Admins", "AD_SERVICE_OU": "Servicekonten",
    "AD_TEST_OU": "Testkonten", "AD_EXTERN_OU": "Extern", "AD_KONTAKT_OU": "Kontakte",
    "AD_FUNKTION_OU": "Funktionskonten", "AD_ROLLEN_OU": "Rollenkonten", "AD_SCHULUNG_OU": "Schulungskonten",
}


def log(msg):
    print(f"install-ad-dienste: {msg}", flush=True)


def domain_dn_of(test_ou):
    """The DC= part of a DN: OU=AtlasTest,DC=atlastest,DC=internal -> DC=atlastest,DC=internal."""
    parts = [p.strip() for p in test_ou.split(",")]
    dcs = [p for p in parts if p.upper().startswith("DC=")]
    if not dcs or parts[-len(dcs):] != dcs:
        sys.exit(f"ad_test_ou {test_ou!r} does not end in DC= components")
    return ",".join(dcs)


def constants_for(test_ou, upn_suffix):
    atlas = f"OU=Atlas,{test_ou}"
    c = {"AD_DOMAIN_DN": domain_dn_of(test_ou), "AD_UPN_SUFFIX": upn_suffix, "AD_PERSONEN_OU": test_ou}
    c.update({name: f"OU={ou},{atlas}" for name, ou in ATLAS_OUS.items()})
    c.update({"AD_IM_GRUPPE": IM_GROUP, "AD_MAINFRAME_GRUPPE": MAINFRAME_GROUP,
              "ATLAS_API_BASE": "http://127.0.0.1:8080"})
    return c


def uploaded_files(pkg):
    """Every file of the package this installer sends: the source tree, the models, the catalogue."""
    man = json.load(open(os.path.join(pkg, "atlas.json"), encoding="utf-8"))
    tree = ["atlas.json"] + [p["path"] for p in man["processes"]] + [f["path"] for f in man["forms"]] + \
        sorted({d["path"] for d in man["decisions"] if d.get("path")}) + \
        [m["path"] for m in man.get("informationModels", [])] + ([man["architecture"]] if man.get("architecture") else [])
    return tree, sorted(set(tree) | {m + ".dmn" for m in MODELS} | {"katalog.json"})


def adapt(src, dst, test_ou, constants):
    """Copies the package and puts its directory under the test OU. Returns the DNs the tables name."""
    shutil.copytree(src, dst)
    for m in TABLES:
        p = os.path.join(dst, m + ".dmn")
        s = open(p, encoding="utf-8").read()
        if EXAMPLE_BASE not in s:
            sys.exit(f"{m}.dmn: {EXAMPLE_BASE} not found; has the package changed?")
        open(p, "w", encoding="utf-8").write(s.replace(EXAMPLE_BASE, test_ou))
    # The processes declare the constants with the package's example values as fallback; they
    # get this environment's values, so a process run before the constants are set (or after
    # one is removed) still stays in the test OU.
    pat = re.compile(r'(<atlas:constant name="([A-Z_]+)"[^>]*?\bvalue=")([^"]*)(")')
    _, files = uploaded_files(dst)
    for f in files:
        p = os.path.join(dst, f)
        s = open(p, encoding="utf-8").read()
        if f.endswith(".bpmn"):
            s = pat.sub(lambda m: m.group(1) + (constants.get(m.group(2), m.group(3))) + m.group(4), s)
        s = s.replace(EXAMPLE_BASE, test_ou)
        open(p, "w", encoding="utf-8").write(s)
    left = []
    for f in files:
        for n, line in enumerate(open(os.path.join(dst, f), encoding="utf-8"), 1):
            if CONTOSO_DN.search(line):
                left.append(f"{f}:{n}: {line.strip()[:160]}")
    if left:
        sys.exit("a contoso DN is left in what would be uploaded:\n  " + "\n  ".join(left))
    dns = set()
    for m in TABLES:
        s = open(os.path.join(dst, m + ".dmn"), encoding="utf-8").read()
        dns |= set(re.findall(r'"((?:CN|OU)=[^"]*?' + re.escape(test_ou) + r')"', s))
    return sorted(dns)


def install(atlas, pkg):
    for m in MODELS:
        atlas.call("POST", f"/api/v1/dmn-models?handle={m}", raw=open(os.path.join(pkg, m + ".dmn"), "rb").read(),
                   ctype="application/xml")
    log("decision models uploaded: " + ", ".join(MODELS))
    tree_files, _ = uploaded_files(pkg)
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for f in tree_files:
            tar.add(os.path.join(pkg, f), arcname=f)
    tree = buf.getvalue()
    _, plan = atlas.call("POST", "/api/v1/applications/source?dryRun=true", raw=tree, ctype="application/gzip")
    _, res = atlas.call("POST", "/api/v1/applications/source" + m365.gap_decisions(plan), raw=tree, ctype="application/gzip")
    app_id = res["applicationId"]
    _, pub = atlas.call("POST", f"/api/v1/applications/{app_id}/publish", {"note": "install-ad-dienste.py"})
    log(f"application {app_id} imported and published, release {(pub.get('release') or {}).get('version')}")

    _, principals = atlas.call("GET", "/api/v1/principals")
    fragen = json.load(open(os.path.join(pkg, "fragen.json"), encoding="utf-8"))
    raw = open(os.path.join(pkg, "katalog.json"), encoding="utf-8").read()

    def fill(m):
        name = m.group(1)
        if name not in GROUPS or name not in fragen:
            sys.exit(f"{{{{{name}}}}}: no answer in this installer or no question in fragen.json; has the package changed?")
        kind = fragen[name]["kind"]
        hit = [p["id"] for p in m365.items(principals, "principals") if p.get("type") == kind and p.get("name") == GROUPS[name]]
        if len(hit) != 1:
            sys.exit(f"{{{{{name}}}}}: group {GROUPS[name]} found {len(hit)} times")
        return hit[0]
    atlas.call("POST", "/api/v1/catalogs/import", raw=re.sub(r"\{\{([a-z0-9-]+)\}\}", fill, raw).encode("utf-8"))
    log("catalogue imported and published")
    return app_id


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--tofu-dir", default=os.path.dirname(HERE))
    ap.add_argument("--example", default=os.path.join(HERE, "..", "..", "..", "..", "examples", "ad-dienste"))
    ap.add_argument("--cafile", help="Caddy's root certificate (atlas_tls = internal)")
    ap.add_argument("--test-ou", help="the test OU (default: tofu output ad_test_ou)")
    ap.add_argument("--upn-suffix", help="AD_UPN_SUFFIX (default: the tenant's initial domain)")
    ap.add_argument("--dry-run", action="store_true", help="adapt the package and print what would be uploaded; sign in nowhere")
    args = ap.parse_args()
    tofu_dir = os.path.abspath(args.tofu_dir)

    test_ou = args.test_ou or m365.tofu_output(tofu_dir, "ad_test_ou")
    if not test_ou:
        sys.exit("No domain controller (ad_test_ou is null): the package needs enable_ad_dc = true.")
    upn_suffix = args.upn_suffix or (m365.tofu_output(tofu_dir, "tenant") or {}).get("initial_domain")
    if not upn_suffix:
        sys.exit("No UPN suffix: pass --upn-suffix.")
    constants = constants_for(test_ou, upn_suffix)

    work = tempfile.mkdtemp(prefix="ad-dienste-")
    try:
        pkg = os.path.join(work, "ad-dienste")
        dns = adapt(os.path.abspath(args.example), pkg, test_ou, constants)
        log(f"tables put under {test_ou}; they name {len(dns)} directory objects")
        if args.dry_run:
            _, files = uploaded_files(pkg)
            print("would upload: " + ", ".join(files))
            print("directory objects the tables name (scripts/ad-setup.ps1 creates them):")
            for dn in dns:
                print("  " + dn)
            print("constants:")
            for k, v in constants.items():
                print(f"  {k} = {v}")
            print(f"groups: {', '.join(sorted(set(GROUPS.values()) | {IM_GROUP, MAINFRAME_GROUP}))}")
            print(f"worker: formLookups on at {WORKER}, if it exists")
            return

        url = m365.tofu_output(tofu_dir, "atlas_url")
        admin = m365.tofu_output(tofu_dir, "atlas_admin")
        shared = m365.tofu_output(tofu_dir, "shared_tenant")
        prefix = (shared or {}).get("prefix", "")
        prefix = prefix + "-" if prefix else ""
        atlas = m365.Atlas(url, args.cafile)
        atlas.call("POST", "/api/v1/auth/login", {"username": admin["username"], "password": admin["password"]})
        log(f"signed in at {url} as {admin['username']}")

        names = sorted(set(GROUPS.values()) | {IM_GROUP, MAINFRAME_GROUP})
        _, listing = atlas.call("GET", "/api/v1/groups")
        have = {g["name"]: g["id"] for g in m365.items(listing, "groups")}
        for name in names:
            if name not in have:
                _, g = atlas.call("POST", "/api/v1/groups", {"name": name})
                have[name] = g["id"]
                log(f"group {name} created")
        # Approvers and the audience need an address; the test persons' Atlas accounts carry
        # their Entra mail once they have signed in (install-m365-dienste.py).
        _, users = atlas.call("GET", "/api/v1/users")
        for u in m365.items(users, "users"):
            if prefix and (u.get("username") or "").startswith(prefix) and u.get("email"):
                for name in names:
                    atlas.call("PUT", f"/api/v1/groups/{have[name]}/members/{u['id']}")
                log(f"{u['username']} joins the AD groups")

        app_id = install(atlas, pkg)
    finally:
        shutil.rmtree(work, ignore_errors=True)

    _, held = atlas.call("GET", f"/api/v1/applications/{app_id}/constants")
    keep = [{"name": c["name"], "type": c["type"], "value": c["value"]} for c in (held or {}).get("constants", [])
            if c.get("name") not in constants]
    atlas.call("PUT", f"/api/v1/applications/{app_id}/constants", {"constants": keep + [
        {"name": k, "type": "string", "value": v} for k, v in constants.items()]})
    log(f"constants set: AD_DOMAIN_DN={constants['AD_DOMAIN_DN']}, AD_PERSONEN_OU={test_ou}, "
        f"AD_UPN_SUFFIX={upn_suffix}, OUs under OU=Atlas,{test_ou}; {len(keep)} other constant(s) kept")

    _, workers = atlas.call("GET", "/api/v1/connectors")
    w = next((c for c in m365.items(workers, "connectors") if (c.get("name") or "").lower() == WORKER), None)
    if not w:
        log(f"worker {WORKER} not found: the package's processes address it; set worker_names = "
            f"{{ ad = \"{WORKER}\" }} or create it in the Console (README, «Inhalt»), then run this again")
    elif w.get("formLookups"):
        log(f"worker {WORKER}: «Forms may search» is already on")
    else:
        atlas.call("PATCH", f"/api/v1/connectors/{w['id']}", {"formLookups": True})
        log(f"worker {WORKER}: «Forms may search» switched on (the forms' person fields search through it)")
    log("done")


if __name__ == "__main__":
    main()
