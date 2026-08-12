# incident

Conformance micro-fixture: a job failure that raises and resolves an incident.

Start -&gt; serviceTask "risky" -&gt; End. The driver plays a worker that fails the job with no retries left (Fail), which raises an incident and takes the job off the activatable index; then an operator resolves the incident with fresh retries (Resolve), re-activating the job; then the worker completes it (Complete). The Resolve step fails loudly if no incident is present, so this scenario only passes if the incident was genuinely raised.

![incident diagram](../diagrams/incident.png)

- **Model:** [`incident.bpmn`](../../models/incident.bpmn)
- **Features:** `incident` (Job failure raises an incident; resolve resumes it)

## How it is driven

**Start:** explicit `CreateInstance` on the root process.

**Steps:**

1. Fail job `risky` with no retries — raises an incident (`boom`).
2. Resolve the incident on `risky`, re-activating its job.
3. Complete job `risky`.

## Expected outcome

- **Completed:** yes
- **Path:** `start` → `risky` → `end`

---
_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._
