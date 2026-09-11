// Package order holds what was asked for: an order against one catalogue release,
// its lines, and how a line's outcome reaches the lines that depend on it.
//
// It is the second of the three models in ADR-draft-portal-catalogue-order-inventory.
// An order names exactly one release and follows the schedule that release
// precomputed; it never reconsults the catalogue, and a catalogue edited while an
// approval is pending cannot change what was ordered.
package order

// LineStatus is where one ordered position stands.
//
// The three ways a line can fail to be provisioned are deliberately three values
// and not one. They differ in who has to do something about it, and a status that
// merges them either raises incidents nobody can repair or files a decision as a
// malfunction:
//
//   - Failed is a defect. Something broke; an operator repairs it and it retries.
//   - Rejected is a decision. Nothing is broken, somebody said no, and there is
//     nothing to repair.
//   - Blocked is a consequence. This line was never attempted, because something it
//     needs is Failed or Rejected. It carries what stopped it.
type LineStatus string

const (
	// StatusPending is ordered and not yet reached.
	StatusPending LineStatus = "pending"
	// StatusRunning is with its provisioning process now.
	StatusRunning LineStatus = "running"
	// StatusDone is provisioned.
	StatusDone LineStatus = "done"
	// StatusSkipped is not provisioned because the recipient already holds it.
	// It satisfies dependents exactly as Done does — a VPN whose laptop was
	// already there has its precondition met, and treating the skip as an absence
	// would block a line for the reason that it was unnecessary.
	StatusSkipped LineStatus = "skipped"
	// StatusFailed is a defect in provisioning: an incident to repair.
	StatusFailed LineStatus = "failed"
	// StatusRejected is an approval refused. Not an incident, and never reported
	// as one.
	StatusRejected LineStatus = "rejected"
	// StatusAbandoned is a failure nobody will repair: the incident behind it was
	// given up on, by a person or by the deadline that sits on the incident.
	//
	// It exists because "an order stays open while a blockage is repairable" needs
	// a way for a failure to stop being repairable — otherwise it reads as "an
	// order never closes". The deadline is on the incident rather than on the
	// order, because the incident is the thing that is actually stuck; the order
	// follows it here.
	StatusAbandoned LineStatus = "abandoned"
	// StatusBlocked is a line that cannot be attempted because something it
	// requires is Failed or Rejected. See Line.BlockedBy.
	//
	// It is *derived*, never a line's own outcome, and it is recomputed from
	// scratch on every pass — which is what lets a repair undo it. An operator who
	// fixes the incident behind a failed precondition releases the line that was
	// waiting on it, with nobody rewriting a status by hand.
	StatusBlocked LineStatus = "blocked"
)

// Satisfied reports whether a line in this status meets a dependent's
// precondition. Done and Skipped do; everything else, including a line still
// running, does not yet.
func (s LineStatus) Satisfied() bool { return s == StatusDone || s == StatusSkipped }

// Settled reports whether this is a line's own final outcome.
//
// Blocked is deliberately absent. It is not an outcome but a consequence of other
// lines, recomputed on every pass, so whether a blocked line has finished depends
// on its causes rather than on itself: see [Line.Terminal].
func (s LineStatus) Settled() bool {
	switch s {
	case StatusDone, StatusSkipped, StatusFailed, StatusRejected, StatusAbandoned:
		return true
	}
	return false
}

// Line is one ordered position.
type Line struct {
	// ItemID is the catalogue item this line orders, as the release named it.
	ItemID string `json:"itemId"`
	// VariantID is the variant the basket resolved, empty where the item has none.
	VariantID string     `json:"variantId,omitempty"`
	Status    LineStatus `json:"status"`
	// BlockedBy names the lines whose failure or rejection stopped this one —
	// the *root* causes, not the intermediate blocked lines between.
	//
	// An intermediate line is itself blocked and carries the same causes, so naming
	// it here would make a reader walk the chain to learn what actually happened.
	// "Your VPN is waiting because the laptop was rejected" is the sentence somebody
	// needs; "waiting because the docking station is waiting" is not.
	BlockedBy []string `json:"blockedBy,omitempty"`
	// TerminallyBlocked marks a blocked line that will never run: one of the things
	// it requires was rejected or abandoned, and neither lifts. A decision does not
	// change because an incident was repaired, and an abandoned incident is one
	// nobody is repairing. A line blocked only by live failures is not terminal —
	// somebody can still fix them, and the order waits.
	//
	// It is set by [Propagate] alongside BlockedBy and is meaningless on any other
	// status.
	TerminallyBlocked bool `json:"terminallyBlocked,omitempty"`
	// AbandonedBy and AbandonedAt record who gave up on this line's incident and
	// when. Both are required on an abandoned line and forbidden on any other, so
	// the record can never say a line was given up on without saying by whom.
	//
	// AbandonedBy is a principal id. Never a name — see
	// ADR-draft-portal-personal-data.
	AbandonedBy string `json:"abandonedBy,omitempty"`
	AbandonedAt int64  `json:"abandonedAt,omitempty"`
}

// Terminal reports whether this line has finished moving: either it reached an
// outcome of its own, or it is blocked by something that will not change.
func (l Line) Terminal() bool {
	if l.Status == StatusBlocked {
		return l.TerminallyBlocked
	}
	return l.Status.Settled()
}

// Status is where a whole order stands. It is derived from the lines rather than
// stored, so it can never disagree with them.
type Status string

const (
	// OrderRunning has at least one line still to settle — including a line
	// blocked by a failure somebody can still repair. An order that settled while
	// an incident behind it was being worked would tell the orderer their line is
	// never coming, at the moment somebody is fixing the reason it has not.
	OrderRunning Status = "running"
	// OrderCompleted provisioned or skipped every line.
	OrderCompleted Status = "completed"
	// OrderPartial settled with some lines provisioned and some not. This is an
	// ordinary outcome, not a degraded one: everything that could be provisioned
	// was.
	OrderPartial Status = "partial"
	// OrderUnfulfilled settled with nothing provisioned at all.
	OrderUnfulfilled Status = "unfulfilled"
)

// Order is one order against one catalogue release.
type Order struct {
	ID string `json:"id"`
	// ReleaseID names the frozen catalogue this order was placed against. It is
	// what makes the order immune to catalogue edits while an approval is pending.
	ReleaseID string `json:"releaseId"`
	// Orderer and Recipient are principal ids. Never names, addresses or anything
	// else about a person — see ADR-draft-portal-personal-data.
	Orderer   string `json:"orderer"`
	Recipient string `json:"recipient"`
	Lines     []Line `json:"lines"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}
