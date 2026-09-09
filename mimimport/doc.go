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
// A preserved activity keeps its namespace binding: the fragment carries the
// prefixes it needs and declares them, so it parses on its own, and the
// <atlas:mimSource> element names the .NET type and assembly the namespace
// identifies — which is what tells a MIMWAL activity apart from a stock MIM one
// of the same local name (see namespace.go).
//
// Flow-node ids come from the activity's x:Name where it has one, so a re-import
// of a workflow that gained a step does not renumber the steps it already had.
//
// Preserved markup is written as escaped character data, never in a CDATA
// section: CDATA suppresses entity resolution, which would turn the quotation
// marks inside a MIM expression into literal &#34; and change the expression
// while appearing to preserve it. A consumer unescapes the element text once
// and holds the activity's markup as MIM wrote it.
//
// Conditionality is part of that structure even where MIM does not express it as
// control flow: an activity of the MIMWAL library runs only when its
// ActivityExecutionCondition holds, and a ConditionedActivityGroup's child runs
// on the passes where its WhenCondition holds, so such an activity is wrapped in
// an exclusive split with a bypass; one carrying an Iteration becomes a
// sequential multi-instance activity, and the group itself the repeat-until loop
// it is. An IfElseBranchActivity usually carries its condition as a WF property
// element rather than an attribute, which is read as the condition it is rather
// than as a step in the flow. No expression is translated — see emitGuard and
// miPlaceholder for why — but each is documented on the model and flagged in the
// Report.
//
// The serialised .NET collections a MIMWAL activity hangs off itself — the
// queries it runs and the assignments it makes, thousands of characters of
// Hashtable markup in the source — are rendered as a small table on the
// activity's documentation, by position: the structure of such a table is
// mechanical, the meaning of its columns is not (see tables.go).
//
// The conversion reproduces the workflow's *structure and intent*, not MIM's
// runtime semantics: the authentication/authorization/action request model, the
// FEEL bodies of individual activities, and diagram layout are intentionally out
// of scope for a first pass and are flagged for manual review where relevant.
package mimimport
