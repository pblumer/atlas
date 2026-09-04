#!/usr/bin/env python3
"""Konvertiert einen ArchiMate-3.x-Model-Exchange-Export in UML XMI 2.5.1.

Hintergrund: Atlas liest als Informationsmodell entweder sein eigenes JSON oder
UML XMI 2.5.1 (api/infomodel/importxmi.go). ArchiMate ist eine andere Notation --
der Reader sucht nach <packagedElement>/<ownedMember> und findet in einer
ArchiMate-Datei nichts, weshalb der Import mit "this document contains no classes
Atlas could read" abbricht.

Abgebildet wird:
  element[@xsi:type='BusinessObject']              -> uml:Class
  property[@key='attr_*']                          -> uml:Property (ownedAttribute)
  relationship[@xsi:type='Specialization...']      -> uml:Generalization
  documentation                                    -> ownedComment/body

Nicht abgebildet (mit Meldung auf stderr):
  - Platzhalter-Properties aus einem unvollstaendigen Export
  - ArchiMate-Beziehungstypen ausser Specialization
"""

import re
import sys
import xml.etree.ElementTree as ET
from xml.sax.saxutils import escape, quoteattr

ARCHIMATE_NS = "{http://www.opengroup.org/xsd/archimate/3.0/}"
XSI = "{http://www.w3.org/2001/XMLSchema-instance}"

# Die Attributzeile des Exports:
#   name | Visibility: X | Aggregation: Y | Multiplicity: [0..*] | ... | Desc: text
MULT_RE = re.compile(r"Multiplicity:\s*\[([^\]]*)\]")
DESC_RE = re.compile(r"\|\s*Desc:\s*(.*)$", re.S)

# Nur die vier Multiplizitaeten, die Atlas kennt.
ALLOWED_MULT = {"0..1", "1", "0..*", "1..*"}

# Properties, die Metadaten des Modellierungswerkzeugs sind und keine
# Klassenattribute -- sie wandern in die Dokumentation der Klasse.
META_KEYS = ("stereotype", "visibility", "isAbstract", "isLeaf", "isActive", "status",
             "accessRights", "owner", "diagramOccurrences", "createdBy", "createdAt",
             "modifiedBy", "modifiedAt")


def warn(msg):
    print("HINWEIS: " + msg, file=sys.stderr)


def is_placeholder(key, value):
    """Erkennt die Platzhalter eines abgeschnittenen Exports."""
    return key.strip().endswith("...") or value.strip().startswith("...")


def parse_multiplicity(value):
    match = MULT_RE.search(value)
    if not match:
        return "0..1"
    raw = match.group(1).strip()
    if raw in ALLOWED_MULT:
        return raw
    if raw in ("*", "0..n", "0..N"):
        return "0..*"
    if raw in ("1..n", "1..N"):
        return "1..*"
    if raw == "1..1":
        return "1"
    return "0..1"


def parse_attribute(value):
    """Zerlegt eine Attributzeile in (Name, Multiplizitaet, Dokumentation)."""
    name = value.split("|", 1)[0].strip()
    desc_match = DESC_RE.search(value)
    desc = desc_match.group(1).strip() if desc_match else ""
    return name, parse_multiplicity(value), desc


def class_documentation(element, doc_text, placeholders=()):
    """Fasst Elementdokumentation, Luecken-Hinweise und Werkzeug-Metadaten zusammen."""
    parts = [doc_text] if doc_text else []
    for placeholder in placeholders:
        parts.append("ACHTUNG: Der Quellexport enthielt hier nur einen Platzhalter "
                     "(%s), keine Attributdaten. Attribute sind nachzupflegen." % placeholder)
    meta = []
    for prop in element.iterfind("./%sproperties/%sproperty" % (ARCHIMATE_NS, ARCHIMATE_NS)):
        key = prop.get("key", "")
        if key in META_KEYS and prop.get("value", "").strip():
            meta.append("%s: %s" % (key, prop.get("value").strip()))
    if meta:
        parts.append("Herkunft (Enterprise Architect): " + "; ".join(meta))
    return "\n\n".join(parts)


def xmi_id(identifier):
    """XMI-ids muessen NCName sein: fuehrende Ziffer und Sonderzeichen entschaerfen."""
    cleaned = re.sub(r"[^A-Za-z0-9_.-]", "_", identifier)
    if not cleaned or not (cleaned[0].isalpha() or cleaned[0] == "_"):
        cleaned = "_" + cleaned
    return cleaned


def convert(source_path, model_name=None):
    tree = ET.parse(source_path)
    root = tree.getroot()

    name = model_name or root.get("name") or "Importiertes Modell"
    doc_node = root.find("./%smetadata/%sdescription" % (ARCHIMATE_NS, ARCHIMATE_NS))
    model_doc = (doc_node.text or "").strip() if doc_node is not None else ""

    classes = []
    known_ids = set()
    for element in root.iterfind("./%selements/%selement" % (ARCHIMATE_NS, ARCHIMATE_NS)):
        kind = element.get(XSI + "type") or ""
        if kind not in ("BusinessObject", "DataObject"):
            warn("Element %r ist ein ArchiMate-%s und wurde nicht uebernommen."
                 % (element.get("name"), kind or "Element ohne Typ"))
            continue
        identifier = xmi_id(element.get("identifier", ""))
        doc_el = element.find("%sdocumentation" % ARCHIMATE_NS)
        doc_text = (doc_el.text or "").strip() if doc_el is not None else ""

        attributes = []
        placeholders = []
        seen = set()
        for prop in element.iterfind("./%sproperties/%sproperty" % (ARCHIMATE_NS, ARCHIMATE_NS)):
            key, value = prop.get("key", ""), prop.get("value", "")
            if not key.startswith("attr_"):
                continue
            if is_placeholder(key, value):
                placeholders.append(value.strip())
                warn("Klasse %r: Platzhalter %r -- der Export enthaelt diese Attribute nicht."
                     % (element.get("name"), value.strip()[:60]))
                continue
            attr_name, mult, desc = parse_attribute(value)
            if not attr_name:
                warn("Klasse %r: Attribut ohne Namen uebersprungen." % element.get("name"))
                continue
            if attr_name in seen:
                warn("Klasse %r: Attribut %r doppelt, zweites Vorkommen verworfen."
                     % (element.get("name"), attr_name))
                continue
            seen.add(attr_name)
            attributes.append({"id": xmi_id(key), "name": attr_name,
                               "multiplicity": mult, "doc": desc})

        classes.append({"id": identifier, "name": element.get("name", ""),
                        "doc": class_documentation(element, doc_text, placeholders),
                        "attributes": attributes})
        known_ids.add(identifier)

    generalizations = {}
    for rel in root.iterfind("./%srelationships/%srelationship" % (ARCHIMATE_NS, ARCHIMATE_NS)):
        kind = (rel.get(XSI + "type") or "").replace("Relationship", "")
        source = xmi_id(rel.get("source", ""))
        target = xmi_id(rel.get("target", ""))
        if kind != "Specialization":
            warn("Beziehung %r ist eine ArchiMate-%s-Beziehung und wurde nicht uebernommen."
                 % (rel.get("name"), kind))
            continue
        if source not in known_ids or target not in known_ids:
            warn("Beziehung %r verweist auf eine Klasse ausserhalb des Dokuments."
                 % rel.get("name"))
            continue
        generalizations.setdefault(source, []).append(
            {"id": xmi_id(rel.get("identifier", "")), "general": target})

    return name, model_doc, classes, generalizations


def emit(name, model_doc, classes, generalizations):
    out = ['<?xml version="1.0" encoding="UTF-8"?>']
    out.append('<uml:Model xmi:version="20131001" '
               'xmlns:xmi="http://www.omg.org/spec/XMI/20131001" '
               'xmlns:uml="http://www.omg.org/spec/UML/20161101" '
               'xmi:id="model" name=%s>' % quoteattr(name))
    if model_doc:
        out.append('  <ownedComment xmi:type="uml:Comment" xmi:id="model-doc">')
        out.append("    <body>%s</body>" % escape(model_doc))
        out.append("  </ownedComment>")
    out.append('  <packagedElement xmi:type="uml:PrimitiveType" xmi:id="String" name="String"/>')

    for cls in classes:
        out.append('  <packagedElement xmi:type="uml:Class" xmi:id=%s name=%s>'
                   % (quoteattr(cls["id"]), quoteattr(cls["name"])))
        if cls["doc"]:
            out.append('    <ownedComment xmi:type="uml:Comment" xmi:id=%s>'
                       % quoteattr(cls["id"] + "-doc"))
            out.append("      <body>%s</body>" % escape(cls["doc"]))
            out.append("    </ownedComment>")
        for gen in generalizations.get(cls["id"], []):
            out.append('    <generalization xmi:type="uml:Generalization" xmi:id=%s general=%s/>'
                       % (quoteattr(gen["id"]), quoteattr(gen["general"])))
        for attr in cls["attributes"]:
            out.append('    <ownedAttribute xmi:type="uml:Property" xmi:id=%s name=%s type="String">'
                       % (quoteattr(attr["id"]), quoteattr(attr["name"])))
            if attr["doc"]:
                out.append('      <ownedComment xmi:type="uml:Comment" xmi:id=%s>'
                           % quoteattr(attr["id"] + "-doc"))
                out.append("        <body>%s</body>" % escape(attr["doc"]))
                out.append("      </ownedComment>")
            lower, _, upper = attr["multiplicity"].partition("..")
            upper = upper or lower
            out.append('      <lowerValue xmi:type="uml:LiteralInteger" value="%s"/>' % escape(lower))
            out.append('      <upperValue xmi:type="uml:LiteralUnlimitedNatural" value="%s"/>' % escape(upper))
            out.append("    </ownedAttribute>")
        out.append("  </packagedElement>")
    out.append("</uml:Model>")
    return "\n".join(out) + "\n"


def main():
    if len(sys.argv) < 2:
        print("Aufruf: archimate2xmi.py <archimate.xml> [Modellname]", file=sys.stderr)
        return 2
    name, doc, classes, gens = convert(sys.argv[1], sys.argv[2] if len(sys.argv) > 2 else None)
    sys.stdout.write(emit(name, doc, classes, gens))
    total = sum(len(c["attributes"]) for c in classes)
    print("%d Klassen, %d Attribute, %d Generalisierungen."
          % (len(classes), total, sum(len(g) for g in gens.values())), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
