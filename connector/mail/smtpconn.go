package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// submissionsPort is the implicit-TLS submission port (RFC 8314 "submissions"): a
// server listening there expects the TLS handshake as the very first thing on the
// connection, before any greeting, where 587 and 25 greet in the clear and are
// upgraded afterwards with STARTTLS. The two are not interchangeable — speaking plain
// SMTP at 465 leaves both sides waiting for the other, which is a hang and not an
// error — so the port selects how the connection is opened.
const submissionsPort = "465"

// dialSMTP opens a session to addr and takes it as far as a message can be sent
// through it: connect (with TLS from the first byte on the submissions port),
// upgrade with STARTTLS wherever the server offers it, and authenticate when a
// credential is configured. It is the shared front half of sending a message and of
// checking a connector — which is what makes the check meaningful, since a check that
// exercised a different path could only tell you about that path.
//
// A credential is never offered over an unencrypted link: AUTH follows the upgrade,
// and against a server that offers no encryption at all net/smtp refuses PLAIN
// outright (outside localhost) rather than putting a password on the wire. That
// refusal is a useful answer, not an obstacle — but "unencrypted connection" says
// nothing about what to do, so it is translated below.
func dialSMTP(ctx context.Context, addr string, implicitTLS bool, auth smtp.Auth) (*smtp.Client, error) {
	// Every outbound connector call is bounded by the shared budget (ADR-0149): the
	// mail worker runs on the run-loop goroutine, so a submission host that accepts
	// the connection and then stops answering would hold the engine's single writer.
	// A caller's own deadline can make that shorter but never longer, which is what
	// keeps the bound a property of the connector rather than of its callers.
	deadline := time.Now().Add(nettimeout.Default)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel() // bounds the dial; the session below is bounded by the connection deadline

	host := hostOf(addr)
	dialer := &net.Dialer{}

	var (
		conn net.Conn
		err  error
	)
	if implicitTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: host}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	// net/smtp predates context: one absolute deadline on the connection bounds the
	// whole exchange, so no later phase can hang past the budget either.
	_ = conn.SetDeadline(deadline)

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("no SMTP greeting from %s: %w", addr, err)
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("STARTTLS with %s: %w", host, err)
		}
	}
	if auth != nil {
		// A server that does not offer AUTH would take the credential as an unknown
		// command; saying so names the mismatch instead of reporting a syntax error.
		if ok, _ := c.Extension("AUTH"); !ok {
			_ = c.Close()
			return nil, fmt.Errorf("%s offers no AUTH, but this connector has a username configured", addr)
		}
		if err := c.Auth(auth); err != nil {
			_ = c.Close()
			if strings.Contains(err.Error(), "unencrypted connection") {
				return nil, fmt.Errorf("%s offers no STARTTLS, so the password cannot be sent (an unencrypted link would expose it) — use the submission port 587 or the implicit-TLS port 465", addr)
			}
			return nil, fmt.Errorf("authenticate with %s: %w", host, err)
		}
	}
	return c, nil
}

// submit is the SMTP transport behind [SMTPClient.Send]: it opens a session through
// dialSMTP and walks the envelope (MAIL FROM, RCPT TO, DATA). It replaced
// net/smtp.SendMail, which cannot reach a submissions-port server at all, and each
// step names itself so a rejection ("relay denied", "mailbox unavailable") points at
// the address it was about rather than at the send as a whole. It is a method so the
// connector's transport settings reach it while its signature stays [sendFunc] — the
// seam a test substitutes.
func (c *SMTPClient) submit(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return sendMailCtx(ctx, addr, c.conn.ImplicitTLS, auth, from, to, msg)
}

// sendMailWithin runs the transport with the budget injected and no implicit TLS, so
// a test can drive the hang paths in milliseconds instead of waiting out the real
// budget (ADR-0149's tests reach the transport through here).
func sendMailWithin(budget time.Duration, addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	return sendMailCtx(ctx, addr, false, a, from, to, msg)
}

// sendMailCtx walks the envelope over a session from dialSMTP. Address validation is
// net/smtp's own: Client.Mail and Client.Rcpt each call validateLine, so an address
// carrying CR or LF is refused before it can inject an SMTP command.
func sendMailCtx(ctx context.Context, addr string, implicitTLS bool, auth smtp.Auth, from string, to []string, msg []byte) error {
	sess, err := dialSMTP(ctx, addr, implicitTLS, auth)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	if err := sess.Mail(from); err != nil {
		return fmt.Errorf("sender %s refused: %w", from, err)
	}
	for _, rcpt := range to {
		if err := sess.Rcpt(rcpt); err != nil {
			return fmt.Errorf("recipient %s refused: %w", rcpt, err)
		}
	}
	w, err := sess.Data()
	if err != nil {
		return fmt.Errorf("start message: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	// The server's accept/reject verdict arrives on close, so this error is the one
	// that says whether the message was taken.
	if err := w.Close(); err != nil {
		return fmt.Errorf("message refused: %w", err)
	}
	return sess.Quit()
}

// portOf returns the port of a "host:port" endpoint, or "" when it has none.
func portOf(endpoint string) string {
	if _, p, err := net.SplitHostPort(endpoint); err == nil {
		return p
	}
	if i := strings.LastIndex(endpoint, ":"); i >= 0 {
		return endpoint[i+1:]
	}
	return ""
}
