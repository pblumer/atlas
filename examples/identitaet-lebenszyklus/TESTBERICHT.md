# Testbericht — Identitäts-Lebenszyklus als Prozessinstanz je Mitarbeiter

**Datum:** 2026-09-03
**Server:** `atlas.blumer.cloud` (server01), Atlas `0.4.0-dev`, Revision `1020986`
**Zugang:** ausschliesslich über die Atlas-MCP-Werkzeuge
**Frage:** Trägt eine Prozessinstanz je Identität (bei 50.000 Mitarbeitern also
50.000 Instanzen) den kompletten Lebenszyklus samt aller bestellten Business
Services mit deren Zuständen?

**Verdikt:** Ja, fachlich und im Schreibpfad. Die Grenze liegt woanders als
erwartet — nicht bei den Instanzen, sondern bei der Abfrage über die Instanzen.
Die Idee, alles in *einer* Instanz je Identität zu halten, trägt; die
naheliegendste Bauform dafür trägt nicht und scheitert still.

---

## 1. Kennzahlen

| Kennzahl | Wert |
|---|---|
| Aktive Identitäts-Instanzen am Ende | **11.002** |
| Aktive Token | **33.006** (3 je Identität) |
| Neue Incidents durch den Test | **0** |
| Erzeugungsdurchsatz | **~2.500 Instanzen/s** (1.000 Instanzen in 0,39 s Server-Zeit) |
| Fachlich vollständig durchgespielte Identitäten | 3 (P10001 bis P10003) |
| Verworfene Modellvarianten mit Beleg | 3 |
| Erste harte Grenze | Instanzsuche ab ~11.000 Instanzen im Timeout |

---

## 2. Fachlicher Durchlauf

Identität **P10001** (Anna Muster, IT-Betrieb) wurde vollständig durchgespielt:

| Schritt | Nachricht | Ergebnis |
|---|---|---|
| Anlage aus SAP/CIS | Start | `identitaet` = Stammdaten, Datenzustand **ERFASST** |
| Vorab-Bestellung *vor* Eintritt | `service-ereignis` m365-e5 BESTELLT | Register: 1 Eintrag |
| Eintritt | `identitaet-eintritt` | Datenzustand **AKTIV** |
| Inbetriebnahme | `service-ereignis` m365-e5 IN_BETRIEB | Status fortgeschrieben |
| Zweites Produkt | `service-ereignis` vpn-remote BESTELLT | Register: 2 Einträge |
| Störung | `service-ereignis` vpn-remote GESTOERT | Status **GESTOERT** |
| Abteilungswechsel | `identitaet-mutation` → IT-Security | Stammdaten fortgeschrieben, Zustand bleibt AKTIV |
| Störung behoben | `service-ereignis` vpn-remote IN_BETRIEB | Status zurück auf IN_BETRIEB |
| Austritt | `identitaet-austritt` | Zustand **AUSTRITT**, *beide* Services in einem Schritt GEKUENDIGT |
| Löschfrist abgelaufen | `identitaet-loeschfrist` | Zustand **ARCHIVIERT**, alle Services DEKOMMISSIONIERT, Instanz beendet |

Die Historie des Datenobjekts `identitaet` enthält danach lückenlos alle fünf
Zustandsübergänge mit Zeitstempel und schreibendem Element; die Historie von
`services` alle sieben Registerstände. Das ist der eigentliche Gewinn der
Bauform: der Verlauf entsteht als Nebenprodukt, nicht als eigene Datenstruktur.

**Ein Fehler im ersten Anlauf, gefunden und behoben:** die durchsuchbare
Spiegelvariable `serviceUebersicht` wurde bei Austritt und Archivierung nicht
nachgeführt — eine Suche nach „GEKUENDIGT" hätte die ausgetretene Identität
nicht gefunden, obwohl das Register korrekt war. Version 2 des Modells führt
sie an beiden Stellen nach (`s_ueb_austritt`, `s_ueb_archiv`), belegt an P10002.

**Nachrichten in falscher Reihenfolge gehen verloren.** Ein `identitaet-austritt`
für eine Identität, die noch im Zustand ERFASST steht, korreliert auf kein
Abonnement und ist ein stiller No-Op — kein Fehler, kein Incident, keine
Rückmeldung an den Absender (`atlas_publish_message` meldet nicht, ob korreliert
wurde). Atlas puffert Nachrichten nicht. Für eine SAP/CIS-Kopplung heisst das:
das Quellsystem muss Reihenfolge garantieren oder wiederholen; ein
„Feuer-und-Vergiss"-Ereignisstrom verliert Ereignisse ohne Spur.

**Gleichzeitige Ereignisse:** vier zeitgleich abgesetzte `service-ereignis` für
dieselbe Identität (P10003) wurden alle vier verbucht — die Hub-Schleife stellt
ihr Abonnement offenbar noch in derselben Verarbeitungskette wieder scharf.
Das ist ein Stichprobenbefund, kein Beweis der Rennfreiheit: bei sehr vielen
Ereignissen je Identität in derselben Millisekunde bleibt ein Restrisiko.

---

## 3. Drei Bauformen, die nicht funktionieren — mit Beleg

Die intuitive Modellierung wäre: je Service-Bestellung ein eigener Token mit
eigenem Produkt-Lebenszyklus, sichtbar im Diagramm. Drei Anläufe, alle
verworfen. Alle drei **deployen fehlerfrei und laufen an** — sie schreiben nur
nichts. Genau das macht sie gefährlich.

### 3.1 Ereignis-Subprozess mit Daten-Assoziationen

Sonde `probe_ident_esp`: nicht-unterbrechender Ereignis-Subprozess auf
`service-ereignis`, darin ein Script-Task, der das Register über
Eingabe-/Ausgabe-Assoziation liest und schreibt.

*Beobachtet:* zwei Bestellungen erzeugen korrekt zwei nebenläufige
Subprozess-Instanzen — aber das Datenobjekt bleibt leer, und die
Skript-Variable steht auf `null`.

*Ursache im Code:* `compiler/parse.go` verdrahtet `zeebe:ioMapping`
(`wireScopeIO`) und Multi-Instance (`wireScopeMI`) rekursiv durch alle Scopes,
`dataInputAssociation` / `dataOutputAssociation` dagegen nur über die
Elementlisten des Prozesses selbst (`proc.ScriptTasks`, `proc.ServiceTasks`, …).
Eine Aktivität innerhalb eines Subprozesses verliert ihre Daten-Assoziationen
beim Kompilieren — ohne Fehler, ohne Warnung.

> Das ist aus meiner Sicht ein Defekt, kein Design: die Laufzeit adressiert
> Datenobjekte ohnehin über `ei.ProcessInstanceKey`, wäre also scope-fähig.
> Der Fix wäre klein (dieselbe Rekursion wie `wireScopeIO`) — er gehört aber in
> einen eigenen Change mit Tests, nicht in diesen Test.

### 3.2 Ereignis-Subprozess mit `zeebe:ioMapping`

Sonde `probe3_ident`: statt Daten-Assoziationen die Ein-/Ausgabe-Zuordnung am
Subprozess selbst — die wird ja rekursiv verdrahtet.

*Beobachtet:* Das Register wird beim ersten Auslösen mit `null` überschrieben.

*Ursache, aus der Zeitleiste:* Der Ereignis-Subprozess wird bereits beim
Instanzstart als vorgehaltener Scope angelegt. Dieser wertet seine
Eingabe-Zuordnung sofort aus — da ist das Register noch nicht initialisiert,
also `null` — und schreibt beim Auslösen seine Ausgabe-Zuordnung, also dieses
`null`, in den Instanz-Scope zurück. Der ausgelöste Durchlauf liest danach das
bereits zerstörte Register.

### 3.3 Nicht-unterbrechender Boundary-Event

Sonde `probe4_ident`: Boundary-Event mit `cancelActivity="false"` an einem
Receive-Task, Schreiben über Daten-Assoziationen im Wurzel-Scope.

*Beobachtet:* Die **erste** Nachricht wird korrekt verbucht (Wurzel-Scope,
Assoziationen greifen). Die zweite verpufft.

*Ursache:* Der Boundary-Event wird nach dem Auslösen nicht neu scharfgestellt.
In `engine/behavior.go` gibt es Re-Arm nur für wiederkehrende **Timer**
(`recurringBoundarySchedule`, `rearmTimerElement`), nicht für Nachrichten.

### 3.4 Was übrig bleibt

| Konstruktion | wiederholt sich | darf ins Datenobjekt schreiben |
|---|---|---|
| Ereignis-Subprozess (Nachricht, nicht-unterbrechend) | ✅ beliebig oft | ❌ |
| Boundary-Event (Nachricht, nicht-unterbrechend) | ❌ genau einmal | ✅ |
| **Schleife im Wurzel-Scope** | ✅ | ✅ |

Deshalb Spur B: ein ereignisbasiertes Gateway in einer Endlosschleife, ein
Token, alle Schreibzugriffe im Wurzel-Scope.

---

## 4. Menge

Erzeugt über einen Seeder mit paralleler Multi-Instance-Call-Activity
(einzeln über MCP wären es bei ~1,5 s je Aufruf über vier Stunden für 10.000).

| Charge | Instanzen | Beobachtung |
|---|---|---|
| 100 | 100 | Iteration am Visit-Count belegt (`s_stammdaten` = 102), nicht an der Laufzeit |
| 1.000 | 1.000 | 0,39 s zwischen erster und letzter Instanzerzeugung |
| 10.000 | 10.000 | Serverseitig sauber durchgelaufen — **der MCP-Aufruf lief dabei in den Client-Timeout**, weil `create_instance` synchron bis zum Leerlauf der Engine wartet. Die Arbeit war trotzdem vollständig |

Endstand: 11.002 aktive Identitäten, 33.006 Token, 44.029 Elementinstanzen,
0 Incidents. Korrelation stichprobenweise geprüft: `service-ereignis` an
MT-1001 (1.000er-Charge) und MT-15000 (10.000er-Charge) wurden beide verbucht
(`s_register` 5 → 7 Besuche).

**Aufräumbarkeit belegt:** Terminieren der Seeder-Instanz beendet ihre Kinder
mit — ein Aufruf, 101 Instanzen weg.

**Hochrechnung auf 50.000:** 150.000 Token, 100.000 Nachrichten-Abonnements,
~20 s reine Erzeugung. Im Schreibpfad ist daran nichts auffällig.

---

## 5. Die eigentliche Grenze: der Lesepfad

Bei 11.000 aktiven Instanzen läuft `atlas_search_instances` in den Timeout —
sowohl bei einer Statussuche (`serviceUebersicht=GESTOERT`) als auch bei einer
Punktabfrage (`identityId=MT-5000`). `atlas_stats` und
`atlas_process_runtime` antworten in derselben Situation sofort; der Server ist
also gesund, der Suchpfad ist das Problem.

Ursache in `api/handlers.go` (`handleSearchInstances`): die Suche

1. scannt **jede aktive und jede abgeschlossene** Prozessinstanz linear,
2. liest je Instanz alle Variablen und filtert in Go — es gibt keinen Index über
   Variablenwerte,
3. und tut das **innerhalb von `s.do(...)`**, also im Single-Writer-Loop der
   Engine.

Punkt 3 wiegt schwerer als Punkt 1: eine langsame Suche hält nicht nur sich
selbst auf, sie hält die **gesamte Verarbeitung** an. Auf server01 kommen zu den
11.000 aktiven noch mehrere hunderttausend abgeschlossene Instanzen aus früheren
Tests hinzu, die jede Suche mitscannt.

Für das Vorhaben ist das der zentrale Punkt, denn die Suche ist genau der
Mechanismus hinter „auf einen Blick": *Zeig mir alle Identitäten im Zustand
AUSTRITT, deren Produkte noch nicht dekommissioniert sind.* Diese Frage
beantwortet die Instanzsuche heute bei 50.000 Identitäten nicht.

**Das ist kein Konzeptfehler, sondern eine fehlende Fähigkeit der Engine.**
Drei Wege, unabhängig voneinander gangbar:

1. **Variablenindex + Paginierung + Ausführung ausserhalb des Run-Loops** (auf
   einem Snapshot statt im Single-Writer). Der saubere Weg, im Kern des Servers.
2. **Export in eine Suchmaschine.** Der OpenSearch-Event-Exporter (ADR-0114) ist
   vorhanden — die Zustandsabfrage läuft dann dort, nicht auf dem Run-Loop.
3. **Fachliche Sicht ausserhalb.** Zustand und Register zusätzlich in ein
   Lesemodell schreiben (clio-Worker o. ä.) und Auswertungen dort fahren.

Bis dahin bleibt der Betrieb möglich, aber gezielt: Zugriff je Identität über
den Instanzschlüssel (`atlas_instance_data_objects`) ist sofort da,
`atlas_process_runtime` liefert die Verteilung über die Zustände (Token je
Element) in konstanter Zeit. Was fehlt, ist die freie Suche über Inhalte.

---

## 6. Nebenbefunde

- **Eingabe-Assoziationen kosten dauerhaft Speicher.** Eine
  `dataInputAssociation` schreibt ihre Kopie mit `ScopeKey =
  ProcessInstanceKey` — sie bleibt also für die Lebensdauer der Instanz stehen
  (anders als `zeebe:ioMapping`-Lokale, die beim Abschliessen verworfen werden).
  Vier verschiedene Lesevariablen bedeuteten vier vollständige Kopien des
  Registers je Identität; im Modell heissen deshalb alle Lesezugriffe `reg`.
  Bei 50.000 Identitäten mit grossem Register ist das der grösste einzelne
  Speicherposten des Modells.
- **FEEL auf 0.4.0-dev:** `for i in 1..n return i` liefert korrekt eine Liste —
  der im Lasttest vom 2026-08 dokumentierte Null-Fehler ist behoben.
  `context put`, `get entries`, `get value`, `string join`, `append`,
  Listenfilter `liste[feld = wert]` und `string(zahl)` funktionieren alle.
  **`get keys` gibt es nicht** — der Aufruf kompiliert, liefert aber still
  `null`; `get entries` ist die richtige Funktion.
- **Nachrichtennutzlast ist instanzweit.** Die Variablen einer korrelierten
  Nachricht landen im Wurzel-Scope der Instanz, nicht im auslösenden Zweig.
  Zwei gleichzeitige Ereignis-Subprozesse teilen sich also dasselbe
  `serviceId` — ein weiterer Grund, warum der Ereignis-Subprozess-Ansatz nicht
  trägt.

---

## 7. Empfehlung

Die Grundidee trägt: eine Instanz je Identität, Lebenszyklus als
Tokenposition, Produkte als Register im Datenobjekt. Sie liefert die
Zustandsübersicht *und* die vollständige, revisionsfeste Historie ohne
zusätzliche Datenhaltung.

Vor einem Rollout auf 50.000 sind drei Dinge zu klären:

1. **Lesepfad** (Abschnitt 5) — ohne indizierte Suche fehlt die
   Auswertungsfähigkeit, die den ganzen Ansatz attraktiv macht.
2. **Ereigniszustellung** — Atlas puffert nicht und meldet dem Absender nicht,
   ob korreliert wurde. Die SAP/CIS-Kopplung braucht Reihenfolge- und
   Zustellgarantie auf ihrer Seite, plus eine Abgleichmöglichkeit.
3. **Produkt-Lebenszyklus** — reicht ein Status je Produkt im Register, oder
   braucht ein Produkt einen eigenen mehrstufigen Ablauf (Fristen, Incidents,
   Wiedervorlagen)? Im zweiten Fall gehört je Bestellung eine eigene
   Prozessinstanz her, verbunden über dieselbe `identityId` — nicht in die
   Identitäts-Instanz hinein.

## 8. Was seit dem Test behoben ist

Der Test hat drei Defekte und einen Engpass gefunden. Drei davon sind auf dem
Branch behoben, jeder mit einem Test, der ohne den Fix fehlschlägt.

| Befund | Stand |
|---|---|
| Daten-Assoziationen in Subprozessen werden still verworfen (§3.1) | **behoben** — der Compiler verdrahtet sie jetzt durch den ganzen Scope-Baum, wie er es für `ioMapping` und Multi-Instance längst tat |
| `ioMapping` am Ereignis-Subprozess überschreibt mit `null` (§3.2) | **behoben** — die vorgehaltene Wächter-Instanz trägt die Element-Id des Handlers und bekam deshalb dessen Zuordnungen; sie gehören der Handler-Ausführung |
| Instanzsuche blockiert die Engine (§5) | **behoben** — Abfragen laufen auf einer konsistenten Momentaufnahme ausserhalb des Run-Loops |
| Nicht-unterbrechender Message-Boundary feuert nur einmal (§3.3) | **offen** — Re-Arm gibt es nur für Timer; das ist eine BPMN-Abweichung und ein eigener Change |

Beim Optimieren kam ein vierter, gravierenderer Fund dazu, den der Test nicht
zeigen konnte, weil wir den Seeder nie terminiert haben: `terminateChildInstance`
suchte das Kind einer Call-Activity durch einen Vollscan **aller** lebenden
Instanzen — einmal je abzubauender Call-Activity, im Prozessor. Der Abbruch des
10.000er-Seeders hätte in der Grössenordnung 10⁸ Vergleiche gekostet, mit
stehender Engine. Dafür gibt es jetzt einen Rückwärts-Index.

**Gemessen** (50.000 aktive + 50.000 abgeschlossene Instanzen, acht Variablen je
Instanz, Suche über alle):

| | |
|---|---|
| die Suche selbst | ~2,0 s (unverändert — sie läuft weiterhin durch) |
| längste Wartezeit für alles andere auf dem Run-Loop | **200–479 µs** |
| derselbe Durchlauf im Loop, wie vorher | **1,8–1,9 s**, für alle |

Die Suche ist nicht schneller geworden. Der Unterschied ist, dass die Engine
nicht mehr auf sie wartet.

**Wichtig:** Das alles liegt auf dem Branch, nicht auf server01. Der Server läuft
weiter mit dem Build von vor dem Test — die 11.000 Instanzen dort zeigen also
unverändert das alte Verhalten, bis der Branch deployed ist.

## 9. Zustand von server01 nach dem Test

Auf ausdrücklichen Wunsch **nicht aufgeräumt**. Auf dem Server stehen:

| Was | Schlüssel | Zustand |
|---|---|---|
| `identitaet-lebenszyklus` v1 | 385 | P10001 vollständig durchgelaufen (abgeschlossen) |
| `identitaet-lebenszyklus` v2 | 386 | 11.002 aktive Instanzen |
| `identitaet-massentest` (Seeder) | 387 | 2 aktive Seeder-Instanzen (1.000er- und 10.000er-Charge) |
| Sonden `probe_feel_caps`, `probe_ident_esp`, `probe3_ident`, `probe4_ident`, `probe_feel_agg`, `probe_feel_liste` | 379–384 | teils mit geparkten Instanzen |

Aufräumen später: die beiden Seeder-Instanzen terminieren (nimmt alle
MT-Identitäten mit), dann die Sonden-Definitionen löschen. **Solange die 11.000
Instanzen stehen, bleibt die Instanzsuche auf server01 im Timeout** — auch für
andere Prozesse und für die Web-UI.
