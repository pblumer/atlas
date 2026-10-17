package oauth2

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// signRS256 encodes header and claims as a compact JWT and signs it with key. Both
// grants that sign an assertion here — the Google service account's JWT-bearer grant
// and the certificate-authenticated client-credentials grant — share it, so there is
// one JWT signer in Atlas and not two. kind prefixes every error.
func signRS256(kind string, key *rsa.PrivateKey, header map[string]string, claims map[string]any) (string, error) {
	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("%s: encode JWT header: %w", kind, err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("%s: encode JWT claims: %w", kind, err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("%s: sign JWT assertion: %w", kind, err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
