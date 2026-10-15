package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Favourite and recent diagrams, end to end (ADR-draft-diagram-shortcuts).

type shortcutEntry struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Name      string `json:"name"`
	Href      string `json:"href"`
	Deployed  bool   `json:"deployed"`
	OpenedAt  int64  `json:"openedAt"`
}

type shortcutsResp struct {
	Favourites []shortcutEntry `json:"favourites"`
	Recent     []shortcutEntry `json:"recent"`
}

func shortcutsOf(t *testing.T, ts *httptest.Server, c *http.Client) shortcutsResp {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/diagram-shortcuts", "")
	if code != http.StatusOK {
		t.Fatalf("read shortcuts: %d (%s)", code, body)
	}
	var got shortcutsResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode shortcuts: %v (%s)", err, body)
	}
	if got.Favourites == nil || got.Recent == nil {
		t.Fatalf("an empty list is served as null: %s", body)
	}
	return got
}

// TestAFavouriteDiagramIsTheCallersOwnAndResolvesAgainstItsScope marks a draft, sees
// it resolve to its name and route, and checks the three things a bookmark must not
// do: reach another account's list, let somebody mark a diagram they cannot open, or
// keep naming a diagram after it is gone — while still being clearable then.
func TestAFavouriteDiagramIsTheCallersOwnAndResolvesAgainstItsScope(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	other := twoUsers(t, ts, admin, "mallory")[0]

	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/drafts", "application/xml", draftBPMN); code != http.StatusOK {
		t.Fatalf("save draft: %d (%s)", code, b)
	}
	if got := shortcutsOf(t, ts, admin); len(got.Favourites) != 0 || len(got.Recent) != 0 {
		t.Fatalf("a fresh account has %+v", got)
	}

	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/diagram-shortcuts/favourites/process/wip-order", ""); code != http.StatusOK {
		t.Fatalf("mark: %d (%s)", code, b)
	}
	// Twice is the state asked for, not a second entry.
	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/diagram-shortcuts/favourites/process/wip-order", ""); code != http.StatusOK {
		t.Fatalf("mark again: %d (%s)", code, b)
	}
	got := shortcutsOf(t, ts, admin)
	if len(got.Favourites) != 1 {
		t.Fatalf("favourites = %+v, want the one draft", got.Favourites)
	}
	if f := got.Favourites[0]; !f.Available || f.Name != "Order fulfillment" || f.Href != "#/modeler/draft/wip-order" {
		t.Errorf("favourite = %+v, want the draft's name and its Modeler route", f)
	}

	// The draft is the administrator's own and ungrouped, so another account cannot
	// open it — and so cannot mark it, record it, or see the administrator's list.
	if got := shortcutsOf(t, ts, other); len(got.Favourites) != 0 {
		t.Errorf("another account reads %+v; a favourites list is one person's", got.Favourites)
	}
	if code, b := cReq(t, other, ts, "PUT", "/api/v1/diagram-shortcuts/favourites/process/wip-order", ""); code != http.StatusNotFound {
		t.Errorf("marking a diagram the caller cannot open: %d (%s), want 404", code, b)
	}
	if code, b := cReq(t, other, ts, "POST", "/api/v1/diagram-shortcuts/recent", `{"kind":"process","id":"wip-order"}`); code != http.StatusNotFound {
		t.Errorf("recording a diagram the caller cannot open: %d (%s), want 404", code, b)
	}
	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/diagram-shortcuts/favourites/form/wip-order", ""); code != http.StatusBadRequest {
		t.Errorf("a kind that is not a diagram: %d (%s), want 400", code, b)
	}

	// A visit lands at the top of the recent list.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/diagram-shortcuts/recent", `{"kind":"process","id":"wip-order"}`); code != http.StatusNoContent {
		t.Fatalf("record visit: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/diagram-shortcuts/recent", `{"kind":"process","id":"no-such-process"}`); code != http.StatusNotFound {
		t.Errorf("recording a diagram that does not exist: %d (%s), want 404", code, b)
	}
	got = shortcutsOf(t, ts, admin)
	if len(got.Recent) != 1 || got.Recent[0].ID != "wip-order" || got.Recent[0].OpenedAt == 0 {
		t.Fatalf("recent = %+v, want the draft with its time", got.Recent)
	}

	// The draft goes. The mark stays, unnamed and unopenable, so it can be cleared;
	// the visit is no longer history anybody can use and drops out.
	if code, b := cReq(t, admin, ts, "DELETE", "/api/v1/drafts/wip-order", ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("delete draft: %d (%s)", code, b)
	}
	got = shortcutsOf(t, ts, admin)
	if len(got.Favourites) != 1 || got.Favourites[0].Available || got.Favourites[0].Name != "" || got.Favourites[0].Href != "" {
		t.Errorf("favourites after delete = %+v, want the mark alone, unavailable and unnamed", got.Favourites)
	}
	if len(got.Recent) != 0 {
		t.Errorf("recent after delete = %+v, want nothing", got.Recent)
	}
	if code, b := cReq(t, admin, ts, "DELETE", "/api/v1/diagram-shortcuts/favourites/process/wip-order", ""); code != http.StatusOK {
		t.Fatalf("clear a mark that no longer resolves: %d (%s)", code, b)
	}
	if got := shortcutsOf(t, ts, admin); len(got.Favourites) != 0 {
		t.Errorf("favourites after clear = %+v, want none", got.Favourites)
	}
}
