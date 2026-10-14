package api

import "testing"

// The client registry is listed oldest-first, and that order is not decoration: the
// eviction that keeps a self-registration flood from filling the registry takes the
// oldest, and "the oldest" is whatever this comparator says it is (oauthregister.go).
//
// It is a two-level order because one level does not settle it. CreatedAt is a whole
// second, and a burst registers many inside one — so a listing ordered by the clock
// alone would hand back an arbitrary member of the burst as its oldest, and evicting
// "the oldest" would mean evicting an arbitrary one. The id breaks the tie, which
// makes the order total and the eviction repeatable.
func TestClientsAreListedOldestFirstAndTiesBreakOnTheId(t *testing.T) {
	store, err := newOAuthClientStore(t.TempDir())
	if err != nil {
		t.Fatalf("open the client store: %v", err)
	}
	// Saved in an order that is neither the answer nor its reverse, so a listing that
	// happens to come back in write order or in filename order fails here too.
	for _, c := range []oauthClient{
		{ID: "zebra", CreatedAt: 200},
		{ID: "alpha", CreatedAt: 300},
		{ID: "beta", CreatedAt: 100},
		{ID: "atlas", CreatedAt: 300}, // same second as alpha: the id decides
	} {
		if err := store.Save(c); err != nil {
			t.Fatalf("save %s: %v", c.ID, err)
		}
	}

	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("list the clients: %v", err)
	}
	want := []string{"beta", "zebra", "alpha", "atlas"}
	if len(got) != len(want) {
		t.Fatalf("listed %d clients, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			ids := make([]string, len(got))
			for j, c := range got {
				ids[j] = c.ID
			}
			t.Fatalf("listed %v, want %v (position %d)", ids, want, i)
		}
	}
}
