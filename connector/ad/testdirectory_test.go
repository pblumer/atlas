package ad

import (
	"net"
	"sort"
	"sync"
	"testing"

	ber "github.com/go-asn1-ber/asn1-ber"
	goldap "github.com/go-ldap/ldap/v3"
)

// testDirectory is a minimal in-process LDAP v3 server — just enough of the wire
// protocol for the *real* GoDialer to dial, bind, and drive one operation against it.
// The worker's tests use the Conn/Dialer fakes; those never touch client.go's go-ldap
// adapter, which is the code that actually talks to a directory in production. LDAP is
// a connection-oriented binary protocol with no httptest equivalent, so the server is
// spelled out here rather than vendored (mirroring connector/ldap's testDirectory).
//
// It answers the operations the AD connector issues — bind, search (for the
// userAccountControl read), add, and modify — and records what it was asked, so a test
// can assert the DN really crossed the wire.
type testDirectory struct {
	URL string

	mu  sync.Mutex
	ops []string
	dns []string
	// searchDN/searchAttrs are the single entry a search returns; result/bindResult are
	// the LDAP result codes answered for operations and for the bind (0 = success).
	searchDN    string
	searchAttrs map[string][]string
	result      uint16
	bindResult  uint16
	// dirsync makes the search-done carry a DirSync response control, which is how a
	// domain controller answers a DirSync request.
	dirsync   bool
	cookieOut []byte
	moreOut   bool
}

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
			return
		}
		go d.handle(conn)
	}
}

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
				dn, attrs := d.snapshotEntry()
				if dn != "" {
					_, _ = conn.Write(searchEntryPacket(id, dn, attrs))
				}
			}
			if d.dirSync() {
				_, _ = conn.Write(searchDoneWithDirSync(id, code, d.syncCookie(), d.syncMore()))
			} else {
				_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationSearchResultDone, code))
			}
		case goldap.ApplicationAddRequest:
			d.record("add", seqChildString(op, 0))
			_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationAddResponse, d.codeFor(false)))
		case goldap.ApplicationModifyRequest:
			d.record("modify", seqChildString(op, 0))
			_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationModifyResponse, d.codeFor(false)))
		case goldap.ApplicationModifyDNRequest:
			d.record("modifydn", seqChildString(op, 0))
			_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationModifyDNResponse, d.codeFor(false)))
		case goldap.ApplicationDelRequest:
			// A del request carries the DN as the operation's own value, not as a
			// child sequence, so it is read directly rather than through seqChildString.
			d.record("delete", delRequestDN(op))
			_, _ = conn.Write(d.resultPacket(id, goldap.ApplicationDelResponse, d.codeFor(false)))
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

func (d *testDirectory) snapshotEntry() (string, map[string][]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	attrs := make(map[string][]string, len(d.searchAttrs))
	for k, v := range d.searchAttrs {
		attrs[k] = append([]string(nil), v...)
	}
	return d.searchDN, attrs
}

func (d *testDirectory) seen() ([]string, []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.ops...), append([]string(nil), d.dns...)
}

// seqChildString reads the i-th child of a constructed request body as a string, where
// every request that carries a DN keeps it.
func seqChildString(op *ber.Packet, i int) string {
	if len(op.Children) <= i {
		return ""
	}
	s, _ := op.Children[i].Value.(string)
	return s
}

// resultPacket builds an LDAPMessage wrapping an LDAPResult.
func (d *testDirectory) resultPacket(id int64, app int, code uint16) []byte {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))
	res := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(app), nil, "Response")
	res.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, int64(code), "resultCode"))
	res.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	res.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
	msg.AppendChild(res)
	return msg.Bytes()
}

// searchEntryPacket builds one SearchResultEntry: the DN plus attributes as SEQUENCE {
// type, SET OF value }.
func searchEntryPacket(id int64, dn string, attributes map[string][]string) []byte {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))
	ent := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(goldap.ApplicationSearchResultEntry), nil, "SearchResultEntry")
	ent.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, dn, "DN"))
	attrs := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attributes")
	names := make([]string, 0, len(attributes))
	for name := range attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		a := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attribute")
		a.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "Type"))
		vals := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "Values")
		for _, v := range attributes[name] {
			vals.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, v, "Value"))
		}
		a.AppendChild(vals)
		attrs.AppendChild(a)
	}
	ent.AppendChild(attrs)
	msg.AppendChild(ent)
	return msg.Bytes()
}

// delRequestDN reads the DN out of a DelRequest. Unlike the other operations the DN
// is not a child of a sequence but the request packet's own primitive payload, so it
// is read from the raw data rather than through seqChildString.
func delRequestDN(op *ber.Packet) string {
	if s, ok := op.Value.(string); ok && s != "" {
		return s
	}
	if len(op.ByteValue) > 0 {
		return string(op.ByteValue)
	}
	if op.Data != nil {
		return op.Data.String()
	}
	return ""
}

func (d *testDirectory) dirSync() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dirsync
}

func (d *testDirectory) syncCookie() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]byte(nil), d.cookieOut...)
}

func (d *testDirectory) syncMore() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.moreOut
}

// searchDoneWithDirSync builds a SearchResultDone carrying the DirSync response
// control: SEQUENCE { MoreResults, unused, CookieServer }. The first field is
// MoreResults even though go-ldap's struct calls it Flags — the response reuses the
// request's shape and the names do not survive the trip.
func searchDoneWithDirSync(id int64, code uint16, cookie []byte, more bool) []byte {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))
	res := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(goldap.ApplicationSearchResultDone), nil, "Response")
	res.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, int64(code), "resultCode"))
	res.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	res.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
	msg.AppendChild(res)

	moreVal := int64(0)
	if more {
		moreVal = 1
	}
	val := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "DirSync Control Value")
	val.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, moreVal, "MoreResults"))
	val.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, int64(0), "unused"))
	val.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, string(cookie), "CookieServer"))

	ctrl := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Control")
	ctrl.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, goldap.ControlTypeDirSync, "Control Type"))
	ctrl.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, string(val.Bytes()), "Control Value"))

	ctrls := ber.Encode(ber.ClassContext, ber.TypeConstructed, 0, nil, "Controls")
	ctrls.AppendChild(ctrl)
	msg.AppendChild(ctrls)
	return msg.Bytes()
}
