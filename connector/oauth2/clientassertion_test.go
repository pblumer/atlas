package oauth2_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/oauth2"
)

// testCertPEM issues a self-signed certificate for key and returns the certificate and
// the key as one PEM text, the shape an operator pastes into the vault.
func testCertPEM(t *testing.T, key crypto.Signer) string {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "atlas-entra-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return key
}

// TestParseCertificateKeyPair covers the one good shape and the three ways an operator
// gets it wrong. Every refusal names the kind and never quotes the PEM, because the
// message reaches a log and the PEM holds a private key.
func TestParseCertificateKeyPair(t *testing.T) {
	key := rsaKey(t)
	good := testCertPEM(t, key)
	cert, got, err := oauth2.ParseCertificateKeyPair("entra", good)
	if err != nil {
		t.Fatalf("certificate with its key: unexpected error %v", err)
	}
	if cert.Subject.CommonName != "atlas-entra-test" || !got.Equal(key) {
		t.Errorf("parsed %q and a key that equals the original: %v", cert.Subject.CommonName, got.Equal(key))
	}

	certOnly := good[:strings.Index(good, "-----BEGIN PRIVATE KEY-----")]
	otherKey, _ := x509.MarshalPKCS8PrivateKey(rsaKey(t))
	mismatched := certOnly + string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherKey}))

	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa key: %v", err)
	}
	for name, in := range map[string]string{
		"no key":            certOnly,
		"key of another":    mismatched,
		"not RSA":           testCertPEM(t, ec),
		"not PEM at all":    "certificate goes here",
		"key without a cer": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherKey})),
	} {
		_, _, err := oauth2.ParseCertificateKeyPair("entra", in)
		if err == nil {
			t.Errorf("%s: want an error, got nil", name)
			continue
		}
		if !strings.HasPrefix(err.Error(), "entra: ") {
			t.Errorf("%s: error %q does not name the kind", name, err)
		}
		if strings.Contains(err.Error(), "BEGIN") {
			t.Errorf("%s: error %q quotes the PEM", name, err)
		}
	}
}

// TestClientAssertionSignsAndExchanges is the whole grant against a token endpoint: a
// client-credentials form that authenticates with a signed assertion instead of a
// secret, a header that names the certificate by both thumbprints, the claims RFC 7523
// requires of a client, and a signature the certificate's public key verifies.
func TestClientAssertionSignsAndExchanges(t *testing.T) {
	form := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3599}`))
	}))
	defer srv.Close()

	cert, key, err := oauth2.ParseCertificateKeyPair("entra", testCertPEM(t, rsaKey(t)))
	if err != nil {
		t.Fatalf("ParseCertificateKeyPair: %v", err)
	}
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	f := oauth2.ClientAssertion(oauth2.ClientAssertionConfig{
		HTTPClient:  srv.Client(),
		Kind:        "entra",
		TokenURL:    srv.URL,
		ClientID:    "11111111-2222-3333-4444-555555555555",
		Certificate: cert,
		PrivateKey:  key,
		Scope:       "https://graph.microsoft.com/.default",
	}, func() time.Time { return now })

	tok, ttl, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if tok != "tok-1" || ttl != 3599*time.Second {
		t.Errorf("Fetch = %q, %v; want tok-1, 3599s", tok, ttl)
	}
	for field, want := range map[string]string{
		"grant_type":            "client_credentials",
		"client_id":             "11111111-2222-3333-4444-555555555555",
		"client_assertion_type": "urn:ietf:params:oauth:client-assertion-type:jwt-bearer",
		"scope":                 "https://graph.microsoft.com/.default",
	} {
		if form[field] != want {
			t.Errorf("form %s = %q; want %q", field, form[field], want)
		}
	}
	if _, ok := form["client_secret"]; ok {
		t.Error("a certificate grant must not send a client_secret")
	}

	parts := strings.Split(form["client_assertion"], ".")
	if len(parts) != 3 {
		t.Fatalf("assertion has %d parts; want 3", len(parts))
	}
	var header map[string]string
	var claims map[string]any
	decode := func(i int, into any) {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			t.Fatalf("decode part %d: %v", i, err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("unmarshal part %d: %v", i, err)
		}
	}
	decode(0, &header)
	decode(1, &claims)

	s1 := sha1.Sum(cert.Raw)
	s256 := sha256.Sum256(cert.Raw)
	for field, want := range map[string]string{
		"alg":      "RS256",
		"typ":      "JWT",
		"x5t":      base64.RawURLEncoding.EncodeToString(s1[:]),
		"x5t#S256": base64.RawURLEncoding.EncodeToString(s256[:]),
	} {
		if header[field] != want {
			t.Errorf("header %s = %q; want %q", field, header[field], want)
		}
	}
	for field, want := range map[string]any{
		"aud": srv.URL,
		"iss": "11111111-2222-3333-4444-555555555555",
		"sub": "11111111-2222-3333-4444-555555555555",
		"nbf": float64(now.Unix()),
		"iat": float64(now.Unix()),
		"exp": float64(now.Add(10 * time.Minute).Unix()),
	} {
		if claims[field] != want {
			t.Errorf("claim %s = %v; want %v", field, claims[field], want)
		}
	}
	if jti, _ := claims["jti"].(string); len(jti) != 32 {
		t.Errorf("jti = %v; want a 16-byte hex nonce", claims["jti"])
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("the certificate's public key does not verify the assertion: %v", err)
	}
}

// TestClientAssertionIsNeverReplayed: each fetch signs a new assertion with a new jti,
// so an assertion captured from one exchange is useless for the next.
func TestClientAssertionIsNeverReplayed(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seen = append(seen, r.PostForm.Get("client_assertion"))
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer srv.Close()
	cert, key, err := oauth2.ParseCertificateKeyPair("entra", testCertPEM(t, rsaKey(t)))
	if err != nil {
		t.Fatalf("ParseCertificateKeyPair: %v", err)
	}
	f := oauth2.ClientAssertion(oauth2.ClientAssertionConfig{
		HTTPClient: srv.Client(), Kind: "entra", TokenURL: srv.URL, ClientID: "app", Certificate: cert, PrivateKey: key,
	}, nil)
	for i := 0; i < 2; i++ {
		if _, _, err := f.Fetch(context.Background()); err != nil {
			t.Fatalf("Fetch %d: %v", i, err)
		}
	}
	if len(seen) != 2 || seen[0] == seen[1] {
		t.Errorf("two fetches sent %d assertions, identical: %v", len(seen), len(seen) == 2 && seen[0] == seen[1])
	}
}

// TestClientAssertionWithoutACertificateFailsBeforeTheNetwork: a misbuilt config is a
// local error naming the kind, not a request the token endpoint has to refuse.
func TestClientAssertionWithoutACertificateFailsBeforeTheNetwork(t *testing.T) {
	f := oauth2.ClientAssertion(oauth2.ClientAssertionConfig{Kind: "entra", TokenURL: "http://127.0.0.1:1/never", ClientID: "app"}, nil)
	_, _, err := f.Fetch(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "entra: ") {
		t.Errorf("want a local error naming the kind, got %v", err)
	}
}
