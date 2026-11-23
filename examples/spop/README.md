# SPoP: Lebenszyklus von SharePoint-OnPrem-Sites und Zusatzleistungen

Proof of Concept für die Geschäftsfälle des Projekts Provisioning vNext (PvN), Bereich
SharePoint OnPrem, und das Leistungsangebot «Collaboration» (Materialien der Marktleistung
«ML Redesign SharePoint»). Abgebildet mit:

- zwei Lebenszyklus-Prozessen
- acht Katalogprodukten
- drei Entscheidungstabellen
- zehn Formularen

## Produkte

| Produkt | Material | Prozess |
|---|---|---|
| `spop-site-hermes` (Project Site Hermes) | 540007512 | `proc_spop_teamsite` |
| `spop-site-communication` (Communication Site) | 540007514 | `proc_spop_teamsite` |
| `spop-site-publishing` (Publishing Site) | 540007515 | `proc_spop_teamsite` |
| `spop-site-teamsite-classic` (Team Site Classic) | 540007516 | `proc_spop_teamsite` |
| `spop-site-teamsite-modern` (Team Site Modern) | 540007560 | `proc_spop_teamsite` |
| `spop-site-wiki` (Wiki Site) | 540008180 | `proc_spop_teamsite` |
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

**Speicherplatzerweiterung (540007510):** keine eigene Bestellung, sondern die Aktion
«Speicherplatz erweitern» an jeder Site (GF-002). Sie nennt die Gesamtmenge an Zusatzspeicher in
GB (1–500, Freigabe über 100 GB); die gesetzte Quota ist die inbegriffene Quota des Site-Typs plus
Zusatzspeicher × 1024 MB. Geprüft wird mit `SpopZusatzRichtlinie` (Produkt `spop-speicher`).

**Stufenpakete:** Die Stufen von Reporting und SubSite Quota sind Verrechnungsbänder einer
Menge, keine eigenen Produkte. `SpopZusatzRichtlinie` leitet das Band und die Materialnummer
aus Produkt und Menge ab; eine Mengenänderung kann das Band wechseln.

**Zuordnung zur Site:** Die Zusatzprodukte beziehen sich auf eine Site, die eine andere
Position hält. Die bestellende Person nennt sie mit ihrer URL. Eine «requires»-Kante im
Katalog drückt das nicht aus: Sie verlangt jedes Zielprodukt, nicht eines von sechs.

**Eigentumsprüfung:** Der Zusatzprozess nimmt nur eine Site an, die der bestellenden Person
gehört. «Gehört» heisst: Die Person ist Besteller (`orderer`) oder Empfänger (`recipient`)
der Site-Position.

| Schritt | Wo | Was |
|---|---|---|
| 1 | Site-Strang, «Zugriff registrieren» | setzt `siteZugriff = <URL>\|<Besteller>\|<Empfänger>`, als `atlas:searchable` deklariert |
| 2 | Zusatz-Strang, «Site-Prozessversionen lesen» | `GET /api/v1/processes`, Versionen von `proc_spop_teamsite` |
| 3 | Zusatz-Strang, «Site im Bestand suchen» | je Version `GET /api/v1/instances/search?process=<Version>&q=siteZugriff=<URL>\|*` (parallele Mehrfach-Instanz) |
| 4 | Zusatz-Strang, «Eigentum prüfen» | nur aktive Stränge zählen; `gefunden`, `gehoert` |
| 5 | `SpopZusatzRichtlinie` | lehnt ab, wenn die Site nicht gefunden wird oder nicht der Person gehört |

Gründe für diesen Weg:

- Der Atlas-Bestand führt keine Site-URL.
- Fremde Bestände darf nur die Admin-Rolle lesen, die ein API-Token nie hat.
- Eine Instanzsuche ohne `?process=` durchsucht alle Instanzen des Servers und lief auf einer
  Installation mit rund 50'000 aktiven Instanzen in ein Timeout.
- Eine Inhaltssuche liefert je Treffer nur die passende Variable; darum stehen URL, Besteller
  und Empfänger in einer Variablen.

## Geschäftsfälle im Site-Prozess

| Geschäftsfall | Inhalt | Im Prozess `proc_spop_teamsite` |
|---|---|---|
| PvN-GF-001 | Site bestellen | Bereitstellung (flach im Strang) |
| PvN-GF-002 | Quota-Erhöhung | Subprozess «Speicherplatz erweitern» (Zusatzspeicher in GB, Material 540007510) |
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
| `spop-zusatz.bpmn` | Prozess `proc_spop_zusatz` (zwei Zusatzprodukte) |
| `SpopTeamsiteRichtlinie.dmn` | Site-Typ, Vorlage, Features, Quota, Freigabe, Material (anstelle von `amt_naming.rego`, `quota_limits.rego`, `approval.rego`) |
| `SpopExternRichtlinie.dmn` | Externe Benutzer: Mail-Domains, Gültigkeit, MFA, Vier-Augen-Prinzip |
| `SpopZusatzRichtlinie.dmn` | Speicherplatzerweiterung und Zusatzprodukte: zulässige Menge, Verrechnungsband, Material |
| `spop-teamsite-bestellung.form.json` | Bestellung einer Site (alle sechs Typen) |
| `spop-quota-erhoehung.form.json`, `spop-extern-einladen.form.json`, `spop-teamsite-rueckgabe.form.json` | Aktionen der Site |
| `spop-reporting-*`, `spop-subsite-*` (`-bestellung`, `-aendern`) | Bestellung und Mengenänderung der Zusatzprodukte |
| `spop-zusatz-rueckgabe.form.json` | Rückgabe einer Zusatzleistung |
| `spop-freigabe.form.json` | Vier-Augen-Freigabe (Gruppe `SPoP-Freigabe`) |
| `produkte.json` | die acht Katalogprodukte |
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

1. Eine Applikation anlegen, darin die zehn Formulare speichern.
2. Die drei DMN-Modelle hochladen (Handles `spop-teamsite-richtlinie`,
   `spop-extern-richtlinie`, `spop-zusatz-richtlinie`) und als Regeln
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
- **Grenzen der Zusatzleistungen (Annahmen):**
  - Speicherplatzerweiterung 1 bis 500 GB, Freigabe über 100 GB
  - SubSite Quota bis 10 in der Basis enthalten, Freigabe ab 101
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
- **Owner aus dem Formular (offen):** `ownerUpn` zählt bei der Eigentumsprüfung noch nicht.
  Eine Bestellung übergibt ihren Prozessen nur die Atlas-Kennung der Personen (`orderer`,
  `recipient`), nicht ihre E-Mail oder UPN, und Benutzerkonten darf nur die Admin-Rolle
  lesen. Ein Abgleich braucht eine Erweiterung von Atlas, etwa `ordererEmail` als
  Startvariable einer Position.
- **Sites ohne `siteZugriff`:** Sites, die vor der Eigentumsprüfung bereitgestellt wurden,
  tragen die Variable nicht und gelten für Zusatzprodukte als nicht gefunden.
- **Grösse der Prozessliste:** «Site-Prozessversionen lesen» legt die ganze Antwort von
  `GET /api/v1/processes` im Strang ab (auf der Testinstallation rund 160 KB). Ein
  Folgeschritt, der nur die Schlüssel behält, oder eine Abfrage nach Prozess-ID wäre
  sparsamer.
