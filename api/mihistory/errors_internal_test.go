package mihistory

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pblumer/atlas/state"
)

func serveOnce(t *testing.T, svc *Service, path string) (int, map[string]string) {
	t.Helper()
	mux := http.NewServeMux()
	(&world{}).mount(mux, svc)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestErrorsKeepTheEnvelopeAndAStableCode(t *testing.T) {
	now := func() time.Time { return time.Unix(1_800_000_000, 0) }
	closing := newService(func(func(*state.ReadView, Definitions) error) error { return ErrLoopClosing }, []byte("k"), now)
	broken := newService(func(func(*state.ReadView, Definitions) error) error { return errors.New("disk on fire") }, []byte("k"), now)
	unavailable := newService(func(func(*state.ReadView, Definitions) error) error { return state.ErrMIHistoryIndexUnavailable }, []byte("k"), now)
	busy := newService(func(func(*state.ReadView, Definitions) error) error { t.Fatal("ran past a full limiter"); return nil }, []byte("k"), now)
	for i := 0; i < maxConcurrentReads; i++ {
		busy.sem <- struct{}{}
	}
	for _, tt := range []struct {
		name   string
		svc    *Service
		path   string
		status int
		code   string
	}{
		{"shutting down", closing, "/api/v1/instances/1/multi-instances", 503, "shutting_down"},
		{"internal", broken, "/api/v1/instances/1/multi-instances/2", 500, "internal"},
		{"unavailable", unavailable, "/api/v1/instances/1/multi-instances/2/iterations", 409, "history_unavailable"},
		{"busy", busy, "/api/v1/instances/1/multi-instances/2/iterations/3", 503, "busy"},
		{"bad body key", closing, "/api/v1/instances/1/multi-instances/x", 400, "invalid_parameter"},
		{"bad iteration key", closing, "/api/v1/instances/1/multi-instances/2/iterations/x", 400, "invalid_parameter"},
		{"bad parent", closing, "/api/v1/instances/1/multi-instances?parentIteration=-1", 400, "invalid_parameter"},
		{"bad bodies limit", closing, "/api/v1/instances/1/multi-instances?limit=0", 400, "invalid_parameter"},
		{"bad bodies cursor", closing, "/api/v1/instances/1/multi-instances?cursor=zz", 400, "invalid_cursor"},
		{"bad iterations body key", closing, "/api/v1/instances/1/multi-instances/x/iterations", 400, "invalid_parameter"},
		{"bad iterations instance key", closing, "/api/v1/instances/x/multi-instances/2/iterations", 400, "invalid_parameter"},
		{"bad exact key", closing, "/api/v1/instances/1/multi-instances/2/iterations?elementInstanceKey=0", 400, "invalid_parameter"},
		{"bad detail instance key", closing, "/api/v1/instances/x/multi-instances/2/iterations/3", 400, "invalid_parameter"},
		{"bad detail body key", closing, "/api/v1/instances/1/multi-instances/x/iterations/3", 400, "invalid_parameter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status, body := serveOnce(t, tt.svc, tt.path)
			if status != tt.status || body["code"] != tt.code || body["error"] == "" {
				t.Fatalf("got %d %v, want %d %s", status, body, tt.status, tt.code)
			}
		})
	}
}

func TestNewDrawsADistinctCursorKey(t *testing.T) {
	view := func(func(*state.ReadView, Definitions) error) error { return nil }
	a, err := New(view, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(view, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	c := cursor{kind: kindIterations, instance: 1, issuedAt: time.Now().Unix()}
	if _, err := b.decodeCursor(a.encodeCursor(c), time.Now()); !errors.Is(err, errCursorForged) {
		t.Fatalf("a cursor from another process was accepted: %v", err)
	}
	if got, err := a.decodeCursor(a.encodeCursor(c), time.Now()); err != nil || got != c {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	future := c
	future.issuedAt = time.Now().Add(time.Hour).Unix()
	if _, err := a.decodeCursor(a.encodeCursor(future), time.Now()); !errors.Is(err, errCursorExpired) {
		t.Fatalf("a cursor from the future was accepted: %v", err)
	}
	if (&apiError{msg: "m"}).Error() != "m" {
		t.Fatal("apiError message")
	}
}
