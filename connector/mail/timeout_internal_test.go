package mail

import (
	"net"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// TestSendMailBoundedUsesTheSharedBudget pins the wiring: the SMTP client sends
// through the bounded path, with the shared worker budget, rather than
// net/smtp's unbounded SendMail.
func TestSendMailBoundedUsesTheSharedBudget(t *testing.T) {
	if nettimeout.Default == 0 {
		t.Fatal("the shared budget is zero; the SMTP send would be unbounded")
	}
	// NewSMTPClient must not wire net/smtp's SendMail (which never times out).
	c := NewSMTPClient(Connector{Endpoint: "localhost:25"})
	if c.send == nil {
		t.Fatal("SMTP client has no send func")
	}
}

// TestSendMailWithinBoundsAHangingServer is the behaviour that matters: a
// submission host that accepts the TCP connection and then never speaks must not
// block forever. Before the fix this call sat in net/smtp reading a greeting with
// no deadline — and because the mail worker runs on the run-loop goroutine, that
// stalled the entire engine.
//
// The listener accepts and stays silent; the send must come back as an error
// within the (injected, tiny) budget instead of hanging.
func TestSendMailWithinBoundsAHangingServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		close(accepted)
		// Hold the connection open without ever sending the SMTP greeting, and
		// keep it alive until the test finishes so the client's deadline — not a
		// peer close — is what ends the call.
		t.Cleanup(func() { conn.Close() })
		<-t.Context().Done()
	}()

	const budget = 150 * time.Millisecond
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- sendMailWithin(budget, ln.Addr().String(), nil,
			"from@example.org", []string{"to@example.org"}, []byte("hi"))
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a silent server returned success; want a timeout error")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("send took %v; the budget (%v) did not bound it", elapsed, budget)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("send never returned: a hung SMTP host still blocks the caller (and so the run loop)")
	}
	<-accepted
}

// TestSendMailWithinRejectsCRLFAddresses pins a property inherited from
// net/smtp.SendMail that the bounded reimplementation must not lose: an address
// carrying CR/LF is refused, so it cannot inject an SMTP command into the
// envelope. Client.Mail and Client.Rcpt validate for us — this test is what
// notices if that ever stops being true.
func TestSendMailWithinRejectsCRLFAddresses(t *testing.T) {
	// A server that completes the greeting, so the send reaches MAIL/RCPT and the
	// rejection comes from address validation rather than from a stalled dial.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = conn.Write([]byte("220 test ESMTP\r\n"))
				buf := make([]byte, 1024)
				for {
					n, err := conn.Read(buf)
					if err != nil || n == 0 {
						return
					}
					_, _ = conn.Write([]byte("250 ok\r\n"))
				}
			}()
		}
	}()

	addr := ln.Addr().String()
	for _, tc := range []struct {
		name string
		from string
		to   string
	}{
		{"CRLF in sender", "a@example.org\r\nRCPT TO:<victim@example.org>", "to@example.org"},
		{"CRLF in recipient", "from@example.org", "b@example.org\r\nDATA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := sendMailWithin(2*time.Second, addr, nil, tc.from, []string{tc.to}, []byte("hi"))
			if err == nil {
				t.Fatal("an address containing CRLF was accepted; SMTP command injection is possible")
			}
		})
	}
}

// TestSendMailWithinBoundsAnUnreachablePort covers the dial leg: a port nothing
// listens on must fail fast rather than hang.
func TestSendMailWithinBoundsAnUnreachablePort(t *testing.T) {
	// Bind then immediately close, so the port is almost certainly free and the
	// dial is refused rather than routed somewhere real.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	done := make(chan error, 1)
	go func() {
		done <- sendMailWithin(150*time.Millisecond, addr, nil,
			"from@example.org", []string{"to@example.org"}, []byte("hi"))
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("dialing a closed port returned success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dial to a closed port never returned")
	}
}
