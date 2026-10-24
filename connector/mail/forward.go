package mail

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"strings"
)

// Forwarding a message a provider hands over raw (IMAP, Gmail), and finding the
// documents inside one (ADR-0478).
//
// Graph forwards server-side; IMAP and Gmail cannot, so the forward is framed here. It
// is the original message's own body entity — every part and attachment exactly as
// the sender's client encoded them — placed after the text the model wrote, inside one
// multipart/mixed. Re-encoding the parts would be lossier and longer; attaching the
// whole message as message/rfc822 would hide the attachments one level down, where a
// receiving system that files attachments (a document store's mail-in address, a
// ticket system) does not look. Placed like this, they arrive as attachments, which
// is what Graph's forward delivers too.

// forwardMessage frames a forward of raw to f's recipients, sent as from.
func forwardMessage(raw []byte, f Forward, from string) ([]byte, error) {
	orig, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("mail: the message to forward cannot be read: %w", err)
	}
	body, err := io.ReadAll(orig.Body)
	if err != nil {
		return nil, fmt.Errorf("mail: the message to forward cannot be read: %w", err)
	}
	h := orig.Header
	subject := decodeHeader(h.Get("Subject"))
	if l := strings.ToLower(strings.TrimSpace(subject)); !strings.HasPrefix(l, "fwd:") && !strings.HasPrefix(l, "fw:") {
		subject = "Fwd: " + subject
	}
	quoted := [][2]string{
		{"From", decodeHeader(h.Get("From"))},
		{"Date", h.Get("Date")},
		{"Subject", decodeHeader(h.Get("Subject"))},
		{"To", decodeHeader(h.Get("To"))},
		{"Cc", decodeHeader(h.Get("Cc"))},
	}
	intro, introType := forwardIntro(f, quoted)

	boundary := "atlas-fwd-" + f.MessageID
	for i := 0; bytes.Contains(body, []byte(boundary)) || strings.Contains(intro, boundary); i++ {
		boundary = fmt.Sprintf("atlas-fwd-%s-%d", f.MessageID, i)
	}

	var b bytes.Buffer
	b.WriteString("From: " + headerSafe(from) + "\r\n")
	b.WriteString("To: " + headerSafe(strings.Join(f.To, ", ")) + "\r\n")
	if len(f.Cc) > 0 {
		b.WriteString("Cc: " + headerSafe(strings.Join(f.Cc, ", ")) + "\r\n")
	}
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	if f.MessageID != "" {
		b.WriteString("Message-ID: <" + f.MessageID + "@atlas>\r\n")
	}
	if refs := headerSafe(strings.TrimSpace(h.Get("References") + " " + h.Get("Message-ID"))); refs != "" {
		b.WriteString("References: " + refs + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: " + introType + "; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrapBase64([]byte(intro)))

	b.WriteString("--" + boundary + "\r\n")
	ct := h.Get("Content-Type")
	if strings.TrimSpace(ct) == "" {
		ct = "text/plain; charset=us-ascii" // RFC 2045 §5.2: what a message without one is
	}
	b.WriteString("Content-Type: " + headerSafe(ct) + "\r\n")
	for _, name := range []string{"Content-Transfer-Encoding", "Content-Disposition"} {
		if v := headerSafe(h.Get(name)); v != "" {
			b.WriteString(name + ": " + v + "\r\n")
		}
	}
	b.WriteString("\r\n")
	b.Write(body)
	if !bytes.HasSuffix(body, []byte("\n")) {
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes(), nil
}

// forwardIntro is the part above the forwarded message: the model's text, then the
// original's sender, date, subject and recipients, the way every mail client marks a
// forward. HTML when the model wrote HTML, text otherwise.
func forwardIntro(f Forward, quoted [][2]string) (string, string) {
	if strings.TrimSpace(f.HTML) != "" {
		var b strings.Builder
		b.WriteString(f.HTML)
		b.WriteString("<br><br><div>---------- Forwarded message ----------<br>")
		for _, q := range quoted {
			if strings.TrimSpace(q[1]) != "" {
				b.WriteString(q[0] + ": " + html.EscapeString(q[1]) + "<br>")
			}
		}
		b.WriteString("</div>")
		return b.String(), "text/html"
	}
	var b strings.Builder
	b.WriteString(f.Body)
	b.WriteString("\r\n\r\n---------- Forwarded message ----------\r\n")
	for _, q := range quoted {
		if strings.TrimSpace(q[1]) != "" {
			b.WriteString(q[0] + ": " + q[1] + "\r\n")
		}
	}
	return b.String(), "text/plain"
}

// wrapBase64 encodes data as base64 in lines of 76, the length RFC 2045 sets.
func wrapBase64(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return b.String()
}

// maxEntityDepth bounds how deep documents walks into multiparts and attached
// messages. Real mail nests a handful of levels; a message built to nest thousands is
// built to exhaust whoever reads it.
const maxEntityDepth = 16

// rawDocuments finds every document in a raw message: each leaf part that is an
// attachment by the rule the envelope lists them by, and each attached message —
// both as a document of its own and opened, because a program inside a forwarded
// mail is still a program.
func rawDocuments(raw []byte) ([]Document, error) {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("mail: the message cannot be read: %w", err)
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, fmt.Errorf("mail: the message cannot be read: %w", err)
	}
	var out []Document
	err = walkEntity(textproto.MIMEHeader(msg.Header), body, "", 0, &out)
	return out, err
}

func walkEntity(h textproto.MIMEHeader, body []byte, section string, depth int, out *[]Document) error {
	if depth > maxEntityDepth {
		return fmt.Errorf("mail: the message nests more than %d levels deep", maxEntityDepth)
	}
	mt, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || mt == "" {
		mt, params = "text/plain", map[string]string{}
	}
	typ, sub, _ := strings.Cut(mt, "/")
	if typ == "multipart" {
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for n := 1; ; n++ {
			// NextRawPart, because NextPart undoes quoted-printable on its own and
			// base64 not at all; decoding both here keeps one rule.
			p, err := mr.NextRawPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return fmt.Errorf("mail: a part of the message cannot be read: %w", err)
			}
			child, err := io.ReadAll(p)
			if err != nil {
				return fmt.Errorf("mail: a part of the message cannot be read: %w", err)
			}
			if err := walkEntity(p.Header, child, joinSection(section, n), depth+1, out); err != nil {
				return err
			}
		}
	}
	part := mimePart{Type: typ, Subtype: sub, Params: params, Section: section}
	if d, dp, err := mime.ParseMediaType(h.Get("Content-Disposition")); err == nil {
		part.Disposition, part.DispParams = d, dp
	}
	if !part.isAttachment() {
		return nil
	}
	data, err := decodeTransfer(body, h.Get("Content-Transfer-Encoding"))
	if err != nil {
		return err
	}
	name := part.filename()
	switch {
	case name != "":
	case typ == "message":
		name = "message.eml"
	default:
		name = "attachment"
	}
	*out = append(*out, Document{ID: section, Name: name, ContentType: mt, Data: data})
	if mt == "message/rfc822" {
		inner, err := netmail.ReadMessage(bytes.NewReader(data))
		if err != nil {
			return nil // an attached message nobody can parse is still inspected as a document
		}
		innerBody, err := io.ReadAll(inner.Body)
		if err != nil {
			return nil
		}
		return walkEntity(textproto.MIMEHeader(inner.Header), innerBody, section, depth+1, out)
	}
	return nil
}

// decodeTransfer undoes a part's transfer encoding, exactly — unlike a body read for a
// process, a document handed on must be the bytes the sender attached, so an encoding
// that does not decode is an error rather than a pass-through.
func decodeTransfer(raw []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		data, err := decodeBase64Loose(raw)
		if err != nil {
			return nil, fmt.Errorf("mail: an attachment's base64 does not decode: %w", err)
		}
		return data, nil
	case "quoted-printable":
		data, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err != nil {
			return nil, fmt.Errorf("mail: an attachment's quoted-printable does not decode: %w", err)
		}
		return data, nil
	}
	return raw, nil
}
