package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTLSConfigured pins the both-or-neither rule. Naming one file without the
// other is a misconfiguration an operator wants to hear about at startup: the
// alternative is a server that silently serves plaintext on the port someone
// believed they had just secured.
func TestTLSConfigured(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cert     string
		key      string
		want     bool
		errWants string
	}{
		{name: "neither", want: false},
		{name: "both", cert: "/etc/atlas/tls.crt", key: "/etc/atlas/tls.key", want: true},
		{name: "cert only", cert: "/etc/atlas/tls.crt", errWants: "--tls-key"},
		{name: "key only", key: "/etc/atlas/tls.key", errWants: "--tls-cert"},
		// An environment variable set to nothing is unset, not a path.
		{name: "blank", cert: "  ", key: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tlsConfigured(tc.cert, tc.key)
			if tc.errWants != "" {
				if err == nil {
					t.Fatalf("tlsConfigured(%q, %q) = %v, nil; want an error naming %s", tc.cert, tc.key, got, tc.errWants)
				}
				if !strings.Contains(err.Error(), tc.errWants) {
					t.Errorf("error %q does not name %s, which is the flag the operator has to add", err, tc.errWants)
				}
				return
			}
			if err != nil {
				t.Fatalf("tlsConfigured(%q, %q): %v", tc.cert, tc.key, err)
			}
			if got != tc.want {
				t.Errorf("tlsConfigured(%q, %q) = %v, want %v", tc.cert, tc.key, got, tc.want)
			}
		})
	}
}

// TestNewCertReloaderRejectsAnUnusablePair fails the server at startup rather than
// at the first handshake, which is the difference between an operator seeing the
// problem while they are still watching and a monitor seeing it at 03:00.
func TestNewCertReloaderRejectsAnUnusablePair(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, _ := writeCertPair(t, dir, 1)

	if _, err := newCertReloader(filepath.Join(dir, "absent.crt"), keyFile); err == nil {
		t.Error("a certificate path that does not exist started the server")
	}
	// A certificate with somebody else's key parses fine and matches nothing.
	otherDir := t.TempDir()
	_, otherKey, _ := writeCertPair(t, otherDir, 2)
	if _, err := newCertReloader(certFile, otherKey); err == nil {
		t.Error("a certificate and a key from different pairs started the server")
	}
}

// TestCertReloaderServesTheConfiguredPair is the base case: what the handshake
// gets is the file the operator named.
func TestCertReloaderServesTheConfiguredPair(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, leaf := writeCertPair(t, dir, 7)

	r, err := newCertReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}
	got, err := r.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got.Leaf == nil || got.Leaf.SerialNumber.Cmp(leaf.SerialNumber) != 0 {
		t.Errorf("served serial %v, want %v", serialOf(got), leaf.SerialNumber)
	}
}

// TestCertReloaderPicksUpARenewal is the reason this is a callback over a cache
// rather than ListenAndServeTLS's filename arguments, which read the files once.
// A 90-day certificate renews about six times a year, and a renewal that needs a
// restart of a stateful engine is a renewal that does not happen.
func TestCertReloaderPicksUpARenewal(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, first := writeCertPair(t, dir, 11)

	r, err := newCertReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}
	if got, _ := r.GetCertificate(&tls.ClientHelloInfo{}); serialOf(got).Cmp(first.SerialNumber) != 0 {
		t.Fatalf("before renewal: serial %v, want %v", serialOf(got), first.SerialNumber)
	}

	_, _, renewed := writeCertPair(t, dir, 12) // same paths, a new pair
	got, err := r.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetCertificate after renewal: %v", err)
	}
	if serialOf(got).Cmp(renewed.SerialNumber) != 0 {
		t.Errorf("after renewal: serial %v, want %v — the renewed pair was not picked up", serialOf(got), renewed.SerialNumber)
	}
}

// TestCertReloaderKeepsTheOldPairWhenAReloadFails covers the renewal caught
// half-written: the cert file is already the new one and the key is still the old.
// Serving the certificate that is still valid beats refusing every handshake until
// somebody notices, so a failed reload is a warning and not an outage.
func TestCertReloaderKeepsTheOldPairWhenAReloadFails(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, first := writeCertPair(t, dir, 21)

	r, err := newCertReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}
	// Only the certificate is replaced; the key on disk still belongs to the old one.
	otherDir := t.TempDir()
	otherCert, otherKey, _ := writeCertPair(t, otherDir, 22)
	replaceFile(t, certFile, otherCert)

	got, err := r.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetCertificate refused the handshake after a failed reload: %v", err)
	}
	if serialOf(got).Cmp(first.SerialNumber) != 0 {
		t.Errorf("serial %v, want the previous %v — a failed reload must not replace a working certificate",
			serialOf(got), first.SerialNumber)
	}

	// Once the matching key lands, the pair is picked up: a failed reload must not
	// latch, or a renewal that completed a second later would never be served.
	replaceFile(t, keyFile, otherKey)
	if got, _ = r.GetCertificate(&tls.ClientHelloInfo{}); serialOf(got).Cmp(big.NewInt(22)) != 0 {
		t.Errorf("serial %v, want 22 — the completed renewal was not picked up", serialOf(got))
	}
}

// TestServerTLSConfigIsTLS13Only pins the choice that removes the configuration
// surface: TLS 1.3 fixes its cipher suites in the protocol, so there is no cipher
// list to expose and nothing an operator can weaken. A client that cannot get
// there is refused rather than quietly downgraded.
func TestServerTLSConfigIsTLS13Only(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, leaf := writeCertPair(t, dir, 31)

	cfg, err := newServerTLSConfig(certFile, keyFile)
	if err != nil {
		t.Fatalf("newServerTLSConfig: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = %#x, want TLS 1.3 (%#x)", cfg.MinVersion, tls.VersionTLS13)
	}
	if cfg.GetCertificate == nil {
		t.Error("GetCertificate is nil — the certificate would then be read once and never again")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				tlsConn := tls.Server(conn, cfg)
				_ = tlsConn.Handshake()
			}()
		}
	}()

	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"})
	if err != nil {
		t.Fatalf("a TLS 1.3 client could not connect: %v", err)
	}
	if v := conn.ConnectionState().Version; v != tls.VersionTLS13 {
		t.Errorf("negotiated %#x, want TLS 1.3 (%#x)", v, tls.VersionTLS13)
	}
	conn.Close()

	stuck, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		RootCAs: pool, ServerName: "127.0.0.1", MaxVersion: tls.VersionTLS12,
	})
	if err == nil {
		stuck.Close()
		t.Error("a client capped at TLS 1.2 was let in")
	}
}

// serialOf is the certificate's serial, or -1 when there is no leaf to read it
// from, so a failing assertion prints something instead of panicking.
func serialOf(cert *tls.Certificate) *big.Int {
	if cert == nil || cert.Leaf == nil {
		return big.NewInt(-1)
	}
	return cert.Leaf.SerialNumber
}

// writeCertPair writes a throwaway self-signed certificate for 127.0.0.1 and its
// key into dir as tls.crt/tls.key, and returns both paths and the leaf. Calling it
// twice on one directory overwrites the pair, which is what a renewal looks like
// from the server's side. Each write is stamped a second into the future so the
// change is visible whatever the file system's timestamp granularity is.
func writeCertPair(t *testing.T, dir string, serial int64) (certFile, keyFile string, leaf *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	if leaf, err = x509.ParseCertificate(der); err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certFile = filepath.Join(dir, "tls.crt")
	keyFile = filepath.Join(dir, "tls.key")
	writeStamped(t, certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeStamped(t, keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certFile, keyFile, leaf
}

// replaceFile copies src over dst, stamped so the change is visible.
func replaceFile(t *testing.T, dst, src string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	writeStamped(t, dst, b)
}

func writeStamped(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	ts := time.Now().Add(time.Second)
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatalf("stamp %s: %v", path, err)
	}
}

// TestTrustPoolAddsToTheHostRoots covers what --tls-ca is for. Without it, an
// Atlas whose certificate an internal CA issued is refused by another Atlas — the
// deployment target accepts the https:// URL (ADR-0129) and the request then fails
// at verification, which is later and further from the cause than the plaintext
// refusal it replaced. The bundle is *added* to the host's roots: it is not a
// replacement for them, and it is not a way to skip verification.
func TestTrustPoolAddsToTheHostRoots(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, _ := writeCertPair(t, dir, 51)

	// Unset means the host's roots and nothing else, which is what a nil pool says
	// to crypto/tls.
	pool, err := trustPool("")
	if err != nil {
		t.Fatalf("trustPool(\"\"): %v", err)
	}
	if pool != nil {
		t.Error("an unset --tls-ca built a pool; it must leave the system roots alone")
	}

	if _, err := trustPool(filepath.Join(dir, "absent.pem")); err == nil {
		t.Error("a --tls-ca path that does not exist was accepted")
	}
	notPEM := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notPEM, []byte("this is not a certificate\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := trustPool(notPEM); err == nil {
		t.Error("a --tls-ca file with no certificate in it was accepted")
	}

	// The certificate this pair serves is its own issuer, so trusting the bundle is
	// what makes the connection verify.
	cfg, err := newServerTLSConfig(certFile, keyFile)
	if err != nil {
		t.Fatalf("newServerTLSConfig: %v", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.(*tls.Conn).Handshake()
			}()
		}
	}()

	if pool, err = trustPool(certFile); err != nil {
		t.Fatalf("trustPool: %v", err)
	}
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"})
	if err != nil {
		t.Fatalf("a peer whose CA is in --tls-ca was still refused: %v", err)
	}
	conn.Close()

	// And the same connection without it is refused, which is the state this flag
	// exists to fix rather than to paper over.
	if conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{ServerName: "127.0.0.1"}); err == nil {
		conn.Close()
		t.Error("the peer verified against the host roots alone; the test proves nothing")
	}
}

// TestTLSPairs reads the two flags as lists. One pair is what every deployment had;
// several are for a server reached under names certified apart, and the key at the
// same place as its certificate is the only pairing there is, so a list that does
// not line up stops the server rather than serving a pair nobody meant
// (ADR-0562).
func TestTLSPairs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		certs      string
		keys       string
		want       []certPair
		errWants   string
		errWantsTo string
	}{
		{name: "neither"},
		{name: "blank", certs: " , ", keys: ""},
		{name: "one", certs: "/etc/atlas/tls.crt", keys: "/etc/atlas/tls.key",
			want: []certPair{{"/etc/atlas/tls.crt", "/etc/atlas/tls.key"}}},
		{name: "two", certs: "a.crt, b.crt", keys: "a.key,b.key",
			want: []certPair{{"a.crt", "a.key"}, {"b.crt", "b.key"}}},
		{name: "cert only", certs: "a.crt", errWants: "--tls-key"},
		{name: "key only", keys: "a.key", errWants: "--tls-cert"},
		{name: "a certificate without its key", certs: "a.crt,b.crt", keys: "a.key", errWants: "2 certificates", errWantsTo: "1 key"},
		{name: "a key without its certificate", certs: "a.crt", keys: "a.key,b.key", errWants: "1 certificate", errWantsTo: "2 keys"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tlsPairs(tc.certs, tc.keys)
			if tc.errWants != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errWants) || !strings.Contains(err.Error(), tc.errWantsTo) {
					t.Fatalf("tlsPairs(%q, %q) = %v, %v; want an error naming %q and %q", tc.certs, tc.keys, got, err, tc.errWants, tc.errWantsTo)
				}
				return
			}
			if err != nil {
				t.Fatalf("tlsPairs(%q, %q): %v", tc.certs, tc.keys, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("tlsPairs(%q, %q) = %v, want %v", tc.certs, tc.keys, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("pair %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// writeNamedCertPair is writeCertPair for a certificate made for names — DNS names,
// wildcards among them — written as <base>.crt and <base>.key, so one directory can
// hold the pairs of several names.
func writeNamedCertPair(t *testing.T, dir, base string, serial int64, names ...string) (certFile, keyFile string, leaf *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     names,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	if leaf, err = x509.ParseCertificate(der); err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certFile = filepath.Join(dir, base+".crt")
	keyFile = filepath.Join(dir, base+".key")
	writeStamped(t, certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeStamped(t, keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certFile, keyFile, leaf
}

// presentedTo dials addr asking for name and returns the serial of the certificate
// the server presented. It verifies nothing: the tests compare it with the files.
func presentedTo(t *testing.T, addr, name string) *big.Int {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: name, InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("dial asking for %q: %v", name, err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].SerialNumber
}

// handshakes answers TLS handshakes with cfg on a loopback port.
func handshakes(t *testing.T, cfg *tls.Config) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.(*tls.Conn).Handshake()
			}()
		}
	}()
	return ln.Addr().String()
}

// TestTheNameAskedForChoosesTheCertificate is what several pairs are for: a server
// reached as atlas.example.org and under the apps' own origin presents each client
// the certificate made for the name it asked for. A name no certificate is made
// for, and a client that names none — one that asks by address — get the first
// pair's, as a single pair answered everybody before
// (ADR-0562).
func TestTheNameAskedForChoosesTheCertificate(t *testing.T) {
	dir := t.TempDir()
	atlasCert, atlasKey, atlasLeaf := writeNamedCertPair(t, dir, "atlas", 31, "atlas.example.org")
	appsCert, appsKey, appsLeaf := writeNamedCertPair(t, dir, "apps", 32, "apps.example.com", "*.apps.example.com")

	cfg, err := newServerTLSConfig(atlasCert+","+appsCert, atlasKey+","+appsKey)
	if err != nil {
		t.Fatalf("newServerTLSConfig: %v", err)
	}
	addr := handshakes(t, cfg)
	for _, tc := range []struct {
		name string
		want *x509.Certificate
	}{
		{"atlas.example.org", atlasLeaf},
		{"apps.example.com", appsLeaf},
		{"orders.apps.example.com", appsLeaf},
		{"unknown.example.net", atlasLeaf},
		{"", atlasLeaf},
	} {
		if got := presentedTo(t, addr, tc.name); got.Cmp(tc.want.SerialNumber) != 0 {
			t.Errorf("asked for %q: presented serial %v, want %v", tc.name, got, tc.want.SerialNumber)
		}
	}

	// A renewal of one pair is served under its name and leaves the other alone.
	_, _, renewed := writeNamedCertPair(t, dir, "apps", 33, "apps.example.com", "*.apps.example.com")
	if got := presentedTo(t, addr, "apps.example.com"); got.Cmp(renewed.SerialNumber) != 0 {
		t.Errorf("after the renewal of one pair: presented serial %v, want %v", got, renewed.SerialNumber)
	}
	if got := presentedTo(t, addr, "atlas.example.org"); got.Cmp(atlasLeaf.SerialNumber) != 0 {
		t.Errorf("the pair that was not renewed: presented serial %v, want %v", got, atlasLeaf.SerialNumber)
	}
}

// TestNewServerTLSConfigRejectsAnyUnusablePair stops the server at startup when one
// of several pairs cannot be served, whichever one it is.
func TestNewServerTLSConfigRejectsAnyUnusablePair(t *testing.T) {
	dir := t.TempDir()
	atlasCert, atlasKey, _ := writeNamedCertPair(t, dir, "atlas", 41, "atlas.example.org")
	appsCert, _, _ := writeNamedCertPair(t, dir, "apps", 42, "apps.example.com")

	if _, err := newServerTLSConfig(atlasCert+","+appsCert, atlasKey+","+atlasKey); err == nil {
		t.Error("a second certificate with the first one's key started the server")
	}
	if _, err := newServerTLSConfig(atlasCert+","+appsCert, atlasKey); err == nil {
		t.Error("a second certificate without a key started the server")
	}
}
