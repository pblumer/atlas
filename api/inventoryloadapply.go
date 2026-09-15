package api

import (
	"fmt"

	"github.com/pblumer/atlas/limits"
)

// Writing a decided load, and turning it into something a person reads
// (ADR-draft-inventory-commissioning-load).

// applyInventoryPlan enqueues a plan's grants and does nothing else.
//
// There is no rule in this function, and that is its specification. Whether a
// subject resolved, which product a reference meant, whether a better record
// already existed — all of it was answered by [decideInventoryLoad], and answering
// any of it again here would be the second implementation the reporting mode exists
// to make impossible.
//
// It must be called on the run-loop goroutine, in the same turn that read the
// inventory the plan was decided against. Splitting the two would leave a window in
// which an order settles between the read and the write, and the legacy grant would
// replace the ordered right that landed in it — which is precisely the case invKeep
// exists to prevent and would then miss.
func (s *Server) applyInventoryPlan(plan inventoryPlan) {
	for _, dec := range plan.Grants {
		s.proc.GrantEntitlement(dec.Value)
	}
}

// The two modes, as the report names them.
const (
	inventoryModeReport = "report-only"
	inventoryModeApply  = "applied"
)

// inventoryReport is what one load tells a person.
//
// Counts for the expected and whole lines for the notable, as the directory mirror
// does: nobody reads eight hundred lines saying a right would be recorded, and
// everybody has to read the four saying a right is already held by a better
// authority. The counts are unbounded — they are numbers — and the lines are not;
// what does not fit is counted in NotesOmitted and never dropped in silence.
type inventoryReport struct {
	// Applied says whether anything was written, Mode which mode asked for it. They
	// are separate fields because a run that meant to write and wrote nothing —
	// because everything in it was already known — is `applied: false` in
	// `mode: applied`, and collapsing the two would hide that.
	Applied bool   `json:"applied"`
	Mode    string `json:"mode"`
	System  string `json:"system"`

	// Reason says, in a sentence, why nothing was written. It is filled on every run
	// that wrote nothing, the ordinary reporting run included: a load left in
	// reporting mode must say so in every report rather than looking like a load
	// with nothing to do.
	Reason string `json:"reason,omitempty"`

	// EverApplied and LastAppliedAt say whether this system has ever had a load
	// written, and when. A false EverApplied on an installation that has been
	// reporting for a week is the sentence somebody needs to read — and it is the
	// one thing the inventory itself cannot answer, since "loaded and found nothing"
	// and "never loaded" leave it equally empty.
	EverApplied   bool  `json:"everApplied"`
	LastAppliedAt int64 `json:"lastAppliedAt,omitempty"`

	Counts inventoryCounts `json:"counts"`

	// Unmapped is what the estate grants that no product claims, by reference,
	// most-held first. On a first load this is usually the most useful part of the
	// answer and the only place it has ever been written down.
	Unmapped []unmappedRef `json:"unmapped"`

	Notes        []inventoryDecision `json:"notes"`
	NotesOmitted int                 `json:"notesOmitted"`
}

// inventoryReportOf renders a plan, identically in both modes.
//
// applied is passed in rather than recomputed, so the one thing that differs
// between a reporting run and a real one is the one thing that is supposed to.
func inventoryReportOf(plan inventoryPlan, msg inventoryLoadMessage, state inventoryLoadState,
	applied bool, budgets limits.Limits) inventoryReport {

	rep := inventoryReport{
		Applied:       applied,
		Mode:          inventoryModeReport,
		System:        msg.System,
		EverApplied:   state.AppliedAt != 0,
		LastAppliedAt: state.AppliedAt,
		Counts:        plan.Counts,
		Unmapped:      plan.Unmapped,
		Notes:         []inventoryDecision{},
	}
	if rep.Unmapped == nil {
		rep.Unmapped = []unmappedRef{}
	}
	if msg.Apply {
		rep.Mode = inventoryModeApply
	}
	if applied {
		rep.EverApplied = true
	}
	rep.Reason = inventoryReason(plan, msg, applied)

	// The grants are listed too, and before the notes: on a first load they are the
	// body of what a person is being asked to approve, and a report that showed only
	// the exceptions would be asking somebody to authorise a number.
	limit := int(budgets.InventoryReport)
	lines := append(append([]inventoryDecision{}, plan.Grants...), plan.Notes...)
	for i, n := range lines {
		if i >= limit {
			rep.NotesOmitted = len(lines) - limit
			break
		}
		rep.Notes = append(rep.Notes, n)
	}
	return rep
}

// inventoryReason says why a run wrote nothing. The empty string is the only case
// where nothing has to be explained: something was written, in the mode that asked
// for it.
func inventoryReason(plan inventoryPlan, msg inventoryLoadMessage, applied bool) string {
	if applied {
		return ""
	}
	if msg.Apply {
		// The mode asked to write and there was nothing to write. Worth its own
		// sentence: on a re-run this is the *expected* outcome and reads alarmingly
		// like the reporting-mode one otherwise.
		return fmt.Sprintf("nothing was written: none of the %d observations produced a new "+
			"record. %d are already recorded as legacy, %d are already held by a better "+
			"authority, and the rest resolved to nobody or to no product. A load only ever "+
			"adds, so running it again is safe and, once it has run, quiet",
			len(msg.Observations), plan.Counts.Held, plan.Counts.Keep)
	}
	return "nothing was written: the message asked for a report (apply was false or absent). " +
		"Everything below is what a run with apply set would do, decided by the same code that " +
		"would do it. Nothing has moved, so the next run reads the same target system again and " +
		"decides the same way"
}
