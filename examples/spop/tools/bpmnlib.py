"""Gemeinsame Bausteine der SPoP-Generatoren: Knoten, Flüsse, Subprozesse mit von Hand berechnetem BPMN-DI.

Jeder Generator ist ein eigener Python-Prozess; der Modulzustand (nodes, flows) gehört deshalb genau einem Modell.
"""
from xml.sax.saxutils import escape, quoteattr

SIZES = {"task": (100, 80), "gw": (50, 50), "ev": (36, 36)}
KIND = {"startEvent": "ev", "endEvent": "ev", "intermediateCatchEvent": "ev", "boundaryEvent": "ev",
        "exclusiveGateway": "gw", "parallelGateway": "gw", "eventBasedGateway": "gw",
        "serviceTask": "task", "sendTask": "task", "userTask": "task", "businessRuleTask": "task", "scriptTask": "task",
        "subProcess": "sp"}
nodes, flows, pos, spbox = [], [], {}, {}


def node(nid, tag, name, cx, cy, inner="", attrs="", doc=None, label="below", parent=None, lw=None, lpos=None, tail=""):
    """tail: Inhalt nach incoming/outgoing, z. B. multiInstanceLoopCharacteristics."""
    nodes.append(dict(id=nid, tag=tag, name=name, inner=inner, attrs=attrs, doc=doc, label=label, parent=parent, lw=lw, lpos=lpos, tail=tail))
    pos[nid] = (cx, cy, KIND[tag])


def subprocess(nid, name, x, y, w, h, doc, parent=None, inner="", attrs=""):
    nodes.append(dict(id=nid, tag="subProcess", name=name, inner=inner, attrs=attrs, doc=doc, label=None, parent=parent, lw=None, lpos=None, tail=""))
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
    return ext(f'<atlas:restConnector method="POST" url={quoteattr(API + " + itemId + " + chr(34) + "/decision" + chr(34))} authType="bearer" authSecret="atlas"/>',
               io(('="' + by + '"', "by"), (reason, "reason")))


def rest_status(status):
    url = API + ' + replace((if positionId = null then itemId else positionId), "#", "%23")'
    return ext(f'<atlas:restConnector method="POST" url={quoteattr(url)} authType="bearer" authSecret="atlas"/>',
               io(('="' + status + '"', "status")))


def shop(action, outcome):
    return ext(f'<atlas:shopTask mode="outcome" action="{action}" outcome="{outcome}"/>')


REASON_FREIGABE = '="Freigabe verweigert: " + (if freigabeKommentar = null then "ohne Kommentar" else freigabeKommentar)'
REST_DOC = (" Braucht das Secret \"atlas\" mit einem API-Token der Rolle operator; die Adresse ist atlasApiBase, sonst die "
            "Applikationskonstante ATLAS_API_BASE. Eine Position mit Variante heisst itemId#variantId; das # wird als %23 "
            "kodiert, sonst schneidet es die URL ab.")
KEY = '=orderId + "/" + (if positionId = null then itemId else positionId)'

IU, IL1, IL2 = -120, 120, 220


def change_sub(spx, p, sid, action, name, cy, width, doc, outputs, read_name, read_mock, read_doc,
               rule_name, decision, inputs, rule_doc, summary, ut_name, trigger=None):
    """Subprozess einer Änderungsaktion: lesen → Regel → zulässig? → Vier Augen? → (Freigabe) → join.

    Ablehnungen enden im Subprozess mit einer Shop-Aufgabe rejected. Gibt X (Offset → Koordinate) zurück; der Aufrufer
    hängt die eigentliche Änderung an f"{p}_join" an.

    trigger=(message_id, Name des Starts) macht daraus einen nicht unterbrechenden Ereignis-Subprozess (ADR-0082): Er
    startet bei jeder Nachricht neu, läuft neben dem Strang her und braucht keinen Rückweg zum Warten.
    """
    out = "".join(f'<zeebe:output source={quoteattr(src)} target="{tgt}"/>' for src, tgt in outputs)
    subprocess(sid, name, spx, cy - 180, width, 480, doc, inner=ext("<zeebe:ioMapping>" + out + "</zeebe:ioMapping>"),
               attrs=' triggeredByEvent="true"' if trigger else "")
    X = lambda o: spx + o
    if trigger:
        node(f"{p}_s", "startEvent", trigger[1], X(40), cy, f'<messageEventDefinition messageRef="{trigger[0]}"/>',
             attrs=' isInterrupting="false"', parent=sid, lw=90)
    else:
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


def check_block(p, x0, M, rule_node_id, rej_name_rule, rej_inner_rule, rej_doc_rule, prep_summary, ut_name, ut_doc,
                rej_inner_appr, rej_doc_appr, U=-150, L1=150, L2=280):
    """Zulässig? → Vier Augen? → Freigabe im flachen Strang, ab x0 (Mitte des Zulässig-Gateways). Gibt die join-id und ihr x zurück."""
    node(f"{p}_gw_ok", "exclusiveGateway", "Zulässig?", x0, M, attrs=f' default="{p}_f_ok_nein"',
         doc="Fail-closed: Der Default-Fluss ist die Ablehnung.")
    node(f"{p}_gw_4e", "exclusiveGateway", "Vier-Augen nötig?", x0 + 130, M, attrs=f' default="{p}_f_4e_nein"', label="above")
    node(f"{p}_rej1", "serviceTask", rej_name_rule, x0 + 130, M + U, rej_inner_rule, doc=rej_doc_rule)
    node(f"{p}_end_rej1", "endEvent", "Abgelehnt: Richtlinie", x0 + 260, M + U)
    node(f"{p}_prep", "scriptTask", "Freigabe vorbereiten", x0 + 260, M + L1, script(prep_summary, "zusammenfassung"),
         doc="Schreibt die Zusammenfassung für das Freigabeformular spop-freigabe.")
    node(f"{p}_ut", "userTask", ut_name, x0 + 410, M + L1, ut("spop-freigabe"), doc=ut_doc)
    node(f"{p}_gw_fr", "exclusiveGateway", "Freigegeben?", x0 + 560, M + L1, attrs=f' default="{p}_f_fr_nein"', label="above")
    node(f"{p}_rej2", "serviceTask", "Ablehnung melden", x0 + 690, M + L2, rej_inner_appr, doc=rej_doc_appr)
    node(f"{p}_end_rej2", "endEvent", "Abgelehnt: Freigabe", x0 + 820, M + L2)
    node(f"{p}_join", "exclusiveGateway", "", x0 + 690, M)
    flow(f"{p}_f2", rule_node_id, f"{p}_gw_ok")
    flow(f"{p}_f_ok_ja", f"{p}_gw_ok", f"{p}_gw_4e", name="Ja", cond="=regel.zulaessig = true", label=(x0 + 34, M - 22, 20, 14))
    flow(f"{p}_f_ok_nein", f"{p}_gw_ok", f"{p}_rej1", "t", "l", name="Nein", label=(x0 + 8, M - 100, 26, 14))
    flow(f"{p}_f_rej1", f"{p}_rej1", f"{p}_end_rej1")
    flow(f"{p}_f_4e_ja", f"{p}_gw_4e", f"{p}_prep", "b", "l", name="Ja", cond="=regel.vierAugen = true", label=(x0 + 138, M + 100, 20, 14))
    flow(f"{p}_f_4e_nein", f"{p}_gw_4e", f"{p}_join", name="Nein", label=(x0 + 165, M - 22, 26, 14))
    flow(f"{p}_f_prep", f"{p}_prep", f"{p}_ut")
    flow(f"{p}_f_ut", f"{p}_ut", f"{p}_gw_fr")
    flow(f"{p}_f_fr_ja", f"{p}_gw_fr", f"{p}_join", "r", "b", name="Ja", cond='=freigabeEntscheid = "freigegeben"', label=(x0 + 595, M + L1 - 22, 20, 14))
    flow(f"{p}_f_fr_nein", f"{p}_gw_fr", f"{p}_rej2", "b", "l", name="Nein", label=(x0 + 568, M + L2 - 60, 26, 14))
    flow(f"{p}_f_rej2", f"{p}_rej2", f"{p}_end_rej2")
    return f"{p}_join", x0 + 690


def _fmt(v):
    v = float(v)
    return str(int(v)) if v.is_integer() else str(v)


def write(out, defs_id, process_id, process_name, doc, messages, process_attrs=""):
    """Schreibt das Modell. messages: [(id, name, correlationKey-Ausdruck oder None)]."""
    incoming, outgoing = {}, {}
    for f in flows:
        outgoing.setdefault(f["src"], []).append(f["id"])
        incoming.setdefault(f["tgt"], []).append(f["id"])

    def emit_flow(f):
        name = f' name={quoteattr(f["name"])}' if f["name"] else ""
        if f["cond"]:
            return (f'<sequenceFlow id="{f["id"]}"{name} sourceRef="{f["src"]}" targetRef="{f["tgt"]}">'
                    f'<conditionExpression>{escape(f["cond"])}</conditionExpression></sequenceFlow>\n')
        return f'<sequenceFlow id="{f["id"]}"{name} sourceRef="{f["src"]}" targetRef="{f["tgt"]}"/>\n'

    def emit_node(n):
        name = f' name={quoteattr(n["name"])}' if n["name"] else ""
        body = f'<documentation>{escape(n["doc"])}</documentation>' if n["doc"] else ""
        inner = n["inner"]
        if inner.startswith("<extensionElements>"):
            body += inner
            inner = ""
        body += "".join(f"<incoming>{i}</incoming>" for i in incoming.get(n["id"], []))
        body += "".join(f"<outgoing>{i}</outgoing>" for i in outgoing.get(n["id"], []))
        body += n["tail"]
        if n["tag"] == "subProcess":
            for c in nodes:
                if c["parent"] == n["id"]:
                    body += emit_node(c)
            for f in flows:
                if f["parent"] == n["id"]:
                    body += emit_flow(f)
        body += inner
        return f'<{n["tag"]} id="{n["id"]}"{name}{n["attrs"]}>{body}</{n["tag"]}>\n'

    o = ['<?xml version="1.0" encoding="UTF-8"?>',
         '<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" '
         'xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" '
         'xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas/schema/1.0" '
         f'id="{defs_id}" targetNamespace="http://atlas/bpmn">']
    for mid, mname, key in messages:
        if key:
            o.append(f'<message id="{mid}" name="{mname}"><extensionElements><zeebe:subscription correlationKey={quoteattr(key)}/></extensionElements></message>')
        else:
            o.append(f'<message id="{mid}" name="{mname}"/>')
    o.append(f'<process id="{process_id}" name={quoteattr(process_name)} isExecutable="true"{process_attrs}>')
    o.append(f'<documentation>{escape(doc)}</documentation>')
    o.append('<extensionElements><atlas:constants><atlas:constant name="ATLAS_API_BASE" type="string" value="http://127.0.0.1:8080" source="application"/></atlas:constants></extensionElements>')
    for n in nodes:
        if n["parent"] is None:
            o.append(emit_node(n).rstrip("\n"))
    for f in flows:
        if f["parent"] is None:
            o.append(emit_flow(f).rstrip("\n"))
    o.append('</process>')
    o.append(f'<bpmndi:BPMNDiagram id="dia_{process_id}"><bpmndi:BPMNPlane id="plane_{process_id}" bpmnElement="{process_id}">')
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
            lab = f'<bpmndi:BPMNLabel><dc:Bounds x="{_fmt(lx)}" y="{_fmt(ly)}" width="{lw}" height="{lh}"/></bpmndi:BPMNLabel>'
        o.append(f'<bpmndi:BPMNShape id="d{k}" bpmnElement="{n["id"]}"{extra}><dc:Bounds x="{_fmt(x)}" y="{_fmt(y)}" width="{_fmt(w)}" height="{_fmt(h)}"/>{lab}</bpmndi:BPMNShape>')
    for f in flows:
        k += 1
        wps = "".join(f'<di:waypoint x="{_fmt(px)}" y="{_fmt(py)}"/>' for px, py in f["pts"])
        lab = ""
        if f["label"]:
            lx, ly, lw, lh = f["label"]
            lab = f'<bpmndi:BPMNLabel><dc:Bounds x="{_fmt(lx)}" y="{_fmt(ly)}" width="{lw}" height="{lh}"/></bpmndi:BPMNLabel>'
        o.append(f'<bpmndi:BPMNEdge id="d{k}" bpmnElement="{f["id"]}">{wps}{lab}</bpmndi:BPMNEdge>')
    o.append('</bpmndi:BPMNPlane></bpmndi:BPMNDiagram>')
    o.append('</definitions>')
    open(out, "w").write("\n".join(o) + "\n")
    print("ok", out, len(nodes), "nodes", len(flows), "flows")
