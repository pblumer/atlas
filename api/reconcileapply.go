package api

import (
	"fmt"

	"github.com/pblumer/atlas/limits"
)

// Folding one run into the journal, and saying what it found
// (ADR-draft-reconciliation).

// applyReconcilePlan records a run's findings as transitions.
//
// Two halves, and the second is the one that is easy to get wrong. Findings still
// present move their last-seen moment or open a new episode. Findings that are
// **gone** close — but only the ones this run was entitled to conclude anything
// about: same system, and an item the scope actually covered. A run that closed
// every open finding it did not happen to see would report a whole estate as
// repaired the first time somebody reconciled one group.
//
// Unlike the commissioning load this writes no entitlements. Nothing here changes
// what Atlas believes or what a target system holds; it changes only what is
// recorded about the disagreement between them, which is the whole posture of this
// slice.
func (s *Server) applyReconcilePlan(plan reconcilePlan, system string, now int64) (opened, closed, refused int, err error) {
	journal, err := s.discrepancies.LoadAll()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read the discrepancy journal: %w", err)
	}
	byID := make(map[string]discrepancyRecord, len(journal))
	standing := 0
	for _, r := range journal {
		byID[r.ID] = r
		if r.Open() {
			standing++
		}
	}
	ceiling := int(s.budgets().ReconcileJournal)

	found := make(map[string]bool, len(plan.Found))
	for _, d := range plan.Found {
		found[d.ID] = true
		prev := byID[d.ID]
		// The ceiling applies to *opening* and never to a finding already standing:
		// refusing to move the last-seen moment of something already recorded would
		// close it on the next run as though it had gone away, which is the one
		// wrong answer a full journal must not start producing.
		if isNew := prev.ID == "" || !prev.Open(); isNew && standing >= ceiling {
			refused++
			continue
		}
		next, changed := seenDiscrepancy(prev, d, now)
		if !changed {
			continue
		}
		if prev.ID == "" || !prev.Open() {
			opened++
			standing++
		}
		if err := s.discrepancies.Save(next); err != nil {
			return opened, closed, refused, fmt.Errorf("record discrepancy %s: %w", d.ID, err)
		}
	}

	for _, r := range journal {
		switch {
		case !r.Open(), found[r.ID], r.System != system, !plan.InScopeItems[r.ItemID]:
			continue
		}
		if err := s.discrepancies.Save(closeDiscrepancy(r, closedGone, "", now)); err != nil {
			return opened, closed, refused, fmt.Errorf("close discrepancy %s: %w", r.ID, err)
		}
		closed++
	}
	return opened, closed, refused, nil
}

// reconcileReport is what one run tells a person.
type reconcileReport struct {
	System string `json:"system"`
	// Scope is what the run claimed to have read completely, echoed so a report
	// read later says what it was entitled to conclude. A finding without its
	// scope beside it is a finding nobody can weigh.
	Scope []string `json:"scope"`

	Counts reconcileCounts `json:"counts"`

	// Opened and Closed are the transitions this run recorded — the two numbers
	// worth reading first. Everything else in a report is a state; these are what
	// changed since the last look.
	Opened int `json:"opened"`
	Closed int `json:"closed"`
	// NotRecorded is how many new findings the journal had no room for. It is
	// never silently zero-looking: a run that found four hundred things and could
	// record none of them must not read like a quiet one.
	NotRecorded int `json:"notRecorded"`

	// Findings are the disagreements, and Notes what could not be compared.
	Findings     []discrepancy `json:"findings"`
	Notes        []discrepancy `json:"notes"`
	Omitted      int           `json:"omitted"`
	InventoryOf  int           `json:"inventoryExamined"`
	NothingFound bool          `json:"nothingFound"`

	// Reason is filled whenever a run found nothing, because "nothing" has three
	// causes that look identical in an empty list: everything agrees, the scope
	// covered nothing, or the reading carried nothing.
	Reason string `json:"reason,omitempty"`

	// Warning is filled when the findings are sound against the contract and
	// suspicious against experience. There is exactly one such case and it is worth
	// a field of its own — see [reconcileWarning].
	Warning string `json:"warning,omitempty"`
}

// reconcileReportOf renders a plan.
func reconcileReportOf(plan reconcilePlan, msg reconcileMessage, opened, closed, refused, examined int,
	budgets limits.Limits) reconcileReport {

	rep := reconcileReport{
		System: msg.System, Scope: msg.Refs, Counts: plan.Counts,
		Opened: opened, Closed: closed, NotRecorded: refused,
		Findings: []discrepancy{}, Notes: []discrepancy{},
		InventoryOf: examined,
	}
	if rep.Scope == nil {
		rep.Scope = []string{}
	}

	limit := int(budgets.ReconcileReport)
	for i, d := range plan.Found {
		if i >= limit {
			rep.Omitted = len(plan.Found) - limit
			break
		}
		rep.Findings = append(rep.Findings, d)
	}
	// The notes share the ceiling rather than having one of their own: what a
	// reader can absorb is a number of lines, not a number of lines per category.
	for _, d := range plan.Notes {
		if len(rep.Findings)+len(rep.Notes) >= limit {
			rep.Omitted += len(plan.Notes) - len(rep.Notes)
			break
		}
		rep.Notes = append(rep.Notes, d)
	}

	rep.NothingFound = len(plan.Found) == 0
	rep.Reason = reconcileReason(plan, msg)
	rep.Warning = reconcileWarning(plan, msg)
	if refused > 0 {
		rep.Reason = fmt.Sprintf("%d new finding(s) were not recorded: the journal already holds "+
			"%d open ones, which is its ceiling (%s). Findings already standing were still "+
			"updated. A number this size is a scope or a catalogue that is wrong rather than a "+
			"governance backlog — deal with the standing ones, or narrow the scope",
			refused, budgets.ReconcileJournal, "ATLAS_LIMIT_RECONCILE_JOURNAL")
	}
	return rep
}

// reconcileReason explains an empty answer, and only an empty one.
//
// Three different situations produce no findings, and an operator reading a bare
// zero cannot tell them apart: everything in scope agreed, the scope resolved to
// no product at all, or the reading carried no observations. The middle one looks
// like success and is usually a misspelled system name.
func reconcileReason(plan reconcilePlan, msg reconcileMessage) string {
	if len(plan.Found) > 0 {
		return ""
	}
	switch {
	case len(plan.InScopeItems) == 0:
		return fmt.Sprintf("nothing was compared: none of the %d reference(s) in scope resolved "+
			"to a product in system %q. Either no product declares them as target references, or "+
			"the system name does not match the one the catalogue uses — check a product's target "+
			"references before reading this as agreement",
			len(msg.Refs), msg.System)
	case len(msg.Observations) == 0:
		return fmt.Sprintf("no disagreement, and nothing to disagree about: the scope named %d "+
			"reference(s), the reading carried no observations, and the inventory records nobody "+
			"holding what they resolve to. Both sides are empty",
			len(msg.Refs))
	default:
		return fmt.Sprintf("no disagreement: %d recorded right(s) in scope were found in %q, and "+
			"nothing was held there that is not recorded. This is what a healthy run looks like, "+
			"and it produces no journal entry — only changes are recorded",
			plan.Counts.Agrees, msg.System)
	}
}

// reconcileWarning names the one case where this endpoint is correct and probably
// wrong at the same time.
//
// A reading that carries **no observations at all**, over a scope where Atlas does
// record rights, means — taken at its word — that every one of them has vanished.
// That is exactly what the scope promise says, and the findings are computed
// accordingly, because the alternative is a heuristic that protects against one
// shape of a broken reading and not against the others: a worker that returned
// half a group produces the same class of wrong answer and no special case would
// catch it.
//
// What is special about zero is not the logic but the prior. A worker that failed,
// a credential that expired, a group id that no longer resolves — all of them
// return nothing, and all of them are far more common than a company emptying
// every group it reconciles on the same day. So the findings stand and the report
// says out loud what it suspects. Nothing acts on a finding without a person, and
// this is the sentence that person needs before they act on several hundred.
func reconcileWarning(plan reconcilePlan, msg reconcileMessage) string {
	if len(msg.Observations) != 0 || plan.Counts.Missing == 0 {
		return ""
	}
	return fmt.Sprintf("this reading carried no observations at all, and %d recorded right(s) in "+
		"scope are therefore reported as missing. That is what a complete reading of an empty "+
		"scope means, and it is also what a worker that failed looks like. Check that the read "+
		"actually ran before acting on any of these — an empty answer is far more often a broken "+
		"reading than an emptied estate",
		plan.Counts.Missing)
}
