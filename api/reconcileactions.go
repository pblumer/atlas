package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
)

// The three things a person may do about a disagreement
// (ADR-draft-reconciliation).
//
// Atlas does not enforce. A system that removed privileges it had not granted
// would lock a company out on its first bad reading, and the reading is the part
// Atlas is least entitled to be confident about — it is one worker's answer about
// somebody else's system. So every one of these is a call somebody makes, about
// one finding, having read it.
//
// # Three, and why not four
//
//   - **adopt** an unmanaged right: Atlas did not grant it, somebody accepts it
//     into the inventory, and it is recorded as `adopted` — which is what that
//     origin has been waiting for since ADR-0312 named it.
//   - **deprovision** an unmanaged right: run the product's deprovisioning process.
//     Never a direct worker call: nothing reaches a target system except through a
//     modelled process, and a reconciliation that bypassed that would be a second
//     provisioning path invisible to the diagram, the replay and the audit trail.
//   - **revoke** a missing right: stop asserting something the target system
//     contradicts. The inventory becomes true again, and the journal keeps what it
//     used to say — which is precisely why the journal is durable.
//
// The fourth, re-provisioning a missing right, is deliberately absent. Granting
// something is ordering it, ordering already exists, and it carries the approval
// rule the catalogue declares. An action here that started a provisioning process
// would be a second granting path with no approval in it — the thing this whole
// portal is built to not have.

// actOnDiscrepancy loads one open finding and hands it to fn, which returns how
// the episode closed. It is the shared half of the three handlers: the lookup, the
// refusals and the journal write are identical, and only the act differs.
func (s *Server) actOnDiscrepancy(w http.ResponseWriter, r *http.Request,
	wantKind, closure string, fn func(discrepancyRecord) error) {

	id := r.PathValue("id")
	by := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		by = p.UserID
	}

	var (
		rec   discrepancyRecord
		found bool
		opErr error
	)
	s.do(func() { rec, found, opErr = s.discrepancies.Get(id) })
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read the finding: "+opErr.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no finding "+id)
		return
	case !rec.Open():
		// Closed already, and saying how matters: "somebody adopted this an hour
		// ago" and "this went away on its own" call for different next steps, and a
		// bare 409 would send the caller back to the list to work out which.
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf(
			"finding %s is already closed (%s). Acting on it again would decide something that is "+
				"no longer true; reconcile again if you believe it has come back", id, rec.ClosedHow))
		return
	case rec.Kind != wantKind:
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf(
			"finding %s is %q, and this action is for a %q one. The two directions of a "+
				"disagreement do not take the same remedy: a right held and not recorded is "+
				"adopted or deprovisioned, one recorded and not held is revoked",
			id, rec.Kind, wantKind))
		return
	}

	if err := fn(rec); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	// The journal last, on purpose. Every act above is the durable one — an
	// entitlement written, a process started — and a journal entry written first
	// would record a decision whose act then failed. This way the worst case is a
	// finding that reads as open after it was acted on, which the next run closes
	// and which nobody has to undo.
	var closeErr error
	s.do(func() {
		closeErr = s.discrepancies.Save(closeDiscrepancy(rec, closure, by, time.Now().Unix()))
	})
	if closeErr != nil {
		httpapi.Error(w, http.StatusInternalServerError,
			"the action was carried out, but the finding could not be closed: "+closeErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"id": id, "closed": true})
}

// handleAdoptDiscrepancy accepts an unmanaged right into the inventory.
//
// Origin `adopted` and not `legacy`: the two are both "Atlas did not grant this",
// and they differ in who said so. Legacy is what a commissioning load found before
// anybody was watching; adopted is a right that appeared afterwards and a person
// looked at and accepted. An audit that could not tell them apart could not tell a
// pre-existing estate from privileges that grew under Atlas's nose.
func (s *Server) handleAdoptDiscrepancy(w http.ResponseWriter, r *http.Request) {
	s.actOnDiscrepancy(w, r, recUnmanaged, closedAdopted, func(rec discrepancyRecord) error {
		at := time.Now().UnixNano()
		s.do(func() {
			s.proc.GrantEntitlement(model.EntitlementValue{
				Principal: rec.Principal, ItemID: rec.ItemID,
				Since: at, Origin: model.OriginAdopted,
				// No order id, and no variant. Neither is knowable: a target system
				// reports that somebody is in a group, not which variant of a product
				// that is, and guessing one would be Atlas inventing the detail that
				// makes the record specific.
			})
		})
		if err := s.drive(); err != nil {
			return fmt.Errorf("adopt: write the entitlement: %w", err)
		}
		return nil
	})
}

// handleRevokeDiscrepancy stops Atlas asserting a right the target system does not
// have.
//
// It removes the record and nothing else. It does not touch the target system —
// there is nothing there to touch, which is the finding — and it does not decide
// that the disappearance was correct. What it decides is that Atlas will stop
// claiming otherwise, and the journal keeps what it used to claim.
func (s *Server) handleRevokeDiscrepancy(w http.ResponseWriter, r *http.Request) {
	s.actOnDiscrepancy(w, r, recMissing, closedRevoked, func(rec discrepancyRecord) error {
		s.do(func() { s.proc.RevokeEntitlement(rec.Principal, rec.ItemID) })
		if err := s.drive(); err != nil {
			return fmt.Errorf("revoke: remove the entitlement: %w", err)
		}
		return nil
	})
}

// handleDeprovisionDiscrepancy runs the product's deprovisioning process against
// an unmanaged right.
//
// The process comes from the catalogue **as it stands now**, and that is a weaker
// guarantee than an order's return has — worth saying rather than leaving to be
// discovered. A returned order line revokes by the release it was ordered against,
// frozen when it was placed, so a grant is undone by the rules that were in force
// when it was made. A right nobody ordered has no such release. There is nothing
// else to use, and the alternative — refusing to deprovision anything unmanaged —
// would leave the one case this exists for unreachable.
func (s *Server) handleDeprovisionDiscrepancy(w http.ResponseWriter, r *http.Request) {
	s.actOnDiscrepancy(w, r, recUnmanaged, closedDeprovisioning, func(rec discrepancyRecord) error {
		var (
			process string
			opErr   error
		)
		s.do(func() {
			it, ok, err := s.catalogStore.Item(rec.ItemID)
			switch {
			case err != nil:
				opErr = err
			case !ok:
				opErr = fmt.Errorf("deprovision: no product %s; it was withdrawn or removed since "+
					"the finding was recorded", rec.ItemID)
			case strings.TrimSpace(it.DeprovisionProcess) == "":
				opErr = fmt.Errorf("deprovision: product %s binds no deprovisioning process. "+
					"Publishing a catalogue refuses that, so this product has never been "+
					"published — bind one and publish before revoking rights through it", rec.ItemID)
			default:
				process = it.DeprovisionProcess
			}
		})
		if opErr != nil {
			return opErr
		}
		return s.startDeprovisioning(process, rec)
	})
}

// startDeprovisioning starts the process with what a deprovisioning needs to know.
//
// The variables are the ones an order's return hands over, minus the order: there
// is no order. A process written for both reads `orderId` as empty and has to cope,
// which is stated here and in the shipped example rather than left as a surprise
// for whoever reuses their return process for this.
func (s *Server) startDeprovisioning(process string, rec discrepancyRecord) error {
	vars := []model.VariableValue{
		{Name: "itemId", Kind: model.VarString, Text: rec.ItemID},
		{Name: "recipient", Kind: model.VarString, Text: rec.Principal},
		{Name: "reason", Kind: model.VarString, Text: "reconciliation: unmanaged in " + rec.System},
	}
	var (
		key   uint64
		found bool
	)
	s.do(func() {
		if d := s.latestDeploymentOf(process); d != nil {
			key, found = d.Key, true
		}
	})
	if !found {
		return fmt.Errorf("deprovision: no deployed process with id %s", process)
	}
	s.do(func() { s.proc.CreateInstance(key, vars...) })
	return s.drive()
}
