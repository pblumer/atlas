#!/usr/bin/env python3
"""Erzeugt spop-zusatz.bpmn (Prozess proc_spop_zusatz): python3 tools/gen_bpmn_zusatz.py spop-zusatz.bpmn (aus examples/spop).

Ein Strang je Position für die zwei Zusatzprodukte Erweitertes Reporting und SubSite Quota. Der
Strang bezieht sich auf eine Site, die eine andere Position hält; die bestellende Person nennt sie mit ihrer URL, und der
Strang prüft über die Instanzsuche, dass diese Site ihr gehört.
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from xml.sax.saxutils import quoteattr  # noqa: E402
from bpmnlib import (KEY, REASON_FREIGABE, REST_DOC, brt, change_sub, check_block, ext, flow, mock, node,  # noqa: E402
                     rest_decision, rest_status, script, shop, spbox, write)

OUT = sys.argv[1]
M = 300

BASE = '(if atlasApiBase = null then application.ATLAS_API_BASE else atlasApiBase)'
# Eigentumsprüfung: treffer ist die Liste der Suchantworten, eine je Version des Site-Prozesses. Jeder Treffer trägt nur die
# passende Variable siteZugriff = <Site-URL>|<Besteller>|<Empfänger>.
EIGEN = (
    '={ zugriffe: flatten(for p in (if treffer = null then [] else treffer) return '
    '(if p = null or p.items = null then [] else for t in p.items[state = "active"] return t.variables[name = "siteZugriff"][1].value)), '
    'passend: zugriffe[item != null and starts with(item, siteUrl + "|")], '
    'gefunden: count(passend) > 0, '
    'gehoert: some z in passend satisfies contains(z + "|", "|" + orderer + "|"), '
    'siteUrl: siteUrl }'
)
node("z_start", "startEvent", "Zusatzleistung bestellt", 118, M, '<messageEventDefinition messageRef="m_bestellen"/>',
     doc="Start der Operation provision. Zwei Produkte binden diesen Prozess: "
         "spop-reporting (Erweitertes Reporting, Pakete) und spop-subsite-quota (SubSite Quota, Subsites). Formulare "
         "spop-reporting-bestellung, spop-subsite-bestellung: siteUrl, menge, begruendung.")
node("z_procs", "serviceTask", "Site-Prozessversionen lesen", 250, M,
     ext(f'<atlas:restConnector method="GET" url={quoteattr("=" + BASE + ' + "/api/v1/processes"')} authType="bearer" authSecret="atlas" resultVariable="prozesse"/>'),
     doc="Liest die deployten Definitionen (GET /api/v1/processes). Site-Stränge laufen auf der Version, mit der sie bereitgestellt "
         "wurden; gesucht wird deshalb in jeder Version von proc_spop_teamsite. Braucht das Secret \"atlas\". Schreibt prozesse.")
node("z_read", "serviceTask", "Site im Bestand suchen", 400, M,
     ext(f'<atlas:restConnector method="GET" url={quoteattr("=" + BASE + ' + "/api/v1/instances/search?process=" + string(siteDefKey) + "&q=siteZugriff%3D" + siteUrl + "%7C*&values=preview"')} '
         'authType="bearer" authSecret="atlas" resultVariable="trefferVersion"/>'),
     tail='<multiInstanceLoopCharacteristics><extensionElements><zeebe:loopCharacteristics '
          'inputCollection="=prozesse[processId = &quot;proc_spop_teamsite&quot;].key" inputElement="siteDefKey" '
          'outputCollection="treffer" outputElement="=trefferVersion"/></extensionElements></multiInstanceLoopCharacteristics>',
     doc="Parallele Mehrfach-Instanz, eine je Version von proc_spop_teamsite: GET /api/v1/instances/search?process=<Version>&"
         "q=siteZugriff=<Site-URL>|*. Die Einschränkung auf eine Version liest deren Index statt aller Instanzen des Servers; "
         "siteZugriff ist im Site-Prozess als atlas:searchable deklariert. Die Instanzsuche verlangt die Rolle operator (Secret "
         "\"atlas\"). Schreibt treffer, die Liste der Antworten.")
node("z_eigen", "scriptTask", "Eigentum prüfen", 550, M,
     script(EIGEN, "aktuell"),
     doc="Prüft, ob die genannte Site der bestellenden Person gehört: Unter den Treffern zählen nur aktive Site-Stränge, deren "
         "siteZugriff mit der Site-URL beginnt. Die Site gehört der Person, wenn ihr orderer (sie bestellt die Zusatzleistung) "
         "Besteller oder Empfänger der Site-Position ist. Schreibt aktuell = {gefunden, gehoert, siteUrl}. Eine zurückgegebene "
         "Site hat keinen aktiven Strang mehr und gilt als nicht gefunden.")
node("z_regel", "businessRuleTask", "Zusatz-Richtlinie prüfen", 700, M,
     brt("SpopZusatzRichtlinie", "regel",
         ("=aktuell.gefunden", "siteGefunden"), ("=aktuell.gehoert", "siteGehoert"), ("=itemId", "produkt"),
         ("=if menge = null then 0 else number(string(menge))", "menge"), ("=false", "unveraendert")),
     doc="DMN SpopZusatzRichtlinie: zulässige Menge je Produkt, Verrechnungsband und Materialnummer (z. B. Reporting 6–10 Pakete "
         "= 540007703), Vier-Augen-Pflicht. Liefert regel = {zulaessig, vierAugen, material, einheit, grund}.")
join, jx = check_block(
    "z", 850, M, "z_regel",
    "Ablehnung melden", rest_decision("Richtlinie SpopZusatzRichtlinie", "=regel.grund"),
    "Meldet die Ablehnung über die Decision-Route der Position mit dem Grund der Richtlinie." + REST_DOC,
    '="Zusatzleistung: " + itemId + "\\nSite: " + siteUrl + "\\nMenge: " + string(menge) + " " + regel.einheit + '
    '" (Material " + regel.material + ")" + (if begruendung = null then "" else "\\nBegründung: " + begruendung) + '
    '"\\nRegel: " + regel.grund',
    "Zusatzleistung freigeben", "Vier-Augen-Freigabe durch die Gruppe SPoP-Freigabe, Formular spop-freigabe.",
    rest_decision("Vier-Augen-Freigabe", REASON_FREIGABE),
    "Meldet die verweigerte Vier-Augen-Freigabe über die Decision-Route der Position." + REST_DOC)
node("z_apply", "serviceTask", "Zusatzleistung einrichten", 1670, M,
     ext(mock('={ produkt: itemId, siteUrl: siteUrl, menge: number(string(menge)), einheit: regel.einheit, material: regel.material }', "bestand")),
     doc="Simuliert die Einrichtung über APS-BW: Speicher (apsbw_sharepoint_quota apply um menge GB), Reporting-Pakete oder "
         "Subsite-Quota. Schreibt bestand, den Stand des Strangs bis zur Rückgabe.")
node("z_bill", "serviceTask", "Verrechnung erfassen", 1820, M,
     ext(mock('={ material: bestand.material, menge: bestand.menge, einheit: bestand.einheit, position: orderId + "/" + (if positionId = null then itemId else positionId) }', "verrechnung")),
     doc="Simuliert die Erfassung der wiederkehrenden Verrechnung mit der Materialnummer des Verrechnungsbands. Die Antworten "
         "eines Bestellformulars kommen als Text an; menge wird deshalb vor Regel und Bestand in eine Zahl umgewandelt.")
node("z_done", "serviceTask", "Position als erledigt melden", 1970, M, rest_status("done"),
     doc="Meldet die Position als erledigt (status done); damit steht die Zusatzleistung im Bestand." + REST_DOC)

JX, GX, CX, SPX = 2100, 2260, 2370, 2420
QY = M + 260
node("j_aktiv", "exclusiveGateway", "", JX, M)
node("g_warten", "eventBasedGateway", "Aktiv: auf Auftrag warten", GX, M, label="custom", lpos=(JX + 25, M - 62, 100, 27),
     doc="Die Zusatzleistung ist in Betrieb. Der Strang wartet auf eine Mengenänderung oder die Rückgabe, korreliert an "
         "orderId/positionId.")
node("c_rueck", "intermediateCatchEvent", "Rückgabe angefordert", CX, M, '<messageEventDefinition messageRef="m_rueckgabe"/>',
     doc="Operation deprovision (Formular spop-zusatz-rueckgabe: grund).", lw=90)
node("c_aendern", "intermediateCatchEvent", "Mengenänderung angefragt", CX, QY, '<messageEventDefinition messageRef="m_aendern"/>',
     doc="Aktion menge-aendern (Formulare spop-reporting-aendern, spop-subsite-aendern: menge, begruendung). "
         "Eine neue Menge kann das Verrechnungsband und damit die Materialnummer wechseln.", lw=90)

W = 1750
X = change_sub(
    SPX, "m", "sp_aendern", "menge-aendern", "Menge ändern", QY, W,
    "Ändert die Menge der Zusatzleistung: Richtlinie mit der neuen Menge, bei Bedarf Vier-Augen-Freigabe, Anpassung und "
    "Verrechnung im neuen Band. Output-Mapping bestand: Ergebnisse innerer Aufgaben bleiben im Subprozess (ADR-0074).",
    [("=bestand", "bestand")],
    "Bestand lesen",
    '={ gefunden: bestand != null, siteUrl: bestand.siteUrl, menge: bestand.menge }',
    "Liest den Stand des Strangs (bestand). Schreibt aktuell.",
    "Zusatz-Richtlinie prüfen", "SpopZusatzRichtlinie",
    [("=aktuell.gefunden", "siteGefunden"), ("=true", "siteGehoert"), ("=itemId", "produkt"),
     ("=if menge = null then 0 else number(string(menge))", "menge"), ("=(if menge = null then 0 else number(string(menge))) = aktuell.menge", "unveraendert")],
    "DMN SpopZusatzRichtlinie mit der neuen Menge; eine unveränderte Menge wird abgelehnt.",
    '="Mengenänderung: " + itemId + "\\nSite: " + bestand.siteUrl + "\\nBisher: " + string(aktuell.menge) + "\\nNeu: " + '
    'string(menge) + " " + regel.einheit + " (Material " + regel.material + ")\\nRegel: " + regel.grund',
    "Mengenänderung freigeben")
SM = dict(parent="sp_aendern")
node("m_apply", "serviceTask", "Menge anpassen", X(1260), QY,
     ext(mock('={ produkt: itemId, siteUrl: bestand.siteUrl, menge: number(string(menge)), einheit: regel.einheit, material: regel.material, '
              'mengeVorher: aktuell.menge, materialVorher: bestand.material }', "bestand")),
     doc="Simuliert die Anpassung über APS-BW und schreibt bestand fort; mengeVorher und materialVorher bleiben für einen Rollback.", **SM)
node("m_bill", "serviceTask", "Verrechnung anpassen", X(1410), QY,
     ext(mock('={ material: bestand.material, menge: bestand.menge, materialVorher: bestand.materialVorher }', "verrechnung")),
     doc="Simuliert die Umstellung der Verrechnung, gegebenenfalls auf das Material eines anderen Bands.", **SM)
node("m_info", "sendTask", "Aktion als erledigt melden", X(1560), QY, shop("menge-aendern", "completed"),
     doc="Shop-Aufgabe (Modus outcome): beantwortet die Aktion menge-aendern mit completed.", **SM)
node("m_end", "endEvent", "Menge geändert", X(1690), QY, **SM)
flow("m_f10", "m_join", "m_apply", **SM)
flow("m_f11", "m_apply", "m_bill", **SM)
flow("m_f12", "m_bill", "m_info", **SM)
flow("m_f13", "m_info", "m_end", **SM)

sx, sy, sw, sh = spbox["sp_aendern"]
BQ = sx + sw - 60
J2 = BQ + 390
node("b_rueck", "boundaryEvent", "Rückgabe", BQ, sy, '<messageEventDefinition messageRef="m_rueckgabe"/>',
     attrs=' attachedToRef="sp_aendern" cancelActivity="true"', label="custom", lpos=(BQ + 22, sy - 34, 60, 14),
     doc="Eine Rückgabe bricht eine laufende Mengenänderung ab.")
node("j_rueck1", "exclusiveGateway", "", BQ, M)
node("j_rueck2", "exclusiveGateway", "", J2, M)
node("r_start", "startEvent", "Rückgabe ohne laufenden Strang", BQ + 130, QY, '<messageEventDefinition messageRef="m_rueckgabe"/>',
     doc="Rückfallebene: Eine Rückgabe für eine Position ohne laufenden Strang startet hier (vom Katalog für per-position verlangt).",
     lw=100)
node("r_bestand", "scriptTask", "Bestand ermitteln", BQ + 260, QY,
     script('={ produkt: itemId, siteUrl: siteUrl, menge: menge }', "bestand"),
     doc="Bildet den Bestand aus der Konfiguration der Position (Annahme: der Katalog liefert siteUrl und menge mit).")
node("r_remove", "serviceTask", "Zusatzleistung entfernen", J2 + 130, M,
     ext(mock('={ produkt: bestand.produkt, siteUrl: bestand.siteUrl, status: "entfernt" }', "rueckbau")),
     doc="Simuliert das Entfernen über APS-BW: Speicher zurücksetzen, Reporting-Pakete oder Subsite-Quota entfernen.")
node("r_bill", "serviceTask", "Verrechnung beenden", J2 + 280, M,
     ext(mock('={ material: bestand.material, status: "beendet" }', "verrechnung")),
     doc="Simuliert das Beenden der wiederkehrenden Verrechnung.")
node("r_info", "serviceTask", "Rückgabe melden", J2 + 430, M, rest_status("returned"),
     doc="Meldet die Rückgabe über die Positionsroute (status returned)." + REST_DOC)
node("r_end", "endEvent", "Zusatzleistung zurückgegeben", J2 + 560, M, '<terminateEventDefinition/>',
     doc="Terminierendes Ende: beendet den Strang der Position vollständig.")

flow("z_f0", "z_start", "z_procs")
flow("z_f0b", "z_procs", "z_read")
flow("z_f1", "z_read", "z_eigen")
flow("z_f1b", "z_eigen", "z_regel")
flow("z_f3", join, "z_apply")
flow("z_f4", "z_apply", "z_bill")
flow("z_f5", "z_bill", "z_done")
flow("z_f6", "z_done", "j_aktiv")
flow("w_f1", "j_aktiv", "g_warten")
flow("w_f2", "g_warten", "c_rueck")
flow("w_f4", "g_warten", "c_aendern", "b", "l")
flow("w_f6", "c_aendern", "sp_aendern", b=(SPX, QY))
flow("w_f8", "sp_aendern", "j_aktiv", a=(SPX + 30, sy + sh), b=(JX, M + 25), via=[(SPX + 30, sy + sh + 30), (JX, sy + sh + 30)])
flow("r_f1", "c_rueck", "j_rueck1")
flow("r_f2", "b_rueck", "j_rueck1", a=(BQ, sy - 18), b=(BQ, M + 25), via=[])
flow("r_f3", "j_rueck1", "j_rueck2")
flow("r_f5", "r_start", "r_bestand")
flow("r_f6", "r_bestand", "j_rueck2", "r", "b")
flow("r_f7", "j_rueck2", "r_remove")
flow("r_f8", "r_remove", "r_bill")
flow("r_f9", "r_bill", "r_info")
flow("r_f10", "r_info", "r_end")

PROCESS_DOC = """SPoP: Lebenszyklus einer Zusatzleistung zu einer SharePoint-OnPrem-Site, ein Strang je Position (Katalogform per-position).
Zwei Produkte binden diesen Prozess (die Speicherplatzerweiterung, 540007510, ist die Aktion quota-erhoehen der Site):
- spop-reporting       Erweitertes Reporting, 1–20 Pakete: 1–5 = 540007511, 6–10 = 540007703, 11–15 = 540007702, 16–20 = 540007704
- spop-subsite-quota   SubSite Quota, 11–500 Subsites: 11–20 = 540007513, 21–50 = 540007705, 51–100 = 540007706,
                       101–200 = 540007707, 201–500 = 540007708
Die Stufen sind Verrechnungsbänder einer Menge, keine eigenen Produkte: die Richtlinie SpopZusatzRichtlinie leitet Band und
Materialnummer aus Produkt und Menge ab. Die Site nennt die bestellende Person mit ihrer URL (siteUrl), weil sie einer anderen
Position gehört. Der Strang sucht diese Position über die Instanzsuche (Secret "atlas") und lehnt ab, wenn die Site nicht
aktiv ist oder nicht der bestellenden Person gehört (sie muss Besteller oder Empfänger der Site-Position sein). Nach der
Bereitstellung wartet der Strang auf spop.zusatz.aendern (Subprozess «Menge ändern») oder
spop.zusatz.rueckgabe (auch als Boundary am Subprozess). Ausführung und Verrechnung im PoC simuliert; Rückmeldungen an den
Katalog echt (Decision-Route, Positionsroute mit Secret "atlas", Shop-Aufgaben)."""

write(OUT, "defs_spop_zusatz", "proc_spop_zusatz", "SPoP: Zusatzleistung-Lebenszyklus", PROCESS_DOC,
      [("m_bestellen", "spop.zusatz.bestellen", None),
       ("m_aendern", "spop.zusatz.aendern", KEY),
       ("m_rueckgabe", "spop.zusatz.rueckgabe", KEY)])
