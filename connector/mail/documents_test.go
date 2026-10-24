package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"net/smtp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
)

// rawWithPDF is a received message carrying a text body and one PDF attachment, the
// way a mail client sends it.
const rawWithPDF = "From: Anna <anna@example.com>\r\n" +
	"To: desk@example.com\r\n" +
	"Subject: =?utf-8?q?Rechnung_M=C3=A4rz?=\r\n" +
	"Date: Thu, 22 Oct 2026 09:00:00 +0200\r\n" +
	"Message-ID: <orig@example.com>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"b1\"\r\n" +
	"\r\n" +
	"--b1\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Anbei die Rechnung.\r\n" +
	"--b1\r\n" +
	"Content-Type: application/pdf; name=\"rechnung.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"rechnung.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0xLjcKJSVFT0Y=\r\n" +
	"--b1--\r\n"

// rawWithForwardedProgram is a message whose only attachment is another message —
// which carries a program.
const rawWithForwardedProgram = "From: x@example.net\r\n" +
	"Subject: fyi\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"o\"\r\n" +
	"\r\n" +
	"--o\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"see below\r\n" +
	"--o\r\n" +
	"Content-Type: message/rfc822\r\n" +
	"\r\n" +
	"From: y@example.net\r\n" +
	"Subject: inner\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"i\"\r\n" +
	"\r\n" +
	"--i\r\n" +
	"Content-Type: application/octet-stream\r\n" +
	"Content-Disposition: attachment; filename=\"setup.exe\"\r\n" +
	"\r\n" +
	"MZ program\r\n" +
	"--i--\r\n" +
	"\r\n" +
	"--o--\r\n"

func TestRawDocumentsFindsEveryAttachmentAndOpensAttachedMessages(t *testing.T) {
	docs, err := rawDocuments([]byte(rawWithPDF))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Name != "rechnung.pdf" || string(docs[0].Data) != "%PDF-1.7\n%%EOF" || docs[0].ID != "2" {
		t.Fatalf("documents = %+v", docs)
	}
	docs, err = rawDocuments([]byte(rawWithForwardedProgram))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range docs {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "message.eml,setup.exe" {
		t.Errorf("documents = %v; the program inside the attached mail must be found", names)
	}
	_, err = inspector{}.inspect(context.Background(), docs[1])
	var rej *Rejection
	if !errors.As(err, &rej) {
		t.Errorf("the program inside a forwarded mail passed inspection: %v", err)
	}
	if _, err := rawDocuments([]byte("not a message")); err == nil {
		t.Error("a message that cannot be read was read")
	}
}

func TestRawDocumentsRefusesBottomlessNesting(t *testing.T) {
	inner := "Content-Type: text/plain\r\n\r\nx\r\n"
	for i := 0; i < maxEntityDepth+2; i++ {
		b := fmt.Sprintf("n%d", i)
		inner = "Content-Type: multipart/mixed; boundary=\"" + b + "\"\r\n\r\n--" + b + "\r\n" + inner + "--" + b + "--\r\n"
	}
	if _, err := rawDocuments([]byte("From: a@b\r\n" + inner)); err == nil || !strings.Contains(err.Error(), "nests more than") {
		t.Errorf("a message nested past the limit = %v", err)
	}
}

func TestForwardMessageCarriesTheOriginalBodyEntityWhole(t *testing.T) {
	raw, err := forwardMessage([]byte(rawWithPDF), Forward{
		To: []string{"ablage@example.ch"}, Cc: []string{"chef@example.ch"}, Bcc: []string{"geheim@example.ch"},
		Body: "Zur Ablage.", MessageID: "4711",
	}, "desk@example.com")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the forward is not a message: %v\n%s", err, raw)
	}
	h := msg.Header
	subject, _ := new(mime.WordDecoder).DecodeHeader(h.Get("Subject"))
	if h.Get("From") != "desk@example.com" || h.Get("To") != "ablage@example.ch" || h.Get("Cc") != "chef@example.ch" ||
		subject != "Fwd: Rechnung März" || h.Get("Message-ID") != "<4711@atlas>" || h.Get("Bcc") != "" {
		t.Errorf("headers = %v (subject %q)", h, subject)
	}
	if !strings.Contains(h.Get("References"), "<orig@example.com>") {
		t.Errorf("References = %q, want the original's id", h.Get("References"))
	}
	_, params, _ := mime.ParseMediaType(h.Get("Content-Type"))
	mr := multipart.NewReader(msg.Body, params["boundary"])
	intro, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	text, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, intro))
	if !strings.HasPrefix(string(text), "Zur Ablage.") || !strings.Contains(string(text), "From: Anna <anna@example.com>") ||
		!strings.Contains(string(text), "Subject: Rechnung März") {
		t.Errorf("intro = %q", text)
	}
	orig, err := mr.NextRawPart()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(orig.Header.Get("Content-Type"), "multipart/mixed") {
		t.Errorf("the original entity's type = %q", orig.Header.Get("Content-Type"))
	}
	docs, err := rawDocuments(raw)
	if err != nil || len(docs) != 1 || docs[0].Name != "rechnung.pdf" {
		t.Errorf("the forwarded PDF is not an attachment of the forward: %+v, %v", docs, err)
	}

	html, err := forwardMessage([]byte(rawWithPDF), Forward{To: []string{"a@b.ch"}, HTML: "<p>Hallo</p>", MessageID: "1"}, "d@e.ch")
	if err != nil || !bytes.Contains(html, []byte("Content-Type: text/html; charset=utf-8")) {
		t.Errorf("an HTML note = %v\n%s", err, html)
	}
	already, _ := forwardMessage([]byte("Subject: FW: alt\r\n\r\nx"), Forward{To: []string{"a@b.ch"}}, "d@e.ch")
	if !bytes.Contains(already, []byte("Subject: FW: alt")) {
		t.Errorf("a subject already marked as a forward was marked again:\n%s", already)
	}
	if !bytes.Contains(already, []byte("Content-Type: text/plain; charset=us-ascii")) {
		t.Error("a message without a content type is text/plain in us-ascii (RFC 2045)")
	}
}

func TestSMTPSendsARawMessageAsItIs(t *testing.T) {
	c := NewSMTPClient(Connector{Endpoint: "smtp.example.com:587", From: "desk@example.com"})
	var gotMsg []byte
	var gotRcpts []string
	c.send = func(_ context.Context, _ string, _ smtp.Auth, _ string, to []string, msg []byte) error {
		gotMsg, gotRcpts = msg, to
		return nil
	}
	raw := []byte("Subject: framed elsewhere\r\n\r\nbody")
	if err := c.Send(context.Background(), Message{To: []string{"a@b.ch"}, Bcc: []string{"c@d.ch"}, Raw: raw}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotMsg, raw) || strings.Join(gotRcpts, ",") != "a@b.ch,c@d.ch" {
		t.Errorf("sent %q to %v", gotMsg, gotRcpts)
	}
}

func TestGraphReadsAnAttachmentAndForwardsWhatItInspected(t *testing.T) {
	pdf := []byte("%PDF-1.7\n%%EOF")
	g := newGraphFake(t, func(r graphReq) (int, any) {
		switch {
		case strings.HasSuffix(r.path, "/attachments/a1/$value"):
			return 200, pdf
		case strings.HasSuffix(r.path, "/attachments/a1"):
			return 200, map[string]any{"@odata.type": "#microsoft.graph.fileAttachment", "id": "a1", "name": "R.pdf", "contentType": "Application/PDF", "size": 400}
		case strings.HasSuffix(r.path, "/attachments/big"):
			return 200, map[string]any{"@odata.type": "#microsoft.graph.fileAttachment", "id": "big", "name": "huge.pdf", "size": limits.Default().Document + 1}
		case strings.HasSuffix(r.path, "/attachments/link"):
			return 200, map[string]any{"@odata.type": "#microsoft.graph.referenceAttachment", "id": "link", "name": "Budget"}
		case strings.HasSuffix(r.path, "/attachments/mail/$value"):
			return 200, []byte("From: a@b\r\n\r\nhi")
		case strings.HasSuffix(r.path, "/attachments/mail"):
			return 200, map[string]any{"@odata.type": "#microsoft.graph.itemAttachment", "id": "mail", "name": "Antwort"}
		case strings.HasSuffix(r.path, "/attachments"):
			return 200, map[string]any{"value": []any{
				map[string]any{"@odata.type": "#microsoft.graph.fileAttachment", "id": "a1", "name": "R.pdf", "contentType": "application/pdf", "size": 400},
				map[string]any{"@odata.type": "#microsoft.graph.referenceAttachment", "id": "link", "name": "Budget"},
			}}
		case strings.HasSuffix(r.path, "/forward"):
			return 202, nil
		}
		return 400, map[string]any{"error": "unexpected " + r.path}
	})
	mb := graphMailboxFor(t, g)
	ctx := context.Background()

	d, err := mb.Attachment(ctx, "m1", "a1")
	if err != nil || d.Name != "R.pdf" || d.ContentType != "application/pdf" || !bytes.Equal(d.Data, pdf) || d.ID != "a1" {
		t.Fatalf("attachment = %+v, %v", d, err)
	}
	for _, r := range g.requests() {
		if r.prefer == "" || !strings.Contains(r.prefer, "ImmutableId") {
			t.Errorf("%s %s asked without immutable ids", r.method, r.path)
		}
	}
	if _, err := mb.Attachment(ctx, "m1", "big"); err == nil || !strings.Contains(err.Error(), "ATLAS_LIMIT_DOCUMENT") {
		t.Errorf("an attachment past the document budget = %v", err)
	}
	if _, err := mb.Attachment(ctx, "m1", "link"); err == nil || !strings.Contains(err.Error(), "link to a file held elsewhere") {
		t.Errorf("a reference attachment = %v", err)
	}
	if d, err := mb.Attachment(ctx, "m1", "mail"); err != nil || d.Name != "Antwort.eml" || d.ContentType != "message/rfc822" {
		t.Errorf("an attached Outlook item = %+v, %v", d, err)
	}

	var inspected []string
	err = mb.Forward(ctx, "m1", Forward{
		To: []string{"ablage@example.ch"}, Bcc: []string{"b@example.ch"}, Body: "Zur Ablage",
		Inspect: func(d Document) error { inspected = append(inspected, d.Name); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(inspected, ",") != "R.pdf" {
		t.Errorf("inspected %v; every file must be read, a reference attachment never", inspected)
	}
	reqs := g.requests()
	last := reqs[len(reqs)-1]
	if !strings.HasSuffix(last.path, "/users/Desk@Example.com/messages/m1/forward") || last.body["comment"] != "Zur Ablage" {
		t.Errorf("forward request = %s %v", last.path, last.body)
	}
	msg, _ := last.body["message"].(map[string]any)
	if to := fmt.Sprint(msg["toRecipients"]); !strings.Contains(to, "ablage@example.ch") || !strings.Contains(fmt.Sprint(msg["bccRecipients"]), "b@example.ch") {
		t.Errorf("forward recipients = %v", msg)
	}
	if _, ok := msg["ccRecipients"]; ok {
		t.Error("an empty cc was sent as a field")
	}

	refused := errors.New("refused")
	before := len(g.requests())
	err = mb.Forward(ctx, "m1", Forward{To: []string{"a@b.ch"}, Inspect: func(Document) error { return refused }})
	if !errors.Is(err, refused) {
		t.Errorf("a refused document = %v", err)
	}
	for _, r := range g.requests()[before:] {
		if strings.HasSuffix(r.path, "/forward") {
			t.Error("a message whose attachment was refused was forwarded anyway")
		}
	}
	if err := mb.Forward(ctx, "m1", Forward{}); err == nil {
		t.Error("a forward without a recipient was sent")
	}
}

func TestGmailReadsAnAttachmentByItsPartID(t *testing.T) {
	g := newGmailFake(t)
	pdf := []byte("%PDF-1.7\n%%EOF")
	g.attachments = map[string][]byte{"ANGjdJ": pdf}
	g.messages["m1"] = map[string]any{
		"id": "m1", "threadId": "t1",
		"payload": map[string]any{
			"partId": "", "mimeType": "multipart/mixed",
			"parts": []any{
				map[string]any{"partId": "0", "mimeType": "text/plain", "body": map[string]any{"size": 5, "data": base64.URLEncoding.EncodeToString([]byte("Hallo"))}},
				map[string]any{"partId": "1", "mimeType": "application/pdf", "filename": "R.pdf",
					"headers": []any{map[string]any{"name": "Content-Disposition", "value": "attachment; filename=\"R.pdf\""}},
					"body":    map[string]any{"size": len(pdf), "attachmentId": "ANGjdJ"}},
				map[string]any{"partId": "2", "mimeType": "text/csv", "filename": "a.csv",
					"headers": []any{map[string]any{"name": "Content-Disposition", "value": "attachment"}},
					"body":    map[string]any{"size": 4, "data": base64.URLEncoding.EncodeToString([]byte("a;b\n"))}},
				map[string]any{"partId": "3", "mimeType": "application/pdf", "filename": "huge.pdf",
					"body": map[string]any{"size": limits.Default().Document + 1, "attachmentId": "x"}},
			},
		},
	}
	mb, _ := MailboxOf(NewGmailClient(staticToken("tok"), g.srv.URL, "desk@example.com"))
	ctx := context.Background()
	d, err := mb.Attachment(ctx, "m1", "1")
	if err != nil || d.Name != "R.pdf" || !bytes.Equal(d.Data, pdf) || d.ID != "1" || d.ContentType != "application/pdf" {
		t.Errorf("an attachment Gmail did not inline = %+v, %v", d, err)
	}
	if d, err := mb.Attachment(ctx, "m1", "2"); err != nil || string(d.Data) != "a;b\n" {
		t.Errorf("an inlined attachment = %+v, %v", d, err)
	}
	if _, err := mb.Attachment(ctx, "m1", "9"); err == nil || !strings.Contains(err.Error(), "no part") {
		t.Errorf("a part that does not exist = %v", err)
	}
	if _, err := mb.Attachment(ctx, "m1", "3"); err == nil || !strings.Contains(err.Error(), "ATLAS_LIMIT_DOCUMENT") {
		t.Errorf("an attachment past the document budget = %v", err)
	}
}

func TestGmailForwardsThroughTheMediaUpload(t *testing.T) {
	g := newGmailFake(t)
	g.raw = map[string][]byte{"m1": []byte(rawWithPDF)}
	mb, _ := MailboxOf(NewGmailClient(staticToken("tok"), g.srv.URL, "desk@example.com"))
	var inspected []string
	err := mb.Forward(context.Background(), "m1", Forward{
		To: []string{"ablage@example.ch"}, Bcc: []string{"b@example.ch"}, Body: "Bitte ablegen", MessageID: "9",
		Inspect: func(d Document) error { inspected = append(inspected, d.Name); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(inspected, ",") != "rechnung.pdf" {
		t.Errorf("inspected %v", inspected)
	}
	var upload *graphReq
	for _, r := range g.requests() {
		if r.path == "/upload/users/me/messages/send" {
			upload = &r
		}
	}
	if upload == nil {
		t.Fatalf("no media upload among %+v", g.requests())
	}
	if upload.query.Get("uploadType") != "media" || upload.contentType != "message/rfc822" {
		t.Errorf("upload = %s %s", upload.query, upload.contentType)
	}
	if !bytes.HasPrefix(upload.raw, []byte("Bcc: b@example.ch\r\n")) || !bytes.Contains(upload.raw, []byte("JVBERi0xLjcKJSVFT0Y=")) {
		t.Errorf("the uploaded forward lacks its Bcc or the original's attachment:\n%s", upload.raw)
	}

	g.raw["m2"] = []byte(rawWithForwardedProgram)
	refused := errors.New("refused")
	err = mb.Forward(context.Background(), "m2", Forward{To: []string{"a@b.ch"}, Inspect: func(d Document) error {
		if d.Name == "setup.exe" {
			return refused
		}
		return nil
	}})
	if !errors.Is(err, refused) {
		t.Errorf("a program inside the message = %v", err)
	}
}

func TestIMAPReadsAnAttachmentAndForwardsThroughSMTP(t *testing.T) {
	pdfB64 := "JVBERi0xLjcKJSVFT0Y="
	srv, clientTLS := newFakeIMAP(t)
	srv.add("INBOX", &fakeMsg{
		date:   "22-Oct-2026 09:00:00 +0200",
		header: "From: anna@example.com\r\nSubject: Rechnung\r\n\r\n",
		bs: `(("TEXT" "PLAIN" ("CHARSET" "utf-8") NIL NIL "7BIT" 5 1 NIL NIL NIL)` +
			`("APPLICATION" "PDF" ("NAME" "R.pdf") NIL NIL "BASE64" 20 NIL ("ATTACHMENT" ("FILENAME" "R.pdf")) NIL) "MIXED" ("BOUNDARY" "b1") NIL NIL)`,
		sections: map[string]string{"1": "Hallo", "2": pdfB64, "": rawWithPDF},
	})
	var sent []Message
	mb := srv.mailbox(t, clientTLS, func(_ context.Context, m Message) error { sent = append(sent, m); return nil })
	ctx := context.Background()
	id := imapMessageID(7, 1, "INBOX")

	envs, err := mb.List(ctx, ListRequest{})
	if err != nil || len(envs) != 1 || len(envs[0].Attachments) != 1 || envs[0].Attachments[0].ID != "2" {
		t.Fatalf("list = %+v, %v", envs, err)
	}
	d, err := mb.Attachment(ctx, id, "2")
	if err != nil || string(d.Data) != "%PDF-1.7\n%%EOF" || d.Name != "R.pdf" || d.ContentType != "application/pdf" {
		t.Fatalf("attachment = %+v, %v", d, err)
	}
	for _, bad := range []string{"", "2]", "1 BODY[]"} {
		if _, err := mb.Attachment(ctx, id, bad); err == nil {
			t.Errorf("section %q was fetched", bad)
		}
	}
	if _, err := mb.Attachment(ctx, id, "7"); err == nil || !strings.Contains(err.Error(), "no body section") {
		t.Errorf("a section the message does not have = %v", err)
	}

	var inspected []string
	err = mb.Forward(ctx, id, Forward{To: []string{"ablage@example.ch"}, Body: "ablegen", MessageID: "5",
		Inspect: func(d Document) error { inspected = append(inspected, d.Name); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Raw == nil || sent[0].To[0] != "ablage@example.ch" || sent[0].From != srv.user {
		t.Fatalf("sent = %+v", sent)
	}
	if !bytes.Contains(sent[0].Raw, []byte(pdfB64)) || strings.Join(inspected, ",") != "rechnung.pdf" {
		t.Errorf("forward lacks the attachment, or did not inspect it (%v)", inspected)
	}
	for _, c := range srv.commands {
		if strings.Contains(c, "SELECT ") || strings.Contains(c, "STORE") {
			t.Errorf("reading an attachment or forwarding changed the mailbox: %s", c)
		}
	}
}
