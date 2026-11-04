package capability

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestTheMapReadsWhatBundlesBroughtAndFailsLoudlyWhenItCannot: a service wired with the
// bundled realizations shows them on the list and on one capability, marked with the
// application that brought them; a store that cannot be read is an error, not a map
// without them.
func TestTheMapReadsWhatBundlesBroughtAndFailsLoudlyWhenItCannot(t *testing.T) {
	fx := newFixture(t)
	st, err := NewRealizationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fx.svc.SetBundledRealizations(st)

	if rec := fx.do(t, "POST", "/api/v1/capabilities", Capability{
		Key: "konten", Name: "Konten", Scope: "Accounts.", Owner: Owner{Name: "IM"},
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if err := st.Save(ApplicationRealizations{ApplicationID: "a1", ApplicationKey: "m365",
		Realizations: []BundledRealization{{Capability: "konten", Kind: RealizationWorker, WorkerRef: "ucc_entra"}}}); err != nil {
		t.Fatal(err)
	}

	rec := fx.do(t, "GET", "/api/v1/capabilities/konten", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	if got := decode[Capability](t, rec); len(got.Realizations) != 1 || got.Realizations[0].BundledBy != "m365" {
		t.Fatalf("one capability = %+v, want the bundled worker marked with m365", got.Realizations)
	}
	rec = fx.do(t, "GET", "/api/v1/capabilities", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"realizationCount":1`) {
		t.Fatalf("list = %d %s, want the bundled realization counted", rec.Code, rec.Body)
	}

	if err := os.RemoveAll(st.Dir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Dir(), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := fx.do(t, "GET", "/api/v1/capabilities/konten", nil); rec.Code < 500 {
		t.Errorf("one capability with an unreadable bundle store = %d, want a server error", rec.Code)
	}
	if rec := fx.do(t, "GET", "/api/v1/capabilities", nil); rec.Code < 500 {
		t.Errorf("the list with an unreadable bundle store = %d, want a server error", rec.Code)
	}
	if _, err := st.Overlay(); err == nil {
		t.Error("an unreadable store gave an overlay")
	}
}
