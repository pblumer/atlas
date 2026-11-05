package api_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

// Pruning a catalogue's old releases (catalogpurge.go), through the whole stack:
// the newest release and every release an order names stay, the rest go, only an
// administrator may do it, and only with the catalogue id repeated.

type prunePlanResp struct {
	Catalog string `json:"catalog"`
	Newest  string `json:"newest"`
	Kept    []struct {
		Release string `json:"release"`
		Why     string `json:"why"`
		Orders  int    `json:"orders"`
	} `json:"kept"`
	Releases []string `json:"releases"`
	Done     bool     `json:"done"`
}

func TestPruningKeepsTheNewestAndEveryReleaseAnOrderNames(t *testing.T) {
	sh := aShopToPurge(t)
	// sh.rel is the first release; an order names it.
	ordered := sh.rel
	sh.order(t, "vpn")
	// Three more publishes: two that nothing names, then the newest.
	var later []string
	for i := 0; i < 3; i++ {
		code, body := cReq(t, sh.admin, sh.ts, "POST", "/api/v1/catalogs/"+sh.cat+"/releases", "")
		if code != http.StatusCreated {
			t.Fatalf("publish: %d (%s)", code, body)
		}
		later = append(later, idOf(t, body))
	}
	newest := later[2]

	plan := func(method, path string) (int, prunePlanResp) {
		t.Helper()
		code, body := cReq(t, sh.admin, sh.ts, method, path, "")
		var p prunePlanResp
		if code == http.StatusOK {
			if err := json.Unmarshal(body, &p); err != nil {
				t.Fatalf("decode the plan: %v (%s)", err, body)
			}
		}
		return code, p
	}
	code, p := plan("GET", "/api/v1/catalogs/"+sh.cat+"/releases/pruning")
	if code != http.StatusOK {
		t.Fatalf("preview = %d", code)
	}
	if p.Newest != newest || p.Done {
		t.Fatalf("preview = %+v, want newest %s and not done", p, newest)
	}
	gone := []string{later[1], later[0]}
	if !slices.Equal(p.Releases, gone) {
		t.Fatalf("would prune %v, want %v (newest first)", p.Releases, gone)
	}
	if len(p.Kept) != 2 || p.Kept[0].Why != "newest" || p.Kept[1].Release != ordered ||
		p.Kept[1].Why != "orders" || p.Kept[1].Orders != 1 {
		t.Fatalf("kept = %+v, want the newest and the ordered one", p.Kept)
	}
	// The preview changed nothing.
	if code, body := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalogs/"+sh.cat+"/releases", ""); code != http.StatusOK || countReleases(t, body) != 4 {
		t.Fatalf("after the preview: %d, %d releases, want 4", code, countReleases(t, body))
	}

	// Without the id repeated, nothing goes.
	if code, body := cReq(t, sh.admin, sh.ts, "POST", "/api/v1/catalogs/"+sh.cat+"/releases/pruning", ""); code != http.StatusBadRequest {
		t.Fatalf("prune without confirm = %d (%s), want 400", code, body)
	}
	// Only an administrator.
	if code, body := cReq(t, sh.alice, sh.ts, "POST", "/api/v1/catalogs/"+sh.cat+"/releases/pruning?confirm="+sh.cat, ""); code != http.StatusForbidden {
		t.Fatalf("alice prunes = %d (%s), want 403", code, body)
	}

	code, p = plan("POST", "/api/v1/catalogs/"+sh.cat+"/releases/pruning?confirm="+sh.cat)
	if code != http.StatusOK || !p.Done || !slices.Equal(p.Releases, gone) {
		t.Fatalf("prune = %d %+v", code, p)
	}
	code, body := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalogs/"+sh.cat+"/releases", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d", code)
	}
	var left []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &left); err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 || left[0].ID != newest || left[1].ID != ordered {
		t.Fatalf("left = %+v, want the newest and the ordered release", left)
	}
	// Pruned again, nothing more goes.
	if code, p = plan("POST", "/api/v1/catalogs/"+sh.cat+"/releases/pruning?confirm="+sh.cat); code != http.StatusOK || len(p.Releases) != 0 {
		t.Fatalf("second prune = %d %+v, want nothing to prune", code, p)
	}
	// And an unknown catalogue is not found.
	if code, _ := cReq(t, sh.admin, sh.ts, "GET", "/api/v1/catalogs/nope/releases/pruning", ""); code != http.StatusNotFound {
		t.Fatalf("unknown catalogue = %d, want 404", code)
	}
}

func countReleases(t *testing.T, body []byte) int {
	t.Helper()
	var list []json.RawMessage
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode releases: %v (%s)", err, body)
	}
	return len(list)
}
