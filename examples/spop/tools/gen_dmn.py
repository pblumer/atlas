#!/usr/bin/env python3
"""Erzeugt die vier DMN-Modelle (Hit Policy FIRST) anstelle der OPA-Policies: python3 tools/gen_dmn.py . (aus examples/spop)."""
import sys
from xml.sax.saxutils import escape

OUT = sys.argv[1]
S = '"{}"'.format

# Site-Typen: Schlüssel, Produkt, Material, Vorlage, Features, Standardquota MB, Maximum MB, Vier-Augen bei Standardquota.
# Vorlagen, Features, Grenzwerte und Freigabepflicht sind Annahmen (die Katalogtexte stützen sich auf kein Factsheet).
TYPES = [
    ("hermes", "spop-site-hermes", "540007512", "STS#3 + HERMES-Struktur", "TeamCollab, HERMES-Projektstruktur", 10240, 51200, True),
    ("communication", "spop-site-communication", "540007514", "SITEPAGEPUBLISHING#0", "Publishing, AudienceTargeting", 10240, 51200, True),
    ("publishing", "spop-site-publishing", "540007515", "BLANKINTERNET#0", "PublishingSite, PublishingWeb, Variations", 10240, 51200, True),
    ("teamsite-classic", "spop-site-teamsite-classic", "540007516", "STS#0", "TeamCollab", 5120, 10240, False),
    ("teamsite-modern", "spop-site-teamsite-modern", "540007560", "STS#3", "TeamCollab, ModernPages", 5120, 10240, False),
    ("wiki", "spop-site-wiki", "540008180", "ENTERWIKI#0", "EnterpriseWiki, PublishingSite", 5120, 10240, False),
]
ITEMS = ",".join(S(t[1]) for t in TYPES)
NO = ["false", "false", "0", S(""), S(""), S(""), S("")]


def reject(reason):
    return NO + [S(reason)]


teamsite_rules = [
    (["false", "-", "-", "-", "-", "-"], reject("Das Amtskürzel entspricht nicht der Namensregel (2 bis 6 Grossbuchstaben).")),
    (["-", "false", "-", "-", "-", "-"], reject("Der Kurzname (URL-Teil) muss 3 bis 30 Zeichen haben: Kleinbuchstaben, Ziffern und Bindestriche.")),
    (["-", "-", f"not({ITEMS})", "-", "-", "-"], reject("Unbekanntes Produkt: kein SharePoint-OnP-Site-Typ.")),
    (["-", "-", "-", 'not("Standard","Custom")', "-", "-"], reject("Unbekannte Quota-Wahl.")),
    (["-", "-", "-", S("Custom"), "-", "false"], reject("Eine individuelle Quota braucht eine Begründung (mindestens 10 Zeichen).")),
    (["-", "-", "-", S("Custom"), "< 1024", "-"], reject("Die Quota muss mindestens 1024 MB betragen.")),
]
for key, item, mat, tpl, feat, std, mx, ve in TYPES:
    teamsite_rules.append((["-", "-", S(item), S("Custom"), f"> {mx}", "-"], reject(f"Über dem Maximum für {key} ({mx} MB).")))
for key, item, mat, tpl, feat, std, mx, ve in TYPES:
    out = [S(key), S(tpl), S(feat), S(mat)]
    teamsite_rules.append((["-", "-", S(item), S("Standard"), "-", "-"],
                           ["true", "true" if ve else "false", str(std)] + out +
                           [S(f"{key}: Vier-Augen-Prinzip (Owner-Zuweisung ausserhalb Self-Service).") if ve else S(f"{key} im Self-Service: ohne Freigabe.")]))
    teamsite_rules.append((["-", "-", S(item), S("Custom"), "-", "-"],
                           ["true", "true", "quotaMb"] + out + [S(f"{key} mit individueller Quota: Vier-Augen-Prinzip.")]))
teamsite_rules.append((["-", "-", "-", "-", "-", "-"], reject("Die Bestellung ist unvollständig.")))

BIG = ",".join(S(t[0]) for t in TYPES if t[6] == 51200)
SMALL = ",".join(S(t[0]) for t in TYPES if t[6] == 10240)


def band(item, rng, mat, unit, ve, text):
    return (["-", S(item), rng, "-"], ["true", "true" if ve else "false", S(mat), S(unit), S(text)])


def zreject(inputs, text):
    return (inputs, ["false", "false", S(""), S(""), S(text)])


# Zeilen in der Reihenfolge, in der FIRST sie liest: zuerst die Ablehnungen, dann je Produktschlüssel die Bänder.
zusatz_rules = [
    zreject(["false", "-", "-", "-"], "Die Site ist noch nicht bereitgestellt."),
    zreject(["-", "-", "-", "true"], "Die Menge ist unverändert."),
    band("spop-speicher", "[1..100]", "540007510", "GB", False, "Speicherplatzerweiterung bis 100 GB: ohne Freigabe."),
    band("spop-speicher", "(100..500]", "540007510", "GB", True, "Speicherplatzerweiterung über 100 GB: Vier-Augen-Prinzip."),
    zreject(["-", S("spop-speicher"), "-", "-"], "Speicherplatzerweiterung: 1 bis 500 GB."),
    band("spop-reporting", "0", "", "Pakete", False, "Erweitertes Reporting beendet: keine Verrechnung mehr."),
    band("spop-reporting", "[1..5]", "540007511", "Pakete", False, "Erweitertes Reporting 1–5 Pakete."),
    band("spop-reporting", "[6..10]", "540007703", "Pakete", False, "Erweitertes Reporting 6–10 Pakete."),
    band("spop-reporting", "[11..15]", "540007702", "Pakete", False, "Erweitertes Reporting 11–15 Pakete."),
    band("spop-reporting", "[16..20]", "540007704", "Pakete", False, "Erweitertes Reporting 16–20 Pakete."),
    zreject(["-", S("spop-reporting"), "-", "-"], "Erweitertes Reporting: 0 bis 20 Pakete."),
    band("spop-subsite-quota", "[0..10]", "", "Subsites", False, "SubSite Quota auf die Basis (10) zurückgesetzt: keine Verrechnung mehr."),
    band("spop-subsite-quota", "[11..20]", "540007513", "Subsites", False, "SubSite Quota 11–20."),
    band("spop-subsite-quota", "[21..50]", "540007705", "Subsites", False, "SubSite Quota 21–50."),
    band("spop-subsite-quota", "[51..100]", "540007706", "Subsites", False, "SubSite Quota 51–100."),
    band("spop-subsite-quota", "[101..200]", "540007707", "Subsites", True, "SubSite Quota 101–200: Vier-Augen-Prinzip."),
    band("spop-subsite-quota", "[201..500]", "540007708", "Subsites", True, "SubSite Quota 201–500: Vier-Augen-Prinzip."),
    zreject(["-", S("spop-subsite-quota"), "-", "-"], "SubSite Quota: 10 bis 500 Subsites; bis 10 sind in der Basis enthalten."),
    zreject(["-", "-", "-", "-"], "Unbekannter Produktschlüssel: keine Leistung dieser Tabelle."),
]

decisions = [
    dict(
        id="SpopTeamsiteRichtlinie", name="Site-Richtlinie (GF-001)",
        doc="Ersetzt amt_naming.rego, quota_limits.rego und approval.rego und bildet das Katalogprodukt auf den Site-Typ ab: "
            "Vorlage, Features, Standard- und Maximalquota, Freigabepflicht und Materialnummer. Annahmen: Amtskürzel 2 bis 6 "
            "Grossbuchstaben; Kurzname 3 bis 30 Zeichen; Vorlagen, Features, Grenzwerte und Freigabepflicht je Typ wie in der "
            "Tabelle; Hermes, Communication und Publishing immer mit Vier-Augen-Prinzip; individuelle Quota immer mit Freigabe.",
        inputs=[("amtGueltig", "boolean"), ("kurznameGueltig", "boolean"), ("produkt", "string"), ("quotaWahl", "string"),
                ("quotaMb", "number"), ("begruendungVorhanden", "boolean")],
        outputs=[("zulaessig", "boolean"), ("vierAugen", "boolean"), ("quotaEffektivMb", "number"), ("siteTyp", "string"),
                 ("template", "string"), ("features", "string"), ("material", "string"), ("grund", "string")],
        rules=teamsite_rules),
    dict(
        id="SpopExternRichtlinie", name="Externe-Benutzer-Richtlinie (GF-003)",
        doc="Ersetzt external_user.rego, mfa_required.rego und approval.rego. Annahmen: Liste privater Mail-Domains; "
            "Gültigkeit 1 bis 90 Tage; MFA ist für alle externen Benutzer Pflicht; Vier-Augen-Prinzip ab Contribute.",
        inputs=[("siteGefunden", "boolean"), ("emailDomain", "string"), ("rolle", "string"), ("ablaufTage", "number")],
        outputs=[("zulaessig", "boolean"), ("vierAugen", "boolean"), ("mfaPflicht", "boolean"), ("grund", "string")],
        rules=[
            (["false", "-", "-", "-"], ["false", "false", "true", S("Die Site wurde nicht gefunden.")]),
            (["-", S(""), "-", "-"], ["false", "false", "true", S("Die E-Mail-Adresse ist ungültig.")]),
            (["-", '"gmail.com","googlemail.com","hotmail.com","outlook.com","live.com","yahoo.com","icloud.com","gmx.ch","gmx.net","gmx.de","bluewin.ch"', "-", "-"],
             ["false", "false", "true", S("Private E-Mail-Domains sind für externe Benutzer nicht zugelassen.")]),
            (["-", "-", "-", "< 1"], ["false", "false", "true", S("Die Gültigkeit muss zwischen 1 und 90 Tagen liegen.")]),
            (["-", "-", "-", "> 90"], ["false", "false", "true", S("Die Gültigkeit muss zwischen 1 und 90 Tagen liegen.")]),
            (["-", "-", S("Read"), "-"], ["true", "false", "true", S("Leserecht: ohne Freigabe, MFA-Pflicht.")]),
            (["-", "-", '"Contribute","Full Control"', "-"], ["true", "true", "true", S("Contribute oder Full Control: Vier-Augen-Prinzip, MFA-Pflicht.")]),
            (["-", "-", "-", "-"], ["false", "false", "true", S("Unbekannte Rolle.")]),
        ]),
    dict(
        id="SpopZusatzRichtlinie", name="Zusatzleistung-Richtlinie",
        doc="Zulässige Menge, Verrechnungsband und Materialnummer der mengenabhängigen Aktionen einer Site, unterschieden nach "
            "Produktschlüssel: Speicherplatzerweiterung spop-speicher 540007510 (1 bis 500 GB, Freigabe über 100 GB), "
            "Erweitertes Reporting spop-reporting 540007511/540007703/540007702/540007704 (0 bis 20 Pakete; 0 beendet), "
            "SubSite Quota spop-subsite-quota 540007513/540007705/540007706/540007707/540007708 (10 bis 500; bis 10 Basis, "
            "Freigabe ab 101). Die Werte sind Annahmen. Abgelehnt wird auch, wenn die Site noch nicht bereitgestellt ist "
            "(siteGefunden) oder die Menge unverändert bleibt.",
        inputs=[("siteGefunden", "boolean"), ("produkt", "string"), ("menge", "number"), ("unveraendert", "boolean")],
        outputs=[("zulaessig", "boolean"), ("vierAugen", "boolean"), ("material", "string"), ("einheit", "string"), ("grund", "string")],
        rules=zusatz_rules),
    dict(
        id="SpopAenderungRichtlinie", name="Site-Änderung-Richtlinie",
        doc="Prüft die Aktionen, die die Angaben einer bereitgestellten Site ändern: site-umbenennen (aenderung titel), "
            "site-url-aendern (url) und berechtigungen-aendern (berechtigungen). Ob die neuen Angaben gültig sind, berechnet der "
            "Prozess (gueltig), ebenso ob sie dem heutigen Stand entsprechen (unveraendert) und ob der Owner wechselt "
            "(ownerWechsel). Annahmen: Titeländerung ohne Freigabe; eine neue URL macht bestehende Links ungültig und braucht "
            "immer die Vier-Augen-Freigabe; ein Owner-Wechsel braucht sie bei Hermes, Communication und Publishing, wie die "
            "Owner-Zuweisung bei der Bestellung.",
        inputs=[("siteGefunden", "boolean"), ("aenderung", "string"), ("gueltig", "boolean"), ("unveraendert", "boolean"),
                ("siteTyp", "string"), ("ownerWechsel", "boolean")],
        outputs=[("zulaessig", "boolean"), ("vierAugen", "boolean"), ("grund", "string")],
        rules=[
            (["false", "-", "-", "-", "-", "-"], ["false", "false", S("Die Site ist noch nicht bereitgestellt.")]),
            (["-", S("titel"), "false", "-", "-", "-"], ["false", "false", S("Der Titel muss 1 bis 80 Zeichen haben.")]),
            (["-", S("url"), "false", "-", "-", "-"],
             ["false", "false", S("Die neue URL ist ungültig oder vergeben: Amtskürzel 2 bis 6 Grossbuchstaben, Kurzname 3 bis 30 "
                                  "Zeichen aus Kleinbuchstaben, Ziffern und Bindestrichen.")]),
            (["-", S("berechtigungen"), "false", "-", "-", "-"],
             ["false", "false", S("Der Owner muss als UPN (name@domain) angegeben sein und im AD existieren.")]),
            (["-", "-", "-", "true", "-", "-"], ["false", "false", S("Die Angaben entsprechen dem heutigen Stand der Site; es gibt nichts zu ändern.")]),
            (["-", S("titel"), "-", "-", "-", "-"], ["true", "false", S("Titeländerung im Self-Service: ohne Freigabe.")]),
            (["-", S("url"), "-", "-", "-", "-"], ["true", "true", S("Eine neue Site-URL macht bestehende Links ungültig: Vier-Augen-Prinzip.")]),
            (["-", S("berechtigungen"), "-", "-", BIG, "true"],
             ["true", "true", S("Owner-Wechsel bei Hermes, Communication oder Publishing: Vier-Augen-Prinzip.")]),
            (["-", S("berechtigungen"), "-", "-", "-", "-"], ["true", "false", S("Berechtigungsänderung im Self-Service: ohne Freigabe.")]),
            (["-", "-", "-", "-", "-", "-"], ["false", "false", S("Unbekannte Änderung.")]),
        ]),
]

import os
for d in decisions:
    p = d["id"]
    o = ['<?xml version="1.0" encoding="UTF-8"?>',
         f'<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_{p}" name="{p}" namespace="http://atlas/dmn">']
    for n, t in d["inputs"]:
        o.append(f'<inputData id="id_{n}" name="{n}"><variable id="v_{n}" name="{n}" typeRef="{t}"/></inputData>')
    o.append(f'<decision id="{p}" name="{escape(d["name"])}"><description>{escape(d["doc"])}</description><variable id="v_{p}" name="{p}"/>')
    o.append("".join(f'<informationRequirement id="ir_{n}"><requiredInput href="#id_{n}"/></informationRequirement>' for n, _ in d["inputs"]))
    o.append(f'<decisionTable id="dt_{p}" hitPolicy="FIRST">')
    for i, (n, t) in enumerate(d["inputs"]):
        o.append(f'<input id="i{i}" label="{n}"><inputExpression id="ie{i}" typeRef="{t}"><text>{n}</text></inputExpression></input>')
    for i, (n, t) in enumerate(d["outputs"]):
        o.append(f'<output id="o{i}" label="{n}" name="{n}" typeRef="{t}"/>')
    for r, (ins, outs) in enumerate(d["rules"], 1):
        o.append(f'<rule id="r{r}">' + "".join(f'<inputEntry id="r{r}i{i}"><text>{escape(v)}</text></inputEntry>' for i, v in enumerate(ins))
                 + "".join(f'<outputEntry id="r{r}o{i}"><text>{escape(v)}</text></outputEntry>' for i, v in enumerate(outs)) + '</rule>')
    o.append('</decisionTable></decision></definitions>')
    open(os.path.join(OUT, p + ".dmn"), "w").write("\n".join(o) + "\n")
print("ok")
