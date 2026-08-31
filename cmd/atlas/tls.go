package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/logging"
)

// tlsConfigured reports whether this server was asked to terminate TLS itself, and
// refuses the half-configured case. --tls-cert without --tls-key is not a server
// that falls back to plaintext: it is a server listening in the clear on the port
// somebody believed they had just secured, so it does not start (ADR-0191).
func tlsConfigured(certFile, keyFile string) (bool, error) {
	cert, key := strings.TrimSpace(certFile), strings.TrimSpace(keyFile)
	switch {
	case cert == "" && key == "":
		return false, nil
	case key == "":
		return false, errors.New("--tls-cert is set without --tls-key: name both or neither")
	case cert == "":
		return false, errors.New("--tls-key is set without --tls-cert: name both or neither")
	}
	return true, nil
}

// newServerTLSConfig is the whole of Atlas's TLS configuration surface.
//
// TLS 1.3 only, and that is the point of choosing it: crypto/tls fixes the suites
// for 1.3 in the protocol and ignores CipherSuites there, so there is no cipher
// list to expose as a flag, nothing an operator can weaken, and no
// CBC-versus-RC4 conversation with an auditor. A client that cannot negotiate 1.3
// is refused rather than quietly downgraded, and there is deliberately no
// --tls-min-version: if a real deployment needs one it is an amendment, added as a
// documented opt-in downgrade rather than by relaxing the default (ADR-0191).
func newServerTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	r, err := newCertReloader(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: r.GetCertificate}, nil
}

// certReloader hands the operator's certificate to each handshake, reloading the
// pair when either file changes on disk.
//
// The alternative is the filename arguments of ListenAndServeTLS, which read both
// files once at startup. A 90-day certificate renews about six times a year, and a
// renewal that needs a stateful engine restarted is a renewal that gets postponed
// or skipped. This is the one piece of the certificate lifecycle Atlas owns, and it
// is owned because the alternative is a restart (ADR-0191).
type certReloader struct {
	certFile, keyFile string

	mu   sync.Mutex
	cert *tls.Certificate
	// The modification times this pair was read at. A failed reload records the
	// times it failed on as well, so a pair that cannot be loaded is retried when
	// the files change again rather than re-read and re-logged on every handshake.
	certMod, keyMod time.Time
}

// newCertReloader loads the pair once, so a wrong path or a certificate and key
// that do not belong together stops the server at startup — while an operator is
// still watching — rather than at the first handshake.
func newCertReloader(certFile, keyFile string) (*certReloader, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile}
	certMod, keyMod, err := r.modTimes()
	if err != nil {
		return nil, err
	}
	cert, err := loadCertPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	r.cert, r.certMod, r.keyMod = cert, certMod, keyMod
	return r, nil
}

// GetCertificate serves the cached pair, reloading it first when either file has
// changed. Two stats per handshake is what never restarting for a renewal costs.
//
// A reload that fails is a warning and not an outage: the certificate already
// loaded is still valid, and refusing every handshake until somebody notices is
// the worse failure. The ordinary case is a renewal caught half-written — the
// certificate on disk is already the new one while the key is still the old, so
// the two do not match for a moment.
func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	certMod, keyMod, err := r.modTimes()
	if err != nil {
		logging.Warn(logging.ServerTLSReloadFailed,
			"TLS certificate files cannot be read; serving the pair already loaded",
			slog.String("error", err.Error()))
		return r.cert, nil
	}
	if certMod.Equal(r.certMod) && keyMod.Equal(r.keyMod) {
		return r.cert, nil
	}
	cert, err := loadCertPair(r.certFile, r.keyFile)
	if err != nil {
		r.certMod, r.keyMod = certMod, keyMod
		logging.Warn(logging.ServerTLSReloadFailed,
			"TLS certificate changed on disk but could not be loaded; serving the previous one",
			slog.String("error", err.Error()))
		return r.cert, nil
	}
	r.cert, r.certMod, r.keyMod = cert, certMod, keyMod
	logging.Info(logging.ServerTLSReloaded, "TLS certificate reloaded",
		slog.String("cert", r.certFile), slog.Time("not_after", cert.Leaf.NotAfter))
	return cert, nil
}

// modTimes stats both files. Either one being unreadable is one condition, not two:
// half a pair is no more usable than none of it.
func (r *certReloader) modTimes() (certMod, keyMod time.Time, err error) {
	cs, err := os.Stat(r.certFile)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("TLS certificate: %w", err)
	}
	ks, err := os.Stat(r.keyFile)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("TLS key: %w", err)
	}
	return cs.ModTime(), ks.ModTime(), nil
}

// loadCertPair reads the pair and keeps its parsed leaf, which is what lets the
// reload line say when the certificate now being served expires.
func loadCertPair(certFile, keyFile string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate %s with key %s: %w", certFile, keyFile, err)
	}
	if cert.Leaf == nil {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("parse TLS certificate %s: %w", certFile, err)
		}
		cert.Leaf = leaf
	}
	return &cert, nil
}

// trustPool returns the host's roots with the operator's CA bundle added, or nil
// when no bundle was named — nil being what crypto/tls reads as "the system roots".
//
// Added, never replacing: an internal CA is the reason this exists, and a
// deployment that also talks to something with a publicly issued certificate must
// not lose that by naming its own. And it is not a way around verification — there
// is deliberately no skip-verify switch anywhere here, because it would be the
// first thing reached for when a certificate is wrong, which is exactly when it
// must not be available (ADR-0129, ADR-0191).
func trustPool(caFile string) (*x509.CertPool, error) {
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
