package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/promquery"
	"github.com/pblumer/atlas/tracing"
)

// TestServeTerminatesTLSAndKeepsTheLoopbackHopPlaintext boots the real server with
// a certificate and checks the two halves of ADR-0191 that only meet at runtime:
// --addr speaks TLS 1.3 and nothing else, and this process's own children still
// reach it over a plaintext loopback port.
//
// The MCP call is what makes the second half observable from outside. The adapter
// is a pure proxy back into this server's own HTTP API (ADR-0016), so a tool call
// arriving over TLS only answers if the loopback client was handed a URL it can
// actually use. Point that client at the TLS port instead and this test fails,
// which is the mistake the record says is the easiest one to make here.
func TestServeTerminatesTLSAndKeepsTheLoopbackHopPlaintext(t *testing.T) {
	addr, pool := bootTLS(t, 41)
	client := tlsClient(pool, false)
	waitHealthy(t, client, "https://"+addr+"/healthz")

	// Nothing is served in the clear on the port the certificate is on. Without
	// this, "TLS is configured" and "TLS is what the port speaks" are two claims.
	// Go's TLS server answers a plaintext request with a 400 rather than dropping
	// it, so what matters is that the request was refused and not served.
	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr + "/healthz"); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Error("the TLS listener served a plaintext request")
		}
	}

	// A tool call over the TLS listener, answered through the loopback hop.
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call",` +
		`"params":{"name":"atlas_info","arguments":{}}}`)
	resp, err := client.Post("https://"+addr+"/mcp", "application/json", body)
	if err != nil {
		t.Fatalf("MCP tool call over TLS: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP tool call: %s — %s", resp.Status, got)
	}
	// The adapter reports a failed loopback call as an error result rather than an
	// HTTP status, so the body is where a broken internal URL would show up.
	if strings.Contains(string(got), `"isError":true`) || !strings.Contains(string(got), "Atlas") {
		t.Errorf("MCP tool call answered %s — the loopback hop did not work", got)
	}
}

// TestServeStreamsOverHTTP2 covers the one behavioural change this record accepts
// rather than chooses: configuring TLS turns HTTP/2 on, because net/http negotiates
// h2 over ALPN whenever TLSNextProto is nil. What that lands on is the endpoints
// shaped around HTTP/1.1's six-connections-per-origin limit — the collaboration
// session stream (ADR-0140) and the log tail — which are the least covered by unit
// tests, so this asserts h2 is really negotiated and that a server-sent stream over
// it still arrives, frame and flush included.
//
// If h2 ever goes badly here, the escape hatch is a non-nil empty TLSNextProto on
// the server, and this test is what would say so.
func TestServeStreamsOverHTTP2(t *testing.T) {
	addr, pool := bootTLS(t, 43)
	client := tlsClient(pool, true)
	waitHealthy(t, client, "https://"+addr+"/healthz")

	resp, err := client.Post("https://"+addr+"/api/v1/drafts", "application/xml", strings.NewReader(draftBPMN))
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save draft: %s — %s", resp.Status, body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/api/v1/drafts/wip-order/session", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	stream, err := client.Do(req)
	if err != nil {
		t.Fatalf("open the session stream: %v", err)
	}
	defer stream.Body.Close()

	if stream.Proto != "HTTP/2.0" {
		t.Errorf("negotiated %s, want HTTP/2.0 — ALPN did not offer h2", stream.Proto)
	}
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("session stream: %s", stream.Status)
	}
	if ct := stream.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want an event stream", ct)
	}
	// The first frame is written and flushed before anything else happens on the
	// session, so it arrives on a stream that is still open — which is the part a
	// broken h2 path would swallow.
	scan := bufio.NewScanner(stream.Body)
	for scan.Scan() {
		if strings.HasPrefix(scan.Text(), "data:") {
			return
		}
	}
	t.Fatalf("no frame arrived on the session stream: %v", scan.Err())
}

// draftBPMN is the smallest diagram that can be saved: a process with an id, which
// is what a draft is keyed by.
const draftBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="wip-order" name="Order fulfillment">
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
  </bpmn:process>
</bpmn:definitions>`

// bootTLS starts the real server with a throwaway certificate and returns the
// address it listens on together with the pool that trusts it.
func bootTLS(t *testing.T, serial int64) (addr string, pool *x509.CertPool) {
	t.Helper()
	dir := t.TempDir()
	certFile, keyFile, leaf := writeCertPair(t, dir, serial)
	addr = freeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveForTest(ctx, addr, filepath.Join(dir, "data"), tlsConfig{certFile: certFile, keyFile: keyFile})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve returned %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("serve did not return after its context was cancelled")
		}
	})

	pool = x509.NewCertPool()
	pool.AddCert(leaf)
	return addr, pool
}

// tlsClient trusts pool and nothing else. Setting TLSClientConfig by hand turns
// Go's automatic HTTP/2 off, so h2 is asked for explicitly where it is the subject.
func tlsClient(pool *x509.CertPool, http2 bool) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"},
			ForceAttemptHTTP2: http2,
		},
	}
}

// TestServeRefusesHalfATLSPair keeps a misconfiguration from starting a server that
// listens in the clear on the port somebody believed they had just secured.
func TestServeRefusesHalfATLSPair(t *testing.T) {
	dir := t.TempDir()
	certFile, _, _ := writeCertPair(t, dir, 42)

	err := serveForTest(context.Background(), freeAddr(t), filepath.Join(dir, "data"), tlsConfig{certFile: certFile})
	if err == nil {
		t.Fatal("serve started with a certificate and no key")
	}
	if !strings.Contains(err.Error(), "--tls-key") {
		t.Errorf("error %q does not name the flag that is missing", err)
	}
}

// serveForTest boots the real serve with everything that would reach outside this
// process turned off: no auth, no vault, no docs, no metrics, and workers in
// process rather than in supervised children.
func serveForTest(ctx context.Context, addr, dataDir string, tlsCfg tlsConfig) error {
	return serve(ctx, addr, dataDir, 5*time.Second,
		false, // docs
		false, // auth
		oauthConfig{}, tlsCfg,
		false, // vault
		false, // userProvisioning
		nil, time.Second, opensearch.Config{}, promquery.Config{}, retentionConfig{},
		0, 0, false,
		false, // metrics
		logging.FormatText, tracing.Config{}, superviseFlag{}, nil, nil,
		true, // inProcessConnectors: no worker subprocesses out of a test binary
		"", api.HistoryScopeAll, "")
}

// freeAddr picks a loopback address nothing is listening on. --addr cannot be port
// 0 here: the test has to know where to connect before the server starts.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

// waitHealthy blocks until the server answers /healthz, so the test never races
// recovery — the port does not open until the log has been replayed.
func waitHealthy(t *testing.T, client *http.Client, url string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			last = nil
		} else {
			last = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s never became healthy (last error: %v)", url, last)
}
