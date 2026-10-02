package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The mailbox half of the mail Worker (ADR-draft-mailbox-worker).
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
)

// Operations lists every operation a mail task may name, send first.
func Operations() []string {
	return []string{OpSend, OpList, OpGet, OpMove, OpMarkRead, OpMarkUnread, OpDelete, OpReply}
}

// ReadsMailbox reports whether an operation reads the mailbox without changing it.
// ChangesMailbox reports whether it changes the mailbox or writes from it. Send is
// neither: it is the one operation a mail Worker always had, and who may use a sender
// is not what the deploy check governs (ADR-draft-mailbox-worker).
func ReadsMailbox(op string) bool { return op == OpList || op == OpGet }

// ChangesMailbox — see [ReadsMailbox].
func ChangesMailbox(op string) bool {
	switch op {
	case OpMove, OpMarkRead, OpMarkUnread, OpDelete, OpReply:
		return true
	}
	return false
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

// Attachment is what a process learns about an attachment: that it exists, and what it
// is called, its type and its size. Never its content (ADR-draft-mailbox-worker).
type Attachment struct {
	Name        string
	ContentType string
	Size        int64
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
	// References is the message's own References header, which a reply extends. It
	// is not part of what a process sees.
	References string
}

// Fields is the envelope as process variables. The body is included only when the
// caller asked for it — an empty body and an absent one must stay distinguishable.
func (e Envelope) Fields(includeBody bool) map[string]any {
	atts := make([]any, 0, len(e.Attachments))
	for _, a := range e.Attachments {
		// The size as a json.Number: these fields become FEEL values and travel as JSON
		// from a Worker Instance, and both read a number in that shape and no other.
		atts = append(atts, map[string]any{"name": a.Name, "contentType": a.ContentType,
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
	return nil, fmt.Errorf("mail: this worker's provider has no mailbox to read (the preview provider only delivers into the server's outbox)")
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
