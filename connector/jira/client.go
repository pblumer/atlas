// Package jira integrates Atlassian Jira as a server-registered Atlas connector: a
// BPMN Jira connector task performs one issue-tracker operation — create an issue,
// read one, update it, move it through its workflow, comment on it, assign it, or
// search — against a configured Jira instance via the job path
// (ADR-draft-jira-connector). It mirrors how the remedy package delegates a ticket to
// a registry-managed ITSM instance (ADR-0106) and mail a send to a registry-managed
// provider (ADR-0079), and inherits the job protocol's durability and non-blocking
// properties (ADR-0007):
//
//   - A connector task creates a job carrying the reserved [compiler.JiraJobType].
//     The processor never performs the outbound call itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, calls Jira off the
//     processor goroutine and after fsync (invariant I2, never inside applyToState /
//     I4), and completes the job, writing what Jira returned into the task's result
//     variable, which drives the token onward.
//   - The base URL and credential live in a server-side [Registry] keyed by connector
//     name, so a model refers to a Jira instance by name only and never carries a URL
//     or a secret (ADR-0036/0041). Only what the task is *about* — the operation and
//     its values — is authored in the model, like a Remedy task's form and fields.
//
// The transport is Jira's REST API v2 (/rest/api/2), which both Jira Cloud and Jira
// Data Center serve. v3 differs from it in the one thing that matters here: a
// description or comment body must be an Atlassian Document Format tree rather than a
// string, and making every model author ADF to write one sentence is the opposite of
// what this connector is for.
//
// Authentication follows the credential bundle an operator stored: an {email,
// apiToken} bundle is Jira Cloud's HTTP Basic scheme, a {token} bundle a Data Center
// personal access token sent as a bearer. The same choice decides how an account is
// addressed when assigning an issue — Cloud by accountId, Data Center by username —
// so a model never has to know which product it is talking to.
//
// Delivery is at-least-once: a crash between "Jira created the issue" and "job
// completed" replays the create, which can produce a duplicate issue. The job key
// rides along as an X-Request-ID for a downstream de-duplicator; a real idempotency
// key is a follow-up (ADR-draft-jira-connector).
package jira

import (
	"context"
	"sort"

	"github.com/pblumer/atlas/connector/clientreg"
)

// Op describes one Jira operation: what a model must author for it, and what it is
// allowed to carry. Keeping this a table rather than a switch is what lets the
// compiler, the Modeler's panel and this worker agree on the same rules — a new
// operation is a row, not three edits that can disagree.
//
// The compiler holds its own copy (compiler.jiraOps) because the dependency runs one
// way: this package imports the compiler, so the compiler cannot import it. The
// behavioural drift test TestJiraOpsMatchTheConnector is what keeps the two honest.
type Op struct {
	// NeedsIssue marks an operation that addresses one issue by key or id.
	NeedsIssue bool
	// NeedsProject marks create-issue, the one operation that says which project and
	// which issue type an issue comes into being in.
	NeedsProject bool
	NeedsSummary bool
	// NeedsTransition, NeedsComment and NeedsAssignee are the operations named after
	// their one value.
	NeedsTransition bool
	NeedsComment    bool
	NeedsAssignee   bool
	NeedsJQL        bool
	// NeedsResult marks an operation whose whole point is what it returns: a read
	// that discards its answer is a call made for nothing.
	NeedsResult bool
	// NeedsChange marks update-issue: without a summary, a description or one extra
	// field it is a request that changes nothing.
	NeedsChange bool
	// Label describes the operation for an error message.
	Label string
}

// Ops is the operation table: the loop a process actually runs against an issue
// tracker. It is deliberately not "every Jira endpoint" — what earns a row is a step a
// business process takes, which is why there is no board, sprint or worklog here and
// why the generic REST connector (ADR-0067) remains the way to reach the rest.
var Ops = map[string]Op{
	"create-issue":     {NeedsProject: true, NeedsSummary: true, Label: "create an issue"},
	"get-issue":        {NeedsIssue: true, NeedsResult: true, Label: "read an issue"},
	"update-issue":     {NeedsIssue: true, NeedsChange: true, Label: "update an issue"},
	"transition-issue": {NeedsIssue: true, NeedsTransition: true, Label: "transition an issue"},
	"add-comment":      {NeedsIssue: true, NeedsComment: true, Label: "comment on an issue"},
	"assign-issue":     {NeedsIssue: true, NeedsAssignee: true, Label: "assign an issue"},
	"search":           {NeedsJQL: true, NeedsResult: true, Label: "search issues"},
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

// Request is one Jira operation with every authored value already resolved: the worker
// has evaluated the task's literal-or-FEEL values against the variables it sees, so
// what reaches a client is plain data.
//
// Which fields carry a value follows from Operation, and the compiler has already
// refused a model that set one the operation does not use — so a client may read the
// fields its operation names and ignore the rest.
type Request struct {
	Operation string
	// Issue is an issue key ("OPS-42") or a numeric issue id.
	Issue string
	// Project and IssueType create an issue. A value that is all digits addresses the
	// project or type by id; anything else by key (project) or name (issue type).
	Project     string
	IssueType   string
	Summary     string
	Description string
	// Transition is a transition id, or the name Jira shows for it — which the client
	// resolves against the issue's available transitions, because Jira moves an issue
	// only by id and a model tied to an id is tied to one workflow configuration.
	Transition string
	// Comment is a comment body: its own operation, and optionally alongside a
	// transition, where it becomes the note explaining the move.
	Comment string
	// Assignee is the account an issue is handed to — an accountId on Jira Cloud, a
	// username on Data Center. The client knows which from its own credential.
	Assignee string
	// JQL is a search's query and MaxResults the cap on what may be returned; 0 reads
	// every matching issue. The compiler has already applied the default.
	JQL        string
	MaxResults int32
	// Fields are extra issue fields keyed by Jira field id or name, each carrying the
	// JSON shape its FEEL value had — a string stays a string, an object or a list is
	// sent as one. It is how a custom field, a component, or anything this connector
	// does not name by itself is set.
	Fields map[string]any
	// RequestID is deterministic (the job key), sent as an X-Request-ID header so an
	// at-least-once retry can be recognized by a downstream de-duplicator.
	RequestID string
}

// Client performs one operation against a configured Jira instance. It is an
// interface so the worker is testable without a live Jira and so a connector name
// binds to exactly one instance.
//
// The shape is a single Do rather than a method per operation for the reason the
// Entra connector gives (ADR-0172): this is a typed façade over Jira's REST API, and
// the value it adds is at the *model* level — naming the operations and building
// their URLs and bodies — not in wrapping seven HTTP calls in seven Go signatures.
type Client interface {
	// Do performs one operation and returns what Jira answered: the created or read
	// object, the array of issues a search matched, or nil where Jira answers with no
	// content.
	Do(ctx context.Context, req Request) (any, error)
}

// Registry resolves a connector name to the [Client] for this kind. Connectors are
// registered at the server from managed configuration (base URL plus credentials), so
// a model refers to a connector by name only (ADR-0036/0041).
//
// It is the shared [clientreg.Registry], which also carries *why* a configured
// connector is missing from it — the difference between "never configured" and
// "configured and broken", which is what a parked token has to be able to say
// (ADR-0158).
type Registry = clientreg.Registry[Client]

// NewRegistry creates an empty connector registry.
func NewRegistry() *Registry { return clientreg.New[Client]() }
