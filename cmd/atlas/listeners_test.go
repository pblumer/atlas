package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestReachableOrigin covers what the startup log prints. Before TLS the answer was
// always loopbackURL(--addr), which is also what this process's children call back
// on. With a TLS listener those two part company: the children keep a plaintext
// loopback port nobody outside the process can use, so printing it would hand an
// operator a URL that does not work (ADR-0191).
func TestReachableOrigin(t *testing.T) {
	for _, tc := range []struct {
		name        string
		externalURL string
		addr        string
		tls         bool
		want        string
	}{
		{name: "plaintext", addr: ":8080", want: "http://127.0.0.1:8080"},
		{name: "tls", addr: ":8080", tls: true, want: "https://127.0.0.1:8080"},
		{name: "tls on every interface", addr: "0.0.0.0:9000", tls: true, want: "https://127.0.0.1:9000"},
		{name: "host given", addr: "10.0.0.5:8080", tls: true, want: "https://10.0.0.5:8080"},
		// Where the operator said what this server is reachable under, that is the
		// answer whatever the listener looks like from in here.
		{name: "external url", externalURL: "https://atlas.example.com", addr: ":8080", want: "https://atlas.example.com"},
		{name: "external url trailing slash", externalURL: "https://atlas.example.com/", addr: ":8080", tls: true, want: "https://atlas.example.com"},
		{name: "external url padded", externalURL: "  https://atlas.example.com  ", addr: ":8080", want: "https://atlas.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reachableOrigin(tc.externalURL, tc.addr, tc.tls); got != tc.want {
				t.Errorf("reachableOrigin(%q, %q, %v) = %q, want %q", tc.externalURL, tc.addr, tc.tls, got, tc.want)
			}
		})
	}
}

// TestInternalURL pins the half of ADR-0191 that is easiest to get wrong: the MCP
// loopback client and every supervised worker must keep a plaintext hop. Pointing
// them at the TLS port cannot work — a certificate issued for atlas.example.com
// carries no name for 127.0.0.1, so verification fails no matter which root they
// trust, and the only thing that would make it pass is the skip-verify switch this
// repository has decided twice not to have.
func TestInternalURL(t *testing.T) {
	// Without a TLS listener there is no second listener either, and the children
	// call back on the server's own address exactly as they did before.
	if got, want := internalURL(":8080", nil), "http://127.0.0.1:8080"; got != want {
		t.Errorf("internalURL(:8080, nil) = %q, want %q", got, want)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	got := internalURL(":8080", ln)
	if want := "http://" + ln.Addr().String(); got != want {
		t.Errorf("internalURL with a loopback listener = %q, want %q", got, want)
	}
	if _, port, _ := net.SplitHostPort(ln.Addr().String()); port == "8080" {
		t.Error("the loopback listener took the public port; it must be an ephemeral one")
	}
}

// TestServeUntilShutsDownEveryListener is the two-listener case shutting down as
// one: both stop inside the single grace period, and neither is left accepting.
func TestServeUntilShutsDownEveryListener(t *testing.T) {
	first, firstAddr := listening(t)
	second, secondAddr := listening(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveUntil(ctx, 5*time.Second, first, second) }()

	waitServing(t, firstAddr)
	waitServing(t, secondAddr)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveUntil: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveUntil did not return after the context was cancelled")
	}
	for _, addr := range []string{firstAddr, secondAddr} {
		if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			conn.Close()
			t.Errorf("%s still accepts connections after shutdown", addr)
		}
	}
}

// TestServeUntilFailsWhenOneListenerFails: a server that reached half of its
// interfaces is worse than one that stopped, so either listener failing ends the
// process rather than leaving the other one quietly serving.
func TestServeUntilFailsWhenOneListenerFails(t *testing.T) {
	healthy, healthyAddr := listening(t)
	boom := errors.New("bind: address already in use")
	// The sibling fails only once the healthy one is really serving. Failing
	// immediately would race Shutdown against a listener that has not been served
	// yet, and prove nothing about what happens to a listener that was.
	fail := make(chan struct{})
	broken := httpListener{srv: &http.Server{}, serve: func() error {
		<-fail
		return boom
	}}

	done := make(chan error, 1)
	go func() { done <- serveUntil(context.Background(), 5*time.Second, healthy, broken) }()
	waitServing(t, healthyAddr)
	close(fail)

	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("serveUntil = %v, want the listener's own error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveUntil did not return when a listener failed")
	}
	if conn, err := net.DialTimeout("tcp", healthyAddr, time.Second); err == nil {
		conn.Close()
		t.Error("the other listener is still accepting after its sibling failed")
	}
}

// listening builds an httpListener over a real loopback listener, and reports the
// address it answers on.
func listening(t *testing.T) (httpListener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	return httpListener{srv: srv, serve: func() error { return srv.Serve(ln) }}, ln.Addr().String()
}

// waitServing blocks until addr answers, so a test never races the goroutine that
// starts serving it.
func waitServing(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never started answering", addr)
}

// TestHTTPServerTimeouts pins the listener's timeout contract. A server built
// with only an address and a handler inherits Go's zero values, which mean "no
// limit": a client that opens a connection and dribbles a request header holds a
// connection indefinitely, and a keep-alive connection nobody reuses is never
// reclaimed. Neither is bounded by the request-body byte caps.
//
// The two zero values here are deliberate and are the reason this test states all
// four rather than only the ones that are set.
func TestHTTPServerTimeouts(t *testing.T) {
	for _, tc := range []struct {
		name string
		srv  *http.Server
	}{
		{"public", newHTTPServer(":8080", http.NewServeMux(), nil)},
		{"loopback", newHTTPServer("", http.NewServeMux(), nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.srv.ReadHeaderTimeout != readHeaderTimeout || tc.srv.ReadHeaderTimeout == 0 {
				t.Errorf("ReadHeaderTimeout = %v, want %v", tc.srv.ReadHeaderTimeout, readHeaderTimeout)
			}
			if tc.srv.IdleTimeout != idleTimeout || tc.srv.IdleTimeout == 0 {
				t.Errorf("IdleTimeout = %v, want %v", tc.srv.IdleTimeout, idleTimeout)
			}
			// A whole-request read deadline would cut off a slow but legitimate
			// restore upload, which is bounded by bytes rather than by seconds.
			if tc.srv.ReadTimeout != 0 {
				t.Errorf("ReadTimeout = %v, want 0: the body is bounded by its byte cap, not by a clock", tc.srv.ReadTimeout)
			}
			// A write deadline would cut off a long-polling worker and a streamed
			// backup, both of which hold a response open on purpose.
			if tc.srv.WriteTimeout != 0 {
				t.Errorf("WriteTimeout = %v, want 0: long polls and streamed backups hold a response open on purpose", tc.srv.WriteTimeout)
			}
		})
	}
}

// TestHTTPServerCarriesItsAddressAndTLS: the timeouts are added to the server, not
// substituted for what it is.
func TestHTTPServerCarriesItsAddressAndTLS(t *testing.T) {
	h := http.NewServeMux()
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	srv := newHTTPServer(":9443", h, cfg)
	if srv.Addr != ":9443" {
		t.Errorf("Addr = %q, want :9443", srv.Addr)
	}
	if srv.Handler == nil {
		t.Error("Handler was dropped")
	}
	if srv.TLSConfig != cfg {
		t.Error("TLSConfig was dropped")
	}
}
