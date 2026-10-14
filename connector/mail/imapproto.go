package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// The IMAP wire protocol, as much of it as a mailbox Worker speaks (RFC 3501, with
// MOVE from RFC 6851 and UIDPLUS from RFC 4315 when the server offers them).
//
// It is written here rather than imported. What a mail Worker needs is a small,
// read-mostly subset — log in, select a folder, search and fetch by UID, set a flag,
// move — and the parser below is the whole of the protocol risk: every response is
// read under hard limits on literal size, token length and nesting depth, because the
// bytes come from a server the engine does not control. A general IMAP library brings
// the rest of the protocol, and its own parser, for no capability this Worker uses.
//
// Every session is bounded by the shared worker budget ([nettimeout.Default]) as one
// absolute connection deadline, the way the SMTP session is: a server that accepts
// and then stops answering costs one job attempt, not a parked token forever.

// Ports an IMAP endpoint defaults to: 993 is implicit TLS (RFC 8314), 143 is
// plaintext upgraded with STARTTLS.
const (
	imapsDefaultPort = "993"
	imapDefaultPort  = "143"
)

// Limits on what one response may contain. A literal is a message part; eight MiB is
// far past any header block and is never reached by a body fetch, which asks for a
// bounded range. Atoms and nesting have no business being large at all.
const (
	maxIMAPLiteral = 8 << 20
	maxIMAPToken   = 64 << 10
	maxIMAPDepth   = 32
)

// imapEndpoint is where an IMAP mailbox is reached and how its connection is secured.
// There is no plaintext mode: a password sent unencrypted to a mail server is a
// password sent to everyone between, so 143 always means STARTTLS, required.
type imapEndpoint struct {
	addr        string
	implicitTLS bool
}

// NormalizeIMAPEndpoint canonicalizes an operator-written IMAP endpoint into the form
// the worker stores — "imaps://host:993" or "imap://host:143" — or explains why it
// cannot. A bare host means implicit TLS on 993; "imap://" and port 143 mean STARTTLS.
func NormalizeIMAPEndpoint(endpoint string) (string, error) {
	ep, err := parseIMAPEndpoint(endpoint)
	if err != nil {
		return "", err
	}
	scheme := "imap"
	if ep.implicitTLS {
		scheme = "imaps"
	}
	return scheme + "://" + ep.addr, nil
}

func parseIMAPEndpoint(endpoint string) (imapEndpoint, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return imapEndpoint{}, errors.New("imap endpoint is required (e.g. \"imaps://imap.example.com:993\")")
	}
	scheme := ""
	if i := strings.Index(raw, "://"); i >= 0 {
		scheme = strings.ToLower(raw[:i])
		if scheme != "imap" && scheme != "imaps" {
			return imapEndpoint{}, fmt.Errorf("imap endpoint %q uses the %q scheme (want a bare host, \"imaps://\" or \"imap://\")", endpoint, scheme)
		}
		raw = raw[i+3:]
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	host, port := raw, ""
	if h, p, err := net.SplitHostPort(raw); err == nil {
		host, port = h, strings.TrimSpace(p)
	} else if net.ParseIP(raw) == nil && strings.Count(raw, ":") > 0 {
		return imapEndpoint{}, fmt.Errorf("imap endpoint %q is not a host or host:port (%w)", endpoint, err)
	}
	switch {
	case host == "":
		return imapEndpoint{}, fmt.Errorf("imap endpoint %q names no host", endpoint)
	case strings.ContainsAny(host, " \t"):
		return imapEndpoint{}, fmt.Errorf("imap endpoint %q has whitespace in its host", endpoint)
	case strings.Contains(host, "@"):
		return imapEndpoint{}, fmt.Errorf("imap endpoint %q looks like an e-mail address — this field wants the IMAP *server* (e.g. \"imaps://imap.example.com:993\")", endpoint)
	}
	if port == "" {
		port = imapsDefaultPort
		if scheme == "imap" {
			port = imapDefaultPort
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return imapEndpoint{}, fmt.Errorf("imap endpoint %q has an invalid port %q (want a number from 1 to 65535)", endpoint, port)
	}
	implicit := scheme == "imaps" || (scheme == "" && port != imapDefaultPort)
	return imapEndpoint{addr: net.JoinHostPort(host, strconv.Itoa(n)), implicitTLS: implicit}, nil
}

// imapResponse is one server response line, with its literals inlined.
type imapResponse struct {
	// Tag is "*" for untagged data, "+" for a continuation, or the command's tag.
	Tag string
	// Status is OK, NO, BAD, BYE or PREAUTH for a status response, upper-cased; empty
	// for a data response.
	Status string
	// Code is the bracketed response code's tokens ("UIDVALIDITY", "3857529045").
	Code []string
	// Text is the human-readable rest of a status response.
	Text string
	// Fields is a data response after the tag: atoms and strings as string, NIL as
	// nil, parenthesized lists as []any.
	Fields []any
}

// imapConn is one authenticated-or-not IMAP session.
type imapConn struct {
	c    net.Conn
	r    *bufio.Reader
	tag  int
	caps map[string]bool
	// preauth is a server that greeted the connection as already authenticated
	// (RFC 3501 §7.1.4): a LOGIN there is a protocol error, not a login.
	preauth bool
}

// dialIMAP connects, secures the connection (implicit TLS or a mandatory STARTTLS)
// and reads the greeting. The whole session is bounded by one deadline.
func dialIMAP(ctx context.Context, ep imapEndpoint, tlsCfg *tls.Config) (*imapConn, error) {
	deadline := time.Now().Add(nettimeout.Default)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	dctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	host := hostOf(ep.addr)
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	dialer := &net.Dialer{}
	var (
		conn net.Conn
		err  error
	)
	if ep.implicitTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).DialContext(dctx, "tcp", ep.addr)
	} else {
		conn, err = dialer.DialContext(dctx, "tcp", ep.addr)
	}
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", ep.addr, err)
	}
	_ = conn.SetDeadline(deadline)
	ic := &imapConn{c: conn, r: bufio.NewReader(conn)}
	greet, err := ic.read()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("no IMAP greeting from %s: %w", ep.addr, err)
	}
	if greet.Tag != "*" || (greet.Status != "OK" && greet.Status != "PREAUTH") {
		_ = conn.Close()
		return nil, fmt.Errorf("IMAP server %s refused the connection: %s %s", ep.addr, greet.Status, greet.Text)
	}
	ic.noteCaps(greet)
	ic.preauth = greet.Status == "PREAUTH"
	if !ep.implicitTLS && !ic.preauth {
		if err := ic.startTLS(tlsCfg, deadline); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("STARTTLS with %s: %w", ep.addr, err)
		}
	}
	return ic, nil
}

// startTLS upgrades the connection. It is not optional: a server that does not offer
// it is refused rather than logged into in the clear.
func (ic *imapConn) startTLS(cfg *tls.Config, deadline time.Time) error {
	if err := ic.ensureCaps(); err != nil {
		return err
	}
	if !ic.caps["STARTTLS"] {
		return errors.New("the server does not offer STARTTLS, and a password is not sent in the clear; use imaps:// (port 993)")
	}
	if _, err := ic.cmd("STARTTLS"); err != nil {
		return err
	}
	tc := tls.Client(ic.c, cfg)
	_ = tc.SetDeadline(deadline)
	if err := tc.Handshake(); err != nil {
		return err
	}
	ic.c, ic.r, ic.caps = tc, bufio.NewReader(tc), nil // capabilities before TLS do not count (RFC 3501 §6.2.1)
	return nil
}

// noteCaps records capabilities a status response announced in its code.
func (ic *imapConn) noteCaps(r imapResponse) {
	if len(r.Code) > 0 && strings.EqualFold(r.Code[0], "CAPABILITY") {
		ic.setCaps(r.Code[1:])
	}
}

func (ic *imapConn) setCaps(tokens []string) {
	ic.caps = map[string]bool{}
	for _, t := range tokens {
		ic.caps[strings.ToUpper(t)] = true
	}
}

// ensureCaps asks for the capabilities when nothing announced them yet.
func (ic *imapConn) ensureCaps() error {
	if ic.caps != nil {
		return nil
	}
	data, err := ic.cmd("CAPABILITY")
	if err != nil {
		return err
	}
	ic.caps = map[string]bool{}
	for _, d := range data {
		if len(d.Fields) > 0 && strings.EqualFold(fieldString(d.Fields[0]), "CAPABILITY") {
			for _, f := range d.Fields[1:] {
				ic.caps[strings.ToUpper(fieldString(f))] = true
			}
		}
	}
	return nil
}

// login authenticates with a username and password. LOGIN is used rather than an
// AUTHENTICATE mechanism because it is the one every server accepts, and the
// connection is already encrypted by the time it is sent.
func (ic *imapConn) login(user, password string) error {
	if ic.preauth {
		return nil
	}
	if err := ic.ensureCaps(); err != nil {
		return err
	}
	command := "LOGIN " + imapQuote(user) + " " + imapQuote(password)
	// A quoted string is seven-bit (RFC 3501 §4.3), so a password with an umlaut in it
	// does not survive LOGIN on a strict server. AUTHENTICATE PLAIN carries the same
	// two values base64-encoded, which any byte survives — sent with its initial
	// response (SASL-IR, RFC 4959) so it needs no continuation round trip.
	if (!isASCII(user) || !isASCII(password) || ic.caps["LOGINDISABLED"]) && ic.caps["AUTH=PLAIN"] && ic.caps["SASL-IR"] {
		command = "AUTHENTICATE PLAIN " + base64.StdEncoding.EncodeToString([]byte("\x00"+user+"\x00"+password))
	} else if ic.caps["LOGINDISABLED"] {
		return errors.New("the server has disabled LOGIN on this connection")
	}
	_, err := ic.cmd(command)
	if err != nil {
		// The server's text is kept out of the error deliberately: some echo the
		// command, and this one carries the password.
		var se *imapStatusError
		if errors.As(err, &se) {
			return fmt.Errorf("the IMAP server refused the login for %q (%s)", user, se.Status)
		}
		return err
	}
	ic.caps = nil // servers may announce new capabilities once authenticated
	return nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// logout ends the session politely and closes the connection whatever the answer.
func (ic *imapConn) logout() {
	_, _ = ic.cmd("LOGOUT")
	_ = ic.c.Close()
}

// imapStatusError is a tagged NO or BAD answer.
type imapStatusError struct {
	Command string
	Status  string
	Text    string
}

func (e *imapStatusError) Error() string {
	return fmt.Sprintf("IMAP %s answered %s: %s", e.Command, e.Status, e.Text)
}

// cmd sends one tagged command and collects the untagged responses up to its
// completion. A NO or BAD completion is an *imapStatusError. The tagged OK itself is
// returned last, so a caller can read its response code (COPYUID).
func (ic *imapConn) cmd(command string) ([]imapResponse, error) {
	ic.tag++
	tag := "a" + strconv.Itoa(ic.tag)
	if _, err := io.WriteString(ic.c, tag+" "+command+"\r\n"); err != nil {
		return nil, err
	}
	verb := command
	if i := strings.IndexByte(verb, ' '); i >= 0 {
		verb = verb[:i]
	}
	if strings.EqualFold(verb, "UID") {
		if rest := strings.Fields(command); len(rest) > 1 {
			verb = "UID " + strings.ToUpper(rest[1])
		}
	}
	var out []imapResponse
	for {
		r, err := ic.read()
		if err != nil {
			return out, err
		}
		switch r.Tag {
		case tag:
			if r.Status != "OK" {
				return out, &imapStatusError{Command: verb, Status: r.Status, Text: r.Text}
			}
			ic.noteCaps(r)
			return append(out, r), nil
		case "*":
			if r.Status == "BYE" && !strings.EqualFold(verb, "LOGOUT") {
				return out, fmt.Errorf("the IMAP server closed the session: %s", r.Text)
			}
			out = append(out, r)
		case "+":
			// Nothing this client sends waits for a continuation; a server asking for
			// one is out of step, and answering it would be guessing.
			return out, fmt.Errorf("unexpected IMAP continuation during %s", verb)
		}
	}
}

// read parses one complete response, literals included.
func (ic *imapConn) read() (imapResponse, error) {
	tag, err := ic.readToken()
	if err != nil {
		return imapResponse{}, err
	}
	r := imapResponse{Tag: tag}
	if tag == "+" {
		r.Text, err = ic.readLine()
		return r, err
	}
	if err := ic.skipSpace(); err != nil {
		return r, err
	}
	first, err := ic.readValue(0)
	if err != nil {
		return r, err
	}
	word := strings.ToUpper(fieldString(first))
	switch word {
	case "OK", "NO", "BAD", "BYE", "PREAUTH":
		r.Status = word
		return r, ic.readStatusRest(&r)
	}
	r.Fields = []any{first}
	for {
		b, err := ic.r.ReadByte()
		if err != nil {
			return r, err
		}
		switch b {
		case ' ':
			// A trailing space before the line end ("* SEARCH " with no hits) is not
			// what RFC 3501 writes, but it is what some servers send.
			if next, err := ic.r.Peek(1); err == nil && (next[0] == '\r' || next[0] == '\n') {
				continue
			}
			v, err := ic.readValue(0)
			if err != nil {
				return r, err
			}
			r.Fields = append(r.Fields, v)
		case '\r':
			if b, err := ic.r.ReadByte(); err != nil || b != '\n' {
				return r, errors.New("malformed IMAP line ending")
			}
			return r, nil
		case '\n':
			return r, nil
		default:
			return r, fmt.Errorf("unexpected %q in an IMAP response", b)
		}
	}
}

// readStatusRest reads a status response's optional [code] and its text.
func (ic *imapConn) readStatusRest(r *imapResponse) error {
	line, err := ic.readLine()
	if err != nil {
		return err
	}
	line = strings.TrimPrefix(line, " ")
	if strings.HasPrefix(line, "[") {
		if end := strings.IndexByte(line, ']'); end > 0 {
			code := strings.NewReplacer("(", " ", ")", " ").Replace(line[1:end])
			r.Code = strings.Fields(code)
			line = strings.TrimPrefix(line[end+1:], " ")
		}
	}
	r.Text = line
	return nil
}

// readLine reads up to CRLF, bounded.
func (ic *imapConn) readLine() (string, error) {
	var b strings.Builder
	for {
		c, err := ic.r.ReadByte()
		if err != nil {
			return "", err
		}
		switch c {
		case '\n':
			return strings.TrimSuffix(b.String(), "\r"), nil
		default:
			if b.Len() >= maxIMAPToken {
				return "", errors.New("IMAP response line too long")
			}
			b.WriteByte(c)
		}
	}
}

func (ic *imapConn) skipSpace() error {
	b, err := ic.r.ReadByte()
	if err != nil {
		return err
	}
	if b != ' ' {
		return ic.r.UnreadByte()
	}
	return nil
}

// readToken reads the tag: everything up to the first space or line end.
func (ic *imapConn) readToken() (string, error) {
	var b strings.Builder
	for {
		c, err := ic.r.ReadByte()
		if err != nil {
			return "", err
		}
		if c == ' ' || c == '\r' || c == '\n' {
			if err := ic.r.UnreadByte(); err != nil {
				return "", err
			}
			if b.Len() == 0 {
				return "", errors.New("empty IMAP tag")
			}
			if c == ' ' {
				_, _ = ic.r.ReadByte()
			}
			return b.String(), nil
		}
		if b.Len() >= maxIMAPToken {
			return "", errors.New("IMAP tag too long")
		}
		b.WriteByte(c)
	}
}

// readValue reads one value: a list, a quoted string, a literal, NIL or an atom.
func (ic *imapConn) readValue(depth int) (any, error) {
	if depth > maxIMAPDepth {
		return nil, errors.New("IMAP response nested too deeply")
	}
	c, err := ic.r.ReadByte()
	if err != nil {
		return nil, err
	}
	switch c {
	case '(':
		return ic.readList(depth + 1)
	case '"':
		return ic.readQuoted()
	case '{':
		return ic.readLiteral()
	}
	if err := ic.r.UnreadByte(); err != nil {
		return nil, err
	}
	atom, err := ic.readAtom()
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(atom, "NIL") {
		return nil, nil
	}
	return atom, nil
}

func (ic *imapConn) readList(depth int) ([]any, error) {
	out := []any{}
	for {
		c, err := ic.r.ReadByte()
		if err != nil {
			return nil, err
		}
		switch c {
		case ')':
			return out, nil
		case ' ':
			continue
		case '\r', '\n':
			return nil, errors.New("IMAP list not closed before the line ended")
		}
		if err := ic.r.UnreadByte(); err != nil {
			return nil, err
		}
		v, err := ic.readValue(depth)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

func (ic *imapConn) readQuoted() (string, error) {
	var b strings.Builder
	for {
		c, err := ic.r.ReadByte()
		if err != nil {
			return "", err
		}
		switch c {
		case '"':
			return b.String(), nil
		case '\\':
			if c, err = ic.r.ReadByte(); err != nil {
				return "", err
			}
		case '\r', '\n':
			return "", errors.New("IMAP quoted string not closed before the line ended")
		}
		if b.Len() >= maxIMAPToken {
			return "", errors.New("IMAP quoted string too long")
		}
		b.WriteByte(c)
	}
}

// readLiteral reads {n}CRLF followed by n bytes. The size is checked before anything
// is allocated for it.
func (ic *imapConn) readLiteral() (string, error) {
	var digits strings.Builder
	for {
		c, err := ic.r.ReadByte()
		if err != nil {
			return "", err
		}
		if c == '}' {
			break
		}
		if c == '+' {
			continue // a non-synchronizing literal marker, harmless in a response
		}
		if c < '0' || c > '9' || digits.Len() > 10 {
			return "", errors.New("malformed IMAP literal length")
		}
		digits.WriteByte(c)
	}
	n, err := strconv.Atoi(digits.String())
	if err != nil {
		return "", errors.New("malformed IMAP literal length")
	}
	if n > maxIMAPLiteral {
		return "", fmt.Errorf("IMAP literal of %d bytes exceeds the %d-byte limit", n, maxIMAPLiteral)
	}
	if c, err := ic.r.ReadByte(); err != nil || c != '\r' {
		return "", errors.New("malformed IMAP literal")
	}
	if c, err := ic.r.ReadByte(); err != nil || c != '\n' {
		return "", errors.New("malformed IMAP literal")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(ic.r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// readAtom reads an atom. A bracketed section inside it ("BODY[1.2]") is read whole,
// spaces included, because a section is part of the name it qualifies.
func (ic *imapConn) readAtom() (string, error) {
	var b strings.Builder
	brackets := 0
	for {
		c, err := ic.r.ReadByte()
		if err != nil {
			return "", err
		}
		switch {
		case c == '[':
			brackets++
		case c == ']' && brackets > 0:
			brackets--
		case brackets == 0 && (c == ' ' || c == '(' || c == ')' || c == '\r' || c == '\n'):
			if err := ic.r.UnreadByte(); err != nil {
				return "", err
			}
			if b.Len() == 0 {
				return "", fmt.Errorf("unexpected %q in an IMAP response", c)
			}
			return b.String(), nil
		case c == '\r' || c == '\n':
			return "", errors.New("IMAP section not closed before the line ended")
		}
		if b.Len() >= maxIMAPToken {
			return "", errors.New("IMAP atom too long")
		}
		b.WriteByte(c)
	}
}

// fieldString reads a parsed value as a string; a list or NIL reads as "".
func fieldString(v any) string {
	s, _ := v.(string)
	return s
}

// imapQuote renders a string as an IMAP quoted string. A value that cannot be quoted
// — one with a line break in it, which no folder name or credential this worker sends
// should carry — is refused by being sent as an empty string the server will reject,
// rather than smuggling a second command onto the line.
func imapQuote(s string) string {
	if strings.ContainsAny(s, "\r\n\x00") {
		return `""`
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// imapFolderName encodes a folder name for the wire: IMAP names are modified UTF-7
// (RFC 3501 §5.1.3), so "Entwürfe" travels as "Entw&APw-rfe".
func imapFolderName(name string) string {
	var b strings.Builder
	var pending []rune
	flush := func() {
		if len(pending) == 0 {
			return
		}
		units := utf16.Encode(pending)
		raw := make([]byte, 0, len(units)*2)
		for _, u := range units {
			raw = append(raw, byte(u>>8), byte(u))
		}
		b.WriteByte('&')
		b.WriteString(mutf7.EncodeToString(raw))
		b.WriteByte('-')
		pending = pending[:0]
	}
	for _, r := range name {
		switch {
		case r == '&':
			flush()
			b.WriteString("&-")
		case r >= 0x20 && r <= 0x7e:
			flush()
			b.WriteRune(r)
		default:
			pending = append(pending, r)
		}
	}
	flush()
	return imapQuote(b.String())
}

// mutf7 is the base64 alphabet of modified UTF-7: "," where base64 has "/", and no
// padding.
var mutf7 = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+,").WithPadding(base64.NoPadding)
