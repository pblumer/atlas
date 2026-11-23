# SPoP: Lebenszyklus von SharePoint-OnPrem-Sites und Zusatzleistungen

Proof of Concept für die Geschäftsfälle des Projekts Provisioning vNext (PvN), Bereich
SharePoint OnPrem, und das Leistungsangebot «Collaboration» (Materialien der Marktleistung
«ML Redesign SharePoint»). Abgebildet mit:

- zwei Lebenszyklus-Prozessen
- neun Katalogprodukten
- vier Entscheidungstabellen
- zwölf Formularen

## Produkte

| Produkt | Material | Prozess |
|---|---|---|
| `spop-site-hermes` (Project Site Hermes) | 540007512 | `proc_spop_teamsite` |
| `spop-site-communication` (Communication Site) | 540007514 | `proc_spop_teamsite` |
| `spop-site-publishing` (Publishing Site) | 540007515 | `proc_spop_teamsite` |
| `spop-site-teamsite-classic` (Team Site Classic) | 540007516 | `proc_spop_teamsite` |
| `spop-site-teamsite-modern` (Team Site Modern) | 540007560 | `proc_spop_teamsite` |
| `spop-site-wiki` (Wiki Site) | 540008180 | `proc_spop_teamsite` |
| `spop-speicher` (Speicherplatzerweiterung) | 540007510 | `proc_spop_zusatz` |
| `spop-reporting` (Erweitertes Reporting) | 540007511, 540007703, 540007702, 540007704 | `proc_spop_zusatz` |
| `spop-subsite-quota` (SubSite Quota) | 540007513, 540007705–540007708 | `proc_spop_zusatz` |

**Site-Typen:** Je Typ ein eigenes Produkt, damit Preis, Text und Materialnummer je Typ im
Katalog stehen können. Alle sechs Produkte binden denselben Prozess. Die Richtlinie
`SpopTeamsiteRichtlinie` leitet aus `itemId` Folgendes ab:

- den Site-Typ und die Vorlage
- die Features
- die Quota-Grenzen
- die Freigabepflicht
- die Materialnummer

**Stufenpakete:** Die Stufen von Reporting und SubSite Quota sind Verrechnungsbänder einer
Menge, keine eigenen Produkte. `SpopZusatzRichtlinie` leitet das Band und die Materialnummer
aus Produkt und Menge ab; eine Mengenänderung kann das Band wechseln.

**Zuordnung zur Site:** Die Zusatzprodukte beziehen sich auf eine Site, die eine andere
Position hält. Die bestellende Person nennt sie mit ihrer URL. Eine «requires»-Kante im
Katalog drückt das nicht aus: Sie verlangt jedes Zielprodukt, nicht eines von sechs.

## Geschäftsfälle im Site-Prozess

| Geschäftsfall | Inhalt | Im Prozess `proc_spop_teamsite` |
|---|---|---|
| PvN-GF-001 | Site bestellen | Bereitstellung (flach im Strang) |
| PvN-GF-002 | Quota-Erhöhung | Subprozess «Quota erhöhen», bis zum Maximum des Site-Typs |
| PvN-GF-003 | Externen Benutzer einladen | Subprozess «Externen Benutzer einladen» |
| Ergänzung | Rückgabe | Catch-Event, Boundary-Events, Nachrichtenstart als Rückfallebene |

## Modellierung

Katalogform `per-position`, nach dem Muster von `lc_proc_m365_funktionspostfach`: Jede
Bestellposition hat genau einen Strang.

1. Nach der Bereitstellung (Richtlinie, gegebenenfalls Vier-Augen-Freigabe, Ausführung,
   Verrechnung, Meldung «erledigt») wartet der Strang am ereignisbasierten Gateway
   «Aktiv: auf Auftrag warten».
2. Jede Änderungsaktion ist ein Subprozess, nach dem der Strang zum Warten zurückkehrt.
3. Die Rückgabe greift auch mitten in einem Subprozess (unterbrechendes Boundary-Event).
4. Alle späteren Aktionen sind an `orderId + "/" + positionId` korreliert.

Der Strang trägt seinen Stand:

| Prozess | Variablen |
|---|---|
| `proc_spop_teamsite` | `site`, `quota`, `externe` |
| `proc_spop_zusatz` | `bestand` |

Ergebnisse innerer Aufgaben bleiben im Subprozess (ADR-0074). Diese Variablen gelangen nur
über Output-Mappings am Subprozess in den Strang.

## Artefakte

| Datei | Zweck |
|---|---|
| `spop-teamsite.bpmn` | Prozess `proc_spop_teamsite` (sechs Site-Produkte) |
| `spop-zusatz.bpmn` | Prozess `proc_spop_zusatz` (drei Zusatzprodukte) |
| `SpopTeamsiteRichtlinie.dmn` | Site-Typ, Vorlage, Features, Quota, Freigabe, Material (anstelle von `amt_naming.rego`, `quota_limits.rego`, `approval.rego`) |
| `SpopQuotaRichtlinie.dmn` | Quota-Erhöhung innerhalb des Maximums des Site-Typs |
| `SpopExternRichtlinie.dmn` | Externe Benutzer: Mail-Domains, Gültigkeit, MFA, Vier-Augen-Prinzip |
| `SpopZusatzRichtlinie.dmn` | Zusatzprodukte: zulässige Menge, Verrechnungsband, Material |
| `spop-teamsite-bestellung.form.json` | Bestellung einer Site (alle sechs Typen) |
| `spop-quota-erhoehung.form.json`, `spop-extern-einladen.form.json`, `spop-teamsite-rueckgabe.form.json` | Aktionen der Site |
| `spop-speicher-*`, `spop-reporting-*`, `spop-subsite-*` (`-bestellung`, `-aendern`) | Bestellung und Mengenänderung der Zusatzprodukte |
| `spop-zusatz-rueckgabe.form.json` | Rückgabe einer Zusatzleistung |
| `spop-freigabe.form.json` | Vier-Augen-Freigabe (Gruppe `SPoP-Freigabe`) |
| `produkte.json` | die neun Katalogprodukte |
| `tools/` | Generatoren (`bpmnlib.py` mit den gemeinsamen Bausteinen) |

## Neu erzeugen

Die Modelle sind generiert. Änderungen gehören in die Generatoren, nicht in die erzeugten
Dateien. Aus `examples/spop` ausführen:

```bash
python3 tools/gen_bpmn.py spop-teamsite.bpmn
```

```bash
python3 tools/gen_bpmn_zusatz.py spop-zusatz.bpmn
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
Katalogprodukten sind. Die Schritte:

1. Eine Applikation anlegen, darin die zwölf Formulare speichern.
2. Die vier DMN-Modelle hochladen (Handles `spop-teamsite-richtlinie`,
   `spop-quota-richtlinie`, `spop-extern-richtlinie`, `spop-zusatz-richtlinie`) und als Regeln
   mit dem Namen der Datei registrieren.
3. Beide Prozesse als Entwürfe speichern und die Applikation deployen.
4. Die Produkte aus `produkte.json` mit einem Heimkatalog anlegen, in den Katalog aufnehmen
   und den Katalog veröffentlichen.
5. Die Rückmeldungen an den Katalog brauchen:
   - das Secret `atlas` mit einem API-Token der Rolle operator;
   - die Applikationskonstante `ATLAS_API_BASE`, falls der Server nicht unter
     `http://127.0.0.1:8080` lauscht.

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
- **Grenzen der Zusatzprodukte (Annahmen):**
  - Speicherplatzerweiterung 1 bis 500 GB, Freigabe über 100 GB
  - SubSite Quota bis 10 in der Basis enthalten, Freigabe ab 101
- **Abgrenzung Quota und Speicher:** Die Quota-Erhöhung der Site (GF-002) gilt bis zum Maximum
  des Site-Typs; darüber hinaus gilt die Speicherplatzerweiterung. Diese Abgrenzung ist eine
  Annahme.
- **Preise:** nur für Reporting bekannt (CHF 25.– pro Paket).
- **Widerspruch in der Quelle:** Der Katalogtext zu «SubSite Quota (11-20)» nennt «21 bis 50
  Subsites»; umgesetzt ist das Band 11–20.
- **Fehlende Materialien:** Die Basismaterialien 540007506–540007509 sind als Voraussetzung
  genannt, aber nicht beschrieben.
- **Simulierte Ausführung:** Die Schritte gegen APS-BW (`atlas-provider-apsbw`) und die
  Verrechnung sind mit `atlas:mockupConnector` simuliert.
- **Vier-Augen-Prinzip:** Getrennt wird nur über die Gruppe `SPoP-Freigabe`.
- **Eine Aktion zur Zeit:** Während ein Subprozess läuft, nimmt der Strang keine weitere
  Aktion an.
- **Personendaten:** `ownerUpn`, `memberUpns` und `email` sind nicht als Personendaten
  deklariert (`atlas:personal`).
