package mail

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
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
// evaluating its FEEL against the instance's variables, splitting the recipient
// lists — all of that needs the compiled process and the scope chain, which only the
// engine has, so [Resolve] does it and produces plain strings. The SMTP host, the
// username and the password are never among them: what travels is the connector's
// *name*, and [Run] looks that name up in the registry the worker itself was
// started with. A worker can therefore hold credentials the engine has never seen,
// which is what makes an offloaded mail connector deployable into a network the
// engine does not sit in.
//
// The cost of that split is a failure mode the engine used to catch: a connector
// name no worker holds. The engine can no longer refuse it at lease time, because
// the engine no longer knows. [Run] refuses it instead, naming the connector — and
// the Workers view is where an operator sees which names are configured nowhere.

// Job is a mail task with everything already evaluated: the message, and the name of
// the connector that will carry it. It is what travels with a leased job.
//
// Every field here is model-authored or instance-derived. None of it is a secret,
// and that is a property of the type rather than of the code that fills it in: there
// is nowhere in a Job to put a password.
type Job struct {
	// Connector names the worker's own configured provider. It is a name and not an
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

// Resolve turns a compiled mail connector task into a [Job]: the authored fields
// evaluated against the scope's variables. It is engine work by necessity — FEEL is
// compiled at deploy (ADR-0008/0015) and the scope lives in the store.
//
// It deliberately does not validate that there is a recipient. That check belongs
// with the send, after the connector lookup, so an operator with both an
// unconfigured connector and an empty recipient list hears about the configuration
// first — that being the one they can act on.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, scope, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("mail: connector task has no detail")
	}
	// Read the scope's variables once: every recipient/subject/body expression
	// evaluates against the same snapshot.
	scopeVars, err := readScopeVars(store, scope)
	if err != nil {
		return Job{}, fmt.Errorf("mail: read variables for scope %d: %w", scope, err)
	}
	return Job{
		Connector: cp.Intern(detail.Connector),
		From:      resolveValue(detail.From, scope, scopeVars),
		To:        splitAddrs(resolveValue(detail.To, scope, scopeVars)),
		Cc:        splitAddrs(resolveValue(detail.Cc, scope, scopeVars)),
		Bcc:       splitAddrs(resolveValue(detail.Bcc, scope, scopeVars)),
		Subject:   resolveValue(detail.MailSubject, scope, scopeVars),
		Body:      resolveValue(detail.Body, scope, scopeVars),
		HTML:      resolveValue(detail.BodyHTML, scope, scopeVars),
		MessageID: strconv.FormatUint(jobKey, 10),
	}, nil
}

// Run sends a resolved job through the caller's own registry. It is the whole of the
// worker's half, and the in-process path calls it too, so there is one definition of
// what a resolved mail task means rather than two that drift.
//
// The connector lookup comes first: an unconfigured name is the more actionable of
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
