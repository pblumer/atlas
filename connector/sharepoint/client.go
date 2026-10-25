// Package sharepoint integrates Microsoft SharePoint as a server-registered Atlas
// worker: a BPMN SharePoint task performs one step in a model-authored site — create,
// read, update, delete or list the items of a list, or upload, download, inspect,
// list, create or delete what a document library holds — through a configured
// provider via the job path (ADR-0141, amended), mirroring how the mail package
// delegates a send to a registry-managed provider (ADR-0079). The integration inherits
// the job protocol's durability and non-blocking properties (ADR-0007):
//
//   - A task creates a job carrying a reserved job type:
//     [compiler.SharePointJobType] for create-item, the one operation there was
//     before the rest existed, and [compiler.SharePointOpsJobType] for every other.
//     The processor never performs the outbound call itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, calls Graph off the
//     processor goroutine and after fsync (invariant I2, never inside applyToState /
//     I4), and completes the job, writing what Graph answered into the task's result
//     variable, which drives the token onward.
//   - The Graph base and OAuth credential live in a server-side [Registry] keyed by
//     worker name, so a model refers to a provider by name only and never carries
//     an endpoint or a secret (ADR-0036/0041). Only the target — the operation, the
//     site, the list or library, the item or the path and their values — is authored
//     in the model, like a REST task's endpoint (ADR-0067).
//
// The transport is Microsoft Graph ([GraphClient]), authenticated with an OAuth2
// bearer token acquired app-only — with a client secret or, as Microsoft recommends
// for an app that runs unattended, a certificate — or via a pre-obtained refresh
// token (ADR-0141), reusing the grant mechanics of the shared oauth2 package.
//
// # Bytes travel only under the variable budget
//
// upload-file and download-file move a document's content through a process
// variable, so both are bounded by [MaxContentBytes] — the variable's own budget — and
// refuse rather than truncate, for the reason the S3 worker gives (ADR-0442): half a
// PDF is not a smaller PDF. A document larger than that belongs in the library and is
// handed to a person by its webUrl, which every file answer carries.
//
// Graph's file answers also carry a pre-authenticated download URL. It is removed
// before the answer reaches a variable: it is a bearer capability nobody asked for,
// and a variable is written to the event log for as long as the installation keeps
// history.
//
// # Delivery
//
// Delivery is at-least-once. Every operation but create-item is safe to repeat: the
// reads change nothing, an update restates the same columns, an upload to a path
// replaces the file with the same bytes, create-folder treats a folder that is already
// there as made, and a delete that finds nothing answers deleted=false rather than
// failing. Create-item is the exception: a crash between "Graph created the item" and
// "job completed" replays the create, which can produce a duplicate list item.
package sharepoint

import (
	"context"
	"sort"

	"github.com/pblumer/atlas/connector/clientreg"
	"github.com/pblumer/atlas/limits"
)

// OpCreateItem is the operation a task performs when it names none — the only one
// there was before operations existed, which is why a job without an operation is
// read as this one.
const OpCreateItem = "create-item"

// The content encodings an upload or a download may author. Text is the default: a
// process writing JSON, CSV or a letter wants the characters it composed. Base64 is
// what makes a binary round-trip through a process variable possible at all, and what
// a form's uploaded file already looks like.
//
// They are spelled here as well as in the compiler because the dependency runs one way
// — this package imports the compiler, so the compiler cannot import it — and the drift
// test TestSharePointOpsMatchTheCompiler holds the two together.
const (
	EncodingText   = "text"
	EncodingBase64 = "base64"
)

// DefaultMaxResults is the page size of a listing that authors none, and MaxPageSize
// the largest a model may author. A hundred items with all their columns stays well
// inside a process variable; 999 is the largest page Graph accepts on the collections
// that state one. A list or a folder may answer with fewer per page than asked, and
// then says where to carry on.
const (
	DefaultMaxResults int32 = 100
	MaxPageSize       int32 = 999
)

// MaxContentBytes bounds the two operations that move a document's content through a
// process variable, and every answer that lands in one.
//
// It *is* [limits.Limits.Variable] rather than a number of this package's own: the
// destination of a download is one process variable, so a cap that could differ from
// the variable's own would only move the failure one step later, to a place with less
// context.
func MaxContentBytes() int64 { return limits.Default().Variable }

// Op describes one SharePoint operation: what a model must author for it and what it
// may carry. Keeping this a table rather than a switch is what lets the compiler, the
// Modeler's panel and this worker agree on the same rules — a new operation is a row,
// not three edits that can disagree.
//
// The compiler holds its own copy (compiler.sharePointOps) because the dependency runs
// one way. The behavioural drift test TestSharePointOpsMatchTheCompiler keeps the two
// honest.
type Op struct {
	// NeedsList marks an item operation, which acts on one list. TakesList marks a file
	// operation, where the list is the document library and may be left to the site's
	// default one.
	NeedsList bool
	TakesList bool
	// NeedsItemID marks an operation on one list item.
	NeedsItemID bool
	// NeedsPath marks an operation on one file or folder; TakesPath a folder listing,
	// whose path may be empty for the library's top level.
	NeedsPath bool
	TakesPath bool
	// NeedsFields marks update-item, which is nothing without the columns it sets;
	// TakesFields the operations that may carry column values without needing them.
	NeedsFields bool
	TakesFields bool
	// NeedsContent and TakesEncoding mark the byte path.
	NeedsContent  bool
	TakesEncoding bool
	// TakesFilter marks the listing that can be filtered, TakesSelect the reads whose
	// columns can be narrowed, and TakesPaging the two listings.
	TakesFilter bool
	TakesSelect bool
	TakesPaging bool
	// NeedsResult marks an operation whose whole point is what it returns; TakesResult
	// one that answers with something a model may keep.
	NeedsResult bool
	TakesResult bool
	// Label describes the operation for an error message.
	Label string
}

// Ops is the operation table: the steps a business process takes in a SharePoint site.
// It is deliberately not "every Graph call" — permissions, sharing links, content
// types, versions and site administration are not here. A sharing link in particular
// changes who may open a document, which is a decision about access rather than a step
// in a case, and the webUrl every file answer carries already opens it for anybody who
// may.
var Ops = map[string]Op{
	OpCreateItem: {
		NeedsList: true, TakesFields: true, TakesResult: true,
		Label: "create a list item",
	},
	"get-item": {
		NeedsList: true, NeedsItemID: true, TakesSelect: true,
		NeedsResult: true, TakesResult: true, Label: "read a list item",
	},
	"update-item": {
		NeedsList: true, NeedsItemID: true, NeedsFields: true,
		TakesResult: true, Label: "update a list item",
	},
	// delete-item answers deleted=true|false rather than failing on an item that is
	// not there, because a retry after a delete that succeeded finds exactly that.
	"delete-item": {
		NeedsList: true, NeedsItemID: true, TakesResult: true,
		Label: "delete a list item",
	},
	"list-items": {
		NeedsList: true, TakesFilter: true, TakesSelect: true, TakesPaging: true,
		NeedsResult: true, TakesResult: true, Label: "list the items of a list",
	},
	"upload-file": {
		TakesList: true, NeedsPath: true, NeedsContent: true, TakesEncoding: true,
		TakesFields: true, TakesResult: true, Label: "upload a file",
	},
	"download-file": {
		TakesList: true, NeedsPath: true, TakesEncoding: true,
		NeedsResult: true, TakesResult: true, Label: "download a file",
	},
	// get-file answers exists=false for a path that is not there instead of failing:
	// "is it there" is the question this operation is asked.
	"get-file": {
		TakesList: true, NeedsPath: true,
		NeedsResult: true, TakesResult: true, Label: "read a file's properties",
	},
	"list-folder": {
		TakesList: true, TakesPath: true, TakesPaging: true,
		NeedsResult: true, TakesResult: true, Label: "list a folder",
	},
	"create-folder": {
		TakesList: true, NeedsPath: true, TakesResult: true,
		Label: "create a folder",
	},
	"delete-file": {
		TakesList: true, NeedsPath: true, TakesResult: true,
		Label: "delete a file or folder",
	},
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

// Request is one SharePoint operation with every authored value already resolved: the
// worker has evaluated the task's literal-or-FEEL values against the variables it sees,
// so what reaches a client is plain data.
//
// Which fields carry a value follows from Operation, and the compiler has already
// refused a model that set one the operation does not use. The JSON names are the wire
// form a worker in mockup mode forwards an operation in ([RemoteMock]).
type Request struct {
	// Operation is one of [Ops]; empty is [OpCreateItem].
	Operation string `json:"operation,omitempty"`
	// Site addresses the site: a Graph site id ("host,siteId,webId"), a host and
	// server-relative path ("contoso.sharepoint.com:/sites/hr"), or the site's URL.
	Site string `json:"site"`
	// List is the list an item operation acts on, or the document library a file
	// operation does; empty there means the site's default library.
	List string `json:"list,omitempty"`
	// ItemID addresses one list item.
	ItemID string `json:"itemId,omitempty"`
	// Path addresses one file or folder inside the library, relative to its top level.
	Path string `json:"path,omitempty"`
	// Fields are column values, each keeping the JSON shape its FEEL value had — a
	// number stays a number, a list a list — because Graph reads a multi-choice or a
	// lookup column from that shape and nothing else.
	Fields map[string]any `json:"fields,omitempty"`
	// Content is what upload-file writes, in the form Encoding names.
	Content string `json:"content,omitempty"`
	// Encoding is EncodingText or EncodingBase64; the compiler has already applied the
	// default.
	Encoding string `json:"encoding,omitempty"`
	// Filter is list-items' OData filter, Select the columns a read answers with.
	Filter string `json:"filter,omitempty"`
	Select string `json:"select,omitempty"`
	// PageToken resumes a listing where an earlier page stopped: the nextPageToken that
	// page answered with, passed back unchanged.
	PageToken string `json:"pageToken,omitempty"`
	// MaxResults is a listing's page size; the compiler has already applied the default
	// and refused a value past [MaxPageSize].
	MaxResults int32 `json:"maxResults,omitempty"`
	// RequestID is the job key, carried for tracing.
	RequestID string `json:"requestId,omitempty"`
}

// Client performs one operation through one configured SharePoint provider. It is an
// interface so the worker is testable without a live tenant and so a worker name binds
// to exactly one provider.
//
// The shape is a single Do rather than a method per operation for the reason the S3,
// Jira and Entra workers give (ADR-0442/0201/0172): this is a typed façade over an HTTP
// API, and the value it adds is at the model level — naming the steps, addressing the
// site and the library — not in wrapping eleven HTTP calls in eleven Go signatures.
type Client interface {
	// Do performs one operation and returns what Graph answered, as decoded JSON.
	Do(ctx context.Context, req Request) (any, error)
}

// Registry resolves a worker name to the [Client] for this kind. Workers are
// registered at the server from managed configuration (endpoint plus credentials), so
// a model refers to a worker by name only (ADR-0036/0041).
//
// It is the shared [clientreg.Registry], which also carries *why* a configured
// worker is missing from it — the difference between "never configured" and
// "configured and broken", which is what a parked token has to be able to say
// (ADR-0158).
type Registry = clientreg.Registry[Client]

// NewRegistry creates an empty worker registry.
func NewRegistry() *Registry { return clientreg.New[Client]() }
