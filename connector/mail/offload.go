package mail

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A mail task resolved into plain values, and the function that sends one.
//
// This is ADR-0168's split applied where it actually costs something. CSV import
// proved the mechanism on a kind with no secret in it; mail is the first kind where
// the question the ADR answers has teeth: **the engine decides what to send, the
// worker knows where to send it and with what credential.**
//
// So the boundary falls exactly between those two. Finding the task's detail,
// evaluating its FEEL against the variables the task sees, splitting the recipient
// lists — all of that needs the compiled process and the scope chain, which only the
// engine has, so [Resolve] does it and produces plain strings. The SMTP host, the
// username and the password are never among them: what travels is the worker's
// *name*, and [Run] looks that name up in the registry the worker itself was
// started with. A worker can therefore hold credentials the engine has never seen,
// which is what makes an offloaded mail worker deployable into a network the
// engine does not sit in.
//
// The cost of that split is a failure mode the engine used to catch: a worker
// name no worker holds. The engine can no longer refuse it at lease time, because
// the engine no longer knows. [Run] refuses it instead, naming the worker — and
// the Workers view is where an operator sees which names are configured nowhere.

// Job is a mail task with everything already evaluated: the message, and the name of
// the worker that will carry it. It is what travels with a leased job.
//
// Every field here is model-authored or instance-derived. None of it is a secret,
// and that is a property of the type rather than of the code that fills it in: there
// is nowhere in a Job to put a password.
type Job struct {
	// Worker names the worker's own configured provider. It is a name and not an
	// endpoint on purpose — an endpoint would be half a credential.
	Connector string   `json:"connector"`
	From      string   `json:"from,omitempty"`
	To        []string `json:"to"`
	Cc        []string `json:"cc,omitempty"`
	Bcc       []string `json:"bcc,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Body      string   `json:"body,omitempty"`
	HTML      string   `json:"html,omitempty"`
	// MessageID is the job key, so a message resent after a lease elapsed is
	// identifiable as the same one rather than looking like a second mail.
	MessageID string `json:"messageId,omitempty"`
}

// Resolve turns a compiled mail task into a [Job]: the authored fields
// evaluated against the scope's variables. It is engine work by necessity — FEEL is
// compiled at deploy (ADR-0008/0015) and the scope lives in the store.
//
// It deliberately does not validate that there is a recipient. That check belongs
// with the send, after the worker lookup, so an operator with both an
// unconfigured worker and an empty recipient list hears about the configuration
// first — that being the one they can act on.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("mail: task has no detail")
	}
	// Read the variables the task sees once — up its scope chain, so its own
	// input-mapped locals shadow what it inherits (ADR-0068) — and evaluate every
	// recipient/subject/body expression against that one snapshot.
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("mail: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	return Job{
		Connector: cp.Intern(detail.Connector),
		From:      resolveValue(detail.From, piKey, scopeVars),
		To:        splitAddrs(resolveValue(detail.To, piKey, scopeVars)),
		Cc:        splitAddrs(resolveValue(detail.Cc, piKey, scopeVars)),
		Bcc:       splitAddrs(resolveValue(detail.Bcc, piKey, scopeVars)),
		Subject:   resolveValue(detail.MailSubject, piKey, scopeVars),
		Body:      resolveValue(detail.Body, piKey, scopeVars),
		HTML:      resolveValue(detail.BodyHTML, piKey, scopeVars),
		MessageID: strconv.FormatUint(jobKey, 10),
	}, nil
}

// Run sends a resolved job through the caller's own registry. It is the whole of the
// worker's half, and the in-process path calls it too, so there is one definition of
// what a resolved mail task means rather than two that drift.
//
// The worker lookup comes first: an unconfigured name is the more actionable of
// the two failures a job can carry here, and reporting it ahead of an empty
// recipient list keeps the message an operator sees pointed at the fix.
func Run(ctx context.Context, j Job, reg *Registry) error {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return reg.Unresolved("mail", j.Connector)
	}
	if len(j.To) == 0 {
		return fmt.Errorf("mail: task resolved no recipient")
	}
	return client.Send(ctx, Message{
		From:      j.From,
		To:        j.To,
		Cc:        j.Cc,
		Bcc:       j.Bcc,
		Subject:   j.Subject,
		Body:      j.Body,
		HTML:      j.HTML,
		MessageID: j.MessageID,
	})
}
