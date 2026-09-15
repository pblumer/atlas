package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// What is due to end, and what should have ended already
// (ADR-draft-time-bounded-entitlements).
//
// # Why this reports rather than acts
//
// An expiry is not an inference. Reconciliation refuses to act because it would be
// acting on one worker's answer about somebody else's system — the thing Atlas is
// least entitled to be confident about. The end of a right is nothing of the kind:
// it was part of what was decided and approved when the right was granted, so
// honouring it is not a judgement, and an automatic removal would be defensible
// here where it is not there.
//
// It is still not done. The act reaches a target system, and ADR-0312 admits no
// exception for acts Atlas happens to be confident about — confidence is not the
// criterion. A removal path invisible to the diagram, the replay and the audit
// trail would be exactly the second granting path this whole portal is built to
// not have, with the arrow reversed.
//
// So this answers what is due; `examples/befristung.bpmn` returns the order line,
// which is the mechanism that already exists and is stronger than the one
// reconciliation and recertification have to use. Only an ordered right ever
// carries an end, so an expiring right always has an order behind it — and an
// order's return revokes by the release it was placed against, frozen when it was
// placed. The other two paths read the catalogue as it stands now, because a right
// nobody ordered has no frozen release to read.
//
// # Overdue is a debt, not a state of the world
//
// A right past its end is still held. The target system still has the membership
// and nothing has run — what is true is that Atlas said the access should have
// ended and it has not. The record stays, because dropping it would make the
// inventory assert that somebody does not have access they demonstrably do, which
// is the direction of wrongness ADR-0334 calls the one that corrupts the evidence.

// expiringRight is one right with an end, and what that end means now.
type expiringRight struct {
	Principal string `json:"principal"`
	ItemID    string `json:"itemId"`
	VariantID string `json:"variantId,omitempty"`
	OrderID   string `json:"orderId,omitempty"`
	Origin    string `json:"origin"`
	Since     int64  `json:"since"`
	Until     int64  `json:"until"`

	// Overdue says the end has passed and the right is still held. Days is how
	// long, negative while it is still in the future — one number rather than two
	// fields, because "in three days" and "eleven days ago" are the same question
	// asked from either side of a date.
	Overdue bool `json:"overdue"`
	Days    int  `json:"days"`

	// Returnable says the order line this right came from is still there to be
	// returned, which is what actually ends it.
	//
	// It is not always true, and the reason is a property of the design rather than
	// a fault: an entitlement deliberately outlives the order that produced it, and
	// the instance behind that order is eligible for retention deletion long before
	// a multi-year right ends. A right whose order is gone cannot be returned, so
	// nothing will clear it — and it is reported rather than hidden, because the one
	// kind that never goes away must not also be the one kind nobody sees.
	Returnable bool `json:"returnable"`
}

// expiringReport is what one read answers.
type expiringReport struct {
	// Within is the window asked for, in days, echoed so a report read later says
	// what it was looking for.
	Within int   `json:"within"`
	Now    int64 `json:"now"`

	Counts expiringCounts  `json:"counts"`
	Rights []expiringRight `json:"rights"`

	// Omitted says the list was cut for reading. The counts are over everything.
	Omitted int `json:"omitted,omitempty"`

	// Note explains an answer of nothing, which otherwise reads as an estate with
	// no temporary access when it usually means no product declares a ceiling.
	Note string `json:"note,omitempty"`
}

type expiringCounts struct {
	// Overdue first, because it is the number somebody is accountable for. Due is
	// work coming; overdue is work that did not happen.
	Overdue int `json:"overdue"`
	Due     int `json:"due"`
	// Unendable are the overdue rights whose order is gone, so no return can clear
	// them. Counted apart because no amount of running the expiry process will
	// reduce them, and a number that never moves has to say why rather than look
	// like a backlog somebody is behind on.
	Unendable int `json:"unendable"`
	// WithAnEnd is how many rights in the whole inventory carry one at all. It is
	// the denominator: "none are overdue" means something different when the answer
	// is none out of none.
	WithAnEnd int `json:"withAnEnd"`
}

// handleExpiring answers what is due to end within a window, and what is past it.
//
// The window has a default, unlike a reconciliation's scope, and the difference is
// worth naming: there, a default would have turned a truncated read into a report
// that the estate lost its access. Here the window only decides how far ahead the
// answer looks. Nothing is concluded from what falls outside it — a right due in
// ninety days is not reported as not due, it is simply not this question's answer.
func (s *Server) handleExpiring(w http.ResponseWriter, r *http.Request) {
	within := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("within")); raw != "" {
		n, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil || n < 0 {
			httpapi.Error(w, http.StatusBadRequest,
				"\"within\" is a number of days to look ahead, e.g. within=30. Zero answers what "+
					"is already overdue and nothing else")
			return
		}
		within = n
	}
	if max := int(s.budgets().ExpiringWindow); within > max {
		httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf(
			"a window of %d days is beyond the ceiling of %d (%s). A window wide enough to "+
				"cover every right with an end is a list of the whole inventory, which is what "+
				"GET /api/v1/inventory answers", within, max, limits.EnvVar("ExpiringWindow")))
		return
	}

	now := time.Now().UnixNano()
	horizon := now + int64(within)*int64(24*time.Hour)

	// Which order lines are still there to return. Read whole and indexed rather
	// than looked up per right: orders are bounded by retention, the rights are not,
	// and a store read per row would be a read per member of the population.
	var (
		returnable map[string]bool
		ran        bool
		loadErr    error
	)
	s.do(func() {
		ran = true
		all, err := s.orderStore.All()
		if err != nil {
			loadErr = err
			return
		}
		returnable = map[string]bool{}
		for _, o := range all {
			for _, l := range o.Lines {
				returnable[o.ID+"\x00"+l.ItemID] = true
			}
		}
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "expiring: this server is shutting down")
		return
	}
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "expiring: "+loadErr.Error())
		return
	}

	// The whole inventory, off the loop, as every population-sized read here is
	// (ADR-0239). "Which rights end soon" cannot be answered from the entitlement
	// key any more than "who holds VPN" can — the principal comes first, and an end
	// is not in the key at all.
	rep := expiringReport{Within: within, Now: now, Rights: []expiringRight{}}
	if err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return rv.Entitlements(func(v *model.EntitlementValue) error {
			if v.Until == 0 {
				return nil
			}
			rep.Counts.WithAnEnd++
			if v.Until > horizon {
				return nil
			}
			row := classifyExpiring(*v, now, returnable[v.OrderID+"\x00"+v.ItemID])
			switch {
			case row.Overdue && !row.Returnable:
				rep.Counts.Overdue++
				rep.Counts.Unendable++
			case row.Overdue:
				rep.Counts.Overdue++
			default:
				rep.Counts.Due++
			}
			rep.Rights = append(rep.Rights, row)
			return nil
		})
	}); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "expiring: read the inventory: "+err.Error())
		return
	}

	// Overdue first and longest overdue at the top: a list a process works through
	// in order should end the oldest debt first, and a person reading the same list
	// wants the worst line at the top rather than the soonest deadline.
	sort.SliceStable(rep.Rights, func(i, j int) bool {
		return rep.Rights[i].Until < rep.Rights[j].Until
	})
	if limit := int(s.budgets().ExpiringReport); len(rep.Rights) > limit {
		rep.Omitted = len(rep.Rights) - limit
		rep.Rights = rep.Rights[:limit]
	}
	rep.Note = expiringNote(rep)
	httpapi.JSON(w, http.StatusOK, rep)
}

// classifyExpiring turns one right and one moment into what a reader needs: how
// far from its end it is, which side of it, and what could end it.
//
// Pure, and separate from the walk that calls it, because everything interesting
// about this route is in these six lines and nothing interesting is in the scan.
//
// Whether the right is returnable is passed in rather than looked up, because the
// answer is one index built once for the whole walk: orders are bounded by
// retention and the rights are not, so a lookup per row would be a store read per
// member of the population.
func classifyExpiring(v model.EntitlementValue, now int64, returnable bool) expiringRight {
	return expiringRight{
		Principal: v.Principal, ItemID: v.ItemID, VariantID: v.VariantID,
		OrderID: v.OrderID, Origin: v.Origin.String(), Since: v.Since, Until: v.Until,
		Overdue: v.Expired(now), Days: daysBetween(now, v.Until),
		Returnable: returnable,
	}
}

// daysBetween renders the distance to an end, negative while it is still ahead.
//
// Truncated towards zero rather than rounded, so "0 days" means today from either
// side: a right ending in eleven hours and one that ended eleven hours ago are both
// today's work, and rounding one of them to a day would put it in tomorrow's list.
func daysBetween(now, until int64) int {
	return int((now - until) / int64(24*time.Hour))
}

// expiringNote explains an empty answer.
//
// An estate with no temporary access and an estate where no product declares a
// ceiling produce the same empty list, and the second is by far the more likely:
// every product has no ceiling until somebody sets one.
func expiringNote(rep expiringReport) string {
	switch {
	case len(rep.Rights) > 0:
		return ""
	case rep.Counts.WithAnEnd == 0:
		return "no right in the inventory carries an end at all. A product declares one with " +
			"\"maxDays\", it reaches a grant through the release, and it is never applied to an " +
			"adopted or legacy right — so an estate that has only ever been loaded and " +
			"reconciled will read like this until a product sets a ceiling and somebody orders " +
			"under it"
	default:
		return fmt.Sprintf("nothing ends within %d day(s): %d right(s) carry an end and all of "+
			"them are further out. This is what a healthy answer looks like",
			rep.Within, rep.Counts.WithAnEnd)
	}
}
