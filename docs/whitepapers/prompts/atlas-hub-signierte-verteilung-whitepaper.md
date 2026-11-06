# Prompt: Technisches Whitepaper «atlas hub – signierte Verteilung von Prozessapplikationen»

> Dieser Prompt hat das Whitepaper
> [`atlas-hub-signierte-verteilung.md`](../atlas-hub-signierte-verteilung.md) in Auftrag
> gegeben. Er wird unverändert als Auftrag übergeben; der Text unterhalb der Linie ist der
> Prompt.
>
> **Herkunft und Stand.** Die erste Fassung entstand am 6. November 2026 für einen
> Assistenten **ohne** Zugriff auf das Repository (ChatGPT). Deshalb trägt sie die geprüfte
> Faktenbasis selbst in sich. Diese aktualisierte Fassung vom selben Tag enthält die
> Korrekturen, die beim Abgleich der ersten Ausgabe mit dem Code gefunden wurden (siehe
> Anhang). Ausserdem beschreibt sie die Pflege des Whitepapers. Sie eignet sich für beide
> Wege:
>
> - für ein Werkzeug ohne Repository-Zugriff, das sich nur auf Abschnitt 4 stützt;
> - für einen Coding-Agenten im Repository `pblumer/atlas`, der Abschnitt 4 gegen die
>   Quellen in Abschnitt 3 prüft. Bei Abweichungen gehen die Quellen vor.

---

## 1. Rolle und Auftrag

Du bist technische Autorin bzw. technischer Autor mit Hintergrund in Softwarearchitektur,
IT-Sicherheit (Supply-Chain-Sicherheit, Kryptografie, Netzwerksicherheit) und
Prozessautomatisierung (BPMN, DMN). Du schreibst für ein fachkundiges Publikum, präzise,
nachvollziehbar und ohne Marketing.

Der Auftrag hat zwei Formen. Welche gilt, steht in der Übergabe; fehlt die Angabe, gilt
**B**, sobald `docs/whitepapers/atlas-hub-signierte-verteilung.md` existiert.

- **A. Neue Ausgabe.** Erstelle ein ausführliches technisches Whitepaper in deutscher
  Sprache. Es erklärt, wie der **atlas hub** funktioniert:
  - wie Beispiel- und Fachapplikationen als signierte, unveränderliche Pakete veröffentlicht
    werden;
  - wie eine atlas-Installation sie findet, prüft und kontrolliert importiert;
  - welche Sicherheits- und Betriebsentscheide dahinterstehen.
- **B. Überarbeitung.** Prüfe das bestehende Whitepaper gegen den aktuellen Stand der
  Quellen, insbesondere gegen neue Nachträge zu ADR-0499 und ADR-0467. Korrigiere, was
  nicht mehr stimmt, ergänze, was neu ist, und führe die Pflegemarken nach (Abschnitt 9).
  Erhalte dabei Gliederung, Leseschlüssel und Ton, soweit sie noch passen.

Zielpublikum:

1. Architektinnen und Architekten sowie technische Entscheidungsträger, die beurteilen
   wollen, ob das Verteilmodell sicher und betreibbar ist.
2. Betreiberinnen und Betreiber von atlas-Installationen: Konfiguration,
   Schlüsselverwaltung, Rotation, Störungsfälle.
3. Entwicklerinnen und Entwickler, die Pakete veröffentlichen oder Clients (Console, CLI,
   MCP-Agent) verstehen wollen.

## 2. Verbindliche Arbeitsregeln

1. Mit Repository-Zugriff: Lies zuerst `AGENTS.md` und `CLAUDE.md`. Deren Regeln gehen
   diesem Prompt vor.
2. **Nichts erfinden.** Jede technische Aussage muss auf Abschnitt 4 oder auf eine Quelle
   aus Abschnitt 3 zurückführbar sein. Fehlt für einen Abschnitt eine Information, schreibe
   das ausdrücklich, etwa «In den vorliegenden Unterlagen nicht spezifiziert», statt zu
   ergänzen. Erfinde keine Zahlen, Benchmarks, Kunden, Zitate, Zertifizierungen oder
   Termine.
3. **Implementierungsstand ehrlich ausweisen.** Verwende den Leseschlüssel **Umgesetzt**,
   **Teilweise umgesetzt**, **Geplant/offen** und **Einschätzung**. Stelle Geplantes nie
   als vorhanden dar. Massgeblich sind die Spalten `Status` und `Implementation` in
   `docs/adr/README.md` und die «As built»-Abschnitte der ADRs.
4. **Annahmen kennzeichnen.** Eine Folgerung, die nicht direkt in den Quellen steht, ist
   als **Einschätzung** zu markieren.
5. Widersprechen sich Quellen, oder ist eine Frage nicht beantwortbar, halte das im
   Kapitel «Offene Punkte, Grenzen und Ausblick» fest, statt eine Antwort zu konstruieren.
6. Nenne **keine** geheimen Werte: keine privaten Schlüssel, Secrets oder Passwörter, auch
   keine echt aussehenden Platzhalter.
7. Ändere **keinen** Code, keine ADRs und keine Beispiele. Der Auftrag ist rein
   dokumentarisch. Die einzige Ausnahme ist der Pflege-Test in Abschnitt 9: Er wird nur
   ergänzt, wenn das Whitepaper einen neuen, im Code erzwungenen Grenzwert nennt.

## 3. Quellen (Pflichtlektüre mit Repository-Zugriff)

Lies mindestens diese Quellen vollständig. Prüfe in `docs/adr/README.md`, ob weitere ADRs
zum Thema hinzugekommen sind (Suchbegriffe: hub, blueprint, registry, package, import,
signing), und beziehe sie ein.

- `docs/adr/0499-blueprint-registry-and-distribution.md` mit allen Nachträgen
- `docs/adr/0467-application-package-export.md` mit allen Nachträgen, besonders «Another
  application's artifact is adopted, never taken» und «A model other applications use is
  replaced only when named»
- `docs/contracts/blueprint-registry/v1/README.md` und `index.schema.json`
- `docs/planning/blueprint-marketplace-program.md`
- `examples/hub/README.md`, `examples/hub/bundles.json` und `examples/hub/releases.json`
- Code: `api/blueprintregistry/` (`verify.go`, `client.go`, `package.go`, `publish.go`,
  `rotation.go`), `api/blueprints.go`, `api/appsource.go`, `api/importplan/`,
  `cmd/atlas/hub.go`, `cmd/atlas/importplan.go`, `mcp/tools_hub.go`
- Console: `api/web/blueprint-marketplace.js`, `api/web/packageimport.js`,
  `api/web/importchoices.js`
- Workflows: `.github/workflows/pages.yml` und `.github/workflows/adr-number.yml`
- Weitere ADRs: 0128, 0134, 0081, 0208, 0034, 0016, 0506 und 0528

## 4. Geprüfte Faktenbasis (Stand 6. November 2026)

### A. Einordnung und Begriffe

- atlas ist eine BPMN-2.x-Workflow-Engine als einzelnes Go-Binary («model, run, and replay
  processes with no database and no broker»).
- Prozessapplikationen bündeln Prozesse (BPMN), Formulare, Entscheidungen
  (DMN-Referenzen) und Entscheidungsmodelle. Sie haben einen portablen Schlüssel (`key`),
  eigene Releases und eigene Deployments.
- **atlas hub** ist seit 2026-10-28 der Produktname für das Finden, Verteilen und den
  Lebenszyklus wiederverwendbarer atlas-Inhalte. Technisch heisst der Vertrag weiterhin
  **Blueprint Registry v1**; die Bezeichner `blueprintId`, `/api/v1/blueprints` und
  `ATLAS_BLUEPRINT_REGISTRY_*` bleiben aus Kompatibilitätsgründen.
- Ein **Blueprint-Release** ist eine unveränderliche, versionierte Verteilhülle um das
  bestehende **Applikationspaket** (ADR-0467). Ein neues Paketformat gibt es nicht.
- **Identität eines Release:** Vertrauen in den Signierschlüssel der Registry,
  `publisherId`, `blueprintId`, `version` und `digest`. Die installierte Prozessapplikation
  ist davon getrennt und hat ihren eigenen Schlüssel, lokale Änderungen, Releases und
  Deployments.
- **Eine Installation pro Server:** Im MVP ist ein Blueprint pro Server einmal
  installierbar, mit seinem Applikationsschlüssel. Mehrere unabhängige Kopien bräuchten
  einen eigenen Entscheid über das Umbenennen von Prozess-, DMN-, Formular- und
  Katalog-IDs.
- **Keine Migration:** Ein Blueprint-Update migriert nie laufende Prozessinstanzen. Ein
  fehlgeschlagenes Update lässt bestehende Deployments unverändert.
- **Grundsatzentscheid (ADR-0499, Option 2):**
  - Öffentliches Protokoll, Registry-Client und Lebenszyklus gehören ins öffentliche
    atlas-Repository. Registries werden unabhängig gehostet, öffentlich oder privat.
  - Allfällige kommerzielle Dienste wie Kauf und Berechtigungen sind getrennt. Gekaufte
    Applikationen laufen offline weiter, und keine Prozessausführung prüft eine Lizenz beim
    Hub.
  - Verworfen wurden: Katalog, Kasse und Lizenzen in atlas selbst; Verteilung nur über das
    Klonen von GitHub-Repositories; ein fest ins Binary eingebauter Katalog.

### B. Registry-Vertrag v1 (umgesetzt)

- **Statische HTTPS-Dateien:** `index.json`, `index.json.sig` und die Pakete unter
  `packages/…tar.gz`. Die öffentliche Beispiel-Registry liegt unter
  `https://pblumer.github.io/atlas/hub/index.json` (GitHub Pages). Jeder HTTPS-Server ist
  möglich; GitHub ist keine Voraussetzung.
- **Index:** `index.json` enthält `schemaVersion`, `registryId` und `releases`. Jedes
  Release hat:
  - `blueprintId`, `version` (SemVer), `publisherId`, `title`, `description` und `license`;
  - `atlas` mit `minVersion` (inklusiv) und optional `maxVersionExclusive` (exklusiv);
  - `artifact` mit relativem `path`, `digest` (`sha256:` und 64 Hex-Zeichen) und
    `sizeBytes`;
  - `needs` mit `workerTypes` und `serverConstants`;
  - optional `tags`.
- **Validierung:** `blueprintId` beginnt mit `publisherId + "."`; (`blueprintId`,
  `version`) ist im Index eindeutig. Unbekannte JSON-Felder, doppelte Objektschlüssel und
  nicht unterstützte Hauptversionen werden abgelehnt (fail closed). Leere oder umgekehrte
  Versionsbereiche werden abgelehnt, und bei Inkompatibilität wird nicht still auf eine
  andere Version ausgewichen.
- **`needs`** sind Hinweise, keine Garantien. Die tatsächlichen Bedürfnisse ermittelt der
  Import aus den importierten Modellen.
- **`tags`** (Nachtrag 2026-11-02):
  - höchstens 16 eindeutige Kleinbuchstaben-Labels mit 1 bis 32 Zeichen (`[a-z0-9]`,
    Bindestriche nur innen);
  - sie werden mitsigniert und steuern nur die Darstellung, nämlich den Filter in der
    Console;
  - sie berechtigen zu nichts und beschreiben nicht verbindlich, was ein Paket tut;
  - ein Server, der älter ist als dieser Nachtrag, lehnt einen Index mit `tags` ab.
- **Artefaktpfade** sind relativ zum Index-Verzeichnis: keine absoluten URLs, kein
  Traversal, keine Prozent-Escapes, nur derselbe HTTPS-Ursprung.
- **Beispiel-Registry:** Kennung `io.github.pblumer.atlas.hub`, Herausgeber
  `io.github.pblumer.atlas`, Lizenz der Beispiele `AGPL-3.0-only`.

### C. Kryptografie und Vertrauen (umgesetzt)

- **Signatur:** Die Registry signiert die **exakten Bytes** von `index.json` mit
  **Ed25519**. `index.json.sig` ist die Base64-Kodierung (RFC 4648, ohne Zeilenumbruch) der
  64-Byte-Signatur. Vor der Prüfung wird nichts neu serialisiert.
- **Digest:** Jedes Paket ist über den signierten SHA-256-Digest der **exakten
  komprimierten Archivbytes** gebunden.
- **Was das beweist:** Signierter Index und Digest belegen, dass diese Bytes aus dem
  Katalog stammen, dem der Betreiber vertraut. Sie belegen **nicht**, dass BPMN, Skripte
  oder Worker Types harmlos sind; dafür gelten weiterhin die bestehenden Prüf- und
  Deploy-Regeln.
- **Gepinnter Schlüssel:** Der Betreiber beschafft den öffentlichen Schlüssel ausserhalb
  der Registry (out of band) und pinnt ihn. Einem Schlüssel aus heruntergeladenem Inhalt
  wird nie vertraut; Trust-on-first-use gibt es nicht. Ein unsignierter oder falsch
  signierter Index wird abgelehnt, nicht als «unverifiziert» akzeptiert.
- **Abgewogene Alternativen** (ADR-0499):
  - Gepinnter Schlüssel statt nur HTTPS: HTTPS identifiziert den Server, nicht die
    Herausgabestelle über Spiegel hinweg. Der Preis ist Aufwand für Pinning und Rotation.
  - Signierte Rohbytes statt kanonischem JSON: in allen Sprachen einfach prüfbar. Dafür
    braucht jede Formatänderung eine neue Signatur.
  - Signierter Index statt einzeln signierter Pakete: Signaturen pro Herausgeber erst nach
    Klärung von Herausgeber-Identität und Widerruf.
  - Artefakte vom selben Ursprung statt beliebiger CDN-URLs: Das schränkt spätere
    kostenpflichtige Auslieferung ein, vermeidet aber eine breite SSRF- und
    Credential-Weiterleitungsfläche.
- **Herausgeber:** Ein bekannter Herausgeber ist vom Registry-Betreiber bestätigt, nicht
  durch DNS-artige Bezeichner bewiesen.

### D. Netzwerk- und Archivsicherheit (umgesetzt)

- **Konfiguration:** Nur ein privilegierter Administrator setzt URL und Schlüssel, über
  Umgebungsvariablen vor dem Start:
  - `ATLAS_BLUEPRINT_REGISTRY_URL`;
  - `ATLAS_BLUEPRINT_REGISTRY_KEY`: Base64 der 32 Bytes des Ed25519-Public-Keys, einer oder
    zwei Schlüssel.

  Es gibt kein Formular, in dem Benutzer URLs eintragen. Fehlen beide Variablen, ist der
  Hub deaktiviert; eine unvollständige oder ungültige Konfiguration verhindert den
  Serverstart.
- **HTTPS-Client:**
  - keine Redirects;
  - Verbindungsaufbau mit 5 Sekunden Timeout, die gesamte Anfrage mit 25 Sekunden;
  - nur öffentliche DNS-Hosts auf Port 443;
  - die DNS-Antwort darf nicht leer sein und höchstens 16 Adressen enthalten.

  Ist eine Adresse nicht öffentlich, wird die Verbindung abgelehnt. Dazu zählen private,
  Loopback-, Link-Local- und unspezifizierte Adressen, reservierte Dokumentations-,
  Benchmark- und Carrier-Grade-NAT-Bereiche sowie die entsprechenden IPv6-Präfixe. Das
  schützt vor SSRF und DNS-Rebinding.
- **Grenzwerte** (im Code erzwungen):

  | Gegenstand | Grenze |
  | --- | --- |
  | Signierter Index | höchstens 1 MiB |
  | Releases pro Index | höchstens 512 |
  | Signaturdatei | höchstens 88 Bytes |
  | Komprimiertes Paket | höchstens 50 MiB |
  | Entpackter Inhalt | höchstens 200 MiB |
  | Archiveinträge | höchstens 2000 |
  | Manifest | höchstens 2 MiB |
  | Katalogdokument | höchstens 16 MiB |

- **Archivprüfung (tar.gz):** Pfade, Eintragstypen, Grössen und GZIP-Integrität werden
  geprüft, ohne Daten auf die Festplatte zu schreiben. Symlinks und Tar-Header werden nicht
  implizit vertraut.
- **Getrennt von der Engine:** Alle Netzwerk- und Validierungsschritte laufen ausserhalb
  der Ausführungsschleife der Engine (run loop). Engine, Write-Ahead-Log und
  `applyToState` bleiben unberührt. Ein Ausfall des Registry-Hosts unterbricht keine
  deployte Applikation, und das Entfernen einer Registry ist kein Lizenzwiderruf zur
  Laufzeit.

### E. Veröffentlichen: Herausgeberseite (umgesetzt)

- **Befehle im atlas-Binary:**
  - `atlas hub keygen` erzeugt den privaten Schlüssel mit Dateimodus 0600, überschreibt nie
    und gibt Public Key und Fingerprint aus.
  - `atlas hub fingerprint` prüft eine wiederhergestellte Sicherung gegen den erwarteten
    Public Key.
  - Ausserdem `atlas hub lock`, `atlas hub build`, `atlas hub announce` und
    `atlas hub verify-announcement`.
- **Bundles** (`examples/hub/bundles.json`): Kleine Beispiele werden nach Thema zu wenigen
  Applikationen gebündelt. Derzeit sind es 13 Bundles:
  - fünf Themen-Bundles: «Beispiele: Einstieg», «Auftrag & Finanzen», «Personal &
    Mitarbeitende», «Umsysteme» und «Identität & Verzeichnis»;
  - drei Leitbeispiele aus dem Handbuch: Bewerbermanagement, Lehrgang Mitarbeitereintritt
    und Onboarding-Tutorial;
  - fünf Shop-Pakete mit Produktkatalogen: Dienstleistungen der Verwaltung,
    Microsoft-365-Dienste, Active-Directory-Dienste, Lizenzmanagement und
    Informationstransfer.

  Mit Repository-Zugriff ist die Anzahl in `bundles.json` nachzuzählen.
- **Bundle-Regeln** (per Test geprüft):
  - Jedes installierbare Handbuch-Beispiel ist in genau einem Bundle.
  - Ein Bundle aus mehreren Beispielen braucht einen Namen; daraus wird der portable
    Schlüssel abgeleitet.
  - Ein Shop-Paket reist allein.
  - Innerhalb eines Bundles ist jede Prozess-, Formular- und Entscheidungs-ID eindeutig.
- **Tags:** Der Build ergänzt pro verwendetem Worker Type einen Tag, zum Beispiel `entra`
  oder `jira`, und den Tag `shop`, wenn ein Katalog dabei ist.
- **Deterministische Pakete:** sortierte USTAR-Einträge, feste Zeitstempel und Dateimodi.
  Dieselben Quellen ergeben byte-identische Archive und damit denselben Digest.
- **Release-Lock** (`examples/hub/releases.json`):
  - Inhalt: Registry- und Herausgeber-Identität, Lizenz, Public Key und für jedes Bundle
    jede je veröffentlichte Kombination aus Version, Digest und `minAtlas`.
  - Einträge werden nur angehängt, nie umgeschrieben.
  - Ein Test schlägt fehl, sobald ein Bundle-Inhalt nicht mehr zu seiner letzten
    erfassten Version passt.
  - `atlas hub lock` hängt für jedes geänderte Bundle die nächste Patch-Version an.
  - Der Workflow, der ADR-Entwürfen ihre Nummer zuteilt und dabei Verweise in
    Beispieldateien umschreibt, führt `hub lock` gleich mit aus.
- **Veröffentlichung:** Der Workflow `pages.yml` baut die Registry nach `hub/` der
  Pages-Site, wenn das Repository-Secret `ATLAS_HUB_SIGNING_KEY` gesetzt ist; ohne Secret
  wird der Schritt übersprungen. `build` verweigert einen Schlüssel, dessen öffentliche
  Hälfte nicht zum `publicKey` im Release-Lock passt.
- **Schlüsselzeremonie** (einmalig, auf einer vertrauenswürdigen Maschine):
  1. Schlüssel erzeugen.
  2. Zwei Offline-Sicherungen an zwei Orten anlegen: Papier sowie ein verschlüsseltes
     Medium oder ein Passwortmanager-Eintrag. Beide werden mit Fingerprint, Datum und
     `registryId` beschriftet; die Passphrase liegt nie beim Medium.
  3. Die Sicherungen mit `fingerprint` prüfen, nach dem Erstellen und danach jährlich.
  4. Den Public Key in den Release-Lock eintragen.
  5. Das Secret setzen und die Arbeitskopie löschen.

  Das GitHub-Secret ist nicht auslesbar. Ohne Sicherung bedeutet ein Verlust eine
  Rotation, bei der jede Installation neu konfiguriert werden muss.
- **Einschränkung:** Veröffentlicht wird nur die neueste Version eines Bundles; ältere
  Archive werden nicht aufbewahrt. Gepinnte Updates über Versionen hinweg gehören zu BMP2
  und sind offen.

### F. Importieren: Installationsseite (umgesetzt)

- **Console:** Applikationsübersicht → «Create new → Browse atlas hub…». Die Liste lässt
  sich nach Tags filtern.
  1. `GET /api/v1/blueprints` liefert die Liste. Der Index wird vor jeder Antwort gegen den
     gepinnten Schlüssel geprüft.
  2. `GET /api/v1/blueprints/{id}/releases/{version}/preview` lädt Index und Paket neu und
     prüft Signatur, Digest, atlas-Kompatibilität und Archiv. Die Vorschau zeigt
     Applikationsname, Dateien, Kataloge und Produkte, deklarierte Bedürfnisse und den
     Digest; geschrieben wird nichts. Der Hinweistext sagt, dass Pakete mit Katalogen ein
     Publizieren auslösen können.
  3. Nach einer Bestätigung («Import … from the trusted registry?») holt die Console
     `GET …/package` mit dem geprüften Digest als `If-Match`-Bedingung. Hat sich der Digest
     geändert, antwortet der Server mit HTTP 412.
  4. Die verifizierten Bytes gehen an denselben Paketimport wie eine hochgeladene Datei
     (ADR-0467): Probelauf (`dryRun`), Entscheiddialog, Quellimport, Konstanten, bei
     Shop-Paketen die Fragen zu Zielgruppen und Genehmigenden.
  5. **Publizieren:** Ohne Produktkatalog publiziert der Import nicht; das bleibt ein
     eigener Schritt. Bringt das Paket Produktkataloge mit, publiziert die Console die
     Applikation im selben Ablauf, wenn sie noch kein Release hat oder der Import etwas
     geändert hat. Grund: Die Produkte werden an die dabei deployten Prozesse gebunden.
     Danach importiert die Console die Kataloge.
- **Serverseitiger Import** (Nachtrag 2026-11-03 d):
  `POST /api/v1/blueprints/{id}/releases/{version}/import?digest=…`.
  - Ohne `digest` antwortet der Server mit 428, bei abweichendem Digest mit 412.
  - Der Server holt das Release selbst aus der gepinnten Registry, prüft es und übergibt
    die Bytes demselben Importer wie `POST /api/v1/applications/source`: gleicher Plan,
    gleiche Entscheide, gleiche Rollenregel, gleiche Bedürfnisse.
  - Er publiziert nie. Kataloge importiert er **nicht**, sondern nennt sie unter
    `cataloguesNotImported`. Ihre Fragen betreffen Gruppen und Personen dieser Installation
    und werden von einer Person in der Console beantwortet.
- **Rollen:**
  - Die Registry-Routen verlangen die Rolle `modeler`.
  - In eine bestehende Applikation importiert nur, wer dort Bearbeitungsrechte hat. Kennt
    der Server den Schlüssel nicht, wird eine neue Applikation angelegt.
  - Der Katalogimport verlangt `productmanager`.
- **Importplan** (ADR-0467): Jedes Artefakt wird mit dem Stand des Servers und mit dem
  Stand des letzten Imports (Import-Baseline) verglichen. Status:

  | Status | Bedeutung | Ohne Entscheid |
  | --- | --- | --- |
  | `new` | fehlt auf dem Server | wird geschrieben |
  | `unchanged` | gleich | bleibt |
  | `behind` | hier unverändert, im Paket neuer | wird aktualisiert |
  | `ahead` | nur hier geändert | bleibt |
  | `changed` | auf beiden Seiten geändert oder nie importiert | stoppt den Import |
  | `elsewhere` | gehört einer anderen, bestehenden Applikation | stoppt den Import |
  | `gap` | Capability oder Wertstrom fehlt in der Capability-Landkarte des Servers (ADR-0528) | stoppt den Import |

  - **Entscheide:** `overwrite` und `keep`, je pro Referenz (`process:ID`, `form:ID`,
    `decision:ID`, `model:HANDLE`, `constant:NAME`) oder als `*`; `adopt` pro Referenz;
    für `gap` die Werte `adopt` (Vorschlag übernehmen), `map` (vorhandenem Key zuordnen)
    und `omit` (weglassen).
  - **`undecided`** ist **kein Status**, sondern das Ergebnis (`outcome`) eines Artefakts
    ohne nötigen Entscheid. Dann stoppt der Import vor dem ersten Schreibzugriff mit HTTP
    409, dem Plan und der Liste `undecided`.
  - Derselbe Release, zweimal importiert, ändert beim zweiten Mal nichts.
- **Bedürfnisse:** Plan und Vorschau nennen, was ein Paket nicht mitbringt: Worker Types
  und Worker, Server-Konstanten sowie die Gruppen und Personen, nach denen ein Shop fragt.
  Worker-Namen gelten pro Server. Ein bekannter Fall: Zwei Beispiele verwenden denselben
  Worker-Namen mit unterschiedlichem Worker Type, und der Plan nennt den Konflikt.
- **Inhaltsgrenzen:** Ein Paket enthält keine Credentials und keine installationsspezifischen
  Geheimnisse. Ein heruntergeladener Worker Type kann keinen beliebigen Code in den
  atlas-Server laden (ADR-0208).

### G. Konfliktfälle (umgesetzt)

- **Artefakt einer anderen, bestehenden Applikation** (Nachtrag ADR-0467 vom 2026-11-03):
  - Der Plan führt es als `elsewhere`, mit `heldBy` (Name der haltenden Applikation, nur
    für Berechtigte) und `compared` (`unchanged` oder `changed`).
  - Erst `adopt` verschiebt es, und zwar einzeln pro Referenz; `adopt=*` wird abgelehnt.
    Weicht der Inhalt ab, braucht es zusätzlich `overwrite` oder `keep`.
  - Übernehmen verlangt die Bearbeitungsrolle in der abgebenden Applikation; ohne sie
    antwortet der Server mit 404 ohne Details. Eine geschützte Systemapplikation gibt
    nichts ab (403).
  - Verworfen wurden: stilles Übernehmen bei gleichem Inhalt, Kopieren unter neuer ID und
    `adopt=*`.
- **Artefakte einer gelöschten Applikation** gelten als «Ungrouped» (ADR-0034). Ein
  erneuter Import reiht sie in die neue Applikation ein, statt dauerhaft zu blockieren.
- **Gemeinsam genutztes Entscheidungsmodell** (Nachtrag ADR-0467 vom 2026-11-05):
  - Gemeint ist ein Modell mit gleichem Handle, aber anderem Inhalt, das eine Entscheidung
    ausserhalb der Applikation verwendet. Der Plan nennt solche Entscheidungen samt
    Applikation unter `sharedWith`.
  - Ersetzt wird es nur bei einzeln benanntem `overwrite`, nie durch `*` und nie
    automatisch als `behind`.
  - Es braucht die Bearbeitungsrolle in jeder betroffenen Applikation; Entscheidungen einer
    geschützten Applikation werden nie geändert. Das Modell des Servers zu behalten bleibt
    ein Ausweg.
  - Verworfen wurden: nie ersetzen (das Beispiel liefe dann auf einem fremden Modell) und
    das Anlegen unter neuem Handle (jeder spätere Import und Export müsste die Umbenennung
    mitführen).
- **Console-Dialog:**
  - Er nennt die Zielapplikation und pro Zeile Art, Name und Referenz. Daneben steht, was
    mit dem Artefakt geschieht: ersetzt, behalten, aus «X» nach «Ziel» verschoben (mit
    dem Inhalt des Servers oder des Pakets) oder zurückgelassen.
  - «Alle anwählen» setzt auch die Übernahme-Kästchen, nie ein gemeinsam genutztes Modell.
  - «Importieren» bleibt gesperrt, solange ein Artefakt einer anderen Applikation nicht
    übernommen ist.

### H. Agenten und Automatisierung (umgesetzt)

- **CLI `atlas import`:**
  - Zeigt den Plan und nimmt Entscheide über `--overwrite`, `--keep` und `--adopt` an
    (`--adopt` nur einzeln) oder fragt am Terminal.
  - In einer Pipeline ohne Terminal stoppt ein unentschiedener Fall vor dem ersten
    Schreibzugriff.
  - `--overwrite all` ersetzt kein gemeinsam genutztes Modell.
- **MCP** (Model Context Protocol; nach ADR-0016 ein reiner HTTP-Adapter zur atlas-API):
  - Werkzeuge: `atlas_hub_list_releases`, `atlas_hub_preview_release` und
    `atlas_hub_import_release`.
  - Der Import ist ein Probelauf, solange nicht `dryRun: false` gesetzt ist, und verlangt
    den Digest aus der Vorschau. `overwrite`, `keep` und `adopt` gibt er nur so weiter,
    wie die Person entschieden hat.
  - Er publiziert nicht und importiert keine Kataloge. Die Route für rohe Paketbytes ist
    über MCP bewusst nicht erreichbar.
  - URL und Schlüssel der Registry sind nie Parameter.

### I. Betrieb und Schlüsselrotation (umgesetzt)

- **Ein oder zwei Schlüssel** (Nachtrag 2026-11-02 b): `ATLAS_BLUEPRINT_REGISTRY_KEY`
  enthält einen oder zwei Public Keys, durch Komma getrennt; ein Index, der von einem davon
  signiert ist, wird akzeptiert.
  - Mehr als zwei, ein doppelter oder ein fehlerhafter Schlüssel verhindern den Start.
  - Beim Start protokolliert der Server die Fingerprints als Ereignis
    `hub.registry_keys_pinned`: als INFO bei einem Schlüssel, als WARN bei zwei.
  - Verworfen wurden: eine Schlüsselliste oder ein Nachfolgeschlüssel im signierten Index
    sowie unbegrenzte Listen.
- **Fester Ankündigungsweg** (Nachtrag 2026-11-02 c):
  - Eine geplante Rotation wird ausschliesslich mit einer Erklärung
    `atlas-hub-key-rotation/v1` angekündigt. Sie enthält Registry-ID, alten Fingerprint,
    neuen Public Key und Fingerprint, Ankündigungstag und frühesten Wechseltag (mindestens
    7 Tage später, Standard 14).
  - Sie ist mit dem **aktuellen** Schlüssel über die exakten Bytes signiert und liegt fest
    unter `examples/hub/key-rotation.json` mit `.sig` auf `main`; eine Kopie liegt auf
    Pages.
  - Betreiber prüfen sie mit `atlas hub verify-announcement` gegen den gepinnten Schlüssel
    und nehmen einen neuen Schlüssel aus keiner anderen Quelle.
- **Geplante Rotation:**
  1. Neuen Schlüssel erzeugen und sichern.
  2. Rotation ankündigen.
  3. Die Betreiber prüfen die Ankündigung und pinnen beide Schlüssel.
  4. Den Signierschlüssel wechseln: Release-Lock, Secret, Pages.
  5. Die Betreiber entfernen den alten Schlüssel.
  6. Die alten Sicherungen vernichten.

  Eine Installation ohne doppeltes Pinning lehnt den Index **ab dem Wechsel** des
  Signierschlüssels ab, nicht schon ab der Ankündigung. Betroffen ist nur der Hub-Dialog;
  importierte und deployte Applikationen laufen weiter.
- **Kompromittierter Schlüssel:** Die Betreiber entfernen sofort beide
  `ATLAS_BLUEPRINT_REGISTRY_*`-Variablen; installierte Inhalte bleiben unverändert. Einen
  kompromittierten Schlüssel pinnt man nie neben den neuen. Danach ist zu prüfen, was seit
  der vermuteten Kompromittierung importiert wurde.
- **Verlorener Schlüssel ohne Verdacht auf Missbrauch:** Mit ihm kann niemand mehr
  signieren, weder eine Ankündigung noch einen Index; eine Überlappung zweier Schlüssel
  hilft deshalb nicht. Der neue Schlüssel wird veröffentlicht, und die Betreiber ersetzen
  den alten direkt.
- **In beiden Notfällen** gelangt der neue Schlüssel nur durch **persönliche Bestätigung**
  über einen vorher vereinbarten Kanal zu den Betreibern: Der Eigentümer liest den
  Fingerprint vor, der Betreiber vergleicht ihn. Eine signierte Ankündigung ist im Notfall
  nie gültig.

### J. Bedrohungen, die das Design adressiert

| Bedrohung | Gegenmassnahme |
| --- | --- |
| Manipulierter Index oder manipuliertes Paket | Ed25519-Signatur und SHA-256-Digest |
| Austausch zwischen Vorschau und Import | Digest-Bindung (`If-Match` bzw. `?digest=`, 412) |
| SSRF gegen interne Netze | nur vom Administrator gesetzte URL, nur öffentliche IPs, Port 443, keine Redirects, Pfade vom selben Ursprung |
| Dekompressionsbombe, Path Traversal | Grössen- und Eintragsgrenzen, Pfad- und Typprüfung, kein Entpacken auf die Festplatte |
| Untergeschobener Schlüssel | Out-of-band-Pinning, kein Trust-on-first-use, fester Ankündigungsweg, persönliche Bestätigung im Notfall |
| Stilles Überschreiben lokaler Arbeit | Importplan mit Baseline, Entscheide pro Artefakt, 409 bei Unentschiedenem |
| Übernahme fremder Artefakte, Änderung fremder Entscheidungen | `adopt` und benanntes `overwrite` nur mit Bearbeitungsrolle in der betroffenen Applikation; geschützte Applikationen ausgenommen |
| Agent handelt über den Kopf der Person hinweg | Probelauf als Standard, Digest-Pflicht, nur die Entscheide der Person, kein Publizieren, keine Kataloge |

Ausdrücklich bleibende Restrisiken:

- Die Signatur bestätigt die Herkunft relativ zum gepinnten Schlüssel, nicht die
  Unbedenklichkeit der Inhalte.
- Die Herausgeber-Identität ist vom Betreiber bestätigt, nicht kryptografisch bewiesen.
- Die Sicherheit hängt am Schutz des privaten Signierschlüssels.

### K. Reifegrad, offene Punkte und Grenzen (nicht als umgesetzt darstellen)

- **Reifegrad:** ADR-0499 hat den Status «Accepted», die Umsetzung ist «Partial».
  - BMP0 (Vertrag und Vertrauensbasis): abgeschlossen.
  - BMP1 (öffentliche Registry bis zur lauffähigen Applikation): aktiv.
  - BMP2 (verwaltete Updates, Wiederherstellung) und BMP3 (föderierte und private Quellen,
    kommerzielle Auslieferung): geplant, ohne Umsetzungsverantwortung.
- **Offen:**
  - dauerhafte Installations-Provenienz als Sidecar-Zustand (Registry-Identität und
    Fingerprint, Release, Digest, Applikation, Katalog-IDs), mit Tests nach Neustart und
    Wiederholung und mit Wiederherstellung bei Teilfehlern;
  - Aufbewahrung älterer Paketversionen und gepinnte Updates (BMP2);
  - mehrere Registries gleichzeitig: Derzeit gibt es genau eine pro Server, und
    Namensraumkollisionen dürfen nie still aufgelöst werden;
  - authentifizierte oder private Auslieferung und Bezahlung (BMP3);
  - eigenständige Katalog-Artefakte und automatische «sichere» Updates (ADR-0506, nur
    vorgeschlagen);
  - Mehrfachinstallation desselben Blueprints unter anderen IDs.
- **Grenzen des Pakets:** Informationsmodelle, Panorama-Modelle, Server-Konstanten, Worker
  und nicht alle Katalog-Medien reisen nicht mit; sie werden als Bedürfnis benannt.
- **Kein Sicherheitsnachweis:** Ein signierter Index macht ein Paket nicht sicher
  ausführbar. Die bestehende Prüfung und die Deploy-Regeln bleiben Pflicht.

## 5. Gliederung

Bei einer neuen Ausgabe (A) diese Gliederung, bei einer Überarbeitung (B) die bestehende:

1. Management Summary (höchstens eine Seite)
2. Ausgangslage und Problem
3. Begriffe und Abgrenzung
4. Architekturüberblick mit Vertrauensgrenzen
5. Der Registry-Vertrag v1
6. Kryptografie und Vertrauensmodell
7. Netzwerk- und Archivsicherheit (Tabelle der Grenzwerte)
8. Veröffentlichen: Herausgeberseite
9. Importieren: Installationsseite (Tabelle der Importstatus)
10. Konfliktfälle im Detail
11. Agenten und Automatisierung
12. Betrieb und Schlüsselrotation
13. Bedrohungsmodell (Tabelle Bedrohung, Gegenmassnahme, Restrisiko)
14. Offene Punkte, Grenzen und Ausblick
15. Glossar und ADR-Verzeichnis

## 6. Sprache und Form

- Deutsch in sachlicher, formeller Sprache, Schweizer Schreibweise: **kein «ß», immer
  «ss»**; Umlaute ä, ö, ü werden verwendet.
- Neutral und wertungsarm; keine Marketing-Sprache, keine Superlative, keine Emojis.
- Markenschreibweise gemäss `docs/brand/README.md`: `atlas` und `atlas hub`
  kleingeschrieben.
- Fachbegriffe beim ersten Auftreten kurz erklären. Englische Bezeichner aus Code und API
  bleiben in `Code-Schreibweise`.
- Kurze, fokussierte Absätze. Wo mehrere Punkte erläutert werden, gelten Überschriften,
  Aufzählungen und Tabellen.
- Umfang etwa 6000 bis 9000 Wörter in Markdown, mit nummerierten Kapiteln und einem
  Inhaltsverzeichnis.
- Mindestens vier Diagramme als Mermaid-Codeblöcke:
  1. Komponenten und Vertrauensgrenzen;
  2. Sequenz «Veröffentlichen»;
  3. Sequenz «Import über die Console», einschliesslich des Publizierens nur bei Paketen
     mit Produktkatalog;
  4. Zustände der geplanten Schlüsselrotation.

## 7. Dateien

Alle Dateien liegen in `docs/whitepapers/`:

| Datei | Zweck |
| --- | --- |
| `atlas-hub-signierte-verteilung.md` | Markdown-Quelle, massgeblicher Text |
| `atlas-hub-signierte-verteilung-dark.pdf` | Bildschirm-PDF, dunkles Design, A4 |
| `atlas-hub-signierte-verteilung-print.pdf` | Druckfreundliches PDF, heller Grund, A4 |

Werkzeugkette und Gestaltung sind dieselben wie in der übrigen Bibliothek: Markdown → HTML
mit `pandoc`, HTML → PDF mit **WeasyPrint**, im Design von `pblumer/atlas-cloud`. Siehe
[`atlas-applikationen-whitepaper.md`](atlas-applikationen-whitepaper.md), Abschnitte 6 und
8. Werden die PDFs nicht neu erzeugt, steht das im Commit, und `docs/whitepapers/README.md`
nennt die Ausgabe, die sie zeigen.

## 8. Mermaid und Darstellung

Diagramme bleiben im Markdown als Mermaid-Codeblöcke. Für die PDFs werden sie im
Farbsystem der Bibliothek gerendert, ohne Rastergrafiken. Beschriftungen bleiben innerhalb
ihrer Formen.

## 9. Pflege des Whitepapers

Das Whitepaper ist ein **gepflegtes** Dokument. `go test ./docs/whitepapers` hält es an
seine Quellen; der Test läuft in jeder CI, auch bei reinen Dokumentationsänderungen.

1. **Pflegemarken.** Der Kopfkommentar des Markdown trägt je eine Zeile
   `reviewed-through ADR-0499: …` und `reviewed-through ADR-0467: …`. Sie nennt wörtlich
   die letzte Nachtrags-Überschrift, gegen die der Text geprüft wurde. Nach der Prüfung
   gegen einen neuen Nachtrag wird die Zeile auf dessen Überschrift gesetzt, auch wenn am
   Text nichts zu ändern war. Ein neuer Nachtrag ohne nachgeführte Marke lässt den Test
   fehlschlagen.
2. **Grenzwerte.** Der Test vergleicht die Grenzen für Index, Releases, komprimiertes und
   entpacktes Paket, Tags und gepinnte Schlüssel mit den Konstanten in
   `api/blueprintregistry`. Die Formulierungen im Text («höchstens 1 MiB», «höchstens
   512», «bis zu 16 eindeutige», «einen oder zwei») sind deshalb beizubehalten oder im
   Test mitzuändern.
3. **Rechtschreibung.** Der Test lehnt jedes «ß» ab.
4. **Stand.** Die Zeile «Stand der Faktenbasis» im Kopf nennt das Datum der letzten
   Prüfung gegen den Code.

## 10. Qualitätsprüfung vor dem Commit

1. Jede Statusaussage gegen `docs/adr/README.md` und die «As built»-Abschnitte abgleichen.
2. Jede Zahl im Text muss in Abschnitt 4 oder in einer Quelle aus Abschnitt 3 vorkommen.
3. `go test ./docs/whitepapers` und `go test ./docs/adr` sind grün; `gofmt -l .` liefert
   keine Ausgabe.
4. Alle relativen Links sind gültig.
5. Werden PDFs erzeugt, sind sie mit `pdfinfo` und einer Seitenvorschau zu prüfen: A4,
   Titelblatt allein auf Seite 1, keine leeren Seiten.

## 11. Abschluss

- Committe auf dem vorgegebenen Arbeits-Branch und pushe ihn. Erstelle **keinen** Pull
  Request, sofern nicht ausdrücklich verlangt.
- Berichte zum Schluss:
  - welche Quellen verwendet wurden;
  - welche Aussagen als **Einschätzung** markiert sind;
  - was gegenüber der vorherigen Ausgabe korrigiert oder ergänzt wurde;
  - welche Fragen aus den Quellen nicht beantwortet werden konnten;
  - ob die PDFs neu erzeugt wurden.

---

## Anhang: Änderungen gegenüber der ersten Fassung dieses Prompts

Die erste Fassung (6. November 2026, für einen Assistenten ohne Repository-Zugriff) enthielt
dieselbe Faktenbasis, mit diesen Fehlern, die das daraus entstandene Whitepaper übernommen
hatte:

1. **Publizieren.** Die erste Fassung sprach von «explizitem Publizieren» als eigenem
   Schritt. Tatsächlich publiziert der Console-Import ein Paket mit Produktkatalogen im
   selben Ablauf; nur ohne Katalog bleibt das Publizieren ein eigener Schritt.
2. **Notfälle der Schlüsselverwaltung.** Die erste Fassung behandelte den verlorenen und
   den kompromittierten Schlüssel gemeinsam. Nur beim kompromittierten entfernen die
   Betreiber sofort die Registry-Variablen.
3. **Importstatus.** `undecided` erschien in der Tabelle der Status; es ist ein Ergebnis.
   Der Status `gap` (ADR-0528) fehlte.
4. **Rotation.** Eine Installation ohne doppeltes Pinning verliert den Hub beim Wechsel
   des Signierschlüssels, nicht bei der Ankündigung.

Neu in dieser Fassung: die zwei Auftragsformen (neue Ausgabe, Überarbeitung), die
Quellenliste für einen Agenten mit Repository-Zugriff und die Pflege mit Pflegemarken und
Test (Abschnitt 9).
