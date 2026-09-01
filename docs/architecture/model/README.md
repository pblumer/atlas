# ArchiMate model export (Open Exchange)

[`atlas.xml`](atlas.xml) is the Atlas enterprise-architecture model serialized in the
[ArchiMate® 3.0 **Model Exchange File Format**](https://www.opengroup.org/xsd/archimate)
— the tool-neutral, standardized XML defined by The Open Group. It is the formal,
round-trippable counterpart to the human-readable
[`../enterprise-architecture.md`](../enterprise-architecture.md) and its
[SVG diagrams](../diagrams/): the *same* view, expressed as a real model that an
ArchiMate tool can load, analyze, and re-draw.

This is the follow-up left open in
[ADR-0099](../../adr/0099-archimate-enterprise-architecture-view.md) (Option 4): a
generated `.archimate` / Open Exchange export, added *without* changing the decision
that the Markdown view remains the primary, in-repo artifact. The model here stays
**subordinate to the code and the deep-dives** — when they disagree, they win, and
this export is regenerated to match.

## What's in it

The full layered view as model elements and relationships, organized into a folder
tree by ArchiMate layer, plus generated diagram views:

| Aspect | Contents |
|--------|----------|
| **Motivation** | Stakeholders, drivers, assessments, goals, outcomes, the four principles, the six invariants as requirements, and the constraints |
| **Business** | Roles, business services, processes, events, objects, and the *Durable Execution* contract |
| **Application** | The Atlas Engine core and its sub-components, channels, workers, application services, data objects, interfaces |
| **Technology** | Single binary, Go runtime, partitions, Pebble, filesystem, technology services, paths, artifacts |
| **External Systems** | temis, clio, mail providers, external job workers |
| **Implementation & Migration** | Plateaus M0–M6 (with a `Roadmap status` property) and the parallel workstreams |
| **Views** | Overview, motivation trace, business, application, technology & deployment, and the implementation roadmap |

Two custom properties are defined and attached: **ADR** (the deciding ADR for an
element, where there is one) and **Roadmap status** (`done` / `in progress` /
`planned`, on the plateaus).

## Opening it in a tool

In [Archi](https://www.archimatetool.com/):

1. **File → Import → Other… → Open Exchange File**, then pick `atlas.xml`.
2. Archi builds the model tree, the folders, and the diagrams. From there you can
   save it as a native `.archimate` file, re-lay-out the views, or run analysis
   (element/relationship reports, model validation, impact analysis).

Any other ArchiMate-conformant tool that supports the Open Exchange format can import
it the same way.

## Regenerating

Edit the model by editing the **generator**, never the XML by hand:

```bash
python3 gen_open_exchange.py     # rewrites atlas.xml in this directory
```

The generator ([`gen_open_exchange.py`](gen_open_exchange.py)) holds the model as
plain Python lists — `ELEMENTS`, `RELATIONSHIPS`, `PROP_DEFS`, `ORG_FOLDERS`, and
`VIEWS` — and emits the exchange XML deterministically (no timestamps, stable IDs), so
a regeneration is a clean, reviewable diff. It needs only the Python standard library.

When you change the view in `../enterprise-architecture.md` or the diagrams in
`../diagrams/gen_diagrams.py`, update the corresponding entry here in the same PR so
the three stay in step.

## Exporting a single BPMN process to Archi

`atlas.xml` above is the *architecture* model. To take a concrete **BPMN process** —
one of the `.bpmn` files Atlas runs — into Archi instead, use
[`bpmn_to_archimate.py`](bpmn_to_archimate.py). Archi speaks ArchiMate, not BPMN, so
this converts the process into the same Open Exchange format:

```bash
# a file with exactly one process:
python3 bpmn_to_archimate.py ../../../examples/order-to-cash.bpmn
# -> writes examples/order-to-cash.archimate.xml

# choose one when the file holds several processes, and name the output:
python3 bpmn_to_archimate.py path/to/models.bpmn --process order-to-cash -o out.xml
```

Then import the resulting `.xml` into Archi (**File → Import → Other → Open Exchange
File**). The mapping is:

| BPMN | ArchiMate |
|------|-----------|
| start / end / intermediate / boundary event | Business event |
| task, user/service/script task, call activity, subprocess | Business process |
| gateway (exclusive, parallel, inclusive, …) | Business process (a routing step; original kind kept in a `BPMN element` property) |
| sequence flow | Triggering relationship (its name or condition becomes the label) |

The BPMN diagram geometry (shape bounds and edge waypoints) is reused, so the imported
ArchiMate view keeps the process's original layout. Gateways map to a plain routing
step rather than an ArchiMate And/Or junction to keep the output always schema-valid;
refine them by hand in Archi if you want the stricter junction semantics.
