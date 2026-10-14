package trustedproxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// resolveThrough runs one request through s.Handler and reports what the handler behind
// it was told.
func resolveThrough(t *testing.T, s Set, remote string, xff ...string) (client string, vouched bool, via string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	s.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		client, vouched = Client(r)
		via = Via(r)
	})).ServeHTTP(httptest.NewRecorder(), r)
	return client, vouched, via
}

// TestForwardedForIsTakenOnlyFromATrustedPeer is the whole security argument in one
// table. X-Forwarded-For is a header, and anybody can send a header; what makes one
// believable is the connection it arrived on. So it is read only when the peer is a
// listed proxy, and then from the right — the entry the nearest proxy appended — never
// from the left, which is whatever the client chose to write.
func TestForwardedForIsTakenOnlyFromATrustedPeer(t *testing.T) {
	s := mustParse("10.179.2.139, 10.0.0.2")
	for _, tc := range []struct {
		name    string
		remote  string
		xff     []string
		client  string // "" means: nothing vouched for, the connection's address stands
		via     string
		comment string
	}{
		{name: "direct client ignored", remote: "192.0.2.50:5555", xff: []string{"203.0.113.5"},
			comment: "a client that bypasses the load balancer cannot name itself"},
		{name: "balancer vouches", remote: "10.179.2.139:40000", xff: []string{"203.0.113.5"},
			client: "203.0.113.5", via: "10.179.2.139"},
		{name: "client-written prefix ignored", remote: "10.179.2.139:40000", xff: []string{"1.1.1.1, 203.0.113.5"},
			client: "203.0.113.5", via: "10.179.2.139",
			comment: "the client wrote 1.1.1.1; the balancer appended what it saw"},
		{name: "two proxies", remote: "10.179.2.139:40000", xff: []string{"203.0.113.5, 10.0.0.2"},
			client: "203.0.113.5", via: "10.0.0.2"},
		{name: "repeated header lines read as one list", remote: "10.179.2.139:40000",
			xff: []string{"1.1.1.1", "203.0.113.5, 10.0.0.2"}, client: "203.0.113.5", via: "10.0.0.2"},
		{name: "port and brackets", remote: "10.179.2.139:40000", xff: []string{"[2001:db8::7]:443"},
			client: "2001:db8::7", via: "10.179.2.139"},
		{name: "v4 with port", remote: "10.179.2.139:40000", xff: []string{"203.0.113.5:4711"},
			client: "203.0.113.5", via: "10.179.2.139"},
		{name: "4-in-6 unmapped", remote: "10.179.2.139:40000", xff: []string{"::ffff:203.0.113.5"},
			client: "203.0.113.5", via: "10.179.2.139"},
		{name: "balancer's own request", remote: "10.179.2.139:40000",
			comment: "a health check over HTTP carries no header; the balancer is the client"},
		{name: "only proxies listed", remote: "10.179.2.139:40000", xff: []string{"10.0.0.2"},
			client: "10.0.0.2", via: "10.179.2.139"},
		{name: "garbage stops the walk", remote: "10.179.2.139:40000", xff: []string{"203.0.113.5, unknown"},
			comment: "past an entry nobody can read there is nothing left to trust"},
		{name: "garbage after the answer is irrelevant", remote: "10.179.2.139:40000", xff: []string{"nonsense, 203.0.113.5"},
			client: "203.0.113.5", via: "10.179.2.139"},
		{name: "empty entry stops the walk", remote: "10.179.2.139:40000", xff: []string{"203.0.113.5, "}},
		{name: "mapped peer", remote: "[::ffff:10.179.2.139]:40000", xff: []string{"203.0.113.5"},
			client: "203.0.113.5", via: "10.179.2.139"},
		{name: "brackets without a port", remote: "10.179.2.139:40000", xff: []string{"[2001:db8::7]"},
			client: "2001:db8::7", via: "10.179.2.139"},
		{name: "unbalanced bracket stops the walk", remote: "10.179.2.139:40000", xff: []string{"[2001:db8::7"}},
		{name: "bracketed nonsense stops the walk", remote: "10.179.2.139:40000", xff: []string{"[nonsense]"}},
		{name: "unreadable peer", remote: "not an address", xff: []string{"203.0.113.5"},
			comment: "a connection whose own address cannot be read vouches for nobody"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, vouched, via := resolveThrough(t, s, tc.remote, tc.xff...)
			if tc.client == "" {
				if vouched {
					t.Errorf("client = %q (via %q), want nothing vouched for — %s", client, via, tc.comment)
				}
				return
			}
			if !vouched || client != tc.client || via != tc.via {
				t.Errorf("client = %q vouched=%v via %q, want %q via %q %s", client, vouched, via, tc.client, tc.via, tc.comment)
			}
		})
	}
}

// TestAZonedPeerIsNeverTrusted: fe80::1 on one interface is not fe80::1 on another, and
// the list cannot say which interface it meant — so a link-local peer that carries a zone
// is not the proxy, even where its address falls inside a listed prefix.
func TestAZonedPeerIsNeverTrusted(t *testing.T) {
	s := mustParse("fe80::/10")
	if _, vouched, _ := resolveThrough(t, s, "[fe80::1%eth0]:40000", "203.0.113.5"); vouched {
		t.Fatal("a zoned link-local peer was trusted")
	}
	if a, ok := addrOf(&net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 1, Zone: "eth0"}); !ok || s.Contains(a) {
		t.Fatalf("addrOf kept no zone, so the listener would trust %v", a)
	}
}

// TestAnEmptySetChangesNothing: without --trusted-proxies the handler is the handler.
func TestAnEmptySetChangesNothing(t *testing.T) {
	if _, vouched, _ := resolveThrough(t, Set{}, "10.179.2.139:40000", "203.0.113.5"); vouched {
		t.Fatal("an empty set took X-Forwarded-For")
	}
	h := http.NotFoundHandler()
	if got := (Set{}).Handler(h); got == nil {
		t.Fatal("Handler returned nil")
	}
}

// TestNothingIsVouchedForOutsideTheHandler: a request that never passed the handler —
// the loopback listener's, a test's — answers from its own connection.
func TestNothingIsVouchedForOutsideTheHandler(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if c, ok := Client(r); ok || c != "" {
		t.Errorf("Client = %q, %v; want nothing", c, ok)
	}
	if v := Via(r); v != "" {
		t.Errorf("Via = %q, want empty", v)
	}
}
