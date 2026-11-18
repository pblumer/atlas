package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/connector/script"
	"github.com/pblumer/atlas/internal/trustedproxy"
	"github.com/pblumer/atlas/limits"
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

	// Every draft belongs to an application (ADR-0544).
	appResp, err := client.Post("https://"+addr+"/api/v1/applications", "application/json", strings.NewReader(`{"name":"TLS"}`))
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	var app struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(appResp.Body).Decode(&app)
	appResp.Body.Close()
	if err != nil || app.ID == "" {
		t.Fatalf("create application: %s (%v)", appResp.Status, err)
	}
	resp, err := client.Post("https://"+addr+"/api/v1/drafts?projectId="+app.ID, "application/xml", strings.NewReader(draftBPMN))
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
	return bootTLSBehind(t, serial, trustedproxy.Set{})
}

// bootTLSBehind is bootTLS behind the proxies --trusted-proxies would name.
func bootTLSBehind(t *testing.T, serial int64, proxies trustedproxy.Set) (addr string, pool *x509.CertPool) {
	t.Helper()
	dir := t.TempDir()
	certFile, keyFile, leaf := writeCertPair(t, dir, serial)
	addr = freeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveBehindForTest(ctx, addr, filepath.Join(dir, "data"), tlsConfig{certFile: certFile, keyFile: keyFile}, proxies)
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

// TestServeChoosesByNameRedirectsAndSendsThePolicy boots the real server with two
// certificates, a redirect listener and a transport policy, the three things a
// server that terminates TLS itself was missing beside the listener
// (ADR-0562, ADR-0560): each name
// is presented its own certificate, plain HTTP is redirected to HTTPS on the port
// --external-url names, and answers over HTTPS carry the policy while the redirect
// does not.
func TestServeChoosesByNameRedirectsAndSendsThePolicy(t *testing.T) {
	dir := t.TempDir()
	atlasCert, atlasKey, atlasLeaf := writeNamedCertPair(t, dir, "atlas", 51, "atlas.example.org")
	appsCert, appsKey, appsLeaf := writeNamedCertPair(t, dir, "apps", 52, "apps.example.com")
	addr, plain := freeAddr(t), freeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveWithForTest(ctx, addr, filepath.Join(dir, "data"), tlsConfig{
			certFile: atlasCert + "," + appsCert, keyFile: atlasKey + "," + appsKey,
			redirectAddr: plain, hstsMaxAge: "300",
		}, oauthConfig{externalURL: "https://atlas.example.org:8443"})
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

	clientFor := func(name string, leaf *x509.Certificate) *http.Client {
		pool := x509.NewCertPool()
		pool.AddCert(leaf)
		return &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: name}},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	atlas := clientFor("atlas.example.org", atlasLeaf)
	waitHealthy(t, atlas, "https://"+addr+"/healthz")

	// Each client trusts only the certificate of the name it asks for, so a request
	// that succeeds was presented that certificate.
	for name, client := range map[string]*http.Client{"atlas.example.org": atlas, "apps.example.com": clientFor("apps.example.com", appsLeaf)} {
		resp, err := client.Get("https://" + addr + "/healthz")
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		resp.Body.Close()
		if got := resp.Header.Get("Strict-Transport-Security"); got != "max-age=300" {
			t.Errorf("%s: Strict-Transport-Security %q over HTTPS, want max-age=300", name, got)
		}
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+plain+"/tasks?x=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "apps.example.com"
	resp, err := atlas.Do(req)
	if err != nil {
		t.Fatalf("plain HTTP: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://apps.example.com:8443/tasks?x=1" {
		t.Errorf("plain HTTP: status %s, Location %q; want 302 to https://apps.example.com:8443/tasks?x=1", resp.Status, resp.Header.Get("Location"))
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Errorf("plain HTTP: Strict-Transport-Security %q, want none", got)
	}
	// The port open to plain HTTP reaches nothing of the server.
	resp, err = atlas.Post("http://"+plain+"/api/v1/login", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("a write over plain HTTP: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a write over plain HTTP: status %s, want 400", resp.Status)
	}
}

// TestServeRefusesARedirectWithoutTLSAndAPolicyThatIsNoNumber stops the server on
// the two settings that cannot mean anything (ADR-0560).
func TestServeRefusesARedirectWithoutTLSAndAPolicyThatIsNoNumber(t *testing.T) {
	dir := t.TempDir()
	err := serveForTest(context.Background(), freeAddr(t), filepath.Join(dir, "a"), tlsConfig{redirectAddr: freeAddr(t)})
	if err == nil || !strings.Contains(err.Error(), "--redirect-addr") || !strings.Contains(err.Error(), "--tls-cert") {
		t.Errorf("a redirect without TLS: %v; want an error naming --redirect-addr and --tls-cert", err)
	}
	err = serveForTest(context.Background(), freeAddr(t), filepath.Join(dir, "b"), tlsConfig{hstsMaxAge: "on"})
	if err == nil || !strings.Contains(err.Error(), "--hsts-max-age") {
		t.Errorf("a policy that is no number: %v; want an error naming --hsts-max-age", err)
	}

	// An address that cannot be listened on ends the server, as --addr does.
	certFile, keyFile, _ := writeCertPair(t, dir, 61)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err = serveForTest(ctx, freeAddr(t), filepath.Join(dir, "c"), tlsConfig{certFile: certFile, keyFile: keyFile, redirectAddr: "256.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "--redirect-addr") {
		t.Errorf("an unusable redirect address: %v; want an error naming --redirect-addr", err)
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
	return serveBehindForTest(ctx, addr, dataDir, tlsCfg, trustedproxy.Set{})
}

// serveBehindForTest is serveForTest with the proxies --trusted-proxies would name.
func serveBehindForTest(ctx context.Context, addr, dataDir string, tlsCfg tlsConfig, proxies trustedproxy.Set) error {
	return serveAllForTest(ctx, addr, dataDir, tlsCfg, oauthConfig{}, proxies)
}

// serveWithForTest is serveForTest with the origin --external-url would state.
func serveWithForTest(ctx context.Context, addr, dataDir string, tlsCfg tlsConfig, oauth oauthConfig) error {
	return serveAllForTest(ctx, addr, dataDir, tlsCfg, oauth, trustedproxy.Set{})
}

func serveAllForTest(ctx context.Context, addr, dataDir string, tlsCfg tlsConfig, oauth oauthConfig, proxies trustedproxy.Set) error {
	return serve(ctx, addr, dataDir, 5*time.Second,
		false, // docs
		false, // auth
		oauth, tlsCfg, proxies,
		false, // vault
		false, // userProvisioning
		nil, time.Second, script.SandboxOff, opensearch.Config{}, promquery.Config{}, retentionConfig{}, storeConfig{},
		0, 0, false,
		false, // metrics
		true,  // catalogue
		true,  // cataloguePurge
		true,  // mcpShop
		logging.FormatText, logging.DefaultLevel, tracing.Config{}, superviseFlag{}, nil, nil,
		api.DefaultSupervisedWorkerMaxJobs,
		true, // inProcessConnectors: no worker subprocesses out of a test binary
		"", api.HistoryScopeAll, "", limits.Default(), false,
		api.DefaultFullReplayMaxIterations)
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
