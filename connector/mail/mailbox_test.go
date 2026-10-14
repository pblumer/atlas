package mail

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSenderAllowed(t *testing.T) {
	cases := []struct {
		from    string
		allowed []string
		want    bool
	}{
		{"anyone@example.com", nil, true},
		{"Anna@Example.com", []string{"anna@example.com"}, true},
		{"bob@example.com", []string{"anna@example.com"}, false},
		{"bob@example.com", []string{"@example.com"}, true},
		{"bob@example.com", []string{"example.com"}, true},
		// A domain entry reaches that domain only — not a subdomain, and not a domain
		// that merely ends in the same letters.
		{"bob@mail.example.com", []string{"@example.com"}, false},
		{"bob@evil-example.com", []string{"@example.com"}, false},
		{"not-an-address", []string{"@example.com"}, false},
		{"trailing@", []string{"@example.com"}, false},
		{"bob@example.com", []string{"", "  "}, false},
	}
	for _, c := range cases {
		if got := SenderAllowed(c.from, c.allowed); got != c.want {
			t.Errorf("SenderAllowed(%q, %q) = %v, want %v", c.from, c.allowed, got, c.want)
		}
	}
}

func TestNormalizeAllowedSenders(t *testing.T) {
	got, err := NormalizeAllowedSenders([]string{" Anna@Example.com ", "", "example.org", "@Example.net"})
	if err != nil {
		t.Fatalf("NormalizeAllowedSenders: %v", err)
	}
	want := []string{"anna@example.com", "@example.org", "@example.net"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("normalized = %q, want %q", got, want)
	}
	for _, bad := range []string{"anna", "a@b@c.com", "a b@c.com", "Anna <a@b.com>", "@.com", "x@com", "a@b.com."} {
		if _, err := NormalizeAllowedSenders([]string{bad}); err == nil {
			t.Errorf("NormalizeAllowedSenders(%q) accepted an entry that is neither an address nor a domain", bad)
		}
	}
}

func TestOperationClasses(t *testing.T) {
	for _, op := range Operations() {
		reads, changes := ReadsMailbox(op), ChangesMailbox(op)
		if reads && changes {
			t.Errorf("%s is both a read and a change", op)
		}
		if op == OpSend && (reads || changes) {
			t.Errorf("send must be neither a read nor a change: who may use a sender is not what the deploy check governs")
		}
		if op != OpSend && !reads && !changes {
			t.Errorf("%s is unclassified, so the deploy check would let anyone use it", op)
		}
	}
}

func TestEnvelopeFields(t *testing.T) {
	e := Envelope{
		ID: "id-1", InternetMessageID: "<m@x>", Folder: "INBOX",
		From: "anna@example.com", FromName: "Anna", ReplyTo: "desk@example.com",
		To: []string{"me@example.com"}, Subject: "Hallo",
		ReceivedAt:  time.Date(2026, 10, 2, 8, 30, 0, 0, time.FixedZone("CEST", 2*3600)),
		Unread:      true,
		Attachments: []Attachment{{Name: "a.pdf", ContentType: "application/pdf", Size: 12}},
		Auth:        AuthResults{SPF: "pass", DKIM: "pass", DMARC: "pass"},
		Body:        "secret text", BodyTruncated: true,
	}
	f := e.Fields(false)
	if _, ok := f["body"]; ok {
		t.Fatal("a body nobody asked for reached the fields")
	}
	if f["receivedAt"] != "2026-10-02T06:30:00Z" {
		t.Errorf("receivedAt = %v, want UTC RFC 3339", f["receivedAt"])
	}
	if f["hasAttachments"] != true {
		t.Errorf("hasAttachments = %v", f["hasAttachments"])
	}
	atts := f["attachments"].([]any)
	if len(atts) != 1 || atts[0].(map[string]any)["name"] != "a.pdf" {
		t.Errorf("attachments = %v", atts)
	}
	if f["auth"].(map[string]any)["dmarc"] != "pass" {
		t.Errorf("auth = %v", f["auth"])
	}
	if cc := f["cc"].([]any); len(cc) != 0 {
		t.Errorf("an absent cc must be an empty list, not null: %v", cc)
	}
	f = e.Fields(true)
	if f["body"] != "secret text" || f["bodyTruncated"] != true {
		t.Errorf("body fields = %v / %v", f["body"], f["bodyTruncated"])
	}
	if (Envelope{}).Fields(false)["receivedAt"] != "" {
		t.Error("a missing receive time must render empty, not as year one")
	}
}

type sendOnly struct{}

func (sendOnly) Send(context.Context, Message) error { return nil }

func TestMailboxOf(t *testing.T) {
	if _, err := MailboxOf(nil); err == nil {
		t.Error("a nil client has no mailbox")
	}
	if _, err := MailboxOf(sendOnly{}); err == nil || !strings.Contains(err.Error(), "no mailbox") {
		t.Errorf("a send-only client must say it has no mailbox, got %v", err)
	}
	preview := NewPreviewClient(NewOutbox(0), "p", "a@b.c")
	if _, err := MailboxOf(preview); err == nil {
		t.Error("the preview provider has no mailbox to read")
	}
	smtp := NewSMTPClient(Connector{Endpoint: "smtp.example.com:587"})
	if _, err := MailboxOf(smtp); err == nil || !strings.Contains(err.Error(), "IMAP") {
		t.Errorf("an SMTP worker without an IMAP endpoint must name what is missing, got %v", err)
	}
}

func TestProviderClientsCarryTheirMailbox(t *testing.T) {
	smtp, err := NewProviderClient(ProviderConfig{Provider: ProviderSMTP, Endpoint: "smtp.example.com",
		Sender: "me@example.com", Secret: "pw", Mailbox: "imap.example.com"})
	if err != nil {
		t.Fatalf("smtp: %v", err)
	}
	mb, err := MailboxOf(smtp)
	if err != nil {
		t.Fatalf("an SMTP worker with an IMAP endpoint has a mailbox: %v", err)
	}
	if im := mb.(*imapMailbox); im.ep.addr != "imap.example.com:993" || !im.ep.implicitTLS || im.user != "me@example.com" {
		t.Errorf("imap mailbox = %+v", im.ep)
	}

	// A broken IMAP endpoint does not take sending down with it.
	broken, err := NewProviderClient(ProviderConfig{Provider: ProviderSMTP, Endpoint: "smtp.example.com",
		Sender: "me@example.com", Mailbox: "pop3://nope"})
	if err != nil {
		t.Fatalf("a broken IMAP endpoint refused the sender: %v", err)
	}
	if _, err := MailboxOf(broken); err == nil || !strings.Contains(err.Error(), "pop3") {
		t.Errorf("the broken endpoint must answer its error when the mailbox is asked for, got %v", err)
	}

	sa := `{"method":"serviceAccount","clientEmail":"sa@p.iam.gserviceaccount.com","privateKey":` +
		jsonString(t, testRSAKeyPEM(t)) + `}`
	gm, err := NewProviderClient(ProviderConfig{Provider: ProviderGmail, Sender: "me@example.com", Secret: sa})
	if err != nil {
		t.Fatalf("gmail: %v", err)
	}
	g := gm.(*GmailClient)
	if g.readTokens == nil || g.modifyTokens == nil {
		t.Error("a Gmail worker reads and changes its mailbox with tokens of their own scope")
	}
	explicit := `{"method":"serviceAccount","scope":"https://mail.google.com/","clientEmail":"sa@p.iam.gserviceaccount.com","privateKey":` +
		jsonString(t, testRSAKeyPEM(t)) + `}`
	gm, err = NewProviderClient(ProviderConfig{Provider: ProviderGmail, Sender: "me@example.com", Secret: explicit})
	if err != nil {
		t.Fatalf("gmail explicit: %v", err)
	}
	if g := gm.(*GmailClient); g.readTokens != nil || g.modifyTokens != nil {
		t.Error("an operator's explicit scope must not be second-guessed")
	}
	if _, err := NewProviderClient(ProviderConfig{Provider: ProviderGmail, Secret: `{"method":"serviceAccount"}`}); err == nil {
		t.Error("an incomplete bundle built a Gmail client")
	}
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
