# Umsetzungsplan zur Architektur- und Codeanalyse vom 7. September 2026

**Grundlage:** externer Analysebericht «Atlas — Gründliche Architektur- und
Codeanalyse», Prüfdatum 2026-09-07, geprüfter Commit
`cd165c67a800cf7ce26adec3453e5f699db86070`, 17 Befunde (2 P0 · 10 P1 · 5 P2),
14 mitgelieferte Reproduktionen.

**Nachprüfung für diesen Plan:** die 14 Reproduktionen wurden unverändert gegen
`main` (75 Commits nach dem geprüften Stand) übersetzt und ausgeführt.

| | |
|---|---|
| Kompiliert gegen heutiges `main` | ja, ohne Anpassung (`go vet` sauber) |
| Davon fehlgeschlagen | **14 von 14** |
| Statisch nachgeprüfte Befunde (F15–F17) | **3 von 3** unverändert vorhanden |

Der Bericht ist damit nicht nur für den geprüften Commit gültig, sondern
beschreibt den heutigen Stand. Keiner der Befunde ist zwischenzeitlich
weggefallen; die zwölf in F05 genannten Store-Verzeichnisse existieren
sämtlich im Server und fehlen sämtlich in `fullBackupDirs`.

---

## 1. Wie ich den Bericht einordne

Der Bericht ist belastbar. Er trennt sauber zwischen reproduziertem
Implementierungsfehler, statisch belegtem Risiko und bereits dokumentierter
Einschränkung, und er behauptet an keiner Stelle mehr, als seine Nachweise
tragen. Die Reproduktionen zielen genau auf die Stellen, an denen die eigene
Testsuite systematisch blind ist — Unterbrechungen zwischen stabilen
Wartezuständen, Scope-Kombinationen, negative Zugriffsmatrizen. Dass 95 %
Statement-Coverage und 5 286 Testfunktionen diese Klasse nicht erwischen, ist
die eigentliche Nachricht des Berichts.

Drei Stellen gewichte ich anders als der Bericht:

**a) Die Reihenfolge.** Der Korrekturplan des Berichts schiebt die
Autorisierungsfehler (F09–F11) hinter die WAL-Arbeit auf Schritt 4. F09 (ein
Modellierer schreibt in ein fremdes privates Projekt) und F10 (ein entzogenes
Admin-Recht wirkt bis zu zwölf Stunden weiter) sind kleine, isolierte
Änderungen ohne Abhängigkeit zum Persistenzumbau. Sie hinter einem Umbau
anzustellen, der Wochen dauert, hält eine bekannte Rechtelücke ohne
technischen Grund offen. Sie gehören in die erste Welle.

**b) F01 und F02 sind eine Entscheidung, nicht zwei.** Beide verlangen eine
Änderung am WAL-Format. Getrennt umgesetzt heisst das zwei Formatwechsel und
zwei Migrationen. Sie gehören in ein Arbeitspaket mit einem Format und einem
Migrationspfad (siehe AP2).

**c) F10 ist kleiner, als der Bericht vermuten lässt.** Der Mechanismus, der
Gruppenänderungen live in bestehende Sessions schiebt, existiert bereits
(`sessionStore.setUserGroupMembership`, `dropGroupFromSessions`,
`destroyUser` — ADR-0185). Für Rollen fehlt genau das Analogon dazu. Das ist
kein Architekturthema, sondern eine vergessene Zeile im selben Muster.

Nicht Teil dieses Plans: horizontale Skalierung. Der Bericht sagt zu Recht,
dass ein fair begrenzter, korrekt wiederanlaufender Einzelpartitionskern die
Voraussetzung dafür ist und nicht umgekehrt.

---

## 2. Die drei Verträge, um die es eigentlich geht

Die 17 Befunde sind Symptome von drei unvollständigen Verträgen. Wer die
Befunde einzeln repariert, ohne die Verträge zu schliessen, bekommt dieselbe
Fehlerklasse beim nächsten Feature zurück. Jedes Arbeitspaket unten schliesst
deshalb einen Vertrag und nicht nur einen Befund.

### V1 — Der Ausführungsvertrag umfasst Zustand *und* offene Arbeit

Heute gilt `state == replay(WAL)`. Das ist eine Sicherheits-, keine
Lebendigkeitseigenschaft: F01 zeigt eine Instanz, die korrekt materialisiert
ist und trotzdem nie weiterläuft, weil die Fortsetzungsverpflichtung nur in
`p.queue` im Arbeitsspeicher stand. I4 ist erfüllt und der Prozess steht
still. Der Vertrag muss lauten: *nach Recovery ist der Zustand identisch und
jede angefangene Übergangsfolge ist fortsetzbar.*

### V2 — Wiederherstellbarkeit ist eine Eigenschaft der ganzen Installation

WAL, State, Checkpoint, Deploymentdefinitionen, Jobtyp-Indizes, Vault und die
Sidecar-Stores sind heute getrennte Halbverträge (F03, F04, F05). Ein
Archiv, das sich «Vollsicherung» nennt und den `vault`-Ordner auslässt,
während es `vault.key` mitnimmt, ist gefährlicher als gar keins: es sieht
vollständig aus. Der Vertrag braucht eine gemeinsame Inventar- und
Konsistenzgrenze, aus der Backup, Restore und Recovery *abgeleitet* werden.

### V3 — Autorisierung hat zwei Achsen

Die Rolle-pro-Route-Tabelle (ADR-0209) ist die erste Achse und funktioniert:
jede Route nennt die Rolle, die sie verlangt, und die Schranke setzt sie an
genau einer Stelle durch. Die zweite — *darf dieser Principal auf dieses Objekt* — fehlt
in der Fläche (F09, F11) und ihr Widerruf ist nicht durchgesetzt (F10). Eine
Route mit Rollenannotation ist keine Sicherheitsgrenze, solange die
Objektbeziehung ungeprüft bleibt. O-02 nennt genau diesen Rest schon heute
als offen; der Bericht zeigt, was er praktisch kostet.

---

## 3. Arbeitspakete

Sizing ist relativ (S ≈ Tage, M ≈ 1–2 Wochen, L ≈ mehrere Wochen, jeweils für
eine Person inkl. Tests und ADR), nicht kalendarisch. Jedes Paket endet an
einem prüfbaren Kriterium, nicht an einem Datum.

### AP0 — Regressionsharness (S) · Voraussetzung für alles Weitere

Die 14 Reproduktionen sind der Abnahmemassstab des ganzen Plans. Sie können
aber nicht einfach in die Suite: sie sind heute rot und würden
`go test ./...` und damit die Definition of Done dauerhaft brechen.

- Die fünf Dateien unter Build-Tag `audit` aufnehmen (`//go:build audit`), in
  den jeweiligen Paketen `engine`, `wal`, `compiler`, `api`.
- Eigenes CI-Job `make audit-regressions`, der **erwartet rot** ist und die
  Zahl offener Befunde als Kennzahl ausgibt — kein blockierendes Gate.
- Pro behobenem Befund wandert genau sein Test aus dem Tag in die normale
  Suite. Der Tag verschwindet, wenn F14 als letzter fällt.
- Der Coverage-Floor (95 %, repositoryweit) bleibt unberührt, weil
  getaggte Dateien nicht im Standardlauf sind.

**Fertig, wenn:** `go test -tags audit ./engine ./wal ./compiler ./api`
14 Fehlschläge zeigt, `go test -race -timeout=25m ./...` unverändert grün ist
und der Zähler offener Befunde in CI sichtbar ist.

**Warum zuerst:** ohne diesen Schritt ist jede spätere Aussage «F0x ist
behoben» eine Behauptung statt einer Messung.

---

### AP1 — Sofortmassnahmen (S) · F09, F10, F17, F07

> **Stand: umgesetzt.** Alle vier Befunde sind behoben, mit Regressionstests in
> der Standardsuite und drei ADR-Entwürfen. Zwei Punkte weichen von der Planung
> unten ab und stehen dort, wo sie auffielen: F07 brauchte einen zweiten Eingriff
> (die Queue), und die Durchsicht der Commit-Lesezugriffe fiel kleiner aus als
> erwartet.

Vier Befunde, die klein, isoliert und ohne Abhängigkeit zum Persistenzumbau
sind. Sie zuerst zu machen, kostet den grossen Umbau nichts und schliesst zwei
Rechtelücken sofort.

| Befund | Eingriff | Aufwand |
|---|---|---|
| **F09** Deployment umgeht Projektmitgliedschaft | Nach Auflösen des effektiven Projekts (`?projectId=` *und* implizite Draftzuordnung) dieselbe objektbezogene Schreibprüfung wie bei den übrigen Projektoperationen, **vor** `claimBlockingModel` und `deployModel` — bei Verweigerung darf weder Sidecar-Datei noch Registry-Eintrag entstehen. Gleicher Pfad für MCP. | S |
| **F10** Rollenentzug wirkt nicht in laufenden Sessions | `sessionStore.setUserRoles(userID, roles)` analog zu `setUserGroupMembership`, aufgerufen aus `handlePatchUser`. Das Muster existiert; es fehlt nur für Rollen. Für API-Tokens mit bewusst eigenständigen Rechten den Unterschied in der Oberfläche ausweisen statt ihn anzugleichen. | S |
| **F17** Keine Lese-/Idle-Timeouts | `ReadHeaderTimeout` und `IdleTimeout` auf beiden `http.Server` explizit setzen, `ReadTimeout` bewusst wählen. **Kein** globales `WriteTimeout` — Long-Polling und gestreamte Backups brauchen abgestimmte Fristen; die betroffenen Endpunkte erhalten sie pro Handler. | S |
| **F07** Abbruch übersieht Kind aus demselben Batch | `ChildInstancesOf` über `c.tx` statt `c.p.store` lesen. Danach **jeden** verbleibenden `c.p.store`-Lesezugriff in `engine/behavior.go` einzeln daraufhin prüfen, ob er absichtlich nur über Commitgrenzen arbeitet; das Ergebnis dieser Durchsicht gehört in den ADR. | S–M |

**Was AP1 gegenüber dieser Planung gelernt hat.**

*Die Durchsicht war kleiner als gedacht.* Im ganzen `engine`-Paket gehen genau
**zwei** Lesezugriffe am `c.tx` vorbei — `ChildInstancesOf` (der Fehler) und
`ForEachStartTimer`, das beim Deployment läuft und nicht im Batchkontext eines
Prozesses. `engine/behavior.go` selbst hat keinen einzigen. Die befürchtete
Fläche existiert nicht.

*F07 brauchte einen zweiten Eingriff.* Der transaktionale Lesezugriff findet das
Kind, reicht aber nicht: die Abbruchkaskade stellt für das Kind ein
Terminating-Command ein, während dessen eigenes Start-Event schon in der Queue
liegt. Das läuft zuerst und baut genau die Ausführung wieder auf, die der Abbruch
entfernt — übrig blieben eine Elementinstanz und ein aktivierbarer Job. Eine
terminierte Instanz verliert deshalb jetzt ihre noch nicht verarbeitete Arbeit
(`advanceQueue`). Commands sind nie persistiert und werden nie repliziert (I6),
also ändert das, was als Nächstes läuft, und nichts an dem, was die Recovery
rekonstruiert.

*Dabei ist ein eigener Defekt aufgefallen, der nicht zu F07 gehört.* Der erste
Versuch war ein breiter Wächter: keine Elementaktivierung für eine Instanz, die
nicht mehr lebt. Er brach die Kompensation — bei einem Compensation-Throw wird
die Instanz **abgeschlossen**, bevor ihr Token das Throw-Event verlassen hat, und
`park` entsteht heute auf einer bereits abgeschlossenen Instanz. Das ist ein
echter Fehler in der Scope-Zählung, der bisher nur deshalb nicht auffällt, weil
niemand hinsieht. Er ist hier bewusst **nicht** behoben — ein Fix für den Abbruch
darf nicht still das Verhalten beim Abschluss ändern — und im ADR beschrieben.
Kandidat für ein eigenes Befund-Register.

**ADR-Bedarf:** `draft-object-authorization` (beginnt hier mit F09, wird in
AP5 vollendet); `draft-session-role-revocation` (verhält sich zu ADR-0185/0044);
`draft-transactional-child-view` — ADR-0238 begründet die reine Commit-Sicht
ausdrücklich mit Determinismus. Diese Ableitung ist falsch: die bereits
angewendeten Ereignisse der laufenden Transaktion sind ein deterministischer
Teil der Commandverarbeitung. Eine dokumentierte Entscheidung umzukehren
verlangt einen Record, keinen stillen Fix.

**Fertig, wenn:** `TestAuditRawDeployRequiresProjectMembership`,
`TestAuditRoleRevocationInvalidatesExistingSession` und
`TestAuditCancelSeesChildCreatedInSameBatch` grün in der Standardsuite sind,
plus eine Zugriffsmatrix Eigentümer/Editor/Viewer/fremder Modeler/Admin gegen
direkte, projektgebundene und implizit zugeordnete Deployments.

---

### AP2 — Persistenzvertrag: F02 + F01 (L) · der eigentliche P0-Block

> **Stand: umgesetzt.** Beide P0-Befunde sind behoben. Das Batchformat trägt
> Versionskopf, Abwärtskompatibilität für vorhandene Logs und ein Kind-Byte je
> Eintrag; die Continuation nutzt dieses Byte. Der Reflection-Wächter über die
> Felder von `Command` steht. Die Aufwandsschätzung für F01 unten war
> **falsch** — siehe den Kasten dort.
>
> **Was erst die Abnahmematrix gefunden hat.** Das Kriterium «an jeder
> Batchgrenze unterbrechen» ist nicht Zierde, sondern hat zwei Fehler
> aufgedeckt, die durch Nachdenken nicht auffielen und durch die
> Einzelreproduktion des Auditors auch nicht:
>
> 1. *Duplikate am Ende.* Wird die Queue leer, wurde ursprünglich **keine**
>    Continuation geschrieben — also blieb die vorherige die neueste und wurde
>    beim Neustart erneut ausgeführt. Ergebnis: doppelte Jobs. Ein Batch, der
>    nichts mehr schuldet, schreibt jetzt eine Continuation der Länge null.
> 2. *Stiller Verlust in der Mitte.* Ein Command in der Queue trägt bereits
>    einen Schlüssel, den noch kein Ereignis nennt — das Ereignis dazu ist ja
>    genau das, was der Absturz verhindert hat. Der Wiederanlauf leitet den
>    Zähler aus den Ereignissen ab und hätte dieselbe Nummer erneut vergeben;
>    die wiederhergestellte Elementinstanz kollidierte mit einer frisch
>    geprägten. Das Symptom war eine fehlende Elementinstanz, keine
>    Fehlermeldung. Die Continuation hebt den Zähler jetzt über jeden Schlüssel,
>    den sie trägt.
>
> Beides wäre mit einem einzelnen Reproduktionstest durchgerutscht. Die Matrix
> läuft über alle Batchgrenzen, jeweils mit erhaltenem *und* aus dem Log neu
> aufgebautem State.

Beide Befunde verlangen dieselbe Formatänderung. Ein Paket, ein Format, eine
Migration.

**F02 — atomare Batchgrenzen.** Empfehlung: **ein Batch ist ein Frame.**
Heute schreibt `Sync` mehrere Frames mit je eigener Länge und CRC in einem
`Write`; der Reader akzeptiert jeden vollständigen Frame vor einem abgerissenen.
Wird stattdessen der ganze Batch als *ein* Frame mit einer Länge und einer
Prüfsumme geschrieben, fällt Batchatomarität mit der bereits vorhandenen
Frame-Integrität zusammen — ein zerrissener Batch ist dann nicht von einem
zerrissenen Frame unterscheidbar, und den verwirft der Reader schon heute.
Kein Commit-Marker, keine zweite Zustandsmaschine im Reader.

Der Preis, der explizit zu tragen ist: `maxRecordSize` (heute 64 MiB) wird zu
einer Batchgrösse und braucht eine eigene, begrenzte Obergrenze; die
Reader-API wechselt von «ein Payload = ein Record» zu «ein Payload = n
Records»; und das Format bekommt eine Version. Die Alternative — Batchhülle
mit `BatchBegin`/`BatchCommit` — ist mächtiger (erlaubt Batches über
Segmentgrenzen), aber sie führt Zustand in den Reader ein, den F03 gerade erst
loswerden will. Die Entscheidung gehört in den ADR, mit dem Umgang mit
bestehenden WAL-Versionen und mit I/O-Fehlern während Append/Sync
ausdrücklich darin.

> **Bei der Durchsicht aufgefallen, im Bericht nicht genannt:** die
> Frame-Dekodierung existiert **zweimal** — `wal/reader.go:readFrames` *und*
> `wal/tailer.go`, letzteres mit identischer «corrupt frame → behandle als
> Tail»-Logik. Der Tailer bedient den OpenSearch-Exporter (ADR-0114). Beide
> Änderungen dieses Plans treffen ihn mit: «Batch = ein Frame» bricht seine
> Annahme «ein Payload = ein Record», und F03 ist ohne ihn nur halb behoben —
> ein beschädigter Frame schneidet den Exportstrom heute genauso still ab.
> Die Frame-Dekodierung gehört im selben Zug an **eine** Stelle.

**F01 — dauerhafte Fortsetzung.** Zwei gangbare Wege:

1. *Continuation-Sektion im Batch.* Der Batchframe trägt neben den Events die
   am Batchende noch offenen internen Commands. Recovery seedet daraus
   `p.queue`. Vollständig und billig, sobald die Hülle sowieso existiert.
   Berührt I6 («nur Events werden persistiert») — aber im Buchstaben, nicht
   im Sinn: die Sektion wird **nicht** von `applyToState` gefaltet und ist
   keine Tatsache über den Prozess, sondern die Buchführung des Logs darüber,
   dass dieser Batch seine Arbeit noch nicht abgeschlossen hat. I4 bleibt
   unangetastet.
2. *Resume-Scan aus dem Zustand.* Nach Recovery über alle Element- und
   Prozessinstanzen laufen, deren letzter Intent ein Übergangszustand ist
   (nicht terminal, nicht wartend), und ihre Fortsetzung idempotent neu
   ausstellen. Verletzt I6 gar nicht, verlangt aber eine vollständige und
   dauerhaft gepflegte Klassifikation *jedes* Lifecycle-Zustands als
   stabil/instabil — jeder neue Zustand, den jemand vergisst einzuordnen, ist
   ein neuer F01.

**Empfehlung: Weg 1**, mit Weg 2 als Prüfung gegen Weg 1 in den Tests. Weg 1
ist total und lokal; Weg 2 ist eine Vollständigkeitsannahme, die mit jedem
Feature neu gebrochen werden kann. Der Resume-Lauf muss in beiden Fällen
**nach** dem Laden der Definitionen laufen, und normale Business-Behaviours
dürfen nicht blind wiederholt werden (Doppeleffekte).

> **Korrektur nach der Umsetzung von F02.** Oben steht, Weg 1 sei «vollständig
> und billig, sobald die Hülle sowieso existiert». Die Hülle ist billig; Weg 1
> ist es nicht. `engine.Command` trägt dreizehn Felder, nicht vier: neben
> `Key`, `ValueType`, `Intent`, `Value` und `SourcePos` auch `StartVars`,
> `StartElements`, `Decision`, `ToolCalls`, `Actor`, `Reason`, `Manual` und
> `RetryBackoff`. Eine Continuation zu persistieren heisst also, einen zweiten
> Codec neben dem Event-Codec zu bauen und zu pflegen.
>
> Das relativiert die Begründung, nicht die Empfehlung. Entlastend ist, dass
> die **internen Followups** — die einzigen, die überleben müssen — eine
> geschlossene, kleine Teilmenge benutzen: `Key`, `ValueType`, `Intent`,
> `Value`, `SourcePos` und, nur an den beiden Anlage-Intents, `StartVars` und
> `StartElements`. Die übrigen Felder reiten ausschliesslich auf extern
> eingereichten Commands (Job-Completion, Variablenänderung,
> Operator-Eingriff), und ein extern eingereichter, noch unbestätigter Command
> darf nach einem Absturz verloren gehen — dieselbe Begründung, die F02 für
> unbestätigte Commands gibt.
>
> Der Codec muss diese Teilmenge deshalb **erzwingen**, nicht annehmen: ein
> Test, der die Felder von `Command` per Reflection aufzählt und fehlschlägt,
> sobald ein neues auftaucht, das er nicht einordnet. Das ist dasselbe Muster
> wie der Store-Registry-Test in AP3 — Vollständigkeit prüfen statt eine Liste
> pflegen. Ohne ihn ist jedes künftige Feld ein stiller Datenverlust beim
> Wiederanlauf.

**ADR-Bedarf:** `draft-wal-batch-envelope`, `draft-durable-continuation`.
Beide berühren I2 und I6 direkt und brauchen je einen Record.

**Fertig, wenn:** eine Crash-Matrix über *jeden* Byte-Abbruchpunkt eines
Batches mit mehreren Commands und mehreren Events pro Command besteht; nur
vollständige Batches sichtbar werden; Short Writes, `fsync`-Fehler und ein
Neustart zwischen WAL-Sync und State-Commit abgedeckt sind; und ein
Unterbruch an jeder Batchgrenze — mit erhaltenem *und* neu aufgebautem State —
in Endzustand, Jobs, Timern, Subscriptions und Anzahl externer Effekte dem
unterbrechungsfreien Lauf entspricht, inklusive verschachtelter Calls,
Conditional Events und Job-Completion.

---

### AP3 — Wiederherstellbarkeit: F03, F04, F05 (M–L)

> **Stand: umgesetzt.** Alle drei Befunde sind behoben. Der Reader kennt jetzt
> den Unterschied zwischen «abgeschlossenes Segment» und «aktives Ende» und
> meldet Korruption im ersten Fall hart, mit Datei und Offset; der Start
> beweist seinen Präfix und verweigert sonst den Dienst; und die Ablage auf
> der Platte hat eine einzige Liste, aus der Backup und Restore *abgeleitet*
> sind statt danebengelegt.
>
> **Was erst die Abnahmekriterien gefunden haben.** Der Bericht nannte für F05
> zwölf fehlende Verzeichnisse. Der Vollständigkeitstest, der einen Server
> hochfährt und jedes nicht klassifizierte Verzeichnis ablehnt, hat zwei
> weitere Sorten gefunden, die keine Liste je erwischt hätte: Stores, die
> **erst bei Benutzung** entstehen (ihre Abwesenheit ist kein Fehler, ihre
> Auslassung im Archiv schon), und einen, den das Archiv über einen **eigenen
> Weg** trägt statt durch Kopieren des Verzeichnisses. Beide sind jetzt
> Eigenschaften des Registers, nicht Fussnoten.
>
> Der Test hat seither schon einmal geliefert, wofür er da ist: `main` hat den
> Store `task-folders` ergänzt, und der Merge in diesen Zweig ist daran
> aufgelaufen, bevor irgendein Backup ihn stillschweigend ausgelassen hätte.


**F03 — Korruption vs. erlaubtes Ende.** `readFrames` erfährt heute nicht, ob
es das aktive letzte Segment liest, und behandelt ungültige Länge, CRC-Fehler
und abgeschnittene Daten gleich als Dateiende. Der Reader bekommt die
Information (`sealed bool`); in einem abgeschlossenen Segment ist jeder dieser
Fälle ein harter Fehler mit Dateiname und Offset. Auch im letzten Segment gilt
ein CRC-Fehler *mit nachfolgenden Daten* nicht als harmloser Tail. Zusätzlich
Segmentfolge und erwartete Eventpositionen validieren, damit eine Lücke
zwischen zwei Segmenten nicht unbemerkt bleibt. Dasselbe gilt für
`wal/tailer.go` (siehe Kasten in AP2) — sonst bleibt der Exportpfad blind.

**F04 — Startup muss den Präfix beweisen.** `checkpointSeed` überspringt heute
nur alte Logdateien und installiert nie Checkpoint-Dateien; fehlt der Präfix,
fällt `RecoverFrom` auf einen vermeintlichen Genesis-Replay zurück, den es
nach Kompaktierung nicht mehr gibt. Der Start stellt fest, welchen WAL-Präfix
der vorhandene State tatsächlich ersetzt. Fehlt er: verifizierten passenden
Checkpoint installieren und den lückenlosen Suffix abspielen — der
Snapshot-Restore-Pfad kann das bereits, diese Logik wird geteilt, nicht
kopiert — sonst den Start mit einer klaren Wiederherstellungsanweisung
verweigern. Aus einem fehlenden Präfix nie einen erfolgreichen Genesis-Replay
ableiten. ADR-0131 verlangt das bereits; hier wird der Vertrag eingelöst.

> **Zu F05, nach der Umsetzung.** Zwei Dinge fand nicht der Entwurf, sondern
> der Vollständigkeitstest — in beide Richtungen:
>
> *Nicht alles entsteht beim Boot.* `checkpoints`, `dmn-models` und `exporter`
> gibt es erst, wenn das Feature dahinter benutzt wird. Das ist jetzt als
> `onDemand` ausgewiesen, statt den Test aufzuweichen.
>
> *Nicht alles wird gleich gesichert.* Der Checkpoint-Ordner wandert
> **absichtlich** nicht als ganzer Baum ins Archiv, sondern nur der neueste
> verifizierte. Ihn in den generischen Walk zu nehmen brach zwei bestehende
> Tests — zu Recht. Das ist jetzt als `ownMechanism` modelliert: klassifiziert,
> gesichert, nicht durchlaufen.
>
> *Bewusst nicht mitgemacht:* der **portable** Design-Time-Export bleibt
> unverändert bei denselben dreizehn Verzeichnissen. `process-docs`,
> `information-models` und `playground-scenarios` gehören dort vermutlich hin,
> aber den Export als Nebenwirkung einer Snapshot-Korrektur zu verbreitern wäre
> eine Änderung, die niemand verlangt hat. Die Registry macht daraus eine
> sichtbare Frage statt einer unsichtbaren Auslassung.

**F05 — Store-Registry.** Der systemische Teil und das wertvollste Stück des
ganzen Plans. Eine zentrale Registry, in der sich jeder persistente Store mit
Verzeichnis, **Backupklasse** (Design-Time / Credential / Secret / Runtime /
flüchtig) und Restore-Abhängigkeit registriert. `backupDirs`, `fullBackupDirs`
und die Restore-Allowlist werden daraus *abgeleitet* statt gepflegt. Vault-Daten
und Jobtyp-Tabelle kommen vollständig mit; Secrets nur in die dafür
vorgesehene geschützte Vollsicherung. Archiv bekommt ein Versions- und
Inhaltsmanifest, der Import weist fehlende Pflichtbestandteile zurück.

Entscheidend ist der begleitende Test: er startet einen Server, liest das
tatsächliche Datenverzeichnis und **schlägt fehl, sobald ein Verzeichnis
existiert, das keine Backupklasse hat**. Das ist der Mechanismus, der
verhindert, dass der dreizehnte Store dieselbe Lücke erneut aufreisst — die
zwölf heutigen sind ja nicht aus Nachlässigkeit entstanden, sondern weil eine
handgepflegte Liste an einer anderen Stelle im Code steht als die
Store-Erzeugung.

**ADR-Bedarf:** `draft-store-registry`; `draft-recovery-completeness`
(verhält sich zu ADR-0131, löst dessen offenen Vertrag ein).

**Fertig, wenn:** End-to-End-Restore in ein leeres Datenverzeichnis: Geheimnis
entschlüsseln, Gruppenberechtigungen vergleichen, alte Jobs demselben
Worker-Typ zuordnen, Informationsmodelle und Prozessdokumentation laden, einen
laufenden Prozess fortsetzen. Und: leerer / veralteter / gültiger State ×
gültiger / beschädigter / fehlender Checkpoint × vollständiges / kompaktiertes
WAL — jede Kombination stellt entweder vollständig korrekt wieder her oder
schlägt eindeutig fehl.

---

### AP4 — BPMN-Semantik und Fairness: F06, F08, F12 (M)

> **Stand: umgesetzt.** F06, F08 und F12 sind behoben; F06 inzwischen in beiden
> Schritten.
>
> **F06, Schritt 1.** Der Schlüssel des Joins ist jetzt (Prozessinstanz,
> Ausführungsscope, Knoten). Der Inclusive-Join hatte dieselbe Verwechslung in
> *beiden* Hälften: er wartete auf Geschwister, die nie ankommen können, und
> verbrauchte beim Feuern jedes auf dem Knoten parkierende Token — auch die der
> anderen Iteration. Die Gefahr lag beim Übercorrigieren, nicht beim
> Untercorrigieren; dafür gibt es einen eigenen Test mit einem Subprozess auf
> einem Zweig.
>
> **F06, Schritt 2.** Gezählt wird jetzt je eingehendem Sequence Flow. Der
> Schlüssel dafür lag schon in jedem Datensatz: `ElementInstanceValue.SourceFlowId`
> hält fest, über welchen Flow das Token angekommen ist. Ein Join feuert, wenn
> `IncomingCount` *verschiedene* Flows wartende Tokens haben, und verbraucht
> genau eines je Flow — das älteste, weil der Index nach Schlüssel aufsteigend
> liest. Zwei Tokens auf einem Zweig ersetzen damit den fehlenden Zweig nicht
> mehr, und das überschüssige Token bleibt liegen, statt beim Feuern zu
> verschwinden. Kein neuer Zustand, kein neues Ereignis, kein zusätzlicher Scan;
> die Herleitung von ADR-0024 bleibt, nur ihre vereinfachte Zählregel wird
> ersetzt (ADR-0290). Der Inclusive-Join hat denselben
> Defekt in seiner eigenen Sprechweise: er löst seinen Überschuss jetzt sofort
> auf, weil bei ihm nichts mehr nachkommen kann.
>
> **F08.** Die Reihenfolge war der ganze Fehler: das Gateway schrieb
> `Completed`, *bevor* die Route feststand, konnte also gar nicht mehr
> parkieren. Jetzt entscheidet es zuerst. Der zweite, gefährlichere Defekt lag
> drei Zeilen daneben — ein Auswertungsfehler wurde als `false` gelesen und
> nahm mit Default-Flow einen Zweig, den niemand gewählt hat. Beides ist
> getrennt behandelt und getrennt getestet.
>
> **F12.** Das Budget zählt **pro Token**, nicht pro Instanz. Das ist der
> Unterschied zwischen einem Zyklus (ein Token dreht sich) und schwerer,
> legitimer Arbeit (fünfzigtausend Multi-Instance-Iterationen sind
> fünfzigtausend Token mit je einem Schritt). Jede Obergrenze pro Instanz, die
> das erste stoppt, stoppt auch das zweite. Ein neu geprägtes Token *erbt* den
> Zähler seines Elters, sonst setzt ein Zyklus durch jede Fork, jeden Join und
> jeden Subprozessausgang sein eigenes Budget zurück — der Fork-Test hat genau
> das gefunden, nachdem die erste Fassung ihn nicht bestand.
>
> **Was der Fixpfad zusätzlich brauchte.** Ein jobloser Incident wurde bisher
> über den *Knotentyp* aufgelöst. Das trägt nur, solange ein Knotentyp genau
> eine Art hat, steckenzubleiben — und das Budget kann jedes Element jeden
> Typs anhalten. `model.IncidentValue` trägt darum jetzt einen `Reason`; er
> hängt hinter der Nachricht, ältere Datensätze sind ein Byte kürzer und lesen
> sich als «nicht klassifiziert». Die vier übrigen joblosen Quellen tragen
> weiterhin genau das und werden weiterhin über den Knotentyp aufgelöst; sie
> zu klassifizieren ist eine Änderung an jeder von ihnen und bewusst nicht
> Teil dieses Schritts.


**F06 — Join-Identität.** `ElementInstancesOnNode` filtert heute nur auf
`ElementId`, nicht auf `FlowScopeKey`; parallele Iterationen eines
Multi-Instance-Subprozesses teilen Prozessinstanz und Knotennummer und
synchronisieren dadurch übereinander hinweg. In zwei Schritten:

1. *Scope-Korrektheit.* Join-Zustand mindestens nach (Prozessinstanz,
   Ausführungsscope, Gateway) identifizieren. Behebt den reproduzierten
   Fehler. Den Inclusive-Join und seine Erreichbarkeitsprüfung im selben Zug
   auf dieselbe Verwechslung prüfen — der Bericht hat sie dort statisch
   gesehen, aber nicht separat reproduziert.
2. *Zählung je eingehendem Flow.* Für allgemein korrekte Synchronisation nach
   OMG BPMN 2.0.2 §13.4 pro eingehendem Sequence Flow zählen und
   überschüssige Tokens erhalten. Das revidiert die in ADR-0024 bewusst
   akzeptierte vereinfachte Zählung und ist die grössere Änderung.

Schritt 1 ist Pflicht und dringend, Schritt 2 ist die eigentliche
Semantikschuld. Sie zu trennen ist bewusst: Schritt 1 ist ein Bugfix,
Schritt 2 eine Spezifikationsangleichung mit eigenem Risiko. Beide sind
umgesetzt, in dieser Reihenfolge und in getrennten Änderungen.

**F08 — XOR ohne Route.** `OnCompleting` schreibt heute das Completed-Ereignis,
*bevor* die Route feststeht, und kehrt bei fehlender Route wortlos zurück; der
Quellkommentar verweist auf Incidents als künftige Arbeit eines Meilensteins,
den es inzwischen gibt. Route vor dem erfolgreichen Abschluss bestimmen;
fehlende Route **oder Auswertungsfehler** parkieren die Ausführung in einem
dauerhaften, reparierbaren Fehlerzustand. Ein Auswertungsfehler muss dabei von
einem gültigen `false` unterscheidbar bleiben. Den analogen OR-Split
mitnehmen.

**F12 — Ausführungsbudget.** `RunUntilIdle` läuft ohne Yield und ohne
Kontext-, Zeit- oder Gesamtbudget, bis die Queue leer ist; die Grenze von
1024 Commands begrenzt nur einen Batch. Budget pro Instanz beziehungsweise pro
Verarbeitungsscheibe, nach dem der Scheduler fair abgibt oder einen sichtbaren
Incident erzeugt. Gültige BPMN-Zyklen dürfen dadurch nicht pauschal verboten
werden; automatische stark zusammenhängende Komponenten ohne Wartepunkt können
beim Deployment zusätzlich als Risiko markiert werden.

**ADR-Bedarf:** `draft-join-scope-identity` (revidiert ADR-0024),
`draft-execution-budget`.

**Fertig, wenn:** verschachtelte und parallele Multi-Instance-Subprozesse in
sämtlichen Abschlussreihenfolgen kein fremdes Token konsumieren; zwei Tokens
auf einem einzigen Eingang und Wiederholungen über denselben Join abgedeckt
sind; ein nicht routbares XOR genau einen Incident erzeugt, der genau einmal
fortgesetzt werden kann, ohne abgeschlossene Folgeaktionen zu wiederholen; und
ein selbstlaufender Zyklus eine konfigurierte Obergrenze einhält, während
unabhängige Prozesse, Gesundheitsabfragen und ein Abbruch des Verursachers mit
begrenzter Latenz möglich bleiben.

---

### AP5 — Objektautorisierung vollenden: F11 (M)

> **Stand: umgesetzt.** Der Endpunkt hat jetzt beide Achsen: die Rolle bleibt
> `any` — sonst bekäme genau die Person, für die die Route offen war, ein leeres
> Formular —, und die Objektfrage stellt der Handler.
>
> **Was die Umsetzung entscheiden musste.** Eine BPMN-Kandidatengruppe ist freier
> Text im Modell, und Atlas hatte ihr Verhältnis zu einer Identitätsgruppe nie
> definiert; ADR-0042 führt genau das seit Langem als Folgearbeit, und die
> Tasks-App nutzte das Attribut nur für «das ist eine Gruppentask». Die hier
> getroffene Regel: eine *unbeanspruchte* Task passt auf eine Identitätsgruppe der
> Aufruferin, über den Gruppennamen (ohne Rücksicht auf Gross-/Kleinschreibung)
> oder über die Gruppen-Id. Eine beanspruchte Task gehört ihrer Inhaberin allein.
> Das ist die einzige Stelle, an der dieser Schritt neues Produktverhalten
> festlegt statt bestehendes zu schützen — bewusst an einer Stelle, wo man es
> findet und ändern kann.
>
> **Enger als vorher, nicht nur zu.** Eine Taskinhaberin bekommt die Felder ihres
> Formulars und sonst nichts — weniger als vor dieser Änderung, obwohl diese
> Änderung diejenige ist, die den Endpunkt geschlossen hat. Eine Task *ohne*
> Formular gewährt dadurch nichts mehr: es gibt keine deklarierte Feldmenge, und
> eine zu raten ist der Weg, auf dem eine Positivliste zur Formalität wird.
>
> **Nicht behandelt, aber benannt:** `GET /api/v1/tasks/{key}` hat dieselbe Lücke
> in kleinerem Format, und die übrigen instanzbezogenen Lesezugriffe sind über die
> Rolle statt über die Beziehung geschützt — wer irgendwo `operator` ist, sieht
> jede Instanz des Servers. Beides ist dieselbe Achse und verdient dieselbe
> Behandlung.


Die in AP1 mit F09 begonnene zweite Achse wird zur Fläche. Instanzsichtbarkeit
wird aus dem fachlichen Berechtigungsmodell abgeleitet statt für jede
angemeldete Identität geöffnet. Für Taskformulare — der Grund, aus dem der
Endpunkt heute offen ist — nur die für *diese* Task zulässigen Variablen
liefern, gebunden an die Taskberechtigung. Ausdrücklich **nicht**: die Route
auf `operator` heben; das sperrt normale Taskbenutzer aus und löst die
Objektfrage nicht.

MCP und öffentliche Formulare halten denselben zulässigen Datenumfang ein —
sonst ist die Lücke nur verschoben. Das schliesst zugleich den im
ISDS-Punkt O-02 als offen geführten Rest («Sichtbarkeit von Prozessvariablen
an eine Berechtigung binden»); `docs/compliance/isds-offene-punkte.md` ist
entsprechend nachzuführen.

**Fertig, wenn:** die Zugriffsmatrix über fremde Projekte, fremde Tasks,
Kandidatengruppen, direkte Zuordnung und abgeschlossene Tasks besteht — für
Session, API-Token und MCP getrennt —, und bei verweigertem Zugriff keine
Seiteneffekte zurückbleiben.

---

### AP6 — Budgets und Entkopplung: F13, F14, F15, F16 (M)

> **Stand: umgesetzt.** F13, F14, F15 und F16 sind behoben; F16 in beiden
> Hälften.
>
> **F15.** Es fehlte keine Fähigkeit. Der Scan bricht seit jeher ab, wenn der
> Callback einen Fehler zurückgibt, und die API-Schicht hat mit
> `errListTruncated`/`unlessTruncated` längst das Muster dafür — die beiden
> Polling-Stellen haben es nur nicht benutzt. Der Worker-Pull hält jetzt bei der
> Seite an, die er wollte; der prozessinterne `Claim` nimmt eine Runde statt des
> ganzen Rückstaus, mit gleichem Anteil je bedientem Typ. Beide Aufrufer treiben
> ohnehin in einer Schleife bis leer, also kostet die Grenze eine Runde und keinen
> Job. Die dauerhafte Hälfte der Korrektur steht im Doc-Kommentar von
> `ActivatableJobs`: dort liest die nächste Aufruferin, was der Vertrag ist.
>
> **F14.** Die Ahnenmenge eines Inclusive-Joins wurde bei *jedem* Eintreffen neu
> hergeleitet — Reverse-Adjazenz über den ganzen Graphen, Map und Stack, alles
> allokiert und weggeworfen, je Tokenbewegung. Das ist I1 und I5 zugleich: ein
> kompilierter Prozess ist unveränderlich, seine Ahnen waren die letzten tausend
> Male dieselben. Jetzt einmal beim Build, als Bitset je relevanter Join-Stelle.
> Maps hätten dasselbe geleistet und für zehn Joins in tausend Knoten ein halbes
> Megabyte je Deployment gekostet; das Bitset kostet gut ein Kilobyte. Der Name
> hat sich mitgeändert: `NodesReaching` klang nach allgemeiner Graphabfrage und
> antwortete für jeden Knoten, `InclusiveJoinReach` sagt, für welche — denn eine
> leere Menge an einem echten Join liest sich als «nichts stromaufwärts» und
> lässt ihn zu früh feuern. Ein Compiler-Test hält fest, dass jeder Inclusive-Join
> eine Menge hat.
>
> **F16 — die scharfe Hälfte.** Die Iterationszahl einer Multi-Instance-Aktivität
> kommt aus dem Modell oder, häufiger, aus einer Instanzvariablen, und zwischen
> der Zahl und der Allokation stand nichts: eine Variable mit einer Milliarde sind
> eine Milliarde `expr.Value` in einem Aufruf, auf der Processor-Goroutine — die
> Partition ist weg, bevor irgendwer sagen kann warum. Die Grenze greift jetzt
> **vor** der Allokation, und die Ablehnung ist ein Incident am Body, der nach
> Korrektur der Daten auflösbar ist. Grenzwert−1 / Grenzwert / Grenzwert+1 sind
> getestet; der Grenzwert selbst ist erlaubt, sonst wäre es ein Budget von eins
> weniger.
>
> **F16 — die zweite Hälfte.** Der Bericht verlangte *einheitliche, konfigurierbare*
> Budgets. Es gab neunzig Stellen, die Eingaben begrenzten, dreissig benannte
> Konstanten neben ihren Handlern und ein paar blanke Literale direkt im Aufruf.
> Jede einzelne war dort, wo sie stand, vertretbar. Zusammen waren sie keine
> Richtlinie, weil nichts sagte, was die Menge *ist* — und eine Menge, die niemand
> aufzählen kann, ist eine Menge, in der niemand ein Loch bemerkt. Genau das hatte
> der Bericht gefunden.
>
> Jetzt gibt es `limits.Limits`: ein Wert, der jedes Budget benennt, gruppiert
> danach, *was* es hält (`ModelUpload`, `Request`, `Archive`, `TokenSteps`), mit
> Vorgaben, die exakt die bisherigen Zahlen sind. Namen, Umgebungsvariablen und das
> Einlesen sind aus der Struktur abgeleitet, nicht danebengeschrieben — eine zweite
> Liste wäre genau der Fehler, den das Paket beendet. Konfiguriert wird über
> `ATLAS_LIMIT_*`; ein unlesbarer Wert lässt die Vorgabe stehen und wird beim Start
> gemeldet, statt eine Grenze zu entfernen. Abschalten geht nicht: «aus» ist der
> Zustand, gegen den Budgets existieren.
>
> Zwei Dinge kamen dabei ans Licht. **Die Ausgabe eines Skripts hatte gar keine
> Grenze** — `cmd.Output()` sammelt stdout in einen `bytes.Buffer` ohne Deckel, und
> was ein Skript schreibt, bestimmt der Modellautor. `while true: print(x)` war eine
> unbegrenzte Allokation auf dem Host, gehalten nur vom 30-Sekunden-Timeout, was bei
> einem Gigabyte pro Sekunde keine Grenze ist. **Und die beiden Engine-Budgets waren
> Einstellungen ohne Aufrufer** — `SetExecutionBudget` und `SetMaxIterations`
> existierten und niemand rief sie. Beides ist behoben.
>
> Das Haltbare daran ist nicht die Liste, sondern `TestNoCeilingWithoutAName`: er
> geht die eigenen Quellen durch und schlägt fehl, sobald eine Eingabegrenze weder
> aus der Registry liest noch mit Begründung als etwas anderes eingeordnet ist. Nach
> dem Muster der Store-Registry aus AP3 — er prüft nicht, ob die Liste stimmt,
> sondern ob überhaupt etwas Unklassifiziertes existiert. Seine erste Fassung lief
> durch den Baum, ohne eine einzige Datei zu betreten, und meldete «ok»; deshalb
> zählt er jetzt, wie viel er gefunden hat.
>
> **Die Variablengrösse — nachgezogen.** Sie stand in der Liste des Berichts und war
> als Einzige offen geblieben, weil sie eine Entscheidung brauchte, die die
> Vereinheitlichung nicht traf: was beim Ablehnen passiert. Jetzt zwei Budgets, und
> zwar bewusst zwei (ADR-draft-a-variable-is-a-record). `Variable` fragt, was *ein
> fachlicher Datensatz* wiegen darf — ein Kunde, eine Bestellung, die Eingaben einer
> Entscheidung —, dafür ist ein Megabyte reichlich. `Collection` fragt, was ein
> legitimer Loop an der Iterationsdecke *ansammeln* darf, und das ist eine andere
> Grössenordnung: hunderttausend bescheidene Ergebnisse sind zweistellige Megabytes.
> Eine Zahl kann beides nicht: auf Datensatzmass gesetzt hören gewöhnliche Loops auf
> durchzulaufen, auf Sammlungsmass gesetzt ist sie für einen Datensatz gar keine
> Grenze mehr — also genau der Zustand, den sie ersetzt.
>
> **Aus einem harten Fehler wird ein auflösbarer.** Vorher lief ein zu grosser Wert
> bis ins WAL und scheiterte dort an der 64-MiB-Grenze je Datensatz — das bricht den
> Batch ab, die Instanz stand mit einem Fehler, den niemand auflösen konnte. Jetzt ist
> es ein Incident auf dem Element, das den Wert erzeugt hat, mit Namen und beiden
> Grössen; Auflösen schreibt erneut.
>
> **Was beim Schreiben des ADR auffiel.** `finishMultiInstanceIteration` ruft je
> beendeter Iteration `setListElement`: ganze Sammlung lesen, ein Element setzen,
> ganze Sammlung zurückschreiben. Für N Iterationen also N vollständige Kopien,
> geparst, serialisiert und **dauerhaft**. Der Bytesaufwand wächst **quadratisch** mit
> der Iterationszahl. `Collection` bemisst die Speicherspitze und ist ausdrücklich
> *keine* Schranke dagegen: um bei hunderttausend Iterationen unter zehn Gigabyte zu
> bleiben, dürfte die Sammlung 200 KB nicht überschreiten — weniger, als eine einzelne
> Variable darf. Ein Budget, das die Verstärkung sicher machte, wäre unbrauchbar. Die
> Verstärkung ist ein eigener Defekt mit eigener Korrektur und ist benannt, damit sie
> nicht wieder gefunden werden muss.
>
> **Und ein Fehlermodus, den eine Sonde fand, kein Review.** Die erste lauffähige
> Fassung lehnte im Trichter ab und liess den Aufrufer weiterlaufen: der Incident
> entstand, der Loop drehte weiter, die Instanz schloss ab — und nahm den Incident mit.
> Nichts geschrieben, nichts gemeldet, der Lauf sah erfolgreich aus. Der Trichter
> meldet sein Urteil deshalb jetzt, und die erzeugenden Stellen handeln danach: der
> Body bleibt aktiviert, statt Iterationen zu säen, deren Ergebnisse nirgendwo landen.
>
> **Nicht mitgemacht:** Komponenten, die im Prozess eines Workers laufen (die
> Antwort eines Modellanbieters, ein Remedy-Aufruf, die Fehlerausschnitte im
> Tracing) lesen den *benannten Vorgabewert*, nicht die Konfiguration dieser
> Installation — die Umgebung des Servers reicht dort nicht hin. Sie haben damit
> einen Namen an einer Stelle, was die Hälfte ist, die für sie gilt; die Verdrahtung
> der Worker-Konfiguration ist eine eigene Änderung.
>
> **F13.** Der Plan hatte recht mit der Reihenfolge: die Identität *war* die
> Arbeit, der Mutex stand nur dafür ein. `Claim` least jetzt — Aktivierung unter
> dem Namen `atlas:in-process`, wodurch der Job den Aktivierungsindex verlässt,
> bevor `Claim` zurückkommt; ein zweiter Claim kann ihn nicht mehr sehen.
> `Submit` prüft Lease und Epoche, bevor er anwendet, mit derselben Fence, die der
> HTTP-Abschluss einem externen Worker vorsetzt. Erst danach liess sich `driveMu`
> auf Claim und Submit verengen.
>
> Nicht ganz weggenommen: zwei unsynchronisierte Treiber würden jeder nur bis zu
> *ihrer* leeren Runde laufen, und das ist nicht dasselbe wie «das System ist
> ruhig». «Die Arbeit, die mein Request ausgelöst hat, ist erledigt, wenn er
> zurückkommt» ist ein Vertrag, an dem jeder Requestpfad und sehr viele Tests
> hängen. Zwei kurze Schritte zu serialisieren erhält ihn und kostet nichts
> Messbares.
>
> Der Test dazu hängt einen Handler auf und verlangt, dass währenddessen eine
> unabhängige Instanz startet. Gegen den vorherigen Code schlägt er fehl.


| Befund | Eingriff |
|---|---|
| **F13** Langsamer Worker blockiert unabhängige Mutationen | `driveMu` deckt heute die komplette Drain-Schleife inklusive `jobRunner.Work` ab. Claim und Completion als begrenzte Scheduleroperationen führen, Worker-Ausführung unabhängig von synchronen Request-Drain-Schleifen. **Voraussetzung**: interne Jobs brauchen eine eindeutige Claim-/In-flight-Identität, sonst führt die Lockerung Doppelausführung ein. Diese Identität ist der eigentliche Arbeitsinhalt, nicht das Entfernen des Mutex. |
| **F14** Erreichbarkeit am Inclusive-Join neu aufgebaut | Reverse-Adjazenz und benötigte Erreichbarkeitsmengen im Compiler vorhalten (Bitsets je relevanter Join-Stelle), Speicherbedarf gegen Abfragehäufigkeit abwägen. Zur Laufzeit nur noch dynamischen Tokenzustand gegen vorbereitete Daten prüfen. Erfüllt I1 und I5 an dieser Stelle wieder. |
| **F15** Job-Poll scannt die ganze Queue | Begrenzter Indexiterator beziehungsweise sauber behandelte Stop-Sentinel — der Callback gibt heute nach Erreichen von `want` weiter `nil` zurück, deshalb läuft `ActivatableJobs` den gesamten Index durch. Interne Claims (`Runner.Claim` sammelt *alle* aktivierbaren Jobs *aller* bedienten Typen und lädt jeden Datensatz auf dem Single-Writer) in begrenzte, faire Chargen aufteilen. Queue-**Alter** beobachten, nicht nur Queue-Länge. |
| **F16** Ressourcenbudgets unvollständig | Einheitliche, konfigurierbare Budgets für Response-Bytes, Script-Ausgabe, Variablengrösse, Multi-Instance-Anzahl (`nullList(n)` allokiert heute direkt aus einem modell- oder variablengesteuerten Wert) und aktive Arbeit. Grenzen greifen **vor** der grossen Allokation. Agent-Provider und Remedy haben bereits begrenzte Reader — das Muster existiert, es ist nur nicht überall angewandt. |

**Fertig, wenn:** ein hängender Worker Start, Fertigstellung und Abbruch
unabhängiger Vorgänge nicht bis zu seinem Timeout aufhält, bei gleichzeitig
höchstens einem aktiven internen Worker je Job; ein Poll mit `maxJobs=1` bei
1, 1 000 und 100 000 wartenden Jobs annähernd konstant wenig Einträge besucht,
ohne dass ein Job verloren geht oder mehrfach geleast wird; ein Benchmark
zeigt, dass am Join keine Topologie neu aufgebaut wird; und Grenzwert−1 /
Grenzwert / Grenzwert+1 je Budget nachvollziehbar fehlschlagen, ohne den
Prozess unerreichbar zu machen.

---

## 4. Reihenfolge und Abhängigkeiten

```
AP0 Harness  ──┬──────────────────────────────────────────────────────────
               │
   Spur A      ├─ AP1 Sofortmassnahmen (F09,F10,F17,F07) ─┐
   (klein)     │                                          ├─ AP5 (F11)
               │                                          │
   Spur B      └─ AP2 Persistenz (F02+F01) ─ AP3 (F03-F05)┤
   (gross)                                                 └─ AP4 (F06,F08,F12)
                                                                    │
                                                              AP6 (F13-F16)
```

- **AP0 vor allem anderen.** Ohne Messmassstab ist «behoben» eine Behauptung.
- **Spur A und Spur B laufen parallel.** Sie berühren disjunkte Pakete
  (`api/auth.go`, `api/handlers.go`, `cmd/atlas` gegen `wal/`, `engine/`).
- **AP3 nach AP2:** die Checkpoint-Installation muss wissen, welche
  Batchgrenzen gültig sind; F04 auf dem alten Format zu lösen heisst, es
  zweimal zu lösen.
- **AP4 nach AP2:** F12 braucht die Batchgrenze aus AP2, um ein Budget
  überhaupt sauber definieren zu können.
- **AP6 zuletzt:** F13 setzt eine Claim-Identität voraus, die nach AP2/AP4
  einfacher zu definieren ist; F14/F15/F16 sind unabhängig und können jederzeit
  vorgezogen werden, wenn Kapazität frei ist.

Freigabe für dauerhafte geschäftskritische Ausführung frühestens nach AP3 —
das ist der Punkt, an dem V1 und V2 geschlossen sind.

> **Stand:** AP0 bis AP6 sind umgesetzt und **alle siebzehn Befunde sind behoben.**
> V1, V2 und V3 sind geschlossen und die Freigabeschwelle oben ist erreicht. Offen
> sind nur noch die drei Nachbarlücken, die dieser Plan unterwegs benannt hat und
> die eigene Befunde verdienen: die Objektlücke an `GET /api/v1/tasks/{key}`, die
> übrigen instanzbezogenen Lesezugriffe, die über die Rolle statt über die Beziehung
> geschützt sind, und der bei F07 gefundene Kompensationsdefekt.

---

## 5. Teststrategie

Der Bericht hat recht mit der schärfsten seiner Aussagen: 95 % Coverage haben
diese Fehler nicht verhindert, und eine höhere Prozentzahl hätte es auch nicht
getan. Coverage misst ausgeführte Statements, nicht Zustandskombinationen. Was
fehlt, sind vier Testarten, die im Repository heute praktisch nicht vorkommen:

1. **Unterbrechungstests zwischen stabilen Wartezuständen.** Recovery-Prüfungen
   müssen Fortschritt und Effektanzahl vergleichen, nicht Snapshotgleichheit.
   `state == replay(WAL)` ist genau die Aussage, die F01 erfüllt und trotzdem
   falsch ist.
2. **Kombinatorik statt Beispiele.** Für Tokenlogik Kombinationen aus Scopes,
   eingehenden Flows und Batchreihenfolgen generieren.
3. **Negative Zugriffsmatrizen**, in denen erreichbare Rolle und konkrete
   Objektbeziehung *getrennt* variiert werden. Genau diese Trennung fehlt
   heute und ist der Grund, warum F09 und F11 an einer vollständig
   annotierten Routentabelle vorbeilaufen konnten.
4. **Vollständigkeitstests statt gepflegter Listen.** Der Store-Registry-Test
   aus AP3 ist der Prototyp: er prüft nicht, ob die Liste stimmt, sondern ob
   überhaupt etwas unklassifiziert existiert.

Ergänzend:

- Den vorhandenen unabhängigen BPMN-Vergleich (`-tags differential`, heute
  vier Kontrollflussmodelle, läuft **nicht** im normalen CI-Job) in einen
  regelmässigen CI-Job aufnehmen und um Multi-Instance, Call-Abbruch und
  Fehlerpfade erweitern. Ein Vergleich, der nicht läuft, ist keine Absicherung.
- Go-Fuzzing für XML-, Record-, WAL-/Checkpoint- und Restore-Grenzen. Das
  Inventar zählt heute **0** Fuzz-Funktionen bei 5 286 Testfunktionen; für ein
  Format, das gerade neu geschnitten wird (AP2), ist das die billigste
  Absicherung, die es gibt.
- Lastbenchmarks nur mit Hardwareangabe und Trend. Ein einmaliger schneller
  Lauf ist keine Kapazitätszusage.

---

## 6. Was dieser Plan bewusst nicht tut

- **Kein Neubau.** Der Bericht sagt es und ich teile es: die Grenzen zwischen
  Compiler, Prozessor, WAL, State und Worker sind tragfähig. Alle 17 Befunde
  sind lokalisierbare Vertragslücken, keine Strukturfehler.
- **Kein pauschales Zerlegen grosser Dateien.** `api/handlers.go` (5 927
  Zeilen auf `main`) und `engine/behavior.go` (4 172) sind ein
  Wartbarkeitsrisiko, aber Verschieben ohne Vertrag ist Verschieben von Code.
  Die Verträge aus Abschnitt 2 sind die sinnvollen Schnittkanten; ADR-0147 und
  `api/processdoc` zeigen die Richtung. Zerlegung folgt den Verträgen, nicht der Zeilenzahl.
- **Keine horizontale Skalierung.** Erst ein korrekt wiederanlaufender, fair
  begrenzter Einzelpartitionskern.
- **Keine Neubewertung der bewussten Produktentscheidungen.** Codeausführung
  durch berechtigte Modellierer, flüchtige Sessions, begrenzte
  BPMN-Unterstützung bleiben, was sie sind — dokumentierte Entscheidungen, die
  zur Einsatzumgebung passen müssen.
- **Keine Zeitzusagen.** Sizing ist relativ. Der WAL-Migrationsansatz aus AP2
  ist die einzige Unbekannte, die die Gesamtdauer wirklich bewegt.

## 7. Dokumentationsdrift (begleitend, S)

Der Bericht nennt sie zu Recht als eigenes Thema: Roadmap und ADR-Status sind
teilweise hinter dem Code (DMN-Modeler als Nichtziel in der Roadmap, während
README und Webbestand ihn enthalten; der XOR-Kommentar kündigt Incidents als
künftige Arbeit an, die es gibt). Pro zentraler Zusage künftig eine aktuelle
Implementierungsreferenz, ein Akzeptanztest und ausdrücklich offene Grenzen.
Historische ADRs dürfen den damaligen Stand bewahren — ein Statusabschnitt muss
aber das heutige Verhalten erklären. Das kostet wenig und ist die Ursache
dafür, dass F07 und F08 mit einer *Begründung im Code* danebenlagen.

---

## Anhang — Befundregister mit Zuordnung

| ID | Prio | Befund | Paket | Reproduktion | Stand |
|---|---|---|---|---|---|
| F01 | P0 | Recovery stellt keine ausstehende Folgearbeit wieder her | AP2 | `TestAuditRecoveryResumesCommittedFollowups` | behoben |
| F02 | P0 | WAL erkennt Frames, aber keine atomaren Batches | AP2 | `TestAuditPartialBatchMustNotRecoverHalfACommand` | behoben |
| F03 | P1 | Korruption in abgeschlossenen Segmenten still übersprungen | AP3 | `TestAuditCorruptionInSealedSegmentMustFail` | behoben |
| F04 | P1 | Nach Kompaktierung erfolgreicher, unvollständiger Recovery | AP3 | `TestAuditCompactedWALMissingStateFailsClosed` | behoben |
| F05 | P1 | Vollsicherung lässt zentrale Stores aus | AP3 | `TestAuditFullSnapshotContainsPersistentStores` | behoben |
| F06 | P1 | Joins vermischen Multi-Instance-Scopes | AP4 | `TestAuditParallelJoinSeparatesMultiInstanceScopes`, `TestTwoTokensOnOneFlowDoNotSatisfyTheOther` | behoben |
| F07 | P1 | Abbruch übersieht Kind aus demselben Batch | AP1 | `TestAuditCancelSeesChildCreatedInSameBatch` | behoben |
| F08 | P1 | XOR ohne Route verliert Token ohne Incident | AP4 | `TestAuditXORNoMatchRaisesIncident` | behoben |
| F09 | P1 | Deployment umgeht Projektmitgliedschaft | AP1 | `TestAuditRawDeployRequiresProjectMembership` | behoben |
| F10 | P1 | Entzogene Rollen bleiben in Sessions wirksam | AP1 | `TestAuditRoleRevocationInvalidatesExistingSession` | behoben |
| F11 | P1 | Jeder Benutzer liest fremde Instanzvariablen | AP5 | `TestAuditUnrelatedUserCannotReadInstanceVariables` | behoben |
| F12 | P1 | Automatische Zyklen besetzen den Single-Writer | AP4 | `TestAuditAutomaticCycleHasExecutionBudget` | behoben |
| F13 | P2 | Langsame Worker blockieren unabhängige Requests | AP6 | `TestAuditSlowWorkerDoesNotBlockIndependentMutation` | behoben |
| F14 | P2 | Erreichbarkeit am Inclusive-Join neu aufgebaut | AP6 | `TestAuditReachabilityAllocations` | behoben |
| F15 | P2 | Job-Polling scannt die ganze Warteschlange | AP6 | statisch belegt | behoben |
| F16 | P2 | Ressourcenbudgets unvollständig | AP6 | `TestNoCeilingWithoutAName`, `TestAScriptsOutputIsBounded`, `TestAVariableIsRefusedAtItsBudget` | behoben |
| F17 | P2 | Keine expliziten Lese-/Idle-Timeouts | AP1 | statisch belegt | behoben |
