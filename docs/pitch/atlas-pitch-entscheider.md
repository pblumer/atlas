# Atlas — 10-Minuten-Pitch für Entscheider

**Zielgruppe:** Manager oder Architekt ohne tiefes technisches Interesse. Er will wissen,
*wie man mit Atlas arbeitet* und *was es kann* — nicht, wie es intern gebaut ist.

**Format:** Live-Demonstration am laufenden System, 10 Minuten, ohne Folien.

**Leitsatz für den ganzen Auftritt:** Nicht das Produkt erklären, sondern den Lebensweg
eines Prozesses zeigen — vom Bild über den Betrieb bis zur Rechenschaft. Der Entscheider
soll am Ende einen Satz mitnehmen können, nicht acht Funktionen.

---

## 1. Die Reihenfolge und warum sie so ist

Ein Entscheider entscheidet nicht über BPMN-Konformität. Er entscheidet über vier Dinge:
Risiko, Kosten, Geschwindigkeit und Abhängigkeit. Die Demonstration ist deshalb entlang
dieser vier Fragen aufgebaut, nicht entlang der Menüstruktur der Anwendung.

| # | Szene | Beantwortet die stille Frage |
|---|-------|------------------------------|
| 0 | Rahmen setzen | „Worum geht es hier überhaupt?" |
| 1 | Das Diagramm *ist* das System | „Verstehen meine Fachleute das?" |
| 2 | Ein Klick vom Bild zum Betrieb | „Wie lange dauert es bis etwas läuft?" |
| 3 | Die Menschen im Prozess | „Was ändert sich für meine Mitarbeitenden?" |
| 4 | Die Regeln gehören dem Fachbereich | „Muss für jede Änderung die IT ran?" |
| 5 | Der Betrieb sieht jeden Vorgang | „Was passiert, wenn es klemmt?" |
| 6 | Die Beweisfrage | „Kann ich nachweisen, warum etwas geschah?" |
| 7 | Grösse und Betriebsaufwand | „Was kostet mich das im Betrieb?" |
| 8 | Arbeiten mit KI | „Ist das zukunftsfähig oder von gestern?" |
| 9 | Abschluss | „Was soll ich jetzt tun?" |

Die Reihenfolge ist bewusst **erst Nutzen, dann Betrieb, dann Technik-Ausblick**. Wer mit
Architektur beginnt, verliert einen Manager in der zweiten Minute. Wer mit dem Diagramm
beginnt, hat ihn, weil das Diagramm die einzige Sprache ist, die Fachbereich, Architektur
und Betrieb gemeinsam lesen können.

**Streichkandidaten**, wenn die Zeit knapp wird (in dieser Reihenfolge streichen):
zuerst Szene 4 (DMN), dann Szene 8 (KI). Niemals streichen: Szene 5 und 6 — sie tragen
das Argument.

---

## 2. Vorbereitung (15 Minuten vor dem Termin)

**Tabs im Voraus öffnen** — es wird während der Demonstration nicht navigiert, sondern
umgeschaltet:

1. **Console → Dashboard** (Einstieg und Schluss)
2. **Modeler**, ein fertiges, gut benanntes Modell offen auf der Zeichenfläche
3. **Tasks → Inbox** mit mindestens einer wartenden Aufgabe
4. **Operations → Instances**, Live-Ansicht des Prozesses aus Szene 2 geöffnet
5. **Operations → Instances**, ein *abgeschlossener* Vorgang im Replay vorbereitet
6. Optional: **Panorama** (nur wenn der Gegenüber Architekt ist)

**Prüfen:**

- Läuft im Hintergrund eine Last, die im Dashboard eine grosse Zahl erzeugt? Auf dem
  vorbereiteten System sind das derzeit rund **50 000 aktive Vorgänge**. Diese Zahl ist
  das stärkste Einzelargument der ganzen Demonstration — sie muss stimmen und sie muss
  sichtbar sein. Falls keine Last läuft: den Lastprozess vorher starten.
- Sind offene **Incidents** vorhanden? Zwei bis drei sind ideal — ein leerer
  Incident-Bildschirm nimmt Szene 5 die Pointe.
- Ist mindestens eine **wartende Benutzeraufgabe** mit Formular da?
- Browser-Zoom auf ca. 125 %, Dunkel- oder Hellmodus passend zum Beamer, alle privaten
  Tabs geschlossen, Benachrichtigungen aus.

**Nicht zeigen:** Rohes BPMN-XML, die API-Dokumentation, Log-Dateien, den
Konfigurationsdialog eines Workers im Detail, ADRs. Alles davon ist richtig und gut — und
kostet einen Manager exakt die Aufmerksamkeit, die man in Szene 6 braucht.

---

## 3. Das Drehbuch

Die Sprechtexte sind wörtlich formuliert. Sie sind als Gerüst gedacht, nicht zum Ablesen.
Kursive Passagen in eckigen Klammern sind Handlungsanweisungen.

### Szene 0 — Rahmen setzen · 0:00–0:40 · kein Klick

*[Bildschirm zeigt das Console-Dashboard. Nicht darauf zeigen, erst reden.]*

> „Ich zeige Ihnen in zehn Minuten kein Werkzeug, sondern einen Arbeitsweg. Die Frage,
> um die es geht, ist diese: **Wo genau läuft eigentlich Ihr Prozess?** In den meisten
> Organisationen lautet die ehrliche Antwort: teils in einem Diagramm, das jemand vor
> drei Jahren gezeichnet hat, teils in einer Mailbox, teils in einem Skript, das ein
> einzelner Mitarbeiter versteht. Das Diagramm beschreibt den Prozess, aber es *führt*
> ihn nicht aus. Genau diese Lücke schliesst Atlas: Das Diagramm ist nicht die
> Dokumentation des Prozesses — es ist der Prozess.
>
> Ich zeige Ihnen das an einem laufenden System. Alles, was Sie gleich sehen, läuft
> gerade wirklich."

**Botschaft:** Es geht um die Lücke zwischen Beschreibung und Ausführung.

---

### Szene 1 — Das Diagramm ist das System · 0:40–2:10 · Modeler

*[Auf den Modeler wechseln, ein Modell auf der Zeichenfläche.]*

> „Das hier ist ein Prozess, wie ihn ein Fachbereich zeichnet — Standard-Notation, BPMN,
> das ist keine Atlas-Erfindung, sondern der internationale Standard, den Ihre
> Prozessverantwortlichen ohnehin verwenden."

*[Mit dem Mauszeiger einmal langsam den Hauptpfad entlangfahren. Ein Element anklicken,
damit die Eigenschaften rechts erscheinen — dann sofort wieder schliessen.]*

> „Rechts hängt an jedem Kästchen das, was es wirklich tun soll. Das ist der ganze
> Unterschied: In den meisten Häusern gibt es ein Diagramm *und* eine Umsetzung, und die
> beiden driften ab dem ersten Tag auseinander. Hier gibt es nur das Diagramm."

*[Simulation starten — die Wiedergabe-Funktion im Modeler.]*

> „Und bevor irgendetwas installiert oder freigegeben ist, kann ich den Prozess
> durchspielen. Schauen Sie: Der Punkt, der hier läuft, ist ein Vorgang. Ich kann an
> jeder Verzweigung entscheiden und sehe sofort, wohin es läuft."

*[Zwei bis drei Schritte durchklicken, dann anhalten.]*

> „Das ist die erste Fähigkeit, über die man reden sollte: **Ein Prozess kann geprüft
> werden, bevor er existiert.** Ihre Fachabteilung kann eine Idee durchspielen, ohne dass
> ein Entwickler eine Zeile schreibt."

**Botschaft:** Eine Sprache für alle Beteiligten; Prüfen vor dem Bauen.
**Zeitwächter:** Bei 2:10 weiter, auch wenn die Simulation nicht zu Ende ist.

---

### Szene 2 — Ein Klick vom Bild zum Betrieb · 2:10–3:10 · Modeler → Operations

*[Auf „Deploy & run" klicken.]*

> „Das war der Weg von der Zeichnung in den produktiven Betrieb. Ein Klick. Kein
> Datenbankschema, kein Release-Fenster, kein Deployment-Team.
>
> Und ein Punkt, der in Projekten sonst Monate kostet: Ein Prozess braucht typischerweise
> fünf Umsysteme — SAP, ein Ticketsystem, ein Verzeichnisdienst. Auf keines davon haben
> Sie am ersten Tag Zugriff. In Atlas kann jeder solche Schritt zunächst als **Attrappe**
> laufen: Der Prozess läuft von Anfang bis Ende durch, mit realistischen Antwortzeiten
> und sogar mit einer einstellbaren Ausfallquote. Später ersetzt man genau diesen einen
> Schritt durch die echte Anbindung — am Prozess selbst ändert sich nichts."

**Botschaft:** Time-to-first-run ist Minuten, nicht Monate. Integrationen blockieren den
Fortschritt nicht mehr.
**Wenn gefragt wird, was angebunden werden kann:** REST-Schnittstellen, Mail, SharePoint,
Jira, Google Sheets, Active Directory, Entra ID, SQL-Datenbanken, PowerShell- und
Python-Skripte, Webseiten-Auslesen. Nicht aufzählen, sondern nachschieben, wenn gefragt.

---

### Szene 3 — Die Menschen im Prozess · 3:10–4:10 · Tasks

*[Auf Tasks → Inbox wechseln.]*

> „Kein Prozess ist vollautomatisch. Irgendwo entscheidet immer ein Mensch. Das ist der
> Posteingang eines Mitarbeitenden — hier liegt seine Arbeit, nicht in seinem Mailfach."

*[Eine Aufgabe anklicken, das Formular zeigen.]*

> „Er sieht das Formular zu seiner Aufgabe, die Arbeitsanweisung dazu, die Frist und die
> Priorität. Er füllt aus, klickt ab — und der Prozess läuft weiter. Aufgaben können an
> eine Person oder an eine ganze Gruppe gehen, aus der sie sich jemand zieht.
>
> Der Nutzen für Sie ist nicht das Formular. Der Nutzen ist: **Es gibt keine Arbeit mehr
> ausserhalb des Prozesses.** Nichts hängt in einem Postfach, das niemand sieht. Was
> wartet, wartet sichtbar."

*[Optional, wenn Zeit: erwähnen, dass ein Prozess auch über einen öffentlichen Link von
jemandem gestartet werden kann, der gar kein Konto hat — etwa ein Antrag von aussen.]*

**Botschaft:** Menschliche Arbeit wird Teil des Prozesses statt sein blinder Fleck.

---

### Szene 4 — Die Regeln gehören dem Fachbereich · 4:10–5:00 · Entscheidungstabelle

*[Eine DMN-Entscheidungstabelle öffnen.]*

> „Eine Sache noch, die in der Praxis mehr Geld spart als alles bisher Gezeigte: Das hier
> sind Geschäftsregeln — Genehmigungsgrenzen, Zuständigkeiten, Bewertungsschlüssel. Sie
> stehen in einer Tabelle, nicht in Programmcode.
>
> Wenn morgen die Genehmigungsgrenze von 1000 auf 2500 steigt, ändert das ein Fachmitarbeiter
> in dieser Zeile. Kein Ticket, kein Entwickler, keine neue Version des Prozesses.
> Und jede einzelne Anwendung dieser Regel wird protokolliert — mit den Eingabewerten,
> dem Ergebnis und der Zeile, die gegriffen hat."

**Botschaft:** Fachliche Änderungen brauchen keinen IT-Auftrag; trotzdem ist jede
Entscheidung prüfbar.
**Erster Streichkandidat**, wenn es eng wird.

---

### Szene 5 — Der Betrieb sieht jeden Vorgang · 5:00–6:20 · Operations

*[Auf Operations → Live-Ansicht des Prozesses wechseln.]*

> „Jetzt der Blick, den heute in den meisten Häusern niemand hat. Sie sehen dasselbe
> Diagramm wie vorhin — aber jetzt mit allen laufenden Vorgängen darauf. Grün bedeutet:
> Hier steht gerade jemand. Die Zahl am Kästchen sagt, wie viele.
>
> Diese eine Ansicht beantwortet die Frage, die Sie sonst in einer Sitzung mit vier
> Leuten klären: **Wo stauen sich die Vorgänge?** Nicht als Vermutung, sondern als
> Tatsache — abgelesen, nicht geschätzt."

*[Auf Operations → Incidents wechseln.]*

> „Und wenn etwas klemmt, verschwindet es nicht in einem Protokoll, sondern steht hier.
> Dieser Vorgang wartet auf ein Umsystem, das nicht antwortet. Ein Vorfall ist in Atlas
> kein Absturz: **Der Vorgang ist nicht verloren, er ist angehalten.** Sobald das
> Umsystem wieder da ist, wird er von genau der Stelle fortgesetzt, an der er steht —
> nicht von vorne."

**Botschaft:** Transparenz über die Warteschlangen; Störungen kosten keine Vorgänge.
**Nicht streichen.**

---

### Szene 6 — Die Beweisfrage · 6:20–7:30 · Replay eines Vorgangs

*[Einen abgeschlossenen Vorgang öffnen, Zeitleiste sichtbar.]*

> „Die wichtigste Minute. Stellen Sie sich vor, in sechs Monaten fragt eine Revision, ein
> Kunde oder ein Gericht: *Warum wurde dieser eine Antrag abgelehnt?*
>
> Heute rekonstruiert man das aus Protokolldateien, wenn man Glück hat."

*[Die Zeitleiste langsam nach hinten ziehen. Die Marken bewegen sich auf dem Diagramm.]*

> „Hier spule ich den Vorgang Schritt für Schritt zurück. Ich sehe nicht nur, welchen Weg
> er genommen hat — ich sehe **die Daten, wie sie in genau diesem Moment waren**, und
> welche Regel welche Entscheidung ausgelöst hat.
>
> Das ist keine Rekonstruktion und keine Auswertung von Logdateien. Das sind die
> aufgezeichneten Tatsachen. Wenn Sie in einem regulierten Umfeld arbeiten — und in der
> Schweiz arbeiten Sie meistens in einem — ist das der Unterschied zwischen einer Antwort
> und einer Ausrede."

**Botschaft:** Lückenlose Nachvollziehbarkeit als Eigenschaft des Systems, nicht als
nachträglicher Bericht.
**Das ist der Höhepunkt der Demonstration.** Danach kurz schweigen.

---

### Szene 7 — Grösse und Betriebsaufwand · 7:30–8:20 · Console-Dashboard

*[Zurück auf das Dashboard.]*

> „Zwei Zahlen zum Schluss, und dann sind wir durch.
>
> Erstens: Was Sie hier sehen, sind **rund fünfzigtausend Vorgänge, die gerade
> gleichzeitig laufen**."

*[Auf die Zahl zeigen.]*

> „Zweitens, und das ist die eigentliche Überraschung: Das läuft in **einer einzigen
> Programmdatei**. Keine Datenbank, die jemand betreiben muss. Kein Message-Broker. Keine
> Middleware. Sie kopieren eine Datei auf einen Server und starten sie.
>
> Übersetzt in Ihre Sprache: Der Betriebsaufwand für die Plattform ist nahe null, und die
> Frage 'Wer betreut uns die Datenbank dazu?' stellt sich nicht."

**Botschaft:** Leistung und Betriebsaufwand stehen nicht im üblichen Verhältnis.

---

### Szene 8 — Arbeiten mit KI · 8:20–9:10 · optional

> „Ein letzter Punkt, weil er die Arbeitsweise verändert: Atlas ist von Grund auf so
> gebaut, dass eine KI es genauso bedienen kann wie ein Mensch — über dieselben Wege,
> mit denselben Rechten, nachvollziehbar in denselben Protokollen.
>
> Praktisch heisst das dreierlei: Ein Assistent kann im Zeichenwerkzeug beim Modellieren
> helfen. Ein Assistent kann einen Prozess anlegen, in Betrieb nehmen, starten und
> auswerten — er ist ein regulärer Benutzer, kein Sonderfall. Und ein Prozessschritt
> selbst kann ein KI-Agent sein, der eine Aufgabe übernimmt, für die es bisher einen
> Menschen brauchte.
>
> Was das für Sie bedeutet: Diese Plattform ist nicht nachträglich mit KI beklebt worden.
> Sie ist so gebaut, dass Automatisierung mit KI keine Sonderarchitektur braucht."

**Botschaft:** Zukunftsfähigkeit als Bauprinzip, nicht als Zusatzfunktion.
**Zweiter Streichkandidat.**

---

### Szene 9 — Abschluss · 9:10–10:00 · kein Klick

*[Vom Bildschirm wegdrehen, Blickkontakt.]*

> „Drei Sätze, dann bin ich fertig.
>
> Erstens: Der Prozess ist nicht mehr beschrieben, er läuft — es gibt nur noch ein
> Artefakt statt zwei, die auseinanderdriften.
>
> Zweitens: Sie sehen jederzeit, wo Ihre Vorgänge stehen, und Sie können für jeden
> einzelnen belegen, warum er so gelaufen ist.
>
> Drittens: Das kostet Sie eine Programmdatei auf einem Server.
>
> Ich sage Ihnen dazu offen, wo das Produkt steht: Atlas ist in aktiver Entwicklung, in
> einer Vorversion, und dafür bewusst noch nicht für den unternehmenskritischen
> Produktivbetrieb freigegeben. Was Sie gesehen haben, läuft — aber es ist ein System im
> Aufbau, kein fertig gekauftes Produkt mit Support-Vertrag.
>
> Der sinnvolle nächste Schritt ist deshalb kein Beschaffungsentscheid, sondern ein
> Versuch: Nennen Sie mir *einen* Prozess bei Ihnen, der heute in Mails und Excel läuft
> und den niemand vollständig überblickt. Ich zeige Ihnen den in zwei Wochen hier laufend
> — mit Attrappen statt echter Anbindungen. Danach reden wir weiter."

**Botschaft:** Ehrlichkeit über den Reifegrad schafft die Glaubwürdigkeit, die den kleinen
nächsten Schritt leicht macht.

---

## 4. Umgang mit den erwartbaren Einwänden

| Einwand | Antwort in einem Satz |
|---------|----------------------|
| „Wir haben schon Camunda / einen BPM-Anbieter." | „Dann kennen Sie den Nutzen bereits — der Unterschied liegt im Betriebsaufwand und in der schrittweisen Nachvollziehbarkeit einzelner Vorgänge; einen Vergleich mache ich Ihnen gern anhand Ihres konkreten Falls." |
| „Ist das produktionsreif?" | „Nein, heute nicht — es ist eine Vorversion in aktiver Entwicklung. Genau deshalb schlage ich einen begleiteten Versuch an einem unkritischen Prozess vor und keine Ablösung." |
| „Wer betreibt das? Wir haben keine Leute." | „Eine Programmdatei, keine Datenbank, kein Broker. Der Betriebsaufwand ist der einer einzelnen Anwendung, nicht der einer Plattform." |
| „Was ist mit Datenschutz und Revision?" | „Jeder Vorgang ist Schritt für Schritt rekonstruierbar, jede Regelanwendung protokolliert; Zugangsdaten liegen verschlüsselt im System und nie im Prozessmodell. Für den Schweizer Bundeskontext existiert eine Auseinandersetzung mit dem ISDS-Konzept, inklusive der offenen Punkte." |
| „Können unsere Fachleute das wirklich?" | „Die Notation ist Standard, die Regeln stehen in Tabellen, und man kann jeden Entwurf gefahrlos durchspielen. Was es weiterhin braucht, ist eine Person, die Prozesse sauber denken kann — die brauchen Sie ohnehin." |
| „Wie hängt es an unseren Systemen?" | „Über fertige Bausteine für die üblichen Systeme, und für alles andere über Standard-Schnittstellen. Wichtiger: Sie müssen damit nicht anfangen — der Prozess läuft zuerst mit Attrappen." |
| „Was, wenn der Server abstürzt?" | „Jeder Zustandswechsel liegt vor der Bestätigung auf der Festplatte. Nach einem Neustart läuft jeder Vorgang an der Stelle weiter, an der er war — kein Vorgang geht verloren." |

---

## 5. Varianten

**Wenn der Gegenüber Architekt ist** (nicht Manager): Szene 4 streichen und stattdessen
nach Szene 5 die **Panorama-Ansicht** zeigen — die Landschaftssicht über Anwendungen,
Prozesse, Schnittstellen und Worker, in der sich die Auswirkung einer Änderung ablesen
lässt. Argument: „Ihre Architekturlandschaft ist hier keine gepflegte Zeichnung, sondern
wird aus dem abgeleitet, was tatsächlich läuft."

**Wenn nur 5 Minuten bleiben:** Szenen 1, 2, 5, 6, 9. Das ist der harte Kern und trägt
allein.

**Wenn 20 Minuten möglich sind:** Zusätzlich eine kleine Anwendung als Ganzes zeigen
(mehrere Prozesse, Formulare und Entscheidungen, gemeinsam versioniert und auf einen
anderen Server übertragbar) sowie die Vorfallsbehandlung mit Wiederholung eines
fehlgeschlagenen Schrittes.

---

## 6. Was man weglassen muss, auch wenn es reizt

- Ereignisprotokoll, Schreibvorgänge, Partitionen, Zustandsspeicher — der Entscheider
  hört „Datenbankthema" und schaltet ab. Der einzige zulässige Satz dazu ist der aus
  Szene 7: keine Datenbank nötig.
- BPMN-Elementtypen und Konformitätsgrade. Interessiert erst den, der bereits kaufen will.
- Zahlenreihen aus Leistungsmessungen. Die eine Zahl auf dem Dashboard schlägt jede Tabelle.
- Die Lizenzfrage. Wenn sie kommt: AGPL-3.0, und das ist ein Gespräch für die Rechtsabteilung,
  nicht für diese zehn Minuten.

---

*Erstellt für eine Live-Demonstration auf einem vorbereiteten Atlas-System. Die genannten
Kennzahlen (rund 50 000 aktive Vorgänge) stammen aus dem Zustand des Demonstrationssystems
und sind vor jedem Termin neu zu prüfen.*
