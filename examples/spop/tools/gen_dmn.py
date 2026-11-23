#!/usr/bin/env python3
"""Erzeugt die drei DMN-Modelle (Hit Policy FIRST) anstelle der OPA-Policies: python3 tools/gen_dmn.py . (aus examples/spop)."""
import sys
from xml.sax.saxutils import escape

OUT = sys.argv[1]
S = '"{}"'.format

decisions = [
    dict(
        id="SpopTeamsiteRichtlinie", name="TeamSite-Richtlinie (GF-001)",
        doc="Ersetzt amt_naming.rego, quota_limits.rego und approval.rego. Annahmen: Amtskürzel 2 bis 6 Grossbuchstaben; "
            "Standardquota Standard 5120 MB, Project 10240 MB, Department 20480 MB; Maximum 10240, 51200 und 102400 MB; "
            "Minimum 1024 MB; Vier-Augen-Prinzip ausser beim Self-Service Standard (Standard-Site mit Standardquota).",
        inputs=[("amtGueltig", "boolean"), ("siteTyp", "string"), ("quotaWahl", "string"), ("quotaMb", "number"),
                ("begruendungVorhanden", "boolean")],
        outputs=[("zulaessig", "boolean"), ("vierAugen", "boolean"), ("quotaEffektivMb", "number"), ("grund", "string")],
        rules=[
            (["false", "-", "-", "-", "-"], ["false", "false", "0", S("Das Amtskürzel entspricht nicht der Namensregel (2 bis 6 Grossbuchstaben).")]),
            (["-", 'not("Standard","Project","Department")', "-", "-", "-"], ["false", "false", "0", S("Unbekannter Site-Typ.")]),
            (["-", "-", 'not("Standard","Custom")', "-", "-"], ["false", "false", "0", S("Unbekannte Quota-Wahl.")]),
            (["-", "-", S("Custom"), "-", "false"], ["false", "false", "0", S("Eine individuelle Quota braucht eine Begründung (mindestens 10 Zeichen).")]),
            (["-", "-", S("Custom"), "< 1024", "-"], ["false", "false", "0", S("Die Quota muss mindestens 1024 MB betragen.")]),
            (["-", S("Standard"), S("Custom"), "> 10240", "-"], ["false", "false", "0", S("Über dem Maximum für Standard (10240 MB).")]),
            (["-", S("Project"), S("Custom"), "> 51200", "-"], ["false", "false", "0", S("Über dem Maximum für Project (51200 MB).")]),
            (["-", S("Department"), S("Custom"), "> 102400", "-"], ["false", "false", "0", S("Über dem Maximum für Department (102400 MB).")]),
            (["-", S("Standard"), S("Standard"), "-", "-"], ["true", "false", "5120", S("Self-Service Standard: ohne Freigabe.")]),
            (["-", S("Standard"), S("Custom"), "-", "-"], ["true", "true", "quotaMb", S("Individuelle Quota: Vier-Augen-Prinzip.")]),
            (["-", S("Project"), S("Standard"), "-", "-"], ["true", "true", "10240", S("Owner-Zuweisung ausserhalb Self-Service Standard: Vier-Augen-Prinzip.")]),
            (["-", S("Project"), S("Custom"), "-", "-"], ["true", "true", "quotaMb", S("Owner-Zuweisung ausserhalb Self-Service Standard: Vier-Augen-Prinzip.")]),
            (["-", S("Department"), S("Standard"), "-", "-"], ["true", "true", "20480", S("Owner-Zuweisung ausserhalb Self-Service Standard: Vier-Augen-Prinzip.")]),
            (["-", S("Department"), S("Custom"), "-", "-"], ["true", "true", "quotaMb", S("Owner-Zuweisung ausserhalb Self-Service Standard: Vier-Augen-Prinzip.")]),
            (["-", "-", "-", "-", "-"], ["false", "false", "0", S("Die Bestellung ist unvollständig.")]),
        ]),
    dict(
        id="SpopQuotaRichtlinie", name="Quota-Richtlinie (GF-002)",
        doc="Ersetzt quota_limits.rego und approval.rego. Annahmen: Maximum je Site-Typ wie GF-001; Vier-Augen-Prinzip, "
            "wenn die Erhöhung mehr als 5120 MB beträgt.",
        inputs=[("siteGefunden", "boolean"), ("siteTyp", "string"), ("neueQuotaMb", "number"), ("erhoehungMb", "number"),
                ("begruendungVorhanden", "boolean")],
        outputs=[("zulaessig", "boolean"), ("vierAugen", "boolean"), ("grund", "string")],
        rules=[
            (["false", "-", "-", "-", "-"], ["false", "false", S("Die Site wurde nicht gefunden.")]),
            (["-", "-", "-", "-", "false"], ["false", "false", S("Die Begründung fehlt (mindestens 10 Zeichen).")]),
            (["-", "-", "-", "<= 0", "-"], ["false", "false", S("Die neue Quota muss grösser als die aktuelle sein.")]),
            (["-", S("Standard"), "> 10240", "-", "-"], ["false", "false", S("Über dem Maximum für Standard (10240 MB).")]),
            (["-", S("Project"), "> 51200", "-", "-"], ["false", "false", S("Über dem Maximum für Project (51200 MB).")]),
            (["-", S("Department"), "> 102400", "-", "-"], ["false", "false", S("Über dem Maximum für Department (102400 MB).")]),
            (["-", "-", "-", "> 5120", "-"], ["true", "true", S("Erhöhung um mehr als 5120 MB: Vier-Augen-Prinzip.")]),
            (["-", "-", "-", "-", "-"], ["true", "false", S("Erhöhung bis 5120 MB: ohne Freigabe.")]),
        ]),
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
