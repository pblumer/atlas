package oauth2_test

import (
	"context"
	"crypto"
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
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/oauth2"
)

// selfSigned makes the certificate and key an app registration would hold: a
// self-signed certificate is what Microsoft's own app-only guide for Exchange creates.
func selfSigned(t *testing.T) (*x509.Certificate, *rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "atlas-exchange"},
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	bundle := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return cert, key, bundle
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// TestClientCertificateSendsAVerifiableAssertion is the whole grant: the token endpoint
// receives a client assertion that names the certificate it was signed with, is signed
// by that certificate's key, and is addressed to that endpoint — the three things the
// Microsoft identity platform checks before it issues an app-only token.
func TestClientCertificateSendsAVerifiableAssertion(t *testing.T) {
	cert, key, _ := selfSigned(t)
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	f := oauth2.ClientCertificate(oauth2.CertificateConfig{
		HTTPClient: srv.Client(), Kind: "exchange", TokenURL: srv.URL + "/token",
		ClientID: "app-id", Certificate: cert, PrivateKey: key, Scope: "https://outlook.office365.com/.default",
	}, func() time.Time { return now })
	tok, ttl, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok != "tok" || ttl != time.Hour {
		t.Errorf("token %q ttl %v", tok, ttl)
	}
	for k, want := range map[string]string{
		"grant_type":            "client_credentials",
		"client_id":             "app-id",
		"client_assertion_type": "urn:ietf:params:oauth:client-assertion-type:jwt-bearer",
		"scope":                 "https://outlook.office365.com/.default",
	} {
		if got := form.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if form.Get("client_secret") != "" {
		t.Error("a certificate grant must not also send a client secret")
	}

	parts := strings.Split(form.Get("client_assertion"), ".")
	if len(parts) != 3 {
		t.Fatalf("assertion has %d parts", len(parts))
	}
	var header map[string]string
	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	if err := json.Unmarshal(hb, &header); err != nil {
		t.Fatal(err)
	}
	s1 := sha1.Sum(cert.Raw)
	s256 := sha256.Sum256(cert.Raw)
	if header["alg"] != "RS256" || header["x5t"] != b64(s1[:]) || header["x5t#S256"] != b64(s256[:]) {
		t.Errorf("header = %v", header)
	}
	var claims map[string]any
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["aud"] != srv.URL+"/token" || claims["iss"] != "app-id" || claims["sub"] != "app-id" {
		t.Errorf("claims = %v", claims)
	}
	if jti, _ := claims["jti"].(string); len(jti) < 16 {
		t.Errorf("jti %q is not a unique identifier", jti)
	}
	if claims["nbf"] != float64(now.Unix()) || claims["exp"] != float64(now.Add(10*time.Minute).Unix()) {
		t.Errorf("validity window nbf=%v exp=%v", claims["nbf"], claims["exp"])
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("the assertion does not verify against the certificate: %v", err)
	}
}

// TestEveryAssertionIsUnique: the identity platform rejects a replayed assertion, and two
// tokens fetched in the same second must not look like one.
func TestEveryAssertionIsUnique(t *testing.T) {
	cert, key, _ := selfSigned(t)
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seen = append(seen, r.PostForm.Get("client_assertion"))
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer srv.Close()
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	f := oauth2.ClientCertificate(oauth2.CertificateConfig{HTTPClient: srv.Client(), Kind: "exchange", TokenURL: srv.URL, ClientID: "a", Certificate: cert, PrivateKey: key}, func() time.Time { return now })
	for i := 0; i < 2; i++ {
		if _, _, err := f.Fetch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] == seen[1] {
		t.Error("two fetches sent the same assertion")
	}
}

func TestParseCertificateBundle(t *testing.T) {
	cert, _, bundle := selfSigned(t)
	gotCert, gotKey, err := oauth2.ParseCertificateBundle("exchange", bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !gotCert.Equal(cert) || gotKey == nil {
		t.Error("the bundle did not round-trip")
	}
	// Key first is as common as certificate first; PEM has no order.
	parts := strings.SplitAfter(bundle, "-----END CERTIFICATE-----\n")
	if _, _, err := oauth2.ParseCertificateBundle("exchange", parts[1]+parts[0]); err != nil {
		t.Errorf("key-first bundle: %v", err)
	}

	_, _, other := selfSigned(t)
	otherKey := other[strings.Index(other, "-----BEGIN PRIVATE KEY-----"):]
	for name, c := range map[string]struct{ pem, want string }{
		"empty":        {"", "no CERTIFICATE"},
		"no key":       {parts[0], "no private key"},
		"no cert":      {parts[1], "no CERTIFICATE"},
		"foreign key":  {parts[0] + otherKey, "does not belong"},
		"garbage cert": {"-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n" + parts[1], "certificate"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := oauth2.ParseCertificateBundle("exchange", c.pem)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "exchange:") {
				t.Errorf("err %q does not name the worker type", err)
			}
		})
	}
}
