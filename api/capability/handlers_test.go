package capability

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// fixture is a live service over temp directories, with a run loop actually running:
// every handler dispatches onto it, so a test that stubbed it would be testing
// nothing the server does.
type fixture struct {
	svc  *Service
	quit chan struct{}
	land Landscape
	mux  *http.ServeMux
	// now and horizonMonths are the two knobs a freshness test turns. The clock is
	// injected rather than read, so a test about a lapsed confirmation does not have to
	// wait a year (AGENTS.md: no test depends on the wall clock).
	now           time.Time
	horizonMonths int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	caps, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	streams, err := NewStreamStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	fx := &fixture{quit: quit, now: time.Unix(1_700_000_000, 0), horizonMonths: DefaultHorizonMonths}
	fx.svc = New(loop, caps, streams,
		func(*http.Request) (Landscape, error) { return fx.land, nil },
		func() (int, error) { return fx.horizonMonths, nil },
		func() time.Time { return fx.now })

	fx.mux = http.NewServeMux()
	fx.mux.HandleFunc("GET /api/v1/business-architecture/subset", fx.svc.HandleSubset)
	fx.mux.HandleFunc("GET /api/v1/business-architecture/gaps", fx.svc.HandleGaps)
	fx.mux.HandleFunc("GET /api/v1/capabilities", fx.svc.HandleListCapabilities)
	fx.mux.HandleFunc("POST /api/v1/capabilities", fx.svc.HandleCreateCapability)
	fx.mux.HandleFunc("GET /api/v1/capabilities/{key}", fx.svc.HandleGetCapability)
	fx.mux.HandleFunc("PUT /api/v1/capabilities/{key}", fx.svc.HandleUpdateCapability)
	fx.mux.HandleFunc("DELETE /api/v1/capabilities/{key}", fx.svc.HandleDeleteCapability)
	fx.mux.HandleFunc("GET /api/v1/capabilities/{key}/coverage", fx.svc.HandleCoverage)
	fx.mux.HandleFunc("GET /api/v1/capabilities/{key}/measurement", fx.svc.HandleMeasurement)
	fx.mux.HandleFunc("POST /api/v1/capabilities/{key}/confirmation", fx.svc.HandleConfirmCapability)
	fx.mux.HandleFunc("POST /api/v1/value-streams/{key}/confirmation", fx.svc.HandleConfirmValueStream)
	fx.mux.HandleFunc("GET /api/v1/value-streams", fx.svc.HandleListValueStreams)
	fx.mux.HandleFunc("POST /api/v1/value-streams", fx.svc.HandleCreateValueStream)
	fx.mux.HandleFunc("GET /api/v1/value-streams/{key}", fx.svc.HandleGetValueStream)
	fx.mux.HandleFunc("PUT /api/v1/value-streams/{key}", fx.svc.HandleUpdateValueStream)
	fx.mux.HandleFunc("DELETE /api/v1/value-streams/{key}", fx.svc.HandleDeleteValueStream)
	return fx
}

func (fx *fixture) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req = req.WithContext(httpapi.WithPrincipal(req.Context(),
		&httpapi.Principal{UserID: "u1", Username: "architect"}))
	rec := httptest.NewRecorder()
	fx.mux.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return out
}

func TestCreateReadUpdateDeleteACapability(t *testing.T) {
	fx := newFixture(t)

	rec := fx.do(t, "POST", "/api/v1/capabilities", Capability{
		Key: "loan-underwriting", Name: "Loan Underwriting",
		Scope: "The credit decision. Not identity or address checks.",
		Owner: Owner{Name: "Head of Credit Risk"},
		Realizations: []Realization{{Kind: RealizationManual,
			Note: "A clerk with a scoring tool"}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	created := decode[Capability](t, rec)
	if created.Revision != 1 || created.CreatedBy != "architect" || created.State != StateProposed {
		t.Errorf("created = %+v", created)
	}

	rec = fx.do(t, "GET", "/api/v1/capabilities/loan-underwriting", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	if got := decode[Capability](t, rec); got.Scope == "" {
		t.Error("the scope did not survive the round trip, and it is the field that settles boundary arguments")
	}

	created.Name = "Underwriting"
	created.State = StateActive
	rec = fx.do(t, "PUT", "/api/v1/capabilities/loan-underwriting", created)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	updated := decode[Capability](t, rec)
	if updated.Revision != 2 || updated.Name != "Underwriting" {
		t.Errorf("updated = %+v", updated)
	}
	if updated.CreatedAt != created.CreatedAt || updated.CreatedBy != "architect" {
		t.Error("an update rewrote who created the record")
	}

	rec = fx.do(t, "DELETE", "/api/v1/capabilities/loan-underwriting", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if rec = fx.do(t, "GET", "/api/v1/capabilities/loan-underwriting", nil); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete = %d", rec.Code)
	}
}

func TestCreateRefusesADuplicateKey(t *testing.T) {
	fx := newFixture(t)
	c := Capability{Key: "billing", Name: "Billing"}
	if rec := fx.do(t, "POST", "/api/v1/capabilities", c); rec.Code != http.StatusCreated {
		t.Fatalf("first create = %d %s", rec.Code, rec.Body)
	}
	rec := fx.do(t, "POST", "/api/v1/capabilities", c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second create = %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "billing") {
		t.Errorf("the refusal does not name the key: %s", rec.Body)
	}
}

func TestCreateAnswersEveryFindingAtOnce(t *testing.T) {
	fx := newFixture(t)
	rec := fx.do(t, "POST", "/api/v1/capabilities", map[string]any{
		"key": "Bad Key", "state": "retired",
		"kpis": []map[string]any{{"goal": "faster"}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	body := decode[struct {
		Error    string   `json:"error"`
		Findings []string `json:"findings"`
	}](t, rec)
	if len(body.Findings) < 4 {
		t.Errorf("findings = %v; an author fixing a form one field at a time should not need "+
			"one round trip per mistake", body.Findings)
	}
}

func TestUpdateRefusesAStaleRevision(t *testing.T) {
	fx := newFixture(t)
	rec := fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	created := decode[Capability](t, rec)

	created.Name = "First writer"
	if rec := fx.do(t, "PUT", "/api/v1/capabilities/a", created); rec.Code != http.StatusOK {
		t.Fatalf("first update = %d %s", rec.Code, rec.Body)
	}
	created.Name = "Second writer, working from the old copy"
	rec = fx.do(t, "PUT", "/api/v1/capabilities/a", created)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second update = %d %s", rec.Code, rec.Body)
	}
}

func TestUpdateRefusesARenamedKey(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "old", Name: "Old"})
	rec := fx.do(t, "PUT", "/api/v1/capabilities/old", Capability{Key: "new", Name: "New"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "identity") {
		t.Errorf("the refusal does not explain why: %s", rec.Body)
	}
}

func TestUpdateWithoutARevisionIsAllowed(t *testing.T) {
	// A caller that has no revision — a script, an import — is not a lost edit, it is a
	// caller that never read one. Demanding one would make the API unusable from a shell.
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	rec := fx.do(t, "PUT", "/api/v1/capabilities/a", Capability{Name: "Renamed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
}

func TestDeleteSaysWhatItLeftDangling(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "identity", Name: "Identity"})
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "onboarding", Name: "Onboarding",
		Requires: []string{"identity"}})
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "loan", Name: "Loan",
		Stages: []Stage{{Key: "apply", Name: "Apply", Capabilities: []string{"identity"}}}})

	rec := fx.do(t, "DELETE", "/api/v1/capabilities/identity", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	result := decode[DeletionResult](t, rec)
	if len(result.RequiredBy) != 1 || result.RequiredBy[0] != "onboarding" {
		t.Errorf("requiredBy = %v, want the capability left pointing at nothing", result.RequiredBy)
	}
	if len(result.Stages) != 1 || result.Stages[0].Key != "apply" {
		t.Errorf("stages = %+v, want the value-stream stage left pointing at nothing", result.Stages)
	}
	if len(result.ValueStreams) != 1 || result.ValueStreams[0] != "loan" {
		t.Errorf("valueStreams = %v", result.ValueStreams)
	}
	// It does not cascade: the referring records are untouched, because rewriting a
	// record the caller never mentioned is not a delete.
	rec = fx.do(t, "GET", "/api/v1/capabilities/onboarding", nil)
	if got := decode[Capability](t, rec); len(got.Requires) != 1 {
		t.Errorf("the delete rewrote another record: %+v", got)
	}
}

func TestDeleteAMissingCapability(t *testing.T) {
	fx := newFixture(t)
	if rec := fx.do(t, "DELETE", "/api/v1/capabilities/ghost", nil); rec.Code != http.StatusNotFound {
		t.Errorf("delete = %d", rec.Code)
	}
}

func TestListFilters(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "underwriting", Name: "Underwriting",
		State: StateActive, Tags: []string{"area:lending"}})
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "billing", Name: "Billing",
		State: StateActive, Tags: []string{"area:finance"},
		Realizations: []Realization{{Kind: RealizationSystem, Note: "SAP"}}})
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "fax", Name: "Fax Intake",
		State: StateDeprecated})

	cases := []struct {
		query string
		want  []string
	}{
		{"", []string{"billing", "fax", "underwriting"}},
		{"?tag=area:lending", []string{"underwriting"}},
		{"?state=deprecated", []string{"fax"}},
		// The adoption backlog: everything nothing is doing.
		{"?realized=false", []string{"fax", "underwriting"}},
		{"?realized=true", []string{"billing"}},
		{"?q=ill", []string{"billing"}},
		{"?state=active&realized=false", []string{"underwriting"}},
	}
	for _, tc := range cases {
		rec := fx.do(t, "GET", "/api/v1/capabilities"+tc.query, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list%s = %d %s", tc.query, rec.Code, rec.Body)
		}
		got := decode[[]CapabilitySummary](t, rec)
		var keys []string
		for _, c := range got {
			keys = append(keys, c.Key)
		}
		if strings.Join(keys, ",") != strings.Join(tc.want, ",") {
			t.Errorf("list%s = %v, want %v", tc.query, keys, tc.want)
		}
	}
}

func TestListIsAnArrayWhenEmpty(t *testing.T) {
	// A client rendering a list must not have to special-case null.
	fx := newFixture(t)
	for _, path := range []string{"/api/v1/capabilities", "/api/v1/value-streams"} {
		rec := fx.do(t, "GET", path, nil)
		if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
			t.Errorf("%s on an empty store = %s, want []", path, body)
		}
	}
}

func TestCoverageThroughTheAPI(t *testing.T) {
	fx := newFixture(t)
	fx.land = Landscape{Processes: []Process{
		{ApplicationKey: "kyc", ApplicationName: "KYC", ProcessID: "idv", Name: "Identity Verification",
			Version: 2, ActiveInstances: 5, CanView: true},
	}}
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "identity", Name: "Identity Verification",
		Realizations: []Realization{{Kind: RealizationProcess, ApplicationKey: "kyc", ProcessID: "idv"}}})
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "onboarding", Name: "Onboarding",
		Requires: []string{"identity"}})
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "loan", Name: "Loan",
		Stages: []Stage{{Key: "apply", Name: "Apply", Capabilities: []string{"identity"}}}})

	rec := fx.do(t, "GET", "/api/v1/capabilities/identity/coverage", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("coverage = %d %s", rec.Code, rec.Body)
	}
	cov := decode[CoverageReport](t, rec)
	if !cov.Realized || cov.Realizations[0].Version != 2 || cov.Realizations[0].ActiveInstances != 5 {
		t.Errorf("coverage = %+v", cov.Realizations)
	}
	if len(cov.RequiredBy) != 1 || cov.RequiredBy[0].Key != "onboarding" {
		t.Errorf("requiredBy = %+v", cov.RequiredBy)
	}
	if len(cov.ValueStreams) != 1 || cov.ValueStreams[0].Stages[0].Key != "apply" {
		t.Errorf("valueStreams = %+v", cov.ValueStreams)
	}
	if cov.Measurement == "" {
		t.Error("the answer carries goals but does not say that nothing here is measured")
	}
}

func TestCoverageOfAMissingCapability(t *testing.T) {
	fx := newFixture(t)
	if rec := fx.do(t, "GET", "/api/v1/capabilities/ghost/coverage", nil); rec.Code != http.StatusNotFound {
		t.Errorf("coverage = %d", rec.Code)
	}
}

func TestGapsThroughTheAPI(t *testing.T) {
	fx := newFixture(t)
	fx.land = Landscape{Processes: []Process{
		{ApplicationKey: "crm", ProcessID: "orphan", Name: "Orphan", Version: 1, CanView: true},
	}}
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "underwriting", Name: "Underwriting",
		State: StateActive})

	rec := fx.do(t, "GET", "/api/v1/business-architecture/gaps", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("gaps = %d %s", rec.Code, rec.Body)
	}
	rep := decode[GapReport](t, rec)
	if rep.Counts[FindingUnrealized] != 1 || rep.Counts[FindingProcessUnclaimed] != 1 {
		t.Errorf("counts = %v", rep.Counts)
	}
	if rep.Checked.Capabilities != 1 || rep.Checked.Processes != 1 {
		t.Errorf("checked = %+v, want the report to say what it looked at", rep.Checked)
	}
}

func TestGapsReportsALandscapeFailureRatherThanAnEmptyMap(t *testing.T) {
	// A resolver that fails must not produce a clean bill of health. That is the one
	// wrong answer this endpoint could give.
	fx := newFixture(t)
	fx.svc.landscape = func(*http.Request) (Landscape, error) {
		return Landscape{}, errAssert
	}
	rec := fx.do(t, "GET", "/api/v1/business-architecture/gaps", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("gaps = %d %s, want the failure reported", rec.Code, rec.Body)
	}
}

func TestValueStreamLifecycle(t *testing.T) {
	fx := newFixture(t)
	rec := fx.do(t, "POST", "/api/v1/value-streams", ValueStream{
		Key: "consumer-loan", Name: "Consumer Loan",
		Owner: Owner{Name: "SVP Consumer Loans", Role: "SVP"},
		Stages: []Stage{
			{Key: "marketing", Name: "Marketing"},
			{Key: "apply", Name: "Application submission", Capabilities: []string{"onboarding"}},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	created := decode[ValueStream](t, rec)

	rec = fx.do(t, "GET", "/api/v1/value-streams", nil)
	list := decode[[]ValueStreamSummary](t, rec)
	if len(list) != 1 || list[0].StageCount != 2 || list[0].CapabilityCount != 1 {
		t.Errorf("list = %+v", list)
	}

	created.Stages = append(created.Stages, Stage{Key: "disburse", Name: "Disbursement"})
	rec = fx.do(t, "PUT", "/api/v1/value-streams/consumer-loan", created)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	if got := decode[ValueStream](t, rec); len(got.Stages) != 3 || got.Stages[2].Key != "disburse" {
		t.Errorf("stage order or content is wrong: %+v", got.Stages)
	}

	if rec = fx.do(t, "DELETE", "/api/v1/value-streams/consumer-loan", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if rec = fx.do(t, "GET", "/api/v1/value-streams/consumer-loan", nil); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete = %d", rec.Code)
	}
}

func TestValueStreamRefusals(t *testing.T) {
	fx := newFixture(t)
	if rec := fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Name: "No key"}); rec.Code != http.StatusBadRequest {
		t.Errorf("create without a key = %d", rec.Code)
	}
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "v", Name: "V"})
	if rec := fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "v", Name: "V"}); rec.Code != http.StatusConflict {
		t.Errorf("duplicate = %d", rec.Code)
	}
	if rec := fx.do(t, "PUT", "/api/v1/value-streams/v", ValueStream{Key: "w", Name: "W"}); rec.Code != http.StatusBadRequest {
		t.Errorf("rename = %d", rec.Code)
	}
	if rec := fx.do(t, "PUT", "/api/v1/value-streams/ghost", ValueStream{Name: "X"}); rec.Code != http.StatusNotFound {
		t.Errorf("update of a missing stream = %d", rec.Code)
	}
	if rec := fx.do(t, "DELETE", "/api/v1/value-streams/ghost", nil); rec.Code != http.StatusNotFound {
		t.Errorf("delete of a missing stream = %d", rec.Code)
	}
	stale := ValueStream{Key: "v", Name: "V", Revision: 99}
	if rec := fx.do(t, "PUT", "/api/v1/value-streams/v", stale); rec.Code != http.StatusConflict {
		t.Errorf("stale revision = %d", rec.Code)
	}
}

func TestUpdateOfAMissingCapability(t *testing.T) {
	fx := newFixture(t)
	if rec := fx.do(t, "PUT", "/api/v1/capabilities/ghost", Capability{Name: "X"}); rec.Code != http.StatusNotFound {
		t.Errorf("update = %d", rec.Code)
	}
}

func TestMalformedBodies(t *testing.T) {
	fx := newFixture(t)
	for _, path := range []string{"/api/v1/capabilities", "/api/v1/value-streams"} {
		req := httptest.NewRequest("POST", path, strings.NewReader("{not json"))
		rec := httptest.NewRecorder()
		fx.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s with a malformed body = %d", path, rec.Code)
		}
	}
}

func TestOversizedBodyIsRefused(t *testing.T) {
	fx := newFixture(t)
	fx.svc.Limits.Request = 16
	req := httptest.NewRequest("POST", "/api/v1/capabilities",
		strings.NewReader(`{"key":"a","name":"`+strings.Repeat("x", 200)+`"}`))
	rec := httptest.NewRecorder()
	fx.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body = %d %s", rec.Code, rec.Body)
	}
}

func TestSubsetIsServed(t *testing.T) {
	fx := newFixture(t)
	rec := fx.do(t, "GET", "/api/v1/business-architecture/subset", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("subset = %d", rec.Code)
	}
	sub := decode[AuthoringSubset](t, rec)
	if len(sub.RealizationKinds) != 4 || sub.KeyPattern == "" || sub.Hierarchy == "" {
		t.Errorf("subset = %+v", sub)
	}
	if len(sub.FindingKinds) != len(FindingKinds()) {
		t.Error("the subset does not carry the finding kinds, so a client cannot render an empty state per kind")
	}
}

func TestAnUnauthenticatedWriteRecordsNoActor(t *testing.T) {
	// Single-user installations run with auth off. The record must still be writable,
	// and it must not invent an author.
	fx := newFixture(t)
	raw, _ := json.Marshal(Capability{Key: "a", Name: "A"})
	req := httptest.NewRequest("POST", "/api/v1/capabilities", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	fx.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if got := decode[Capability](t, rec); got.CreatedBy != "" {
		t.Errorf("CreatedBy = %q with no principal", got.CreatedBy)
	}
}

// errAssert is the failure a stubbed collaborator returns when a test needs the
// service to meet one.
var errAssert = errors.New("landscape unavailable")
