# n8n und Atlas: Entscheidungshilfe für die Unternehmensarchitektur

> **Sprachen:** Deutsch (dieses Dokument) · [English](n8n.md)
>
> **Zweck.** Dieses Dokument unterstützt Architektur- und Plattformentscheidungen,
> wenn ein Unternehmensbereich n8n für Automatisierungen eingeführt hat und ein
> anderer Bereich Atlas für die Automatisierung von Geschäftsprozessen evaluiert.
> Es ist **weder ein Produkt-Ranking noch eine Forderung, eines der Produkte
> durch das andere zu ersetzen**.
>
> **Überprüft:** 13. Oktober 2026 anhand der verlinkten Produkt- und
> Projektdokumentation. Fähigkeiten und Lizenz-/Editionseigenschaften können
> sich ändern. Für konkrete Entscheidungen sind jeweils Version, Betriebsmodell,
> Konfiguration und Lizenz zu prüfen.
>
> **Hinweis zur Produktreife.** Atlas befindet sich weiterhin in einer
> **Entwicklervorschau (0.x)** mit noch instabilen APIs und Datenformaten.
> Die [README](../../README.md) erklärt ausdrücklich, dass Atlas nicht für den
> Produktivbetrieb bereit ist. n8n ist dagegen ein etabliertes Cloud- und
> Self-Hosting-Produkt. Eine bessere *architektonische Eignung* bedeutet nicht
> automatisch eine höhere *Produktionsreife*.

## Zusammenfassung für Entscheidungsträger

**Es geht nicht darum, ob n8n einen Geschäftsprozess ausführen kann. Das kann
es.** n8n unterstützt persistente Wartezustände, Webhooks, Formulare,
menschliche Freigaben, Ausführungshistorie, Wiederholungsversuche und
skalierte Verarbeitung im Queue Mode. n8n auf kurzlebige Integrationen zu
reduzieren, wäre sachlich falsch.

**Der zentrale Unterschied ist, wo der verbindliche fachliche Prozesszustand
geführt wird.**

- **n8n** vereinfacht Integrationen und technische Workflow-Automatisierung
  durch einen umfangreichen Node-Katalog, Anmeldeinformationen, Datenmapping
  und interaktive Ausführungswerkzeuge. Auch längere Abläufe mit menschlicher
  Beteiligung sind möglich.
- **Atlas** behandelt den *Geschäftsprozess* als ausdrückliches
  Ausführungsobjekt: BPMN-Prozess- und Elementinstanzen, Ereignisse,
  Gateways, Benutzeraufgaben, Timer, Incidents, Jobs, Variablen und
  persistente Prozesshistorie. Geschäftsentscheidungen lassen sich über
  FEEL/DMN und Temis abbilden.
- **Beide** Produkte können sich überschneidende Anwendungsfälle abdecken.
  BPMN und DMN sind Argumente für Atlas, **wenn sie echte Anforderungen
  darstellen** – kein automatischer Nachweis, dass n8n ungeeignet ist.
- **Heute** hat n8n einen deutlichen Vorsprung bei Betriebserfahrung,
  Integrationsbreite sowie etablierten Skalierungs- und Supportmodellen.
  Die spezialisierte Engine-Architektur von Atlas ist vielversprechend,
  stellt aber noch **keine nachgewiesene produktive Alternative** für
  unternehmenskritische Lasten dar.

> **Entscheidungsprinzip:** Auswahl und Governance einer
> Automatisierungsplattform müssen sich nach der Art der Automatisierung,
> der Hoheit über den verbindlichen Zustand, den benötigten Semantiken und
> Nachweisen sowie dem Betriebsrisiko richten – nicht nach dem
> organisatorischen Einfluss der Befürworter oder dem Aussehen des Editors.

Eine konsistente Unternehmensarchitektur **kann n8n für technische
Integrationen und Atlas für eine klar abgegrenzte Klasse BPMN-gesteuerter
Geschäftsprozesse einsetzen**. Diese Kombination ist eine zu prüfende Option
und **keine zwingende Zwei-Produkte-Architektur**. Einfache Automatisierungen
sollten einfach bleiben.

## 1. Welche Architekturentscheidung steht tatsächlich an?

Der Begriff «Workflow» hat mehrere Bedeutungen. Dass beide Produkte
Workflows ausführen, beantwortet noch nicht, ob sie dieselbe Verantwortung
innerhalb des Unternehmens übernehmen sollten.

| Verantwortungsbereich | Zentrale Fragen | Typische Eignung |
|---|---|---|
| **Integrationsautomatisierung** | Wie werden Daten übertragen, transformiert, synchronisiert und APIs aufgerufen? | n8n ist häufig die einfachere Wahl. |
| **Geschäftsprozessorchestrierung** | In welchem Zustand befindet sich ein Geschäftsfall, welches Ereignis verändert ihn, wer ist verantwortlich und was geschieht nach Wartezeiten oder Fehlern? | Atlas ist ein Kandidat, wenn ausführbare BPMN-Semantik gefordert wird. Auch n8n kann solche Abläufe mit eigenen Konstrukten realisieren. |
| **Geschäftsentscheidungen** | Wo werden Geschäftsregeln definiert, versioniert, ausgewertet und erklärt? | Atlas und Temis bieten einen DMN-orientierten Ansatz; n8n kann externe Regeldienste oder eigene Ausdrücke bzw. Code einsetzen. |
| **Menschliche Aufgabenbearbeitung** | Wer darf eine Aufgabe übernehmen oder abschliessen, welche Fristen gelten und wie ist die Aufgabe mit dem Geschäftsfall verknüpft? | Die konkreten Anforderungen entscheiden. Atlas modelliert BPMN User Tasks; n8n unterstützt Formulare/Freigaben und die Anbindung von Task-Systemen. |
| **Betrieb und Governance** | Wer betreibt die Lösung, schützt Daten, bearbeitet Störungen, führt Upgrades durch und leistet Support? | n8n hat aktuell einen erheblichen Reifegradvorsprung. Atlas benötigt einen abgegrenzten Einführungsplan und eine ausdrückliche Risikoübernahme. |

Die **unternehmensweite Automatisierungsarchitektur** sollte die Hoheit
über diese Verantwortungen festlegen. Eine Produktwahl für einen Bereich
darf nicht stillschweigend zum verbindlichen Standard für jede Form der
Automatisierung werden.

### Falsche Gegensätze vermeiden

- n8n **kann** langlaufende Workflows ausführen. Der
  [Wait Node](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.wait/)
  speichert pausierte Ausführungen und setzt diese zeit- oder
  ereignisgesteuert bzw. nach einem Formulareingang fort.
- n8n **kann** Menschen über Formulare, Benachrichtigungen und Freigaben
  einbinden. Es ist kein ausschliesslich maschinenorientiertes Werkzeug.
- Atlas **kann** Integrationen über Worker Types und externe Jobs
  durchführen. Es ist nicht nur BPMN-Modellierung ohne Konnektivität.
- Die Verwendung von BPMN garantiert **nicht von selbst** vollständige
  Konformität, Verständlichkeit für Fachpersonen oder Interoperabilität
  mit beliebigen Modellierungswerkzeugen.
- Event Sourcing und deterministisches Replay garantieren **weder
  genau einmal ausgeführte externe Aktionen** noch automatisch eine
  rechtsgenügende Audit-Historie oder vollständigen Schutz vor Datenverlust.
  Dafür sind zusätzliche Betriebs- und Testmassnahmen erforderlich.

## 2. Nachweisstufen und aktueller Reifegrad

Die folgenden Begriffe sollten konsequent unterschieden werden:

| Begriff | Bedeutung |
|---|---|
| **Dokumentiert / implementiert** | Aktuelle Atlas-Dokumentation, ADRs, technische Tests oder Betriebsanleitungen beschreiben eine Funktion als umgesetzt. Die eingesetzte Version muss dennoch überprüft werden. |
| **Nachgewiesen** | Die Anforderung wurde in einem reproduzierbaren Test anhand vereinbarter Abnahmekriterien geprüft und dokumentiert. |
| **Geplant / architektonisches Ziel** | Roadmap oder ADR beschreiben eine Zielsetzung; sie darf nicht als bereits verfügbare Funktion bewertet werden. |
| **Produktiv bewährt** | Ein unterstütztes Betriebsmodell und belastbare Erfahrung mit der geforderten Last, Verfügbarkeit und Sicherheit liegen vor. |

Die [BPMN-Conformance-Übersicht](../../conformance/COVERAGE.md) von Atlas
enthält registrierte Testszenarien für Benutzeraufgaben, Nachrichten,
Timer, Gateways, Subprozesse, Multi-Instance-Aktivitäten, Kompensation und
Incidents. Das ist ein wesentlicher **Implementierungsnachweis**. Dass alle
registrierten Szenarien abgedeckt sind, ist jedoch **weder eine unabhängige
Zertifizierung vollständiger BPMN-2.0-Konformität noch eine Garantie für
den Produktivbetrieb**.

Atlas dokumentiert ausserdem einen browserbasierten Modeler, eine
Tasks-App, Betriebsansichten, Prozess-Replay, Worker, einen DMN-Editor
und Entscheidungsauswertungen. Siehe [README](../../README.md),
[Roadmap](../../ROADMAP.md) und
[ADR zum Worker-Ausführungsmodell](../adr/0203-worker-execution-model.md).
Bereits implementierte Funktionen dürfen nicht pauschal als Zukunftspläne
bezeichnet werden. Umgekehrt belegt ein im Modeler sichtbares BPMN-Symbol
noch nicht die vollständige Unterstützung seiner Ausführungssemantik.

Bei n8n hängt die Verfügbarkeit einzelner Funktionen von **Cloud oder
Self-Hosting, Edition, Lizenz und Betriebsmodus** ab. Statt allen
Installationen sämtliche Enterprise-Funktionen zuzuschreiben, ist die
[aktuelle Editionsübersicht](https://docs.n8n.io/hosting/community-edition-features/)
heranzuziehen.

## 3. Entscheidungsmatrix: Eignung ist nicht Betriebsreife

Die Angaben zur «Eignung» beschreiben eine **architektonische Ausrichtung**.
Sie sind weder ein Benchmarkresultat noch eine Freigabe durch die
Organisation.

| Anforderung oder Aspekt | n8n | Atlas | Was zu prüfen ist |
|---|---|---|---|
| Schnelle SaaS-/API-/Datenintegration | **Sehr gut geeignet**; umfangreicher Node- und Credential-Katalog | Worker Types, REST/API und eigene Worker; kleinerer Katalog | Zielsysteme, Zuständigkeiten, Wartung, Credentials |
| Ausführbares **BPMN 2.x** als verbindliches Prozessmodell | Kein natives BPMN-Ausführungsmodell; eigener Node-Graph | **Kernkonzept mit umfangreicher implementierter Abdeckung** | Benötigte BPMN-Muster und Übertragbarkeit von Erweiterungen |
| Warten über Tage/Wochen oder auf einen Callback | **Unterstützt** durch Wait Nodes und gespeicherte Ausführungen | **Zentraler Anwendungsfall** mit Timern, Nachrichten und Prozesszustand | Recovery, Fristen und Lebenszyklusregeln |
| Menschliche Aufgaben | Formulare, Freigabeabläufe und externe Task-Systeme | BPMN User Tasks, Zuweisungen und Arbeitsvorräte | Delegation, Stellvertretung, Zugriffsregeln und Fristen |
| Geschäftsentscheidungen nach **DMN** | Externer Regeldienst oder workfloweigene Logik | Integration von Temis/DMN und FEEL, Editor und Entscheidungsprotokolle | Regelverantwortung, Nachvollziehbarkeit und Versionierung |
| Technische Fehlerdiagnose | **Ausgereifte** Node-Tests, Ausführungsdaten und Retry-Werkzeuge | Jobs, Incidents, Prozessansichten und Historie; jüngere Bedienoberfläche | Operator-Abläufe und Runbooks |
| Wiederherstellung des fachlichen Prozesszustands nach Absturz | Persistenz und Wiederherstellungsmechanismen für Ausführungen | Ereignisprotokoll und deterministisches Replay als Architektur-Invarianten | Absturztests; Engine-Zustand von externen Effekten unterscheiden |
| Horizontale Verarbeitungsskalierung | **Etablierter Queue Mode** mit mehreren Workern | Partitionsorientierter Entwurf; unternehmensweite Skalierung noch Zielsetzung | Konkrete Topologie, Verfügbarkeit und Leistung unter Last |
| Hochverfügbarkeit | Etablierte Betriebsansätze und teils editionsabhängige Funktionen | **Noch kein nachgewiesener, unterstützter Multi-Replica-Betrieb für Atlas** | Failover, Split-Brain-Schutz und Wiederherstellungsziele |
| Unternehmensweite Identität und Governance | Produktfunktionen abhängig von Edition und Konfiguration | OIDC/RBAC und weitere Kontrollen implementiert; Sicherheitslücken bleiben | Identitätsprovider, objektbezogene Rechte, Secrets und Audit |
| Lizenzierung und kommerzieller Support | Sustainable Use License; kommerzielle Angebote | **AGPL-3.0-only**; Support durch Projekt/Community | Rechtliche Prüfung, Support und langfristige Verantwortung |
| Sofortiger Produktivbetrieb | **Etablierter Kandidat**, vorbehaltlich Prüfung im Unternehmen | **Entwicklervorschau, nicht produktionsbereit** | Verbindliche Sicherheits-, Risiko- und Betriebsvorgaben |

Die wichtigste Unterscheidung von Atlas liegt in **formaler
Prozesssemantik und einer prozessorientierten Betriebs- und
Ausführungssicht**, nicht in der Behauptung, n8n könne entsprechende
fachliche Ergebnisse grundsätzlich nicht liefern.

## 4. Modellierung und Prozesssemantik

Ein n8n-Workflow ist ein proprietärer **ausführbarer Node-Graph** mit
Triggern, Integrationen, Transformationen, Bedingungen, Schleifen und
Teil-Workflows. Der Editor eignet sich besonders für technische
Automatisierung und die direkte Untersuchung von Daten. Das Modell ist
jedoch kein BPMN-Austauschformat und verspricht keine BPMN-Ausführungssemantik.

Atlas kompiliert BPMN-XML in einen ausführbaren Prozessgraphen.
BPMN definiert Bedeutungen für Ereignisse, Aktivitäten, angeheftete
Boundary Events, Scopes, Gateways, Call Activities und
Multi-Instance-Verhalten. Dadurch kann ein gemeinsames Artefakt für
Business Analysts, Prozesseigentümer, Automatisierungsentwickler und
Betrieb entstehen – **vorausgesetzt, die Beteiligten sind geschult und
der tatsächlich unterstützte BPMN-Umfang ist geregelt**.

Ein ausführbares BPMN-Modell kann die Lücke zwischen
Prozessdokumentation und Ausführung verkleinern. Es beseitigt jedoch
nicht automatisch herstellerspezifische Erweiterungen, unklare
Anforderungen oder den Bedarf nach Governance.

Quellen:
[Atlas BPMN-Abdeckung](../../conformance/COVERAGE.md),
[Architektur](../ARCHITECTURE.md) und
[Unternehmensarchitektur](../architecture/enterprise-architecture.md).

## 5. Langlaufende Geschäftsfälle und verbindlicher Zustand

Die entscheidende Frage lautet **nicht**, ob ein System warten kann.
Das können beide.

Bei einem Geschäftsfall mit einer Laufzeit von mehreren Wochen muss
die Organisation beispielsweise beantworten können:

- Welche Prozessinstanz und welche Version der Prozessdefinition sind aktiv?
- Welche Aufgaben, Timer, Nachrichten und Boundary Events stehen noch aus?
- Wer ist für die nächste fachliche Handlung verantwortlich und welche Frist gilt?
- Welche Entscheidungsregel und welche Eingabewerte haben zu einem
  bestimmten Ergebnis geführt?
- Welcher Zustand bleibt korrekt, wenn ein Worker fehlschlägt, ein Callback
  doppelt eintrifft oder der Server neu startet?
- Welche Daten sind aufzubewahren, zu archivieren oder zu löschen?

Atlas behandelt viele dieser Konzepte als native Bestandteile der
Prozess-Engine. n8n stellt primär Workflow-Ausführungen, Nodes,
Ein-/Ausgabedaten und deren Status dar. Eine gleichwertige
Geschäftsfallsicht kann dort mit Konventionen, Datenhaltung oder einer
separaten Fachanwendung aufgebaut werden. Keine der beiden Lösungen
ersetzt automatisch das benötigte fachliche Datenmodell.

**Praxistest:** Beide Umsetzungen sollen den Zustand eines offenen
Geschäftsfalls zeigen und denselben Fall nach einem Absturz, einem
Fristablauf, einer manuellen Neuzuweisung und einer Prozessänderung
erklären. Verglichen werden die Nachweise und der Wartungsaufwand,
nicht die Anzahl Symbole auf dem Bildschirm.

## 6. Menschliche Arbeit und Entscheidungsmanagement

### Benutzeraufgaben

n8n kann Informationen über Formulare erfassen, auf Antworten warten,
Freigaben versenden und komplexere Aufgabenverwaltung an andere
Systeme delegieren. Für zahlreiche Fälle ist das ausreichend.

Atlas implementiert BPMN User Tasks und dokumentiert Zuweisung,
Übernahme, Formulare und Terminierung von Aufgaben in seinen ADRs
und Oberflächen, beispielsweise
[Zuweisung und Übernahme](../adr/0042-user-task-assignment-and-claim.md)
sowie [Task-Terminierung](../adr/0091-user-task-scheduling.md).

Ein PoC sollte Kandidatengruppen, Neuzuweisung, Stellvertretung,
Fälligkeit, Berechtigungsgrenzen, Abschluss-Historie und die tatsächliche
Formularbedienung gezielt prüfen. Aus dem Vorhandensein eines User Tasks
allein darf keine produktionsreife Aufgabenverwaltung abgeleitet werden.

### FEEL und DMN

Mit Bedingungen, Ausdrücken, Code Nodes und externen Regeldiensten
kann n8n Geschäftsregeln ausführen. Bei kleinen oder von Entwicklern
verwalteten Regeln ist dies häufig ein angemessener Ansatz.

Atlas integriert FEEL und DMN über
[Temis](https://github.com/pblumer/temis) in BPMN Business Rule Tasks.
[ADR-0014](../adr/0014-dmn-business-rule-tasks-via-temis.md) beschreibt
die Ausführungsgrenze; [ADR-0062](../adr/0062-embedded-dmn-editor.md)
dokumentiert den integrierten DMN-Editor und dessen Entwicklung.

DMN schafft vor allem dort Mehrwert, wo **fachliche
Regelverantwortung, unabhängige Prüfung, Versionierung und
Erklärbarkeit** wichtig sind. Ein PoC muss dennoch zeigen, welche
Regelversion angewendet wurde, welche Fakten gespeichert wurden und
was Regeländerungen für bereits laufende Fälle bedeuten.
Die pauschale Aussage, Geschäftsregeln liessen sich ohne jede
Auswirkung auf Deployments ändern, wäre zu weitgehend.

## 7. Integrationen und Entwicklungserlebnis

Der [Integrationskatalog](https://n8n.io/integrations/) ist eine
wesentliche Stärke von n8n: vorgefertigte Nodes, Credentials,
Ausdrücke, Datenvorschau, Ausführungshistorie, Fehlerbehandlung und
Vorlagen reduzieren den Umsetzungsaufwand.

Atlas erzeugt an seiner Service-Task-Grenze persistente Jobs, welche
von Worker Types und Workern bearbeitet werden. Die aktuelle
Atlas-Dokumentation beschreibt bereits ausgewählte Integrationen,
externe Worker, Credential-Verwaltung und einen Modeler; diese
Funktionen sind nicht ausschliesslich Zukunftspläne. Der Umfang
des Integrationskatalogs und das Entwicklungserlebnis sollten
dennoch nicht als gleichwertig mit n8n dargestellt werden.

Atlas sollte **keinen umfassenden n8n-ähnlichen Integrationskatalog
als strategisches Hauptziel** verfolgen. Vorrang haben robuste,
gut definierte Worker-Verträge und wenige, besonders wertvolle
Implementierungen. Für einen einfachen HTTP-Aufruf ist ein direkter
Atlas Worker oft sinnvoller als eine zusätzliche n8n-Schicht.

**Governance-Frage:** Wer verantwortet bei einer Koexistenz
Integrationen, Secrets, Service Accounts und API-Verträge?
Doppelt implementierte Schnittstellen mit unterschiedlichen
Regeln sollten vermieden werden.

## 8. Ausführung, Fehlerbehandlung und Leistung

### n8n

n8n speichert Workflow- und Ausführungszustände und bietet Error
Workflows, Retries, Fehlerdiagnose und manuelle Wiederholung.
Im [Queue Mode](https://docs.n8n.io/hosting/scaling/queue-mode/)
empfängt eine Hauptinstanz die Trigger. Redis verteilt Aufträge
an Worker, während Ausführungsdaten in einer gemeinsamen Datenbank
gespeichert werden. Das ist ein etabliertes Modell zur technischen
Skalierung, sofern die vorgesehenen Datenbank- und
Konfigurationseinstellungen verwendet werden.

Die Aufbewahrung der Ausführungshistorie ist eine konfigurierbare
Betriebsfrage und stellt nicht automatisch ein dauerhaftes
Compliance-Archiv dar. Die n8n-Dokumentation zu
[Ausführungen](https://docs.n8n.io/workflows/executions/all-executions/)
erläutert beispielsweise die Folgen gelöschter Workflows.

### Atlas

Atlas verwendet ein append-only Write-Ahead-Log (WAL) und einen
materialisierten Zustandsspeicher. Zu seinen wesentlichen
[Architektur-Invarianten](../architecture/invariants.md) gehören:

1. Genau ein zustandsverändernder Writer pro Partition.
2. Dauerhafte Speicherung vor extern sichtbaren Engine-Effekten.
3. Derselbe deterministische Pfad zur Anwendung von Ereignissen
   im Normalbetrieb und bei der Wiederherstellung.
4. Kompilierte Prozessstrukturen und Ausdrücke ausserhalb des
   zeitkritischen Ausführungspfads.

Diese Architektur dient insbesondere der Wiederherstellung des
**Engine-Zustands**. Sie garantiert **nicht**, dass ein externer
API-Aufruf genau einmal ausgeführt wird. Worker und Integrationen
benötigen weiterhin Idempotenz, Korrelation, Retry-Regeln und
einen Abgleich mit Zielsystemen. Auch eine Ereignishistorie
allein ist kein Nachweis der vollständigen Erfüllung von
Audit- oder Aufbewahrungspflichten.

### Leistung und Skalierung

Atlas sollte **nicht ohne einen gemeinsamen Benchmark als schneller
als n8n bezeichnet werden**. Die Batch-/WAL-Architektur kann
persistente Zustandsübergänge effizient verarbeiten.
Bei n8n hängen Durchsatz und Latenz unter anderem von
Workflow-Aufbau, Payloads, API-Laufzeiten, Queue-Einstellungen und
Limits der Zielsysteme ab. Diese Lasten sind nicht von sich aus
vergleichbar.

Besonders wichtig: Das aktuelle
[Atlas-Helm-Deployment](../../deploy/README.md) schreibt **eine
StatefulSet-Replica** und ein exklusives persistentes Volume vor.
Horizontale Partitionsreplikation und Hochverfügbarkeit sind noch
Entwicklungsgegenstand (siehe
[Roadmap](../../ROADMAP.md) und
[offene Compliance-Punkte](../compliance/isds-offene-punkte.md)).
Ein Architekturziel darf nicht als produktiv bewährte
Skalierungsfähigkeit ausgegeben werden.

## 9. Sicherheit, Betrieb, Audit und Governance

**n8n:** etablierte Anleitungen für Cloud- und Self-Hosting-Betrieb,
teilweise editionsabhängige Enterprise-Funktionen und eingebaute
Sicherheitswerkzeuge wie ein
[Security Audit](https://docs.n8n.io/hosting/securing/security-audit/).
Vor dem Produktiveinsatz ist die **tatsächlich eingesetzte Edition**
zu prüfen, statt pauschal sämtliche Enterprise-Funktionen anzurechnen.

**Atlas:** README und ADRs dokumentieren Authentisierung
(einschliesslich OIDC), Rollen, Worker-Identitäten,
Secret-Verwaltung, Kontrollen für die Sichtbarkeit von
Prozessinstanzen, Backup/Restore, Metriken und
Ereignisprotokollierung. Das ist mehr, als der ältere Vergleich
erkennen liess.

**Handeln als die anfragende Person.** n8n Enterprise dokumentiert
[End-User Credentials](https://docs.n8n.io/administer/manage-credentials/end-user-credentials):
eine OAuth-Anmeldung, die zur Laufzeit auf das Konto der Person aufgelöst
wird, die den Workflow auslöst. Eine ausführende Identität pro Ausführung ist
also **kein** Alleinstellungsmerkmal von Atlas. Atlas hat einen
vorgeschlagenen, teilweise umgesetzten Entwurf
([ADR-0475](../adr/0475-identity-aware-worker-execution.md)),
in dem ein geregelter Worker vor jeder Aktion einen Entscheid anfordert –
gegen versionierte Policies, mit Genehmigungen, die an einen Digest der
genauen Aktion gebunden sind, und mit dem Entscheid im Ereignisprotokoll.
Er umfasst nur den SharePoint-Worker-Typ. Die geprüfte n8n-Dokumentation
(2026-10-23) beschreibt keine Policy-Auswertung pro Operation, keine
digest-gebundene Genehmigung und keine dauerhaften Entscheidnachweise dieser
Art; *in der geprüften Dokumentation nicht gefunden* heisst nicht *im Produkt
nicht vorhanden*, und eine Evaluation sollte beim Hersteller nachfragen.

Die [Atlas-Liste offener Compliance-Punkte](../compliance/isds-offene-punkte.md)
nennt aber weiterhin Risiken und notwendige Arbeiten bei:

- Hochverfügbarkeit und Replikation;
- stabilen APIs und Datenformaten für Version 1.0, Migrationen
  und Sicherheits-Supportvereinbarungen;
- nachweisbarer Löschung über WAL, Exporte und Snapshots hinweg;
- Verschlüsselung persistierter Nicht-Secret-Daten bzw.
  geprüften Verschlüsselungsvorgaben für den Betrieb;
- Mandanten-/Schutzbedarfstrennung und verbleibenden
  objektbezogenen Berechtigungsprüfungen;
- Software-Lieferkette und Sicherheits-Audit-Abdeckung.

Einzelne Kontrollen sind bereits implementiert, müssen aber gehärtet
oder in der Zielumgebung überprüft werden. Die Projektliste
dokumentiert den **vom Projekt beschriebenen Stand** und ersetzt
keine unabhängige Prüfung. Das
[ISDS-Material für die Schweizer Bundesverwaltung](../compliance/README.md)
ist für regulierte Umgebungen relevant, bedeutet aber **keine
behördlich genehmigte Sicherheitsfreigabe**.

Eine Betriebsentscheidung muss Wiederherstellungsziele (RTO/RPO),
Backup- und Restore-Tests, Incident-Verantwortung, Überwachung,
Schlüsselverwaltung, Datenaufbewahrung, Upgrades, Rollback,
Verfügbarkeit und Support ausdrücklich abdecken.
Eine gute Engine-Architektur ersetzt diese Anforderungen nicht.

## 10. Lizenzierung und strategische Kontrolle

**Korrektur gegenüber dem alten Vergleich:** Atlas steht unter
**GNU Affero General Public License v3.0 only (AGPL-3.0-only)**.
Dies ist in [LICENSE](../../LICENSE) und
[README](../../README.md) dokumentiert. Atlas steht **nicht**
unter Apache 2.0.

n8n ist unter der
[Sustainable Use License](https://docs.n8n.io/sustainable-use-license/)
quellverfügbar und bietet kommerzielle Editionen. Diese Lizenz
entspricht keiner permissiven Open-Source-Lizenz. Die normale
interne Geschäftsprozessautomatisierung gehört zum vorgesehenen
Nutzungsbereich; Weiterverteilung, Einbettung, Managed Services
und bestimmte kommerzielle Nutzungsszenarien sind gesondert
rechtlich zu beurteilen.

Die AGPL von Atlas gewährt Quellcode-Rechte unter entsprechenden
Copyleft-Bedingungen. Änderungen an einem über ein Netzwerk
bereitgestellten AGPL-Dienst können eine Pflicht zur Bereitstellung
des modifizierten Quellcodes gegenüber den Nutzern auslösen.
Die genauen Pflichten müssen für das konkrete Verteilungs- und
Betriebsmodell geprüft werden. **Keine der beiden Lizenzen
rechtfertigt allein die Annahme oder Ablehnung einer
Geschäftsprozessarchitektur.**

Auch die interne Verantwortung zählt: Ein junges,
selbst betriebenes Projekt kann Lizenzkosten vermeiden,
dafür aber deutlich höhere Entwicklungs-, Prüf-, Support-
und Upgrade-Kosten verursachen.

## 11. Praxisbeispiel: langlaufender Antrag oder Bewilligungsprozess

Betrachten wir einen Geschäftsfall mit mehreren
Organisationseinheiten:

1. Eine Bürgerin, ein Bürger oder eine angestellte Person
   reicht einen Antrag ein.
2. Die Sachbearbeitung prüft die Vollständigkeit und fordert
   bei Bedarf fehlende Unterlagen nach.
3. Der Fall wartet auf die Antwort; dabei gelten eine
   ausdrückliche Frist und eine Eskalationsregel.
4. Eine Entscheidungstabelle ermittelt den weiteren
   Bearbeitungsweg oder die zuständige Bewilligungsstufe.
5. Eine prüfende Person bewilligt den Antrag oder lehnt ihn ab.
6. Das Ergebnis wird mit wiederholungssicherer Integration
   an ein externes System übermittelt.
7. Die Organisation muss Verlauf und Verantwortlichkeiten
   des Geschäftsfalls später erklären können.

**Mit n8n** lässt sich dies mit Formularen/Webhooks, Wait Nodes,
Bedingungen, Teil-Workflows, API Nodes, gespeicherten
Ausführungsdaten und bei Bedarf einer externen Geschäftsfall-
oder Task-Verwaltung umsetzen. Zu prüfen ist, ob der entstandene
fachliche Zustand auch ausserhalb des technischen
Automatisierungsteams verständlich und steuerbar bleibt.

**Mit Atlas** kann der Ablauf über BPMN User Tasks, Timer,
Nachrichtenereignisse, DMN Business Rule Tasks und Service-Task-Jobs
modelliert werden. Zu prüfen sind die tatsächlich benötigten
BPMN-Semantiken, Aufgabenberechtigungen, Entscheidungsnachweise,
Prozessversionen und Wiederherstellung. Die
Enterprise-Betriebsreife darf daraus nicht einfach abgeleitet werden.

**Für eine einzelne Benachrichtigung, einen Webhook oder eine
schlichte Datensynchronisierung** ist n8n – oder eine bestehende
Integrationslösung – normalerweise angemessener als die Einführung
einer zusätzlichen Prozess-Engine. Architekturkomplexität ist
kein Selbstzweck.

## 12. Architekturvarianten und Verantwortung

### Variante A – n8n als einzige Workflow-Plattform

Geeignet, wenn der Grossteil der Anwendungsfälle Integrationen
betrifft, menschliche Freigaben einfach sind, proprietäre
Workflow-Konstrukte akzeptiert werden und kein ausführbares BPMN
als verbindliches Prozessmodell verlangt wird.

Das kann ein legitimer Architekturentscheid sein. Werden darüber
hinaus fachliche Geschäftsfall-Semantiken benötigt, müssen
Datenmodell, Aufgaben-/Fallverwaltung und Governance zusätzlich
konzipiert und budgetiert werden.

### Variante B – Atlas orchestriert BPMN-Prozesse, n8n integriert Systeme

**Nach einem abgegrenzten PoC und einer expliziten Risikofreigabe**
kann dies geeignet sein, wenn standardisierte und langlaufende
Prozesssemantiken nötig sind und vorhandene n8n-Integrationen
wiederverwendet werden sollen.

~~~mermaid
flowchart TB
    P["Prozesseigentümer / BPMN- und DMN-Modelle"]
    A["Atlas: verbindlicher BPMN-Prozess- und Geschäftsfallzustand"]
    J["Persistenter Service-Task-Job mit Korrelations-ID"]
    N["n8n: technischer Integrationsworkflow"]
    X["Externe Systeme und APIs"]
    P --> A
    A --> J
    J --> N
    N --> X
    X --> N
    N -->|"Authentisierter Abschluss-/Fehler-Callback"|A
~~~

**Erforderlicher Integrationsvertrag:**

- Atlas führt Prozessinstanz und verbindlichen
  Geschäftsfallzustand; n8n verantwortet nur den delegierten
  Integrationsablauf.
- Die Atlas-Job-ID und ein stabiler Idempotenz-/Korrelationsschlüssel
  werden durch n8n und, soweit unterstützt, das Zielsystem
  durchgereicht.
- Callbacks werden authentisiert und spezifisch für den
  betreffenden Job autorisiert. Eine beliebige, vom Aufrufer
  übergebene Job-ID darf kein hinreichender Berechtigungsnachweis sein.
- Doppelte, verspätete und in falscher Reihenfolge eintreffende
  Antworten sind als reale Fehlerszenarien zu behandeln;
  Retry-, Abbruch- und Abgleichsregeln sind festzulegen.
- Ein Job wird erst abgeschlossen, wenn das externe Ergebnis
  feststeht. Technische Fehler und modellierte BPMN-Geschäftsfehler
  sind zu unterscheiden.
- Credentials gehören nicht in BPMN-Modelle oder übertragene
  Nutzdaten. Sicherheitsgrenzen, Log-Redaktion,
  Datenklassifizierung und Aufbewahrung müssen definiert sein.
- Prozessinstanzen, Atlas-Jobs, n8n-Ausführungen und nachgelagerte
  Aufrufe müssen in Monitoring und Betriebsanleitungen
  korrelierbar sein.
- Für denselben Geschäftsfall dürfen nicht zwei unabhängig
  gepflegte, konkurrierende Prozesszustandsmaschinen entstehen.

**Nicht jeder Service Task benötigt n8n.** Ist ein direkter
Atlas Worker einfacher und sauber geregelt, sollte dieser
eingesetzt werden.

### Variante C – Atlas für abgegrenzte Fälle, n8n unabhängig für andere Automatisierungen

Ein sinnvolles anfängliches Koexistenzmodell: Bereiche betreiben
n8n für ihre genehmigten Automatisierungen weiter; Atlas wird
isoliert mit einem klaren Prozess-PoC und geregelter Verantwortung
geprüft. Atlas wird nicht vorschnell zum unternehmensweiten
Standard erklärt.

**Wichtig:** Die Wahl von n8n für Integrationsaufgaben bedeutet
nicht, dass Atlas obligatorisch als übergeordnete Schicht
eingesetzt werden muss. Umgekehrt entscheidet die Einführung
von n8n nicht automatisch über die langfristige Strategie
zur BPMN-Geschäftsprozessorchestrierung.

## 13. Gemeinsamer Proof of Concept: Bewertungsverfahren

Ein belastbarer Entscheid basiert auf **demselben repräsentativen
Geschäftsszenario, denselben Annahmen zu Datenschutz und Betrieb
sowie denselben Abnahmekriterien**.

Beide Plattformteams, Enterprise Architecture, Prozesseigentümer,
Informationssicherheit und Betrieb sollen die Tests **vorab**
gemeinsam definieren. Reproduzierbare Nachweise sind
festzuhalten: Prozess-/Workflow-Dateien, Versionsstände,
Konfiguration, Logs, Screenshots, Recovery-Beobachtungen und
benötigter Aufwand.

| Prüfung | Erforderlicher Nachweis | Aussage |
|---|---|---|
| **P1 – Fachliche Modellprüfung** | Der Prozesseigentümer erkennt Entscheidungen, Verantwortlichkeiten, Fristen und Eskalationswege im gelieferten Artefakt. | Prüft Wartbarkeit und fachliches Verständnis, nicht die optische Gestaltung. |
| **P2 – Langer Wartezustand und Callback** | Einen Fall pausieren und nach Serverneustart über Zeit, Nachricht/Webhook oder menschliche Eingabe fortsetzen. | Beide Produkte erhalten die gleiche Anerkennung, wenn sie bestehen. |
| **P3 – Übergabe menschlicher Aufgaben** | Zuweisung, Übernahme, Frist, Berechtigung und Bearbeitungshistorie; zusätzlich Test mit unberechtigtem Benutzer. | Deckt echte Anforderungen und Zugriffslücken auf. |
| **P4 – Regeländerung** | Eine Geschäftsregel ändern und versionieren; angewendete Regel und Eingaben jedes Falls erklären. | DMN und n8n bzw. externe Regeln fair vergleichen. |
| **P5 – Gezielte Fehlerauslösung** | Absturz während eines Jobs, doppelter Callback, API-Fehler und Wiederherstellung aus Backup. | Korrektheit des Falls **und** Schutz vor doppelten externen Effekten testen. |
| **P6 – Versionierung** | Eine geänderte Prozessdefinition deployen, während ein Fall der früheren Version noch läuft. | Versionsstrategie belegen; keine automatische Migration laufender Fälle unterstellen. |
| **P7 – Betrieb** | Einen Fehler lokalisieren und beheben; Historie, Metriken, Alarmierung und Runbooks prüfen. | Operativen Aufwand und Wiederherstellungsdauer bewerten. |
| **P8 – Governance und Sicherheit** | Anmeldung, Least Privilege, Secrets, Audit sowie Aufbewahrung und Löschung prüfen. | Harte Sicherheitsanforderungen als Ausschlusskriterien behandeln. |
| **P9 – Deployment und Kosten** | Installation, Upgrade, Rollback/Restore, Lasttests und TCO für die **gewählte Edition** reproduzieren. | Tatsächliche Reife und Betriebsaufwand vergleichen. |

Für jede Anforderung sind **bestanden / bedingt bestanden /
nicht bestanden / nicht geprüft** und die Begründung festzuhalten.
Erfundene Durchsatzzahlen, pauschale «100 % BPMN»-Bewertungen oder
vorab festgelegte Gewinner haben in dieser Beurteilung keinen Platz.

### Empfohlenes Auswahlverfahren

1. **Zwingende Anforderungen zuerst festlegen.** Beispiele:
   rechtliche Zulässigkeit, Informationssicherheit, erforderliche
   Verfügbarkeit, Supportvorgaben und Verantwortung für den Betrieb.
   Eine Plattform, die ein zwingendes Produktionskriterium nicht
   erfüllt, kann dies nicht durch andere Stärken ausgleichen.
2. **Anwendungsfälle klassifizieren.** Integrationsworkflow,
   einfache menschliche Freigabe oder langlaufender, formal
   modellierter Geschäftsprozess.
3. **Bewertungskriterien gemeinsam gewichten.** BPMN-Semantik,
   Integrationsaufwand, Governance von Geschäftsregeln, Betrieb,
   Lizenzierung, Sicherheit, Personalbedarf, Migration und
   Gesamtbetriebskosten einbeziehen. Die Gewichtung darf nicht
   nachträglich dem bevorzugten Produkt angepasst werden.
4. **Mit demselben Szenario demonstrieren.** Beide Umsetzungen
   liefern nachprüfbare Belege für jede beanspruchte Eigenschaft.
5. **Architekturentscheid und Ausnahmen dokumentieren.**
   Review-Termin, Verantwortlichkeiten, Schnittstellen und
   messbare Ausstiegskriterien festlegen.

### Mindestvoraussetzungen für den Atlas-Produktivbetrieb

Vor einer Ausweitung über einen geschützten PoC hinaus müssen
die [offenen Compliance-Punkte](../compliance/isds-offene-punkte.md)
ausdrücklich behandelt werden. Dazu gehören Hochverfügbarkeit,
Wiederherstellungsziele, stabile Daten-/API-Migrationen,
Datenschutz und Zugriffstrennung. Akzeptierte Ausnahmen und
kompensierende Massnahmen müssen dokumentiert werden.
Ein funktional erfolgreicher PoC ist **kein Nachweis der
Produktionsreife**.

## 14. Empfehlung

Für die Unternehmensarchitektur und die Positionierung von Atlas:

1. **n8n als ernstzunehmende, ausgereifte Integrations- und
   Automatisierungsplattform anerkennen.** Die Entscheidung
   für n8n kann richtig sein und muss nicht rückgängig gemacht werden.
2. **Diese Entscheidung nicht ungeprüft zur unternehmensweiten
   BPM-Strategie erklären.** Den Bedarf an ausführbarem BPMN,
   DMN, Geschäftsfallzuständen, Aufgaben und formalem
   Prozess-Lifecycle separat beurteilen.
3. **Atlas als Kandidaten für BPMN-/DMN-Prozessorchestrierung
   positionieren**, nicht als n8n-Ersatz und heute auch nicht als
   pauschal produktionsbereiten Standard.
4. **Einen gemeinsamen, nachweisorientierten PoC** anhand eines
   repräsentativen langlaufenden Geschäftsfalls durchführen.
   Wo technisch und wirtschaftlich sinnvoll, n8n allein,
   Atlas allein und eine Kombination vergleichen.
5. **Die einfachste Architektur wählen, welche die Anforderungen
   erfüllt.** Zusätzliche Plattformen verursachen neue Schnittstellen,
   Sicherheitsgrenzen, Betriebsaufgaben und Kosten.
6. **Atlas schrittweise und unter Bedingungen einführen**,
   erst nachdem Produktionsvoraussetzungen, Zuständigkeiten
   und Lebenszykluspflichten erfüllt sind.

> **Eine Plattform zur Workflow-Automatisierung ist nicht
> automatisch eine vollständige Strategie für das
> Geschäftsprozessmanagement.** Eine Entscheidung für n8n löst
> ein reales Automatisierungsproblem. Sie beantwortet aber
> noch nicht, wer fachliche End-to-End-Geschäftsprozesse
> unternehmensweit modelliert, ausführt und verantwortet.

## Quellen und Pflege

**Nachweise aus dem Atlas-Projekt**

- [README: aktueller Stand, Funktionsumfang und Lizenz](../../README.md)
- [Roadmap](../../ROADMAP.md) und
  [BPMN-Conformance-Übersicht](../../conformance/COVERAGE.md)
- [Architektur](../ARCHITECTURE.md) und
  [Invarianten](../architecture/invariants.md)
- [Worker-Ausführungsmodell](../adr/0203-worker-execution-model.md)
- [DMN-Integration](../adr/0014-dmn-business-rule-tasks-via-temis.md)
- [Kubernetes-Deployment](../../deploy/README.md)
- [Compliance-Dokumentation](../compliance/README.md) und
  [offene Sicherheits- und Produktionslücken](../compliance/isds-offene-punkte.md)
- [Tatsächliche Atlas-Lizenz](../../LICENSE)

**Primärquellen zu n8n**

- [Wait Node](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.wait/)
- [Queue Mode](https://docs.n8n.io/hosting/scaling/queue-mode/)
- [Fehlerbehandlung](https://docs.n8n.io/flow-logic/error-handling/)
- [Ausführungshistorie](https://docs.n8n.io/workflows/executions/all-executions/)
- [Integrationskatalog](https://n8n.io/integrations/)
- [Community- und kostenpflichtige Editionsfunktionen](https://docs.n8n.io/hosting/community-edition-features/)
- [Security Audit](https://docs.n8n.io/hosting/securing/security-audit/)
- [End-User Credentials](https://docs.n8n.io/administer/manage-credentials/end-user-credentials) (Enterprise)
- [Sustainable Use License](https://docs.n8n.io/sustainable-use-license/)

**Pflegeregel:** Dieses Dokument ist bei Änderungen an der
Atlas-Lizenz, der Aussage zur Produktivbereitschaft, der
Deployment-Topologie oder wesentlichen BPMN-Abdeckung
mitzuführen. Produktvergleichsaussagen sind zu datieren;
«implementiert», «getestet», «geplant» und «produktiv
bewährt» bleiben klar voneinander getrennt.
