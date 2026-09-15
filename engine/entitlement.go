package engine

import "github.com/pblumer/atlas/model"

// The inventory's two commands.
//
// An entitlement is observed or derived, never authored
// (ADR-0312), so there are two of them and no
// third for editing one: a correction is a revocation and a grant, both of which
// say when they happened. The day somebody can edit an entitlement directly is the
// day the inventory stops being evidence and becomes an opinion.
//
// Both go through the log like every other engine fact, which is the whole point
// of putting the inventory here: the order instance that produced an entitlement
// is eligible for retention deletion long before the entitlement ends, and an
// access record that could not be rebuilt after that is not a record.

// GrantEntitlement enqueues the fact that a principal now holds an item. The
// moment travels in the value, read by the caller at command time and frozen into
// the event, so replay reproduces it rather than re-reading a clock (I4/I6).
//
// Granting something already held replaces it, which is how a re-grant after a
// revocation reads: one entitlement with a new start, rather than two overlapping
// ones nobody can subtract.
func (p *Processor) GrantEntitlement(v model.EntitlementValue) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTEntitlement,
		Intent:    model.IntentEntitlementGranted,
		Value:     inflightValue{entitlement: v},
	})
}

// RevokeEntitlement enqueues the fact that a principal no longer holds an item.
// Revoking something nobody was recorded as holding is a no-op rather than an
// error: it is the state the caller asked for, and refusing it would make a
// reconciliation that removes a privilege twice into a failure.
func (p *Processor) RevokeEntitlement(principal, itemID string) {
	p.queue = append(p.queue, Command{
		ValueType: model.VTEntitlement,
		Intent:    model.IntentEntitlementRevoked,
		Value: inflightValue{entitlement: model.EntitlementValue{
			Principal: principal, ItemID: itemID,
		}},
	})
}

// handleEntitlementGranted records a grant. It refuses an entitlement that names
// nobody or nothing: both halves of "who holds what" are required, and a record
// missing one is a fact about nothing that a reader would nonetheless count.
func handleEntitlementGranted(c *ProcessingContext) {
	v := c.cmd.Value.entitlement
	if !v.Valid() {
		return
	}
	c.appendEvent(0, model.VTEntitlement, model.IntentEntitlementGranted,
		inflightValue{entitlement: v})
}

// handleEntitlementRevoked records a revocation.
func handleEntitlementRevoked(c *ProcessingContext) {
	v := c.cmd.Value.entitlement
	if !v.Valid() {
		return
	}
	c.appendEvent(0, model.VTEntitlement, model.IntentEntitlementRevoked,
		inflightValue{entitlement: v})
}
