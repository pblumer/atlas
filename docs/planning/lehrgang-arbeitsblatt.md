# Leitfall — Teilnehmenden-Arbeitsblatt

Arbeitsblatt für das Tandem zum Leitfall «Mitarbeitereintritt». Jedes Modul beginnt
mit einer **Vorhersage** und endet mit einem Nachweis, den Atlas selbst zeigt.

**So wird gearbeitet:**

1. Die Vorhersagefrage lesen und die Vorhersage **notieren, bevor** etwas ausprobiert
   wird — mit kurzer Begründung. Wer richtig vorhersagt und begründet, darf die
   Hands-on-Übung überspringen.
2. Erst dann handeln. Den Nachweis holt das Produkt, nicht die Meinung.
3. Bei einem Hindernis von mehr als etwa 15 Minuten die Moderation um einen Hinweis
   bitten; das jeweilige Referenzkapitel steht unter «Nachschlagen».

Die Antworten stehen nicht auf diesem Blatt — das ist Absicht. Das Referenzkapitel und
Atlas selbst liefern sie.

**Leitfall installieren:** über die Beispiel-Karte im Handbuch unter
[Beispiele → Mitarbeitereintritt](/handbuch.html#bsp-lehrgang).

---

## Modul 1 · Modellieren

- **Startzustand:** ein leeres Diagramm im Modeler.
- **Vorhersagefrage:** Welche Ausstattung bekommt die Rolle `entwicklung`, und was
  geschieht mit einer Rolle, die in der Ausstattungsregel gar nicht steht?

  _Vorhersage (vor dem Ausprobieren): _____________________________________________

- **Aufgabe:** das Modell nachbauen — das Start-Formular als Datenvertrag, die
  Entscheidung `lg-ausstattung` als Business-Rule-Task, den Job-Task `konto-anlegen`,
  zwei Human-Tasks mit den Gruppen `it` und `hr` und den nicht unterbrechenden
  Boundary-Timer.
- **Nachweis:** die Entscheidung für `entwicklung` und für eine unbekannte Rolle
  auswerten (Playground oder Entscheidungs-API) und mit der Vorhersage vergleichen.
- **Nachschlagen:** [Modellieren & Design](/handbuch.html#designen).

## Modul 2 · Testen

- **Startzustand:** das Modell im Playground.
- **Vorhersagefrage:** Eine Regel, deren `when` durch einen Tippfehler in der Rolle
  keinen einzigen Fall trifft — schlägt das Szenario Alarm, oder besteht es?

  _Vorhersage: _____________________________________________________________________

- **Aufgabe:** einen Datensatz über `rolle` anlegen (entwicklung, vertrieb, produktion,
  verwaltung und eine unbekannte Rolle); Stubs beantworten Job und Aufgaben, damit jeder
  Fall zu Ende läuft. Eine Regel je Fall, etwa
  `when rolle = "vertrieb" then ausstattung.laptop = "ThinkPad X1"`, mit den Erwartungen
  *no incidents* und *every case finishes*.
- **Nachweis:** den Lauf über alle fünf Fälle grün sehen; dann die Regel des
  Auffangfalls absichtlich falsch schreiben und beobachten, ob der Lauf rot wird.
- **Nachschlagen:** [Testen als Code](/handbuch.html#szenarien).

## Modul 3 · Ausliefern

- **Startzustand:** die geprüfte Applikation, publiziert; ein zweites Deployment-Ziel
  und ein Deploy-Token stehen bereit (fragt die Moderation).
- **Vorhersagefrage:** Nach dem Promoten auf das zweite Ziel — woran erkennt das Ziel,
  welche Applikation es aktualisiert, und trägt es danach dieselbe Version?

  _Vorhersage: _____________________________________________________________________

- **Aufgabe:** die Applikation `lehrgang` publizieren, ein Release festhalten und es mit
  dem Deploy-Token auf das zweite Ziel (Abnahme) promoten.
- **Nachweis:** feststellen, woran das zweite Ziel die Applikation zuordnet und welches
  Release es danach trägt.
- **Nachschlagen:** [Ausliefern](/handbuch.html#ausliefern).

## Modul 4 · Weiterentwickeln

- **Startzustand:** eine laufende Instanz auf Version 1.
- **Vorhersagefrage:** Es läuft eine Instanz auf Version 1. Man deployt Version 2 mit
  einer neuen Rolle. Was geschieht mit der laufenden Instanz?

  _Vorhersage: _____________________________________________________________________

- **Aufgabe:** eine neue Rolle einführen — eine Zeile in `ausstattung.dmn` —, als
  Version 2 deployen und die laufende Instanz migrieren.
- **Nachweis:** feststellen, welche Version die laufende Instanz trägt — vor und nach
  der Migration.
- **Nachschlagen:** [Weiterentwickeln](/handbuch.html#weiterentwickeln).

## Modul 5 · Eigener Worker

- **Startzustand:** eine Instanz, deren Job `konto-anlegen` geparkt wartet (eine frisch
  mit `rolle: "entwicklung"` gestartete Instanz erreicht das von selbst).
- **Vorhersagefrage:** Der Job wird nach einem Absturz ein zweites Mal zugestellt. Was
  geschieht, wenn der Worker das Konto beim zweiten Mal einfach noch einmal anlegt?

  _Vorhersage: _____________________________________________________________________

- **Aufgabe:** einen Worker schreiben (HTTP oder Go aus der
  [README](../../examples/lehrgang/README.md)), der den Job aktiviert, das Konto anlegt
  und ihn abschliesst — idempotent über den Job-Key oder die `personalnummer`.
- **Nachweis:** den geparkten Job abschliessen (`kontoId` gesetzt) und beobachten, wie
  weit die Instanz läuft; begründen, was eine zweite Zustellung ohne Idempotenz auslöste.
- **Nachschlagen:** [Eigener Worker](/handbuch.html#eigener-worker).

## Modul 6 · Überwachen

- **Startzustand:** derselbe Worker wie in Modul 5, so betrieben, dass er scheitert.
- **Vorhersagefrage:** Das Zielsystem antwortet seit einer Stunde nicht. Operations
  zeigt keine Incidents. Ist alles in Ordnung?

  _Vorhersage: _____________________________________________________________________

- **Aufgabe:** den Job scheitern lassen — ein `fail` mit `retries: 1` parkt ihn neu, ein
  `fail` mit `retries: 0` parkt einen Incident auf dem Element `konto`.
- **Nachweis:** feststellen, ob ein bloss nicht erreichbares Ziel schon einen Incident
  erzeugt; den Incident über `GET /api/v1/incidents` lesen, in `/metrics` an
  `atlas_open_incidents` zählen und auflösen.
- **Nachschlagen:** [Überwachen](/handbuch.html#ueberwachen).

---

_Für die Moderation: Die Fassung mit den erwarteten Antworten und dem Zeitplan steht in
[lehrgang-drehbuch.md](lehrgang-drehbuch.md) und wird während der Vorhersage
zurückgehalten._
