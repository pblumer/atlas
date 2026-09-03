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

// implicitTLSPort is the IANA-registered SMTPS port. A server on 465 expects a TLS
// handshake immediately, with no plaintext greeting and no STARTTLS (RFC 8314).
const implicitTLSPort = "465"

// usesImplicitTLS reports whether addr should be reached with implicit TLS (SMTPS)
// rather than a plaintext connection upgraded by STARTTLS.
//
// The decision is made by port, because the worker configuration carries no TLS
// mode: an operator supplies only "host:port". Port 465 is unambiguous — it is
// registered for exactly this and serves nothing else — so keying off it makes the
// common submission endpoints work with no extra configuration. Isolating the choice
// here also means an explicit per-connector override, should one be added, changes
// only this function's caller.
//
// Getting this wrong hangs rather than fails cleanly: a plaintext client waits for a
// greeting the TLS server will never send while the server waits for a ClientHello,
// until the deadline expires.
func usesImplicitTLS(addr string) bool {
	_, port, err := net.SplitHostPort(addr)
	return err == nil && port == implicitTLSPort
}

// dialSMTP opens a session to addr and takes it as far as a message can be sent
// through it: connect (with TLS from the first byte on the submissions port),
// upgrade with STARTTLS wherever the server offers it, and authenticate when a
// credential is configured. It is the shared front half of sending a message and of
// checking a worker — which is what makes the check meaningful, since a check that
// exercised a different path could only tell you about that path.
//
// A credential is never offered over an unencrypted link: AUTH follows the upgrade,
// and against a server that offers no encryption at all net/smtp refuses PLAIN
// outright (outside localhost) rather than putting a password on the wire. That
// refusal is a useful answer, not an obstacle — but "unencrypted connection" says
// nothing about what to do, so it is translated below.
func dialSMTP(ctx context.Context, addr string, implicitTLS bool, tlsCfg *tls.Config, auth smtp.Auth) (*smtp.Client, error) {
	// Every outbound worker call is bounded by the shared budget (ADR-0149): the
	// mail worker runs on the run-loop goroutine, so a submission host that accepts
	// the connection and then stops answering would hold the engine's single writer.
	// A caller's own deadline can make that shorter but never longer, which is what
	// keeps the bound a property of the worker rather than of its callers.
	deadline := time.Now().Add(nettimeout.Default)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel() // bounds the dial; the session below is bounded by the connection deadline

	host := hostOf(addr)
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: host}
	}
	dialer := &net.Dialer{}

	var (
		conn net.Conn
		err  error
	)
	if implicitTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
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
	// STARTTLS only upgrades a plaintext connection; on an implicit-TLS connection the
	// session is already encrypted and the server offers no such extension.
	if !implicitTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				_ = c.Close()
				return nil, fmt.Errorf("STARTTLS with %s: %w", host, err)
			}
		}
	}
	if auth != nil {
		// A server that does not offer AUTH would take the credential as an unknown
		// command; saying so names the mismatch instead of reporting a syntax error.
		if ok, _ := c.Extension("AUTH"); !ok {
			_ = c.Close()
			return nil, fmt.Errorf("%s offers no AUTH, but this worker has a username configured", addr)
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
// worker's transport settings reach it while its signature stays [sendFunc] — the
// seam a test substitutes.
func (c *SMTPClient) submit(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return sendMailCtx(ctx, addr, c.conn.ImplicitTLS, nil, auth, from, to, msg)
}

// sendMailWithin runs the transport with the budget injected, choosing the transport
// from the endpoint's port, so a test can drive the hang paths in milliseconds
// instead of waiting out the real budget (ADR-0149).
func sendMailWithin(budget time.Duration, addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	return sendMailOver(budget, addr, usesImplicitTLS(addr), nil, a, from, to, msg)
}

// sendMailOver is the send with the transport decision and TLS settings injected, so
// a test can exercise the implicit-TLS path against a throwaway certificate on an
// ordinary port (465 is privileged and unavailable to tests).
func sendMailOver(budget time.Duration, addr string, implicitTLS bool, tlsCfg *tls.Config, a smtp.Auth, from string, to []string, msg []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	return sendMailCtx(ctx, addr, implicitTLS, tlsCfg, a, from, to, msg)
}

// sendMailCtx walks the envelope over a session from dialSMTP. Address validation is
// net/smtp's own: Client.Mail and Client.Rcpt each call validateLine, so an address
// carrying CR or LF is refused before it can inject an SMTP command.
func sendMailCtx(ctx context.Context, addr string, implicitTLS bool, tlsCfg *tls.Config, auth smtp.Auth, from string, to []string, msg []byte) error {
	sess, err := dialSMTP(ctx, addr, implicitTLS, tlsCfg, auth)
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
