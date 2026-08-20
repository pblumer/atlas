package ldap

import (
	"strings"
	"testing"
)

// dialTest opens a real GoDialer connection against the in-process directory and
// closes it when the test ends.
func dialTest(t *testing.T, d *testDirectory, bindDN, password string) Conn {
	t.Helper()
	conn, err := NewDialer().Dial(d.URL, bindDN, password, false)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestGoDialerBindsAndSearches drives the production adapter end to end: it dials
// a real socket, performs a simple bind, and reads a search result back off the
// wire — the path every LDAP connector task takes and that the worker's fakes
// deliberately skip.
func TestGoDialerBindsAndSearches(t *testing.T) {
	d := startTestDirectory(t, &testDirectory{entries: []Entry{
		{DN: "uid=ada,ou=users,dc=example,dc=com", Attributes: map[string][]string{
			"mail":        {"ada@example.com"},
			"objectClass": {"top", "person"},
		}},
	}})
	conn := dialTest(t, d, "cn=svc,dc=example,dc=com", "secret")

	got, err := conn.Search(SearchRequest{BaseDN: "ou=users,dc=example,dc=com", Scope: "sub", Filter: "(uid=ada)"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}
	if got[0].DN != "uid=ada,ou=users,dc=example,dc=com" {
		t.Errorf("DN = %q, want the entry's DN", got[0].DN)
	}
	if v := got[0].Attributes["mail"]; len(v) != 1 || v[0] != "ada@example.com" {
		t.Errorf("mail = %v, want [ada@example.com]", v)
	}
	if v := got[0].Attributes["objectClass"]; len(v) != 2 {
		t.Errorf("objectClass = %v, want both values", v)
	}

	ops, dns := d.seen()
	if len(ops) != 2 || ops[0] != "bind" || ops[1] != "search" {
		t.Fatalf("operations = %v, want [bind search]", ops)
	}
	if dns[0] != "cn=svc,dc=example,dc=com" {
		t.Errorf("bind DN = %q, want the configured bind DN", dns[0])
	}
	if dns[1] != "ou=users,dc=example,dc=com" {
		t.Errorf("search base = %q, want the base DN", dns[1])
	}
}

// TestGoDialerAnonymousBind covers the documented "empty bind DN leaves the
// connection anonymous" branch: no bind is sent at all.
func TestGoDialerAnonymousBind(t *testing.T) {
	d := startTestDirectory(t, nil)
	conn := dialTest(t, d, "", "")

	if _, err := conn.Search(SearchRequest{BaseDN: "dc=example,dc=com"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	ops, _ := d.seen()
	if len(ops) != 1 || ops[0] != "search" {
		t.Errorf("operations = %v, want [search] with no bind", ops)
	}
}

// TestGoConnWriteOperations drives add, modify, delete and the RFC 3062
// password-modify extended operation, asserting each reaches the directory with
// the DN it was given.
func TestGoConnWriteOperations(t *testing.T) {
	d := startTestDirectory(t, nil)
	conn := dialTest(t, d, "cn=svc", "secret")

	if err := conn.Add("uid=neu,dc=example,dc=com", map[string][]string{"objectClass": {"top", "person"}, "cn": {"Neu"}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := conn.Modify("uid=neu,dc=example,dc=com", map[string][]string{"mail": {"neu@example.com"}}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if err := conn.SetPassword("uid=neu,dc=example,dc=com", "geheim"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := conn.Delete("uid=neu,dc=example,dc=com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	ops, dns := d.seen()
	want := []string{"bind", "add", "modify", "extended", "delete"}
	if len(ops) != len(want) {
		t.Fatalf("operations = %v, want %v", ops, want)
	}
	for i, w := range want {
		if ops[i] != w {
			t.Fatalf("operations = %v, want %v", ops, want)
		}
	}
	// add, modify and delete each carry the DN; the extended request keeps it in
	// its own request value, which this server does not decode.
	for _, i := range []int{1, 2, 4} {
		if dns[i] != "uid=neu,dc=example,dc=com" {
			t.Errorf("%s DN = %q, want uid=neu,dc=example,dc=com", ops[i], dns[i])
		}
	}
}

// TestGoConnOperationErrors covers each adapter method's failure branch: the
// directory answers a non-zero result code, and the connector wraps it with the
// operation and DN so a parked incident names what failed.
func TestGoConnOperationErrors(t *testing.T) {
	// 1 = operationsError; any non-zero code takes the same path.
	d := startTestDirectory(t, &testDirectory{result: 1})
	conn := dialTest(t, d, "cn=svc", "secret")

	cases := map[string]func() error{
		"ldap: search":          func() error { _, err := conn.Search(SearchRequest{BaseDN: "dc=x"}); return err },
		"ldap: add":             func() error { return conn.Add("uid=a,dc=x", map[string][]string{"cn": {"A"}}) },
		"ldap: modify":          func() error { return conn.Modify("uid=a,dc=x", map[string][]string{"cn": {"A"}}) },
		"ldap: delete":          func() error { return conn.Delete("uid=a,dc=x") },
		"ldap: modify-password": func() error { return conn.SetPassword("uid=a,dc=x", "pw") },
	}
	for wantPrefix, call := range cases {
		err := call()
		if err == nil {
			t.Errorf("%s: err = nil, want the directory's error", wantPrefix)
			continue
		}
		if !strings.HasPrefix(err.Error(), wantPrefix) {
			t.Errorf("error = %q, want it to start with %q", err, wantPrefix)
		}
	}
}

// TestGoDialerBindError covers the bind-failure branch: a rejected credential
// closes the connection and names the bind DN, which is what an operator sees
// when a service account's password was rotated.
func TestGoDialerBindError(t *testing.T) {
	d := startTestDirectory(t, &testDirectory{bindResult: 49}) // invalidCredentials
	_, err := NewDialer().Dial(d.URL, "cn=svc,dc=example,dc=com", "wrong", false)
	if err == nil {
		t.Fatal("Dial with a rejected bind: err = nil, want error")
	}
	if !strings.Contains(err.Error(), "bind cn=svc,dc=example,dc=com") {
		t.Errorf("error = %q, want it to name the bind DN", err)
	}
}

// TestGoDialerStartTLSError covers the STARTTLS-failure branch: a server that
// refuses the upgrade fails the dial rather than silently continuing in the
// clear, which would send the bind password unencrypted.
func TestGoDialerStartTLSError(t *testing.T) {
	d := startTestDirectory(t, &testDirectory{result: 1})
	_, err := NewDialer().Dial(d.URL, "cn=svc", "secret", true)
	if err == nil {
		t.Fatal("Dial with a refused STARTTLS: err = nil, want error")
	}
	if !strings.Contains(err.Error(), "starttls") {
		t.Errorf("error = %q, want it to name starttls", err)
	}
}
