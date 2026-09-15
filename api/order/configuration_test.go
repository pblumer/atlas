package order

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/limits"
)

// What a product needs that its name does not say
// (ADR-0358).
//
// A laptop is not fully described by being a laptop: somebody has to say which
// cost centre it is booked to and which site it goes to. None of that could be
// recorded, so every order that needed more than a product name finished as a
// phone call — and the answer, when it was given, lived in whatever the caller
// wrote down.
//
// The product declares one Atlas form; the line carries the answers and the id of
// the form they answered. The answers are what is frozen, and deliberately not the
// questions: a release freezes *rules*, because a rule relaxed next week must not
// change what somebody was held to this week. A form is not a rule. "Cost centre
// 4711" stays true whatever the form does afterwards.

// asking builds a release whose laptop asks a form and whose account does not.
func asking(t *testing.T) catalog.Release {
	t.Helper()
	ids := []string{"laptop", "account"}
	var items []catalog.Item
	for _, id := range ids {
		it := catalog.Item{
			ID: id, HomeCatalog: "cat", State: catalog.StateActive,
			Texts:            map[string]string{"de": id},
			Approval:         catalog.Approval{Kind: catalog.KindNone},
			ProvisionProcess: "prov", DeprovisionProcess: "deprov",
		}
		if id == "laptop" {
			it.ConfigForm = "form_laptop"
		}
		items = append(items, it)
	}
	rel, problems := catalog.Publish(catalog.Input{
		Catalogs: []catalog.Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids}},
		Items:    items,
	})
	if len(problems) != 0 {
		t.Fatalf("Publish: %v", problems)
	}
	rel.ID, rel.CatalogID, rel.CreatedAt = "rel_1", "cat", 1000
	return rel
}

// ordering builds a service over that release.
func ordering(t *testing.T) *Service {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	rel := asking(t)
	return New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		inAnyGroup, mayOrderForAnyone,
		func(message, orderID string, vars map[string]string) error { return nil },
		func() string { return "https://atlas.example.ch" },
		ignoreGrant, ignoreRevoke, holdsNothing)
}

func placed(t *testing.T, rec interface{ Bytes() []byte }) Order {
	t.Helper()
	var got Order
	if err := json.Unmarshal(rec.Bytes(), &got); err != nil {
		t.Fatalf("decode order: %v (%s)", err, rec.Bytes())
	}
	return got
}

// lineOf returns the order's line for one item.
func lineOf(t *testing.T, o Order, item string) Line {
	t.Helper()
	for _, l := range o.Lines {
		if l.ItemID == item {
			return l
		}
	}
	t.Fatalf("the order carries no line for %q: %+v", item, o.Lines)
	return Line{}
}

// TestTheAnswersTravelWithTheLineAndSoDoesTheFormTheyAnswered.
//
// Both, because either alone is useless: a set of answers with no form is a map of
// keys nobody can interpret, and a form id with no answers on a placed line says
// the question was never asked.
func TestTheAnswersTravelWithTheLineAndSoDoesTheFormTheyAnswered(t *testing.T) {
	rec := do(t, ordering(t).HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["laptop"],`+
			`"config":{"laptop":{"kostenstelle":"4711","standort":"Bern"}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("placing an order with details = %d, want 201 (%s)", rec.Code, rec.Body)
	}
	line := lineOf(t, placed(t, rec.Body), "laptop")
	if line.ConfigForm != "form_laptop" {
		t.Errorf("the line does not say which form was answered: %q", line.ConfigForm)
	}
	if line.Config["kostenstelle"] != "4711" || line.Config["standort"] != "Bern" {
		t.Errorf("the line carries %v, want both answers", line.Config)
	}
}

// TestTwoProductsInOneBasketKeepTheirOwnAnswers: the reason the answers are keyed
// by item rather than flat. Two laptops in one basket are two cost centres, and a
// flat map would silently keep one of them.
func TestTwoProductsInOneBasketKeepTheirOwnAnswers(t *testing.T) {
	rel := asking(t)
	// A second product that asks the same question. Found by id, because a release
	// sorts its items and the first is not the one this test means.
	var second catalog.Item
	for _, it := range rel.Items {
		if it.ID == "laptop" {
			second = it
			break
		}
	}
	second.ID = "laptop-2"
	rel.Items = append(rel.Items, second)

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		inAnyGroup, mayOrderForAnyone,
		func(message, orderID string, vars map[string]string) error { return nil },
		func() string { return "https://atlas.example.ch" },
		ignoreGrant, ignoreRevoke, holdsNothing)

	rec := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["laptop","laptop-2"],`+
			`"config":{"laptop":{"kostenstelle":"4711"},"laptop-2":{"kostenstelle":"0815"}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("placing = %d, want 201 (%s)", rec.Code, rec.Body)
	}
	o := placed(t, rec.Body)
	if got := lineOf(t, o, "laptop").Config["kostenstelle"]; got != "4711" {
		t.Errorf("the first laptop kept %q", got)
	}
	if got := lineOf(t, o, "laptop-2").Config["kostenstelle"]; got != "0815" {
		t.Errorf("the second laptop kept %q", got)
	}
}

// TestAnswersForSomethingNotOrderedAreRefused.
//
// Almost always a stale basket: the product was taken out and its answers were
// not. Dropping them silently would leave somebody certain they had given a cost
// centre that the order does not have.
func TestAnswersForSomethingNotOrderedAreRefused(t *testing.T) {
	rec := do(t, ordering(t).HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["laptop"],`+
			`"config":{"screen":{"kostenstelle":"4711"}}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("answers for a product not in the order = %d, want 400 (%s)", rec.Code, rec.Body)
	}
	// The message and not only the status: an item that is not in the basket is
	// also, trivially, an item that asks no questions, so the other refusal fires
	// for it too and says something misleading — "screen asks for no details" is
	// true of a product the order never carried and tells the reader nothing.
	if !strings.Contains(rec.Body.String(), "which is not in it") {
		t.Errorf("the refusal blames the wrong thing: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "screen") {
		t.Errorf("the refusal does not name what is stray: %s", rec.Body)
	}
}

// TestAnswersForAProductThatAsksNothingAreRefused: nothing would read them, so
// storing them would put data in the record that nothing can interpret.
func TestAnswersForAProductThatAsksNothingAreRefused(t *testing.T) {
	rec := do(t, ordering(t).HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"],`+
			`"config":{"account":{"kostenstelle":"4711"}}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("answers for a product that asks nothing = %d, want 400 (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "asks for no details") {
		t.Errorf("the refusal does not say why: %s", rec.Body)
	}
}

// TestAFormLeftUnansweredIsNotThisPackagesRefusal.
//
// Whether a field is required is the form's own statement, rendered by the form
// runtime. Re-deciding it here would be a second copy of a rule that already
// exists, and it would be wrong the first time somebody marks a field optional.
func TestAFormLeftUnansweredIsNotThisPackagesRefusal(t *testing.T) {
	rec := do(t, ordering(t).HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["laptop"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ordering a product whose form was not filled in = %d, want 201 (%s)",
			rec.Code, rec.Body)
	}
	line := lineOf(t, placed(t, rec.Body), "laptop")
	// The form still travels, so the order says what was asked even when nothing
	// was answered.
	if line.ConfigForm != "form_laptop" {
		t.Errorf("the line lost the form it should have asked: %q", line.ConfigForm)
	}
	if line.Config != nil {
		t.Errorf("the line invented answers: %v", line.Config)
	}
}

// TestMoreAnswersThanOneLineMayCarryAreRefused: the budget, which exists because
// this map arrives whole in a request body.
func TestMoreAnswersThanOneLineMayCarryAreRefused(t *testing.T) {
	s := ordering(t)
	s.Limits = limits.Default()
	s.Limits.OrderLineAnswers = 3

	answers := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		answers = append(answers, fmt.Sprintf("%q:%q", fmt.Sprintf("f%d", i), "x"))
	}
	rec := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["laptop"],"config":{"laptop":{`+
			strings.Join(answers, ",")+`}}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("four answers against a ceiling of three = %d, want 400 (%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "no more than 3") {
		t.Errorf("the refusal does not name the ceiling: %s", rec.Body)
	}
}

// TestTheOrderDoesNotShareTheRequestsMap: the answers are the order's own, so a
// caller cannot reach into a written record through the map it sent.
func TestTheOrderDoesNotShareTheRequestsMap(t *testing.T) {
	sent := map[string]string{"kostenstelle": "4711"}
	lines := linesFor(asking(t), []string{"laptop"}, nil,
		map[string]map[string]string{"laptop": sent})
	sent["kostenstelle"] = "geändert"
	if lines[0].Config["kostenstelle"] != "4711" {
		t.Error("the line shares the request's map, so editing it reaches into the order")
	}
}
