package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/model"
)

// Where one product is used (ADR-0353), over HTTP: the holders it counts, the
// requests it refuses, and the answers when the catalogue cannot be read or the
// server is going away.

// productUsagePathsRelease files a catalogue and a release of it carrying items.
func productUsagePathsRelease(t *testing.T, s *Server, catalogID string, items ...string) {
	t.Helper()
	rel := catalog.Release{ID: "rel_" + catalogID, CatalogID: catalogID, CreatedAt: 1}
	for _, id := range items {
		rel.Items = append(rel.Items, catalog.Item{ID: id})
	}
	var err error
	s.do(func() {
		if err = s.catalogStore.SaveCatalog(catalog.Catalog{ID: catalogID, Rank: 1}); err != nil {
			return
		}
		err = s.catalogStore.SaveRelease(rel)
	})
	if err != nil {
		t.Fatalf("seed release: %v", err)
	}
}

// productUsagePathsGet asks for one product's usage.
func productUsagePathsGet(t *testing.T, s *Server, id string) (int, string) {
	t.Helper()
	return recertifyHTTPPathsCall(t, s.Handler(), http.MethodGet, "/api/v1/catalog-products/"+id+"/usage", nil)
}

// TestProductUsageCountsOnlyThisProductsHolders. Holders are a scan of
// everybody's holdings, so the count must be of this product's — another product's
// holder counted here would make a service look in use that nobody holds.
func TestProductUsageCountsOnlyThisProductsHolders(t *testing.T) {
	srv := newServerForErrors(t)
	productUsagePathsRelease(t, srv, "cat_1", "vpn", "sap")
	recertifyHTTPPathsGrant(t, srv, model.EntitlementValue{Principal: "usr_ada", ItemID: "vpn",
		Since: 1_000, Origin: model.OriginLegacy})
	recertifyHTTPPathsGrant(t, srv, model.EntitlementValue{Principal: "usr_bo", ItemID: "sap",
		Since: 1_000, Origin: model.OriginOrdered})

	code, body := productUsagePathsGet(t, srv, "vpn")
	if code != http.StatusOK {
		t.Fatalf("usage = %d %s", code, body)
	}
	var rep usageReport
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Held.Total != 1 || rep.Held.ByOrigin["legacy"] != 1 || len(rep.Held.ByOrigin) != 1 {
		t.Errorf("held = %+v, want Ada's one legacy hold and not Bo's SAP", rep.Held)
	}
}

// TestProductUsageRefusals. A blank id names nothing; a product no released
// catalogue carries is not found — a catalogue that was never published is not
// evidence the product exists.
func TestProductUsageRefusals(t *testing.T) {
	srv := newServerForErrors(t)
	var err error
	srv.do(func() { err = srv.catalogStore.SaveCatalog(catalog.Catalog{ID: "cat_draft", Rank: 1}) })
	if err != nil {
		t.Fatalf("seed catalogue: %v", err)
	}

	if code, body := productUsagePathsGet(t, srv, "%20"); code != http.StatusBadRequest ||
		!strings.Contains(body, "name a product") {
		t.Errorf("blank = %d %s, want 400", code, body)
	}
	if code, body := productUsagePathsGet(t, srv, "vpn"); code != http.StatusNotFound ||
		!strings.Contains(body, "no released catalogue carries the product vpn") {
		t.Errorf("unreleased = %d %s, want 404", code, body)
	}
}

// TestProductUsageAnUnreadableCatalogueIsAFault. Neither a catalogue nor a
// release that cannot be decoded may be skipped: the answer would then omit
// exactly the catalogue that uses the product.
func TestProductUsageAnUnreadableCatalogueIsAFault(t *testing.T) {
	for _, tc := range []struct{ name, dir, key, store string }{
		{"a catalogue", "catalogs", "cat_1", "catalogstore"},
		{"a release", "releases", "rel_cat_1", "catalogreleasestore"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			productUsagePathsRelease(t, srv, "cat_1", "vpn")
			path := filepath.Join(srv.dataDir, "catalog", tc.dir, hex.EncodeToString([]byte(tc.key))+".json")
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				t.Fatalf("corrupt: %v", err)
			}
			if code, body := productUsagePathsGet(t, srv, "vpn"); code != http.StatusInternalServerError ||
				!strings.Contains(body, tc.store) {
				t.Errorf("usage = %d %s, want 500 naming %s", code, body, tc.store)
			}
		})
	}
}

// TestProductUsageAServerShuttingDownSaysSo, rather than "not found".
func TestProductUsageAServerShuttingDownSaysSo(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()
	if code, body := productUsagePathsGet(t, srv, "vpn"); code != http.StatusServiceUnavailable ||
		!strings.Contains(body, "shutting down") {
		t.Errorf("usage = %d %s, want 503", code, body)
	}
}
