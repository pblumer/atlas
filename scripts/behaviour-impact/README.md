# Behaviour impact

`impact.py` answers, before or after an upgrade, which deployed models an engine
behaviour change touches. It reads the BPMN of the deployed definitions and reports each
element whose behaviour changed, how certain that is, and what to do about it. Today it
knows the changes of [ADR-0508](../../docs/adr/0508-scope-aware-messaging-and-compensation.md)
(scope-aware messaging and compensation).

```bash
# Against a server: the newest version of each process and every version still running.
scripts/behaviour-impact/impact.py --server https://atlas.example.org --token "$ATLAS_TOKEN"

# As a gate before an upgrade: exit 1 when a model behaves differently.
scripts/behaviour-impact/impact.py --server https://atlas.example.org --fail-on changed

# Against exported models, as JSON.
scripts/behaviour-impact/impact.py --dir exported/ --json
```

`--all-versions` checks every deployed version, `--key` one definition (repeatable). The
token (or `ATLAS_TOKEN`) needs the operator role: the check reads
`GET /api/v1/instances/summary` to choose the definitions and
`GET /api/v1/processes/{key}/xml` for each model. Python 3.8 or later, standard library
only, so it runs from any machine that reaches the server.

## What a finding says

| Severity | Meaning |
|---|---|
| `changed` | The element behaves differently whenever it runs. Read the message and decide. |
| `review` | It may: that depends on names the model does not state — a worker's result, a form's fields, a message's payload. Check them. |
| `info` | It behaves differently, but nothing a model reads changes. |

| Rule | Looks for | Engine function |
|---|---|---|
| `compensation.waits` | A compensation throw that starts handlers waits for them before the next step (`waitForCompletion`, default true). An end event: `info`. | `triggerCompensation`, `releaseCompensationWaiters` |
| `compensation.event-subprocess` | A compensation event directly in an event subprocess compensates the scope the event subprocess handles, not its own run. | `compensationScope` |
| `receive.output-mappings` | A receive task with output mappings keeps the payload to itself; only the mapped values reach the instance. `changed` when the model reads the rest. | `writePayload` |
| `correlation.scope` | A correlation key resolves up the scope chain from the waiting or sending element. `changed` when a scope on the way declares a name the key reads. | `evalCorrelationKey` |
| `payload.scope` | A payload variable updates the nearest scope that declares it, not the root, and is dropped with that scope. | `writePayload`, `propagationScope` |
| `event-subprocess.runs` | A non-interrupting message or signal event subprocess re-arms at once, and each run reads its own message. | `rearmFiredTrigger`, `bindTriggerPayload` |

A receiver or sender at the process root without mappings of its own is never reported:
there the scope chain is the root alone, as it was before.

## What it cannot see

- **Names the model does not state.** What a worker returns, what a form submits and what a
  message carries are decided at run time; a finding that depends on them is `review`.
- **What already happened.** A subscription opened before the upgrade keeps the key it was
  opened with, and a compensation throw that completed before it has completed.
- **Versions nothing runs.** Without `--all-versions`, an old version with no running
  instance is skipped; it matters only if somebody starts it by its key.

## How it is kept right

`impact_test.go` runs in the ordinary `go test ./...` sweep (and skips where there is no
`python3`):

- every fixture in `testdata/` says in a comment what the check must report about it, and
  the test holds the check to that — the case each rule reports and the neighbouring case
  it must leave alone;
- what the check reads from a model — the scope of each element, which subprocess is an
  event subprocess, which `zeebe:ioMapping` the compiler wires, what a compensation event
  waits for and names — is compared with the compiler's reading of every BPMN model in the
  repository;
- the variables the check finds in a FEEL expression are compared with the FEEL compiler's
  `Inputs()` for every expression and correlation key in those models;
- against a stub server, it reads the summary, fetches only the definitions it should and
  sends the token.

To hold the check to models of your own, export them and add their directory:
`IMPACT_CORPUS=exported/ go test ./scripts/behaviour-impact/`.

## Adding a rule

A later behaviour change adds one rule function that names the engine function it
mirrors, a `RULE_ADR` entry with its decision record, a fixture whose comment says what
must be reported, a row in the test's table and a row above.
