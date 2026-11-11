# ADR-DRAFT: A task form shows a report: charts over a snapshot Atlas computed, acknowledged by completing the task

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-11
- **Deciders:** pblumer (maintainer; asked for this record on 2026-11-11 and settled four
  choices: read means acknowledged, the data comes from Atlas, a report is a snapshot,
  and a report can be printed and saved as PDF)
- **Open question:** whether a report must also leave Atlas as a PDF *file* — attached to
  a mail for a recipient without an account, or stored as a document — which needs the
  charts drawn on the server. This record draws them in the browser only (§6); the
  server-side drawing is named as a follow-up and not taken here.
- **Question checked:** 2026-11

## Context and problem statement

More and more requests ask Atlas for reports that look like a dashboard: a handful of
figures, a bar per process, a trend over the last weeks. They are not asked for as
analysis. A report is asked for as a piece of work with an addressee: somebody triggers
it, named people receive it, and if nobody reads it, the list of recipients is
corrected or somebody is reminded.

That second half is what a workflow engine does and a BI tool does not. Atlas can
already address a user task to people, repeat it per recipient, remind with a timer and
escalate. What it cannot do is **show a chart in a task form**:

- Task forms are form-js schemas ([ADR-0028](0028-forms-and-the-tasks-app.md)). The
  vendored viewer (form-js 1.24) has a table but no chart.
- Atlas extends form-js only through its own modules in
  `api/web/vendor/form-js/src/atlas/`, never by patching form-js. The precedent for a
  component that only *shows* a variable is the e-mail view `mailview`
  ([ADR-0489](0489-mail-html-view.md)).
- The Console draws its graphics as hand-written SVG (`heatoverlay.js`, the
  `panorama-*.js` views, the playground's sparkline). No chart library is vendored.
- The reports Atlas computes (the delivery report of
  [ADR-0515](0515-service-reporting-and-customer-views.md), the process statistics of
  [ADR-0496](0496-operations-heatmap.md), the instance and incident
  summaries) are Console cards. None of them reaches a process.
- Atlas records that a task was **completed**, and nothing about it being opened.

The question: **how does a task form show a small set of charts over data Atlas
computed, how does that data get into the instance, and what does "the report was read"
mean?**

## Decision drivers

- **The engine does not change.** A chart is a rendering concern; a report is data the
  instance already knows how to hold. No new event, no change to `applyToState` (I4),
  nothing on the hot path (I1, I5).
- **A report is a fact of its moment.** Every recipient sees the same figures, and an
  auditor a year later sees what they saw. A chart that recomputes when it is opened
  shows each reader something else.
- **Aggregates, not people.** A report that carries names is a list of who did what.
  [ADR-0353](0353-product-usage.md) and ADR-0515 made a name a disclosure that needs a
  stated relationship and an audit record. A report task addressed to a group must not
  become a way round that.
- **Credentials stay on the Worker** ([ADR-0164](0164-no-in-process-service-tasks.md),
  [ADR-0168](0168-connector-work-on-a-worker.md)). Atlas reading Atlas is still a call
  from a Worker Instance, not something the run loop does.
- **Buildless and vendored as before** ([ADR-0012](0012-web-ui-app-shell.md),
  [ADR-0013](0013-embed-bpmn-js-modeler.md)). No new dependency in the viewer bundle.
- **Small and finished.** A few chart types that each answer a question well, not an
  open-ended charting toolkit that each new request extends.
- **What is measured must be a fact.** "Read" must mean something Atlas can prove, not
  a guess from a browser event.
- **Printable.** A report is printed, filed and forwarded as PDF.

## Considered options

For what "read" means:

1. **Read means acknowledged: the recipient completes the task.**
2. Read means opened: Atlas records when a task's form was first shown.

For where the figures come from:

1. **A snapshot: a service task computes the report into a variable; the form shows
   that variable.**
2. Live: the chart queries Atlas when the form is opened.

For how Atlas data reaches the snapshot:

1. **A built-in Worker Type, *Atlas report*, with a short list of read operations, each
   one an existing read route, answering in the report shape below.**
2. The REST Worker Type against Atlas's own HTTP API, with the model mapping each answer
   into the report shape.
3. The server answers the report job itself, inside the engine process.

For drawing the charts:

1. **Atlas's own form component `chart`, drawing SVG from one computed geometry.**
2. A vendored chart library (Chart.js, ECharts, Vega-Lite) inside the viewer bundle.
3. form-js's HTML view with a server-rendered SVG.

For print and PDF:

1. **A print stylesheet for the task form, and a *Save as PDF* that draws the same
   geometry with the PDF writer Atlas already owns (`api/web/pdf.js`).**
2. Print stylesheet only: the browser's own *Save as PDF*.
3. PDF rendered on the server.

## Decision outcome

Chosen: **read = acknowledged (1); a snapshot (1); an *Atlas report* Worker Type (1); a
`chart` component (1); print stylesheet plus a PDF from the same geometry (1).**

### 1. Read means acknowledged

A report is read when its recipient **completes the task**. The form says what
completing means. A required checkbox *Ich habe den Bericht zur Kenntnis genommen*,
and optionally a comment, are ordinary fields. The completion records who completed it
(`completedBy`, `api/taskcompleter.go`) and when, as every completion does.

Atlas does **not** record that a task was opened (option 2):

- A shown form is not a read report. A preview pane, a link scanner opening a task link
  (the problem [ADR-0523](0523-user-task-answered-by-link.md) solves for answers) and a
  tab opened and abandoned would all count.
- Recording it would be a new fact for the sake of a weaker answer. It would be a new
  event in the log (I4, I6) or a sidecar that recovery does not replay
  ([ADR-0312](0312-portal-catalogue-order-inventory.md)'s argument for keeping access facts in the log).

Everything else is modelled with what exists:

- **One task per recipient.** A parallel multi-instance user task over a `recipients`
  collection ([ADR-0077](0077-multi-instance-activities.md)), each instance assigned to
  its element ([ADR-0318](0318-user-task-assignment-expressions.md)). A recipient
  without an account answers through a link a mail carries (ADR-0523).
- **Reminders.** A non-interrupting timer boundary event with a cycle on the
  multi-instance activity sends a reminder through the mail Worker.
- **Changing the recipients.** An interrupting timer, the deadline, leads to a task for
  the report's owner. It shows who has not acknowledged and lets the owner edit the list.
  The process then loops back for the recipients still open and those added. The
  collection of a multi-instance activity is fixed when it starts, so a correction is a
  new round, never an edit of a running one.
- The pattern ships as an example in `examples/`, so it is copied rather than
  re-invented.

### 2. A report is a snapshot

The report is computed once, by a service task, into one process variable. Every chart
in the form reads that variable. Nothing is fetched when the form is opened (option 2):

- Live data shows each recipient a different report.
- It cannot be audited afterwards.
- It needs a query path and an authorization rule at reading time. ADR-0489 refused the
  same path for a mail for the same reasons.

The variable has a versioned shape, the **report contract**:

```json
{
  "schema": "atlas.report/1",
  "title": "Prozesslauf KW 45",
  "generatedAt": "2026-11-11T07:00:00Z",
  "period": { "from": "2026-11-03T00:00:00Z", "to": "2026-11-10T00:00:00Z" },
  "notes": ["Statistik auf die neuesten 10000 Instanzen begrenzt"],
  "datasets": {
    "byProcess": {
      "unit": "count",
      "categories": ["Eintritt", "Austritt", "Mutation"],
      "series": [
        { "name": "gestartet", "values": [42, 17, 63] },
        { "name": "beendet",   "values": [40, 15, 58] }
      ]
    },
    "openIncidents": { "unit": "count", "value": 3, "previous": 5 }
  }
}
```

- A dataset is either a **value** (`value`, optional `previous` and `target`) or a
  **series table** (`categories` and one or more `series` of numbers or `null`).
- `unit` is one of `count`, `percent`, `ms`, `days`, `currency:<ISO 4217>`. The chart
  formats by the unit and the reader's locale (`numfmt.js`).
- Limits, checked by the Worker Type and again by the component: at most 50 categories,
  at most 8 series and 20 datasets per report, and the whole report within
  `limits.Variable`. A report beyond them is an aggregation mistake, not a chart to
  draw. The limits are a readability bound first and a size bound second.
- `notes` is how a report states its own limits. A source that truncated (ADR-0496's
  `truncated`) says so here, and the chart shows the note. A part must never pass for
  the whole.
- The contract does not carry names of people. The *Atlas report* Worker Type never
  produces them (§3). A model that writes a report by other means (a script task, a
  decision, another Worker) is the author's responsibility, as every variable is.

### 3. The *Atlas report* Worker Type

A built-in Worker Type, run out of process on `atlas worker`, with **read-only
operations**. Each operation calls exactly one existing read route and folds its answer
into the report contract:

| Operation | Route | Datasets |
| --- | --- | --- |
| `instancesSummary` | `GET /api/v1/instances/summary` ([ADR-0083](0083-o1-instance-summary.md)) | instances by state, per process |
| `processStatistics` | `GET /api/v1/processes/{key}/statistics` (ADR-0496) | started, finished, running; duration median and p90 per element; with `buckets`, one window per bucket as a series |
| `incidentSummary` | `GET /api/v1/incidents/summary` | open incidents by process and by element |
| `deliveryReport` | `GET /api/v1/catalog-products/delivery-report` (ADR-0515) | met, missed, overdue, open per product; median and p90 lead time |

- **No route that answers names is an operation.** The shop's team and organisation
  reports (ADR-0515) are excluded, as they are from MCP. An operation is listed here
  only if its answer is counts and durations.
- `deliveryReport` is tagged `Catalogue`, so it is not offered when `--catalogue=false`
  ([ADR-0434](0434-the-catalogue-can-be-switched-off.md)).
- A time series is built from separate windows: `buckets: 8, bucket: "week"` asks eight
  statistics windows. Each window costs one fold of up to 10000 instances, so `buckets`
  is limited to 26.
- The manifest declares every operation `idempotent: true` and `effect: none`
  ([ADR-0538](0538-operations-declare-idempotency-and-effect.md),
  [ADR-0546](0546-new-built-in-worker-types-ship-a-manifest.md)). A second delivery of
  the job computes the report again, which is harmless.
- **Credential.** The Worker Instance holds an API token
  ([ADR-0194](0194-api-tokens.md)) of a technical principal with the read roles those
  routes require. It is held as [ADR-0545](0545-installed-worker-instances-hold-their-own-credential.md)
  provides for an installed Worker Type's instance. The engine never holds it.
- **Whose reach.** The report is computed with the technical principal's reach, not the
  recipient's. Who may start a report process is decided by its start grants
  ([ADR-0029](0029-public-process-start-links.md) and the start rights of the
  application). Because every operation answers aggregates only, a recipient learns how
  many cases a process had, never whose. Computing in the name of the person who
  triggered the report is the stronger rule. It waits for identity-aware Worker
  execution ([ADR-0475](0475-identity-aware-worker-execution.md), Proposed).

Option 2, REST against Atlas, stays possible and is not forbidden. It is not the
recommended path:

- Every author would map route answers into the contract in FEEL.
- Every author would configure a credential.
- Nothing would keep the answers to aggregates.

Option 3, the server answering its own job, is the in-process connector
[ADR-0233](0233-in-process-connectors-refused.md) refused.

### 4. The `chart` component

A form component of Atlas's own, type `chart` (*Diagramm* in the form editor's palette),
in `api/web/vendor/form-js/src/atlas/chart.jsx`. `viewer.jsx` registers it and
`editor.jsx` gives it its properties.

- **It reads and never writes.** Like `mailview`, it names the report variable by its
  `key`, and form-js never submits it. On the server its key counts as a binding the
  form reads and not one it writes. That is the `mailViewType` case in
  `api/publicstartfields.go` and `api/startformcheck.go`, extended to `chart`.
- **Properties:** `key` (the report variable), `dataset`, `chartType`, `title`, and per
  type: `orientation`, `series` (a subset, in order), `stack100`, `target`,
  `showValues`.
- **Five types:**

  | `chartType` | Answers | Notes |
  | --- | --- | --- |
  | `kpi` | one figure, its change against `previous`, optional sparkline | from a value dataset, or the last value of a series |
  | `bar` | compare categories | horizontal by default; vertical for few, short labels |
  | `stackedBar` | parts of each category | `stack100` for shares; replaces a pie chart |
  | `line` | a value over time | one to eight series; area fill only for a single series |
  | `bullet` | actual against target or threshold | replaces a gauge |

  Pie, donut, gauge and 3-D charts are not offered. Beyond three segments a reader
  compares angles badly, and a stacked bar answers the same question more legibly.
  This choice can be revisited. It is not a technical limit.
- **One geometry, two renderers.** A layout step turns dataset and properties into a
  list of primitives: rectangle, line, polyline, text, with sizes in points. The SVG
  renderer draws them in the form, and the PDF renderer (§6) draws the same list. Print
  and screen cannot disagree.
- **Colours** come from the form runtime's tokens
  ([ADR-0263](0263-form-runtime-brand-theming.md)): the brand accent for a single
  series, and a fixed categorical order for several, checked for contrast in light and
  dark. A series keeps its colour across every chart of one form.
- **Accessible.** Every chart has an SVG `<title>`, and a *Als Tabelle* toggle shows the
  same dataset as a table. Values are never told apart by colour alone: series are
  labelled directly where they fit, and listed in a legend otherwise.
- **Errors are visible.** A missing variable, an unknown `schema`, a missing dataset or
  a dataset beyond the limits draws a framed message in the chart's place, never an
  empty box. The form editor's test tab (ADR-0028, [ADR-0025](0025-full-properties-panel.md))
  renders against a sample report, so an author sees the chart before deploying.

### 5. The report form

Nothing more is needed: a report task's form is an ordinary form with `chart`
components, the existing `table` for detail, text views for commentary, and the
acknowledgement fields of §1. Charts can be placed side by side in form-js's row layout.
On a narrow screen they stack in one column, as the Tasks app does
([ADR-0417](0417-the-shop-and-tasks-are-one-column-wide-on-a-narrow-screen.md)).

### 6. Print and PDF

- **Print.** The Tasks app gets a print stylesheet for a task's form:
  - the app's chrome, the buttons and the input fields' frames are hidden;
  - the report's title, period and `generatedAt` head the page;
  - a chart is never split across a page break;
  - colours print as on screen (`print-color-adjust: exact`).

  The browser's own *Print → Save as PDF* works from this alone.
- **Save as PDF.** A button on a form that holds at least one `chart` writes an A4 PDF
  with `api/web/pdf.js`, the dependency-free writer of the process documentation export
  ([ADR-0143](0143-process-documentation-export.md)). The writer gains vector primitives
  (filled rectangle, stroked line and polyline) beside its text and its JPEG. The charts
  are drawn from the geometry of §4 as vectors, not as a screenshot. The PDF holds the
  title, the period, `generatedAt`, every chart with its table, the notes, and the form's
  text views, plus a footer with the instance and who printed it and when. Input fields
  are not part of it: a PDF is the report, not the answer.
- **Not taken here:** a PDF rendered on the server (option 3). It is what a mail
  attachment or a stored document ([ADR-0543](0543-a-file-is-a-document-not-a-variable.md))
  would need. It means drawing the charts in Go and a second implementation of the
  geometry. See the open question.

### Consequences

- **Positive:**
  - A report becomes a piece of work with addressees, reminders and escalation, built
    from elements that already exist.
  - Every recipient sees the same, auditable figures.
  - The engine is not touched.
  - Charts, tables and PDF draw from one geometry.
  - Names of people cannot leave through the report Worker Type.
- **Negative / trade-offs accepted:**
  - A report is in the log, every checkpoint and every backup for as long as its
    instance's history lives. That is why the contract is aggregates and is bounded.
  - The figures are computed with a technical principal's reach, not the recipient's.
  - A time series costs one statistics fold per bucket.
  - A forwarded mail link forwards the right to acknowledge (ADR-0523's open question).
- **Follow-ups / risks to watch:**
  - A server-side PDF for mail attachments (the open question).
  - Computing in the triggering person's name once ADR-0475 lands.
  - Further *Atlas report* operations (user-task throughput and overdue tasks per
    process) when a route answers them.
  - A recipient who wants to see the report again after acknowledging. That is
    [ADR-draft-completed-tasks-stay-readable](draft-completed-tasks-stay-readable.md),
    which this record depends on for "reading it again".

## Pros and cons of the options

### Read = acknowledged
- Good: a fact Atlas already records, with who and when; the recipient states it.
- Bad: a person who read the report and did not click is reported as not having read it.
  The remedy is the reminder, which is the point.

### Read = opened
- Good: no click asked of the recipient.
- Bad: proves a render, not a read; link scanners and previews count; a new fact or an
  unreplayed sidecar.

### Snapshot
- Good: one report for all, auditable, no read-time authorization.
- Bad: persisted with the instance; stale by design.

### Live
- Good: always current; nothing persisted.
- Bad: each reader sees a different report; a query path and an authorization rule at
  reading time; nothing to audit.

### *Atlas report* Worker Type
- Good: the report shape is fixed; only aggregates; one credential configured once; a
  manifest declares it read-only.
- Bad: a new Worker Type to maintain; the reach is the technical principal's.

### REST against Atlas
- Good: nothing new to build; any route reachable.
- Bad: every author maps answers in FEEL and holds a credential; nothing keeps names out.

### Own `chart` component
- Good: no dependency; one geometry for SVG and PDF; the Console's drawing style.
- Bad: every chart type is code Atlas owns.

### Vendored chart library
- Good: many chart types at once; mature interaction.
- Bad: a large dependency inside a bundle whose upgrades are already load-bearing; its
  canvas output does not give the PDF a geometry; every type it offers invites a
  request.

### Print stylesheet plus own PDF
- Good: the browser's print works everywhere; the PDF is vector and identical to the
  screen.
- Bad: the PDF writer grows; fonts are the standard Helvetica faces only.

### Server-side PDF
- Good: a report can be attached to a mail or stored as a document.
- Bad: a second rendering in Go; deferred.

### Scale-out assessment

- **State ownership:** the report is a variable of its instance, owned by that
  instance's partition. Nothing global is added.
- **Routing and boundary:** the *Atlas report* operations are ordinary jobs on an
  ordinary service task. The routes they call are the existing read API, and a
  multi-node Atlas answers them wherever it answers them then.
- **Failure and retry:** operations are idempotent and without effect; a duplicate
  delivery recomputes the same report or a newer one, and only the completing delivery
  writes the variable.
- **Consistency and visibility:** a summary read across partitions is not a consistent
  cut; `generatedAt` and `period` state what the report claims, and nothing more is
  claimed.
- **Standalone and migration:** standalone mode is unchanged; the contract and the
  component do not depend on placement.
- **Verification:** component tests for each chart type and each error state; a
  Worker Type conformance test per operation against recorded route answers; a
  Playwright test that prints and saves the PDF; no engine test changes.

## Links

- adds a component to the forms of [ADR-0028](0028-forms-and-the-tasks-app.md), as
  [ADR-0489](0489-mail-html-view.md) added `mailview`
- reads what [ADR-0083](0083-o1-instance-summary.md),
  [ADR-0496](0496-operations-heatmap.md) and
  [ADR-0515](0515-service-reporting-and-customer-views.md) compute; keeps names out as
  [ADR-0353](0353-product-usage.md) does
- a Worker Type under [ADR-0546](0546-new-built-in-worker-types-ship-a-manifest.md) and
  [ADR-0538](0538-operations-declare-idempotency-and-effect.md)
- extends the PDF writer of [ADR-0143](0143-process-documentation-export.md)
- reading a report again: [ADR-draft-completed-tasks-stay-readable](draft-completed-tasks-stay-readable.md)
