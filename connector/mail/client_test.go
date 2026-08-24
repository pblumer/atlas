package mail

import (
	"context"
	"errors"
	"net/smtp"
	"strings"
	"testing"
)

// capture records the arguments net/smtp's SendMail would have received, so the SMTP
// framing and auth are exercised without a live server.
type capture struct {
	addr string
	auth smtp.Auth
	from string
	to   []string
	msg  []byte
}

// withCapture swaps the client's send seam for one that records into c.
func withCapture(client *SMTPClient, c *capture) {
	client.send = func(_ context.Context, addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		c.addr, c.auth, c.from, c.to, c.msg = addr, a, from, to, msg
		return nil
	}
}

func TestSMTPClientFramesAndSends(t *testing.T) {
	client := NewSMTPClient(Connector{Endpoint: "smtp.example.com:587", Username: "bot@example.com", Password: "pw", From: "bot@example.com"})
	var got capture
	withCapture(client, &got)

	err := client.Send(context.Background(), Message{
		To:        []string{"a@example.com", "b@example.com"},
		Cc:        []string{"c@example.com"},
		Bcc:       []string{"secret@example.com"},
		Subject:   "Hëllo",
		Body:      "Line one.\nLine two.",
		MessageID: "12345",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.addr != "smtp.example.com:587" {
		t.Errorf("addr = %q, want the endpoint", got.addr)
	}
	if got.from != "bot@example.com" {
		t.Errorf("from = %q, want the connector default sender", got.from)
	}
	// The envelope is To + Cc + Bcc; a Bcc address is delivered but never headered.
	if strings.Join(got.to, ",") != "a@example.com,b@example.com,c@example.com,secret@example.com" {
		t.Errorf("envelope recipients = %v, want To+Cc+Bcc", got.to)
	}
	if got.auth == nil {
		t.Error("auth = nil, want PLAIN auth when a username is configured")
	}
	msg := string(got.msg)
	for _, want := range []string{
		"From: bot@example.com\r\n",
		"To: a@example.com, b@example.com\r\n",
		"Cc: c@example.com\r\n",
		"Message-ID: <12345@atlas>\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Line one.\nLine two.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q\n---\n%s", want, msg)
		}
	}
	// Bcc must not appear as a header.
	if strings.Contains(msg, "secret@example.com") {
		t.Errorf("Bcc address leaked into headers:\n%s", msg)
	}
	// A non-ASCII subject is MIME word-encoded, not sent raw.
	if strings.Contains(msg, "Subject: Hëllo") {
		t.Errorf("subject sent raw, want MIME-encoded:\n%s", msg)
	}
	if !strings.Contains(msg, "Subject: =?utf-8?q?") {
		t.Errorf("subject not MIME-encoded:\n%s", msg)
	}
}

// A task-authored From overrides the connector's default sender.
func TestSMTPClientFromOverride(t *testing.T) {
	client := NewSMTPClient(Connector{Endpoint: "smtp.example.com:587", From: "default@example.com"})
	var got capture
	withCapture(client, &got)
	if err := client.Send(context.Background(), Message{From: "custom@example.com", To: []string{"a@b.c"}, Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.from != "custom@example.com" {
		t.Errorf("from = %q, want the task override", got.from)
	}
	// No username configured → no auth.
	if got.auth != nil {
		t.Error("auth != nil, want no auth when no username is configured")
	}
}

func TestSMTPClientErrors(t *testing.T) {
	t.Run("no sender", func(t *testing.T) {
		client := NewSMTPClient(Connector{Endpoint: "smtp.example.com:587"})
		if err := client.Send(context.Background(), Message{To: []string{"a@b.c"}}); err == nil {
			t.Fatal("want an error when no sender is configured, got nil")
		}
	})
	t.Run("no recipients", func(t *testing.T) {
		client := NewSMTPClient(Connector{Endpoint: "smtp.example.com:587", From: "bot@example.com"})
		if err := client.Send(context.Background(), Message{Subject: "s"}); err == nil {
			t.Fatal("want an error when there are no recipients, got nil")
		}
	})
	t.Run("send failure propagates", func(t *testing.T) {
		client := NewSMTPClient(Connector{Endpoint: "smtp.example.com:587", From: "bot@example.com"})
		client.send = func(context.Context, string, smtp.Auth, string, []string, []byte) error { return errors.New("boom") }
		if err := client.Send(context.Background(), Message{To: []string{"a@b.c"}}); err == nil {
			t.Fatal("want the send error propagated, got nil")
		}
	})
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"smtp.example.com:587": "smtp.example.com",
		"smtp.example.com":     "smtp.example.com",
		"":                     "",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.Client("x"); ok {
		t.Error("empty registry resolved a client, want miss")
	}
	a := NewSMTPClient(Connector{})
	reg.Register("x", a)
	if c, ok := reg.Client("x"); !ok || c != a {
		t.Error("Register/Client did not round-trip")
	}
	// Replace swaps the whole set.
	b := NewSMTPClient(Connector{})
	reg.Replace(map[string]Client{"y": b})
	if _, ok := reg.Client("x"); ok {
		t.Error("Replace did not drop the old binding")
	}
	if c, ok := reg.Client("y"); !ok || c != b {
		t.Error("Replace did not install the new binding")
	}
	// A nil map clears the registry.
	reg.Replace(nil)
	if _, ok := reg.Client("y"); ok {
		t.Error("Replace(nil) did not clear the registry")
	}
}
