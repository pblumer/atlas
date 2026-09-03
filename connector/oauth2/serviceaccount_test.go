package oauth2_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/oauth2"
)

// testKeyPEM generates a fresh RSA key, PKCS#8 PEM-encoded — the shape a Google
// service-account JSON file carries its private key in.
func testKeyPEM(t *testing.T) string {
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

// TestParseRSAPrivateKey covers the formats a service-account key arrives in, and the
// two ways one can be wrong. The kind prefixes the error so an operator reading it
// still learns which connector refused the key.
func TestParseRSAPrivateKey(t *testing.T) {
	if _, err := oauth2.ParseRSAPrivateKey("sheets", testKeyPEM(t)); err != nil {
		t.Errorf("PKCS#8 RSA key: unexpected error %v", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if _, err := oauth2.ParseRSAPrivateKey("sheets", string(pkcs1)); err != nil {
		t.Errorf("PKCS#1 RSA key: unexpected error %v", err)
	}
	_, err = oauth2.ParseRSAPrivateKey("sheets", "not a pem at all")
	if err == nil || !strings.HasPrefix(err.Error(), "sheets: ") {
		t.Errorf("non-PEM input: want an error prefixed with the kind, got %v", err)
	}
	if _, err := oauth2.ParseRSAPrivateKey("sheets", "-----BEGIN X-----\nbm90a2V5\n-----END X-----"); err == nil {
		t.Error("unparseable PEM block: want an error, got nil")
	}
}

// TestParseRSAPrivateKeyRejectsNonRSA: a valid PKCS#8 key that is not RSA cannot sign
// an RS256 assertion, and saying so here beats a signing failure on the first call.
func TestParseRSAPrivateKeyRejectsNonRSA(t *testing.T) {
	_, edPriv, err := ed25519KeyPair()
	if err != nil {
		t.Fatalf("ed25519 key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(edPriv)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	edPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if _, err := oauth2.ParseRSAPrivateKey("sheets", string(edPEM)); err == nil {
		t.Error("an Ed25519 key: want an error, got nil")
	}
}

// TestServiceAccountSignsAndExchanges is the whole grant: the fetcher signs a JWT
// assertion with the service account's key and posts it as a jwt-bearer grant. The
// claims are what Google checks, so the test reads them back out of the assertion the
// token endpoint received.
func TestServiceAccountSignsAndExchanges(t *testing.T) {
	var gotGrant, gotAssertion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		gotGrant = r.Form.Get("grant_type")
		gotAssertion = r.Form.Get("assertion")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
	}))
	defer srv.Close()

	key, err := oauth2.ParseRSAPrivateKey("sheets", testKeyPEM(t))
	if err != nil {
		t.Fatalf("ParseRSAPrivateKey: %v", err)
	}
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	f := oauth2.ServiceAccount(oauth2.ServiceAccountConfig{
		HTTPClient:  srv.Client(),
		Kind:        "sheets",
		TokenURL:    srv.URL,
		ClientEmail: "atlas@example.iam.gserviceaccount.com",
		PrivateKey:  key,
		Scope:       "https://www.googleapis.com/auth/spreadsheets",
		Subject:     "person@example.com",
	}, func() time.Time { return now })

	tok, ttl, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if tok != "tok-1" || ttl != time.Hour {
		t.Errorf("Fetch = %q, %v; want tok-1, 1h", tok, ttl)
	}
	if gotGrant != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("grant_type = %q; want the jwt-bearer grant", gotGrant)
	}

	parts := strings.Split(gotAssertion, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion has %d parts; want 3", len(parts))
	}
	var claims map[string]any
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	for field, want := range map[string]any{
		"iss":   "atlas@example.iam.gserviceaccount.com",
		"sub":   "person@example.com",
		"aud":   srv.URL,
		"scope": "https://www.googleapis.com/auth/spreadsheets",
		"iat":   float64(now.Unix()),
		"exp":   float64(now.Add(time.Hour).Unix()),
	} {
		if claims[field] != want {
			t.Errorf("claim %s = %v; want %v", field, claims[field], want)
		}
	}
}

// TestServiceAccountOmitsSubjectWhenUnset: without domain-wide delegation the account
// acts as itself, and Google rejects an assertion carrying an empty sub.
func TestServiceAccountOmitsSubjectWhenUnset(t *testing.T) {
	var gotAssertion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotAssertion = r.Form.Get("assertion")
		_, _ = w.Write([]byte(`{"access_token":"t","expires_in":60}`))
	}))
	defer srv.Close()
	key, err := oauth2.ParseRSAPrivateKey("sheets", testKeyPEM(t))
	if err != nil {
		t.Fatalf("ParseRSAPrivateKey: %v", err)
	}
	f := oauth2.ServiceAccount(oauth2.ServiceAccountConfig{
		HTTPClient: srv.Client(), Kind: "sheets", TokenURL: srv.URL,
		ClientEmail: "a@b.iam.gserviceaccount.com", PrivateKey: key,
	}, nil)
	if _, _, err := f.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	parts := strings.Split(gotAssertion, ".")
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if strings.Contains(string(claims), `"sub"`) {
		t.Errorf("claims carry a sub with no subject configured: %s", claims)
	}
}
