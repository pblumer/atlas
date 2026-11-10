package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/calendar/graphfake"
)

const graphBundle = `{"provider":"microsoft-graph","tenantId":"t","clientId":"c","clientSecret":"s"}`

func graphBackend(t *testing.T, fake *graphfake.Server) Backend {
	t.Helper()
	b, err := NewBackend(fake.GraphURL(), graphBundle, Options{HTTPClient: fake.HTTPClient(), TokenURL: fake.TokenURL()})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTheBundleIsReadStrictly(t *testing.T) {
	for name, tc := range map[string]struct{ endpoint, bundle, want string }{
		"empty":                {"", "", "empty"},
		"not JSON":             {"", "{", "not a JSON object"},
		"unknown field":        {"", `{"provider":"mock","calendar":{}}`, "unknown field"},
		"no provider":          {"", `{}`, "names no provider"},
		"unknown provider":     {"", `{"provider":"lotus-notes"}`, "does not implement"},
		"graph without secret": {"", `{"provider":"microsoft-graph","tenantId":"t","clientId":"c"}`, "needs tenantId, clientId and clientSecret"},
		"graph over http":      {"http://graph.example.com/v1.0", graphBundle, "not an https URL"},
		"graph with calendars": {"", `{"provider":"microsoft-graph","tenantId":"t","clientId":"c","clientSecret":"s","calendars":{}}`, "calendars belongs to another provider"},
		"mock with secret":     {"", `{"provider":"mock","clientSecret":"s"}`, "clientSecret belongs to another provider"},
		"mock with endpoint":   {"https://example.com", `{"provider":"mock"}`, "has no endpoint"},
	} {
		err := CheckBundle(tc.endpoint, tc.bundle)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if err := CheckBundle("", graphBundle); err != nil {
		t.Errorf("a complete Graph bundle: %v", err)
	}
	if err := CheckBundle("", `{"provider":"mock","calendars":{"werkstatt":{}}}`); err != nil {
		t.Errorf("a mock bundle: %v", err)
	}
}

func TestASecretIsNeverQuotedInABundleError(t *testing.T) {
	err := CheckBundle("", `{"provider":"microsoft-graph","clientSecret":"geheim-123",`)
	if err == nil || strings.Contains(err.Error(), "geheim") {
		t.Fatalf("err = %v", err)
	}
}

func TestGraphPagesThroughAWindowAndReadsOnlyTimes(t *testing.T) {
	var busy []graphfake.Busy
	for d := 25; d <= 29; d++ {
		busy = append(busy, graphfake.Busy{Start: at(d, 8, 0), End: at(d, 9, 0)}, graphfake.Busy{Start: at(d, 13, 0), End: at(d, 14, 0), ShowAs: "free"})
	}
	fake := graphfake.New(map[string]graphfake.Mailbox{"werkstatt@firma.ch": {Busy: busy}})
	defer fake.Close()
	fake.PageSize = 3
	evs, err := graphBackend(t, fake).Events(context.Background(), "werkstatt@firma.ch", at(25, 0, 0), at(30, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 10 {
		t.Fatalf("%d events, want all 10 across the pages", len(evs))
	}
	nBusy := 0
	for _, e := range evs {
		if e.Busy {
			nBusy++
		}
	}
	if nBusy != 5 {
		t.Fatalf("%d busy, want the five not marked free", nBusy)
	}
}

func TestGraphRefusesALinkOutsideItsRoot(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the backend followed a link to another host and sent its token there")
	}))
	defer evil.Close()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"x","expires_in":3600}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[],"@odata.nextLink":"` + evil.URL + `/v1.0/users/x/calendar/calendarView"}`))
	}))
	defer srv.Close()
	b, err := NewBackend(srv.URL+"/v1.0", graphBundle, Options{HTTPClient: srv.Client(), TokenURL: srv.URL + "/token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Events(context.Background(), "x", at(25, 0, 0), at(26, 0, 0)); err == nil || !strings.Contains(err.Error(), "outside its own root") {
		t.Fatalf("err = %v", err)
	}
}

func TestGraphFailsAWindowTooFullToReadWhole(t *testing.T) {
	var busy []graphfake.Busy
	for i := 0; i < maxGraphPages*2+1; i++ {
		busy = append(busy, graphfake.Busy{Start: at(25, 8, 0).Add(time.Duration(i) * time.Minute), End: at(25, 8, 1).Add(time.Duration(i) * time.Minute)})
	}
	fake := graphfake.New(map[string]graphfake.Mailbox{"werkstatt": {Busy: busy}})
	defer fake.Close()
	fake.PageSize = 2
	_, err := graphBackend(t, fake).Events(context.Background(), "werkstatt", at(25, 0, 0), at(26, 0, 0))
	if err == nil || !strings.Contains(err.Error(), "shorter window") {
		t.Fatalf("err = %v; a busy list cut short would offer taken slots", err)
	}
}

func TestGraphFindsItsOwnReservationWithoutRelyingOnTransactionID(t *testing.T) {
	// The fake does not filter a repeated transactionId: only the backend's own look
	// keeps a repeated delivery from booking twice.
	fake := graphfake.New(map[string]graphfake.Mailbox{"werkstatt": {}})
	defer fake.Close()
	reg := NewRegistry()
	reg.Register("shop", graphBackend(t, fake))
	j := reserveJob("k1", at(25, 9, 0), at(25, 10, 0))
	j.Calendar = "werkstatt"
	for i := 0; i < 2; i++ {
		if _, err := RunAt(context.Background(), j, reg, now); err != nil {
			t.Fatalf("delivery %d: %v", i+1, err)
		}
	}
	if evs := fake.Events("werkstatt"); len(evs) != 1 || evs[0].TransactionID != "k1" || evs[0].Subject != "Veloservice" {
		t.Fatalf("calendar %+v", evs)
	}
}
