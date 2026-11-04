#!/usr/bin/env python3
"""Legt die Architektur des Beispiels m365-dienste in einer Atlas-Instanz an.

    ATLAS_TOKEN=atlasat_… python3 einspielen.py SERVER APPLIKATIONS-ID [--onprem]

Was es tut, in dieser Reihenfolge:
  1. die Capabilities und Wertstroeme aus capabilities.json anlegen oder ersetzen
     (serverweit, ADR-0305). Ohne --onprem fallen die Realisierungen der OnPrem-Variante
     weg, damit der Gap-Report sie nicht als fehlend zaehlt;
  2. das Informationsmodell aus informationsmodell.json fuer die Applikation importieren
     oder ersetzen (ADR-0230). Mit --onprem heisst der Exchange-Speicher «Exchange Server:
     Empfaenger», wie in den Prozessen der OnPrem-Variante.

installieren.sh ruft es zwischen Import und Veroeffentlichung auf: Die Architektur-Skizze
eines Release (ADR-0471, ADR-0495) haelt fest, was beim Veroeffentlichen bekannt ist.
Ein zweiter Lauf ersetzt alles auf der aktuellen Revision. Braucht ein Token der Rolle
modeler und nur die Standardbibliothek.
"""
import json
import os
import sys
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
APP_KEY = "beispiel-m365-dienste"
ONLINE_STORE = "Exchange Online: Empfaenger"
ONPREM_STORE = ("Exchange Server: Empfaenger", "ucc_exchange_onprem")


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    if len(args) != 2:
        sys.exit(__doc__.split("\n\n", 2)[1])
    server, app_id = args[0].rstrip("/"), args[1]
    onprem = "--onprem" in sys.argv
    token = os.environ.get("ATLAS_TOKEN") or sys.exit("ATLAS_TOKEN ist nicht gesetzt")

    def call(method, path, body=None, ok=(200, 201, 204)):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(server + path, data=data, method=method)
        req.add_header("Authorization", "Bearer " + token)
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                text = r.read().decode() or "null"
                return r.status, json.loads(text)
        except urllib.error.HTTPError as e:
            if e.code in ok:
                return e.code, None
            sys.exit(f"{method} {path}: HTTP {e.code} {e.read().decode(errors='replace')[:300]}")

    def put(kind, rec):
        status, cur = call("GET", f"/api/v1/{kind}/{rec['key']}", ok=(200, 404))
        if status == 404:
            call("POST", f"/api/v1/{kind}", rec)
        else:
            call("PUT", f"/api/v1/{kind}/{rec['key']}", dict(rec, revision=cur.get("revision", 0)))

    doc = json.load(open(os.path.join(HERE, "capabilities.json"), encoding="utf-8"))
    for cap in doc["capabilities"]:
        if not onprem:
            cap["realizations"] = [r for r in cap.get("realizations", []) if r.get("applicationKey") != APP_KEY + "-onprem"]
        put("capabilities", cap)
    for vs in doc["valueStreams"]:
        put("value-streams", vs)
    print(f"   {len(doc['capabilities'])} Capabilities, {len(doc['valueStreams'])} Wertstroeme")

    model = json.load(open(os.path.join(HERE, "informationsmodell.json"), encoding="utf-8"))
    if onprem:
        for st in model["stores"]:
            if st["name"] == ONLINE_STORE:
                st["name"], st["worker"] = ONPREM_STORE
        model["name"] += " (Exchange Server)"
    _, listing = call("GET", f"/api/v1/infomodel/models?applicationId={app_id}")
    have = next((m for m in (listing or []) if m.get("name") == model["name"]), None)
    if have is None:
        call("POST", "/api/v1/infomodel/import", {"applicationId": app_id, "name": model["name"],
             "documentation": model["documentation"], "format": "json", "document": json.dumps(model, ensure_ascii=False)})
        print(f"   Informationsmodell «{model['name']}» angelegt")
    else:
        _, cur = call("GET", f"/api/v1/infomodel/models/{have['id']}")
        call("PUT", f"/api/v1/infomodel/models/{have['id']}", dict(model, revision=cur.get("revision", 0)))
        print(f"   Informationsmodell «{model['name']}» ersetzt")


if __name__ == "__main__":
    main()
