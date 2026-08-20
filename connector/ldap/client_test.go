package ldap

import (
	goldap "github.com/go-ldap/ldap/v3"
	"testing"
)

// TestGoDialerDialError covers the real dialer's connect-failure path: an unreachable
// server yields an error (the remaining bind/search/modify paths need a live
// directory and are exercised by integration, not unit tests).
func TestGoDialerDialError(t *testing.T) {
	if _, err := NewDialer().Dial("ldap://127.0.0.1:1", "cn=admin", "pw", false); err == nil {
		t.Fatal("Dial to an unreachable server: err = nil, want error")
	}
}

// TestScopeCode maps each authored scope name to its go-ldap constant and defaults an
// unknown value to the whole subtree.
func TestScopeCode(t *testing.T) {
	cases := map[string]int{
		"base":    goldap.ScopeBaseObject,
		"one":     goldap.ScopeSingleLevel,
		"sub":     goldap.ScopeWholeSubtree,
		"":        goldap.ScopeWholeSubtree,
		"unknown": goldap.ScopeWholeSubtree,
	}
	for scope, want := range cases {
		if got := scopeCode(scope); got != want {
			t.Errorf("scopeCode(%q) = %d, want %d", scope, got, want)
		}
	}
}

// TestHostOf extracts the host (no port) for the STARTTLS server name and yields ""
// for an unparseable URL.
func TestHostOf(t *testing.T) {
	if got := hostOf("ldaps://dir.example.com:636"); got != "dir.example.com" {
		t.Errorf("hostOf = %q, want dir.example.com", got)
	}
	if got := hostOf("ldap://host"); got != "host" {
		t.Errorf("hostOf = %q, want host", got)
	}
	if got := hostOf("://bad::"); got != "" {
		t.Errorf("hostOf(bad) = %q, want empty", got)
	}
}

// TestBuildSearchRequest defaults an empty filter and maps the scope.
func TestBuildSearchRequest(t *testing.T) {
	r := buildSearchRequest(SearchRequest{BaseDN: "dc=x", Scope: "one", Attributes: []string{"cn"}})
	if r.BaseDN != "dc=x" || r.Scope != goldap.ScopeSingleLevel || r.Filter != "(objectClass=*)" {
		t.Errorf("request = base %q scope %d filter %q, want dc=x/one/default filter", r.BaseDN, r.Scope, r.Filter)
	}
	if len(r.Attributes) != 1 || r.Attributes[0] != "cn" {
		t.Errorf("attributes = %v, want [cn]", r.Attributes)
	}
	if r2 := buildSearchRequest(SearchRequest{BaseDN: "dc=x", Filter: "(uid=ada)"}); r2.Filter != "(uid=ada)" {
		t.Errorf("filter = %q, want the given filter", r2.Filter)
	}
}

// TestEntriesFrom flattens a go-ldap result into connector entries.
func TestEntriesFrom(t *testing.T) {
	res := &goldap.SearchResult{Entries: []*goldap.Entry{
		goldap.NewEntry("uid=ada,dc=x", map[string][]string{"mail": {"ada@x"}, "objectClass": {"top", "person"}}),
	}}
	got := entriesFrom(res)
	if len(got) != 1 || got[0].DN != "uid=ada,dc=x" {
		t.Fatalf("entries = %+v, want one entry with the DN", got)
	}
	if len(got[0].Attributes["objectClass"]) != 2 || got[0].Attributes["mail"][0] != "ada@x" {
		t.Errorf("attributes = %v, want mapped multi-values", got[0].Attributes)
	}
	if n := entriesFrom(&goldap.SearchResult{}); len(n) != 0 {
		t.Errorf("empty result = %v, want no entries", n)
	}
}

// TestBuildAddRequest carries the DN and each attribute's values.
func TestBuildAddRequest(t *testing.T) {
	r := buildAddRequest("uid=ada,dc=x", map[string][]string{"objectClass": {"top", "person"}})
	if r.DN != "uid=ada,dc=x" {
		t.Errorf("DN = %q, want uid=ada,dc=x", r.DN)
	}
	var found bool
	for _, a := range r.Attributes {
		if a.Type == "objectClass" {
			found = true
			if len(a.Vals) != 2 || a.Vals[0] != "top" {
				t.Errorf("objectClass vals = %v, want [top person]", a.Vals)
			}
		}
	}
	if !found {
		t.Error("objectClass attribute not present in the add request")
	}
}

// TestBuildModifyRequest replaces each named attribute.
func TestBuildModifyRequest(t *testing.T) {
	r := buildModifyRequest("uid=ada", map[string][]string{"mail": {"new@x"}})
	if r.DN != "uid=ada" {
		t.Errorf("DN = %q, want uid=ada", r.DN)
	}
	if len(r.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(r.Changes))
	}
	ch := r.Changes[0]
	if ch.Operation != goldap.ReplaceAttribute || ch.Modification.Type != "mail" || ch.Modification.Vals[0] != "new@x" {
		t.Errorf("change = %+v, want a replace of mail", ch)
	}
}
