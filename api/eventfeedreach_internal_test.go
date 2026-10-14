package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
)

// The event feed narrowed by catalogue: an events token minted with a reach of
// catalogues reads the rows about the products those catalogues maintain, and the
// cursor moves past the rest.

// feedPageAs reads one page of the event feed as the given principal.
func feedPageAs(t *testing.T, srv *Server, p *httpapi.Principal, query string) (int, eventPage, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/events"+query, nil)
	srv.handleListEvents(w, r.WithContext(httpapi.WithPrincipal(r.Context(), p)))
	var page eventPage
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode page: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, page, w.Body.String()
}

// feedReachFixture writes two catalogues, a product each — vpn maintained by IT,
// badge by HR — and four facts: a grant of each with its provision's outcome, and a
// right a commissioning load adopted for an item the catalogue does not have.
func feedReachFixture(t *testing.T, srv *Server) {
	t.Helper()
	var err error
	srv.do(func() {
		for _, c := range []string{"cat-it", "cat-hr"} {
			if err = srv.catalogStore.SaveCatalog(catalog.Catalog{ID: c}); err != nil {
				return
			}
		}
		if err = srv.catalogStore.SaveItem(catalog.Item{ID: "vpn", HomeCatalog: "cat-it"}); err != nil {
			return
		}
		if err = srv.catalogStore.SaveItem(catalog.Item{ID: "badge", HomeCatalog: "cat-hr"}); err != nil {
			return
		}
		for i, item := range []string{"vpn", "badge"} {
			order := "ord_" + item
			srv.proc.GrantEntitlementWithOutcome(
				model.EntitlementValue{Principal: "usr_ada", ItemID: item, OrderID: order, Since: int64(1000 + i), Origin: model.OriginOrdered},
				model.ActionOutcomeValue{OrderID: order, Position: item, CommandID: "order:" + order + ":" + item + ":provision:1",
					Source: "atlas:order", Action: "provision", Effect: "provision", Outcome: "completed",
					Principal: "usr_ada", ItemID: item, At: int64(1000 + i)})
		}
		srv.proc.GrantEntitlement(model.EntitlementValue{Principal: "usr_bob", ItemID: "ghost", Since: 3000, Origin: model.OriginAdopted})
		err = srv.proc.RunUntilIdle()
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
}

// eventsToken is the principal an events token minted with this reach authenticates as.
func eventsToken(reach ...string) *httpapi.Principal {
	return &httpapi.Principal{UserID: apiTokenPrincipalPrefix + "tok-1", Roles: []string{RoleFeedReader},
		Scope: apiScopeEvents, Reach: reach}
}

// homeOf is the homeCatalog an event's data names, or "" for none.
func homeOf(ev cloudEvent) string {
	home, _ := ev.Data.(map[string]any)["homeCatalog"].(string)
	return home
}

// TestAReachOfCataloguesNarrowsTheFeedToTheirProducts: a reader of the whole feed gets
// every row, each naming the catalogue that maintains its product — the adopted right
// to an item the catalogue does not have names none. A token narrowed to IT gets the
// vpn's grant and outcome and nothing of HR's, and its cursor passes over the rows it
// was not given, so the next page does not read them again. A token narrowed to both
// gets both, and a reach naming no catalogue at all gets nothing — never the rows
// whose product has no home.
func TestAReachOfCataloguesNarrowsTheFeedToTheirProducts(t *testing.T) {
	srv := newServerWithOptions(t)
	feedReachFixture(t, srv)

	code, all, body := feedPageAs(t, srv, nil, "")
	if code != http.StatusOK || len(all.Events) != 5 {
		t.Fatalf("the whole feed = %d (%s), want five rows", code, body)
	}
	var homes []string
	for _, ev := range all.Events {
		homes = append(homes, homeOf(ev))
	}
	if got := strings.Join(homes, ","); got != "cat-it,cat-it,cat-hr,cat-hr," {
		t.Fatalf("the rows' homes = %s, want IT's two, HR's two and the adopted right's none", got)
	}

	code, it, body := feedPageAs(t, srv, eventsToken("cat-it"), "")
	if code != http.StatusOK || len(it.Events) != 2 || it.More {
		t.Fatalf("narrowed to IT = %d (%s), want the vpn's two rows", code, body)
	}
	for _, ev := range it.Events {
		if homeOf(ev) != "cat-it" || !strings.HasPrefix(ev.Subject, "orders/ord_vpn/") {
			t.Errorf("IT was given %s about %s, homed in %q", ev.Type, ev.Subject, homeOf(ev))
		}
	}
	if it.Next != all.Next {
		t.Fatalf("IT's cursor = %s, want it past the rows it was not given (%s)", it.Next, all.Next)
	}
	if code, again, _ := feedPageAs(t, srv, eventsToken("cat-it"), "?after="+it.Next); code != http.StatusOK ||
		len(again.Events) != 0 || again.Next != it.Next {
		t.Fatalf("IT's next page = %d %+v, want empty and the same cursor", code, again)
	}

	if _, both, _ := feedPageAs(t, srv, eventsToken("cat-it", "cat-hr"), ""); len(both.Events) != 4 {
		t.Fatalf("narrowed to both = %d rows, want four", len(both.Events))
	}
	if _, none, _ := feedPageAs(t, srv, eventsToken("cat-gone"), ""); len(none.Events) != 0 || none.Next != all.Next {
		t.Fatalf("narrowed to no catalogue = %+v, want nothing and the cursor at the end", none)
	}
}

// TestANarrowedPageStopsAtItsScanBound: a reader whose catalogues hold few of the rows
// does not make one request walk the whole feed. The page is cut after the rows it may
// read, with what it found, the cursor at the last row read and `more`, and the next
// page carries on from there.
func TestANarrowedPageStopsAtItsScanBound(t *testing.T) {
	srv := newServerWithOptions(t)
	feedReachFixture(t, srv)
	_, all, _ := feedPageAs(t, srv, nil, "")
	defer func(n int) { eventFeedScan = n }(eventFeedScan)
	eventFeedScan = 3

	_, first, body := feedPageAs(t, srv, eventsToken("cat-hr"), "")
	if len(first.Events) != 1 || !first.More || first.Next != all.Events[2].ID[strings.LastIndex(all.Events[2].ID, ":")+1:] {
		t.Fatalf("the first bounded page = %s, want HR's grant, more, and the cursor at the third row", body)
	}
	_, second, body := feedPageAs(t, srv, eventsToken("cat-hr"), "?after="+first.Next)
	if len(second.Events) != 1 || second.More || second.Next != all.Next || second.Events[0].Type != "atlas.action.completed" {
		t.Fatalf("the second page = %s, want HR's outcome and the end", body)
	}
}

// TestTheFeedFailsRatherThanPassOverARowItCannotPlace: a catalogue store that cannot be
// read is answered 500. Passing the row over would move a narrowed reader's cursor past
// a row that may have been its own, which no later page could give back.
func TestTheFeedFailsRatherThanPassOverARowItCannotPlace(t *testing.T) {
	srv := newServerWithOptions(t)
	feedReachFixture(t, srv)
	approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "catalog", "items"))

	if code, _, body := feedPageAs(t, srv, eventsToken("cat-it"), ""); code != http.StatusInternalServerError ||
		!strings.Contains(body, "event feed") {
		t.Fatalf("over an unreadable catalogue = %d (%s), want 500", code, body)
	}
}

// TestAnEventsTokensReachIsCataloguesItsMinterMaintains: the reach of an events token
// names catalogues. One this server does not have is refused, and so is one the minter
// does not maintain — being its audience is not enough, because the feed is the estate
// behind a catalogue rather than the shop in front. A store that cannot be read is the
// server's fault and answers 500.
func TestAnEventsTokensReachIsCataloguesItsMinterMaintains(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	feedReachFixture(t, srv)
	as := func(p *httpapi.Principal) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", nil)
		return r.WithContext(httpapi.WithPrincipal(r.Context(), p))
	}
	admin := &httpapi.Principal{UserID: "usr_root", Roles: []string{RoleAdmin}}
	stranger := &httpapi.Principal{UserID: "usr_eve", Roles: []string{RoleFeedReader}}

	if reach, refusal, err := srv.reachFor(as(admin), apiScopeEvents, []string{" cat-it ", ""}); err != nil ||
		refusal != "" || len(reach) != 1 || reach[0] != "cat-it" {
		t.Fatalf("an administrator naming IT = %v %q %v", reach, refusal, err)
	}
	if reach, refusal, err := srv.reachFor(as(admin), apiScopeEvents, nil); err != nil || refusal != "" || reach != nil {
		t.Fatalf("no reach = %v %q %v, want the whole feed", reach, refusal, err)
	}
	if _, refusal, _ := srv.reachFor(as(admin), apiScopeEvents, []string{"cat-gone"}); !strings.Contains(refusal, "no catalogue") {
		t.Fatalf("an unknown catalogue = %q", refusal)
	}
	if _, refusal, _ := srv.reachFor(as(stranger), apiScopeEvents, []string{"cat-hr"}); !strings.Contains(refusal, "do not maintain") {
		t.Fatalf("a catalogue the minter does not maintain = %q", refusal)
	}

	approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "catalog", "catalogs"))
	if _, _, err := srv.reachFor(as(admin), apiScopeEvents, []string{"cat-it"}); err == nil ||
		!strings.Contains(err.Error(), "read catalogues") {
		t.Fatalf("over an unreadable catalogue store: %v, want an error", err)
	}
}
