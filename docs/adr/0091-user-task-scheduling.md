# ADR-0091: User-task scheduling — priority and due date

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Atlas maintainers

## Context and problem statement

The Tasks app is an Outlook-style inbox (ADR-0028): a person opens a parked user
task, fills its form, and completes it. What the inbox is missing is the *time
and importance* dimension every real task list has — **which task is urgent, and
which is due soon**. Camunda's user-task panel exposes exactly this: a **priority**
and a **task schedule** (due date / follow-up date). Without them the inbox can
only be sorted by chance of arrival, and nothing flags an overdue task.

The question: **how does Atlas attach a priority and a due date to a user task**,
surface them in the inbox, and do it without breaking an engine invariant (no
hot-path allocation, deterministic `applyToState`, one durable event log)?

## Decision drivers

- **Reuse the compiled-metadata pattern.** Assignee, candidate groups, and the
  form id already ride on the compiled process (ADR-0028); priority is the same
  kind of static, model-authored attribute and should travel the same way.
- **A due date is per-instance.** "Due three days after the task appears" means a
  concrete instant that differs for every instance, so it must be computed when
  the task is created and frozen as a fact — exactly how Atlas already handles
  timer due dates (ADR-0040).
- **Honor the invariants.** Parse durations at compile time (no hot-path
  allocation); compute the absolute due instant from the command-time clock and
  record it in the job-created event, so `applyToState` stays deterministic and
  replay reproduces the same value.
- **No storage-format churn.** `model.JobValue` already carries a persisted but
  dormant `Deadline int64` field; a user-task due date *is* a deadline, so it can
  occupy that field with no wire-format change.

## Decision

**Priority** is a static, model-authored integer (`zeebe:priority`, default 50 to
match Camunda). The compiler parses it into `UserTaskDetail.Priority`; the API
reads it back from the compiled process at list time, just like candidate groups.

**Due date** is authored as an ISO-8601 **duration** relative to task creation,
carried on `zeebe:taskSchedule`'s `dueDate` attribute. This mirrors Atlas's
existing timer semantics, which are duration-only (ADR-0040 / `compiler/duration.go`)
rather than absolute datetimes or FEEL — keeping one consistent notion of "time"
across the engine. The compiler parses the duration to nanoseconds
(`UserTaskDetail.DueDateNanos`, 0 = unset). When the user-task job is created
(`userTaskBehavior.OnActivated`), the behavior computes
`Deadline = c.Now() + DueDateNanos` and records it on the job-created event —
`Now()` is read once during command processing and frozen into the fact, so
recovery replays the identical instant. The API exposes the job's `Deadline` as
the task's `dueDate` (Unix milliseconds, 0 = none); the inbox renders it, sorts by
it, and flags a task whose due date has passed as **overdue**.

A task with no schedule keeps `DueDateNanos == 0`, so its job's `Deadline` stays
0 and the inbox shows no due date — fully backward compatible with tasks deployed
before this change.

## Consequences

- The inbox gains a real priority sort and an overdue indicator; a modeler can
  set both from the user-task panel.
- Due dates are **relative to task creation**, not absolute calendar dates. This
  fits Atlas's duration-only time model but means "due on 2026-08-01" is not yet
  expressible; an absolute-date or FEEL due date is a later extension.
- **Follow-up date** is intentionally out of scope here: it would need a second
  per-job timestamp (a real wire-format change) rather than the dormant `Deadline`
  field, so it is deferred.
- `JobValue.Deadline` now has a concrete meaning (user-task due instant). Should a
  future job-lease/lock-expiry mechanism want a deadline, it must not assume this
  field is free for user-task jobs.
