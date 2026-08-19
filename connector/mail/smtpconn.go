package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// submissionsPort is the implicit-TLS submission port (RFC 8314 "submissions"): a
// server listening there expects the TLS handshake as the very first thing on the
// connection, before any greeting, where 587 and 25 greet in the clear and are
// upgraded afterwards with STARTTLS. The two are not interchangeable — speaking plain
// SMTP at 465 leaves both sides waiting for the other, which is a hang and not an
// error — so the port selects how the connection is opened.
const submissionsPort = "465"

// defaultSMTPTimeout bounds a connection whose caller set no deadline of its own. A
// submission server that has not greeted, authenticated, and accepted a message
// within this is not going to.
const defaultSMTPTimeout = 30 * time.Second

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
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultSMTPTimeout)
		defer cancel()
	}
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
	// net/smtp predates context: the deadline is carried onto the connection so a
	// server that stops answering mid-session fails instead of hanging.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

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
	sess, err := dialSMTP(ctx, addr, c.conn.ImplicitTLS, auth)
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
