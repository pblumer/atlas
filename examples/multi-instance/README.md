# Multi-Instance: parallel, sequenziell, abgebrochen und pro CSV-Zeile

Zwei kleine Prozesse, gebaut, um die gruppierte Multi-Instance-Darstellung der
**Operations-Historie** auszuprobieren: Jede Ausführung eines Multi-Instance-Elements
erscheint dort als eine eingeklappte Zeile mit der Anzahl Ausführungen und lässt sich
aufklappen, um jede Runde einzeln zu sehen. Beide Prozesse brauchen keinen
konfigurierten Worker.

## Die Artefakte

| Datei | Inhalt |
|---|---|
| [`bestellpositionen.bpmn`](bestellpositionen.bpmn) | **MI-Beispiel: Bestellpositionen** (`mi_sample_bestellpositionen`): drei Multi-Instance-Elemente über `positionen` |
| [`csv-datensaetze.bpmn`](csv-datensaetze.bpmn) | **MI-Beispiel: CSV-Datensätze prüfen** (`mi_sample_csv_datensaetze`): eine Aufgabe pro CSV-Zeile |
| [`position-freigeben.form.json`](position-freigeben.form.json) | Formular `mi-position-freigeben` |
| [`bestellung-ueberarbeiten.form.json`](bestellung-ueberarbeiten.form.json) | Formular `mi-bestellung-ueberarbeiten` |
| [`csv-upload.form.json`](csv-upload.form.json) | Formular `mi-csv-upload` (Datei-Auswahl → `csvText`) |
| [`datensatz-pruefen.form.json`](datensatz-pruefen.form.json) | Formular `mi-csv-datensatz-pruefen` |
| [`beispiel-datensaetze.csv`](beispiel-datensaetze.csv) | Testdatei: 15 Datensätze, Semikolon, UTF-8 |

## MI-Beispiel: Bestellpositionen

```
Start → Position bewerten → Position freigeben → (X) Position abgelehnt?
                                  ↑                  │ ja → Bestellung überarbeiten ─┘
                                  │                  │ nein
                                  └──────────────────┴→ Lieferant benachrichtigen → Ende
```

| Element | Multi-Instance | Was man in der Historie sieht |
|---|---|---|
| Position bewerten | parallel, Script Task | eine Zeile «4 Ausführungen», alle abgeschlossen |
| Position freigeben | parallel, User Task, Abbruchbedingung `entscheid = "abgelehnt"` | nach einer Ablehnung: abgeschlossene und abgebrochene Ausführungen; nach dem Rücksprung eine zweite, eigene Zeile |
| Lieferant benachrichtigen | sequenziell, Service Task, Job-Typ `mi-beispiel-lieferant` | eine Runde nach der anderen; ein Incident auf einer Runde bleibt am eingeklappten Eintrag sichtbar |

Startvariablen: `positionen` (vier Positionen vorbelegt) und `szenario` (freier Text,
um mehrere Testinstanzen in der Instanzliste auseinanderzuhalten).

Für «Lieferant benachrichtigen» gibt es absichtlich keinen Worker. Den Job schliesst
du in Operations von Hand ab, oder du lässt ihn fehlschlagen, um einen Incident zu
erzeugen. Über MCP geht das mit `atlas_instance_jobs`, `atlas_complete_job` und
`atlas_fail_job` (mit `retries: 0`).

### Drei Testszenarien

1. **Alles abgeschlossen:** alle vier Freigaben mit `entscheid: "freigegeben"`, danach
   die vier Lieferanten-Jobs nacheinander abschliessen.
2. **Ablehnung und zweite Runde:** eine Position freigeben, eine zweite ablehnen. Die
   zwei offenen Freigaben werden abgebrochen (`entscheide` ist dann
   `["freigegeben", "abgelehnt", null, null]`), «Bestellung überarbeiten» erscheint.
   Nach deren Abschluss läuft «Position freigeben» ein zweites Mal.
3. **Incident:** alle Positionen freigeben, zwei Lieferanten-Jobs abschliessen, den
   dritten mit `retries: 0` fehlschlagen lassen.

## MI-Beispiel: CSV-Datensätze prüfen

```
Start → CSV hochladen → CSV einlesen → Datensatz prüfen (je Zeile) → Entscheide zählen → Ende
```

Die Aufgabe «CSV hochladen» nimmt eine Datei entgegen; die Tasks-App legt ihren
Inhalt als `csvText` ab. «CSV einlesen» (`atlas:csvConnector`, `delimiter=";"`,
`hasHeader="true"`) schreibt `rows` und `rowCount`. Ohne `columns` wird jede Spalte
der Kopfzeile zu einem Textfeld. «Datensatz prüfen» läuft parallel über `rows` und
sammelt `{personalnummer, name, entscheid, bemerkung}` in `pruefungen`;
«Entscheide zählen» schreibt
`zusammenfassung = {total, ok, korrektur, abgelehnt}`.

Erwartete Kopfzeile:

```
personalnummer;vorname;nachname;email;abteilung;rolle
```

In [`beispiel-datensaetze.csv`](beispiel-datensaetze.csv) haben P-1008
(`elias.moser@example`) und P-1011 (leere E-Mail) absichtlich fehlerhafte Angaben.

## Fallen, die das Beispiel zeigt

- **Eckige Klammern sind in FEEL ein Filter.** `= bewertungen[loopCounter].betrag`
  liefert eine leere Liste statt des Betrags der Runde: Ein Ausdruck in eckigen
  Klammern, der kein Zahlenliteral ist, wird gegen jedes Listenelement ausgewertet.
  Das Modell rechnet den Betrag deshalb in der Runde aus `pos` aus.
- **Abgebrochene Runden hinterlassen `null`** in der Output-Collection. Das Gateway
  nach «Position freigeben» prüft mit `some e in entscheide satisfies e = "abgelehnt"`;
  ein Vergleich mit `null` ist nie wahr.
- **Trennzeichen.** Eine Datei mit Komma wird nicht abgelehnt, sondern als eine
  einzige Spalte gelesen. Alle Formularfelder bleiben dann leer.

## Verifiziert

Gegen `atlas.blumer.cloud` (Revision `ae3f528`) über MCP durchgespielt: alle drei
Szenarien der Bestellpositionen, die CSV-Variante mit den 15 Datensätzen (15 Aufgaben,
`parentTokenId` je Runde gesetzt) und mit drei Datensätzen bis zum Ende
(`zusammenfassung = {total: 3, ok: 1, korrektur: 1, abgelehnt: 1}`).
