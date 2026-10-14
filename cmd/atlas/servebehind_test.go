package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/pblumer/atlas/internal/trustedproxy"
)

// healthzThroughATCPBalancer does what a TCP load balancer with send-proxy does: a
// PROXY header in front of the stream, then the client's own TLS handshake and request.
// It reports the status, or the error that stopped it.
func healthzThroughATCPBalancer(addr string, pool *x509.CertPool) (int, error) {
	raw, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return 0, err
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := raw.Write([]byte("PROXY TCP4 203.0.113.5 10.0.0.9 51234 443\r\n")); err != nil {
		return 0, err
	}
	conn := tls.Client(raw, &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"})
	req, _ := http.NewRequest(http.MethodGet, "https://"+addr+"/healthz", nil)
	if err := req.Write(conn); err != nil {
		return 0, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// TestServeBehindATCPBalancer boots the real server the way the deployment that
// prompted ADR-0448 runs it: Atlas terminates TLS, a balancer forwards
// TCP in front of it. With the balancer listed, a PROXY header ahead of the handshake is
// read and the request is served; a client that connects without one — the balancer's
// own health check, or anything else from that address — is served as before.
func TestServeBehindATCPBalancer(t *testing.T) {
	proxies, err := trustedproxy.Parse("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	addr, pool := bootTLSBehind(t, 43, proxies)
	waitHealthy(t, tlsClient(pool, false), "https://"+addr+"/healthz")

	code, err := healthzThroughATCPBalancer(addr, pool)
	if err != nil || code != http.StatusOK {
		t.Fatalf("through the balancer: %d, %v; want 200", code, err)
	}
}

// TestServeWithoutTrustedProxiesHearsNoHeader: the same header from a peer nobody
// listed is the first bytes of a TLS stream, and the handshake refuses it. This is the
// half that keeps a client from naming its own address.
func TestServeWithoutTrustedProxiesHearsNoHeader(t *testing.T) {
	addr, pool := bootTLS(t, 44)
	waitHealthy(t, tlsClient(pool, false), "https://"+addr+"/healthz")

	if code, err := healthzThroughATCPBalancer(addr, pool); err == nil {
		t.Fatalf("a PROXY header was read from a peer nobody trusted (status %d)", code)
	}
}
