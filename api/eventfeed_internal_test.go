package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
)

// feedPage reads one page of the event feed as the route answers it.
func feedPage(t *testing.T, srv *Server, query string) (int, eventPage, map[string]string) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.handleListEvents(w, httptest.NewRequest(http.MethodGet, "/api/v1/events"+query, nil))
	var page eventPage
	var refusal map[string]string
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode page: %v (%s)", err, w.Body.String())
		}
	} else {
		_ = json.Unmarshal(w.Body.Bytes(), &refusal)
	}
	return w.Code, page, refusal
}

// TestTheFeedServesCloudEventsFromACursor: a grant with the provision's outcome and a
// revocation with the return's arrive as CloudEvents 1.0 in log order — the id naming
// the node, the partition and the position, the source the installation's external
// URL, the subject the order position — a page at a time from the cursor a consumer
// keeps. Pruned through a position, the feed answers a cursor below it 410 with the
// oldest it still holds; the retention sweep prunes what aged out.
func TestTheFeedServesCloudEventsFromACursor(t *testing.T) {
	srv := newServerWithOptions(t, WithExternalURL("https://atlas.example"))
	provision := model.ActionOutcomeValue{OrderID: "ord_1", Position: "vpn", CommandID: "order:ord_1:vpn:provision:1",
		Source: "atlas:order", Action: "provision", Effect: "provision", Outcome: "completed",
		EventType: "vpn.provision.completed", Principal: "usr_ada", ItemID: "vpn", At: 1000, Result: `{"ticket":"INC-1"}`}
	ret := provision
	ret.CommandID, ret.Action, ret.Effect, ret.EventType = "order:ord_1:vpn:deprovision:1", "deprovision", "deprovision", "vpn.deprovision.completed"
	var nodeID string
	srv.do(func() {
		srv.proc.GrantEntitlementWithOutcome(model.EntitlementValue{Principal: "usr_ada", ItemID: "vpn", OrderID: "ord_1",
			Since: 1000, Origin: model.OriginOrdered}, provision)
		srv.proc.RevokeEntitlementWithOutcome("usr_ada", "vpn", 2000, model.EndReturned, "usr_ada", ret)
		if err := srv.proc.RunUntilIdle(); err != nil {
			t.Errorf("RunUntilIdle: %v", err)
		}
		id, err := srv.nodeIdentity()
		if err != nil {
			t.Errorf("nodeIdentity: %v", err)
		}
		nodeID = id.ID
	})

	code, first, _ := feedPage(t, srv, "?limit=1")
	if code != http.StatusOK || len(first.Events) != 1 || !first.More {
		t.Fatalf("first page = %d %+v", code, first)
	}
	grant := first.Events[0]
	if grant.SpecVersion != "1.0" || grant.Type != "atlas.entitlement.granted" ||
		grant.Source != "https://atlas.example/catalog" || grant.Subject != "orders/ord_1/positions/vpn" ||
		grant.ID != nodeID+":1:"+first.Next || grant.DataContentType != "application/json" {
		t.Fatalf("the grant's envelope = %+v", grant)
	}
	if data := grant.Data.(map[string]any); data["principal"] != "usr_ada" || data["origin"] != "ordered" {
		t.Fatalf("the grant's data = %v", data)
	}

	code, rest, _ := feedPage(t, srv, "?after="+first.Next)
	if code != http.StatusOK || len(rest.Events) != 3 || rest.More {
		t.Fatalf("the rest = %d %+v", code, rest)
	}
	types := []string{rest.Events[0].Type, rest.Events[1].Type, rest.Events[2].Type}
	if strings.Join(types, ",") != "vpn.provision.completed,atlas.entitlement.revoked,vpn.deprovision.completed" {
		t.Fatalf("types after the grant = %v", types)
	}
	outcome := rest.Events[0].Data.(map[string]any)
	if result, ok := outcome["result"].(map[string]any); !ok || result["ticket"] != "INC-1" || outcome["commandId"] != provision.CommandID {
		t.Fatalf("the outcome's data = %v", outcome)
	}
	if revoked := rest.Events[1]; revoked.Subject != "orders/ord_1/positions/vpn" ||
		revoked.Data.(map[string]any)["reason"] != model.EndReturned.String() {
		t.Fatalf("the revocation = %+v", revoked)
	}
	if code, empty, _ := feedPage(t, srv, "?after="+rest.Next); code != http.StatusOK || len(empty.Events) != 0 || empty.Next != rest.Next {
		t.Fatalf("past the end = %d %+v, want an empty page that keeps the cursor", code, empty)
	}

	// Pruned through the provision's outcome: a cursor at the grant missed it.
	through, _ := strconv.ParseUint(rest.Events[0].ID[strings.LastIndex(rest.Events[0].ID, ":")+1:], 10, 64)
	srv.do(func() {
		srv.proc.PruneFeed(through)
		_ = srv.proc.RunUntilIdle()
	})
	code, _, refusal := feedPage(t, srv, "?after="+first.Next)
	if code != http.StatusGone || refusal["oldest"] != strconv.FormatUint(through, 10) {
		t.Fatalf("a cursor below the cut = %d %v, want 410 naming %d", code, refusal, through)
	}
	if code, fresh, _ := feedPage(t, srv, ""); code != http.StatusOK || len(fresh.Events) != 2 {
		t.Fatalf("a consumer starting fresh = %d %+v, want the two rows after the cut", code, fresh)
	}

	// Everything aged out: the sweep prunes through the last row, once an hour.
	srv.do(func() { srv.pruneEventFeed(time.Now().Add(31 * 24 * time.Hour).UnixNano()) })
	if code, gone, _ := feedPage(t, srv, ""); code != http.StatusOK || len(gone.Events) != 0 {
		t.Fatalf("after the sweep = %d %+v, want nothing held", code, gone)
	}
	if code, _, _ := feedPage(t, srv, "?after="+first.Next); code != http.StatusGone {
		t.Fatalf("a stale cursor after the sweep = %d, want 410", code)
	}
}

// TestTheFeedRefusesAMalformedRequestAndNamesItselfWithoutAnAddress: a cursor that is
// not a position and a limit out of range are refused; a server given no external URL
// names its events by a URN of the node rather than an address nobody can reach.
func TestTheFeedRefusesAMalformedRequestAndNamesItselfWithoutAnAddress(t *testing.T) {
	srv := newServerWithOptions(t)
	for _, q := range []string{"?after=abc", "?after=-1", "?limit=0", "?limit=1001", "?limit=x"} {
		if code, _, _ := feedPage(t, srv, q); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", q, code)
		}
	}
	if got := srv.eventSource("node-1"); got != "urn:atlas:node-1:catalog" {
		t.Fatalf("source without an external URL = %q", got)
	}
	if got := holdSubject("usr_ada", "vpn", "", ""); got != "principals/usr_ada/items/vpn" {
		t.Fatalf("an adopted right's subject = %q", got)
	}
	if got := holdSubject("usr_ada", "laptop", "large", "ord 2"); got != "orders/ord%202/positions/laptop%23large" {
		t.Fatalf("a variant's subject = %q", got)
	}
}
