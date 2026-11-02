# MFE1 mobile work experience — user and operations guide

Status: browser UI implemented and CI-tested in PR #1472; physical iOS/Android acceptance [#1451](https://github.com/pblumer/atlas/issues/1451) remains open. This is not a mobile release certification.

## User journey

On a narrow phone viewport, use the compact work navigation to reach Tasks and authorized Process Start entries. Available actions depend on the signed-in user's roles and server authorization; a visible navigation entry is not a permission grant. On a wider tablet or desktop, the layout adapts to available width. Forms use responsive form-js layouts; review required fields and validation feedback before submitting.

Task completion must be acknowledged by the server. Do not treat a tap, spinner, offline state or lost response as proof of durable completion. If a response is lost, refresh and inspect task status before attempting another action.

## Freshness and incidents

The foreground UI polls for task and incident updates on an approximately 10-second cadence. Polling is not push notification, guaranteed delivery latency or a background alarm service. A suspended browser or offline device may not receive updates until it becomes active and reconnects. Incident visibility and actions remain authorization-gated. MFE2 mobile incident triage and MFE3 secure alert delivery are future work.

## Operational checks

Verify signed-in and signed-out navigation, role-limited Process Start, completion receipts, polling after a new task or incident, transient API errors, offline/reconnect, tablet landscape and 200% zoom. Do not imply that ordinary users can start processes yet: the self-service start routes of ADR-0516 are not implemented, so an administrator's start grant (below) does not yet enable any start.

Automated browser evidence: `e2e/mfe1-mobile-entry.spec.mjs`, `e2e/mfe1-mobile-work-shell.spec.mjs`, and `e2e/console-menu.spec.mjs`. Browser CI does not replace physical-device tests. Record actual iOS Safari and Android browser versions, orientation, input/keyboard behaviour, form-js validation, accessibility and screenshots in #1451.

## Delivery boundaries

MFE0 hardware acceptance remains open. QR capture, offline draft synchronization, incident acknowledgement/escalation, background push alarms, watch companion and full tablet authoring are not MFE1-shipped capabilities. Refer to [programme plan](mobile-field-experience-program.md) and [device validation runbook](mfe0-device-validation-runbook.md).

## Administering start grants (ADR-0516)

An administrator decides which people or groups may later start one process of one application from the self-service Start view. This is the administration half only; the self-service routes that read it are not implemented yet, and until they are, a grant lets nobody start anything.

- **Where:** `GET /api/v1/applications/{id}/start-grants` lists an application's grants; `GET`, `PUT` and `DELETE /api/v1/applications/{id}/start-grants/{processId}` read, grant or replace, and revoke one; `…/history` lists every audited change, newest first; `…/preview?userId=` or `?groupId=` answers whether that account or group would be offered the process and why not. All are admin-only and not available to AI agents over MCP.
- **Who may change a grant:** an administrator signed in with their own session. API tokens, OAuth-connected applications, application owners and editors cannot. A server running without authentication refuses all of these routes.
- **What a grant names:** user ids and group ids from this server's directory (`GET /api/v1/users`, `GET /api/v1/groups`), never user names or group names. The application must exist and must not be a protected system application, and the newest deployed version of the process must belong to that application. Send `expectedRevision` (0 for a process never granted); a 409 means another administrator changed it first — read it again and decide on the current revision.
- **Revoking:** `DELETE …?expectedRevision=N`. Every authorization check after the revocation is durable refuses; a start already authorized before it may still finish. Revoking works even after the application or process was deleted; revoking twice changes nothing.
- **When a write fails:** the answer is 500 and the grant is not trusted. Every start-grant route then answers 503 until the server is restarted, which reads back what actually reached the disk. Check disk space and the data directory's permissions before restarting.
- **Backups:** grants live in the `start-grants` directory of the data directory, are classified as `credential` in the backup inventory and are not part of an application's portable export. Restoring an older backup restores older grants; treat it like any other permission rollback.

