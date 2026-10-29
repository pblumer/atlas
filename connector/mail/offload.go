package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/limits"
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
	// The mailbox half (ADR-0438). Operation is empty for a send.
	// Target is the message an operation addresses — the messageId a watch or a list
	// answered — named apart from MessageID above, which is this job's own key.
	Operation   string `json:"operation,omitempty"`
	Folder      string `json:"folder,omitempty"`
	Target      string `json:"target,omitempty"`
	Destination string `json:"destination,omitempty"`
	MaxResults  int    `json:"maxResults,omitempty"`
	IncludeBody bool   `json:"includeBody,omitempty"`
	// IncludeHTML asks a get for the HTML body as well, cleaned
	// (ADR-0489). A Worker Instance older than the field ignores it
	// and answers the text alone, which is what the task asked for before.
	IncludeHTML    bool   `json:"includeHtml,omitempty"`
	UnreadOnly     bool   `json:"unreadOnly,omitempty"`
	ResultVariable string `json:"resultVariable,omitempty"`
	// The documents half (ADR-0478). Attachment is the
	// attachment get-attachment and export-attachment address, by the id the envelope
	// listed it under; AllowedTypes the media types the task accepts, empty for any the
	// inspection admits. Store, Bucket and Key are where export-attachment puts the
	// document — Store an S3 Worker's *name*, like Connector, which whoever runs the
	// job resolves against the Workers it holds.
	Attachment   string   `json:"attachment,omitempty"`
	AllowedTypes []string `json:"allowedTypes,omitempty"`
	Store        string   `json:"store,omitempty"`
	Bucket       string   `json:"bucket,omitempty"`
	Key          string   `json:"key,omitempty"`
	// ErrorCodes are the codes the task's process catches, of the ones this Worker Type
	// throws. A refused document is thrown as a BPMN error only when its code is among
	// them, and fails the job otherwise (ADR-0461). They travel with the job because a
	// worker has no compiled process to ask.
	ErrorCodes []string `json:"errorCodes,omitempty"`
	// ResolveError is why the engine could not finish resolving the task — today, the
	// link of the task the mail is for (ADR-draft-user-task-answered-by-link). Whoever
	// runs the job fails it with this message, before any worker is looked up, so the
	// incident says what is wrong wherever the job ran.
	ResolveError string `json:"resolveError,omitempty"`
}

// AnswerLinker mints the link of a user task answered by link: the task with BPMN id
// task, in the instance piKey, inside the scope instance scopeKey the mail is sent from
// (ADR-draft-user-task-answered-by-link). The scope is what tells two runs of one event
// subprocess apart, each mailing the link of its own task. A [Directory] that can
// implements it: both are the installation's answers to a question a model cannot carry
// — an address, and a link signed with the installation's key.
type AnswerLinker interface {
	AnswerLink(piKey, scopeKey uint64, task string) (string, error)
}

// DocumentStore puts a document into an object store a named Worker reaches. It is how
// export-attachment hands a document to an S3 Worker without the mail package knowing
// S3: whoever runs mail jobs and holds S3 Workers passes an adapter (s3.Documents).
type DocumentStore interface {
	PutDocument(ctx context.Context, worker, bucket, key, contentType, disposition string, data []byte, metadata map[string]string) (map[string]any, error)
}

// Directory resolves a recipient that is not a mail address — a person or a group
// this installation knows — to the addresses it should reach.
//
// It exists so a model can address somebody it only holds a *reference* to. The
// portal names people by principal id and by nothing else, and resolves a name or
// an address from the account at the moment a screen is rendered, never copying
// one into an order or a variable (ADR-0314). A notification
// is that rule applied to a message instead of a screen: the model writes
// `to="=approvalRef"`, the address is looked up here, at send time, and it exists
// nowhere else.
//
// A nil Directory resolves nothing and leaves every recipient as written, which is
// what this did before the interface existed.
type Directory interface {
	Recipients(ref string) ([]string, error)
}

// Resolve turns a compiled mail task into a [Job]: the authored fields
// evaluated against the scope's variables, with any recipient that is a reference
// rather than an address resolved through dir. It is engine work by necessity —
// FEEL is compiled at deploy (ADR-0008/0015) and the scope lives in the store.
//
// It deliberately does not validate that there is a recipient. That check belongs
// with the send, after the worker lookup, so an operator with both an
// unconfigured worker and an empty recipient list hears about the configuration
// first — that being the one they can act on.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64, dir Directory) (Job, error) {
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
	if detail.MailOp != "" {
		j := Job{
			Connector:   cp.Intern(detail.Connector),
			Operation:   detail.MailOp,
			Folder:      resolveValue(detail.MailFolder, piKey, scopeVars),
			Target:      strings.TrimSpace(resolveValue(detail.MailMessage, piKey, scopeVars)),
			Destination: resolveValue(detail.MailDestination, piKey, scopeVars),
			MaxResults:  int(detail.MailMaxResults),
			IncludeBody: detail.MailIncludeBody,
			IncludeHTML: detail.MailIncludeHTML,
			UnreadOnly:  detail.MailUnreadOnly,
			Body:        mailText(detail.BodyTmpl, detail.Body, piKey, scopeVars, nil),
			HTML:        mailText(detail.BodyHTMLTmpl, detail.BodyHTML, piKey, scopeVars, html.EscapeString),
			MessageID:   strconv.FormatUint(jobKey, 10),
		}
		if detail.ResultVar >= 0 {
			j.ResultVariable = cp.Intern(detail.ResultVar)
		}
		if detail.MailOp == OpForward {
			// A forward's recipients are resolved like a send's: a reference to a
			// person this installation knows becomes their address here, at send time.
			for _, r := range []struct {
				expr compiler.RestExpr
				into *[]string
			}{{detail.To, &j.To}, {detail.Cc, &j.Cc}, {detail.Bcc, &j.Bcc}} {
				if *r.into, err = lookUp(splitAddrs(resolveValue(r.expr, piKey, scopeVars)), dir); err != nil {
					return Job{}, err
				}
			}
		}
		if detail.MailOp == OpGetAttachment || detail.MailOp == OpExportAttachment {
			j.Attachment = strings.TrimSpace(resolveValue(detail.MailAttachment, piKey, scopeVars))
			j.AllowedTypes = detail.MailAllowedTypes
		}
		if detail.MailOp == OpExportAttachment {
			j.Store = detail.MailStore
			j.Bucket = strings.TrimSpace(resolveValue(detail.MailBucket, piKey, scopeVars))
			j.Key = strings.TrimSpace(resolveValue(detail.MailKey, piKey, scopeVars))
		}
		if HandsOnDocuments(detail.MailOp) {
			for _, code := range Codes {
				if cp.CatchesErrorCode(code) {
					j.ErrorCodes = append(j.ErrorCodes, code)
				}
			}
		}
		return j, nil
	}
	to, err := lookUp(splitAddrs(resolveValue(detail.To, piKey, scopeVars)), dir)
	if err != nil {
		return Job{}, err
	}
	cc, err := lookUp(splitAddrs(resolveValue(detail.Cc, piKey, scopeVars)), dir)
	if err != nil {
		return Job{}, err
	}
	bcc, err := lookUp(splitAddrs(resolveValue(detail.Bcc, piKey, scopeVars)), dir)
	if err != nil {
		return Job{}, err
	}
	// A message that carries the link of a task answered by link reads it as answerLink.
	// The link is added to this resolution's variables and to nothing else: it is never
	// written to the instance, where every operator could read it.
	if task, ok := cp.AnswerTask(ei.ElementId); ok {
		linker, ok := dir.(AnswerLinker)
		if !ok {
			return Job{Connector: cp.Intern(detail.Connector), MessageID: strconv.FormatUint(jobKey, 10),
				ResolveError: fmt.Sprintf("mail: this task carries the answer link of %q, and nothing here mints answer links", task)}, nil
		}
		link, err := linker.AnswerLink(piKey, ei.FlowScopeKey, task)
		if err != nil {
			return Job{Connector: cp.Intern(detail.Connector), MessageID: strconv.FormatUint(jobKey, 10),
				ResolveError: fmt.Sprintf("mail: the answer link of %q: %v", task, err)}, nil
		}
		scopeVars[compiler.AnswerLinkVariable] = model.VariableValue{Name: compiler.AnswerLinkVariable, Kind: model.VarString, Text: link}
	}
	return Job{
		Connector: cp.Intern(detail.Connector),
		From:      resolveValue(detail.From, piKey, scopeVars),
		To:        to,
		Cc:        cc,
		Bcc:       bcc,
		Subject:   mailSubject(detail, piKey, scopeVars),
		Body:      mailText(detail.BodyTmpl, detail.Body, piKey, scopeVars, nil),
		HTML:      mailText(detail.BodyHTMLTmpl, detail.BodyHTML, piKey, scopeVars, html.EscapeString),
		MessageID: strconv.FormatUint(jobKey, 10),
	}, nil
}

// Run performs a resolved job through the caller's own registry: a send, or a
// mailbox operation, whose answer it returns for the task's result variable (nil
// when the operation answers nothing). It is the whole of the worker's half, and the
// in-process path calls it too, so there is one definition of what a resolved mail
// task means rather than two that drift.
//
// The worker lookup comes first: an unconfigured name is the more actionable of
// the two failures a job can carry here, and reporting it ahead of an empty
// recipient list keeps the message an operator sees pointed at the fix.
func Run(ctx context.Context, j Job, reg *Registry) (any, error) {
	return RunWith(ctx, j, reg, nil)
}

// RunWith is [Run] with the object stores export-attachment may put a document into;
// nil holds none, and an export then fails naming the store it could not reach. A
// document inspection refuses ends the job in [CodeAttachmentRejected] when the task's
// process catches it, and fails it otherwise.
func RunWith(ctx context.Context, j Job, reg *Registry, docs DocumentStore) (any, error) {
	res, err := run(ctx, j, reg, docs)
	var rej *Rejection
	if errors.As(err, &rej) && slices.Contains(j.ErrorCodes, CodeAttachmentRejected) {
		return nil, &job.BPMNError{Code: CodeAttachmentRejected, Message: rej.Error()}
	}
	return res, err
}

func run(ctx context.Context, j Job, reg *Registry, docs DocumentStore) (any, error) {
	if j.ResolveError != "" {
		return nil, errors.New(j.ResolveError)
	}
	client, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("mail", j.Connector)
	}
	if j.Operation == "" || j.Operation == OpSend {
		if len(j.To) == 0 {
			return nil, fmt.Errorf("mail: task resolved no recipient")
		}
		return nil, client.Send(ctx, Message{
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
	mb, err := MailboxOf(client)
	if err != nil {
		return nil, fmt.Errorf("mail: worker %q: %w", j.Connector, err)
	}
	// One operation is one worker call, and the shared budget bounds it whole — a
	// list of twenty-five is twenty-six requests to Gmail, not twenty-six budgets.
	ctx, cancel := context.WithTimeout(ctx, nettimeout.Default)
	defer cancel()
	if HandsOnDocuments(j.Operation) {
		in := inspector{scanner: ScannerOf(client), allowed: j.AllowedTypes}
		return runDocuments(ctx, mb, j, in, docs)
	}
	return runMailbox(ctx, mb, j)
}

// runMailbox performs one mailbox operation.
func runMailbox(ctx context.Context, mb Mailbox, j Job) (any, error) {
	if j.Operation != OpList && j.Target == "" {
		return nil, fmt.Errorf("mail: %s: the task's messageId resolved to nothing", j.Operation)
	}
	switch j.Operation {
	case OpList:
		envs, err := mb.List(ctx, ListRequest{Folder: j.Folder, MaxResults: j.MaxResults, UnreadOnly: j.UnreadOnly, IncludeBody: j.IncludeBody})
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(envs))
		for _, e := range envs {
			out = append(out, e.Fields(j.IncludeBody))
		}
		return out, nil
	case OpGet:
		if j.IncludeHTML {
			hm, ok := mb.(HTMLMailbox)
			if !ok {
				return nil, fmt.Errorf("mail: get: this Worker's mailbox cannot read a message's HTML body")
			}
			e, err := hm.GetHTML(ctx, j.Target)
			if err != nil {
				return nil, err
			}
			return fitHTML(e, limits.Default().Variable-limits.Default().ErrorBody), nil
		}
		e, err := mb.Get(ctx, j.Target, j.IncludeBody)
		if err != nil {
			return nil, err
		}
		return e.Fields(j.IncludeBody), nil
	case OpMove:
		if strings.TrimSpace(j.Destination) == "" {
			return nil, fmt.Errorf("mail: move: the task's destination resolved to nothing")
		}
		id, err := mb.Move(ctx, j.Target, j.Destination)
		if err != nil || id == "" {
			// No id is an IMAP server that cannot say where the message went; the
			// variable is left as it was rather than overwritten with nothing.
			return nil, err
		}
		return id, nil
	case OpMarkRead, OpMarkUnread:
		return nil, mb.SetRead(ctx, j.Target, j.Operation == OpMarkRead)
	case OpDelete:
		return nil, mb.Delete(ctx, j.Target)
	case OpReply:
		return nil, mb.Reply(ctx, j.Target, Reply{Body: j.Body, HTML: j.HTML, MessageID: j.MessageID})
	}
	return nil, fmt.Errorf("mail: unknown operation %q", j.Operation)
}

// runDocuments performs one of the three operations that hand a document on. Each
// inspects every document before it leaves; none hands on one inspection refused.
func runDocuments(ctx context.Context, mb Mailbox, j Job, in inspector, docs DocumentStore) (any, error) {
	if j.Target == "" {
		return nil, fmt.Errorf("mail: %s: the task's messageId resolved to nothing", j.Operation)
	}
	if j.Operation == OpForward {
		if len(j.To) == 0 {
			return nil, fmt.Errorf("mail: forward: the task resolved no recipient")
		}
		return nil, mb.Forward(ctx, j.Target, Forward{
			To: j.To, Cc: j.Cc, Bcc: j.Bcc, Body: j.Body, HTML: j.HTML, MessageID: j.MessageID,
			Inspect: func(d Document) error {
				_, err := in.inspect(ctx, d)
				return err
			},
		})
	}
	if j.Attachment == "" {
		return nil, fmt.Errorf("mail: %s: the task's attachmentId resolved to nothing", j.Operation)
	}
	if j.Operation == OpExportAttachment && docs == nil {
		// Before the mailbox is read: a Worker Instance that cannot reach the store
		// should not first pull a document across the network to throw it away.
		return nil, fmt.Errorf("mail: export-attachment: this Worker Instance holds no S3 Worker, so it cannot reach %q "+
			"(serve the s3 kind beside mail, or configure the S3 Worker on this worker's host)", j.Store)
	}
	d, err := mb.Attachment(ctx, j.Target, j.Attachment)
	if err != nil {
		return nil, err
	}
	v, err := in.inspect(ctx, d)
	if err != nil {
		return nil, err
	}
	switch j.Operation {
	case OpGetAttachment:
		return attachmentContent(d, v)
	case OpExportAttachment:
		return exportAttachment(ctx, j, d, v, docs)
	}
	return nil, fmt.Errorf("mail: unknown operation %q", j.Operation)
}

// fitHTML is a get's answer with its HTML body: cleaned (htmlclean.go), cut at
// MaxHTMLBytes, and held to what one process variable may weigh. It is the one place
// every HTML body passes, whichever mailbox read it, so it is where the cleaning is.
// The HTML is the one part that can be large, and the variable is stored as JSON, where
// every < and > grows to six bytes; so the answer is measured as stored, and the HTML
// is cut further — before a tag, and marked as cut — until it fits. The text is capped
// already and is never what gives way.
func fitHTML(e Envelope, budget int64) map[string]any {
	e.withHTML = true
	if strings.TrimSpace(e.HTML) == "" {
		e.HTML = ""
	} else {
		var cut bool
		e.HTML, cut = capHTML(cleanHTML(e.HTML), MaxHTMLBytes)
		e.HTMLTruncated = e.HTMLTruncated || cut
	}
	for range 12 {
		fields := e.Fields(true)
		size := encodedSize(fields)
		if size <= budget || e.HTML == "" {
			return fields
		}
		markup := encodedSize(e.HTML)
		room := budget - (size - markup)
		next := 0
		if room > 0 {
			next = int(int64(len(e.HTML)) * room / markup)
		}
		e.HTML, _ = capHTML(e.HTML, next)
		e.HTMLTruncated = true
	}
	e.HTML = ""
	return e.Fields(true)
}

// encodedSize is how many bytes a value takes in the JSON a variable is stored as. An
// envelope's fields are strings, booleans, numbers and lists of them, which always
// encode.
func encodedSize(v any) int64 {
	b, _ := json.Marshal(v)
	return int64(len(b))
}

// attachmentContent is get-attachment's answer: the document base64-encoded, with what
// inspection found. The content type it reports is the detected one — the sender's
// claim is kept beside it as declaredType, never in its place.
//
// It refuses a document whose answer would not fit a process variable rather than
// letting the engine refuse the completion: here the attachment and the alternative
// are both still in view.
func attachmentContent(d Document, v Verdict) (any, error) {
	encoded := base64.StdEncoding.EncodeToString(d.Data)
	l := limits.Default()
	if int64(len(encoded)) > l.Variable-l.ErrorBody {
		return nil, fmt.Errorf("mail: get-attachment: attachment %q is %d bytes, %d once base64 has grown it, which does not fit "+
			"a process variable (%d bytes, ATLAS_LIMIT_VARIABLE); use export-attachment to put it into an object store instead",
			d.Name, len(d.Data), len(encoded), l.Variable)
	}
	return map[string]any{
		"id":           d.ID,
		"name":         d.Name,
		"contentType":  v.DetectedType,
		"declaredType": d.ContentType,
		"size":         json.Number(strconv.Itoa(len(d.Data))),
		"encoding":     "base64",
		"content":      encoded,
		"inspection":   v.Fields(),
	}, nil
}

// exportAttachment puts the document into the store and answers where it went. The
// object is stored under its detected type, so a presigned link opens what the bytes
// are and not what the sender called them, and a document a browser would run as a
// page is stored to download rather than to open. What inspection found is written
// into the object's metadata, so whoever presents it later can see how far it was
// checked.
func exportAttachment(ctx context.Context, j Job, d Document, v Verdict, docs DocumentStore) (any, error) {
	if j.Bucket == "" || j.Key == "" {
		return nil, fmt.Errorf("mail: export-attachment: the task's bucket and key must both resolve to a value")
	}
	disposition := "inline"
	if v.Active {
		disposition = "attachment"
	}
	disposition += "; filename*=UTF-8''" + url.PathEscape(d.Name)
	meta := map[string]string{
		"atlas-detected-type": v.DetectedType,
		"atlas-scanned":       strconv.FormatBool(v.Scanned),
	}
	if v.Scanner != "" {
		meta["atlas-scanner"] = v.Scanner
	}
	put, err := docs.PutDocument(ctx, j.Store, j.Bucket, j.Key, v.DetectedType, disposition, d.Data, meta)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for k, val := range put {
		out[k] = val
	}
	out["name"] = d.Name
	out["contentType"] = v.DetectedType
	out["declaredType"] = d.ContentType
	out["inspection"] = v.Fields()
	return out, nil
}

// lookUp turns a recipient list into addresses: an entry with an "@" is already
// one, anything else is a reference for the directory to resolve.
//
// The test is deliberately that crude. A mail address is the thing with an "@" in
// it, and every string without one is something this server would have handed to
// SMTP to be rejected — so treating it as a reference takes nothing away and needs
// no new syntax in the model. A reference nobody answers to fails the job, which
// parks the task with a message naming it, because a notification quietly sent to
// nobody is the failure mode a notification exists to avoid.
func lookUp(rcpts []string, dir Directory) ([]string, error) {
	if dir == nil || len(rcpts) == 0 {
		return rcpts, nil
	}
	// Declared rather than made: nothing resolved has to come back as nil and not
	// as an empty list. A leased job's payload is JSON, where the two are `null`
	// and `[]`, and an unresolved expression must travel as the absence of a value
	// rather than as a value that happens to be empty — which is a contract a test
	// holds and this function quietly broke.
	var out []string
	for _, r := range rcpts {
		if strings.Contains(r, "@") {
			out = append(out, r)
			continue
		}
		found, err := dir.Recipients(r)
		if err != nil {
			return nil, fmt.Errorf("mail: recipient %q: %w", r, err)
		}
		if len(found) == 0 {
			return nil, fmt.Errorf("mail: recipient %q is neither an address nor anybody this server knows", r)
		}
		out = append(out, found...)
	}
	return out, nil
}
