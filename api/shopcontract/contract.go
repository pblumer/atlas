// Package shopcontract is the boundary between tshopper — the service catalogue,
// the shop and their reports — and the engine that fulfils what they sell
// (tshopper phase A, ADR-0515; docs/planning/tshopper-program.md).
//
// Phase A keeps both halves in one binary (ADR-0011). What it changes is that the
// shop's half reaches the engine through the interfaces declared here instead of
// through the server's fields, so the seam a later phase would cut along is a set
// of Go types a reviewer can read, not a convention. A package on the shop's side
// depends on this one and on nothing of the engine; the server implements it.
//
// The contract is deliberately small and read-mostly. Starting a position stays
// the order's act through the routes ADR-0425 defines, because that is already a
// network-shaped call; what had no shape was the reading — of the inventory, which
// is engine state folded by applyToState (ADR-0312), and of the directory.
//
// Each interface names the TM Forum resource it would answer to at the boundary
// (ADR-0387, docs/comparisons/catalogue-standards.md): the inventory to TMF637
// Product Inventory, the order to TMF622 Product Ordering, the catalogue to TMF620
// Product Catalog. Atlas keeps its own model inside; the mapping is documented,
// not adopted.
package shopcontract

// Hold is one right as the inventory records it (TMF637 Product, in the mapping):
// who holds what, the order that produced it, how it came to be held and when it
// began and is meant to end. A hold that has ended carries its end in Until only
// where the history recorded one.
type Hold struct {
	Principal string
	ItemID    string
	VariantID string
	// OrderID names the order that produced the right; empty for one no order of
	// the shop produced.
	OrderID string
	// Origin is how the right came to be held: ordered, adopted, granted.
	Origin string
	// Since and Until are unix nanoseconds; Until is zero for a right with no end.
	Since int64
	Until int64
}

// InventoryView is one consistent read of the inventory.
type InventoryView interface {
	// EachHold calls fn for every right held now, and stops at the first error.
	EachHold(fn func(Hold) error) error
	// HistoryOf calls fn for every right principal held and no longer holds.
	HistoryOf(principal string, fn func(Hold) error) error
}

// Inventory opens a read of the inventory. The read runs without holding the
// engine's single writer (ADR-0239): an implementation takes its view on the run
// loop and hands it to fn after it has let go.
type Inventory interface {
	Read(fn func(InventoryView) error) error
}

// Account is one person as the directory knows them: what a report needs to say
// who someone is, whom they report to and which catalogue they see.
type Account struct {
	ID        string
	Name      string
	ManagerID string
	Disabled  bool
	// Groups are the ids of the groups the account is a member of.
	Groups []string
}

// Directory lists the accounts this installation knows.
type Directory interface {
	Accounts() ([]Account, error)
}
