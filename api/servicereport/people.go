package servicereport

import (
	"io"
	"sort"
	"time"
)

// Held is one right a person holds now, as the inventory records it.
type Held struct {
	Principal string
	ItemID    string
	VariantID string
	OrderID   string
	// Origin is how the right came to be held: ordered, adopted, granted.
	Origin string
	// Since and Until are unix nanoseconds; Until is zero for a right with no end.
	Since int64
	Until int64
}

// HoldingRow is one right in a person's row.
type HoldingRow struct {
	ItemID    string `json:"itemId"`
	Name      string `json:"name"`
	VariantID string `json:"variantId,omitempty"`
	Origin    string `json:"origin,omitempty"`
	// Price is the figure the order line carried when the right was ordered, or
	// empty for a right no order of ours produced.
	Price string `json:"price,omitempty"`
	Since int64  `json:"since"`
	Until int64  `json:"until,omitempty"`
}

// PersonRow is one person in a report that may show names: what they hold now and
// how their positions in the period stand.
type PersonRow struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Holdings         []HoldingRow `json:"holdings"`
	Ordered          int          `json:"ordered"`
	Open             int          `json:"open"`
	Overdue          int          `json:"overdue"`
	AwaitingApproval int          `json:"awaitingApproval"`
}

// People answers one row per person in ids, sorted by name, with the rights each
// holds and the counts of their positions. nameOf resolves a principal to the name
// shown; items names a product; priceOf answers the price an order line carried.
func People(ids []string, nameOf func(string) string, held []Held, items map[string]string,
	priceOf func(orderID, itemID, variantID string) string, ps []Position) []PersonRow {

	rows := make(map[string]*PersonRow, len(ids))
	for _, id := range ids {
		rows[id] = &PersonRow{ID: id, Name: nameOf(id), Holdings: []HoldingRow{}}
	}
	for _, h := range held {
		r, ok := rows[h.Principal]
		if !ok {
			continue
		}
		name := items[h.ItemID]
		if name == "" {
			name = h.ItemID
		}
		price := ""
		if h.OrderID != "" && priceOf != nil {
			price = priceOf(h.OrderID, h.ItemID, h.VariantID)
		}
		r.Holdings = append(r.Holdings, HoldingRow{ItemID: h.ItemID, Name: name, VariantID: h.VariantID,
			Origin: h.Origin, Price: price, Since: h.Since, Until: h.Until})
	}
	for _, p := range ps {
		r, ok := rows[p.Recipient]
		if !ok {
			continue
		}
		r.Ordered++
		switch p.Verdict {
		case Open:
			r.Open++
		case Overdue:
			r.Overdue++
		case AwaitingApproval:
			r.AwaitingApproval++
		}
	}
	out := make([]PersonRow, 0, len(rows))
	for _, r := range rows {
		sort.Slice(r.Holdings, func(i, j int) bool {
			if r.Holdings[i].Name != r.Holdings[j].Name {
				return r.Holdings[i].Name < r.Holdings[j].Name
			}
			return r.Holdings[i].VariantID < r.Holdings[j].VariantID
		})
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// WriteHoldingsCSV writes one line per right a person holds, by name.
func WriteHoldingsCSV(w io.Writer, people []PersonRow, loc *time.Location) error {
	c := newCSV(w)
	_ = c.Write([]string{"person", "item", "name", "variant", "origin", "price", "since", "until"})
	day := func(ns int64) string {
		if ns == 0 {
			return ""
		}
		return time.Unix(0, ns).In(loc).Format("2006-01-02")
	}
	for _, p := range people {
		for _, h := range p.Holdings {
			_ = c.Write([]string{p.Name, h.ItemID, h.Name, h.VariantID, h.Origin, h.Price, day(h.Since), day(h.Until)})
		}
	}
	c.Flush()
	return c.Error()
}

// PeopleCSVHeader is the first line of WriteHoldingsCSV, exposed for callers that
// check a download without parsing it.
var PeopleCSVHeader = "person;item;name;variant;origin;price;since;until"
