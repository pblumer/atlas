#!/usr/bin/env python3
"""Installs the directory mirroring (examples/entra-verzeichnis-abgleich.bpmn) for this environment only.

    python3 scripts/install-verzeichnisabgleich.py --cafile out/caddy-root.crt            # dry run
    python3 scripts/install-verzeichnisabgleich.py --cafile out/caddy-root.crt --bericht  # read the report
    python3 scripts/install-verzeichnisabgleich.py --cafile out/caddy-root.crt --apply    # then write

Run from deploy/opentofu/m365-devenv after scripts/wait-atlas.sh. The tenant of this
environment may be shared with real people, and the mirroring's first run reads the whole
tenant. What a job answers is in the event log before the model can drop anything, so the
copy deployed here filters in the Entra Worker (keep, ADR-draft-entra-delta-keep): only
this environment's test users and groups leave the Worker. It

  1. signs in as the local administrator (password from `tofu output -json atlas_admin`,
     never printed);
  2. mints an API token of scope `directory` (it reaches the two directory-sync routes and
     nothing else, and cannot deploy) and seals it in the vault as `verzeichnisabgleich`,
     unless a token of that name exists; the secret is never returned or printed;
  3. writes an environment copy of the model:
       - delta-users: keep="department eq '<env>'", with department added to its select:
         the test users carry the environment id as their department (entra.tf), and so do
         the accounts the HR entry creates (constant DEPARTMENT, install-m365-dienste.py).
         Not the prefix: room, equipment and shared mailboxes and partner accounts carry it
         too, and they are not people who sign in to Atlas;
       - delta-groups: keep="startswith(displayName,'<env>-')" (the test groups' names);
       - both name this environment's Entra Worker (tofu output atlas_workers, kind entra);
       - the two calls to Atlas become REST connector tasks against http://127.0.0.1:8080
         with authSecret="verzeichnisabgleich" (ADR-0411: a `rest` job type with a
         `connector` header is served by nobody, and the Console has no REST Worker);
       - apply stays false unless --apply is given; the process id gets a `_devenv` suffix,
         so a copy imported from the hub is a different process;
  4. deploys it into the application «Verzeichnis-Abgleich (<env>)», creating it if needed;
  5. prints how to read the report, the instance variable `bericht`.

The timer starts the first instance one interval after the deploy (--interval, default
PT1H). While apply is false the cursors never move, so every run reads the tenant again.

Re-running is safe: the token is kept, the application is reused, the model is deployed as
a new version. Needs python3 and tofu; it changes nothing in Azure or the tenant — the
Worker only reads.
"""
import argparse
import json
import os
import re
import sys
import time
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
# The session, tofu_output and the listing helper are those of the m365 installer.
m365 = __import__("install-m365-dienste")

EXAMPLE = os.path.join(HERE, "..", "..", "..", "..", "examples", "entra-verzeichnis-abgleich.bpmn")
PROCESS_ID = "proc_entra_verzeichnis_abgleich"
COPY_ID = PROCESS_ID + "_devenv"
SECRET = "verzeichnisabgleich"
ATLAS_BASE = "http://127.0.0.1:8080"


def log(msg):
    print(f"install-verzeichnisabgleich: {msg}", flush=True)


def keeps(env_id):
    """The two keep predicates of this environment. A literal inside a keep doubles a quote,
    and an environment id never holds one; refuse rather than build a predicate that means
    something else."""
    if not re.fullmatch(r"[A-Za-z0-9._-]+", env_id):
        sys.exit(f"environment id {env_id!r} is not a plain name; refusing to build a keep from it")
    users = f"department eq '{env_id}'"
    groups = f"startswith(displayName,'{env_id}-')"
    return users, groups


def _replace_once(s, old, new, what):
    if s.count(old) != 1:
        sys.exit(f"entra-verzeichnis-abgleich.bpmn: {what} found {s.count(old)} times, want once; has the example changed?")
    return s.replace(old, new)


def _xml_attr(v):
    return v.replace("&", "&amp;").replace('"', "&quot;").replace("<", "&lt;")


def adapt(xml, env_id, entra_worker, apply=False, interval="PT1H"):
    """Returns the environment copy of the model. Pure, so the offline test can hold it."""
    if not re.fullmatch(r"P([0-9]+[YMWD])*(T([0-9]+[HMS])+)?", interval) or interval in ("P", "PT"):
        sys.exit(f"--interval {interval!r} is not an ISO-8601 duration such as PT1H or PT15M")
    users_keep, groups_keep = keeps(env_id)
    s = xml
    n = s.count(PROCESS_ID)
    if n < 2:
        sys.exit("entra-verzeichnis-abgleich.bpmn: the process id is missing; has the example changed?")
    s = s.replace(PROCESS_ID, COPY_ID)
    s = _replace_once(s, 'name="Entra ID: Verzeichnis-Abgleich"',
                      f'name="Entra ID: Verzeichnis-Abgleich ({_xml_attr(env_id)})"', "the process name")
    s = _replace_once(s, "<bpmn:documentation>Führt die atlas-Konten",
                      "<bpmn:documentation>Kopie für die Testumgebung " + env_id +
                      ", eingespielt von deploy/opentofu/m365-devenv/scripts/install-verzeichnisabgleich.py: "
                      "keep an beiden Delta-Tasks, damit im geteilten Mandanten nur die Testkonten und -gruppen "
                      "dieser Umgebung den Entra-Worker verlassen (ADR-draft-entra-delta-keep), und die beiden "
                      "Aufrufe an atlas als REST-Connector-Tasks mit dem Vault-Secret " + SECRET + " (ADR-0411).\n\n"
                      "Führt die atlas-Konten", "the process documentation")
    s = _replace_once(s, "<bpmn:timeCycle>R/PT1H</bpmn:timeCycle>",
                      f"<bpmn:timeCycle>R/{interval}</bpmn:timeCycle>", "the timer cycle")
    if s.count('connector="contoso"') != 2:
        sys.exit("entra-verzeichnis-abgleich.bpmn: the Entra Worker is not named twice; has the example changed?")
    s = s.replace('connector="contoso"', f'connector="{_xml_attr(entra_worker)}"')
    s = _replace_once(s, 'select="id,userPrincipalName,displayName,mail,accountEnabled,manager"',
                      'select="id,userPrincipalName,displayName,mail,accountEnabled,manager,department"\n'
                      f'                              keep="{_xml_attr(users_keep)}"', "the users' select")
    s = _replace_once(s, 'select="id,displayName"',
                      'select="id,displayName"\n'
                      f'                              keep="{_xml_attr(groups_keep)}"', "the groups' select")
    url = ATLAS_BASE + "/api/v1/directory-sync"
    s = _replace_once(s, '''        <zeebe:taskDefinition type="rest" />
        <zeebe:taskHeaders>
          <zeebe:header key="connector" value="atlas" />
          <zeebe:header key="method" value="GET" />
          <zeebe:header key="path" value="/api/v1/directory-sync" />
        </zeebe:taskHeaders>
        <zeebe:ioMapping>
          <zeebe:output source="=body" target="stand" />
        </zeebe:ioMapping>''', f'''        <atlas:restConnector method="GET" url="{url}" resultVariable="stand"
                             authType="bearer" authSecret="{SECRET}" />''', "the GET to directory-sync")
    flag = "true" if apply else "false"
    s = _replace_once(s, '''        <zeebe:taskDefinition type="rest" />
        <zeebe:taskHeaders>
          <zeebe:header key="connector" value="atlas" />
          <zeebe:header key="method" value="POST" />
          <zeebe:header key="path" value="/api/v1/directory-sync" />
        </zeebe:taskHeaders>
        <zeebe:ioMapping>
          <zeebe:input source="={apply: false, fromRevision: stand.revision, users: konten.value, usersDeltaLink: konten.deltaLink, groups: gruppen.value, groupsDeltaLink: gruppen.deltaLink}" target="body" />
          <zeebe:output source="=body" target="bericht" />
        </zeebe:ioMapping>''', f'''        <atlas:restConnector method="POST" url="{url}" resultVariable="bericht"
                             authType="bearer" authSecret="{SECRET}" />
        <zeebe:ioMapping>
          <zeebe:input source="={flag}" target="apply" />
          <zeebe:input source="=stand.revision" target="fromRevision" />
          <zeebe:input source="=konten.value" target="users" />
          <zeebe:input source="=konten.deltaLink" target="usersDeltaLink" />
          <zeebe:input source="=gruppen.value" target="groups" />
          <zeebe:input source="=gruppen.deltaLink" target="groupsDeltaLink" />
        </zeebe:ioMapping>''', "the POST to directory-sync")
    if apply:
        s = _replace_once(s, "apply: false — hier steht der Schalter.",
                          "apply: true — von install-verzeichnisabgleich.py --apply gesetzt.", "the apply documentation")
    return s


def entra_worker_of(workers):
    names = sorted(n for n, k in (workers or {}).items() if k == "entra")
    if len(names) != 1:
        sys.exit(f"this environment has {len(names)} Entra Workers ({', '.join(names) or 'none'}); "
                 "the mirroring needs exactly one (workers.entra in terraform.tfvars)")
    return names[0]


def ensure_token(atlas, env_id, days, fresh):
    name = f"{env_id} verzeichnisabgleich"
    _, listing = atlas.call("GET", "/api/v1/api-tokens")
    have = [t for t in m365.items(listing, "tokens") if isinstance(t, dict) and t.get("name", "").startswith(name)]
    live = [t for t in have if not t.get("expiresAt") or t["expiresAt"] > time.time()]
    if live and not fresh:
        log(f"API token '{live[0]['name']}' exists (scope {live[0].get('scope')}, vault secret {live[0].get('vaultSecret') or SECRET})")
        return
    if have and not live:
        log(f"API token '{name}' has expired; minting a new one")
    if have:
        name = f"{name} {time.strftime('%Y-%m-%d %H:%M')}"
    atlas.call("POST", "/api/v1/api-tokens",
               {"name": name, "scope": "directory", "vaultSecret": SECRET, "expiresInDays": days})
    log(f"API token '{name}' (scope directory, {days} days) minted and sealed in the vault as '{SECRET}'")


def ensure_application(atlas, env_id):
    name = f"Verzeichnis-Abgleich ({env_id})"
    _, apps = atlas.call("GET", "/api/v1/applications")
    hit = next((a for a in m365.items(apps, "applications") if isinstance(a, dict) and a.get("name") == name), None)
    if hit:
        return hit["id"]
    _, created = atlas.call("POST", "/api/v1/applications", {"name": name})
    app_id = (created or {}).get("id") or ((created or {}).get("application") or {}).get("id")
    if not app_id:
        sys.exit(f"POST /api/v1/applications answered without an id: {str(created)[:200]}")
    log(f"application {name} created")
    return app_id


def definitions(atlas):
    _, procs = atlas.call("GET", "/api/v1/processes")
    return [p for p in m365.items(procs, "processes") if isinstance(p, dict) and p.get("processId") == COPY_ID]


def variables(atlas, key):
    _, v = atlas.call("GET", f"/api/v1/instances/{key}/variables")
    if isinstance(v, dict) and isinstance(v.get("variables"), (dict, list)):
        v = v["variables"]
    if isinstance(v, list):
        v = {x.get("name"): x.get("value") for x in v if isinstance(x, dict)}
    return {k: (json.loads(x) if isinstance(x, str) and x[:1] in "[{" else x) for k, x in (v or {}).items()}


def print_report(atlas):
    """Prints the newest finished run's report: its verdict, counts and the kinds of its notes.
    The notes themselves (names of this environment's test users) are left to the Console."""
    runs = []
    for d in definitions(atlas):
        _, page = atlas.call("GET", f"/api/v1/instances?process={d['key']}&state=finished&limit=50&values=preview")
        runs += [i for i in m365.items(page, "items") if isinstance(i, dict)]
    if not runs:
        log("no finished run yet; the timer starts the first one an interval after the deploy")
        return
    run = max(runs, key=lambda i: i.get("completedAt") or 0)
    v = variables(atlas, run["key"])
    b = v.get("bericht")
    if not isinstance(b, dict):
        log(f"instance {run['key']} finished without a bericht; open it in the Console (Operations)")
        return
    log(f"instance {run['key']}: applied={b.get('applied')} mode={b.get('mode')} everApplied={b.get('everApplied')}")
    if b.get("reason"):
        log(f"reason: {b['reason']}")
    log("counts: " + json.dumps(b.get("counts") or {}, ensure_ascii=False, sort_keys=True))
    kinds = {}
    for n in b.get("notes") or []:
        kinds[n.get("kind")] = kinds.get(n.get("kind"), 0) + 1
    log(f"notes by kind: {json.dumps(kinds, sort_keys=True)} (omitted: {b.get('notesOmitted', 0)})")
    for name in ("konten", "gruppen"):
        r = v.get(name)
        if isinstance(r, dict) and "withheld" in r:
            log(f"{name}: withheld by keep {r.get('withheld')}, of those undecided {r.get('undecided')}")


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--tofu-dir", default=os.path.dirname(HERE))
    ap.add_argument("--example", default=EXAMPLE)
    ap.add_argument("--cafile", help="Caddy's root certificate (atlas_tls = internal)")
    ap.add_argument("--apply", action="store_true",
                    help="deploy with apply: true, so the next run writes accounts and groups (read a dry run's bericht first)")
    ap.add_argument("--interval", default="PT1H", help="the timer's interval (default PT1H, the example's)")
    ap.add_argument("--token-days", type=int, default=90, help="lifetime of a newly minted API token (default 90)")
    ap.add_argument("--new-token", action="store_true", help="mint a fresh API token even when one exists")
    ap.add_argument("--show", action="store_true", help="print the environment copy and sign in nowhere")
    ap.add_argument("--bericht", action="store_true", help="print the newest finished run's report and change nothing")
    args = ap.parse_args()
    tofu_dir = os.path.abspath(args.tofu_dir)

    env_id = m365.tofu_output(tofu_dir, "environment_id")
    entra = entra_worker_of(m365.tofu_output(tofu_dir, "atlas_workers"))
    xml = adapt(open(os.path.abspath(args.example), encoding="utf-8").read(), env_id, entra,
                apply=args.apply, interval=args.interval)
    if args.show:
        sys.stdout.write(xml)
        return

    url = m365.tofu_output(tofu_dir, "atlas_url")
    admin = m365.tofu_output(tofu_dir, "atlas_admin")
    atlas = m365.Atlas(url, args.cafile)
    atlas.call("POST", "/api/v1/auth/login", {"username": admin["username"], "password": admin["password"]})
    log(f"signed in at {url} as {admin['username']}")
    if args.bericht:
        print_report(atlas)
        return

    ensure_token(atlas, env_id, args.token_days, args.new_token)
    app_id = ensure_application(atlas, env_id)
    _, dep = atlas.call("POST", "/api/v1/deployments?projectId=" + urllib.parse.quote(app_id),
                        raw=xml.encode("utf-8"), ctype="application/xml")
    for w in (dep or {}).get("warnings", []) or []:
        log(f"deploy warning: {w}")
    log(f"{COPY_ID} version {(dep or {}).get('version')} deployed with Entra Worker {entra}, "
        f"apply={'true' if args.apply else 'false'}, every {args.interval}")
    users_keep, groups_keep = keeps(env_id)
    log(f"delta-users keep: {users_keep}")
    log(f"delta-groups keep: {groups_keep}")
    log("the report: Console → Operations → «Entra ID: Verzeichnis-Abgleich (" + env_id + ")» → the newest "
        "finished instance → Variablen → bericht (applied, reason, counts, notes); konten and gruppen there "
        "say how many objects the keep withheld")
    log("or: python3 scripts/install-verzeichnisabgleich.py" + (f" --cafile {args.cafile}" if args.cafile else "") +
        " --bericht")
    if not args.apply:
        log("nothing is written while apply is false; once the bericht reads right, run again with --apply")


if __name__ == "__main__":
    main()
