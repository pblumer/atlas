#!/usr/bin/env python3
"""Erzeugt die form-js-Schemata der SPoP-App: python3 tools/gen_forms.py . (aus examples/spop)."""
import json
import sys

OUTDIR = sys.argv[1]
NP = {"personal": "false"}


def text(t):
    return {"type": "text", "text": t}


forms = {
    "spop-teamsite-bestellung": ("SPoP: TeamSite bestellen (GF-001)", [
        text("**SharePoint OnPrem: TeamSite bestellen (PvN-GF-001)**\n\nClean Order: Das Formular enthält alle Angaben, die die Bereitstellung braucht. "
             "Standard-Sites mit Standardquota laufen ohne Freigabe; alle anderen Bestellungen gehen in die Vier-Augen-Freigabe."),
        {"key": "titel", "type": "textfield", "label": "Titel der TeamSite", "validate": {"required": True, "maxLength": 80}, "properties": NP},
        {"key": "amtKuerzel", "type": "textfield", "label": "Amtskürzel",
         "description": "2 bis 6 Grossbuchstaben, z. B. BIT. Bildet zusammen mit dem Site-Typ die URL.",
         "validate": {"required": True, "pattern": "^[A-Z]{2,6}$"}, "properties": NP},
        {"key": "siteTyp", "type": "select", "label": "Site-Typ", "defaultValue": "Standard", "validate": {"required": True},
         "values": [{"label": "Standard", "value": "Standard"}, {"label": "Projekt (Project)", "value": "Project"},
                    {"label": "Abteilung (Department)", "value": "Department"}], "properties": NP},
        {"key": "sprache", "type": "select", "label": "Sprache", "defaultValue": "1031", "validate": {"required": True},
         "values": [{"label": "Deutsch", "value": "1031"}, {"label": "Französisch", "value": "1036"},
                    {"label": "Italienisch", "value": "1040"}, {"label": "Englisch", "value": "1033"}], "properties": NP},
        {"key": "ownerUpn", "type": "textfield", "label": "Owner (UPN)",
         "description": "Verantwortliche Person, z. B. vorname.name@admin.ch. Wird in die Owner-Gruppe aufgenommen.",
         "validate": {"required": True, "pattern": "^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$"}},
        {"key": "memberUpns", "type": "textarea", "label": "Members (UPN, eine pro Zeile)",
         "description": "Optional. Werden in die Member-Gruppe aufgenommen."},
        {"key": "quotaWahl", "type": "radio", "label": "Speicherquota", "defaultValue": "Standard", "validate": {"required": True},
         "values": [{"label": "Standard des Site-Typs", "value": "Standard"}, {"label": "Individuell (Custom)", "value": "Custom"}],
         "properties": NP},
        {"key": "quotaMb", "type": "number", "label": "Quota (MB)",
         "description": "Minimum 1024 MB; Maximum Standard 10240, Project 51200, Department 102400 MB.",
         "conditional": {"hide": "=quotaWahl != \"Custom\""}, "validate": {"min": 1024, "max": 102400}, "properties": NP},
        {"key": "quotaBegruendung", "type": "textarea", "label": "Begründung für die individuelle Quota",
         "description": "Pflicht bei individueller Quota, mindestens 10 Zeichen.",
         "conditional": {"hide": "=quotaWahl != \"Custom\""}, "validate": {"minLength": 10}, "properties": NP},
        {"key": "bemerkung", "type": "textarea", "label": "Bemerkung", "properties": NP},
    ]),
    "spop-quota-erhoehung": ("SPoP: Quota-Erhöhung (GF-002)", [
        text("**SharePoint OnPrem: Quota-Erhöhung (PvN-GF-002)**\n\nGilt für die TeamSite dieser Position. Die neue Quota muss "
             "grösser als die aktuelle sein. Eine Erhöhung um mehr als 5120 MB geht in die Vier-Augen-Freigabe."),
        {"key": "neueQuotaMb", "type": "number", "label": "Neue Quota (MB)",
         "validate": {"required": True, "min": 1024, "max": 102400}, "properties": NP},
        {"key": "begruendung", "type": "textarea", "label": "Begründung", "description": "Mindestens 10 Zeichen.",
         "validate": {"required": True, "minLength": 10}, "properties": NP},
    ]),
    "spop-extern-einladen": ("SPoP: Externen Benutzer einladen (GF-003)", [
        text("**SharePoint OnPrem: Externen Benutzer einladen (PvN-GF-003)**\n\nGilt für die TeamSite dieser Position. Private Mail-Domains sind nicht zugelassen; "
             "für externe Benutzer gilt MFA-Pflicht. Contribute und Full Control gehen in die Vier-Augen-Freigabe. "
             "Die Berechtigung wird erst gesetzt, wenn die Einladung angenommen ist; sonst wird sie nach Ablauf widerrufen."),
        {"key": "email", "type": "textfield", "label": "E-Mail-Adresse des externen Benutzers",
         "validate": {"required": True, "pattern": "^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$"}},
        {"key": "rolle", "type": "select", "label": "Berechtigung", "defaultValue": "Read", "validate": {"required": True},
         "description": "Least Privilege: nur so viel wie nötig.",
         "values": [{"label": "Lesen (Read)", "value": "Read"}, {"label": "Bearbeiten (Contribute)", "value": "Contribute"},
                    {"label": "Vollzugriff (Full Control)", "value": "Full Control"}], "properties": NP},
        {"key": "ablaufTage", "type": "number", "label": "Gültigkeit der Einladung (Tage)", "defaultValue": 30,
         "validate": {"required": True, "min": 1, "max": 90}, "properties": NP},
        {"key": "nachricht", "type": "textarea", "label": "Nachricht an den eingeladenen Benutzer", "properties": NP},
    ]),
    "spop-teamsite-rueckgabe": ("SPoP: TeamSite zurückgeben", [
        text("**SharePoint OnPrem: TeamSite zurückgeben**\n\nGilt für die TeamSite dieser Position. Entfernt alle externen Zugriffe, "
             "verschiebt die Site in den Papierkorb und bricht eine laufende Quota-Erhöhung oder Einladung ab."),
        {"key": "grund", "type": "textarea", "label": "Grund der Rückgabe", "validate": {"required": True}, "properties": NP},
    ]),
    "spop-freigabe": ("SPoP: Vier-Augen-Freigabe", [
        text("**Vier-Augen-Freigabe**\n\nBitte den Antrag prüfen. Die Freigabe darf nicht durch die antragstellende Person erfolgen."),
        {"key": "zusammenfassung", "type": "textarea", "label": "Antrag", "disabled": True, "properties": NP},
        {"key": "freigabeEntscheid", "type": "radio", "label": "Entscheid", "validate": {"required": True},
         "values": [{"label": "Freigeben", "value": "freigegeben"}, {"label": "Ablehnen", "value": "abgelehnt"}], "properties": NP},
        {"key": "freigabeKommentar", "type": "textarea", "label": "Kommentar",
         "description": "Bei einer Ablehnung wird der Kommentar der antragstellenden Person mitgeteilt.", "properties": NP},
    ]),
}

for fid, (name, comps) in forms.items():
    schema = {"type": "default", "id": fid, "components": comps}
    with open(f"{OUTDIR}/{fid}.form.json", "w") as f:
        json.dump(schema, f, ensure_ascii=False, indent=2)
        f.write("\n")
print("ok", list(forms))
