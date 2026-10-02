package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// feedSubscription is one push delivery of the event feed
// (ADR-0433): the feed's rows after
// Cursor, narrowed to Reach, POSTed as CloudEvents batches to the endpoint of the
// cloudevents Worker it names.
//
// The cursor is the only thing here that delivery moves, and it moves only after the
// endpoint answered 2xx for every event up to it, so a crash between the answer and the
// write delivers the batch again — at least once, deduplicated by the events' ids, as the
// pull feed is. It is operating state rather than engine state (ADR-0332): nothing in the
// log or in applyToState knows a subscription exists.
type feedSubscription struct {
	ID string `json:"id"`
	// WorkerID is the cloudevents Worker whose endpoint and credential deliver this
	// subscription. A Worker carries several subscriptions — one per catalogue, say — and
	// disabling it pauses them all.
	WorkerID string `json:"workerId"`
	// Reach narrows the subscription to the catalogues named, as an events token's reach
	// does: only the rows about products they maintain are delivered, and the cursor moves
	// past the rest. Empty delivers the whole feed.
	Reach []string `json:"reach,omitempty"`
	// Cursor is the log position of the last row delivered or passed over; delivery reads
	// the rows after it. It is the pull feed's `next`, held by the server instead of the
	// consumer.
	Cursor uint64 `json:"cursor"`
	// BatchSize is how many events one POST carries at most; 0 means the default.
	BatchSize int  `json:"batchSize,omitempty"`
	Enabled   bool `json:"enabled"`
	// DisabledReason says why delivery switched this subscription off — the feed's
	// retention dropped rows it had not yet delivered — for whoever finds it off later.
	// Empty for one an administrator disabled.
	DisabledReason string `json:"disabledReason,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
	CreatedBy      string `json:"createdBy,omitempty"`
	// DeliveredAt is when the endpoint last accepted a batch, in unix seconds.
	DeliveredAt int64 `json:"deliveredAt,omitempty"`
}

// feedSubStore holds the feed subscriptions, one JSON file per id (ADR-0019). Like every
// design-time store it is owned by the run loop and read off it freely.
type feedSubStore = sidecar.Store[feedSubscription]

// newFeedSubStore opens (creating if needed) the feed subscription directory.
func newFeedSubStore(dir string) (*feedSubStore, error) {
	return sidecar.NewStore(dir, "feedsubstore",
		func(rec feedSubscription) string { return rec.ID },
		sidecar.Order(func(a, b feedSubscription) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}
