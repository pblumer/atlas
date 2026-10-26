package api

import (
	"fmt"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
)

// A process an order still needs cannot be deleted (ADR-0427).
//
// Deleting a process refused only while it had running instances. A product's old
// deprovisioning process, kept for the lines placed before the product was converted
// to a lifecycle process, has none most of the time — and deleting it then succeeds,
// and the next return of such a line fails with "no deployed process". So deletion
// also asks the orders, and the current releases, whether anything can still start
// the process.
//
// Only the last version counts. Deleting v1 of a process whose v2 is deployed leaves
// every order able to start it, because an order starts the newest version.

// processStillNeeded reports why the last deployed version of processID must not be
// deleted, or "" when nothing needs it. It scans the order store, so it runs off the
// run loop (ADR-0239); what it cannot see from there — the current releases — is
// asked by [Server.releaseBindsOnLoop] inside the delete's own turn.
func (s *Server) processStillNeeded(processID string) (string, error) {
	if s.orderStore == nil {
		return "", nil
	}
	orders, err := s.orderStore.All()
	if err != nil {
		return "", err
	}
	counts := order.LinesStarting(orders, processID)
	if len(counts) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(counts))
	total := 0
	for _, c := range counts {
		parts = append(parts, fmt.Sprintf("%s: %d", c.ItemID, c.Lines))
		total += c.Lines
	}
	return fmt.Sprintf("cannot delete: %d order line(s) can still start %s (%s). "+
		"Deactivate it instead — returns keep working — or move the lines to the "+
		"product's lifecycle process first (ADR-0427)", total, processID, strings.Join(parts, ", ")), nil
}

// releaseBindsOnLoop names the products whose catalogue's newest release binds
// processID, sorted; an order placed now would freeze that binding. Reads the
// catalogue store, so it runs on the run loop.
func (s *Server) releaseBindsOnLoop(processID string) ([]string, error) {
	bindings, err := s.releaseBindingsOnLoop(map[string]bool{processID: true})
	if err != nil {
		return nil, err
	}
	if bindings[processID] == nil {
		return []string{}, nil
	}
	return bindings[processID], nil
}

// onlyVersionOnLoop reports whether key is the only deployed version of processID.
func (s *Server) onlyVersionOnLoop(key uint64, processID string) bool {
	for k, d := range s.deployments {
		if k != key && d.ProcessID == processID {
			return false
		}
	}
	return true
}

// remainderLookup answers the fulfilment report's remainder from the order store
// (ADR-0427). It reads the store off the run loop; the report calls it after its own
// turn on the loop has ended.
type remainderLookup struct{ s *Server }

func (l remainderLookup) Remainder(items []catalog.Item) ([]catalog.Remainder, error) {
	if l.s.orderStore == nil {
		return nil, nil
	}
	orders, err := l.s.orderStore.All()
	if err != nil {
		return nil, err
	}
	var out []catalog.Remainder
	for _, it := range items {
		for _, fb := range order.OldBindings(orders, it) {
			out = append(out, catalog.Remainder{ItemID: fb.ItemID, Process: fb.Process, Lines: fb.Lines})
		}
	}
	return out, nil
}
