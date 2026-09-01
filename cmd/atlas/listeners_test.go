package main

import (
	"context"
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
