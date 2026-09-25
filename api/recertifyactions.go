package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The two things a reviewer may say about one right
// (ADR-0341).
//
// Two, and one row at a time. There is no third decision and there is no way to
// answer several rows at once, and neither of those is an omission:
//
//   - **No bulk.** A "certify all" button produces a signed attestation containing
//     no information, which is worse than no attestation at all — an auditor
//     reading it has evidence that somebody looked. The reading is the product, so
//     the interface must not offer a way to skip it.
//   - **No re-grant.** Granting is ordering, ordering already exists, and it
//     carries the approval rule the catalogue declares. Identical to the fourth
//     action ADR-0334 deliberately left out.
//
// # Authority is per row
//
// The reconciliation actions are in no confined scope because each takes access
// away. A recertification decision does too, and yet it is made by a line manager —
// an ordinary `user`. So the gate is not the role, it is the row: whoever the row
// names may answer it, and so may an operator or an administrator, who can reach
// the same effect through the catalogue anyway.
//
// This is api/taskauthority.go's rule rather than a new one. The one deliberate
// difference: an *unassigned* row is not open to everybody the way an unassigned
// user task is. A task nobody was addressed about has no holder to impersonate; an
// attestation nobody was asked for is a signature, and somebody has to be
// accountable for it. So an unassigned row belongs to the campaign's owner.

// recertifyDecision is what a reviewer sends.
type recertifyDecision struct {
	// Note is optional and is the only place a reviewer can say something the
	// numbers cannot. It is not required, deliberately: a mandatory justification
	// field is answered with "ok" by the third row and then means nothing.
	Note string `json:"note,omitempty"`
}

// mayDecideRow answers whether this request may decide this row.
func (s *Server) mayDecideRow(r *http.Request, cmp recertifyCampaign, row recertifyRow, me string) bool {
	if !s.authEnabled {
		// Single-user mode is a single user, as everywhere else.
		return true
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		return false
	}
	for _, role := range p.Roles {
		if role == RoleAdmin || role == RoleOperator {
			return true
		}
	}
	if row.Reviewer != "" {
		return row.Reviewer == me
	}
	return cmp.OpenedBy == me
}

// decideOnRow is the shared half of the two handlers: the lookup, the refusals and
// the write are the same, and only the judgement differs.
func (s *Server) decideOnRow(w http.ResponseWriter, r *http.Request, decision string) {
	campaignID, id := r.PathValue("id"), r.PathValue("row")

	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().RecertifyNote))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var msg recertifyDecision
	if len(body) > 0 {
		if err := json.Unmarshal(body, &msg); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}

	cmp, found, err := s.loadCampaign(campaignID)
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "recertification: "+err.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no campaign "+campaignID)
		return
	case !cmp.Open():
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf(
			"campaign %s is closed. What it recorded is what was decided while it ran, and an "+
				"attestation added afterwards would be one nobody was asked for; open a new "+
				"campaign to ask the question again", campaignID))
		return
	}

	var (
		row     recertifyRow
		hasRow  bool
		loadErr error
	)
	s.do(func() { row, hasRow, loadErr = s.recertifications.row(campaignID, id) })
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read the row: "+loadErr.Error())
		return
	case !hasRow:
		httpapi.Error(w, http.StatusNotFound, "no row "+id+" in campaign "+campaignID)
		return
	case row.Decided():
		// Refused rather than overwritten. A campaign is a moment and a row is
		// evidence of it; letting a decision be replaced would make the record answer
		// "what did they decide" with whatever was written last, which is the one
		// question it exists to answer truthfully.
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf(
			"row %s was already decided (%s, by %s). An attestation is not editable — it says what "+
				"somebody judged at one moment. Changing that judgement is a new campaign, or an "+
				"order", id, row.Decision, row.DecidedBy))
		return
	}

	me := principalID(r)
	if !s.mayDecideRow(r, cmp, row, me) {
		// 403 and not 404. The row's existence is not the secret — a campaign lists
		// its rows to anybody who may read it — and answering "not found" would send
		// a reviewer hunting for a typo in an id that is perfectly correct.
		httpapi.Error(w, http.StatusForbidden, fmt.Sprintf(
			"row %s was addressed to somebody else. A recertification is an attestation by the "+
				"person who was asked; signing it for them would record a judgement they never "+
				"made", id))
		return
	}

	outcome := ""
	if decision == decisionRevoke {
		if outcome, err = s.revokeCertifiedRight(row, me); err != nil {
			// A return the order refuses — the line already going back, or still
			// needed by something held beside it — is a state somebody can see and
			// act on, not a fault; the row stays undecided so it can be answered
			// once that has changed.
			var refused errReturnRefused
			if errors.As(err, &refused) {
				httpapi.Error(w, http.StatusConflict, err.Error())
				return
			}
			httpapi.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// The row last, for the reason the discrepancy journal closes last: the act
	// above is the durable one, and a row written first would record a decision
	// whose act then failed. The worst case this way is a decision that reads as
	// undecided after its deprovisioning started, which a person can see and answer
	// again — the opposite is an attestation for something that never happened.
	next := decideRow(row, decision, me, strings.TrimSpace(msg.Note), outcome, time.Now().Unix())
	var saveErr error
	s.do(func() { saveErr = s.recertifications.rows.Save(next) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError,
			"the decision was carried out, but it could not be recorded: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, next)
}

// handleKeepRecertifyRow records that somebody judged a right still needed.
//
// It writes no entitlement, because nothing changed: the right is held, it stays
// held, and what is new is the judgement. That is the whole of it — a "keep" that
// touched the inventory would move a hold's start date and destroy the one thing
// that date is for.
func (s *Server) handleKeepRecertifyRow(w http.ResponseWriter, r *http.Request) {
	s.decideOnRow(w, r, decisionKeep)
}

// handleRevokeRecertifyRow records the opposite judgement and runs the product's
// deprovisioning process.
func (s *Server) handleRevokeRecertifyRow(w http.ResponseWriter, r *http.Request) {
	s.decideOnRow(w, r, decisionRevoke)
}

// errReturnRefused is an order declining to give a line back: it is already on its
// way back, or something still held needs it. It is the order's answer, and it
// is passed on as the reason the decision could not be carried out.
type errReturnRefused struct{ err error }

func (e errReturnRefused) Error() string { return e.err.Error() }
func (e errReturnRefused) Unwrap() error { return e.err }

// revokeCertifiedRight takes the right away, and reports which of the three things
// happened.
//
// The right may be gone already: a campaign is a snapshot and the estate moves
// under it. That is not an error and is not treated as one — the decision is still
// a decision somebody made, and starting a deprovisioning against nothing would
// park an instance raising an incident about a fact rather than a fault.
//
// A right an order granted goes back through that order
// (ADR-draft-a-withdrawn-ordered-right-goes-back-through-its-order): the line is
// marked returning and the process the order froze runs with the order, the
// position and the recipient, exactly as when the orderer gives it back. That is
// what lets the process find what it provisioned and report the line returned,
// and a returned line is the one thing that ends an ordered right in the
// inventory. Only a right no order stands behind — adopted, legacy, or one whose
// order retention has deleted — takes the catalogue's deprovisioning directly.
func (s *Server) revokeCertifiedRight(row recertifyRow, by string) (string, error) {
	held, ok, err := s.heldRight(row.Principal, row.ItemID)
	if err != nil {
		return "", fmt.Errorf("revoke: read what %s holds: %w", row.Principal, err)
	}
	if !ok {
		return outcomeAlreadyGone, nil
	}
	reason := "recertification: withdrawn in campaign " + row.CampaignID
	if held.OrderID != "" {
		returned, err := s.returnOrderedRight(held, by, reason)
		if err != nil {
			return "", err
		}
		if returned {
			return outcomeReturnStarted, nil
		}
	}

	var (
		process string
		opErr   error
	)
	s.do(func() {
		it, ok, err := s.catalogStore.Item(row.ItemID)
		switch {
		case err != nil:
			opErr = err
		case !ok:
			opErr = fmt.Errorf("revoke: no product %s; it was withdrawn or removed since the "+
				"campaign was opened", row.ItemID)
		case strings.TrimSpace(it.DeprovisionProcess) == "":
			opErr = fmt.Errorf("revoke: product %s binds no deprovisioning process. Publishing a "+
				"catalogue refuses that, so this product has never been published — bind one and "+
				"publish before revoking rights through it", row.ItemID)
		default:
			process = it.DeprovisionProcess
		}
	})
	if opErr != nil {
		return "", opErr
	}
	if err := s.startDeprovisioningFor(process, row.ItemID, row.Principal, reason); err != nil {
		return "", err
	}
	return outcomeDeprovisioning, nil
}

// returnOrderedRight gives an ordered right back through the order that granted
// it, and reports whether it did.
//
// false with no error is "no order stands behind this any more": the order was
// deleted by retention, no longer carries the position, or names somebody else as
// its recipient. The caller then takes the catalogue's path, as it did before
// orders were consulted at all. An order that carries the position but refuses to
// return it is an errReturnRefused, and nothing is started.
func (s *Server) returnOrderedRight(held model.EntitlementValue, by, reason string) (bool, error) {
	ref := held.ItemID
	if held.VariantID != "" {
		ref = held.ItemID + "#" + held.VariantID
	}
	var (
		out       order.Order
		process   string
		found     bool
		returnErr error
		opErr     error
	)
	at := s.now()
	s.do(func() {
		ord, ok, err := s.orderStore.Get(held.OrderID)
		if err != nil {
			opErr = err
			return
		}
		if !ok || ord.Recipient != held.Principal {
			return
		}
		if _, err := order.ResolveLine(ord, ref); err != nil {
			return
		}
		found = true
		process = order.ReturnProcessOf(ord, ref)
		if out, returnErr = order.Returning(ord, ref, at, by); returnErr != nil {
			return
		}
		opErr = s.orderStore.Save(out)
	})
	switch {
	case opErr != nil:
		return false, fmt.Errorf("revoke: return %s through order %s: %w", ref, held.OrderID, opErr)
	case !found:
		return false, nil
	case returnErr != nil:
		return false, errReturnRefused{fmt.Errorf("revoke: order %s does not give %s back: %w",
			held.OrderID, ref, returnErr)}
	}
	// Durable first, then the process, as for the orderer's own return (I2).
	if err := s.startReturn(process, held.OrderID, ref, out, reason); err != nil {
		return false, fmt.Errorf("the return was recorded on order %s, but its process could "+
			"not be started: %w", held.OrderID, err)
	}
	return true, nil
}

// heldRight reads what the inventory records for this pair, if anything.
//
// One person's holdings are a prefix scan, so this is cheap — but a scan is still a
// scan, and ADR-0239 puts scans off the loop whatever their expected size. The
// answer is a moment's, which is all it needs to be: what it decides is whether
// there is anything to deprovision, and a right that disappears between this read
// and the process starting produces a deprovisioning that finds nothing, which is
// what a deprovisioning does about a right that is not there.
func (s *Server) heldRight(principal, itemID string) (model.EntitlementValue, bool, error) {
	var (
		held  model.EntitlementValue
		found bool
	)
	err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return rv.EntitlementsOf(principal, func(v *model.EntitlementValue) error {
			if v.ItemID == itemID {
				held, found = *v, true
			}
			return nil
		})
	})
	return held, found, err
}
