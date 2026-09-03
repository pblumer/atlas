package oauth2_test

import (
	"crypto/ed25519"
	"crypto/rand"
)

// ed25519KeyPair is a valid PKCS#8 key that is not RSA — the one shape a
// service-account bundle can carry that parses and still cannot sign RS256.
func ed25519KeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}
