# Lehrgang Prozessentwicklung — Plan

Lebendiges Planungsdokument für einen Lehrgang, der Menschen befähigt, ihre
Betriebsprozesse mit Atlas vom Entwurf bis in die Produktion zu bringen. Es hält fest,
was entschieden ist, was offen ist und welche Arbeit in welcher Reihenfolge ansteht,
damit spätere Arbeitssitzungen daran anschliessen können, ohne die Herleitung zu
wiederholen. Anders als ein ADR wird es fortgeschrieben, sobald sich etwas ändert.

**Stand:** 2026-10-01 · **Basis:** `main` nach Release 0.8.0

---

## 1. Entscheide

| Datum | Entscheid |
|---|---|
| 2026-09-28 | Es wird ein Lehrgang für die Prozessentwicklung aufgebaut; Zielgruppe sind Software-Entwicklerinnen und -Entwickler sowie Fachpersonen. |
| 2026-09-28 | «Betriebsprozesse» meint den IT-Betrieb. Leitfall ist ein **befristeter Berechtigungsantrag** (AD-Gruppe, Freigabe nach Schutzbedarf, Entzug am Fristende). |
| 2026-09-28 | Vor dem Bau des Leitfalls (Phase 3) findet eine **Beobachtungssitzung** mit zwei bis drei Personen der Zielgruppe statt; Material: [lehrgang-beobachtungssitzung.md](lehrgang-beobachtungssitzung.md). |
| 2026-09-28 | Begonnen wird mit Phase 1 (Referenzlücken im Handbuch); ein Thema je Pull Request, nacheinander. |

## 2. Ausgangslage

Das Handbuch (`api/web/handbuch.html`) ist ein Nachschlagewerk: Vokabular, Rezepte,
Beispiele, zwei Bauanleitungen, die Rolle des Prozess-Developers mit Definition of
Done und Runbooks je Worker-Typ. Es bietet keinen Lernweg und keine Rückmeldung, und
es endet faktisch beim Publizieren. Im Produkt vorhanden, im Handbuch aber nicht oder
kaum beschrieben sind:

| Thema | Im Produkt |
|---|---|
| Playground-Szenarien mit Erwartungen, Regeln je Fall und Baseline; CI-Runner `atlas playground` (Exit-Code 3 bei Verstoss) | ADR-0215, `cmd/atlas/playgroundrun.go` |
| Promotion eines Releases auf ein Deployment-Ziel, API-Tokens | ADR-0129, ADR-0194 |
| Export und Import des Quellbaums einer Applikation | ADR-0134 (`GET /api/v1/applications/{id}/source`) |
| Instanzmigration, auch per Fork | ADR-0162, ADR-0389 |
| Eigener Worker über die Job-API (`activate`, `complete`, `fail`) | `api/openapi.go` |
| Metriken und Alarmierung | ADR-0142 |

## 3. Herleitung in Kürze

- **Kompetenzziel:** einen Betriebsprozess eigenständig von der fachlichen Aufnahme bis
  zum überwachten Produktivbetrieb umsetzen und später ändern, ohne laufende Fälle zu
  gefährden.
- **Drei Schichten:** Notation (Wissen, aus dem Handbuch lernbar), Ausführungssemantik
  (mentales Modell: dauerhafte Wartezustände, Langläufer, mindestens einmalige
  Zustellung, Versionen), Lieferkette (Test, Release, Umgebungen, Betrieb).
- **Hypothese:** Der Engpass liegt in Semantik und Lieferkette, nicht in der Notation.
  Sie ist plausibel, aber ungeprüft; die Beobachtungssitzung prüft sie.

## 4. Didaktische Leitlinien

1. Ein Fall durch alle Phasen.
2. Vorhersage vor Ausführung; wer richtig vorhersagt und begründet, überspringt die Übung.
3. Das Produkt gibt die Rückmeldung: jede Übung endet mit einem Playground-Szenario.
4. Das getestete Modell ist das ausgelieferte: Umgebungen unterscheiden sich über
   Worker-Konfiguration, Mockup-Schalter, Mail-Vorschau und Playground-Stubs, nicht
   durch Umbau des Modells (ADR-0215). `atlas:mockupConnector` bleibt das Mittel für
   Systeme, die es noch nicht gibt.
5. Verweisen statt kopieren: Erklärungen bleiben im Handbuch.
6. Wiedereinstieg jederzeit über einen installierbaren Startpunkt je Modul.
7. Der Lehrgang wird getestet wie Code: Startpunkte, Musterlösungen und Szenarien liegen
   im Repository und laufen in CI.

## 5. Module (Entwurf)

| Modul | Kern | Widerlegte Annahme |
|---|---|---|
| M0 Orientierung | Instanz, erster Durchlauf | – |
| M1 Aufnehmen und schneiden | Fall, Business Key, Datenvertrag, Eignung | «Modelliert wird der Ablauf» |
| M2 Modellieren | Happy Path, Abweichungen, DMN, Formular, Informationsmodell | «Das Diagramm ist ein Programm» |
| M3 Implementieren | Variablen, FEEL, Task → Job → Worker, Fehlerwege, Timer, Nachrichten; eigener Worker | «Ein Aufruf geschieht genau einmal» |
| M4 Absichern | Datensatz, Erwartungen, Regeln, Baseline, CI | «Ein grüner Durchlauf beweist den Prozess» |
| M5 Ausliefern | Applikation, Release, Quellbaum, Promotion, Umgebungen | «Für die Produktion passt man das Modell an» |
| M6 Betreiben | Live-Ansicht, Incidents, Held back, Audit, Metriken | «Keine Incidents heisst: alles in Ordnung» |
| M7 Weiterentwickeln | DMN-Änderung, neue Version, Migration | «Ein neues Deploy ändert laufende Fälle» |
| Abschlussarbeit | eigener Prozess im Tandem bis zur Definition of Done | – |

Umfang geschätzt 25–30 Stunden ohne Abschlussarbeit (ohne Pilotdaten).

Zu beachten beim Schreiben: Instanzmigration und das Überschreiben von Instanzvariablen
verlangen die Rolle `admin`, nicht `operator` (`api/openapi.go`); M6 und M7 müssen das
sagen und die Übungsumgebung entsprechend ausstatten.

## 6. Phasen und Arbeitspakete

### Phase 1 — Referenzlücken schliessen

| PR | Inhalt | Stand |
|---|---|---|
| 1 | Grundlagen und Nebenbefunde: dieser Plan, Beobachtungsmaterial; Beispielzahlen an den Katalog gebunden (`examples/handbookcounts_test.go`); Apps- und Rollenangaben im Handbuch nachgeführt (Rolle `productmanager`); ISDS-Konzept 5.2.3 und R-04 auf das Rollenmodell nachgeführt | gemergt (#1127) |
| 2 | Testen als Code: Szenarien, Erwartungen, Regeln, Baseline, Vergleich, `atlas playground` in CI — Erweiterung «Testen & Simulieren» (Anker `#szenarien`); Flag-Tabelle und Exit-Code durch `cmd/atlas/playgroundhandbook_test.go` an den Code gebunden | gemergt (#1129) |
| 3 | Ausliefern: Applikation, Release, Deployment-Ziele, Deploy-Token, Promotion, Quellbaum — neues Kapitel «Ausliefern» (Anker `#ausliefern`), nur Mechanik; Routentabelle und Token-Präfixe durch `api/deliveryhandbook_internal_test.go` an den Code gebunden | gemergt (#1134) |
| 4 | Weiterentwickeln: neue Version, Migration inkl. Fork, Pausieren, DMN-Deployment und Versionierung — neues Kapitel «Weiterentwickeln» (Anker `#weiterentwickeln`), nur Mechanik; Routentabelle und Rollen durch `api/evolvehandbook_internal_test.go` an den Code gebunden | gemergt (#1137) |
| 5 | Eigener Worker über die Job-API, Zustellgarantie, Idempotenz — Erweiterung «Formulare & Worker» (Anker `#eigener-worker`); Job-Routentabelle und Rollen durch `api/workerjobshandbook_internal_test.go` an den Code gebunden | gemergt (#1141) |
| 6 | Überwachen: Metriken (metrics-Scope), Alarmierung extern, OpenSearch-Export, Aufbewahrung, Backup/Full-Snapshot — Erweiterung «Betrieb & Incidents» (Anker `#ueberwachen`); Routentabelle und Rollen durch `api/monitorhandbook_internal_test.go` an den Code gebunden; veraltete /metrics-Zeile korrigiert | gemergt (#1149) |

Arbeitsweise je PR: beschrieben wird nur, was an einer lokal gebauten Instanz
nachvollzogen wurde; Texte auf Deutsch und Englisch; vollständige Prüfsequenz aus
`AGENTS.md` vor dem Push.

### Phase 2 — Referenzweg in die Produktion (ADR-Entwurf, parallel)

Offene Fragen, über die ein ADR-Entwurf entscheiden soll: Trennung der Umgebungen,
Freigabe vor einer Promotion, Schreibschutz auf dem Produktivserver, Verhältnis von
Quelle (Git) und ausgeliefertem Release, Worker-Konfiguration je Umgebung. Das
ISDS-Konzept verlangt getrennte Umgebungen und einen engen Kreis von `modeler`- und
`admin`-Konten auf der Produktion (M-05). Modul M5 wird erst nach diesem Entscheid
geschrieben.

### Phase 3 — Leitfall

**Gebaut.** Referenz-Applikation `examples/lehrgang/` (Mitarbeitereintritt): ein Prozess,
eine DMN und drei Formulare, an denen alle sechs Referenzmodule hängen. Ein Schritt
(«Konto anlegen») ist bewusst kein Mockup, sondern ein Job über die Job-API für einen
eigenen Worker, der bei wiederholtem Fehlschlag einen Incident parkt — so hängen Modul 5
(Eigener Worker) und Modul 6 (Überwachen) an einem Datenfluss. Das Handbuch trägt die
Beispiel-Karte `bsp-lehrgang` (Kapitel «Beispiele») und ein Abschlusskapitel `#leitfall`,
das den Fall Modul für Modul durchgeht — Startpunkt und Szenario je Modul, mit Verweis auf
das jeweilige Referenzkapitel. Guard nach dem Muster der Werkstatt: die Dateien sind die
Quelle, `api/web/examples-catalog.json` wird daraus erzeugt, `examples/catalog_test.go`
verhindert Drift und kompiliert die Modelle. Auf Wunsch vor der Beobachtungssitzung gebaut;
deren Ergebnis kann das Thema später schärfen.

**Sitzungsmaterial.** Zum Leitfall gehören zwei Moderationsdokumente, die ihn in eine
durchführbare Sitzung übersetzen: die Moderationsfassung
[lehrgang-drehbuch.md](lehrgang-drehbuch.md) (Zeitfenster, Vorhersagefrage und erwartete
Antwort, Tätigkeit und Erfolgsmass je Modul, Vorbereitungs-Checkliste) und das
Teilnehmenden-Arbeitsblatt [lehrgang-arbeitsblatt.md](lehrgang-arbeitsblatt.md) (dieselben
Vorhersagefragen ohne Antworten, Erfolg als Beobachtungsauftrag). Getrennt gehalten, damit
die didaktische Leitlinie «Vorhersage vor Ausführung» nicht durch mitgelieferte Antworten
entwertet wird. Beide liegen in `docs/planning/` neben diesem Plan.

### Phase 4 — Lehrgangsseite

Eigene Seite neben dem Handbuch oder Kapitelgruppe im Handbuch: noch zu entscheiden.

### Phase 5 — Pilot

Zwei bis drei Tandems aus Fach und Entwicklung. Gemessen werden Zeit je Modul,
Szenarien, die rot bleiben, und die Qualität der Abschlussarbeiten; zum Vergleich
dient die Ausgangsmessung der Beobachtungssitzung.

Das Sitzungsinstrument liegt bereit (Phase 3): Der Ablauf je Tandem folgt der
Moderationsfassung [lehrgang-drehbuch.md](lehrgang-drehbuch.md), die Teilnehmenden
arbeiten am [Arbeitsblatt](lehrgang-arbeitsblatt.md), und gemessen wird an den
Erfolgsmassen je Modul. Die Zeitfenster im Drehbuch sind Richtwerte, die der Pilot durch
Ist-Zeiten ersetzt.

## 7. Was nicht bekannt ist

- **Git-Anbindung:** ADR-0134 (Status «Landed») beschreibt eine Synchronisation über
  go-git. Im Code finden sich nur Export und Import des Quellbaums als tar.gz; eine
  Git-Bibliothek steht nicht in `go.mod`. Ob die Synchronisation bewusst entfallen ist
  oder fehlt, ist offen.
- **Fähigkeiten und Wertströme** (ADR-0305, «Partial»): keine Oberfläche gefunden, nur
  API und MCP.
- **Gemeinsamer Schulungsserver:** Der AD-Mockup-Schalter gilt für die ganze
  Installation; die Trennung je Person über verschiedene LDAP-URLs ist nicht erprobt.
- **Zeitangaben** sind Schätzungen.

## 8. Weitere Befunde ausserhalb dieses Plans

- `docs/compliance/isds-konzept.md`, Risiko R-02 («Kein TLS im Produkt»), entspricht
  nicht mehr dem Stand: Seit ADR-0191 kann Atlas TLS 1.3 selbst terminieren. Die
  Bewertung des Risikos ist Sache der Verantwortlichen des Dokuments und wurde
  deshalb nicht angepasst; eine vollständige Durchsicht des Dokuments wird empfohlen.
- `docs/compliance/zugriffsschutz-konzept.md`, Massnahme M9, nennt vier Rollen. Das ist
  der Stand zum Zeitpunkt der Massnahme und wurde als Aufzeichnung nicht verändert.
- **Playground-Regel ohne Treffer gilt als erfüllt.** Eine Regel, deren `when` keinen
  Fall auswählt (etwa wegen eines Tippfehlers), besteht; das Urteil vermerkt nur «no case
  of N matched», und `atlas playground` endet mit Exit-Code 0. ADR-0215 hat das bewusst
  so entschieden. Für den Lehrgang ist es ein Lernpunkt («jede Regel einmal rot sehen»).
  Vorschlag für eine Mindestzahl an Treffern je Regel, samt Meldung der Variablen, die kein
  Fall trägt: ADR-0424
  (`docs/adr/0424-a-playground-rule-can-demand-the-cases-it-speaks-about.md`, Status
  «Proposed»). Modul M4 wird nach dem Entscheid darüber geschrieben.
