package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The mailbox half of the mail Worker (ADR-0438).
//
// Until this file the mail Worker could only send. A mailbox Worker reads as well:
// an inbound watch publishes what arrives in a folder, and a mail task with an
// operation other than send lists, reads, files, marks, deletes or answers a message.
// Both go through one [Mailbox], so "what a message looks like to a process" has one
// definition whether a watch delivered it or a task fetched it.
//
// The shape of that definition is the decision that matters most here, and it is a
// narrow one on purpose: a curated envelope, the body only when a watch or a task asks
// for it, and attachments as names and sizes, never content. Whatever a process
// receives is in the log, the checkpoints, the exporter and every backup, and on a
// shared installation every operator can read it. What it does not receive cannot leak.
//
// Three operations hand an attachment's content on, and each says so in the model
// (ADR-0478): get-attachment reads one into a
// variable under the variable's own budget, export-attachment carries one into an
// object store without the engine ever holding it, and forward sends the whole message
// on. Every document that leaves through one of them is inspected first (inspect.go).

// Operations a mail task can perform. An absent operation is [OpSend], which is every
// mail task authored before mailboxes existed.
const (
	OpSend       = "send"
	OpList       = "list"
	OpGet        = "get"
	OpMove       = "move"
	OpMarkRead   = "mark-read"
	OpMarkUnread = "mark-unread"
	OpDelete     = "delete"
	OpReply      = "reply"
	// The three that hand a document on (ADR-0478).
	OpForward          = "forward"
	OpGetAttachment    = "get-attachment"
	OpExportAttachment = "export-attachment"
)

// Operations lists every operation a mail task may name, send first.
func Operations() []string {
	return []string{OpSend, OpList, OpGet, OpMove, OpMarkRead, OpMarkUnread, OpDelete, OpReply,
		OpForward, OpGetAttachment, OpExportAttachment}
}

// ReadsMailbox reports whether an operation reads the mailbox without changing it.
// ChangesMailbox reports whether it changes the mailbox or writes from it. Send is
// neither: it is the one operation a mail Worker always had, and who may use a sender
// is not what the deploy check governs (ADR-0438).
//
// get-attachment reads, like a get with its body: the document lands in the process
// that asked for it. forward and export-attachment write *from* the mailbox — one to
// whoever the model names, the other into somebody else's store — which is what the
// editor role is asked for, as it is for a reply.
func ReadsMailbox(op string) bool { return op == OpList || op == OpGet || op == OpGetAttachment }

// ChangesMailbox — see [ReadsMailbox].
func ChangesMailbox(op string) bool {
	switch op {
	case OpMove, OpMarkRead, OpMarkUnread, OpDelete, OpReply, OpForward, OpExportAttachment:
		return true
	}
	return false
}

// HandsOnDocuments reports whether an operation carries attachment content out of the
// mailbox, and so inspects it first.
func HandsOnDocuments(op string) bool {
	return op == OpForward || op == OpGetAttachment || op == OpExportAttachment
}

// DefaultFolder is the folder a watch or a list reads when it names none. Every
// provider has one by this name: IMAP's INBOX is mandatory (RFC 3501), Gmail's system
// label is INBOX, and the Graph mailbox maps it onto its well-known inbox folder.
const DefaultFolder = "INBOX"

// DefaultListResults and MaxListResults bound a list. A list's answer becomes a process
// variable, so the cap is about what an instance carries, not about the provider.
const (
	DefaultListResults = 25
	MaxListResults     = 100
)

// MaxBodyBytes is where a message body is cut. A body is opt-in, and even then it lands
// in a variable that is persisted with the instance; a mail with a forwarded thread
// below it is easily a megabyte of text nobody's process reads. The cut is marked, so
// a process can tell a short mail from a shortened one.
const MaxBodyBytes = 64 << 10

// MaxHTMLBytes is where a message's HTML body is cut, once it is cleaned (htmlclean.go).
// The HTML body is opt-in on top of the text, and only for a get
// (ADR-0489): it is markup a stranger wrote, usually several times
// the size of the text it carries, and the variable it lands in is persisted with the
// instance. What still does not fit a process variable is cut further when the answer
// is assembled (fitHTML).
const MaxHTMLBytes = 256 << 10

// bodyRead is how much of a message's content one read carries.
type bodyRead uint8

const (
	readEnvelope bodyRead = iota
	readText
	readHTML
)

func readOf(includeBody bool) bodyRead {
	if includeBody {
		return readText
	}
	return readEnvelope
}

// Attachment is what a process learns about an attachment: that it exists, and what it
// is called, its type and its size. Never its content (ADR-0438).
//
// ID addresses the attachment within its message, for get-attachment and
// export-attachment: Graph's attachment id, Gmail's part id, an IMAP body section.
// Each is stable for as long as the message is — Gmail's own attachmentId is not, it
// changes between two reads of one message, which is why the part id is used instead.
type Attachment struct {
	ID          string
	Name        string
	ContentType string
	Size        int64
}

// Document is one attachment's content, read to be handed on. ContentType is what the
// sender declared, which is a claim like any other header; what the bytes actually are
// is decided by inspecting them (inspect.go), never by this field.
type Document struct {
	ID          string
	Name        string
	ContentType string
	Data        []byte
}

// AuthResults is the receiving server's verdict on a message, read from the topmost
// Authentication-Results header — the one that server added. An empty field means the
// header named no result for that method, not that the method failed.
type AuthResults struct {
	SPF   string
	DKIM  string
	DMARC string
}

// Envelope is one message as a process sees it.
type Envelope struct {
	// ID addresses the message in later operations on the same Worker. It is the
	// provider's own id: a Graph immutable id, a Gmail message id, or for IMAP the
	// folder's UIDVALIDITY, the UID and the folder (see imapMessageID).
	ID                string
	InternetMessageID string
	Folder            string
	From              string
	FromName          string
	ReplyTo           string
	To                []string
	Cc                []string
	Subject           string
	ReceivedAt        time.Time
	Unread            bool
	Attachments       []Attachment
	Auth              AuthResults
	// Body is the plain-text body, set only when the caller asked for it.
	// BodyTruncated says it was cut at MaxBodyBytes.
	Body          string
	BodyTruncated bool
	// HTML is the HTML body, set only when a get asked for it (ADR-0489):
	// decoded as the provider read it, and cleaned and cut at MaxHTMLBytes when the
	// answer is assembled (fitHTML) — whoever implemented the mailbox. HTMLTruncated says
	// it was cut. withHTML records that it was asked for, so a message without an HTML
	// part answers an empty html rather than none.
	HTML          string
	HTMLTruncated bool
	withHTML      bool
	// References is the message's own References header, which a reply extends. It
	// is not part of what a process sees.
	References string
}

// setHTML records a message's HTML body as a provider read it; cut says it read only
// part of it.
func (e *Envelope) setHTML(raw string, cut bool) {
	e.withHTML = true
	e.HTML = raw
	e.HTMLTruncated = cut
}

// Fields is the envelope as process variables. The body is included only when the
// caller asked for it — an empty body and an absent one must stay distinguishable.
func (e Envelope) Fields(includeBody bool) map[string]any {
	atts := make([]any, 0, len(e.Attachments))
	for _, a := range e.Attachments {
		// The size as a json.Number: these fields become FEEL values and travel as JSON
		// from a Worker Instance, and both read a number in that shape and no other.
		atts = append(atts, map[string]any{"id": a.ID, "name": a.Name, "contentType": a.ContentType,
			"size": json.Number(strconv.FormatInt(a.Size, 10))})
	}
	out := map[string]any{
		"messageId":         e.ID,
		"internetMessageId": e.InternetMessageID,
		"folder":            e.Folder,
		"from":              e.From,
		"fromName":          e.FromName,
		"replyTo":           e.ReplyTo,
		"to":                stringsToAny(e.To),
		"cc":                stringsToAny(e.Cc),
		"subject":           e.Subject,
		"receivedAt":        formatReceived(e.ReceivedAt),
		"unread":            e.Unread,
		"hasAttachments":    len(e.Attachments) > 0,
		"attachments":       atts,
		"auth":              map[string]any{"spf": e.Auth.SPF, "dkim": e.Auth.DKIM, "dmarc": e.Auth.DMARC},
	}
	if includeBody {
		out["body"] = e.Body
		out["bodyTruncated"] = e.BodyTruncated
	}
	if e.withHTML {
		out["html"] = e.HTML
		out["htmlTruncated"] = e.HTMLTruncated
	}
	return out
}

// formatReceived renders a receive time as RFC 3339 in UTC, or "" when the provider
// gave none — a zero time rendered as year one would read as a real, very old date.
func formatReceived(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func stringsToAny(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// WatchRequest is one read of a watched folder: everything after Cursor, at most
// Limit messages, oldest first.
type WatchRequest struct {
	Folder      string
	Cursor      string
	Limit       int
	IncludeBody bool
}

// Received is one message a watch read, with the idempotency mark the engine
// deduplicates it under (ADR-0075): MarkKey scopes the high-water mark and Seq is
// compared against it. Which of the two carries the identity depends on the provider's
// sequence, and that is the provider's business, not the bridge's.
type Received struct {
	MarkKey  string
	Seq      uint64
	Envelope Envelope
}

// WatchPage is a watch read's answer: the messages, oldest first, and where the next
// read resumes. A page may be empty and still move the cursor — the folder was
// renumbered, or history expired — and the caller must store it either way.
type WatchPage struct {
	Items  []Received
	Cursor string
	// Gap is set when the read had to skip mail it could no longer reach — an IMAP
	// folder the server renumbered, Gmail history older than Gmail keeps — and says
	// what was skipped. The caller logs it: a watch that silently resumed past a gap
	// would look like a quiet mailbox.
	Gap string
}

// ListRequest is a list operation: the newest MaxResults messages of Folder.
type ListRequest struct {
	Folder      string
	MaxResults  int
	UnreadOnly  bool
	IncludeBody bool
}

// normalized fills a list request's defaults and holds it to the caps.
func (r ListRequest) normalized() ListRequest {
	if strings.TrimSpace(r.Folder) == "" {
		r.Folder = DefaultFolder
	}
	if r.MaxResults <= 0 {
		r.MaxResults = DefaultListResults
	}
	if r.MaxResults > MaxListResults {
		r.MaxResults = MaxListResults
	}
	return r
}

// Reply is a reply operation's content. MessageID is the job key, so a reply resent
// after a lease elapsed carries the same Message-ID as the first attempt, exactly as a
// send does.
type Reply struct {
	Body      string
	HTML      string
	MessageID string
}

// Forward is a forward operation's content: who receives the message, and the text
// written above it. MessageID is the job key, as a reply's is.
//
// Inspect is called with every document the message carries before anything is sent,
// and an error from it stops the forward: a message is handed on whole or not at all.
// A provider that forwards server-side (Graph) still reads each attachment for it,
// which is the price of not forwarding what nobody looked at.
type Forward struct {
	To        []string
	Cc        []string
	Bcc       []string
	Body      string
	HTML      string
	MessageID string
	Inspect   func(Document) error
}

// Mailbox is a mail Worker's mailbox. Every provider implements the same operations;
// what differs is how a folder and a message are addressed and how a watch's sequence
// is made.
type Mailbox interface {
	// WatchTip answers the cursor of a folder's newest message without reading any,
	// which is how a forward-only watch skips a mailbox's history.
	WatchTip(ctx context.Context, folder string) (string, error)
	// WatchSince reads what arrived after a cursor.
	WatchSince(ctx context.Context, req WatchRequest) (WatchPage, error)
	// List answers the newest messages of a folder, newest first.
	List(ctx context.Context, req ListRequest) ([]Envelope, error)
	// Get reads one message.
	Get(ctx context.Context, id string, includeBody bool) (Envelope, error)
	// Move files a message into another folder and answers the id it has there, which
	// for IMAP is a new one and for Graph and Gmail is the same.
	Move(ctx context.Context, id, destination string) (string, error)
	// SetRead marks a message read or unread.
	SetRead(ctx context.Context, id string, read bool) error
	// Delete moves a message to the provider's trash.
	Delete(ctx context.Context, id string) error
	// Reply answers a message to its Reply-To or From, threaded under it.
	Reply(ctx context.Context, id string, r Reply) error
	// Attachment reads one attachment's content, addressed by the id the envelope
	// listed it under, refusing one larger than limits.Document rather than holding it.
	Attachment(ctx context.Context, id, attachmentID string) (Document, error)
	// Forward sends the message, attachments included, to new recipients.
	Forward(ctx context.Context, id string, f Forward) error
}

// HTMLMailbox is a mailbox that also reads a message's HTML body, for a get that asks for
// it (ADR-0489). Every provider's mailbox is one; it is an interface of
// its own so the operations that never need markup keep the narrower Mailbox.
type HTMLMailbox interface {
	// GetHTML reads one message with its text and its HTML body, cleaned.
	GetHTML(ctx context.Context, id string) (Envelope, error)
}

// MailboxOf answers the mailbox behind a Worker's client, or why it has none. A client
// either is a mailbox (a test's fake) or can produce one (the provider clients); the
// preview provider can do neither, because it delivers into the server's outbox and
// there is no mailbox behind it to read.
func MailboxOf(c Client) (Mailbox, error) {
	switch v := c.(type) {
	case nil:
		return nil, fmt.Errorf("mail: no worker client")
	case Mailbox:
		return v, nil
	case interface{ Mailbox() (Mailbox, error) }:
		return v.Mailbox()
	}
	return nil, errPreviewHasNoMailbox
}

// The reasons a mail Worker has no mailbox, each said once: MailboxOf answers them for
// a built client and CanReadMailbox for a Worker's configuration, and the two must not
// drift into different words for the same fact.
var (
	errNoIMAPEndpoint = errors.New("mail: this SMTP worker has no IMAP endpoint, so it cannot read a mailbox " +
		"(set the worker's mailbox endpoint, e.g. imaps://imap.example.com:993)")
	errGraphHasNoSender    = errors.New("mail: graph: the worker names no sender mailbox, so there is no mailbox to read")
	errPreviewHasNoMailbox = errors.New("mail: this worker's provider has no mailbox to read " +
		"(the preview provider only delivers into the server's outbox)")
)

// CanReadMailbox says, from a Worker's configuration alone, why it has no mailbox to
// read, or nil when it has one. It is MailboxOf asked before any client is built, so a
// watch can be refused on a Worker that could never serve it rather than skipped at
// every poll. A Worker that has a mailbox can still fail to read it — a wrong password,
// a missing permission — and that is answered by the first read, not here.
func CanReadMailbox(provider, sender, imapEndpoint string) error {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", ProviderSMTP:
		if strings.TrimSpace(imapEndpoint) == "" {
			return errNoIMAPEndpoint
		}
	case ProviderMicrosoft:
		if strings.TrimSpace(sender) == "" {
			return errGraphHasNoSender
		}
	case ProviderPreview:
		return errPreviewHasNoMailbox
	}
	return nil
}

// SenderAllowed reports whether a sender address passes an allow-list. An empty list
// allows everyone. An entry with an "@" in front ("@example.com"), or no "@" at all
// ("example.com"), allows that exact domain; anything else is an exact address. The
// comparison ignores case, and a domain entry does not reach a subdomain — allowing
// example.com must not allow mail.example.com.evil.net, and allowing sub-domains on
// purpose is a list entry each.
func SenderAllowed(from string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	from = strings.ToLower(strings.TrimSpace(from))
	at := strings.LastIndex(from, "@")
	if at <= 0 || at == len(from)-1 {
		return false // not an address, so not on anybody's list
	}
	domain := from[at+1:]
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		switch {
		case a == "":
			continue
		case strings.HasPrefix(a, "@"):
			if domain == a[1:] {
				return true
			}
		case !strings.Contains(a, "@"):
			if domain == a {
				return true
			}
		case a == from:
			return true
		}
	}
	return false
}

// NormalizeAllowedSenders checks and normalizes an allow-list as an operator wrote it:
// trimmed, lower-cased, empties dropped, a bare domain written as "@domain". It refuses
// an entry that is neither an address nor a domain, naming it, so a typo is not a rule
// that quietly matches nothing.
func NormalizeAllowedSenders(in []string) ([]string, error) {
	var out []string
	for _, raw := range in {
		a := strings.ToLower(strings.TrimSpace(raw))
		if a == "" {
			continue
		}
		if strings.ContainsAny(a, " \t,;<>\"") {
			return nil, fmt.Errorf("allowedSenders entry %q is not an address or a domain", raw)
		}
		local, domain, hasAt := strings.Cut(a, "@")
		if !hasAt {
			domain, local = a, ""
		}
		if strings.Contains(domain, "@") || !strings.Contains(domain, ".") ||
			strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
			return nil, fmt.Errorf("allowedSenders entry %q is not an address or a domain", raw)
		}
		if local == "" {
			out = append(out, "@"+domain)
			continue
		}
		out = append(out, local+"@"+domain)
	}
	return out, nil
}
