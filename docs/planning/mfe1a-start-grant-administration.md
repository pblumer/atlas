# MFE1A — Start-grant administration in the Console

**Programme:** [Mobile & Field Experience](mobile-field-experience-program.md) ·
**Manifest:** [MFE](../programs/mfe/program.json) · **Slice:** MFE1A ·
**Owner:** pblumer · **Status:** active (planned and started 2026-11-09)

**Decision basis:** [ADR-0516](../adr/0516-scoped-authenticated-process-start.md)
(Accepted / Landed). This slice adds a surface to the decision already taken; it
needs no new ADR. ADR-0516 requires "an auditable management action and a read-only
grant preview", and both exist as admin-only HTTP routes since #1492. What is
missing is a way for an administrator to use them without writing HTTP requests
with ids and revision numbers by hand.

## Outcome

An administrator opens **Console → Administration → Start grants**, picks an
application, sees each of its processes with who may start it and whether it would
be offered at all, grants or revokes it for users and groups picked by name, checks
for one person or group whether they would be offered the process and why not, and
reads the audited history — without ever typing an id or a revision. In the
application's **Deployments** tab, an administrator also sees, per process, whether
start grants exist, with a link to the Console page; nobody else sees that hint, and
it offers no way to change anything.

A task user notices nothing new: the self-service routes and the Start view
(#1495, #1497, #1498) are unchanged.

## Decisions taken with the owner (2026-11-09)

1. **Where:** a Console administration page **and** a read-only hint in the
   application view for administrators. The page is where grants are changed. The
   hint only shows that grants exist and links to the page, because ADR-0516 keeps
   grants away from application owners and editors, who work in that view: a control
   there would read as theirs.
2. **Server:** the UI uses the existing routes, plus one **additive** field:
   `blockers` on the list (`GET /api/v1/applications/{id}/start-grants`) and on the
   single read (`GET …/start-grants/{processId}`), computed by the same
   `selfServiceBlockers` rule the preview and the self-service routes use. Without
   it, the table could only say whether a process would be offered by asking the
   preview once per subject. No new route; no change to what any route permits.

## Scope

### PR A — `blockers` on the grant reads (server, Go)

- `startgrant.GrantView` gains `blockers` (`[]string`, never null) and, for the
  newest version, `definitionKey` and `version`, filled from the resolver the
  service already holds. The list resolves each scope once in the same loop turn;
  per-application cost stays bounded by the number of grant records of that
  application.
- OpenAPI summaries of both routes name the new fields and their codes.
- Tests first (`api/startgrant/service_test.go`, `api/startgrants_http_test.go`):
  an eligible process answers `[]`; deactivated, not executable, newest version in
  another application, deleted application and protected application answer their
  codes; a never-granted scope answers its blockers too; a revoked scope keeps its
  blockers; the uncertain store still answers 503; no field changes on PUT/DELETE
  answers beyond the shared view.

### PR B — Console page, application hint, documentation (web)

- **Route and module:** `#/console/start-grants` in `api/web/app.js`, nav entry
  under Administration with `role: "admin"` beside API tokens and Connected systems;
  the page is a lazy-loaded module `api/web/startgrants.js` in the shape of
  `connectedsystems.js` (`{api, toast, view, isSuperseded}`).
- **Page flow:**
  1. Application picker (`GET /api/v1/applications`); protected system applications
     listed but disabled, with the reason.
  2. Process table for the chosen application, merging
     `GET /api/v1/applications/{id}/deployments` (current versions) with
     `GET …/start-grants` (grant records): process name, newest version, audience
     summary by name ("Field inspectors, Anna Muster"), **offered / not offered**
     with the blocker codes said in words, and the last change (who, when). Grant
     records whose process is no longer deployed in this application stay listed as
     "no longer deployed here", so they can be revoked.
  3. **Edit audience** dialog: search users (`GET /api/v1/users`) and groups
     (`GET /api/v1/groups`) by name, store ids; shows the revision it edits; sends
     `PUT …` with `expectedRevision`. A 409 shows the current audience and asks the
     administrator to decide again — never an automatic retry. A 400 names the
     unknown ids. Granting the same audience again says "nothing changed".
  4. **Revoke** with an explicit confirmation naming the process and the audience;
     `DELETE …?expectedRevision=`; a 409 as above.
  5. **Check a person or group:** `GET …/preview?userId=|groupId=`, rendered as one
     sentence plus the reasons (granted through which group, account disabled, user
     role missing, process blockers).
  6. **History:** `GET …/history`, newest first, actors and audience by name; an id
     that no longer resolves is shown as "removed account" with the id.
- **States, each in words:** loading, no applications, application without
  processes, load failed, saving, 503 (grants untrusted until restart — say so and
  disable changes), 403 on a server without authentication ("start grants need
  sign-in"), session expired (401).
- **Application hint:** in `viewProjectDetail`'s Deployments tab, for
  `mayUse("admin")` only, a small read-only badge per process with a grant ("Start
  grant: 2 groups, 1 person") linking to
  `#/console/start-grants?app=<id>&process=<processId>`; nothing for anybody else,
  and the hint's request failing costs the hint, not the tab.
- **Messages** in `api/web/locales/de.js` first, then `en.js`.
- **Docs:** handbook section *Startfreigaben* (replace "the Console has no screen
  for it yet" with the page, keep the API as the scripted path), MFE1 operations
  guide, ADR-0516 *Admin Console checkpoint*, CHANGELOG `### Added` with the
  key-features marker, `e2e/README.md`, manifest and index.

## Security constraints (hold in review)

- The page is a convenience over routes that already refuse everyone but an
  administrator's own session for changes; it adds no authority. Hiding the nav entry
  is not the check.
- The application hint is read-only, shown only to administrators, and must not
  render an edit control in the view owners and editors use.
- Ids, never names, are sent; names are only displayed.
- A 409 or a failed write is never retried automatically; the administrator decides
  on the current state.
- No new route, so the MCP classification (`mcp/tool_registry_drift_test.go`) and the
  admin allowlist are unchanged; the new field is admin-only data already.

## Acceptance criteria

- [ ] An administrator grants a group and a person a process from the Console page,
      sees it as offered, checks a member with "Check a person", revokes it, and
      reads all three changes in the history — without typing an id or revision.
- [ ] A process that is deactivated, not executable, or whose newest version sits in
      another application is shown as not offered, with the reason in words.
- [ ] A competing change (409) shows the current audience and changes nothing until
      the administrator decides again.
- [ ] A grant whose process is no longer deployed in the application is listed and
      can be revoked.
- [ ] A modeler, operator or task user sees neither the nav entry nor the hint; the
      route opened directly says it is for administrators.
- [ ] On a server without authentication the page says start grants need sign-in.
- [ ] `blockers` on the list and single read match the preview's for every code.
- [ ] Playwright spec(s) for the page and the hint at desktop and 768px, plus the
      existing MFE1 and Console specs unchanged; Go tests for PR A; the full
      `AGENTS.md` sequence green.

## Out of scope

Delegating grant administration to application owners (a new ADR, see ADR-0516); a
cross-application overview route; bulk import or export of grants; time-limited
grants; notifying people when they are granted or revoked; physical-device
acceptance (MFE0 #1451).

## Risks and open points

- **Large directories:** `GET /api/v1/users` and `GET /api/v1/groups` are unpaged.
  With a large mirrored directory the pickers load slowly. The slice filters
  client-side; a paged search route would be a separate server change if a real
  installation needs it.
- **Names of removed identities:** a grant can name an account or group deleted
  since; the page must show it as removed rather than hide it, so it can still be
  revoked.
- **Two places, one truth:** the hint reads the same grant routes as the page; it
  must not cache across navigation, or it can show a grant that was revoked.

## Dependencies

- MFE1 server slices #1492, #1495, #1497 and the Start view #1498 — merged.
- ADR-0516 Accepted / Landed — no further decision gate.
- WIP: MFE already runs MFE0, MFE1 and MFE2 as active under the owner's recorded
  exception; MFE1A stays **planned** until the owner starts it.
