package ad

import (
	"testing"
	"unicode/utf16"

	goldap "github.com/go-ldap/ldap/v3"
)

// TestEncodePassword checks the unicodePwd encoding: the password wrapped in double
// quotes, UTF-16LE, no BOM. Decoding the bytes back must yield the quoted password.
func TestEncodePassword(t *testing.T) {
	enc := encodePassword("Pa$$w0rd")
	b := []byte(enc)
	if len(b)%2 != 0 {
		t.Fatalf("encoded length %d is not even (not UTF-16)", len(b))
	}
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i < len(b); i += 2 {
		u16 = append(u16, uint16(b[i])|uint16(b[i+1])<<8) // little-endian
	}
	if got := string(utf16.Decode(u16)); got != `"Pa$$w0rd"` {
		t.Errorf("decoded = %q, want the quote-wrapped password", got)
	}
	if enc == "" || enc == `"Pa$$w0rd"` {
		t.Error("encodePassword returned the plain string, want UTF-16LE bytes")
	}
}

// TestAccountControl covers the enable/disable bit math: it flips ACCOUNTDISABLE while
// preserving other flags, and falls back to a normal-account baseline when the current
// value is missing or unparseable.
func TestAccountControl(t *testing.T) {
	cases := []struct {
		current []string
		disable bool
		want    string
	}{
		{nil, false, "512"},                 // baseline, enabled
		{nil, true, "514"},                  // baseline + ACCOUNTDISABLE
		{[]string{"514"}, false, "512"},     // clear the disable bit
		{[]string{"512"}, true, "514"},      // set the disable bit
		{[]string{"66048"}, true, "66050"},  // preserve DONT_EXPIRE_PASSWORD, add disable
		{[]string{"66050"}, false, "66048"}, // preserve it, clear disable
		{[]string{"garbage"}, false, "512"}, // unparseable → baseline
	}
	for _, c := range cases {
		if got := accountControl(c.current, c.disable); got != c.want {
			t.Errorf("accountControl(%v, disable=%v) = %q, want %q", c.current, c.disable, got, c.want)
		}
	}
}

// TestBuildModify maps each Mod to the right go-ldap change operation.
func TestBuildModify(t *testing.T) {
	req := buildModify("cn=g,dc=x", []Mod{
		{Op: modAdd, Attr: "member", Vals: []string{"cn=u,dc=x"}},
		{Op: modReplace, Attr: "userAccountControl", Vals: []string{"512"}},
		{Op: modDelete, Attr: "member", Vals: []string{"cn=old,dc=x"}},
	})
	if req.DN != "cn=g,dc=x" {
		t.Errorf("DN = %q, want cn=g,dc=x", req.DN)
	}
	if len(req.Changes) != 3 {
		t.Fatalf("changes = %d, want 3", len(req.Changes))
	}
	wantOps := []uint{goldap.AddAttribute, goldap.ReplaceAttribute, goldap.DeleteAttribute}
	for i, ch := range req.Changes {
		if ch.Operation != wantOps[i] {
			t.Errorf("change %d op = %d, want %d", i, ch.Operation, wantOps[i])
		}
	}
	if req.Changes[0].Modification.Type != "member" || req.Changes[0].Modification.Vals[0] != "cn=u,dc=x" {
		t.Errorf("first change = %+v, want add member cn=u,dc=x", req.Changes[0].Modification)
	}
}

// TestHostOf extracts the host for the STARTTLS server name.
func TestHostOf(t *testing.T) {
	if got := hostOf("ldaps://dc.example.com:636"); got != "dc.example.com" {
		t.Errorf("hostOf = %q, want dc.example.com", got)
	}
	if got := hostOf("://bad::"); got != "" {
		t.Errorf("hostOf(bad) = %q, want empty", got)
	}
}

// TestGoDialerDialError covers the real dialer's connect-failure path; the bind and
// operation paths need a live directory and are exercised by the worker tests through
// the Dialer interface.
func TestGoDialerDialError(t *testing.T) {
	if _, err := NewDialer().Dial("ldap://127.0.0.1:1", "cn=admin", "pw", false); err == nil {
		t.Fatal("Dial to an unreachable server: err = nil, want error")
	}
}
