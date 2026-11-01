package startgrant

import "testing"

func TestEffectiveGrantDenyByDefault(t *testing.T) {
	p := Principal{UserID: "alice", GroupIDs: []string{"team"}}
	for _, grants := range [][]Grant{nil, {}, {{ApplicationID: "app-a", ProcessID: "credit", UserIDs: []string{"bob"}}}} {
		if Allowed(grants, "app-a", "credit", p) {
			t.Fatal("ungranted start allowed")
		}
	}
}
func TestEffectiveGrantRequiresExactApplicationAndProcess(t *testing.T) {
	grants := []Grant{{ApplicationID: "app-a", ProcessID: "credit", UserIDs: []string{"alice"}}}
	p := Principal{UserID: "alice"}
	if !Allowed(grants, "app-a", "credit", p) {
		t.Fatal("valid grant refused")
	}
	for _, target := range [][2]string{{"app-b", "credit"}, {"app-a", "refund"}, {"", "credit"}, {"app-a", ""}} {
		if Allowed(grants, target[0], target[1], p) {
			t.Fatalf("cross-scope grant accepted: %v", target)
		}
	}
}
func TestEffectiveGroupAndRevocation(t *testing.T) {
	grants := []Grant{{ApplicationID: "app-a", ProcessID: "credit", GroupIDs: []string{"field"}}}
	p := Principal{UserID: "alice", GroupIDs: []string{"field"}}
	if !Allowed(grants, "app-a", "credit", p) {
		t.Fatal("group grant refused")
	}
	p.GroupIDs = []string{"other"}
	if Allowed(grants, "app-a", "credit", p) {
		t.Fatal("removed membership retained grant")
	}
	grants = nil
	p.GroupIDs = []string{"field"}
	if Allowed(grants, "app-a", "credit", p) {
		t.Fatal("revoked grant accepted")
	}
}
func TestEffectiveGrantRejectsAnonymousOrMalformed(t *testing.T) {
	grants := []Grant{{ApplicationID: "app-a", ProcessID: "credit", UserIDs: []string{"alice"}, GroupIDs: []string{"field"}}}
	for _, p := range []Principal{{}, {GroupIDs: []string{"field"}}, {UserID: " "}, {UserID: "bob", GroupIDs: []string{"other"}}} {
		if Allowed(grants, "app-a", "credit", p) {
			t.Fatalf("unexpected access: %+v", p)
		}
	}
	for _, g := range []Grant{{ApplicationID: "", ProcessID: "credit", UserIDs: []string{"alice"}}, {ApplicationID: "app-a", ProcessID: "", UserIDs: []string{"alice"}}} {
		if Allowed([]Grant{g}, "app-a", "credit", Principal{UserID: "alice"}) {
			t.Fatal("malformed grant accepted")
		}
	}
}
