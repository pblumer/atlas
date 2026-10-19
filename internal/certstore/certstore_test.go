package certstore

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"strings"
	"testing"
	"time"
)

// A thumbprint is read as Windows shows it: hexadecimal in either case, and with the
// spaces and invisible characters a copy out of the certificate dialog brings along.
func TestParseThumbprint(t *testing.T) {
	want := [20]byte{0x02, 0xcb, 0x60, 0x38, 0xf2, 0xe9, 0x16, 0x93, 0xae, 0x8d, 0x31, 0xc9, 0x83, 0x56, 0x93, 0x66, 0x03, 0x4b, 0x21, 0x00}
	for _, in := range []string{
		"02CB6038F2E91693AE8D31C983569366034B2100",
		"02cb6038f2e91693ae8d31c983569366034b2100",
		"02 cb 60 38 f2 e9 16 93 ae 8d 31 c9 83 56 93 66 03 4b 21 00",
		"‎02CB6038F2E91693AE8D31C983569366034B2100‎",
		" 02:CB:60:38:F2:E9:16:93:AE:8D:31:C9:83:56:93:66:03:4B:21:00 ",
	} {
		got, err := ParseThumbprint(in)
		if err != nil {
			t.Errorf("ParseThumbprint(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseThumbprint(%q) = %x, want %x", in, got, want)
		}
	}
	for _, in := range []string{"", "02CB6038", "02CB6038F2E91693AE8D31C983569366034B210011", "not a thumbprint at all"} {
		if _, err := ParseThumbprint(in); err == nil {
			t.Errorf("ParseThumbprint(%q) succeeded, want an error", in)
		}
	}
}

// The message for a wrong thumbprint says what a thumbprint is, so the operator can see
// whether they pasted a serial number, a subject or half the string.
func TestAThumbprintThatIsNoneSaysWhatOneIs(t *testing.T) {
	_, err := ParseThumbprint("02CB6038")
	if err == nil || !strings.Contains(err.Error(), "40 hexadecimal") || !strings.Contains(err.Error(), "8") {
		t.Errorf("err = %v, want it to name the 40 characters a thumbprint has and the 8 it got", err)
	}
}

func selfSigned(t *testing.T, key crypto.Signer) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "atlas-certstore"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

type failingSigner struct{ crypto.Signer }

func (failingSigner) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) {
	return nil, errors.New("access denied")
}

// The key is tried once when the certificate is opened, so a key the worker's account
// cannot use — or one that is not the certificate's — is found at startup with the
// operator watching, and not at the first token request in the middle of a run.
func TestVerifyTriesTheKeyOnce(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cert := selfSigned(t, key)
	if err := verify(cert, key); err != nil {
		t.Errorf("the certificate's own key: %v", err)
	}
	if err := verify(cert, other); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Errorf("another key: err = %v, want it refused as not the certificate's", err)
	}
	if err := verify(cert, failingSigner{key}); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Errorf("a key that cannot sign: err = %v, want the reason passed on", err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(selfSigned(t, ec), ec); err == nil || !strings.Contains(err.Error(), "RSA") {
		t.Errorf("an EC certificate: err = %v, want it refused for not being RSA", err)
	}
}

// Open reads the thumbprint before it touches any store, on every platform.
func TestOpenRefusesAThumbprintThatIsNone(t *testing.T) {
	if _, _, err := Open("not a thumbprint"); err == nil || !strings.Contains(err.Error(), "40 hexadecimal") {
		t.Errorf("err = %v, want the thumbprint refused", err)
	}
}
