// Package mimimport converts Microsoft Identity Manager (MIM/FIM) workflow
// definitions into Atlas-executable BPMN 2.0 XML.
//
// MIM does not emit BPMN. Its workflows are serialised as XOML — the markup of
// the Windows Workflow Foundation (WF) — and are normally extracted with the
// FIMAutomation cmdlet Export-FIMConfig, which wraps each WorkflowDefinition's
// XOML as an attribute inside a resource-graph XML. This package accepts either
// raw XOML or such a wrapper (it locates and unescapes the embedded XOML) and
// produces a single <definitions> document the Atlas compiler can deploy. XOML
// is not reliably well-formed — MIM writes a workflow root's xmlns declarations
// without quotes around the value — so input that does not parse is repaired
// once and the repair is reported.
//
// # Losslessness
//
// The translation follows one rule: nothing is silently dropped. Where a WF
// construct has a faithful BPMN counterpart it is emitted natively (see the
// mapping in Convert); where it does not, the activity is still emitted — as a
// typed or plain task placeholder — and its original markup is preserved in an
// <atlas:mimSource> extension element together with a <documentation> note. A
// Report lists every produced node with a status of native, preserved or
// manual-review, so the lossy points are explicit rather than hidden, and its
// Warnings carry what belongs to no single node — today, that the input only
// parsed after a repair.
//
// Preserved markup is written as escaped character data, never in a CDATA
// section: CDATA suppresses entity resolution, which would turn the quotation
// marks inside a MIM expression into literal &#34; and change the expression
// while appearing to preserve it. A consumer unescapes the element text once
// and holds the activity's markup as MIM wrote it.
//
// Conditionality is part of that structure even where MIM does not express it as
// control flow: an activity of the MIMWAL library runs only when its
// ActivityExecutionCondition holds, so such an activity is wrapped in an
// exclusive split with a bypass. The guard expression itself is not translated —
// see emitGuard for why — but it is documented on the split and flagged in the
// Report.
//
// The conversion reproduces the workflow's *structure and intent*, not MIM's
// runtime semantics: the authentication/authorization/action request model, the
// FEEL bodies of individual activities, and diagram layout are intentionally out
// of scope for a first pass and are flagged for manual review where relevant.
package mimimport
