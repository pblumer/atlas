package sharepoint

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A SharePoint app may name its certificate by thumbprint in the Windows certificate
// store, whose key signs there and is never read, as the Entra worker's may (ADR-0141,
// amended). The PEM certificate beside it is TestCertificateGrant's.

func spTestCertificate(t *testing.T) (*x509.Certificate, *rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "atlas-sharepoint"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	return cert, key, pemText
}

// stubCertStore stands in for the Windows certificate store and records what it was
// asked for, so the thumbprint path is tested on every platform.
func stubCertStore(t *testing.T, answer func(string) (*x509.Certificate, crypto.Signer, error)) *[]string {
	t.Helper()
	var asked []string
	prev := openStoreCertificate
	openStoreCertificate = func(tp string) (*x509.Certificate, crypto.Signer, error) {
		asked = append(asked, tp)
		return answer(tp)
	}
	t.Cleanup(func() { openStoreCertificate = prev })
	return &asked
}

// assertSignedBy checks that the token request authenticated with a client assertion
// signed by cert's key, and carried no client secret.
func assertSignedBy(t *testing.T, form map[string]string, cert *x509.Certificate) {
	t.Helper()
	if form["grant_type"] != "client_credentials" {
		t.Errorf("grant_type = %q, want client_credentials", form["grant_type"])
	}
	if form["client_assertion_type"] != "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" {
		t.Errorf("client_assertion_type = %q, want the RFC 7523 JWT bearer", form["client_assertion_type"])
	}
	if _, ok := form["client_secret"]; ok {
		t.Error("a certificate request also sent a client_secret")
	}
	parts := strings.Split(form["client_assertion"], ".")
	if len(parts) != 3 {
		t.Fatalf("client_assertion = %q, want a JWT", form["client_assertion"])
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("assertion signature: %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("the assertion is not signed by the certificate's key: %v", err)
	}
}

// The store is not asked while the client is built — the engine builds its clients on
// its run loop — but on the first token request, and the key it answers with signs.
func TestClientCredentialsWithAThumbprintAsksTheStoreOnFirstUse(t *testing.T) {
	cert, key, _ := spTestCertificate(t)
	asked := stubCertStore(t, func(string) (*x509.Certificate, crypto.Signer, error) { return cert, key, nil })
	ts := newTokenServer(t, 3600)
	src, err := newTokenSource(credentialBundle{
		Method: methodClientCredentials, TokenURL: ts.srv.URL, Scope: graphScope,
		ClientID: "app-1", CertificateThumbprint: " 02CB6038F2E91693AE8D31C983569366034B2100 ",
	}, http.DefaultClient, nil)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	if len(*asked) != 0 {
		t.Fatalf("the store was asked %d times while the client was built, want none", len(*asked))
	}
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	assertSignedBy(t, ts.lastForm, cert)
	if len(*asked) != 1 || (*asked)[0] != "02CB6038F2E91693AE8D31C983569366034B2100" {
		t.Errorf("the store was asked for %q, want the trimmed thumbprint once", *asked)
	}
}

// A certificate the store cannot give fails the token request with the store's reason,
// and is asked for again on the next one — so a certificate installed, or a key
// permission granted, after the failure is picked up without a restart.
func TestAThumbprintTheStoreCannotServeIsAskedForAgain(t *testing.T) {
	cert, key, _ := spTestCertificate(t)
	missing := true
	asked := stubCertStore(t, func(string) (*x509.Certificate, crypto.Signer, error) {
		if missing {
			return nil, nil, errors.New("certstore: no certificate with this thumbprint")
		}
		return cert, key, nil
	})
	ts := newTokenServer(t, 3600)
	src, err := newTokenSource(credentialBundle{
		Method: methodClientCredentials, TokenURL: ts.srv.URL, ClientID: "app-1",
		CertificateThumbprint: "02CB6038F2E91693AE8D31C983569366034B2100",
	}, http.DefaultClient, nil)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	if _, err := src.Token(context.Background()); err == nil || !strings.Contains(err.Error(), "no certificate") {
		t.Fatalf("Token err = %v, want the store's reason", err)
	}
	if n := atomic.LoadInt32(&ts.calls); n != 0 {
		t.Errorf("the token endpoint was called %d times without a certificate", n)
	}
	missing = false
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token after the certificate was installed: %v", err)
	}
	if len(*asked) != 2 {
		t.Errorf("the store was asked %d times, want twice", len(*asked))
	}
	// Once opened, the certificate is kept: its key is asked to sign per token, which
	// the store's signer does by opening the store itself.
	ts2 := newTokenServer(t, 0) // a token with no lifetime is fetched every time
	src2, _ := newTokenSource(credentialBundle{
		Method: methodClientCredentials, TokenURL: ts2.srv.URL, ClientID: "app-1",
		CertificateThumbprint: "02CB6038F2E91693AE8D31C983569366034B2100",
	}, http.DefaultClient, nil)
	*asked = nil
	for i := 0; i < 2; i++ {
		if _, err := src2.Token(context.Background()); err != nil {
			t.Fatalf("Token: %v", err)
		}
	}
	if n := atomic.LoadInt32(&ts2.calls); len(*asked) != 1 || n != 2 {
		t.Errorf("store asked %d times for %d token requests, want once", len(*asked), n)
	}
}

// One credential per app: two would leave the choice to an accident of precedence, so
// the bundle is refused naming each, and one with none names all three ways to give one.
// A bundle naming no method is a clientCredentials bundle and held to the same rule.
func TestClientCredentialsTakeExactlyOneCredential(t *testing.T) {
	_, _, pemText := spTestCertificate(t)
	stubCertStore(t, func(string) (*x509.Certificate, crypto.Signer, error) {
		t.Error("the store was asked although the bundle is refused")
		return nil, nil, errors.New("unreachable")
	})
	const tp = "02CB6038F2E91693AE8D31C983569366034B2100"
	for _, c := range []struct {
		name                     string
		secret, cert, thumbprint string
		want                     []string
	}{
		{"secret and certificate", "s", pemText, "", []string{"clientSecret", "certificate", "give one, not both"}},
		{"secret and thumbprint", "s", "", tp, []string{"clientSecret", "certificateThumbprint", "give one, not both"}},
		{"certificate and thumbprint", "", pemText, tp, []string{"certificate", "certificateThumbprint", "give one, not both"}},
		{"all three", "s", pemText, tp, []string{"clientSecret", "certificate", "certificateThumbprint", "give one of them"}},
		{"none", "", "", "", []string{"clientSecret", "certificate", "certificateThumbprint"}},
		{"blank thumbprint", "", "", "   ", []string{"certificateThumbprint"}},
	} {
		_, err := newTokenSource(credentialBundle{
			Method: methodClientCredentials, TokenURL: "http://x", ClientID: "c",
			ClientSecret: c.secret, Certificate: c.cert, CertificateThumbprint: c.thumbprint,
		}, http.DefaultClient, nil)
		if err == nil {
			t.Errorf("%s: accepted, want refused", c.name)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error %q does not name %s", c.name, err, w)
			}
		}
	}
}

func TestACertificateBundleThatCannotSignIsRefusedWhenBuilt(t *testing.T) {
	stubCertStore(t, func(string) (*x509.Certificate, crypto.Signer, error) {
		t.Error("the store was asked for a malformed thumbprint")
		return nil, nil, errors.New("unreachable")
	})
	for name, b := range map[string]credentialBundle{
		"not a PEM":            {Certificate: "not a certificate"},
		"thumbprint too short": {CertificateThumbprint: "02CB6038"},
	} {
		b.Method, b.TokenURL, b.ClientID = methodClientCredentials, "http://x", "c"
		if _, err := newTokenSource(b, http.DefaultClient, nil); err == nil {
			t.Errorf("%s: accepted, want refused", name)
		}
	}
}

// A certificate signs an app in as itself, which is the clientCredentials grant; the
// refresh-token grant acts for a person and keeps its client secret. A certificate in a
// refresh-token bundle is refused rather than silently ignored.
func TestARefreshTokenBundleRefusesACertificate(t *testing.T) {
	_, _, pemText := spTestCertificate(t)
	for name, b := range map[string]credentialBundle{
		"certificate": {Certificate: pemText},
		"thumbprint":  {CertificateThumbprint: "02CB6038F2E91693AE8D31C983569366034B2100"},
	} {
		b.Method, b.TokenURL, b.ClientID, b.ClientSecret, b.RefreshToken = methodRefreshToken, "http://x", "c", "s", "r"
		_, err := newTokenSource(b, http.DefaultClient, nil)
		if err == nil || !strings.Contains(err.Error(), "clientCredentials") {
			t.Errorf("%s: err = %v, want a refusal pointing at clientCredentials", name, err)
		}
	}
}

// A bundle that names a thumbprint builds a client without the store being asked:
// NewProviderClient is what the engine calls on its run loop.
func TestNewProviderClientWithAThumbprintDoesNotAskTheStore(t *testing.T) {
	asked := stubCertStore(t, func(string) (*x509.Certificate, crypto.Signer, error) {
		return nil, nil, errors.New("certstore: no certificate with this thumbprint")
	})
	// The second names no method, which is a clientCredentials bundle.
	for _, secret := range []string{
		`{"method":"clientCredentials","tenantId":"tid","clientId":"c","certificateThumbprint":"02CB6038F2E91693AE8D31C983569366034B2100"}`,
		`{"tenantId":"tid","clientId":"c","certificateThumbprint":"02CB6038F2E91693AE8D31C983569366034B2100"}`,
	} {
		if _, err := NewProviderClient(ProviderConfig{Secret: secret}); err != nil {
			t.Fatalf("NewProviderClient(%s): %v", secret, err)
		}
	}
	if len(*asked) != 0 {
		t.Errorf("the store was asked %d times while the client was built", len(*asked))
	}
}

func TestCertificateThumbprint(t *testing.T) {
	for raw, want := range map[string]string{
		`{"method":"clientCredentials","certificateThumbprint":" 02CB6038F2E91693AE8D31C983569366034B2100 "}`: "02CB6038F2E91693AE8D31C983569366034B2100",
		`{"method":"clientCredentials","clientSecret":"s"}`:                                                   "",
		`not json`: "",
		``:         "",
	} {
		if got := CertificateThumbprint(raw); got != want {
			t.Errorf("CertificateThumbprint(%q) = %q, want %q", raw, got, want)
		}
	}
}
