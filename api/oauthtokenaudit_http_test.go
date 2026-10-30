package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A refused token request is recorded (ADR-0197, ADR-0200).
//
// A client whose renewal is refused stops working, and the person sees "the
// connection has expired" on the client's side. Before these records, nothing on
// this side said when, which client, or why — the one question an operator then
// has. What follows holds the record to that question, and to the rule every audit
// line here keeps: it carries no secret.

// tokenRefusals returns the auth.oauth_denied records the given endpoint wrote.
func tokenRefusals(t *testing.T, sink *syncBuffer, endpoint string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, rec := range auditLines(t, sink, "auth.oauth_denied") {
		if rec["endpoint"] == endpoint {
			out = append(out, rec)
		}
	}
	return out
}

// lastTokenRefusal returns the newest refusal of the endpoint, failing if there is none.
func lastTokenRefusal(t *testing.T, sink *syncBuffer, endpoint string) map[string]any {
	t.Helper()
	recs := tokenRefusals(t, sink, endpoint)
	if len(recs) == 0 {
		t.Fatalf("no auth.oauth_denied record for endpoint %q\n--- log ---\n%s", endpoint, sink.String())
	}
	return recs[len(recs)-1]
}

// TestARefusedTokenRequestIsAudited walks the refusals a working client meets —
// a wrong secret, a removed client, a rotated or foreign refresh token, a spent
// code, a grant type not offered — and holds each record to what an operator needs:
// the endpoint, the OAuth error, which client, and a reason the wire withholds.
func TestARefusedTokenRequestIsAudited(t *testing.T) {
	sink := captureLog(t)
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)
	otherID, otherSecret := registerSecondClient(t, admin, ts.URL)

	code := codeFrom(t, approve(t, admin, ts.URL, clientID, ts.URL+"/mcp", true))
	exchange := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {clientID}, "client_secret": {clientSecret},
	}
	status, first := postToken(t, ts.URL, exchange)
	if status != http.StatusOK {
		t.Fatalf("exchange = %d %v", status, first)
	}
	firstRefresh, _ := first["refresh_token"].(string)
	status, second := postToken(t, ts.URL, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {firstRefresh},
		"client_id": {clientID}, "client_secret": {clientSecret},
	})
	if status != http.StatusOK {
		t.Fatalf("first renewal = %d %v", status, second)
	}
	secondRefresh, _ := second["refresh_token"].(string)
	if n := len(tokenRefusals(t, sink, "token")); n != 0 {
		t.Fatalf("a successful exchange and renewal wrote %d refusals", n)
	}

	t.Run("a wrong client secret", func(t *testing.T) {
		postToken(t, ts.URL, url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {secondRefresh},
			"client_id": {clientID}, "client_secret": {"atlascs_" + strings.Repeat("0", 64)},
		})
		rec := lastTokenRefusal(t, sink, "token")
		if rec["level"] != "WARN" || rec["error"] != "invalid_client" || rec["client_id"] != clientID ||
			rec["reason"] != "wrong client secret" || rec["grant_type"] != "refresh_token" {
			t.Errorf("record = %v, want a WARN invalid_client naming the client and a wrong secret", rec)
		}
		if rec["client_ip"] == nil {
			t.Error("the record carries no client_ip")
		}
	})

	t.Run("a client that is not registered any more", func(t *testing.T) {
		postToken(t, ts.URL, url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {secondRefresh},
			"client_id": {"0123456789abcdef"}, "client_secret": {clientSecret},
		})
		rec := lastTokenRefusal(t, sink, "token")
		reason, _ := rec["reason"].(string)
		if rec["error"] != "invalid_client" || rec["client_id"] != "0123456789abcdef" || !strings.HasPrefix(reason, "unknown client") {
			t.Errorf("record = %v, want invalid_client naming the unknown client id", rec)
		}
	})

	t.Run("a secret sent as the client id", func(t *testing.T) {
		postToken(t, ts.URL, url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {secondRefresh},
			"client_id": {clientSecret}, "client_secret": {clientSecret},
		})
		rec := lastTokenRefusal(t, sink, "token")
		if _, ok := rec["client_id"]; ok {
			t.Errorf("record = %v: a value that is not a client id this server issues reached the log", rec)
		}
		if rec["reason"] != "not a client id this server issues" {
			t.Errorf("reason = %v, want the shape named", rec["reason"])
		}
	})

	t.Run("a refresh token already rotated away", func(t *testing.T) {
		status, out := postToken(t, ts.URL, url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {firstRefresh},
			"client_id": {clientID}, "client_secret": {clientSecret},
		})
		if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
			t.Fatalf("= %d %v, want 400 invalid_grant", status, out)
		}
		rec := lastTokenRefusal(t, sink, "token")
		reason, _ := rec["reason"].(string)
		if rec["error"] != "invalid_grant" || rec["client_id"] != clientID || !strings.Contains(reason, "rotated away") {
			t.Errorf("record = %v, want invalid_grant naming the client and the rotation among the reasons", rec)
		}
	})

	t.Run("another client's refresh token", func(t *testing.T) {
		postToken(t, ts.URL, url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {secondRefresh},
			"client_id": {otherID}, "client_secret": {otherSecret},
		})
		rec := lastTokenRefusal(t, sink, "token")
		if rec["error"] != "invalid_grant" || rec["client_id"] != otherID ||
			rec["reason"] != "the refresh token belongs to another client's grant" {
			t.Errorf("record = %v, want invalid_grant naming the presenting client and whose token it is not", rec)
		}
	})

	t.Run("a spent code", func(t *testing.T) {
		postToken(t, ts.URL, exchange)
		rec := lastTokenRefusal(t, sink, "token")
		if rec["error"] != "invalid_grant" || rec["grant_type"] != "authorization_code" ||
			rec["error_description"] != "the code is unknown, spent or expired" {
			t.Errorf("record = %v, want invalid_grant for a spent code", rec)
		}
	})

	t.Run("a grant type not offered", func(t *testing.T) {
		long := "urn:example:" + strings.Repeat("x", 200)
		postToken(t, ts.URL, url.Values{
			"grant_type": {long}, "client_id": {clientID}, "client_secret": {clientSecret},
		})
		rec := lastTokenRefusal(t, sink, "token")
		gt, _ := rec["grant_type"].(string)
		if rec["error"] != "unsupported_grant_type" || !strings.HasPrefix(long, strings.TrimSuffix(gt, "…")) || len(gt) > 80 {
			t.Errorf("record = %v, want unsupported_grant_type with the grant type clipped", rec)
		}
	})

	log := sink.String()
	for _, secret := range []string{
		clientSecret, otherSecret, code, testVerifier, firstRefresh, secondRefresh,
		first["access_token"].(string), second["access_token"].(string),
	} {
		if strings.Contains(log, secret) {
			t.Errorf("the log contains a secret (%.12s…)", secret)
		}
	}
}

// TestARefusedRevocationIsAudited: the revocation endpoint judges the client out
// loud (RFC 7009 §2.1), and that judgement is recorded like the token endpoint's;
// what it says about a token stays silent, so a token it does not know is no record.
func TestARefusedRevocationIsAudited(t *testing.T) {
	sink := captureLog(t)
	ts := newMCPServer(t)
	admin := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, admin, ts.URL)

	postRevoke(t, ts.URL, url.Values{
		"token": {"atlasor_" + strings.Repeat("0", 64)}, "client_id": {clientID}, "client_secret": {"wrong"},
	})
	rec := lastTokenRefusal(t, sink, "revoke")
	if rec["error"] != "invalid_client" || rec["client_id"] != clientID || rec["reason"] != "wrong client secret" {
		t.Errorf("record = %v, want invalid_client naming the client", rec)
	}

	postRevoke(t, ts.URL, url.Values{"client_id": {clientID}, "client_secret": {clientSecret}})
	rec = lastTokenRefusal(t, sink, "revoke")
	if rec["error"] != "invalid_request" || rec["error_description"] != "token is required" {
		t.Errorf("record = %v, want invalid_request for a missing token", rec)
	}

	before := len(tokenRefusals(t, sink, "revoke"))
	if status, _ := postRevoke(t, ts.URL, url.Values{
		"token": {"atlasor_" + strings.Repeat("1", 64)}, "client_id": {clientID}, "client_secret": {clientSecret},
	}); status != http.StatusOK {
		t.Fatalf("revoking an unknown token = %d, want 200", status)
	}
	if after := len(tokenRefusals(t, sink, "revoke")); after != before {
		t.Error("an unknown token at the revocation endpoint was recorded as a refusal; RFC 7009 answers it 200")
	}
	if strings.Contains(sink.String(), clientSecret) {
		t.Error("the log contains the client secret")
	}
}
