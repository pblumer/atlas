package api

import (
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
)

// The catalogue's fulfilment report walks the path an order takes, and it names
// that path in its own package — a process id and a kind-to-process table that
// the order package already owns.
//
// Copied rather than imported, because the order package depends on the catalogue
// package and importing back would be a cycle. A copy with nothing holding it to
// the original is how the first version of that table came to name three
// processes that were never deployed: every approval failed to start, and nothing
// said so. This is the thing holding it.

// TestTheReportWalksTheProcessOrdersActuallyTake.
func TestTheReportWalksTheProcessOrdersActuallyTake(t *testing.T) {
	if catalog.OrderProcess != order.FulfilmentProcess {
		t.Errorf("the report checks %q and orders are worked by %q, so the one thing "+
			"every order goes through is not the thing the report reads",
			catalog.OrderProcess, order.FulfilmentProcess)
	}
}

// TestTheReportResolvesAnApprovalKindTheWayFulfilmentDoes.
//
// Through the order package's own resolver rather than against a second table
// here: a guard that restated the mapping would be the copy it exists to prevent.
func TestTheReportResolvesAnApprovalKindTheWayFulfilmentDoes(t *testing.T) {
	// Every kind the catalogue offers, plus one an installation defines itself.
	for _, kind := range []string{"none", "fixed", "role", "superior", "mein-eigener-prozess"} {
		want := order.Line{Approval: order.Approval{Kind: kind}}.ApprovalProcess()
		got := catalog.ApprovalProcessFor(catalog.Item{
			Approval: catalog.Approval{Kind: catalog.ApprovalKind(kind)},
		})
		if got != want {
			t.Errorf("kind %q: the report checks %q and fulfilment starts %q — the "+
				"report would pass an installation whose approvals cannot start, or "+
				"fail one whose approvals work", kind, got, want)
		}
	}
}
