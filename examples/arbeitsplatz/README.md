# `arbeitsplatz` — ein interner Wizard

Das Gegenstück zur [Reisebuchung](../reisebuchung/): Dort führt ein
**öffentlicher** Wizard eine Besucherin ohne Konto von Maske zu Maske. Hier führt
ein **interner** Wizard
([ADR-0449](../../docs/adr/0449-a-wizard-is-a-subprocess-one-filler-walks-through.md))
eine angemeldete Person in der **Tasks-App** durch mehrere Masken, und welche
Maske nach der ersten kommt, hängt von ihrer Antwort ab. Diese Verzweigung steht
im Diagramm, nicht in einem Formular.

**Fachlich:** Die Personalabteilung meldet einen Eintritt. Die vorgesetzte Person
wählt danach die Ausstattung aus: Gerät, je nach Gerät die Details, Software, eine
Zusammenfassung. Die IT stellt den Arbeitsplatz anschliessend bereit.

## Ablauf

```
Start "Eintritt gemeldet"           (Startformular arbeitsplatz-start:
                                     neuePerson, eintrittsdatum, vorgesetzter)
 → Subprozess "Ausstattung wählen"  (atlas:wizard="internal" — EINE Sitzung)
      User-Task "Gerät"                 (assignee = vorgesetzter)
      Exklusiv-Gateway "Welches Gerät?"
           geraet = "Laptop"   → User-Task "Laptop"   (Modell, Docking-Station)
           geraet = "Desktop"  → User-Task "Desktop"  (Bildschirme, Standort)
           sonst (kein Gerät)  → direkt weiter
      User-Task "Software"
      Script "Kosten schätzen"           (kosten aus Gerät, Modell, Bildschirmen)
      User-Task "Zusammenfassung"        (zeigt Name, Eintritt und Kosten nur an)
   Timer am Rand: 7 Tage ohne Abschluss → Ende "Nicht ausgewählt"
 → User-Task "Arbeitsplatz bereitstellen" (Gruppe it)
 → Ende "Arbeitsplatz bereit"
```

## Wer wird geführt?

Ein interner Wizard führt die angemeldete Person, die eine Maske abschickt, zur
nächsten Maske derselben Sitzung: Die Tasks-App öffnet sie sofort, und atlas
übernimmt sie für diese Person, so dass sie aus allen anderen Posteingängen
verschwindet. Wie eine Person in die Sitzung **hineinkommt**, hängt an der Rolle:

- **Starten dürfen nur Operatoren.** Die Ansicht *Start* der Tasks-App und der
  Aufruf `POST /api/v1/processes/{key}/instances` verlangen die Rolle
  `operator`. Wer mit der Rolle `user` arbeitet, also die meisten Menschen,
  startet keine Prozesse, sondern erledigt Aufgaben.
- **Deshalb ist die erste Maske zugewiesen.** `assignee="=vorgesetzter"` legt
  die erste Maske in den Posteingang der vorgesetzten Person. Die
  Personalabteilung, die startet, wird nicht in die Sitzung geführt: Die Antwort
  auf ihren Start lautet `handedOver`, und sie sieht von der Maske nichts.
- **Ab der ersten Maske wird geführt.** Die vorgesetzte Person öffnet die Maske
  im Posteingang. Sobald sie sie abschickt, öffnet die Tasks-App die nächste
  Maske, zeigt sie als nächsten Schritt der Sitzung und hält sie für diese
  Person fest.
- **Ohne Zuweisung** würde die Person geführt, die startet: Ein Operator, der
  einen Prozess mit nicht zugewiesener erster Maske startet, landet direkt auf
  dieser Maske.

## Was das Beispiel sonst zeigt

- **Die Verzweigung im Diagramm.** Das Exklusiv-Gateway nach der ersten Maske
  entscheidet, ob als Nächstes die Laptop-, die Desktop- oder gleich die
  Software-Maske kommt. Wer das Diagramm liest, sieht die Abfolge, ohne ein
  Formular zu öffnen.
- **Rechnen zwischen zwei Masken.** Das Script schätzt die Gerätekosten. Die
  Zusammenfassung zeigt sie in einem *deaktivierten* Feld, zusammen mit Name und
  Eintrittsdatum aus der Meldung der Personalabteilung: vorbelegt, aber nicht zum
  Ändern gedacht.
- **Die Erklärung steht im Modell.** Jede Maske trägt eine Dokumentation, zum
  Beispiel „Dieser Schritt erscheint, weil du einen Laptop gewählt hast."
- **Die Antworten bleiben im Wizard.** Was die IT nach der Sitzung braucht, geben
  die **Output-Mappings** des Subprozesses nach aussen: Gerät, Details,
  Software, Bemerkung und die Kostenschätzung. Eine Maske, die niemand gesehen
  hat, liefert dort `null`; der Haken `bestaetigt` der Zusammenfassung ist nicht
  gemappt und bleibt im Wizard.
- **Die Bereitstellung liegt ausserhalb.** Sie ist Arbeit einer anderen Gruppe.
  Die Sitzung endet mit der Zusammenfassung; die vorgesetzte Person erfährt, dass
  ihr Teil fertig ist (`done`), und die Bereitstellung erscheint im Posteingang
  der Gruppe `it`.
- **Abbruch.** Der unterbrechende Timer beendet eine Sitzung, die sieben Tage
  lang nicht abgeschlossen wurde. Bei einem internen Wizard ist das nur eine
  Warnung des Problems-Panels; das Beispiel setzt ihn trotzdem, weil eine
  liegengelassene Auswahl sonst für immer offen bliebe.

## Die Dateien

| Datei | Rolle |
|-------|-------|
| [`arbeitsplatz.bpmn`](arbeitsplatz.bpmn) | der Prozess: Startformular, interner Wizard, Bereitstellung (mit handgezeichnetem Layout) |
| [`arbeitsplatz-start.form.json`](arbeitsplatz-start.form.json) | Startformular der Personalabteilung |
| [`arbeitsplatz-geraet.form.json`](arbeitsplatz-geraet.form.json) | erste Maske: Gerät und Arbeitsort |
| [`arbeitsplatz-laptop.form.json`](arbeitsplatz-laptop.form.json) | nur bei einem Laptop |
| [`arbeitsplatz-desktop.form.json`](arbeitsplatz-desktop.form.json) | nur bei einem Desktop |
| [`arbeitsplatz-software.form.json`](arbeitsplatz-software.form.json) | Software-Pakete und Bemerkung |
| [`arbeitsplatz-abschluss.form.json`](arbeitsplatz-abschluss.form.json) | Zusammenfassung mit deaktivierten Feldern |
| [`arbeitsplatz-bereitstellen.form.json`](arbeitsplatz-bereitstellen.form.json) | Aufgabe der IT nach der Sitzung |

## Selbst ausführen

1. Das Beispiel installieren (Handbuch, Kapitel *Beispiele*, Karte
   *Arbeitsplatz-Ausstattung*) oder die sieben Formulare und das BPMN von Hand
   ablegen und deployen.
2. Ein Konto mit der Rolle `user` für die vorgesetzte Person anlegen und eine
   Gruppe `it` mit mindestens einem Mitglied.
3. Als Operator in der Tasks-App unter *Start* den Prozess
   *Arbeitsplatz-Ausstattung* wählen und den Eintritt melden; als vorgesetzte
   Person den Benutzernamen des Kontos aus Schritt 2 eintragen.
4. Als vorgesetzte Person anmelden, die Maske *Gerät* im Posteingang öffnen und
   abschicken. Die Tasks-App führt von da an durch die übrigen Masken.
5. Als Mitglied der Gruppe `it` die Aufgabe *Arbeitsplatz bereitstellen*
   erledigen.

## Drei Durchläufe zum Nachvollziehen

Alle drei laufen als Test gegen genau diese Dateien, angemeldet als gewöhnliche
Konten: `TestTheWorkplaceExample…` in
[`api/arbeitsplatz_example_test.go`](../../api/arbeitsplatz_example_test.go).

| Gerät | Masken nach „Gerät" | `kosten` |
|-------|---------------------|---------:|
| Laptop, Modell „Leistung" | Laptop → Software → Zusammenfassung | 2400 |
| Desktop, 2 Bildschirme | Desktop → Software → Zusammenfassung | 1600 |
| kein Gerät | Software → Zusammenfassung | 0 |

In jedem Durchlauf wird der Start der Personalabteilung als `handedOver`
beantwortet, die vorgesetzte Person ab der ersten Maske geführt, und die
Bereitstellung erscheint nur im Posteingang der Gruppe `it`.

## Grenze: Personendaten in einem Wizard

Ein naheliegendes internes Beispiel wäre ein Personalbogen, den die neue Person
selbst ausfüllt: Adresse, Geburtsdatum, Bankverbindung. Solche Felder sind
Personendaten und werden mit `atlas:personal` verschlüsselt gespeichert
([ADR-0314](../../docs/adr/0314-portal-personal-data.md)). Ein Output-Mapping
darf aber keine Personendaten schreiben, weil die Engine sie nicht verschlüsseln
kann. Personendaten, die in einem Wizard erfasst werden, lassen sich deshalb
heute nicht über die Output-Mappings nach aussen geben. Dieses Beispiel kommt
ohne Personendaten aus; wer sie braucht, muss sie innerhalb des Wizards
weiterverarbeiten oder auf eine Lösung dieser Grenze warten.
