package api

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The API-token expiry metrics (ADR-0194, amendment 2026-10-22).
//
// A token that runs out fails every call its machine makes from then on, and the
// Console says so only to whoever opens it. These series put the same warning where
// an alert can read it: how many tokens are about to go, how many already have, and
// when the next one does.

const secsPerDay = int64(24 * 60 * 60)

// TestTheMetricsCountTheAPITokensAboutToExpire pins the boundaries: "about to expire"
// is a token still accepted with less than apiTokenExpiryWarning left — the Console's
// definition, so the alert and the page warn about the same tokens — and a token
// whose expiry is this very second is already refused, so it counts as expired.
func TestTheMetricsCountTheAPITokensAboutToExpire(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	srv := newServerWithOptions(t, withClock(func() int64 { return now.UnixNano() }))
	at := now.Unix()
	srv.apiTokens.replaceAll([]apiToken{
		{ID: "never", Hash: "h-never"},
		{ID: "far", Hash: "h-far", ExpiresAt: at + 31*secsPerDay},
		{ID: "edge", Hash: "h-edge", ExpiresAt: at + 30*secsPerDay}, // exactly thirty days: not less
		{ID: "soon", Hash: "h-soon", ExpiresAt: at + 29*secsPerDay},
		{ID: "sooner", Hash: "h-sooner", ExpiresAt: at + 3600},
		{ID: "now", Hash: "h-now", ExpiresAt: at},
		{ID: "gone", Hash: "h-gone", ExpiresAt: at - secsPerDay},
	})

	body := scrapeServer(t, srv)
	if got := sampleValue(t, body, "atlas_api_tokens_expiring_soon"); got != 2 {
		t.Errorf("expiring soon = %v, want 2 (the tokens with 29 days and one hour left)", got)
	}
	if got := sampleValue(t, body, "atlas_api_tokens_expired"); got != 2 {
		t.Errorf("expired = %v, want 2 (the one past and the one expiring this second)", got)
	}
	if got := sampleValue(t, body, "atlas_api_token_next_expiry_timestamp_seconds"); got != float64(at+3600) {
		t.Errorf("next expiry = %v, want %d: the earliest of the tokens still accepted", got, at+3600)
	}
	// The threshold is the Console's: the page and the alert must not disagree about
	// which tokens are in trouble.
	if apiTokenExpiryWarning != 30*24*time.Hour {
		t.Errorf("apiTokenExpiryWarning = %v; the boundaries above assume thirty days", apiTokenExpiryWarning)
	}
}

// TestTheNextExpiryIsInfiniteWhenNothingExpires: with no token that runs out the
// answer is a known "never", not an unreadable number, so the series is present —
// an absent one would trip the "no data" alert ADR-0142 reserves for unreadable
// state — and its value keeps "next expiry minus now below a week" quiet.
func TestTheNextExpiryIsInfiniteWhenNothingExpires(t *testing.T) {
	for name, recs := range map[string][]apiToken{
		"no tokens":             nil,
		"only unlimited":        {{ID: "never", Hash: "h-never"}},
		"only expired ones":     {{ID: "gone", Hash: "h-gone", ExpiresAt: time.Now().Unix() - secsPerDay}},
		"unlimited and expired": {{ID: "never", Hash: "h-never"}, {ID: "gone", Hash: "h-gone", ExpiresAt: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newServerForErrors(t)
			srv.apiTokens.replaceAll(recs)
			body := scrapeServer(t, srv)
			if got := sampleValue(t, body, "atlas_api_token_next_expiry_timestamp_seconds"); !math.IsInf(got, 1) {
				t.Errorf("next expiry = %v, want +Inf", got)
			}
			if got := sampleValue(t, body, "atlas_api_tokens_expiring_soon"); got != 0 {
				t.Errorf("expiring soon = %v, want 0", got)
			}
		})
	}
}

// TestTheExpiryMetricsFollowMintAndRevoke drives the routes rather than the index:
// a token minted with ten days counts at once, and revoking it — what an operator
// does once the replacement is in place — clears the count without a restart.
func TestTheExpiryMetricsFollowMintAndRevoke(t *testing.T) {
	srv := newServerForErrors(t)
	if got := sampleValue(t, scrapeServer(t, srv), "atlas_api_tokens_expiring_soon"); got != 0 {
		t.Fatalf("expiring soon = %v on a server without tokens, want 0", got)
	}

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens",
		`{"name":"order sync","scope":"full","expiresInDays":10}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("mint: %d (%s)", code, body)
	}
	var minted struct {
		ID        string `json:"id"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.Unmarshal(body, &minted); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	after := scrapeServer(t, srv)
	if got := sampleValue(t, after, "atlas_api_tokens_expiring_soon"); got != 1 {
		t.Errorf("expiring soon = %v after minting a ten-secsPerDay token, want 1", got)
	}
	if got := sampleValue(t, after, "atlas_api_token_next_expiry_timestamp_seconds"); got != float64(minted.ExpiresAt) {
		t.Errorf("next expiry = %v, want the minted token's %d", got, minted.ExpiresAt)
	}

	code, body = serveInternal(t, srv, http.MethodDelete, "/api/v1/api-tokens/"+minted.ID, "", "")
	if code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("revoke: %d (%s)", code, body)
	}
	if got := sampleValue(t, scrapeServer(t, srv), "atlas_api_tokens_expiring_soon"); got != 0 {
		t.Errorf("expiring soon = %v after revoking the token, want 0", got)
	}
}

// TestTheExpiryMetricsNameNoToken holds the cardinality rule (ADR-0142): a token's
// name or id is a value the data invents, and on /metrics it would also hand every
// holder of a metrics-scoped token the inventory of the server's credentials. The
// series are bare aggregates; GET /api/v1/api-tokens says which token, to an
// administrator.
func TestTheExpiryMetricsNameNoToken(t *testing.T) {
	srv := newServerForErrors(t)
	srv.apiTokens.replaceAll([]apiToken{
		{ID: "tok-id-7f3a", Name: "nightly-ledger-export", Hash: "h", ExpiresAt: time.Now().Unix() + secsPerDay},
	})
	body := scrapeServer(t, srv)
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "api_token") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "{") {
			t.Errorf("token series carries labels: %s", line)
		}
	}
	for _, leak := range []string{"tok-id-7f3a", "nightly-ledger-export"} {
		if strings.Contains(body, leak) {
			t.Errorf("the exposition names a token (%q)", leak)
		}
	}
}
