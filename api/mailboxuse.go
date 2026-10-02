package api

import (
	"bytes"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
)

// Who may use a mailbox (ADR-0438).
//
// ADR-0205 decides who may see and change a mail Worker and who may point a watch at
// a message name. It never asked who may *use* a Worker from a model, because until
// now using a mail Worker meant sending through it, and a sender is infrastructure.
// A task that lists, reads, files or answers the mail of a mailbox is not: without
// this check the mailbox would be private in the Console and readable by anybody who
// can draw a service task naming it.
//
// So a deploy is refused when a mail task performs a mailbox operation on a Worker the
// deployer cannot reach — viewer for reading, editor for changing or answering — and
// when it names a mail Worker that does not exist, because a model deployed against a
// name nobody holds today reads whichever mailbox somebody configures under it
// tomorrow. A send is not checked: it stays exactly as open as it was.
//
// It is a check at the doors, as ADR-0205's claim is, and not isolation: it decides
// whether a definition may be deployed and is not consulted while a job runs.

// mailboxRefusal is why a model may not be deployed: which task, which Worker, which
// operation. Naming the Worker discloses nothing — every Worker's name is in the
// catalog everybody sees (ADR-0205, "existence is not configuration").
type mailboxRefusal struct {
	element, worker, operation, reason string
}

// mailboxUseBlockingModel parses a model and answers the first mail task whose
// mailbox operation the caller may not perform, or nil.
//
// Must be called on the run-loop goroutine: it reads the worker store.
func (s *Server) mailboxUseBlockingModel(r *http.Request, body []byte) (*mailboxRefusal, error) {
	if !s.authEnabled {
		return nil, nil
	}
	deployables, err := compiler.ParseAll(s.nextKey, 1, bytes.NewReader(body))
	if err != nil {
		// Not this check's business: an unparseable model is refused by the deploy
		// itself, with the compiler's own message.
		return nil, nil
	}
	var uses []compiler.MailboxUse
	for i := range deployables {
		uses = append(uses, deployables[i].Process.MailboxUses()...)
	}
	if len(uses) == 0 {
		return nil, nil
	}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]connector, len(recs))
	for _, c := range recs {
		if c.Kind == connectorKindMail {
			byName[c.Name] = c
		}
	}
	for _, u := range uses {
		need, verb := ScopeRoleViewer, "read"
		if mail.ChangesMailbox(u.Operation) {
			need, verb = ScopeRoleEditor, "change or answer from"
		}
		ref := &mailboxRefusal{element: u.ElementID, worker: u.Worker, operation: u.Operation}
		conn, ok := byName[u.Worker]
		if !ok {
			ref.reason = "no mail worker of that name exists. A task that reads or changes a mailbox " +
				"can only be deployed against a mail worker you can reach — configure it first."
			return ref, nil
		}
		if code, _ := s.checkConnectorRole(r, conn, need); code != 0 {
			ref.reason = "you may not " + verb + " this worker's mailbox. Ask whoever owns the worker to " +
				"share it with you (as " + need + ")."
			return ref, nil
		}
	}
	return nil, nil
}

// mailboxRefusalBody is the answer every door gives a refused model.
func mailboxRefusalBody(ref *mailboxRefusal) map[string]any {
	return map[string]any{
		"error":     "this model uses a mailbox you may not use",
		"worker":    ref.worker,
		"operation": ref.operation,
		"elementId": ref.element,
		"details":   ref.reason,
	}
}

// mailboxRefusalResponse answers 403 with that body.
func mailboxRefusalResponse(w http.ResponseWriter, ref *mailboxRefusal) {
	httpapi.JSON(w, http.StatusForbidden, mailboxRefusalBody(ref))
}

// mailboxRefusalReason renders a refusal as one sentence, for the doors whose answer
// is a reason string rather than a body of their own.
func mailboxRefusalReason(ref *mailboxRefusal) string {
	return "task " + ref.element + " performs " + ref.operation + " on the mail worker " + ref.worker +
		": " + ref.reason
}
