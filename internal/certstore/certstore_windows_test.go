//go:build windows

package certstore

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"os/exec"
	"strings"
	"testing"
)

// storeCertificate creates a self-signed certificate in CurrentUser\My the way an
// administrator would, with a key that cannot be exported, and removes it with its key
// when the test ends. provider picks where the key lives: a CNG key storage provider, or
// a legacy CryptoAPI one.
func storeCertificate(t *testing.T, provider string) string {
	t.Helper()
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("powershell.exe is not available to create a test certificate")
	}
	script := `$ErrorActionPreference = 'Stop'
$c = New-SelfSignedCertificate -Subject 'CN=atlas-certstore-test' -CertStoreLocation Cert:\CurrentUser\My ` +
		`-KeyAlgorithm RSA -KeyLength 2048 -KeyExportPolicy NonExportable -KeySpec Signature ` +
		`-Provider '` + provider + `' -NotAfter (Get-Date).AddDays(1)
$c.Thumbprint`
	out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		t.Skipf("New-SelfSignedCertificate with %s failed on this runner: %v\n%s", provider, err, out)
	}
	tp := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		_ = exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command",
			`Remove-Item -Path Cert:\CurrentUser\My\`+tp+` -DeleteKey`).Run()
	})
	return tp
}

// A certificate in the store, its key non-exportable, signs through the store: the
// signature verifies against the certificate, and the key never left CNG.
func TestOpenSignsWithAKeyThatCannotBeExported(t *testing.T) {
	for _, provider := range []string{
		"Microsoft Software Key Storage Provider",
		"Microsoft Enhanced RSA and AES Cryptographic Provider",
	} {
		t.Run(provider, func(t *testing.T) {
			tp := storeCertificate(t, provider)
			cert, signer, err := Open(strings.ToLower(tp))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if cert.Subject.CommonName != "atlas-certstore-test" {
				t.Errorf("opened %q, want the test certificate", cert.Subject.CommonName)
			}
			digest := sha256.Sum256([]byte("a client assertion"))
			sig, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if err := rsa.VerifyPKCS1v15(cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, digest[:], sig); err != nil {
				t.Errorf("the signature does not verify against the certificate: %v", err)
			}
			if _, err := signer.Sign(rand.Reader, digest[:], &rsa.PSSOptions{Hash: crypto.SHA256}); err == nil {
				t.Error("a PSS signature was produced; only PKCS #1 v1.5 is supported")
			}
			if _, err := signer.Sign(rand.Reader, digest[:10], crypto.SHA256); err == nil {
				t.Error("a digest of the wrong length was signed")
			}
		})
	}
}

// A thumbprint no store holds is reported as such, naming both stores and the account
// whose CurrentUser store was searched.
func TestOpenReportsAThumbprintNoStoreHolds(t *testing.T) {
	_, _, err := Open("0000000000000000000000000000000000000001")
	if err == nil {
		t.Fatal("a certificate nobody created was found")
	}
	for _, want := range []string{`CurrentUser\My`, `LocalMachine\My`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}
