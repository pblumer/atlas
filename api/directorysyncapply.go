package api

import (
	"fmt"

	"github.com/pblumer/atlas/limits"
)

// Writing a decided plan, and turning it into something a person reads
// (ADR-0332).

// applyDirectoryPlan writes a plan and does nothing else.
//
// There is no rule in this function, and that is its specification. Every question —
// whether an account exists, which one an object belongs to, whether disabling it
// would lock the instance out — was answered by [decideDirectorySync], and answering
// any of them again here would be the second implementation the reporting mode exists
// to make impossible.
//
// It must be called on the run-loop goroutine, in the same turn that produced the
// plan. Deciding and writing in one turn is what makes the plan describe the state it
// is written against; splitting them would leave a window in which an administrator's
// edit disappears under a decision taken before it.
func (s *Server) applyDirectoryPlan(plan directoryPlan, msg directorySyncMessage, state directorySyncState, now int64) error {
	for _, dec := range plan.Users {
		switch dec.Action {
		case dirUserUnchanged, dirUserRefuse:
			continue
		}
		// The picture first, then the record, and the order is the whole of what this
		// loop decides (ADR-0367). A failure between them leaves a
		// picture no record points at, which the next run repairs through the
		// fingerprint; the other order would leave a record claiming a picture that is
		// not there, which nothing repairs because the record already agrees with
		// itself.
		switch {
		case dec.PhotoClear:
			if err := s.users.clearAvatar(dec.Record.ID); err != nil {
				return fmt.Errorf("entra sync: remove the picture of %s: %w", dec.Record.ID, err)
			}
		case len(dec.Photo) > 0:
			if err := s.users.saveAvatar(dec.Record.ID, dec.Photo, dec.PhotoType); err != nil {
				return fmt.Errorf("entra sync: write the picture of %s: %w", dec.Record.ID, err)
			}
		}
		if err := s.users.Save(dec.Record); err != nil {
			return fmt.Errorf("entra sync: write account %s: %w", dec.Record.ID, err)
		}
	}
	for _, dec := range plan.Groups {
		switch dec.Action {
		case dirGroupUnchanged:
			continue
		}
		if err := s.groups.Save(dec.Record); err != nil {
			return fmt.Errorf("entra sync: write group %s: %w", dec.Record.ID, err)
		}
	}
	return s.directorySync.Save(advanceDirectoryState(state, msg, now))
}

// advanceDirectoryState moves the cursor and counts the run.
//
// An empty cursor in the message leaves the stored one alone. It means "this run did
// not read that collection", which is what a message carrying only accounts looks
// like — and overwriting a cursor with nothing would silently schedule a full
// enumeration of a collection nobody asked about. Clearing a cursor deliberately is
// deleting the record, which is a thing an operator does knowingly.
func advanceDirectoryState(state directorySyncState, msg directorySyncMessage, now int64) directorySyncState {
	next := state
	next.ID = directorySyncStateID
	next.Revision = state.Revision + 1
	if msg.UsersDeltaLink != "" {
		next.UsersDeltaLink = msg.UsersDeltaLink
	}
	if msg.GroupsDeltaLink != "" {
		next.GroupsDeltaLink = msg.GroupsDeltaLink
	}
	next.AppliedAt = now
	next.UpdatedAt = now
	return next
}

// The two modes, as the report names them.
const (
	directoryModeReport = "report-only"
	directoryModeApply  = "applied"
)

// directoryReport is what one run tells a person.
//
// Numbers for the expected and whole lines for the notable, because the two are read
// differently: nobody reads ten thousand lines saying an account would be created,
// and everybody has to read the twelve saying an account would be merged onto one
// that already exists. The counts are unbounded — they are numbers — and the lines
// are not; what does not fit is counted in NotesOmitted and never dropped in silence.
type directoryReport struct {
	// Applied says whether anything was written, and Mode says which mode asked for
	// that. They are separate fields because they answer different questions: a run
	// that meant to write and was refused as stale is `applied: false` in
	// `mode: applied`, and collapsing the two would hide exactly that case.
	Applied bool   `json:"applied"`
	Mode    string `json:"mode"`

	// Reason says, in a sentence, why nothing was written. It is filled on every run
	// that wrote nothing — including the ordinary reporting run, which is the point:
	// a mirror left in reporting mode for a month must say so in every report rather
	// than looking like a mirror with nothing to do.
	Reason string `json:"reason,omitempty"`

	// Revision is where the state stands after this run, and EverApplied whether any
	// run ever wrote. A false EverApplied on an installation that has been
	// synchronising for weeks is the sentence somebody needs to read.
	Revision      int64 `json:"revision"`
	EverApplied   bool  `json:"everApplied"`
	LastAppliedAt int64 `json:"lastAppliedAt,omitempty"`

	Counts       directoryCounts `json:"counts"`
	Notes        []directoryNote `json:"notes"`
	NotesOmitted int             `json:"notesOmitted"`
}

// directoryReportOf renders a plan, identically in both modes.
//
// applied is passed in rather than recomputed, so the one thing that differs between
// a reporting run and a real one is the one thing that is supposed to differ.
func directoryReportOf(plan directoryPlan, msg directorySyncMessage, state directorySyncState,
	applied bool, budgets limits.Limits) directoryReport {

	rep := directoryReport{
		Applied:       applied,
		Mode:          directoryModeReport,
		Revision:      state.Revision,
		EverApplied:   state.AppliedAt != 0,
		LastAppliedAt: state.AppliedAt,
		Counts:        plan.Counts,
		Notes:         []directoryNote{},
	}
	if msg.Apply {
		rep.Mode = directoryModeApply
	}
	if applied {
		rep.Revision = state.Revision + 1
		rep.EverApplied = true
	}
	rep.Reason = directoryReason(plan, msg, applied)

	limit := int(budgets.DirectoryReport)
	for i, n := range plan.Notes {
		if i >= limit {
			rep.NotesOmitted = len(plan.Notes) - limit
			break
		}
		rep.Notes = append(rep.Notes, n)
	}
	return rep
}

// directoryReason says why a run wrote nothing. The empty string is the only case
// where nothing has to be explained: something was written, in the mode that asked
// for it.
func directoryReason(plan directoryPlan, msg directorySyncMessage, applied bool) string {
	if applied {
		return ""
	}
	// Staleness first: a repeat delivery of a batch that asked to be applied is not a
	// reporting run, and saying so is the difference between "somebody left this in
	// reporting mode" and "this message arrived twice".
	if plan.Stale {
		return fmt.Sprintf("nothing was written: this message was read against revision %d and the state "+
			"is at revision %d, so it is a repeat, or a run another overtook. What it carries is either "+
			"already recorded or still ahead of the stored cursor, and will be read again",
			msg.FromRevision, plan.Current)
	}
	return "nothing was written: the message asked for a report (apply was false or absent). " +
		"Everything below is what a run with apply set would do, decided by the same code that would " +
		"do it. The cursor has not moved, so the next run reads this same change set again"
}
