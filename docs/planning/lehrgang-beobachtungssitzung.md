# Beobachtungssitzung vor dem Lehrgang

Material für eine Sitzung von etwa einem halben Tag, in der zwei bis drei Personen der
Zielgruppe eine Aufgabe mit dem heutigen Handbuch lösen, ohne Hilfestellung und
beobachtet. Zugehöriger Plan: [lehrgang-prozessentwicklung.md](lehrgang-prozessentwicklung.md).

**Stand:** 2026-09-28 · **Atlas-Version:** 0.7.0

---

## 1. Zweck

Der Lehrgangsplan beruht auf der Analyse von Dokumenten und Code, nicht auf der
Beobachtung von Lernenden. Die Sitzung prüft seine zentrale Annahme und liefert eine
Ausgangsmessung, an der sich der Pilot später messen lässt.

**Hypothese H1:** Die meisten und längsten Hindernisse liegen in der
Ausführungssemantik (S) und der Lieferkette (L), nicht in der Notation (N) oder der
Bedienung (B).

**Entscheidungsregel, vorab festgelegt:** Liegt mehr als die Hälfte der blockierten
Zeit in N oder B, wird der Lehrgang vor Phase 3 umgewichtet: mehr Gewicht auf
Modellieren (M2) und Bedienung, weniger auf Ausliefern, Betreiben und
Weiterentwickeln (M5–M7). Liegt sie überwiegend in S und L, bleibt der Plan.
Die Regel steht hier, bevor Ergebnisse vorliegen, damit sie nicht nachträglich den
Ergebnissen angepasst wird.

## 2. Teilnehmende und Rollen

- **Teilnehmende:** zwei bis drei Personen, mindestens eine aus der
  Software-Entwicklung und eine aus dem Fachbereich. Keine Vorkenntnisse in Atlas.
  Wer an der Beobachtung teilnimmt, nimmt nicht am späteren Pilot teil; sonst misst
  der Pilot den Lerneffekt dieser Sitzung mit.
- **Moderation:** führt ein, achtet auf die Zeit, gibt bei Bedarf einen Rettungshinweis.
- **Beobachtung:** führt das Protokoll (Abschnitt 5). Moderation und Beobachtung
  sollten verschiedene Personen sein.

## 3. Rahmen

- **Dauer:** 180 Minuten Arbeit, danach 20 Minuten Nachgespräch.
- **Erlaubte Quellen:** das Handbuch und die Oberfläche von Atlas. Wer zu anderen
  Quellen greift (Repository, Internetsuche, KI-Assistent), darf das, sagt es laut, und
  die Beobachtung notiert es. Genau diese Stellen zeigen, wo das Handbuch nicht trägt.
- **Laut denken:** Die Teilnehmenden sagen, was sie gerade versuchen und was sie
  erwarten.
- **Keine Hilfe.** Bleibt eine Person länger als 15 Minuten am selben Hindernis, gibt
  die Moderation einen minimalen Rettungshinweis (ein Kapitel oder ein Menüpunkt, keine
  Lösung) und die Beobachtung vermerkt ihn.
- **Keine Wertung.** Die Sitzung prüft das Handbuch, nicht die Person.

## 4. Die Aufgabe

Die Aufgabe ist bewusst **nicht** der Leitfall des Lehrgangs, sondern ein Fall mit
derselben Struktur: Sonst würde der Lehrgang später auf die Messung hin geschrieben.

### Auftrag: Gastzugang mit Ablaufdatum

Eine Mitarbeiterin (die Gastgeberin) beantragt für eine externe Person einen
befristeten Zugang, also ein Konto im Verzeichnis.

**Fachliche Regeln**

1. Der Antrag enthält Vorname, Nachname und E-Mail-Adresse der externen Person, deren
   Firma, die gewünschte Dauer in Tagen und eine Begründung.
2. Bis 7 Tage wird automatisch freigegeben. Von 8 bis 30 Tagen muss die Linie der
   Gastgeberin freigeben (Kandidatengruppe `linie`). Mehr als 30 Tage sind nicht
   möglich; der Antrag wird mit Begründung abgelehnt.
3. Nach der Freigabe wird für die externe Person ein Konto in der OU für Gäste
   angelegt, und die Gastgeberin wird per Mail informiert.
4. Am Ablaufdatum wird das Konto deaktiviert, und die Gastgeberin erhält eine
   Mitteilung.
5. Bei einer Ablehnung erhält die Gastgeberin eine Mitteilung mit der Begründung.

**Technische Vorgaben**

- Das Verzeichnis ist ein Active Directory; in der Übung läuft es als Mockup, das
  bereits eingeschaltet ist.
- Mails gehen über einen Mail-Worker im Vorschaumodus.
- Den AD-Worker und den Mail-Worker richten die Teilnehmenden selbst ein.
- Am Ende steht der Prozess als publizierte Applikation mit Release bereit.

### Meilensteine

Die Beobachtung notiert für jeden Meilenstein die Uhrzeit, zu der er erreicht wurde.

| | Meilenstein | Nachweis |
|---|---|---|
| A | Ein Prozess ist deployt und mit einem Testfall gestartet, gleich wie unvollständig | Instanz in Operations |
| B | Die Freigaberegel ist umgesetzt: 5, 20 und 45 Tage nehmen den richtigen Weg | drei Instanzen |
| C | Die Freigabeaufgabe der Linie wird mit einem Formular im Posteingang erledigt | abgeschlossene Aufgabe |
| D | Das Konto steht im AD-Mockup, die Mail im Postausgang | Betrieb › Mock directory, Operations › Outbox |
| E | Die Deaktivierung am Ablaufdatum ist modelliert und nachgewiesen | Nachweis nach Wahl der Teilnehmenden |
| F | Alles ist als Applikation publiziert | Release vorhanden |

Wie Meilenstein E nachgewiesen wird, ist absichtlich offen gelassen: Zeitabhängiges
Verhalten zu testen, ohne Tage zu warten, gehört zu dem, was beobachtet werden soll.

## 5. Beobachtungsprotokoll

Eine Zeile je Tätigkeitswechsel oder Hindernis. Hindernisse möglichst mit der
wörtlichen Aussage der Person.

| Zeit | Tätigkeit oder Meilenstein | Quelle | Hindernis (wörtlich) | Kat. | Dauer | Aufgelöst durch |
|---|---|---|---|---|---|---|
| | | | | | | |

**Quelle:** Kapitel des Handbuchs, Oberfläche, Repository, Internet, KI-Assistent.

**Kategorien:**

| Kürzel | Bedeutung | Beispiele |
|---|---|---|
| N | Notation | BPMN-Element wählen, DMN-Tabelle, FEEL-Ausdruck, Formular |
| S | Ausführungssemantik | Variablen und Scopes, Wartezustände, Timer mit Datum, Zustellung, Versionen |
| L | Lieferkette | Worker einrichten, Secret, Test ohne Wartezeit, Publizieren |
| B | Bedienung | Menüpunkt oder Schaltfläche nicht gefunden |
| D | Dokumentation | fehlt, ist falsch oder nicht auffindbar |

**Aufgelöst durch:** selbst, Handbuch, andere Quelle, Rettungshinweis, gar nicht.

## 6. Nachgespräch (20 Minuten)

Die ersten drei Fragen sind Vorhersagen: Sie werden beantwortet, ohne etwas
auszuprobieren. Sie prüfen die Ausführungssemantik, die der Lehrgang vermitteln soll.

1. Es laufen 40 Gastzugänge, die in drei Wochen ablaufen. Morgen wird eine Version 2
   deployt, die vor der Deaktivierung bei der Gastgeberin nachfragt. Was geschieht mit
   den 40?
2. Der Job «Konto anlegen» wird nach einem Absturz ein zweites Mal zugestellt. Was
   geschieht?
3. Das Verzeichnis antwortet seit einer Stunde nicht. Operations zeigt keine Incidents.
   Ist alles in Ordnung?
4. An welcher Stelle hätten Sie ohne Beobachtung aufgegeben?
5. Welche Stelle im Handbuch hat am meisten geholfen, welche hat in die Irre geführt?
6. Was hätten Sie als Erstes lernen wollen?

**Erwartete Antworten (nur für Moderation und Auswertung):**

1. Sie laufen auf Version 1 weiter und werden ohne Nachfrage deaktiviert, bis sie
   migriert werden. Ein Deploy ändert keinen laufenden Fall.
2. Die Zustellung ist mindestens einmal. Der zweite Versuch scheitert, weil das Konto
   schon existiert; das Mockup lehnt es ab wie ein echtes AD. Ohne idempotente
   Behandlung entsteht ein Incident.
3. Nein. Atlas hält die Arbeit für ein nicht erreichbares Ziel zurück, ohne Retries zu
   verbrauchen; sichtbar unter Operations › Workers, Karte «Held back».

## 7. Auswertung

Je Person:

- Zeit bis Meilenstein A und erreichte Meilensteine nach 180 Minuten;
- Anzahl und Dauer der Hindernisse je Kategorie;
- Anzahl der Rettungshinweise;
- richtige Vorhersagen (0 bis 3).

Über alle Personen:

- Anwendung der Entscheidungsregel aus Abschnitt 1;
- jede Stelle der Kategorie D als Issue erfasst (Fehler oder Lücke im Handbuch);
- das Ergebnis anonymisiert (P1 bis P3) festgehalten in
  `docs/planning/lehrgang-beobachtung-ergebnis.md`.

Dieselbe Aufgabe lösen im Pilot die Teilnehmenden nach dem Lehrgang. Der Vergleich ist
bei so wenigen Personen qualitativ, nicht statistisch; er zeigt, ob sich die Art der
Hindernisse verschiebt, nicht um wie viele Prozent.

## 8. Vorbereitung

- [ ] **Probelauf:** Die Moderation löst die Aufgabe vorher einmal selbst auf 0.7.0.
      Stösst sie dabei auf eine Produktlücke, wird die Aufgabe angepasst, bevor sie
      eine Messung verfälscht.
- [ ] Je Person eine frische Atlas-Instanz mit eigenem Datenverzeichnis, Anmeldung mit
      der Rolle `admin` (Worker, Secrets und Mockup-Schalter dürfen nicht an Rechten
      scheitern) oder lokal mit `--auth=false`.
- [ ] AD-Mockup eingeschaltet (Console › Workers, Karte Active Directory), Startbestand
      mit einer OU für Gäste, zum Beispiel:

      ```text
      dn: ou=Gaeste,dc=example,dc=com
      objectClass: organizationalUnit
      ou: Gaeste
      ```

- [ ] Handbuch der Instanz erreichbar (`/handbuch.html`).
- [ ] Einverständnis der Teilnehmenden; Bildschirmaufzeichnung nur mit ausdrücklicher
      Zustimmung. Ergebnisse ohne Namen.
- [ ] Protokollvorlage (Abschnitt 5) für jede Person bereit.
