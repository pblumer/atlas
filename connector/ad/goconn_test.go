package ad

import (
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
