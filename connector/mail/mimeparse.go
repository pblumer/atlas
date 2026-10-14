package mail

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/htmlindex"
)

// Reading a received message: the pieces every provider shares.
//
// IMAP hands over raw headers and a body structure; Gmail hands over decoded header
// pairs and a part tree with the content inline; Graph hands over neither, because it
// has already done this work. So the decoding lives here once — header words, transfer
// encodings, charsets, the choice of a body part and the list of attachments — and each
// provider only translates its own shape into a [mimePart].

// charsetReader decodes a named charset into UTF-8 for the header-word decoder and the
// address parser. UTF-8 and ASCII pass through; anything else is looked up in the
// WHATWG index, which is what mail clients actually send (it maps ISO-8859-1 onto
// windows-1252, the superset every mail labelled Latin-1 is really written in).
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch normalizeCharset(charset) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	}
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, fmt.Errorf("mail: unknown charset %q", charset)
	}
	return enc.NewDecoder().Reader(input), nil
}

func normalizeCharset(charset string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(charset), `"`))
}

var headerDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

// decodeHeader decodes the RFC 2047 encoded words of a header value. A word in a
// charset nobody knows is left as written rather than dropped: a subject that shows
// its encoding is still a subject.
func decodeHeader(s string) string {
	out, err := headerDecoder.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

// decodeContent turns a part's raw content into text: the transfer encoding undone,
// then the charset converted to UTF-8. What cannot be decoded is passed through rather
// than refused, and the result is always valid UTF-8, because it becomes a variable.
func decodeContent(raw []byte, encoding, charset string) string {
	data := raw
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		if b, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw))); err == nil {
			data = b
		}
	case "base64":
		if b, err := decodeBase64Loose(raw); err == nil {
			data = b
		}
	}
	return toUTF8(data, charset)
}

// decodeBase64Loose decodes a base64 body as mail carries it: wrapped at 76 columns,
// sometimes with its padding dropped.
func decodeBase64Loose(raw []byte) ([]byte, error) {
	clean := bytes.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', ' ', '\t':
			return -1
		}
		return r
	}, raw)
	if b, err := base64.StdEncoding.DecodeString(string(clean)); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(string(clean), "="))
}

// toUTF8 converts bytes in a named charset to a valid UTF-8 string.
func toUTF8(data []byte, charset string) string {
	r, err := charsetReader(charset, bytes.NewReader(data))
	if err == nil {
		if b, err := io.ReadAll(r); err == nil {
			data = b
		}
	}
	return strings.ToValidUTF8(string(data), "�")
}

// htmlToText reduces an HTML body to its text: what a person reading the mail would
// see, without scripts, styles or the document head, one line per block. It is how a
// message that has only an HTML body still has a body a process can read.
func htmlToText(src string) string {
	z := html.NewTokenizer(strings.NewReader(src))
	var b strings.Builder
	skip := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return tidyLines(b.String())
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			switch tag {
			case "script", "style", "head", "title", "noscript":
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
				continue
			}
			if htmlBlockTags[tag] {
				b.WriteByte('\n')
			}
		case html.TextToken:
			if skip > 0 {
				continue
			}
			text := strings.ReplaceAll(string(z.Text()), " ", " ")
			b.WriteString(strings.Join(strings.Fields(text), " "))
			if strings.HasSuffix(text, " ") || strings.HasSuffix(text, "\n") {
				b.WriteByte(' ')
			}
		}
	}
}

var htmlBlockTags = map[string]bool{
	"br": true, "p": true, "div": true, "li": true, "tr": true, "ul": true, "ol": true,
	"table": true, "blockquote": true, "pre": true, "section": true, "article": true,
	"header": true, "footer": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// tidyLines trims every line and drops the empty ones, so the block breaks htmlToText
// writes do not turn into runs of blank lines.
func tidyLines(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// capBody cuts a body at MaxBodyBytes on a character boundary and says whether it did.
func capBody(s string) (string, bool) {
	if len(s) <= MaxBodyBytes {
		return s, false
	}
	n := MaxBodyBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

var addressParser = &netmail.AddressParser{WordDecoder: headerDecoder}

// parseAddressList reads an address header into bare, lower-cased addresses. A list
// net/mail cannot parse is kept entry by entry as written, as long as an entry looks
// like it addresses somebody: a recipient nobody can see is worse than one that looks
// odd.
func parseAddressList(h string) []string {
	if strings.TrimSpace(h) == "" {
		return nil
	}
	list, err := addressParser.ParseList(h)
	if err != nil {
		var out []string
		for _, part := range strings.Split(h, ",") {
			if p := strings.TrimSpace(part); strings.Contains(p, "@") {
				out = append(out, p)
			}
		}
		return out
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, strings.ToLower(a.Address))
	}
	return out
}

// parseAddress reads a single-address header (From, Reply-To) into the address and
// the display name.
func parseAddress(h string) (addr, name string) {
	if strings.TrimSpace(h) == "" {
		return "", ""
	}
	a, err := addressParser.Parse(h)
	if err != nil {
		if list := parseAddressList(h); len(list) > 0 {
			return list[0], ""
		}
		return "", ""
	}
	return strings.ToLower(a.Address), a.Name
}

// parseAuthResults reads the receiving server's verdict from the topmost
// Authentication-Results header (RFC 8601). Only the topmost one is read: servers
// prepend headers, so a lower one was written before the receiving server saw the
// message — possibly by whoever sent it.
//
// Comments are removed before the header is split, because a comment can contain a
// semicolon ("domain of a@example.com; designates …"), and only the first token of a
// result segment is read, because that is where RFC 8601 puts method=result. A segment
// whose first token has no "=" is the authserv-id, which Exchange Online omits.
func parseAuthResults(headers []string) AuthResults {
	var out AuthResults
	if len(headers) == 0 {
		return out
	}
	for _, seg := range strings.Split(stripComments(headers[0]), ";") {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		method, result, ok := strings.Cut(fields[0], "=")
		if !ok {
			continue
		}
		result = strings.ToLower(result)
		switch strings.ToLower(method) {
		case "spf":
			if out.SPF == "" {
				out.SPF = result
			}
		case "dkim":
			if out.DKIM == "" {
				out.DKIM = result
			}
		case "dmarc":
			if out.DMARC == "" {
				out.DMARC = result
			}
		}
	}
	return out
}

// stripComments removes RFC 5322 comments — parenthesized, possibly nested — from a
// header value.
func stripComments(s string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && depth > 0:
			i++ // an escaped character inside a comment
		case c == '(':
			depth++
		case c == ')' && depth > 0:
			depth--
		case depth == 0:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// mimePart is one node of a message's structure, in the shape both IMAP's
// BODYSTRUCTURE and Gmail's payload reduce to. Type, Subtype, Disposition and Encoding
// are lower-cased; Params and DispParams have lower-cased keys.
type mimePart struct {
	Type, Subtype string
	Params        map[string]string
	Disposition   string
	DispParams    map[string]string
	Encoding      string
	Size          int64
	// Section addresses the part at the provider: an IMAP section number ("1.2") or a
	// Gmail part id.
	Section  string
	Children []mimePart
	// Data is the part's content when the provider hands it over inline (Gmail),
	// already free of its transfer encoding but still in its own charset.
	Data []byte
}

func (p *mimePart) contentType() string { return p.Type + "/" + p.Subtype }

func (p *mimePart) charset() string { return p.Params["charset"] }

// filename is the part's file name: the disposition's filename, else the content
// type's name, each possibly RFC 2231- or RFC 2047-encoded.
func (p *mimePart) filename() string {
	if v := paramValue(p.DispParams, "filename"); v != "" {
		return v
	}
	return paramValue(p.Params, "name")
}

// paramValue reads a MIME parameter, decoding the RFC 2231 form — the name with a "*"
// appended, and a value of charset, language and percent-encoded text separated by
// single quotes — when that is how it was sent, and RFC 2047 encoded words when that is.
func paramValue(params map[string]string, name string) string {
	if v, ok := params[name+"*"]; ok {
		if charset, rest, ok := strings.Cut(v, "'"); ok {
			if _, enc, ok := strings.Cut(rest, "'"); ok {
				if raw, err := url.PathUnescape(enc); err == nil {
					return toUTF8([]byte(raw), charset)
				}
			}
		}
	}
	return decodeHeader(params[name])
}

// isAttachment says whether a leaf part is a document rather than the body: one that
// says so, a forwarded message, or a named part that is not text. A text part with a
// name but no attachment disposition is still a body — some clients name every part.
func (p *mimePart) isAttachment() bool {
	return p.Disposition == "attachment" || p.Type == "message" ||
		(p.Type != "text" && p.filename() != "")
}

// bodyParts finds the first plain-text and the first HTML part that are not
// attachments, depth first, which is the order a multipart/alternative lists them in.
func (p *mimePart) bodyParts() (plain, htmlPart *mimePart) {
	var walk func(n *mimePart)
	walk = func(n *mimePart) {
		if len(n.Children) > 0 {
			for i := range n.Children {
				walk(&n.Children[i])
			}
			return
		}
		if n.Type != "text" || n.isAttachment() {
			return
		}
		switch {
		case n.Subtype == "plain" && plain == nil:
			plain = n
		case n.Subtype == "html" && htmlPart == nil:
			htmlPart = n
		}
	}
	walk(p)
	return plain, htmlPart
}

// attachments lists the leaf parts that are documents, in message order.
func (p *mimePart) attachments() []Attachment {
	var out []Attachment
	var walk func(n *mimePart)
	walk = func(n *mimePart) {
		if len(n.Children) > 0 {
			for i := range n.Children {
				walk(&n.Children[i])
			}
			return
		}
		if !n.isAttachment() {
			return
		}
		name := n.filename()
		switch {
		case name != "":
		case n.Type == "message":
			name = "message.eml"
		default:
			name = "attachment"
		}
		out = append(out, Attachment{Name: name, ContentType: n.contentType(), Size: n.Size})
	}
	walk(p)
	return out
}

// bodyFromParts makes a body out of a message's text parts: the plain-text part when
// there is one, else the HTML part reduced to its text. read fetches a part's content
// when the provider did not hand it over inline.
func bodyFromParts(plain, htmlPart *mimePart, read func(*mimePart) ([]byte, error)) (string, error) {
	part, isHTML := plain, false
	if part == nil {
		part, isHTML = htmlPart, true
	}
	if part == nil {
		return "", nil
	}
	raw := part.Data
	if raw == nil && read != nil {
		var err error
		if raw, err = read(part); err != nil {
			return "", err
		}
	}
	text := decodeContent(raw, part.Encoding, part.charset())
	if isHTML {
		text = htmlToText(text)
	}
	return text, nil
}
