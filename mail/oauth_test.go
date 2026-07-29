package mail

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tokenServer is a fake OAuth2 token endpoint that records the last form it received
// and counts requests, returning a canned access token with the given TTL.
type tokenServer struct {
	srv      *httptest.Server
	lastForm map[string]string
	calls    int32
	ttl      int64
	status   int
	body     string // when set, returned verbatim instead of a token JSON
}

func newTokenServer(t *testing.T, ttl int64) *tokenServer {
	t.Helper()
	ts := &tokenServer{ttl: ttl, status: 200}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ts.calls, 1)
		_ = r.ParseForm()
		ts.lastForm = map[string]string{}
		for k := range r.PostForm {
			ts.lastForm[k] = r.PostForm.Get(k)
		}
		w.WriteHeader(ts.status)
		if ts.body != "" {
			_, _ = w.Write([]byte(ts.body))
			return
		}
		n := atomic.LoadInt32(&ts.calls)
		_, _ = w.Write([]byte(`{"access_token":"tok-` + strconv.Itoa(int(n)) + `","expires_in":` + strconv.FormatInt(ts.ttl, 10) + `}`))
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func TestClientCredentialsFetcher(t *testing.T) {
	ts := newTokenServer(t, 3600)
	src, err := newTokenSource(credentialBundle{
		Method: methodClientCredentials, TokenURL: ts.srv.URL, Scope: "https://graph.microsoft.com/.default",
		ClientID: "app-1", ClientSecret: "s3cr3t",
	}, http.DefaultClient, nil)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	tok, err := src.Token(context.Background())
	if err != nil || tok == "" {
		t.Fatalf("Token = %q, %v", tok, err)
	}
	if ts.lastForm["grant_type"] != "client_credentials" || ts.lastForm["client_id"] != "app-1" ||
		ts.lastForm["client_secret"] != "s3cr3t" || ts.lastForm["scope"] != "https://graph.microsoft.com/.default" {
		t.Errorf("token form = %v, want the client-credentials grant", ts.lastForm)
	}
}

func TestRefreshTokenFetcher(t *testing.T) {
	ts := newTokenServer(t, 3600)
	src, err := newTokenSource(credentialBundle{
		Method: methodRefreshToken, TokenURL: ts.srv.URL, Scope: "https://www.googleapis.com/auth/gmail.send",
		ClientID: "c", ClientSecret: "s", RefreshToken: "r3fresh",
	}, http.DefaultClient, nil)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if ts.lastForm["grant_type"] != "refresh_token" || ts.lastForm["refresh_token"] != "r3fresh" ||
		ts.lastForm["scope"] != "https://www.googleapis.com/auth/gmail.send" {
		t.Errorf("token form = %v, want the refresh-token grant", ts.lastForm)
	}
}

func TestServiceAccountFetcher(t *testing.T) {
	ts := newTokenServer(t, 3600)
	src, err := newTokenSource(credentialBundle{
		Method: methodServiceAccount, TokenURL: ts.srv.URL, Scope: "https://www.googleapis.com/auth/gmail.send",
		ClientEmail: "svc@project.iam.gserviceaccount.com", PrivateKey: testRSAKeyPEM(t), Subject: "user@example.com",
	}, http.DefaultClient, nil)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if ts.lastForm["grant_type"] != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("grant_type = %q, want jwt-bearer", ts.lastForm["grant_type"])
	}
	// The assertion is a three-part JWT whose claims carry iss/sub/aud.
	parts := strings.Split(ts.lastForm["assertion"], ".")
	if len(parts) != 3 {
		t.Fatalf("assertion has %d parts, want 3 (a JWT)", len(parts))
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]any
	_ = json.Unmarshal(claimsJSON, &claims)
	if claims["iss"] != "svc@project.iam.gserviceaccount.com" || claims["sub"] != "user@example.com" || claims["aud"] != ts.srv.URL {
		t.Errorf("JWT claims = %v, want iss/sub/aud set", claims)
	}
}

// TestTokenCaching proves a token is reused within its lifetime and re-fetched once it
// nears expiry (driven by an injected clock, so no wall-clock dependency).
func TestTokenCaching(t *testing.T) {
	ts := newTokenServer(t, 3600) // 1h tokens
	nowT := time.Unix(1_000_000, 0)
	clock := func() time.Time { return nowT }
	src, err := newTokenSource(credentialBundle{
		Method: methodClientCredentials, TokenURL: ts.srv.URL, ClientID: "c", ClientSecret: "s",
	}, http.DefaultClient, clock)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	first, _ := src.Token(context.Background())
	second, _ := src.Token(context.Background())
	if first != second || atomic.LoadInt32(&ts.calls) != 1 {
		t.Fatalf("within lifetime: calls=%d first=%q second=%q, want one fetch reused", ts.calls, first, second)
	}
	// Advance past (expiry - skew) → the next Token refreshes.
	nowT = nowT.Add(3600*time.Second - 30*time.Second)
	third, _ := src.Token(context.Background())
	if atomic.LoadInt32(&ts.calls) != 2 || third == first {
		t.Fatalf("after near-expiry: calls=%d third=%q, want a refresh", ts.calls, third)
	}
}

// TestTokenNoTTLNotCached proves a token with no expires_in is never reused.
func TestTokenNoTTLNotCached(t *testing.T) {
	ts := newTokenServer(t, 0) // no TTL
	src, _ := newTokenSource(credentialBundle{Method: methodClientCredentials, TokenURL: ts.srv.URL, ClientID: "c", ClientSecret: "s"}, http.DefaultClient, nil)
	_, _ = src.Token(context.Background())
	_, _ = src.Token(context.Background())
	if atomic.LoadInt32(&ts.calls) != 2 {
		t.Errorf("no-TTL token: calls=%d, want a fetch each time", ts.calls)
	}
}

func TestFetchTokenErrors(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		ts := newTokenServer(t, 3600)
		ts.status = 401
		ts.body = `{"error":"invalid_client"}`
		src, _ := newTokenSource(credentialBundle{Method: methodClientCredentials, TokenURL: ts.srv.URL, ClientID: "c", ClientSecret: "bad"}, http.DefaultClient, nil)
		if _, err := src.Token(context.Background()); err == nil {
			t.Error("want an error for a 401 token endpoint")
		}
	})
	t.Run("no access_token", func(t *testing.T) {
		ts := newTokenServer(t, 3600)
		ts.body = `{"expires_in":3600}`
		src, _ := newTokenSource(credentialBundle{Method: methodRefreshToken, TokenURL: ts.srv.URL, ClientID: "c", ClientSecret: "s", RefreshToken: "r"}, http.DefaultClient, nil)
		if _, err := src.Token(context.Background()); err == nil {
			t.Error("want an error when no access_token is returned")
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		src, _ := newTokenSource(credentialBundle{Method: methodClientCredentials, TokenURL: "http://127.0.0.1:0/token", ClientID: "c", ClientSecret: "s"}, http.DefaultClient, nil)
		if _, err := src.Token(context.Background()); err == nil {
			t.Error("want an error for an unreachable token endpoint")
		}
	})
}

func TestNewTokenSourceValidation(t *testing.T) {
	cases := map[string]credentialBundle{
		"no token url":            {Method: methodClientCredentials, ClientID: "c", ClientSecret: "s"},
		"client-creds missing":    {Method: methodClientCredentials, TokenURL: "http://x"},
		"refresh missing":         {Method: methodRefreshToken, TokenURL: "http://x", ClientID: "c"},
		"service-account missing": {Method: methodServiceAccount, TokenURL: "http://x", ClientEmail: "s@x"},
		"bad private key":         {Method: methodServiceAccount, TokenURL: "http://x", ClientEmail: "s@x", PrivateKey: "not a pem"},
		"unknown method":          {Method: "magic", TokenURL: "http://x"},
	}
	for name, b := range cases {
		if _, err := newTokenSource(b, http.DefaultClient, nil); err == nil {
			t.Errorf("%s: want an error, got nil", name)
		}
	}
}

// TestParseRSAPrivateKey covers the key-parsing branches: PKCS#8 (the Google format),
// PKCS#1, a non-RSA key, and unparseable input.
func TestParseRSAPrivateKey(t *testing.T) {
	if _, err := parseRSAPrivateKey(testRSAKeyPEM(t)); err != nil {
		t.Errorf("PKCS#8 RSA key: unexpected error %v", err)
	}
	// PKCS#1 ("RSA PRIVATE KEY").
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if _, err := parseRSAPrivateKey(string(pkcs1)); err != nil {
		t.Errorf("PKCS#1 RSA key: unexpected error %v", err)
	}
	// A valid PKCS#8 key that is not RSA (Ed25519) is rejected.
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	edDER, _ := x509.MarshalPKCS8PrivateKey(edPriv)
	edPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: edDER})
	if _, err := parseRSAPrivateKey(string(edPEM)); err == nil {
		t.Error("non-RSA key: want an error")
	}
	if _, err := parseRSAPrivateKey("-----BEGIN X-----\nbm90a2V5\n-----END X-----"); err == nil {
		t.Error("undecodable key: want an error")
	}
}

// TestFetchTokenBuildError covers fetchToken's request-build failure via a malformed
// token URL.
func TestFetchTokenBuildError(t *testing.T) {
	src, _ := newTokenSource(credentialBundle{Method: methodClientCredentials, TokenURL: "://not a url", ClientID: "c", ClientSecret: "s"}, http.DefaultClient, nil)
	if _, err := src.Token(context.Background()); err == nil {
		t.Error("malformed token URL: want a build error")
	}
}

// testRSAKeyPEM generates a fresh RSA key and returns it PKCS#8 PEM-encoded, as a
// Google service-account JSON carries its private_key.
func testRSAKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}
