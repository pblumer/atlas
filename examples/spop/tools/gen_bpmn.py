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
     ext(mock('={ siteUrl: site.url, storageMb: regel.quotaEffektivMb }', "quota")),
     doc="Simuliert apsbw_sharepoint_quota apply (Worker sharepointserver: set-site-quota). Schreibt quota, den Stand, "
         "den die Quota-Erhöhung später liest und fortschreibt.")
node("a_feat", "serviceTask", "Features aktivieren (S8)", 1630, M + L1,
     ext(mock('={ siteUrl: site.url, features: regel.features }', "features")),
     doc="Simuliert apsbw_sharepoint_feature activate mit den Features des Site-Typs aus der Richtlinie (Annahme).")
node("a_parj", "parallelGateway", "", 1910, M)
node("a_bill", "serviceTask", "Verrechnung erfassen", 2040, M,
     ext(mock('={ material: site.material, menge: 1, einheit: "Site", siteUrl: site.url, position: orderId + "/" + (if positionId = null then itemId else positionId) }', "verrechnung")),
     doc="Simuliert die Erfassung der wiederkehrenden Verrechnung (Marktleistungsstruktur ML Redesign SharePoint) mit der "
         "Materialnummer des Site-Typs.")
node("a_info", "serviceTask", "Position als erledigt melden", 2170, M, rest_status("done"),
     doc="Meldet die Position über POST /api/v1/orders/{orderId}/lines/{positionId} als erledigt (status done); erst damit "
         "steht die Site im Bestand und die Aktionen werden möglich." + REST_DOC)

# ======================================================================= Warten auf Aufträge
JX, GX, CX, SPX = 2300, 2460, 2570, 2620
node("j_aktiv", "exclusiveGateway", "", JX, M)
node("g_warten", "eventBasedGateway", "Aktiv: auf Auftrag warten", GX, M, label="custom", lpos=(JX + 25, M - 62, 100, 27),
     doc="Die Site ist in Betrieb. Der Strang wartet auf die nächste Aktion der Position: Quota-Erhöhung, externe Einladung "
         "oder Rückgabe. Jede Aktion ist an orderId/positionId korreliert. Während eine Aktion läuft, wartet der Strang nicht; "
         "eine zweite Aktion derselben Position wird erst nach Abschluss der ersten angenommen.")
node("c_rueck", "intermediateCatchEvent", "Rückgabe angefordert", CX, M, '<messageEventDefinition messageRef="m_rueckgabe"/>',
     doc="Operation deprovision (Formular spop-teamsite-rueckgabe: grund).", lw=90)
node("c_extern", "intermediateCatchEvent", "Einladung angefragt", CX, 330, '<messageEventDefinition messageRef="m_extern"/>',
     doc="GF-003: Aktion extern-einladen (Formular spop-extern-einladen: email, rolle, ablaufTage, nachricht).", lw=90)
node("c_quota", "intermediateCatchEvent", "Quota-Erhöhung angefragt", CX, 960, '<messageEventDefinition messageRef="m_quota"/>',
     doc="GF-002: Aktion quota-erhoehen (Formular spop-quota-erhoehung: neueQuotaMb, begruendung).", lw=90)

# ----------------------------------------------------------------------- Subprozess Quota (GF-002), unterhalb
QY = 960
X = change_sub(
    SPX, "q", "sp_quota", "quota-erhoehen", "Quota erhöhen (GF-002)", QY, 1600,
    "GF-002 SP OnPrem Quota-Erhöhung als Subprozess des Strangs, innerhalb des Maximums des Site-Typs; Speicher darüber hinaus "
    "ist das Zusatzprodukt Speicherplatzerweiterung (Annahme). Liest Site und aktuelle Quota aus dem Strang (site, quota), "
    "prüft die Richtlinie, holt bei Bedarf die Vier-Augen-Freigabe und schreibt quota fort. Output-Mapping quota: Ergebnisse "
    "innerer Aufgaben bleiben im Subprozess (ADR-0074); endet er mit einer Ablehnung, löst =quota den unveränderten Wert auf.",
    [("=quota", "quota")],
    "Site und Quota lesen (S1)",
    '={ gefunden: site != null, siteUrl: site.url, siteTyp: site.typ, quotaMb: quota.storageMb }',
    "Simuliert apsbw_sharepoint_site read und apsbw_sharepoint_quota read. Im PoC aus dem Stand des Strangs (site, quota). Schreibt aktuell.",
    "Quota-Richtlinie prüfen (S3)", "SpopQuotaRichtlinie",
    [("=aktuell.gefunden", "siteGefunden"), ("=aktuell.siteTyp", "siteTyp"),
     ("=if neueQuotaMb = null then 0 else number(string(neueQuotaMb))", "neueQuotaMb"),
     ("=(if neueQuotaMb = null then 0 else number(string(neueQuotaMb))) - aktuell.quotaMb", "erhoehungMb"),
     ("=begruendung != null and string length(begruendung) >= 10", "begruendungVorhanden")],
    "DMN SpopQuotaRichtlinie anstelle von quota_limits.rego und approval.rego: Begründung Pflicht, neue Quota grösser als die aktuelle, "
    "Maximum je Site-Typ, Vier-Augen-Prinzip ab einer Erhöhung um mehr als 5120 MB (Annahme).",
    '="Quota-Erhöhung (GF-002)\\nSite: " + site.url + " (" + site.typ + ")\\nAktuell: " + string(aktuell.quotaMb) + " MB\\nNeu: " + '
    'string(neueQuotaMb) + " MB\\nBegründung: " + begruendung + "\\nRegel: " + regel.grund',
    "Quota-Erhöhung freigeben")
node("q_apply", "serviceTask", "Quota setzen (S2)", X(1260), QY,
     ext(mock('={ siteUrl: site.url, storageMb: number(string(neueQuotaMb)), alteQuotaMb: aktuell.quotaMb }', "quota")),
     doc="Simuliert apsbw_sharepoint_quota apply (Worker sharepointserver: set-site-quota) und schreibt quota fort; "
         "alteQuotaMb bleibt für einen Rollback erhalten.", parent="sp_quota")
node("q_info", "sendTask", "Aktion als erledigt melden", X(1410), QY, shop("quota-erhoehen", "completed"),
     doc="Shop-Aufgabe (Modus outcome): beantwortet die Aktion quota-erhoehen mit completed.", parent="sp_quota")
node("q_end", "endEvent", "Quota erhöht", X(1540), QY, parent="sp_quota")
flow("q_f10", "q_join", "q_apply", parent="sp_quota")
flow("q_f11", "q_apply", "q_info", parent="sp_quota")
flow("q_f12", "q_info", "q_end", parent="sp_quota")

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

# ----------------------------------------------------------------------- Rückgabe
qx, qy, qw, qh = spbox["sp_quota"]
ex, ey, ew, eh = spbox["sp_extern"]
BQ = qx + qw - 60          # Boundary auf der Oberkante des Quota-Subprozesses
BE = ex + ew - 60          # Boundary auf der Unterkante des Einladungs-Subprozesses
node("b_rueck_q", "boundaryEvent", "Rückgabe", BQ, qy, '<messageEventDefinition messageRef="m_rueckgabe"/>',
     attrs=' attachedToRef="sp_quota" cancelActivity="true"', label="custom", lpos=(BQ + 22, qy - 34, 60, 14),
     doc="Eine Rückgabe bricht eine laufende Quota-Erhöhung ab.")
node("b_rueck_x", "boundaryEvent", "Rückgabe", BE, ey + eh, '<messageEventDefinition messageRef="m_rueckgabe"/>',
     attrs=' attachedToRef="sp_extern" cancelActivity="true"', label="custom", lpos=(BE - 82, ey + eh + 20, 60, 14),
     doc="Eine Rückgabe bricht eine laufende Einladung ab, auch während auf die Annahme gewartet wird.")
node("j_rueck1", "exclusiveGateway", "", BQ, M)
node("j_rueck2", "exclusiveGateway", "", BE, M)
node("r_start", "startEvent", "Rückgabe ohne laufenden Strang", BQ + 130, QY, '<messageEventDefinition messageRef="m_rueckgabe"/>',
     doc="Rückfallebene wie im Referenzmodell: Eine Rückgabe für eine Position ohne laufenden Strang startet hier. Der Katalog "
         "verlangt für per-position diesen Nachrichtenstart.", lw=100)
node("r_site", "scriptTask", "Site ermitteln", BQ + 260, QY,
     script('={ url: "/sites/" + lower case(amtKuerzel) + "-" + kurzname }', "site"),
     doc="Bildet die Site-URL aus der Konfiguration der Position nach demselben Schema wie die Bereitstellung (Annahme: der "
         "Katalog liefert amtKuerzel und kurzname der Position mit).")
node("r_extern", "serviceTask", "Externe Zugriffe entfernen", BE + 130, M,
     ext(mock('={ siteUrl: site.url, entfernt: if externe = null then [] else externe }', "externEntfernt")),
     doc="Simuliert apsbw_sharepoint_externaluser remove für jeden Eintrag in externe, der Liste der angenommenen Einladungen des Strangs.")
node("r_delete", "serviceTask", "Site löschen", BE + 280, M,
     ext(mock('={ siteUrl: site.url, status: "im Papierkorb" }', "rueckbau")),
     doc="Simuliert apsbw_sharepoint_site delete (Worker sharepointserver: delete-site, Papierkorb).")
node("r_bill", "serviceTask", "Verrechnung beenden", BE + 430, M,
     ext(mock('={ material: site.material, siteUrl: site.url, status: "beendet" }', "verrechnung")),
     doc="Simuliert das Beenden der wiederkehrenden Verrechnung des Site-Materials.")
node("r_info", "serviceTask", "Rückgabe melden", BE + 580, M, rest_status("returned"),
     doc="Meldet die Rückgabe über die Positionsroute (status returned); damit endet das Recht im Bestand." + REST_DOC)
node("r_end", "endEvent", "Site zurückgegeben", BE + 710, M, '<terminateEventDefinition/>',
     doc="Terminierendes Ende: beendet den Strang der Position vollständig.")

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
flow("w_f2", "g_warten", "c_rueck")
flow("w_f3", "g_warten", "c_extern", "t", "l")
flow("w_f4", "g_warten", "c_quota", "b", "l")
flow("w_f5", "c_extern", "sp_extern", b=(SPX, EY))
flow("w_f6", "c_quota", "sp_quota", b=(SPX, QY))
# Rückkehr der Subprozesse zum Warten (Back-Edges)
flow("w_f7", "sp_extern", "j_aktiv", a=(SPX + 30, ey), b=(JX, M - 25), via=[(SPX + 30, ey - 30), (JX, ey - 30)])
flow("w_f8", "sp_quota", "j_aktiv", a=(SPX + 30, qy + qh), b=(JX, M + 25), via=[(SPX + 30, qy + qh + 30), (JX, qy + qh + 30)])
# Rückgabe
flow("r_f1", "c_rueck", "j_rueck1")
flow("r_f2", "b_rueck_q", "j_rueck1", a=(BQ, qy - 18), b=(BQ, M + 25), via=[])
flow("r_f3", "j_rueck1", "j_rueck2")
flow("r_f4", "b_rueck_x", "j_rueck2", a=(BE, ey + eh + 18), b=(BE, M - 25), via=[])
flow("r_f5", "r_start", "r_site")
flow("r_f6", "r_site", "j_rueck2", "r", "b")
flow("r_f7", "j_rueck2", "r_extern")
flow("r_f8", "r_extern", "r_delete")
flow("r_f9", "r_delete", "r_bill")
flow("r_f9b", "r_bill", "r_info")
flow("r_f10", "r_info", "r_end")

PROCESS_DOC = """SPoP: Lebenszyklus einer SharePoint-OnPrem-Site als ein einziger Strang je Position (Muster lc_proc_m365_funktionspostfach).
Sechs Katalogprodukte binden diesen Prozess, je Site-Typ eines: Project Site Hermes (540007512), Communication Site (540007514),
Publishing Site (540007515), Team Site Classic (540007516), Team Site Modern (540007560), Wiki Site (540008180). Den Typ, seine
Vorlage, Features, Quota-Grenzen, Freigabepflicht und Materialnummer bestimmt die Richtlinie SpopTeamsiteRichtlinie aus itemId.
Bereitstellung (GF-001) läuft flach; danach wartet der Strang am ereignisbasierten Gateway «Aktiv: auf Auftrag warten» auf:
- spop.teamsite.quota     → Subprozess «Quota erhöhen (GF-002)»                 (Katalog: change, Formular spop-quota-erhoehung)
- spop.teamsite.extern    → Subprozess «Externen Benutzer einladen (GF-003)»    (Katalog: change, Formular spop-extern-einladen)
- spop.teamsite.rueckgabe → Rückgabe, auch als Boundary an beiden Subprozessen  (Katalog: deprovision, Formular spop-teamsite-rueckgabe)
Nach jedem Subprozess kehrt der Strang zum Warten zurück. Alle späteren Aktionen sind an orderId + "/" + positionId korreliert
(Katalogform per-position). Der Strang trägt den Stand der Site (site, quota, externe). Zusatzleistungen (Speicherplatzerweiterung,
Erweitertes Reporting, SubSite Quota) sind eigene Produkte mit eigenem Prozess proc_spop_zusatz. Regeln: DMN SpopTeamsiteRichtlinie,
SpopQuotaRichtlinie, SpopExternRichtlinie. Vier-Augen-Freigabe: Gruppe SPoP-Freigabe, Formular spop-freigabe. Ausführung gegen APS-BW
und Verrechnung im PoC simuliert (atlas:mockupConnector). Rückmeldungen an den Katalog sind echt: Decision-Route, Positionsroute
(Secret "atlas", Konstante ATLAS_API_BASE) und Shop-Aufgaben."""

write(OUT, "defs_spop_teamsite", "proc_spop_teamsite", "SPoP: Site-Lebenszyklus", PROCESS_DOC,
      [("m_bestellen", "spop.teamsite.bestellen", None),
       ("m_quota", "spop.teamsite.quota", KEY),
       ("m_extern", "spop.teamsite.extern", KEY),
       ("m_rueckgabe", "spop.teamsite.rueckgabe", KEY),
       ("m_angenommen", "spop.extern.angenommen", "=einladung.einladungId")])
