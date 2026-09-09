package capability

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/limits"
)

// A store whose directory has gone is how this package meets a disk failure in a test:
// LoadAll cannot list it, and a write cannot land in it. What matters is not the
// particular failure but that every handler reports one instead of answering as though
// the map were empty — an empty answer to "what has no realization?" is the one wrong
// answer this area can give.
func breakStores(t *testing.T, fx *fixture) {
	t.Helper()
	for _, dir := range []string{fx.svc.caps.Dir(), fx.svc.streams.Dir()} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoreFailuresAreReportedRatherThanAnsweredAsEmpty(t *testing.T) {
	seed := func(t *testing.T) *fixture {
		fx := newFixture(t)
		fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A",
			Requires: []string{"b"}})
		fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "v", Name: "V"})
		breakStores(t, fx)
		return fx
	}
	cases := []struct {
		name, method, path string
		body               any
	}{
		{"list capabilities", "GET", "/api/v1/capabilities", nil},
		{"list value streams", "GET", "/api/v1/value-streams", nil},
		{"gaps", "GET", "/api/v1/business-architecture/gaps", nil},
		{"create capability", "POST", "/api/v1/capabilities", Capability{Key: "n", Name: "N"}},
		{"create value stream", "POST", "/api/v1/value-streams", ValueStream{Key: "n", Name: "N"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := seed(t)
			rec := fx.do(t, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("%s = %d %s, want the failure reported", tc.name, rec.Code, rec.Body)
			}
		})
	}
}

// A record that is on disk but unreadable is the other half: Get succeeds in finding
// the file and fails to decode it.
func TestACorruptRecordIsReported(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "v", Name: "V"})
	if err := os.WriteFile(filepath.Join(fx.svc.caps.Dir(), "a.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.svc.streams.Dir(), "v.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ method, path string }{
		{"GET", "/api/v1/capabilities/a"},
		{"GET", "/api/v1/capabilities/a/coverage"},
		{"DELETE", "/api/v1/capabilities/a"},
		{"GET", "/api/v1/value-streams/v"},
		{"DELETE", "/api/v1/value-streams/v"},
		{"GET", "/api/v1/business-architecture/gaps"},
		{"GET", "/api/v1/capabilities"},
		{"GET", "/api/v1/value-streams"},
	}
	for _, tc := range cases {
		rec := fx.do(t, tc.method, tc.path, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s %s = %d %s, want the corrupt record reported", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	rec := fx.do(t, "PUT", "/api/v1/capabilities/a", Capability{Name: "A"})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("update over a corrupt record = %d %s", rec.Code, rec.Body)
	}
	rec = fx.do(t, "PUT", "/api/v1/value-streams/v", ValueStream{Name: "V"})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("update over a corrupt value stream = %d %s", rec.Code, rec.Body)
	}
}

// A capability that exists while the value-stream store does not: the delete path has
// to report that, not delete half of what it was going to say.
func TestDeleteReportsAValueStreamStoreFailure(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	if err := os.RemoveAll(fx.svc.streams.Dir()); err != nil {
		t.Fatal(err)
	}
	if rec := fx.do(t, "DELETE", "/api/v1/capabilities/a", nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("delete = %d %s", rec.Code, rec.Body)
	}
}

func TestCoverageReportsAValueStreamStoreFailure(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	if err := os.RemoveAll(fx.svc.streams.Dir()); err != nil {
		t.Fatal(err)
	}
	if rec := fx.do(t, "GET", "/api/v1/capabilities/a/coverage", nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("coverage = %d %s", rec.Code, rec.Body)
	}
}

func TestCoverageReportsALandscapeFailure(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	fx.svc.landscape = func(*http.Request) (Landscape, error) { return Landscape{}, errAssert }
	if rec := fx.do(t, "GET", "/api/v1/capabilities/a/coverage", nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("coverage = %d %s", rec.Code, rec.Body)
	}
}

func TestGapsReportsAValueStreamStoreFailure(t *testing.T) {
	fx := newFixture(t)
	if err := os.RemoveAll(fx.svc.streams.Dir()); err != nil {
		t.Fatal(err)
	}
	if rec := fx.do(t, "GET", "/api/v1/business-architecture/gaps", nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("gaps = %d %s", rec.Code, rec.Body)
	}
}

// A write that cannot land must not answer 201. The directory is gone under an
// existing record, so Get misses, validation passes, and only the Save fails.
func TestAFailedSaveIsReported(t *testing.T) {
	fx := newFixture(t)
	breakStores(t, fx)
	if rec := fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"}); rec.Code != http.StatusInternalServerError {
		t.Errorf("create = %d %s", rec.Code, rec.Body)
	}
}

func TestUpdateReportsAFailedSave(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "v", Name: "V"})
	// Replace each store's directory with a file, so the record is still found through
	// the path this store already knows and the atomic write into the directory fails.
	for _, dir := range []string{fx.svc.caps.Dir(), fx.svc.streams.Dir()} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir, []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if rec := fx.do(t, "PUT", "/api/v1/capabilities/a", Capability{Name: "A2"}); rec.Code == http.StatusOK {
		t.Errorf("update over a broken store answered %d", rec.Code)
	}
	if rec := fx.do(t, "PUT", "/api/v1/value-streams/v", ValueStream{Name: "V2"}); rec.Code == http.StatusOK {
		t.Errorf("update over a broken store answered %d", rec.Code)
	}
}

func TestNewStoreReportsAnUnusableDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(file); err == nil {
		t.Error("NewStore accepted a path that is a file")
	}
	if _, err := NewStreamStore(file); err == nil {
		t.Error("NewStreamStore accepted a path that is a file")
	}
}

// failingBody is a request body that cannot be read. A truncated upload is the real
// case; the handler has to answer 400 rather than treat it as an empty record.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (failingBody) Close() error             { return nil }

func TestAnUnreadableBodyIsRefused(t *testing.T) {
	fx := newFixture(t)
	req := httptest.NewRequest("POST", "/api/v1/capabilities", io.Reader(failingBody{}))
	rec := httptest.NewRecorder()
	fx.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unreadable body = %d %s", rec.Code, rec.Body)
	}
}

// TestBudgetsDefaultsAZeroValueService is the rule the limits registry states: the
// zero Limits is every ceiling at zero, and a ceiling of zero admits nothing — which
// would look like a bad request rather than like missing configuration.
func TestBudgetsDefaultsAZeroValueService(t *testing.T) {
	if got := (&Service{}).budgets(); got != limits.Default() {
		t.Errorf("a Service built as a struct literal reads budgets %+v, want the defaults", got)
	}
	configured := limits.Default()
	configured.Request = 1234
	if got := (&Service{Limits: configured}).budgets(); got.Request != 1234 {
		t.Errorf("a configured Service reads Request = %d", got.Request)
	}
}

// TestAClosingLoopDoesNotAnswerSuccessfully: Loop.Do abandons the closure when the
// loop is shutting down, so every handler's result variables keep their zero values.
// The handlers must not read that as an answer.
func TestAClosingLoopDoesNotAnswerSuccessfully(t *testing.T) {
	caps, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	streams, err := NewStreamStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	close(quit) // the loop never runs
	svc := New(runloop.New(quit), caps, streams,
		func(*http.Request) (Landscape, error) { return Landscape{}, nil },
		func() time.Time { return time.Unix(0, 0) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/capabilities/{key}", svc.HandleGetCapability)
	mux.HandleFunc("PUT /api/v1/capabilities/{key}", svc.HandleUpdateCapability)
	mux.HandleFunc("DELETE /api/v1/capabilities/{key}", svc.HandleDeleteCapability)
	mux.HandleFunc("GET /api/v1/value-streams/{key}", svc.HandleGetValueStream)
	mux.HandleFunc("PUT /api/v1/value-streams/{key}", svc.HandleUpdateValueStream)
	mux.HandleFunc("DELETE /api/v1/value-streams/{key}", svc.HandleDeleteValueStream)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/capabilities/a"},
		{"PUT", "/api/v1/capabilities/a"},
		{"DELETE", "/api/v1/capabilities/a"},
		{"GET", "/api/v1/value-streams/v"},
		{"PUT", "/api/v1/value-streams/v"},
		{"DELETE", "/api/v1/value-streams/v"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, io.Reader(nopBody(`{"name":"X"}`)))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code < 400 {
			t.Errorf("%s %s against a closing loop = %d, want a refusal", tc.method, tc.path, rec.Code)
		}
	}
}

type nopBody string

func (b nopBody) Read(p []byte) (int, error) {
	if len(b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b)
	return n, io.EOF
}
func (nopBody) Close() error { return nil }
