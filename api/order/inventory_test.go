package order

import (
	"errors"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// recordedInventory is the inventory as these tests watch it: what was granted,
// what was revoked, and an error it can be told to answer with.
type recordedInventory struct {
	granted []Grant
	revoked [][2]string
	err     error
}

func (i *recordedInventory) grant(g Grant) error {
	if i.err != nil {
		return i.err
	}
	i.granted = append(i.granted, g)
	return nil
}

func (i *recordedInventory) revoke(principal, itemID string) error {
	if i.err != nil {
		return i.err
	}
	i.revoked = append(i.revoked, [2]string{principal, itemID})
	return nil
}

// inventoryFixture builds a service whose inventory and wake are both watched,
// with one order already in its store. The order is written directly rather than
// placed, so a line can carry a variant — what somebody holds is the variant,
// not the product, and a test that could not say so would not be testing it.
func inventoryFixture(t *testing.T, lines ...Line) (*Service, *Store, *recordedInventory, *int) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	inv := &recordedInventory{}
	wakes := 0
	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return testRelease(t), true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(message, orderID string, vars map[string]string) error { wakes++; return nil },
		func() string { return "https://atlas.example.ch" },
		inv.grant, inv.revoke)

	if err := store.Save(Order{
		ID: "ord_1", ReleaseID: "rel_1", Orderer: "usr_chef", Recipient: "usr_ada",
		Lines: lines, CreatedAt: 1000, UpdatedAt: 1000,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return s, store, inv, &wakes
}

// reporter is the provisioning worker reporting an outcome.
func reporter() *httpapi.Principal {
	return &httpapi.Principal{UserID: "usr_worker", Username: "worker"}
}

// A line that reached done is the moment somebody starts holding something. It
// is recorded against the *recipient*, not the orderer: an integration manager
// who orders a laptop for a new colleague does not thereby hold a laptop, and an
// inventory that confused the two would put access on the wrong person.
func TestAProvisionedLineIsRecordedAgainstTheRecipient(t *testing.T) {
	s, _, inv, _ := inventoryFixture(t, Line{
		ItemID: "laptop", VariantID: "14zoll", Status: StatusPending,
		ProvisionProcess: "prov", DeprovisionProcess: "deprov",
	})

	if rec := do(t, s.HandleReport, reporter(), "POST", `{"status":"done"}`,
		"id", "ord_1", "item", "laptop"); rec.Code != http.StatusOK {
		t.Fatalf("report = %d: %s", rec.Code, rec.Body)
	}

	if len(inv.granted) != 1 {
		t.Fatalf("%d rights granted, want 1", len(inv.granted))
	}
	want := Grant{Principal: "usr_ada", ItemID: "laptop", VariantID: "14zoll",
		OrderID: "ord_1", At: 1700}
	if inv.granted[0] != want {
		t.Errorf("granted = %+v, want %+v", inv.granted[0], want)
	}
}

// The moment a right starts is the moment the order records, not a second clock
// reading. Two records of the same event that disagree about when it happened
// are worse than one, because an audit has to decide which of them lies.
func TestTheRightStartsWhenTheOrderSaysItDid(t *testing.T) {
	s, store, inv, _ := inventoryFixture(t, Line{
		ItemID: "account", Status: StatusPending, ProvisionProcess: "prov",
	})
	do(t, s.HandleReport, reporter(), "POST", `{"status":"done"}`, "id", "ord_1", "item", "account")

	got, _, err := store.Get("ord_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(inv.granted) != 1 || inv.granted[0].At != got.UpdatedAt {
		t.Fatalf("right starts at %v, order moved at %d", inv.granted, got.UpdatedAt)
	}
}

// Giving a line back takes the right away again. It names the principal and the
// item and nothing else: a revocation is not a second opinion about what was
// held, it is the end of whatever was.
func TestAReturnedLineStopsBeingHeld(t *testing.T) {
	s, store, inv, _ := inventoryFixture(t, Line{
		ItemID: "laptop", VariantID: "14zoll", Status: StatusDone,
		ProvisionProcess: "prov", DeprovisionProcess: "deprov",
	})
	got, _, err := store.Get("ord_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// A return is reported only by the revocation that was asked for, so ask.
	if got, err = Returning(got, "laptop", 1650); err != nil {
		t.Fatalf("Returning: %v", err)
	}
	if err := store.Save(got); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if rec := do(t, s.HandleReport, reporter(), "POST", `{"status":"returned"}`,
		"id", "ord_1", "item", "laptop"); rec.Code != http.StatusOK {
		t.Fatalf("report = %d: %s", rec.Code, rec.Body)
	}
	if len(inv.revoked) != 1 || inv.revoked[0] != [2]string{"usr_ada", "laptop"} {
		t.Fatalf("revoked = %v, want the recipient's laptop", inv.revoked)
	}
	if len(inv.granted) != 0 {
		t.Errorf("granted = %v on a return", inv.granted)
	}
}

// Every other outcome leaves the inventory alone, and each for its own reason.
//
// Skipped is the one worth stating: it is *satisfied*, and it is satisfied
// precisely because the recipient already had the item. Recording it here would
// make a right that predates this order look like one this portal handed out,
// which is the distinction the origin field exists to keep.
func TestNoOtherOutcomeTouchesTheInventory(t *testing.T) {
	for _, status := range []LineStatus{StatusSkipped, StatusFailed, StatusRunning} {
		t.Run(string(status), func(t *testing.T) {
			s, _, inv, _ := inventoryFixture(t, Line{
				ItemID: "account", Status: StatusPending, ProvisionProcess: "prov",
			})
			if rec := do(t, s.HandleReport, reporter(), "POST", `{"status":"`+string(status)+`"}`,
				"id", "ord_1", "item", "account"); rec.Code != http.StatusOK {
				t.Fatalf("report = %d: %s", rec.Code, rec.Body)
			}
			if len(inv.granted) != 0 || len(inv.revoked) != 0 {
				t.Fatalf("%s moved the inventory: granted %v, revoked %v",
					status, inv.granted, inv.revoked)
			}
		})
	}
}

// A revocation that could not be run is not a right that ended. The line is
// recorded as returnFailed so somebody can retry it, and until that retry
// succeeds the person still holds the thing — which is the only answer an access
// record may give, because the target system has not been told otherwise.
func TestAFailedReturnLeavesTheRightStanding(t *testing.T) {
	s, _, inv, _ := inventoryFixture(t, Line{
		ItemID: "laptop", Status: StatusReturning,
		ProvisionProcess: "prov", DeprovisionProcess: "deprov",
	})

	if rec := do(t, s.HandleReport, reporter(), "POST", `{"status":"returnFailed"}`,
		"id", "ord_1", "item", "laptop"); rec.Code != http.StatusOK {
		t.Fatalf("report = %d: %s", rec.Code, rec.Body)
	}
	if len(inv.revoked) != 0 {
		t.Fatalf("a failed revocation ended the right: %v", inv.revoked)
	}
}

// An inventory that refuses is a 500 and not a 200, and the process is not woken.
//
// The order has already moved and nothing is coming to move it again, so the
// reporter is the one thing that can retry — the same contract the wake has. If
// this returned 200 the order would say the laptop is provisioned and the
// inventory would say nobody holds one, and nothing would ever reconcile them.
func TestAnInventoryThatRefusesStopsTheReport(t *testing.T) {
	s, _, inv, wakes := inventoryFixture(t, Line{
		ItemID: "account", Status: StatusPending, ProvisionProcess: "prov",
	})
	inv.err = errors.New("the log is not writable")

	rec := do(t, s.HandleReport, reporter(), "POST", `{"status":"done"}`, "id", "ord_1", "item", "account")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("report = %d, want 500: %s", rec.Code, rec.Body)
	}
	if *wakes != 0 {
		t.Errorf("the process was woken %d times although the right was not recorded", *wakes)
	}
}
