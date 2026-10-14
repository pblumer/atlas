# `reisebuchung` — abhängige Formulare, auf zwei Ebenen

Ein durchgängiges Beispiel für das, was man umgangssprachlich „abhängige
Formulare" nennt: *ich wähle etwas, und davon hängt ab, was ich als Nächstes
sehe oder ausfüllen muss.* Der Kniff ist, dass darin **zwei völlig
verschiedene Dinge** stecken — und atlas löst sie an zwei verschiedenen Stellen.
Dieses Beispiel zeigt beide in **einem** Modell, und zwar als **öffentlichen
Wizard** ([ADR-0449](../../docs/adr/0449-a-wizard-is-a-subprocess-one-filler-walks-through.md)):
Eine Kundin ohne Konto füllt über einen öffentlichen Link die Reisedaten aus und
wird danach von Maske zu Maske geführt, ohne je eine Aufgabenliste zu sehen.

## Die zwei Ebenen

### Ebene A — Feld-Abhängigkeit *innerhalb* eines Formulars → Sache des Formulars

„Ich wähle eine Reiseart und kann dann ein Ziel aus einer Liste wählen" oder
„zeige Feld X nur, wenn …". Das passiert **im Browser, ohne Prozess-Roundtrip**.
Atlas nutzt das bpmn.io-**form-js**-Schema (ADR-0028); die Reaktivität steckt
im Formular, nicht in der Engine. In [`reise-start.form.json`](reise-start.form.json):

- **Kaskadierende Auswahl** — das Feld `ziel` bezieht seine Optionen dynamisch
  aus einem FEEL-Ausdruck über die anderen Feldwerte:

  ```json
  "valuesExpression": "=if reiseart = \"Fernreise\" then [ … ] else if reiseart = \"EU\" then [ … ] else [ … ]"
  ```

  Ändert sich `reiseart`, rechnet form-js die Zielliste live neu.

- **Bedingtes Feld** — `reisepassNr` erscheint nur bei Fernreisen:

  ```json
  "conditional": { "hide": "=reiseart != \"Fernreise\"" }
  ```

Die Engine sieht von alledem **nichts**. Sie speichert am Ende nur die
abgeschickten Werte als Variablen. Merksatz: **reine Anzeige-/Auswahllogik
gehört ins Formular.**

### Ebene B — Ablauf-Abhängigkeit *über mehrere Schritte* → Sache des Prozesses

„Wenn Fernreise, dann Visum-Formular; wenn minderjährig, zusätzlich eine
Einverständniserklärung." Das sind **verschiedene User-Tasks, die je nach Daten
aktiviert werden** — das gehört ins BPMN-Modell
([`reisebuchung.bpmn`](reisebuchung.bpmn)):

```
Start "Reise angefragt"            (Startformular reise-start; Ebene A lebt hier;
                                    erreichbar über einen öffentlichen Link)
 → Subprozess "Buchungsassistent"  (atlas:wizard="public" — EINE Sitzung)
      Script     "Alter berechnen"        (alter aus geburtsdatum, FEEL-Datumsmathematik)
      Rule-Task  "Pflichten ermitteln"    (DMN ReisePflichten → pflichten{…})
      Inclusive-Split ────────────────────────────────────────────────┐
           pflichten.brauchtVisum          → User-Task "Visum-Angaben"    │
           pflichten.brauchtEinverstaendnis→ User-Task "Einverständnis"   │
           sonst (Default) ───────────────────────────────────────────►  │
         Inclusive-Join  (wartet auf genau die geöffneten Zweige) ◄───────┘
      User-Task  "Buchung bestätigen"     (immer)
   Timer am Rand: 30 Minuten ohne Abschluss → Ende "Buchung verfallen"
 → Ende "Buchung abgeschlossen"
```

**Welche** Formulare Pflicht sind, entscheidet nicht der Code, sondern die
**DMN-Entscheidungstabelle** [`reisepflichten.dmn`](reisepflichten.dmn):

| reiseart          | alter  | brauchtVisum | brauchtEinverstaendnis |
|-------------------|--------|:------------:|:----------------------:|
| `"Fernreise"`     | `< 18` | true         | true                   |
| `"Fernreise"`     | `>= 18`| true         | false                  |
| `not("Fernreise")`| `< 18` | false        | true                   |
| `not("Fernreise")`| `>= 18`| false        | false                  |

Die Fachregeln leben in dieser Tabelle. Soll künftig auch für EU-Reisen ein
Visum nötig sein, ändert eine Fachperson **eine Zelle** — ohne Redeploy der
Engine (Invariante 5, „Compile, don't interpret"). Der Business-Rule-Task legt
das Ergebnis als Kontext in `pflichten` ab; die Inclusive-Gateways verzweigen
auf `pflichten.brauchtVisum` bzw. `pflichten.brauchtEinverstaendnis`.

Das Muster, das man sich merken sollte, ist immer dasselbe:
**DMN berechnet *was* nötig ist → Gateways routen darauf → User-Tasks tragen die
jeweiligen Formulare.**

### Der Wizard — eine Sitzung, eine Person

Der Subprozess trägt die Markierung `atlas:wizard="public"`. Sie sagt der
Laufzeit, dass seine Masken **eine Sitzung** der Person sind, die den
öffentlichen Link geöffnet hat:

- **Der Pass.** Der Start über den öffentlichen Link antwortet mit einem Pass
  `<instanceKey>.<secret>` und gleich mit der ersten Maske. Der Pass öffnet genau
  die offenen Masken dieser Sitzung und sonst nichts; die Instanz speichert nur
  seinen SHA-256.
- **Weiter von Maske zu Maske.** Jedes Absenden antwortet mit der nächsten Maske,
  mit `pending`, solange die Engine zwischen zwei Masken rechnet (hier die
  DMN-Auswertung), oder mit `done`. Die zwei *parallel* geöffneten Zweige
  (Visum + Einverständnis) erscheinen der einen Person als zwei Masken
  hintereinander.
- **Die Erklärung steht im Modell.** Die Dokumentation einer Maske
  (`<documentation>` am User-Task) liest die Besucherin als Erklärung mit, etwa
  „Dieser Schritt erscheint, weil die Entscheidungstabelle ReisePflichten …".
- **Was nur angezeigt wird, ist nicht beschreibbar.** Die Bestätigungsmaske
  zeigt Reiseart und berechnetes Alter in *deaktivierten* Feldern. Sie sind
  vorbelegt, aber der Server nimmt sie von der Besucherin nicht an; ein
  schreibgeschütztes (`readonly`) Feld dagegen schickt das Formular mit.
- **Die Antworten bleiben im Wizard.** Was nach der Sitzung bleiben soll, geben
  die **Output-Mappings** des Subprozesses nach aussen: `alter`, `pflichten`, die
  Visum- und Einverständnis-Angaben und `buchungBestaetigt`. Eine Maske, die
  niemand gesehen hat, liefert dort `null`; der Haken `visumBestaetigt` ist nicht
  gemappt und bleibt im Wizard.
- **Abbruch.** Der unterbrechende Timer am Subprozess beendet eine Sitzung, die
  30 Minuten lang nicht abgeschlossen wurde. Ohne ihn liesse sich der öffentliche
  Link gar nicht veröffentlichen: Jeder liegengelassene Versuch bliebe sonst eine
  lebende Instanz.

In der Tasks-App erscheinen die Masken eines öffentlichen Wizards in keinem
Posteingang. Sehen und bearbeiten können sie nur Operatoren und Administratoren.

## Die Dateien

| Datei | Rolle |
|-------|-------|
| [`reisebuchung.bpmn`](reisebuchung.bpmn) | der Prozess: Startformular, dann der öffentliche Wizard (mit handgezeichnetem Layout: gerade Hauptachse, Zweige oben und unten) |
| [`reisepflichten.dmn`](reisepflichten.dmn) | die Fachregeln (Entscheidungstabelle `ReisePflichten`) |
| [`reise-start.form.json`](reise-start.form.json) | Startformular am Startereignis — kaskadierende Auswahl (Ebene A) + bedingtes Feld |
| [`reise-visum.form.json`](reise-visum.form.json) | bedingte Maske (nur bei Fernreise) |
| [`reise-einverstaendnis.form.json`](reise-einverstaendnis.form.json) | bedingte Maske (nur bei Minderjährigen) |
| [`reise-bestaetigung.form.json`](reise-bestaetigung.form.json) | abschliessende Maske (immer), zeigt Reiseart und Alter in deaktivierten Feldern |

## Selbst ausführen

Die Reihenfolge ist: Projekt → Formulare → DMN hochladen + registrieren →
BPMN-Draft → Projekt deployen → öffentlichen Link veröffentlichen. Über MCP:

```
atlas_create_project      { name: "Reisebuchung" }                         → projectId
atlas_save_form           { id: "reise-start", schema: … , projectId }     (× 4 Formulare)
atlas_upload_decision_model { handle: "reisepflichten", xml: <dmn> }
atlas_register_decision   { name: "ReisePflichten", modelRef: "reisepflichten", projectId }
atlas_save_draft          { xml: <reisebuchung.bpmn>, projectId }
atlas_deploy_project      { id: projectId }                                → definitionKey
```

Ein Business-Rule-Task lehnt den Deploy ab, wenn seine `calledDecision` nicht
als DMN-Referenz registriert ist — deshalb kommen `upload` + `register` **vor**
dem Deploy.

Den öffentlichen Link veröffentlicht die Console (Prozess → Öffentlicher Link)
oder ein Aufruf:

```
POST /api/v1/public-links   {"processId": "proc_reisebuchung"}   → {"token": …, "url": "/public/forms/<token>"}
```

Danach füllt jede Person ohne Konto die Buchung aus, entweder auf der
allgemeinen öffentlichen Seite `/public/forms/<token>` oder im
Kunden-Assistenten unten. Ohne Browser sind es dieselben vier Aufrufe, die beide
Seiten machen:

```
GET  /public/forms/<token>/schema                                    → Startformular
POST /public/forms/<token>/start   {"variables": {reiseart, ziel, geburtsdatum, …}}
                                   → {"started": true, "wizard": {"pass": …, "state": "step", "step": {…}}}
POST /public/wizard/steps/<taskKey>/complete   (Authorization: Bearer <pass>)
                                   → die nächste Maske, "pending" oder "done"
GET  /public/wizard/step                       (Authorization: Bearer <pass>)
                                   → wo die Sitzung steht (nach "pending" oder einem Neuladen)
```

## Zwei Durchläufe zum Nachvollziehen

Beide laufen als Test gegen genau diese Dateien, als anonyme Besucherin des
öffentlichen Links: `TestTheTripBookingExample…` in
[`api/reisebuchung_example_test.go`](../../api/reisebuchung_example_test.go).
Der Stichtag für das Alter ist im Script fest der 8. August 2026, solange keine
Variable `stichtag` gesetzt ist.

**A) Fernreise + minderjährig** (`reiseart: "Fernreise"`, `geburtsdatum:
"2010-05-01"` ⇒ `alter = 16`). Die DMN matcht Zeile 1 → `brauchtVisum = true`,
`brauchtEinverstaendnis = true`. Der Inclusive-Split öffnet **beide** Zweige;
es parken gleichzeitig die Tasks *Visum-Angaben* und *Einverständnis*. Die
Besucherin sieht sie als zwei Masken hintereinander. Danach synchronisiert der
Inclusive-Join beide und es folgt *Buchung bestätigen*, mit Reiseart und Alter
16 als deaktivierte Felder. Element-Visits danach (`instances: 0, tokens: 0`):

| Element | Typ | Visits |
|---------|-----|:------:|
| `split` | InclusiveGateway | 1 |
| `visum` · `einverstaendnis` | UserTask | 1 · 1 |
| `join` | InclusiveGateway | **2** (ein Besuch je geöffnetem Zweig) |
| `bestaetigen` · `end` | UserTask · EndEvent | 1 · 1 |

`join = 2` (nicht 3) beweist: der Inclusive-Join wartete auf **genau die zwei
geöffneten** Zweige — der `sonst`-Default wurde korrekt unterdrückt.

**B) Inland + volljährig** (`reiseart: "Inland"`, `geburtsdatum: "1990-03-15"`
⇒ `alter = 36`). Die DMN matcht Zeile 4 → beides `false`. Der Split nimmt den
`sonst`-Default; **kein** Zusatzformular erscheint, die erste Maske nach dem
Startformular ist direkt *Buchung bestätigen*. Genau dieselbe
Prozessdefinition, anderes Verhalten — gesteuert allein durch die Daten und die
Entscheidungstabelle.

## Kunden-Assistent: Mehrschritt ohne Konto und ohne Aufgabenliste

Jeder Schritt ist unter der Haube ein eigener `userTask` (durabler Checkpoint) —
aber eine Endkundin soll **keine** Aufgabenliste sehen, sich nicht anmelden
müssen und nicht spürbar „auf den nächsten Task wechseln". Das leistet schon die
allgemeine öffentliche Seite `/public/forms/<token>`. Wie eine eigene Seite
dasselbe macht, zeigt
[`../../api/web/reisebuchung-kunde.html`](../../api/web/reisebuchung-kunde.html):
ein schlanker **Assistent**, der das Startformular holt, startet und danach
jeder Antwort des Wizards folgt. Er rendert jede Maske **an derselben Stelle**,
zeigt ihre Dokumentation als Erklärung, wartet bei `pending` und protokolliert
jeden Aufruf (ohne den Pass).

Der Vertrag zum Server sind die vier öffentlichen Aufrufe oben; **welche** Maske
als Nächstes kommt, entscheidet die Engine (DMN + Gateways), und der Assistent
rät nichts. Er braucht kein Konto und kein Cookie: Der Pass liegt im
`sessionStorage` des Tabs und geht nur im `Authorization`-Header mit, so dass ein
Neuladen die Sitzung fortsetzt. Dasselbe Muster funktioniert auf jeder Website,
deren Origin der Betreiber in `--public-forms-cors` nennt
([ADR-0186](../../docs/adr/0186-embed-public-forms-cross-origin.md)), wenn sie
dieselben Aufrufe an die Adresse von atlas richtet. Die Seite
rendert die echten form-js-Formulare, also funktionieren die kaskadierende
Zielauswahl und das bedingte Reisepass-Feld darin live.

Weil `api/web/` in die Server-Binary **einkompiliert** wird (`//go:embed web`),
muss der Server **neu gebaut und gestartet** werden; danach unter
`/reisebuchung-kunde.html?link=<token>` öffnen. Voraussetzung: der Prozess
`proc_reisebuchung` ist deployed und sein öffentlicher Link veröffentlicht. Die
Verdrahtung der Seite prüft
[`../../e2e/reisebuchung-wizard.spec.mjs`](../../e2e/reisebuchung-wizard.spec.mjs).

Die zwei „Ebene A"-Verhalten des Startformulars (kaskadierende Auswahl +
bedingtes Feld) sind gegen das echte form-js-Bundle durch einen Browser-Test
abgesichert: [`../../e2e/reise-form.spec.mjs`](../../e2e/reise-form.spec.mjs)
(Harness: `e2e/reise-form-harness.html`) — `cd e2e && npm ci && npx playwright test reise-form.spec.mjs`.

## Wann welche Ebene?

- Hängt die Auswahl/Sichtbarkeit **nur von anderen Feldern desselben
  Formulars** ab und braucht **keine** serverseitige Berechnung, Berechtigung
  oder Persistenz zwischendurch? → **Ebene A** (form-js `valuesExpression` /
  `conditional.hide`). Kein Gateway, kein Redeploy.
- Hängt der **Ablauf** davon ab — andere Menschen, andere Schritte, geprüfte
  Regeln, ein Audit-Trail, Wiederaufnahme nach Tagen? → **Ebene B** (BPMN
  Gateways + DMN). Die Entscheidung wird als Fakt persistiert
  (`atlas_instance_decisions` zeigt Eingaben, Ausgaben und die getroffene
  Regel je Instanz).

Die Kunst ist, beides **nicht** zu vermischen: Anzeigelogik ins Formular,
Ablauflogik in den Prozess, Fachregeln in DMN.

Dieses Beispiel modelliert den **Fall B** (echte Prozessschritte). Die
**Fall-A**-Variante — dieselbe Fachlichkeit, aber als *ein* Formular in *einem*
Prozessschritt, im Client geblättert — liegt daneben in
[`fall-a/`](fall-a/), inklusive eigenem Kunden-Assistenten
(`api/web/reisebuchung-einschritt-kunde.html`, „ein start, ein complete").
