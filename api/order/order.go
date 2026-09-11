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
	// StatusBlocked is a line that cannot be attempted because something it
	// requires is Failed or Rejected. See Line.BlockedBy.
	StatusBlocked LineStatus = "blocked"
)

// Satisfied reports whether a line in this status meets a dependent's
// precondition. Done and Skipped do; everything else, including a line still
// running, does not yet.
func (s LineStatus) Satisfied() bool { return s == StatusDone || s == StatusSkipped }

// Settled reports whether a line will not change again without somebody acting.
func (s LineStatus) Settled() bool {
	switch s {
	case StatusDone, StatusSkipped, StatusFailed, StatusRejected, StatusBlocked:
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
}

// Status is where a whole order stands. It is derived from the lines rather than
// stored, so it can never disagree with them.
type Status string

const (
	// OrderRunning has at least one line still to settle.
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
