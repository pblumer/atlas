package order

import (
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// holdingService is newService for a recipient who already holds what holds names:
// the bundle of testRelease, or the parts of it they kept.
func holdingService(t *testing.T, holds ...string) *Service {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	rel := testRelease(t)
	has := map[string]bool{}
	for _, id := range holds {
		has[id] = true
	}
	return New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		inAnyGroup, mayOrderForAnyone,
		func(message, orderID string, vars map[string]string) error { return nil },
		func() string { return "https://atlas.example.ch" },
		func() string { return "http://atlas.test" },
		ignoreGrant, ignoreRevoke,
		func(string) (map[string]bool, error) { return has, nil })
}

func placeFor(t *testing.T, s *Service, body string) Order {
	t.Helper()
	rec := do(t, s.HandlePlace, someone("usr_ada"), "POST", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("place = %d (%s), want 201", rec.Code, rec.Body)
	}
	return decode[Order](t, rec)
}

// A part of a bundle that was given back — the laptop out of a workplace the
// recipient still holds — is ordered again on its own. The shop offers exactly this
// once the part is no longer held, and the order is one position: the laptop,
// ordered for itself rather than carried, so it can be withdrawn like any other.
func TestAPartGivenBackFromAHeldBundleIsOrderedAgainOnItsOwn(t *testing.T) {
	got := placeFor(t, holdingService(t, "workplace", "account"),
		`{"releaseId":"rel_1","items":["laptop"]}`)

	if lineIDs(got) != "laptop" {
		t.Fatalf("lines = %s, want the laptop alone", lineIDs(got))
	}
	l := got.Lines[0]
	if l.Status != StatusPending {
		t.Errorf("the laptop is %s, want pending — it is no longer held", l.Status)
	}
	if l.Integral {
		t.Error("the laptop is marked integral although nothing in this order carries it")
	}
}

// Ordering the bundle again brings back only what is missing: what is still held
// is recorded as skipped, the part that was given back is provisioned.
func TestOrderingTheBundleAgainProvisionsOnlyThePartGivenBack(t *testing.T) {
	got := placeFor(t, holdingService(t, "workplace", "account"),
		`{"releaseId":"rel_1","items":["workplace"]}`)

	for id, want := range map[string]LineStatus{
		"workplace": StatusSkipped, "account": StatusSkipped, "laptop": StatusPending,
	} {
		if status := lineStatusOf(t, got, id); status != want {
			t.Errorf("%s is %s, want %s", id, status, want)
		}
	}
}

// An option nobody took when the bundle was ordered is ordered later on its own,
// although the catalogue lists it only beneath the bundle and not as an offering of
// its own.
func TestAnOptionNotTakenIsOrderedLaterOnItsOwn(t *testing.T) {
	got := placeFor(t, holdingService(t, "workplace", "account", "laptop"),
		`{"releaseId":"rel_1","items":["screen"]}`)

	if lineIDs(got) != "screen" {
		t.Fatalf("lines = %s, want the screen alone", lineIDs(got))
	}
	if got.Lines[0].Status != StatusPending {
		t.Errorf("the screen is %s, want pending", got.Lines[0].Status)
	}
}
