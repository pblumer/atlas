# Atlas examples

Runnable BPMN models that double as showcases of what the Atlas engine can do
and as deterministic test scenarios. Every request/response below was verified
against a live Atlas server (`0.1.0-dev`).

| File | What it is |
|------|-----------|
| [`pruefe-auftrag.bpmn`](pruefe-auftrag.bpmn) | The minimal runnable scenario: `Start → Script Task "prüfe den Auftrag" → Ende`. A single inline FEEL script releases an order at/below the 1000 € approval limit and flags larger ones. Self-completing, no external worker. |
| [`pruefe-datensaetze.bpmn`](pruefe-datensaetze.bpmn) + [`pruefe-datensaetze.dmn`](pruefe-datensaetze.dmn) + [`csv-upload-form.json`](csv-upload-form.json) + [`row-correction-form.json`](row-correction-form.json) | CSV batch validation, **fully in-process** (ADR-0084 + ADR-0086 + ADR-0087): the instance starts and parks at a **"CSV hochladen" user task** — the Quality Manager uploads the file in its form (the Tasks app submits the content as `csvText`). A **script task** holds the column layout (`columnConfig`); a **CSV-import service task** (`io.atlas.csv-import`) parses it into `rows`; then a **multi-instance subprocess** runs a **business rule task** per row against the DMN decision `RowValid` (email/group/licence). A valid row collects its `verdict`; an invalid one parks on a **correction user task**, and once corrected it re-validates (loop until valid). Self-completing; no ingestion endpoint or separate screen. |
| [`order-fulfillment.bpmn`](order-fulfillment.bpmn) | A self-completing order-fulfillment process that exercises inline scripts and all three gateway kinds, and drives itself to an end event with **no external workers attached**. |
| [`cart-total.bpmn`](cart-total.bpmn) | A shopping-cart checkout that computes an order total (subtotal → rebate → VAT → shipping) entirely in inline FEEL, and routes on the computed sum. Self-completing. |
| [`order-to-cash.bpmn`](order-to-cash.bpmn) | The full order lifecycle: cart calculation → approval (≥ 100 €, user task) → **parallel** delivery & billing (service tasks). Parks on the worker-backed steps — a realistic, not-fully-automatic process. |
| [`order-to-cash-app.html`](order-to-cash-app.html) | A self-contained single-page app that mirrors `order-to-cash.bpmn`: edit the cart, watch the sum compute live, approve, and clear the delivery/billing tasks. No server needed — open it in a browser. |
| [`order-to-cash-live.bpmn`](order-to-cash-live.bpmn) | The **live** sibling: same flow, but delivery/billing are `userTask`s (HTTP-completable) instead of `serviceTask`s, so a browser app can drive the real instance end to end. |
| [`../api/web/order-to-cash-live.html`](../api/web/order-to-cash-live.html) | The **live** app — served by the Atlas server itself (`/order-to-cash-live.html`). It deploys the model, starts a real instance with your cart, and completes each task over the HTTP API. No business logic in the client; Atlas runs the process. |
| [`../api/web/order-to-cash-jobs.html`](../api/web/order-to-cash-jobs.html) | The **live** app against the *service-task* model: it lists the parked jobs (`GET /instances/{key}/jobs`) and completes them (`POST /jobs/{key}/complete`) — the app acts as the job worker. |
| [`galsync.bpmn`](galsync.bpmn) | Cross-forest Global Address List sync: a DirSync delta over the source forest, then a contact created, updated or removed in the target one, looping on a timer with the cookie carried in a variable. MIM ships this as a management agent; here it is a *process*, because GALSync is a policy rather than a protocol. Der Lookup „gibt es den Kontakt schon?" ist seit [ADR-0166](../docs/adr/0166-active-directory-connector.md) (fünfte Ergänzung) derselbe AD-Worker-Typ, der ihn auch schreibt — vorher musste dafür der generische LDAP-Konnektor an denselben Forest gebunden werden. |
| [`ad-gruppenzuweisung.bpmn`](ad-gruppenzuweisung.bpmn) | **Benutzer einer AD-Gruppe zuweisen** (ADR-0166, fünfte Ergänzung): das Beispiel zur AD-Suche. Zwei `atlas:adConnector`-Tasks mit `operation="search"` suchen erst das Konto über seinen `sAMAccountName`, dann die Gruppe über ihren `cn`; Exklusiv-Gateways verzweigen auf `=benutzer.found` bzw. `=gruppe.found`, ein Zweig legt die fehlende Gruppe an, und `add-group-member` setzt die Mitgliedschaft. Der Lehrsatz ist, **warum die Suche überhaupt nötig ist**: eine Mitgliedschaft adressiert die Gruppe über ihren Distinguished Name, und ein DN ist eine Position im Baum — nichts, was eine Fachabteilung mitliefert. Das Ergebnis `{found, count, dn, entries}` trennt beides sauber: `found` ist die Verzweigung, `dn` die Adresse für den nächsten Task. Nichts gefunden ist eine Antwort und kein Incident (Pfad „Benutzer unbekannt"), `maxEntries="1"` macht einen zweiten Treffer zum Befund statt zum Zufall. Parkt auf jedem AD-Task, bis eine Worker-Instanz für den Worker-Typ `ad` läuft — mit `ATLAS_AD_MOCK` läuft das Modell komplett gegen ein Verzeichnis im Arbeitsspeicher (ADR-0181). |
| [`entra-create-account.bpmn`](entra-create-account.bpmn) | A PowerShell `jobScript` task that creates an EntraID account — the *worker-backed* counterpart: its token parks on the script job until a PowerShell script worker runs it. |
| [`entra-konten-rezertifizierung.bpmn`](entra-konten-rezertifizierung.bpmn) | **Konten-Rezertifizierung (Entra ID)** (ADR-0172, amended): das Gegenstück zum Anlegen — hier *befragt* ein Prozess das Verzeichnis. Ein `atlas:entraConnector` mit `list-users` holt die aktiven Konten eines Bereichs (der `$filter` ist FEEL, also listet der Prozess den Bereich auf, um den es gerade geht), ein Inline-FEEL-Skript gleicht sie gegen die UPN-Liste aus dem Personalsystem ab, und eine **parallele Multi-Instance** deaktiviert je verwaistes Konto. Der Lehrsatz steht als das im Diagramm, was *nicht* darin steht: **nirgends wird geblättert**. Eine Graph-Collection kommt seitenweise hinter einem `@odata.nextLink`, aber der Worker folgt den Seiten selbst — deshalb ist `konten` eine Liste und der Abgleich ein einzelner Ausdruck statt einer Schleife über einen Continuation-Token. Parkt auf beiden Entra-Tasks, bis eine Worker-Instanz mit `--connector entra` läuft (der Worker-Typ `entra` ist worker-only, damit das Tenant-Credential nie in die Engine gelangt). |
| [`entra-team-onboarding.bpmn`](entra-team-onboarding.bpmn) | **Team-Onboarding (Entra ID)** (ADR-0172, amended): das dritte Entra-Beispiel — hier *baut* ein Prozess einen Arbeitsplatz auf. Eine gerade Kette von `atlas:entraConnector`-Tasks legt Konto (`create-user` → `konto`), Lizenz (`assign-license`), Projektgruppe (`create-group` → `gruppe`), Mitgliedschaft (`add-group-member`), Team (`create-team`) und Team-Mitgliedschaft (`add-team-member`) an. Der rote Faden ist die id-Kette: alles adressiert über `=konto.id`/`=gruppe.id`, und weil die id eines Teams die seiner M365-Gruppe *ist*, nehmen die Team-Schritte dieselbe `=gruppe.id` — kein zweites Team-Feld. Das Anfangspasswort steht im `passwordProfile` einer Startvariablen, nie im Diagramm. Parkt auf jedem Task, bis eine Worker-Instanz mit `--connector entra` läuft. |
| [`entra-delta-sync.bpmn`](entra-delta-sync.bpmn) | **Delta-Sync (Entra ID)** (ADR-0172, amended): das vierte Entra-Beispiel — hier liest ein Prozess *nur, was sich geändert hat*. Ein `atlas:entraConnector` mit `delta-users` liest ab dem Cursor `letzterDeltaLink` (leer beim ersten Lauf) die geänderten und entfernten Benutzer nach `aenderungen` — ein Objekt `{ value, deltaLink }`. Der Lehrsatz ist, **wo der Cursor lebt**: nicht im Worker (der ist zustandslos, worker-only), sondern im Prozess. Der neue `=aenderungen.deltaLink` wird persistiert und beim nächsten Lauf als `letzterDeltaLink` wieder hereingereicht, genau wie [`galsync.bpmn`](galsync.bpmn) den DirSync-Cookie durch eine Variable reicht. Gelöschte Objekte kommen in `value` mit `@removed` markiert mit — ein Leaver-Flow verliert sie so nicht. Parkt auf dem Entra-Task, bis eine Worker-Instanz mit `--connector entra` läuft. |
| [`mssql-eintrittsmeldung.bpmn`](mssql-eintrittsmeldung.bpmn) | **Eintrittsmeldung (MS SQL)** ([ADR-0173](../docs/adr/0173-generic-sql-connector.md)): das Beispiel zum Datenbank-Worker. Ein `atlas:mssqlConnector` mit `operation="query-one"` schlägt die Person über ihr Kürzel nach, ein Exklusiv-Gateway verzweigt auf `= person != null`, und ein zweiter mit `operation="execute"` setzt sie aktiv. Der Lehrsatz ist, **warum die Anweisung literal ist**: Das `statement`-Feld hat als einziges bewusst keinen `fx`-Schalter, denn eine aus Prozessdaten zusammengesetzte Anweisung wäre eine Injection, die keinen Anführungszeichenfehler mehr bräuchte. Werte kommen deshalb über `parametersVariable` herein — eine **Liste** bindet der Reihe nach (`@p1`), ein **Objekt** über die Namen (`@id`), und Namensbindung kann nur SQL Server: MariaDB und PostgreSQL lehnen ein Objekt ab, statt es in eine Reihenfolge zu bringen, die niemand geschrieben hat. Deshalb sind die drei Produkte drei Worker-Typen und kein Auswahlfeld. `query-one` liefert `null` statt einer leeren Liste und scheitert bei zwei Treffern — „die Zeile zu dieser Person", die zweimal passt, ist eine falsche Annahme und kein Ergebnis. Parkt auf jedem SQL-Task, bis eine Worker-Instanz für den Worker-Typ `mssql` läuft; einen In-Process-Fallback gibt es hier als einzigem Worker-Typ nicht, weil die Verbindungszeichenfolge die Zugangsdatei selbst ist und die Engine sie nie halten soll. Mit `ATLAS_MSSQL_MOCK` läuft das Modell komplett gegen hinterlegte Antworten im Arbeitsspeicher des Workers. |
| [`blick-schlagzeilen.bpmn`](blick-schlagzeilen.bpmn) | **Schlagzeilen aus einem RSS-Feed** (ADR-0118 + ADR-0190): der Ausbau des Web-Scraping-Workers in einem Diagramm — derselbe Task, der HTML per CSS-Selektor ausliest, holt mit `format="rss"` den Feed von Blick.ch und schreibt **strukturierte Einträge** (`title`, `link`, `description`, `published`) statt Strings nach `schlagzeilen`; `maxItems="15"` ist die Schranke ohne Folge-Skript. Der Lehrsatz ist, **wer das Format wählt**: das Modell beim Deployment, nicht die Antwort zur Laufzeit — ein Server, der seinen Feed als `application/xml` ausliefert, ändert damit nicht die Gestalt einer Prozessvariablen. Ein Inline-FEEL-Skript behält die Einträge, deren Titel das Suchwort trägt (mit `for e in …` statt mit einem Filter, denn ein Filter wertet jeden freien Namen gegen den *Eintrag* aus und macht `suchwort` still zu null), ein Exklusiv-Gateway routet auf `count(treffer) > 0` und parkt sonst nie. Läuft ohne Konfiguration und ohne Credential: `webscrape` ist ein Worker-Typ, den Atlas standardmässig auslagert (ADR-0164) und für den es die Worker-Instanz selbst startet. |
| [`hypothekarzinsen-migrosbank.bpmn`](hypothekarzinsen-migrosbank.bpmn) | **Hypothekarzinsen täglich erfassen** (ADR-0118 + [ADR-0231](../docs/adr/0231-webscrape-structured-extraction.md)): das Gegenstück zu `blick-schlagzeilen.bpmn` — dort liefert die Quelle einen fertigen RSS-Feed, hier gibt es nur eine HTML-Seite, und trotzdem kommt **pro Tabellenzeile ein Objekt** heraus. Der Selektor wählt die Zeile, drei `<atlas:scrapeField>`-Kinder wählen darin die Zellen (`laufzeit`, `vorzugszins`, `standardzins`); ohne Felder wäre es weiterhin die flache String-Liste aus ADR-0118. Der Lehrsatz ist, **wie man die richtige Tabelle trifft**: sechs Tabellen der Seite tragen dieselbe CSS-Klasse, also identifiziert der Selektor sie über ihren Inhalt (`table:has(th:contains('Online-Hypothek'))`) und nicht über ihre Position — `nth-of-type(1)` träfe hier alle sechs, weil jede die erste ihres eigenen Elternelements ist. Ein Timer-Start (`0 6 * * *`, ADR-0051) holt die Seite jeden Morgen, ein Exklusiv-Gateway trennt "heute keine Änderung" von "der Selektor trifft nichts mehr", ein Inline-FEEL-Skript macht aus "1.130 %" die Zahl 1.13 und hängt das Abrufdatum an. Der Ablage-Schritt ins Google-Sheet ist bewusst noch ein **Mockup-Task** (ADR-0120): der Prozess läuft ohne Credential durch, und zu ersetzen ist genau dieser eine Task. |
| [`bonitaet-mockup.bpmn`](bonitaet-mockup.bpmn) | **Bonitätsprüfung mit simuliertem Umsystem** (ADR-0120): ein `serviceTask` im **Mockup-Modus** (`atlas:mockupConnector`) — die Engine simuliert die Umsystem-Abfrage selbst, mit **zufälliger Antwortzeit** (`PT1S`–`PT4S`, als Timer), einer **geskripteten Antwort** (`resultExpression` → `bonitaet`) und einer **Ausfallquote** (`failRate` + `errorCode`), die einen **BPMN-Fehler** wirft, den eine **Error-Boundary** in einen manuellen Pfad leitet. Ein Exklusiv-Gateway verzweigt auf die simulierte Antwort. Zeigt, wie sich ein Prozess end-to-end durchspielen lässt, bevor das echte (per REST angebundene) Umsystem existiert — kein externer Worker nötig. Setzt man `failRate` auf 0, entfällt der Ausfall; `min == max` macht die Dauer fix. |
| [`jira-zugangsantrag/`](jira-zugangsantrag/) | **Zugangsantrag mit Freigabe (Jira)** ([ADR-0201](../docs/adr/0201-jira-connector.md)): der ausgehende Weg — ein Antrag legt einen Jira-Vorgang an (`create-issue` → `ticket`), ein **User-Task** in Atlas holt die menschliche Freigabe, und je nach Entscheidung schaltet `transition-issue` den Vorgang weiter (mit dem Kommentar im selben Aufruf) oder `add-comment` hält die Ablehnung fest, ohne ihn zu schliessen. Drei Dinge sind Absicht: der **Default des Gateways ist die Ablehnung** (ein Gateway, dessen Bedingung nicht greift, darf keinen Zugang gewähren), der **Übergangsname steht im Startformular** statt im Modell (der Konnektor löst ihn über den Namen auf, den Jira auf dem Knopf zeigt, und der ist pro Workflow verschieden), und die Begründungsfelder sind **Pflicht**, weil FEEL-Verkettung `null` propagiert — eine fehlende Variable macht den *ganzen* Ausdruck leer, nicht nur ihren Teil. `jira-verbindungstest.bpmn` daneben ist der Dreizeiler, mit dem man die Einrichtung prüft, bevor man das grosse Modell startet. |
| [`jira-ticket-eingang/`](jira-ticket-eingang/) | **Ticket-Eingang (Jira)** ([ADR-0214](../docs/adr/0214-jira-inbound-issue-watch.md) + [ADR-0223](../docs/adr/0223-jira-account-lookup.md)): die Gegenrichtung — hier *hört* Atlas auf Jira. Ein **Nachrichten-Start** wartet auf `jira.ticket.created`, und eine Ereignisüberwachung unter *Console → Connectors → Events* veröffentlicht jeden Vorgang, den ihre JQL findet, unter diesem Namen; Atlas pollt, es muss also nichts aus dem Internet den Server erreichen. Die Instanz startet **ohne Formular** — `issueKey`, `projectKey`, `summary`, `reporter` und der ganze Vorgang kommen aus dem Ereignis. Der Lehrsatz ist die Kette dahinter: `reporter` ist ein **Anzeigename**, Jira übergibt einen Vorgang aber an eine **`accountId`**, also steht `search-users` dazwischen und `assign-issue` dahinter — mit `=konten[1].accountId`, denn FEEL-Listen sind 1-basiert. Der Default des Gateways ist der Zweig **ohne** Zuweisung, weil ein leeres Ergebnis dreierlei heissen kann („niemand passt", „das Profil ist nicht auffindbar", „dem Worker fehlt *Benutzer und Gruppen durchsuchen*") und Jira die drei nicht unterscheidet. |
| [`google-sheets-antragseingang/`](google-sheets-antragseingang/) | **Antragseingang (Google Sheets)** ([ADR-draft-google-sheets-worker](../docs/adr/draft-google-sheets-worker.md) + [ADR-draft-google-inbound-watch](../docs/adr/draft-google-inbound-watch.md)): beide Richtungen an einer Tabelle. `google-sheets-verbindungstest.bpmn` fährt die ganze Schleife — anlegen, Kopfzeile schreiben, Zeilen anhängen, mit `header="true"` als Liste von **Objekten** zurücklesen — und zeigt dabei die drei Formen, die `values` annimmt: Zeilen, Objekte (über `columns`, denn die Schlüsselreihenfolge eines Objekts ist keine Spaltenreihenfolge und wird nicht geraten) und einen einzelnen Wert. `antragseingang.bpmn` ist die Gegenrichtung: ein **Zeilen-Watch** veröffentlicht jede neue Zeile als Nachricht, die Instanz startet **ohne Formular** — die Spalten sind schon Variablen —, und sie schreibt das Ergebnis in **ihre eigene Zeile** zurück, was `rowNumber` möglich macht. Zwei Dinge sind Absicht: die Bedingung rechnet `number(Betrag)`, weil Sheets Zellen als Text liefert und ein Vergleich zwischen Text und Zahl in FEEL `null` ist, und die Statusspalte ist zugleich das **Muster gegen doppelte Zeilen** — Zustellung ist „mindestens einmal", und eine Tabelle hat keinen Idempotenzschlüssel. |
| [`pruefung/`](pruefung/) | **Prüfung mit Zeitlimit und DMN-Bewertung** (ADR-0028 + ADR-0040 + DMN): ein Assessment als Prozess. Ein **User-Task** trägt das Prüfungsformular (vier Single-Choice-Fragen); ein unterbrechendes **Boundary-Timer-Event** (`PT45M`) setzt das Zeitlimit — läuft es ab, endet der Prozess in *„Zeit abgelaufen"*. Ein Skript zählt die Punkte, ein **Business-Rule-Task** bewertet sie über die DMN-Tabelle `Notenschluessel` (Bestehensgrenze + Notenstufen — fachlich pflegbar, nicht im Code), und ein **Exklusiv-Gateway** verzweigt auf `bestanden`. Zeigt, was Atlas einer reinen Prüfungssoftware voraushat: hartes Zeitlimit, auditierbarer DMN-Trace, ergebnisabhängige Folgeschritte. |
| [`reisebuchung/`](reisebuchung/) | **Abhängige Formulare auf zwei Ebenen** (ADR-0028 + DMN): ein Reisebuchungs-Prozess, der (A) *im Formular* eine **kaskadierende** Zielauswahl und ein **bedingtes Feld** zeigt (form-js `valuesExpression` / `conditional.hide`), und (B) *im Prozess* je nach Daten unterschiedliche Formulare erzwingt — eine **DMN-Tabelle** entscheidet aus Reiseart + Alter, ob Visum- und/oder Einverständnis-Formular Pflicht sind, **Inclusive-Gateways** routen darauf. Zeigt sauber, welche „Abhängigkeit" ins Formular gehört und welche in den Prozess. Zwei Kunden-Apps unter `api/web/` (Fall B: Wizard über mehrere User-Tasks; [`fall-a/`](reisebuchung/fall-a/): **ein** User-Task, im Client geblättert). |
| [`bewerbermanagement/`](bewerbermanagement/) | **Eine kleine Applikation, nicht ein Muster** (ADR-0128 + ADR-0077 + ADR-0120): Bewerbermanagement als *Applikation* aus zwei Prozessen, drei Formularen und einer DMN-Entscheidung — gemeinsam publiziert und versioniert. Der Hauptprozess `bw-bewerbung` lässt eine **DMN-Tabelle** entscheiden, wer eingeladen wird *und in wie vielen Runden* (Ausgabe `runden` ist eine **Liste**), iteriert mit einer **seriellen Multi-Instance-Call-Activity** über diese Liste und ruft je Runde den eigenständigen Teilprozess `bw-interview` auf — mit explizitem **Datenvertrag** (`propagateAll…="false"` + `ioMapping`), den *beide* Enden des Teilprozesses erfüllen. Dazu: ein **nicht unterbrechender** Boundary-Timer als Erinnerung, eine **Error-Boundary** auf einem Mockup-Task mit Ausfallquote (das ausgefallene Umsystem wird zur Aufgabe für einen Menschen) und ein **Event-Subprozess** für den Rückzug, der jederzeit eintreffen kann. Alle Umsysteme sind **Mockups** (ADR-0120), also läuft die Applikation end-to-end ohne eine einzige echte Anbindung. Das durchgängige Beispiel des Handbuch-Kapitels „Werkstatt". |

> Looking for a model that parks on human tasks so you can watch the task
> lifecycle? See [`../postman/order-approval.bpmn`](../postman/order-approval.bpmn)
> and the [Postman onboarding kit](../postman/README.md). The two examples are
> complementary: that one shows the *human-in-the-loop* side, this one shows the
> *fully automatic* side.

---

## `order-fulfillment.bpmn` — the self-completing showcase

A small order-to-fulfillment flow, modelled as a realistic happy path with
data-based branching and concurrency:

```
Order received
   → Register order        (seed case data)
   → Score risk            (compute from prior data)
   → Order value?  ──ᴇxᴄʟᴜsɪᴠᴇ──┐
        ≥ 1000               otherwise
          │                     └→ Auto-approve small order → (Order fast-tracked)
   ┌──ᴘᴀʀᴀʟʟᴇʟ (fork)──┐
   Reserve stock   Prepare invoice
   └────ᴘᴀʀᴀʟʟᴇʟ (join)────┘        ← waits for BOTH
   → Which notifications? ──ɪɴᴄʟᴜsɪᴠᴇ (fork)──┐
        priority        EU region       otherwise
        Priority        EU compliance    Standard
        handling        check            notification
   └────────ɪɴᴄʟᴜsɪᴠᴇ (join)────────┘   ← waits for the branches that opened
   → Order fulfilled
```

### Why it is interesting

The whole model runs to completion the moment you start an instance, because:

- **Every activity is an inline FEEL script task** (`<zeebe:script expression="…"
  resultVariable="…"/>`). Atlas evaluates these *inside the engine* — unlike a
  `serviceTask`, an `<atlas:jobScript>`, or a `businessRuleTask`, an inline script needs
  no external worker, so the token never parks. (A `serviceTask` with no worker
  attached would sit active forever — correct engine behavior, but not what you
  want in a smoke test.)
- **The process seeds its own data.** `atlas_create_instance` / `POST
  …/instances` here is called with no input variables, so the first script task
  writes the case data (`order`) as a FEEL context, and every downstream decision
  reads from it. That makes each run **deterministic** — the same path every
  time — which is exactly what you want from a regression scenario.

### Engine capabilities it demonstrates

| Capability | Where in the model |
|-----------|--------------------|
| Inline FEEL script task (in-engine, no worker) | every `scriptTask` |
| Variables written by one step, read by the next | `score_risk` reads `order.amount`/`order.items` |
| FEEL context literals + member access | `register_order` writes `{amount, region, priority, items}` |
| FEEL arithmetic | `score_risk` (`= order.amount / 100 + order.items`) |
| Data-based **exclusive** gateway + default flow | `value_gw` (`= order.amount > 1000`, default `flow_express`) |
| **Parallel** split & synchronizing join | `fanout` / `fanin` |
| **Inclusive** split (multi-branch) & synchronizing join | `notify_split` / `notify_join` |
| Default-flow suppression when a condition matches | `flow_notify_standard` is *not* taken |
| Multiple end events | `order_fulfilled`, `order_fast_tracked` |

### Modelling conventions (Camunda best practices)

The model follows the naming and structure guidance from the Camunda
[BPMN modelling best practices](https://docs.camunda.io/docs/components/best-practices/modeling/):

- **Tasks** are named *verb + object* — *Register order*, *Reserve stock*.
- **Gateways** are named as a **question** — *Order value?*, *Which notifications?* —
  and their **outgoing flows are labelled with the answers** (`≥ 1000`,
  `otherwise`, `priority`, `EU region`).
- **Events** are named *object + past participle* — *Order received*,
  *Order fulfilled*.
- **Splitting and joining gateways are paired** (`fanout`/`fanin`,
  `notify_split`/`notify_join`); routing is never done by branching a task's
  outgoing flows directly.
- A single blank start event; explicit, differently-named end states.

> No `<bpmndi:BPMNDiagram>` (visual layout) is checked in, matching the
> convention of the other sample model in this repo. Atlas **auto-generates**
> diagram layout on deploy, so `atlas_get_process_xml` (or the bpmn.io viewer at
> `http://localhost:8080/`) returns a fully rendered diagram anyway.

---

## Run it

### Via the Atlas MCP server (AI agents)

```
atlas_deploy          { xml: <contents of order-fulfillment.bpmn> }   → returns a definition `key`
atlas_create_instance { key: <that key> }                             → runs to idle (here: completed)
atlas_process_runtime { key: <that key> }                             → per-element visit counts
```

### Via curl

```bash
BASE=http://localhost:8080

KEY=$(curl -s -X POST $BASE/api/v1/deployments \
  -H 'Content-Type: application/xml' \
  --data-binary @order-fulfillment.bpmn | python3 -c 'import sys,json;print(json.load(sys.stdin)["key"])')

curl -s -X POST $BASE/api/v1/processes/$KEY/instances -H 'Content-Type: application/json' -d '{}'
curl -s "$BASE/api/v1/processes/$KEY/runtime"
```

## Expected result — use it as a test

Because the run is deterministic, the per-element **visit counts** are a precise
assertion target. After one instance the definition should report
`instances: 0, tokens: 0` (the instance completed) with:

| Element | Type | Visits |
|---------|------|:------:|
| `order_received` | StartEvent | 1 |
| `register_order` | ScriptTask | 1 |
| `score_risk` | ScriptTask | 1 |
| `value_gw` | ExclusiveGateway | 1 |
| `fanout` | ParallelGateway | 1 |
| `reserve_stock` | ScriptTask | 1 |
| `prepare_invoice` | ScriptTask | 1 |
| `fanin` | ParallelGateway | **2** (one visit per incoming branch; join fires once) |
| `notify_split` | InclusiveGateway | 1 |
| `notify_priority` | ScriptTask | 1 |
| `notify_eu` | ScriptTask | 1 |
| `notify_standard` | ScriptTask | **0** (default suppressed — a condition matched) |
| `notify_join` | InclusiveGateway | **2** (one visit per opened branch) |
| `order_fulfilled` | EndEvent | 1 |
| `auto_approve` / `order_fast_tracked` | ScriptTask / EndEvent | 0 (express path not taken) |

Those three highlighted numbers are the interesting invariants:

- `fanin = 2` proves the **parallel join synchronised** both branches.
- `notify_join = 2` proves the **inclusive join waited for exactly the branches
  that opened** (two), not all three and not just one.
- `notify_standard = 0` proves the **inclusive default was correctly suppressed**
  because at least one condition held.

To exercise the *other* exclusive branch, change the seeded amount in
`register_order` to a value `≤ 1000`; the instance then takes
`Auto-approve small order → Order fast-tracked` instead.

---

## `cart-total.bpmn` — shopping-cart sum calculation

A checkout that turns a cart of line items into a payable order total, computed
step by step in inline FEEL — the engine does real money arithmetic, no worker
involved:

```
Warenkorb abgeschickt
   → Warenkorb uebernehmen    cart = positions  (or a default cart if none given)
   → Artikelanzahl zaehlen    itemCount = sum(for p in cart return p.qty)
   → Zwischensumme berechnen  subtotal  = decimal(sum(for p in cart
                                                      return p.price * p.qty), 2)
   → Rabattsatz ermitteln     discountRate = «rule table» (customerType × subtotal)
   → Rabatt berechnen         discount = decimal(subtotal * discountRate, 2)
   → Zwischensumme ≥ 50 €? ──┐
        ja                  nein
        Gratisversand        Standardversand
        shipping = 0         shipping = 4.90
   └──────────┬──────────┘
   → Nettobetrag    net   = decimal(subtotal − discount, 2)
   → MwSt (19%)     tax   = decimal(net * 0.19, 2)
   → Gesamtsumme    total = decimal(net + tax + shipping, 2)
   → Summe > 100 €? ── ja → Freigabe erforderlich
                      nein → Bestellung bestaetigt
```

### Dynamic input via start variables

The cart and the customer type are declared as **start variables**
(`atlas:startForm`), so in the Modeler's Deploy form (or the Variables panel) you
enter your own cart and every figure recomputes:

| Start variable | Type | Default |
|----------------|------|---------|
| `positions` | json | the 3-item demo cart |
| `customerType` | string | `Business` (`Business` \| `Private`) |

Start-variable defaults are applied by the Deploy form, not by a bare
instance-create, so the first task `Warenkorb uebernehmen` guards `positions` with
a default cart — meaning an instance started with **no input still self-completes**
against the demo cart, which is what makes this usable as a deterministic test.

### The discount — a FEEL rule table

`Rabattsatz ermitteln` is an inline FEEL decision table (the same logic a DMN
`businessRuleTask` would model, but evaluated in-engine so it needs no external
decision service):

| Customer type | Order value | Rate |
|---------------|-------------|-----:|
| Business | ≥ 1000 € | 15 % |
| Business | otherwise | 10 % |
| Private | ≥ 200 € | 5 % |
| Private | otherwise | 0 % |

> Want a *real* DMN task instead? Reference a decision with
> `<zeebe:calledDecision decisionId="…">` and register that decision as a DMN
> reference in Atlas (Modeler → business rule task → decision picker) first — a
> plain deploy of a business rule task whose decision isn't registered is refused
> (`no DMN model provides …`).

### The calculation — default cart, Business customer

`sum(for p in cart return p.price * p.qty)` is the cart sum; `decimal(x, 2)` rounds
to cents (FEEL uses exact decimals, so the figures are reproducible):

| Step | FEEL | Value |
|------|------|------:|
| Zwischensumme | `decimal(sum(p.price·p.qty), 2)` | **58.30 €** |
| Rabattsatz | rule table: Business, < 1000 € | 10 % |
| Rabatt | `decimal(subtotal · discountRate, 2)` | − 5.83 € |
| Nettobetrag | `subtotal − discount` | 52.47 € |
| MwSt 19 % | `decimal(net · 0.19, 2)` | + 9.97 € |
| Versand (frei ≥ 50 €) | — | 0.00 € |
| **Gesamtsumme** | `decimal(net + tax + shipping, 2)` | **62.44 €** |

Both gateways branch on the *computed* sum, so the visited end event alone proves
the arithmetic. Expected visits after one default-cart instance
(`instances: 0, tokens: 0`):

| Element | Type | Visits |
|---------|------|:------:|
| `cart_submitted` | StartEvent | 1 |
| `take_cart` · `count_items` · `subtotal` | ScriptTask | 1 each |
| `discount_rate` · `discount_amt` | ScriptTask | 1 each |
| `ship_gw` | ExclusiveGateway | 1 |
| `free_ship` | ScriptTask | 1 |
| `std_ship` | ScriptTask | **0** (subtotal ≥ 50 → free shipping) |
| `ship_join` | ExclusiveGateway | 1 |
| `net` · `tax` · `grand_total` | ScriptTask | 1 each |
| `approval_gw` | ExclusiveGateway | 1 |
| `confirmed` | EndEvent | 1 |
| `needs_approval` | EndEvent | **0** (total ≤ 100 → no approval) |

To land on the *other* branches, start with your own `positions`: a cart under 50 €
takes `Standardversand` (4.90 € shipping), and one whose total exceeds 100 € reaches
`Freigabe erforderlich`. Setting `customerType` to `Private` switches the discount
row.

> **Scope:** this models the cart → order-sum calculation. It does *not* cover the
> downstream order-to-cash lifecycle (delivery, invoicing, settlement) — those are
> worker-backed steps that park until completed.

---

## `order-to-cash.bpmn` — the full order lifecycle

Where `cart-total.bpmn` stops at the order sum, this model carries the order all
the way to cash, and shows what a *realistic* (not fully automatic) process looks
like:

```
Bestellung eingegangen
  → Warenkorb → Zwischensumme → Rabattsatz → Rabatt → Gesamtsumme   (inline FEEL, auto)
  → Summe > 100 €?  ── ja → [User-Task] Bestellung freigeben ──┐
                     nein ───────────────────────────────────┤
  → ⟨parallel⟩                                                 │
       Liefern:    [Service] Ware kommissionieren → Ware versenden
       Verrechnen: [Service] Rechnung erstellen   → Zahlung verbuchen
    ⟨join⟩
  → Auftrag abgeschlossen
```

The calculation part self-completes, then the instance **parks**: on the
`Bestellung freigeben` user task (only when the sum exceeds 100 €), and on the two
parallel service tasks `Ware kommissionieren` (job `kommissionierung`) and
`Rechnung erstellen` (job `fakturierung`). Those tokens wait until a user
completes the task / a job worker runs — the honest behaviour of a live business
process. Verified on the live server: with the default cart the instance parks
with one token on `pick` and one on `invoice`.

`positions` and `customerType` are the same start variables as `cart-total`.

## `order-to-cash-app.html` — an interactive single-page app

A self-contained SPA (vanilla HTML/JS, no build, no server) that mirrors the BPMN
and embodies its **forms**:

- the **cart** is the start form — add/remove positions, pick the customer type;
- the **sum** recomputes live in the exact same FEEL logic (rule-table discount,
  VAT, free shipping ≥ 50 €), with the matched discount rule highlighted;
- **Bestellung freigeben** is the user-task form (shown only above 100 €);
- **Liefern** and **Verrechnen** are two lanes whose service tasks you clear one
  by one — standing in for the job workers — until the order closes.

**Open it:**

- Download and open the file in any browser, or
- browse the repo file and use a raw-HTML preview
  (`https://htmlpreview.github.io/?<raw file URL>`) if the repo is public, or
- serve `examples/` via GitHub Pages for a permanent link (ask and I'll add a
  Pages workflow).

## Live mode — `api/web/order-to-cash-live.html`

`order-to-cash-app.html` *simulates* the process in the browser. The **live** app
drives a **real** Atlas instance instead, and shows how little client code that
takes — the business logic stays in the engine. Two facts shape it:

- **User tasks, not service tasks.** Only `userTask`s can be completed over HTTP
  (`POST /api/v1/tasks/{key}/complete`); service-task jobs need a gRPC worker.
  So the live model, [`order-to-cash-live.bpmn`](order-to-cash-live.bpmn), makes
  delivery and billing user tasks the app can clear.
- **Same-origin, because the server sends no CORS headers.** The app therefore
  lives in `api/web/` and is served by Atlas itself at
  `https://<your-atlas>/order-to-cash-live.html`, so `fetch("/api/v1/…")` is
  same-origin. (Opening the file from another origin would be blocked by the
  browser unless a proxy adds CORS. The base-URL field at the bottom lets you
  repoint it if you have one.)

The entire integration is three calls — `POST /deployments`, `POST
/processes/{key}/instances`, `POST /tasks/{key}/complete` — and a live request log
on the page makes them visible. Because it ships under `api/web/`, it is embedded
in the binary: **rebuild and redeploy the Atlas server**, then open
`/order-to-cash-live.html`.

### Service-task variant — `api/web/order-to-cash-jobs.html`

The live app above uses user tasks because only they are listed by `GET /tasks`.
The **jobs** variant drives the *service-task* model instead, using the read side
of the operator job affordance:

- `GET /api/v1/instances/{key}/jobs` lists every activatable job the instance is
  parked on — of **any** type, not just user tasks (this is the endpoint that makes
  the operator complete usable from a client), and
- `POST /api/v1/jobs/{key}/complete` finishes each one, the same call for a
  service-task job and the approval user-task job alike.

So the app becomes the job worker: list the parked jobs, complete them, watch the
parallel delivery/billing branches close. Same tiny client, different mechanism.

## Clean up

The instance completes, so it does not block anything, but to remove the
definition from the engine and disk:

```
atlas_delete_process { key: <that key> }     # or: curl -X DELETE $BASE/api/v1/processes/$KEY
```
