package capability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestValueStreamListFilters: the tag narrows to a team's streams, and stale=true is
// the review backlog — the streams nobody has stood behind within the horizon. Both
// are confirmed by being written; a year later only the one re-confirmed is fresh.
func TestValueStreamListFilters(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "consumer-loan", Name: "Consumer Loan",
		Tags: []string{"area:lending"}})
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "claims", Name: "Claims"})
	fx.now = fx.now.Add(400 * 24 * time.Hour)
	if rec := fx.do(t, "POST", "/api/v1/value-streams/claims/confirmation", map[string]any{"with": "COO"}); rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Body)
	}

	for _, tc := range []struct{ query, want string }{
		{"", "claims,consumer-loan"},
		{"?tag=area:lending", "consumer-loan"},
		{"?stale=true", "consumer-loan"},
		{"?stale=false", "claims"},
		{"?tag=area:lending&stale=false", ""},
	} {
		rec := fx.do(t, "GET", "/api/v1/value-streams"+tc.query, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list%s = %d %s", tc.query, rec.Code, rec.Body)
		}
		var keys []string
		for _, v := range decode[[]ValueStreamSummary](t, rec) {
			keys = append(keys, v.Key)
		}
		if got := strings.Join(keys, ","); got != tc.want {
			t.Errorf("list%s = %q, want %q", tc.query, got, tc.want)
		}
	}
}

// TestAnUpdateThatWouldNotValidateIsRefusedWithItsFindings: a replace is checked as
// a create is, and the record on disk stays what it was.
func TestAnUpdateThatWouldNotValidateIsRefusedWithItsFindings(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "billing", Name: "Billing", State: StateActive})
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "claims", Name: "Claims"})

	for _, tc := range []struct {
		path, want string
		body       any
	}{
		{"/api/v1/capabilities/billing", `state \"retired\" is not one of`, Capability{Name: "Billing", State: "retired"}},
		{"/api/v1/value-streams/claims", "name is required", ValueStream{}},
	} {
		rec := fx.do(t, "PUT", tc.path, tc.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("PUT %s = %d %s, want 400 with %q", tc.path, rec.Code, rec.Body, tc.want)
		}
	}
	if got := decode[Capability](t, fx.do(t, "GET", "/api/v1/capabilities/billing", nil)); got.State != StateActive {
		t.Errorf("a refused update changed the capability: %+v", got)
	}
	if got := decode[ValueStream](t, fx.do(t, "GET", "/api/v1/value-streams/claims", nil)); got.Name != "Claims" {
		t.Errorf("a refused update changed the value stream: %+v", got)
	}
}

// TestAnUpdateWithAMalformedBodyIsRefused completes TestMalformedBodies for the two
// replace routes.
func TestAnUpdateWithAMalformedBodyIsRefused(t *testing.T) {
	fx := newFixture(t)
	for _, path := range []string{"/api/v1/capabilities/billing", "/api/v1/value-streams/claims"} {
		req := httptest.NewRequest("PUT", path, strings.NewReader("{not json"))
		rec := httptest.NewRecorder()
		fx.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s with a malformed body = %d, want 400", path, rec.Code)
		}
	}
}
