#!/usr/bin/env python3
"""Erzeugt spop-teamsite.bpmn mit von Hand berechnetem BPMN-DI: python3 tools/gen_bpmn.py spop-teamsite.bpmn (aus examples/spop).

Ein einziger Strang je Position (Muster lc_proc_m365_funktionspostfach), Änderungen als Subprozesse."""
import sys
from xml.sax.saxutils import escape, quoteattr

OUT = sys.argv[1]
SIZES = {"task": (100, 80), "gw": (50, 50), "ev": (36, 36)}
KIND = {"startEvent": "ev", "endEvent": "ev", "intermediateCatchEvent": "ev", "boundaryEvent": "ev",
        "exclusiveGateway": "gw", "parallelGateway": "gw", "eventBasedGateway": "gw",
        "serviceTask": "task", "sendTask": "task", "userTask": "task", "businessRuleTask": "task", "scriptTask": "task",
        "subProcess": "sp"}
nodes, flows, pos = [], [], {}
spbox = {}


def node(nid, tag, name, cx, cy, inner="", attrs="", doc=None, label="below", parent=None, lw=None, lpos=None):
    nodes.append(dict(id=nid, tag=tag, name=name, inner=inner, attrs=attrs, doc=doc, label=label, parent=parent, lw=lw, lpos=lpos))
    pos[nid] = (cx, cy, KIND[tag])


def subprocess(nid, name, x, y, w, h, doc, parent=None, inner=""):
    nodes.append(dict(id=nid, tag="subProcess", name=name, inner=inner, attrs="", doc=doc, label=None, parent=parent, lw=None, lpos=None))
    pos[nid] = (x + w / 2, y + h / 2, "sp")
    spbox[nid] = (x, y, w, h)


def box(nid):
    if nid in spbox:
        return spbox[nid]
    cx, cy, k = pos[nid]
    w, h = SIZES[k]
    return cx - w / 2, cy - h / 2, w, h


def side(nid, s):
    x, y, w, h = box(nid)
    return {"l": (x, y + h / 2), "r": (x + w, y + h / 2), "t": (x + w / 2, y), "b": (x + w / 2, y + h)}[s]


def flow(fid, src, tgt, s1="r", s2="l", via=None, name=None, cond=None, label=None, parent=None, a=None, b=None):
    a = a or side(src, s1)
    b = b or side(tgt, s2)
    pts = [a]
    if via is not None:
        pts += via
    elif a[1] != b[1] and a[0] != b[0]:
        pts.append((a[0], b[1]) if s1 in ("t", "b") else (b[0], a[1]))
    pts.append(b)
    flows.append(dict(id=fid, src=src, tgt=tgt, name=name, cond=cond, pts=pts, label=label, parent=parent))


def mock(result, var, mn="PT1S", mx="PT2S"):
    return (f'<atlas:mockupConnector minDuration="{mn}" maxDuration="{mx}" '
            f'resultExpression={quoteattr(result)} resultVariable="{var}"/>')


def ext(*parts):
    return "<extensionElements>" + "".join(parts) + "</extensionElements>"


def io(*pairs):
    return "<zeebe:ioMapping>" + "".join(f'<zeebe:input source={quoteattr(s)} target="{t}"/>' for s, t in pairs) + "</zeebe:ioMapping>"


def brt(decision, var, *pairs):
    return ext(f'<zeebe:calledDecision decisionId="{decision}" resultVariable="{var}"/>', io(*pairs))


def ut(form, group="SPoP-Freigabe"):
    return ext(f'<zeebe:assignmentDefinition candidateGroups="{group}"/>', f'<zeebe:formDefinition formId="{form}"/>')


def script(expr, var):
    return ext(f'<zeebe:script expression={quoteattr(expr)} resultVariable="{var}"/>')


API = '=(if atlasApiBase = null then application.ATLAS_API_BASE else atlasApiBase) + "/api/v1/orders/" + orderId + "/lines/"'


def rest_decision(by, reason):
    return ext(f'<atlas:restConnector method="POST" url={quoteattr(API + ' + itemId + "/decision"')} authType="bearer" authSecret="atlas"/>',
               io(('="' + by + '"', "by"), (reason, "reason")))


def rest_status(status):
    return ext(f'<atlas:restConnector method="POST" url={quoteattr(API + ' + replace((if positionId = null then itemId else positionId), "#", "%23")')} authType="bearer" authSecret="atlas"/>',
               io(('="' + status + '"', "status")))


def shop(action, outcome):
    return ext(f'<atlas:shopTask mode="outcome" action="{action}" outcome="{outcome}"/>')


REASON_FREIGABE = '="Freigabe verweigert: " + (if freigabeKommentar = null then "ohne Kommentar" else freigabeKommentar)'
REST_DOC = (" Braucht das Secret \"atlas\" mit einem API-Token der Rolle operator; die Adresse ist atlasApiBase, sonst die "
            "Applikationskonstante ATLAS_API_BASE. Eine Position mit Variante heisst itemId#variantId; das # wird als %23 "
            "kodiert, sonst schneidet es die URL ab.")
REJ_FREIGABE = '={ benachrichtigt: true, grund: "Freigabe verweigert: " + (if freigabeKommentar = null then "ohne Kommentar" else freigabeKommentar) }'

# ======================================================================= Bereitstellung (GF-001), flach
M = 700
U, L1, L2 = -150, 150, 280
node("s_neu", "startEvent", "TeamSite bestellt", 118, M, '<messageEventDefinition messageRef="m_bestellen"/>',
     doc="GF-001: Start der Operation provision (Formular spop-teamsite-bestellung): titel, amtKuerzel, siteTyp, sprache, ownerUpn, "
         "memberUpns, quotaWahl, quotaMb, quotaBegruendung, bemerkung. Vom Katalog kommen orderId, positionId, itemId dazu; "
         "sie bilden den Korrelationsschlüssel aller späteren Aktionen.")
node("a_regel", "businessRuleTask", "Richtlinien prüfen (S6)", 250, M,
     brt("SpopTeamsiteRichtlinie", "regel",
         ('=amtKuerzel != null and matches(amtKuerzel, "^[A-Z]{2,6}$")', "amtGueltig"),
         ("=siteTyp", "siteTyp"),
         ('=if quotaWahl = null then "Standard" else quotaWahl', "quotaWahl"),
         ("=if quotaMb = null then 0 else quotaMb", "quotaMb"),
         ("=quotaBegruendung != null and string length(quotaBegruendung) >= 10", "begruendungVorhanden")),
     doc="DMN SpopTeamsiteRichtlinie anstelle von amt_naming.rego, quota_limits.rego und approval.rego. Liefert regel = "
         "{zulaessig, vierAugen, quotaEffektivMb, grund}. Grenzwerte sind Annahmen und im DMN gepflegt.")
node("a_gw_ok", "exclusiveGateway", "Zulässig?", 400, M, attrs=' default="a_f_ok_nein"',
     doc="Fail-closed: Der Default-Fluss ist die Ablehnung.")
node("a_gw_4e", "exclusiveGateway", "Vier-Augen nötig?", 530, M, attrs=' default="a_f_4e_nein"', label="above",
     doc="approval.rego: zweiter Approver ausser beim Self-Service Standard.")
node("a_rej1", "serviceTask", "Ablehnung melden", 530, M + U, rest_decision("Richtlinie SpopTeamsiteRichtlinie", "=regel.grund"),
     doc="Meldet die Ablehnung über POST /api/v1/orders/{orderId}/lines/{itemId}/decision mit dem Grund der Richtlinie. Eine "
         "verletzte Richtlinie ist eine Entscheidung, keine Störung." + REST_DOC)
node("a_end_rej1", "endEvent", "Abgelehnt: Richtlinie", 660, M + U)
node("a_prep", "scriptTask", "Freigabe vorbereiten", 660, M + L1,
     script('="TeamSite-Bestellung (GF-001)\\nAmt: " + amtKuerzel + "\\nTitel: " + titel + "\\nSite-Typ: " + siteTyp + '
            '"\\nOwner: " + ownerUpn + "\\nQuota: " + string(regel.quotaEffektivMb) + " MB (" + quotaWahl + ")" + '
            '(if quotaBegruendung = null then "" else "\\nBegründung: " + quotaBegruendung) + "\\nRegel: " + regel.grund',
            "zusammenfassung"),
     doc="Schreibt die Zusammenfassung für das Freigabeformular spop-freigabe.")
node("a_ut", "userTask", "Bestellung freigeben", 810, M + L1, ut("spop-freigabe"),
     doc="Vier-Augen-Freigabe durch die Gruppe SPoP-Freigabe (Service Owner DO-COC-SPC), Formular spop-freigabe.")
node("a_gw_fr", "exclusiveGateway", "Freigegeben?", 960, M + L1, attrs=' default="a_f_fr_nein"', label="above")
node("a_rej2", "serviceTask", "Ablehnung melden", 1090, M + L2, rest_decision("Vier-Augen-Freigabe", REASON_FREIGABE),
     doc="Meldet die verweigerte Vier-Augen-Freigabe über die Decision-Route der Position, mit dem Kommentar als Grund." + REST_DOC)
node("a_end_rej2", "endEvent", "Abgelehnt: Freigabe", 1220, M + L2)
node("a_join", "exclusiveGateway", "", 1090, M)
node("a_owner", "serviceTask", "Owner im AD prüfen", 1220, M, ext(mock('={ existiert: true, upn: ownerUpn }', "ownerPruefung")),
     doc="Simuliert apsbw_ad_user read.")
node("a_site", "serviceTask", "TeamSite erstellen (S1)", 1370, M,
     ext(mock('={ url: "/sites/" + lower case(amtKuerzel) + "-" + lower case(siteTyp), titel: amtKuerzel + "-" + siteTyp + " " + titel, template: "STS#0", sprache: if sprache = null then "1031" else sprache }', "site", "PT2S", "PT4S")),
     doc="Simuliert apsbw_sharepoint_site create (Worker sharepointserver: create-site). Schreibt site; der Strang trägt site "
         "bis zur Rückgabe, alle Aktionen lesen die Site-URL daraus.")
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
     ext(mock('={ siteUrl: site.url, features: ["TeamCollab"], typFeature: siteTyp }', "features")),
     doc="Simuliert apsbw_sharepoint_feature activate; feature_mapping je Site-Typ ist offen.")
node("a_parj", "parallelGateway", "", 1910, M)
node("a_info", "serviceTask", "Position als erledigt melden", 2040, M, rest_status("done"),
     doc="Meldet die Position über POST /api/v1/orders/{orderId}/lines/{positionId} als erledigt (status done); erst damit "
         "steht die TeamSite im Bestand und die Aktionen werden möglich." + REST_DOC)

# ======================================================================= Warten auf Aufträge
node("j_aktiv", "exclusiveGateway", "", 2170, M)
node("g_warten", "eventBasedGateway", "Aktiv: auf Auftrag warten", 2330, M, label="custom", lpos=(2195, M - 62, 100, 27),
     doc="Die TeamSite ist in Betrieb. Der Strang wartet auf die nächste Aktion der Position: Quota-Erhöhung, externe Einladung "
         "oder Rückgabe. Jede Aktion ist an orderId/positionId korreliert. Während eine Aktion läuft, wartet der Strang nicht; "
         "eine zweite Aktion derselben Position wird erst nach Abschluss der ersten angenommen.")
node("c_rueck", "intermediateCatchEvent", "Rückgabe angefordert", 2440, M, '<messageEventDefinition messageRef="m_rueckgabe"/>',
     doc="Operation deprovision (Formular spop-teamsite-rueckgabe: grund).", lw=90)
node("c_extern", "intermediateCatchEvent", "Einladung angefragt", 2440, 330, '<messageEventDefinition messageRef="m_extern"/>',
     doc="GF-003: Aktion extern-einladen (Formular spop-extern-einladen: email, rolle, ablaufTage, nachricht).", lw=90)
node("c_quota", "intermediateCatchEvent", "Quota-Erhöhung angefragt", 2440, 960, '<messageEventDefinition messageRef="m_quota"/>',
     doc="GF-002: Aktion quota-erhoehen (Formular spop-quota-erhoehung: neueQuotaMb, begruendung).", lw=90)

SPX = 2490
IU, IL1, IL2 = -120, 120, 220


def change_sub(p, sid, action, name, cy, width, doc, outputs, read_name, read_mock, read_doc, rule_name, decision, inputs, rule_doc, summary, ut_name):
    out = "".join(f'<zeebe:output source={quoteattr(src)} target="{tgt}"/>' for src, tgt in outputs)
    subprocess(sid, name, SPX, cy - 180, width, 480, doc, inner=ext("<zeebe:ioMapping>" + out + "</zeebe:ioMapping>"))
    X = lambda o: SPX + o
    node(f"{p}_s", "startEvent", "", X(40), cy, parent=sid)
    node(f"{p}_read", "serviceTask", read_name, X(140), cy, ext(mock(read_mock, "aktuell")), doc=read_doc, parent=sid)
    node(f"{p}_regel", "businessRuleTask", rule_name, X(290), cy, brt(decision, "regel", *inputs), doc=rule_doc, parent=sid)
    node(f"{p}_gw_ok", "exclusiveGateway", "Zulässig?", X(440), cy, attrs=f' default="{p}_f_ok_nein"', parent=sid)
    node(f"{p}_gw_4e", "exclusiveGateway", "Vier-Augen nötig?", X(570), cy, attrs=f' default="{p}_f_4e_nein"', label="above", parent=sid)
    node(f"{p}_rej1", "sendTask", "Ablehnung melden", X(570), cy + IU, shop(action, "rejected"),
         doc=f"Shop-Aufgabe (Modus outcome): beantwortet die Aktion {action} mit rejected, weil die Richtlinie sie nicht zulässt.", parent=sid)
    node(f"{p}_end_rej1", "endEvent", "Abgelehnt: Richtlinie", X(700), cy + IU, parent=sid)
    node(f"{p}_prep", "scriptTask", "Freigabe vorbereiten", X(700), cy + IL1, script(summary, "zusammenfassung"),
         doc="Schreibt die Zusammenfassung für das Freigabeformular spop-freigabe.", parent=sid)
    node(f"{p}_ut", "userTask", ut_name, X(850), cy + IL1, ut("spop-freigabe"),
         doc="Vier-Augen-Freigabe durch die Gruppe SPoP-Freigabe.", parent=sid)
    node(f"{p}_gw_fr", "exclusiveGateway", "Freigegeben?", X(1000), cy + IL1, attrs=f' default="{p}_f_fr_nein"', label="above", parent=sid)
    node(f"{p}_rej2", "sendTask", "Ablehnung melden", X(1130), cy + IL2, shop(action, "rejected"),
         doc=f"Shop-Aufgabe (Modus outcome): beantwortet die Aktion {action} mit rejected, weil die Vier-Augen-Freigabe verweigert wurde.", parent=sid)
    node(f"{p}_end_rej2", "endEvent", "Abgelehnt: Freigabe", X(1260), cy + IL2, parent=sid)
    node(f"{p}_join", "exclusiveGateway", "", X(1130), cy, parent=sid)
    P = dict(parent=sid)
    flow(f"{p}_f0", f"{p}_s", f"{p}_read", **P)
    flow(f"{p}_f1", f"{p}_read", f"{p}_regel", **P)
    flow(f"{p}_f2", f"{p}_regel", f"{p}_gw_ok", **P)
    flow(f"{p}_f_ok_ja", f"{p}_gw_ok", f"{p}_gw_4e", name="Ja", cond="=regel.zulaessig = true", label=(X(474), cy - 22, 20, 14), **P)
    flow(f"{p}_f_ok_nein", f"{p}_gw_ok", f"{p}_rej1", "t", "l", name="Nein", label=(X(448), cy - 80, 26, 14), **P)
    flow(f"{p}_f_rej1", f"{p}_rej1", f"{p}_end_rej1", **P)
    flow(f"{p}_f_4e_ja", f"{p}_gw_4e", f"{p}_prep", "b", "l", name="Ja", cond="=regel.vierAugen = true", label=(X(578), cy + 70, 20, 14), **P)
    flow(f"{p}_f_4e_nein", f"{p}_gw_4e", f"{p}_join", name="Nein", label=(X(605), cy - 22, 26, 14), **P)
    flow(f"{p}_f_prep", f"{p}_prep", f"{p}_ut", **P)
    flow(f"{p}_f_ut", f"{p}_ut", f"{p}_gw_fr", **P)
    flow(f"{p}_f_fr_ja", f"{p}_gw_fr", f"{p}_join", "r", "b", name="Ja", cond='=freigabeEntscheid = "freigegeben"', label=(X(1035), cy + IL1 - 22, 20, 14), **P)
    flow(f"{p}_f_fr_nein", f"{p}_gw_fr", f"{p}_rej2", "b", "l", name="Nein", label=(X(1008), cy + IL2 - 50, 26, 14), **P)
    flow(f"{p}_f_rej2", f"{p}_rej2", f"{p}_end_rej2", **P)
    return X


# ----------------------------------------------------------------------- Subprozess Quota (GF-002), unterhalb
QY = 960
X = change_sub(
    "q", "sp_quota", "quota-erhoehen", "Quota erhöhen (GF-002)", QY, 1600,
    "GF-002 SP OnPrem Quota-Erhöhung als Subprozess des Strangs. Liest die Site und die aktuelle Quota aus dem Strang "
    "(site, quota), prüft die Richtlinie, holt bei Bedarf die Vier-Augen-Freigabe und schreibt quota fort. "
    "Eine Ablehnung endet im Subprozess; danach wartet der Strang wieder auf Aufträge. Output-Mapping quota: Ergebnisse innerer "
    "Aufgaben bleiben im Subprozess (ADR-0074); nur so erreicht die neue Quota den Strang. Endet der Subprozess mit einer "
    "Ablehnung, löst =quota den unveränderten Wert des Strangs auf.",
    [("=quota", "quota")],
    "Site und Quota lesen (S1)",
    '={ gefunden: site != null, siteUrl: site.url, siteTyp: siteTyp, quotaMb: quota.storageMb }',
    "Simuliert apsbw_sharepoint_site read und apsbw_sharepoint_quota read. Im PoC aus dem Stand des Strangs (site, quota). Schreibt aktuell.",
    "Quota-Richtlinie prüfen (S3)", "SpopQuotaRichtlinie",
    [("=aktuell.gefunden", "siteGefunden"), ("=aktuell.siteTyp", "siteTyp"),
     ("=if neueQuotaMb = null then 0 else neueQuotaMb", "neueQuotaMb"),
     ("=(if neueQuotaMb = null then 0 else neueQuotaMb) - aktuell.quotaMb", "erhoehungMb"),
     ("=begruendung != null and string length(begruendung) >= 10", "begruendungVorhanden")],
    "DMN SpopQuotaRichtlinie anstelle von quota_limits.rego und approval.rego: Begründung Pflicht, neue Quota grösser als die aktuelle, "
    "Maximum je Site-Typ, Vier-Augen-Prinzip ab einer Erhöhung um mehr als 5120 MB (Annahme).",
    '="Quota-Erhöhung (GF-002)\\nSite: " + site.url + "\\nAktuell: " + string(aktuell.quotaMb) + " MB\\nNeu: " + '
    'string(neueQuotaMb) + " MB\\nBegründung: " + begruendung + "\\nRegel: " + regel.grund',
    "Quota-Erhöhung freigeben")
node("q_apply", "serviceTask", "Quota setzen (S2)", X(1260), QY,
     ext(mock('={ siteUrl: site.url, storageMb: neueQuotaMb, alteQuotaMb: aktuell.quotaMb }', "quota")),
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
    "x", "sp_extern", "extern-einladen", "Externen Benutzer einladen (GF-003)", EY, 1970,
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
     ("=rolle", "rolle"), ("=if ablaufTage = null then 30 else ablaufTage", "ablaufTage")],
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
     doc="Rückfallebene wie im Referenzmodell: Eine Rückgabe für eine Position ohne laufenden Strang (z. B. eine vor der Umstellung "
         "bereitgestellte Site) startet hier. Der Katalog verlangt für per-position diesen Nachrichtenstart.", lw=100)
node("r_site", "scriptTask", "Site ermitteln", BQ + 260, QY,
     script('={ url: "/sites/" + lower case(amtKuerzel) + "-" + lower case(siteTyp) }', "site"),
     doc="Bildet die Site-URL aus der Konfiguration der Position nach demselben Schema wie die Bereitstellung (Annahme: der "
         "Katalog liefert amtKuerzel und siteTyp der Position mit).")
node("r_extern", "serviceTask", "Externe Zugriffe entfernen", BE + 130, M,
     ext(mock('={ siteUrl: site.url, entfernt: if externe = null then [] else externe }', "externEntfernt")),
     doc="Simuliert apsbw_sharepoint_externaluser remove für jeden Eintrag in externe, der Liste der angenommenen Einladungen des Strangs.")
node("r_delete", "serviceTask", "TeamSite löschen", BE + 280, M,
     ext(mock('={ siteUrl: site.url, status: "im Papierkorb" }', "rueckbau")),
     doc="Simuliert apsbw_sharepoint_site delete (Worker sharepointserver: delete-site, Papierkorb).")
node("r_info", "serviceTask", "Rückgabe melden", BE + 430, M, rest_status("returned"),
     doc="Meldet die Rückgabe über die Positionsroute (status returned); damit endet das Recht im Bestand." + REST_DOC)
node("r_end", "endEvent", "TeamSite zurückgegeben", BE + 560, M, '<terminateEventDefinition/>',
     doc="Terminierendes Ende: beendet den Strang der Position vollständig.")

# ----------------------------------------------------------------------- Flüsse Hauptstrang
flow("a_f1", "s_neu", "a_regel")
flow("a_f2", "a_regel", "a_gw_ok")
flow("a_f_ok_ja", "a_gw_ok", "a_gw_4e", name="Ja", cond="=regel.zulaessig = true", label=(434, M - 22, 20, 14))
flow("a_f_ok_nein", "a_gw_ok", "a_rej1", "t", "l", name="Nein", label=(408, M - 100, 26, 14))
flow("a_f_rej1", "a_rej1", "a_end_rej1")
flow("a_f_4e_ja", "a_gw_4e", "a_prep", "b", "l", name="Ja", cond="=regel.vierAugen = true", label=(538, M + 100, 20, 14))
flow("a_f_4e_nein", "a_gw_4e", "a_join", name="Nein", label=(565, M - 22, 26, 14))
flow("a_f_prep", "a_prep", "a_ut")
flow("a_f_ut", "a_ut", "a_gw_fr")
flow("a_f_fr_ja", "a_gw_fr", "a_join", "r", "b", name="Ja", cond='=freigabeEntscheid = "freigegeben"', label=(995, M + L1 - 22, 20, 14))
flow("a_f_fr_nein", "a_gw_fr", "a_rej2", "b", "l", name="Nein", label=(968, M + L2 - 60, 26, 14))
flow("a_f_rej2", "a_rej2", "a_end_rej2")
flow("a_f3", "a_join", "a_owner")
flow("a_f4", "a_owner", "a_site")
flow("a_f5", "a_site", "a_par")
flow("a_f6", "a_par", "a_grp", "t", "l")
flow("a_f7", "a_grp", "a_rechte")
flow("a_f8", "a_rechte", "a_parj", "r", "t")
flow("a_f9", "a_par", "a_quota")
flow("a_f10", "a_quota", "a_parj")
flow("a_f11", "a_par", "a_feat", "b", "l")
flow("a_f12", "a_feat", "a_parj", "r", "b")
flow("a_f13", "a_parj", "a_info")
flow("a_f14", "a_info", "j_aktiv")
flow("w_f1", "j_aktiv", "g_warten")
flow("w_f2", "g_warten", "c_rueck")
flow("w_f3", "g_warten", "c_extern", "t", "l")
flow("w_f4", "g_warten", "c_quota", "b", "l")
flow("w_f5", "c_extern", "sp_extern", b=(SPX, EY))
flow("w_f6", "c_quota", "sp_quota", b=(SPX, QY))
# Rückkehr der Subprozesse zum Warten (Back-Edges)
flow("w_f7", "sp_extern", "j_aktiv", a=(SPX + 30, ey), b=(2170, M - 25), via=[(SPX + 30, ey - 30), (2170, ey - 30)])
flow("w_f8", "sp_quota", "j_aktiv", a=(SPX + 30, qy + qh), b=(2170, M + 25), via=[(SPX + 30, qy + qh + 30), (2170, qy + qh + 30)])
# Rückgabe
flow("r_f1", "c_rueck", "j_rueck1")
flow("r_f2", "b_rueck_q", "j_rueck1", a=(BQ, qy - 18), b=(BQ, M + 25), via=[])
flow("r_f3", "j_rueck1", "j_rueck2")
flow("r_f4", "b_rueck_x", "j_rueck2", a=(BE, ey + eh + 18), b=(BE, M - 25), via=[])
flow("r_f5", "r_start", "r_site")
flow("r_f6", "r_site", "j_rueck2", "r", "b")
flow("r_f7", "j_rueck2", "r_extern")
flow("r_f8", "r_extern", "r_delete")
flow("r_f9", "r_delete", "r_info")
flow("r_f10", "r_info", "r_end")

# ======================================================================= Ausgabe
incoming, outgoing = {}, {}
for f in flows:
    outgoing.setdefault(f["src"], []).append(f["id"])
    incoming.setdefault(f["tgt"], []).append(f["id"])

KEY = '=orderId + "/" + (if positionId = null then itemId else positionId)'
PROCESS_DOC = """SPoP: Lebenszyklus einer SharePoint-OnPrem-TeamSite als ein einziger Strang je Position (Muster lc_proc_m365_funktionspostfach).
Bereitstellung (GF-001) läuft flach; danach wartet der Strang am ereignisbasierten Gateway «Aktiv: auf Auftrag warten» auf:
- spop.teamsite.quota     → Subprozess «Quota erhöhen (GF-002)»                 (Katalog: change, Formular spop-quota-erhoehung)
- spop.teamsite.extern    → Subprozess «Externen Benutzer einladen (GF-003)»    (Katalog: change, Formular spop-extern-einladen)
- spop.teamsite.rueckgabe → Rückgabe, auch als Boundary an beiden Subprozessen  (Katalog: deprovision, Formular spop-teamsite-rueckgabe)
Nach jedem Subprozess kehrt der Strang zum Warten zurück. Alle späteren Aktionen sind an orderId + "/" + positionId korreliert
(Katalogform per-position). Der Strang trägt den Stand der Site (site, quota), Aktionen müssen die Site deshalb nicht mehr nennen.
Rückfallebene: Nachrichtenstart «Rückgabe ohne laufenden Strang». Regeln: DMN SpopTeamsiteRichtlinie, SpopQuotaRichtlinie,
SpopExternRichtlinie. Vier-Augen-Freigabe: Gruppe SPoP-Freigabe, Formular spop-freigabe. Ausführung gegen APS-BW im PoC simuliert
(atlas:mockupConnector); jede Aufgabe nennt die APS-BW-Ressource, die sie ersetzt. Rückmeldungen an den Katalog sind echt: Ablehnung
der Bestellung über die Decision-Route, Bereitstellung und Rückgabe über die Positionsroute (Secret "atlas", Konstante ATLAS_API_BASE),
Änderungsaktionen über Shop-Aufgaben (atlas:shopTask, outcome completed, rejected oder failed). Katalogprodukt: spop-teamsite."""

o = ['<?xml version="1.0" encoding="UTF-8"?>',
     '<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" '
     'xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" '
     'xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas/schema/1.0" '
     'id="defs_spop_teamsite" targetNamespace="http://atlas/bpmn">',
     '<message id="m_bestellen" name="spop.teamsite.bestellen"/>']
for mid, mname in (("m_quota", "spop.teamsite.quota"), ("m_extern", "spop.teamsite.extern"), ("m_rueckgabe", "spop.teamsite.rueckgabe")):
    o.append(f'<message id="{mid}" name="{mname}"><extensionElements><zeebe:subscription correlationKey={quoteattr(KEY)}/></extensionElements></message>')
o.append('<message id="m_angenommen" name="spop.extern.angenommen"><extensionElements><zeebe:subscription correlationKey="=einladung.einladungId"/></extensionElements></message>')
o.append('<process id="proc_spop_teamsite" name="SPoP: TeamSite-Lebenszyklus" isExecutable="true">')
o.append(f'<documentation>{escape(PROCESS_DOC)}</documentation>')
o.append('<extensionElements><atlas:constants><atlas:constant name="ATLAS_API_BASE" type="string" value="http://127.0.0.1:8080" source="application"/></atlas:constants></extensionElements>')


def emit_node(n):
    name = f' name={quoteattr(n["name"])}' if n["name"] else ""
    body = f'<documentation>{escape(n["doc"])}</documentation>' if n["doc"] else ""
    inner = n["inner"]
    if inner.startswith("<extensionElements>"):
        body += inner
        inner = ""
    body += "".join(f"<incoming>{i}</incoming>" for i in incoming.get(n["id"], []))
    body += "".join(f"<outgoing>{i}</outgoing>" for i in outgoing.get(n["id"], []))
    if n["tag"] == "subProcess":
        for c in nodes:
            if c["parent"] == n["id"]:
                body += emit_node(c)
        for f in flows:
            if f["parent"] == n["id"]:
                body += emit_flow(f)
    body += inner
    return f'<{n["tag"]} id="{n["id"]}"{name}{n["attrs"]}>{body}</{n["tag"]}>\n'


def emit_flow(f):
    name = f' name={quoteattr(f["name"])}' if f["name"] else ""
    if f["cond"]:
        return (f'<sequenceFlow id="{f["id"]}"{name} sourceRef="{f["src"]}" targetRef="{f["tgt"]}">'
                f'<conditionExpression>{escape(f["cond"])}</conditionExpression></sequenceFlow>\n')
    return f'<sequenceFlow id="{f["id"]}"{name} sourceRef="{f["src"]}" targetRef="{f["tgt"]}"/>\n'


for n in nodes:
    if n["parent"] is None:
        o.append(emit_node(n).rstrip("\n"))
for f in flows:
    if f["parent"] is None:
        o.append(emit_flow(f).rstrip("\n"))
o.append('</process>')
o.append('<bpmndi:BPMNDiagram id="dia_spop"><bpmndi:BPMNPlane id="plane_spop" bpmnElement="proc_spop_teamsite">')


def fmt(v):
    v = float(v)
    return str(int(v)) if v.is_integer() else str(v)


k = 0
for n in nodes:
    k += 1
    x, y, w, h = box(n["id"])
    kind = pos[n["id"]][2]
    extra = ' isExpanded="true"' if kind == "sp" else ""
    lab = ""
    if kind in ("ev", "gw") and n["name"]:
        if n["lpos"]:
            lx, ly, lw, lh = n["lpos"]
        else:
            lw = n["lw"] or max(60, min(110, 7 * len(n["name"])))
            lh = 27 if 7 * len(n["name"]) > lw else 14
            cx, cy, _ = pos[n["id"]]
            lx = cx - lw / 2
            ly = y - lh - 6 if n["label"] == "above" else y + h + 4
        lab = f'<bpmndi:BPMNLabel><dc:Bounds x="{fmt(lx)}" y="{fmt(ly)}" width="{lw}" height="{lh}"/></bpmndi:BPMNLabel>'
    o.append(f'<bpmndi:BPMNShape id="d{k}" bpmnElement="{n["id"]}"{extra}><dc:Bounds x="{fmt(x)}" y="{fmt(y)}" width="{fmt(w)}" height="{fmt(h)}"/>{lab}</bpmndi:BPMNShape>')
for f in flows:
    k += 1
    wps = "".join(f'<di:waypoint x="{fmt(px)}" y="{fmt(py)}"/>' for px, py in f["pts"])
    lab = ""
    if f["label"]:
        lx, ly, lw, lh = f["label"]
        lab = f'<bpmndi:BPMNLabel><dc:Bounds x="{fmt(lx)}" y="{fmt(ly)}" width="{lw}" height="{lh}"/></bpmndi:BPMNLabel>'
    o.append(f'<bpmndi:BPMNEdge id="d{k}" bpmnElement="{f["id"]}">{wps}{lab}</bpmndi:BPMNEdge>')
o.append('</bpmndi:BPMNPlane></bpmndi:BPMNDiagram>')
o.append('</definitions>')
open(OUT, "w").write("\n".join(o) + "\n")
print("ok", len(nodes), "nodes", len(flows), "flows")
