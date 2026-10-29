# ADR-0511: A form field takes its options from a decision

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-29
- **Deciders:** Atlas maintainers

## Context and problem statement

A select, a radio group, a checklist and a tag list offer a person a fixed set of
choices: cost centres, countries, product categories, the kinds of leave. form-js gives
such a field its options in one of three ways, and Atlas added a fourth for the
directory ([ADR-0473](0473-form-remote-data-sources.md)):

- **Static** — the options are written into the form (`values`);
- **Input data** — the options are a variable the form is opened with (`valuesKey`);
- **Expression** — a FEEL expression in the form computes them in the browser
  (`valuesExpression`);
- **Remote data source** — a person searches a directory through a Worker.

None of them fits a reference list. Such a list has an owner other than the form's
author — finance keeps the cost centres, HR the kinds of leave — and it changes on its
owner's schedule, not the form's. It is used by several forms and by processes, which
evaluate it in business rule tasks. Written into each form, it is copied as often as
it is used, and a change is an edit to every copy. Passed in as a variable, it needs a
modelled step in every process that shows it, and it is written into the log of every
instance that does.

Atlas already has a home for a list with an owner, a version and a publication: a
decision. Decisions are deployed on their own and versioned
([ADR-0319](0319-durable-versioned-decision-deployments.md), [ADR-0322](0322-deploying-one-decision.md)),
documented ([ADR-0324](0324-decision-documentation.md)), and refused deletion while
something depends on them ([ADR-0336](0336-cleaning-up-the-decision-store.md)). DMN 1.3 has
the notation for exactly this shape, the boxed list (§10.3.2.7): one FEEL expression per
entry, the list of their values as the result. temis evaluates it. Two things were
missing:

1. **A form cannot name a decision.** No options source reads one.
2. **The decision editor cannot open a list.** dmn-js renders a `dmn:List`, but the
   Modeler opens its boxed-expression view for a business knowledge model only, so a
   list decision could be written in the XML source and nowhere else.

The question this record answers: **how does a form field show the list a deployed
decision answers with, kept by the people who own it, without copying it into forms or
instances — and, where the list depends on a choice made in the same form (a group,
then its members), without opening a door through which a decision is evaluated against
values the form never offers?**

## Decision drivers

- **One list, one place.** The form references the list; it does not hold a copy.
- **Live.** A new version of the decision is what the next person to open the form
  sees — no form edit, no redeploy of the process.
- **Nothing per display is a fact** (I2, I3, I6). Showing a form writes nothing to the
  log, the state store or the processor; only the value the person submits becomes a
  variable.
- **The form stays a form-js document** ([ADR-0028](0028-forms-and-the-tasks-app.md),
  ADR-0473): anything that renders form-js without Atlas still renders it.
- **No new exposure.** A request may not choose the decision or its inputs, and may not
  give an input a value the form does not offer; nobody reads more than they could
  already.
- **The submission is checked** as ADR-0473 checks a directory field's.

## Considered options

1. **Static options**, copied from the list into each form.
2. **A business rule task before the user task**, writing the list into a variable the
   field reads (`valuesKey`) — possible today.
3. **An expression source** with the list as a FEEL literal in the form.
4. **A snapshot at design time:** the form editor evaluates the decision and writes the
   result into the form as static options.
5. **Evaluation at task activation:** the engine evaluates the decision when the user
   task is created and stores the list with the task.
6. **Evaluation when the form is shown:** the field names the decision and binds its
   inputs to other fields of the form or fixes them; the server evaluates the newest
   deployment when the form is opened and again whenever a bound field changes.

## Decision outcome

Chosen option: **6 — evaluation when the form is shown.**

### Why not the others, at their strongest

**Option 2** is the strongest alternative and stays the right tool in two cases. It is
built from parts that exist, it can pass the decision inputs from the instance — a list
that depends on the case, such as the cost centres of the requester's department — and
it records in the instance what the person was offered, which an audit may need. For a
reference list it does every expensive thing this record exists to avoid: a modelled
step in every process that shows the list, the whole list in the log, the state and
every backup of every instance, a list frozen at the moment the step ran while the task
may wait for days, and options that depend on a variable the form's author cannot see
from the form. Where the options depend on the case or the offered list must be
recorded, model option 2; this record removes nothing from it.

**Option 4** is the simplest at runtime — the rendered form is plain form-js with
static options, it works on every page including public ones, and nothing is evaluated
when a form is shown. But it is option 1 with a button: the list is copied again, and a
new version of the decision changes nothing until somebody opens and saves every form
that copied it, which nobody will know to do.

**Option 5** puts the list where the task is. It also puts work into the processor's
path for every user task whose form names a decision, writes the list into the log per
task, and freezes it at activation — the same costs as option 2, paid by the engine
instead of the model.

**Options 1 and 3** keep the list in the form, which is the problem.

### What a form says

A field names the decision in a block of its own, as a directory field names its data
source:

```json
{
  "type": "select",
  "key": "kostenstelle",
  "label": "Kostenstelle",
  "atlasDecision": { "version": 1, "decisionId": "Kostenstellen" }
}
```

A decision that takes inputs has each of them bound to another field of the form, whose
value is the input, or fixed:

```json
{
  "type": "radio",
  "key": "mitglied",
  "atlasDecision": {
    "version": 1,
    "decisionId": "Mitglieder",
    "inputs": [ { "name": "gruppe", "field": "gruppe" }, { "name": "land", "value": "CH" } ]
  }
}
```

- It is offered on the four fields form-js gives options to: `select`, `radio`,
  `checklist`, `taglist`. A select and a radio group submit one value; a checklist and a
  tag list submit a list of them.
- The block stands where `values`, `valuesKey`, `valuesExpression` or `atlasDataSource`
  would. A field naming one of those as well is refused at save: a field takes its
  options from one source.
- `decisionId` is a plain decision name — the name Atlas addresses decisions by
  everywhere — never FEEL. There is no expression language in the block at all: the
  only input at run time is the person's own form state, and a binding says everything
  an expression over it would, without an evaluation surface — ADR-0473 decided the same
  for its filters.
- An input names exactly one of `field` and `value`. The bound field must be a field of
  the same form whose key no other field has, not the field itself, and either outside
  every dynamic list or in a row of one the field is in too — a row's field reads its
  own row's choice, or one outside the rows, never a row of a list it is not in. A field
  naming a decision has a key no other field has, because a rendered form asks for its
  options by that key. Decision fields bound to each other in a cycle are refused:
  neither could be offered before the other was chosen.
- The save checks the block's shape, not whether the decision is deployed now. That is
  the server's state, it changes after the form is saved, and an import may well bring
  a form in before the decision it names.

form-js keeps a member it does not know on export, so the block survives the editor and
is ignored wherever form-js renders without Atlas.

### Which decision, and what counts as a list

- The **newest decision deployment** of that name (ADR-0319). A model bundled with a
  process is not a decision in its own right and is not offered; what a form shows is a
  decision somebody published as one, versioned and deleted with the impact check a
  decision deployment gets.
- Every input the decision declares must be given — bound or fixed — or the field
  answers `unbound_input`. An input bound to a field that holds nothing yet makes the
  field offer nothing, without asking the decision: no group chosen, no members.
- Its result must be a **list** (`not_a_list`), of at most 1 000 entries — more is not
  a list a person scrolls through but a search, which is what ADR-0473 is for. The list
  is refused rather than cut, because a cut list hides entries without saying which.
- Each entry is turned into an option the way form-js turns `values` into options: a
  string, number or boolean is its own label and value; a context with a `label` and a
  `value` is taken as it is; a context with only a `value` is labelled with it.
  Anything else — null, a nested list, a context without a usable value — is skipped
  and counted. Normalizing on the server the way form-js does means the server checks a
  submission against exactly what the person was shown.

A boxed list is the natural notation, but any decision that answers with a list
qualifies — a decision table with a `COLLECT` hit policy, a literal expression
`if Gruppe = "IT" then ["anna", "ben"] else ["carla"]`.

### Who may ask for the options, and about what

A rendered form asks `POST /api/v1/forms/{id}/fields/{field}/options` with the values
chosen in the fields the field depends on — the fields its inputs are bound to, and
theirs in turn where those are decision fields too. Which decision, which input each
value is, and which inputs are fixed are read from the stored form; a request naming a
field the options do not depend on, or anything but `values`, is refused. Whoever may
read the form (`GET /api/v1/forms/{id}`, any signed-in caller) may ask.

What keeps the route from being a way to ask a decision about anything is one rule: **a
bound value must be one of the choices the field it comes from offers**, where that
field fixes them — its own static options, or, for a decision field, the list its own
decision answers for the values before it. A form that offers the groups IT and
Vertrieb cannot be used to ask for the members of a group it does not offer, though the
decision knows them. A field a person types into fixes nothing, and its value is the
person's: binding a decision to a text field is the author's choice to let anybody who
can fill in the form ask the decision about any value. Without inputs nothing changes
from a read of the model: any signed-in caller reads it whole through
`GET /api/v1/decision-deployments/{key}/xml`.

The form editor's previews ask `POST /api/v1/form-data/decisions/{decisionId}/options`
with the inputs themselves — a form being edited may not be saved — and that route is a
modeler's, who may already try any model (`POST /api/v1/decisions/evaluate`).
`GET /api/v1/form-data/decisions` lists the decisions with the inputs each takes, for
the editor's picker.

A **public start link** and a **public wizard step** have nobody signed in. A field
whose inputs are all fixed is written into the schema the server serves them, as its
static `values`, and needs no request. A field bound to another field asks
`POST /public/forms/{token}/fields/{field}/options` or
`POST /public/wizard/steps/{taskKey}/fields/{field}/options` — the link or the pass is
the authorization, the form is the link's or the screen's, the same rule holds, and the
public routes' rate limit applies. Putting such a field on a public form shows the list
to anonymous visitors — the author's choice, as it is with static options.

### Rendering

The vendored form-js bundles are extended as ADR-0473 extended them: the decision is a
fifth row in the four tables form-js describes its options sources in, and the viewer
registers its select, radio group, checklist and tag list through a wrapper. For a field
naming a decision the wrapper asks the page's `atlasDecisions` service for the options
and renders **form-js's own field** with them as its static `values` — label, required,
description, errors, keyboard and value stay form-js's. Until the options are there it
shows that it is loading; a decision that cannot answer shows that the choices could
not be loaded, with the server's reason as the tooltip.

A value the form is opened with is kept while the options load and dropped by form-js
once they are there if the list does not offer it. Without the wrapper's own sanitizing,
form-js would have dropped every prefilled value at import, because the options are not
known yet.

A field bound to another field reads that field's value from the form's data, through
form-js's own path registry and in the row of every dynamic list it is in, whenever
form-js renders — which it does on every change. When the value changes it asks again,
a quarter of a second later so a value being typed is asked about once, and keeps
showing the previous options until the new ones are there; form-js then drops a value
the new list does not offer, so choosing another group clears the member chosen before.

Every page that renders a stored form — the Tasks app, the start form, the repair form,
the shop — gives it `decisionOptions({ formId })` (`formviewer.js`); the public page
gives it the link's or the step's route.

### Submission

Before a task completes, a public start link starts an instance or a public wizard step
completes, every decision field's submitted value is checked against what the decision
answers now — after the authorization check, before the command exists, off the run
loop:

- a value the list does not offer: `400 invalid_value`, naming the field and the value;
- a checklist or tag list submitting something other than a list: `400 invalid_value`;
- a decision that cannot answer — undeployed, an input not given, no longer a list:
  `422` with the reason's code, because a value that cannot be checked is not completed
  unchecked;
- the list a value is held to is the one the same submission's choices give: the group
  submitted beside the member, the row's own group for a member in a dynamic list's
  row. A bound field form-js did not submit — a disabled one — is read from what the
  form was opened with (the task's variables), and failing that from its default value;
- a bound value the field it comes from does not offer: `400 invalid_value`;
- an empty value is not checked; whether a value is required stays form-js's to insist
  on, as it is for every other field.

Values are compared as the JSON they travel as, so a number submitted as `2` equals the
decision's `2` whatever Go type each arrived in. As ADR-0473 says of the directory, this
is a check before the completion, not a transaction with it: the list can change between
the check and the next step that reads the value.

### Editing the list

The decision editor opens a decision whose logic is a `dmn:List` in its own tab, as it
opens a table or a literal expression, and edits it entry by entry: an entry added at
the end, moved up or down, removed. The entries are the fork's literal-expression
editors; the list around them is Atlas's (`api/web/vendor/dmn/src/atlas/`), and every
change is one `element.updateProperties` on the list — one step that Ctrl+Z undoes. A
decision is made a list where its kind is changed today, in the requirements graph's
replace menu, which now offers **List** beside Decision table and Literal expression;
the decision keeps its id and its requirements.

The fork's boxed-expression header offers an "Expression type" select that turns a
decision into a For, Every, Some or Filter. This Modeler does not open those, so a
decision turned into one could not be opened again; a list decision's header is its
name. The fork itself is not changed: "No Atlas-specific behavior is implemented
inside dmn-js" stays true, and the modules are built into the bundle from
`src/atlas/`, as form-js's are.

### Consequences

- **Positive:**
  - A reference list is kept once, by its owners, in the decision editor; it is
    versioned, deployed, documented and protected from deletion like any decision.
  - Forms show it live, on every page Atlas renders forms on, and public pages receive
    plain form-js.
  - Nothing about showing a form reaches the log; the instance holds the chosen value
    and nothing else.
  - The submitted value is checked against the list, which a static select's never was.
- **Negative / trade-offs accepted:**
  - **What a person was shown is not recorded.** Only the chosen value is. A process
    that must prove what was offered models option 2.
  - **The decision is evaluated on every display.** It is an in-memory evaluation of a
    compiled model, with no cache; a cache would only add staleness.
  - **Deleting or changing a decision can break a form.** The deletion impact check of
    ADR-0331/ADR-0336 counts process definitions pinned to a decision, not forms naming
    it. A form whose decision is gone says its choices could not be loaded, and a
    completion with a value for it is refused with `422`.
  - **A list can change between display and completion.** A value the new version no
    longer offers is refused; the person opens the task again and sees the new list.
  - **Numbers are what the decision answers.** temis returns a FEEL number as its
    decimal string unless the model declares the result a number
    ([ADR-0386](0386-a-decision-result-keeps-its-declared-type.md)); inside a list it
    stays a string, and that string is the option's value.
  - **Not checked on every door.** The Console's start form, the shop's product
    configuration and an incident's repair form submit variables without naming the
    form, so their values are not checked against the list — as a static select's never
    were.
  - **Each entry is a FEEL editor.** A list of hundreds of entries draws hundreds of
    editors; reference lists of tens of entries are the case this is built for.
  - **A field bound to a text field can be asked about any value** a person types — the
    author's choice, as above.
  - **A dependent field asks per change.** Each choice in a bound field is one request
    and one evaluation; nothing is cached across people.
  - **The form editor's panel speaks English**, as the rest of form-js's panel does.
- **Follow-ups / risks to watch:**
  - Count forms naming a decision in the deletion impact check.
  - An input bound to a process variable directly, rather than through a field of the
    form; today an expression field (`= variable`) carries one into the form.
  - The list editor could move into the dmn-js fork, which already authors other boxed
    expressions generically.

### Fixed on the way

After a decision's kind was changed in the replace menu — a table into a literal
expression, or now into a list — its tab still opened the old kind, over a business
object no longer in the model, so edits made there went nowhere. dmn-js compares views
by id, and the id does not change, so it reported no change and the tab strip kept the
view it had captured. The tab now looks the view up again when it is pressed.

### As built

- `api/formdecision` — the `atlasDecision` block and its bindings (`Parse`, `Check`:
  bound fields, rows, cycles), the path of a field's value through groups and dynamic
  lists, `OptionsOf` (form-js's normalization, the 1 000-entry bound), `FieldOptions`
  with the rule that a bound value is one its field offers, the editor's routes,
  `WriteFieldOptions` for the console's and the public routes, `Resolve` for public
  pages and `CheckSubmission`. `api/formdecisions.go` is its reach into the server: the registry
  read on the run loop, the evaluation off it.
- `dmn.Registry.LatestDeployedDecision` / `LatestDeployedDecisions` — the newest
  decision deployment of a decision, described, and the key it is evaluated under.
- The form save (`handleSaveForm`), the form's options route (`handleFormFieldOptions`),
  task completion (`checkTaskSubmission`, `taskVariables`), the public start link
  (`handlePublicFormSchema`, `handlePublicFieldOptions`, `handlePublicFormStart`) and the
  public wizard (`publicStepOf`, `handlePublicWizardFieldOptions`,
  `handlePublicWizardComplete`).
- `api/web/vendor/form-js/src/atlas/decision-source.js`, `viewer.jsx`, `editor.jsx`,
  the two entries, and the rebuilt bundles; `api/web/formviewer.js` `decisionOptions`,
  given to every form by the pages that render one (`app.js`, `incidents.js`,
  `shop.js`, `public-form.html`); `api/web/form-editor.js` passes the decisions and the
  service to the Playground and the Validate tab.
- `api/web/vendor/dmn/src/atlas/` — `modeler.js`, `list-editor.jsx`, `drd-list.js` —
  and the rebuilt DMN bundle; `api/web/dmn-editor.js` (the list hint, the tab looked up
  again); the decision documentation collects and prints a list's entries
  (`decision-doc.js`, `decisiondoc.Decision.List`).

Tested by `dmn/latestdeployed_test.go`; `api/formdecision` (schema, normalization,
routes, resolution, submission); `api/formdecision_http_test.go` (the whole path over
HTTP against a real server: the picker, the options, the save check, task completion,
a public link's schema and start, a new version of the decision shown without touching
the form, and a group chosen deciding the members offered — in the console, on a public
link and on a wizard screen, and at completion); `api/decisiondoc`; and the Playwright specs
`form-decision-options`, `dmn-list-editor` and `decision-doc`.

## Links

- extends [ADR-0473](0473-form-remote-data-sources.md) (an options source beside form-js's three)
- relies on [ADR-0319](0319-durable-versioned-decision-deployments.md) (decision deployments)
- relates to [ADR-0028](0028-forms-and-the-tasks-app.md), [ADR-0062](0062-embedded-dmn-editor.md)
