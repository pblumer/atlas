package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// The SMTP session's refusals that the fixed fakeSMTP cannot provoke: a server that
// offers no AUTH, one that advertises STARTTLS and then cannot do it, one that takes
// a message and refuses it at the end, and one that will not say goodbye.

// scriptedSMTP answers EHLO with ehlo (the lines after the greeting line), refuses
// STARTTLS, and gives dataVerdict after a message body and quitReply to QUIT.
type scriptedSMTP struct {
	ehlo        []string
	dataVerdict string
	quitReply   string
}

func (s scriptedSMTP) start(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return ln.Addr().String()
}

func (s scriptedSMTP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	say := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }

	say("220 scripted ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			lines := append([]string{"scripted greets you"}, s.ehlo...)
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				say("250" + sep + l)
			}
		case cmd == "STARTTLS":
			say("454 4.7.0 TLS not available due to temporary reason")
		case strings.HasPrefix(cmd, "AUTH"):
			say("235 accepted")
		case cmd == "DATA":
			say("354 go ahead")
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(l) == "." {
					break
				}
			}
			say(s.dataVerdict)
		case cmd == "QUIT":
			say(s.quitReply)
			return
		default:
			say("250 ok")
		}
	}
}

func TestSMTPNamesAServerThatOffersNoAuth(t *testing.T) {
	addr := scriptedSMTP{quitReply: "221 bye"}.start(t)
	c := NewSMTPClient(Connector{Endpoint: addr, Username: "bot@example.com", Password: "pw", From: "bot@example.com"})
	err := c.Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "offers no AUTH, but this worker has a username configured") {
		t.Fatalf("Probe = %v, want the missing AUTH named rather than a syntax error", err)
	}
}

func TestSMTPNamesAFailedStartTLS(t *testing.T) {
	addr := scriptedSMTP{ehlo: []string{"STARTTLS"}, quitReply: "221 bye"}.start(t)
	c := NewSMTPClient(Connector{Endpoint: addr, From: "bot@example.com"})
	err := c.Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "STARTTLS with 127.0.0.1") {
		t.Fatalf("Probe = %v, want the failed upgrade named", err)
	}
}

// TestSMTPReportsAMessageRefusedAtTheEnd: the server's verdict on a message arrives
// when the body is closed, so a refusal there must come back as the send's failure.
func TestSMTPReportsAMessageRefusedAtTheEnd(t *testing.T) {
	addr := scriptedSMTP{dataVerdict: "554 5.7.1 rejected as spam", quitReply: "221 bye"}.start(t)
	c := NewSMTPClient(Connector{Endpoint: addr, From: "bot@example.com"})
	err := c.Send(context.Background(), Message{To: []string{"ada@example.com"}, Subject: "s", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "message refused") || !strings.Contains(err.Error(), "rejected as spam") {
		t.Fatalf("Send = %v, want the end-of-data refusal", err)
	}
}

// TestSMTPProbeReportsAnUncleanGoodbye: a check that connected and authenticated but
// could not close the session has not shown the server works.
func TestSMTPProbeReportsAnUncleanGoodbye(t *testing.T) {
	addr := scriptedSMTP{quitReply: "500 5.5.1 not now"}.start(t)
	c := NewSMTPClient(Connector{Endpoint: addr, From: "bot@example.com"})
	err := c.Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "closing the session with "+addr) {
		t.Fatalf("Probe = %v, want the failed QUIT named", err)
	}
}
