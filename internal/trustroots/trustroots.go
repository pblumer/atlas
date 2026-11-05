// Package trustroots builds the certificate pool a client verifies a server against:
// the host's roots, with an operator's internal CA bundle added (ADR-0129, ADR-0191).
package trustroots

import (
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// Load returns the host's roots with the operator's CA bundle added, or nil
// when no bundle was named — nil being what crypto/tls reads as "the system roots".
//
// Added, never replacing: an internal CA is the reason this exists, and a
// deployment that also talks to something with a publicly issued certificate must
// not lose that by naming its own. And it is not a way around verification — there
// is deliberately no skip-verify switch anywhere here, because it would be the
// first thing reached for when a certificate is wrong, which is exactly when it
// must not be available (ADR-0129, ADR-0191).
func Load(caFile string) (*x509.CertPool, error) {
	caFile = strings.TrimSpace(caFile)
	if caFile == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read --tls-ca: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("read the host's certificate authorities: %w", err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("--tls-ca %s holds no PEM certificate", caFile)
	}
	return pool, nil
}
