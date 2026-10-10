package trustedproxy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// seen is what the handler behind the trusted-proxy layer was told.
type seen struct {
	Remote  string `json:"remote"`
	Client  string `json:"client"`
	Vouched bool   `json:"vouched"`
	Via     string `json:"via"`
}

// startTLS runs a real net/http TLS server the way cmd/atlas wires it: the PROXY
// listener under the TLS one, ConnContext on the server, the handler wrapped.
func startTLS(t *testing.T, s Set) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	ts := httptest.NewUnstartedServer(s.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := Client(r)
		_ = json.NewEncoder(w).Encode(seen{Remote: r.RemoteAddr, Client: c, Vouched: ok, Via: Via(r)})
	})))
	ts.Listener = s.Listener(ts.Listener)
	ts.Config.ConnContext = ConnContext
	ts.StartTLS()
	t.Cleanup(ts.Close)
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	return ts, pool
}

// ask opens a TCP connection, writes prefix (a PROXY header, or nothing), then speaks
// TLS and one HTTP request over it, the way a TCP load balancer forwards a client.
func ask(t *testing.T, ts *httptest.Server, pool *x509.CertPool, prefix string, header http.Header) seen {
	t.Helper()
	raw, err := net.DialTimeout("tcp", ts.Listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	if prefix != "" {
		if _, err := raw.Write([]byte(prefix)); err != nil {
			t.Fatal(err)
		}
	}
	conn := tls.Client(raw, &tls.Config{RootCAs: pool, ServerName: "example.com"})
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if err := req.Write(conn); err != nil {
		t.Fatalf("write the request over TLS: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatalf("read the response: %v", err)
	}
	defer resp.Body.Close()
	var got seen
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

// TestBehindATCPBalancer: the deployment the log came from. The balancer forwards TCP,
// Atlas terminates TLS, and the only way the client's address can reach the handler is
// the PROXY header in front of the handshake.
func TestBehindATCPBalancer(t *testing.T) {
	ts, pool := startTLS(t, mustParse("127.0.0.1"))
	got := ask(t, ts, pool, "PROXY TCP4 203.0.113.5 10.0.0.9 51234 443\r\n", nil)
	if got.Remote != "203.0.113.5:51234" || !got.Vouched || got.Client != "203.0.113.5" || got.Via != "127.0.0.1" {
		t.Errorf("handler saw %+v, want client 203.0.113.5 via 127.0.0.1", got)
	}
	// The same balancer, a connection without a header: the balancer is the client and
	// nothing is vouched for.
	got = ask(t, ts, pool, "", nil)
	if got.Vouched || got.Via != "" {
		t.Errorf("without a header the handler saw %+v, want nothing vouched for", got)
	}
}

// TestBehindAnHTTPBalancer: a balancer that re-encrypts to Atlas says who the client is
// in X-Forwarded-For; the client's own entries to the left are not taken.
func TestBehindAnHTTPBalancer(t *testing.T) {
	ts, pool := startTLS(t, mustParse("127.0.0.1"))
	got := ask(t, ts, pool, "", http.Header{"X-Forwarded-For": {"1.1.1.1, 203.0.113.5"}})
	if !got.Vouched || got.Client != "203.0.113.5" || got.Via != "127.0.0.1" {
		t.Errorf("handler saw %+v, want client 203.0.113.5 via 127.0.0.1", got)
	}
}

// TestNotBehindABalancer: a client that reaches the port directly can write both a
// PROXY header and X-Forwarded-For, and is believed for neither. The header is the
// first bytes of a TLS stream, and the handshake refuses it.
func TestNotBehindABalancer(t *testing.T) {
	ts, pool := startTLS(t, mustParse("192.0.2.1"))
	got := ask(t, ts, pool, "", http.Header{"X-Forwarded-For": {"203.0.113.5"}})
	if got.Vouched {
		t.Errorf("an untrusted peer was believed: %+v", got)
	}

	raw, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = raw.Write([]byte("PROXY TCP4 203.0.113.5 10.0.0.9 51234 443\r\n"))
	if err := tls.Client(raw, &tls.Config{RootCAs: pool, ServerName: "example.com"}).Handshake(); err == nil {
		t.Fatal("a PROXY header from an untrusted peer was accepted in front of a handshake")
	}
}
