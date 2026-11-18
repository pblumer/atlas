package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/pblumer/atlas/internal/trustroots"
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
	pairs, err := tlsPairs(certFile, keyFile)
	return len(pairs) > 0, err
}

// certPair names the files of one certificate: its chain and the key that belongs
// to it.
type certPair struct{ certFile, keyFile string }

// tlsPairs reads --tls-cert and --tls-key as lists separated by commas, the key at
// the same place as its certificate (ADR-0562). One of
// each is what every deployment had. Several are for a server reached under names
// that are certified apart; position is the only pairing there is, so lists of
// different lengths stop the server rather than serve a pair nobody meant.
func tlsPairs(certFiles, keyFiles string) ([]certPair, error) {
	certs, keys := splitFileList(certFiles), splitFileList(keyFiles)
	switch {
	case len(certs) == 0 && len(keys) == 0:
		return nil, nil
	case len(keys) == 0:
		return nil, errors.New("--tls-cert is set without --tls-key: name both or neither")
	case len(certs) == 0:
		return nil, errors.New("--tls-key is set without --tls-cert: name both or neither")
	case len(certs) != len(keys):
		return nil, fmt.Errorf("--tls-cert names %s and --tls-key names %s: name a key for every certificate, in the same order",
			counted(len(certs), "certificate"), counted(len(keys), "key"))
	}
	pairs := make([]certPair, len(certs))
	for i := range certs {
		pairs[i] = certPair{certFile: certs[i], keyFile: keys[i]}
	}
	return pairs, nil
}

// splitFileList splits a comma-separated list of paths, dropping what is empty: an
// environment variable set to nothing is unset, not a path.
func splitFileList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// counted is "1 certificate" or "2 certificates".
func counted(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
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
//
// certFiles and keyFiles are the two flags as given: one pair, or several separated
// by commas, of which the name a client asks for chooses one
// (ADR-0562).
func newServerTLSConfig(certFiles, keyFiles string) (*tls.Config, error) {
	pairs, err := tlsPairs(certFiles, keyFiles)
	if err != nil {
		return nil, err
	}
	if len(pairs) == 0 {
		return nil, errors.New("no TLS certificate named")
	}
	// Every pair is loaded now, so that a wrong path or a key that belongs to another
	// certificate stops the server at startup, whichever pair it is.
	chooser := make(certChooser, len(pairs))
	for i, p := range pairs {
		if chooser[i], err = newCertReloader(p.certFile, p.keyFile); err != nil {
			return nil, err
		}
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: chooser.GetCertificate}, nil
}

// certChooser hands each handshake the certificate made for the name the client
// asks for (SNI), out of the pairs the operator named, each reloaded as its files
// change.
//
// One certificate that lists every name was the only way to serve several, and it
// stops working where names are certified apart: the Console under the
// organisation's name and the hosted apps under an origin of their own (ADR-0204),
// by different authorities or on different renewal dates. The choice is the one
// crypto/tls makes for a list of certificates — the first one the client asked for
// by name and can verify — kept while the files may change underneath it.
//
// A name no certificate is made for, and a client that names none because it asks
// by address, get the first pair's certificate, which is what a single pair
// answered everybody with. Whether to trust it is the client's decision, and
// refusing the handshake instead would turn a certificate warning into a
// connection error that names nothing (ADR-0562).
type certChooser []*certReloader

func (c certChooser) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	current := make([]*tls.Certificate, len(c))
	for i, r := range c {
		// A reloader does not fail: it serves the pair it has when a reload does.
		current[i], _ = r.GetCertificate(hello)
	}
	if len(current) > 1 {
		for _, cert := range current {
			if hello.SupportsCertificate(cert) == nil {
				return cert, nil
			}
		}
	}
	return current[0], nil
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

// trustPool is [trustroots.Load]: the host's roots with the operator's CA bundle added,
// or nil when none was named. It lives in internal/trustroots so the worker command
// shares it (ADR-0535, migration step 2).
func trustPool(caFile string) (*x509.CertPool, error) { return trustroots.Load(caFile) }
