package servicereport

import (
	"bytes"
	"strings"
	"testing"
)

func TestPeopleListsEachPersonWithTheirRights(t *testing.T) {
	names := map[string]string{"ada": "Ada Muster", "bob": "Bob Beispiel", "eve": "Eve"}
	held := []Held{
		{Principal: "bob", ItemID: "vpn", OrderID: "o1", Origin: "ordered", Since: 10},
		{Principal: "bob", ItemID: "laptop", Origin: "adopted", Since: 5},
		{Principal: "ada", ItemID: "laptop", OrderID: "o2", Origin: "ordered", Since: 7, Until: 99},
		{Principal: "mallory", ItemID: "laptop", Since: 1}, // not asked for
	}
	ps := []Position{
		{Recipient: "ada", Verdict: Overdue}, {Recipient: "ada", Verdict: Met},
		{Recipient: "bob", Verdict: AwaitingApproval}, {Recipient: "bob", Verdict: Open},
		{Recipient: "mallory", Verdict: Open},
	}
	price := func(order, item, variant string) string {
		if order == "o1" && item == "vpn" {
			return "CHF 5.–"
		}
		return ""
	}
	got := People([]string{"bob", "eve", "ada"}, func(id string) string { return names[id] },
		held, map[string]string{"laptop": "Notebook"}, price, ps)
	if len(got) != 3 || got[0].ID != "ada" || got[1].ID != "bob" || got[2].ID != "eve" {
		t.Fatalf("people %+v, want ada, bob, eve by name", got)
	}
	if a := got[0]; a.Name != "Ada Muster" || len(a.Holdings) != 1 || a.Holdings[0].Name != "Notebook" ||
		a.Holdings[0].Until != 99 || a.Ordered != 2 || a.Overdue != 1 {
		t.Errorf("ada %+v", a)
	}
	if b := got[1]; len(b.Holdings) != 2 || b.Holdings[0].Name != "Notebook" || b.Holdings[1].Name != "vpn" ||
		b.Holdings[1].Price != "CHF 5.–" || b.AwaitingApproval != 1 || b.Open != 1 {
		t.Errorf("bob %+v", b)
	}
	if e := got[2]; e.Holdings == nil || len(e.Holdings) != 0 || e.Ordered != 0 {
		t.Errorf("somebody holding nothing is a row with an empty list: %+v", e)
	}

	var b bytes.Buffer
	if err := WriteHoldingsCSV(&b, got, zurich); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if lines[0] != PeopleCSVHeader || len(lines) != 4 || !strings.HasPrefix(lines[1], "Ada Muster;laptop;Notebook;") {
		t.Errorf("csv:\n%s", b.String())
	}
}
