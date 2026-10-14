package api

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// favouritesPathsAs builds a favourites request carrying a principal, the way the
// access boundary would hand it on.
func favouritesPathsAs(method, item string, p *httpapi.Principal) *http.Request {
	req := httptest.NewRequest(method, "/api/v1/shop/favourites/"+strings.TrimSpace(item), nil)
	req.SetPathValue("itemId", item)
	if p != nil {
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), p))
	}
	return req
}

// TestFavouritesRefusesAMarkWithoutAnAccountOrAProduct: a mark belongs to an
// account and names a product. Without the first there is nobody to file it under,
// and without the second there is nothing to file — neither is stored.
func TestFavouritesRefusesAMarkWithoutAnAccountOrAProduct(t *testing.T) {
	srv := newServerForErrors(t)
	ada := &httpapi.Principal{UserID: "usr_ada", Username: "ada"}

	for name, tc := range map[string]struct {
		req  *http.Request
		says string
	}{
		"nobody signed in": {favouritesPathsAs(http.MethodPut, "vpn", nil), "nobody is signed in"},
		"no product":       {favouritesPathsAs(http.MethodPut, "  ", ada), "name a product"},
	} {
		rec := httptest.NewRecorder()
		srv.handleSetFavourite(rec, tc.req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.says) {
			t.Errorf("%s: %d (%s), want 400 saying %q", name, rec.Code, rec.Body, tc.says)
		}
	}
	if entries, err := os.ReadDir(srv.favourites.Dir()); err != nil || len(entries) != 0 {
		t.Errorf("favourites store after refusals = %v (%v), want empty", entries, err)
	}
}

// TestFavouritesListsAStoredNullAsAnEmptyList: a record written with no items at
// all — by an older build, or by hand — is still a list. Serving null would break
// every client that iterates it.
func TestFavouritesListsAStoredNullAsAnEmptyList(t *testing.T) {
	srv := newServerForErrors(t)
	var err error
	srv.do(func() { err = srv.favourites.Save(favourites{Principal: "usr_ada"}) })
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.handleListFavourites(rec, favouritesPathsAs(http.MethodGet, "", &httpapi.Principal{UserID: "usr_ada"}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"itemIds":[]`) {
		t.Errorf("list: %d (%s), want an empty array", rec.Code, rec.Body)
	}
}

// TestFavouritesFailsOnAListItCannotRead: a stored list that cannot be decoded is
// neither "nothing marked" nor something to overwrite. Marking over it would replace
// every star the person had with one.
func TestFavouritesFailsOnAListItCannotRead(t *testing.T) {
	srv := newServerForErrors(t)
	corrupt(t, srv.favourites.Dir(), "usr_ada")
	file := filepath.Join(srv.favourites.Dir(), hex.EncodeToString([]byte("usr_ada"))+".json")
	ada := &httpapi.Principal{UserID: "usr_ada"}

	rec := httptest.NewRecorder()
	srv.handleListFavourites(rec, favouritesPathsAs(http.MethodGet, "", ada))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "favourites: ") {
		t.Errorf("list: %d (%s), want 500", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	srv.handleSetFavourite(rec, favouritesPathsAs(http.MethodPut, "vpn", ada))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "favourites: ") {
		t.Errorf("mark: %d (%s), want 500", rec.Code, rec.Body)
	}
	if got, err := os.ReadFile(file); err != nil || !bytes.Equal(got, []byte("{")) {
		t.Errorf("the unreadable list was rewritten: %q (%v)", got, err)
	}
}

// TestFavouritesDuringShutdown: a list that was never read must not be served as an
// empty one, and a mark that was never written must not be answered as made.
func TestFavouritesDuringShutdown(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()
	ada := &httpapi.Principal{UserID: "usr_ada"}

	rec := httptest.NewRecorder()
	srv.handleListFavourites(rec, favouritesPathsAs(http.MethodGet, "", ada))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("list during shutdown: %d (%s), want 503", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	srv.handleClearFavourite(rec, favouritesPathsAs(http.MethodDelete, "vpn", ada))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("clear during shutdown: %d (%s), want 503", rec.Code, rec.Body)
	}
}
