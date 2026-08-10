# Öffentliche Variante — Start-Formular via `/public/forms/{token}` (ADR-0029)

Fall A, aber für den **unauthentifizierten Erstkontakt**: das Intake-Formular
hängt am **Start-Event** und wird über einen opaken, widerrufbaren Token
veröffentlicht. Ein Besucher ohne Account öffnet die URL, füllt das Formular aus
(Kaskade + bedingte Felder laufen im Browser) und startet damit eine echte
Instanz — die Formulardaten werden zu **Start-Variablen**.

Das ist die richtige Antwort auf „die Tasks-App ist nicht für Endkunden": der
**erste** Schritt ist öffentlich, danach übernimmt der Prozess (hier ein
Script-Task; in echt: interne Bearbeitung).

## Das Modell

[`reise-antrag-public.bpmn`](reise-antrag-public.bpmn):

```
Start (Formular reise-antrag)  →  Script "Antrag aufnehmen"  →  Ende
```

Das Start-Event bindet das Formular über
`<zeebe:formDefinition formId="reise-antrag"/>` — **dasselbe** Intake-Formular wie
in [`../fall-a/`](../fall-a/), nur an einem anderen Ort. Der Compiler nimmt die
erste solche Bindung an einem Root-Start-Event als `StartFormId` des Prozesses
(ADR-0028); genau das ist die Voraussetzung, um ihn zu veröffentlichen. Die
Formulardaten landen als Start-Variablen, der Script-Task rechnet darauf (z. B.
`minderjaehrig`).

## Veröffentlichen und aufrufen

Drei Routen, zwei Vertrauensstufen (siehe `api/publiclinks.go`):

```bash
BASE=https://<dein-atlas>

# 1) Token minten — VERTRAUT (gated mit /api/v1, wenn Auth an ist). Idempotent
#    pro processId; verlangt, dass der Prozess ein Start-Formular hat.
curl -s -X POST $BASE/api/v1/public-links \
  -H 'Content-Type: application/json' \
  -d '{"processId":"proc_reise_antrag_public"}'
# → { "token":"<opak>", "processId":"…", "formId":"reise-antrag",
#     "url":"/public/forms/<token>" }

# 2) ÖFFENTLICH, unauthentifiziert — die eingebaute Seite (web/public-form.html):
#    im Browser öffnen:
$BASE/public/forms/<token>

#    …die zwei JSON-Endpunkte, die diese Seite nutzt:
curl -s   $BASE/public/forms/<token>/schema         # → { processName, schema }
curl -s -X POST $BASE/public/forms/<token>/start \
  -H 'Content-Type: application/json' \
  -d '{"variables":{"reiseart":"Fernreise","ziel":"th","geburtsdatum":"2010-05-01","reisepassNr":"C01X00T47","staatsangehoerigkeit":"deutsch","vertreterName":"Erika Mustermann","einverstaendnis":true,"buchungBestaetigt":true}}'
# → { "started": true }   (liest NICHTS zurück — keine Instanz-/Task-Keys)

# 3) Widerrufen — killt die URL:
curl -s -X DELETE $BASE/api/v1/public-links/<token>
```

Die eingebaute Seite `/public/forms/<token>` rendert das Schema mit **form-js**
(`new Form(...).importSchema(schema)`) und schickt beim Absenden
`{ "variables": <Formulardaten> }` an `…/start`. Kein eigener Client nötig — der
Reiseantrag mit kaskadierender Zielauswahl und den bedingten Feldern erscheint
dort unmittelbar.

## Sicherheitshaltung (ADR-0029)

- **Least Authority.** Der Token startet **genau** diesen Prozess über **genau**
  dieses Start-Formular — er liest keinen Zustand, listet keine Instanzen, greift
  auf keine andere API zu. `/start` liefert nur `{started:true}` zurück.
- **Opt-in & widerrufbar.** Public-Access ist standardmäßig aus; Veröffentlichen
  mintet den Token, `DELETE …/public-links/{token}` macht die URL sofort tot.
- **Missbrauchsschutz.** Die `/public/`-Endpunkte sind rate-limitiert und
  payload-gecappt (256 KiB); wie `/mcp` gehört die Fläche hinter einen
  authentifizierenden Reverse-Proxy, wenn sie ins Internet zeigt.
- **Gleiche Engine-Invarianten.** Der öffentliche Start geht durch denselben
  Single-Writer-Pfad wie jeder andere (ADR-0002/0016) — nur *wer* aufruft ist
  anders, nicht *wie* die Engine berührt wird (durable before visible, I2).

## Öffentlich vs. authentifiziert — wann was

| | Öffentliches Start-Formular (hier) | Kunden-Assistent ([`../fall-a/`](../fall-a/), [`../`](../)) |
|---|---|---|
| Auth | **keine** | angemeldeter Kundenbereich (`/api/v1/tasks`) |
| Umfang | nur der **Start** (Erstkontakt) | beliebig viele Schritte |
| Datenfluss | Formular → **Start-Variablen** | Formular → Task-Completion-Variablen |
| Muster | öffentliches Intake (Signup, Anfrage, Antrag) | interne/kundengebundene Weiterbearbeitung |

Ein rein **öffentlicher Mehrschritt**-Flow ist bewusst nicht vorgesehen (das wäre
eine offene Tür): entweder **ein** reiches Start-Formular (Fall A, hier) — oder,
wenn wirklich mehrere öffentliche Schritte nötig sind, per korrelierten
Nachrichten/Tokens modellieren.

## Verifiziert

- **Prozessseite, live gegen `0.1.0-dev`:** eine Instanz gestartet mit den
  Formulardaten als **Start-Variablen** (der exakte `handlePublicFormStart`-Effekt:
  `parseStartVariables` → `CreateInstance`) läuft self-completing `start → aufnehmen
  → end`; alle Felder als Variablen übernommen, der Script leitete
  `aufnahme = {reiseart, ziel, minderjaehrig: true}` ab.
- **HTTP-Schicht:** die Public-Link-Endpunkte sind im Repo getestet
  (`api/publiclinks_test.go`, `api/publiclinks_internal_test.go`,
  `api/executable_test.go` mintet einen Link und startet über
  `/public/forms/{token}/start`).
- **Rendering:** die eingebaute `web/public-form.html` nutzt denselben form-js-Pfad
  wie der Kunden-Assistent; das Intake-Schema ist durch
  [`../../../e2e/reise-antrag.spec.mjs`](../../../e2e/reise-antrag.spec.mjs)
  gegen das echte Bundle abgesichert.

## Selbst deployen (MCP)

```
atlas_create_project { name: "Reiseantrag (öffentlich)" }               → projectId
atlas_save_form      { id: "reise-antrag", schema: …, projectId }        (das Start-Formular)
atlas_save_draft     { xml: <reise-antrag-public.bpmn>, projectId }
atlas_deploy_project { id: projectId }                                    → definitionKey
```

Danach den Token über `POST /api/v1/public-links {"processId":"proc_reise_antrag_public"}`
minten (kein MCP-Tool dafür — vertraute HTTP-Route) und
`/public/forms/<token>` öffnen.
