package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/internal/trustedproxy"
)

// TestAuditNamesTheClientBehindTheBalancer: the log that prompted ADR-draft-trusted-proxies
// recorded every login, password change and role change with client_ip set to the load
// balancer — true of the connection, useless to an audit. Behind a trusted proxy the
// line names the client and keeps the balancer as via; a request that came round the
// balancer has no via, and that absence is itself worth reading.
func TestAuditNamesTheClientBehindTheBalancer(t *testing.T) {
	proxies, err := trustedproxy.Parse("10.179.2.139")
	if err != nil {
		t.Fatal(err)
	}
	attrsFor := func(remote, xff string) map[string]string {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		got := map[string]string{}
		proxies.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			for _, a := range auditActor(r) {
				got[a.Key] = a.Value.String()
			}
		})).ServeHTTP(httptest.NewRecorder(), r)
		return got
	}

	through := attrsFor("10.179.2.139:40000", "203.0.113.5")
	if through["client_ip"] != "203.0.113.5" || through["via"] != "10.179.2.139" {
		t.Errorf("through the balancer: %v, want client_ip 203.0.113.5 via 10.179.2.139", through)
	}
	round := attrsFor("192.0.2.50:5555", "203.0.113.5")
	if round["client_ip"] != "192.0.2.50" {
		t.Errorf("round the balancer: client_ip = %q, want the connection's own 192.0.2.50", round["client_ip"])
	}
	if _, ok := round["via"]; ok {
		t.Errorf("round the balancer: via = %q, want no via at all", round["via"])
	}
}
