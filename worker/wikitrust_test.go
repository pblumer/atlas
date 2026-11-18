package worker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// wikiBehindAnInternalCA is a wiki whose certificate an organisation's own authority
// signed, as one that terminates TLS itself inside a network has, and the path of
// that authority's certificate: the bundle --tls-ca names.
func wikiBehindAnInternalCA(t *testing.T) (url, caFile string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "internal CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "wiki"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" || r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"scope":"write","user":{"id":"u-bot","username":"bot"}}`))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	caFile = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return srv.URL, caFile
}

// TestTheWikiWorkerTrustsTheOperatorsCA covers what --tls-ca means for the
// wikiwikibangbang Worker Type: a wiki of the same operator, with a certificate their
// own authority signed, is reached once the worker is given that authority's bundle,
// and refused on the host's roots alone. The bundle is added to those roots and is no
// way around verification (ADR-0561).
func TestTheWikiWorkerTrustsTheOperatorsCA(t *testing.T) {
	wikiURL, caFile := wikiBehindAnInternalCA(t)
	env := func(ca string) func(string) string {
		return func(k string) string {
			switch k {
			case "ATLAS_WIKIWIKIBANGBANG_CONNECTORS":
				return "wiki"
			case "ATLAS_WIKIWIKIBANGBANG_WIKI_URL":
				return wikiURL
			case "ATLAS_WIKIWIKIBANGBANG_WIKI_CREDENTIALS":
				return `{"token":"wwbb_test-token-value"}`
			case "ATLAS_TLS_CA":
				return ca
			}
			return ""
		}
	}
	standing := func(ca string) (string, error) {
		reg, _, err := wikiRegistryFromEnv(env(ca))
		if err != nil {
			t.Fatalf("build the wiki registry: %v", err)
		}
		c, ok := reg.Client("wiki")
		if !ok {
			t.Fatal("the registry holds no wiki")
		}
		return c.Standing(context.Background())
	}

	if me, err := standing(caFile); err != nil || me != "u-bot" {
		t.Errorf("with the operator's CA: account %q, %v; want the wiki's answer", me, err)
	}
	if _, err := standing(""); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("on the host's roots alone: %v; want a certificate error", err)
	}

	// A bundle that cannot be used stops the worker at startup, where the cause is.
	_, _, err := wikiRegistryFromEnv(env(filepath.Join(t.TempDir(), "absent.pem")))
	if err == nil || !strings.Contains(err.Error(), "--tls-ca") {
		t.Errorf("a bundle that does not exist: %v; want an error naming --tls-ca", err)
	}
}
