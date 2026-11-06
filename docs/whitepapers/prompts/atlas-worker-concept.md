Rolle und Auftrag
Du bist ein erfahrener technischer Autor für Software-Architektur. Schreibe ein technisches Whitepaper über das Worker-Konzept der Workflow-Engine «atlas». Es soll genau erklären, wie das Konzept funktioniert und welche Vorteile es hat.

Zielgruppe: Architektinnen und Architekten, Betriebsverantwortliche, Sicherheitsverantwortliche sowie Hersteller, die eigene Worker entwickeln wollen. Technisches Grundwissen (Prozesse, HTTP, Signaturen, Hashes) darfst du voraussetzen.

Verbindliche Regeln
1. Verwende ausschliesslich die Fakten unten. Erfinde keine Funktionen, Zahlen, Kunden, Benchmarks oder Zitate. Wenn dir für einen Abschnitt etwas fehlt, schreibe «[offen: …]», statt die Lücke zu füllen.
2. Kennzeichne jede Aussage über Funktionalität eindeutig als «umgesetzt» oder «geplant». Stelle Geplantes nie als vorhanden dar. Am Ende steht eine Statustabelle.
3. Sprache: Deutsch, sachlich und formell, Schweizer Rechtschreibung (immer «ss», nie «ß»), keine Marketing-Sprache, keine Übertreibungen, keine Emojis.
4. Fachbegriffe erklärst du beim ersten Auftreten kurz. Die Begriffe Worker Type, Worker und Worker Instance verwendest du durchgehend gleich (Definitionen unten).
5. Diagramme als Mermaid-Codeblöcke: mindestens eine Komponentenübersicht, ein Sequenzdiagramm «Job von der Erzeugung bis zum Abschluss» und eines «Installation und Start eines Worker Types».
6. Umfang: etwa 12–18 Seiten. Kurze Absätze, klare Überschriften.
7. Benenne Nachteile und Grenzen offen, in einem eigenen Kapitel.

Gewünschte Gliederung
1. Zusammenfassung (eine halbe Seite)
2. Ausgangslage und Problem
3. Begriffe
4. Architekturüberblick
5. Trennung der Adressräume
6. Das Worker-Protokoll (inkl. Handshake und Versionierung)
7. Wie Worker-Binaries auf den Rechner gelangen (heute und künftig)
8. Start, Überwachung und Konfiguration durch den Supervisor
9. atlas hub: Verteilung, Signaturen, Kuratierung
10. Sicherheit (Bedrohungsmodell, Schutzmassnahmen, Restrisiken)
11. Aktualisierung, Versionierung, Widerruf
12. Zielumgebungen (Windows ohne Container, Linux, Kubernetes)
13. Lizenzmodell (als Vorschlag gekennzeichnet)
14. Vorteile gegenüber der bisherigen Lösung, mit fairer Darstellung der Gegenposition
15. Grenzen, offene Fragen, nächste Schritte
16. Statustabelle (umgesetzt / geplant)

FAKTEN

A. Ausgangslage
- atlas ist eine BPMN-2.x-Workflow-Engine in Go, ausgeliefert als ein einziges Binary.
- Die Engine speichert jeden Zustandsübergang als Ereignis in einem Write-Ahead-Log (Append, fsync, dann Commit).
- Jede Partition hat genau einen schreibenden Goroutine (Single Writer).
- Grundsatz «durable before visible»: Ein Job wird für einen Worker erst sichtbar, wenn er dauerhaft auf der Platte steht.
- Bisher waren alle 33 Integrationen (Worker Types, z. B. Jira, Mail, SQL, Active Directory, Entra ID, Exchange) fest in das atlas-Binary kompiliert. Jede Änderung oder Sicherheitslücke in einer Integration verlangte ein neues atlas-Release. Dritte konnten keine eigenen Integrationen ausliefern.
- Ein Teil jedes Worker Types lag auf der Serverseite als typspezifischer Code (Konfigurationsprüfung, Übergabe der Zugangsdaten, Mock-Ansichten).

B. Begriffe
- Worker Type: eine installierbare Fähigkeit bzw. Implementierung (z. B. «Jira»).
- Worker: eine konfigurierte Ausprägung eines Worker Types (Ziel, Identität, Zugangsdaten-Verweise), z. B. «jira-produktion».
- Worker Instance: ein laufender Prozess, der Jobs eines Workers abarbeitet. Es kann null bis viele davon geben.
- Job: dauerhafte Arbeitseinheit, die die Engine für einen Service Task erzeugt.

C. Trennung der Adressräume (umgesetzt)
- Fremder Code läuft nie im Prozess der Engine. Abgelehnt wurden:
  - Go-Plugins (`plugin.Open`)
  - WebAssembly im Prozess
  - beliebige Kommandozeilen aus Paketen
- Worker laufen als eigene Betriebssystem-Prozesse und kommunizieren nur über das HTTP-basierte Worker-Protokoll.
- Ein abstürzender oder fehlerhafter Worker kann die Engine nicht destabilisieren.
- Es gibt drei Ausführungsarten:
  1. In-Engine-Handler für deterministische Engine-Semantik: FEEL-Ausdrücke, DMN, User Tasks. Das sind keine Worker.
  2. Das mitgelieferte Binary `atlas-workers` für die eingebauten Worker Types.
  3. Installierte Artefakte von Atlas, Drittanbietern oder der eigenen Organisation.
- `atlas-workers` enthält nur den Worker-Modus, ohne Server, Engine, Web-Oberfläche und MCP-Server. Gemessen auf linux/amd64: atlas 88 MB, atlas-workers 35 MB.
- Liegt `atlas-workers` neben dem Server-Binary, startet der Supervisor die eingebauten Worker damit, sonst mit dem eigenen Worker-Modus. Die Umgebungsvariable `ATLAS_WORKERS_BINARY` übersteuert das (`self` oder ein Pfad).
- Das SDK `workersdk` (Go) bindet nichts von atlas ein, weder Engine noch Speicher-Engine noch andere atlas-Pakete. Ein automatischer Test erzwingt das.
- Einschränkung: Das Server-Binary enthält vorerst weiterhin alle eingebauten Worker Types, weil einige noch im Server-Prozess laufen. Das Entfernen ist für die Migration geplant.

D. Worker-Protokoll v1 (umgesetzt)
- Es ist sprachneutral als Vertrag dokumentiert; Worker sind in jeder Sprache möglich.
- Nachrichten:
  - `POST /api/v1/jobs/activate` (Long-Poll-Lease)
  - `…/complete`, `…/fail` (mit Wartezeit vor erneutem Versuch), `…/error` (BPMN-Fehler)
  - `…/authorize` (vor einer Aktion fragen, ADR-0475)
  - Abfragen für Formular-Lookups: `/api/v1/worker-queries/activate` und `…/answer`. Sie sind nie dauerhaft, und Fehlermeldungen enthalten nie Suchbegriffe.
- Ausführung «mindestens einmal»: Jeder Lease hat ein Lease-Token; Meldungen nach Ablauf des Leases werden abgewiesen. Handler mit Seiteneffekten müssen idempotent sein, der Job-Key eignet sich als Schlüssel.
- Handshake: Jeder Poll trägt `protocol` (Hauptversion) und optional `workerType` {id, version, digest}.
- Kompatibilitätsregel:
  - Innerhalb einer Hauptversion sind Änderungen nur additiv.
  - Der Server akzeptiert die aktuelle und die vorherige Hauptversion.
  - Ein Poll ohne Versionsangabe gilt als Version 1.
  - Eine unbekannte Version wird mit HTTP 400 und den unterstützten Versionen abgewiesen.
  - Jede Antwort trägt den Header `X-Atlas-Worker-Protocol`.
- Die Workers-Ansicht zeigt pro Instanz Protokollversion, Typ-ID, Version und Digest.

E. Wie Binaries auf den Rechner gelangen
Umgesetzt:
- Ein Administrator lädt Manifest und Programmdatei für die Plattform des Servers hoch: `POST /api/v1/worker-types/installed` (multipart).
- Der Server prüft die Programmdatei gegen den SHA-256-Wert im Manifest, bevor er etwas behält.
- Ablage inhaltsadressiert unter `<data>/workertypes/<id>/<version>/<sha256>/`, atomar geschrieben, Metadaten zuletzt.
- Ein Release ist unveränderlich: derselbe Digest erneut ändert nichts, ein anderer Digest unter derselben Version wird mit 409 abgewiesen.
- Weitere Routen: Liste (`GET`) und Entfernen (`DELETE …/{id}/{version}`). Beim Entfernen wird zuerst die Instanz gestoppt.
- Alle Routen sind nur für Administratoren und bewusst nicht über MCP (KI-Agenten) erreichbar.

Manifest (umgesetzt):
- `apiVersion` atlas.io/v1alpha1, `kind` WorkerType, `metadata.id` (umgekehrte DNS-Schreibweise, z. B. com.example.sap-s4), `metadata.version` (SemVer).
- `spec.jobTypes`, `spec.operations`.
- `spec.artifacts`: eine Programmdatei pro Plattform, mit `platform` (os/arch), `mediaType` `application/vnd.atlas.worker.executable` und `digest` (sha256).
- `spec.configuration`: Konfiguration als JSON Schema.

Geplant:
- Download aus atlas hub nach Signaturprüfung.
- Datei-Import für Umgebungen ohne Internetzugang.
- Erstanbieter-Artefakte vorinstalliert im Distributionsarchiv.
- Pflichtplattformen für eine Listung im Hub: linux/amd64, linux/arm64, windows/amd64.
- Optional OCI-Images für Container und Kubernetes.

F. Start, Überwachung, Konfiguration (umgesetzt)
- Der Supervisor startet installierte Programmdateien ohne Argumente.
- Umgebung: nur eine Positivliste (PATH, HOME, temporäre Verzeichnisse, Locale, Proxy-Variablen), nie die übrige Umgebung des Servers mit dessen Geheimnissen.
- Dazu kommt der Startvertrag:
  - `ATLAS_SERVER_URL`, `ATLAS_WORKER_ID`, `ATLAS_TOKEN`
  - `ATLAS_WORKER_TYPE_ID`, `_VERSION`, `_DIGEST` (die vom Supervisor geprüfte Identität)
  - `ATLAS_WORKER_CONFIG`
- Vor jedem Start berechnet der Supervisor den Hash neu. Eine auf der Platte veränderte Datei startet er nicht (Zustand «refused»).
- Installierte Releases werden beim Serverstart wieder gestartet.
- Konfiguration:
  - Das Manifest enthält ein JSON Schema. Eigenschaften mit `writeOnly: true` sind Secrets.
  - Ein Administrator legt Worker an: `PUT /api/v1/worker-types/installed/{id}/workers/{name}`.
  - Die Einstellungen werden gegen das Schema jedes installierten Releases geprüft.
  - Secrets dürfen nur als Name eines Vault-Eintrags angegeben werden; die API zeigt ihren Wert nie.
  - Beim Start erhält die Instanz die Konfiguration mit aufgelösten Secrets. Bei einer Änderung startet sie neu.
- Der serverseitige Code dafür kennt keinen einzelnen Worker Type.
- Ein Pilot-Typ («echo»), der nur auf `workersdk` aufbaut, belegt den ganzen Ablauf in einem automatisierten End-to-End-Test.

G. atlas hub
Umgesetzt (bisher nur für Application Blueprints, nicht für Worker):
- Statische, signierte Registry: `index.json` plus Ed25519-Signatur über die exakten Bytes.
- Der Schlüssel ist vom Betreiber fest hinterlegt. Es gibt kein «Trust on first use» und keinen Schlüssel aus heruntergeladenen Inhalten.
- Für einen Schlüsselwechsel dürfen höchstens zwei Schlüssel gleichzeitig hinterlegt sein. Neue Schlüssel werden über einen festen, mit dem alten Schlüssel signierten Ankündigungskanal mit mindestens sieben Tagen Vorlauf bekannt gegeben.
- Strenge Download-Prüfungen: Grössengrenzen, keine Weiterleitungen, SSRF-Schutz.

Geplant für Worker Types:
- Neue Release-Art `worker-type` (neue Version des Index-Schemas).
- Zwei Signaturen, beide Pflicht: die Publisher-Signatur über die Release-Angaben (Manifest- und Artefakt-Digests) und die Registry-Signatur über den Index.
- Ein Namespace gehört genau einem Publisher. `io.atlas.*` wird nur mit dem atlas-Release-Schlüssel akzeptiert.
- Vier Herkünfte: atlas kostenlos, atlas kommerziell, Drittanbieter bzw. Community, organisationsintern (eigene Registry oder Datei-Import).
- Kuratierung (Vorschlag, Status «Proposed»), zwei Stufen:
  - «verified»: Identität und Domain geprüft, Herkunft des Builds belegt, automatische Prüfungen und manuelles Review. Bei geschlossenem Quellcode: Review unter Geheimhaltung oder Herstellerzusicherung plus Verhaltenstest. Standardmässig erlaubt.
  - «community»: Identität geprüft, nur automatische Prüfungen. Standardmässig abgelehnt; wenn erlaubt, nur im Container oder in Kubernetes.
  - Ungeprüfter Code wird nie mit dem Hub-Schlüssel signiert; er existiert nur in Registries Dritter, die ein Betreiber bewusst hinzufügt.
  - Pro Release steht im signierten Index, was geprüft wurde. Die Signatur besagt, was geprüft wurde, nicht, dass der Code sicher ist.
- Kommerzielle Lizenzprüfungen sind, wenn überhaupt, Sache des Workers, nie der Engine.

H. Sicherheit
Umgesetzt:
- Fremder Code läuft nur im eigenen Prozess.
- Digest-Prüfung bei der Installation und vor jedem Start.
- Unsignierte Installationen sind standardmässig abgelehnt (HTTP 403) und nur mit dem Startschalter `--allow-unsigned-worker-types` möglich, ausdrücklich nur für die Entwicklung. Signaturen werden noch nicht geprüft.
- Umgebung nur über eine Positivliste; Secrets nur über Vault-Verweise; nur Administratoren können installieren; kein Zugang über MCP.

Geplant:
- Bedrohungsmodell als Tabelle (bitte übernehmen):
  - Nur der Publisher-Schlüssel ist kompromittiert: Der Angreifer kann signieren, aber nicht in fremden Registries oder Namespaces listen.
  - Nur der Registry-Schlüssel ist kompromittiert: Er kann listen und widerrufen, aber keine gültige Publisher-Signatur erzeugen, also keinen neuen Code bringen.
  - Beide Schlüssel sind kompromittiert: Er kann eine bösartige Version listen, aber nicht installieren, weil Aktualisierungen immer eine ausdrückliche Entscheidung des Betreibers sind.
  - Der Registry-Host ist kompromittiert: Er kann alte oder veränderte Bytes ausliefern, die aber die Digest- und Signaturprüfung nicht bestehen.
- Restrisiko: Ein Betreiber bestätigt ein bösartiges Update, das korrekt mit beiden Schlüsseln signiert ist. Der Dialog zeigt deshalb Publisher, Signaturstatus, Digest und geänderte Anforderungen an.
- Widerruf: Signierte Widerrufseinträge im Index (ID, Versionsbereich oder Digest, Schweregrad, Grund).
  - Ein Widerruf kann nur verhindern und stoppen, nie installieren, starten oder umkonfigurieren. Der Hub wird so kein Fernsteuerungskanal; ein gestohlener Registry-Schlüssel bewirkt über den Widerruf höchstens einen sichtbaren, behebbaren Ausfall.
  - Gestoppte Worker verlieren keine Jobs; diese warten dauerhaft.
  - Ohne Verbindung zur Registry gilt die letzte geprüfte Liste mit Altersanzeige.
- Vertrauensrichtlinie pro Installation: erlaubte Registries, Publisher, Namespaces und Launcher je Vertrauensstufe.
- Abschottung auf Betriebssystemebene: Unter Linux Landlock/seccomp (wie bei der bestehenden Script-Sandbox), unter Windows nur ein Job-Objekt für Ressourcengrenzen. Die Abschottung ist unter Windows schwächer. Fremde Worker Types laufen dort als Prozess nur mit ausdrücklicher Freigabe. Diese Abschottung ist für installierte Artefakte noch nicht umgesetzt.

I. Aktualisierung und Versionierung
Umgesetzt:
- Releases sind unveränderlich; eine neue Version ist eine neue Installation, und mehrere Versionen können nebeneinander installiert sein.
- Protokollkompatibilität N/N-1 (siehe D).

Geplant:
- Ein Deployment legt die Worker-Type-ID und die Hauptversion fest; Installation oder Entfernen ändern nie bestehende Deployments.
- Das Entfernen einer Version wird verweigert, solange sie genutzt wird.
- Keine automatischen Updates, jede Aktualisierung ist eine Entscheidung des Betreibers.
- Widerruf wie in H.

J. Zielumgebungen
- Trennung zwischen Artefaktformat und Starter (Launcher).
- Launcher:
  - `process` für Windows, Linux und macOS: umgesetzt.
  - `container` (Docker/Podman): geplant.
  - `kubernetes`: geplant, nur nach Freischaltung, weil RBAC-Rechte nötig sind.
  - `external`: Betreiber betreibt die Instanz selbst. Das ist heute schon möglich mit `atlas worker` bzw. `atlas-workers` auf anderen Rechnern.
- Natives Binary als Basis, weil nur es überall läuft, auch unter Windows ohne Container. OCI ist optional für Isolation und für mitgelieferte Laufzeit-Abhängigkeiten (z. B. PowerShell für Exchange und Teams).

K. Lizenz (Vorschlag, juristische Prüfung ausstehend)
- Die Engine bleibt AGPL-3.0-only.
- Das SDK und die Protokollbeschreibung sollen Apache-2.0 werden, damit Hersteller Worker ohne Offenlegung ihres Quellcodes anbieten können.
- Voraussetzung, dass das SDK nichts AGPL-Lizenziertes aus atlas einbindet: erfüllt.
- Offen: die Zustimmung der Rechteinhaber und die juristische Bestätigung.

L. Migrationsplan und Status
1. SDK, Protokollbeschreibung, Handshake: umgesetzt. Das SDK als eigenes Go-Modul ist bis zur Lizenzentscheidung zurückgestellt.
2. Eigenes Binary `atlas-workers`: umgesetzt.
3. Geprüfte lokale Artefakte, Supervisor-Start, Pilot-Typ: umgesetzt.
4. Generische Serverhälfte: teilweise umgesetzt (Konfiguration über Schema, Secrets über Vault). Offen:
   - Worker-Abfragen `validate-config`, `check-connection` und `diagnostics`
   - Zuordnung eines BPMN-Tasks zu einem bestimmten Worker
   - Credential-Broker pro Job für installierte Typen
   - Umzug der eingebauten Typen
5. Signaturen, Release-Art im Hub, Widerruf: geplant. Das ist harte Voraussetzung vor dem ersten veröffentlichten Worker-Binary.
6. Launcher für Container und Kubernetes: geplant.
7. Migration der aufwendigen Typen (AD, Entra, Exchange, Teams Phone, SQL, Mail, Script): geplant.
8. Entfernen der eingebauten Typen aus dem Server-Binary: geplant.

M. Abwägung (für Kapitel 14 und 15)
- Stärkste Gegenposition «alles einkompiliert lassen»: ein Binary, eine Version, keine Versionsabweichungen, keine zusätzliche Lieferkette, einfacher Betrieb unter Windows.
- Warum das nicht genügt:
  - Release-Kopplung bei jeder Integrationsänderung und jeder Sicherheitslücke in deren Abhängigkeiten.
  - Die Angriffsfläche des Engine-Prozesses wächst mit jeder Integration.
  - Dritte können nicht beitragen.
- Nachteile des neuen Modells:
  - Kompatibilitätsmatrix aus atlas-, Worker- und Protokollversion.
  - Mehr Artefakte und Launcher, die getestet werden müssen.
  - Kuratierungsaufwand und Haftungsfragen.
  - Schwächere Isolation unter Windows.
  - Die Migration der aufwendigen Typen ist umfangreich.

Ausgabeformat
- Markdown mit Titel, Datum (Platzhalter), Versionsangabe «Entwurf 0.1» und Inhaltsverzeichnis.
- Mermaid-Diagramme wie verlangt.
- Am Schluss eine Statustabelle mit allen Funktionen, jeweils «umgesetzt», «geplant» oder «Vorschlag», und eine Liste der offenen Fragen.10. Berechtigungen und Credentials
11. Sicherheit (Bedrohungsmodell, Schutzmassnahmen, Restrisiken)
12. Qualität der Abarbeitung (Zustellgarantien, Wiederholungen, Fehlerbehandlung, Überwachung)
13. Aktualisierung, Versionierung, Widerruf
14. Zielumgebungen (Windows ohne Container, Linux, Kubernetes)
15. Lizenzmodell (als Vorschlag gekennzeichnet)
16. Vorteile gegenüber der bisherigen Lösung, mit fairer Darstellung der Gegenposition
17. Grenzen, offene Fragen, nächste Schritte
18. Statustabelle (umgesetzt / geplant)

N. Berechtigungen und Credentials

Rollen und Zugänge (umgesetzt)
- Die API kennt die Rollen admin, operator, modeler und user. Die Routen des Worker-Protokolls verlangen mindestens operator.
- Das Installieren, Konfigurieren und Entfernen von Worker Types ist nur Administratoren erlaubt und bewusst nicht über MCP (KI-Agenten) erreichbar.
- Maschinen authentisieren sich mit API-Tokens, die einen Geltungsbereich (Scope) haben (ADR-0194).
  - Der Scope `worker` ist eine abschliessende Positivliste: Er erlaubt nur die Aufrufe des Worker-Protokolls und lehnt alles andere ab, selbst wenn die Rolle es zuliesse.
  - Auch das interne Token, das der Supervisor seinen Workern gibt, ist auf diesen Scope beschränkt (ADR-0297).
  - Ein extern betriebener Worker braucht ein API-Token mit Scope `worker` (`ATLAS_TOKEN`). Ein ungültiges Token meldet der Server beim Start als Warnung.

Wo Zugangsdaten liegen (umgesetzt)
- Zugangsdaten zu Zielsystemen stehen nie im BPMN-Modell, nie in Ereignissen und nie im Write-Ahead-Log.
- Ein Job enthält die aufgelösten Fachwerte einer Aufgabe, aber keine Zugangsdaten (ADR-0168).
- Ablage im verschlüsselten Vault des Servers (ADR-0069/0070):
  - Der Vault ist standardmässig eingeschaltet. Sein Schlüssel wird erzeugt und ist nur für das Dienstkonto lesbar; alternativ setzt der Betreiber `ATLAS_VAULT_KEY`.
  - Jeder Eintrag ist einzeln versiegelt. Ein mit einem anderen Schlüssel versiegelter oder manipulierter Eintrag ist ein Fehler, kein stiller Fehlgriff.
- Drei Wege zur Worker Instance:
  1. Ein extern betriebener Worker hält seine Zugangsdaten selbst in seiner Umgebung; der Server kennt sie nicht.
  2. Ein überwachter eingebauter Worker erhält sie beim Start aus dem Vault.
  3. Ein installierter Worker Type erhält sie beim Start in `ATLAS_WORKER_CONFIG`. Das Schema im Manifest markiert Secrets mit `writeOnly`. Konfiguriert wird nur der Name des Vault-Eintrags, und die API zeigt den Wert nie.
- Eine Worker Instance meldet bei jedem Abruf nur die Namen der Worker, für die sie Zugangsdaten hält, nie die Zugangsdaten selbst.
- Jobs, die einen bestimmten Worker nennen, erhält nur eine Instanz, die diesen Worker hält.
- Antworten auf Formular-Abfragen enthalten im Fehlerfall nur einen Code, nie die Fehlermeldung des Anbieters, weil diese den Suchbegriff einer Person enthalten kann.

Identitätsbewusste Ausführung (umgesetzt, ADR-0475)
- Ein Worker Type, dessen Aktionen der Server steuert, fragt vor der Aktion am Zielsystem nach (`/authorize`).
- Der Server zeichnet die Entscheidung dauerhaft auf, bevor er antwortet. Die Antwort enthält die Parameter, die die Entscheidung abdeckt; nur auf diese darf der Worker handeln.
- Modi: `off` (keine Richtlinie), `shadow` (entscheiden und protokollieren, aber nicht durchsetzen) und `enforce`.
- Für eine delegierte Aktion erhält der Worker ein kurzlebiges Zugangs-Token im Namen der Person. Das gilt nur für Instanzen, die der Server selbst überwacht und die einen Broker-Schlüssel vorweisen.
- Bei einer Ablehnung hat der Server den Job bereits abgeschlossen, und der Worker meldet nichts mehr.

Offen (bitte ausdrücklich als offene Lücke darstellen)
- Alle installierten Worker Types erhalten heute dasselbe Token mit Scope `worker`. Dieser Scope beschränkt auf das Worker-Protokoll, nicht auf die Job-Typen eines bestimmten Worker Types.
- Ein installierter Worker Type könnte damit technisch Jobs anderer Typen abrufen.
- Ein Token pro Instanz, beschränkt auf die im Manifest deklarierten Job-Typen, ist noch nicht entschieden. Es ist eine Voraussetzung, bevor Drittanbieter-Worker produktiv betrieben werden.
- Der Credential-Broker pro Job ist für installierte Worker Types noch nicht umgesetzt.

O. Qualität der Abarbeitung (umgesetzt, wenn nicht anders vermerkt)

Dauerhaftigkeit und Zustellgarantie
- Ein Job wird erst sichtbar, wenn er dauerhaft gespeichert ist, und übersteht Neustarts von Server und Worker.
- Zustellung mindestens einmal (at least once). Ein Lease hält einen Job für eine Instanz, standardmässig 5 Minuten.
- Ein Lease-Token schützt vor späten Meldungen: Meldet sich eine Instanz nach Ablauf ihres Leases, wird die Meldung abgewiesen, und der Job wurde bereits neu angeboten.
- Handler mit Seiteneffekten müssen deshalb idempotent sein; der Job-Key eignet sich als Schlüssel dafür.

Wiederholungen und Fehler
- Ein Fehler verbraucht einen Versuch. Danach wird der Job erst nach einer Wartezeit wieder angeboten: 60 Sekunden bei einem verbleibenden Versuch, 15 Sekunden bei mehreren (ADR-0111).
- Bei den standardmässigen 3 Versuchen bleiben einer Störung so 75 Sekunden, bevor ein Incident entsteht. Das reicht etwa für die Replikationsverzögerungen von Microsoft Graph und ist kurz genug, damit eine echte Störung innert anderthalb Minuten beim Betrieb ankommt.
- Extern laufende Worker warten genau so lange wie die Engine selbst; ein Test stellt das sicher.
- Sind die Versuche aufgebraucht, entsteht ein Incident (ADR-0061). Der Prozess wartet, bis jemand ihn behebt, und das Beheben startet den Job erneut.
- Ein fachlicher Fehler wird als BPMN-Fehler geworfen, nicht als Störung: Er verbraucht keinen Versuch, und ein passendes Fehlerereignis im Modell fängt ihn ab. Fängt keines ihn ab, wartet der Job auf einem Incident.

Schutz vor Überlast und Folgefehlern
- Eine Instanz ruft nur so viele Jobs ab, wie sie freie Plätze hat (ADR-0440). Ein abgerufener Job startet sofort, statt mit laufendem Lease zu warten.
- Ein Circuit Breaker pro Paar aus Job-Typ und Worker hält Jobs zurück, deren Zielsystem wiederholt ausfällt (ADR-0340). Er lässt einzelne Probe-Jobs durch, um die Erholung zu erkennen.
  - Er öffnet nur bei Fehlern über verschiedene Prozessinstanzen hinweg, damit ein Datenfehler in einer einzelnen Instanz nicht die ganze Integration stoppt.
  - Der Ausfall endet beim Worker, statt jeden Job zu verbrauchen.
- Ein fehlgeschlagener Abruf beendet eine Worker Instance nie, etwa wenn der Server neu startet oder ein Job-Typ noch nicht deployt ist. Die Instanz protokolliert den Fehler und versucht es erneut.

Betrieb durch den Supervisor
- Abgestürzte Worker startet der Supervisor mit wachsender Wartezeit neu, von 0,5 bis höchstens 30 Sekunden.
- Ein Worker ohne Konfiguration endet mit einem eigenen Exit-Code (78) und wird geparkt statt endlos neu gestartet. Sobald die Konfiguration vorhanden ist, startet er wieder.
- Überwachte Worker enden mit dem Server, unter Windows auch bei einem Absturz des Servers (ADR-0439).
- Installierte Artefakte werden vor jedem Start erneut gegen ihren Digest geprüft.

Beobachtbarkeit
- Die Workers-Ansicht zeigt pro Instanz:
  - abonnierte Job-Typen und gehaltene Worker
  - Zähler für abgerufene, erledigte und fehlgeschlagene Jobs
  - laufende Jobs pro Typ
  - Protokollversion, Worker-Type-Identität und Digest
- Sie zeigt auch die letzten Jobs einer Instanz mit Ein- und Ausgabe. Dieser Verlauf ist begrenzt und wird bei einem Neustart geleert.
- Ein dauerhafter Job-Verlauf ist über einen konfigurierten clio-Event-Store möglich (ADR-0179). Dazu kommen Prometheus-Metriken und die Anzeige von Worker-Namen, die Modelle referenzieren, die aber keine Instanz bedient.
- Für eingebaute Worker-Type-Operationen gibt es Konformitätsnachweise gegen technische Fähigkeitsverträge (ADR-0483).

Geplant
- Konformitätsnachweise für Worker Types, die ausserhalb des Repositorys gebaut werden.
- Die Abfragen `check-connection` und `diagnostics`, damit der Betrieb eine Konfiguration prüfen kann, bevor der erste Job scheitert.

