# Identitäts-Lebenszyklus — eine Prozessinstanz je Identität

Ein Modell für die Frage: *Kann eine BPMN-Instanz je Mitarbeiter den kompletten
Lebenszyklus einer Identität aus SAP/CIS führen — inklusive aller bestellten
Business Services mit ihrem jeweiligen Zustand?*

Kurzantwort aus dem Testlauf auf server01 (siehe [TESTBERICHT.md](TESTBERICHT.md)):
Ja, mit einer bestimmten Bauform — und mit einer Grenze, die nicht im
Schreibpfad liegt, sondern im Lesepfad.

## Was das Modell tut

[`identitaet-lebenszyklus.bpmn`](identitaet-lebenszyklus.bpmn) hat zwei dauerhaft
nebenläufige Token im Wurzel-Scope:

**Spur A — der Lebenszyklus.** Jeder Zustand ist ein Receive-Task, der Token steht
also *in* dem Kästchen, das den Zustand benennt:

```
Erfasst ──▶ Aktiv ──▶ Austritt ──▶ Gesperrt ──▶ Archiviert ──▶ (terminate)
```

**Spur B — der Ereignis-Hub.** Ein ereignisbasiertes Gateway in einer Endlosschleife,
das beliebig oft Service-Ereignisse und Stammdaten-Mutationen verbucht.

Zustand liegt in zwei Datenobjekten:

| Datenobjekt | Wert | Datenzustand |
|---|---|---|
| `identitaet` | Stammdaten (Name, OrgEinheit, Funktion, Eintritt) | ERFASST → AKTIV → AUSTRITT → ARCHIVIERT |
| `services` (Collection) | je Produkt `{id, name, status, seit}` | — |

Atlas führt zu beiden eine lückenlose Historie mit Zeitstempel und schreibendem
Element. Damit ist der Lebenszyklus nicht nur als Momentaufnahme sichtbar,
sondern als Verlauf — ohne dass das Modell dafür eine eigene Protokollstruktur
braucht.

## Nachrichten

Alle korrelieren auf `identityId` (die Personalnummer aus SAP/CIS).

| Nachricht | Nutzlast | Wirkung |
|---|---|---|
| `identitaet-eintritt` | `zeitpunkt` | ERFASST → AKTIV |
| `identitaet-mutation` | `neueOrgEinheit`, `neueFunktion`, `zeitpunkt` | Stammdaten fortschreiben (nicht gelieferte Felder bleiben stehen) |
| `identitaet-austritt` | `zeitpunkt`, `grund` | AKTIV → AUSTRITT, alle Services auf GEKUENDIGT |
| `identitaet-loeschfrist` | `zeitpunkt` | GESPERRT → ARCHIVIERT, alle Services DEKOMMISSIONIERT, Instanz endet |
| `service-ereignis` | `serviceId`, `serviceName`, `serviceStatus`, `zeitpunkt` | Upsert ins Register |

`serviceStatus` ist ein freies Statusfeld — BESTELLT, IN_BETRIEB, GESTOERT,
GEKUENDIGT, DEKOMMISSIONIERT. Ein neuer Produktzustand kostet damit keine
Modelländerung. Bewusste Entscheidung: nicht ein Nachrichtentyp je Zustand.

Beispiel:

```
atlas_create_instance  key=<def>  {identityId:"P10001", nachname:"Muster", vorname:"Anna",
                                   orgEinheit:"IT-Betrieb", funktion:"Systemtechnikerin",
                                   eintritt:"2026-10-01"}
atlas_publish_message  service-ereignis  P10001  {serviceId:"m365-e5", serviceName:"Microsoft 365 E5",
                                                  serviceStatus:"BESTELLT", zeitpunkt:"2026-09-15"}
atlas_publish_message  identitaet-eintritt P10001 {zeitpunkt:"2026-10-01"}
```

## Warum es so gebaut ist

Die naheliegende Bauform — ein nicht-unterbrechender **Ereignis-Subprozess** je
Service-Bestellung, jeder mit eigenem Token und eigenem Produkt-Lebenszyklus —
funktioniert auf diesem Build **nicht**, und zwar still: sie deployt, läuft an,
und schreibt nichts. Zwei unabhängige Gründe (beide im Testbericht mit Beleg):

1. **Daten-Assoziationen werden nur im Wurzel-Scope verdrahtet.** Der Compiler
   läuft für `zeebe:ioMapping` und Multi-Instance rekursiv durch alle Scopes
   (`wireScopeIO`, `wireScopeMI`), für `dataInputAssociation` /
   `dataOutputAssociation` aber nur über die Elementlisten des Prozesses selbst.
   Eine Aktivität in einem (Ereignis-)Subprozess verliert ihre Daten-Assoziationen
   ohne Fehlermeldung.
2. **`zeebe:ioMapping` am Ereignis-Subprozess ist kein Ausweg.** Die beim
   Instanzstart vorgehaltene Scope-Instanz wertet ihre Eingabe-Zuordnung sofort
   aus (Register noch nicht initialisiert → null) und schreibt beim Auslösen ihre
   Ausgabe-Zuordnung — und überschreibt damit das Register mit null.

Ein nicht-unterbrechender **Boundary-Event** schreibt zwar korrekt (seine Kante
liegt im Wurzel-Scope), feuert aber genau einmal: nur Timer-Boundaries werden
neu scharfgestellt.

Bleibt die **Schleife im Wurzel-Scope**: sie wiederholt sich beliebig oft *und*
darf schreiben. Das ist Spur B.

Der Preis: die Produkte haben keinen eigenen Token, ihr Zustand steht im
Register statt in einer Tokenposition. Wer je Produkt einen sichtbaren eigenen
Ablauf braucht (mehrstufige Bereitstellung, eigene Fristen, eigene Incidents),
modelliert ihn als eigenen Prozess und startet je Bestellung eine Instanz —
verbunden über dieselbe `identityId`.

## Der Mengentest-Seeder

[`identitaet-massentest.bpmn`](identitaet-massentest.bpmn) erzeugt in einem Aufruf
`anzahl` Identitäten über eine parallele Multi-Instance-Call-Activity. Nur für
Tests: die erzeugten Identitäten sind Kind-Instanzen des Seeders, im Betrieb
entstehen sie als Wurzelinstanzen über die API. Der Seeder ist zugleich der
Aufräum-Hebel — wird seine Instanz terminiert, fallen alle Kinder mit
(im Test belegt: ein Aufruf, 101 Instanzen weg).

```
atlas_create_instance key=<seeder> {anzahl:1000, offset:1000, praefix:"MT-"}
```

## Bewegung: Dispatcher und Tageslast

Zwei Modelle erzeugen Bewegung in einem Bestand von Identitäten — das eine als
Werkzeug, das andere als Dauerlauf.

[`identitaet-bewegung-eins.bpmn`](identitaet-bewegung-eins.bpmn) ist der
Dispatcher: eine kurzlebige Instanz, die genau eine der fünf Nachrichten an genau
eine Identität wirft. Warum je Ziel eine eigene Instanz und nicht eine Schleife im
Fächer: ein Message-Throw wertet den Korrelationsschlüssel im *Instanz*-Scope aus,
nicht im Iterations-Scope einer Multi-Instance. Eine Schleife könnte den Schlüssel
also nicht je Runde wechseln, eine Kind-Instanz kann es — sie bekommt ihre
`identityId` als Startvariable.

```
atlas_create_instance key=<dispatcher> {identityId:"MT-42", bewegung:"austritt",
                                        zeitpunkt:"2026-09-04"}
```

[`identitaet-tageslast.bpmn`](identitaet-tageslast.bpmn) simuliert in Echtzeit,
was 50.000 Identitäten an einem Arbeitstag produzieren: rund **1.800 Ereignisse**
— 24 Eintritte, 24 Austritte, 24 Löschfristen, 300 Stammdaten-Mutationen und
etwa 1.400 Service-Ereignisse. Der Takt kommt aus einem zyklischen Timer-Startevent
im Server (`R/PT1M`); der Generator läuft also ohne Zutun von aussen weiter. Die
Tagesform hat eine Vormittagsspitze, ein Mittagstal und nachts Ruhe, dazu um
02:00 einen JML-Batch aus SAP. Abschalten heisst: Definition löschen.

Bemerkenswert an der Zahl ist ihre Kleinheit. Ein voller Arbeitstag von 50.000
Identitäten ist rund ein Ereignis alle 30 Sekunden, in der Spitze vier pro Minute.
Der Sinn des Generators ist nicht Last, sondern eine Oberfläche, die sich bewegt
wie im Betrieb — neben 50.000 ruhenden Instanzen und 150.000 Tokens.

### Warum die Streuung ein Zufallsersatz mit Vorsicht ist

FEEL hat kein `random()`, und die Wanduhr allein taugt nicht als Ersatz. Der Takt
ist exakt 60 s lang, Minute und Sekunde laufen also im Gleichschritt — jede
*affine* Formel darauf (`a*min + b*sek + c*i mod n`) steht damit über alle Takte
auf denselben wenigen Resten. Im ersten Entwurf war genau das der Fall: statt
1.900 wurden nur **250** verschiedene Identitäten pro Tag getroffen, und sobald
die Sekunde von Takt zu Takt driftete, brach die Ereignismischung auf zwei Werte
zusammen (48 % IN_BETRIEB, 35 % DEKOMMISSIONIERT, kein einziges BESTELLT).

Der Generator mischt deshalb die Sekunde des Tages erst durch einen
Lehmer-Schritt (`x * 48271 mod 2^31-1`, MINSTD) und zieht die vier Würfel aus
verschiedenen Bit-Bereichen des Ergebnisses. Über einen simulierten Tag gemessen:
1.910 Ereignisse auf 1.910 verschiedene Identitäten, Mischung stabil bei jeder
Sekundendrift. Das Zwischenprodukt bleibt unter 1,4e13 und damit weit unter 2^53,
ist also auch in doppelter Genauigkeit exakt.

Weil das eine reine Funktion der Uhrzeit bleibt, ist es replay-fest. Ein echter
Zufallsgenerator wäre es nicht — `applyToState` läuft live *und* bei der
Wiederherstellung (Invariante I4).

## Was der Test gezeigt hat

| | |
|---|---|
| Funktional | Vollständiger Lebenszyklus durchgespielt, inkl. Vorab-Bestellung vor Eintritt, Störung, Mutation, Massenkündigung, Dekommissionierung |
| Kosten je Identität | 3 Token, 2 Datenobjekte, 3 Nachrichten-Abos |
| Erzeugung | ~2.500 Instanzen/s |
| Gemessene Menge | 11.002 aktive Identitäten, 33.006 Token, 0 Incidents |
| Grenze | **Nicht** der Schreibpfad. Die Instanzsuche (`/api/v1/instances/search`) ist ein ungeindizierter Vollscan über aktive *und* abgeschlossene Instanzen und läuft im Single-Writer-Loop — bei 11.000 Instanzen im Timeout, und währenddessen steht die Engine |

Details, Belege und die Konsequenzen für 50.000 Identitäten: [TESTBERICHT.md](TESTBERICHT.md).
