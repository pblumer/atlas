#!/usr/bin/env python3
"""Erzeugt spop-teamsite.bpmn (Prozess proc_spop_teamsite): python3 tools/gen_bpmn.py spop-teamsite.bpmn (aus examples/spop).

Ein einziger Strang je Position (Muster lc_proc_m365_funktionspostfach), Änderungen als Subprozesse. Sechs Katalogprodukte
(Site-Typen) binden diesen einen Prozess; der Typ kommt aus itemId.
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from bpmnlib import (KEY, REASON_FREIGABE, REST_DOC, IU, brt, change_sub, check_block, ext, flow, mock, node,  # noqa: E402
                     rest_decision, rest_status, script, shop, spbox, write)

OUT = sys.argv[1]

# ======================================================================= Bereitstellung (GF-001), flach
M = 700
U, L1 = -150, 150
node("s_neu", "startEvent", "Site bestellt", 118, M, '<messageEventDefinition messageRef="m_bestellen"/>',
     doc="GF-001: Start der Operation provision. Sechs Produkte binden diesen Prozess, je Site-Typ eines (spop-site-hermes, "
         "-communication, -publishing, -teamsite-classic, -teamsite-modern, -wiki); der Typ kommt aus itemId. Formular "
         "spop-teamsite-bestellung: titel, amtKuerzel, kurzname, sprache, ownerUpn, memberUpns, quotaWahl, quotaMb, "
         "quotaBegruendung, bemerkung. Vom Katalog kommen orderId, positionId, itemId; sie bilden den Korrelationsschlüssel.")
node("a_regel", "businessRuleTask", "Richtlinien prüfen (S6)", 250, M,
     brt("SpopTeamsiteRichtlinie", "regel",
         ('=amtKuerzel != null and matches(amtKuerzel, "^[A-Z]{2,6}$")', "amtGueltig"),
         ('=kurzname != null and matches(kurzname, "^[a-z0-9][a-z0-9-]{2,29}$")', "kurznameGueltig"),
         ("=itemId", "produkt"),
         ('=if quotaWahl = null then "Standard" else quotaWahl', "quotaWahl"),
         ("=if quotaMb = null then 0 else number(string(quotaMb))", "quotaMb"),
         ("=quotaBegruendung != null and string length(quotaBegruendung) >= 10", "begruendungVorhanden")),
     doc="DMN SpopTeamsiteRichtlinie anstelle von amt_naming.rego, quota_limits.rego und approval.rego. Bildet das Produkt auf "
         "den Site-Typ ab und liefert regel = {zulaessig, vierAugen, quotaEffektivMb, siteTyp, template, features, material, grund}. "
         "Vorlagen, Grenzwerte und Freigabepflicht je Typ sind Annahmen und im DMN gepflegt. Die Antworten eines Bestellformulars "
         "kommen als Text an; quotaMb wird deshalb vor der Regel in eine Zahl umgewandelt.")
join, jx = check_block(
    "a", 400, M, "a_regel",
    "Ablehnung melden", rest_decision("Richtlinie SpopTeamsiteRichtlinie", "=regel.grund"),
    "Meldet die Ablehnung über POST /api/v1/orders/{orderId}/lines/{itemId}/decision mit dem Grund der Richtlinie. Eine "
    "verletzte Richtlinie ist eine Entscheidung, keine Störung." + REST_DOC,
    '="Site-Bestellung (GF-001)\\nTyp: " + regel.siteTyp + " (Material " + regel.material + ")\\nAmt: " + amtKuerzel + '
    '"\\nURL: /sites/" + lower case(amtKuerzel) + "-" + kurzname + "\\nTitel: " + titel + "\\nOwner: " + ownerUpn + '
    '"\\nQuota: " + string(regel.quotaEffektivMb) + " MB (" + quotaWahl + ")" + '
    '(if quotaBegruendung = null then "" else "\\nBegründung: " + quotaBegruendung) + "\\nRegel: " + regel.grund',
    "Bestellung freigeben",
    "Vier-Augen-Freigabe durch die Gruppe SPoP-Freigabe (Service Owner DO-COC-SPC), Formular spop-freigabe.",
    rest_decision("Vier-Augen-Freigabe", REASON_FREIGABE),
    "Meldet die verweigerte Vier-Augen-Freigabe über die Decision-Route der Position, mit dem Kommentar als Grund." + REST_DOC)
node("a_owner", "serviceTask", "Owner im AD prüfen", 1220, M, ext(mock('={ existiert: true, upn: ownerUpn }', "ownerPruefung")),
     doc="Simuliert apsbw_ad_user read.")
node("a_site", "serviceTask", "Site erstellen (S1)", 1370, M,
     ext(mock('={ url: "/sites/" + lower case(amtKuerzel) + "-" + kurzname, titel: titel, typ: regel.siteTyp, template: regel.template, '
              'material: regel.material, sprache: if sprache = null then "1031" else sprache }', "site", "PT2S", "PT4S")),
     doc="Simuliert apsbw_sharepoint_site create mit der Vorlage des Site-Typs (Worker sharepointserver: create-site). URL "
         "/sites/<amt>-<kurzname>. Schreibt site (url, typ, template, material); der Strang trägt site bis zur Rückgabe.")
node("a_par", "parallelGateway", "", 1500, M)
node("a_grp", "serviceTask", "Gruppen anlegen (S2–S4)", 1630, M + U,
     ext(mock('={ owners: site.url + " Owners", members: site.url + " Members", visitors: site.url + " Visitors", owner: ownerUpn, mitglieder: memberUpns }', "gruppen")),
     doc="Simuliert apsbw_sharepoint_group create und add_member.")
node("a_rechte", "serviceTask", "Berechtigungen zuweisen (S5)", 1780, M + U,
     ext(mock('={ owners: "Full Control", members: "Contribute", visitors: "Read" }', "berechtigungen")),
     doc="Simuliert apsbw_sharepoint_roleassignment create.")
node("a_quota", "serviceTask", "Quota setzen (S7)", 1630, M,
     ext(mock('={ siteUrl: site.url, basisMb: regel.quotaEffektivMb, zusatzGb: 0, storageMb: regel.quotaEffektivMb }', "quota")),
     doc="Simuliert apsbw_sharepoint_quota apply (Worker sharepointserver: set-site-quota). Schreibt quota: die inbegriffene "
         "Quota des Site-Typs (basisMb), den Zusatzspeicher (zusatzGb, anfangs 0) und die gesetzte Quota (storageMb). Die "
         "Speicherplatzerweiterung liest und schreibt quota fort.")
node("a_feat", "serviceTask", "Features aktivieren (S8)", 1630, M + L1,
     ext(mock('={ siteUrl: site.url, features: regel.features }', "features")),
     doc="Simuliert apsbw_sharepoint_feature activate mit den Features des Site-Typs aus der Richtlinie (Annahme).")
node("a_parj", "parallelGateway", "", 1910, M)
node("a_bill", "serviceTask", "Verrechnung erfassen", 2170, M,
     ext(mock('={ material: site.material, menge: 1, einheit: "Site", siteUrl: site.url, position: orderId + "/" + (if positionId = null then itemId else positionId) }', "verrechnung")),
     doc="Simuliert die Erfassung der wiederkehrenden Verrechnung (Marktleistungsstruktur ML Redesign SharePoint) mit der "
         "Materialnummer des Site-Typs.")
node("a_info", "serviceTask", "Position als erledigt melden", 2300, M, rest_status("done"),
     doc="Meldet die Position über POST /api/v1/orders/{orderId}/lines/{positionId} als erledigt (status done); erst damit "
         "steht die Site im Bestand und die Aktionen werden möglich." + REST_DOC)

# ======================================================================= Warten auf Aufträge
# Jede Aktion ist ein Zwischenereignis direkt am ereignisbasierten Gateway, gefolgt von ihrem Subprozess. Die Einladung
# liegt oberhalb und kehrt über die Oberkante zurück. Die übrigen Aktionen stehen untereinander; jeder Subprozess mündet
# rechts in den gemeinsamen Rückweg j_zurueck, der unter allen hindurch zum Warten führt. So kreuzt kein Rückweg die
# Verzweigung des Gateways. Die Rückgabe geht nach rechts und endet den Strang; sie braucht keinen Rückweg.
JX, GX, CX, SPX = 2430, 2590, 2700, 2750
W = 1750                      # Breite jedes Änderungs-Subprozesses unterhalb
XR = SPX + W + 60             # Spalte des gemeinsamen Rückwegs
node("j_aktiv", "exclusiveGateway", "", JX, M)
node("g_warten", "eventBasedGateway", "Aktiv: auf Auftrag warten", GX, M, label="custom", lpos=(JX + 25, M - 62, 100, 27),
     doc="Die Site ist in Betrieb. Der Strang wartet auf die nächste Aktion der Position: Einladung, Speicher, Erweitertes "
         "Reporting, SubSite Quota, Umbenennung, URL-Änderung oder Berechtigungen. Jede Aktion ist an orderId/positionId "
         "korreliert. Während eine Aktion läuft, wartet der Strang nicht; eine zweite Aktion derselben Position wird erst nach "
         "Abschluss der ersten angenommen; das gilt auch für die Rückgabe.")
node("c_extern", "intermediateCatchEvent", "Einladung angefragt", CX, 330, '<messageEventDefinition messageRef="m_extern"/>',
     doc="GF-003: Aktion extern-einladen (Formular spop-extern-einladen: email, rolle, ablaufTage, nachricht).", lw=90)

# ----------------------------------------------------------------------- Subprozess Einladung (GF-003), oberhalb
EY = 330
X = change_sub(
    SPX, "x", "sp_extern", "extern-einladen", "Externen Benutzer einladen (GF-003)", EY, 1970,
    "GF-003 SP OnPrem Invite External User als Subprozess des Strangs. Prüft Site und Richtlinie, holt bei Contribute oder "
    "Full Control die Vier-Augen-Freigabe, lädt ein und setzt die Berechtigung erst nach der Annahme. Läuft die Gültigkeit "
    "ab, wird die Einladung widerrufen. Bis dahin nimmt der Strang keine weitere Aktion an. Output-Mapping externe: hängt "
    "eine angenommene Einladung an die Liste der externen Benutzer des Strangs an; die Rückgabe entfernt diese Zugriffe.",
    [('=if berechtigung = null then externe else append(if externe = null then [] else externe, { email: email, rolle: rolle })', "externe")],
    "Site prüfen (S1)",
    '={ gefunden: site != null, siteUrl: site.url }',
    "Simuliert apsbw_sharepoint_site read aus dem Stand des Strangs (site). Schreibt aktuell.",
    "Externe-Benutzer-Richtlinie prüfen (S4)", "SpopExternRichtlinie",
    [("=aktuell.gefunden", "siteGefunden"),
     ('=if email = null then "" else lower case(substring after(email, "@"))', "emailDomain"),
     ("=rolle", "rolle"), ("=if ablaufTage = null then 30 else number(string(ablaufTage))", "ablaufTage")],
    "DMN SpopExternRichtlinie anstelle von external_user.rego, mfa_required.rego und approval.rego.",
    '="Externe Einladung (GF-003)\\nSite: " + site.url + "\\nE-Mail: " + email + "\\nRolle: " + rolle + '
    '"\\nGültigkeit: " + string(ablaufTage) + " Tage\\nRegel: " + regel.grund',
    "Einladung freigeben")
SX = dict(parent="sp_extern")
node("x_invite", "serviceTask", "Externen Benutzer einladen (S2)", X(1260), EY,
     ext(mock('={ einladungId: site.url + "|" + lower case(email), email: email, mfaPflicht: regel.mfaPflicht, gueltigTage: ablaufTage }', "einladung")),
     doc="Simuliert apsbw_sharepoint_externaluser invite und die Gast-Anlage (apsbw_ad_user create). Der Worker sharepointserver "
         "hat dafür keine Operation. Schreibt einladung.einladungId, den Korrelationsschlüssel der Annahme.", **SX)
node("x_wait", "eventBasedGateway", "Annahme oder Ablauf", X(1390), EY,
     doc="Wartet auf die Annahme (Nachricht spop.extern.angenommen, Schlüssel einladungId) oder den Ablauf der Gültigkeit.", **SX)
node("x_ang", "intermediateCatchEvent", "Einladung angenommen", X(1500), EY, '<messageEventDefinition messageRef="m_angenommen"/>', **SX)
node("x_rechte", "serviceTask", "Berechtigung zuweisen (S3)", X(1630), EY,
     ext(mock('={ siteUrl: site.url, email: email, rolle: rolle, mfaPflicht: true }', "berechtigung")),
     doc="Simuliert apsbw_sharepoint_roleassignment create; Vorbedingung: Einladung angenommen.", **SX)
node("x_info", "sendTask", "Aktion als erledigt melden", X(1780), EY, shop("extern-einladen", "completed"),
     doc="Shop-Aufgabe (Modus outcome): beantwortet die Aktion extern-einladen mit completed.", **SX)
node("x_end", "endEvent", "Externer Zugriff aktiv", X(1910), EY, **SX)
node("x_ablauf", "intermediateCatchEvent", "Einladung abgelaufen", X(1500), EY + IU,
     '<timerEventDefinition><timeDuration>="P" + string(ablaufTage) + "D"</timeDuration></timerEventDefinition>', **SX)
node("x_widerruf", "serviceTask", "Einladung widerrufen", X(1630), EY + IU,
     ext(mock('={ einladungId: einladung.einladungId, status: "widerrufen" }', "widerruf")),
     doc="Simuliert apsbw_sharepoint_externaluser remove nach Ablauf.", **SX)
node("x_info2", "sendTask", "Aktion als fehlgeschlagen melden", X(1780), EY + IU, shop("extern-einladen", "failed"),
     doc="Shop-Aufgabe (Modus outcome): beantwortet die Aktion extern-einladen mit failed; die Einladung wurde nicht angenommen und ist widerrufen.", **SX)
node("x_end2", "endEvent", "Einladung verfallen", X(1910), EY + IU, **SX)
flow("x_f10", "x_join", "x_invite", **SX)
flow("x_f11", "x_invite", "x_wait", **SX)
flow("x_f12", "x_wait", "x_ang", **SX)
flow("x_f13", "x_ang", "x_rechte", **SX)
flow("x_f14", "x_rechte", "x_info", **SX)
flow("x_f15", "x_info", "x_end", **SX)
flow("x_f16", "x_wait", "x_ablauf", "t", "l", **SX)
flow("x_f17", "x_ablauf", "x_widerruf", **SX)
flow("x_f18", "x_widerruf", "x_info2", **SX)
flow("x_f19", "x_info2", "x_end2", **SX)


ROWS = []                     # (Catch-Id, Subprozess-Id, y) der Aktionen unterhalb, von oben nach unten


def action_row(i, cid, cname, msg, cdoc):
    y = 960 + 630 * i
    node(cid, "intermediateCatchEvent", cname, CX, y, f'<messageEventDefinition messageRef="{msg}"/>', doc=cdoc, lw=90)
    return y


def merged(var, part="neu", empty="null"):
    """Setzt die lokalen Felder eines Laufs beim Abschluss in den Stand des Strangs ein (context merge)."""
    base = var if empty == "null" else f"(if {var} = null then {empty} else {var})"
    return f"=if {part} = null then {var} else context merge([{base}, {part}])"


def finish(p, sid, X, y, apply_name, apply_inner, apply_doc, action, end_name, extra=()):
    """Ausführung, (weitere Schritte), Shop-Meldung und Ende hinter f"{p}_join"."""
    P = dict(parent=sid)
    node(f"{p}_apply", "serviceTask", apply_name, X(1260), y, apply_inner, doc=apply_doc, **P)
    prev, x = f"{p}_apply", 1410
    for k, (nid, name, inner, doc) in enumerate(extra):
        node(nid, "serviceTask", name, X(x), y, inner, doc=doc, **P)
        flow(f"{p}_fx{k}", prev, nid, **P)
        prev, x = nid, x + 150
    node(f"{p}_info", "sendTask", "Aktion als erledigt melden", X(x), y, shop(action, "completed"),
         doc=f"Shop-Aufgabe (Modus outcome): beantwortet die Aktion {action} mit completed.", **P)
    node(f"{p}_end", "endEvent", end_name, X(x + 130), y, **P)
    flow(f"{p}_f10", f"{p}_join", f"{p}_apply", **P)
    flow(f"{p}_f11", prev, f"{p}_info", **P)
    flow(f"{p}_f12", f"{p}_info", f"{p}_end", **P)


MERGE_DOC = (" Die Ausführung schreibt nur die geänderten Felder in die lokale Variable neu (Ergebnisse innerer Aufgaben bleiben "
             "im Subprozess, ADR-0074); das Output-Mapping setzt sie beim Abschluss mit context merge in den Stand des Strangs "
             "ein. Endet der Subprozess mit einer Ablehnung, ist neu leer und der Stand bleibt.")

# ----------------------------------------------------------------------- Speicherplatz erweitern (GF-002)
GB = 'if menge = null then 0 else number(string(menge))'
y = action_row(0, "c_quota", "Speichererweiterung angefragt", "m_quota",
               "GF-002: Aktion quota-erhoehen, im Shop «Speicherplatz erweitern» (Formular spop-quota-erhoehung: menge = "
               "Zusatzspeicher in GB, begruendung).")
X = change_sub(
    SPX, "q", "sp_quota", "quota-erhoehen", "Speicherplatz erweitern (GF-002)", y, W,
    "GF-002 SP OnPrem Quota-Erhöhung als Speicherplatzerweiterung (Material 540007510, Verrechnung pro GB und Monat): Die "
    "Aktion nennt die neue Gesamtmenge Zusatzspeicher in GB. Die Site behält ihre inbegriffene Quota (quota.basisMb); die "
    "gesetzte Quota ist basisMb + zusatzGb × 1024. Richtlinie SpopZusatzRichtlinie mit dem Produktschlüssel spop-speicher: "
    "1 bis 500 GB, Vier-Augen-Prinzip über 100 GB." + MERGE_DOC,
    [(merged("quota"), "quota")],
    "Site und Speicher lesen (S1)",
    '={ gefunden: site != null, basisMb: if quota.basisMb = null then quota.storageMb else quota.basisMb, '
    'gb: if quota.zusatzGb = null then 0 else quota.zusatzGb }',
    "Simuliert apsbw_sharepoint_site read und apsbw_sharepoint_quota read aus dem Stand des Strangs (site, quota). Schreibt aktuell.",
    "Speicher-Richtlinie prüfen (S3)", "SpopZusatzRichtlinie",
    [("=aktuell.gefunden", "siteGefunden"), ('="spop-speicher"', "produkt"),
     (f"={GB}", "menge"), (f"=({GB}) = aktuell.gb", "unveraendert")],
    "DMN SpopZusatzRichtlinie mit dem Produktschlüssel spop-speicher: zulässige Menge, Material 540007510, Vier-Augen-Prinzip "
    "über 100 GB.",
    '="Speicherplatzerweiterung (GF-002, Material " + regel.material + ")\\nSite: " + site.url + " (" + site.typ + ")\\n'
    'Zusatzspeicher bisher: " + string(aktuell.gb) + " GB\\nNeu: " + string(menge) + " GB" + '
    '(if begruendung = null then "" else "\\nBegründung: " + begruendung) + "\\nRegel: " + regel.grund',
    "Speichererweiterung freigeben")
finish("q", "sp_quota", X, y, "Speicher setzen (S2)",
       ext(mock('={ zusatzGb: number(string(menge)), zusatzGbVorher: aktuell.gb, '
                'storageMb: aktuell.basisMb + number(string(menge)) * 1024, material: regel.material }', "neu")),
       "Simuliert apsbw_sharepoint_quota apply (Worker sharepointserver: set-site-quota) mit basisMb + zusatzGb × 1024 und die "
       "Umstellung der Verrechnung auf zusatzGb × Material 540007510; zusatzGbVorher bleibt für einen Rollback erhalten.",
       "quota-erhoehen", "Speicher erweitert")
ROWS.append(("c_quota", "sp_quota", y))


# ----------------------------------------------------------------------- Zusatzleistungen: Erweitertes Reporting, SubSite Quota
def zusatz_row(i, p, cid, cname, msg, sid, name, action, key, produkt, label, default, einheit, form, doc, ut_name, end_name):
    menge = 'if menge = null then 0 else number(string(menge))'
    y = action_row(i, cid, cname, msg, f"Aktion {action} (Formular {form}: menge = neue Gesamtmenge, begruendung).")
    X = change_sub(
        SPX, p, sid, action, name, y, W, doc + MERGE_DOC,
        [(merged("zusatz", empty="{}"), "zusatz")],
        "Site und Zusatzleistung lesen (S1)",
        f'={{ gefunden: site != null, menge: if zusatz = null then {default} else '
        f'(if zusatz.{key} = null then {default} else zusatz.{key}.menge) }}',
        f"Simuliert apsbw_sharepoint_site read und liest den Stand der Zusatzleistung aus zusatz.{key}. Schreibt aktuell.",
        f"{label}-Richtlinie prüfen", "SpopZusatzRichtlinie",
        [("=aktuell.gefunden", "siteGefunden"), (f'="{produkt}"', "produkt"),
         (f"={menge}", "menge"), (f"=({menge}) = aktuell.menge", "unveraendert")],
        f"DMN SpopZusatzRichtlinie mit dem Produktschlüssel {produkt}: zulässige Menge, Verrechnungsband und Materialnummer.",
        f'="{label}\\nSite: " + site.url + "\\nBisher: " + string(aktuell.menge) + " {einheit}\\nNeu: " + string(menge) + '
        f'" {einheit}" + (if begruendung = null then "" else "\\nBegründung: " + begruendung) + "\\nRegel: " + regel.grund',
        ut_name)
    finish(p, sid, X, y, "Zusatzleistung setzen (S2)",
           ext(mock(f'={{ {key}: {{ menge: number(string(menge)), mengeVorher: aktuell.menge, material: regel.material, '
                    'einheit: regel.einheit } }', "neu")),
           f"Simuliert die Ausführung in SharePoint und die Umstellung der Verrechnung auf das Band der neuen Menge (Material "
           f"aus der Richtlinie, leer, wenn die Leistung endet). Schreibt neu.{key}; mengeVorher bleibt für einen Rollback.",
           action, end_name)
    ROWS.append((cid, sid, y))


zusatz_row(1, "rp", "c_reporting", "Reporting angefragt", "m_reporting", "sp_reporting", "Erweitertes Reporting",
           "reporting-aendern", "reporting", "spop-reporting", "Erweitertes Reporting", 0, "Pakete", "spop-reporting",
           "Erweitertes Reporting der Site: Report-Pakete 0 bis 20, CHF 25.– pro Paket; 0 beendet die Leistung. Die Anzahl "
           "bestimmt das Verrechnungsband: 1–5 (540007511), 6–10 (540007703), 11–15 (540007702), 16–20 (540007704). Die "
           "Leistung gehört zur Site und endet mit ihrer Rückgabe.",
           "Reporting freigeben", "Reporting gesetzt")
zusatz_row(2, "ss", "c_subsites", "SubSite Quota angefragt", "m_subsites", "sp_subsites", "SubSite Quota",
           "subsites-aendern", "subsites", "spop-subsite-quota", "SubSite Quota", 10, "Subsites", "spop-subsites",
           "SubSite Quota der Site: erlaubte Subsites 10 bis 500; bis 10 sind in der Basis enthalten, 10 beendet die "
           "Leistung. Bänder 11–20 (540007513), 21–50 (540007705), 51–100 (540007706), 101–200 (540007707), 201–500 "
           "(540007708); ab 101 Vier-Augen-Prinzip. Die Leistung gehört zur Site und endet mit ihrer Rückgabe.",
           "SubSite Quota freigeben", "SubSite Quota gesetzt")

# ----------------------------------------------------------------------- Angaben der Site: Titel, URL, Berechtigungen
# Was «Angaben ändern» im Shop nur im Datensatz der Bestellung korrigiert (ADR-0359), ändert hier je eine Aktion.
y = action_row(3, "c_titel", "Umbenennung angefragt", "m_titel",
               "Aktion site-umbenennen (Formular spop-site-umbenennen: neuerTitel, begruendung).")
X = change_sub(
    SPX, "t", "sp_titel", "site-umbenennen", "Site umbenennen", y, W,
    "Aktion site-umbenennen: ändert den Titel der Site; die URL bleibt. Richtlinie SpopAenderungRichtlinie mit aenderung "
    "titel: 1 bis 80 Zeichen, ohne Freigabe." + MERGE_DOC,
    [(merged("site"), "site")],
    "Site lesen (S1)",
    '={ gefunden: site != null, titel: if site = null then null else site.titel }',
    "Simuliert apsbw_sharepoint_site read aus dem Stand des Strangs (site). Schreibt aktuell.",
    "Änderungs-Richtlinie prüfen", "SpopAenderungRichtlinie",
    [("=aktuell.gefunden", "siteGefunden"), ('="titel"', "aenderung"),
     ("=neuerTitel != null and string length(neuerTitel) >= 1 and string length(neuerTitel) <= 80", "gueltig"),
     ("=neuerTitel = aktuell.titel", "unveraendert"), ('=if site = null then "" else site.typ', "siteTyp"),
     ("=false", "ownerWechsel")],
    "DMN SpopAenderungRichtlinie mit aenderung titel.",
    '="Site umbenennen\\nSite: " + site.url + "\\nTitel bisher: " + aktuell.titel + "\\nNeu: " + neuerTitel + '
    '"\\nRegel: " + regel.grund',
    "Umbenennung freigeben")
finish("t", "sp_titel", X, y, "Titel setzen (S1)", ext(mock('={ titel: neuerTitel, titelVorher: site.titel }', "neu")),
       "Simuliert apsbw_sharepoint_site update (Titel). Schreibt neu mit dem neuen Titel und titelVorher für einen Rollback.",
       "site-umbenennen", "Site umbenannt")
ROWS.append(("c_titel", "sp_titel", y))

NEUE_URL = '"/sites/" + lower case(neuesAmtKuerzel) + "-" + neuerKurzname'
y = action_row(4, "c_url", "URL-Änderung angefragt", "m_url",
               "Aktion site-url-aendern (Formular spop-site-url-aendern: neuesAmtKuerzel, neuerKurzname, begruendung).")
X = change_sub(
    SPX, "u", "sp_url", "site-url-aendern", "Site-URL ändern", y, W,
    "Aktion site-url-aendern: gibt der Site die URL /sites/<amt>-<kurzname>. Bestehende Links werden ungültig; darum immer "
    "Vier-Augen-Freigabe (SpopAenderungRichtlinie, aenderung url). Führt die Verrechnung nach. Annahme: SharePoint OnPrem "
    "erlaubt das Umbenennen der Site-Collection-URL; technisch nicht geklärt." + MERGE_DOC,
    [(merged("site"), "site"), (merged("verrechnung", "neuVerrechnung"), "verrechnung")],
    "Site und neue URL lesen (S1)",
    '={ gefunden: site != null, url: if site = null then null else site.url, neueUrl: ' + NEUE_URL + ', urlFrei: true }',
    "Simuliert apsbw_sharepoint_site read für die heutige und die neue URL; urlFrei sagt, ob unter der neuen URL noch keine "
    "Site besteht. Schreibt aktuell.",
    "Änderungs-Richtlinie prüfen", "SpopAenderungRichtlinie",
    [("=aktuell.gefunden", "siteGefunden"), ('="url"', "aenderung"),
     ('=neuesAmtKuerzel != null and matches(neuesAmtKuerzel, "^[A-Z]{2,6}$") and neuerKurzname != null and '
      'matches(neuerKurzname, "^[a-z0-9][a-z0-9-]{2,29}$") and aktuell.urlFrei = true', "gueltig"),
     ("=aktuell.neueUrl = aktuell.url", "unveraendert"), ('=if site = null then "" else site.typ', "siteTyp"),
     ("=false", "ownerWechsel")],
    "DMN SpopAenderungRichtlinie mit aenderung url: immer Vier-Augen-Prinzip.",
    '="Site-URL ändern\\nURL bisher: " + aktuell.url + "\\nNeu: " + aktuell.neueUrl + '
    '(if begruendung = null then "" else "\\nBegründung: " + begruendung) + "\\nRegel: " + regel.grund',
    "URL-Änderung freigeben")
finish("u", "sp_url", X, y, "Site-URL ändern (S1)", ext(mock('={ url: aktuell.neueUrl, urlVorher: site.url }', "neu", "PT2S", "PT4S")),
       "Simuliert das Umbenennen der Site-Collection-URL (apsbw_sharepoint_site rename). Schreibt neu mit der neuen URL und "
       "urlVorher für einen Rollback.",
       "site-url-aendern", "URL geändert",
       extra=[("u_bill", "Verrechnung nachführen", ext(mock('={ siteUrl: neu.url }', "neuVerrechnung")),
               "Simuliert die Anpassung der wiederkehrenden Verrechnung auf die neue URL. Schreibt neuVerrechnung.")])
ROWS.append(("c_url", "sp_url", y))

MITGLIEDER = 'if neueMemberUpns = null then "" else neueMemberUpns'
y = action_row(5, "c_rechte", "Berechtigungsänderung angefragt", "m_rechte",
               "Aktion berechtigungen-aendern (Formular spop-berechtigungen-aendern: neuerOwnerUpn, neueMemberUpns, begruendung).")
X = change_sub(
    SPX, "b", "sp_rechte", "berechtigungen-aendern", "Berechtigungen ändern", y, W,
    "Aktion berechtigungen-aendern: setzt Owner und Members der Site neu; anzugeben ist der vollständige neue Stand. "
    "Richtlinie SpopAenderungRichtlinie mit aenderung berechtigungen: Owner-Wechsel bei Hermes, Communication und Publishing "
    "mit Vier-Augen-Freigabe." + MERGE_DOC,
    [(merged("gruppen"), "gruppen")],
    "Gruppen und neuen Owner lesen (S2)",
    '={ gefunden: site != null, owner: if gruppen = null then null else gruppen.owner, '
    'mitglieder: if gruppen = null then "" else (if gruppen.mitglieder = null then "" else gruppen.mitglieder), ownerExistiert: true }',
    "Simuliert apsbw_sharepoint_group read und apsbw_ad_user read für den neuen Owner (ownerExistiert). Schreibt aktuell.",
    "Änderungs-Richtlinie prüfen", "SpopAenderungRichtlinie",
    [("=aktuell.gefunden", "siteGefunden"), ('="berechtigungen"', "aenderung"),
     ('=neuerOwnerUpn != null and matches(neuerOwnerUpn, "^[^@ ]+@[^@ ]+[.][^@ ]+$") and aktuell.ownerExistiert = true', "gueltig"),
     (f'=aktuell.owner != null and neuerOwnerUpn != null and lower case(neuerOwnerUpn) = lower case(aktuell.owner) and ({MITGLIEDER}) = aktuell.mitglieder', "unveraendert"),
     ('=if site = null then "" else site.typ', "siteTyp"),
     ('=aktuell.owner = null or neuerOwnerUpn = null or lower case(neuerOwnerUpn) != lower case(aktuell.owner)', "ownerWechsel")],
    "DMN SpopAenderungRichtlinie mit aenderung berechtigungen.",
    '="Berechtigungen ändern\\nSite: " + site.url + "\\nOwner bisher: " + aktuell.owner + "\\nNeu: " + neuerOwnerUpn + '
    f'"\\nMembers neu: " + ({MITGLIEDER}) + "\\nRegel: " + regel.grund',
    "Owner-Wechsel freigeben")
finish("b", "sp_rechte", X, y, "Gruppen aktualisieren (S2–S5)",
       ext(mock(f'={{ owner: neuerOwnerUpn, mitglieder: {MITGLIEDER}, ownerVorher: gruppen.owner, '
                'mitgliederVorher: gruppen.mitglieder }', "neu")),
       "Simuliert apsbw_sharepoint_group add_member und remove_member für die Owner- und die Member-Gruppe. Schreibt neu mit "
       "Owner und Members sowie ownerVorher und mitgliederVorher für einen Rollback.",
       "berechtigungen-aendern", "Berechtigungen geändert")
ROWS.append(("c_rechte", "sp_rechte", y))

# ----------------------------------------------------------------------- Gemeinsamer Rückweg zum Warten
YJ = ROWS[-1][2] + 300 + 70
node("j_zurueck", "exclusiveGateway", "", XR, YJ,
     doc="Gemeinsamer Rückweg: Jeder Änderungs-Subprozess unterhalb mündet hier; von hier geht der Strang zurück zum Warten.")
for k, (cid, sid, yy) in enumerate(ROWS):
    flow(f"w_c{k}", "g_warten", cid, "b", "l")
    flow(f"w_s{k}", cid, sid, b=(SPX, yy))
    flow(f"w_z{k}", sid, "j_zurueck", a=(SPX + W, yy), b=(XR, YJ - 25), via=[(XR, yy)])
flow("w_back", "j_zurueck", "j_aktiv", a=(XR, YJ + 25), b=(JX, M + 25), via=[(XR, YJ + 60), (JX, YJ + 60)])

# ----------------------------------------------------------------------- Rückgabe
# Wie jede Aktion ein Zwischenereignis am Gateway: rechts davon, zwischen Einladung und Speicher. Sie greift, wenn der
# Strang wartet; während eine Änderung läuft, nimmt der Strang sie nicht an, und der Katalog lehnt sie ab, bis die
# Änderung abgeschlossen ist. Ohne laufenden Strang startet die Rückfallebene und mündet in denselben Abbau.
RJ = SPX + 2050                # rechts der Einladung und des gemeinsamen Rückwegs
node("c_rueck", "intermediateCatchEvent", "Rückgabe angefordert", CX, M, '<messageEventDefinition messageRef="m_rueckgabe"/>',
     doc="Operation deprovision (Formular spop-teamsite-rueckgabe: grund). Nur, wenn der Strang wartet.", lw=90)
node("j_rueck", "exclusiveGateway", "", RJ, M,
     doc="Rückgabe aus dem wartenden Strang und Rückgabe ohne laufenden Strang führen in denselben Abbau.")
node("r_extern", "serviceTask", "Externe Zugriffe entfernen", RJ + 130, M,
     ext(mock('={ siteUrl: site.url, entfernt: if externe = null then [] else externe }', "externEntfernt")),
     doc="Simuliert apsbw_sharepoint_externaluser remove für jeden Eintrag in externe, der Liste der angenommenen Einladungen.")
node("r_delete", "serviceTask", "Site löschen", RJ + 280, M,
     ext(mock('={ siteUrl: site.url, status: "im Papierkorb" }', "rueckbau")),
     doc="Simuliert apsbw_sharepoint_site delete (Worker sharepointserver: delete-site, Papierkorb).")
node("r_bill", "serviceTask", "Verrechnung beenden", RJ + 430, M,
     ext(mock('={ material: site.material, siteUrl: site.url, zusatz: zusatz, quota: quota, status: "beendet" }', "verrechnung")),
     doc="Simuliert das Beenden der wiederkehrenden Verrechnung: Site-Material, Speicherplatzerweiterung und Zusatzleistungen.")
node("r_info", "serviceTask", "Rückgabe melden", RJ + 580, M, rest_status("returned"),
     doc="Meldet die Rückgabe über die Positionsroute (status returned); damit endet das Recht im Bestand." + REST_DOC)
node("r_end", "endEvent", "Site zurückgegeben", RJ + 710, M, '<terminateEventDefinition/>',
     doc="Terminierendes Ende: beendet den Strang der Position vollständig.")

FY = M + 330
node("r_start", "startEvent", "Rückgabe ohne laufenden Strang", RJ - 130, FY, '<messageEventDefinition messageRef="m_rueckgabe"/>',
     doc="Rückfallebene wie im Referenzmodell: Eine Rückgabe für eine Position ohne laufenden Strang startet hier. Der Katalog "
         "verlangt für per-position diesen Nachrichtenstart.", lw=100)
node("r_site", "scriptTask", "Site ermitteln", RJ, FY,
     script('={ url: "/sites/" + lower case(amtKuerzel) + "-" + kurzname }', "site"),
     doc="Bildet die Site-URL aus der Konfiguration der Position nach demselben Schema wie die Bereitstellung (Annahme: der "
         "Katalog liefert amtKuerzel und kurzname der Position mit). Eine spätere URL-Änderung kennt diese Ebene nicht.")
flow("r_f0", "g_warten", "c_rueck")
flow("r_f1", "c_rueck", "j_rueck")
flow("r_f2", "r_start", "r_site")
flow("r_f3", "r_site", "j_rueck", "t", "b")
flow("r_f4", "j_rueck", "r_extern")
flow("r_f5", "r_extern", "r_delete")
flow("r_f6", "r_delete", "r_bill")
flow("r_f7", "r_bill", "r_info")
flow("r_f8", "r_info", "r_end")

# ----------------------------------------------------------------------- Flüsse Hauptstrang
flow("a_f1", "s_neu", "a_regel")
flow("a_f3", join, "a_owner")
flow("a_f4", "a_owner", "a_site")
flow("a_f5", "a_site", "a_par")
flow("a_f6", "a_par", "a_grp", "t", "l")
flow("a_f7", "a_grp", "a_rechte")
flow("a_f8", "a_rechte", "a_parj", "r", "t")
flow("a_f9", "a_par", "a_quota")
flow("a_f10", "a_quota", "a_parj")
flow("a_f11", "a_par", "a_feat", "b", "l")
flow("a_f12", "a_feat", "a_parj", "r", "b")
flow("a_f13", "a_parj", "a_bill")
flow("a_f13b", "a_bill", "a_info")
flow("a_f14", "a_info", "j_aktiv")
flow("w_f1", "j_aktiv", "g_warten")
flow("w_f3", "g_warten", "c_extern", "t", "l")
flow("w_f5", "c_extern", "sp_extern", b=(SPX, EY))
ex, ey, ew, eh = spbox["sp_extern"]
flow("w_f7", "sp_extern", "j_aktiv", a=(SPX + 30, ey), b=(JX, M - 25), via=[(SPX + 30, ey - 30), (JX, ey - 30)])

PROCESS_DOC = """SPoP: Lebenszyklus einer SharePoint-OnPrem-Site als ein einziger Strang je Position (Muster lc_proc_m365_funktionspostfach).
Sechs Katalogprodukte binden diesen Prozess, je Site-Typ eines: Project Site Hermes (540007512), Communication Site (540007514),
Publishing Site (540007515), Team Site Classic (540007516), Team Site Modern (540007560), Wiki Site (540008180). Den Typ, seine
Vorlage, Features, Quota-Grenzen, Freigabepflicht und Materialnummer bestimmt die Richtlinie SpopTeamsiteRichtlinie aus itemId.
Bereitstellung (GF-001) läuft flach; danach wartet der Strang am ereignisbasierten Gateway «Aktiv: auf Auftrag warten». Jede Aktion
ist ein Zwischenereignis an diesem Gateway mit ihrem Subprozess (Katalog: change):
- spop.teamsite.extern         → «Externen Benutzer einladen (GF-003)»  (Formular spop-extern-einladen)
- spop.teamsite.quota          → «Speicherplatz erweitern (GF-002)»     (Formular spop-quota-erhoehung, Material 540007510)
- spop.teamsite.reporting      → «Erweitertes Reporting»                (Formular spop-reporting)
- spop.teamsite.subsites       → «SubSite Quota»                        (Formular spop-subsites)
- spop.teamsite.titel          → «Site umbenennen»                      (Formular spop-site-umbenennen)
- spop.teamsite.url            → «Site-URL ändern»                      (Formular spop-site-url-aendern)
- spop.teamsite.berechtigungen → «Berechtigungen ändern»                (Formular spop-berechtigungen-aendern)
Nach jedem Subprozess kehrt der Strang zum Warten zurück. Die Rückgabe (spop.teamsite.rueckgabe, Katalog: deprovision) ist ebenfalls
ein Zwischenereignis am Gateway und als Nachrichtenstart die Rückfallebene ohne laufenden Strang; während eine Änderung läuft, nimmt
der Strang sie nicht an. Alle späteren Aktionen sind an
orderId + "/" + positionId korreliert (Katalogform per-position). Der Strang trägt den Stand der Site (site, quota, zusatz, gruppen,
externe, verrechnung); jede Änderung setzt nur ihre eigenen Felder mit context merge ein. «Angaben ändern» im Shop korrigiert nur den
Datensatz der Bestellung; die Sprache ist nach der Erstellung fest. Regeln: DMN SpopTeamsiteRichtlinie, SpopZusatzRichtlinie
(Speicher, Reporting, SubSites), SpopExternRichtlinie, SpopAenderungRichtlinie. Vier-Augen-Freigabe: Gruppe SPoP-Freigabe, Formular
spop-freigabe. Ausführung gegen APS-BW und Verrechnung im PoC simuliert (atlas:mockupConnector). Rückmeldungen an den Katalog sind
echt: Decision-Route, Positionsroute (Secret "atlas", Konstante ATLAS_API_BASE) und Shop-Aufgaben."""

write(OUT, "defs_spop_teamsite", "proc_spop_teamsite", "SPoP: Site-Lebenszyklus", PROCESS_DOC,
      [("m_bestellen", "spop.teamsite.bestellen", None),
       ("m_extern", "spop.teamsite.extern", KEY),
       ("m_quota", "spop.teamsite.quota", KEY),
       ("m_reporting", "spop.teamsite.reporting", KEY),
       ("m_subsites", "spop.teamsite.subsites", KEY),
       ("m_titel", "spop.teamsite.titel", KEY),
       ("m_url", "spop.teamsite.url", KEY),
       ("m_rechte", "spop.teamsite.berechtigungen", KEY),
       ("m_rueckgabe", "spop.teamsite.rueckgabe", KEY),
       ("m_angenommen", "spop.extern.angenommen", "=einladung.einladungId")])
