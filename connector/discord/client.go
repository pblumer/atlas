// Package discord integrates Discord as a server-registered Atlas worker: a BPMN
// Discord task performs one chat operation — send a message, edit one, delete one,
// read one, list a channel's messages, or open a thread — against a configured Discord
// Worker via the job path (ADR-draft-discord-worker). It mirrors how the jira package
// delegates an issue-tracker step to a registry-managed instance (ADR-0201) and mail a
// send to a registry-managed provider (ADR-0079), and inherits the job protocol's
// durability and non-blocking properties (ADR-0007):
//
//   - A task creates a job carrying the reserved [compiler.DiscordJobType].
//     The processor never performs the outbound call itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, calls Discord off the
//     processor goroutine and after fsync (invariant I2, never inside applyToState /
//     I4), and completes the job, writing what Discord returned into the task's result
//     variable, which drives the token onward.
//   - The bot token lives in a server-side [Registry] keyed by worker name, so a model
//     refers to a Discord Worker by name only and never carries a token
//     (ADR-0036/0041). Only what the task is *about* — the operation and its values —
//     is authored in the model.
//
// The transport is Discord's HTTP API v10. Authentication is the bot scheme: a single
// `Authorization: Bot <token>` header, composed by the client from the token an
// operator stored. The `Bot ` prefix is never part of the stored value — an operator
// pastes what Discord's developer portal shows them, and two vault entries differing
// only by a prefix would behave alike in some places and not others.
//
// # Why there is no reply-in-thread operation
//
// In Discord a thread *is* a channel, and its id is on the object create-thread
// returns. A reply is therefore [Ops] "send-message" addressing that id, not an
// operation of its own. A second name for the same call is how two rows later disagree
// about which one sets allowed_mentions.
//
// # Delivery
//
// Delivery is at-least-once: a crash between "Discord accepted the message" and "job
// completed" replays the send, which can produce a duplicate message. Discord offers no
// idempotency key, so the job key rides along as the message's nonce instead — Discord
// echoes a nonce back on the message object, which makes a replay's duplicate
// *recognizable* as one. It does not suppress it, and nothing here should be read as
// claiming otherwise.
package discord

import (
	"context"
	"sort"

	"github.com/pblumer/atlas/connector/clientreg"
)

// Op describes one Discord operation: what a model must author for it, and what it is
// allowed to carry. Keeping this a table rather than a switch is what lets the
// compiler, the Modeler's panel and this worker agree on the same rules — a new
// operation is a row, not three edits that can disagree.
//
// The compiler holds its own copy (compiler.discordOps) because the dependency runs one
// way: this package imports the compiler, so the compiler cannot import it. The
// behavioural drift test TestDiscordOpsMatchTheConnector is what keeps the two honest.
type Op struct {
	// NeedsChannel marks an operation that acts in a channel — which every one does,
	// because there is nowhere else for a message to be. It is a field rather than an
	// assumption so the drift test checks it like any other rule.
	NeedsChannel bool
	// NeedsMessage marks an operation that addresses one existing message.
	NeedsMessage bool
	// TakesMessage marks create-thread, where naming a message is the authored
	// difference between a thread hanging under that message and a standalone one —
	// optional rather than required or refused.
	TakesMessage bool
	NeedsContent bool
	// NeedsName marks create-thread: a thread without a title is one nobody can find
	// in the channel's thread list.
	NeedsName bool
	// TakesList marks the one operation that pages: After and MaxResults apply to it
	// and to nothing else.
	TakesList bool
	// NeedsResult marks an operation whose whole point is what it returns: a read that
	// discards its answer is a call made for nothing.
	NeedsResult bool
	// TakesResult marks an operation that answers with something a model may keep.
	// delete-message is the one that does not — Discord answers it with 204, where a
	// result variable would name a value that is never written.
	TakesResult bool
	// TakesFields marks an operation whose request has a body extra properties can be
	// merged into. A GET and a DELETE have none.
	TakesFields bool
	// Label describes the operation for an error message.
	Label string
}

// Ops is the operation table: what a process actually does in a channel. It is
// deliberately not "every Discord endpoint" — what earns a row is a step a business
// process takes, which is why there is no guild, role or scheduled-event operation here
// and why the generic REST worker (ADR-0067) remains the way to reach the rest.
var Ops = map[string]Op{
	"send-message":   {NeedsChannel: true, NeedsContent: true, TakesResult: true, TakesFields: true, Label: "send a message"},
	"edit-message":   {NeedsChannel: true, NeedsMessage: true, NeedsContent: true, TakesResult: true, TakesFields: true, Label: "edit a message"},
	"delete-message": {NeedsChannel: true, NeedsMessage: true, Label: "delete a message"},
	"get-message":    {NeedsChannel: true, NeedsMessage: true, NeedsResult: true, TakesResult: true, Label: "read a message"},
	"list-messages":  {NeedsChannel: true, TakesList: true, NeedsResult: true, TakesResult: true, Label: "list a channel's messages"},
	// create-thread does not require a result variable, for the reason Jira's
	// create-issue does not: opening a thread under a notice is a complete act, and
	// whether the process then posts into it is the model's business. The two reads
	// below are the ones that require one — a read that discards its answer is a call
	// made for nothing.
	"create-thread": {NeedsChannel: true, TakesMessage: true, NeedsName: true, TakesResult: true, TakesFields: true, Label: "open a thread"},
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

// Request is one Discord operation with every authored value already resolved: the
// worker has evaluated the task's literal-or-FEEL values against the variables it sees,
// so what reaches a client is plain data.
//
// Which fields carry a value follows from Operation, and the compiler has already
// refused a model that set one the operation does not use — so a client may read the
// fields its operation names and ignore the rest.
type Request struct {
	Operation string
	// Channel is the channel id the operation acts in. A thread is a channel, so a
	// reply into one carries the thread's id here.
	Channel string
	// Message is a message id: the message edited, deleted or read, or — on
	// create-thread — the message the thread hangs under.
	Message string
	// Content is a message body.
	Content string
	// Name is a new thread's title.
	Name string
	// After is a list's exclusive lower bound: the message id to read after. Discord
	// orders by id, so a process can page a channel forward without re-reading what it
	// already has.
	After string
	// MaxResults caps what a list may return. The compiler has already applied the
	// default and refused a value past what the endpoint accepts.
	MaxResults int32
	// Fields are extra request-body properties keyed by name, each carrying the JSON
	// shape its FEEL value had — a string stays a string, an object or a list is sent
	// as one. It is how a model reaches embeds, allowed_mentions or components without
	// this type naming every property Discord will ever add. They are merged last, so
	// a model can override what the worker composed.
	Fields map[string]any
	// Nonce is deterministic (the job key). It is sent on a created message so a
	// duplicate produced by an at-least-once replay carries the same value and is
	// recognizable as one; Discord does not de-duplicate on it.
	Nonce string
}

// Client performs one operation against a configured Discord Worker. It is an
// interface so the worker is testable without a live Discord and so a worker name
// binds to exactly one bot identity.
//
// The shape is a single Do rather than a method per operation for the reason the Entra
// and Jira workers give (ADR-0172/0201): this is a typed façade over an HTTP API, and
// the value it adds is at the *model* level — naming the operations and building their
// URLs and bodies — not in wrapping six HTTP calls in six Go signatures.
type Client interface {
	// Do performs one operation and returns what Discord answered: the created, edited
	// or read message, the created thread channel, the array a list matched, or nil
	// where Discord answers with no content.
	Do(ctx context.Context, req Request) (any, error)
}

// Registry resolves a worker name to the [Client] for this kind. Workers are
// registered at the server from managed configuration (a bot token), so a model refers
// to a worker by name only (ADR-0036/0041).
//
// It is the shared [clientreg.Registry], which also carries *why* a configured worker
// is missing from it — the difference between "never configured" and "configured and
// broken", which is what a parked token has to be able to say (ADR-0158).
type Registry = clientreg.Registry[Client]

// NewRegistry creates an empty worker registry.
func NewRegistry() *Registry { return clientreg.New[Client]() }
