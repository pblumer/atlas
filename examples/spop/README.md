# SPoP: Lebenszyklus von SharePoint-OnPrem-Sites

Proof of Concept für die Geschäftsfälle des Projekts Provisioning vNext (PvN), Bereich
SharePoint OnPrem, und das Leistungsangebot «Collaboration» (Materialien der Marktleistung
«ML Redesign SharePoint»). Abgebildet mit:

- einem Lebenszyklus-Prozess (`proc_spop_teamsite`)
- sechs Katalogprodukten, einem je Site-Typ
- vier Entscheidungstabellen
- zehn Formularen

## Produkte

| Produkt | Material |
|---|---|
| `spop-site-hermes` (Project Site Hermes) | 540007512 |
| `spop-site-communication` (Communication Site) | 540007514 |
| `spop-site-publishing` (Publishing Site) | 540007515 |
| `spop-site-teamsite-classic` (Team Site Classic) | 540007516 |
| `spop-site-teamsite-modern` (Team Site Modern) | 540007560 |
| `spop-site-wiki` (Wiki Site) | 540008180 |

**Site-Typen:** Je Typ ein eigenes Produkt, damit Preis, Text und Materialnummer je Typ im
Katalog stehen können. Alle sechs Produkte binden denselben Prozess. Die Richtlinie
`SpopTeamsiteRichtlinie` leitet aus `itemId` Folgendes ab:

- den Site-Typ und die Vorlage
- die Features
- die Quota-Grenzen
- die Freigabepflicht
- die Materialnummer

**Leistungen an der Site:** Speicherplatzerweiterung, Erweitertes Reporting und SubSite Quota
gibt es nur zu einer bestehenden Site. Sie sind darum keine eigenen Produkte, sondern Aktionen
der Site-Position. Jede nennt die neue Gesamtmenge; `SpopZusatzRichtlinie` leitet daraus
Verrechnungsband und Materialnummer ab. Die Schlüssel `spop-speicher`, `spop-reporting` und
`spop-subsite-quota` sind dort nur Zeilenschlüssel, keine Katalogprodukte.

| Aktion | Menge | Material | Freigabe |
|---|---|---|---|
| «Speicherplatz erweitern» (`quota-erhoehen`) | 1–500 GB Zusatzspeicher | 540007510, pro GB | über 100 GB |
| «Erweitertes Reporting» (`reporting-aendern`) | 0–20 Pakete, 0 beendet | 540007511 (1–5), 540007703 (6–10), 540007702 (11–15), 540007704 (16–20) | keine |
| «SubSite Quota» (`subsites-aendern`) | 10–500 Subsites, 10 = Basis | 540007513 (11–20), 540007705 (21–50), 540007706 (51–100), 540007707 (101–200), 540007708 (201–500) | ab 101 |

Ergebnisse der Speicherplatzerweiterung im Live-Test (Team Site Modern, 5120 MB inbegriffen):

| Anfrage (Gesamtmenge) | Ergebnis |
|---|---|
| 50 GB | ohne Freigabe; Quota 56 320 MB; Shop meldet «completed» |
| 150 GB | Vier-Augen-Freigabe; danach Quota 158 720 MB; Shop meldet «completed» |
| erneut 150 GB | abgelehnt, weil unverändert; Shop meldet «rejected» |

Weil die Leistungen zur Site gehören, braucht es keine Eigentumsprüfung über die
Instanzsuche, und die Rückgabe der Site beendet ihre Verrechnung mit. Frühere Stände führten
Reporting und SubSite Quota als eigene Produkte mit eigenem Prozess `proc_spop_zusatz`.

## Geschäftsfälle im Site-Prozess

| Geschäftsfall | Inhalt | Im Prozess `proc_spop_teamsite` |
|---|---|---|
| PvN-GF-001 | Site bestellen | Bereitstellung (flach im Strang) |
| PvN-GF-002 | Quota-Erhöhung | «Speicherplatz erweitern» (Zusatzspeicher in GB, Material 540007510) |
| PvN-GF-003 | Externen Benutzer einladen | «Externen Benutzer einladen» |
| Collaboration | Zusatzleistungen | «Erweitertes Reporting», «SubSite Quota» |
| Ergänzung | Angaben der Site ändern | «Site umbenennen», «Site-URL ändern», «Berechtigungen ändern» |
| Ergänzung | Rückgabe | unterbrechender Ereignis-Subprozess «Rückgabe», Nachrichtenstart als Rückfallebene |

## Modellierung

Katalogform `per-position`, nach dem Muster von `lc_proc_m365_funktionspostfach`: Jede
Bestellposition hat genau einen Strang.

1. Nach der Bereitstellung (Richtlinie, gegebenenfalls Vier-Augen-Freigabe, Ausführung,
   Verrechnung, Meldung «erledigt») wartet der Strang am ereignisbasierten Gateway
   «Aktiv: auf Auftrag warten».
2. Jede Aktion ist ein Zwischenereignis an diesem Gateway, gefolgt von ihrem Subprozess. Alle
   Subprozesse haben denselben Aufbau: lesen, Regel, zulässig?, Vier-Augen-Freigabe, ausführen,
   Shop-Aufgabe; eine Ablehnung endet im Subprozess mit einer Shop-Aufgabe «rejected».
3. Die Einladung liegt oberhalb des Gateways und kehrt über die Oberkante zum Warten zurück.
   Die übrigen sieben Aktionen stehen untereinander; jeder Subprozess mündet rechts in den
   gemeinsamen Rückweg `j_zurueck`, der unter allen hindurch zum Warten führt. Nur so kreuzt
   kein Rückweg die Verzweigung des Gateways.
4. Die Rückgabe ist ein unterbrechender Ereignis-Subprozess (ADR-0082). Sie unterbricht den
   Strang, wo immer er steht, auch während einer wartenden Freigabe, und braucht darum weder
   ein Catch-Event am Gateway noch Boundary-Events an den Subprozessen. Ohne laufenden Strang
   startet der Nachrichtenstart «Rückgabe ohne laufenden Strang».
5. Alle späteren Aktionen sind an `orderId + "/" + positionId` korreliert. Während eine Aktion
   läuft, wartet der Strang nicht; eine zweite Aktion derselben Position wird erst nach
   Abschluss der ersten angenommen.

Der Strang trägt den Stand der Site in `site`, `quota`, `zusatz`, `gruppen`, `externe` und
`verrechnung`. Ergebnisse innerer Aufgaben bleiben im Subprozess (ADR-0074). Jede Ausführung
schreibt nur ihre geänderten Felder in die lokale Variable `neu`; das Output-Mapping am
Subprozess setzt sie beim Abschluss mit `context merge` in den Stand des Strangs ein. Endet
ein Subprozess mit einer Ablehnung, ist `neu` leer und der Stand bleibt.

## Angaben der Site ändern

Im Shop korrigiert «Angaben ändern» bei einer bereitgestellten Site nur den Datensatz der
Bestellung (ADR-0359): Die Korrektur wird festgehalten, erreicht aber weder den laufenden
Strang noch SharePoint (ADR-0441). Darum hat jedes Feld des Bestellformulars einen
prozessualen Weg, und das Formular sagt das in einem Hinweis:

| Feld | Weg nach der Bereitstellung | Freigabe |
|---|---|---|
| `titel` | Aktion «Site umbenennen» (`site-umbenennen`) | keine |
| `amtKuerzel`, `kurzname` | Aktion «Site-URL ändern» (`site-url-aendern`); führt die Verrechnung nach | immer |
| `ownerUpn`, `memberUpns` | Aktion «Berechtigungen ändern» (`berechtigungen-aendern`), vollständiger neuer Stand | bei Owner-Wechsel für Hermes, Communication, Publishing |
| `quotaWahl`, `quotaMb`, `quotaBegruendung` | Aktion «Speicherplatz erweitern» (`quota-erhoehen`) | über 100 GB |
| `sprache` | keiner: SharePoint legt die Standardsprache bei der Erstellung fest | – |
| `bemerkung` | keiner nötig: reine Angabe ohne Wirkung auf die Site | – |

Geprüft werden die drei Änderungen mit `SpopAenderungRichtlinie`. Ihre Formularfelder heissen
anders als die der Bestellung (`neuerTitel`, `neuesAmtKuerzel`, `neuerKurzname`,
`neuerOwnerUpn`, `neueMemberUpns`), damit die ursprünglichen Antworten nicht überschrieben
werden. Die Bestellung zeigt weiterhin die ursprünglichen Antworten; was die Site heute ist,
steht im Strang (`site`, `gruppen`).

## Artefakte

| Datei | Zweck |
|---|---|
| `spop-teamsite.bpmn` | Prozess `proc_spop_teamsite` (sechs Site-Produkte) |
| `SpopTeamsiteRichtlinie.dmn` | Site-Typ, Vorlage, Features, Quota, Freigabe, Material (anstelle von `amt_naming.rego`, `quota_limits.rego`, `approval.rego`) |
| `SpopExternRichtlinie.dmn` | Externe Benutzer: Mail-Domains, Gültigkeit, MFA, Vier-Augen-Prinzip |
| `SpopZusatzRichtlinie.dmn` | Speicher, Reporting, SubSites: zulässige Menge, Verrechnungsband, Material |
| `SpopAenderungRichtlinie.dmn` | Titel, URL und Berechtigungen einer Site ändern: gültig, unverändert, Freigabe |
| `spop-teamsite-bestellung.form.json` | Bestellung einer Site (alle sechs Typen) |
| `spop-quota-erhoehung.form.json`, `spop-reporting.form.json`, `spop-subsites.form.json` | Leistungen an der Site |
| `spop-extern-einladen.form.json` | Externen Benutzer einladen |
| `spop-site-umbenennen.form.json`, `spop-site-url-aendern.form.json`, `spop-berechtigungen-aendern.form.json` | Angaben der Site ändern |
| `spop-teamsite-rueckgabe.form.json` | Rückgabe der Site |
| `spop-freigabe.form.json` | Vier-Augen-Freigabe (Gruppe `SPoP-Freigabe`) |
| `produkte.json` | die sechs aktiven und die zurückgezogenen Katalogprodukte |
| `tools/` | Generatoren (`bpmnlib.py` mit den gemeinsamen Bausteinen) |

## Neu erzeugen

Die Modelle sind generiert. Änderungen gehören in die Generatoren, nicht in die erzeugten
Dateien. Aus `examples/spop` ausführen:

```bash
python3 tools/gen_bpmn.py spop-teamsite.bpmn
```

```bash
python3 tools/gen_dmn.py .
```

```bash
python3 tools/gen_forms.py .
```

Danach `go test ./examples -update` (Katalog des Handbuchs) und
`go test ./examples/... ./api/layout/...` laufen lassen.

## Installation

Das Beispiel wird nicht aus dem Handbuch installiert, weil seine Starts die Aktionen von
Katalogprodukten sind. Es braucht Atlas 0.28 oder neuer: Ältere Versionen lehnen beim
Veröffentlichen eine Rückgabe ab, die ein Ereignis-Subprozess entgegennimmt. Die Schritte:

1. Eine Applikation anlegen, darin die zehn Formulare speichern.
2. Die vier DMN-Modelle hochladen (Handles `spop-teamsite-richtlinie`,
   `spop-extern-richtlinie`, `spop-zusatz-richtlinie`, `spop-aenderung-richtlinie`) und als Regeln
   mit dem Namen der Datei registrieren.
3. Den Prozess als Entwurf speichern und die Applikation deployen.
4. Die aktiven Produkte aus `produkte.json` mit einem Heimkatalog anlegen, in den Katalog
   aufnehmen und den Katalog veröffentlichen.
5. Die Rückmeldungen an den Katalog brauchen:
   - das Secret `atlas` mit einem API-Token der Rolle operator;
   - die Applikationskonstante `ATLAS_API_BASE`, falls der Server nicht unter
     `http://127.0.0.1:8080` lauscht.

Eine Installation aus einem früheren Stand bereinigt man so:

- Regel und Modell `SpopQuotaRichtlinie` (`spop-quota-richtlinie`) in der Console löschen; das
  MCP-Werkzeug kann Regeln nur registrieren, nicht löschen. Danach neu deployen.
- Die früheren Produkte `spop-speicher`, `spop-reporting` und `spop-subsite-quota` zurückziehen
  (`state: withdrawn`) und aus dem Katalog nehmen; ihre Leistungen sind Aktionen der Site.
- Den Entwurf `proc_spop_zusatz` löschen und seine deployten Versionen entfernen, sobald keine
  Zusatzleistung mehr darauf läuft.

Auf einen anderen Server überträgt `atlas export --application … --catalog …` zusammen mit
`atlas import` das Ganze als Paket (ADR-0467).

## Annahmen und offene Punkte

- **Quelle der Katalogtexte:** Die Texte der Site-Typen stützen sich nach eigenem Vermerk auf
  kein Factsheet. Daraus abgeleitet und damit Annahmen sind:
  - Vorlage je Typ (STS#0, STS#3, SITEPAGEPUBLISHING#0, BLANKINTERNET#0, ENTERWIKI#0; Hermes
    als STS#3 mit HERMES-Struktur)
  - Features je Typ
  - Quota je Typ: Standard 5120 MB, Maximum 10240 MB für Team Sites und Wiki; Standard
    10240 MB, Maximum 51200 MB für Hermes, Communication und Publishing
  - Freigabepflicht: Hermes, Communication und Publishing immer
- **Grenzen der Aktionen (Annahmen):**
  - Speicherplatzerweiterung 1 bis 500 GB, Freigabe über 100 GB. Die Grenze gilt für die
    Gesamtmenge, nicht für die einzelne Erhöhung: 90 → 110 GB braucht eine Freigabe, zwei
    Schritte zu je 50 GB bis 100 GB nicht.
  - Verrechnet wird die Gesamtmenge; ob APS-BW Gesamtmenge oder Differenz erwartet, ist nicht
    geklärt.
  - Erweitertes Reporting 0 bis 20 Pakete ohne Freigabe; SubSite Quota bis 10 in der Basis
    enthalten, Freigabe ab 101.
  - Titeländerung ohne Freigabe, neue URL immer mit Freigabe, Owner-Wechsel mit Freigabe bei
    Hermes, Communication und Publishing.
- **Preise:** nur für Reporting bekannt (CHF 25.– pro Paket). Weil die Zusatzleistungen keine
  eigenen Produkte mehr sind, steht der Preis nur noch im Formular der Aktion, nicht im Katalog.
- **Widerspruch in der Quelle:** Der Katalogtext zu «SubSite Quota (11-20)» nennt «21 bis 50
  Subsites»; umgesetzt ist das Band 11–20.
- **Fehlende Materialien:** Die Basismaterialien 540007506–540007509 sind als Voraussetzung
  genannt, aber nicht beschrieben.
- **Simulierte Ausführung:** Die Schritte gegen APS-BW (`atlas-provider-apsbw`) und die
  Verrechnung sind mit `atlas:mockupConnector` simuliert.
- **Vier-Augen-Prinzip:** Getrennt wird nur über die Gruppe `SPoP-Freigabe`.
- **Rückgabe während einer Änderung:** Die Rückgabe unterbricht auch eine Änderung, die auf
  ihre Freigabe wartet (im Live-Test geprüft). Die unterbrochene Aktion erhält im Shop aber
  keine Rückmeldung «rejected» oder «failed»; sie bleibt ohne Ergebnis. Eine Shop-Aufgabe im
  Rückgabe-Subprozess könnte das nachholen, müsste dafür aber wissen, welche Aktion lief.
- **Rückgabe ohne laufenden Strang:** Die Rückfallebene bildet die Site-URL aus den Antworten
  der Bestellung. Eine spätere URL-Änderung kennt sie nicht.
- **URL-Änderung (offen):** Ob und wie SharePoint OnPrem die URL einer Site-Collection
  umbenennt und was mit Links, Gruppennamen und Suchindex geschieht, ist nicht geklärt; der
  Schritt ist simuliert. Die Gruppennamen behalten die alte URL.
- **Personendaten:** `ownerUpn`, `memberUpns`, `neuerOwnerUpn`, `neueMemberUpns` und `email`
  sind nicht als Personendaten deklariert (`atlas:personal`).
- **Entscheidungsversionen:** Seit ADR-0423 wertet eine Regelaufgabe mit Bindung «latest» die
  neueste Entscheidungsversion aus, wenn sie läuft, sonst die mit dem Prozess gebündelte Kopie.
  Die Tabellen rufen die Prozesse über den FEEL-Namen (`SpopTeamsiteRichtlinie`) auf; Atlas
  zählt Versionen aber unter der Beschriftung («Site-Richtlinie (GF-001)»). Eine geänderte
  Beschriftung beginnt eine neue Zählung, und «latest» springt trotzdem auf die neueste
  Version. Seit Atlas 0.28 verhindert die Löschprüfung, dass die letzte Version einer solchen
  Entscheidung gelöscht wird, solange ein Prozess sie ohne gebündelte Kopie braucht.
