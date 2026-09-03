// Package googlesheets integrates Google Sheets as a Worker Type (ADR-0203): a BPMN
// service task performs one spreadsheet operation — create a spreadsheet, add a sheet
// to one, read a range, write a range, append rows, clear a range, delete a sheet, or
// trash the whole file — against a Worker an operator configured, via the job path
// (ADR-draft-google-sheets-worker). It mirrors how the jira package delegates one
// operation to a registry-managed Worker (ADR-0201) and inherits the job protocol's
// durability and non-blocking properties (ADR-0007):
//
//   - The task creates a job carrying the reserved [compiler.GoogleSheetsJobType]. The
//     processor never performs the outbound call itself, so it stays allocation-free
//     (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a Worker Instance inside the server — pulls those
//     jobs, calls Google off the processor goroutine and after fsync (invariant I2,
//     never inside applyToState / I4), and completes the job, writing what Google
//     returned into the task's result variable, which drives the token onward.
//   - The credential lives in a server-side [Registry] keyed by Worker name, so a model
//     names a Worker and never carries a key (ADR-0036/0041). Only what the task is
//     *about* — the operation, the spreadsheet, the range and the values — is authored
//     in the model.
//
// (The BPMN attribute a task states its Worker in is spelled `connector="…"`, and the
// extension element is <atlas:googleSheetsConnector>. Both keep the pre-ADR-0203
// spelling on purpose: they are authored in deployed models, and renaming them is a
// separate step of that migration, not a side effect of adding a Worker Type.)
//
// # Two APIs behind one Worker
//
// A spreadsheet is cells and it is also a file. The cell-level work is the Sheets API
// v4, addressed by spreadsheet id; creating one in a folder and deleting one are Drive
// API v3 operations on the same object. Both are reached with the same OAuth2 access
// token, so they are one Worker Type rather than two — but which half works depends on
// the scopes the operator granted, and a missing scope surfaces as a 403 from Google
// on the operation rather than at configuration time.
//
// A delete moves the file to the owner's trash (`trashed: true`) rather than purging
// it. `files.delete` is permanent and an unrecoverable mistake; trashing is what a
// person does in the UI and what an owner can undo.
//
// # Delivery
//
// At-least-once, and a spreadsheet has no idempotency key. A crash between "Sheets
// appended the row" and "job completed" replays the append and the row appears twice.
// Google offers nothing to prevent it — there is no request id an append is
// deduplicated on — so a process that cannot tolerate a duplicate row must write a
// mark column and read before it appends. The job key still rides along as an
// X-Request-ID for tracing.
package googlesheets

import (
	"context"
	"sort"

	"github.com/pblumer/atlas/connector/clientreg"
)

// Op describes one spreadsheet operation: what a model must author for it, and what it
// is allowed to carry. Keeping this a table rather than a switch is what lets the
// compiler, the Modeler's panel and this worker agree on the same rules — a new
// operation is a row, not three edits that can disagree.
//
// The compiler holds its own copy (compiler.googleSheetsOps) because the dependency
// runs one way: this package imports the compiler, so the compiler cannot import it.
// The behavioural drift test TestGoogleSheetsOpsMatchTheWorkerType keeps the two honest.
type Op struct {
	// NeedsSpreadsheet marks every operation that addresses an existing spreadsheet —
	// all but create-spreadsheet, which is the one that brings one into being.
	NeedsSpreadsheet bool
	// NeedsTitle marks create-spreadsheet: what the new file is called.
	NeedsTitle bool
	// NeedsSheet marks the two operations named after a tab; TakesSheet allows one
	// without requiring it, which is how create-spreadsheet names its first tab.
	NeedsSheet bool
	TakesSheet bool
	// NeedsRange marks the four cell-level operations. A range is A1 notation and may
	// name its sheet ("Anträge!A2:F"), which is why those four do not also take a sheet.
	NeedsRange bool
	// NeedsValues marks the two operations that write. TakesColumns allows the column
	// projection a list of objects needs, and TakesInput the raw/user value-input
	// choice; both belong to exactly the writing operations.
	NeedsValues  bool
	TakesColumns bool
	TakesInput   bool
	// TakesHeader belongs to read-range alone: read the first row as column names and
	// answer with a list of objects.
	TakesHeader bool
	// TakesFolder belongs to create-spreadsheet alone: the Drive folder the new file is
	// moved into.
	TakesFolder bool
	// NeedsResult marks an operation whose whole point is what it returns: a read that
	// discards its answer is a call made for nothing. TakesResult marks one that
	// returns something a model may keep or discard — and, by its absence, the two
	// deletes, where a result variable would name a value nobody has a use for.
	NeedsResult bool
	TakesResult bool
	// Label describes the operation for an error message.
	Label string
}

// Ops is the operation table: the loop a process actually runs against a spreadsheet —
// make one, give it a tab, read what is in it, change what is in it, add to it, empty
// part of it, and take either the tab or the file away again.
//
// It is deliberately not "every Sheets endpoint". The API also merges cells, sets
// conditional formats, builds pivot tables and draws charts; none of those is a step a
// process takes, and each would be a row nobody could explain in a properties panel.
// The generic REST Worker Type (ADR-0067) remains the way to reach the rest.
var Ops = map[string]Op{
	"create-spreadsheet": {NeedsTitle: true, TakesSheet: true, TakesFolder: true, TakesResult: true, Label: "create a spreadsheet"},
	"add-sheet":          {NeedsSpreadsheet: true, NeedsSheet: true, TakesResult: true, Label: "add a sheet"},
	"read-range":         {NeedsSpreadsheet: true, NeedsRange: true, TakesHeader: true, NeedsResult: true, TakesResult: true, Label: "read a range"},
	"write-range":        {NeedsSpreadsheet: true, NeedsRange: true, NeedsValues: true, TakesColumns: true, TakesInput: true, TakesResult: true, Label: "write a range"},
	"append-row":         {NeedsSpreadsheet: true, NeedsRange: true, NeedsValues: true, TakesColumns: true, TakesInput: true, TakesResult: true, Label: "append rows"},
	"clear-range":        {NeedsSpreadsheet: true, NeedsRange: true, TakesResult: true, Label: "clear a range"},
	"delete-sheet":       {NeedsSpreadsheet: true, NeedsSheet: true, Label: "delete a sheet"},
	"delete-spreadsheet": {NeedsSpreadsheet: true, Label: "delete a spreadsheet"},
}

// OpNames lists the operations, sorted, for the error messages that have to say what
// was expected.
func OpNames() []string {
	out := make([]string, 0, len(Ops))
	for name := range Ops {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Value-input modes for the two writing operations. InputUser is Google's
// USER_ENTERED: a value is interpreted as if it had been typed, so "=SUM(A1:A9)"
// becomes a formula and "3,50 €" becomes a currency number. InputRaw is RAW: the
// value is stored exactly as given. User is the default because a process writing a
// date or an amount into a sheet people read wants it to *be* a date, not a string
// that looks like one.
const (
	InputUser = "user"
	InputRaw  = "raw"
)

// Request is one spreadsheet operation with every authored value already resolved: the
// worker has evaluated the task's literal-or-FEEL values against the variables it saw,
// so what reaches a client is plain data.
//
// Which fields carry a value follows from Operation, and the compiler has already
// refused a model that set one the operation does not use — so a client may read the
// fields its operation names and ignore the rest.
type Request struct {
	Operation string
	// Spreadsheet is the spreadsheet id (a URL has already been reduced to one).
	Spreadsheet string
	// Sheet is a tab title: the tab added or deleted, or the first tab of a new file.
	Sheet string
	// Range is A1 notation, optionally naming its sheet ("Anträge!A2:F").
	Range string
	// Title is a new spreadsheet's name and Folder the Drive folder it is moved into
	// (empty leaves it in the credential's root).
	Title  string
	Folder string
	// Values are the rows to write, already projected from whatever shape the model
	// held (see [Rows]).
	Values [][]any
	// Input is [InputUser] or [InputRaw]; the compiler has applied the default.
	Input string
	// Header asks read-range to key its answer by the range's first row.
	Header bool
	// RequestID is deterministic (the job key), sent as an X-Request-ID header. It
	// buys tracing, not idempotency: see the package doc.
	RequestID string
}

// Client performs one operation against a configured Google account. It is an
// interface so the worker is testable without a live Google and so a Worker name
// binds to exactly one credential.
//
// The shape is a single Do rather than a method per operation for the reason the Entra
// Worker Type gives (ADR-0172): this is a typed façade over two REST APIs, and the value
// it adds is at the *model* level — naming the operations and building their URLs and
// bodies — not in wrapping eight HTTP calls in eight Go signatures.
type Client interface {
	// Do performs one operation and returns what Google answered: the created
	// spreadsheet, the read range's rows, the update summary, or nil where the answer
	// holds nothing a process needs.
	Do(ctx context.Context, req Request) (any, error)
	// ListFiles lists one Drive folder. It is the inbound half's read
	// (ADR-draft-google-inbound-watch) and deliberately not one of [Ops]: a watch on a
	// drop folder needs it, and a model must not be able to author it.
	ListFiles(ctx context.Context, q FileQuery) ([]map[string]any, error)
}

// Registry resolves a Worker name to the [Client] behind it. Workers are registered at
// the server from operator-managed configuration (a credential reference), so a model
// names a Worker and nothing else about the account (ADR-0036/0041).
//
// It is the shared [clientreg.Registry], which also carries *why* a configured Worker
// is missing from it — the difference between "never configured" and "configured and
// broken", which is what a parked token has to be able to say (ADR-0158).
type Registry = clientreg.Registry[Client]

// NewRegistry creates an empty Worker registry.
func NewRegistry() *Registry { return clientreg.New[Client]() }
