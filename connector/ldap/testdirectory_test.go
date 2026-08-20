package ldap

import (
	"net"
	"sort"
	"sync"
	"testing"

	ber "github.com/go-asn1-ber/asn1-ber"
	goldap "github.com/go-ldap/ldap/v3"
)

// testDirectory is a minimal in-process LDAP v3 server — just enough of the wire
// protocol for the *real* GoDialer to dial, bind, and drive one operation against
// it. The worker's own tests use the Conn/Dialer fakes; those never touch
// client.go's go-ldap adapter, which is the code that actually talks to a
// directory in production. LDAP is a connection-oriented binary protocol with no
// httptest equivalent, so the server is spelled out here rather than vendored.
//
// It answers the six operations the connector issues (bind, search, add, modify,
// delete, and the RFC 3062 password-modify extended request) and records what it
// was asked, so a test can assert the DN really crossed the wire rather than only
// that no error came back.
type testDirectory struct {
	// URL is the ldap:// address the dialer should be pointed at.
	URL string

	mu sync.Mutex
	// ops and dns record each request in arrival order.
	ops []string
	dns []string
	// entries is what a search returns; result and bindResult are the LDAP result
	// codes answered for operations and for the bind (0 = success). Keeping the
	// bind separate lets a test fail one operation while still getting connected.
	entries    []Entry
	result     uint16
	bindResult uint16
}

// startTestDirectory brings up the server on a loopback port and shuts it down
// when the test ends.
func startTestDirectory(t *testing.T, d *testDirectory) *testDirectory {
	t.Helper()
	if d == nil {
		d = &testDirectory{}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d.URL = "ldap://" + ln.Addr().String()
	go d.serve(ln)
	t.Cleanup(func() { _ = ln.Close() })
	return d
}

func (d *testDirectory) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed by the test's cleanup
		}
		go d.handle(conn)
	}
}

// handle answers one connection's requests until the client unbinds or hangs up.
func (d *testDirectory) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	for {
		pkt, err := ber.ReadPacket(conn)
		if err != nil {
			return
		}
		if len(pkt.Children) < 2 {
			return
		}
		id, ok := pkt.Children[0].Value.(int64)
		if !ok {
			return
		}
		op := pkt.Children[1]
		switch op.Tag {
		case goldap.ApplicationBindRequest:
			d.record("bind", seqChildString(op, 1))
			_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationBindResponse, d.codeFor(true)))
		case goldap.ApplicationUnbindRequest:
			return
		case goldap.ApplicationSearchRequest:
			d.record("search", seqChildString(op, 0))
			code := d.codeFor(false)
			if code == 0 {
				for _, e := range d.snapshotEntries() {
					_, _ = conn.Write(searchEntryPacket(id, e))
				}
			}
			_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationSearchResultDone, code))
		case goldap.ApplicationAddRequest:
			d.record("add", seqChildString(op, 0))
			_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationAddResponse, d.codeFor(false)))
		case goldap.ApplicationModifyRequest:
			d.record("modify", seqChildString(op, 0))
			_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationModifyResponse, d.codeFor(false)))
		case goldap.ApplicationDelRequest:
			// A delete request is [APPLICATION 10] LDAPDN — primitive, so the DN is
			// the element's own payload rather than a child.
			d.record("delete", string(op.Data.Bytes()))
			_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationDelResponse, d.codeFor(false)))
		case goldap.ApplicationExtendedRequest:
			// Both STARTTLS and password-modify arrive here; the connector only ever
			// sends one per connection, so one answer covers both.
			d.record("extended", "")
			_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationExtendedResponse, d.codeFor(false)))
		default:
			return
		}
	}
}

func (d *testDirectory) record(op, dn string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ops = append(d.ops, op)
	d.dns = append(d.dns, dn)
}

func (d *testDirectory) codeFor(bind bool) uint16 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if bind {
		return d.bindResult
	}
	return d.result
}

func (d *testDirectory) snapshotEntries() []Entry {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Entry(nil), d.entries...)
}

// seen reports the operations recorded so far, in arrival order.
func (d *testDirectory) seen() ([]string, []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.ops...), append([]string(nil), d.dns...)
}

// seqChildString reads the i-th child of a constructed request body as a string,
// which is where every request that carries a DN keeps it.
func seqChildString(op *ber.Packet, i int) string {
	if len(op.Children) <= i {
		return ""
	}
	s, _ := op.Children[i].Value.(string)
	return s
}

// resultPacket builds an LDAPMessage wrapping an LDAPResult — the shape every
// response in this server shares: SEQUENCE { messageID, [APPLICATION tag] {
// resultCode, matchedDN, diagnosticMessage } }.
func resultPacketFor(id int64, app int, code uint16) []byte {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))
	res := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(app), nil, "Response")
	res.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, int64(code), "resultCode"))
	res.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	res.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
	msg.AppendChild(res)
	return msg.Bytes()
}

func (d *testDirectory) resultPacket(id int64, app int, code uint16) []byte {
	return resultPacketFor(id, app, code)
}

// searchEntryPacket builds one SearchResultEntry: the DN plus the entry's
// attributes as SEQUENCE { type, SET OF value }.
func searchEntryPacket(id int64, e Entry) []byte {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))
	ent := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(goldap.ApplicationSearchResultEntry), nil, "SearchResultEntry")
	ent.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, e.DN, "DN"))
	attrs := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attributes")
	names := make([]string, 0, len(e.Attributes))
	for name := range e.Attributes {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic wire order, so a test can compare exactly
	for _, name := range names {
		a := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attribute")
		a.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "Type"))
		vals := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "Values")
		for _, v := range e.Attributes[name] {
			vals.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, v, "Value"))
		}
		a.AppendChild(vals)
		attrs.AppendChild(a)
	}
	ent.AppendChild(attrs)
	msg.AppendChild(ent)
	return msg.Bytes()
}
