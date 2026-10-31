# MFE2 implementation contract — read-only mobile incident triage

Tracking: [Issue #1457](https://github.com/pblumer/atlas/issues/1457), [ADR-0514](../adr/0514-mobile-first-tasks-start-and-incident-triage.md).

## Scope of the first vertical slice

Provide a role-gated mobile Incidents destination that reads the existing `GET /api/v1/incidents/summary` endpoint. Render a bounded cause-first grouped list and selected read-only context with process, element, incident type, count and truthful truncation/ungrouped indicators. The server remains the authorization authority.

## Test-first acceptance

1. Add failing Playwright tests for operator versus ordinary-user navigation and 401/403/expired-session responses; no incident metadata is rendered after denial.
2. Add fixtures for empty summary, many groups, `groupsTruncated` and `ungrouped`; do not request an unbounded incident list.
3. Test list and detail at 320, 390 and 768 CSS pixels, keyboard/focus and screen-reader names.
4. Reuse the existing app shell, navigation, incident presentation and API contracts. No new persistence, engine hot-path work or duplicate authorization logic.
5. Confirm there are no resolve, retry, variable-edit or worker-edit actions in this slice. Human alarm acknowledgement belongs to MFE3.
6. Run relevant Go and Playwright suites, update the MFE manifest and generated programme index together, and preserve the full CHANGELOG.
7. Capture physical iOS/Android and tablet landscape results separately in #1451; Chromium E2E alone is not device acceptance.

## Release gate

Do not declare MFE2 delivered until the UI, negative tests, CI and the authorization boundary are reviewed. MFE0 hardware evidence remains open and must not be represented as complete.
