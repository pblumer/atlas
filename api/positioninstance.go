package api

import (
	"log/slog"
	"time"

	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/model"
)

// Which instances work an order's position (ADR-draft-the-shop-shows-an-orders-open-tasks).
//
// Every process that works a position is started through this server with the
// two variables that name it — `orderId` and `positionId` — whoever starts it:
// the fulfilment orchestration, an approval model starting the provisioning it
// approved, the return route. So the server notes the link at the one moment it is
// certain, on the order itself, and the shop reads the position's open tasks from
// the instances it names instead of searching every running instance for them.

// notePositionInstance records a freshly started instance on the order position
// its start variables name, if they name one.
//
// Nothing here fails the start. The instance is durable and running by the time
// this is called; a caller told the start failed would retry it and the position
// would be worked twice. What a failure costs is the shop's view of that
// instance's tasks, and that is what the warning says.
func (s *Server) notePositionInstance(vars []model.VariableValue, key uint64, processID string) {
	if s.orders == nil || key == 0 {
		return
	}
	var orderID, positionID string
	for _, v := range vars {
		if v.Kind != model.VarString {
			continue
		}
		switch v.Name {
		case progressOrderVar:
			orderID = v.Text
		case progressPositionVar:
			positionID = v.Text
		}
	}
	if orderID == "" || positionID == "" {
		return
	}
	_, err := s.orders.RecordInstance(orderID, positionID, order.LineInstance{
		Key:       key,
		ProcessID: processID,
		StartedAt: time.Now().UnixNano(),
	})
	if err != nil {
		logging.Warn(logging.OrderInstanceUnrecorded,
			"an instance working an order position could not be noted on the order",
			slog.String("order", orderID), slog.String("position", positionID),
			slog.Uint64("instance", key), slog.String("error", err.Error()))
	}
}
