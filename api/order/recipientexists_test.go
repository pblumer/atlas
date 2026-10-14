package order

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// An order names somebody, and the name has to be somebody
// (ADR-0356).
//
// The recipient is a string out of the request body and nothing resolved it. An
// order for "Ada Lovelace" — a display name, a typo, a person who left — placed
// as readily as one for a principal id: it put an approval in nobody's inbox,
// provisioned against nothing, and left a right attached to a string.
//
// It was not silently accepted, as it turned out: the eligibility check has to
// know the recipient's groups, and asking that fails for a name nobody holds. But
// it failed as **500**, which says the server is broken when the request is, and
// hands whoever typed the name an error they cannot act on.

// placing builds a service whose recipient lookup answers however the test says.
func placing(t *testing.T, groups func(string) ([]string, error)) *Service {
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
	return New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		groups, mayOrderForAnyone,
		func(message, orderID string, vars map[string]string) error { return nil },
		func() string { return "https://atlas.example.ch" },
		func() string { return "http://atlas.test" },
		ignoreGrant, ignoreRevoke, holdsNothing)
}

// TestAnOrderForNobodyIsRefusedAsBadInput.
func TestAnOrderForNobodyIsRefusedAsBadInput(t *testing.T) {
	nobody := func(who string) ([]string, error) {
		return nil, fmt.Errorf("%w: no account %q; name somebody by principal id, "+
			"username, directory id or mail address", httpapi.ErrNoSuchPrincipal, who)
	}
	got := do(t, placing(t, nobody).HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"],"recipient":"Ada Lovelace"}`)
	if got.Code != http.StatusBadRequest {
		t.Fatalf("an order for a name nobody holds = %d, want 400 (%s)",
			got.Code, got.Body.String())
	}
	// And it says what to type instead. A refusal that only says "no" leaves the
	// orderer guessing which of four spellings Atlas wanted.
	for _, want := range []string{"Ada Lovelace", "username", "mail address"} {
		if !strings.Contains(got.Body.String(), want) {
			t.Errorf("the refusal does not mention %q: %s", want, got.Body.String())
		}
	}
}

// TestAnUnreadableUserStoreIsStillTheServersFault: the other half, and the reason
// this is a sentinel rather than a status picked from an error string. A store
// that could not be read is not a statement about the recipient, and answering
// 400 would tell an operator their colleague does not exist.
func TestAnUnreadableUserStoreIsStillTheServersFault(t *testing.T) {
	broken := func(string) ([]string, error) { return nil, errors.New("disk is on fire") }
	got := do(t, placing(t, broken).HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"],"recipient":"usr_kollegin"}`)
	if got.Code != http.StatusInternalServerError {
		t.Fatalf("an order placed while the user store is unreadable = %d, want 500 (%s)",
			got.Code, got.Body.String())
	}
}

// TestOrderingForYourselfAsksNobodyAnything: the ordinary case must not pay for
// this. An order with no recipient is for the caller, who is by definition
// somebody — and a self-service portal that failed when the directory was slow
// would be a portal that failed.
func TestOrderingForYourselfAsksNobodyAnything(t *testing.T) {
	asked := ""
	watching := func(who string) ([]string, error) { asked = who; return nil, nil }
	got := do(t, placing(t, watching).HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`)
	if got.Code != http.StatusCreated {
		t.Fatalf("ordering for oneself = %d, want 201 (%s)", got.Code, got.Body.String())
	}
	if asked != "usr_1" {
		t.Errorf("the recipient asked about was %q, want the caller's own id", asked)
	}
}
