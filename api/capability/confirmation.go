package capability

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// Confirming a record: the one act that says its prose is still true.
//
// It is a route of its own and not a flag on a save, and that separation is the whole
// mechanism. See ADR-draft-a-capability-says-when-it-was-last-confirmed; the short
// version is that a date refreshed by any edit would let a typo fix in the summary
// assert that the owner, the scope and every SLA had been re-read — which is the lie
// ADR-0289 names, made automatic and therefore leaving no diff in which anybody could
// have noticed.

const (
	// maxConfirmationNote keeps the note to one line's worth of prose. It is not a
	// resource budget — nothing here is bounded against memory, the whole record is
	// already bounded by the request budget — it is the shape of the field: a sentence
	// about what the review found, not a report.
	maxConfirmationNote = 500
	// maxConfirmedWith is a person's name or a role, not a distribution list.
	maxConfirmedWith = 200
)

// confirmRequest is what a confirmation carries. Neither field is required: the
// minimum confirmation is "I read this and it is still true", which is a legitimate
// thing to say and the commonest.
type confirmRequest struct {
	// With names who was asked, where that is somebody other than the caller. Empty
	// means the caller spoke for the record alone — a weaker confirmation, and one the
	// record says rather than implying the opposite.
	With string `json:"with"`
	// Note is one line on what the review found.
	Note string `json:"note"`
}

// validateConfirmation reports everything wrong with a confirmation request.
func validateConfirmation(req confirmRequest) []string {
	var findings []string
	if len([]rune(req.With)) > maxConfirmedWith {
		findings = append(findings, fmt.Sprintf(
			"with is longer than %d characters: it names who was asked, not a distribution list",
			maxConfirmedWith))
	}
	if len([]rune(req.Note)) > maxConfirmationNote {
		findings = append(findings, fmt.Sprintf(
			"note is longer than %d characters: it is one line on what the review found, "+
				"and it replaces the previous one rather than being appended to it", maxConfirmationNote))
	}
	if strings.ContainsAny(req.Note, "\r\n") || strings.ContainsAny(req.With, "\r\n") {
		findings = append(findings, "with and note are single lines")
	}
	return findings
}

// ConfirmationView is a confirmation as a read renders it: what the record says, plus
// the two judgements a reader would otherwise have to make themselves.
type ConfirmationView struct {
	Confirmation
	// HorizonMonths is the interval this answer applied. Published rather than assumed,
	// because it is an installation setting: a reader seeing "not stale" is entitled to
	// know whether that means "confirmed last month" or "the horizon is a century".
	HorizonMonths int `json:"horizonMonths"`
	// Stale is whether the confirmation has lapsed, or was never made.
	Stale bool `json:"stale"`
	// Ever is whether anybody has confirmed this at all. Stale without Ever is a record
	// nobody has ever stood behind, which is different work from one that lapsed.
	Ever bool `json:"ever"`
	// SelfConfirmed is true when the confirmer is also the last person to have edited
	// the record — the map confirming itself.
	//
	// It is shown and never reported. In a four-person installation the architect is
	// the only person who *can* confirm, so a finding here would fire on the normal
	// case, and a report that fires on the normal case stops being read. Shown beside
	// the confirmer and who they asked, it is one of three facts a reader weighs.
	SelfConfirmed bool `json:"selfConfirmed"`
}

func viewConfirmation(c Confirmation, updatedBy string, now time.Time, horizonMonths int) ConfirmationView {
	return ConfirmationView{
		Confirmation:  c,
		HorizonMonths: horizonMonths,
		Stale:         c.StaleAt(now, horizonMonths),
		Ever:          c.Confirmed(),
		// An unconfirmed record is not self-confirmed: nobody confirmed it at all.
		SelfConfirmed: c.Confirmed() && c.By != "" && c.By == updatedBy,
	}
}

// confirmApplier is the per-kind half of a confirmation: read the record, set its
// confirmation, save it, and report the UpdatedBy the saved record carries — which is
// what decides whether the map has just confirmed itself.
//
// One read and one write. An earlier shape took a reader and a writer separately and
// paid three reads for one confirmation, with a defensive branch at each.
type confirmApplier func(key string, c Confirmation) (updatedBy string, ok bool, err error)

// HandleConfirmCapability records that somebody has read a capability and says it
// still describes reality.
func (s *Service) HandleConfirmCapability(w http.ResponseWriter, r *http.Request) {
	s.confirm(w, r, notFoundCapability, func(key string, c Confirmation) (string, bool, error) {
		rec, ok, err := s.caps.Get(key)
		if err != nil || !ok {
			return "", ok, err
		}
		rec.Confirmation = c
		return rec.UpdatedBy, true, s.caps.Save(rec)
	})
}

// HandleConfirmValueStream is the value stream's twin: a stream's owner and KPIs decay
// exactly as a capability's do.
func (s *Service) HandleConfirmValueStream(w http.ResponseWriter, r *http.Request) {
	s.confirm(w, r, notFoundValueStream, func(key string, c Confirmation) (string, bool, error) {
		rec, ok, err := s.streams.Get(key)
		if err != nil || !ok {
			return "", ok, err
		}
		rec.Confirmation = c
		return rec.UpdatedBy, true, s.streams.Save(rec)
	})
}

// confirm is the half both records share. It does not bump the revision: a
// confirmation asserts nothing about the content, so a client holding a copy has not
// been made stale by one, and refusing their next save would be refusing it for a
// reason that is not true.
func (s *Service) confirm(w http.ResponseWriter, r *http.Request, notFound string, apply confirmApplier) {
	var req confirmRequest
	if !decodeJSONLimit(w, r, &req, s.budgets().Request) {
		return
	}
	req.With = strings.TrimSpace(req.With)
	req.Note = strings.TrimSpace(req.Note)
	if findings := validateConfirmation(req); len(findings) > 0 {
		writeFindings(w, "the confirmation is not valid", findings)
		return
	}

	key := r.PathValue("key")
	var (
		found bool
		opErr error
		view  ConfirmationView
	)
	if !s.dispatch(func() {
		now := s.now()
		next := Confirmation{At: now.Unix(), By: requestActor(r), With: req.With, Note: req.Note}
		updatedBy, ok, err := apply(key, next)
		if err != nil {
			opErr = err
			return
		}
		if !ok {
			return
		}
		found = true
		view = viewConfirmation(next, updatedBy, now, s.horizonMonths())
	}) {
		writeShuttingDown(w)
		return
	}
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "confirm: "+opErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, notFound)
	default:
		httpapi.JSON(w, http.StatusOK, view)
	}
}
