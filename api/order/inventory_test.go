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
		inv.grant, inv.revoke, holdsNothing)

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

// basketRelease is a catalogue with one item that may be held once and one that
// may be held twice, so the second resolution has both cases to decide.
func basketRelease(t *testing.T) catalog.Release {
	t.Helper()
	items := []catalog.Item{
		{ID: "vpn", HomeCatalog: "cat", State: catalog.StateActive,
			Texts: map[string]string{"de": "VPN"}, Approval: catalog.Approval{Kind: catalog.KindNone},
			ProvisionProcess: "prov", DeprovisionProcess: "deprov"},
		{ID: "lizenz", HomeCatalog: "cat", State: catalog.StateActive,
			Texts: map[string]string{"de": "Lizenz"}, Approval: catalog.Approval{Kind: catalog.KindNone},
			ProvisionProcess: "prov", DeprovisionProcess: "deprov", MultipleAllowed: true},
	}
	rel, problems := catalog.Publish(catalog.Input{
		Catalogs: []catalog.Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"vpn", "lizenz"}}},
		Items: items,
	})
	if len(problems) != 0 {
		t.Fatalf("Publish: %v", problems)
	}
	rel.ID, rel.CatalogID, rel.CreatedAt = "rel_1", "cat", 1000
	return rel
}

// basketService is a service whose caller already holds whatever holds names.
func basketService(t *testing.T, holds ...string) (*Service, *Store) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	rel := basketRelease(t)
	has := map[string]bool{}
	for _, id := range holds {
		has[id] = true
	}
	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(message, orderID string, vars map[string]string) error { return nil },
		func() string { return "https://atlas.example.ch" },
		ignoreGrant, ignoreRevoke,
		func(string) (map[string]bool, error) { return has, nil })
	return s, store
}

func lineStatusOf(t *testing.T, o Order, itemID string) LineStatus {
	t.Helper()
	for _, l := range o.Lines {
		if l.ItemID == itemID {
			return l.Status
		}
	}
	t.Fatalf("order carries no line for %s: %+v", itemID, o.Lines)
	return ""
}

// The basket's second resolution: something the recipient already holds, and may
// not hold twice, is ordered as skipped rather than provisioned again.
//
// Skipped and not dropped. The request was made, and an order that silently
// omitted it could not answer "I ordered a VPN, where is it" — the honest answer
// is "you already had one", and only a recorded line can give it.
func TestSomethingAlreadyHeldIsOrderedAsSkipped(t *testing.T) {
	s, _ := basketService(t, "vpn")

	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_ada"), "POST",
		`{"releaseId":"rel_1","items":["vpn","lizenz"]}`))

	if got := lineStatusOf(t, placed, "vpn"); got != StatusSkipped {
		t.Errorf("the vpn is %s, want skipped — the recipient already holds one", got)
	}
	// Nothing is started for it either: a skipped line is settled, not waiting.
	for _, id := range Next(placed) {
		if id == "vpn" {
			t.Error("the vpn is offered for provisioning although it is already held")
		}
	}
}

// An item that says it may be held more than once is ordered again, held or not.
// Two licences and two mailboxes are ordinary, and a portal that refused the
// second would be answering from its own tidiness rather than from the catalogue.
func TestSomethingThatMayBeHeldTwiceIsOrderedAgain(t *testing.T) {
	s, _ := basketService(t, "vpn", "lizenz")

	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_ada"), "POST",
		`{"releaseId":"rel_1","items":["vpn","lizenz"]}`))

	if got := lineStatusOf(t, placed, "lizenz"); got != StatusPending {
		t.Errorf("the licence is %s, want pending — the catalogue allows a second", got)
	}
}

// An unreadable inventory refuses the order rather than placing one that would
// provision a second copy of something. Ordering twice is visible in a target
// system and undoing it is somebody's afternoon; refusing is a retry.
func TestAnUnreadableInventoryRefusesTheOrder(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	rel := basketRelease(t)
	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(message, orderID string, vars map[string]string) error { return nil },
		func() string { return "" },
		ignoreGrant, ignoreRevoke,
		func(string) (map[string]bool, error) { return nil, errors.New("the store is gone") })

	rec := do(t, s.HandlePlace, someone("usr_ada"), "POST", `{"releaseId":"rel_1","items":["vpn"]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("place = %d, want 500: %s", rec.Code, rec.Body)
	}
	all, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("%d orders were placed although the inventory could not be read", len(all))
	}
}
