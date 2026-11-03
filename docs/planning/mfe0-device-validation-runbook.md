# MFE0 real-device acceptance and handoff runbook

**Programme:** [Mobile & Field Experience](mobile-field-experience-program.md), MFE0 (Milestone S).  
**Decision:** [ADR-0514](../adr/0514-mobile-first-tasks-start-and-incident-triage.md) — Accepted, implementation Not started.  
**Tracking:** [MFE0 issue #1451](https://github.com/pblumer/atlas/issues/1451).  
**Purpose:** Provide repeatable evidence to complete MFE0 without mistaking passing Chromium viewport tests for actual iOS/Android acceptance.

## Starting position and scope

The existing [Tasks responsive tests](../../e2e/tasks-responsive.spec.mjs) cover
320/360/390/600/768/1024/1280 CSS-pixel viewports and selected-detail navigation.
[Form refusal tests](../../e2e/form-refusal.spec.mjs) exercise real vendored
form-js required-field validation, submit request payloads and preservation of
entered values across viewport/rotation changes. The relevant test PRs,
[#1453](https://github.com/pblumer/atlas/pull/1453) and
[#1454](https://github.com/pblumer/atlas/pull/1454), have merged with passing
pull-request CI.

**Not yet proven:** mobile hardware behaviour, complex form-js layouts,
software keyboard and select/date controls, 200% text/browser zoom,
safe-area insets, screen readers, actual foldable segments, authenticated
deep links and field photo capture. A green Chromium CI result does not
fill any of those gaps.

Use a **non-production test instance** with authentication enabled and
synthetic process/task data. Use the normal UI and supported HTTP APIs
without direct storage or event-log modifications. Do not put private
documents, personal data, tokens, passwords or customer identifiers in
screenshots, logs or GitHub issues. Testers retain raw artefacts privately
when access restrictions require it; record a redacted evidence reference.

## Evidence header — fill in for every run

Copy this block to a dated evidence file under a suitable approved location
or to [issue #1451](https://github.com/pblumer/atlas/issues/1451). Never
replace an unexecuted case with a guessed `pass`.

```text
Run ID: MFE0-YYYYMMDD-<device>
Date/time + timezone:
Tester:
atlas commit/version + test server:
Authentication on: yes / no (no => auth cases not tested)
Device manufacturer/model:
OS/build:
Browser/version:
Screen/posture/orientation:
Reported CSS viewport size (if measured):
Text/browser zoom or text-size setting:
Input method: touch / physical keyboard / assistive technology
Test process/form version or synthetic fixture:
Case IDs executed:
Result for each: PASS / FAIL / BLOCKED / NOT RUN
Evidence: redacted screenshot/video/test output reference
Issue for every FAIL/BLOCKED:
Security/privacy notes (no secrets):
```

Record **BLOCKED** when the device, permissions or test form is missing;
**NOT RUN** when the test was not attempted. A CSS viewport emulation on a
desktop browser is **NOT RUN** for the matching hardware test.

## Required device coverage

| Device class | Minimum concrete environment | Key risks |
| --- | --- | --- |
| iPhone | Real iPhone, Safari, current installed iOS; portrait + landscape | Dynamic browser bars, notch/safe areas, keyboard/focus, native date/select |
| Android phone | Real Android handset, Chrome; portrait + landscape | Back gesture, keyboard resize, select/file picker, font scaling |
| Tablet | Physical iPad Safari or Android tablet; portrait + landscape | List/detail reflow, focus/data retention, optional hardware keyboard |
| Foldable | Real folded/unfolded device when available; otherwise **BLOCKED** | Hinge/viewport segments, data and focus preserved during posture change |
| Assistive tech | VoiceOver on iOS and TalkBack on Android when available | Spoken field labels/errors, landmark navigation, action order |
| Larger text / zoom | 200% browser zoom where supported; mobile OS large-text where browser zoom differs | Controls do not overlap, clip or hide required validation/actions |

Do not map physical device models to fabricated CSS widths. Capture the
actual viewport if measurable; keep the synthetic 320–1280px Chromium
matrix as **separate automated evidence**. For foldable testing, record
both postures and whether the browser exposes distinct viewport segments.

## Test data preparation

1. Sign in to an isolated atlas test instance as an ordinary task assignee;
   separately have a permitted process starter and a designated incident
   operator (with an explicit non-operator account for denials).
2. Prepare one executable test process with a **normal BPMN user task**
   assigned to that user; make a second task unassigned to the user so the
   restricted case can be checked without leaking its data.
3. Bind a real form-js test form with required text, date/select and a
   group of several fields; use synthetic values. Add a multi-column/long
   label form **if its schema is supported by the installed form-js**.
   Record when that fixture cannot be created instead of assuming the
   renderer supports a particular undocumented component.
4. Prepare a permitted **Start Process** form. Use the current, documented
   Tasks and Process Start UI; do not assume that MFE1's new mobile landing
   page or MFE4 QR/photo evidence feature already exists.
5. For incident checks, create a synthetic worker failure and a role-scoped
   operator account. An **engine incident** is not a **business finding**
   and neither implies an **acknowledged human alarm**.

## Executable cases

| ID | Action on the real device | Pass criterion |
| --- | --- | --- |
| HW-01 | Open Tasks list, choose assigned task, return to list; repeat after rotation | List/detail, Back, title, focus and primary action accessible; no unexpected horizontal page scroll |
| HW-02 | In form-js, enter a field then trigger a missing-required-field error | Exact missing field is identified visibly and accessibly; no completion request; entered values persist |
| HW-03 | Keep draft inputs while rotating, opening/closing keyboard, and moving between list/detail where supported | No unintended submit, no lost values, no hidden primary action or blocked focus |
| HW-04 | Exercise actual date/select and file chooser on device using a supported form fixture | Native control can open, select, return focus and retain its value; **not** a claim of MFE4 evidence upload |
| HW-05 | Open Process Start, enter values, rotate, submit valid form, then repeat with invalid form | Invalid input cannot start a process; valid request happens explicitly; confirmed result is shown only after server acknowledgement |
| HW-06 | Inspect at 200% browser zoom (where supported) or OS enlarged text; repeat errors/submit | Text and essential actions stay perceivable, reachable and readable; capture any clipping |
| HW-07 | Use VoiceOver/TalkBack/keyboard to navigate actions and failed validation | Meaningful labels, predictable focus order, spoken errors, no hover-only or gesture-only critical action |
| HW-08 | Sign out, lose authentication, open stale link or try a task not assigned to the user | No protected task or incident data disclosure; server 401/403 respected; no success state on refusal |
| HW-09 | As operator, view bounded incident summary; as non-operator, attempt same view | Only authorised summary/detail visible; truncation/ungrouped states represented; no human acknowledgement UI implied |
| HW-10 | Test offline, delayed response and loss of response while editing, without expecting offline completion | Clear failure/unknown state; no assertion of success until confirmed; **MFE5** idempotent reconciliation remains out of scope |
| HW-11 | On supported foldable, change posture with a partially filled form | Data, field focus and actions remain usable and do not straddle the hinge; record device capabilities |
| HW-12 | Follow an authorised deep link to a task and a process start; attempt as another user | Correct context and assignment checks, sign-in flow, no implicit completion/start and no leaked information |

Only check a case as **PASS** when executed on listed hardware and evidence
has been captured. No code change or new API is authorised by this runbook.

## Recording failures and decisions

For a failed test capture: case ID, device/browser, exact reproduction
steps, expected/actual result, redacted evidence, severity, and proposed
owner/slice. Do not conflate a CSS overflow defect (potential MFE1)
with an incident permission leak (security gate for MFE2) or a missing
durable alarm acknowledgement (MFE3, separate ADR).

MFE0 can be marked **complete** only when the programme owner has reviewed
the evidence matrix, resolved or explicitly accepted all safety/security/
accessibility blockers, documented unsupported devices and chosen slice
handoffs. Update [the programme manifest](../programs/mfe/program.json)
with exact evidence paths and owner review; regenerate
`docs/programs/README.md` using `python3 scripts/programs.py`.

## Ready-to-implement follow-up issues

- [MFE1: first mobile Tasks and Process Start vertical slice (#1456)](https://github.com/pblumer/atlas/issues/1456):
  test-first adaptive Tasks/Start journey, strictly existing APIs/form-js.
- [MFE2: first read-only mobile incident triage vertical slice (#1457)](https://github.com/pblumer/atlas/issues/1457):
  operator-scoped bounded incident summary and read-only detail first.
- [MFE0 device acceptance and programme exit (#1451)](https://github.com/pblumer/atlas/issues/1451):
  remains open while hardware and accessibility evidence is missing.

These follow-up slices remain **planned**, not active or delivered. Never
substitute a new native renderer, runtime-side notifications or offline
completion store for a missing MFE0 acceptance result.
