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
}
