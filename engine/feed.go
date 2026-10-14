package engine

import "github.com/pblumer/atlas/model"

// The event feed's retention (ADR-0429 §5).
//
// The feed is state folded from facts — every action outcome, every grant, every
// revocation — and it is shortened the same way: by a fact. The retention sweep picks
// the last position old enough to drop and freezes it into the command; the event
// carries it; applyToState drops the rows through it and remembers the cut, live and
// on replay alike (I4). Nothing else ever deletes a feed row.

// PruneFeed enqueues dropping every feed row of this partition at or before through,
// a log position.
func (p *Processor) PruneFeed(through uint64) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTFeedRetention,
		Intent:    model.IntentFeedPruning,
		Value:     inflightValue{feedRetention: model.FeedRetentionValue{Through: through}},
	})
}

func handleFeedPruning(c *ProcessingContext) {
	c.appendEvent(0, model.VTFeedRetention, model.IntentFeedPruned, c.cmd.Value)
}
