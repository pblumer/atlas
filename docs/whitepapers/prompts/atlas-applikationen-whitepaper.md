# Prompt: Technisches Whitepaper «atlas-Applikationen»

> Dieser Prompt ist für einen Coding-Agenten bestimmt, der im Repository
> `pblumer/atlas` arbeitet und Lesezugriff auf `pblumer/atlas-cloud` hat.
> Er wird unverändert als Auftrag übergeben. Der Text unterhalb der Linie ist
> der Prompt.

---

## 1. Rolle und Auftrag

Du bist technische Autorin bzw. technischer Autor und Software-Architekt für atlas.
Erstelle ein **technisches Whitepaper in deutscher Sprache**, das erklärt, was eine
**atlas-Applikation (Prozessapplikation)** ist, wie sie aufgebaut ist, wie sie
entsteht, versioniert, veröffentlicht, verteilt, betrieben und gelöscht wird, und
wo die Grenzen des heutigen Implementierungsstands liegen.

Das Whitepaper übernimmt das **visuelle Design der öffentlichen Website aus
`pblumer/atlas-cloud`** und wird im Repository `pblumer/atlas` unter
`docs/whitepapers/` publiziert.

Zielgruppe: Architektinnen und Architekten, Entwicklerinnen und Entwickler von
Prozessapplikationen, Betreiber von atlas-Installationen sowie technisch
versierte Entscheidungsträger. Leseniveau: Fachpublikum, das BPMN und DMN kennt,
atlas aber nicht im Detail.

## 2. Verbindliche Arbeitsregeln

1. Lies zuerst `AGENTS.md` und `CLAUDE.md` in `pblumer/atlas` sowie `AGENTS.md`
   in `pblumer/atlas-cloud`. Deren Regeln haben Vorrang vor diesem Prompt.
2. **Nichts erfinden.** Jede technische Aussage muss auf eine Quelle im Repository
   zurückführbar sein (ADR, Architektur-Dokument, Planungsdokument, Code, Test,
   Beispiel). Keine erfundenen Kennzahlen, Performance-Werte, Kundenreferenzen,
   Reifegrade oder Zertifizierungen.
3. **Implementierungsstand ehrlich ausweisen.** Massgeblich sind die Spalten
   `Status` und `Implementation` in `docs/adr/README.md` sowie die
   «Implementation status»- bzw. «As built»-Abschnitte der einzelnen ADRs und
   `docs/audits/adr-status-reconciliation-2026-10-11.md`. Ein ADR mit
   `Implementation: Partial` darf nicht als vollständig umgesetzt beschrieben
   werden.
4. Verwende denselben **Leseschlüssel** wie
   `docs/whitepapers/atlas-hub-signierte-verteilung.md`:
   **Umgesetzt**, **Teilweise umgesetzt**, **Geplant/offen**, **Einschätzung**.
   Jede Einschätzung ist als solche zu kennzeichnen.
5. Wenn Quellen einander widersprechen oder eine Frage aus dem Repository nicht
   beantwortbar ist, halte das im Kapitel «Offene Punkte und Grenzen» fest,
   statt eine Antwort zu konstruieren.
6. Ändere **keinen** Code, keine ADRs und keine Beispiele. Der Auftrag ist rein
   dokumentarisch.

## 3. Faktenbasis (Pflichtlektüre)

Lies mindestens die folgenden Quellen vollständig, bevor du schreibst. Prüfe in
`docs/adr/README.md`, ob seitdem weitere ADRs zum Thema hinzugekommen sind
(Suchbegriffe: application, release, package, bundle, constant, deploy,
promotion, hub), und beziehe sie ein.

**Grundlagen und Abgrenzung**

- `docs/ARCHITECTURE.md`, `docs/architecture/invariants.md`,
  `docs/architecture/glossary.md`
- ADR-0034 (Projekte und Artefakte), ADR-0128 (Prozessapplikationen) und
  `docs/planning/0128-process-applications-plan.md`

**Releases, Deployments und Verteilung**

- ADR-0019 (dauerhafte Deployments), ADR-0129 (entfernte Deployment-Ziele),
  ADR-0134 (Git-gestützte Applikationen)
- ADR-0467 (Applikationspaket-Export und -Import), ADR-0437 (Import über die
  Kommandozeile), ADR-0468 (Löschen einer Applikation mit Inhalt)
- ADR-0499 (atlas hub, Blueprint-Registry) und
  `docs/whitepapers/atlas-hub-signierte-verteilung.md` — **verweisen, nicht
  wiederholen**: Das Applikations-Whitepaper fasst die Verteilung über atlas hub
  in einem Abschnitt zusammen und verlinkt das Hub-Whitepaper für Details.

**Konfiguration, Inhalte und Laufzeitbezug**

- ADR-0453 (Werte pro Installation), ADR-0454 (Prozesskonstanten),
  ADR-0456 (Namensräume von Konstanten)
- ADR-0319, ADR-0322, ADR-0327, ADR-0329 (Entscheidungs-Deployments)
- ADR-0373 (veröffentlichte Prozessschnittstelle), ADR-0466 (Startansicht nach
  Applikation), ADR-0520 (Bestellung im Namen der Applikation)
- ADR-0471, ADR-0495, ADR-0528 (Release-Architektur und Bundle-Inhalt)
- ADR-0208 und `docs/whitepapers/atlas-worker-whitepaper.md` für die Rolle der
  Worker (Begriff «Worker», nicht «Connector», gemäss `AGENTS.md`)

**Code und Belege** (zur Verifikation, nicht zur Paraphrase von Implementierungsdetails)

- `api/apppackage.go`, `api/packagemappings.go`, `api/promote.go`,
  `api/heldconstants.go`, `api/constantsimport.go`, `api/releasearchitecture*.go`
  und die zugehörigen `*_test.go`
- `examples/README.md` und ein bis zwei vollständige Beispielapplikationen unter
  `examples/` als durchgehendes Fallbeispiel

## 4. Inhaltliche Gliederung

Halte dich an diese Gliederung. Kapitel dürfen zusammengelegt werden, wenn die
Faktenbasis für ein Kapitel nicht ausreicht; das ist dann im Text zu begründen.

1. **Management Summary** (maximal eine Seite, inklusive Statusaussage)
2. **Ausgangslage und Problem** — warum einzelne BPMN-Dateien als Einheit nicht
   genügen
3. **Begriffe und Abgrenzung** — Projekt, Applikation, Artefakt, Release,
   Deployment, Deployment-Ziel, Paket, Blueprint, Instanz; Abgrenzung zu
   Prozessdefinition und Prozessinstanz
4. **Architekturüberblick** — Einordnung als Design-Time-Schicht oberhalb der
   Engine; ausdrücklich darstellen, dass Applikationen weder im Event-Log noch in
   `applyToState` erscheinen (gemäss ADR-0128 und den Invarianten)
5. **Bestandteile einer Applikation** — BPMN, DMN, Formulare, Konstanten,
   Werte pro Installation, Architektur-Skizze, Abhängigkeiten zu Workern
6. **Lebenszyklus** — Entwurf, Validierung, Release, Deployment, Promotion auf
   andere Installationen, Export/Import, Löschen
7. **Versionierung und Migration** — Verhältnis von Applikationsversion zu
   Prozessversionen; was mit laufenden Instanzen geschieht und was nicht
8. **Konfiguration und Konstanten** — Namensräume, Bindung pro Installation
9. **Verteilung** — Paketformat, Konfliktbehandlung beim Import, atlas hub
   (Kurzfassung mit Verweis)
10. **Sicherheit und Berechtigungen** — nur soweit durch Quellen belegt
11. **Betrieb** — Werkzeuge (Console, CLI, MCP-Tools, HTTP-API), Beobachtbarkeit
12. **Durchgehendes Fallbeispiel** anhand einer realen Applikation aus `examples/`
13. **Offene Punkte, Grenzen und Ausblick** — insbesondere die als `Partial`
    oder `Proposed` geführten ADRs
14. **Glossar und ADR-Verzeichnis** — Tabelle mit ADR-Nummer, Titel, Status,
    Implementierungsstand und Link

Umfang: Richtwert 15 bis 25 A4-Seiten im PDF. Kurze, fokussierte Absätze.
Mindestens drei Diagramme: Architektur-Einordnung, Lebenszyklus (Zustände und
Übergänge), Verteilungsweg zwischen Installationen.

## 5. Sprache und Schreibregeln

- Deutsch in sachlicher, formeller Sprache, Schweizer Schreibweise: **kein «ß»,
  immer «ss»**; Umlaute ä, ö, ü werden verwendet.
- Neutral und wertungsarm; keine Marketing-Sprache, keine Superlative, keine
  Emojis.
- Markenschreibweise gemäss `docs/brand/README.md`: `atlas` immer
  kleingeschrieben, `Atlas` nur am Satz- oder Überschriftsanfang. `atlas hub`
  kleingeschrieben.
- Fachbegriffe beim ersten Auftreten kurz erklären; englische Bezeichner aus
  Code und API in `Code-Schreibweise` belassen.
- Quellenangaben als relative Links auf ADRs und Dokumente im Repository.

## 6. Design (aus `pblumer/atlas-cloud`)

Übernimm das Design der Website, nicht deren Inhalte oder Interaktionen.

**Massgebliche Quellen**

- `pblumer/atlas-cloud`: `internal/site/web/assets/site.css` (Design-Tokens,
  Typografie, Karten, Hintergrund), `internal/site/web/index.html`
  (Aufbau, Überschriften-Hierarchie, Icon-Sprite), `internal/site/web/brand/`
  und `docs/development/site.md` (Markenregeln, Icon-Regeln)
- `pblumer/atlas`: `docs/brand/README.md` (Logo-Regeln, Wortmarke, Farben)
- **Bestehende Vorlage:** `docs/whitepapers/atlas-worker-whitepaper.html`
  verwendet bereits die atlas-cloud-Tokens und enthält Druckregeln. Nutze sie als
  technische Ausgangsbasis, damit die Whitepaper-Bibliothek einheitlich bleibt.
  Gleiche Abweichungen gegenüber dem aktuellen `site.css` zugunsten von
  `atlas-cloud` ab und dokumentiere sie im Commit.

**Verbindliche Gestaltungselemente**

- Design-Tokens exakt aus `site.css` übernehmen (`--ink`, `--panel`, `--panel2`,
  `--line`, `--muted`, `--white`, `--blue`, `--mint`, `--shadow`) und
  ausschliesslich über diese Variablen färben.
- Systemschrift-Stack wie auf der Website; keine externen Webfonts.
- Dunkler Hintergrund mit dem radialen Verlauf der Website; Sternenfeld nur
  dezent und nur auf dem Titelblatt.
- Titelblatt mit tile-losem `atlas-glyph.svg` (gemäss Markenregeln auf dunklem
  Grund), Wortmarke `atlas`, Titel, Untertitel, Stand der Faktenbasis, Version,
  Einordnung.
- Statuskennzeichnung (Umgesetzt / Teilweise umgesetzt / Geplant/offen /
  Einschätzung) als konsistente, farblich und textlich unterscheidbare Marken;
  Farbe darf nicht das einzige Unterscheidungsmerkmal sein.
- Icons nur aus `internal/site/web/brand/icons/` bzw. `docs/brand/icons/`,
  unverändert, als Inline-SVG mit `currentColor`.
- Diagramme als Inline-SVG im selben Farbsystem; Beschriftungen bleiben
  innerhalb ihrer Formen; keine Rastergrafiken für Diagramme.
- Kein JavaScript, keine externen Ressourcen, keine Tracking- oder CDN-Links.

## 7. Zu erzeugende Dateien

Alle Dateien liegen in `docs/whitepapers/` des Repositorys `pblumer/atlas`:

| Datei | Zweck |
|-------|-------|
| `atlas-applikationen.md` | Markdown-Quelle, massgeblicher Text |
| `atlas-applikationen.html` | Eigenständige HTML-Ausgabe im atlas-cloud-Design |
| `atlas-applikationen-dark.pdf` | Bildschirm-PDF, dunkles Design, A4 |
| `atlas-applikationen-print.pdf` | Druckfreundliches PDF, heller Grund, A4 |

Zusätzlich: Eintrag im Abschnitt «Library» von `docs/whitepapers/README.md`
im selben Format wie die bestehenden Einträge (Titel, Links, Sprache, Datum,
Einordnung, Hinweis «not acceptance evidence»).

Erstelle **kein** neues Verzeichnis `docs/whitepaper/` (Singular); der README
schliesst das ausdrücklich aus.

## 8. Erzeugung der PDFs

- Verwende die bestehende Werkzeugkette der Bibliothek: Markdown → HTML mit
  `pandoc`, HTML → PDF mit **WeasyPrint** (so wurden die vorhandenen PDFs
  erzeugt). Ist WeasyPrint nicht installiert, installiere es in einer
  isolierten Python-Umgebung ausserhalb des Repositorys.
- Druckvariante: heller Grund, dunkle Schrift, gleiche Typografie und
  Hierarchie; Farbwerte aus den Tokens ableiten und Kontrast nach WCAG AA
  (mindestens 4.5:1 für Fliesstext) einhalten.
- Seitenumbrüche: Titelblatt allein auf Seite 1, keine Überschrift am
  Seitenende, Tabellen und Diagramme nicht umbrechen.
- Build-Skripte und temporäre Dateien gehören **nicht** ins Repository, es sei
  denn, ein bestehendes Skript wird erweitert.

## 9. Qualitätsprüfung vor dem Commit

1. Jede Statusaussage gegen `docs/adr/README.md` abgleichen; Abweichungen
   korrigieren.
2. Alle relativen Links prüfen (keine toten Links).
3. Rechtschreibung: kein «ß» in allen erzeugten Dateien
   (`grep -c 'ß'` muss `0` liefern); Wortmarke `atlas` kleingeschrieben.
4. HTML mit Chromium/Playwright bei 1440 px und 390 px Breite rendern und
   visuell prüfen: kein horizontales Überlaufen, Diagrammbeschriftungen
   innerhalb ihrer Formen.
5. Beide PDFs öffnen bzw. mit `pdfinfo` und Seitenvorschau prüfen:
   A4, plausible Seitenzahl, Titelblatt korrekt, keine leeren Seiten.
6. `gofmt -l .` liefert keine Ausgabe (nur zur Bestätigung, dass kein Go-Code
   berührt wurde). Die vollständige Go-Testsuite ist für eine reine
   Dokumentationsänderung nicht erforderlich; das ist im Commit ausdrücklich zu
   vermerken.

## 10. Abschluss

- Committe auf dem vorgegebenen Arbeits-Branch mit einer beschreibenden
  Commit-Nachricht (z. B. `docs: publish atlas applications whitepaper`) und
  pushe ihn. Erstelle **keinen** Pull Request, sofern nicht ausdrücklich
  verlangt.
- Berichte zum Schluss:
  - welche Quellen verwendet wurden,
  - welche Aussagen als **Einschätzung** markiert sind,
  - welche Fragen aus dem Repository nicht beantwortet werden konnten,
  - welche Abweichungen zwischen dem Design der Worker-Vorlage und
    `atlas-cloud` bestanden und wie sie aufgelöst wurden.
