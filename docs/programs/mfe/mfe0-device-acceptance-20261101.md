# MFE0 physical-device acceptance sheet

Status: **pending actual device evidence**. This checklist prepares issue #1451; browser emulation and green CI do not constitute physical-device acceptance.

## Devices and evidence

Record the device model, OS/browser version, viewport/orientation, test account role, date, build SHA, expected/actual outcome, screenshot or recording reference, and linked issue for each run. Do not record secrets, tokens, personal data or confidential incident messages in screenshots.

| Scenario | iPhone Safari | Android Chrome | Tablet portrait | Tablet landscape | 200% zoom |
| --- | --- | --- | --- | --- | --- |
| Tasks list, assignment and server-confirmed completion | pending | pending | pending | pending | pending |
| Start list, empty state, form validation, denied access | pending | pending | pending | pending | pending |
| Complex two-column form-js, software keyboard, safe areas | pending | pending | pending | pending | pending |
| MFE2 incident triage, truncated causes, role denial | pending | pending | pending | pending | pending |
| Keyboard, screen-reader labels and focus order | pending | pending | pending | pending | pending |
| Session expiry, reconnect and double-tap prevention | pending | pending | pending | pending | pending |

Also test at least one foldable or explicitly mark it untested. Confirm that no client shows a successful process start or task completion before the server confirms durable success.

## MFE1 scoped-start security cases (ADR-0516)

Before a user-facing start is enabled, collect API evidence for deny-by-default, user/group grants, cross-application collisions, hidden/retired deployments, admin-only grant mutation, revocation, manipulated form values, auth-disabled mode, machine tokens, 401/403/404, and post-fsync startedBy/recovery equivalence. A passing pure grant evaluator test does **not** satisfy these end-to-end requirements.

## Release gate

Do not close #1451 or declare mobile release acceptance without recorded physical-device evidence. Keep operator-only Start behaviour until the authenticated scoped-start API, sidecar persistence and recovery tests are integrated.
