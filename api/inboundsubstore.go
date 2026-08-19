package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// inboundSubscription is an operator-configured inbound event binding for a clio
// connector (ADR-0075): the bridge watches WatchedSubject on the connector's clio
// instance and republishes each new event as an Atlas message named MessageName,
// correlated on CorrelationKey (a FEEL expression over the event body; empty =
// keyless), so the event both starts message-start processes and wakes waiting
// message-catch instances. LastEventID is a *best-effort* cursor advanced after each
// batch is durably published: it only speeds a restart's resume. Correctness — no
// duplicate delivery across a crash — is guaranteed by the engine's durable
// high-water mark (ADR-0075), not by this cursor, so losing it re-reads harmlessly.
type inboundSubscription struct {
	ID             string `json:"id"`
	ConnectorID    string `json:"connectorId"`    // FK → a kind:"clio" connector record
	WatchedSubject string `json:"watchedSubject"` // clio subject path, e.g. "orders/new"
	Recursive      bool   `json:"recursive"`      // include the subject's subtree
	MessageName    string `json:"messageName"`    // published Atlas message name
	CorrelationKey string `json:"correlationKey"` // FEEL over the event body; "" = keyless
	Enabled        bool   `json:"enabled"`
	CreatedAt      int64  `json:"createdAt"`
	LastEventID    string `json:"lastEventId,omitempty"` // best-effort resume cursor
	// StartFromTip makes a new subscription forward-only: on first activation the
	// bridge primes its cursor past the subject's existing backlog WITHOUT
	// republishing it, so connecting a watch to a subject that already has history
	// does not start a process per historical event (the reported /employees flood,
	// ADR-0075). Defaults to true on create; set false to intentionally backfill the
	// whole history. Primed records that the one-time skip has completed.
	StartFromTip bool `json:"startFromTip"`
	Primed       bool `json:"primed,omitempty"`
}

// inboundSubStore is a durable store for inbound subscriptions, one JSON file per id
// under a directory — the same sidecar approach as the connector store
// (ADR-0019/0041). It is owned solely by the server's run-loop goroutine, so it needs
// no locking, and it holds no secret material.

// inboundSubStore is a durable store for inboundSubscription records, one JSON file per id
// under a single directory (ADR-0019). Like every design-time store it is owned
// solely by the server's run-loop goroutine, so it needs no locking of its own.
type inboundSubStore = sidecar.Store[inboundSubscription]

// newInboundSubStore opens (creating if needed) the inboundsub directory.
func newInboundSubStore(dir string) (*inboundSubStore, error) {
	return sidecar.NewStore(dir, "inboundsubstore",
		func(rec inboundSubscription) string { return rec.ID },
		sidecar.Order(func(a, b inboundSubscription) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}
