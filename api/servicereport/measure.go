// Package servicereport measures how the catalogue's delivery promises were kept
// (ADR-0515).
//
// It is pure: orders, the holds the inventory recorded and a clock go in, positions
// with a verdict and per-product summaries come out. Loading those inputs — off the
// run loop, scoped to what the reader may see — is the caller's job, so that the one
// place every rule of measurement lives can be tested without a store.
//
// Nothing here is persisted and nothing here is an engine fact. Every number is
// computed from what was already durable: the order's creation, the line's approval
// and the start of the right the inventory records (ADR-0312).
package servicereport

import (
	"time"

	"github.com/pblumer/atlas/api/order"
)

// Verdict is how one position stands against its promise.
type Verdict string

const (
	// Met: provisioned within the promised working days.
	Met Verdict = "met"
	// Missed: provisioned, later than promised.
	Missed Verdict = "missed"
	// Overdue: still open, and the promised time has passed.
	Overdue Verdict = "overdue"
	// Open: still open, within the promised time.
	Open Verdict = "open"
	// AwaitingApproval: an approval is still owed. The clock has not started,
	// because the customer's own organisation decides it.
	AwaitingApproval Verdict = "awaitingApproval"
	// Failed: provisioning failed and nothing was delivered.
	Failed Verdict = "failed"
	// NoPromise: the line was ordered from a release that promised nothing.
	NoPromise Verdict = "noPromise"
	// Unmeasured: delivered, but the inventory holds no right this order produced, so
	// when it began cannot be said. Reported rather than guessed.
	Unmeasured Verdict = "unmeasured"
)

// Hold is the part of an inventory record measurement needs: who holds what, which
// order produced it and when it began. It is filled from current holdings and, for
// a position given back since, from the holding's history.
type Hold struct {
	Principal string
	ItemID    string
	VariantID string
	OrderID   string
	// Since is when the right began, in unix nanoseconds.
	Since int64
}

// Position is one measured order line.
type Position struct {
	OrderID   string `json:"orderId"`
	ReleaseID string `json:"releaseId,omitempty"`
	ItemID    string `json:"itemId"`
	VariantID string `json:"variantId,omitempty"`
	// Recipient and Orderer are principal ids. A report that may show names resolves
	// them when it is rendered (ADR-0314); the product manager's report drops them.
	Recipient string `json:"recipient,omitempty"`
	Orderer   string `json:"orderer,omitempty"`
	Status    string `json:"status"`
	Price     string `json:"price,omitempty"`
	// ServiceLevel and DeliveryDays are the promise the line was ordered under.
	ServiceLevel string `json:"serviceLevel,omitempty"`
	DeliveryDays int    `json:"deliveryDays,omitempty"`
	// OrderedAt, ClockFrom and ProvisionedAt are unix nanoseconds; ProvisionedAt is
	// zero for a position not (measurably) provisioned.
	OrderedAt     int64 `json:"orderedAt"`
	ClockFrom     int64 `json:"clockFrom"`
	ProvisionedAt int64 `json:"provisionedAt,omitempty"`
	// LeadDays is the working days from ClockFrom to ProvisionedAt, or, for a position
	// still open, to now.
	LeadDays float64 `json:"leadDays"`
	// ApprovalWaitDays is the working days the approval took, reported apart from the
	// lead time it is not part of.
	ApprovalWaitDays float64 `json:"approvalWaitDays,omitempty"`
	Verdict          Verdict `json:"verdict"`
}

type holdKey struct{ principal, item, variant, order string }

// Measure answers one position per order line that delivered, or was meant to
// deliver, something. A line that was rejected, cancelled, abandoned or skipped is
// left out: nothing was delivered and nothing needed to be.
func Measure(orders []order.Order, holds []Hold, now time.Time, loc *time.Location) []Position {
	since := make(map[holdKey]int64, len(holds))
	for _, h := range holds {
		k := holdKey{h.Principal, h.ItemID, h.VariantID, h.OrderID}
		// Two records of one hold (current and history) agree on Since; keep the
		// earliest should they not, because that is when the right first began.
		if prev, ok := since[k]; !ok || h.Since < prev {
			since[k] = h.Since
		}
	}
	var out []Position
	for _, o := range orders {
		for _, l := range o.Lines {
			switch l.Status {
			case order.StatusRejected, order.StatusCancelled, order.StatusAbandoned, order.StatusSkipped:
				continue
			}
			p := Position{
				OrderID: o.ID, ReleaseID: o.ReleaseID, ItemID: l.ItemID, VariantID: l.VariantID,
				Recipient: o.Recipient, Orderer: o.Orderer, Status: string(l.Status), Price: l.Price,
				OrderedAt: o.CreatedAt, ClockFrom: o.CreatedAt,
			}
			if l.ServiceLevel != nil {
				p.ServiceLevel, p.DeliveryDays = l.ServiceLevel.Name, l.ServiceLevel.DeliveryDays
			}
			if l.ApprovedAt > o.CreatedAt {
				p.ClockFrom = l.ApprovedAt
				p.ApprovalWaitDays = WorkingDays(time.Unix(0, o.CreatedAt), time.Unix(0, l.ApprovedAt), loc)
			}
			p.Verdict = judge(&p, l, since[holdKey{o.Recipient, l.ItemID, l.VariantID, o.ID}], now, loc)
			out = append(out, p)
		}
	}
	return out
}

// judge sets the lead time and answers the verdict of one position.
func judge(p *Position, l order.Line, began int64, now time.Time, loc *time.Location) Verdict {
	from := time.Unix(0, p.ClockFrom)
	switch {
	case l.Status == order.StatusFailed:
		return Failed
	case l.Status.Held() || l.Status == order.StatusReturned:
		// Delivered. When, the inventory says; a line it holds no record for is
		// reported as delivered at an unknown moment rather than dated by a guess.
		if began == 0 {
			return Unmeasured
		}
		p.ProvisionedAt = began
		p.LeadDays = WorkingDays(from, time.Unix(0, began), loc)
		switch {
		case p.DeliveryDays == 0:
			return NoPromise
		case p.LeadDays <= float64(p.DeliveryDays):
			return Met
		}
		return Missed
	}
	// Still open: pending, running, blocked.
	if l.ApprovedAt == 0 && l.Approval.Kind != "" && l.Approval.Kind != "none" {
		return AwaitingApproval
	}
	p.LeadDays = WorkingDays(from, now, loc)
	switch {
	case p.DeliveryDays == 0:
		return NoPromise
	case p.LeadDays > float64(p.DeliveryDays):
		return Overdue
	}
	return Open
}

// WorkingDays answers how many working days lie between from and to: the time
// elapsed on Mondays to Fridays in loc, in days of 24 hours. Weekends do not count;
// public holidays do, until the ADR's open question is settled. Zero when to is not
// after from.
func WorkingDays(from, to time.Time, loc *time.Location) float64 {
	if !to.After(from) {
		return 0
	}
	if loc == nil {
		loc = time.UTC
	}
	var worked time.Duration
	cur := from.In(loc)
	end := to.In(loc)
	for cur.Before(end) {
		y, m, d := cur.Date()
		next := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
		seg := next
		if end.Before(seg) {
			seg = end
		}
		if wd := cur.Weekday(); wd != time.Saturday && wd != time.Sunday {
			worked += seg.Sub(cur)
		}
		cur = seg
	}
	return worked.Hours() / 24
}
