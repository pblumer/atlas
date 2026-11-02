package servicereport

import (
	"bytes"
	"strings"
	"testing"
)

func pos(item string, v Verdict, lead float64) Position {
	p := Position{ItemID: item, Verdict: v, LeadDays: lead, ServiceLevel: "Standard", DeliveryDays: 3}
	if v == Met || v == Missed || v == NoPromise {
		p.ProvisionedAt = 1 // delivered, at a known moment
	}
	return p
}

func TestSummariseCountsPerProduct(t *testing.T) {
	ps := []Position{
		pos("laptop", Met, 1), pos("laptop", Met, 2), pos("laptop", Missed, 5), pos("laptop", Overdue, 4),
		pos("laptop", Open, 1), pos("laptop", AwaitingApproval, 0), pos("laptop", Failed, 0),
		pos("screen", NoPromise, 2), pos("screen", Unmeasured, 0),
	}
	ps[1].ApprovalWaitDays = 1
	rows := Summarise(ps, map[string]string{"laptop": "Notebook"}, map[string]int{"laptop": 40, "phone": 7})
	if len(rows) != 3 {
		t.Fatalf("rows %+v, want laptop, phone and screen", rows)
	}
	l := rows[0]
	if l.ItemID != "laptop" || l.Name != "Notebook" || l.Ordered != 7 || l.Delivered != 3 ||
		l.Met != 1+1 || l.Missed != 1 || l.Overdue != 1 || l.Open != 1 || l.AwaitingApproval != 1 ||
		l.Failed != 1 || l.Held != 40 || l.ServiceLevel != "Standard" || l.DeliveryDays != 3 {
		t.Errorf("laptop row %+v", l)
	}
	if l.MetShare == nil || !near(*l.MetShare, 2.0/4) {
		t.Errorf("met share %v, want met over met+missed+overdue = 0.5", l.MetShare)
	}
	if l.LeadMedianDays == nil || !near(*l.LeadMedianDays, 2) || l.LeadP90Days == nil || !near(*l.LeadP90Days, 5) {
		t.Errorf("lead median/p90 %v/%v, want 2/5 over the delivered positions", l.LeadMedianDays, l.LeadP90Days)
	}
	if l.ApprovalWaitMedianDays == nil || !near(*l.ApprovalWaitMedianDays, 1) {
		t.Errorf("approval wait median %v, want 1 over the positions that waited", l.ApprovalWaitMedianDays)
	}
	if p := rows[1]; p.ItemID != "phone" || p.Ordered != 0 || p.Held != 7 || p.MetShare != nil || p.LeadMedianDays != nil {
		t.Errorf("a product only held is a row with nothing measured: %+v", p)
	}
	if s := rows[2]; s.ItemID != "screen" || s.Name != "screen" || s.MetShare != nil || s.LeadMedianDays == nil {
		t.Errorf("screen row %+v: no promise means no share, but a lead time", s)
	}
	tot := Total(rows)
	if tot.Ordered != 9 || tot.Met != 2 || tot.Held != 47 || tot.MetShare == nil || !near(*tot.MetShare, 0.5) {
		t.Errorf("totals %+v", tot)
	}
}

func TestPercentile(t *testing.T) {
	if percentile(nil, 0.5) != nil {
		t.Error("no values have no median")
	}
	got := percentile([]float64{4, 1, 3, 2}, 0.5)
	if got == nil || !near(*got, 2) {
		t.Errorf("median %v, want 2 (nearest rank)", got)
	}
}

func TestCSVWritesOneRowPerProduct(t *testing.T) {
	share := 0.5
	var b bytes.Buffer
	if err := WriteProductsCSV(&b, []ProductRow{{ItemID: "laptop", Name: "Note;book", Ordered: 2, MetShare: &share}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "item;name;") || !strings.Contains(lines[1], `"Note;book"`) ||
		!strings.Contains(lines[1], ";0.50;") {
		t.Errorf("csv:\n%s", b.String())
	}
}

func TestCSVOfPositionsCarriesTheNames(t *testing.T) {
	var b bytes.Buffer
	ps := []Position{{OrderID: "o1", ItemID: "laptop", Recipient: "ada", Orderer: "bob", Verdict: Met, LeadDays: 1.25,
		OrderedAt: at(2026, 10, 26, 9), DeliveryDays: 3, ServiceLevel: "Standard", Price: "CHF 9.–"}}
	err := WritePositionsCSV(&b, ps, func(id string) string { return map[string]string{"ada": "Ada Muster", "bob": "Bob"}[id] },
		map[string]string{"laptop": "Notebook"}, zurich)
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"Ada Muster", "Bob", "Notebook", "met", "1.25", "2026-10-26", "CHF 9.–"} {
		if !strings.Contains(out, want) {
			t.Errorf("csv lacks %q:\n%s", want, out)
		}
	}
}
