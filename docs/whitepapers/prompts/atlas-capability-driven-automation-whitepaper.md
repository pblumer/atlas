# Prompt: Technisches Whitepaper «Capability-driven Automation»

> Dieser Prompt beauftragt die quellenbasierte Überarbeitung des bestehenden
> Whitepapers [«Capability-driven Automation»](../Atlas_Whitepaper_Capability_Driven_Automation.pdf).
> Er ist für einen Coding-Agenten mit Lesezugriff auf `pblumer/atlas` und
> `pblumer/atlas-cloud` bestimmt. Der Prompt ist der reproduzierbare Auftrag,
> **nicht** selbst das Whitepaper.
>
> **Faktenstand:** bei jeder Ausführung neu vom aktuellen `main`-Branch ermitteln.
> Das CDA-Programm dokumentierte zuletzt einen Basisstand vom 27. Oktober 2026.
> Diese Datierung ist keine Bestätigung des heutigen Implementierungsstands.
> Der vorhandene PDF-Dateiname wird als Bestandsdokument beibehalten.

---

## 1. Rolle, Zweck und Zielgruppen

Du bist technische Autorin bzw. technischer Autor sowie Enterprise-, BPMN-,
Security- und Distributed-Systems-Architekt. Erarbeite eine **aktualisierte,
technisch belastbare deutsche Fassung** des Whitepapers über
**Capability-driven Automation (CDA) in atlas**.

Erkläre den Weg vom fachlichen Bedarf über Business Capabilities,
Value Streams und BPMN-Prozesse bis zu präzise beschriebenen technischen
Operationen, Worker Types, konfigurierten Workern und autorisierten
externen Aktionen. Zeige die Grenzen der bestehenden Implementierung,
die vorgesehenen Ausbauschritte und ihre Abhängigkeiten.

Zielgruppen: (1) Enterprise- und Business-Architektur,
(2) Informationssicherheit, Governance und Betrieb,
(3) BPMN-/API-/Worker-Entwicklung. Fachwissen darf vorausgesetzt werden,
aber die zentralen Begriffe und ihre Beziehungen müssen erklärt werden.

**Wichtig:** Ein Whitepaper ist eine technische Einordnung, **kein**
Abnahmebeleg, keine Sicherheitszertifizierung und kein Nachweis der
Produktionstauglichkeit. Die Developer-Preview-Einschränkung aus
`README.md` ist in die Einordnung einzubeziehen.

## 2. Verbindliche Regeln

1. Lies zuerst `AGENTS.md`, `README.md`, `ROADMAP.md`,
   `docs/ARCHITECTURE.md` und `docs/architecture/invariants.md`
   in `pblumer/atlas` sowie `AGENTS.md` in `pblumer/atlas-cloud`.
   Beachte deren anwendbare Vorgaben.
2. Extrahiere zuerst den Text aus
   `docs/whitepapers/Atlas_Whitepaper_Capability_Driven_Automation.pdf`,
   prüfe Titel, Aufbau, Diagramme und zentrale Aussagen und halte fest,
   was fachlich übernommen, korrigiert, gekürzt oder ergänzt wird.
   Öffne bzw. rendere die PDF-Seiten, statt ausschliesslich aus dem
   Dateinamen auf den Inhalt zu schliessen. Ist die PDF nicht lesbar,
   kennzeichne dies und arbeite nur mit überprüfbaren Repository-Quellen.
3. **Keine erfundenen Fakten:** keine Benchmarkwerte, zertifizierten
   Konformitäten, produktiven Referenzen, Provider-Fähigkeiten,
   Releasezusagen, API-Endpunkte oder Sicherheitsgarantien ohne Beleg.
4. **Quelle vor Behauptung:** Prüfe für jede wichtige technische Aussage
   die passenden ADRs, die Implementierung und relevante Tests.
   Ein gemergter PR oder ein `Accepted`-ADR ist allein kein Beleg,
   dass die beschriebene Funktion vollständig nutzbar ist.
5. Verwende durchgehend die Statuskategorien
   **Umgesetzt**, **Teilweise umgesetzt**, **Geplant/offen** und
   **Einschätzung**. Die Kategorien gelten je Teilfunktion, nicht
   pauschal für das gesamte Programm. Trenne auch
   **authored**, **derived**, **observed**, **claimed** und
   **independently verified** dort, wo diese Unterscheidung relevant ist.
6. Bei Widersprüchen zwischen Programm, Roadmap, ADR, Code und Tests
   benenne die Abweichung mit Quellen. Bestätige nicht ungeprüft den
   historischen Programmstatus.
7. Arbeite ausschliesslich dokumentarisch. Keine Änderungen an Runtime,
   APIs, Tests, ADRs, Architekturinvarianten oder Beispielen.
8. Keine versteckten Architekturentscheide: Offene Designfragen,
   insbesondere dynamisches Routing und Rebinding, als offen ausweisen.

## 3. Pflichtquellen und Belegprüfung

Lies mindestens diese Quellen aus dem **aktuellen** Repository:

**Programm und Gesamtarchitektur**

- `docs/planning/capability-driven-automation-program.md` — CDA0 bis CDA8,
  Gates, Abhängigkeiten, Stand und offene Entscheide
- `docs/planning/technical-capability-contract.md` — technischer T0–T5-Plan
- `ROADMAP.md`, insbesondere Milestone CDA und abhängige Milestones
- `docs/ARCHITECTURE.md`,
  `docs/architecture/invariants.md`,
  `docs/architecture/glossary.md` und
  `docs/runtime-contract.md`
- `docs/adr/README.md` und gegebenenfalls aktuelle
  ADR-Status-Abgleichdokumente unter `docs/audits/`

**Business Capability, Value Stream und Panorama**

- ADR-0305 (Business Capabilities und Value Streams)
- ADR-0308 (Panorama und Capability-Register)
- ADR-0420 (ein Graph, mehrere Projektionen)
- `api/capability/` und `api/panorama/` sowie passende Tests

**Worker, technische Verträge und Evidenz**

- ADR-0203, ADR-0208 (Worker-Modell und Worker-Type-Pakete)
- ADR-0480 (operation-level descriptive claims)
- ADR-0485 (semantischer Technical-Capability-Vertrag)
- ADR-0483 (Governance und Conformance)
- `workertype/` einschliesslich Manifest-, Capability-, Contract-,
  Schema-, Digest- und Evidence-Code sowie zugehöriger Tests
- `docs/whitepapers/atlas-worker-whitepaper.md` zum Worker-Konzept;
  dessen möglicherweise veraltete Statusaussagen **neu verifizieren**

**BPMN-Bindung, Identität und Betrieb**

- ADR-0482 (Capability-aware Binding und Routing)
- ADR-0475 (identity-aware Worker Execution)
- ADR-0492 (kontrolliertes Worker-Rebinding)
- `docs/planning/identity-aware-execution-program.md`
- `docs/whitepapers/identity-aware-workflow-execution.md`
- relevante Implementierungen und Tests unter
  `compiler/`, `engine/`, `job/`, `authz/` und `api/`

**Betrieb und Präsentation**

- `docs/whitepapers/README.md` und die vorhandenen Whitepaper
- `docs/brand/README.md` sowie `pblumer/atlas-cloud`
  (siehe Gestaltungskapitel)
- Zuletzt gemergte, thematisch einschlägige PRs auf `main`
  nur als Wegweiser; fachliche Belege bleiben Code, Tests und ADRs.

Prüfe im ADR-Index, ob neue oder ersetzende ADRs zu Claims,
Conformance, Bindings, Discovery, Governance, Rebinding oder
Worker-Provenienz hinzugekommen sind. Veraltete Nummern und Pfade
nicht stillschweigend übernehmen.

## 4. Fachliche Leitplanken

Der folgende Zusammenhang ist die **zu überprüfende fachliche
Struktur**, nicht der Beweis einer vollständig implementierten Kette:

```text
Value Stream -> Stage -> Business Capability
Business Capability --authored realization--> BPMN Process
BPMN Service Task --explicit binding--> Worker Type Operation
Worker Type Operation --claim--> Technical Capability Contract
Claim + reviewed evidence --may establish--> Conformance
Configured Worker --realizes--> Worker Type
Worker Instance --leases--> durable Job
Identity + Policy --authorizes--> external action
```

Erläutere ausdrücklich:

- **Business Capability ist keine Technical Capability.** Eine
  Geschäftsleistung wird nicht aus einem API-Namen oder Worker-Aufruf
  abgeleitet. Die Business-Realisierung ist fachlich modelliert.
- **Ein Claim ist kein Konformitätsnachweis.** Ein Manifest-Claim ist
  zunächst deklarativ. JSON-Schema-Validierung allein beweist keine
  Provider-Semantik. Evidenz, Digest, Verifikation, Rücknahme und
  Gültigkeitsgrenzen getrennt behandeln.
- **Konformität ist keine Autorisierung.** Gleichwertige Operationen
  sind nicht automatisch berechtigt. Identität, Zielsystem,
  Credential-Scope, Policy und Genehmigungsentscheidung sind eigene Gates.
- **Ähnlich benannte Operationen sind nicht gleichwertig.**
  Entra-Konto, LDAP-Eintrag und Google-Kontakt nicht ohne nachgewiesene
  Semantik unter einen gemeinsamen Vertrag subsumieren.
- **Discovery ist nicht Routing.** Nachweisbare Read-only-Abfragen,
  BPMN-Deployment-Validierung, tatsächlich ausführbare Bindung und
  spätere Migration/Rebinding sind verschiedene Ausbaustufen.
- **Keine automatische Worker-Auswahl behaupten.** Erstes geplantes
  ausführbares Modell: explizite, verifizierte, gepinnte Bindung.
  Wechsel von Worker A zu B für *künftige* Jobs einer bestehenden
  Deployment-Version (CDA8) getrennt von automatischem per-job Routing
  behandeln; bestehende Jobs dürfen nicht rückwirkend umgeroutet werden.
- **Keine unerlaubte Graph-Offenlegung.** Kanten benötigen Herkunft,
  stabile Identitäten, zugriffsbeschränkte Traversierung und eine
  klare Trennung zwischen fachlich erfasst, abgeleitet und beobachtet.
- **BPMN bleibt BPMN.** Eine optionale atlas-Extension zur technischen
  Bindung darf BPMN-Semantik und bestehende Modelle nicht verändern.
- **Runtime-Invarianten gelten weiter:** keine Allokationen im Hot Path,
  `durable before visible` mit gemeinsamem `fsync`, Single Writer je
  Partition, genau ein deterministisches `applyToState`,
  `compile, don't interpret` sowie persistierte Events als Fakten.
  Autorisierungs-/Routing-Entscheide dürfen beim Replay nicht neu
  evaluiert werden. Keine externen Effekte vor `fsync`.
- **Daten- und Geheimnisschutz:** Keine Credentials oder
  personenbezogenen Provider-Payloads in Capability-Metadaten,
  BPMN-Modellen, Graphantworten oder WAL aufnehmen.

## 5. Verbindliche Gliederung

Schreibe **ca. 18–30 A4-Seiten** mit kurzen Absätzen und einem
Inhaltsverzeichnis. Kapiteltitel dürfen redaktionell angepasst werden,
die Themen müssen erhalten bleiben:

1. **Management Summary** — Geschäftsnutzen, technische Einordnung,
   drei zentrale Grenzen, Gesamtstatus
2. **Motivation und Problemstellung** — Business-/IT-Bruch,
   Abhängigkeiten und fehlende Nachweise
3. **Begriffe und semantische Grenzen** — Business Capability,
   Technical Capability, Contract, Claim, Evidence, Worker Type,
   Worker, Worker Instance, Job, Bindung
4. **Ist-Architektur von atlas** — Business Architecture, Panorama,
   BPMN-Engine, Worker und Identity-Aware Execution
5. **Zielbild CDA** — End-to-End-Nachweiskette mit den unterschiedlichen
   Typen von Kanten und Vertrauensgrenzen
6. **Capability-Modellierung und Value Streams** — authored Realization,
   Coverage, Gaps, kein automatischer Business-Nachweis
7. **Technische Capability-Verträge** — Version, Semantik, Ressourcen,
   Schemas, Idempotenz, Fehler und Kompatibilität
8. **Claims, Conformance und Governance** — Nachweisqualität,
   Provenienz, unabhängiges Review, Digests, Rücknahme und Grenzen
9. **Worker-Bindung und Konfiguration** — Identitäten, Versionen,
   konkrete Zielsysteme, Scope und Berechtigungen
10. **Architekturgraph und Discovery** — fachliche/technische
    Verknüpfungen, Sichtbarkeit, Impact, autorisierte Abfragen
11. **BPMN-Modellierung und Deployment-Prüfung** — optionale Bindung,
    Diagnostik, Legacy-Kompatibilität und Fail-closed-Verhalten
12. **Governed Execution und Recovery** — dauerhafte Entscheidung,
    Leasing, Autorisierung, `fsync`, Events und deterministischer Replay
13. **Kontrolliertes Worker-Rebinding** — CDA8 als eigene Stufe,
    Freigabe, Cutover, offene Jobs, Rollback, Multi-Partition-Fragen
14. **Sicherheit und Datenschutz** — Bedrohungen, Trust Boundaries,
    Geheimnisse, Berechtigungen, Restrisiken und Nachweislücken
15. **Praxisbeispiel und Negativfälle** — Employee Onboarding:
    Value Stream -> Identity Management -> BPMN -> verifizierte
    Operation -> autorisierter Worker; gesondert falsche
    Äquivalenz, fehlende Evidenz, verweigerte Berechtigung,
    eingeschränkte Sichtbarkeit und Neustart
16. **Roadmap und Abhängigkeiten** — CDA0–CDA8 als Tabelle mit
    *aktuell verifiziertem* Status, Meilenstein, Voraussetzung,
    Abnahme-Gate und offener Lücke
17. **Abgrenzung zu Alternativen und Trade-offs** — direkte
    Provider-Kopplung, manuelle Katalogpflege, statisches
    Job-Routing; Stärken und Nachteile fair darstellen
18. **Offene Fragen, Fazit, Glossar und Quellenverzeichnis** —
    ADRs mit Status und Implementierungsstand; dokumentierte
    Annahmen und Differenzen zur ursprünglichen PDF

Der Fall muss auch zeigen, was **heute bereits demonstrierbar**
ist und was erst nach CDA2–CDA8 möglich wäre. Kein fiktiver
End-to-End-Demostatus.

## 6. Diagramme und Nachvollziehbarkeit

Erzeuge mindestens fünf sorgfältig beschriftete **Inline-SVG-Diagramme**:

1. Business-to-technical-Kette mit Kantenlegende
   (authored / claimed / verified / derived / authorized).
2. System- und Vertrauensgrenzen mit Business Architecture,
   BPMN, Worker, externem Provider und Policy-Enforcement.
3. CDA0–CDA8-Abhängigkeitsplan, wobei Read-only-Discovery
   nicht als Voraussetzung für jede Engine-Implementierung
   dargestellt wird.
4. Sequenz für den geplanten gepinnten Job:
   Deployment-Prüfung -> durable Job -> Lease ->
   Autorisierung -> `fsync`-Gate -> externer Effekt ->
   Ergebnis -> Replay ohne erneute Aktion.
   Die tatsächliche Reihenfolge und den Abstraktionsgrad
   gegen Code/ADRs prüfen; keine unzutreffende Runtime-Semantik zeichnen.
5. Rebinding-Skizze mit A/B-Revisionen, expliziter Freigabe,
   Cutover und unveränderter Zuordnung alter Jobs.

In Diagrammen klar zwischen **real implementierter** und
**konzeptioneller** Architektur unterscheiden. Eigene Abbildungen
nicht mit Screenshots tatsächlicher UI verwechseln.
Kein Pfeil darf durch Beschriftungen verlaufen; keine
überlappenden Kanten, abgeschnittenen Texte oder
nicht erklärten Farben. Bei Platzproblemen auf mehrere
Abbildungen aufteilen statt verkleinern.

Jede grössere Statustabelle nennt Referenzen auf
beobachteten Code, Tests und ADRs. Links sind relativ zum
Markdown-Standort `docs/whitepapers/`.

## 7. Sprache und Publikationsstil

- Sachliches, formelles Deutsch (Schweiz); immer `ss`, nie `ß`,
  Umlaute ä/ö/ü verwenden.
- `atlas` als Wortmarke und im Fliesstext klein; `Atlas` nur
  am Satz- oder Überschriftsanfang. `atlas hub` klein.
- Keine Werbesprache, keine Superlative, keine Emojis,
  keine unbelegten Produkt- oder Zertifizierungsbehauptungen.
- Code-/API-Bezeichner in Monospace, Fachbegriffe beim ersten
  Auftreten erklären. Bei Vorschlägen «Einschätzung» schreiben.
- Gut lesbare Tabellen mit aussagekräftigen Spalten; nicht
  unlesbar viele Spalten in ein A4-Hochformat pressen.

## 8. Visuelles Design aus `pblumer/atlas-cloud`

Orientiere dich an
`docs/whitepapers/prompts/atlas-applikationen-whitepaper.md`
und der vorhandenen HTML-Vorlage
`docs/whitepapers/atlas-worker-whitepaper.html`.
Die **aktuelle** Website ist die Designquelle, nicht die
Gestaltung des alten Capability-PDF.

Verbindlich prüfen:

- `pblumer/atlas-cloud/internal/site/web/assets/site.css`
  (Tokens, Typografie, Karten, radialer Hintergrund),
  `internal/site/web/index.html`, `internal/site/web/brand/`,
  `docs/development/site.md`.
- `pblumer/atlas/docs/brand/README.md` und
  `docs/brand/atlas-glyph.svg`.

Übernimm die tatsächlichen Token-Werte von `site.css`
(`--ink`, `--panel`, `--panel2`, `--line`, `--muted`,
`--white`, `--blue`, `--mint`, `--shadow`). Benutze
Systemschriften ohne Webfont, dunklen Verlauf für die
Bildschirmfassung und ein zurückhaltendes Sternenfeld
**nur auf dem Titelblatt**. Wortmarke und kachelloses
`atlas-glyph.svg` markenkonform verwenden. Icons nur aus
den freigegebenen SVG-Quellen. Kein externes JavaScript,
kein CDN, keine verlinkten Laufzeitressourcen.

Leite eine **druckfreundliche helle Variante** mit gutem
Kontrast ab (für Fliesstext mindestens WCAG AA 4.5:1).
Status immer zugleich durch Text kennzeichnen,
niemals ausschliesslich durch Farbe. Einfache A4-
Seitenumbrüche, wiederkehrende Kopf-/Fusszeilen,
alle Diagramme als Inline-SVG, keine Raster-Screenshots
an Stelle der Fachdiagramme.

## 9. Ausgabedateien und Umgang mit dem Bestand

Erzeuge bei tatsächlicher **Ausführung dieses Prompts**
folgende Dateien unter `docs/whitepapers/`:

| Datei | Zweck |
|---|---|
| `atlas-capability-driven-automation.md` | Bearbeitbare, führende Quelle mit Quellenlinks und Status |
| `atlas-capability-driven-automation.html` | Selbständige HTML-Ausgabe im atlas-cloud-Design |
| `atlas-capability-driven-automation-dark.pdf` | A4-PDF für Bildschirm, dunkles Design |
| `atlas-capability-driven-automation-print.pdf` | A4-PDF für Druck, heller Grund |

Das historische
`Atlas_Whitepaper_Capability_Driven_Automation.pdf`
**nicht stillschweigend löschen oder überschreiben**.
Die Ausgabe als neue, überprüfbare Fassung kennzeichnen
und auf dem Titelblatt Publikationsdatum, Textversion
und überprüften Repository-Commit angeben.
Bei einer später ausdrücklich gewünschten Ablösung
des Alt-PDF zunächst Links, Referenzen und
Migrationswirkung prüfen.

Ergänze den Bibliothekseintrag in
`docs/whitepapers/README.md` nach vorhandener Konvention.
Dies ist eine **Dokumentationsaufgabe**: keine neuen
Runtime- oder Produktversprechen.

## 10. Herstellung, Qualitätsprüfung und Abschluss

1. Markdown als führende Quelle schreiben; HTML im
   atlas-cloud-Stil erzeugen. Vorhandene Bibliotheks-
   Werkzeugkette (Pandoc und WeasyPrint) bevorzugen.
   Falls nötig isolierte Abhängigkeiten ausserhalb
   des Repositories verwenden.
2. Alle Statusaussagen gegen den aktuellen `main`-Stand
   und die Tests/ADRs verifizieren; Widersprüche in
   einer Änderungsübersicht dokumentieren.
3. Relativlinks auf Existenz prüfen, inkl.
   verlinkter ADRs und Referenzbeispiele.
4. Deutschprüfung: kein `ß`; Marken-/Begriffskonsistenz.
5. Eigenständiges HTML bei 1440, 820 und 390 px
   visuell testen; keine horizontale Überläufe oder
   abgeschnittenen SVG-Texte.
6. **Beide PDFs vollständig rendern und mindestens
   Titelblatt, Diagrammseiten, Tabellen, Seitenumbrüche
   und Schlussseiten visuell prüfen.**
   Mit `pdfinfo` A4, Seitenzahl und Metadaten prüfen;
   keine leeren Seiten oder überlappenden Beschriftungen.
7. Verifizieren, dass beide PDFs und HTML keine externen
   Ressourcen für die Darstellung benötigen.
8. `gofmt -l .` darf keine Ausgabe liefern. Die
   vollständige Go-Suite ist für dokumentations-
   ausschliessliche Änderungen nicht erforderlich;
   wenn keine Go-Tests liefen, dies explizit melden.
9. Auf dem **vorgegebenen Arbeitsbranch** committen.
   Pull Request nur erstellen, wenn die auftraggebende
   Person dies ausdrücklich verlangt.

Abschlussbericht mit **Quellenstand/Commit**, generierten
Dateien, wichtigster fachlicher Abweichung zum Alt-PDF,
Status CDA0–CDA8, als «Einschätzung» gekennzeichneten
Aussagen, ungeklärten Punkten und Resultaten der
Layout-/Link-/PDF-Prüfung.
