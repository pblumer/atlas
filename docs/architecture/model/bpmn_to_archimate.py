#!/usr/bin/env python3
"""Convert exactly one BPMN process into an ArchiMate Open Exchange model.

Atlas runs BPMN; Archi (and other ArchiMate tools) speak ArchiMate, not BPMN. This
script bridges the two: it reads a ``.bpmn`` file, picks **one** process out of it,
and writes that process as an ArchiMate 3.0 *Model Exchange File Format* XML — the
tool-neutral format you import into Archi (``File -> Import -> Other -> Open Exchange
File``), which then saves it as a native ``.archimate`` file.

Usage:
    python3 bpmn_to_archimate.py INPUT.bpmn [--process ID_OR_NAME] [-o OUTPUT.xml]

A single ``.bpmn`` definitions file may hold several <process> elements. If there is
exactly one, it is used. If there are several, pass --process with the process id (or
its name) to choose the one you want; without it the script lists them and stops.

Mapping (BPMN -> ArchiMate business layer)
------------------------------------------
* start / end / intermediate / boundary events  -> BusinessEvent
* every activity (task, userTask, serviceTask, scriptTask, callActivity,
  subProcess, ...)                                -> BusinessProcess
* gateways (exclusive, parallel, inclusive, ...)  -> BusinessProcess (a routing step),
                                                     with the original kind kept in a
                                                     "BPMN element" property
* every sequenceFlow                              -> Triggering relationship
  (the flow's name or condition becomes the relationship name)

The original BPMN tag of each node is preserved in a "BPMN element" property, and the
BPMN diagram geometry (shape bounds and edge waypoints) is reused so the ArchiMate
view keeps the process's original layout. Gateways map to a plain routing step rather
than an ArchiMate junction so the output is always schema-valid and importable; refine
them to And/Or junctions by hand in Archi if you want the stricter semantics.
"""
import argparse
import os
import re
import sys
import xml.etree.ElementTree as ET
from xml.sax.saxutils import escape, quoteattr

NS = "http://www.opengroup.org/xsd/archimate/3.0/"
XSI = "http://www.w3.org/2001/XMLSchema-instance"
SCHEMA_LOC = (
    "http://www.opengroup.org/xsd/archimate/3.0/ "
    "http://www.opengroup.org/xsd/archimate/3.0/archimate3_Model.xsd"
)

B = "{http://www.omg.org/spec/BPMN/20100524/MODEL}"
BPMNDI = "{http://www.omg.org/spec/BPMN/20100524/DI}"
DC = "{http://www.omg.org/spec/DD/20100524/DC}"
DI = "{http://www.omg.org/spec/DD/20100524/DI}"
ZEEBE = "{http://camunda.org/schema/zeebe/1.0}"

BIZ_FILL = (251, 239, 168)   # ArchiMate business-layer yellow

EVENT_TAGS = {
    "startEvent", "endEvent", "intermediateCatchEvent",
    "intermediateThrowEvent", "boundaryEvent",
}
GATEWAY_TAGS = {
    "exclusiveGateway", "parallelGateway", "inclusiveGateway",
    "eventBasedGateway", "complexGateway",
}
ACTIVITY_TAGS = {
    "task", "userTask", "serviceTask", "scriptTask", "manualTask",
    "businessRuleTask", "sendTask", "receiveTask", "callActivity", "subProcess",
}
FLOWNODE_TAGS = EVENT_TAGS | GATEWAY_TAGS | ACTIVITY_TAGS


def local(tag):
    return tag.rsplit("}", 1)[-1]


def sane(s):
    """Make an NCName-safe id fragment from a BPMN id."""
    s = re.sub(r"[^A-Za-z0-9_.-]", "-", s)
    return s if s and (s[0].isalpha() or s[0] == "_") else "n-" + s


def txt(tag, s, indent):
    return f'{indent}<{tag} xml:lang="en">{escape(s)}</{tag}>\n'


def pick_process(defs, wanted):
    procs = defs.findall(f"{B}process")
    if not procs:
        sys.exit("error: no <process> found in the BPMN file")
    if wanted:
        for p in procs:
            if p.get("id") == wanted or (p.get("name") or "") == wanted:
                return p
        ids = ", ".join(repr(p.get("id")) for p in procs)
        sys.exit(f"error: no process matches {wanted!r}; available: {ids}")
    if len(procs) == 1:
        return procs[0]
    listing = "\n".join(
        f"  --process {p.get('id')!r}   ({p.get('name') or 'unnamed'})" for p in procs
    )
    sys.exit(
        "error: the file has several processes; choose one with --process:\n" + listing
    )


def node_kind(tag):
    if tag in EVENT_TAGS:
        return "BusinessEvent"
    return "BusinessProcess"          # activities and gateways


def enrich(node, tag):
    """A short documentation string for special node kinds."""
    if tag == "serviceTask":
        td = node.find(f".//{ZEEBE}taskDefinition")
        if td is not None and td.get("type"):
            return f"Service task — job worker type '{td.get('type')}'."
    if tag == "userTask":
        ad = node.find(f".//{ZEEBE}assignmentDefinition")
        if ad is not None and ad.get("candidateGroups"):
            return f"User task — candidate groups '{ad.get('candidateGroups')}'."
        return "User task."
    if tag == "scriptTask":
        return "Script task (inline expression)."
    if tag in GATEWAY_TAGS:
        return {
            "exclusiveGateway": "Exclusive gateway (XOR routing).",
            "parallelGateway": "Parallel gateway (AND fork/join).",
            "inclusiveGateway": "Inclusive gateway (OR routing).",
            "eventBasedGateway": "Event-based gateway.",
            "complexGateway": "Complex gateway.",
        }.get(tag, "Gateway.")
    return ""


def bounds_of(plane):
    """node id -> (x, y, w, h) from BPMNShape/dc:Bounds."""
    out = {}
    for shape in plane.findall(f"{BPMNDI}BPMNShape"):
        ref = shape.get("bpmnElement")
        b = shape.find(f"{DC}Bounds")
        if ref and b is not None:
            out[ref] = (
                float(b.get("x", 0)), float(b.get("y", 0)),
                float(b.get("width", 100)), float(b.get("height", 60)),
            )
    return out


def waypoints_of(plane):
    """flow id -> [(x, y), ...] from BPMNEdge/di:waypoint."""
    out = {}
    for edge in plane.findall(f"{BPMNDI}BPMNEdge"):
        ref = edge.get("bpmnElement")
        pts = [
            (float(w.get("x", 0)), float(w.get("y", 0)))
            for w in edge.findall(f"{DI}waypoint")
        ]
        if ref:
            out[ref] = pts
    return out


def convert(bpmn_path, wanted_process, out_path):
    tree = ET.parse(bpmn_path)
    defs = tree.getroot()
    proc = pick_process(defs, wanted_process)
    proc_id = proc.get("id") or "process"
    proc_name = proc.get("name") or proc_id

    # --- collect flow nodes and sequence flows (in document order) ---
    nodes, flows = [], []
    for child in proc:
        tag = local(child.tag)
        if tag in FLOWNODE_TAGS:
            nodes.append((child.get("id"), tag, child.get("name") or child.get("id"), child))
        elif tag == "sequenceFlow":
            cond = child.find(f"{B}conditionExpression")
            label = child.get("name") or (cond.text.strip() if cond is not None and cond.text else "")
            flows.append((child.get("id"), child.get("sourceRef"),
                          child.get("targetRef"), label))

    node_ids = {n[0] for n in nodes}
    flows = [f for f in flows if f[1] in node_ids and f[2] in node_ids]

    # --- diagram interchange geometry for this process ---
    bounds, waypoints = {}, {}
    for plane in defs.iter(f"{BPMNDI}BPMNPlane"):
        if plane.get("bpmnElement") == proc_id:
            bounds = bounds_of(plane)
            waypoints = waypoints_of(plane)
            break

    def eid(bpmn_id):
        return "el-" + sane(bpmn_id)

    def rid(bpmn_id):
        return "rel-" + sane(bpmn_id)

    # --- emit ---
    o = []
    o.append('<?xml version="1.0" encoding="UTF-8"?>\n')
    o.append(f'<model xmlns="{NS}" xmlns:xsi="{XSI}" '
             f'xsi:schemaLocation={quoteattr(SCHEMA_LOC)} '
             f'identifier="id-{sane(proc_id)}">\n')
    o.append(txt("name", proc_name, "  "))
    o.append(txt("documentation",
                 f"BPMN process '{proc_name}' (id: {proc_id}), converted from "
                 f"{os.path.basename(bpmn_path)} into ArchiMate by "
                 f"bpmn_to_archimate.py.", "  "))

    # elements
    o.append("  <elements>\n")
    for bid, tag, name, _ in nodes:
        etype = node_kind(tag)
        doc = enrich(nodes_by_id(nodes, bid), tag)
        o.append(f'    <element identifier="{eid(bid)}" xsi:type="{etype}">\n')
        o.append(txt("name", name, "      "))
        if doc:
            o.append(txt("documentation", doc, "      "))
        o.append('      <properties>\n')
        o.append('        <property propertyDefinitionRef="propid-bpmn">\n')
        o.append(txt("value", tag, "          "))
        o.append('        </property>\n')
        o.append('      </properties>\n')
        o.append("    </element>\n")
    o.append("  </elements>\n")

    # relationships
    o.append("  <relationships>\n")
    for fid, src, tgt, label in flows:
        attrs = (f'identifier="{rid(fid)}" source="{eid(src)}" '
                 f'target="{eid(tgt)}" xsi:type="Triggering"')
        if label:
            o.append(f"    <relationship {attrs}>\n")
            o.append(txt("name", label, "      "))
            o.append("    </relationship>\n")
        else:
            o.append(f"    <relationship {attrs}/>\n")
    o.append("  </relationships>\n")

    # organizations: one folder named after the process
    o.append("  <organizations>\n")
    o.append("    <item>\n")
    o.append(txt("label", proc_name, "      "))
    for bid, *_ in nodes:
        o.append(f'      <item identifierRef="{eid(bid)}"/>\n')
    o.append("    </item>\n")
    o.append("    <item>\n")
    o.append(txt("label", "Views", "      "))
    o.append(f'      <item identifierRef="view-{sane(proc_id)}"/>\n')
    o.append("    </item>\n")
    o.append("  </organizations>\n")

    # property definitions
    o.append("  <propertyDefinitions>\n")
    o.append('    <propertyDefinition identifier="propid-bpmn" type="string">\n')
    o.append("      <name>BPMN element</name>\n")
    o.append("    </propertyDefinition>\n")
    o.append("  </propertyDefinitions>\n")

    # view — reuse BPMN geometry (translate so the min corner sits at the margin)
    minx = min((b[0] for b in bounds.values()), default=0)
    miny = min((b[1] for b in bounds.values()), default=0)
    ox, oy = 20 - minx, 20 - miny

    o.append("  <views>\n")
    o.append("    <diagrams>\n")
    o.append(f'      <view identifier="view-{sane(proc_id)}" xsi:type="Diagram">\n')
    o.append(txt("name", proc_name, "        "))
    node_of = {}
    for bid, tag, name, _ in nodes:
        nid = f"node-{sane(bid)}"
        node_of[bid] = nid
        x, y, w, h = bounds.get(bid, (0, 0, 120, 55))
        r, g, b = BIZ_FILL
        o.append(
            f'        <node identifier="{nid}" elementRef="{eid(bid)}" '
            f'xsi:type="Element" x="{int(x + ox)}" y="{int(y + oy)}" '
            f'w="{int(w)}" h="{int(h)}">\n'
            f'          <style>\n'
            f'            <fillColor r="{r}" g="{g}" b="{b}" a="100"/>\n'
            f'          </style>\n'
            f'        </node>\n'
        )
    for fid, src, tgt, label in flows:
        cid = f"conn-{sane(fid)}"
        pts = waypoints.get(fid, [])
        inner = pts[1:-1] if len(pts) > 2 else []
        if inner:
            o.append(f'        <connection identifier="{cid}" '
                     f'relationshipRef="{rid(fid)}" xsi:type="Relationship" '
                     f'source="{node_of[src]}" target="{node_of[tgt]}">\n')
            for px, py in inner:
                o.append(f'          <bendpoint x="{int(px + ox)}" y="{int(py + oy)}"/>\n')
            o.append("        </connection>\n")
        else:
            o.append(f'        <connection identifier="{cid}" '
                     f'relationshipRef="{rid(fid)}" xsi:type="Relationship" '
                     f'source="{node_of[src]}" target="{node_of[tgt]}"/>\n')
    o.append("      </view>\n")
    o.append("    </diagrams>\n")
    o.append("  </views>\n")
    o.append("</model>\n")

    xml = "".join(o)
    with open(out_path, "w", encoding="utf-8") as f:
        f.write(xml)
    return proc_id, proc_name, len(nodes), len(flows)


def nodes_by_id(nodes, bid):
    for n in nodes:
        if n[0] == bid:
            return n[3]
    return None


def main():
    ap = argparse.ArgumentParser(description="Convert one BPMN process to ArchiMate Open Exchange XML.")
    ap.add_argument("input", help="path to the .bpmn file")
    ap.add_argument("--process", help="process id or name (needed only if the file has several)")
    ap.add_argument("-o", "--output", help="output path (default: <input>.archimate.xml)")
    args = ap.parse_args()

    out = args.output or (os.path.splitext(args.input)[0] + ".archimate.xml")
    pid, pname, n, f = convert(args.input, args.process, out)
    print(f"wrote {out}")
    print(f"process: {pname} (id: {pid}) — {n} elements, {f} triggering relationships")


if __name__ == "__main__":
    main()
