package api

import "github.com/pblumer/atlas/compiler"

// The release registry: one row per reserved job type, saying what decided it, what
// class of thing it is, and which Repository package advertises it.
//
// It exists because ADR-0167 makes a promise the repository could not keep track of.
// The rule there is that a connector kind is not *released* until it is in the
// Repository catalog — and the only way to check that was to read `editor.js`, the
// bundled packages and the ADR index side by side and hold the answer in your head.
// Nobody did, so the catalog fell nine kinds behind without a single failing test.
//
// What the rows below make checkable, in releasedkinds_test.go:
//
//   - **Every reserved job type is accounted for.** A new kind adds a name to
//     compiler.ReservedJobTypes, and the guard fails until it also adds a row here.
//     That is the drift half: the registry cannot quietly fall behind the compiler,
//     because the compiler is what it is checked against.
//   - **Every row cites a record that exists and is Accepted.** A kind backed by a
//     Proposed record is not released yet (ADR-0167) and owes nothing.
//   - **Every released connector kind owes exactly one package**, and the ones that do
//     not have one are counted rather than forgotten.
//
// The registry is data, not behaviour. It states what the repository promises about a
// kind; it does not implement the kind, and nothing on the hot path reads it.
type releasedKind struct {
	// JobType is the reserved name, and must match compiler.ReservedJobTypes exactly.
	// The name rather than the index, because the index is a position in that slice and
	// a row keyed by position would silently re-point if one were ever inserted.
	JobType string
	// ADR is the record that decided this kind. Where a kind's own comment cites
	// several records, this is the one whose subject *is* the kind — ADR-0172 for
	// Entra, not the ADR-0166 it is compared to — and the rest are context.
	ADR int
	// Class decides whether a package is owed at all. See the constants below.
	Class kindClass
	// Package is the Repository package id that advertises this kind, or "" where none
	// exists. An id here must name a package that is actually bundled: a row claiming
	// a package that was renamed or deleted fails the guard.
	Package string
	// Why is the reason a package is not owed, for a row whose Class needs one. It is
	// prose for a reviewer, not a parsed field, and an empty one on an exempt row is
	// what the guard refuses — an exemption nobody wrote a reason for is an oversight
	// that looks like a decision.
	Why string
}

// kindClass is what a reserved job type *is*, which is what decides whether ADR-0167's
// publication mandate reaches it.
type kindClass int

const (
	// classConnector is a data-only integration kind: an author configures it, and the
	// configuration is the whole artifact. These are what ADR-0167 mandates a package
	// for, because a package *is* their configuration shape.
	classConnector kindClass = iota
	// classScriptTask carries executable code. ADR-0167 deliberately leaves these out:
	// ADR-0081's trust split keeps code-bearing artifacts behind human review, and
	// compelling them into a gallery would run against it.
	classScriptTask
	// classEngine is a job type that is not an authorable integration at all — the
	// engine's own work, or a shape an author reaches through a different BPMN element
	// than a service task. No gallery entry could describe it, so none is owed.
	classEngine
)

// releasedKinds is the registry. Order follows compiler.ReservedJobTypes, so a reader
// comparing the two reads them in the same order; the guard compares by name, not by
// position, so a reordering here is harmless and an omission is not.
//
// Sixteen connector kinds carry no package today. That is ADR-0167's gap, and it is
// listed here rather than described in a document nobody re-reads: the guard counts the
// rows with an empty Package and fails if the count grows, so the gap can close but not
// widen. ADR-0212's applier is what makes filling them worth doing — a package that
// installs today lands in a store the Modeler does not read.
var releasedKinds = []releasedKind{
	{JobType: compiler.DMNJobType, ADR: 14, Class: classEngine,
		Why: "a business rule task, not a service task: the author reaches it through a BPMN element of its own and binds a decision, not a worker"},
	{JobType: compiler.UserTaskJobType, ADR: 28, Class: classEngine,
		Why: "human work. The Tasks app is its surface; there is no worker to configure"},
	{JobType: compiler.PwshJobType, ADR: 47, Class: classScriptTask},
	{JobType: compiler.TemisDecisionJobType, ADR: 50, Class: classConnector},
	{JobType: compiler.RestJobType, ADR: 67, Class: classConnector, Package: "atlas.rest-outbound"},
	{JobType: compiler.PythonJobType, ADR: 47, Class: classScriptTask},
	{JobType: compiler.JsJobType, ADR: 47, Class: classScriptTask},
	{JobType: compiler.ClioWriteJobType, ADR: 36, Class: classConnector},
	{JobType: compiler.ClioQueryJobType, ADR: 36, Class: classConnector},
	{JobType: compiler.ClioReadJobType, ADR: 36, Class: classConnector},
	{JobType: compiler.MailJobType, ADR: 79, Class: classConnector, Package: "atlas.send-mail-smtp"},
	{JobType: compiler.CsvImportJobType, ADR: 87, Class: classConnector, Package: "atlas.csv-to-json"},
	{JobType: compiler.SharePointJobType, ADR: 141, Class: classConnector, Package: "atlas.sharepoint-create-item"},
	{JobType: compiler.RemedyJobType, ADR: 106, Class: classConnector},
	{JobType: compiler.WebScrapeJobType, ADR: 118, Class: classConnector},
	{JobType: compiler.UserConnectorJobType, ADR: 123, Class: classEngine,
		Why: "the author's own job worker: the job type is whatever they typed, so there is no fixed configuration shape to publish"},
	{JobType: compiler.ScimJobType, ADR: 153, Class: classConnector, Package: "atlas.scim-provisioning"},
	{JobType: compiler.LdapJobType, ADR: 154, Class: classConnector, Package: "atlas.ldap-directory"},
	{JobType: compiler.SoapJobType, ADR: 165, Class: classConnector},
	{JobType: compiler.AdJobType, ADR: 166, Class: classConnector},
	{JobType: compiler.MsSqlJobType, ADR: 173, Class: classConnector},
	{JobType: compiler.MariaDBJobType, ADR: 173, Class: classConnector},
	{JobType: compiler.PostgresJobType, ADR: 173, Class: classConnector},
	{JobType: compiler.EntraJobType, ADR: 172, Class: classConnector},
	{JobType: compiler.LdifJobType, ADR: 171, Class: classConnector},
	{JobType: compiler.JiraJobType, ADR: 201, Class: classConnector, Package: "atlas.jira-issues"},
	{JobType: compiler.GoogleSheetsJobType, ADR: 235, Class: classConnector, Package: "atlas.google-sheets"},
	{JobType: compiler.AgentJobType, ADR: 117, Class: classConnector},
	{JobType: compiler.AiTaskJobType, ADR: 256, Class: classConnector},
	{JobType: compiler.DiscordJobType, ADR: 258, Class: classConnector},
}

// packagesOwed is how many released connector kinds have no Repository package right
// now. The guard holds the registry to this number: closing the gap means lowering it,
// and a new kind that ships without a package cannot pass by leaving it alone.
//
// It is a ratchet rather than a hard zero because the alternative was to leave the whole
// mandate unenforced until sixteen packages exist — which is how it went unenforced for
// the two months ADR-0167 has been open. A number that can only fall is worth more than
// a test nobody can turn on.
const packagesOwed = 16
