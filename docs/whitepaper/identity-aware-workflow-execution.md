<!-- Source: Atlas Identity-Aware Workflow Whitepaper, 23 October 2026. Markdown edition derived from the original Word manuscript. -->

# Identity-Aware Workflow Execution

*Wenn Geschäftsprozesse unter klaren Identitäten und verbindlichen Regeln handeln*

> **Nicht nur automatisieren. Autorisiert handeln. Verantwortung
> nachweisen.**

Ein Architektur- und Produktkonzept für delegierte Ausführung,
Policy-basierte Autorisierung und nachvollziehbare
Geschäftsautomatisierung.

Zielgruppe Enterprise-Architektur · Informationssicherheit ·
Prozessmanagement · IT-Leitung

Ausgabe 23. Oktober 2026

Status Strategisches Zielbild; ADR und Umsetzung in Arbeit

Projekt github.com/pblumer/atlas

# Management Summary

Unternehmen automatisieren immer mehr fachliche Entscheidungen und
Eingriffe in externe Systeme. Die Frage, ob eine Integration technisch
funktioniert, reicht dabei nicht mehr aus. Entscheidend ist, ob eine
konkrete Aktion fachlich zulässig ist, welche Identität sie ausführt und
ob die Entscheidung später belastbar erklärt werden kann.

Atlas verfolgt hierfür das Zielbild einer identitätsbewussten,
policygesteuerten Prozessausführung. Ein BPMN-Prozess beschreibt den
Ablauf; eine externe Identität und eine kontrollierte Verbindung
ermöglichen den Zugriff; ein Policy Decision Point (PDP) bewertet die
Berechtigung; ein Policy Enforcement Point (PEP) verhindert unzulässige
Aktionen. Entscheidung und Ergebnis werden dauerhaft nachvollziehbar
dokumentiert.

> **Die zentrale Frage lautet nicht nur: «Kann der Worker das?» Sondern:
> «Darf er diese konkrete Aktion jetzt, in diesem Auftrag, mit dieser
> Identität ausführen?»**

Der Ansatz verbindet technische Zugriffskontrolle mit fachlichen Regeln,
Genehmigungen und Prozesshistorie. Er schafft eine Grundlage für
regulierte Abläufe, etwa in Verwaltung, Finanzdienstleistungen,
Gesundheit oder grösseren Unternehmen. Als Produktdifferenzierung ist
die integrierte Durchsetzung entscheidend – nicht das blosse
Vorhandensein von OAuth oder einer Rule Engine.

## Fünf Kernversprechen

- Klare Identität: Jede externe Aktion besitzt einen nachweisbaren
  technischen Akteur und einen dokumentierten fachlichen Auftrag.

- Policy vor Wirkung: Eine zentrale, nicht umgehbare
  Autorisierungsgrenze entscheidet vor dem externen Seiteneffekt.

- Delegation mit Grenzen: Benutzerkonten und technische Identitäten
  können nur in genehmigten Kontexten eingesetzt werden.

- Erklärbarkeit: Die verwendete Policy-Version, relevante
  Entscheidungsgründe und das Ausführungsergebnis bleiben
  unterscheidbar.

- Robustheit: Persistenz, Recovery und Worker-Retries respektieren
  Änderungen an Berechtigungen und Freigaben.

## Status und Einordnung

Atlas besitzt bereits eine dauerhafte BPMN-Ausführungsarchitektur,
Benutzer- und Rollenmodelle, Worker-Strukturen und OAuth-Bausteine. Das
hier beschriebene integrierte Autorisierungsmodell ist ein Zielbild,
dessen ADR und Implementierung derzeit erarbeitet werden. Das
öffentliche Repository kennzeichnet Atlas weiterhin als Developer
Preview (0.x) und nicht als produktionsreife Plattform.

# 1. Das Problem: Automatisierung ohne eindeutigen Auftrag

Viele Automatisierungen nutzen ein technisches Credential, das einem
Workflow oder Connector hinterlegt wird. Damit kann der Connector eine
Aktion ausführen. Ob sie im konkreten Geschäftsfall berechtigt ist, wird
oft ausserhalb des Integrationsschritts entschieden – oder bleibt
implizit.

Das führt zu grundlegenden Fragen: Ein Prozess wird durch Person A
gestartet, von Person B genehmigt und von einer Anwendung mit
weitreichenden Rechten ausgeführt. Ist später klar, wer den Auftrag
erteilt hat? War die Freigabe für genau diese Aktion gültig? Wurde der
Benutzer zwischen Freigabe und Ausführung deaktiviert? Kann ein Retry
trotz zwischenzeitlichem Widerruf erneut schreiben?

Die blosse Zuordnung eines Tokens zu einem Worker beantwortet diese
Fragen nicht. Ebenso wenig genügt ein Audit-Log, das erst nach der
externen Aktion geschrieben wird. Notwendig ist eine verbindliche,
technische Autorisierungsgrenze.

# 2. Das Atlas-Zielbild: Identität, Auftrag und Ausführung trennen

Der Ansatz trennt sechs Begriffe, die in einfachen Integrationslösungen
häufig vermischt werden.

| **Baustein**                  | **Bedeutung**                                                                                      |
|-------------------------------|----------------------------------------------------------------------------------------------------|
| Principal                     | Natürliche Person oder technisches Subjekt innerhalb von Atlas.                                    |
| External Identity             | Konto oder Workload-Identität in Microsoft, Google oder einem anderen Zielsystem.                  |
| Connection                    | Kontrollierte Verbindung mit genehmigten Scopes, Lebenszyklus und geschützter Credential-Referenz. |
| Execution Context             | Konkreter Auftrag: Initiator, Genehmiger, ausführende Identität, Ressource und Prozessbezug.       |
| Worker / Worker Instance      | Konfigurierte Integrationsfähigkeit bzw. deren laufende technische Ausführung.                     |
| Policy Decision / Enforcement | Entscheidung über die Zulässigkeit und verpflichtende Durchsetzung vor der Aktion.                 |

Ein Worker kann fest an eine technische Identität gebunden sein oder –
wenn sicher unterstützt – eine pro Job autorisierte delegierte Identität
verwenden. Die Identität, mit der sich eine Worker Instance gegenüber
Atlas ausweist, ist ausdrücklich nicht automatisch ihre Identität beim
externen Zielsystem.

## Unterstützte Identitätsklassen

- Personal Identity: Ein Benutzer autorisiert eine Verbindung, die
  innerhalb definierter Grenzen in seinem Namen genutzt werden darf.

- Service Identity: Ein technischer Account führt wiederkehrende oder
  unbeaufsichtigte Aufgaben aus.

- Workload Identity: Beispielsweise eine Azure Managed Identity oder
  eine föderierte Maschinenidentität, soweit vom Zielsystem unterstützt.

Die Verknüpfung eines Microsoft- oder Google-Kontos mit einem
Atlas-Benutzer sollte weder das Atlas-Login verändern noch pauschal jede
Prozessausführung berechtigen. Die Nutzung einer Connection bleibt eine
separate Autorisierungsentscheidung.

# 3. Policy-Based Authorization als verbindlicher Kontrollpunkt

Das Konzept orientiert sich an der erprobten Trennung zwischen Policy
Administration Point (PAP), Policy Decision Point (PDP), Policy
Information Point (PIP) und Policy Enforcement Point (PEP). Atlas muss
hierfür keine vollständige XACML-Laufzeit übernehmen.

| **Komponente** | **Aufgabe**                                                                       | **Sicherheitsanforderung**                                 |
|----------------|-----------------------------------------------------------------------------------|------------------------------------------------------------|
| PAP            | Policies erstellen, testen und versioniert veröffentlichen.                       | Änderungen nur durch berechtigte Administration.           |
| PIP            | Vertrauenswürdige Identitäts-, Rollen-, Ressourcen- und Prozessattribute liefern. | Keine unvalidierten BPMN-Variablen als Sicherheitsfakten.  |
| PDP            | Aus konkretem Kontext Permit, Deny oder unentscheidbar ableiten.                  | Deterministische Kombination; fail closed.                 |
| PEP            | Vor dem externen Aufruf Entscheidung durchsetzen.                                 | Nicht durch Modeler, Worker-Protokoll oder Retry umgehbar. |

## Was eine Policy prüfen kann

Beispiel: Ein SharePoint-Worker soll ein Dokument löschen. Die Policy
berücksichtigt den Antragsteller, die Klassifikation des Dokuments, den
Zielbereich, die verwendete delegierte Identität, eine gültige
Genehmigung sowie die technische Berechtigung der Connection. Nur wenn
sämtliche notwendigen Bedingungen erfüllt sind, darf der externe
API-Aufruf stattfinden.

Technische Basisregeln – etwa eine gültige Connection, zulässige
Ressourcengrenzen und überprüfte Identitäten – dürfen nicht von einer
fachlichen DMN-Regel überschrieben werden. Die Plattform sollte fehlende
oder fehlerhafte Entscheidungsdaten als Ablehnung beziehungsweise
kontrollierten Fehler behandeln, nicht als stillschweigende Freigabe.

## DMN und FEEL richtig einsetzen

Atlas nutzt bereits pblumer/feel und pblumer/temis. Deshalb bietet es
sich an, fachliche Policies als versionierte DMN-Entscheidungen und
begrenzte FEEL-Bedingungen zu modellieren. Das erlaubt transparente
Entscheidungstabellen, ohne eine parallele Regel-Engine einzuführen.
Technische Sicherheitsprüfungen bleiben jedoch eine separate, nicht
überschreibbare Schicht.

# 4. Praxisbeispiel: Berechtigtes Dokumentenmanagement

Eine Mitarbeiterin beantragt in Atlas die Veröffentlichung eines
vertraulichen Dokuments. Der BPMN-Prozess prüft die Angaben, fordert
gegebenenfalls eine Genehmigung an und beauftragt anschliessend einen
SharePoint-Worker.

| **Schritt**   | **Was Atlas nachvollziehbar festhält**                                                                 |
|---------------|--------------------------------------------------------------------------------------------------------|
| 1 Antrag      | Wer hat den Prozess gestartet und welche konkrete Operation wurde beantragt?                           |
| 2 Genehmigung | Wer hat welche unveränderten Aktionsparameter genehmigt, mit welcher Gültigkeit?                       |
| 3 Policy      | Welche veröffentlichte Policy-Version erlaubte oder untersagte die Aktion – und warum?                 |
| 4 Identität   | Mit welchem externen Microsoft-Konto oder welcher Service Identity wird ausgeführt?                    |
| 5 Ausführung  | Welche Worker Instance führte die Operation aus, mit welchem Ergebnis und Korrelationsbezug?           |
| 6 Recovery    | Was ist dauerhaft bekannt, und welche Berechtigung muss vor einem neuen Versuch erneut geprüft werden? |

## Beispielhafte Geschäftsregeln

- Lesen ist für berechtigte Projektmitglieder erlaubt.

- Das Erstellen eines Dokuments benötigt eine Verbindung mit passenden
  delegierten Rechten.

- Das Löschen ist standardmässig untersagt; eine ausdrückliche
  Genehmigung kann es für bestimmte Rollen und Ressourcen erlauben.

- Antragsteller und Genehmiger müssen unterschiedliche stabile
  Benutzeridentitäten sein.

- Eine Genehmigung gilt nur für den konkreten unveränderten Auftrag –
  nicht für beliebige spätere API-Aufrufe.

Das Zielsystem trifft zusätzlich eigene Zugriffsentscheidungen. Eine
Atlas-Policy kann keine fehlende Microsoft-Berechtigung ersetzen.
Umgekehrt bedeutet ein gültiges Microsoft-Token noch keine fachliche
Freigabe durch Atlas.

# 5. Auditierbarkeit, Verantwortung und Grenzen

Das Modell unterscheidet Initiator, Auftraggeber, Genehmiger, technische
Ausführungsidentität und tatsächliche Worker Instance. Diese Kette
schafft fachliche Nachvollziehbarkeit. Sie ist jedoch nicht
gleichzusetzen mit einer automatischen rechtlichen Haftungszuweisung.
Verantwortung entsteht auch aus Organisation, Funktion, Weisungen und
geltendem Recht.

Eine historische Permit-Entscheidung ist ein unveränderlicher Nachweis
dafür, was zu diesem Zeitpunkt entschieden wurde. Sie darf aber nicht
dauerhaft neue Aktionen freischalten. Widerrufene Connections, geänderte
Rollen oder veränderte Policies müssen bei einem erneuten
Ausführungsversuch entsprechend dem festgelegten Konsistenzmodell
berücksichtigt werden.

Auch eine dauerhafte BPMN-Engine kann nicht pauschal exakt-einmalige
Seiteneffekte in einem fremden System garantieren: Ein API-Aufruf kann
erfolgreich sein, während die Bestätigung an Atlas verloren geht. Dafür
sind Idempotenzstrategien, Korrelation, Retries und ein transparentes
Fehlerbild nötig.

## Minimaler Audit-Nachweis

Sinnvoll sind Prozess-/Job-ID, stabile Beteiligten-IDs,
Connection-Referenz, Zielressource, Operation, Policy-ID und Version,
Decision-ID, Entscheidungsgrund, Freigabebelege, Zeitpunkte, externe
Korrelation sowie das beobachtete Ergebnis. Tokens, Passwörter und
private Schlüssel gehören niemals in Prozessvariablen oder das Event
Log.

# 6. Architektur und Betrieb: Wo Sicherheit erzwungen wird

Atlas basiert auf einem kompilierten BPMN-Ausführungsgraphen, einem
Single-Writer pro Partition und dauerhaft protokollierten Events. Die
Reihenfolge «durable before visible» bleibt zwingend: Jobs werden erst
nach WAL, fsync und State-Commit extern verfügbar.
Autorisierungsnachweise, die vor einer Aktion erforderlich sind, müssen
ebenfalls vor dem Seiteneffekt verlässlich persistent sein.

Das bedeutet nicht, OAuth- oder PDP-Netzwerkaufrufe in den Processor
oder in applyToState einzubauen. Die Policy-Bewertung und
Identitätsauflösung müssen an kontrollierten Worker- oder Servicegrenzen
erfolgen, mit klaren Regeln für Persistenz, Gültigkeit und erneute
Prüfung.

## Besonders kritische Punkte

- Ein nicht vertrauenswürdiger externer Worker darf den zentralen
  Kontrollpunkt nicht einfach umgehen können. Das erfordert geeignete
  Isolation, Credential-Grenzen und restriktive Worker-Protokolle.

- Refresh Tokens dürfen bei mehreren Worker Instances nicht
  konkurrierend erneuert und anschliessend mit veralteten Werten
  überschrieben werden.

- Die Policy muss auch bei verspäteten Jobs, veränderten Rollen,
  deaktivierten Benutzern und Recovery korrekt wirken.

- Unternehmensrichtlinien – etwa Microsoft Entra Consent und Conditional
  Access – dürfen nicht umgangen werden.

- Autorisierungsprüfungen dürfen die garantierte Deterministik der
  Engine nicht aufbrechen.

# 7. Geschäftlicher Nutzen und Positionierung

| **Interessengruppe**   | **Nutzen**                                                                                               |
|------------------------|----------------------------------------------------------------------------------------------------------|
| Fachbereiche           | Klare Freigaberegeln und sichtbare Verantwortung über Systemgrenzen hinweg.                              |
| Informationssicherheit | Konsistente Durchsetzung und überprüfbare Identitäts-/Berechtigungskette.                                |
| Revision & Compliance  | Versionierte Entscheidungsevidenz statt ausschliesslich technischer API-Logs.                            |
| Betrieb                | Nachvollziehbare Fehler, Widerrufe, Wiederholungen und ausgeführte Aktionen.                             |
| Entwicklung            | Wiederverwendbare Identity-, Connection- und Policy-Infrastruktur statt pro Worker neu erfundener Logik. |

## Einordnung gegenüber n8n

n8n bietet umfangreiche Integrationsmöglichkeiten, Credentials,
Benutzerverwaltung und Möglichkeiten zur Workflow-Orchestrierung. Seine
operative Reife und Breite sind wichtige Stärken. Atlas sollte daraus
keine unbelegten Defizitbehauptungen ableiten.

Die strategische Differenzierung, die Atlas anstrebt, liegt in der
gemeinsamen Verbindung von standardisierten BPMN-Prozessen,
DMN/FEEL-Entscheidungen, pro Aktion bindender Autorisierung,
differenzierten Ausführungsidentitäten und dauerhaftem
Entscheidungsnachweis. Ob dieser Vorsprung gegenüber einer bestimmten
n8n-Edition und konkreten Architektur tatsächlich besteht, muss nach
Implementierung anhand identischer Szenarien demonstriert werden.

> **Wettbewerbsvorteil entsteht erst, wenn Policy Enforcement nicht nur
> sichtbar, sondern nachweislich nicht umgehbar ist.**

# 8. Umsetzungsweg und Nachweis der Wirksamkeit

Eine glaubwürdige Umsetzung sollte mit einem eng gefassten
SharePoint-Szenario beginnen und danach kontrolliert auf weitere Worker
Types erweitert werden.

| **Etappe**                   | **Ergebnis / Nachweis**                                                                       |
|------------------------------|-----------------------------------------------------------------------------------------------|
| Architektur und Threat Model | ADR, Identitätsmodell, Vertrauensgrenzen und Migrationsstrategie.                             |
| Connections und Delegation   | Sichere Konto-Verknüpfung, Token-Lifecycle, Besitzer und Widerruf.                            |
| Policy PDP + PEP             | Versionierte Regeln, zentrale Entscheidung und technisch erzwungener Schutz vor Side Effects. |
| BPMN / DMN-Integration       | Nachweisbares Vier-Augen-Prinzip inklusive Obligation und unveränderter Aktionsparameter.     |
| SharePoint-PoC               | Lesen, Erstellen und kontrolliertes Ablehnen/Löschen unter überprüfbaren Berechtigungen.      |
| Recovery und Sicherheit      | Widerruf, Retry, Restart, Worker-Protokoll, Audit und Angriffstests.                          |
| Skalierung auf andere Worker | Microsoft Mail, Entra, Google, Jira und Generic REST nach Kompatibilitätsprüfung.             |

## Messbare Akzeptanzkriterien

- Jeder geschützte externe Aufruf kann auf eine gültige, überprüfbare
  Policy-Entscheidung zurückgeführt werden.

- Ein unberechtigter Benutzer und ein manipuliertes BPMN-Datenfeld
  können keine fremde Connection verwenden.

- Ein fehlender oder fehlerhafter Entscheid fällt nicht auf Permit
  zurück.

- Eine nachträgliche Änderung von Berechtigungen wird bei neuen
  Ausführungsversuchen gemäss festgelegter Policy berücksichtigt.

- Ausführung und Entscheidung bleiben auch nach einem Neustart
  nachvollziehbar, ohne Tokens im Event Log abzulegen.

- Die Lösung funktioniert für unterstützte externe Worker nicht nur im
  UI, sondern an der durchsetzbaren Backend-Grenze.

# 9. Fazit

Identity-Aware Workflow Execution erweitert die Leitfrage der
Prozessautomatisierung. Es geht nicht mehr allein um den erfolgreichen
technischen Ablauf, sondern um das überprüfbare Recht, eine konkrete
Aktion im Namen einer bestimmten Identität auszuführen.

Der Ansatz verbindet die Stärken von BPMN, DMN und einem dauerhaften
Ausführungsmodell mit einer verbindlichen Autorisierungsarchitektur. Bei
konsequenter Realisierung könnte Atlas damit eine klare Position im
Segment governanceorientierter Business Process Automation erreichen.

> **Atlas soll nicht nur wissen, was geschehen ist. Atlas soll auch
> belegen können, weshalb es geschehen durfte.**

# Quellen und Projektstand

• Atlas Repository und README (Developer Preview):
https://github.com/pblumer/atlas

• Atlas ADR-0203 – Worker execution model:
https://github.com/pblumer/atlas/blob/main/docs/adr/0203-worker-execution-model.md

• Atlas ADR-0141 – SharePoint connector:
https://github.com/pblumer/atlas/blob/main/docs/adr/0141-sharepoint-connector.md

• Atlas ADR-0210 – Federated authentication:
https://github.com/pblumer/atlas/blob/main/docs/adr/0210-federated-authentication.md

• Atlas ADR-0281 – Session role revocation:
https://github.com/pblumer/atlas/blob/main/docs/adr/0281-session-role-revocation.md

• Atlas Produktvergleich n8n:
https://github.com/pblumer/atlas/blob/main/docs/comparisons/n8n.md

• Microsoft Graph – selected permissions:
https://learn.microsoft.com/en-us/graph/permissions-selected-overview

• NIST SP 800-162 – ABAC:
https://csrc.nist.gov/pubs/sp/800/162/upd2/final

• OASIS XACML 3.0 (PDP/PEP/PIP/PAP):
https://docs.oasis-open.org/xacml/3.0/xacml-3.0-core-spec-os-en.html

• OMG DMN Specification: https://www.omg.org/spec/DMN/

*Redaktioneller Hinweis: Architektur-Zielbild; keine Bestätigung der
abgeschlossenen Umsetzung oder einer produktiven BIT-Zulassung. Stand:
23. Oktober 2026.*
