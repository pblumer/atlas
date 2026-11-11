package api

import (
	"log/slog"

	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// An order line whose process an operator ended
// (ADR-draft-a-cancelled-line-instance-settles-its-line).
//
// A line settles when its process reports, and an operator who cancels that process
// from the instance list ends the one thing that was going to. So every operator
// cancel path — DELETE /api/v1/instances/{key}, the bulk terminate in both its modes,
// and a definition's drain — hands the keys it cancelled to
// [Server.settleLinesEndedBy] once its drive has made the terminations durable, and
// that settles the lines that were waiting on them.
//
// It runs after the fact and outside the engine on purpose. The engine's fold
// (applyToState) must stay deterministic and touch nothing but engine state; the
// order is a design-time store, and settling it publishes a message. Observed here,
// after the drive returned, the end is on disk before anything reads it (I2).
//
// The paths the shop itself takes do not come through here: withdrawing a line or an
// order settles the line first and then stops its work (ordercancel.go), so the line
// is no longer waiting on anything when its instance ends. Nothing is lost if one
// does: a line that is not waiting on the key is left alone.

// endedInstance is one instance that ended and the order its start variables name.
type endedInstance struct {
	key     uint64
	orderID string
	state   string
}

// settleLinesEndedBy settles the order lines that were waiting on any of keys, which
// the caller has just cancelled and driven to their end.
//
// Nothing here fails the cancel. The instances are terminated and durable by the time
// this runs, and a caller told otherwise would retry a cancel that now answers 404. A
// line that could not be settled is said in the log, and the fulfilment repair finds
// it again: its dry run lists every line waiting on an instance that is no longer
// active.
func (s *Server) settleLinesEndedBy(keys []uint64) {
	if s.orders == nil || len(keys) == 0 {
		return
	}
	var ended []endedInstance
	// Off the loop: a drain hands up to fifty thousand keys, and reading each one's
	// variables is a cost of the batch, not something to hold the writer for
	// (ADR-0239).
	err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		for _, k := range keys {
			pi, ok, err := rv.ProcessInstance(k)
			if err != nil {
				return err
			}
			// Still active is a cancel that did not take — an instance that finished
			// on its own in between, or one whose termination is still under way. Its
			// end, when it comes, is either a report or the repair's to find.
			if !ok || pi.State == model.PIActive {
				continue
			}
			orderID, err := orderIDOf(rv, k)
			if err != nil {
				return err
			}
			if orderID != "" {
				ended = append(ended, endedInstance{key: k, orderID: orderID, state: pi.State.String()})
			}
		}
		return nil
	})
	if err != nil {
		logging.Warn(logging.OrderLineUnsettled,
			"cancelled instances could not be checked for the order lines waiting on them",
			slog.Int("instances", len(keys)), slog.String("error", err.Error()))
		return
	}
	for _, e := range ended {
		s.settleLineEndedBy(e)
	}
}

// settleLineEndedBy settles the lines of one order waiting on one ended instance,
// and says what it did.
func (s *Server) settleLineEndedBy(e endedInstance) {
	_, settled, err := s.orders.InstanceEnded(e.orderID, e.key, e.state)
	switch {
	case err != nil && len(settled) == 0:
		logging.Warn(logging.OrderLineUnsettled,
			"an instance an order line was waiting on ended, and the line could not be settled",
			slog.String("order", e.orderID), slog.Uint64("instance", e.key), slog.String("error", err.Error()))
		return
	case err != nil:
		// Settled and saved; what failed is its outcome record or the wake.
		logging.Warn(logging.OrderLineUnsettled,
			"an order line was settled after its instance ended, but its outcome or the wake failed",
			slog.String("order", e.orderID), slog.Uint64("instance", e.key), slog.String("error", err.Error()))
	}
	for _, l := range settled {
		logging.Warn(logging.OrderLineInstanceEnded,
			"the instance an order line was waiting on ended without reporting; the line was settled",
			slog.String("order", e.orderID), slog.String("position", l.Line),
			slog.Uint64("instance", e.key), slog.String("instanceState", e.state),
			slog.String("status", string(l.Status)))
	}
}

// orderIDOf is the order id instance key was started with, or empty for one that
// names no order. The same variable the start recorded the instance from
// ([Server.notePositionInstanceOp]), read the same way: a string, or nothing.
func orderIDOf(rv *state.ReadView, key uint64) (string, error) {
	var orderID string
	err := rv.VariablesOfScope(key, func(v *model.VariableValue) error {
		if v.Name == progressOrderVar && v.Kind == model.VarString {
			orderID = v.Text
		}
		return nil
	})
	return orderID, err
}
