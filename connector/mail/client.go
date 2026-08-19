// Package mail integrates an outbound e-mail provider as a server-registered Atlas
// connector: a BPMN mail connector task sends a model-authored message through a
// configured provider via the job path (ADR-0079), mirroring how the clio package
// delegates an append to a registry-managed endpoint (ADR-0036). The integration
// inherits the job protocol's durability and non-blocking properties (ADR-0007):
//
//   - A connector task creates a job carrying the reserved [compiler.MailJobType].
//     The processor never performs the outbound send itself, so it stays
//     allocation-free (invariant I1) and free of any SMTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, sends the message
//     off the processor goroutine and after fsync (invariant I2, never inside
//     applyToState / I4), and completes the job, which drives the token onward.
//   - The provider host and credentials live in a server-side [Registry] keyed by
//     connector name, so a model refers to a provider by name only and never carries
//     a host or a secret (ADR-0036/0041). Only the message (recipients, subject,
//     body) is authored in the model, like a REST task's endpoint (ADR-0067).
//
// The first provider is SMTP ([SMTPClient]), which reaches Google, Microsoft 365,
// and any standards-compliant server via its submission endpoint; native Gmail /
// Microsoft Graph API providers are additive behind the same [Client] seam (ADR-0079).
//
// Delivery is at-least-once (a crash between "the provider accepted the message" and
// "job completed" replays the send); every message carries the job key as its
// RFC 5322 Message-ID so a provider or downstream de-duplicator can recognize a
// replayed send rather than delivering it twice.
package mail

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
)

// Message is one e-mail an outbound mail connector task sends. To is the required
// recipient list; Cc and Bcc are optional. From overrides the provider's default
// sender when set. MessageID is deterministic (the job key), so an at-least-once
// retry carries the same RFC 5322 Message-ID and can be de-duplicated rather than
// delivered twice.
type Message struct {
	From    string
	To      []string
	Cc      []string
	Bcc     []string
	Subject string
	Body    string
	// HTML is the optional HTML body (ADR-0079, amended). When set alongside Body the
	// message goes out as multipart/alternative — the plain text for clients that
	// cannot or will not render HTML, the markup for those that can; alone it is a
	// plain text/html message. Empty leaves the message exactly as it was before HTML
	// bodies existed: one text/plain part.
	HTML      string
	MessageID string
}

// Client sends a Message through one configured mail provider. It is an interface so
// the worker is testable without a live server and so a connector name binds to
// exactly one provider (SMTP today; a native Gmail / Graph provider is additive).
type Client interface {
	Send(ctx context.Context, m Message) error
}

// Registry resolves a connector name to the [Client] for its mail provider.
// Connectors are registered at the server from managed configuration (endpoint plus
// credentials), so a model refers to a connector by name only (ADR-0036/0041). A
// Registry is read-only once populated and safe for concurrent use by workers.
type Registry struct {
	clients map[string]Client
}

// NewRegistry creates an empty connector registry.
func NewRegistry() *Registry { return &Registry{clients: map[string]Client{}} }

// Register binds a connector name to its client. Registering the same name again
// replaces the earlier binding (last write wins), so reconfiguration is simple.
func (r *Registry) Register(name string, c Client) { r.clients[name] = c }

// Client returns the client bound to name, or nil and false if none is registered.
func (r *Registry) Client(name string) (Client, bool) {
	c, ok := r.clients[name]
	return c, ok
}

// Replace swaps the whole set of registered connectors at once, so a server can
// rebuild the registry from managed configuration after a change (ADR-0041). The
// caller must serialize Replace with the workers that read the registry — the Atlas
// server does both on its run-loop goroutine — so no lock is needed. A nil map
// clears the registry.
func (r *Registry) Replace(clients map[string]Client) {
	if clients == nil {
		clients = map[string]Client{}
	}
	r.clients = clients
}

// Connector is the server-side configuration of one SMTP mail provider: the
// submission Endpoint ("host:port"), the auth Username and Password (the Password is
// the resolved secret — an app password or account password — held only at call
// time, never persisted, I6), and the default From address a task that authors no
// sender falls back to.
type Connector struct {
	Endpoint string
	Username string
	Password string
	From     string
	// ImplicitTLS opens the connection with a TLS handshake instead of upgrading a
	// plaintext one with STARTTLS. [NewSMTPClient] derives it from the endpoint's
	// port (465, the RFC 8314 submissions port), which is what an "smtps://" endpoint
	// normalizes to; a caller can also set it outright.
	ImplicitTLS bool
}

// sendFunc is the seam a test substitutes for the SMTP transport, so the worker and
// the MIME framing are exercised without a live server. It once matched
// [smtp.SendMail]; it now matches [submit], which does what SendMail does and also
// reaches an implicit-TLS submissions server — and takes the caller's context, so a
// send is bounded by the job that asked for it rather than by the network alone.
type sendFunc func(ctx context.Context, addr string, a smtp.Auth, from string, to []string, msg []byte) error

// SMTPClient sends a Message over SMTP (the submission endpoint of any standards
// compliant provider, including Google and Microsoft 365). It authenticates with the
// connector's username/password when a username is configured, and frames the
// message as a UTF-8 MIME e-mail (text, HTML, or both — see buildRFC822).
type SMTPClient struct {
	conn Connector
	send sendFunc
}

// NewSMTPClient builds an SMTP mail client for a configured connector, backed by the
// [submit] transport.
func NewSMTPClient(conn Connector) *SMTPClient {
	conn.ImplicitTLS = conn.ImplicitTLS || portOf(conn.Endpoint) == submissionsPort
	c := &SMTPClient{conn: conn}
	c.send = c.submit
	return c
}

// auth is the credential this connector presents, or nil when it is configured
// without a username (an internal relay that authenticates by network position). Send
// and Probe build it the same way, so what a check authenticates is what a send
// authenticates.
func (c *SMTPClient) auth() smtp.Auth {
	if c.conn.Username == "" {
		return nil
	}
	return smtp.PlainAuth("", c.conn.Username, c.conn.Password, hostOf(c.conn.Endpoint))
}

// Probe opens the session a send would open — connect, TLS, authenticate — and hangs
// up without a message (ADR-0150). It is what the connector form's check button
// calls: the failures it catches (a host that does not resolve, a port nothing
// listens on, a credential the server rejects) are exactly the ones that otherwise
// surface much later as an incident on a parked token.
func (c *SMTPClient) Probe(ctx context.Context) error {
	sess, err := dialSMTP(ctx, c.conn.Endpoint, c.conn.ImplicitTLS, c.auth())
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	defer func() { _ = sess.Close() }()
	if err := sess.Quit(); err != nil {
		return fmt.Errorf("mail: closing the session with %s: %w", c.conn.Endpoint, err)
	}
	return nil
}

// Send frames m as a UTF-8 MIME e-mail and submits it to the connector's SMTP
// endpoint. The sender is the message's From, or the connector's default From when
// the task authored none; a message with no sender and no default is a configuration
// error. Recipients are the union of To, Cc and Bcc (the SMTP envelope); Bcc
// addresses are delivered but never written into a header. A missing recipient or a
// send failure returns an error so the job stays pending and is retried (at-least-once).
func (c *SMTPClient) Send(ctx context.Context, m Message) error {
	from := strings.TrimSpace(m.From)
	if from == "" {
		from = strings.TrimSpace(c.conn.From)
	}
	if from == "" {
		return fmt.Errorf("mail: no sender configured (set the connector's From or the task's from)")
	}
	rcpts := recipients(m)
	if len(rcpts) == 0 {
		return fmt.Errorf("mail: message has no recipients")
	}
	if err := c.send(ctx, c.conn.Endpoint, c.auth(), from, rcpts, buildRFC822(m, from)); err != nil {
		return fmt.Errorf("mail: send via %s: %w", c.conn.Endpoint, err)
	}
	return nil
}

// recipients is the SMTP envelope: every To, Cc and Bcc address, in that order,
// already trimmed of empties by the worker.
func recipients(m Message) []string {
	out := make([]string, 0, len(m.To)+len(m.Cc)+len(m.Bcc))
	out = append(out, m.To...)
	out = append(out, m.Cc...)
	out = append(out, m.Bcc...)
	return out
}

// hostOf returns the host part of a "host:port" endpoint (SMTP PLAIN auth is scoped
// to the server host). SplitHostPort leads so a bracketed IPv6 endpoint yields the
// address itself rather than "[::1]"; an endpoint without a port is used verbatim.
func hostOf(endpoint string) string {
	if h, _, err := net.SplitHostPort(endpoint); err == nil {
		return h
	}
	if i := strings.LastIndex(endpoint, ":"); i >= 0 {
		return endpoint[:i]
	}
	return endpoint
}

// mimeBoundary derives the multipart boundary for a message. It is a pure function
// of the message — the deterministic Message-ID plus, if that is not enough, more
// padding until the boundary occurs in neither body. A boundary that appears inside
// a part would end it early and truncate the mail, so this is correctness, not
// hygiene; deriving it (rather than drawing a random one) keeps buildRFC822
// deterministic and testable without a clock or an RNG.
func mimeBoundary(m Message) string {
	base := "atlas-" + m.MessageID
	if m.MessageID == "" {
		base = "atlas-part"
	}
	b := base
	for i := 0; strings.Contains(m.Body, b) || strings.Contains(m.HTML, b); i++ {
		b = fmt.Sprintf("%s-%d", base, i)
	}
	return b
}

// buildRFC822 frames a message as an RFC 5322 / MIME e-mail with UTF-8 bodies. The
// subject is MIME word-encoded so non-ASCII survives; To and Cc are exposed as
// headers while Bcc is intentionally omitted (blind copy). No Date header is written
// — the submission server stamps it — so the framing is deterministic and
// unit-testable without a clock (invariant-friendly: this is a side effect, not
// replayed state). The Message-ID carries the deterministic idempotency key.
//
// The content type follows the bodies the author wrote: text/plain alone (the shape
// every message had before HTML bodies), text/html alone, or multipart/alternative
// with the plain text first and the HTML last — the order that lets a client pick
// the richest part it can render (RFC 2046 §5.1.4).
func buildRFC822(m Message, from string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	if len(m.To) > 0 {
		b.WriteString("To: " + strings.Join(m.To, ", ") + "\r\n")
	}
	if len(m.Cc) > 0 {
		b.WriteString("Cc: " + strings.Join(m.Cc, ", ") + "\r\n")
	}
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n")
	if m.MessageID != "" {
		b.WriteString("Message-ID: <" + m.MessageID + "@atlas>\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")

	switch {
	case m.HTML == "":
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		b.WriteString(m.Body)
	case m.Body == "":
		b.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
		b.WriteString(m.HTML)
	default:
		boundary := mimeBoundary(m)
		b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		b.WriteString(m.Body + "\r\n")
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
		b.WriteString(m.HTML + "\r\n")
		b.WriteString("--" + boundary + "--\r\n")
	}
	return []byte(b.String())
}
