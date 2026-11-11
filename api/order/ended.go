package order

import (
	"strconv"

	"github.com/pblumer/atlas/api/catalog"
)

// A line whose process ended without saying how (ADR-0558).
//
// A line moves only when its process reports — the provisioning's outcome, the
// return's, the approver's decision. An operator who cancels that process directly
// (DELETE /api/v1/instances/{key}, the bulk terminate, a definition's drain) ends the
// one thing that was going to report, and the line said "running" for ever: the
// fulfilment orchestration waited on a message nobody would publish, and nothing
// warned. So the end of the instance the line is waiting on is itself an outcome,
// and it is the one a failed report would have been — the attempt did not deliver,
// and somebody has to look.

// approvalOp is the operation an approval instance is recorded under.
const approvalOp = "approval"

// WaitsOn is the instance this line is waiting to hear from, and the status the line
// settles to if that instance ends without reporting. ok is false for a line that
// waits on no instance: one that has settled, one nothing was started for, or one
// whose newest instance is not the one its status says is working.
//
// Three statuses wait on an instance, each on a different one:
//
//   - running waits on its newest provisioning. Ended, it settles failed: nothing
//     was delivered as far as anybody knows, which is exactly what failed says, and
//     failed is not terminal — the order stays open until somebody repairs or
//     abandons it.
//   - returning waits on its newest deprovisioning. Ended, it settles returnFailed:
//     the recipient still has the thing as far as anybody knows, and asking for the
//     return again is the ordinary repair.
//   - pending waits on its approval, while nobody approved and no provisioning was
//     started. Ended, it settles failed too, and deliberately not rejected or
//     cancelled: both are decisions with an author — an approver's refusal, the
//     orderer's withdrawal — and an operator stopping a process is neither. Filed as
//     a rejection it would tell the orderer somebody refused them.
func (l Line) WaitsOn() (inst LineInstance, settle LineStatus, ok bool) {
	switch l.Status {
	case StatusRunning:
		// An instance recorded with no operation is from a starter that did not say
		// (ADR-0416, before ADR-0425); on a running line that is its provisioning.
		if in, found := newestOf(l, catalog.OpProvision, ""); found {
			return in, StatusFailed, true
		}
	case StatusReturning:
		if in, found := newestOf(l, catalog.OpDeprovision); found {
			return in, StatusReturnFailed, true
		}
	case StatusPending:
		if n := len(l.Instances); n > 0 && l.ApprovedBy == "" && l.StartsOf(catalog.OpProvision) == 0 &&
			l.Instances[n-1].Operation == approvalOp {
			return l.Instances[n-1], StatusFailed, true
		}
	}
	return LineInstance{}, "", false
}

// newestOf is the newest instance recorded for one of ops.
func newestOf(l Line, ops ...string) (LineInstance, bool) {
	for i := len(l.Instances) - 1; i >= 0; i-- {
		for _, op := range ops {
			if l.Instances[i].Operation == op {
				return l.Instances[i], true
			}
		}
	}
	return LineInstance{}, false
}

// Ended is one line settled because the instance it waited on ended.
type Ended struct {
	// Line is the line's key.
	Line string
	// Status is what it settled to.
	Status LineStatus
}

// SettleEnded settles every line of o that waits on instance key, which has ended
// without reporting, and returns the updated order and what it settled. It returns
// o unchanged and nothing when no line waits on key — the ordinary case for an
// instance that has nothing to do with this order, for a line that already
// reported, and for one whose work a newer instance took over.
//
// Applied once it leaves nothing waiting on key, so applying it again changes
// nothing: that is what lets the end be noticed twice — as it happens and again by
// the repair — without settling anything twice.
func SettleEnded(o Order, key uint64, at int64) (Order, []Ended, error) {
	var ended []Ended
	for _, l := range o.Lines {
		inst, settle, ok := l.WaitsOn()
		if !ok || inst.Key != key {
			continue
		}
		next, err := Apply(o, l.Key(), settle, at)
		if err != nil {
			return o, nil, err
		}
		o = next
		ended = append(ended, Ended{Line: l.Key(), Status: settle})
	}
	return o, ended, nil
}

// EndedResult is the result an outcome of a line settled this way carries
// (ADR-0429 §3): why it failed, and which instance it was waiting on. The order
// line has no place for a failure's reason — [Line.Valid] keeps Reason for a
// decision — so the outcome record is where the why is kept.
func EndedResult(key uint64, state string) string {
	return `{"reason":"the instance working this line ended without reporting an outcome",` +
		`"instanceKey":"` + strconv.FormatUint(key, 10) + `","instanceState":"` + state + `"}`
}

// InstanceEnded settles the lines of order orderID that were waiting on instance
// key, which ended — state says how, as the engine recorded it — without reporting
// an outcome. It does for those lines what [Service.HandleReport] does for a failed
// report: the order is saved, then the inventory is told, then the fulfilment
// process is woken, in that order and for the same reasons.
//
// found is false for an order this server does not hold. Nothing settled is not an
// error: it is the answer for an instance no line was waiting on.
//
// Call it only once the instance's end is durable (I2), and never from inside the
// engine: it reads and writes the order store, which is a design-time store and no
// part of a record's fold.
func (s *Service) InstanceEnded(orderID string, key uint64, state string) (found bool, ended []Ended, err error) {
	var got Order
	s.loop.Do(func() {
		var cur Order
		if cur, found, err = s.store.Get(orderID); err != nil || !found {
			return
		}
		var next Order
		if next, ended, err = SettleEnded(cur, key, s.now()); err != nil || len(ended) == 0 {
			return
		}
		got = next
		err = s.store.Save(got)
	})
	if err != nil || len(ended) == 0 {
		return found, nil, err
	}
	// Unlike a report, nobody is here to retry: the line no longer waits on key, so
	// neither a second cancel nor the repair would come back to it. So an outcome
	// that could not be written does not keep the orchestration asleep — it is woken
	// regardless, and the first failure is what the caller logs.
	result := EndedResult(key, state)
	for _, e := range ended {
		if recErr := s.recordInventoryWith(got, e.Line, e.Status, result); recErr != nil && err == nil {
			err = recErr
		}
	}
	if wakeErr := s.wake(AdvancedMessage, orderID, nil); wakeErr != nil && err == nil {
		err = wakeErr
	}
	return found, ended, err
}
