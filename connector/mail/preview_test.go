package mail

import (
	"context"
	"net/smtp"
	"strings"
	"testing"
)

func previewMessage() Message {
	return Message{
		To:        []string{"her@example.com"},
		Cc:        []string{"cc@example.com"},
		Bcc:       []string{"quiet@example.com"},
		Subject:   "Bestellung versandt",
		Body:      "Hallo,\nIhre Bestellung ist unterwegs.",
		HTML:      "<p>Ihre Bestellung ist <b>unterwegs</b>.</p>",
		MessageID: "job-42",
	}
}

func TestPreviewClientDeliversToOutbox(t *testing.T) {
	box := NewOutbox(0)
	c := NewPreviewClient(box, "preview", "bot@example.com")

	if err := c.Send(context.Background(), previewMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msgs, truncated := box.Messages(0)
	if len(msgs) != 1 || truncated {
		t.Fatalf("outbox = %d messages (truncated=%v), want 1", len(msgs), truncated)
	}
	m := msgs[0]
	switch {
	case m.Connector != "preview":
		t.Errorf("worker = %q, want %q", m.Connector, "preview")
	case m.From != "bot@example.com":
		t.Errorf("from = %q, want the worker's default sender", m.From)
	case m.Subject != "Bestellung versandt":
		t.Errorf("subject = %q", m.Subject)
	case m.Seq != 1 || m.At == 0:
		t.Errorf("seq/at = %d/%d, want a stamped message", m.Seq, m.At)
	}
	// Bcc is delivered but never written into a header — the outbox shows the
	// address (it is what the envelope carried) while the source does not.
	if len(m.Bcc) != 1 || strings.Contains(m.Raw, "quiet@example.com") {
		t.Errorf("bcc leaked into the framed message:\n%s", m.Raw)
	}
	if !strings.Contains(m.Raw, "Message-ID: <job-42@atlas>") {
		t.Errorf("framed message lost its Message-ID:\n%s", m.Raw)
	}
}

// TestPreviewFramesExactlyWhatSMTPWouldSend is the claim the preview provider rests
// on: it is a rehearsal, not a mock. What an author reads in the outbox has to be
// byte-identical to what the SMTP provider would put on the wire — otherwise a
// preview run proves nothing about the message that is later sent for real.
func TestPreviewFramesExactlyWhatSMTPWouldSend(t *testing.T) {
	var sent []byte
	sc := NewSMTPClient(Connector{Endpoint: "mail.example.com:587", From: "bot@example.com"})
	sc.send = func(_ context.Context, _ string, _ smtp.Auth, _ string, _ []string, msg []byte) error {
		sent = msg
		return nil
	}
	if err := sc.Send(context.Background(), previewMessage()); err != nil {
		t.Fatalf("smtp Send: %v", err)
	}

	box := NewOutbox(0)
	if err := NewPreviewClient(box, "preview", "bot@example.com").Send(context.Background(), previewMessage()); err != nil {
		t.Fatalf("preview Send: %v", err)
	}
	msgs, _ := box.Messages(0)
	if got := msgs[0].Raw; got != string(sent) {
		t.Errorf("preview framing differs from SMTP framing:\npreview:\n%s\nsmtp:\n%s", got, sent)
	}
}

// TestPreviewRefusesWhatARealProviderRefuses: the same two authoring mistakes have to
// fail here, or a message would preview cleanly and then fail on the first real send.
func TestPreviewRefusesWhatARealProviderRefuses(t *testing.T) {
	box := NewOutbox(0)

	noSender := NewPreviewClient(box, "preview", "")
	if err := noSender.Send(context.Background(), Message{To: []string{"a@x"}, Subject: "s"}); err == nil {
		t.Error("a message with no sender previewed successfully, want an error")
	}
	noRcpt := NewPreviewClient(box, "preview", "bot@example.com")
	if err := noRcpt.Send(context.Background(), Message{Subject: "s"}); err == nil {
		t.Error("a message with no recipients previewed successfully, want an error")
	}
	if n := box.Len(); n != 0 {
		t.Errorf("outbox holds %d refused message(s), want 0", n)
	}
}

func TestPreviewClientWithoutOutboxFails(t *testing.T) {
	if err := NewPreviewClient(nil, "preview", "bot@x").Send(context.Background(), previewMessage()); err == nil {
		t.Error("want an error when the worker has no outbox")
	}
}

// TestOutboxIsBoundedAndNewestFirst: the outbox is memory, so a process that sends in
// a loop must not grow it without limit, and a reader wants the newest first.
func TestOutboxIsBoundedAndNewestFirst(t *testing.T) {
	box := NewOutbox(3)
	c := NewPreviewClient(box, "preview", "bot@example.com")
	for _, subject := range []string{"one", "two", "three", "four", "five"} {
		if err := c.Send(context.Background(), Message{To: []string{"a@x"}, Subject: subject}); err != nil {
			t.Fatalf("Send %q: %v", subject, err)
		}
	}
	msgs, truncated := box.Messages(0)
	if len(msgs) != 3 || !truncated {
		t.Fatalf("outbox = %d messages (truncated=%v), want 3 and truncated", len(msgs), truncated)
	}
	for i, want := range []string{"five", "four", "three"} {
		if msgs[i].Subject != want {
			t.Errorf("message %d = %q, want %q (newest first)", i, msgs[i].Subject, want)
		}
	}
	// Sequence numbers keep counting across a drop, so a reader never sees one reused.
	if msgs[0].Seq != 5 {
		t.Errorf("newest seq = %d, want 5", msgs[0].Seq)
	}
	if got, truncated := box.Messages(2); len(got) != 2 || !truncated || got[0].Subject != "five" {
		t.Errorf("Messages(2) = %d messages (truncated=%v), want the newest 2", len(got), truncated)
	}

	box.Clear()
	if n := box.Len(); n != 0 {
		t.Errorf("after Clear the outbox holds %d messages, want 0", n)
	}
	if err := c.Send(context.Background(), Message{To: []string{"a@x"}, Subject: "after"}); err != nil {
		t.Fatalf("Send after Clear: %v", err)
	}
	msgs, _ = box.Messages(0)
	if len(msgs) != 1 || msgs[0].Seq != 6 {
		t.Errorf("after Clear: seq = %d (%d messages), want the counter to keep running", msgs[0].Seq, len(msgs))
	}
}

// TestOutboxClipsHugeBodies: a stored body is bounded, and the truncation says so
// rather than silently presenting a shortened message as what would be sent.
func TestOutboxClipsHugeBodies(t *testing.T) {
	box := NewOutbox(0)
	huge := strings.Repeat("x", maxOutboxField+1000)
	if err := NewPreviewClient(box, "preview", "bot@x").Send(context.Background(), Message{To: []string{"a@x"}, Body: huge}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msgs, _ := box.Messages(0)
	if n := len(msgs[0].Body); n > maxOutboxField+64 {
		t.Errorf("stored body = %d bytes, want it clipped near %d", n, maxOutboxField)
	}
	if !strings.Contains(msgs[0].Body, "truncated") {
		t.Error("a clipped body does not say it was clipped")
	}
}

// TestPreviewProviderBuildsThroughTheDispatch keeps the provider reachable the way a
// server builds it — by name, through the one dispatch every provider goes through.
func TestPreviewProviderBuildsThroughTheDispatch(t *testing.T) {
	box := NewOutbox(0)
	c, err := NewProviderClient(ProviderConfig{Provider: ProviderPreview, Sender: "bot@x", Name: "trial", Outbox: box})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if _, ok := c.(*PreviewClient); !ok {
		t.Fatalf("preview provider = %T, want *PreviewClient", c)
	}
	if _, err := NewProviderClient(ProviderConfig{Provider: ProviderPreview, Sender: "bot@x", Name: "trial"}); err == nil {
		t.Error("a preview worker with no outbox built successfully, want an error")
	}
}

// TestSMTPProviderNormalizesItsEndpoint: a record stored before the endpoint was ever
// checked (or edited around the check) still has to produce a client that dials.
func TestSMTPProviderNormalizesItsEndpoint(t *testing.T) {
	c, err := NewProviderClient(ProviderConfig{Provider: ProviderSMTP, Endpoint: "mail.example.com", Sender: "bot@x"})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	sc, ok := c.(*SMTPClient)
	if !ok {
		t.Fatalf("smtp provider = %T, want *SMTPClient", c)
	}
	if sc.conn.Endpoint != "mail.example.com:587" {
		t.Errorf("endpoint = %q, want the submission port filled in", sc.conn.Endpoint)
	}
	if _, err := NewProviderClient(ProviderConfig{Provider: ProviderSMTP, Endpoint: "", Sender: "bot@x"}); err == nil {
		t.Error("an SMTP worker with no endpoint built successfully, want an error")
	}
}
