# `terminbuchung` — freie Termine aus dem Kalender, ohne Doppelbuchung

Eine Kundin ohne Konto bucht über einen **öffentlichen Link** einen Termin: Sie wählt
eine Dienstleistung, sieht die freien Termine, gibt ihre Angaben ein und bucht. Die
freien Termine kommen aus dem **Kalender der Werkstatt**, und der gebuchte Termin steht
danach dort — gelesen und geschrieben vom Kalender-Worker
([ADR-0552](../../docs/adr/0552-calendar-worker-type.md)). Die
Masken sind ein öffentlicher Wizard
([ADR-0449](../../docs/adr/0449-a-wizard-is-a-subprocess-one-filler-walks-through.md)),
wie in [`reisebuchung`](../reisebuchung/README.md).

## Der Ablauf

```
Start "Termin anfragen"            (Startformular termin-dienstleistung, öffentlicher Link)
 → Subprozess "Buchungsassistent"  (atlas:wizard="public" — EINE Sitzung)
      Script     "Dienstleistung nachschlagen"   leistung {name, dauer}
      Kalender   "Freie Termine suchen"          find-free-slots → freieTermine
      Script     "Termine aufbereiten"           terminOptionen [{label, value}]
      User-Task  "Termin wählen"                 Auswahl aus terminOptionen (valuesKey)
      Script     "Termin übernehmen"             slot: nur ein angebotener Termin
      Gateway    kein angebotener Termin → zurück zur Wahl
      User-Task  "Ihre Angaben"
      Kalender   "Termin reservieren"            reserve-slot → buchung
          Fehler CALENDAR_SLOT_TAKEN → "Hinweis: Termin vergeben" → neu suchen
      Kalender   "Buchung bestätigen"            confirm-booking: Betreff, Text, Einladung
      User-Task  "Bestätigung"
   Timer am Rand: 30 Minuten ohne Abschluss → "Sitzung abgelaufen"
 → User-Task "Termin vorbereiten"  (Werkstatt, Gruppe werkstatt)
```

## Worauf es ankommt

- **Frei ist, was der Kalender sagt.** `find-free-slots` liest die belegten Zeiten —
  nur Zeiten und Verfügbarkeit, nie Betreffzeilen fremder Termine — und legt das Raster
  der Dienstleistung über die Öffnungszeiten im Modell. Was jemand von Hand in Outlook
  einträgt, blockiert genauso wie eine frühere Buchung.
- **Die Auswahl ist ein Angebot, keine Zusage.** Zwischen der Wahl und der Buchung
  vergehen Minuten. Verbindlich ist erst `reserve-slot`: Es trägt einen Platzhalter ein
  und liest den Kalender danach. Ist der Termin inzwischen anderweitig belegt, entfernt
  es den Platzhalter und wirft `CALENDAR_SLOT_TAKEN`; das Fehler-Grenzereignis führt mit
  einer neu gelesenen Liste zurück zur Wahl, und die Maske sagt, warum. Zwei Kundinnen
  erhalten so nie denselben Termin.
- **Die Einladung kommt erst, wenn der Termin sicher ist.** `confirm-booking` setzt
  Betreff, Text und die Kundin als Teilnehmerin. Ein Platzhalter, der verliert, hat
  niemanden eingeladen.
- **Nur ein angebotener Termin wird übernommen.** Die Auswahl schickt der Browser, und
  ein Besucher könnte jeden Wert schicken. Das Script `Termin übernehmen` sucht den
  Wert in `freieTermine`; findet es ihn nicht, geht es zurück zur Wahl. Die Liste
  selbst (`terminOptionen`) liest die Maske nur: Schickt ein Besucher sie mit, lehnt der
  Server die Eingabe ab.
- **Welcher Kalender antwortet, ist die Sache des Workers.** Das Modell nennt den Worker
  `filiale` und das Postfach `werkstatt@velo-beispiel.ch`. Ob dahinter Microsoft 365 oder
  der Mock steht, entscheidet das Bundle im Vault — das Modell bleibt gleich.

## Selbst ausführen

1. Unter **Workers** einen Worker `filiale` vom Typ **Calendar booking** anlegen, mit
   einem Secret wie

   ```json
   {"provider": "mock", "calendars": {"werkstatt@velo-beispiel.ch": {}}}
   ```

   Für Microsoft 365: `{"provider": "microsoft-graph", "tenantId": "…", "clientId": "…",
   "clientSecret": "…"}` und im Modell die Adresse des eigenen Ressourcenpostfachs. Für
   Google Workspace: `{"provider": "google-calendar", "clientEmail": "…", "privateKey":
   "…"}` und im Modell die Kalender-ID des freigegebenen Kalenders. Die Schritte stehen im
   Handbuch unter *Calendar booking*.
2. Das Beispiel installieren (Handbuch, Kapitel Beispiele, oder atlas hub, Bundle
   `terminbuchung`) und in der Tasks-App unter *Start process* bei **Terminbuchung** den
   öffentlichen Link veröffentlichen.
3. Den Link in einem privaten Browserfenster öffnen und buchen.

`api/terminbuchung_example_test.go` spielt beides als anonyme Besucherin durch: eine
Buchung, und eine, bei der jemand den gewählten Termin in der Zwischenzeit belegt.

## Was das Beispiel nicht zeigt

- **Öffnungszeiten und Dauern im Modell.** Sie stehen hier im Input des Tasks und im
  Script. In einer echten Applikation gehören sie in eine Entscheidungstabelle oder in
  die Werte der Installation (ADR-0453).
- **Termine über mehrere Ressourcen.** Mehrere Kalender mit `require: "any"` (irgendein
  freier Mechaniker) oder `"all"` (Person und Raum) sind möglich; hier ist es einer.
- **Was nach der Buchung im Kalender geschieht.** Wird ein Termin in Outlook verschoben
  oder gelöscht, merkt der Prozess das nur über `get-booking`.
