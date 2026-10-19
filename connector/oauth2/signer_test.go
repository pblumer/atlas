package oauth2_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/oauth2"
)

// opaqueSigner is a key the process cannot read — the shape of one held in the Windows
// certificate store, which signs on request and never hands the key over. It only
// exposes crypto.Signer, so nothing can reach for an *rsa.PrivateKey behind it.
type opaqueSigner struct {
	key   crypto.Signer
	calls int
}

func (s *opaqueSigner) Public() crypto.PublicKey { return s.key.Public() }

func (s *opaqueSigner) Sign(r io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	s.calls++
	return s.key.Sign(r, digest, opts)
}

// A certificate whose key only signs on request serves the grant as one held in memory
// does: the assertion verifies against the certificate (ADR-0172, amended).
func TestClientCertificateSignsThroughAnOpaqueKey(t *testing.T) {
	cert, key, _ := selfSigned(t)
	signer := &opaqueSigner{key: key}
	var assertion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		assertion = r.PostForm.Get("client_assertion")
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer srv.Close()

	f := oauth2.ClientCertificate(oauth2.CertificateConfig{
		HTTPClient: srv.Client(), Kind: "entra", TokenURL: srv.URL, ClientID: "app-id",
		Certificate: cert, PrivateKey: signer,
	}, func() time.Time { return time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC) })
	if _, _, err := f.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if signer.calls != 1 {
		t.Errorf("the key was asked to sign %d times, want 1", signer.calls)
	}
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion has %d parts", len(parts))
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("the assertion does not verify against the certificate: %v", err)
	}
}

// The assertion is RS256, so a key that is not RSA is refused before anything is sent,
// naming the Worker Type, rather than producing a signature the token endpoint rejects.
func TestClientCertificateRefusesAKeyThatIsNotRSA(t *testing.T) {
	cert, _, _ := selfSigned(t)
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	posted := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posted = true }))
	defer srv.Close()
	f := oauth2.ClientCertificate(oauth2.CertificateConfig{
		HTTPClient: srv.Client(), Kind: "entra", TokenURL: srv.URL, ClientID: "app-id",
		Certificate: cert, PrivateKey: &opaqueSigner{key: ec},
	}, nil)
	_, _, err = f.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "entra") || !strings.Contains(err.Error(), "RSA") {
		t.Errorf("err = %v, want a refusal naming entra and RSA", err)
	}
	if posted {
		t.Error("an assertion that cannot be RS256 was sent to the token endpoint")
	}
}
