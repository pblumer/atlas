package identity

import "testing"

// TestUnrevokedCountsTheConnectionsAChangedRegistrationWouldStrand: per tenant, the
// connections of one provider that are not revoked — what an administrator is warned
// about before changing the app registration.
func TestUnrevokedCountsTheConnectionsAChangedRegistrationWouldStrand(t *testing.T) {
	r := newRig(t)
	alice, err := r.svc.Link(aliceLink())
	if err != nil {
		t.Fatal(err)
	}
	bob := aliceLink()
	bob.UserID, bob.Subject, bob.Username = "usr_bob", "oid-bob", "bob@contoso.example"
	if _, err := r.svc.Link(bob); err != nil {
		t.Fatal(err)
	}
	if got := r.svc.Unrevoked(ProviderMicrosoft); got["tid-1"] != 2 {
		t.Fatalf("unrevoked = %v, want two in tid-1", got)
	}
	if _, err := r.svc.Revoke(alice.ID, "usr_alice", false, "test"); err != nil {
		t.Fatal(err)
	}
	if got := r.svc.Unrevoked(ProviderMicrosoft); got["tid-1"] != 1 {
		t.Fatalf("after a revocation = %v, want one", got)
	}
	if got := r.svc.Unrevoked("other"); len(got) != 0 {
		t.Fatalf("another provider = %v", got)
	}
}
