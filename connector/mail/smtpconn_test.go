package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a submission server with just enough of the protocol for a real
// net/smtp client to greet, authenticate, and hand over a message. It exists so the
// transport and the connector check are exercised against something that answers in
// SMTP rather than against a stubbed function — the failures worth catching here
// (a rejected credential, a refused recipient) are answers, not call arguments.
type fakeSMTP struct {
	addr   string
	authOK bool
	rcptOK bool

	mu      sync.Mutex
	sawAuth bool
	from    string
	rcpts   []string
	data    string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{addr: ln.Addr().String(), authOK: true, rcptOK: true}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			// No STARTTLS: the client is on localhost, where net/smtp allows PLAIN
			// on a clear connection, so the test needs no certificate machinery.
			_, _ = conn.Write([]byte("250-fake greets you\r\n250 AUTH PLAIN\r\n"))
		case strings.HasPrefix(cmd, "HELO"):
			say("250 fake greets you")
		case strings.HasPrefix(cmd, "AUTH"):
			f.mu.Lock()
			f.sawAuth = true
			f.mu.Unlock()
			if f.authOK {
				say("235 accepted")
			} else {
				say("535 5.7.8 authentication failed")
			}
		case strings.HasPrefix(cmd, "MAIL FROM"):
			f.mu.Lock()
			f.from = addrOf(line)
			f.mu.Unlock()
			say("250 sender ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			if !f.rcptOK {
				say("550 5.1.1 mailbox unavailable")
				continue
			}
			f.mu.Lock()
			f.rcpts = append(f.rcpts, addrOf(line))
			f.mu.Unlock()
			say("250 recipient ok")
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(l) == "." {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

// addrOf pulls the address out of "MAIL FROM:<a@b>" / "RCPT TO:<a@b>".
func addrOf(line string) string {
	if i, j := strings.Index(line, "<"), strings.LastIndex(line, ">"); i >= 0 && j > i {
		return line[i+1 : j]
	}
	return strings.TrimSpace(line)
}

func (f *fakeSMTP) seen() (bool, string, []string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sawAuth, f.from, append([]string(nil), f.rcpts...), f.data
}

// TestSMTPSendOverTheWire drives a real submission session end to end: the transport
// replaced net/smtp.SendMail, so the envelope it walks has to be exercised against a
// server rather than against the seam the other tests substitute.
func TestSMTPSendOverTheWire(t *testing.T) {
	srv := newFakeSMTP(t)
	c := NewSMTPClient(Connector{Endpoint: srv.addr, Username: "bot@example.com", Password: "pw", From: "bot@example.com"})

	err := c.Send(context.Background(), Message{
		To:      []string{"a@example.com"},
		Bcc:     []string{"quiet@example.com"},
		Subject: "Hallo",
		Body:    "Text",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	sawAuth, from, rcpts, data := srv.seen()
	switch {
	case !sawAuth:
		t.Error("the server saw no AUTH, want the configured credential presented")
	case from != "bot@example.com":
		t.Errorf("MAIL FROM = %q, want the connector's sender", from)
	case strings.Join(rcpts, ",") != "a@example.com,quiet@example.com":
		t.Errorf("envelope = %v, want To + Bcc", rcpts)
	case !strings.Contains(data, "Subject: Hallo"):
		t.Errorf("delivered message lost its subject:\n%s", data)
	case strings.Contains(data, "quiet@example.com"):
		t.Errorf("the Bcc address reached a header:\n%s", data)
	}
}

// TestSMTPProbeChecksWithoutSending is the connector check's whole premise: it walks
// the same path a send walks, and stops before the message.
func TestSMTPProbeChecksWithoutSending(t *testing.T) {
	srv := newFakeSMTP(t)
	c := NewSMTPClient(Connector{Endpoint: srv.addr, Username: "bot@example.com", Password: "pw", From: "bot@example.com"})

	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	sawAuth, from, rcpts, _ := srv.seen()
	if !sawAuth {
		t.Error("the check did not authenticate — then it proves nothing about the credential")
	}
	if from != "" || len(rcpts) != 0 {
		t.Errorf("the check started a message (from=%q rcpts=%v), want it to stop at the door", from, rcpts)
	}
}

func TestSMTPProbeReportsARejectedCredential(t *testing.T) {
	srv := newFakeSMTP(t)
	srv.authOK = false
	c := NewSMTPClient(Connector{Endpoint: srv.addr, Username: "bot@example.com", Password: "wrong", From: "bot@example.com"})

	err := c.Probe(context.Background())
	if err == nil {
		t.Fatal("Probe succeeded against a server that refused the credential")
	}
	if !strings.Contains(err.Error(), "authenticate") {
		t.Errorf("error = %q, want it to name the authentication step", err)
	}
}

func TestSMTPProbeReportsAnUnreachableServer(t *testing.T) {
	// A port nobody listens on: bind one, learn its address, and hand it back.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c := NewSMTPClient(Connector{Endpoint: addr, From: "bot@example.com"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = c.Probe(ctx)
	if err == nil {
		t.Fatal("Probe succeeded against a closed port")
	}
	if !strings.Contains(err.Error(), "connect to") {
		t.Errorf("error = %q, want it to name the connection attempt", err)
	}
}

func TestSMTPSendNamesARefusedRecipient(t *testing.T) {
	srv := newFakeSMTP(t)
	srv.rcptOK = false
	c := NewSMTPClient(Connector{Endpoint: srv.addr, From: "bot@example.com"})

	err := c.Send(context.Background(), Message{To: []string{"nobody@example.com"}, Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("Send succeeded although the recipient was refused")
	}
	if !strings.Contains(err.Error(), "nobody@example.com") {
		t.Errorf("error = %q, want it to name the refused address", err)
	}
}

// TestImplicitTLSOpensWithAHandshake guards the reason the transport exists: a
// submissions-port connector must speak TLS from the first byte, where net/smtp's
// SendMail would have sent a plaintext greeting and waited forever. The fake listener
// only reads what arrives first — a TLS record header (0x16, then the version) is
// proof the branch was taken, with no certificate machinery in the test.
func TestImplicitTLSOpensWithAHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	first := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 3)
		n, _ := conn.Read(buf)
		first <- buf[:n]
	}()

	c := NewSMTPClient(Connector{Endpoint: ln.Addr().String(), ImplicitTLS: true, From: "bot@example.com"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.Probe(ctx) // fails: the listener is not a TLS server. What it *said* is the point.

	select {
	case got := <-first:
		if len(got) < 2 || got[0] != 0x16 || got[1] != 0x03 {
			t.Errorf("first bytes = % x, want a TLS handshake record (16 03 …)", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was sent — the implicit-TLS connection never opened")
	}
}

// TestImplicitTLSFollowsTheSubmissionsPort: an operator writes "smtps://host", which
// normalizes to :465, and the transport has to notice that on its own.
func TestImplicitTLSFollowsTheSubmissionsPort(t *testing.T) {
	endpoint, err := NormalizeSMTPEndpoint("smtps://mail.example.com")
	if err != nil {
		t.Fatalf("NormalizeSMTPEndpoint: %v", err)
	}
	if c := NewSMTPClient(Connector{Endpoint: endpoint}); !c.conn.ImplicitTLS {
		t.Errorf("endpoint %q did not select implicit TLS", endpoint)
	}
	if c := NewSMTPClient(Connector{Endpoint: "mail.example.com:587"}); c.conn.ImplicitTLS {
		t.Error("the submission port 587 selected implicit TLS, want STARTTLS")
	}
}

func TestPortOf(t *testing.T) {
	for in, want := range map[string]string{
		"mail.example.com:587": "587",
		"[::1]:465":            "465",
		"mail.example.com":     "",
		"":                     "",
	} {
		if got := portOf(in); got != want {
			t.Errorf("portOf(%q) = %q, want %q", in, got, want)
		}
	}
}
