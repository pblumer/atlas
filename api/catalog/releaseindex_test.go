package catalog

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// publishTimes publishes one catalogue n times and answers the releases in the
// order they were made.
func publishTimes(t *testing.T, s *Service, n int) (Catalog, []Release) {
	t.Helper()
	cat := decode[Catalog](t, do(t, s.HandleCreateCatalog, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"]}`))
	rec := do(t, s.HandleSaveItem, "POST", "/api/v1/catalog-items",
		`{"id":"laptop","homeCatalog":"`+cat.ID+`","state":"active","texts":{"de":"Laptop"},`+
			`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d (%s)", rec.Code, rec.Body)
	}
	do(t, s.HandleUpdateCatalog, "PATCH", "/x", `{"items":["laptop"]}`, "id", cat.ID)
	var made []Release
	for i := 0; i < n; i++ {
		pub := do(t, s.HandlePublish, "POST", "/x", "", "id", cat.ID)
		if pub.Code != http.StatusCreated {
			t.Fatalf("publish = %d (%s)", pub.Code, pub.Body)
		}
		made = append(made, decode[Release](t, pub))
	}
	return cat, made
}

// TestTheShopCanAskForTheNewestReleaseAlone: ?limit=1 answers the newest release
// and nothing else, the same one the full listing puts first.
func TestTheShopCanAskForTheNewestReleaseAlone(t *testing.T) {
	s := newService(t)
	cat, made := publishTimes(t, s, 4)

	all := decode[[]Release](t, do(t, s.HandleListReleases, "GET", "/x", "", "id", cat.ID))
	if len(all) != 4 {
		t.Fatalf("full listing has %d releases, want 4", len(all))
	}
	one := decode[[]Release](t, do(t, s.HandleListReleases, "GET", "/x?limit=1", "", "id", cat.ID))
	if len(one) != 1 || one[0].ID != made[3].ID || one[0].ID != all[0].ID {
		t.Fatalf("limit=1 answered %v, want only the newest %s", one, made[3].ID)
	}
	if len(one[0].Items) != 1 {
		t.Fatalf("the newest release lost its items: %+v", one[0])
	}
	two := decode[[]Release](t, do(t, s.HandleListReleases, "GET", "/x?limit=2", "", "id", cat.ID))
	if len(two) != 2 || two[0].ID != all[0].ID || two[1].ID != all[1].ID {
		t.Fatalf("limit=2 answered %v, want the first two of the full listing", two)
	}
	for _, bad := range []string{"0", "-1", "x"} {
		if rec := do(t, s.HandleListReleases, "GET", "/x?limit="+bad, "", "id", cat.ID); rec.Code != http.StatusBadRequest {
			t.Errorf("limit=%s = %d, want 400", bad, rec.Code)
		}
	}
}

// TestTheIndexFollowsTheDirectory: a release written or removed after the index
// was built is seen, including by something other than this store.
func TestTheIndexFollowsTheDirectory(t *testing.T) {
	s := newService(t)
	cat, made := publishTimes(t, s, 2)
	newest := func() string {
		got, err := s.store.NewestReleasesOf(cat.ID, 1)
		if err != nil || len(got) != 1 {
			t.Fatalf("NewestReleasesOf = %v, %v", got, err)
		}
		return got[0].ID
	}
	if newest() != made[1].ID {
		t.Fatal("the index does not answer the newest release")
	}
	// A third publish after the index was built.
	pub := do(t, s.HandlePublish, "POST", "/x", "", "id", cat.ID)
	third := decode[Release](t, pub)
	if newest() != third.ID {
		t.Fatal("a release published after the index was built is not seen")
	}
	// The newest removed from disk behind the store's back.
	if err := os.Remove(s.store.releases.FileFor(third.ID)); err != nil {
		t.Fatal(err)
	}
	if newest() != made[1].ID {
		t.Fatal("a release removed from disk is still answered")
	}
	// A file that is no release does not break the listing.
	if err := os.WriteFile(filepath.Join(s.store.releases.Dir(), "stray.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if newest() != made[1].ID {
		t.Fatal("a stray file changed the answer")
	}
	// Another catalogue's releases are not answered for this one.
	if got, _ := s.store.NewestReleasesOf("cat_none", 3); len(got) != 0 {
		t.Fatalf("an unknown catalogue answered %v", got)
	}
}
