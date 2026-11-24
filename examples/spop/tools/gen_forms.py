#!/usr/bin/env python3
"""Erzeugt die form-js-Schemata der SPoP-App: python3 tools/gen_forms.py . (aus examples/spop)."""
import json
import sys

OUTDIR = sys.argv[1]
NP = {"personal": "false"}


def text(t):
    return {"type": "text", "text": t}


forms = {
    "spop-teamsite-bestellung": ("SPoP: SharePoint OnP Site bestellen (GF-001)", [
        text("**SharePoint OnPrem: Site bestellen (PvN-GF-001)**\n\nClean Order: Das Formular enthält alle Angaben, die die "
             "Bereitstellung braucht. Der Site-Typ ist das gewählte Produkt. Team Site Classic, Team Site Modern und Wiki Site mit "
             "Standardquota laufen ohne Freigabe; Hermes, Communication und Publishing sowie jede individuelle Quota gehen in die "
             "Vier-Augen-Freigabe."),
        {"key": "titel", "type": "textfield", "label": "Titel der Site", "validate": {"required": True, "maxLength": 80}, "properties": NP},
        {"key": "amtKuerzel", "type": "textfield", "label": "Amtskürzel",
         "description": "2 bis 6 Grossbuchstaben, z. B. BIT. Erster Teil der URL.",
         "validate": {"required": True, "pattern": "^[A-Z]{2,6}$"}, "properties": NP},
        {"key": "kurzname", "type": "textfield", "label": "Kurzname (URL-Teil)",
         "description": "3 bis 30 Zeichen, Kleinbuchstaben, Ziffern und Bindestriche. Die Site erhält die URL /sites/<amt>-<kurzname>.",
         "validate": {"required": True, "pattern": "^[a-z0-9][a-z0-9-]{2,29}$"}, "properties": NP},
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
         "description": "Minimum 1024 MB; Maximum 10240 MB (Team Sites, Wiki) bzw. 51200 MB (Hermes, Communication, Publishing). "
                        "Mehr Speicher später über die Aktion «Speicherplatz erweitern».",
         "conditional": {"hide": "=quotaWahl != \"Custom\""}, "validate": {"min": 1024, "max": 51200}, "properties": NP},
        {"key": "quotaBegruendung", "type": "textarea", "label": "Begründung für die individuelle Quota",
         "description": "Pflicht bei individueller Quota, mindestens 10 Zeichen.",
         "conditional": {"hide": "=quotaWahl != \"Custom\""}, "validate": {"minLength": 10}, "properties": NP},
        {"key": "bemerkung", "type": "textarea", "label": "Bemerkung", "properties": NP},
        text("**Hinweis zu «Angaben ändern»:** Nach der Bereitstellung ändert eine Korrektur dieser Angaben nur den Datensatz "
             "der Bestellung, nicht die Site. Änderungen an der Site laufen über die Aktionen der Position:\n\n"
             "- Titel: «Site umbenennen»\n"
             "- Amtskürzel und Kurzname (URL): «Site-URL ändern»\n"
             "- Owner und Members: «Berechtigungen ändern»\n"
             "- Quota: «Speicherplatz erweitern»\n\n"
             "Die Sprache legt SharePoint bei der Erstellung der Site fest; sie lässt sich danach nicht ändern. Die Bemerkung "
             "ist eine reine Angabe ohne Wirkung auf die Site."),
    ]),
    "spop-site-umbenennen": ("SPoP: Site umbenennen", [
        text("**SharePoint OnPrem: Site umbenennen**\n\nÄndert den Titel der Site dieser Position. Die URL bleibt gleich. "
             "Ohne Freigabe."),
        {"key": "neuerTitel", "type": "textfield", "label": "Neuer Titel der Site",
         "validate": {"required": True, "maxLength": 80}, "properties": NP},
        {"key": "begruendung", "type": "textarea", "label": "Begründung", "properties": NP},
    ]),
    "spop-site-url-aendern": ("SPoP: Site-URL ändern", [
        text("**SharePoint OnPrem: Site-URL ändern**\n\nGibt der Site dieser Position eine neue URL /sites/<amt>-<kurzname>. "
             "Bestehende Links und Lesezeichen auf die alte URL werden ungültig. Eine neue URL braucht immer die "
             "Vier-Augen-Freigabe."),
        {"key": "neuesAmtKuerzel", "type": "textfield", "label": "Amtskürzel",
         "description": "2 bis 6 Grossbuchstaben, z. B. BIT.",
         "validate": {"required": True, "pattern": "^[A-Z]{2,6}$"}, "properties": NP},
        {"key": "neuerKurzname", "type": "textfield", "label": "Kurzname (URL-Teil)",
         "description": "3 bis 30 Zeichen, Kleinbuchstaben, Ziffern und Bindestriche.",
         "validate": {"required": True, "pattern": "^[a-z0-9][a-z0-9-]{2,29}$"}, "properties": NP},
        {"key": "begruendung", "type": "textarea", "label": "Begründung", "validate": {"required": True}, "properties": NP},
    ]),
    "spop-berechtigungen-aendern": ("SPoP: Berechtigungen ändern", [
        text("**SharePoint OnPrem: Berechtigungen ändern**\n\nSetzt Owner und Members der Site dieser Position neu. Anzugeben "
             "ist der **vollständige** neue Stand: Wer nicht mehr aufgeführt ist, wird aus der Gruppe entfernt. Ein Owner-Wechsel "
             "bei Hermes, Communication und Publishing geht in die Vier-Augen-Freigabe."),
        {"key": "neuerOwnerUpn", "type": "textfield", "label": "Owner (UPN)",
         "description": "Verantwortliche Person, z. B. vorname.name@admin.ch.",
         "validate": {"required": True, "pattern": "^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$"}},
        {"key": "neueMemberUpns", "type": "textarea", "label": "Members (UPN, eine pro Zeile)",
         "description": "Leer lassen, wenn die Site keine Members haben soll."},
        {"key": "begruendung", "type": "textarea", "label": "Begründung", "properties": NP},
    ]),
    "spop-quota-erhoehung": ("SPoP: Speicherplatz erweitern (GF-002)", [
        text("**SharePoint OnP Speicherplatzerweiterung (PvN-GF-002, Material 540007510)**\n\nZusatzspeicher für die Site dieser "
             "Position, über ihre inbegriffene Quota hinaus. Anzugeben ist die neue **Gesamtmenge** an Zusatzspeicher in GB, nicht "
             "die Differenz; 0 ist nicht möglich, für weniger Speicher ist eine kleinere Menge anzugeben. Verrechnung pro GB und "
             "Monat. Über 100 GB geht die Erweiterung in die Vier-Augen-Freigabe."),
        {"key": "menge", "type": "number", "label": "Zusatzspeicher gesamt (GB)", "description": "1 bis 500 GB.",
         "validate": {"required": True, "min": 1, "max": 500}, "properties": NP},
        {"key": "begruendung", "type": "textarea", "label": "Begründung", "properties": NP},
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
    "spop-teamsite-rueckgabe": ("SPoP: Site zurückgeben", [
        text("**SharePoint OnPrem: Site zurückgeben**\n\nGilt für die Site dieser Position. Entfernt alle externen Zugriffe, "
             "verschiebt die Site in den Papierkorb und beendet ihre Zusatzleistungen. Während eine Änderung der Site läuft, "
             "etwa eine Freigabe aussteht, ist die Rückgabe erst nach deren Abschluss möglich."),
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
    "spop-reporting": ("SPoP: Erweitertes Reporting", [
        text("**SharePoint OnP Erweitertes Reporting**\n\nReport-Pakete für die Site dieser Position (z. B. Nutzung, Speicher, "
             "Berechtigungen, Performance). CHF 25.– pro Paket und Monat. Anzugeben ist die neue **Gesamtzahl** der Pakete; "
             "0 beendet die Leistung. Die Anzahl bestimmt das Verrechnungsband: 1–5, 6–10, 11–15 oder 16–20 Pakete."),
        {"key": "menge", "type": "number", "label": "Report-Pakete gesamt", "description": "0 bis 20 Pakete; 0 beendet das Reporting.",
         "validate": {"required": True, "min": 0, "max": 20}, "properties": NP},
        {"key": "begruendung", "type": "textarea", "label": "Begründung", "properties": NP},
    ]),
    "spop-subsites": ("SPoP: SubSite Quota", [
        text("**SharePoint OnP SubSite Quota**\n\nErlaubte Zahl der Subsites der Site dieser Position. Bis 10 Subsites sind in der "
             "Basis enthalten; 10 beendet die Leistung. Die Anzahl bestimmt das Verrechnungsband: 11–20, 21–50, 51–100, "
             "101–200 oder 201–500; ab 101 geht die Änderung in die Vier-Augen-Freigabe."),
        {"key": "menge", "type": "number", "label": "Erlaubte Subsites gesamt", "description": "10 bis 500 Subsites; 10 = Basis.",
         "validate": {"required": True, "min": 10, "max": 500}, "properties": NP},
        {"key": "begruendung", "type": "textarea", "label": "Begründung", "properties": NP},
    ]),
}

for fid, (name, comps) in forms.items():
    schema = {"type": "default", "id": fid, "components": comps}
    with open(f"{OUTDIR}/{fid}.form.json", "w") as f:
        json.dump(schema, f, ensure_ascii=False, indent=2)
        f.write("\n")
print("ok", list(forms))
