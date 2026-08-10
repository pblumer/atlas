# Fall A — ein Formular, ein Prozessschritt, im Client geblättert

Das Gegenstück zum Hauptbeispiel [`../`](../) (Fall B). Beide lösen „abhängige
Formulare", aber an **verschiedenen Stellen** — und genau diese Wahl ist der
Punkt.

|  | **Fall B** (`../reisebuchung.bpmn`) | **Fall A** (hier) |
|---|---|---|
| Prozess | mehrere `userTask`s, DMN + Inclusive-Gateways verzweigen | **ein** `userTask` |
| „welches Formular als Nächstes?" | die **Engine** entscheidet (persistiert, auditierbar) | der **Client** entscheidet (reine UI) |
| Abgaben an Atlas | eine `complete` pro Schritt (hier: 4) | **eine** `complete`, ganz am Ende |
| Wann | verschiedene Akteure, Warten/Genehmigung dazwischen, tagelang wiederaufnehmbar | dieselbe Person, eine Sitzung, atomar |

## Das Modell

[`reise-antrag-einschritt.bpmn`](reise-antrag-einschritt.bpmn):

```
Start → User-Task "Reiseantrag erfassen" (Formular reise-antrag)
      → Script "Antrag zusammenfassen"
      → Ende
```

Ein einziger menschlicher Schritt. Die gesamte Abhängigkeit steckt im **einen**
Formular [`reise-antrag.form.json`](reise-antrag.form.json):

- **kaskadierende Auswahl** — `ziel` bezieht seine Optionen per
  `valuesExpression` aus `reiseart`;
- **Fernreise-Felder** (`reisepassNr`, `staatsangehoerigkeit`) —
  `conditional.hide: =reiseart != "Fernreise"`;
- **Minderjährigen-Felder** (`vertreterName`, `einverstaendnis`) — versteckt per
  FEEL-**Datumsvergleich** über dem Geburtsdatum:
  `=(geburtsdatum = null) or (date(geburtsdatum) <= date("2008-08-08"))`
  (der 2008-08-08 ist der 18. Geburtstag bezogen auf den Stichtag 2026-08-08;
  die `null`-Klausel verhindert einen Fehler, solange nichts eingetragen ist).

Alles davon läuft **im Browser** (form-js/feelin). Die Engine sieht davon nichts
und bekommt am Ende genau eine Abgabe mit allen Feldern.

## Der Kunden-Assistent (im Client geblättert)

[`../../../api/web/reisebuchung-einschritt-kunde.html`](../../../api/web/reisebuchung-einschritt-kunde.html)
lädt dieses **eine** Schema und teilt es clientseitig in Seiten auf (Reise →
Person → Fernreise-Nachweise* → Begleitung* → Bestätigung; die mit \* nur, wenn
sie zutreffen). Die Seiten-Reihenfolge lebt **im Client** — das ist bei Fall A
korrekt, denn die Mehrstufigkeit ist reine UI. Erst der letzte Klick löst die
**eine** `POST /tasks/{key}/complete` aus. Der HTTP-Log der Seite zeigt genau
das: ein `start`, ein `complete`.

> Weil `api/web/` in die Server-Binary einkompiliert wird (`//go:embed web`),
> muss der Server neu gebaut/gestartet werden; danach unter
> `/reisebuchung-einschritt-kunde.html` öffnen (Prozess `proc_reise_antrag`
> deployed vorausgesetzt).

## Live verifiziert (`0.1.0-dev`)

Eine Instanz, mit **einer** `complete` (Fernreise + minderjährig, alle Felder):

| Element | Typ | Visits |
|---|---|:--:|
| `start` | StartEvent | 1 |
| `erfassen` | **UserTask** | **1** |
| `zusammenfassen` | ScriptTask | 1 |
| `end` | EndEvent | 1 |

`erfassen = 1` ist der ganze Beweis: **ein** menschlicher Schritt trägt das
komplette, in sich abhängige Formular. Nach dem Absenden lief der Script-Task und
schrieb `zusammenfassung = {reiseart, ziel, mitVertreter}`, dann endete die
Instanz.

## Selbst ausführen (MCP)

```
atlas_create_project { name: "Reiseantrag (Fall A)" }                → projectId
atlas_save_form      { id: "reise-antrag", schema: …, projectId }
atlas_save_draft     { xml: <reise-antrag-einschritt.bpmn>, projectId }
atlas_deploy_project { id: projectId }                               → definitionKey
atlas_create_instance{ key: definitionKey }
atlas_list_tasks     { processInstance: … }        → der eine Task-Key
atlas_complete_task  { key: …, variables: { reiseart, ziel, geburtsdatum, … } }
```

Die zwei „im Formular"-Verhalten (Kaskade + bedingte Felder inkl.
Minderjährigen-Datumslogik) sind gegen das echte form-js-Bundle durch einen
Browser-Test abgesichert:
[`../../../e2e/reise-antrag.spec.mjs`](../../../e2e/reise-antrag.spec.mjs)
(Harness: `e2e/reise-antrag-harness.html`) —
`cd e2e && npm ci && npx playwright test reise-antrag.spec.mjs`.
