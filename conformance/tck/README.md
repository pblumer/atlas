# BPMN conformance cases — a portable TCK format

This directory re-expresses the conformance suite as **language-neutral test
cases**, so an engine other than Atlas can run them without reading any Go. It is
the seed of a BPMN *execution* Technology Compatibility Kit — the thing the
ecosystem has for DMN (the [DMN TCK](https://dmn-tck.github.io/tck/)) but not for
BPMN execution. The [BPMN MIWG](https://www.omg.org/bpmn/) checks model *interchange*
(can engines round-trip the XML); this checks *execution semantics* (does the engine
run the model to the right outcome).

Everything here is **generated** from the Go register by
`go test ./conformance -update`. Do not edit by hand — edit the scenario and
regenerate. A stale file fails `TestTCKCasesUpToDate`.

## Layout

```
cases/<name>/           a positive case — must run to the expected outcome
  model.bpmn            the process model (Atlas/Zeebe dialect)
  case.json             how the instance is born and driven
  expected.json         the outcome Atlas produces
negative/<name>/        an adversarial case — must be rejected at compile
  model.bpmn
  case.json             { rejected: true, reason }
```

## `case.json`

```jsonc
{
  "name": "incident",
  "model": "incident.bpmn",
  "features": ["incident"],
  "patterns": ["WCP-4"],            // control-flow patterns realized, if any
  "root": "",                       // BPMN id to instantiate; "" = the sole process
  "start": { "kind": "explicit" },  // or message / timer / signal — see below
  "driver": [                       // ordered steps to advance parked tokens
    { "action": "fail",    "element": "risky", "message": "boom" },
    { "action": "resolve", "element": "risky" },
    { "action": "complete","element": "risky" }
  ]
}
```

**Start kinds** — how the instance is born:

| kind | fields | meaning |
|------|--------|---------|
| `explicit` | — | create an instance directly (none start event) |
| `message` | `name`, `correlation` | publish a message to a message start event |
| `timer` | `afterMs` | advance the clock past a timer start event |
| `signal` | `trigger` | instantiate the trigger process, whose signal throw broadcasts |

**Driver actions** — how a parked token is advanced:

| action | fields | engine effect |
|--------|--------|---------------|
| `complete` | `element`, `vars?` | complete the job of that task (user/service) |
| `publish` | `message`, `correlation`, `vars?` | deliver a message to a waiting subscription |
| `wait` | `afterMs` | advance the clock past a due timer and fire it |
| `fail` | `element`, `message` | fail a job with no retries, raising an incident |
| `resolve` | `element` | resolve the incident on that element, re-activating its job |
| `throwError` | `element`, `code` | make a job throw a business error a boundary catches |

## `expected.json`

```jsonc
{
  "completed": true,                    // did the (root) instance complete
  "activities": ["end","risky","start"],// the set of activities that ran (sorted, unique)
  "path": ["start","risky","end"],      // Atlas's ordered token path (engine detail)
  "variables": { },                     // Atlas's final root-scope variables (engine detail)
  "dataObjects": [ ]                    // "name[state]=value" (engine detail)
}
```

The **portable comparison key is `completed` + `activities`** — the control-flow
outcome any engine can reproduce. `path`, `variables`, and `dataObjects` are Atlas's
richer detail; a foreign engine need not match them (variable/value formatting is not
portable across dialects). This is exactly the normalized projection the
[`../differential`](../differential) oracle already compares against an independent
engine.

## Running a case on another engine — the runner contract

A vendor runner is a program that, given a case directory:

1. deploys `model.bpmn` (translating the executable extensions to its own dialect if
   needed — the control-flow skeleton is portable BPMN, the `zeebe:`/FEEL bits are not),
2. births the instance per `case.json`'s `start`,
3. applies each `driver` step,
4. emits its own `{completed, activities}` projection,

and the harness checks it against `expected.json`. The
[`../differential/reference`](../differential/reference) Node runner is a working
example of this contract for the control-flow subset.

## Status and honest scope

The portable format covers all 30 positive scenarios and 4 negative models. What it
does *not* yet solve — and what a real, adopted TCK needs — is the
executable-extension portability problem (each vendor's scripts/task-types differ) and
governance (a single vendor's kit becomes a TCK only when others adopt it). See the
suite [README](../README.md) and the differential oracle for how far the cross-engine
checking currently reaches.
