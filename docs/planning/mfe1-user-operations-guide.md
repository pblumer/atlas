# MFE1 mobile work experience — user and operations guide

Status: browser UI implemented and CI-tested in PR #1472; physical iOS/Android acceptance [#1451](https://github.com/pblumer/atlas/issues/1451) remains open. This is not a mobile release certification.

## User journey

On a narrow phone viewport, use the compact work navigation to reach Tasks and authorized Process Start entries. Available actions depend on the signed-in user's roles and server authorization; a visible navigation entry is not a permission grant. On a wider tablet or desktop, the layout adapts to available width. Forms use responsive form-js layouts; review required fields and validation feedback before submitting.

Task completion must be acknowledged by the server. Do not treat a tap, spinner, offline state or lost response as proof of durable completion. If a response is lost, refresh and inspect task status before attempting another action.

## Freshness and incidents

The foreground UI polls for task and incident updates on an approximately 10-second cadence. Polling is not push notification, guaranteed delivery latency or a background alarm service. A suspended browser or offline device may not receive updates until it becomes active and reconnects. Incident visibility and actions remain authorization-gated. MFE2 mobile incident triage and MFE3 secure alert delivery are future work.

## Operational checks

Verify signed-in and signed-out navigation, role-limited Process Start, completion receipts, polling after a new task or incident, transient API errors, offline/reconnect, tablet landscape and 200% zoom. Do not imply that ordinary users have new process-start grants: permission expansion requires separate ADR approval (#1471).

Automated browser evidence: `e2e/mfe1-mobile-entry.spec.mjs`, `e2e/mfe1-mobile-work-shell.spec.mjs`, and `e2e/console-menu.spec.mjs`. Browser CI does not replace physical-device tests. Record actual iOS Safari and Android browser versions, orientation, input/keyboard behaviour, form-js validation, accessibility and screenshots in #1451.

## Delivery boundaries

MFE0 hardware acceptance remains open. QR capture, offline draft synchronization, incident acknowledgement/escalation, background push alarms, watch companion and full tablet authoring are not MFE1-shipped capabilities. Refer to [programme plan](mobile-field-experience-program.md) and [device validation runbook](mfe0-device-validation-runbook.md).
