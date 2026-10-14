package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/model"
)

// What a decision on one row answers when it cannot be taken (ADR-0341,
// ADR-0418): the refusals before anything is touched, and the revocations that
// find nothing they could legitimately run. In every one of them the row must
// stay undecided — a row that read "revoked" while nothing was withdrawn is an
// attestation of an act that never happened.

// recertifyActionsPathsDeprovisionBPMN is the smallest process a product can bind
// as its deprovisioning: it starts and ends.
const recertifyActionsPathsDeprovisionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="recertify-deprovision" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// recertifyActionsPathsCampaign seeds one right and opens a campaign over it,
// returning the campaign and its single row.
func recertifyActionsPathsCampaign(t *testing.T, s *Server, held model.EntitlementValue) (string, string) {
	t.Helper()
	recertifyHTTPPathsGrant(t, s, held)
	rep := recertifyHTTPPathsOpen(t, s.Handler(), `{"name":"Q3 access review"}`)
	if len(rep.Rows) != 1 {
		t.Fatalf("%d row(s), want the one seeded right: %+v", len(rep.Rows), rep.Rows)
	}
	return rep.ID, rep.Rows[0].ID
}

// recertifyActionsPathsRow reads one row back through the campaign route.
func recertifyActionsPathsRow(t *testing.T, s *Server, campaignID, rowID string) recertifyRow {
	t.Helper()
	code, raw := recertifyHTTPPathsCall(t, s.Handler(), http.MethodGet, "/api/v1/recertification/"+campaignID, nil)
	if code != http.StatusOK {
		t.Fatalf("read campaign: %d %s", code, raw)
	}
	var rep recertifyReport
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("decode campaign: %v", err)
	}
	for _, r := range rep.Rows {
		if r.ID == rowID {
			return r
		}
	}
	t.Fatalf("row %s is not in campaign %s: %+v", rowID, campaignID, rep.Rows)
	return recertifyRow{}
}

// recertifyActionsPathsDecide posts a decision on one row.
func recertifyActionsPathsDecide(t *testing.T, s *Server, campaignID, rowID, decision string) (int, string) {
	t.Helper()
	return recertifyHTTPPathsCall(t, s.Handler(), http.MethodPost,
		"/api/v1/recertification/"+campaignID+"/rows/"+rowID+"/"+decision, nil)
}

// recertifyActionsPathsUndecided fails the test if the row carries a decision.
func recertifyActionsPathsUndecided(t *testing.T, s *Server, campaignID, rowID string) {
	t.Helper()
	if row := recertifyActionsPathsRow(t, s, campaignID, rowID); row.Decided() {
		t.Errorf("the row was decided (%s, outcome %q) although the decision was not carried out",
			row.Decision, row.Outcome)
	}
}

// recertifyActionsPathsSaveItem files a product straight into the catalogue store.
func recertifyActionsPathsSaveItem(t *testing.T, s *Server, it catalog.Item) {
	t.Helper()
	var err error
	s.do(func() { err = s.catalogStore.SaveItem(it) })
	if err != nil {
		t.Fatalf("save item %s: %v", it.ID, err)
	}
}

// TestRecertifyActionsRefusalsComeBeforeAnything. A body that cannot be read or
// parsed, a campaign or a row that does not exist: each is refused with what was
// wrong, and the row a well-formed request would have decided is untouched.
func TestRecertifyActionsRefusalsComeBeforeAnything(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	cmp, row := recertifyActionsPathsCampaign(t, srv, model.EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})
	base := "/api/v1/recertification/" + cmp + "/rows/" + row + "/keep"

	code, body := recertifyHTTPPathsCall(t, h, http.MethodPost, base, errReader{})
	if code != http.StatusBadRequest || !strings.Contains(body, "read body") {
		t.Errorf("unreadable note = %d %s, want 400", code, body)
	}
	code, body = recertifyHTTPPathsCall(t, h, http.MethodPost, base, strings.NewReader(`{"note":`))
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid JSON body") {
		t.Errorf("malformed note = %d %s, want 400", code, body)
	}
	code, body = recertifyActionsPathsDecide(t, srv, "cmp_nope", row, "keep")
	if code != http.StatusNotFound || !strings.Contains(body, "no campaign cmp_nope") {
		t.Errorf("unknown campaign = %d %s, want 404 naming it", code, body)
	}
	code, body = recertifyActionsPathsDecide(t, srv, cmp, "row_nope", "revoke")
	if code != http.StatusNotFound || !strings.Contains(body, "no row row_nope") {
		t.Errorf("unknown row = %d %s, want 404 naming it", code, body)
	}
	recertifyActionsPathsUndecided(t, srv, cmp, row)
}

// TestRecertifyActionsSingleUserModeDecidesAnyRow. Without authentication there is
// one user, and every row is theirs to answer — including one addressed to
// somebody else, who in this mode cannot sign in to answer it.
func TestRecertifyActionsSingleUserModeDecidesAnyRow(t *testing.T) {
	srv := newServerForErrors(t)
	recertifyHTTPPathsGrant(t, srv, model.EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})
	rep := recertifyHTTPPathsOpen(t, srv.Handler(),
		`{"name":"Q3","reviewers":{"usr_ada":"usr_manager"}}`)
	if len(rep.Rows) != 1 || rep.Rows[0].Reviewer != "usr_manager" {
		t.Fatalf("rows = %+v, want one addressed to usr_manager", rep.Rows)
	}

	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost,
		"/api/v1/recertification/"+rep.ID+"/rows/"+rep.Rows[0].ID+"/keep",
		strings.NewReader(`{"note":"still on the project"}`))
	if code != http.StatusOK {
		t.Fatalf("keep = %d %s, want 200", code, body)
	}
	row := recertifyActionsPathsRow(t, srv, rep.ID, rep.Rows[0].ID)
	if row.Decision != decisionKeep || row.Note != "still on the project" {
		t.Errorf("row = %+v, want it kept with the note", row)
	}
}

// TestRecertifyActionsAnUnreadableCampaignIsAFault. A campaign record that cannot
// be decoded is a broken store, and a decision against it is refused as a fault
// rather than as "no campaign", which would invite opening a second one.
func TestRecertifyActionsAnUnreadableCampaignIsAFault(t *testing.T) {
	srv := newServerForErrors(t)
	cmp, row := recertifyActionsPathsCampaign(t, srv, model.EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})
	if err := os.WriteFile(srv.recertifications.campaigns.FileFor(cmp), []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt campaign: %v", err)
	}

	code, body := recertifyActionsPathsDecide(t, srv, cmp, row, "keep")
	if code != http.StatusInternalServerError || !strings.Contains(body, "recertifycampaignstore") {
		t.Errorf("decide = %d %s, want 500 naming the campaign store", code, body)
	}
	if r, ok, err := srv.recertifications.row(cmp, row); err != nil || !ok || r.Decided() {
		t.Errorf("row after the refusal = %+v ok=%v err=%v, want it present and undecided", r, ok, err)
	}
}

// TestRecertifyActionsARevocationWithNothingToRunIsRefused. When neither the order
// nor the catalogue offers a process to take the right back, the revocation is a
// fault the operator has to see — and the row stays open to be answered again.
func TestRecertifyActionsARevocationWithNothingToRunIsRefused(t *testing.T) {
	t.Run("the order is gone and so is the product", func(t *testing.T) {
		// An ordered right whose order retention has deleted falls back to the
		// catalogue, and a variant is named as item#variant on the way.
		srv := newServerForErrors(t)
		cmp, row := recertifyActionsPathsCampaign(t, srv, model.EntitlementValue{
			Principal: "usr_ada", ItemID: "ghost", VariantID: "large", OrderID: "ord_gone",
			Since: 1_000, Origin: model.OriginOrdered})

		code, body := recertifyActionsPathsDecide(t, srv, cmp, row, "revoke")
		if code != http.StatusInternalServerError || !strings.Contains(body, "no product ghost") {
			t.Errorf("revoke = %d %s, want 500 saying the product is gone", code, body)
		}
		recertifyActionsPathsUndecided(t, srv, cmp, row)
	})

	t.Run("the order no longer carries the position", func(t *testing.T) {
		srv := newServerForErrors(t)
		var err error
		srv.do(func() {
			err = srv.orderStore.Save(order.Order{ID: "ord_other", Recipient: "usr_ada", Orderer: "usr_ada"})
		})
		if err != nil {
			t.Fatalf("seed order: %v", err)
		}
		cmp, row := recertifyActionsPathsCampaign(t, srv, model.EntitlementValue{
			Principal: "usr_ada", ItemID: "ghost", OrderID: "ord_other",
			Since: 1_000, Origin: model.OriginOrdered})

		code, body := recertifyActionsPathsDecide(t, srv, cmp, row, "revoke")
		if code != http.StatusInternalServerError || !strings.Contains(body, "no product ghost") {
			t.Errorf("revoke = %d %s, want the catalogue's refusal once the order has no such line", code, body)
		}
		if o, ok, err := srv.orderStore.Get("ord_other"); err != nil || !ok || len(o.Lines) != 0 {
			t.Errorf("the order was touched: %+v ok=%v err=%v", o, ok, err)
		}
		recertifyActionsPathsUndecided(t, srv, cmp, row)
	})

	t.Run("the product binds no deprovisioning", func(t *testing.T) {
		srv := newServerForErrors(t)
		recertifyActionsPathsSaveItem(t, srv, catalog.Item{ID: "vpn", ProvisionProcess: "p"})
		cmp, row := recertifyActionsPathsCampaign(t, srv, model.EntitlementValue{
			Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})

		code, body := recertifyActionsPathsDecide(t, srv, cmp, row, "revoke")
		if code != http.StatusInternalServerError || !strings.Contains(body, "binds no deprovisioning process") {
			t.Errorf("revoke = %d %s, want 500 naming the missing binding", code, body)
		}
		recertifyActionsPathsUndecided(t, srv, cmp, row)
	})
}

// TestRecertifyActionsAStoreThatCannotBeReadStopsTheRevocation. An order or a
// product record that cannot be decoded is not "no order" or "no product": the
// first would send an ordered right down the catalogue's path, the second would
// blame the catalogue. Both are faults, named, and nothing is decided.
func TestRecertifyActionsAStoreThatCannotBeReadStopsTheRevocation(t *testing.T) {
	t.Run("the order", func(t *testing.T) {
		srv := newServerForErrors(t)
		cmp, row := recertifyActionsPathsCampaign(t, srv, model.EntitlementValue{
			Principal: "usr_ada", ItemID: "vpn", OrderID: "ord_torn",
			Since: 1_000, Origin: model.OriginOrdered})
		path := filepath.Join(srv.dataDir, "orders", hex.EncodeToString([]byte("ord_torn"))+".json")
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatalf("corrupt order: %v", err)
		}

		code, body := recertifyActionsPathsDecide(t, srv, cmp, row, "revoke")
		if code != http.StatusInternalServerError || !strings.Contains(body, "through order ord_torn") {
			t.Errorf("revoke = %d %s, want 500 naming the order", code, body)
		}
		recertifyActionsPathsUndecided(t, srv, cmp, row)
	})

	t.Run("the product", func(t *testing.T) {
		srv := newServerForErrors(t)
		cmp, row := recertifyActionsPathsCampaign(t, srv, model.EntitlementValue{
			Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})
		path := filepath.Join(srv.dataDir, "catalog", "items", hex.EncodeToString([]byte("vpn"))+".json")
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatalf("corrupt product: %v", err)
		}

		code, body := recertifyActionsPathsDecide(t, srv, cmp, row, "revoke")
		if code != http.StatusInternalServerError || !strings.Contains(body, "catalogitemstore") {
			t.Errorf("revoke = %d %s, want 500 naming the product store", code, body)
		}
		recertifyActionsPathsUndecided(t, srv, cmp, row)
	})
}

// TestRecertifyActionsARevokedLegacyRightRunsTheCatalogueProcess. A right no
// order stands behind goes back through the product's own deprovisioning, and
// the row records that this is what happened.
func TestRecertifyActionsARevokedLegacyRightRunsTheCatalogueProcess(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/deployments",
		strings.NewReader(recertifyActionsPathsDeprovisionBPMN))
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	recertifyActionsPathsSaveItem(t, srv, catalog.Item{
		ID: "vpn", ProvisionProcess: "p", DeprovisionProcess: "recertify-deprovision"})
	cmp, row := recertifyActionsPathsCampaign(t, srv, model.EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})

	code, body = recertifyActionsPathsDecide(t, srv, cmp, row, "revoke")
	if code != http.StatusOK {
		t.Fatalf("revoke = %d %s, want 200", code, body)
	}
	got := recertifyActionsPathsRow(t, srv, cmp, row)
	if got.Decision != decisionRevoke || got.Outcome != outcomeDeprovisioning {
		t.Errorf("row = %+v, want revoked with outcome %q", got, outcomeDeprovisioning)
	}
}

// TestRecertifyActionsADecisionThatCannotBeRecordedSaysSo. A keep changes nothing
// in the estate, so when its row cannot be written the whole answer is that the
// decision was not recorded — and the row still asks its question.
func TestRecertifyActionsADecisionThatCannotBeRecordedSaysSo(t *testing.T) {
	srv := newServerForErrors(t)
	cmp, row := recertifyActionsPathsCampaign(t, srv, model.EntitlementValue{
		Principal: "usr_ada", ItemID: "vpn", Since: 1_000, Origin: model.OriginLegacy})
	reconcileApplyPathsBlockSave(t, srv.recertifications.rows.FileFor(cmp+":"+row))

	code, body := recertifyActionsPathsDecide(t, srv, cmp, row, "keep")
	if code != http.StatusInternalServerError || !strings.Contains(body, "could not be recorded") {
		t.Errorf("keep = %d %s, want 500 saying the decision was not recorded", code, body)
	}
	recertifyActionsPathsUndecided(t, srv, cmp, row)
}

// TestRecertifyActionsARefusedReturnKeepsTheOrdersReason. The refusal wraps the
// order's own error, so a caller can still ask which rule refused it.
func TestRecertifyActionsARefusedReturnKeepsTheOrdersReason(t *testing.T) {
	cause := errors.New("line is already returning")
	err := error(errReturnRefused{err: cause})
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is lost the order's reason through %v", err)
	}
	if err.Error() != cause.Error() {
		t.Errorf("message = %q, want the order's own %q", err.Error(), cause.Error())
	}
}
