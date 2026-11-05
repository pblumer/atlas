package mail

import (
	"context"
	"strings"
	"testing"
)

// TestPreviewWithholdsWhatItMustNotKeep: a message that carries a password link
// (ADR-0526) reaches the outbox without it — in the subject,
// both bodies and the framed raw message — and says so where it stood.
func TestPreviewWithholdsWhatItMustNotKeep(t *testing.T) {
	const link = "https://atlas.example.ch/public/password#dXNy.1999999999.00112233445566778899aabbccddeeff"
	out := NewOutbox(0)
	c := NewPreviewClient(out, "mail", "atlas@example.ch")
	err := c.Send(context.Background(), Message{
		To:       []string{"anna@example.org"},
		Subject:  "Link: " + link,
		Body:     "Hier: " + link + "\nGruss",
		HTML:     `<a href="` + link + `">setzen</a>`,
		Withhold: []string{link},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	msgs, _ := out.Messages(0)
	if len(msgs) != 1 {
		t.Fatalf("outbox holds %d messages", len(msgs))
	}
	m := msgs[0]
	for name, field := range map[string]string{"subject": m.Subject, "body": m.Body, "html": m.HTML, "raw": m.Raw} {
		if strings.Contains(field, "dXNy.1999999999") {
			t.Errorf("the %s kept the link: %q", name, field)
		}
	}
	if !strings.Contains(m.Body, WithheldText) {
		t.Errorf("the body does not say what was withheld: %q", m.Body)
	}
}

// TestPreviewWithholdsALinkInPieces: a model that cuts the link apart, so that no
// search for the link finds it, still leaves nothing of it in the outbox — the subject
// and both bodies of a message that carried a link are withheld whole.
func TestPreviewWithholdsALinkInPieces(t *testing.T) {
	const link = "https://atlas.example.ch/public/password#dXNy.1999999999.00112233445566778899aabbccddeeff"
	out := NewOutbox(0)
	c := NewPreviewClient(out, "mail", "atlas@example.ch")
	split := link[:30] + "|" + link[30:]
	if err := c.Send(context.Background(), Message{
		To:       []string{"anna@example.org"},
		Subject:  "Teil: " + link[40:],
		Body:     "Hier: " + split,
		HTML:     "<p>" + split + "</p>",
		Withhold: []string{link},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	msgs, _ := out.Messages(0)
	if len(msgs) != 1 {
		t.Fatalf("outbox holds %d messages", len(msgs))
	}
	m := msgs[0]
	for name, field := range map[string]string{"subject": m.Subject, "body": m.Body, "html": m.HTML, "raw": m.Raw} {
		if strings.Contains(field, "dXNy") || strings.Contains(field, "00112233") {
			t.Errorf("the %s kept a piece of the link: %q", name, field)
		}
	}
	if len(m.To) != 1 || m.To[0] != "anna@example.org" {
		t.Errorf("the envelope was not kept: %v", m.To)
	}
}
