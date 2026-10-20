// Package certstore signs with a private key held in the Windows certificate store,
// named by its certificate's SHA-1 thumbprint, without ever reading the key out of the
// store (ADR-0172, amended).
//
// # Why
//
// An app registration's certificate usually lives in the store of the host that signs
// in with it, its key marked non-exportable — the property that makes it hard to steal.
// That is the certificate PowerShell's Connect-MgGraph -CertificateThumbprint, the Teams
// Phone Worker and every script before them use. A key that cannot be exported cannot be
// handed to a Worker as PEM either, so the only way a Go Worker can use the same
// certificate is to ask the store to sign, which is what this package does: it finds the
// certificate, and its signer hands each digest to CNG (NCryptSignHash).
//
// # The rules
//
// The store is searched as Connect-MicrosoftTeams and the Teams Phone Worker search it:
// CurrentUser\My of the account the process runs as, then LocalMachine\My. The key is
// tried once when the certificate is opened, so a key the account may not use, or one
// that is not the certificate's, is a startup error rather than a failed token request
// in the middle of a run. The key must be a CNG key — every key a current Windows
// creates or imports is one — and RSA, because the client assertion is RS256.
package certstore

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Supported says whether this platform has a certificate store to sign with.
const Supported = supported

// ErrUnsupported is Open's answer outside Windows.
var ErrUnsupported = errors.New("certstore: a certificate thumbprint names a certificate in the Windows certificate store, and this process is not running on Windows; give the certificate and its key as PEM instead")

// notHex is what a copy of a thumbprint out of the certificate dialog brings along:
// spaces, colons, and an invisible left-to-right mark.
var notHex = regexp.MustCompile(`[^0-9A-Fa-f]`)

// ParseThumbprint reads a SHA-1 thumbprint as Windows shows it: forty hexadecimal
// characters in either case, whatever else surrounds them.
func ParseThumbprint(s string) ([20]byte, error) {
	var tp [20]byte
	digits := notHex.ReplaceAllString(s, "")
	if len(digits) != 2*len(tp) {
		return tp, fmt.Errorf("certstore: a certificate thumbprint has 40 hexadecimal characters, this one has %d", len(digits))
	}
	_, err := hex.Decode(tp[:], []byte(strings.ToLower(digits)))
	return tp, err
}

// Open finds the certificate with this thumbprint and returns it with a signer for its
// private key, tried once already. Outside Windows it answers ErrUnsupported.
func Open(thumbprint string) (*x509.Certificate, crypto.Signer, error) {
	tp, err := ParseThumbprint(thumbprint)
	if err != nil {
		return nil, nil, err
	}
	return open(tp)
}

// verify signs a fixed digest with s and checks the signature against cert.
func verify(cert *x509.Certificate, s crypto.Signer) error {
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("the certificate does not hold an RSA key, and the client assertion is RS256")
	}
	digest := sha256.Sum256([]byte("atlas certstore: is this the certificate's key, and may this account use it?"))
	sig, err := s.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return fmt.Errorf("its private key cannot sign for this account: %w", err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return errors.New("the private key found with it does not belong to it")
	}
	return nil
}
