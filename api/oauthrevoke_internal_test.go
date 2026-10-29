package api

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// TestAnExpiredAccessTokenStillGivesItsGrantBack (ADR-0510, RFC 7009).
//
// An access token runs out after two hours while its grant goes on: the refresh token
// still renews it. A connected system whose user disconnects may hold only the access
// token it last used, and by then it may have run out — so for giving the grant back
// it still names the grant, and presenting it ends the grant like a fresh one would.
// The expiry decides whether a request may proceed, never which grant is meant.
func TestAnExpiredAccessTokenStillGivesItsGrantBack(t *testing.T) {
	srv := newServerForErrors(t)
	const (
		clientSecret = oauthClientSecretPrefix + "c0ffee"
		access       = oauthAccessPrefix + "expired-but-current"
		refresh      = oauthRefreshPrefix + "still-renews"
	)
	client := oauthClient{ID: "wiki", Name: "Wiki", SecretHash: hashAPIToken(clientSecret),
		RedirectURIs: []string{"https://wiki.example/cb"}, CreatedAt: 1}
	grant := oauthGrant{ID: "g1", ClientID: client.ID, ClientName: client.Name,
		UserID: "u1", Username: "someone", Resource: "https://atlas.example/tasks",
		AccessHash: hashAPIToken(access), AccessExpires: time.Now().Add(-time.Hour).Unix(),
		RefreshHash: hashAPIToken(refresh), CreatedAt: 1}
	var err error
	srv.do(func() {
		if err = srv.oauthClientStore.Save(client); err != nil {
			return
		}
		srv.oauthClients.add(client)
		if err = srv.oauthGrantStore.Save(grant); err != nil {
			return
		}
		srv.oauthGrants.put(grant)
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, ok := srv.oauthGrants.matchAccess(access, time.Now().Unix()); ok {
		t.Fatal("the seeded access token still resolves; the premise is an expired one")
	}

	status, out, _ := oauthPost(t, srv.Handler(), oauthRevokePath, url.Values{
		"token": {access}, "client_id": {client.ID}, "client_secret": {clientSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("revoke = %d %v, want 200", status, out)
	}
	if _, ok := srv.oauthGrants.matchRefresh(refresh); ok {
		t.Error("the grant's refresh token still renews it after its expired access token gave it back")
	}
	var left []oauthGrant
	srv.do(func() { left, err = srv.oauthGrantStore.LoadAll() })
	if err != nil {
		t.Fatalf("load grants: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("%d grant(s) still stored; giving one back has to delete the durable record, "+
			"or a restart brings it back", len(left))
	}
}
