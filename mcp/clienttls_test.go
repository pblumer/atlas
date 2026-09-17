package mcp

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"
	"time"
)

// WithTLSRoots is a trust anchor, not a way around verification (ADR-0191), and it
// had no test. Two things about it are load-bearing and neither is obvious from the
// call site.

// A nil pool leaves the client exactly as it was, so a caller can pass one
// unconditionally — which is what every caller does.
func TestWithTLSRootsIgnoresANilPool(t *testing.T) {
	plain := NewClient("https://atlas.example")
	withNil := NewClient("https://atlas.example", WithTLSRoots(nil))
	if withNil.http.Transport != plain.http.Transport {
		t.Error("a nil pool replaced the transport; it must verify against the host's roots as before")
	}
	if withNil.http.Timeout != plain.http.Timeout {
		t.Errorf("timeout = %v, want the untouched %v", withNil.http.Timeout, plain.http.Timeout)
	}
}

// A real pool installs it — and carries the timeout across, which is the part a
// rewrite of this function would quietly drop: the option builds a *new*
// http.Client, so the 30s default lives or dies by one field being copied.
func TestWithTLSRootsKeepsTheTimeoutWhenItInstallsAPool(t *testing.T) {
	c := NewClient("https://atlas.example", WithTLSRoots(x509.NewCertPool()))
	if want := 30 * time.Second; c.http.Timeout != want {
		t.Errorf("timeout = %v after installing roots, want %v — a client with no timeout hangs on a silent peer", c.http.Timeout, want)
	}
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport carrying the roots", c.http.Transport)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil {
		t.Fatal("the pool was not installed as RootCAs")
	}
	if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, want TLS 1.2 — installing roots must not widen what is accepted", tr.TLSClientConfig.MinVersion)
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify is set; there is no skip-verify switch in Atlas (ADR-0191)")
	}
}
