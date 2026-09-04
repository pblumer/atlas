#!/usr/bin/env python3
"""Erzeugt ein Atlas-Informationsmodell aus Microsofts AD-Schemadokumentation.

Quelle ist der Markdown-Bestand hinter learn.microsoft.com/windows/win32/adschema,
oeffentlich im Repository MicrosoftDocs/win32 unter desktop-src/ADSchema:

    git clone --filter=blob:none --sparse https://github.com/MicrosoftDocs/win32.git
    cd win32 && git sparse-checkout set desktop-src/ADSchema

Je Klasse (c-*.md) steht dort eine Attributtabelle pro Windows-Version mit den
Spalten Attribut, Mandatory und "Derived from"; je Attribut (a-*.md) stehen
LDAP-Display-Name, Syntax und Is-Single-Valued. Genommen wird die neueste
dokumentierte Version und nur, was die Klasse selbst deklariert -- geerbte
Attribute stehen an der Oberklasse, die Vererbung selbst wird als
Generalisierung modelliert.

Ausgabe wahlweise Atlas-JSON (mit berechnetem Layout) oder UML XMI 2.5.1.
"""

import argparse
import json
import pathlib
import re
import sys
from xml.sax.saxutils import escape, quoteattr

# Der fachliche Ausschnitt: alles, was Account-, Berechtigungs- und
# Verzeichnisprozesse anfassen. Oberklassen werden automatisch ergaenzt.
SEEDS = [
    "c-user.md", "c-group.md", "c-computer.md", "c-contact.md",
    "c-inetorgperson.md", "c-organizationalunit.md", "c-container.md",
    "c-domain.md", "c-domaindns.md", "c-builtindomain.md",
    "c-foreignsecurityprincipal.md", "c-msds-managedserviceaccount.md",
    "c-msds-groupmanagedserviceaccount.md",
]

# Hilfsklassen (auxiliary classes) sind in UML keine Oberklassen, tragen in AD
# aber echte fachliche Attribute -- objectSid und sAMAccountName am
# Security-Principal, die Mailadressen am Mail-Recipient. Sie werden wie im
# bisherigen EA-Modell als Generalisierung gefuehrt; ausserhalb dieser Liste
# bleiben sie draussen, damit der Ausschnitt nicht ueber POSIX- und
# Exchange-Erweiterungen ausufert.
AUXILIARY = {"Security-Principal", "Mail-Recipient"}

ATTR_ROW = re.compile(
    r"^\|\s*\[\*\*(?P<attr>[^\]]+?)\*\*\]\((?P<file>a-[^)]+\.md)\)\s*\|"
    r"\s*(?P<mandatory>\w+)\s*\|\s*(?P<derived>.*?)\s*\|")

# LDAP-Syntax -> die sieben Primitiven, die Atlas kennt. Bewusst grob: ein
# Prozessinformationsmodell sagt, dass ein Konto ein Ablaufdatum hat, nicht dass
# es als Interval in 100-Nanosekunden-Schritten seit 1601 abgelegt ist.
SYNTAX = {
    "String(Unicode)": "string", "String(IA5)": "string",
    "String(Teletex)": "string", "String(Numeric)": "string",
    "String(Printable)": "string", "String(Case)": "string",
    "String(Object-Identifier)": "string", "String(Sid)": "string",
    "String(NT-Sec-Desc)": "string", "String(Octet)": "string",
    "Object(DS-DN)": "string", "Object(DN-Binary)": "string",
    "Object(DN-String)": "string", "Object(Replica-Link)": "string",
    "Object(Presentation-Address)": "string", "Object(Access-Point)": "string",
    "Object(OR-Name)": "string",
    "String(Generalized-Time)": "dateTime", "String(UTC-Time)": "dateTime",
    "Boolean": "boolean",
    "Enumeration": "number", "Integer": "number", "Interval": "number",
    "LargeInteger": "number",
}

# objectGUID ist der unveraenderliche Bezeichner jedes AD-Objekts. Microsoft
# fuehrt ihn als optional, weil das Verzeichnis ihn selbst setzt; als
# Geschaeftsschluessel muss er nach Atlas' Regel vorhanden und einwertig sein,
# also wird die Multiplizitaet hier bewusst auf 1 gezogen.
IDENTITY_CLASS, IDENTITY_ATTR = "Top", "objectGUID"


def cell(text):
    """Raeumt eine Markdown-Tabellenzelle zu blanken Bezeichnern ab."""
    text = re.sub(r"\(c-[^)]*\.md\)", " ", text)
    text = re.sub(r"[\*\[\]]|<br/>", " ", text)
    return [token for token in text.split() if token != "-"]


class Schema:
    def __init__(self, docs):
        self.docs = pathlib.Path(docs)
        self._attrs = {}

    def read(self, name):
        return (self.docs / name).read_text(encoding="utf-8")

    def field(self, text, entry):
        match = re.search(r"^\|\s*%s\s*\|\s*(.+?)\s*\|" % re.escape(entry), text, re.M)
        return match.group(1).strip() if match else ""

    def front_matter(self, text, key):
        match = re.search(r"^%s:\s*(.+?)\s*$" % re.escape(key), text, re.M)
        return match.group(1).strip() if match else ""

    def attribute(self, filename):
        """LDAP-Name, Typ, Einwertigkeit und Beschreibung eines Attributs."""
        if filename in self._attrs:
            return self._attrs[filename]
        text = self.read(filename)
        syntax = re.sub(r"[\*\[\]]|\(s-[^)]*\.md\)", "", self.field(text, "Syntax")).strip()
        single = "True" in self.field(text, "Is-Single-Valued")
        self._attrs[filename] = {
            "ldap": self.field(text, "Ldap-Display-Name"),
            "cn": self.field(text, "CN"),
            "type": SYNTAX.get(syntax, "string"),
            "syntax": syntax,
            "single": single,
            "doc": self.front_matter(text, "description"),
        }
        return self._attrs[filename]

    def klass(self, filename):
        """CN, Oberklasse, Hilfsklassen und die selbst deklarierten Attribute."""
        text = self.read(filename)
        cn = self.field(text, "CN") or filename[2:-3]
        parents = cell(re.findall(r"^\|\s*Subclass of\s*\|\s*(.+?)\s*\|", text, re.M)[-1]) \
            if re.search(r"^\|\s*Subclass of", text, re.M) else []
        aux = cell(re.findall(r"^\|\s*Auxiliary Classes\s*\|\s*(.+?)\s*\|", text, re.M)[-1]) \
            if re.search(r"^\|\s*Auxiliary Classes", text, re.M) else []

        sections = list(re.finditer(r"^## (.+?) Attributes\s*$", text, re.M))
        attributes, version = [], ""
        if sections:
            version = sections[-1].group(1)
            seen = set()
            for line in text[sections[-1].end():].splitlines():
                row = ATTR_ROW.match(line)
                if not row or cn not in cell(row.group("derived")):
                    continue
                info = self.attribute(row.group("file"))
                if not info["ldap"] or info["ldap"] in seen:
                    continue
                seen.add(info["ldap"])
                mandatory = row.group("mandatory") == "True"
                attributes.append({
                    "name": info["ldap"],
                    "type": info["type"],
                    "multiplicity": ("1" if mandatory else "0..1") if info["single"]
                                    else ("1..*" if mandatory else "0..*"),
                    "documentation": "%s - %s (LDAP-Syntax: %s)"
                                     % (info["cn"], info["doc"], info["syntax"]),
                })
        return {"file": filename, "cn": cn, "parents": parents, "auxiliary": aux,
                "version": version, "attributes": attributes,
                "documentation": self.front_matter(text, "description")}


def resolve(schema, seeds):
    """Saatklassen plus alle Oberklassen und die zugelassenen Hilfsklassen."""
    by_cn, queue, files = {}, list(seeds), set(seeds)
    while queue:
        current = queue.pop(0)
        if not (schema.docs / current).exists():
            print("HINWEIS: %s ist nicht dokumentiert und entfaellt." % current, file=sys.stderr)
            continue
        info = schema.klass(current)
        if info["cn"] in by_cn:
            continue
        by_cn[info["cn"]] = info
        for parent in info["parents"] + [a for a in info["auxiliary"] if a in AUXILIARY]:
            candidate = "c-%s.md" % parent.replace("-", "").lower()
            if parent != info["cn"] and candidate not in files:
                files.add(candidate)
                queue.append(candidate)
    return by_cn


def generalizations(by_cn):
    """Vererbung und zugelassene Hilfsklassen als gerichtete Kanten."""
    edges = []
    for cn, info in by_cn.items():
        targets = [p for p in info["parents"] if p != cn]
        targets += [a for a in info["auxiliary"] if a in AUXILIARY and a != cn]
        for target in dict.fromkeys(targets):
            if target in by_cn:
                edges.append((cn, target))
    return edges


def layout(by_cn, edges):
    """Spalte nach Vererbungstiefe, in der Spalte gestapelt nach Kastenhoehe.

    Eine Klasse mit 150 Attributen ist ein sehr hoher Kasten; ein Raster mit
    fester Zeilenhoehe wuerde sie ueber die naechste legen. Deshalb waechst der
    Abstand in der Spalte mit der Attributzahl.
    """
    parent_of = {}
    for source, target in edges:
        parent_of.setdefault(source, target)

    def depth(cn, guard=()):
        if cn in guard or cn not in parent_of:
            return 0
        return 1 + depth(parent_of[cn], guard + (cn,))

    columns = {}
    for cn in sorted(by_cn):
        columns.setdefault(depth(cn), []).append(cn)
    placed = {}
    for column, members in columns.items():
        y = 60.0
        for cn in members:
            placed[cn] = (60.0 + column * 400.0, y)
            y += 90.0 + 20.0 * len(by_cn[cn]["attributes"])
    return placed


def build(by_cn, edges, name, documentation):
    positions = layout(by_cn, edges)
    classes = []
    for cn in sorted(by_cn):
        info = by_cn[cn]
        attributes = [dict(a) for a in info["attributes"]]
        identity = []
        if cn == IDENTITY_CLASS:
            for attribute in attributes:
                if attribute["name"] == IDENTITY_ATTR:
                    attribute["multiplicity"] = "1"
                    attribute["documentation"] += (
                        " Als Geschaeftsschluessel gefuehrt: Microsoft fuehrt das "
                        "Attribut als optional, weil das Verzeichnis es selbst setzt.")
                    identity = [IDENTITY_ATTR]
        classes.append({
            "id": cn, "name": cn, "stereotype": "businessObject",
            "documentation": "%s (Active-Directory-Schemaklasse, Stand %s)"
                             % (info["documentation"], info["version"] or "unbekannt"),
            "attributes": attributes, "identity": identity,
            "x": positions[cn][0], "y": positions[cn][1],
        })
    associations = [{
        "id": "gen-%s-%s" % (source, target), "kind": "generalization",
        "from": {"classId": source}, "to": {"classId": target},
    } for source, target in sorted(edges)]
    return {"name": name, "documentation": documentation,
            "classes": classes, "associations": associations, "stores": []}


def to_xmi(model):
    out = ['<?xml version="1.0" encoding="UTF-8"?>',
           '<uml:Model xmi:version="20131001" '
           'xmlns:xmi="http://www.omg.org/spec/XMI/20131001" '
           'xmlns:uml="http://www.omg.org/spec/UML/20161101" '
           'xmi:id="model" name=%s>' % quoteattr(model["name"]),
           '  <ownedComment xmi:type="uml:Comment" xmi:id="model-doc">',
           "    <body>%s</body>" % escape(model["documentation"]),
           "  </ownedComment>"]
    for primitive in sorted({a["type"] for c in model["classes"] for a in c["attributes"]}):
        out.append('  <packagedElement xmi:type="uml:PrimitiveType" xmi:id=%s name=%s/>'
                   % (quoteattr("primitive-" + primitive), quoteattr(primitive)))
    parents = {}
    for association in model["associations"]:
        parents.setdefault(association["from"]["classId"], []).append(association)
    for klass in model["classes"]:
        out.append('  <packagedElement xmi:type="uml:Class" xmi:id=%s name=%s>'
                   % (quoteattr(klass["id"]), quoteattr(klass["name"])))
        out.append('    <ownedComment xmi:type="uml:Comment" xmi:id=%s>'
                   % quoteattr(klass["id"] + "-doc"))
        out.append("      <body>%s</body>" % escape(klass["documentation"]))
        out.append("    </ownedComment>")
        for association in parents.get(klass["id"], []):
            out.append('    <generalization xmi:type="uml:Generalization" xmi:id=%s general=%s/>'
                       % (quoteattr(association["id"]), quoteattr(association["to"]["classId"])))
        for attribute in klass["attributes"]:
            identifier = "%s-%s" % (klass["id"], attribute["name"])
            out.append('    <ownedAttribute xmi:type="uml:Property" xmi:id=%s name=%s type=%s%s>'
                       % (quoteattr(identifier), quoteattr(attribute["name"]),
                          quoteattr("primitive-" + attribute["type"]),
                          ' isID="true"' if attribute["name"] in klass["identity"] else ""))
            out.append('      <ownedComment xmi:type="uml:Comment" xmi:id=%s>'
                       % quoteattr(identifier + "-doc"))
            out.append("        <body>%s</body>" % escape(attribute["documentation"]))
            out.append("      </ownedComment>")
            lower, _, upper = attribute["multiplicity"].partition("..")
            out.append('      <lowerValue xmi:type="uml:LiteralInteger" value="%s"/>' % (upper and lower or lower))
            out.append('      <upperValue xmi:type="uml:LiteralUnlimitedNatural" value="%s"/>' % (upper or lower))
            out.append("    </ownedAttribute>")
        out.append("  </packagedElement>")
    out.append("</uml:Model>")
    return "\n".join(out) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("docs", help="Verzeichnis desktop-src/ADSchema")
    parser.add_argument("--format", choices=("json", "xmi"), default="json")
    parser.add_argument("--name", default="Active Directory Objektmodell")
    args = parser.parse_args()

    schema = Schema(args.docs)
    by_cn = resolve(schema, SEEDS)
    edges = generalizations(by_cn)
    documentation = (
        "Active-Directory-Schemaklassen fuer Identitaets- und Berechtigungsprozesse, "
        "erzeugt aus Microsofts Schemadokumentation (MicrosoftDocs/win32, "
        "desktop-src/ADSchema). Je Klasse nur die selbst deklarierten Attribute; "
        "geerbte stehen an der Oberklasse. Hilfsklassen (auxiliary classes) sind als "
        "Generalisierung gefuehrt.")
    model = build(by_cn, edges, args.name, documentation)

    if args.format == "json":
        sys.stdout.write(json.dumps(model, indent=2, ensure_ascii=False) + "\n")
    else:
        sys.stdout.write(to_xmi(model))

    total = sum(len(c["attributes"]) for c in model["classes"])
    print("%d Klassen, %d Attribute, %d Generalisierungen."
          % (len(model["classes"]), total, len(model["associations"])), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
