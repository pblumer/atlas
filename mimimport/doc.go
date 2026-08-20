// Package mimimport converts Microsoft Identity Manager (MIM/FIM) workflow
// definitions into Atlas-executable BPMN 2.0 XML.
//
// MIM does not emit BPMN. Its workflows are serialised as XOML — the markup of
// the Windows Workflow Foundation (WF) — and are normally extracted with the
// FIMAutomation cmdlet Export-FIMConfig, which wraps each WorkflowDefinition's
// XOML as an attribute inside a resource-graph XML. This package accepts either
// raw XOML or such a wrapper (it locates and unescapes the embedded XOML) and
// produces a single <definitions> document the Atlas compiler can deploy.
//
// # Losslessness
//
// The translation follows one rule: nothing is silently dropped. Where a WF
// construct has a faithful BPMN counterpart it is emitted natively (see the
// mapping in Convert); where it does not, the activity is still emitted — as a
// typed or plain task placeholder — and its original markup is preserved in an
// <atlas:mimSource> extension element together with a <documentation> note. A
// Report lists every produced node with a status of native, preserved or
// manual-review, so the lossy points are explicit rather than hidden.
//
// The conversion reproduces the workflow's *structure and intent*, not MIM's
// runtime semantics: the authentication/authorization/action request model, the
// resource operations inside individual activities (their UpdatesTable /
// QueriesTable bodies), and diagram layout are intentionally out of scope for a
// first pass and are flagged for manual review where relevant. A per-activity
// ActivityExecutionCondition is the one guard that *is* made executable: where
// the WAL expression is translatable it becomes a FEEL condition on an exclusive
// skip gateway around the activity, and where it is not it is preserved and
// flagged instead.
package mimimport
