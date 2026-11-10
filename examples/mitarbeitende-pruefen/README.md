# Mitarbeitende prüfen und korrigieren

Der Personalstamm, an Regeln gemessen, die in DMN stehen, und korrigiert bis zur Datei,
die wieder ins Personalsystem geladen wird
([ADR-0548](../../docs/adr/0548-bulk-validation-evaluates-a-decision-per-row-in-one-job.md),
Plan [AD-Suche als Dokument](../../docs/planning/ad-search-as-document.md)).

| Datei | Inhalt |
|---|---|
| [`mitarbeitende-datei-pruefen.bpmn`](mitarbeitende-datei-pruefen.bpmn) | Eine hochgeladene CSV-Datei prüfen. |
| [`mitarbeitende-sql-pruefen.bpmn`](mitarbeitende-sql-pruefen.bpmn) | Die Tabelle `dbo.Mitarbeitende` eines SQL Servers als Dokument lesen und prüfen. |
| [`mitarbeitende-korrigieren.bpmn`](mitarbeitende-korrigieren.bpmn) | Prüfen und korrigieren, vom Upload bis zur korrigierten Datei (ADR-0548, S2). |
| [`mitarbeitende-pruefen.dmn`](mitarbeitende-pruefen.dmn) | Die Entscheidung `MitarbeitendeGueltig`, die alle drei verwenden. |
| `*.form.json` | Upload, Korrektur eines Datensatzes, Korrektur der Datei. |
| [`mitarbeitende.csv`](mitarbeitende.csv), [`mssql-seed.json`](mssql-seed.json) | 200 erfundene Datensätze, 12 mit Befund; als Upload und als Seed des MS-SQL-Mockups. |

## Die Regeln

| Ausgabe | Regel |
|---|---|
| `mailGueltig` | Die Adresse endet auf `@beispiel.ch`. |
| `abteilungBekannt` | Buchhaltung, Informatik, Logistik, Personal oder Vertrieb. |
| `pensumGueltig` | Eine Zahl von 10 bis 100. |
| `datenKonsistent` | Kein Austritt, oder der Austritt liegt nicht vor dem Eintritt. |

Abteilungen, Domain und Grenzen sind **Annahmen dieses Beispiels**.

## Prüfen und korrigieren

```
Datei hochladen -> Datensätze prüfen -> Befunde?
   keine         -> Ende "Datei in Ordnung"
   höchstens 20  -> ein Task je Befund, sofort nachgeprüft  ─┐
   mehr          -> Befunddatei herunterladen, korrigieren,   ├─> Korrekturen anwenden -> nochmals prüfen
                    hochladen (oder den Rest akzeptieren)    ─┘
```

- **Wenige Befunde** (die Schwelle 20 steht im Modell): ein Multi-Instance-Subprozess über
  `pruefung.failures`. Jeder Befund wird ein Task mit seinen Werten; die Korrektur wird mit
  derselben Entscheidung nachgeprüft und so lange zurückgegeben, bis sie gültig ist. Die
  Ausgabesammlung `korrekturen` ist eine Liste, jeder Eintrag mit seiner `_row`.
- **Viele Befunde:** Die Prüfung legt neben der Ergebnisdatei eine **Befunddatei** ab
  (`pruefung.failuresDocument`): nur die fehlerhaften Datensätze, mit `_row`, den verletzten
  Regeln und einer leeren Spalte `_drop`. Der Task *Datei korrigieren* bietet sie zum
  Herunterladen an. Werte in den Spalten des Personalstamms korrigieren, `_row` stehen
  lassen, `x` in `_drop` verwirft einen Datensatz, die Datei wieder hochladen. Oder die
  verbleibenden Befunde mit einer Begründung akzeptieren.
- **Korrekturen anwenden** (`atlas:csvConnector operation="apply-corrections"`): schreibt eine
  korrigierte Kopie der geprüften Datei. Jeder Datensatz, den eine Korrektur über seine
  `_row` nennt, wird ersetzt oder verworfen; alle anderen bleiben, wie sie sind; Kopfzeile,
  Trennzeichen und Byte-Order-Mark bleiben erhalten. Die hochgeladene Datei wird nie
  verändert. Eine `_row`, die die Datei nicht hat, eine doppelte `_row` oder ein Feld ohne
  Spalte lässt den Task scheitern: Eine Korrektur kann keinen Datensatz unbemerkt hinzufügen
  oder umbauen.
- Die korrigierte Kopie wird **noch einmal geprüft**. Der Prozess endet erst ohne Befund oder
  mit akzeptiertem Rest.

Getestet in `worker/quality_corrections_test.go`: die 200 mitgelieferten Datensätze über den
Token-Weg (mit einer Korrektur, die die Nachprüfung zurückgibt), 1 000 generierte über den
Datei-Weg auf einer Worker-Instanz, und das Akzeptieren des Rests.

## Ausprobieren

1. Beispiel installieren (Handbuch → Beispiele → *Mitarbeitende prüfen*).
2. *Mitarbeitende prüfen und korrigieren* starten und `mitarbeitende.csv` hochladen: 12
   Befunde, also zwölf Korrektur-Tasks (Gruppe `quality`).
3. Für den Datei-Weg mehr Befunde erzeugen:
   `go run ./examples/testdaten/generieren -n 1000 -out ./testdaten` (60 Befunde) und
   `testdaten/mitarbeitende.csv` hochladen.

Für den SQL-Server-Prozess unter Console → Workers einen MS-SQL-Worker `hr` anlegen, den
Mockup einschalten und `mssql-seed.json` als Seed hinterlegen.
