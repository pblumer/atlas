package blueprintregistry

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The registry's name in these tests: the name httptest's certificate is made out
// to, resolved to a public address so dialPublic vets it, and
// dialled — whatever the address — to the local test server.
const traceHost = "example.com"

type traceRig struct {
	client *PublicClient
	steps  []Step
	mu     sync.Mutex
}

func (r *traceRig) load(t *testing.T) (Index, error) {
	t.Helper()
	return r.client.LoadIndexTraced(context.Background(), func(s Step) {
		r.mu.Lock()
		r.steps = append(r.steps, s)
		r.mu.Unlock()
	})
}

// stations is "stage:ok" or "stage:cause" for each station reported, in order.
func (r *traceRig) stations() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.steps))
	for _, s := range r.steps {
		if s.OK {
			out = append(out, s.Stage+":ok")
		} else {
			out = append(out, s.Stage+":"+s.Cause)
		}
	}
	return strings.Join(out, " ")
}

func (r *traceRig) step(stage string) Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.steps {
		if s.Stage == stage {
			return s
		}
	}
	return Step{}
}

// newTraceRig points a client at srv under traceHost. trusted pins the test
// server's certificate as a root; without it the handshake meets a certificate
// this server does not trust, as behind a TLS inspection.
func newTraceRig(t *testing.T, srv *httptest.Server, trusted bool, keys ...ed25519.PublicKey) *traceRig {
	t.Helper()
	if len(keys) == 0 {
		_, key := signIndex(testIndex(t))
		keys = []ed25519.PublicKey{key}
	}
	c, err := NewPublicClient("https://"+traceHost+"/hub/index.json", keys...)
	if err != nil {
		t.Fatal(err)
	}
	c.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.215.14")}}, nil
	}
	local := srv.Listener.Addr().String()
	c.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, local)
	}
	if trusted && srv.Certificate() != nil {
		pool := x509.NewCertPool()
		pool.AddCert(srv.Certificate())
		c.httpClient.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: pool}
	}
	return &traceRig{client: c}
}

func registryHandler(t *testing.T, override func(w http.ResponseWriter, r *http.Request) bool) http.Handler {
	index := testIndex(t)
	sig, _ := signIndex(index)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if override != nil && override(w, r) {
			return
		}
		switch r.URL.Path {
		case "/hub/index.json":
			_, _ = w.Write(index)
		case "/hub/index.json.sig":
			_, _ = w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	})
}

func tlsRegistry(t *testing.T, override func(w http.ResponseWriter, r *http.Request) bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(registryHandler(t, override))
	srv.EnableHTTP2 = false
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// TestTracedLoadPassesEveryStation is the way to a healthy registry: each station
// passed, in order, with what it established.
func TestTracedLoadPassesEveryStation(t *testing.T) {
	rig := newTraceRig(t, tlsRegistry(t, nil), true)
	index, err := rig.load(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Releases) == 0 {
		t.Fatal("no releases")
	}
	if got := rig.stations(); got != "resolve:ok connect:ok tls:ok index:ok signature:ok" {
		t.Fatalf("stations: %s", got)
	}
	if s := rig.step(StageResolve); s.Host != traceHost || len(s.Addresses) != 1 || s.Addresses[0] != "93.184.215.14" {
		t.Fatalf("resolve: %+v", s)
	}
	if s := rig.step(StageConnect); s.Address != "93.184.215.14:443" {
		t.Fatalf("connect: %+v", s)
	}
	if s := rig.step(StageTLS); !strings.HasPrefix(s.TLSVersion, "TLS 1.") || !strings.Contains(s.Issuer, "Acme Co") {
		t.Fatalf("tls: %+v", s)
	}
	if s := rig.step(StageIndex); s.Status != 200 || s.Bytes == 0 {
		t.Fatalf("index: %+v", s)
	}
	_, key := signIndex(testIndex(t))
	if s := rig.step(StageSignature); s.Key != KeyFingerprint(key) {
		t.Fatalf("signature: %+v", s)
	}
}

// TestTracedLoadStopsWhereTheWayEnds holds each way a registry can be out of
// reach to the station it stops at and the cause that station names. The way
// stops there: nothing after it is reported.
func TestTracedLoadStopsWhereTheWayEnds(t *testing.T) {
	healthy := tlsRegistry(t, nil)

	t.Run("the name is unknown to the resolver", func(t *testing.T) {
		rig := newTraceRig(t, healthy, true)
		rig.client.lookup = func(context.Context, string) ([]net.IPAddr, error) {
			return nil, &net.DNSError{Err: "no such host", Name: traceHost, IsNotFound: true}
		}
		mustFail(t, rig, "resolve:dns_not_found")
	})

	t.Run("the name resolves to an internal address", func(t *testing.T) {
		rig := newTraceRig(t, healthy, true)
		rig.client.lookup = func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.20.30.40")}}, nil
		}
		mustFail(t, rig, "resolve:nonpublic_address")
		if s := rig.step(StageResolve); len(s.Addresses) != 1 || s.Addresses[0] != "10.20.30.40" {
			t.Fatalf("the internal answer is not named: %+v", s)
		}
	})

	t.Run("a firewall refuses the connection", func(t *testing.T) {
		// A real refusal on this platform: a port that was listening and is not.
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		closed := l.Addr().String()
		_ = l.Close()
		rig := newTraceRig(t, healthy, true)
		rig.client.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, closed)
		}
		mustFail(t, rig, "resolve:ok connect:refused")
		if s := rig.step(StageConnect); s.Address != "93.184.215.14:443" || s.Tried != 1 {
			t.Fatalf("connect: %+v", s)
		}
	})

	t.Run("a firewall drops the connection", func(t *testing.T) {
		rig := newTraceRig(t, healthy, true)
		rig.client.dial = func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: timeoutError{}}
		}
		mustFail(t, rig, "resolve:ok connect:timeout")
	})

	t.Run("a TLS inspection presents its own certificate", func(t *testing.T) {
		rig := newTraceRig(t, healthy, false)
		mustFail(t, rig, "resolve:ok connect:ok tls:unknown_authority")
		if s := rig.step(StageTLS); !strings.Contains(s.Issuer, "Acme Co") {
			t.Fatalf("the certificate's issuer is not named: %+v", s)
		}
	})

	t.Run("something on 443 does not speak TLS", func(t *testing.T) {
		plain := httptest.NewServer(registryHandler(t, nil))
		t.Cleanup(plain.Close)
		mustFail(t, newTraceRig(t, plain, false), "resolve:ok connect:ok tls:not_tls")
	})

	t.Run("a portal redirects the request", func(t *testing.T) {
		srv := tlsRegistry(t, func(w http.ResponseWriter, r *http.Request) bool {
			http.Redirect(w, r, "https://portal.example.net/login", http.StatusFound)
			return true
		})
		rig := newTraceRig(t, srv, true)
		mustFail(t, rig, "resolve:ok connect:ok tls:ok index:redirect")
		if s := rig.step(StageIndex); s.Location != "portal.example.net" {
			t.Fatalf("the redirect target is not named: %+v", s)
		}
	})

	t.Run("the index is not where the URL says", func(t *testing.T) {
		srv := tlsRegistry(t, func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/hub/index.json" {
				http.Error(w, "blocked", http.StatusForbidden)
				return true
			}
			return false
		})
		rig := newTraceRig(t, srv, true)
		mustFail(t, rig, "resolve:ok connect:ok tls:ok index:http_status")
		if s := rig.step(StageIndex); s.Status != http.StatusForbidden {
			t.Fatalf("status: %+v", s)
		}
	})

	t.Run("the signature file is missing", func(t *testing.T) {
		srv := tlsRegistry(t, func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/hub/index.json.sig" {
				http.NotFound(w, r)
				return true
			}
			return false
		})
		rig := newTraceRig(t, srv, true)
		mustFail(t, rig, "resolve:ok connect:ok tls:ok index:ok signature:signature_unavailable")
		if s := rig.step(StageSignature); s.Status != http.StatusNotFound {
			t.Fatalf("status: %+v", s)
		}
	})

	t.Run("no pinned key made the signature", func(t *testing.T) {
		other := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)
		mustFail(t, newTraceRig(t, healthy, true, other), "resolve:ok connect:ok tls:ok index:ok signature:no_pinned_key")
	})
}

func mustFail(t *testing.T, rig *traceRig, want string) {
	t.Helper()
	if _, err := rig.load(t); err == nil {
		t.Fatal("load succeeded")
	}
	if got := rig.stations(); got != want {
		t.Fatalf("stations: %s, want %s", got, want)
	}
	rig.mu.Lock()
	defer rig.mu.Unlock()
	if last := rig.steps[len(rig.steps)-1]; last.Err == nil {
		t.Fatalf("the station it stopped at carries no error for the operator: %+v", last)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// A key whose signature matched and whose manifest failed is the error a load
// with two pinned keys returns, whichever order the keys were pinned in: "no
// pinned key" would send the operator after the wrong thing.
func TestVerifyWithAnyPrefersTheKeyThatMatched(t *testing.T) {
	raw := []byte(`{"schemaVersion":99}`)
	sig, key := signIndex(raw)
	other := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	for _, keys := range [][]ed25519.PublicKey{{key, other}, {other, key}} {
		_, _, err := VerifySignedIndexWithAny(raw, sig, keys)
		if err == nil || errors.Is(err, ErrSignatureMismatch) {
			t.Fatalf("keys %v: %v", keys, err)
		}
	}
}
