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
	"github.com/pblumer/atlas/api/order"
)

// Moving a product's held lines onto its lifecycle process (ADR-0428), when the
// catalogue or an order cannot be read or written, and for a line whose process
// is running right now.

// rebindPathsProduct files a home catalogue and a product in it that binds a
// lifecycle process — the shape a rebind moves lines onto.
func rebindPathsProduct(t *testing.T, s *Server) {
	t.Helper()
	var err error
	s.do(func() {
		if err = s.catalogStore.SaveCatalog(catalog.Catalog{ID: "cat_home", Rank: 1}); err != nil {
			return
		}
		err = s.catalogStore.SaveItem(catalog.Item{ID: "vpn", HomeCatalog: "cat_home",
			LifecycleProcess: "vpn-lifecycle", LifecycleForm: catalog.FormPerPosition,
			Operations: map[string]string{catalog.OpChange: "Change"}})
	})
	if err != nil {
		t.Fatalf("seed product: %v", err)
	}
}

// rebindPathsPost asks for the rebind.
func rebindPathsPost(t *testing.T, s *Server) (int, string) {
	t.Helper()
	return recertifyHTTPPathsCall(t, s.Handler(), http.MethodPost, "/api/v1/catalog-products/vpn/rebind",
		strings.NewReader(`{"reason":"moved to the lifecycle model"}`))
}

// rebindPathsHeld is an order of one held line on the old two-process binding.
func rebindPathsHeld(id string, status order.LineStatus) order.Order {
	return order.Order{ID: id, Orderer: "usr_ada", Recipient: "usr_ada", Lines: []order.Line{{
		ItemID: "vpn", Status: status, ProvisionProcess: "p-old", DeprovisionProcess: "d-old"}}}
}

// TestRebindALineWhoseProcessIsRunningIsLeftWhereItIs. Rebinding a line under a
// running provisioning would have that process report into a binding it was not
// started from; the line stays, and the answer does not list its order.
func TestRebindALineWhoseProcessIsRunningIsLeftWhereItIs(t *testing.T) {
	srv := newServerForErrors(t)
	rebindPathsProduct(t, srv)
	orderCancelPathsSave(t, srv, rebindPathsHeld("ord_running", order.StatusRunning))
	orderCancelPathsSave(t, srv, rebindPathsHeld("ord_held", order.StatusDone))

	code, body := rebindPathsPost(t, srv)
	if code != http.StatusOK {
		t.Fatalf("rebind = %d %s", code, body)
	}
	var resp rebindResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Lines != 1 || len(resp.Orders) != 1 || resp.Orders[0] != "ord_held" {
		t.Errorf("resp = %+v, want only the held line moved", resp)
	}
	if l := orderCancelPathsGet(t, srv, "ord_running").Lines[0]; l.LifecycleProcess != "" || l.ProvisionProcess != "p-old" {
		t.Errorf("running line = %+v, want it on its old binding", l)
	}
	if l := orderCancelPathsGet(t, srv, "ord_held").Lines[0]; l.LifecycleProcess != "vpn-lifecycle" {
		t.Errorf("held line = %+v, want it on the lifecycle process", l)
	}
}

// TestRebindAStoreThatCannotBeReadOrWrittenIsAFault. An unreadable home catalogue
// cannot answer who may edit, an unreadable order cannot be scanned, and an order
// that cannot be written stays as it was — each a 500, never a partial move
// reported as a whole one.
func TestRebindAStoreThatCannotBeReadOrWrittenIsAFault(t *testing.T) {
	t.Run("the home catalogue", func(t *testing.T) {
		srv := newServerForErrors(t)
		rebindPathsProduct(t, srv)
		path := filepath.Join(srv.dataDir, "catalog", "catalogs", hex.EncodeToString([]byte("cat_home"))+".json")
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatalf("corrupt catalogue: %v", err)
		}
		if code, body := rebindPathsPost(t, srv); code != http.StatusInternalServerError ||
			!strings.Contains(body, "catalogstore") {
			t.Errorf("rebind = %d %s, want 500 naming the catalogue store", code, body)
		}
	})

	t.Run("the orders", func(t *testing.T) {
		srv := newServerForErrors(t)
		rebindPathsProduct(t, srv)
		orderCancelPathsSave(t, srv, rebindPathsHeld("ord_torn", order.StatusDone))
		orderCancelPathsCorrupt(t, srv, "ord_torn")
		if code, body := rebindPathsPost(t, srv); code != http.StatusInternalServerError ||
			!strings.Contains(body, "orderstore") {
			t.Errorf("rebind = %d %s, want 500 naming the order store", code, body)
		}
	})

	t.Run("an order that cannot be written", func(t *testing.T) {
		srv := newServerForErrors(t)
		rebindPathsProduct(t, srv)
		orderCancelPathsSave(t, srv, rebindPathsHeld("ord_stuck", order.StatusDone))
		reconcileApplyPathsBlockSave(t, filepath.Join(srv.dataDir, "orders", hex.EncodeToString([]byte("ord_stuck"))+".json"))

		if code, body := rebindPathsPost(t, srv); code != http.StatusInternalServerError ||
			!strings.Contains(body, "rebind:") {
			t.Errorf("rebind = %d %s, want 500", code, body)
		}
		if l := orderCancelPathsGet(t, srv, "ord_stuck").Lines[0]; l.LifecycleProcess != "" {
			t.Errorf("line = %+v, want it on its old binding", l)
		}
	})
}
