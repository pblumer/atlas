package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
)

// Token revocation (RFC 7009, ADR-0510).
//
// A connected system that lets its people disconnect — a wiki whose user removes the
// link to their Atlas account — must be able to give the grant back, or the grant
// outlives the only copy of its refresh token and stands where nobody remembers it.
// These hold the three things the endpoint promises: presenting either token of a
// grant ends the whole grant; a client can end only its own; and nothing about a
// token is learned by presenting it.

const oauthRevokeTestPath = "/oauth/revoke"

// postRevoke calls the revocation endpoint and returns its status and raw body.
func postRevoke(t *testing.T, base string, form url.Values) (int, []byte) {
	t.Helper()
	resp, err := http.PostForm(base+oauthRevokeTestPath, form)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// tasksTokenPair runs the signed-in person through the flow for their tasks, as the wiki
// does, and returns both tokens.
func tasksTokenPair(t *testing.T, person *http.Client, base, clientID, clientSecret string) (access, refresh string) {
	t.Helper()
	code := codeFrom(t, approve(t, person, base, clientID, base+"/tasks", true))
	status, tok := postToken(t, base, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("exchange = %d: %v", status, tok)
	}
	access, _ = tok["access_token"].(string)
	refresh, _ = tok["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("exchange returned no tokens: %v", tok)
	}
	return access, refresh
}

// refreshStatus asks the token endpoint to renew a grant and returns the status and
// the OAuth error code, if any.
func refreshStatus(t *testing.T, base, clientID, clientSecret, refresh string) (int, string) {
	t.Helper()
	status, out := postToken(t, base, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	code, _ := out["error"].(string)
	return status, code
}

// grantCount is how many grants the signed-in person sees.
func grantCount(t *testing.T, c *http.Client, base string) int {
	t.Helper()
	resp, err := c.Get(base + "/api/v1/oauth-grants")
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	defer resp.Body.Close()
	var grants []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&grants); err != nil {
		t.Fatalf("decode grants: %v", err)
	}
	return len(grants)
}

// TestRevokingEitherTokenEndsTheWholeGrant: an access token and a refresh token are
// two halves of one approval, so giving back either one gives back the approval —
// the refresh token is refused at its next renewal and the access token at its next
// request, whichever of the two was presented, and whatever the hint said.
func TestRevokingEitherTokenEndsTheWholeGrant(t *testing.T) {
	for _, tc := range []struct {
		name, which, hint string
	}{
		{"refresh token", "refresh", "refresh_token"},
		{"access token", "access", "access_token"},
		{"refresh token under the wrong hint", "refresh", "access_token"},
		{"access token under a hint nobody defined", "access", "id_token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newMCPServer(t)
			admin := signedInClient(t, ts.URL)
			clientID, clientSecret := registerClient(t, admin, ts.URL)
			access, refresh := tasksTokenPair(t, admin, ts.URL, clientID, clientSecret)
			if got := bearerGet(t, ts.URL, "/api/v1/auth/me", access); got != http.StatusOK {
				t.Fatalf("the fresh access token was refused (%d)", got)
			}

			token := refresh
			if tc.which == "access" {
				token = access
			}
			status, body := postRevoke(t, ts.URL, url.Values{
				"token": {token}, "token_type_hint": {tc.hint},
				"client_id": {clientID}, "client_secret": {clientSecret},
			})
			if status != http.StatusOK {
				t.Fatalf("revoke = %d: %s, want 200", status, body)
			}

			if got := bearerGet(t, ts.URL, "/api/v1/auth/me", access); got != http.StatusUnauthorized {
				t.Errorf("the access token still reached the API with %d after its grant was given back, want 401", got)
			}
			if status, code := refreshStatus(t, ts.URL, clientID, clientSecret, refresh); status != http.StatusBadRequest || code != "invalid_grant" {
				t.Errorf("renewing a given-back grant = %d %q, want 400 invalid_grant", status, code)
			}
			if n := grantCount(t, admin, ts.URL); n != 0 {
				t.Errorf("the person still sees %d grant(s); giving one back has to end it where they look too", n)
			}

			// Given back twice is still an answer the client can act on: it is gone.
			if status, body := postRevoke(t, ts.URL, url.Values{
				"token": {token}, "client_id": {clientID}, "client_secret": {clientSecret},
			}); status != http.StatusOK {
				t.Errorf("revoking an already revoked token = %d: %s, want 200", status, body)
			}
		})
	}
}

// TestAClientCannotGiveBackAnotherClientsGrant: the credential that authenticates
// the request decides what it can end. A token of somebody else's grant is answered
// exactly like an unknown one, and the grant keeps working.
func TestAClientCannotGiveBackAnotherClientsGrant(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)
	otherID, otherSecret := registerSecondClient(t, admin, ts.URL)
	access, refresh := tasksTokenPair(t, admin, ts.URL, clientID, clientSecret)

	_, unknownBody := postRevoke(t, ts.URL, url.Values{
		"token":     {"atlasor_" + "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"},
		"client_id": {otherID}, "client_secret": {otherSecret},
	})
	for _, token := range []string{refresh, access} {
		status, body := postRevoke(t, ts.URL, url.Values{
			"token": {token}, "client_id": {otherID}, "client_secret": {otherSecret},
		})
		if status != http.StatusOK {
			t.Errorf("another client's token = %d: %s, want 200 — the answer must not say whose it is", status, body)
		}
		if string(body) != string(unknownBody) {
			t.Errorf("another client's token was answered %q, an unknown one %q — the difference "+
				"tells the caller the token is real", body, unknownBody)
		}
	}

	if got := bearerGet(t, ts.URL, "/api/v1/auth/me", access); got != http.StatusOK {
		t.Errorf("another client ended this grant: its access token now gets %d", got)
	}
	if status, code := refreshStatus(t, ts.URL, clientID, clientSecret, refresh); status != http.StatusOK {
		t.Errorf("another client ended this grant: renewing it = %d %q", status, code)
	}
}

// TestRevokingANonTokenAnswersOK: unknown, malformed, rotated-away or never-issued,
// the answer is the same 200 (RFC 7009 §2.2) — a client cannot do anything useful
// with an error about a token it no longer has, and a stranger learns nothing.
func TestRevokingANonTokenAnswersOK(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)
	_, refresh := tasksTokenPair(t, admin, ts.URL, clientID, clientSecret)

	// Renew once, so the first refresh token is one that was rotated away.
	status, renewed := postToken(t, ts.URL, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("renew = %d: %v", status, renewed)
	}
	current, _ := renewed["refresh_token"].(string)

	for _, token := range []string{
		refresh, // rotated away
		"atlasor_00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		"atlasoa_00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		"not a token at all",
		"atlas_" + "an-api-token-shape",
	} {
		if status, body := postRevoke(t, ts.URL, url.Values{
			"token": {token}, "client_id": {clientID}, "client_secret": {clientSecret},
		}); status != http.StatusOK {
			t.Errorf("revoke(%q) = %d: %s, want 200", token, status, body)
		}
	}
	// None of that touched the grant, whose current token still renews it.
	if status, code := refreshStatus(t, ts.URL, clientID, clientSecret, current); status != http.StatusOK {
		t.Errorf("revoking tokens that name nothing ended the grant: renewing it = %d %q", status, code)
	}
}

// TestRevocationJudgesTheClientOutLoud: the one thing the endpoint does say is
// whether the client is who it claims, in the token endpoint's own words — and a bad
// secret changes nothing, even when the token it carries is real.
func TestRevocationJudgesTheClientOutLoud(t *testing.T) {
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)
	access, refresh := tasksTokenPair(t, admin, ts.URL, clientID, clientSecret)

	for _, tc := range []struct {
		name string
		form url.Values
		want int
		code string
	}{
		{"wrong secret", url.Values{"token": {refresh}, "client_id": {clientID}, "client_secret": {"atlascs_wrong"}},
			http.StatusUnauthorized, "invalid_client"},
		{"no secret", url.Values{"token": {refresh}, "client_id": {clientID}},
			http.StatusUnauthorized, "invalid_client"},
		{"unknown client", url.Values{"token": {refresh}, "client_id": {"nope"}, "client_secret": {clientSecret}},
			http.StatusUnauthorized, "invalid_client"},
		{"no token", url.Values{"client_id": {clientID}, "client_secret": {clientSecret}},
			http.StatusBadRequest, "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := postRevoke(t, ts.URL, tc.form)
			var out map[string]any
			_ = json.Unmarshal(body, &out)
			if status != tc.want || out["error"] != tc.code {
				t.Errorf("revoke = %d %s, want %d %s", status, body, tc.want, tc.code)
			}
		})
	}
	if got := bearerGet(t, ts.URL, "/api/v1/auth/me", access); got != http.StatusOK {
		t.Errorf("a refused revocation ended the grant anyway: its access token now gets %d", got)
	}
}

// TestTheMetadataAdvertisesRevocation: a client finds the endpoint where it finds the
// others, with the one way it authenticates there.
func TestTheMetadataAdvertisesRevocation(t *testing.T) {
	ts := newMCPServer(t)
	status, _, doc := getJSON(t, ts.URL+"/.well-known/oauth-authorization-server")
	if status != http.StatusOK {
		t.Fatalf("metadata = %d, want 200", status)
	}
	if doc["revocation_endpoint"] != ts.URL+oauthRevokeTestPath {
		t.Errorf("revocation_endpoint = %v, want %q", doc["revocation_endpoint"], ts.URL+oauthRevokeTestPath)
	}
	methods, _ := doc["revocation_endpoint_auth_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "client_secret_post" {
		t.Errorf("revocation_endpoint_auth_methods_supported = %v, want [client_secret_post]",
			doc["revocation_endpoint_auth_methods_supported"])
	}
}
