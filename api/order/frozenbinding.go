package order

import (
	"sort"

	"github.com/pblumer/atlas/api/catalog"
)

// What a frozen binding keeps alive (ADR-0427).
//
// A line freezes its product's processes when it is placed, so converting the product
// to a lifecycle process does not reach the lines already placed: they still start
// the old processes. Which of them a line can still start depends on its status —
// traced through this package in the record rather than assumed:
//
//   - provisioning: only a line that has not started (pending, blocked);
//   - deprovisioning: every line that holds the right or may yet come to (pending,
//     blocked, running, failed — a repaired instance can still report done — done,
//     returnFailed).
//
// returning is left out of both: its revocation is running, and a definition with a
// running instance is protected by the engine already.

// mayStartProvision is where a line can still start its provisioning process.
func (s LineStatus) mayStartProvision() bool {
	return s == StatusPending || s == StatusBlocked
}

// mayStartDeprovision is where a line can still start its deprovisioning process.
func (s LineStatus) mayStartDeprovision() bool {
	switch s {
	case StatusPending, StatusBlocked, StatusRunning, StatusFailed, StatusDone, StatusReturnFailed:
		return true
	}
	return false
}

// StillStarts reports whether this line can still start the process with this id,
// through either binding form.
func (l Line) StillStarts(processID string) bool {
	if processID == "" {
		return false
	}
	switch {
	case l.LifecycleProcess == processID:
		return l.Status.mayStartProvision() || l.Status.mayStartDeprovision()
	case l.ProvisionProcess == processID && l.Status.mayStartProvision():
		return true
	case l.DeprovisionProcess == processID && l.Status.mayStartDeprovision():
		return true
	}
	return false
}

// FrozenBinding is how many lines of one product still start one process.
type FrozenBinding struct {
	ItemID  string `json:"itemId"`
	Process string `json:"process"`
	Lines   int    `json:"lines"`
}

// LinesStarting counts, per product, the lines of these orders that can still start
// processID. A count and not a list, for the reason ADR-0353 gives: what an operator
// needs is how much still depends on the process, not who.
func LinesStarting(orders []Order, processID string) []FrozenBinding {
	counts := map[string]int{}
	for _, o := range orders {
		for _, l := range o.Lines {
			if l.StillStarts(processID) {
				counts[l.ItemID]++
			}
		}
	}
	out := make([]FrozenBinding, 0, len(counts))
	for item, n := range counts {
		out = append(out, FrozenBinding{ItemID: item, Process: processID, Lines: n})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ItemID < out[b].ItemID })
	return out
}

// OldBindings counts, for one product, the lines still bound to processes the
// product no longer binds — the remainder a conversion leaves behind (ADR-0427).
func OldBindings(orders []Order, it catalog.Item) []FrozenBinding {
	current := map[string]bool{}
	for _, op := range []string{catalog.OpProvision, catalog.OpDeprovision, catalog.OpChange} {
		if b := it.BindingFor(op); b.Bound() {
			current[b.Process] = true
		}
	}
	counts := map[string]int{}
	for _, o := range orders {
		for _, l := range o.Lines {
			if l.ItemID != it.ID {
				continue
			}
			for _, p := range []string{l.ProvisionProcess, l.DeprovisionProcess, l.LifecycleProcess} {
				if p != "" && !current[p] && l.StillStarts(p) {
					counts[p]++
				}
			}
		}
	}
	out := make([]FrozenBinding, 0, len(counts))
	for p, n := range counts {
		out = append(out, FrozenBinding{ItemID: it.ID, Process: p, Lines: n})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Process < out[b].Process })
	return out
}

// Rebinding records that a line was moved from the binding it froze to its product's
// current lifecycle binding (ADR-0427). The line keeps saying what it was granted
// under, and that this was changed afterwards, by whom and why — the same shape as a
// recorded correction of its details (ADR-0359).
type Rebinding struct {
	FromProvision   string `json:"fromProvision,omitempty"`
	FromDeprovision string `json:"fromDeprovision,omitempty"`
	ToLifecycle     string `json:"toLifecycle"`
	By              string `json:"by,omitempty"`
	At              int64  `json:"at"`
	Reason          string `json:"reason,omitempty"`
}

// Rebind moves every line of one product that can still start one of its old
// processes onto the product's current lifecycle binding, and returns the order and
// how many lines moved. A line whose process is running now — running, returning —
// is left where it is: a process has it.
func Rebind(o Order, it catalog.Item, by, reason string, at int64) (Order, int) {
	if !it.UsesLifecycleProcess() {
		return o, 0
	}
	moved := 0
	lines := append([]Line(nil), o.Lines...)
	for i, l := range lines {
		if l.ItemID != it.ID || l.LifecycleProcess != "" {
			continue
		}
		if l.Status == StatusRunning || l.Status == StatusReturning {
			continue
		}
		if !l.StillStarts(l.ProvisionProcess) && !l.StillStarts(l.DeprovisionProcess) {
			continue
		}
		l.Rebindings = append(append([]Rebinding(nil), l.Rebindings...), Rebinding{
			FromProvision: l.ProvisionProcess, FromDeprovision: l.DeprovisionProcess,
			ToLifecycle: it.LifecycleProcess, By: by, At: at, Reason: reason,
		})
		l.ProvisionProcess, l.DeprovisionProcess = "", ""
		l.LifecycleProcess = it.LifecycleProcess
		l.Operations = make(map[string]string, len(it.Operations))
		for k, v := range it.Operations {
			l.Operations[k] = v
		}
		lines[i] = l
		moved++
	}
	if moved > 0 {
		o.Lines = lines
		o.UpdatedAt = at
	}
	return o, moved
}
