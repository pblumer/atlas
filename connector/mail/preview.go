package mail

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ProviderPreview is the zero-configuration mail provider: a connector that frames
// every message exactly like a real one and then delivers it to an in-server
// [Outbox] instead of the internet (ADR-0150).
//
// It exists for the first message someone ever sends from a process. Every other
// provider asks for a submission host, or an OAuth app registration and a refresh
// token in the vault, *before* the author can see whether their subject line renders
// — so the first run of a mail task fails on infrastructure the author was not yet
// thinking about. Preview removes that ordering: model the message, run it, read it,
// and configure a real provider once there is something worth delivering.
//
// It is a rehearsal, not a bypass. The message is framed by the same buildRFC822 the
// SMTP and Gmail providers send, and a message that a real provider would refuse —
// no sender, no recipients — is refused here too, with the same shape of error. What
// a preview run proves about a message therefore stays true after the switch to real
// sending; only the transport changes.
const ProviderPreview = "preview"

// Default bounds on what an outbox keeps. The outbox is memory, not history: it holds
// the newest maxOutboxMessages and clips each stored field, so a process that sends in
// a loop cannot grow the server's heap without limit.
const (
	maxOutboxMessages = 200
	maxOutboxField    = 256 << 10 // 256 KiB per stored body
)

// OutboxMessage is one message a preview connector delivered: the addressing and
// bodies the model authored, plus the framed RFC 5322 bytes that would have gone out.
// Raw is what makes a preview run worth reading — headers, MIME structure and
// encoding are the parts an author cannot check by re-reading their own model.
type OutboxMessage struct {
	Seq       uint64   `json:"seq"`
	Connector string   `json:"connector"`
	At        int64    `json:"at"` // unix nanoseconds the outbox accepted the message
	From      string   `json:"from"`
	To        []string `json:"to"`
	Cc        []string `json:"cc,omitempty"`
	Bcc       []string `json:"bcc,omitempty"`
	Subject   string   `json:"subject"`
	Body      string   `json:"body,omitempty"`
	HTML      string   `json:"html,omitempty"`
	MessageID string   `json:"messageId,omitempty"`
	Raw       string   `json:"raw"`
}

// Outbox is the bounded, newest-last mailbox every preview connector on a server
// delivers into — the "Outbox" view in Operations reads it.
//
// It holds its own mutex, which is deliberate and is the one piece of shared state in
// this package that needs one: a mail worker writes it off the run loop and after
// fsync (I2/I3), while an HTTP handler reads it on a request goroutine, so neither the
// run-loop single-writer discipline nor a lock-free registry swap applies. It is
// explicitly *not* durable state — no event, no log, nothing replayed (I4/I6) — so a
// restart empties it, which is the honest behavior for a preview of something that was
// never sent.
type Outbox struct {
	mu      sync.Mutex
	cap     int
	seq     uint64
	msgs    []OutboxMessage
	dropped uint64 // messages evicted by the capacity bound, so a reader can be told
	now     func() int64
}

// NewOutbox creates an outbox holding at most the newest capacity messages; a
// capacity below one falls back to the default.
func NewOutbox(capacity int) *Outbox {
	if capacity < 1 {
		capacity = maxOutboxMessages
	}
	return &Outbox{cap: capacity, now: func() int64 { return time.Now().UnixNano() }}
}

// Add stamps a message with the next sequence number and the current time, stores it,
// and returns the stored copy. The oldest message is dropped once the outbox is full.
func (o *Outbox) Add(m OutboxMessage) OutboxMessage {
	m.Body, m.HTML, m.Raw = clipField(m.Body), clipField(m.HTML), clipField(m.Raw)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	m.Seq, m.At = o.seq, o.now()
	o.msgs = append(o.msgs, m)
	if len(o.msgs) > o.cap {
		// Copy down rather than reslice: a reslice would keep the dropped messages
		// (and their bodies) alive behind the array for as long as the outbox lives.
		drop := len(o.msgs) - o.cap
		o.msgs = append(o.msgs[:0], o.msgs[drop:]...)
		o.dropped += uint64(drop)
	}
	return m
}

// Deliver stores a message, satisfying [Sink]. It cannot fail: the outbox is this
// process's own memory, and the error exists for the sinks that reach another one.
func (o *Outbox) Deliver(m OutboxMessage) error {
	o.Add(m)
	return nil
}

// Messages returns the newest limit messages, newest first, and whether older ones
// were left behind — by this limit, or earlier by the capacity bound. A limit below
// one returns everything the outbox holds.
func (o *Outbox) Messages(limit int) ([]OutboxMessage, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(o.msgs)
	if limit < 1 || limit > n {
		limit = n
	}
	out := make([]OutboxMessage, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, o.msgs[n-1-i])
	}
	return out, limit < n || o.dropped > 0
}

// Len is how many messages the outbox currently holds.
func (o *Outbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.msgs)
}

// Clear empties the outbox, keeping the sequence counter so a message a reader
// already saw never has its number reused. The drop count starts over with the
// contents: after a clear there is no older message to have been dropped.
func (o *Outbox) Clear() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs, o.dropped = nil, 0
}

// clipField bounds one stored body. Truncation is marked in the text itself, so a
// reader is never shown a silently shortened message and told it is what went out.
func clipField(s string) string {
	if len(s) <= maxOutboxField {
		return s
	}
	return s[:maxOutboxField] + "\n… [truncated by the preview outbox]"
}

// Sink is where a preview connector delivers. [Outbox] is the one that lives in the
// server's own memory, and for as long as mail was an in-process connector it was
// the only one there could be.
//
// It is an interface because mail now runs on a worker (ADR-0168), and a preview
// connector that framed its message in another process and appended it to *that*
// process's memory would have previewed into a window nobody can open. A worker
// binds a sink that hands the message back to the engine's outbox instead, so where
// the framing happened stops being visible to the person reading Operations › Outbox
// — which is the whole promise of the preview provider (ADR-0150).
type Sink interface {
	// Deliver stores a message, or reports why it could not be stored. The in-process
	// outbox never fails; a sink that has to reach another process can, and a preview
	// task whose message did not arrive must fail rather than report a send nobody
	// can find.
	Deliver(OutboxMessage) error
}

// PreviewClient is the [Client] for [ProviderPreview]: it frames the message and
// delivers it to a sink.
type PreviewClient struct {
	outbox    Sink
	connector string // the connector name, so one outbox can serve several of them
	sender    string // the connector's default From
}

// NewPreviewClient binds a preview connector to the sink it delivers into.
func NewPreviewClient(outbox Sink, connector, sender string) *PreviewClient {
	return &PreviewClient{outbox: outbox, connector: connector, sender: sender}
}

// Send frames m and appends it to the outbox. The sender and recipient checks are the
// ones a real provider applies, so a message that previews cleanly is a message that
// can be sent (see [ProviderPreview]).
func (c *PreviewClient) Send(ctx context.Context, m Message) error {
	_ = ctx // nothing is dialed; the signature is the provider seam
	from := firstNonEmpty(m.From, c.sender)
	if from == "" {
		return fmt.Errorf("mail: preview: no sender configured (set the connector's sender or the task's from)")
	}
	if len(recipients(m)) == 0 {
		return fmt.Errorf("mail: preview: message has no recipients")
	}
	if c.outbox == nil {
		return fmt.Errorf("mail: preview: connector %q has no outbox", c.connector)
	}
	return c.outbox.Deliver(OutboxMessage{
		Connector: c.connector,
		From:      from,
		To:        append([]string(nil), m.To...),
		Cc:        append([]string(nil), m.Cc...),
		Bcc:       append([]string(nil), m.Bcc...),
		Subject:   strings.TrimSpace(m.Subject),
		Body:      m.Body,
		HTML:      m.HTML,
		MessageID: m.MessageID,
		Raw:       string(buildRFC822(m, from)),
	})
}
