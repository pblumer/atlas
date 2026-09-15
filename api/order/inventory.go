package order

// What a settled line leaves behind.
//
// An order is a request that completes; an entitlement is a right that persists.
// Keeping the second in the order record would tie "what does this person hold
// today" to the retention of the instance that granted it, and the two have
// nothing to do with each other: the order may be deleted years before the right
// ends. So a line that reaches done hands the fact to the inventory, and a line
// that is given back takes it away again.
//
// Only those two transitions do. A skipped line provisioned nothing — it was
// satisfied because the recipient already held the item, and a right that
// predates this order is adopted rather than granted, which is a different origin
// recorded by a different path. Writing it here would make every pre-existing
// privilege look like something this portal handed out.

// Grant is what one settled line tells the inventory. It carries no origin: a
// grant this package makes always came through an order, and a package that
// cannot name any other origin cannot mislabel one.
type Grant struct {
	// Principal is who holds it — an id, never a name.
	Principal string
	ItemID    string
	VariantID string
	// OrderID is the order that granted it, so a right can be traced back to the
	// request and the approval behind it for as long as both are kept.
	OrderID string
	// At is when it started, read from the server clock by the caller.
	At int64
	// Until is when it was meant to end, or zero for a right that does not end.
	// Computed from the ceiling the release's product declared, frozen into the
	// line when the order was placed (ADR-draft-time-bounded-entitlements).
	//
	// A promise about when the access should end, not a statement that it has: the
	// record stays held past it, and a modelled process is what actually takes the
	// access away.
	Until int64
}
