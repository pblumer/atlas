package api

import (
	"testing"
)

// TestAFederatedLoginDoesNotTakeAnAddressItCannotVouchFor: the provider's e-mail
// claim was copied onto the account at every login, as it came. Two things about an
// address matter more than keeping it current. One account holds it: directory
// synchronisation, reconciliation and password recovery all read an address as the
// person, and the administrator's own path refuses a second account the same address
// — this path saved it without asking. And the provider may say outright that it has
// not verified it. An address somebody else holds, or one the provider marks
// unverified, is not written; the account keeps the address it had.
func TestAFederatedLoginDoesNotTakeAnAddressItCannotVouchFor(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)

	// A colleague's account, with their address.
	var saveErr error
	srv.do(func() {
		saveErr = srv.users.Save(User{ID: "usr_colleague", Username: "colleague", Email: "colleague@example.org",
			Roles: []string{RoleUser}, CreatedAt: 1, UpdatedAt: 1})
	})
	if saveErr != nil {
		t.Fatalf("saving the colleague's account: %v", saveErr)
	}

	first := federate(t, ts, idp, noRedirects(t))
	first.Body.Close()
	before, ok := accountBySubject(t, srv, "subject-1")
	if !ok || before.Email == "" {
		t.Fatalf("the first login created no account with an address: %+v", before)
	}

	for _, tc := range []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{"an address another account holds", map[string]any{"email": "colleague@example.org"}, before.Email},
		{"the same address in another case", map[string]any{"email": "Colleague@Example.org"}, before.Email},
		{"an address the provider marks unverified", map[string]any{"email": "new@example.org", "email_verified": false}, before.Email},
		{"unverified, said as a string", map[string]any{"email": "new@example.org", "email_verified": "false"}, before.Email},
		{"an address the provider verified", map[string]any{"email": "verified@example.org", "email_verified": true}, "verified@example.org"},
		{"an address from a provider that does not say", map[string]any{"email": "plain@example.org"}, "plain@example.org"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idp.mutate = func(_, c map[string]any) {
				delete(c, "email_verified")
				for k, v := range tc.claims {
					c[k] = v
				}
			}
			resp := federate(t, ts, idp, noRedirects(t))
			resp.Body.Close()
			after, ok := accountBySubject(t, srv, "subject-1")
			if !ok {
				t.Fatal("the login lost the account")
			}
			if after.Email != tc.want {
				t.Errorf("the account's address is %q, want %q", after.Email, tc.want)
			}
			before = after
		})
	}
	// The colleague's address is still the colleague's alone.
	var holder User
	var found bool
	srv.do(func() { holder, found, _ = srv.users.byEmail("colleague@example.org") })
	if !found || holder.ID != "usr_colleague" {
		t.Errorf("colleague@example.org now belongs to %q (found %v), want the colleague", holder.ID, found)
	}
}
