//go:build !windows

package certstore

import (
	"crypto"
	"crypto/x509"
)

const supported = false

func open([20]byte) (*x509.Certificate, crypto.Signer, error) { return nil, nil, ErrUnsupported }
