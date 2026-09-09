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
// ActivityExecutionCondition holds, so such an activity is wrapped in an
// exclusive split with a bypass, and one carrying an Iteration becomes a
// sequential multi-instance activity. Neither expression is translated — see
// emitGuard and miPlaceholder for why — but both are documented on the model and
// flagged in the Report.
//
// The serialised .NET collections a MIMWAL activity hangs off itself — the
// queries it runs and the assignments it makes, thousands of characters of
// Hashtable markup in the source — are decoded once and rendered three ways: as
// a small table on the activity's documentation, as <atlas:mimCollection>
// extension elements on the node, and as one item per row in the Report. All
// three are by position: the structure of such a table is mechanical, the
// meaning of its columns is not (see tables.go).
//
// # The decomposition is machine-readable, and stays out of the graph
//
// A row of an UpdatesTable is a write and a row of a QueriesTable is a read, so
// it is tempting to import each as its own task. This package does not, and the
// reason is worth stating: which target system a row writes to is not in the
// XOML at all — it is in MIM's sync rules and attribute flows — and MIM applies
// the whole table as one request. Splitting the rows into flow nodes would put a
// structure into the diagram that the source does not contain, and would carry
// the untranslated per-activity guard onto every one of them, so a model that
// looks precise would be exactly as unexecutable as before. Nothing here decides
// that; the migrator does, knowing the sync rules.
//
// What the importer can do without inventing anything is hand over the
// decomposition: <atlas:mimCollection property="UpdatesTable" kind="table"> with
// an <atlas:mimRow> per row and an <atlas:mimCell column="…"> per cell, verbatim.
// A cell says where it sat, never what it does. That is addressable by a tool,
// checkable against the preserved source, and asserts nothing that was not read
// out of the markup.
//
// # The Report is a migration worksheet
//
// The Report counts *work*, not BPMN elements. A MIMWAL activity carrying five
// assignments and a named query is one preserved node and six pieces of work,
// each of which has to be re-expressed against a real target system, so it
// contributes six items and the manual-review count says six. A worksheet that
// counted nodes would report "1 preserved" for a step nobody can migrate in an
// afternoon, which is the number a plan would then be made with.
//
// The conversion reproduces the workflow's *structure and intent*, not MIM's
// runtime semantics: the authentication/authorization/action request model, the
// FEEL bodies of individual activities, and diagram layout are intentionally out
// of scope for a first pass and are flagged for manual review where relevant.
package mimimport
