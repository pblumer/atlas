package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeIMAP is an in-memory IMAP server speaking the subset imapMailbox uses. It is
// strict where the protocol is strict — a command it does not know is a BAD, a UID
// that does not exist answers nothing — so a client that leans on a lenient server
// fails here rather than at a customer's.
type fakeIMAP struct {
	t        *testing.T
	ln       net.Listener
	user     string
	password string
	caps     []string // beyond IMAP4rev1
	// startTLS serves plaintext and offers STARTTLS; tlsCfg is the server's TLS.
	startTLS    bool
	offerTLS    bool
	tlsCfg      *tls.Config
	maxLiteral  int // >0: answer a header fetch with a literal of this declared size
	noUIDNext   bool
	preauth     bool
	mu          sync.Mutex
	folders     map[string]*fakeFolder
	commands    []string
	listEntries []string // raw LIST lines after "* LIST "
}

type fakeFolder struct {
	validity uint32
	next     uint32
	msgs     []*fakeMsg
}

type fakeMsg struct {
	uid      uint32
	flags    map[string]bool
	date     string
	header   string
	bs       string
	sections map[string]string
}

// newFakeIMAP starts a server; opts configure it before it serves, so nothing the
// session goroutines read is written concurrently.
func newFakeIMAP(t *testing.T, opts ...func(*fakeIMAP)) (*fakeIMAP, *tls.Config) {
	t.Helper()
	serverTLS, clientTLS := testTLSConfig(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeIMAP{t: t, ln: ln, user: "me@example.com", password: "pw", tlsCfg: serverTLS,
		folders: map[string]*fakeFolder{"INBOX": {validity: 7, next: 1}}}
	for _, o := range opts {
		o(f)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go f.serve()
	return f, clientTLS
}

func (f *fakeIMAP) addr() string { return f.ln.Addr().String() }

// mailbox builds the client under test against this server.
func (f *fakeIMAP) mailbox(t *testing.T, clientTLS *tls.Config, send func(context.Context, Message) error) *imapMailbox {
	t.Helper()
	scheme := "imaps://"
	if f.startTLS {
		scheme = "imap://"
	}
	m, err := newIMAPMailbox(scheme+f.addr(), f.user, f.password, send)
	if err != nil {
		t.Fatalf("newIMAPMailbox: %v", err)
	}
	m.tlsCfg = clientTLS
	return m
}

func (f *fakeIMAP) add(folder string, m *fakeMsg) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	fo := f.folders[folder]
	if fo == nil {
		fo = &fakeFolder{validity: 9, next: 1}
		f.folders[folder] = fo
	}
	m.uid = fo.next
	fo.next++
	if m.flags == nil {
		m.flags = map[string]bool{}
	}
	fo.msgs = append(fo.msgs, m)
	return m.uid
}

// snapshot copies a folder's messages, flags included, under the lock.
func (f *fakeIMAP) snapshot(folder string) []fakeMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	fo := f.folders[folder]
	if fo == nil {
		return nil
	}
	out := make([]fakeMsg, len(fo.msgs))
	for i, m := range fo.msgs {
		out[i] = *m
		out[i].flags = map[string]bool{}
		for k, v := range m.flags {
			out[i].flags[k] = v
		}
	}
	return out
}

func withFolder(name string, validity, next uint32) func(*fakeIMAP) {
	return func(f *fakeIMAP) { f.folders[name] = &fakeFolder{validity: validity, next: next} }
}

func withCaps(caps ...string) func(*fakeIMAP) {
	return func(f *fakeIMAP) { f.caps = caps }
}

func (f *fakeIMAP) cmds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

func (f *fakeIMAP) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.session(c)
	}
}

func (f *fakeIMAP) session(c net.Conn) {
	defer c.Close()
	if !f.startTLS {
		tc := tls.Server(c, f.tlsCfg)
		if err := tc.Handshake(); err != nil {
			return
		}
		c = tc
	}
	r, w := bufio.NewReader(c), bufio.NewWriter(c)
	say := func(format string, a ...any) {
		fmt.Fprintf(w, format+"\r\n", a...)
	}
	if f.preauth {
		say("* PREAUTH [CAPABILITY %s] already logged in", f.capString(f.startTLS))
	} else {
		say("* OK [CAPABILITY %s] fake ready", f.capString(f.startTLS))
	}
	_ = w.Flush()
	var selected *fakeFolder
	var selectedName string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		tag, rest, _ := strings.Cut(line, " ")
		args := splitIMAPArgs(rest)
		if len(args) == 0 {
			say("%s BAD empty", tag)
			_ = w.Flush()
			continue
		}
		verb := strings.ToUpper(args[0])
		f.mu.Lock()
		if verb == "LOGIN" || verb == "AUTHENTICATE" {
			f.commands = append(f.commands, verb) // never the credentials
		} else {
			f.commands = append(f.commands, rest)
		}
		f.mu.Unlock()
		switch verb {
		case "CAPABILITY":
			say("* CAPABILITY %s", f.capString(f.startTLS))
			say("%s OK done", tag)
		case "STARTTLS":
			say("%s OK begin TLS", tag)
			_ = w.Flush()
			tc := tls.Server(c, f.tlsCfg)
			if err := tc.Handshake(); err != nil {
				return
			}
			c = tc
			r, w = bufio.NewReader(c), bufio.NewWriter(c)
			continue
		case "LOGIN":
			if len(args) == 3 && args[1] == f.user && args[2] == f.password {
				say("%s OK logged in", tag)
			} else {
				say("%s NO [AUTHENTICATIONFAILED] wrong credentials for %s / %s", tag, args[1], args[2])
			}
		case "AUTHENTICATE":
			raw, _ := base64.StdEncoding.DecodeString(args[len(args)-1])
			if len(args) == 3 && strings.EqualFold(args[1], "PLAIN") && string(raw) == "\x00"+f.user+"\x00"+f.password {
				say("%s OK authenticated", tag)
			} else {
				say("%s NO [AUTHENTICATIONFAILED] no", tag)
			}
		case "LOGOUT":
			say("* BYE bye")
			say("%s OK done", tag)
			_ = w.Flush()
			return
		case "SELECT", "EXAMINE":
			f.mu.Lock()
			fo := f.folders[args[1]]
			f.mu.Unlock()
			if fo == nil {
				say("%s NO no such folder", tag)
				break
			}
			selected, selectedName = fo, args[1]
			say("* %d EXISTS", len(fo.msgs))
			say("* OK [UIDVALIDITY %d] valid", fo.validity)
			if !f.noUIDNext {
				say("* OK [UIDNEXT %d] next", fo.next)
			}
			say("%s OK [READ-WRITE] selected", tag)
		case "LIST":
			for _, e := range f.listEntries {
				say("* LIST %s", e)
			}
			say("%s OK done", tag)
		case "UID":
			if selected == nil {
				say("%s BAD nothing selected", tag)
				break
			}
			f.uidCommand(tag, args[1:], selected, selectedName, say)
		default:
			say("%s BAD unknown command %s", tag, verb)
		}
		_ = w.Flush()
	}
}

func (f *fakeIMAP) capString(plain bool) string {
	caps := append([]string{"IMAP4rev1"}, f.caps...)
	if plain && f.offerTLS {
		caps = append(caps, "STARTTLS")
	}
	return strings.Join(caps, " ")
}

func (f *fakeIMAP) uidCommand(tag string, args []string, fo *fakeFolder, folderName string, say func(string, ...any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sub := strings.ToUpper(args[0])
	switch sub {
	case "SEARCH":
		var hits []string
		crit := strings.ToUpper(strings.Join(args[1:], " "))
		for _, m := range fo.msgs {
			switch {
			case crit == "ALL":
			case crit == "UNSEEN":
				if m.flags[`\Seen`] {
					continue
				}
			case strings.HasPrefix(crit, "UID "):
				lo, _, _ := strings.Cut(strings.TrimPrefix(crit, "UID "), ":")
				n, _ := strconv.Atoi(lo)
				// The real servers' trap, reproduced on purpose: n:* includes the
				// highest UID even when it is below n.
				if int(m.uid) < n && m.uid != fo.msgs[len(fo.msgs)-1].uid {
					continue
				}
			default:
				say("%s BAD search %s", tag, crit)
				return
			}
			hits = append(hits, strconv.Itoa(int(m.uid)))
		}
		say("* SEARCH %s", strings.Join(hits, " "))
		say("%s OK done", tag)
	case "FETCH":
		items := strings.Join(args[2:], " ")
		for i, m := range fo.msgs {
			if !uidInSet(m.uid, args[1]) {
				continue
			}
			if strings.Contains(items, "BODY.PEEK[HEADER]") {
				flags := []string{}
				for fl := range m.flags {
					flags = append(flags, fl)
				}
				sort.Strings(flags)
				size := len(m.header)
				if f.maxLiteral > 0 {
					size = f.maxLiteral
				}
				say("* %d FETCH (UID %d FLAGS (%s) INTERNALDATE \"%s\" BODY[HEADER] {%d}\r\n%s BODYSTRUCTURE %s)",
					i+1, m.uid, strings.Join(flags, " "), m.date, size, m.header, m.bs)
				continue
			}
			start := strings.Index(items, "BODY.PEEK[")
			end := strings.Index(items, "]")
			section := items[start+len("BODY.PEEK[") : end]
			data := m.sections[section]
			say("* %d FETCH (UID %d BODY[%s]<0> {%d}\r\n%s)", i+1, m.uid, section, len(data), data)
		}
		say("%s OK done", tag)
	case "STORE":
		for _, m := range fo.msgs {
			if !uidInSet(m.uid, args[1]) {
				continue
			}
			flag := strings.Trim(args[3], "()")
			if strings.HasPrefix(args[2], "+") {
				m.flags[flag] = true
			} else {
				delete(m.flags, flag)
			}
		}
		say("%s OK done", tag)
	case "MOVE", "COPY":
		dst := f.folders[args[2]]
		if dst == nil {
			say("%s NO [TRYCREATE] no such folder", tag)
			return
		}
		var kept []*fakeMsg
		var src, dstUID uint32
		for _, m := range fo.msgs {
			if !uidInSet(m.uid, args[1]) {
				kept = append(kept, m)
				continue
			}
			cp := *m
			cp.flags = map[string]bool{}
			for k, v := range m.flags {
				cp.flags[k] = v
			}
			cp.uid = dst.next
			dst.next++
			dst.msgs = append(dst.msgs, &cp)
			src, dstUID = m.uid, cp.uid
			if sub == "COPY" {
				kept = append(kept, m)
			}
		}
		fo.msgs = kept
		code := ""
		if contains(f.caps, "UIDPLUS") {
			code = fmt.Sprintf("[COPYUID %d %d %d] ", dst.validity, src, dstUID)
		}
		if sub == "MOVE" && code != "" {
			say("* OK %smoved", code)
			code = ""
		}
		say("%s OK %sdone", tag, code)
	case "EXPUNGE":
		var kept []*fakeMsg
		for _, m := range fo.msgs {
			if uidInSet(m.uid, args[1]) && m.flags[`\Deleted`] {
				continue
			}
			kept = append(kept, m)
		}
		fo.msgs = kept
		say("%s OK done", tag)
	default:
		say("%s BAD uid %s", tag, sub)
	}
	_ = folderName
}

func uidInSet(uid uint32, set string) bool {
	for _, s := range strings.Split(set, ",") {
		if s == strconv.Itoa(int(uid)) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// splitIMAPArgs splits a command line into words, unquoting quoted strings.
func splitIMAPArgs(s string) []string {
	var out []string
	var b strings.Builder
	inQuote, have := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote && c == '\\' && i+1 < len(s):
			i++
			b.WriteByte(s[i])
		case c == '"':
			inQuote = !inQuote
			have = true
		case c == ' ' && !inQuote:
			if have || b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
				have = false
			}
		default:
			b.WriteByte(c)
		}
	}
	if have || b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
