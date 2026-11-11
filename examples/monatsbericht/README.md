# `monatsbericht` — Kennzahlen als Schnappschuss an einen Verteiler

Atlas liest seine eigenen Kennzahlen, die laufenden und abgeschlossenen Instanzen je
Prozess und die offenen Incidents, und stellt sie jeder Person im Verteiler als
**User-Task** zu. Das Formular zeigt die Zahlen als Diagramme
([ADR-0555](../../docs/adr/0555-report-tasks-with-charts.md)). **Gelesen heisst
bestätigt:** Wer die Aufgabe erledigt, hat den Bericht gelesen, und die erledigte Aufgabe
bleibt unter *Erledigt* einsehbar
([ADR-0554](../../docs/adr/0554-completed-tasks-stay-readable.md)).

Wer den Bericht nach drei Tagen nicht gelesen hat, erscheint bei der verantwortlichen
Person. Sie passt den Verteiler an, etwa die Stellvertretung statt der Person in den
Ferien, und stellt **denselben Bericht** erneut zu.

## Der Ablauf

```
Start "Bericht versenden"            (mb-verteiler: Verteiler, Verantwortlich)
 → Atlas-Bericht "Instanzen lesen"   instances-summary → bericht
 → Atlas-Bericht "Incidents lesen"   incident-summary  → incidents
 → Script "Verteiler lesen"          verteilerText → offen
 → Subprozess "Zustellen"            parallel, je Person in offen
      User-Task "Monatsbericht lesen"  (mb-bericht, assignee = empfaenger)
          erledigt   → quittung {empfaenger, gelesen: true}
          Frist P3D  → quittung {empfaenger, gelesen: false}
 → Script "Nicht Gelesene bestimmen" quittungen → offen
 → alle gelesen?  ja → Ende "Alle haben gelesen"
                  nein → User-Task "Verteiler anpassen" (mb-anpassen, assignee = verantwortlich)
                         → abschliessen?  ja → Ende "Zustellung abgeschlossen"
                                          nein → zurück zu "Verteiler lesen"
```

## Warum so

- **Ein Schnappschuss, nicht eine Abfrage beim Öffnen.** Der Bericht ist eine
  gewöhnliche Instanzvariable. Wer in der zweiten Runde liest, sieht dieselben Zahlen wie
  alle anderen, und eine erledigte Aufgabe zeigt auch nächstes Jahr, was gelesen wurde.
  Deshalb liest die zweite Runde die Kennzahlen nicht erneut.
- **`quittung` ist lokal je Zustellung.** Die Eingabezuordnung `=null` auf dem
  Subprozess legt die Variable in jeder Zustellung an. Ohne sie würden die parallelen
  Zustellungen dieselbe Variable des Prozesses überschreiben.
- **Die Frist unterbricht.** Nach drei Tagen wird die Aufgabe zurückgezogen. Ob jemand
  nachfasst und bei wem, entscheidet ein Mensch, nicht das Modell.
- **Der Verteiler ist Text.** Ein Benutzername pro Zeile oder durch Komma getrennt;
  Leerzeichen, leere Zeilen und Doppelte fallen weg. Eine Gruppe als Empfänger ist bewusst
  nicht vorgesehen: Bei einer Gruppe bestätigt die erste Person für alle.

## Was es braucht

Einen Worker `atlas` vom Typ **Atlas report** (Console › Workers): die Adresse des
Atlas-Servers und ein API-Token im Scope `full` mit der Rolle `operator` im Vault. Die
Schritte stehen im Handbuch unter *Atlas-Bericht* (`runbook-atlasreport`).

Kann der Worker eine Route nicht lesen, weil dem Token die Rolle fehlt, endet die Aufgabe
als Incident mit dieser Meldung. Wer das im Modell abfangen will, setzt ein
Fehler-Grenzereignis mit dem Code `ATLASREPORT_REFUSED`.

## Ausprobieren

1. Worker `atlas` anlegen, wie oben beschrieben; als Endpoint die Adresse dieses Servers.
2. Das Beispiel installieren und *Monatsbericht* in der Tasks-App starten: im Verteiler
   der eigene Benutzername, als verantwortliche Person ebenfalls.
3. Die Aufgabe *Monatsbericht lesen* öffnen, die Diagramme ansehen, drucken oder als PDF
   sichern, und erledigen.

`TestMonatsberichtNachfassen` (`examples/monatsbericht_test.go`) spielt den Ablauf mit
zwei Personen, einer abgelaufenen Frist und einem angepassten Verteiler durch.
