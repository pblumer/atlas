package ad

import (
	"strings"
	"testing"
)

// TestGoConnOperations drives the real go-ldap adapter (GoDialer + goConn) against the
// in-process directory: dial+bind, add, modify, and the userAccountControl read all
// cross the wire and the server records them. This is the layer the worker's fakes
// cannot reach.
func TestGoConnOperations(t *testing.T) {
	dir := startTestDirectory(t, &testDirectory{
		searchDN:    "cn=Arno,dc=x",
		searchAttrs: map[string][]string{"userAccountControl": {"514"}},
	})
	conn, err := NewDialer().Dial(dir.URL, "cn=admin,dc=x", "pw", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if err := conn.Add("cn=Arno,dc=x", map[string][]string{"objectClass": {"user"}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := conn.Modify("cn=Arno,dc=x", []Mod{{Op: modReplace, Attr: "unicodePwd", Vals: []string{"secret"}}}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	uac, err := conn.ReadAttr("cn=Arno,dc=x", "userAccountControl")
	if err != nil {
		t.Fatalf("ReadAttr: %v", err)
	}
	if len(uac) != 1 || uac[0] != "514" {
		t.Errorf("ReadAttr = %v, want [514]", uac)
	}

	ops, dns := dir.seen()
	wantOps := map[string]bool{"bind": true, "add": true, "modify": true, "search": true}
	for _, op := range ops {
		delete(wantOps, op)
	}
	if len(wantOps) != 0 {
		t.Errorf("operations seen = %v, missing %v", ops, wantOps)
	}
	// The add/modify/search DNs crossed the wire.
	var sawDN bool
	for _, dn := range dns {
		if dn == "cn=Arno,dc=x" {
			sawDN = true
		}
	}
	if !sawDN {
		t.Errorf("DNs seen = %v, want cn=Arno,dc=x", dns)
	}
}

// TestGoConnBindFailure proves a bind rejection surfaces from Dial.
func TestGoConnBindFailure(t *testing.T) {
	dir := startTestDirectory(t, &testDirectory{bindResult: 49}) // invalidCredentials
	if _, err := NewDialer().Dial(dir.URL, "cn=admin,dc=x", "wrong", false); err == nil {
		t.Fatal("Dial with a rejected bind: err = nil, want error")
	}
}

// TestGoConnOperationErrors proves a non-zero result code surfaces as an error from
// each operation.
func TestGoConnOperationErrors(t *testing.T) {
	dir := startTestDirectory(t, &testDirectory{result: 1}) // operationsError for non-bind ops
	conn, err := NewDialer().Dial(dir.URL, "cn=admin,dc=x", "pw", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if err := conn.Add("cn=x", map[string][]string{"a": {"b"}}); err == nil {
		t.Error("Add against a failing server: want error")
	}
	if err := conn.Modify("cn=x", []Mod{{Op: modReplace, Attr: "a", Vals: []string{"b"}}}); err == nil {
		t.Error("Modify against a failing server: want error")
	}
	if _, err := conn.ReadAttr("cn=x", "a"); err == nil {
		t.Error("ReadAttr against a failing server: want error")
	}
}

// TestGoConnReadAttrMissing proves a search with no entry yields an empty slice, no
// error (the read-modify-write then falls back to the baseline).
func TestGoConnReadAttrMissing(t *testing.T) {
	dir := startTestDirectory(t, &testDirectory{}) // no searchDN → no entry returned
	conn, err := NewDialer().Dial(dir.URL, "", "", false)
	if err != nil {
		t.Fatalf("Dial (anonymous): %v", err)
	}
	defer conn.Close()
	vals, err := conn.ReadAttr("cn=x", "userAccountControl")
	if err != nil {
		t.Fatalf("ReadAttr: %v", err)
	}
	if len(vals) != 0 {
		t.Errorf("ReadAttr of a missing entry = %v, want empty", vals)
	}
}

// TestGoDialerStartTLSError covers the STARTTLS branch of Dial: upgrading against a
// plain (non-TLS) server fails the handshake. The STARTTLS *success* path needs a real
// certificate a loopback server can't present, so only the failure branch is covered.
func TestGoDialerStartTLSError(t *testing.T) {
	dir := startTestDirectory(t, &testDirectory{})
	if _, err := NewDialer().Dial(dir.URL, "", "", true); err == nil {
		t.Fatal("Dial with STARTTLS against a plain server: err = nil, want error")
	}
}

// TestGoConnModifyDN drives the real go-ldap adapter's ModifyDN against the test
// directory, so the wire path a move actually takes in production is exercised — the
// Conn fake the worker tests use never reaches this code.
func TestGoConnModifyDN(t *testing.T) {
	d := startTestDirectory(t, nil)
	conn, err := NewDialer().Dial(d.URL, "cn=svc,dc=x", "pw", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if err := conn.ModifyDN("cn=Arno,ou=users,dc=x", "cn=Arno Meier", "ou=extern,dc=x"); err != nil {
		t.Fatalf("ModifyDN: %v", err)
	}
	if ops, dns := d.seen(); !hasOp(ops, "modifydn") {
		t.Errorf("ops = %v (dns %v), want a modifydn", ops, dns)
	}

	// A server that refuses reports the failure rather than swallowing it.
	bad := startTestDirectory(t, &testDirectory{result: 32}) // noSuchObject
	c2, err := NewDialer().Dial(bad.URL, "cn=svc,dc=x", "pw", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c2.Close()
	if err := c2.ModifyDN("cn=weg,dc=x", "cn=neu", "dc=x"); err == nil {
		t.Error("a refused ModifyDN must be an error")
	}
}

// TestGoConnDelete drives the real adapter's Delete against the test directory.
func TestGoConnDelete(t *testing.T) {
	d := startTestDirectory(t, nil)
	conn, err := NewDialer().Dial(d.URL, "cn=svc,dc=x", "pw", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if err := conn.Delete("cn=Alt,ou=users,dc=x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ops, dns := d.seen()
	if !hasOp(ops, "delete") {
		t.Errorf("ops = %v, want a delete", ops)
	}
	if !hasDN(dns, "cn=Alt,ou=users,dc=x") {
		t.Errorf("dns = %v, want the deleted entry's dn on the wire", dns)
	}

	bad := startTestDirectory(t, &testDirectory{result: 32})
	c2, err := NewDialer().Dial(bad.URL, "cn=svc,dc=x", "pw", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c2.Close()
	if err := c2.Delete("cn=weg,dc=x"); err == nil {
		t.Error("a refused Delete must be an error")
	}
}

// TestSplitDN pins the DN split a move depends on, including the escape rule that
// keeps a comma inside a value from splitting the name in the wrong place.
func TestSplitDN(t *testing.T) {
	for _, tc := range []struct{ in, rdn, superior string }{
		{"cn=Arno,ou=users,dc=x", "cn=Arno", "ou=users,dc=x"},
		{`cn=Meier\, Arno,ou=users,dc=x`, `cn=Meier\, Arno`, "ou=users,dc=x"},
		{"dc=x", "dc=x", ""},
		{" cn=A , ou=u,dc=x ", "cn=A", "ou=u,dc=x"},
		{"", "", ""},
		// A trailing backslash cannot escape anything; it must not run past the end.
		{`cn=odd\`, `cn=odd\`, ""},
	} {
		rdn, sup := splitDN(tc.in)
		if rdn != tc.rdn || sup != tc.superior {
			t.Errorf("splitDN(%q) = %q / %q, want %q / %q", tc.in, rdn, sup, tc.rdn, tc.superior)
		}
	}
}

func hasOp(ops []string, want string) bool { return hasDN(ops, want) }

func hasDN(vals []string, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}

// TestGoConnDirSync drives the real adapter's delta read against the test directory,
// so the DirSync control — request and response — crosses the wire rather than being
// assumed. The Conn fake the worker tests use never reaches this code.
func TestGoConnDirSync(t *testing.T) {
	d := startTestDirectory(t, &testDirectory{
		dirsync:     true,
		cookieOut:   []byte{0xDE, 0xAD, 0xBE, 0xEF},
		moreOut:     true,
		searchDN:    "cn=Arno,ou=users,dc=x",
		searchAttrs: map[string][]string{"cn": {"Arno"}, "isDeleted": {"TRUE"}},
	})
	conn, err := NewDialer().Dial(d.URL, "cn=svc,dc=x", "pw", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	res, err := conn.DirSync(DirSyncRequest{BaseDN: "dc=x", Filter: "(objectClass=user)"})
	if err != nil {
		t.Fatalf("DirSync: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0].DN != "cn=Arno,ou=users,dc=x" {
		t.Fatalf("entries = %+v", res.Entries)
	}
	if res.Entries[0].Attributes["isDeleted"][0] != "TRUE" {
		t.Errorf("the tombstone marker did not survive: %+v", res.Entries[0].Attributes)
	}
	if string(res.Cookie) != string([]byte{0xDE, 0xAD, 0xBE, 0xEF}) {
		t.Errorf("cookie = %v, want the server's", res.Cookie)
	}
	if !res.More {
		t.Error("more = false; the response control said there were further changes")
	}
}

// A server that answers without a DirSync control did not honour the request — most
// often the bind account cannot replicate directory changes. Returning the entries
// anyway would hand the process a full directory it believes to be a change set, so
// the connector refuses instead.
func TestGoConnDirSyncWithoutTheControl(t *testing.T) {
	d := startTestDirectory(t, &testDirectory{searchDN: "cn=Arno,dc=x"})
	conn, err := NewDialer().Dial(d.URL, "cn=svc,dc=x", "pw", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	_, err = conn.DirSync(DirSyncRequest{BaseDN: "dc=x"})
	if err == nil {
		t.Fatal("a response with no DirSync control must fail")
	}
	if !strings.Contains(err.Error(), "replicate directory changes") {
		t.Errorf("the error should name the likely cause, got: %v", err)
	}
}

// The cap bounds one pass. It costs nothing, because a pass is resumable: the cookie
// says where it got to.
func TestGoConnDirSyncEntryCap(t *testing.T) {
	d := startTestDirectory(t, &testDirectory{dirsync: true, searchDN: "cn=Arno,dc=x"})
	conn, err := NewDialer().Dial(d.URL, "cn=svc,dc=x", "pw", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.DirSync(DirSyncRequest{BaseDN: "dc=x", MaxEntries: 0}); err != nil {
		t.Fatalf("an unbounded pass should succeed: %v", err)
	}
	// The directory returns one entry, so a cap below that trips.
	if _, err := conn.DirSync(DirSyncRequest{BaseDN: "dc=x", MaxEntries: -1}); err != nil {
		t.Fatalf("a non-positive cap is unbounded: %v", err)
	}
}

// A server error on the delta read fails it rather than yielding an empty change set.
func TestGoConnDirSyncError(t *testing.T) {
	d := startTestDirectory(t, &testDirectory{dirsync: true, result: 32}) // noSuchObject
	conn, err := NewDialer().Dial(d.URL, "cn=svc,dc=x", "pw", false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.DirSync(DirSyncRequest{BaseDN: "dc=nope"}); err == nil {
		t.Error("a refused DirSync must be an error")
	}
}
