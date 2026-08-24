# `reisebuchung` — abhängige Formulare, auf zwei Ebenen

Ein durchgängiges Beispiel für das, was man umgangssprachlich „abhängige
Formulare" nennt: *ich wähle etwas, und davon hängt ab, was ich als Nächstes
sehe oder ausfüllen muss.* Der Kniff ist, dass darin **zwei völlig
verschiedene Dinge** stecken — und Atlas löst sie an zwei verschiedenen Stellen.
Dieses Beispiel zeigt beide in **einem** Modell.

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
Start
 → User-Task  "Reisedaten erfassen"   (Formular reise-start; Ebene A lebt hier)
 → Script     "Alter berechnen"        (alter aus geburtsdatum, FEEL-Datumsmathematik)
 → Rule-Task  "Pflichten ermitteln"    (DMN ReisePflichten → pflichten{…})
 → Inclusive-Split ────────────────────────────────────────────────┐
      pflichten.brauchtVisum          → User-Task "Visum-Angaben"    │
      pflichten.brauchtEinverstaendnis→ User-Task "Einverständnis"   │
      sonst (Default) ───────────────────────────────────────────►  │
    Inclusive-Join  (wartet auf genau die geöffneten Zweige) ◄───────┘
 → User-Task  "Buchung bestätigen"     (immer)
 → Ende
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

## Die Dateien

| Datei | Rolle |
|-------|-------|
| [`reisebuchung.bpmn`](reisebuchung.bpmn) | der Prozess (mit handgezeichnetem Layout: gerade Hauptachse, Zweige in eigenen Lanes) |
| [`reisepflichten.dmn`](reisepflichten.dmn) | die Fachregeln (Entscheidungstabelle `ReisePflichten`) |
| [`reise-start.form.json`](reise-start.form.json) | Startformular — kaskadierende Auswahl (Ebene A) + bedingtes Feld |
| [`reise-visum.form.json`](reise-visum.form.json) | bedingter User-Task (nur bei Fernreise) |
| [`reise-einverstaendnis.form.json`](reise-einverstaendnis.form.json) | bedingter User-Task (nur bei Minderjährigen) |
| [`reise-bestaetigung.form.json`](reise-bestaetigung.form.json) | abschließender User-Task (immer) |

## Selbst ausführen (MCP)

Die Reihenfolge ist: Projekt → Formulare → DMN hochladen + registrieren →
BPMN-Draft → Projekt deployen.

```
atlas_create_project      { name: "Reisebuchung" }                         → projectId
atlas_save_form           { id: "reise-start", schema: … , projectId }     (× 4 Formulare)
atlas_upload_decision_model { handle: "reisepflichten", xml: <dmn> }
atlas_register_decision   { name: "ReisePflichten", modelRef: "reisepflichten", projectId }
atlas_save_draft          { xml: <reisebuchung.bpmn>, projectId }
atlas_deploy_project      { id: projectId }                                → definitionKey
atlas_create_instance     { key: definitionKey }
atlas_list_tasks          { processInstance: … }        → Task-Key des Startformulars
atlas_complete_task       { key: …, variables: { reiseart, ziel, geburtsdatum, … } }
```

Ein Business-Rule-Task lehnt den Deploy ab, wenn seine `calledDecision` nicht
als DMN-Referenz registriert ist — deshalb kommen `upload` + `register` **vor**
dem Deploy.

## Zwei Durchläufe zum Nachvollziehen

Beide wurden gegen den laufenden Server (`0.1.0-dev`) verifiziert.

**A) Fernreise + minderjährig** (`reiseart: "Fernreise"`, `geburtsdatum:
"2010-05-01"` ⇒ `alter = 16`). Die DMN matcht Zeile 1 → `brauchtVisum = true`,
`brauchtEinverstaendnis = true`. Der Inclusive-Split öffnet **beide** Zweige;
es parken gleichzeitig die Tasks *Visum-Angaben* und *Einverständnis*. Nach dem
Ausfüllen synchronisiert der Inclusive-Join beide und es folgt *Buchung
bestätigen*. Element-Visits danach (`instances: 0, tokens: 0`):

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
`sonst`-Default; **kein** Zusatzformular erscheint, der Token geht direkt zu
*Buchung bestätigen*. Genau dieselbe Prozessdefinition, anderes Verhalten —
gesteuert allein durch die Daten und die Entscheidungstabelle.

## Kunden-Assistent: Mehrschritt ohne Aufgabenliste

Jeder Schritt ist unter der Haube ein eigener `userTask` (durabler Checkpoint) —
aber ein Endkunde soll **keine** Aufgabenliste sehen und nicht spürbar „auf den
nächsten Task wechseln". Genau das zeigt
[`../../api/web/reisebuchung-kunde.html`](../../api/web/reisebuchung-kunde.html):
ein schlanker, same-origin **Assistent**, der nach jedem Absenden sofort den
nächsten Task derselben Instanz holt und dessen Formular **an derselben Stelle**
rendert. Der Effekt ist ein nahtloser Wizard über durable Prozessschritte; selbst
die zwei *parallel* geöffneten Zweige (Visum + Einverständnis) erscheinen dem
einen Nutzer einfach als zwei aufeinanderfolgende Schritte.

Der Vertrag zum Server sind vier Aufrufe — `start instance` · `get next task` ·
`get form` · `complete task`; **welches** Formular als Nächstes kommt, entscheidet
die Engine (DMN + Gateways). Die Seite rendert die echten form-js-Formulare, also
funktionieren die kaskadierende Zielauswahl und das bedingte Reisepass-Feld darin
live.

Weil `api/web/` in die Server-Binary **einkompiliert** wird (`//go:embed web`),
muss der Server **neu gebaut und gestartet** werden; danach unter
`/reisebuchung-kunde.html` öffnen. Voraussetzung: der Prozess `proc_reisebuchung`
ist deployed (dieses Beispiel).

> **Grenze der Öffentlichkeit:** `/api/v1/tasks` liegt hinter der (optionalen)
> Auth des Servers — der Assistent ist also das Muster für einen *angemeldeten*
> Kundenbereich. Ein völlig unauthentifizierter Erstkontakt läuft über das
> **öffentliche Startformular** (ADR-0029, `/public/forms/{token}`); ein rein
> öffentlicher Mehrschritt-Flow braucht dann entweder **ein** reiches
> Startformular (Fall A unten) oder korrelierte öffentliche Schritte.

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
