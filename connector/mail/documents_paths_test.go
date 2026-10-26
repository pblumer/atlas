package mail

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// The edges of reading documents out of a raw message, framing a forward from one, and
// judging what the bytes are (ADR-0478).

func TestRawDocumentsReadsEachEncodingAndRefusesWhatDoesNotDecode(t *testing.T) {
	raw := "From: a@example.com\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"m\"\r\n\r\n" +
		"--m\r\n" +
		"Content-Type: text/csv\r\n" +
		"Content-Disposition: attachment\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"a;b=3Bc\r\n" +
		"--m\r\n" +
		"Content-Type: message/rfc822\r\n\r\n" +
		"no header block at all" +
		"\r\n--m\r\n" +
		"Content-Type: ;;;\r\n\r\n" +
		"a body whose type does not parse is text\r\n" +
		"--m--\r\n"
	docs, err := rawDocuments([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Name != "attachment" || string(docs[0].Data) != "a;b;c" || docs[1].Name != "message.eml" {
		t.Errorf("documents = %+v; an unnamed attachment is \"attachment\", quoted-printable is undone, "+
			"and an attached message nobody can parse is still a document to inspect", docs)
	}

	badBase64 := "From: a@example.com\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n!!!not base64!!!\r\n"
	if _, err := rawDocuments([]byte(badBase64)); err == nil || !strings.Contains(err.Error(), "base64 does not decode") {
		t.Errorf("an attachment whose base64 does not decode = %v; handing on other bytes than the sender's is not an option", err)
	}

	cut := "From: a@example.com\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nunterminated"
	if _, err := rawDocuments([]byte(cut)); err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("a multipart cut short = %v", err)
	}
}

func TestForwardMessageEdges(t *testing.T) {
	if _, err := forwardMessage([]byte("not a message"), Forward{To: []string{"a@b.ch"}}, "d@e.ch"); err == nil {
		t.Error("a message that cannot be read was forwarded")
	}
	// A body that already carries the boundary the forward would pick forces another,
	// and a single-part original keeps its own transfer encoding and disposition.
	orig := "Subject: scan\r\nContent-Type: application/pdf\r\nContent-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: attachment; filename=\"s.pdf\"\r\n\r\nJVBERi0xLjcKJSVFT0Y=\r\n--atlas-fwd-7\r\n"
	out, err := forwardMessage([]byte(orig), Forward{To: []string{"a@b.ch"}, MessageID: "7"}, "d@e.ch")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`boundary="atlas-fwd-7-0"`)) {
		t.Errorf("the forward kept a boundary its own body contains:\n%s", out)
	}
	for _, h := range []string{"Content-Transfer-Encoding: base64", `Content-Disposition: attachment; filename="s.pdf"`} {
		if !bytes.Contains(out, []byte(h)) {
			t.Errorf("the original entity lost %q", h)
		}
	}
	if bytes.Contains(out, []byte("References:")) {
		t.Error("an original with neither Message-ID nor References gave the forward a References header")
	}
}

func TestInspectionKnowsTheFormatsItsRulesNameByContent(t *testing.T) {
	ctx := context.Background()
	legacy := append(append([]byte{}, oleMagic...), utf16le("Workbook")...)
	if v, err := (inspector{}).inspect(ctx, Document{Name: "budget.xls", Data: legacy}); err != nil || v.DetectedType != "application/x-ole-storage" {
		t.Errorf("a legacy workbook without macros = %+v, %v", v, err)
	}
	for name, data := range map[string][]byte{
		"brief.doc": []byte(`{\rtf1\ansi Sehr geehrte Damen und Herren}`),
		"leer.txt":  nil,
	} {
		var rej *Rejection
		if _, err := (inspector{}).inspect(ctx, Document{Name: name, Data: data}); !errors.As(err, &rej) {
			t.Errorf("%s = %v, want it refused as not what its name says", name, err)
		}
	}
	for name, want := range map[string]string{
		"tabelle.xlsx":  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"folien.pptx":   "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"leer.bin":      "application/octet-stream",
		"defekt.xyzzy":  "application/zip",
		"kurz.unknown?": "text/plain",
	} {
		var data []byte
		switch name {
		case "tabelle.xlsx":
			data = zipOf(t, false, "xl/workbook.xml")
		case "folien.pptx":
			data = zipOf(t, false, "ppt/presentation.xml")
		case "defekt.xyzzy":
			data = []byte("PK\x03\x04 a zip whose directory is missing")
		case "kurz.unknown?":
			data = []byte("hi")
		}
		if got := sniffType(data); got != want {
			t.Errorf("%s sniffed as %q, want %q", name, got, want)
		}
	}
	if !isExecutable([]byte{0xCA, 0xFE, 0xBA, 0xBE, 0, 0, 0, 2}) {
		t.Error("a fat Mach-O binary is a program")
	}
	for data, want := range map[string]string{
		"%PDF-1.4 << /S /#4A#53 (x) >>":    "runs JavaScript", // upper-case escapes spell /JS
		"%PDF-1.4 << /A#zzB /Launch#2 >>":  "",                // not an escape, and no action named
		"%PDF-1.4 << /Type /Catalog >> /J": "",
	} {
		if got := pdfRefusal([]byte(data)); !strings.Contains(got, want) || (want == "" && got != "") {
			t.Errorf("pdfRefusal(%q) = %q, want %q", data, got, want)
		}
	}
}

// clamdAnswering serves one connection per answer: it reads the command and replies
// with the answer verbatim — or closes without a word when the answer is empty.
func clamdAnswering(t *testing.T, answers ...string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for _, a := range answers {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 64)
			_, _ = c.Read(buf)
			if a != "" {
				_, _ = c.Write([]byte(a + "\x00"))
			}
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestClamdAnswersThatAreNotAVerdict(t *testing.T) {
	ctx := context.Background()
	sc, _ := NewScanner("clamd://" + clamdAnswering(t, "PANG", "", "stream: OK"))
	if sc.Name() != "clamav" {
		t.Errorf("name = %q", sc.Name())
	}
	if err := sc.Ping(ctx); err == nil || !strings.Contains(err.Error(), "to PING") {
		t.Errorf("a wrong answer to PING = %v", err)
	}
	if err := sc.Ping(ctx); err == nil || !strings.Contains(err.Error(), "without an answer") {
		t.Errorf("a clamd that hangs up = %v", err)
	}
}

func TestProviderDocumentReadsFailWithTheProvidersReason(t *testing.T) {
	ctx := context.Background()
	g := newGraphFake(t, func(r graphReq) (int, any) {
		switch {
		case strings.HasSuffix(r.path, "/attachments/a1/$value"):
			return 404, []byte("gone")
		case strings.HasSuffix(r.path, "/attachments/a1"):
			return 200, map[string]any{"id": "a1", "name": "R.pdf", "size": 10}
		case strings.HasSuffix(r.path, "/attachments/nope"):
			return 404, map[string]any{"error": "no such attachment"}
		case strings.HasSuffix(r.path, "/attachments"):
			return 500, map[string]any{"error": "down"}
		}
		return 400, nil
	})
	mb := graphMailboxFor(t, g)
	if _, err := mb.Attachment(ctx, "m1", "a1"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("content the provider refuses = %v", err)
	}
	if _, err := mb.Attachment(ctx, "m1", "nope"); err == nil {
		t.Error("an attachment Graph does not know was read")
	}
	if err := mb.Forward(ctx, "m1", Forward{To: []string{"a@b.ch"}, Inspect: func(Document) error { return nil }}); err == nil {
		t.Error("a forward whose attachments cannot be listed went out uninspected")
	}

	gm := newGmailFake(t)
	gm.raw = map[string][]byte{"bad": []byte("x")}
	gm.messages["bad"] = map[string]any{"id": "bad"}
	noSender, _ := MailboxOf(NewGmailClient(staticToken("tok"), gm.srv.URL, ""))
	if err := noSender.Forward(ctx, "bad", Forward{To: []string{"a@b.ch"}}); err == nil || !strings.Contains(err.Error(), "no sender") {
		t.Errorf("a Gmail forward with no sender = %v", err)
	}
	gmb, _ := MailboxOf(NewGmailClient(staticToken("tok"), gm.srv.URL, "desk@example.com"))
	if err := gmb.Forward(ctx, "missing", Forward{To: []string{"a@b.ch"}}); err == nil {
		t.Error("a Gmail forward of a message that does not exist went out")
	}
	if err := gmb.Forward(ctx, "bad", Forward{To: []string{"a@b.ch"}}); err == nil {
		t.Error("a raw message that is not a message was forwarded")
	}
	if err := gmb.Forward(ctx, "bad", Forward{}); err == nil {
		t.Error("a Gmail forward without a recipient went out")
	}
	if data, err := decodeBase64URL("aGk"); err != nil || string(data) != "hi" {
		t.Errorf("unpadded base64url = %q, %v", data, err)
	}
	if got := (gmailMailbox{c: &GmailClient{baseURL: "not a url"}}).uploadBase(); got != "not a url" {
		t.Errorf("an unparseable base = %q, want it left as it is", got)
	}

	srv, clientTLS := newFakeIMAP(t)
	noSend := srv.mailbox(t, clientTLS, nil)
	if err := noSend.Forward(ctx, imapMessageID(7, 1, "INBOX"), Forward{To: []string{"a@b.ch"}}); err == nil || !strings.Contains(err.Error(), "cannot send") {
		t.Errorf("an IMAP forward on a worker that cannot send = %v", err)
	}
	withSend := srv.mailbox(t, clientTLS, func(context.Context, Message) error { return nil })
	if err := withSend.Forward(ctx, imapMessageID(7, 99, "INBOX"), Forward{To: []string{"a@b.ch"}}); err == nil {
		t.Error("a forward of a message the folder does not hold went out")
	}
	if err := withSend.Forward(ctx, "x", Forward{}); err == nil {
		t.Error("an IMAP forward without a recipient went out")
	}
}
