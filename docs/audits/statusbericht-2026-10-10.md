# Statusbericht: Entwicklungsstränge, Roadmap, ADRs und Issues

**Repository:** `pblumer/atlas`
**Geprüfter Stand:** `main` bei `517135c` (Release 0.9.1 vom 2026-10-09)
**Datum:** 2026-10-10
**Umfang:** `ROADMAP.md` (alle zwölf Meilensteine), alle 448 nummerierten ADRs in
`docs/adr/`, alle 65 offenen Issues und die drei offenen Pull Requests, dazu die früheren
Audits in diesem Verzeichnis und die Pläne unter `docs/planning/`.

Der Bericht beantwortet drei Fragen:

1. Wo steht jeder Entwicklungsstrang, und wo soll die Arbeit weitergehen?
2. Ist das, was in Roadmap und ADRs als umgesetzt markiert ist, tatsächlich gebaut?
3. Was steckt in den offenen Issues noch drin?

**Schweregrade in diesem Bericht.** *Hoch:* Die Kernaussage eines Records oder Punkts ist
nicht gebaut oder nicht verdrahtet, oder ein Befund betrifft Korrektheit oder Sicherheit.
*Mittel:* Ein Teil fehlt, oder ein zugesagter Guard oder Test fehlt. *Niedrig:* Der Text ist
veraltet; der Code ist in Ordnung.

---

## 1. Kurzfassung

1. **Der Code ist der Dokumentation in beide Richtungen davongelaufen.** Von 396 ADRs mit
   `Implementation: Landed` halten 332 der Stichprobe ohne Befund stand. 64 weichen ab,
   davon **sechs mit hohem Schweregrad**: Die Kernaussage ist nicht gebaut oder nicht
   verdrahtet (ADR-0089, 0117, 0134, 0207, 0216), oder ein gelandeter Record wurde durch
   eine spätere Änderung wieder geöffnet (ADR-0275). Umgekehrt sind 13 von 50 als `Partial`
   oder `Not started` markierten Records weiter, als sie angeben.
2. **Die Roadmap ist als Planungsinstrument derzeit nicht verlässlich.** Vier von zwölf
   Meilenstein-Köpfen stimmen nicht (M2, M4, M6, A). Rund 29 als offen oder in Arbeit
   markierte Punkte sind ganz oder weitgehend umgesetzt. Rund 30 gelandete Fähigkeiten
   fehlen in der Roadmap ganz. Da `AGENTS.md` jeden Agenten anweist, eine Aufgabe zuerst in
   der Roadmap zu verorten, ist das kein kosmetisches Problem.
3. **Sechs Korrektheits- und Sicherheitsbefunde verdienen Vorrang vor neuen Funktionen.**
   Fünf sind am Code nachgeprüft:
   - #1013: Ein Kompensationswurf schliesst die Instanz zu früh ab; reproduziert.
   - Neu, ohne Issue: `GET /api/v1/tasks?content=1` gibt Variablenwerte an Personen
     heraus, denen der Variablen-Endpoint sie verweigert.
   - Ein Worker kann keinen BPMN-Fehler werfen; ADR-0089 behauptet das Gegenteil.
   - Der öffentliche Formularstart prüft die Variablen nicht gegen das Formular (ADR-0029).
   - #468: OAuth2-Felder des REST-Workers gehen im Modeler stillschweigend verloren.

   Der sechste betrifft die Dokumentation: Der Standardmodus `--script-sandbox=off` (#894)
   ist nirgends als Risiko beschrieben.
4. **25 der 65 offenen Issues (38 %) können geschlossen oder in ein ADR überführt werden.**
   Ihr Inhalt ist gebaut, entschieden oder ein Platzhalter. Der Issue-Tracker ist ein
   Nebenkanal: 65 offenen stehen 37 geschlossene Issues gegenüber, aber mehr als 1'200
   Pull Requests.
5. **Der Umsetzungsplan vom 7. September (F01–F17) ist weitgehend abgearbeitet.** 13 von 17
   Befunden sind geschlossen, vier nur teilweise: F03 (WAL-Tailer), F05 (Archiv-Manifest
   und End-to-End-Restore), F11 (durch das `?content=1`-Leck wieder geöffnet) und F15 (ein
   Scan im Workers-View).
6. **Wo weiterarbeiten** (Details in Abschnitt 8):
   - Stufe 0: die Korrektheits- und Sicherheitsliste.
   - Stufe 1: je ein Bereinigungs-PR für Roadmap, ADR-Status und Issues.
   - Stufe 2: vier Produktstränge mit eigenem Nutzen und ohne offene Grundsatzfrage:
     Nachrichtenpuffer (ADR-0370), Console-Oberfläche der Business-Architektur (B8),
     Element-Template-Applier (ADR-0212), Messgate für die Katalog-Lebenszyklen.
   - Stufe 3: Scale-out, Föderation, gRPC und Hosted Apps brauchen zuerst Entscheidungen,
     nicht Code.

---

## 2. Methode und Grenzen

Die Prüfung lief in neun parallelen, ausschliesslich lesenden Durchgängen: fünf
ADR-Bereiche (0001–0100, 0101–0200, 0201–0295, 0296–0372, 0373–0448), zwei
Roadmap-Hälften und zwei Issue-Gruppen (Fehler, Betrieb und PRs; Funktionen, Konzepte und
Worker-Wünsche).

Jeder als `Landed` markierte Record wurde an zwei bis fünf konkreten Aussagen geprüft:
Routen in `api/openapi.go`, Go-Bezeichner, `applyToState`-Zweige, Compiler-Pfade,
UI-Aufrufe, benannte Tests und Guards. Ein Kommentar, der eine ADR-Nummer zitiert, galt
nicht als Nachweis. Die früheren Audits wurden gelesen, ihre Befunde aber neu geprüft.

**Alle Befunde mit hohem Schweregrad und alle Punkte der Stufe 0 habe ich selbst am Code
nachgeprüft.** #1013 wurde in einer separaten Kopie des Repositorys reproduziert, nicht im
Arbeitsverzeichnis.

**Was dieser Bericht nicht leistet:**

- **Keine Laufzeitprüfung.** Ausser `go test ./docs/adr`, `go build ./...` und einzelnen
  Paket-Tests (`limits`, `wal`, `eventcatalog`, `internal/trustedproxy`,
  `api/releasenotes`) lief keine Testsuite, kein Browser und kein e2e-Test.
- **«Ohne Befund» heisst: Die genannten Bezeichner existieren und sind verdrahtet.** Es
  heisst nicht, dass das Verhalten in allen Fällen der Entscheidung entspricht.
  Fundamentale Records (0003–0013) sowie reine UI- und Prozess-Records wurden nur leicht
  geprüft.
- **Aufwand und Nutzen sind Schätzungen aus dem Code**, nicht aus Nutzungs- oder
  Kundendaten. Grössen: S (Stunden bis ein Tag), M (Tage), L (ein bis zwei Wochen),
  XL (mehr, oder mit vorgelagerter Grundsatzentscheidung).
- **Der Klon ist flach** (Historie ab 2026-09-23). Ältere Änderungen wurden über Code,
  `CHANGELOG.md` und ADRs nachvollzogen, nicht über `git log`.

---

## 3. Ausgangslage in Zahlen

| Grösse | Wert |
|---|---:|
| Nummerierte ADRs | 448 |
| davon `Landed` / `Partial` / `Not started` / `Superseded` | 396 / 22 / 28 / 2 |
| ADRs seit der Status-Abstimmung vom 2026-09-09 (ab ADR-0296) | 153 |
| ADRs mit `Open question` (fast alle mit Prüfmonat 2026-09) | 77 |
| Releases | 10 (0.1.0 am 2026-08-11 bis 0.9.1 am 2026-10-09) |
| Go-Produktivcode / Go-Testcode | ca. 202'000 / 329'000 Zeilen |
| Reservierte Job-Typen (`api/releasedkinds.go`) | 33, davon 25 Integrationsarten |
| Integrationsarten mit Repository-Paket / ohne (ADR-0167) | 9 / 16 |
| Worker-Type-Pakete unter `connector/` | 20 |
| MCP-Werkzeuge (laut `README.md`) | 137 |
| Methoden auf `*Server` in `api/` | 738 |
| Offene / geschlossene Issues | 65 / 37 |
| Offene Pull Requests | 3 |

Drei Kennzahlen sind für die Planung relevant:

- **Die 77 offenen Fragen laufen gleichzeitig ab.** Fast alle tragen den Prüfmonat 2026-09,
  und `go test ./docs/adr` verlangt die Prüfung nach einem Jahr. Im September 2027 werden
  sie gemeinsam fällig. Wer sie über das Jahr verteilt prüft, vermeidet einen Stau.
- **`*Server` wächst gegen die eigene Entscheidung.** ADR-0147 wollte die Zahl der
  `*Server`-Methoden senken. Sie ist von 286 auf 738 gestiegen.
- **16 Integrationsarten haben kein Repository-Paket.** `packagesOwed = 16` ist im Guard
  festgeschrieben und darf nur sinken.

---

## 4. Entwicklungsstränge

| Strang | Roadmap | Stand | Wichtigste offene Punkte |
|---|---|---|---|
| **Engine-Kern und BPMN-Semantik** | M1–M3 | Reif. Breite BPMN-Abdeckung: Ereignisse, Subprozesse, Kompensation, Transaktionen, Ad-hoc, Multi-Instance, Lanes. | #1013 (Kompensation); Worker-geworfene BPMN-Fehler (ADR-0089); paralleles Event-Gateway (#804); Nachrichtenpuffer (ADR-0370); Korrelationsschlüssel beim Message-Start; zyklische Joins; sequenzielles Ad-hoc |
| **Robustheit und Betrieb** | M4 | Grösstenteils erledigt. F01–F17 zu 13 von 17 geschlossen. Metriken, Traces, Incidents, Backup und Snapshots vorhanden. | WAL-Tailer (F03); Archiv-Manifest und Restore in ein leeres Verzeichnis (F05); gRPC-Entscheid; Go-SDK paketieren; Deploy als Event (ADR-0019) |
| **Skalierung** | M5 | Nicht begonnen. Der Server läuft fest mit einer Partition (`engine.New(1, …)` in `cmd/atlas/main.go`). | Grundsatzentscheid zwischen Partitionszellen (ADR-0175) und Föderation (ADR-0369–0374) |
| **Worker Types und Integration** | M1, M4 | 25 Integrationsarten. Auslagerung auf Worker-Prozesse ist Standard; Circuit-Breaker und parallele Ausführung sind gebaut. | Paketierung (ADR-0207/0208 überbewertet); 16 fehlende Repository-Pakete (ADR-0167); Template-Applier (ADR-0212); Oracle (#444); Härtung der Script-Sandbox (#893–#898) |
| **Sicherheit und Identität** | S | Stark ausgebaut: Authentisierung standardmässig an, API-Tokens, OIDC, Rollen je Endpunktgruppe, Audit-Log, Drosselung, Trusted Proxies. | `?content=1`-Leck; #1012 (Instanz-Lesepfade nur rollenbasiert); Schema-Prüfung beim öffentlichen Start; #1042; #894; einheitliche Sperrung von Sitzungen und Tokens (ADR-0281); RP-initiierter Logout (ADR-0210) |
| **Modeler und Authoring** | A, S | Weit fortgeschritten: Playground, Problems-Panel, Formulare, Tasks-App, Co-Editing, DMN-Editor als Seite, FEEL-Assistent, Formulargenerierung. | Template-Applier (ADR-0212/0027); Versionshistorie (ADR-0031); Copilot-Panel (ADR-0032); Execution Listeners; MCP-Validierungswerkzeug |
| **DMN und Entscheidungen** | M1 | Weitgehend erledigt: Decision Deployments, Drafts, Decision Services, Typtreue, Versionsbindung. | Promotion trägt keine Entscheidungen mit (ADR-0319); doppelte Decision-ID in zwei Modellen (ADR-0321); Prozessdokument zeigt die neueste statt der gebundenen Version (ADR-0423) |
| **Business-Architektur** | B | B0–B7 und B10 gebaut; HTTP und MCP vollständig. | B8 (Console-Oberfläche fehlt ganz); B9 (Dokumentaustausch); modellseitige Deklaration `atlas:capability` |
| **Katalog, Shop und Portal** | K | Hauptarbeitsstrang der letzten Wochen. Alle Slices von ADR-0429 (Produktaktionen) sind gelandet, dazu Event-Feed (Pull und Push), Abschaltbarkeit und Dokumentimport. | Console-Bestellansicht und Operator-Aktionen; ausgesetzter Berechtigungszustand; Messgate für Lebenszyklen pro Position; Sprache am Konto (ADR-0313); TMF620 (ADR-0387); Katalogexport |
| **Panorama und Estate-Graph** | P | Meilenstein P erledigt. Estate-Höhe und Run-Graph teilweise gebaut. | `rungraph/` wird von nichts importiert; ADR-0420 ist `Proposed`; die Estate-Sicht ist nicht in den Starmap integriert |
| **Föderation zwischen Instanzen** | M5 (#986) | Nur Entscheidungen, kein Code. | ADR-0370 als erster Slice; danach ADR-0369, 0372 und 0373 |
| **Engineering-Prozess** | – | Strenge Guards: Coverage-Boden 95 %, ADR-Konventionen, Drift-Tests. | CI-Dauer des `api`-Pakets (#1001, #1023); ADR- und Roadmap-Drift; die in ADR-0298 verlangte periodische Nachprüfung hat keinen Mechanismus |

---

## 5. Roadmap-Abgleich

### 5.1 Meilenstein-Köpfe

| Meilenstein | Markiert | Tatsächlich | Bemerkung |
|---|---|---|---|
| M0 Foundations | erledigt | erledigt | Zeilen 19, 21 und 22 sagen noch «still to come» für Validierung, Leases und XML-Deploy; alle drei sind gelandet. |
| M1 Core BPMN | in Arbeit | in Arbeit | Vier der sieben offenen Punkte sind erledigt. |
| M2 Events and timers | in Arbeit | **faktisch erledigt** | Alle Punkte sind erledigt. Die echten Restpunkte (Nachrichtenpuffer, Korrelationsschlüssel beim Message-Start) stehen nur im Fliesstext. |
| M3 Structure | erledigt | erledigt | – |
| M4 Operability | nicht begonnen | **in Arbeit, grösstenteils erledigt** | Offen: gRPC, Go-SDK, Deploy als Event. |
| M5 Scale-out | nicht begonnen | nicht begonnen | Korrekt. |
| M6 Ecosystem | nicht begonnen | **in Arbeit** | Benchmarks, Interop (Zeebe-Dialekt, MIM-Import) und Handbücher sind vorhanden. |
| S Server und Web UI | in Arbeit | in Arbeit | Markierung des Eigenschaftspanels und Text zur Benutzerverwaltung veraltet. |
| P Panorama | erledigt | erledigt | Die Folgearbeit (Estate, Run-Graph, ADR-0420) fehlt in der Roadmap. |
| A Modeler | nicht begonnen | **in Arbeit** | Rund 20 Punkte erledigt, rund 11 fälschlich als offen markiert. Die Einleitung «ADRs Proposed» ist veraltet. |
| B Business-Architektur | in Arbeit | in Arbeit | Korrekt; B8 und B9 offen. |
| K Katalog, Shop und Portal | in Arbeit | in Arbeit | Korrekt; zwei Punkte halb veraltet. |

### 5.2 Als offen markiert, aber umgesetzt

| Zeile | Punkt | Markiert | Tatsächlich | Nachweis |
|---|---|---|---|---|
| 34 | Prozessvariablen | in Arbeit | erledigt | ADR-0068 |
| 79 | Data Objects | in Arbeit | erledigt | ADR-0053/0058/0059/0060, ADR-0230 Slices 2–5c |
| 256 | Compiler-Validierung (Erreichbarkeit, Gateways, Scopes) | offen | erledigt | `compiler/validation.go`, `POST /api/v1/validate`, ADR-0026 |
| 307 | Business Rule Tasks | in Arbeit | erledigt | `dmn/numbers.go`; Auswertung ausserhalb des Loops. Zeilen 461–469 widersprechen ADR-0423 |
| 949 | Öffentliche API | offen | weitgehend erledigt | 438 Routen in `api/openapi.go`, API-Tokens (ADR-0194), Runtime-Contract (ADR-0176). Deploy ist weiterhin ein Sidecar (ADR-0019) |
| 950 | Job-Worker-Protokoll | offen | Protokoll erledigt, gRPC offen | ADR-0007: Long-Poll, `leaseToken`-Fencing, Lease-Timeout-Zähler |
| 951 | Worker-SDK (Go) | offen | in Arbeit | Das Paket `worker` ist nutzbar, aber nicht als SDK paketiert und dokumentiert |
| 959 | Metriken, Logs, OTel | in Arbeit | erledigt | `api/metrics.go`, `logging/`, `tracing/` |
| 1006 | Export-Stream | offen | erledigt | OpenSearch-Exporter (ADR-0114), CloudEvents-Feed (ADR-0433) |
| 1042 | Operator-Werkzeuge | in Arbeit | erledigt | Suche, Zusammenfassungen, Jobs, Sammelauflösung, Workers |
| 1118 | Modeler-Interoperabilität | offen | in Arbeit | Zeebe-Dialekt, MIM-Import (ADR-0292) |
| 1138 | Dokumentation, Tutorials, Beispiele | offen | in Arbeit | Handbücher, API-Explorer, 56 Beispiele; keine eigene Doku-Site |
| 1192 | Vollständiges Eigenschaftspanel | offen | in Arbeit | ADR-0025 `Partial` |
| 1585 | Allgemein: ID und Name | offen | erledigt | `editor.js` |
| 1600, 1602 | `/validate` und Problems-Panel | offen | erledigt | `api/validate.go`, `wireProblems` |
| 1606 | Element-Template-Schema und Katalog | offen | teilweise | `api/repository.go`, ADR-0081/0300; die Anwendung fehlt (ADR-0212) |
| 1611–1616 | User Task, form-js, Tasks-App, Formularbindung und Test | offen | erledigt | ADR-0028 |
| 1619 | Öffentliche Startlinks mit Ratenbegrenzung | offen | erledigt | ADR-0029, ADR-0186 |
| 1624 | Play-Sandbox | offen | erledigt | ADR-0030, Playground (ADR-0215) |
| 1745 | MCP-Authoring-Werkzeuge | offen | in Arbeit | Draft-Werkzeuge vorhanden, kein Validierungswerkzeug |
| 1758, 1760 | Ausrichten und Verteilen; Projekte und Ordner | offen | in Arbeit | bpmn-js-Kontextmenü; Projekte (ADR-0034/0128); verschachtelte Ordner offen |
| 2093 | Theme-Bildschirm eines Katalogs | offen | erledigt (diese Hälfte) | `catalog-admin.js` |
| 2096 | TMF620-Punkt enthält «change» | offen | diese Hälfte erledigt | `POST …/lines/{item}/change` |

Dazu kommen veraltete Fliesstexte, unter anderem in den Zeilen 297, 486, 606, 627, 1163,
1284, 1507–1511, 1627, 1697, 1735, 1814 und 1938.

### 5.3 Gelandet, aber nicht in der Roadmap

- **Engine:** Decision Services aus Business Rule Tasks (ADR-0398); FEEL-Ausdrücke für
  Assignee und Candidate Groups (ADR-0318); Trigger-Start mit Deduplizierung; Instanz-TTL
  (ADR-0085); TCK-Fallformat; Script-Sandbox (ADR-0303).
- **Betrieb:** Circuit-Breaker (ADR-0340); Supervised Workers und Workers-View;
  Deaktivieren einer Version (ADR-0119); Snapshots und Backup; Remote-Deployment-Ziele
  (ADR-0129); TLS, Helm und Docker.
- **Modeler:** Token-Simulation im Design-View (ADR-0078 und Folge-Records); Co-Editing
  (ADR-0140); Angleichen eines deployten Diagramms (ADR-0251); Element-I/O im Diagramm
  (ADR-0161); Call-Activity-Drilldown (ADR-0245); Task-Ordner (ADR-0268).
- **Panorama:** Estate-Höhe (ADR-0402); Token-Scope `landscape` (ADR-0410); Run-Graph
  (ADR-0400/0404). Der Name «Starmap» kommt in Meilenstein P nicht vor.
- **Falsch eingeordnet:** Die KI-Formulargenerierung (ADR-0260) und der FEEL-Assistent
  (ADR-0445) stehen in M2 («Events and timers»), gehören aber zum Authoring.

### 5.4 Spannungen mit den Nicht-Zielen und Leitplanken

- **«A standalone DMN product».** Inzwischen gibt es eigenständige Decision Deployments,
  Drafts, Co-Editing, teilbare Entscheidungsdokumentation, Bereinigung des Decision Stores
  und einen eigenen DRG-Renderer. Der Text wurde nur für ADR-0319 nachgeführt. Das ist
  kein Verstoss, aber eine Bewegung auf die Grenze zu, die eine bewusste Neuformulierung
  verdient.
- **«The engine core stays a library first».** Katalogbegriffe (Berechtigungen,
  Aktionsergebnisse, Feed) leben in `engine/` und `state/`. Das ist durch ADR-0312 und
  ADR-0434 gedeckt; `engine/` importiert `api/` weiterhin nicht.
- **Leitplanken unvollständig.** Die «Guiding constraints» nennen nur die Invarianten
  I1–I4. I5 («Compile, don't interpret») und I6 («Events are facts») fehlen, obwohl
  `AGENTS.md` sechs Invarianten verlangt.

### 5.5 Tatsächlich offene Arbeit je Meilenstein

| Meilenstein | Offen (Grösse) |
|---|---|
| M1 | Lesen aus einem Data Store zur Laufzeit (L, braucht ADR); XMI-Export (S–M); Ausbau der Conformance (M, laufend); sequenzielles Ad-hoc (M); zyklische Gateway-Joins (M–L); IMAP XOAUTH2 (S); Push-Postfach (M) |
| M2 | Nachrichtenpuffer, ADR-0370 (M); Korrelationsschlüssel beim Message-Start (S–M); wiederholbare nicht-unterbrechende Conditional-Events (S–M); Übersichten über Nachrichten und Signale, ADR-0446/0447 (M); Lanes-Schichten B und C (M/L) |
| M4 | gRPC entscheiden (bauen = L plus Protobuf-Abhängigkeit gegen ADR-0010, oder streichen); Go-SDK paketieren und dokumentieren (S–M); Deploy als event-sourced Command (L, ADR nötig, berührt I4/I6); Auslaufzustand einer Version, ADR-0130 (M); Problem-Datensätze, ADR-0381 (L) |
| M5 | ADR-0006 nachführen; mehrere Partitionen pro Knoten; Zellen nach ADR-0175 (alles XL). Alternative: Föderation nach ADR-0369–0374 (L bis XL) |
| M6 | SDKs in weiteren Sprachen (je M, nach 1.0); Import aus Camunda 7 und Signavio (M–L); Doku-Site (M); Zusage für 1.0 (L) |
| S | Execution Listeners (M, ADR nötig); `zeebe:properties` (S); Hosted Apps H1–H4 (L, sicherheitskritisch, ADR-0204 `Proposed`); verteilte Sitzungen (M); Rotation des Vault-Schlüssels und KMS (M) |
| A | Template-Applier, ADR-0212 (M–L); Versionshistorie, ADR-0031 (M); MCP-Validierungswerkzeug (S); Copilot-Panel (L); Minimap und Elementfarbe (je S); Kommentare (M); Playground-Regel mit Mindestfällen, ADR-0424 (S–M) |
| B | B8 Console (M); B9 Dokumentaustausch mit Trockenlauf (M); `atlas:capability` (M); Import von Referenzmodellen (L) |
| K | Console-Bestellansicht, dann Operator-Aktionen (M–L); Aktion über alle Positionen (M); ausgesetzter Berechtigungszustand (L, berührt `applyToState`); Messgate für Lebenszyklen pro Position (S–M); Sprache am Konto (S–M); Löschung personenbezogener Daten, ADR-0314 (M, Entscheid); TMF620, ADR-0387 (L); restliche Event-Katalog-Einträge (je S, Deployment M); Katalogexport (S–M) |

Zwei Planungsdokumente verdienen einen Hinweis:

- `docs/planning/0429-product-actions-plan.md` ist in allen markierten Slices umgesetzt;
  seine drei Punkte «Not in any slice yet» sind offen.
- Die Vorlage unter `docs/planning/lebenszyklus-pro-position` wurde nie ausgeführt (eigener
  Abschnitt «Ungeprüft»). Sie würde nach ADR-0429 beim Veröffentlichen abgelehnt, weil die
  Shop-Send-Task fehlt (`catalog/lifecycle.go`, `shopProblems`).

---

## 6. ADR-Prüfung

### 6.1 Ergebnis nach Bereich

Die Spalten «OK» und «Abweichung» zählen nur die `Landed`-Records eines Bereichs.

| Bereich | `Landed` | OK | Abweichung (hoch / mittel / niedrig) | `Partial` / `Not started` korrekt | zu niedrig eingestuft |
|---|---:|---:|---|---:|---:|
| 0001–0100 | 94 | 76 | 18 (1 / 2 / 15) | 6 | 0 |
| 0101–0200 | 94 | 75 | 19 (2 / 0 / 17) | 5 | 0 |
| 0201–0295 | 90 | 78 | 12 (3 / 4 / 5) | 5 | 0 |
| 0296–0372 | 62 | 53 | 9 (0 / 1 / 8) | 7 | 8 |
| 0373–0448 | 56 | 50 | 6 (0 / 1 / 5) | 14 | 5 |
| **Summe** | **396** | **332** | **64 (6 / 8 / 50)** | **37** | **13** |

`go test ./docs/adr` ist grün: Index und Front Matter stimmen im Basisstatus überein. Die
Abweichungen liegen in der Sache, die kein Guard lesen kann. Das Muster ist deutlich:
**Ältere Records sind eher überbewertet, neuere eher unterbewertet.** Der Status wird beim
Merge gesetzt und danach selten nachgeführt.

### 6.2 Abweichungen mit hohem Schweregrad

Alle sechs sind am Code bestätigt.

| ADR | Markiert | Befund | Nachweis | Empfehlung |
|---|---|---|---|---|
| **0089** Fehlerereignisse | `Landed` | Phase 3 («a service task with an error boundary catches a worker-thrown error») hat keinen Produktionspfad. `Processor.ThrowJobError` rufen nur der Playground und der Conformance-Treiber auf. Es gibt keine HTTP-Route, kein MCP-Werkzeug und keinen Pfad über `worker/` oder die Worker Types. Ein Worker kann nur `fail` melden und damit nie ein Fehler-Boundary-Event oder einen Fehler-Event-Subprozess auslösen. | `engine/processor.go:588`; Job-Routen `api/openapi.go:493–511`: nur activate, complete, fail | Route `POST /api/v1/jobs/{key}/error`, Worker-Client und MCP-Werkzeug (M); bis dahin `Partial` |
| **0117** KI-Agent-Task | `Landed` | `VTAgentRun`, `GET …/agent-runs`, die Grenzen `maxIterations`, `timeout` und `tokenBudget`, die Prüfung gegen ein Ausgabeschema und die Erfassung des Token-Verbrauchs fehlen. Das Agent-Feature wurde anders gebaut (ADR-0253/0256), ohne dass ADR-0117 nachgeführt wurde. | `grep VTAgentRun` trifft nur ADR-Text; `compiler/connector_compile.go` kompiliert nur Worker, Model, Prompt, ResultVar, Retries | `Partial`, oder Ergänzung, die das Entfallene benennt. Token- und Kostenbudget als eigenes Thema prüfen; auch ADR-0445 hat keinen Kostenzähler |
| **0134** Git-basierte Anwendungen | `Landed` | Keine Git-Anbindung: kein Clone, Sync oder Push, keine Repository-Credentials. Vorhanden sind das Quell-Layout und ein Export/Import als `tar.gz`. Der Record trägt noch «Decision outcome (proposed)» und drei offene Fragen. `README.md:135` wirbt mit «git-backed». Das Audit vom August hatte ihn fälschlich als umgesetzt geführt. | `go.mod` ohne Git-Bibliothek; `api/appsource_http.go:21–23` nennt den Git-Slice als künftig | `Partial`; README korrigieren |
| **0207** Paketierung von Worker Types | `Landed` | Nur Schritt 1 von 6 existiert (Manifest-Parser in `workertype/`), und kein Produktionspaket importiert ihn. Keine Digest-Prüfung, keine Signaturpolitik, kein OCI-Start. Der Record sagt selbst, er sei «a target contract, not a claim that packaging is already implemented». | `workertype/manifest.go`; kein Import ausserhalb des Pakets | `Partial` (Rest XL); dasselbe Muster bei ADR-0208 (Schritte 4–8 fehlen) |
| **0216** Mockups als eine Ansicht | `Landed` | Nichts davon existiert: kein `/api/v1/mocks`, kein `MockReport`, keine vereinte Ansicht, kein Jira-Mock. Die Console hat weiterhin eine Seite pro Art, die verworfene Option. | Suche nach `MockReport` und `/api/v1/mocks` leer; `api/web/app.js:698–699` | `Not started` |
| **0275** Sichtbarkeit von Instanzen | `Landed` | **Regression.** `GET /api/v1/tasks?content=1` liefert pro sichtbarer Aufgabe bis zu 40 Variablenwerte zu je 200 Zeichen aus dem Scope der Aufgabe, nicht nur die Felder des Formulars. Das gilt auch für Aufgaben ohne Adressaten, die jeder angemeldete Benutzer sieht, deren Variablen-Endpoint aber 404 antwortet. Die Funktion kam am 2026-09-28 mit Commit `d2a3d10` ohne ADR hinzu; ihr Kommentar «widens what a list row says, not who may read it» trifft seit ADR-0275 nicht mehr zu. F11 ist damit teilweise wieder offen. | `api/taskfolders.go:94–133` (`taskContent`, ohne Rollenprüfung); `api/taskauthority.go:125–126` | Issue eröffnen; auf Formularfelder adressierter Aufgaben beschränken oder an `instanceAccessFor` binden (S–M) |

### 6.3 Abweichungen mit mittlerem Schweregrad

| ADR | Befund | Nachweis |
|---|---|---|
| 0029 | Der anonyme Start `POST /public/forms/{token}/start` prüft die Variablen nicht gegen das Formular, obwohl der Record das zusagt. Ein anonymer Aufrufer kann beliebige Variablennamen und -werte setzen, darunter solche, die ein Gateway auswertet. Ratenbegrenzung und Grössenlimit greifen. | `api/publiclinks.go:268ff` mit `parseStartVariables` |
| 0041 | Die zugesagten Credential-Quellen «inline», «externer Secret-Manager» und «gemountete Secret-Datei» fehlen; es gibt nur Vault und Umgebungsvariablen. | `api/connectors.go:57–83` |
| 0208 | Die Schritte 4–8 fehlen (Pakete, Vertrauen und Signatur, Herkunft und Digest, entfernte Quellen). | `api/builtinworkertypes.go` |
| 0283 | Die strikte Behandlung von Log-Korruption gilt nur für den Reader. Der WAL-Tailer, der den OpenSearch-Exporter speist, behandelt eine korrupte Batch als Ende und springt zum nächsten Segment. Der Rest eines beschädigten, versiegelten Segments wird ohne Fehler übersprungen. Der Plan vom 7. September verlangte das für F03 ausdrücklich. | `wal/tailer.go:77–81, 117–128` |
| 0211 | Der im Record verlangte Vollständigkeitstest für das Mesh fehlt. | `api/panorama/mesh_test.go` |
| 0246 | Der Prozess-Tab der Tasks-App liest `/timeline`, das die Rolle `operator` verlangt. Ein einfacher Benutzer kann ihn nicht nutzen. | `api/openapi.go:385` |
| 0335 | Der Kern ist gebaut (`instanceKey` in der Antwort, über ADR-0416), der Record steht auf `Proposed` / `Not started`. Der CSV-Start und vier Hosted Apps nutzen noch den alten Umweg. | `api/startinstance.go:164`; `api/csvupload.go:23` |
| 0313 | Teilweise gebaut (Sprachwahl im Browser) und als `Not started` markiert. Es fehlt die Sprache am Konto. Fehlt eine Übersetzung, zeigt `t()` den Schlüssel an. | `api/web/shop.js:721–741` |
| 0354 | Beschreibt die Genehmigungsseite, die ADR-0394 abgelöst hat; die Standardsortierung weicht ab. | `api/web/app.js:7904ff` |
| 0411 | Die API erfasst, welche Typen ein Worker bedient (`serves`), die Console zeigt es nicht. Ein Worker, der eine leere Queue abfragt, wirkt in der UI abwesend. | `api/workers.go:55`; `api/web/app.js:6765, 6905` |
| 0425 | `change` ist gebaut; der Record steht noch auf `Partial`. | `api/orderaction.go:203` |

### 6.4 Zu niedrig eingestufte Records

Diese Records sind weiter, als ihr Status sagt: ADR-0299, 0303, 0311, 0312, 0313, 0316,
0333, 0335, 0387, 0403, 0409, 0410 und 0425. Bei 0299, 0303 und 0311 bleiben nur
Folgearbeiten, die ADR-0298 ausdrücklich nicht als `Partial` zählt.

### 6.5 Verdeckte Restarbeit in gelandeten Records

Viele `Landed`-Records nennen im eigenen Text Arbeit, die aufgeschoben wurde. Das ist
legitim, macht die Arbeit aber unsichtbar: Sie steht weder in der Roadmap noch in einem
Issue. Die gewichtigsten Punkte nach Thema:

- **Nachrichten.** Weder Message-Events (ADR-0020/0035/0094) noch Receive-Tasks (ADR-0102)
  puffern eine Nachricht, die vor dem Abonnenten eintrifft. Message-Starts korrelieren nur
  über den Namen. Ein Singleton-Start weckt die laufende Instanz nicht. Hier liegt die
  eigentliche Lücke von M2.
- **Kompensation.** Kompensation über Call Activities, strikt sequenzielle Verkettung und
  ein äusserer Kompensationswurf auf eine Transaktion fehlen (ADR-0103/0108). Der
  Kompensationsfehler aus #1013 hat keinen eigenen Record (ADR-0284 erwähnt ihn).
- **Arbeit auf dem Run-Loop**, entgegen den Regeln in `AGENTS.md`:
  Besuchs- und Terminierungs-Scans der Kollaborationsansicht (ADR-0080); Queue-Tiefe im
  Workers-View und Incident-Scan im Starmap (ADR-0365/0366); die Typzählung in
  `api/workers.go:289–296` läuft über `maxTypeScan` hinaus weiter; `GET /api/v1/data-objects`
  scannt auf dem Writer, wenn auch durch `dataObjectsScanLimit` begrenzt (ADR-0382).
- **Sicherheit.** Der Jira-Inbound-Leser hält die Jira-Credentials in der Engine
  (ADR-0214, Schritt 3). Kurzlebige Credentials pro Worker fehlen (ADR-0297). Ein
  fehlgeschlagener Token-Login ist kein eigenes Audit-Ereignis (ADR-0198). Ein
  einheitliches Sperrmodell für Sitzungen und Tokens fehlt (ADR-0281). Beim öffentlichen
  Start fehlen CAPTCHA und Audit-Log (ADR-0029).
- **Ereignisse.** Conditional-Events werten einen Auswertungsfehler als `false`
  (`engine/conditional.go:21–27`, ADR-0273). Nicht-unterbrechende Escalation- und
  Conditional-Boundaries feuern nur einmal (ADR-0137/0236).
- **Daten.** Das Purgen gibt WAL-Platz nicht frei (ADR-0115/0144/0146). Für einen Restore in
  ein leeres Verzeichnis fehlt der End-to-End-Test (ADR-0282). Der Store hat O(N²) Churn
  (ADR-0296).
- **DMN.** Die Promotion trägt keine Entscheidungen mit (`api/promote.go:311–316`,
  ADR-0319). Zwei Modelle mit derselben Decision-ID lassen sich beide deployen (ADR-0321).
  `versionTag` wird nicht unterstützt (ADR-0063/0423).
- **Prüfmechanik.** Die in ADR-0298 verlangte periodische Nachprüfung hat keinen
  Mechanismus. Es gibt keine `Fuzz*`-Funktionen, den in ADR-0271 verlangten Test
  eingeschlossen. Die Differentialtests laufen nicht in der CI. Das `audit`-Build-Tag aus
  dem Plan vom 7. September wurde nie eingeführt.

### 6.6 Formale Mängel

- **Veraltete Aussagen in gelandeten Records.** ADR-0007, 0037, 0050, 0052, 0071, 0079,
  0082, 0098, 0103, 0142 und 0154 nennen als offen, was gebaut ist. ADR-0164 und 0165
  beschreiben die Ausführung im Serverprozess als aktuell. ADR-0327, 0329 und 0330 sagen
  «not deletable», obwohl ADR-0336 das Löschen gebaut hat. ADR-0419 beginnt mit
  «(Proposed…)». ADR-0376 nennt neun Werkzeuge; es sind zehn. ADR-0429 §9 sagt «the rest
  is not built», obwohl alle Slices gelandet sind.
- **Nicht existierende Bezeichner zitiert.** ADR-0160/0163 (`connectordialog.js`),
  ADR-0255 (Flag `--supervise-connectors`), ADR-0277 (`ElementInstancesOnNode`), ADR-0325
  (`api/dmnlayout`), ADR-0343 (Pfad des Beispiels), ADR-0428 (`catalogOwnerOfDelivered`).
- **Fehlerhaftes Front Matter.** Bei ADR-0127, 0139, 0154, 0166, 0191 und 0200 steht die
  Zeile `Implementation` mitten in der mehrzeiligen Klammer der Statuszeile; der Parser
  toleriert das. Der Zusatz «(amended)» erscheint im Index uneinheitlich.
- **Dokumente ausserhalb `docs/adr`.**
  - `docs/runtime-contract.md` gibt «Applies to: Atlas 0.6.x» an.
  - `deploy/helm/atlas/README.md:190–192` sagt, `/metrics` sei ohne Authentisierung
    erreichbar. Seit ADR-0198 trifft das nicht mehr zu; der Satz ist sicherheitsrelevant
    irreführend.
  - Die Repository-Ansicht verspricht «Install one and it lands in your palette»
    (`api/web/app.js:10305`), obwohl der Applier fehlt.
  - `docs/compliance/isds-offene-punkte.md` (O-02) führt eine geschlossene Lücke als offen.
  - `docs/comparisons/mim.md` widerspricht sich bei Oracle.
  - Codekommentare in `api/server.go:15–25` und `api/handlers.go:4970` behaupten, es gebe
    keine Leases.

### 6.7 Stand der früheren Audits

**Audit vom 2026-08-25.** Geschlossen: Worker-Migration (ADR-0164/0165/0168; 18 Arten
standardmässig ausgelagert), Aktivierungs- und Lease-Zähler sowie `atlas_open_incidents`
(ADR-0142), `docs/runtime-contract.md` (ADR-0176), Playground auf der echten Engine
(ADR-0030). Offen: Template-Anwendung (ADR-0027/0081) samt UI-Formulierung, die 16
fehlenden Pakete (ADR-0167), widersprüchlicher Text in ADR-0154, WSDL und WS-Security
(ADR-0165). Korrektur: Die damalige Einstufung von ADR-0134 als umgesetzt war falsch.

**Umsetzungsplan vom 2026-09-07 (F01–F17).**

| Status | Befunde |
|---|---|
| Geschlossen | F01, F02, F04, F06, F07, F08, F09, F10, F12, F13, F14, F16, F17 |
| Teilweise | F03 (Tailer; Frame-Decoder nicht zusammengeführt); F05 (kein Archiv-Manifest, kein End-to-End-Restore); F11 (`?content=1`); F15 (Scan in `api/workers.go`, Alter der Queue nicht beobachtet) |

---

## 7. Issues

### 7.1 Überblick

Alle 65 offenen Issues wurden mit ihren Kommentaren gelesen und gegen den Code geprüft.

| Empfehlung | Anzahl |
|---|---:|
| Schliessen (gebaut, obsolet, Platzhalter) oder in ein ADR überführen | 25 |
| Sofort beheben (S bis M) | 10 |
| Planen | 21 |
| Zurückstellen | 9 |

Zwei Issue-Texte widersprechen dem Code: #802 ist entgegen dem Kommentar vom 16. September
**nicht** erledigt, und die ISDS-Liste führt #1011 als offen, obwohl es behoben ist.

**Befunde ohne Issue.** Das `?content=1`-Leck, ADR-0089 (Worker-Fehler) und ADR-0029
(Schema-Prüfung beim öffentlichen Start) haben kein Issue. Für alle drei sollte eines
eröffnet werden.

### 7.2 Bewertung aller offenen Issues

Spalten: **Wirkung** hoch / mittel / niedrig; **Aufwand** S / M / L / XL.

**Engine-Korrektheit und Betrieb**

| # | Thema | Stand | Wirkung | Aufwand | Empfehlung |
|---|---|---|---|---|---|
| 1013 | Kompensationswurf schliesst Instanz zu früh ab | gültig, **reproduziert** | hoch | M | sofort |
| 804 | Paralleles Event-Gateway: `instantiate` wird stillschweigend ignoriert; der Compiler liest das Attribut nirgends | gültig | hoch | S (Stufe 1) / L | Stufe 1 sofort: beim Deploy ablehnen, nicht beim Laden (ADR-0393) |
| 1123 | Auflösen eines VariableTooLarge-Incidents setzt das Element nicht fort | gültig | mittel | L (ADR) | planen |
| 837 | Token wartet ohne Incident auf einen nicht konfigurierten Worker | gültig | mittel | M | planen |
| 1014 | Grund eines Auflösungsfehlers geht verloren (22 stumme Zweige) | gültig | mittel | M | planen |
| 490 | 429/503 und Retry-After als vorübergehend behandeln | gültig, durch ADR-0340 gemildert | mittel | M–L | planen (ADR) |
| 983 | Restore ersetzt eine Definition unter laufenden Instanzen | gültig, nie gemessen | mittel | M | messen, dann entscheiden |
| 980 | Job-Type-Tabelle verwirft einen Eintrag stillschweigend | gültig | niedrig–mittel | M | planen |
| 982 | `settings/` mischt Konfiguration und Identität der Installation | gültig | niedrig | S–M | planen |
| 981 | Import mit Neuvergabe der Schlüssel | offen | niedrig–mittel | L | zurückstellen, mit #983 zusammenführen |

**Sicherheit und Script-Sandbox**

| # | Thema | Stand | Wirkung | Aufwand | Empfehlung |
|---|---|---|---|---|---|
| 1012 | Instanz-Lesepfade prüfen nur die Rolle (8 Routen) | gültig | mittel | M / L–XL | zuerst ADR (Operator pro Projekt?), dann Routen über `instanceAccessFor` |
| 1011 | Lesepfad für Aufgaben ohne Beziehungsprüfung | **behoben** (ADR-0421, 0.8.0) | – | – | schliessen, ISDS O-02 nachführen |
| 1042 | Passwort-Reset über `window.prompt` im Klartext | gültig | niedrig | S | sofort |
| 894 | Risiko von `--script-sandbox=off` dokumentieren | gültig | mittel | S | sofort, zusammen mit PR #882 |
| 893 | Mittleres Sandbox-Profil | offen | hoch | M | planen |
| 895 | Capability-Deklarationen im Modell | offen | hoch | L | planen; Voraussetzung, damit `strict` Standard werden kann |
| 896 | Ressourcenlimits (setrlimit, cgroups) | offen | mittel | S / L | setrlimit-Slice planen |
| 897 | Eigene OS-Identität für Script-Worker | mit externem Worker heute möglich | mittel | S | als Betriebsmuster dokumentieren (Helm) |
| 898 | Egress-Policy | offen | mittel | L | zurückstellen bis #895/#897 |
| 1002 | Node startet nicht in der strikten Sandbox | **behoben** (0.7.0) | – | – | schliessen |

**Modeler, UI und Informationsmodell**

| # | Thema | Stand | Wirkung | Aufwand | Empfehlung |
|---|---|---|---|---|---|
| 468 | OAuth2-Felder des REST-Workers gehen beim Speichern verloren: Der Compiler liest `authTokenUrl`, `authClientId` und `authScope` (`compiler/parse.go:2441`), das Moddle kennt sie nicht, bpmn-js verwirft unbekannte Attribute beim Speichern; der Guard `TestModdleKnowsEveryConnectorAttribute` prüft nur AD und Jira | gültig, stiller Datenverlust | mittel | S | sofort; Guard auf alle Worker Types ausweiten |
| 802 | Kollaborations-Overlay ohne abgebrochene Tokens | gültig; der Kommentar «erledigt» ist falsch | niedrig | S | sofort |
| 1100 | Shop: optionale Teile in der Katalogliste | gültig | niedrig | S | sofort |
| 803 | Heatmap liest nur Besuche | offen; der Zähler existiert | niedrig–mittel | S | planen |
| 927 | Screenshots der Nuggets veraltet | gültig | niedrig | S | `make nuggets` |
| 919 | DMN-Editor als eigene Seite | **erledigt** (ADR-0320–0322) | – | – | schliessen |
| 978 | Schreibpfeile, «enumeration», Kontextmenü | erledigt bis auf einen Rest | – | S | schliessen; Rest (zweite Zuweisung auf einer Lese-Assoziation wird verworfen) als eigenen Bug erfassen |
| 945 | Verwendungsnachweis aus dem Klassen-Canvas | durch ADR-0363 überholt | – | – | schliessen |
| 905 | Klassenattribute im Modeler | Teil 1 erledigt | niedrig | M | schliessen |
| 944 | Drafts im Verwendungsnachweis | offene Frage aus ADR-0338 | niedrig–mittel | M | zurückstellen |
| 947 | Geschäftsobjekt umbenennen | teilweise; der Entscheid fehlt | mittel | S | kurzes ADR und Handbuchtext |

**API, DMN und FEEL**

| # | Thema | Stand | Wirkung | Aufwand | Empfehlung |
|---|---|---|---|---|---|
| 933 | Start liefert den Instanzschlüssel | weitgehend erledigt | mittel | S | Rest erledigen (CSV-Start, Hosted Apps, Status von ADR-0335), dann schliessen |
| 202 | Cursor-Paginierung für Instanzen | teilweise (ADR-0378) | niedrig | L | eingrenzen auf den ungebundenen Cursor |
| 917 | Reihenfolge der Slices nach DMN 1.5 | obsolet | – | – | schliessen |
| 996 | Welchen FEEL-Dialekt Atlas spricht | faktisch Option 1 (ADR-0388) | mittel | S | kurzes ADR und Handbuchseite |
| 1030 | Folgearbeiten zu TMF620 | offen | niedrig–mittel | S / M | Test der Feldzuordnung zuerst |

**Architektur, Graph und Föderation**

| # | Thema | Stand | Wirkung | Aufwand | Empfehlung |
|---|---|---|---|---|---|
| 986 | Zusammenarbeit zwischen Instanzen | nur ADRs, kein Code | hoch (strategisch) | XL | planen, Start mit ADR-0370 |
| 890 | Meilenstein B | B8 und B9 offen | mittel–hoch | M + M | B8 als Nächstes |
| 887 | Graphabfragen im Panorama | offen; Draft-PR #911 | mittel | L | in ADR-0420 überführen; Widerspruch bei verborgenen Knoten klären (#887: nie durchlaufen; ADR-0420 §8: opt-in) |
| 1050 | Estate als Graph | **erledigt** (ADR-0400–0403) | – | – | schliessen |
| 1053 | Ganzen Run-Graph durchlaufen | weitgehend erledigt (`rungraph/`) | – | – | schliessen; Rest in ADR-0404/0409/0420 |
| 1074 | Anforderungsregister | Konzept (ADR-0407 `Proposed`) | mittel | L | zurückstellen bis zu einem konkreten Bedarf |

**CI und Werkzeuge**

| # | Thema | Stand | Wirkung | Aufwand | Empfehlung |
|---|---|---|---|---|---|
| 1001 | Race-Job wird vom Paket `api` dominiert | gültig | mittel | L | planen |
| 1023 | Docs-only-Job einengen | gültig | niedrig | S–M | mit #1001 |
| 975 | `make adr-number` überspringt `vendor/canvas/src`; der Guard überspringt dasselbe | teilweise | niedrig | S | sofort |
| 979 | Coverage-Boden driftet | entschieden (Ergänzung zu ADR-0018) | – | – | schliessen oder als Tracker umbenennen |
| 198 | ChatGPT-Konnektor findet Werkzeuge nicht | kein Atlas-Defekt | – | – | schliessen |

**Worker-Katalog (Epic #431 und Kinder)**

| # | Thema | Stand | Empfehlung |
|---|---|---|---|
| 431 | Epic Worker-Framework | Welle 1 erledigt (`docs/comparisons/mim.md`) | schliessen |
| 434, 437, 440, 448 | LDAP, Flat File, Script, eDirectory | erledigt | schliessen |
| 436 | SQL | erledigt für MSSQL, MariaDB und Postgres | schliessen (Oracle = #444) |
| 444 | Oracle | nicht gebaut; `go-ora` ist reines Go | bei Bedarf als Nächstes (S) |
| 435 | HR-Feed (CSV/SFTP) | teilweise | auf SFTP und einen Joiner-Mover-Leaver-Referenzprozess eingrenzen (M) |
| 441, 446, 451 | Keycloak, ServiceNow, Salesforce | über REST, OAuth2 oder SCIM erreichbar | als Repository-Paket anbieten; schliessen |
| 445 | Exchange / M365 | weitgehend abgedeckt (Entra, Mail über Graph) | schliessen oder eingrenzen |
| 442 | SAP HCM / SuccessFactors | BAPI durch No-CGO blockiert (ADR-0010) | zurückstellen (XL) |
| 443, 449, 450 | Abacus, Workday, Google Workspace | erreichbar oder mittlerer Aufwand | zurückstellen |
| 447 | eIAM (Bund) | Umfang nie validiert | zurückstellen |
| 453 | IDV (CH) | Platzhalter ohne konkretes System | schliessen |

Die meisten SaaS-Wünsche bestehen das zweite Kriterium aus ADR-0299 nicht: REST, SCIM oder
SOAP mit OAuth2 erreichen die Systeme bereits. Sie gehören als Pakete ins Repository, nicht
als eigene Worker Types. Wird ADR-0299 angenommen, lassen sich solche Wünsche künftig mit
einem Verweis beantworten.

### 7.3 Offene Pull Requests

| PR | Inhalt | Bewertung |
|---|---|---|
| #1171 | Testkorrektur für Script-Timeouts unter Windows (5 Zeilen) | relevant; mergen, sobald die CI grün ist |
| #911 (Draft, 29 Tage) | Graphabfrage v1 im Panorama | **Nicht mergen.** Der PR fügt `.github/workflows/panorama-query-finish.yml` mit `contents: write` hinzu; der Workflow führt Python-Skripte aus und pusht Commits. Er zitiert eine ADR-Nummer, die inzwischen anderweitig vergeben ist, und nutzt den alten Coverage-Boden von 94 %. Schliessen; `api/panorama/query_*.go` und den ADR-Entwurf auf einen frischen Branch übernehmen. |
| #882 (29 Tage) | Script-Sandbox in der Architekturübersicht | Konflikt mit der neu gezeichneten Übersicht; der Link zeigt auf einen Draft, der heute ADR-0303 ist. Neu aufsetzen, zusammen mit #894. |

---

## 8. Massnahmenplan

Die Reihenfolge folgt drei Kriterien: Korrektheit und Sicherheit vor Funktionsumfang;
kleine, unabhängige Schritte vor grossen; Entscheidungen vor Code, wo ein Strang an einer
Grundsatzfrage hängt.

### Stufe 0: Korrektheit und Sicherheit

| Nr. | Massnahme | Grösse | Bezug | Hinweis |
|---|---|---|---|---|
| 0.1 | Kompensation: Instanz erst abschliessen, wenn das Token den Wurf verlassen hat | M | #1013, ADR-0284 | Zuerst Regressions- und Recovery-Test; `completeScope` in `engine/behavior.go:1632–1638` |
| 0.2 | `?content=1` auf die Formularfelder adressierter Aufgaben beschränken oder an `instanceAccessFor` binden | S–M | ADR-0275, F11 | Vorher Issue eröffnen |
| 0.3 | Öffentlichen Start gegen das Formularschema prüfen | S–M | ADR-0029 | Einziger anonymer Schreibpfad in eine Instanz |
| 0.4 | OAuth2-Attribute ins Moddle aufnehmen; Drift-Guard auf alle Worker Types ausweiten | S | #468 | Stiller Datenverlust in Modellen |
| 0.5 | `instantiate="true"` beim Deploy ablehnen | S | #804 | Nur das Deploy sperren, nicht das Laden beim Start (`AGENTS.md`, ADR-0393) |
| 0.6 | Worker-Fehlerpfad: Route `POST /api/v1/jobs/{key}/error`, Worker-Client, MCP-Werkzeug | M | ADR-0089 | Ohne ihn ist das Fehler-Boundary-Event an Service Tasks mit echten Workern nicht nutzbar |
| 0.7 | Risiko von `--script-sandbox=off` dokumentieren; Helm-README zu `/metrics` korrigieren | S | #894, ADR-0198 | Beides sicherheitsrelevante Dokumentationsfehler |

### Stufe 1: Wahrheit der Dokumente wiederherstellen

| Nr. | Massnahme | Grösse | Inhalt |
|---|---|---|---|
| 1.1 | Roadmap-PR | S | Vier Köpfe und rund 29 Markierungen (Abschnitt 5.1 und 5.2); fehlende Fähigkeiten nachtragen (5.3); Leitplanken auf sechs Invarianten ergänzen; die echten Restpunkte von M2 als eigene Punkte führen; ADR-0260 und 0445 nach A verschieben |
| 1.2 | ADR-Status-PR | S | Herabstufen: 0117, 0134, 0207, 0208, 0216. Heraufstufen: die 13 Records aus 6.4. Veraltete Texte, Front Matter, `docs/runtime-contract.md`, README («git-backed»), Repository-Formulierung, ISDS O-02 korrigieren |
| 1.3 | Issue-Bereinigung | S | 25 Issues schliessen oder überführen (7.2); drei Issues eröffnen (7.1); PR #911 schliessen, #882 neu aufsetzen |

### Stufe 2: Produktstränge mit eigenem Nutzen

| Nr. | Massnahme | Grösse | Voraussetzung | Nutzen |
|---|---|---|---|---|
| 2.1 | Nachrichtenpuffer | M | ADR-0370 (angenommen) | Behebt den Verlust einer Nachricht, die vor ihrem Abonnenten veröffentlicht wird; schliesst die echte Lücke von M2; erster Baustein der Föderation (#986) |
| 2.2 | B8: Console-Oberfläche der Business-Architektur, danach B9 | M + M | keine; alle Routen existieren | Einziger gebauter Bereich ohne UI. B9 nach dem Muster des Katalogimports (ADR-0436) |
| 2.3 | Element-Template-Applier und MCP-Validierungswerkzeug | M–L + S | ADR-0212 annehmen | Macht ADR-0027, 0081 und 0300 sowie die vorhandenen Repository-Pakete nutzbar |
| 2.4 | Katalog: Messgate (Run-Loop-Last bei Lebenszyklen pro Position) und die nie ausgeführte Vorlage; danach Console-Bestellansicht | S–M, S, M–L | – | Die Bestellansicht ist Voraussetzung für Operator-Aktionen |
| 2.5 | Instanz-Lesepfade: ADR zur Frage «Operator pro Projekt?»; unabhängig davon die acht Routen über `instanceAccessFor` führen | S + M | #1012, ADR-0275 | – |
| 2.6 | Script-Sandbox: #897 als Betriebsmuster, dann #893, dann #895 | S, M, L | – | #895 ist Voraussetzung, damit `strict` Standard werden kann |

### Stufe 3: Grundsatzentscheidungen vor weiterem Code

| Nr. | Entscheidung | Empfehlung |
|---|---|---|
| 3.1 | gRPC (M4) | Streichen. Die HTTP-Job-API mit Long-Poll und Fencing erfüllt den Zweck; gRPC brächte Protobuf-Abhängigkeiten gegen ADR-0010. Danach das Paket `worker` als Go-SDK veröffentlichen |
| 3.2 | Föderation oder Partitionszellen (M5) | Vorher ADR-0006 nachführen. Die Föderation (ADR-0369–0374) ist weitgehend angenommen und günstiger. Partitionszellen (ADR-0175) lösen ein anderes Problem, nämlich Durchsatz einer Domäne statt Kopplung mehrerer Domänen |
| 3.3 | Estate-Graph | ADR-0420 entscheiden. Bis dahin ist `rungraph/` eine gebaute, aber ungenutzte Bibliothek |
| 3.4 | Hosted Apps (ADR-0204) | Erst bauen, wenn der Record angenommen ist; der Strang ist sicherheitskritisch |
| 3.5 | Offene Fragen in ADRs | Die 77 Prüfungen über das Jahr verteilen statt im September 2027 gesammelt |

---

## 9. Einordnung

### 9.1 Soll die Roadmap überhaupt nachgeführt werden?

**Stärkste Gegenposition.** Der ADR-Index mit seiner Spalte `Implementation` ist präziser
als die Roadmap, wird von Tests bewacht und enthält jede Entscheidung. Eine Roadmap mit
2'200 Zeilen dupliziert ihn und veraltet zwangsläufig. Ehrlicher wäre, sie auf Richtung
und Reihenfolge zu kürzen.

**Warum ich trotzdem für das Nachführen bin.** Der Index kennt weder Reihenfolge noch
Priorität noch Abhängigkeiten, und `AGENTS.md` schickt jeden Agenten zuerst in die
Roadmap. Eine Roadmap, die Gebautes als offen führt, lädt dazu ein, es ein zweites Mal zu
bauen. Ich empfehle einen Mittelweg: die Roadmap auf Status, Reihenfolge und Verweise
kürzen, sodass die Details in den ADRs stehen und die Roadmap nicht mehr in Fliesstext
veraltet.

### 9.2 Ist die hohe ADR-Kadenz ein Problem?

**Stärkste Gegenposition.** 448 Records in rund drei Monaten bedeuten, dass jede
Entscheidung begründet und auffindbar ist. Guards erzwingen Nummern, Zitate und das
Ablaufen offener Fragen. Das ist mehr Disziplin, als die meisten Projekte aufbringen.

**Warum die Kadenz dennoch ein Risiko ist.** Das Etikett `Landed` setzt der Autor beim
Merge, und nichts prüft es danach. 16 % der gelandeten Records weichen ab, sechs davon in
der Kernaussage. Auch ein früheres Audit hat sich bei ADR-0134 geirrt. ADR-0298 benennt
die periodische Nachprüfung als einzige echte Kontrolle, hat aber keinen Mechanismus dafür.

Ein leichtgewichtiger Mechanismus: Jeder `Landed`-Record nennt in einer Zeile `Evidence`
mindestens einen Test oder eine Route, und `go test ./docs/adr` prüft, dass diese
existieren. Das hätte ADR-0089, 0117 und 0216 aufgedeckt. Es ersetzt die Lektüre nicht,
verhindert aber die gröbsten Fälle.

---

## 10. Was ich nicht weiss

- **Laufzeit.** Ob die 332 Records «ohne Befund» sich auch zur Laufzeit wie entschieden
  verhalten. Geprüft wurden Existenz und Verdrahtung, nicht das Verhalten.
- **Reichweite der neuen Funde.** Ob Escalation- oder Signal-Würfe denselben Fehler zeigen
  wie die Kompensation in #1013; geprüft wurde nur die Kompensation. Ob die Befunde unter
  Windows dieselben sind.
- **Reale Exposition.** Wie gross die Exposition durch `?content=1` und den ungeprüften
  öffentlichen Start in realen Installationen ist. Das hängt davon ab, welche Variablen
  Prozesse führen und welche Gateways sie auswerten.
- **Gewichtung.** Wie Nutzen und Dringlichkeit aus Sicht von Kunden zu gewichten sind.
  Sämtliche Wertungen in Abschnitt 7 beruhen auf dem Code, nicht auf Nutzungsdaten.
- **Zeitpunkt.** Wann genau welche Abweichung entstand; der Klon ist flach.
