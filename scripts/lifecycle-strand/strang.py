#!/usr/bin/env python3
"""Merge a provision and a deprovision process into one per-position strand (ADR-0428).

provision:   none start -> message start <product>.provision
             success end(s) (reached after the "done" report) -> Ausgegeben -> Warten auf Rückgabe
deprovision: catch <product>.deprovision (correlated on orderId/positionId) -> join -> old deprovision body
             old none start -> message start (same message) for rights without a running instance
Everything else (tasks, forms, gateways, rejection ends) is copied unchanged, ids prefixed p_ / d_.
"""
import argparse
import copy
import sys
import xml.etree.ElementTree as ET

B = "http://www.omg.org/spec/BPMN/20100524/MODEL"
DI = "http://www.omg.org/spec/BPMN/20100524/DI"
DC = "http://www.omg.org/spec/DD/20100524/DC"
DDI = "http://www.omg.org/spec/DD/20100524/DI"
Z = "http://camunda.org/schema/zeebe/1.0"
A = "http://atlas/schema/1.0"
for p, u in {"": B, "bpmndi": DI, "omgdc": DC, "omgdi": DDI, "zeebe": Z, "atlas": A}.items():
    ET.register_namespace(p, u)
q = lambda t: f"{{{B}}}{t}"
EVDEFS = {"messageEventDefinition", "timerEventDefinition", "signalEventDefinition", "errorEventDefinition",
          "conditionalEventDefinition", "escalationEventDefinition", "terminateEventDefinition",
          "compensateEventDefinition", "linkEventDefinition"}
CK = '=orderId + "/" + (if positionId = null then itemId else positionId)'


def load(path):
    root = ET.parse(path).getroot()
    procs = root.findall(q("process"))
    assert len(procs) == 1, f"{path}: {len(procs)} processes"
    return root, procs[0]


def local(tag):
    return tag.split("}")[1]


def is_none_event(el):
    return not any(local(c.tag) in EVDEFS for c in el)


def rename(root, proc, pre):
    """Prefix every id in the process (and root-level message/error/signal defs) with pre."""
    ids = {}
    for el in proc.iter():
        if "id" in el.attrib and el is not proc:
            ids[el.get("id")] = pre + el.get("id")
    for el in root:
        if local(el.tag) in ("message", "error", "signal", "escalation"):
            ids[el.get("id")] = pre + el.get("id")
    def fix(el):
        for a in ("id", "sourceRef", "targetRef", "default", "attachedToRef", "messageRef", "errorRef",
                  "signalRef", "escalationRef", "activityRef"):
            if a in el.attrib and el.get(a) in ids:
                el.set(a, ids[el.get(a)])
        if local(el.tag) in ("incoming", "outgoing") and el.text in ids:
            el.text = ids[el.text]
    for el in proc.iter():
        fix(el)
    defs = [copy.deepcopy(el) for el in root if local(el.tag) in ("message", "error", "signal", "escalation")]
    for d in defs:
        for el in d.iter():
            fix(el)
    # DI
    shapes, edges = {}, {}
    for s in root.iter(f"{{{DI}}}BPMNShape"):
        b = s.find(f"{{{DC}}}Bounds")
        shapes[ids.get(s.get("bpmnElement"), s.get("bpmnElement"))] = [float(b.get(k)) for k in ("x", "y", "width", "height")]
    for e in root.iter(f"{{{DI}}}BPMNEdge"):
        edges[ids.get(e.get("bpmnElement"), e.get("bpmnElement"))] = [
            (float(w.get("x")), float(w.get("y"))) for w in e.findall(f"{{{DDI}}}waypoint")]
    return defs, shapes, edges


def nodes_flows(proc):
    flows = {f.get("id"): f for f in proc.findall(q("sequenceFlow"))}
    nodes = {el.get("id"): el for el in proc if local(el.tag) not in ("sequenceFlow", "documentation",
                                                                      "extensionElements", "laneSet")}
    return nodes, flows


def reports_done(el):
    for inp in el.iter(f"{{{Z}}}input"):
        if inp.get("target") == "status" and inp.get("source", "").replace(" ", "") in ('="done"',):
            return True
    return False


def success_ends(proc):
    nodes, flows = nodes_flows(proc)
    into = {}
    for f in flows.values():
        into.setdefault(f.get("targetRef"), []).append(f.get("sourceRef"))
    out = []
    for nid, el in nodes.items():
        if local(el.tag) != "endEvent" or not is_none_event(el):
            continue
        seen, todo = set(), [nid]
        while todo:
            n = todo.pop()
            if n in seen:
                continue
            seen.add(n)
            if reports_done(nodes.get(n, ET.Element("x"))):
                out.append(nid)
                break
            todo += into.get(n, [])
    return out


def sub(parent, tag, **attrs):
    el = ET.SubElement(parent, q(tag))
    for k, v in attrs.items():
        el.set(k, v)
    return el


def build(prov_path, deprov_path, product, pid, name):
    proot, pproc = load(prov_path)
    droot, dproc = load(deprov_path)
    pdefs, pshapes, pedges = rename(proot, pproc, "p_")
    ddefs, dshapes, dedges = rename(droot, dproc, "d_")

    root = ET.Element(q("definitions"), {"id": "defs_" + pid, "targetNamespace": "http://atlas/katalog-demo"})
    mp = sub(root, "message", id="msg_provision", name=f"{product}.provision")
    md = sub(root, "message", id="msg_deprovision", name=f"{product}.deprovision")
    ext = ET.SubElement(md, q("extensionElements"))
    ET.SubElement(ext, f"{{{Z}}}subscription", {"correlationKey": CK})
    for d in pdefs + ddefs:
        root.append(d)
    proc = sub(root, "process", id=pid, name=name, isExecutable="true")
    doc = sub(proc, "documentation")
    doc.text = (f"Lebenszyklus als ein Strang pro Position für das Produkt {product} (ADR-0428, Form per-position).\n\n"
                f"Ablauf: {product}.provision -> Bereitstellung (aus {pproc.get('id')}) -> Ausgegeben -> "
                f"Warten auf Rückgabe -> Deprovisionierung (aus {dproc.get('id')}) -> Ende.\n"
                f"Die Rückgabe wird der wartenden Instanz dieser Position zugestellt (Korrelation orderId/positionId). "
                f"Rechte ohne laufende Instanz (ältere Bestellungen, abgebrochene Instanzen) treten über das "
                f"Startereignis 'Rückgabe ohne laufende Instanz' mit derselben Nachricht direkt in die Deprovisionierung ein.\n"
                f"Keine Schleife: jeder Pfad endet oder wartet auf ein Ereignis von aussen.\n"
                f"Schritte, Formulare, Gruppen, Genehmigungen und Meldungen sind unverändert übernommen.")

    ends = success_ends(pproc)
    if not ends:
        raise SystemExit(f"{prov_path}: no end event after a 'done' report")
    pstarts = [el for el in pproc if local(el.tag) == "startEvent" and is_none_event(el)]
    dstarts = [el for el in dproc if local(el.tag) == "startEvent" and is_none_event(el)]
    assert len(pstarts) == 1 and len(dstarts) == 1, (len(pstarts), len(dstarts))

    # copy provision body
    for el in pproc:
        t = local(el.tag)
        if t in ("documentation",):
            continue
        if el.get("id") in ends:
            continue
        c = copy.deepcopy(el)
        if el is pstarts[0]:
            c.set("name", c.get("name") or "Bestellt")
            ET.SubElement(c, q("messageEventDefinition"), {"messageRef": "msg_provision"})
        proc.append(c)
    # redirect flows into success ends -> ausgegeben
    end_in = []
    for f in proc.findall(q("sequenceFlow")):
        if f.get("targetRef") in ends:
            f.set("targetRef", "ausgegeben")
            end_in.append(f.get("id"))
    aus = sub(proc, "intermediateThrowEvent", id="ausgegeben", name="Ausgegeben")
    for fid in end_in:
        sub(aus, "incoming").text = fid
    sub(aus, "outgoing").text = "s_f1"
    wait = sub(proc, "intermediateCatchEvent", id="warten_rueckgabe", name="Rückgabe angefordert")
    sub(wait, "incoming").text = "s_f1"
    sub(wait, "outgoing").text = "s_f2"
    sub(wait, "messageEventDefinition", messageRef="msg_deprovision")

    # deprovision body
    dstart = dstarts[0]
    first = [f for f in dproc.findall(q("sequenceFlow")) if f.get("sourceRef") == dstart.get("id")]
    assert len(first) == 1
    for el in dproc:
        if local(el.tag) == "documentation":
            continue
        c = copy.deepcopy(el)
        if el is dstart:
            c = ET.Element(q("startEvent"), {"id": dstart.get("id"), "name": "Rückgabe ohne laufende Instanz"})
            sub(c, "outgoing").text = "s_f3"
            sub(c, "messageEventDefinition", messageRef="msg_deprovision")
        if el is first[0]:
            c.set("sourceRef", "gw_rueckgabe")
        proc.append(c)
    join = sub(proc, "exclusiveGateway", id="gw_rueckgabe")
    sub(join, "incoming").text = "s_f2"
    sub(join, "incoming").text = "s_f3"
    sub(join, "outgoing").text = first[0].get("id")
    sub(proc, "sequenceFlow", id="s_f1", sourceRef="ausgegeben", targetRef="warten_rueckgabe")
    sub(proc, "sequenceFlow", id="s_f2", sourceRef="warten_rueckgabe", targetRef="gw_rueckgabe")
    sub(proc, "sequenceFlow", id="s_f3", sourceRef=dstart.get("id"), targetRef="gw_rueckgabe")

    # ---- layout: keep both original drawings, deprovision block to the right of the handover
    shapes, edges = {}, {}
    if not pshapes or not dshapes:
        return root, None
    for k, v in pshapes.items():
        if k not in ends:
            shapes[k] = v
    for k, v in pedges.items():
        edges[k] = v
    e0 = pshapes[ends[0]]
    ecx, ecy = e0[0] + 18, e0[1] + 18
    shapes["ausgegeben"] = [ecx - 18, ecy - 18, 36, 36]
    for fid in end_in:
        pts = edges.get(fid)
        if pts and len(ends) > 1:
            pts[-1] = (ecx - 18, pts[-1][1]) if abs(pts[-1][1] - ecy) < 1 else (ecx, ecy - 18 if pts[-1][1] < ecy else ecy + 18)
    shapes["warten_rueckgabe"] = [ecx + 100 - 18, ecy - 18, 36, 36]
    edges["s_f1"] = [(ecx + 18, ecy), (ecx + 82, ecy)]
    ds = dshapes[dstart.get("id")]
    scx, scy = ds[0] + 18, ds[1] + 18
    jx = ecx + 210
    dx, dy = jx - scx, ecy - scy
    for k, v in dshapes.items():
        if k == dstart.get("id"):
            continue
        shapes[k] = [v[0] + dx, v[1] + dy, v[2], v[3]]
    for k, v in dedges.items():
        edges[k] = [(x + dx, y + dy) for x, y in v]
    shapes["gw_rueckgabe"] = [jx - 25, ecy - 25, 50, 50]
    fe = edges.get(first[0].get("id"))
    if fe:
        fe[0] = (jx + 25, fe[0][1])
    edges["s_f2"] = [(ecx + 118, ecy), (jx - 25, ecy)]
    fx, fy = jx - 90, ecy + 80
    shapes[dstart.get("id")] = [fx - 18, fy - 18, 36, 36]
    edges["s_f3"] = [(fx + 18, fy), (jx, fy), (jx, ecy + 25)]
    for k, (x, y, w, h) in shapes.items():
        if k != dstart.get("id") and x < jx + 5 and x + w > fx - 18 and y < fy + 18 and y + h > ecy + 25:
            print(f"warning {pid}: {k} overlaps the fallback start area", file=sys.stderr)
    # overlap guard: nothing of the provision drawing may reach past the handover
    pmax = max(v[0] + v[2] for k, v in pshapes.items())
    if pmax > ecx + 40:
        print(f"warning {pid}: provision drawing extends right of its end ({pmax} > {ecx})", file=sys.stderr)

    diag = ET.SubElement(root, f"{{{DI}}}BPMNDiagram", {"id": "di_" + pid})
    plane = ET.SubElement(diag, f"{{{DI}}}BPMNPlane", {"id": "plane_" + pid, "bpmnElement": pid})
    gws = {el.get("id") for el in proc if local(el.tag).endswith("Gateway")}
    for k, (x, y, w, h) in shapes.items():
        s = ET.SubElement(plane, f"{{{DI}}}BPMNShape", {"id": k + "_di", "bpmnElement": k})
        if k in gws:
            s.set("isMarkerVisible", "true")
        ET.SubElement(s, f"{{{DC}}}Bounds", {"x": fmt(x), "y": fmt(y), "width": fmt(w), "height": fmt(h)})
    for k, pts in edges.items():
        e = ET.SubElement(plane, f"{{{DI}}}BPMNEdge", {"id": k + "_di", "bpmnElement": k})
        for x, y in pts:
            ET.SubElement(e, f"{{{DDI}}}waypoint", {"x": fmt(x), "y": fmt(y)})
    return root, True


def fmt(v):
    return str(int(v)) if float(v).is_integer() else str(v)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("provision", help="BPMN file of the product's provisioning process")
    ap.add_argument("deprovision", help="BPMN file of the product's deprovisioning process")
    ap.add_argument("--product", required=True, help="catalogue product id; names the two messages")
    ap.add_argument("--process-id", required=True, help="id of the merged process")
    ap.add_argument("--name", help="name of the merged process (default: the process id)")
    ap.add_argument("-o", "--out", required=True, help="where to write the merged BPMN")
    a = ap.parse_args(argv)
    root, _ = build(a.provision, a.deprovision, a.product, a.process_id, a.name or a.process_id)
    ET.indent(root, "  ")
    with open(a.out, "wb") as f:
        f.write(b'<?xml version="1.0" encoding="UTF-8"?>\n')
        f.write(ET.tostring(root, encoding="utf-8"))


if __name__ == "__main__":
    main()
