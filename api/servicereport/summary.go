package servicereport

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"time"
)

// ProductRow is one product's line in a report: what was ordered, how it was
// delivered against its promise and how many hold it now. It carries no person.
type ProductRow struct {
	ItemID       string `json:"itemId"`
	Name         string `json:"name"`
	ServiceLevel string `json:"serviceLevel,omitempty"`
	DeliveryDays int    `json:"deliveryDays,omitempty"`
	Ordered      int    `json:"ordered"`
	// Delivered counts the positions provisioned, measurably or not.
	Delivered        int `json:"delivered"`
	Met              int `json:"met"`
	Missed           int `json:"missed"`
	Overdue          int `json:"overdue"`
	Open             int `json:"open"`
	AwaitingApproval int `json:"awaitingApproval"`
	Failed           int `json:"failed"`
	// Held is how many rights to this product are held now, whoever ordered them.
	Held int `json:"held"`
	// MetShare is met over met, missed and overdue: the positions a promise was due
	// for. Nil where none was.
	MetShare *float64 `json:"metShare,omitempty"`
	// LeadMedianDays and LeadP90Days are over the measurably delivered positions.
	LeadMedianDays *float64 `json:"leadMedianDays,omitempty"`
	LeadP90Days    *float64 `json:"leadP90Days,omitempty"`
	// ApprovalWaitMedianDays is over the positions that waited for an approval.
	ApprovalWaitMedianDays *float64 `json:"approvalWaitMedianDays,omitempty"`
}

// Summarise answers one row per product that was ordered or is held, sorted by
// product id. names maps a product id to the name to show; held counts current
// rights per product.
func Summarise(ps []Position, names map[string]string, held map[string]int) []ProductRow {
	rows := map[string]*ProductRow{}
	leads := map[string][]float64{}
	waits := map[string][]float64{}
	row := func(id string) *ProductRow {
		r, ok := rows[id]
		if !ok {
			name := names[id]
			if name == "" {
				name = id
			}
			r = &ProductRow{ItemID: id, Name: name}
			rows[id] = r
		}
		return r
	}
	for _, p := range ps {
		r := row(p.ItemID)
		r.Ordered++
		if p.ServiceLevel != "" && r.ServiceLevel == "" {
			r.ServiceLevel, r.DeliveryDays = p.ServiceLevel, p.DeliveryDays
		}
		switch p.Verdict {
		case Met:
			r.Met++
		case Missed:
			r.Missed++
		case Overdue:
			r.Overdue++
		case Open:
			r.Open++
		case AwaitingApproval:
			r.AwaitingApproval++
		case Failed:
			r.Failed++
		}
		if p.ProvisionedAt != 0 || p.Verdict == Unmeasured {
			r.Delivered++
		}
		if p.ProvisionedAt != 0 {
			leads[p.ItemID] = append(leads[p.ItemID], p.LeadDays)
		}
		if p.ApprovalWaitDays > 0 {
			waits[p.ItemID] = append(waits[p.ItemID], p.ApprovalWaitDays)
		}
	}
	for id, n := range held {
		row(id).Held = n
	}
	out := make([]ProductRow, 0, len(rows))
	for id, r := range rows {
		r.MetShare = share(r.Met, r.Met+r.Missed+r.Overdue)
		r.LeadMedianDays = percentile(leads[id], 0.5)
		r.LeadP90Days = percentile(leads[id], 0.9)
		r.ApprovalWaitMedianDays = percentile(waits[id], 0.5)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ItemID < out[j].ItemID })
	return out
}

// Totals are the rows added up. Lead times are not: a median of medians is not a
// median, and the rows say them per product.
type Totals struct {
	Ordered          int      `json:"ordered"`
	Delivered        int      `json:"delivered"`
	Met              int      `json:"met"`
	Missed           int      `json:"missed"`
	Overdue          int      `json:"overdue"`
	Open             int      `json:"open"`
	AwaitingApproval int      `json:"awaitingApproval"`
	Failed           int      `json:"failed"`
	Held             int      `json:"held"`
	MetShare         *float64 `json:"metShare,omitempty"`
}

// Total adds the rows up.
func Total(rows []ProductRow) Totals {
	var t Totals
	for _, r := range rows {
		t.Ordered += r.Ordered
		t.Delivered += r.Delivered
		t.Met += r.Met
		t.Missed += r.Missed
		t.Overdue += r.Overdue
		t.Open += r.Open
		t.AwaitingApproval += r.AwaitingApproval
		t.Failed += r.Failed
		t.Held += r.Held
	}
	t.MetShare = share(t.Met, t.Met+t.Missed+t.Overdue)
	return t
}

func share(n, of int) *float64 {
	if of == 0 {
		return nil
	}
	v := float64(n) / float64(of)
	return &v
}

// percentile answers the nearest-rank percentile of vs, or nil for none.
func percentile(vs []float64, q float64) *float64 {
	if len(vs) == 0 {
		return nil
	}
	s := append([]float64(nil), vs...)
	sort.Float64s(s)
	rank := int(math.Ceil(q*float64(len(s)))) - 1
	rank = max(0, min(rank, len(s)-1))
	v := s[rank]
	return &v
}

func num(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 2, 64)
}

// newCSV answers a writer in the shape a spreadsheet in a German-speaking locale
// opens without an import dialog: semicolon-separated.
func newCSV(w io.Writer) *csv.Writer {
	c := csv.NewWriter(w)
	c.Comma = ';'
	return c
}

// WriteProductsCSV writes the product rows, one per line, with a header.
func WriteProductsCSV(w io.Writer, rows []ProductRow) error {
	c := newCSV(w)
	_ = c.Write([]string{"item", "name", "serviceLevel", "deliveryDays", "ordered", "delivered", "met",
		"missed", "overdue", "open", "awaitingApproval", "failed", "held", "metShare",
		"leadMedianDays", "leadP90Days", "approvalWaitMedianDays"})
	for _, r := range rows {
		_ = c.Write([]string{r.ItemID, r.Name, r.ServiceLevel, strconv.Itoa(r.DeliveryDays),
			strconv.Itoa(r.Ordered), strconv.Itoa(r.Delivered), strconv.Itoa(r.Met), strconv.Itoa(r.Missed),
			strconv.Itoa(r.Overdue), strconv.Itoa(r.Open), strconv.Itoa(r.AwaitingApproval),
			strconv.Itoa(r.Failed), strconv.Itoa(r.Held), num(r.MetShare), num(r.LeadMedianDays),
			num(r.LeadP90Days), num(r.ApprovalWaitMedianDays)})
	}
	c.Flush()
	return c.Error()
}

// WritePositionsCSV writes one line per position, with the people it concerns by
// name. Only a report permitted to show names calls it
// (ADR-0515 §3).
func WritePositionsCSV(w io.Writer, ps []Position, nameOf func(principal string) string,
	items map[string]string, loc *time.Location) error {
	c := newCSV(w)
	_ = c.Write([]string{"order", "orderedAt", "recipient", "orderer", "item", "name", "variant", "status",
		"price", "serviceLevel", "deliveryDays", "provisionedAt", "leadDays", "approvalWaitDays", "verdict"})
	day := func(ns int64) string {
		if ns == 0 {
			return ""
		}
		return time.Unix(0, ns).In(loc).Format("2006-01-02")
	}
	for _, p := range ps {
		name := items[p.ItemID]
		if name == "" {
			name = p.ItemID
		}
		_ = c.Write([]string{p.OrderID, day(p.OrderedAt), nameOf(p.Recipient), nameOf(p.Orderer), p.ItemID,
			name, p.VariantID, p.Status, p.Price, p.ServiceLevel, strconv.Itoa(p.DeliveryDays),
			day(p.ProvisionedAt), fmt.Sprintf("%.2f", p.LeadDays), fmt.Sprintf("%.2f", p.ApprovalWaitDays),
			string(p.Verdict)})
	}
	c.Flush()
	return c.Error()
}
