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
	// StatusCancelled is a line the person who ordered it withdrew before it was
	// provisioned.
	//
	// It is a fifth value and not a reuse of Rejected for the reason the three
	// above are three: they differ in who has to do something about it. A
	// rejection is somebody refusing a request that was made; a cancellation is
	// the request being taken back, and nobody has to act on it at all. Filed as a
	// rejection it would say in a record kept for years that an approver turned
	// down a colleague's laptop, when what happened is that the colleague changed
	// their mind.
	StatusCancelled LineStatus = "cancelled"
	// StatusReturning is a provisioned line whose deprovisioning process is
	// running now: the revocation was asked for and has not come back.
	//
	// It is deliberately *not* settled. Something is in flight against a target
	// system, and an order that reported itself finished while an account was
	// half-deleted would be reporting the thing it is least entitled to guess at.
	StatusReturning LineStatus = "returning"
	// StatusReturnFailed is a revocation that ran and did not succeed: the
	// recipient still has the thing, and somebody has to do something about it.
	//
	// It is not Failed, which says a provisioning never delivered — a reader
	// seeing that would conclude nobody has it, and the precedence guard would let
	// the account underneath be revoked out from under something that is very much
	// still there. And it is not a silent fall back to Done, which would lose the
	// fact that a revocation was attempted and lost: the next person to look sees
	// an ordinary held line and no sign anything went wrong.
	//
	// Asking for the return again is the ordinary repair — fix the target system,
	// try once more — and is what [Returnable] accepts it for.
	StatusReturnFailed LineStatus = "returnFailed"
	// StatusReturned is a line that was provisioned and has been given back.
	//
	// Distinct from Cancelled, which is a line that never was: a record that says
	// "cancelled" where somebody held a laptop for three weeks is a record that
	// lost three weeks. What was granted and then revoked is a different fact from
	// what was never granted, and an audit that cannot tell them apart cannot
	// answer who had access when.
	StatusReturned LineStatus = "returned"
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
	case StatusDone, StatusSkipped, StatusFailed, StatusRejected, StatusAbandoned,
		StatusCancelled, StatusReturned:
		return true
	}
	return false
}

// Held reports whether the recipient has this line's product, as far as anybody
// knows, because of this order.
//
// Three statuses say yes, and the two beyond Done are the point. A revocation that
// has been *asked for* has not happened: until it confirms, the access is there,
// and treating a requested return as an absence would let the account underneath
// be revoked out from under a laptop that is still working. One that ran and
// failed is even plainer — it is still held, and now somebody knows it.
//
// A skipped line they got elsewhere and this order never granted; a returned one
// they no longer have; everything before Done was never delivered.
//
// This is what the precedence guard asks: a line may only be given back once
// nothing that needed it is still held. It is deliberately *not* what decides
// whether a line may be returned — see [Returnable], which asks a narrower
// question, because a return already under way must not be asked for twice.
func (s LineStatus) Held() bool {
	switch s {
	case StatusDone, StatusReturning, StatusReturnFailed:
		return true
	}
	return false
}

// Cancellable reports whether a line can still be withdrawn.
//
// Two things cannot. One with an outcome already is finished — including a failed
// one, whose incident is a decision of its own: somebody has to give up on it
// ([Abandon]), and calling that a cancellation would file a repair nobody finished
// as a change of mind. And a running line is with a provisioning process now,
// which is a conversation with a system this server does not control; stopping it
// halfway is not withdrawal but a half-provisioned account nobody owns.
//
// What is left is exactly what has not happened yet: a line still waiting its
// turn, and one blocked behind something else.
func (s LineStatus) Cancellable() bool {
	return s == StatusPending || s == StatusBlocked
}

// Line is one ordered position.
type Line struct {
	// ItemID is the catalogue item this line orders, as the release named it.
	ItemID string `json:"itemId"`
	// VariantID is the variant the basket resolved, empty where the item has none.
	VariantID string     `json:"variantId,omitempty"`
	Status    LineStatus `json:"status"`
	// ProvisionProcess and DeprovisionProcess are the processes bound to this
	// line's product, copied from the release when the order was placed.
	//
	// They travel with the order for the same reason the schedule does: fulfilment
	// reads one record, and looking the binding up in the catalogue at the moment
	// of provisioning would reintroduce exactly the edit the release was frozen
	// against. The deprovision process travels too, so that revoking what this
	// order granted uses the process that was in force when it was granted.
	ProvisionProcess   string `json:"provisionProcess,omitempty"`
	DeprovisionProcess string `json:"deprovisionProcess,omitempty"`
	// Approval is the rule this line is approved under, copied from the release
	// like the bindings are. It travels for the same reason: the rule belongs to
	// the catalogue, and reading it when the line is reached would let a product's
	// approval be relaxed after somebody ordered under the stricter one.
	Approval Approval `json:"approval,omitempty"`
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
	// DecidedBy and DecidedAt record who decided this line's fate and when, and
	// Reason carries their words. All three are required on a rejected line; a
	// cancelled one requires the first two.
	//
	// The two decisions share these fields because they are the same kind of fact —
	// somebody, at a moment, settled this line without it being provisioned — and
	// the status says which kind. A rejection kept forever without an author is a
	// decision nobody made, and one without a reason produces the message that
	// generates a phone call: "your request was declined", and nothing else. A
	// cancellation needs no reason, because the person reading it is the person who
	// made it. DecidedBy is a principal id.
	DecidedBy string `json:"decidedBy,omitempty"`
	DecidedAt int64  `json:"decidedAt,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Terminal reports whether this line has finished moving.
//
// Two statuses that have an outcome of their own are still not terminal, and both
// follow from the same rule — an order stays open while something can still be
// repaired:
//
//   - Failed is an open incident. Somebody repairs it and the line provisions
//     after all, so an order carrying one is not finished. Only [Abandon] ends it.
//   - Blocked depends on its causes: terminal when one of them was rejected or
//     abandoned, open while they are live failures.
//
// This is a different question from [LineStatus.Settled], which asks whether a
// line reached an outcome of its own — a failure has, and that is why propagation
// leaves it alone.
func (l Line) Terminal() bool {
	switch l.Status {
	case StatusBlocked:
		return l.TerminallyBlocked
	case StatusFailed:
		return false
	}
	return l.Status.Settled()
}

// Approval is one line's approval rule, as the release froze it. It mirrors the
// catalogue's shape rather than importing it, so an order can be read back
// without the catalogue package — and so that a change to the authoring shape is
// a deliberate change here rather than a silent one.
type Approval struct {
	// Kind is "none", "fixed", "role", "superior", or the name of a registered
	// approval process.
	Kind string `json:"kind,omitempty"`
	// Ref names the principal for "fixed" or the group for "role", empty for the
	// kinds that resolve their approver from the order itself.
	Ref string `json:"ref,omitempty"`
}

// NeedsApproval reports whether this line waits on somebody before it is
// provisioned. The fulfilment process asks it to decide which process to start,
// so it has to be answerable from the line alone.
func (l Line) NeedsApproval() bool {
	return l.Approval.Kind != "" && l.Approval.Kind != "none"
}

// approvalProcesses maps the three built-in approval kinds to the processes that
// decide them.
//
// It exists because the two vocabularies are not the same words and cannot be
// made to be. A kind is the catalogue's contract — "fixed", "role", "superior",
// the words a product manager binds a product with — and the processes are
// Atlas's own models, named in the language they are written in. The first cut
// built the process id by concatenating the kind onto a prefix, which produced
// three ids that were never deployed: *every* approval would have failed to start,
// and nothing said so, because a mapping that lives in a string expression has
// nowhere to be checked. It is a table here so that a test can walk it against the
// processes this binary actually ships.
var approvalProcesses = map[string]string{
	"fixed":    "atlas-genehmigung-fix",
	"role":     "atlas-genehmigung-rolle",
	"superior": "atlas-genehmigung-vorgesetzter",
}

// ApprovalProcess names the process that decides this line, or "" when the line
// needs none.
//
// It is resolved when fulfilment asks and deliberately *not* frozen into the
// order. What a catalogue promised is frozen — the product, its variant, the rule
// it is approved under — because an edit to the catalogue must not change a
// pending order. Which model implements that rule is not the catalogue's promise;
// it is this installation's wiring, and freezing it would mean an operator who
// redeploys an approval process breaks every order already waiting on one.
//
// A kind that is not one of the three is taken as naming a process directly, which
// is what [Approval.Kind] documents: an installation with its own approval model
// binds a product to it by name.
func (l Line) ApprovalProcess() string {
	if !l.NeedsApproval() {
		return ""
	}
	if p, ok := approvalProcesses[l.Approval.Kind]; ok {
		return p
	}
	return l.Approval.Kind
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
	// OrderCancelled settled because every line was withdrawn.
	//
	// Distinct from unfulfilled, which is what an order says when it tried and did
	// not manage. This one was taken back before it tried, and telling somebody
	// their own cancellation "was not fulfilled" invites them to ask why it
	// failed.
	OrderCancelled Status = "cancelled"
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
	// Waves and Requires are copied from the release when the order is placed, for
	// the lines this order actually carries. The release computed them; the order
	// records them rather than recomputing later against a catalogue that may have
	// moved on, and so that fulfilment reads one record instead of two.
	Waves    [][]string          `json:"waves,omitempty"`
	Requires map[string][]string `json:"requires,omitempty"`
	// Assignments is where each approval sits and every hop it took to get there
	// (assignment.go). There is at most one per line, and only for lines that
	// needed approving.
	//
	// It is written when a deadline first moves an approval and not when the order
	// is placed, because at placement the approver of a "superior" line is not
	// known — the directory has not been asked yet. Until something moves it, the
	// live task's assignee is the whole truth and copying it here would be a second
	// one. What cannot be derived from the task is the *history*: who it started
	// with, every hop, and whether the chain ran out. That is what this holds, and
	// it outlives the task.
	Assignments []Assignment `json:"assignments,omitempty"`
	CreatedAt   int64        `json:"createdAt"`
	UpdatedAt   int64        `json:"updatedAt"`
}

// AssignmentFor returns the recorded approval for one line, and whether there is
// one. A line whose approval has never moved has none.
func (o Order) AssignmentFor(itemID string) (Assignment, bool) {
	for _, a := range o.Assignments {
		if a.ItemID == itemID {
			return a, true
		}
	}
	return Assignment{}, false
}

// WithAssignment returns a copy of the order carrying this approval, replacing any
// record for the same line. A copy, so a caller holding the previous order keeps
// its own slice rather than sharing the array under it.
func (o Order) WithAssignment(a Assignment) Order {
	out := make([]Assignment, 0, len(o.Assignments)+1)
	replaced := false
	for _, have := range o.Assignments {
		if have.ItemID == a.ItemID {
			out, replaced = append(out, a), true
			continue
		}
		out = append(out, have)
	}
	if !replaced {
		out = append(out, a)
	}
	o.Assignments = out
	return o
}
