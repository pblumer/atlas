# MFE1 safety inspection – deployable vertical example

This example moves the safety inspection from PR #1469's synthetic browser fixture
into a real BPMN model and form-js schema, without changing the engine or HTTP API.

## Scenario

1. An authorised operator starts `proc_mfe1_safety_inspection` on a mobile or desktop
   Tasks > Start page (until issue #1467 is decided, ordinary `user` accounts may
   not start arbitrary processes).
2. A permitted task user opens/claims the unassigned "Sicherheitskontrolle
   durchführen" task in My Tasks, enters the required site, asset, inspector and
   result, and submits. No automatic assignment is assumed.
3. The existing task-completion API verifies the submission. The mobile UI
   only shows its receipt when the server confirms completion (PR #1469).
4. Explicit `safetyCheckResult = "ok"` ends at "Kontrolle bestanden"; every
   other result takes the default "Befund erfasst" path. A business finding
   is NOT a technical engine incident.

## Deploy as one atlas application

The files are an authoring sample, **not** automatically installed by atlas.

- Create an application such as `MFE – Sicherheitskontrolle`.
- In that application, save the form from
  [safety-inspection.form.json](safety-inspection.form.json) with the exact id
  `mfe1-safety-inspection`.
- Import [safety-inspection.bpmn](safety-inspection.bpmn) into its Modeler
  draft, validate the BPMN model, then publish/deploy the application.
- As an operator, start "Sicherheitskontrolle vor Ort". Sign in with a
  permitted Tasks user, claim the unassigned task where necessary and complete
  it. Try both results and a missing required field.

The same sequence can be automated via the public HTTP API or atlas MCP
application/form/draft/publish tools. Task visibility and assignment are
subject to the real server's authorisation configuration; the example creates
no users or permissions.

## Checks and boundaries

`go test ./examples -run TestSafetyInspection` checks that the BPMN compiles,
that the form reference resolves, and that required fields and the fail-closed
business branch remain present. The CI Go checks and browser E2E suite remain
mandatory. Actual phone/tablet acceptance belongs to MFE0 #1451.

Not included: camera/evidence uploads (MFE4), QR links (MFE4), offline completion
and unknown-response reconciliation (MFE5), push alarms or alarm acknowledgements
(MFE3), and unauthorised/self-service process starts (#1467). No attachments or
secrets are persisted in the model.
