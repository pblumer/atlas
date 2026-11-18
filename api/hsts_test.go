package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api"
)

// TestHSTSIsSentOverHTTPSOnly covers --hsts-max-age: the transport policy goes with
// every answer to a request that arrived over HTTPS — this server's own TLS, or a
// proxy that ended it and says so — and never over plain HTTP, where RFC 6797 has a
// browser ignore it. Without the option nothing is sent, which is what every
// deployment had (ADR-0560).
func TestHSTSIsSentOverHTTPSOnly(t *testing.T) {
	header := func(srv *httptest.Server, proto string) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/healthz", nil)
		if err != nil {
			t.Fatal(err)
		}
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.Header.Get("Strict-Transport-Security")
	}

	with := newTestServerWith(t, api.WithHSTS(300))
	if got := header(with, "https"); got != "max-age=300" {
		t.Errorf("behind a proxy that ended TLS: Strict-Transport-Security %q, want max-age=300", got)
	}
	if got := header(with, ""); got != "" {
		t.Errorf("over plain HTTP: Strict-Transport-Security %q, want none", got)
	}
	if got := header(with, "http"); got != "" {
		t.Errorf("behind a proxy that says http: Strict-Transport-Security %q, want none", got)
	}

	// Zero is how an installation leaves the policy: it tells browsers to forget it.
	if got := header(newTestServerWith(t, api.WithHSTS(0)), "https"); got != "max-age=0" {
		t.Errorf("max-age 0: Strict-Transport-Security %q, want max-age=0", got)
	}
	if got := header(newTestServer(t), "https"); got != "" {
		t.Errorf("without the option: Strict-Transport-Security %q, want none", got)
	}
}
