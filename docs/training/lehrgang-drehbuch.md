# Leitfall-Drehbuch — Mitarbeitereintritt (Moderationsfassung)

Moderations- und Ablaufhilfe, die den installierbaren Leitfall
([`examples/lehrgang/`](../../examples/lehrgang/)) in eine tatsächlich
durchführbare Lehrgangssitzung übersetzt: ein Zeitfenster je Modul, was die
Teilnehmenden tun, und woran der Erfolg abgelesen wird. Das Drehbuch erklärt die
Mechanik nicht — dafür verweist jedes Modul auf sein Referenzkapitel im Handbuch.
Es ordnet die Mechanik zu einer Sitzung.

**Zwei Fassungen.** Diese Datei ist die **Moderationsfassung**: Sie trägt zu jeder
Vorhersagefrage die erwartete Antwort. Das **Teilnehmenden-Arbeitsblatt**
([lehrgang-arbeitsblatt.md](lehrgang-arbeitsblatt.md)) trägt dieselben Fragen ohne
Antworten und wird ausgegeben. Die erwarteten Antworten stehen nur hier und werden
erst gezeigt, nachdem das Tandem vorhergesagt hat — sonst ist die Leitlinie
«Vorhersage vor Ausführung» entwertet.

Zugehörige Dokumente: der [Lehrgangsplan](lehrgang-prozessentwicklung.md), die
[Beobachtungssitzung](lehrgang-beobachtungssitzung.md), die
[README des Leitfalls](../../examples/lehrgang/README.md) und der modulweise
Durchgang im Handbuch unter
[Leitfall: ein Prozess durch alle Module](/handbuch.html#leitfall).

**Stand:** 2026-09-30 · **Basis:** `main` nach Release 0.8.0 · **Leitfall:** `examples/lehrgang/`

---

## 1. Zweck und Einordnung

Der Leitfall liegt fertig vor und installiert sich mit einem Knopf. Das
Handbuch-Kapitel `#leitfall` beschreibt je Modul den Startpunkt und das Szenario.
Was beiden fehlt, ist die Sicht der Moderation: wie lange ein Modul dauert, was in
dieser Zeit tatsächlich geschieht, welcher Ausgangszustand vorher hergestellt sein
muss und woran man am Ende sieht, dass das Ziel erreicht ist. Diese Lücke schliesst
das Drehbuch.

Es deckt die **sechs Referenzmodule des Leitfalls** ab — Modellieren, Testen,
Ausliefern, Weiterentwickeln, eigener Worker, Überwachen — in derselben Nummerierung
wie das Handbuch-Kapitel. Es ist das Instrument, an dem der Pilot (Phase 5 des Plans)
die Zeit je Modul und die Zahl der rot bleibenden Szenarien misst.

Bewusst nicht Gegenstand des Drehbuchs:

- die Module M0 (Orientierung) und M1 (Aufnehmen und Schneiden) des Plans — der
  Leitfall setzt einen bereits geschnittenen Fall voraus;
- die noch offenen Entscheide, an denen einzelne Module später schärfer werden
  können: der ADR-Entwurf zum Referenzweg in die Produktion (Phase 2, betrifft
  Ausliefern), ADR-0424 zur Mindesttrefferzahl je Playground-Regel (betrifft Testen)
  und die Form der Lehrgangsseite (Phase 4).

## 2. Rollen und Grundhaltung

- **Moderation:** führt ein, hält die Zeit, gibt bei Bedarf einen minimalen
  Rettungshinweis (ein Kapitel oder einen Menüpunkt, keine Lösung). Sie hat den
  Leitfall vorher einmal selbst durchgespielt (Abschnitt 7) und hält die erwarteten
  Antworten zurück, bis das Tandem vorhergesagt hat.
- **Teilnehmende:** ein Tandem aus Fach und Entwicklung, wie im Plan vorgesehen. Das
  Tandem arbeitet am [Arbeitsblatt](lehrgang-arbeitsblatt.md) und sagt laut, was es
  gerade versucht und was es erwartet.

Zwei didaktische Leitlinien des Plans tragen jedes Modul:

1. **Vorhersage vor Ausführung.** Jedes Modul öffnet mit einer Vorhersagefrage. Das
   Tandem beantwortet sie und begründet sie, bevor es etwas ausprobiert. Wer richtig
   vorhersagt und begründet, darf die zugehörige Hands-on-Übung überspringen — die
   Vorhersage war der Lernpunkt, nicht das Tippen.
2. **Das Produkt gibt die Rückmeldung.** Jedes Modul schliesst mit einem Nachweis,
   den Atlas selbst zeigt — eine Instanz, ein grünes oder rotes Szenario, ein Release,
   ein Incident —, nicht mit dem Urteil der Moderation.

## 3. Rahmen und Voraussetzungen

- **Umgebung:** eine frische Atlas-Instanz je Tandem mit eigenem Datenverzeichnis,
  angemeldet mit der Rolle `admin` oder lokal mit `--auth=false`. Die Rolle `admin`
  ist nötig, weil die Instanzmigration und das Überschreiben von Instanzvariablen sie
  verlangen (nicht `operator`); die beiden Human-Tasks laufen über die
  Kandidatengruppen `it` und `hr`, die am Übungskonto vorhanden sein müssen (oder eben
  `--auth=false`).
- **Gemeinsamer Startpunkt:** der über die
  [Beispiel-Karte](/handbuch.html#bsp-lehrgang) installierte Leitfall. Der Knopf legt
  Applikation, Formulare, Entscheidung und Prozess an und publiziert sie.
- **Zweites Ziel:** Modul 3 (Ausliefern) promotet ein Release auf ein zweites
  Deployment-Ziel. Die Umgebung stellt dieses Ziel und einen Deploy-Token bereit;
  ohne zweites Ziel wird das Modul zur Trockenübung an einem Ziel.
- **Eigener Worker:** die Module 5 und 6 brauchen einen lauffähigen Worker für den
  Job-Typ `konto-anlegen`. Vorlage sprachneutral (HTTP) und als Go in der
  [README](../../examples/lehrgang/README.md).
- **Handbuch:** unter `/handbuch.html` erreichbar; die Referenzkapitel sind der
  Lesestoff je Modul.

**Als Annahmen gekennzeichnet:** Die Zeitangaben in Abschnitt 4 sind Richtwerte, nicht
gemessene Werte — der Plan hält alle Zeitangaben ausdrücklich als Schätzung fest, und
der Pilot ersetzt sie durch Ist-Zeiten. Die Verfügbarkeit eines zweiten Ziels für
Modul 3 ist eine Annahme über die Übungsumgebung, keine Eigenschaft des Leitfalls.

## 4. Zeitplan (Richtwerte)

Ein Durchgang ist der Leitfaden-Rücken, nicht der ganze Lehrgang. Die Zeiten sind
Richtwerte je Modul, jeweils inklusive Lesen des Referenzkapitels, Vorhersage,
Hands-on und Nachweis.

| Modul | Zeitfenster | Schwerpunkt |
|---|---|---|
| 1 · Modellieren | 90–120 min | Das Modell nachbauen; DMN statt Gateway-Gestrüpp |
| 2 · Testen | 75–90 min | Szenario über den Rollen-Datensatz; einmal absichtlich rot |
| 3 · Ausliefern | 60–75 min | Publizieren, Release, Promotion auf ein zweites Ziel |
| 4 · Weiterentwickeln | 75–90 min | Eine Rolle ergänzen, Version 2, laufende Instanz migrieren |
| 5 · Eigener Worker | 90–120 min | Den geparkten Job bedienen; Idempotenz über den Job-Key |
| 6 · Überwachen | 75–90 min | Fehlschlag → Incident; lesen, zählen, auflösen |

Summe rund neun bis zehn Stunden. Ob als sechs Blöcke an verschiedenen Tagen oder als
zusammenhängende Intensivform, entscheidet der organisatorische Rahmen; das Drehbuch
setzt nur die Reihenfolge voraus, weil jedes Modul auf dem Zustand des vorigen aufbaut.

## 5. Ablauf je Modul

Jedes Modul folgt derselben Gliederung: **Startzustand** (was vorher hergestellt sein
muss), **Vorhersagefrage** (steht so auch auf dem Arbeitsblatt), **Erwartete Antwort**
(nur hier), **Was die Teilnehmenden tun**, **Erfolgsmass** und **Rettungshinweis**.

### Modul 1 · Modellieren

- **Startzustand:** ein leeres Diagramm im Modeler. Der installierte Leitfall dient als
  Musterlösung, in die erst am Ende geschaut wird.
- **Vorhersagefrage:** Welche Ausstattung bekommt die Rolle `entwicklung`, und was
  geschieht mit einer Rolle, die in der Ausstattungsregel gar nicht steht?
- **Erwartete Antwort:** `entwicklung` bekommt das MacBook Pro 16 mit den Zugriffen aus
  der Regelzeile; eine Rolle, die keine Zeile trifft, läuft in den Auffangfall (`-`) und
  bekommt die Grundausstattung (`basis`) — die Entscheidung liefert nie nichts.
- **Was die Teilnehmenden tun:** das Modell nachbauen — das Start-Formular als
  Datenvertrag, die Entscheidung `lg-ausstattung` als Business-Rule-Task, den Job-Task
  `konto-anlegen`, zwei Human-Tasks mit den Gruppen `it` und `hr` und den nicht
  unterbrechenden Boundary-Timer. Die Leitfrage der Moderation: warum eine DMN und kein
  Verzweigungsgestrüpp — weil sich die Ausstattungsregel häufiger ändert als der Ablauf.
- **Erfolgsmass:** Der Entwurf validiert, und eine einzelne Auswertung der Entscheidung
  (etwa im Playground oder über die Entscheidungs-API) liefert für `entwicklung` das
  MacBook Pro 16 und für eine unbekannte Rolle den Auffangfall — genau wie vorhergesagt.
- **Rettungshinweis:** Kapitel [Modellieren & Design](/handbuch.html#designen).

### Modul 2 · Testen

- **Startzustand:** das Modell im Playground (Wegwerf-Sandbox, kein Deploy nötig).
- **Vorhersagefrage:** Eine Regel, deren `when` durch einen Tippfehler in der Rolle
  keinen einzigen Fall trifft — schlägt das Szenario Alarm, oder besteht es?
- **Erwartete Antwort:** Es **besteht**. Eine Regel ohne Treffer gilt als erfüllt
  (ADR-0215); das Urteil vermerkt nur «no case matched», und `atlas playground` endet mit
  Exit-Code 0. Deshalb prüft man jede Regel einmal, indem man sie absichtlich rot macht.
- **Was die Teilnehmenden tun:** einen Datensatz über `rolle` anlegen (entwicklung,
  vertrieb, produktion, verwaltung und eine unbekannte Rolle); Stubs beantworten den
  Job und die Aufgaben, damit jeder Fall zu Ende läuft. Eine Regel je Fall, etwa
  `when rolle = "vertrieb" then ausstattung.laptop = "ThinkPad X1"`, mit den Erwartungen
  *no incidents* und *every case finishes*. Danach den Auffangfall einmal absichtlich
  **rot** sehen: die Regel falsch schreiben und prüfen, dass sie anschlägt.
- **Erfolgsmass:** ein grüner Lauf über alle fünf Fälle; ein bewusst herbeigeführter
  roter Lauf, der wieder grün gemacht wird.
- **Rettungshinweis:** Kapitel [Testen als Code](/handbuch.html#szenarien).

### Modul 3 · Ausliefern

- **Startzustand:** die in Modul 2 geprüfte Applikation, publiziert; ein zweites
  Deployment-Ziel und ein Deploy-Token stehen bereit (Abschnitt 3).
- **Vorhersagefrage:** Nach dem Promoten auf das zweite Ziel — woran erkennt das Ziel,
  welche Applikation es aktualisiert, und trägt es danach dieselbe Version?
- **Erwartete Antwort:** Die Zuordnung geschieht über den **Namen** der Applikation, nicht
  über eine Kennung des ersten Ziels; nach dem Promoten trägt das zweite Ziel dasselbe
  Release.
- **Was die Teilnehmenden tun:** die Applikation `lehrgang` publizieren, ein Release
  festhalten und es mit dem Deploy-Token auf das zweite Ziel (Abnahme) promoten.
- **Erfolgsmass:** das Release liegt auf dem zweiten Ziel, über den Namen zugeordnet.
- **Rettungshinweis:** Kapitel [Ausliefern](/handbuch.html#ausliefern).

### Modul 4 · Weiterentwickeln

- **Startzustand:** eine laufende Instanz auf Version 1 (eine mit beliebiger Rolle
  gestartete Instanz, die noch nicht abgeschlossen ist).
- **Vorhersagefrage:** Es läuft eine Instanz auf Version 1. Man deployt Version 2 mit
  einer neuen Rolle. Was geschieht mit der laufenden Instanz?
- **Erwartete Antwort:** Sie **bleibt auf Version 1**, bis man sie migriert. Ein neues
  Deploy ändert keinen laufenden Fall; erst die Migration hebt die Instanz auf Version 2.
- **Was die Teilnehmenden tun:** eine neue Rolle einführen — eine Zeile in
  `ausstattung.dmn` —, als Version 2 deployen und die laufende Instanz migrieren.
- **Erfolgsmass:** Die laufende Instanz steht vor der Migration auf Version 1 und danach
  auf Version 2 — das widerlegt die Annahme «ein neues Deploy ändert laufende Fälle».
- **Rettungshinweis:** Kapitel [Weiterentwickeln](/handbuch.html#weiterentwickeln).

### Modul 5 · Eigener Worker

- **Startzustand:** eine Instanz, deren Job `konto-anlegen` **geparkt** wartet — eine
  frisch mit `rolle: "entwicklung"` gestartete Instanz erreicht diesen Zustand von
  selbst, weil kein Worker den Job bedient.
- **Vorhersagefrage:** Der Job wird nach einem Absturz ein zweites Mal zugestellt. Was
  geschieht, wenn der Worker das Konto beim zweiten Mal einfach noch einmal anlegt?
- **Erwartete Antwort:** Die Zustellung ist mindestens einmal. Ohne idempotente
  Behandlung legt der zweite Versuch ein zweites Konto an oder scheitert, weil das Konto
  schon existiert — daraus wird ein Incident. Idempotenz liegt beim Worker (Job-Key oder
  `personalnummer`), nicht bei der Engine.
- **Was die Teilnehmenden tun:** einen Worker schreiben (HTTP oder Go aus der README),
  der den Job aktiviert, das Konto anlegt und ihn abschliesst — idempotent über den
  Job-Key oder die `personalnummer`.
- **Erfolgsmass:** Der geparkte Job wird abgeschlossen (`kontoId` gesetzt), die Instanz
  läuft über die Mockups bis zur Aufgabe «Arbeitsplatz vorbereiten» (Gruppe `it`) weiter.
- **Rettungshinweis:** Kapitel [Eigener Worker](/handbuch.html#eigener-worker) und der
  Worker-Code in der [README](../../examples/lehrgang/README.md).

### Modul 6 · Überwachen

- **Startzustand:** derselbe Worker wie in Modul 5, nun so betrieben, dass er scheitert.
- **Vorhersagefrage:** Das Zielsystem antwortet seit einer Stunde nicht. Operations
  zeigt keine Incidents. Ist alles in Ordnung?
- **Erwartete Antwort:** Nein. Atlas hält die Arbeit für ein nicht erreichbares Ziel
  zurück, ohne Retries zu verbrauchen (Operations › Workers, Karte «Held back»). Das ist
  kein Incident — «keine Incidents» heisst deshalb nicht «alles in Ordnung».
- **Was die Teilnehmenden tun:** den Job scheitern lassen — ein `fail` mit `retries: 1`
  parkt ihn neu, ein `fail` mit `retries: 0` parkt einen **Incident** auf dem Element
  `konto`. Den Incident über `GET /api/v1/incidents` lesen, in `/metrics` an
  `atlas_open_incidents` zählen und auflösen.
- **Erfolgsmass:** Der Incident ist sichtbar und wird aufgelöst; die Metrik steigt und
  fällt wieder.
- **Rettungshinweis:** Kapitel [Überwachen](/handbuch.html#ueberwachen).

## 6. Erfolgsmessung über die Module

Je Modul zählt der Nachweis aus Abschnitt 5 — er ist bestanden oder nicht, und Atlas
zeigt ihn. Über die Sitzung hinweg notiert die Moderation drei Grössen, die in die
Pilot-Auswertung (Phase 5) einfliessen:

- **Zeit je Modul** (Ist gegen die Richtwerte aus Abschnitt 4);
- **richtige Vorhersagen** je Tandem (0 bis 6) — das Mass für das Verständnis der
  Ausführungssemantik, das der Lehrgang vermitteln will;
- **Module, die rot bleiben**, mit der wörtlichen Stelle, an der es hakte (Kandidat für
  eine Lücke im Handbuch oder im Leitfall).

Die Vorhersagefragen der Module 4, 5 und 6 entsprechen bewusst den drei
Semantik-Fragen der Beobachtungssitzung (laufender Fall gegen neues Deploy, doppelte
Zustellung, zurückgehaltene Arbeit gegen Incident). So lässt sich der Lerneffekt vorher
und nachher an derselben Frage ablesen.

## 7. Vorbereitung

- [ ] **Probelauf:** Die Moderation spielt den Leitfall vorher einmal vollständig durch
      (Modul 1 bis 6). Stösst sie dabei auf eine Produktlücke, wird das Modul angepasst,
      bevor es eine Messung verfälscht.
- [ ] Je Tandem eine frische Atlas-Instanz mit eigenem Datenverzeichnis, Rolle `admin`
      oder lokal `--auth=false`.
- [ ] Kandidatengruppen `it` und `hr` am Übungskonto vorhanden (oder `--auth=false`).
- [ ] Der Leitfall über die [Beispiel-Karte](/handbuch.html#bsp-lehrgang) installiert
      und einmal gestartet.
- [ ] Für Modul 3: ein zweites Deployment-Ziel und ein Deploy-Token bereit.
- [ ] Für Modul 5 und 6: ein lauffähiger Worker für `konto-anlegen` (HTTP oder Go aus
      der README).
- [ ] Handbuch der Instanz erreichbar (`/handbuch.html`); je Modul das Referenzkapitel
      als Lesestoff.
- [ ] Das [Arbeitsblatt](lehrgang-arbeitsblatt.md) je Tandem bereit; diese
      Moderationsfassung bleibt bei der Moderation.
- [ ] Zeitfenster als Richtwerte kommuniziert; Ist-Zeiten je Modul werden notiert.

## 8. Offene Punkte

- Die Zeitangaben sind Schätzungen und werden im Pilot durch Ist-Zeiten ersetzt.
- Modul 2 kann sich schärfen, sobald ADR-0424 (Mindesttrefferzahl je Regel) entschieden
  ist; heute ist «jede Regel einmal rot sehen» der Behelf für den Fall ohne Treffer.
- Modul 3 setzt ein zweites Ziel in der Übungsumgebung voraus; der Referenzweg in die
  Produktion (Phase 2) kann diesen Teil später präzisieren.
- Ob Drehbuch und Arbeitsblatt später aus dem Handbuch verlinkt oder als internes
  Sitzungsmaterial geführt werden, hängt an der Entscheidung zur Lehrgangsseite (Phase 4).
