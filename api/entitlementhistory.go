package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Reading what somebody used to hold
// (ADR-draft-entitlement-history).
//
// The inventory answers "what does this person hold today" and deliberately
// nothing else. This answers the two questions an access review asks that the
// present tense cannot: what has ended, and — the one that matters — what did the
// record say on a given day.
//
// # Why "what did the record say" and not "what did they have"
//
// Atlas never observed the target system except through reconciliation, so it can
// only ever report what it recorded. Most of the time the two coincide and the
// distinction is pedantry. Where they do not, it is the whole answer: a hold
// closed as *corrected* is one reconciliation found the target system did not
// have, and reporting it as a period of access would assert on Atlas's behalf
// exactly what ADR-0334 had it decline to decide. So every row says which it is,
// and a reader that ignores the flag gets the larger, more cautious set rather
// than a confidently wrong one.

// endedHold is one hold that has ended.
type endedHold struct {
	ItemID    string `json:"itemId"`
	VariantID string `json:"variantId,omitempty"`
	Since     int64  `json:"since"`
	EndedAt   int64  `json:"endedAt"`
	// Until is the end the hold was granted with, zero for one that had none.
	Until int64 `json:"until,omitempty"`
	// OverdueDays is how long past that end it was still recorded. After the row is
	// written this is the only surviving trace that the right outstayed its
	// welcome, and the expiry report — which reads the live inventory — can no
	// longer see it at all.
	OverdueDays int    `json:"overdueDays,omitempty"`
	Origin      string `json:"origin"`
	OrderID     string `json:"orderId,omitempty"`
	// Reason is "returned" or "corrected", and Held is what it means: whether this
	// row is evidence the access existed, or only that Atlas claimed it did. Both,
	// because a caller that reads neither should still see the word.
	Reason string `json:"reason"`
	Held   bool   `json:"held"`
	// EndedBy is who decided, empty where nothing recorded it.
	EndedBy string `json:"endedBy,omitempty"`
}

// historyResp is one principal's ended holds, or — with ?at= — what the record
// said they held at a moment.
type historyResp struct {
	Principal string `json:"principal"`
	// At echoes the moment asked about, zero when none was. Echoed because the
	// answer is meaningless without it and a caller that mis-parsed its own
	// parameter would otherwise read a confident answer to a different question.
	At    int64       `json:"at,omitempty"`
	Items []endedHold `json:"items"`
	// Counts separates the two kinds, because a page that showed one number would
	// hide the distinction the whole family is built on.
	Counts  historyCounts `json:"counts"`
	Omitted int           `json:"omitted,omitempty"`
	Note    string        `json:"note,omitempty"`
}

type historyCounts struct {
	// Held is rows that are evidence of access, Claimed rows that are evidence
	// only of a claim Atlas later withdrew.
	Held    int `json:"held"`
	Claimed int `json:"claimed"`
}

// handleEntitlementHistory answers what one principal used to hold.
//
// The subject rule is the inventory's, unchanged: yourself by default, somebody
// else only with the admin role. It is if anything sharper here — a person's
// access history is their inventory plus everything it ever was.
func (s *Server) handleEntitlementHistory(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.historySubject(w, r)
	if !ok {
		return
	}

	at, err := momentParam(r.URL.Query().Get("at"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "entitlement history: "+err.Error())
		return
	}

	rep := historyResp{Principal: principal, At: at, Items: []endedHold{}}
	if err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		if err := rv.EntitlementHistoryOf(principal, func(v *model.EntitlementHistoryValue) error {
			if at != 0 && !v.CoveredAt(at) {
				return nil
			}
			rep.Items = append(rep.Items, endedHoldOf(v))
			return nil
		}); err != nil {
			return err
		}
		if at == 0 {
			return nil
		}
		// A moment is also covered by what is still held, and leaving those out
		// would answer "what had ended by then" while claiming to answer "what did
		// they hold". A right granted before the moment and never given back is the
		// most ordinary way to hold something on a given day.
		return rv.EntitlementsOf(principal, func(v *model.EntitlementValue) error {
			if v.Since > at {
				return nil
			}
			rep.Items = append(rep.Items, stillHeldAt(v))
			return nil
		})
	}); err != nil {
		httpapi.Error(w, http.StatusInternalServerError,
			"entitlement history: read: "+err.Error())
		return
	}

	for _, it := range rep.Items {
		if it.Held {
			rep.Counts.Held++
			continue
		}
		rep.Counts.Claimed++
	}
	if limit := int(s.budgets().HistoryReport); len(rep.Items) > limit {
		rep.Omitted = len(rep.Items) - limit
		rep.Items = rep.Items[:limit]
	}
	if len(rep.Items) == 0 {
		rep.Note = emptyHistoryNote(at)
	}
	httpapi.JSON(w, http.StatusOK, rep)
}

// historySubject decides whose history this request may read, answering the
// client itself when it may not.
func (s *Server) historySubject(w http.ResponseWriter, r *http.Request) (string, bool) {
	pr := httpapi.PrincipalFrom(r.Context())
	principal := ""
	if pr != nil {
		principal = pr.UserID
	}
	if asked := r.URL.Query().Get("principal"); asked != "" && asked != principal {
		if !s.isAdmin(r) {
			// Named rather than silently narrowed to the caller's own, for the
			// reason the inventory names it: an answer that quietly changes the
			// question is how somebody comes to believe they read a colleague's
			// history and found it clean.
			httpapi.Error(w, http.StatusForbidden,
				"reading somebody else's access history needs the admin role")
			return "", false
		}
		principal = asked
	}
	if principal == "" {
		httpapi.Error(w, http.StatusBadRequest,
			"no principal: name one with ?principal= when nobody is signed in")
		return "", false
	}
	return principal, true
}

// endedHoldOf renders one closed row.
func endedHoldOf(v *model.EntitlementHistoryValue) endedHold {
	return endedHold{
		ItemID: v.ItemID, VariantID: v.VariantID, Since: v.Since,
		EndedAt: v.EndedAt, Until: v.Until,
		OverdueDays: int(v.Overdue() / int64(24*time.Hour)),
		Origin:      v.Origin.String(), OrderID: v.OrderID,
		Reason: v.EndedReason.String(), Held: v.EndedReason.Held(),
		EndedBy: v.EndedBy,
	}
}

// stillHeldAt renders a live entitlement as it looked at the moment asked about.
//
// EndedAt stays zero and says so: this hold has not ended, and filling it with
// the moment asked about would turn "still held" into "ended that day" in a
// record somebody reads years later.
func stillHeldAt(v *model.EntitlementValue) endedHold {
	return endedHold{
		ItemID: v.ItemID, VariantID: v.VariantID, Since: v.Since, Until: v.Until,
		Origin: v.Origin.String(), OrderID: v.OrderID,
		Reason: "held", Held: true,
	}
}

// momentParam reads ?at=, accepting RFC 3339 or unix nanoseconds.
//
// Both, because the two callers are different: a person pastes a date, and a
// modelled process has a FEEL date-time it renders as RFC 3339 or a number it
// already holds in the unit every other moment in this API uses. Rejecting either
// would push a conversion into a BPMN diagram.
func momentParam(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UnixNano(), nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("at=%q is neither an RFC 3339 moment nor unix nanoseconds", raw)
	}
	return n, nil
}

// emptyHistoryNote explains an empty answer, which otherwise reads as a clean
// record when it usually means nothing has ended yet.
func emptyHistoryNote(at int64) string {
	if at != 0 {
		return "the record says this principal held nothing at " +
			time.Unix(0, at).UTC().Format(time.RFC3339) + ". That is an answer about " +
			"what Atlas recorded, not about what a target system contained — the two " +
			"are the same only where reconciliation has run"
	}
	return "no hold recorded against this principal has ended. A hold enters this " +
		"list when an order line is returned, or when a reconciliation discrepancy " +
		"is accepted; holds that have simply never ended are in the inventory"
}
