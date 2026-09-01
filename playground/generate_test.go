package playground_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// runStart is the simulated instant the generated timestamps are measured from.
var runStart = time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC)

// "300 cases with a random amount" is the whole point of describing a dataset
// rather than listing one: nobody types three hundred amounts, and nobody reviews
// a pull request that contains them.
func TestADatasetIsDescribedRatherThanListed(t *testing.T) {
	d := playground.Dataset{
		Count: 300,
		Fields: []playground.Field{
			{Name: "amount", Kind: playground.FieldInt, Min: 100, Max: 5000},
		},
	}
	rows, err := d.Generate(4711, runStart)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(rows) != 300 {
		t.Fatalf("rows = %d, want 300", len(rows))
	}
	seen := map[string]int{}
	for i, row := range rows {
		if len(row) != 1 {
			t.Fatalf("case %d carries %d variables, want only the one described", i, len(row))
		}
		n, ok := row["amount"].(json.Number)
		if !ok {
			t.Fatalf("case %d's amount is %T, want a JSON number so it reaches FEEL as one", i, row["amount"])
		}
		v, err := n.Int64()
		if err != nil {
			t.Fatalf("case %d's amount %q is not whole: %v", i, n, err)
		}
		if v < 100 || v > 5000 {
			t.Errorf("case %d's amount is %d, outside the 100..5000 asked for", i, v)
		}
		seen[n.String()]++
	}
	// A "random amount" that came out the same three hundred times would satisfy
	// every bound above and be useless.
	if len(seen) < 100 {
		t.Errorf("300 cases produced only %d distinct amounts", len(seen))
	}
}

// The same description and the same seed produce the same dataset. Without that a
// scenario cannot be re-run, a baseline cannot be compared against, and a figure
// quoted in a review cannot be checked.
func TestTheSameSeedProducesTheSameDataset(t *testing.T) {
	d := playground.Dataset{
		Count: 50,
		Fields: []playground.Field{
			{Name: "amount", Kind: playground.FieldInt, Min: 1, Max: 1_000_000},
			{Name: "tier", Kind: playground.FieldChoice, Choices: []playground.Choice{
				{Value: "gold"}, {Value: "silver"}, {Value: "standard"},
			}},
			{Name: "express", Kind: playground.FieldBool, PercentTrue: 30},
		},
	}
	first, err := d.Generate(99, runStart)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	again, err := d.Generate(99, runStart)
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}
	if !equalRows(first, again) {
		t.Fatal("the same seed produced a different dataset")
	}
	other, err := d.Generate(100, runStart)
	if err != nil {
		t.Fatalf("generate with another seed: %v", err)
	}
	if equalRows(first, other) {
		t.Error("two seeds produced the same dataset; the seed is not reaching the draws")
	}
}

// A field's value must not depend on what the other fields are doing. Two fields
// sharing a draw would make "amount" and "tier" move together, which reads as a
// pattern in the results that is not in the model.
func TestTwoFieldsDrawIndependently(t *testing.T) {
	d := playground.Dataset{
		Count: 200,
		Fields: []playground.Field{
			{Name: "a", Kind: playground.FieldInt, Min: 0, Max: 9},
			{Name: "b", Kind: playground.FieldInt, Min: 0, Max: 9},
		},
	}
	rows, err := d.Generate(7, runStart)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	same := 0
	for _, row := range rows {
		if row["a"] == row["b"] {
			same++
		}
	}
	// A tenth of 200 is 20; identical draws would give 200.
	if same > 60 {
		t.Errorf("two fields agreed in %d of 200 cases; they are drawing from the same number", same)
	}
}

// Each kind writes the shape the engine's start path takes: numbers as JSON
// numbers, so FEEL sees arithmetic rather than text, and everything else as
// itself.
func TestEachKindWritesTheValueTheEngineExpects(t *testing.T) {
	d := playground.Dataset{
		Count: 12,
		Fields: []playground.Field{
			{Name: "count", Kind: playground.FieldInt, Min: 3, Max: 3},
			{Name: "price", Kind: playground.FieldDecimal, Min: 10, Max: 99.99, Decimals: 2},
			{Name: "express", Kind: playground.FieldBool, PercentTrue: 100},
			{Name: "region", Kind: playground.FieldConstant, Value: "EU"},
			{Name: "ref", Kind: playground.FieldSequence, Prefix: "ORDER-"},
			{Name: "due", Kind: playground.FieldTimestamp, MinOffset: 24 * time.Hour, MaxOffset: 48 * time.Hour},
			{Name: "day", Kind: playground.FieldTimestamp, OnlyDate: true},
		},
	}
	rows, err := d.Generate(3, runStart)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for i, row := range rows {
		if got := row["count"]; got != json.Number("3") {
			t.Errorf("case %d: count = %#v, want the single value the bounds allow", i, got)
		}
		price, ok := row["price"].(json.Number)
		if !ok || len(price.String())-strings.IndexByte(price.String(), '.') != 3 {
			t.Errorf("case %d: price = %#v, want a number written to two places", i, row["price"])
		}
		if got := row["express"]; got != true {
			t.Errorf("case %d: express = %#v, want the certainty 100%% asked for", i, got)
		}
		if got := row["region"]; got != "EU" {
			t.Errorf("case %d: region = %#v, want the constant", i, got)
		}
		if got, want := row["ref"], "ORDER-"+[]string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12"}[i]; got != want {
			t.Errorf("case %d: ref = %#v, want %q", i, got, want)
		}
		at, err := time.Parse(time.RFC3339, row["due"].(string))
		if err != nil {
			t.Fatalf("case %d: due = %#v, want an instant: %v", i, row["due"], err)
		}
		if at.Before(runStart.Add(24*time.Hour)) || at.After(runStart.Add(48*time.Hour)) {
			t.Errorf("case %d: due = %s, outside the day-to-two-days window", i, at)
		}
		if got := row["day"]; got != "2026-03-17" {
			t.Errorf("case %d: day = %#v, want the run's own date", i, got)
		}
	}
}

// A sequence numbers the cases so the results table can be read back against the
// dataset that produced it, padded to the width of the whole run rather than of
// the page in hand.
func TestASequenceIsUniqueAndPaddedToTheWholeRun(t *testing.T) {
	d := playground.Dataset{
		Count:  120,
		Fields: []playground.Field{{Name: "ref", Kind: playground.FieldSequence, Prefix: "A-"}},
	}
	rows, err := d.Generate(1, runStart)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	seen := map[any]bool{}
	for _, row := range rows {
		if seen[row["ref"]] {
			t.Fatalf("%v was handed out twice", row["ref"])
		}
		seen[row["ref"]] = true
	}
	if rows[0]["ref"] != "A-001" || rows[119]["ref"] != "A-120" {
		t.Errorf("first and last are %v and %v, want A-001 and A-120", rows[0]["ref"], rows[119]["ref"])
	}
}

// Weights are how a dataset says "most orders are standard". Without them a
// three-way choice exercises the rare branch as often as the common one, which is
// a load profile nobody has.
func TestWeightsDecideHowOftenAnOptionComesUp(t *testing.T) {
	d := playground.Dataset{
		Count: 1000,
		Fields: []playground.Field{{Name: "tier", Kind: playground.FieldChoice, Choices: []playground.Choice{
			{Value: "gold", Weight: 1},
			{Value: "silver", Weight: 3},
			{Value: "standard", Weight: 6},
		}}},
	}
	rows, err := d.Generate(23, runStart)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := map[any]int{}
	for _, row := range rows {
		got[row["tier"]]++
	}
	for _, tc := range []struct {
		value    string
		low, top int
	}{{"gold", 50, 150}, {"silver", 250, 350}, {"standard", 550, 650}} {
		if n := got[tc.value]; n < tc.low || n > tc.top {
			t.Errorf("%s came up %d times in 1000, want between %d and %d", tc.value, n, tc.low, tc.top)
		}
	}

	// An option weighted zero among weighted ones is switched off rather than
	// dropped: it stays in the list, ready to be turned back on.
	off := playground.Dataset{
		Count: 200,
		Fields: []playground.Field{{Name: "tier", Kind: playground.FieldChoice, Choices: []playground.Choice{
			{Value: "on", Weight: 5}, {Value: "off", Weight: 0},
		}}},
	}
	rows, err = off.Generate(5, runStart)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for i, row := range rows {
		if row["tier"] != "on" {
			t.Fatalf("case %d drew the option weighted zero", i)
		}
	}

	// No weights at all is not a refusal: the options are equally likely.
	even := playground.Dataset{
		Count: 400,
		Fields: []playground.Field{{Name: "tier", Kind: playground.FieldChoice, Choices: []playground.Choice{
			{Value: "a"}, {Value: "b"},
		}}},
	}
	rows, err = even.Generate(11, runStart)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	as := 0
	for _, row := range rows {
		if row["tier"] == "a" {
			as++
		}
	}
	if as < 150 || as > 250 {
		t.Errorf("unweighted options split %d/%d, want roughly even", as, 400-as)
	}
}

// The preview is the run: a panel that showed rows the run would not carry would
// be worse than showing nothing.
func TestAPreviewIsTheFirstRowsOfTheRunItself(t *testing.T) {
	d := playground.Dataset{
		Count: 5000,
		Fields: []playground.Field{
			{Name: "amount", Kind: playground.FieldDecimal, Min: 1, Max: 999, Decimals: 2},
			{Name: "ref", Kind: playground.FieldSequence, Prefix: "R-"},
		},
	}
	preview, err := d.Preview(88, runStart, 5)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview) != 5 {
		t.Fatalf("preview has %d rows, want 5", len(preview))
	}
	full, err := d.Generate(88, runStart)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !equalRows(preview, full[:5]) {
		t.Errorf("the preview showed something the run would not carry:\n%v\n%v", preview, full[:5])
	}
	// Padded to the run's width, not the preview's.
	if preview[0]["ref"] != "R-0001" {
		t.Errorf("ref = %v, want it numbered for a run of 5000", preview[0]["ref"])
	}

	// Asking for more than there are, or for a negative number, is answered rather
	// than refused: it is a page size, not an assertion.
	short, err := d.Preview(88, runStart, 0)
	if err != nil || len(short) != 0 {
		t.Errorf("preview of none = %d rows, %v", len(short), err)
	}
	if rows, err := d.Preview(88, runStart, -3); err != nil || len(rows) != 0 {
		t.Errorf("preview of minus three = %d rows, %v", len(rows), err)
	}
	small := playground.Dataset{Count: 2, Fields: d.Fields}
	if rows, err := small.Preview(88, runStart, 50); err != nil || len(rows) != 2 {
		t.Errorf("preview of more than there are = %d rows, %v; want the two that exist", len(rows), err)
	}
}

// A dataset with no fields is a run of N cases with no input — a load test of the
// model's own shape. Refusing it would be arbitrary.
func TestADatasetMayDescribeNoFieldsAtAll(t *testing.T) {
	rows, err := playground.Dataset{Count: 3}.Generate(1, runStart)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	for i, row := range rows {
		if len(row) != 0 {
			t.Errorf("case %d carries %v, want nothing", i, row)
		}
	}
}

// A description that cannot produce what it says is refused where it was typed.
// The alternative is a scenario that fails in CI at three in the morning, or
// worse, one that quietly runs something else.
func TestADescriptionThatCannotBeDrawnFromIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    playground.Dataset
		says string
	}{
		{"no cases", playground.Dataset{Count: 0}, "at least one case"},
		{"a negative count", playground.Dataset{Count: -5}, "at least one case"},
		{"a field with no name", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Kind: playground.FieldInt},
		}}, "no name"},
		{"the same name twice", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldInt}, {Name: "n", Kind: playground.FieldBool},
		}}, "described twice"},
		{"a kind that does not exist", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: "lorem"},
		}}, "not one of"},
		{"a maximum below the minimum", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldInt, Min: 10, Max: 5},
		}}, "below its minimum"},
		{"bounds that are not whole", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldInt, Min: 1.5, Max: 9},
		}}, "have to be whole"},
		{"a range too wide to draw exactly", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldInt, Min: 0, Max: 1e17},
		}}, "cannot be drawn exactly"},
		// A narrow range around a huge number: the span is fine, the bounds are not.
		// This one used to pass every check and then convert to an int64 Go does not
		// define, so the case ran with a number nobody asked for.
		{"a bound past what a number holds", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldInt, Min: 1e300, Max: 1e300},
		}}, "cannot be drawn exactly"},
		{"more decimals than a number carries", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldDecimal, Min: 0, Max: 1, Decimals: 9},
		}}, "decimal places"},
		{"negative decimals", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldDecimal, Min: 0, Max: 1, Decimals: -1},
		}}, "decimal places"},
		{"decimals over a range too wide for them", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldDecimal, Min: 0, Max: 1e12, Decimals: 6},
		}}, "cannot be drawn exactly"},
		{"a probability past certainty", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldBool, PercentTrue: 101},
		}}, "0 to 100"},
		{"a negative probability", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldBool, PercentTrue: -1},
		}}, "0 to 100"},
		{"a choice between nothing", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldChoice},
		}}, "no options"},
		{"a negative weight", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldChoice, Choices: []playground.Choice{{Value: "a", Weight: -2}}},
		}}, "cannot be negative"},
		{"a window that ends before it begins", playground.Dataset{Count: 1, Fields: []playground.Field{
			{Name: "n", Kind: playground.FieldTimestamp, MinOffset: time.Hour, MaxOffset: -time.Hour},
		}}, "ends before it begins"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.d.Validate()
			if err == nil {
				t.Fatal("a description that cannot be drawn from was accepted")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %q, want it to say %q", err, tc.says)
			}
			// Generating has to refuse it too: a caller that skips Validate must not
			// get a dataset that quietly means something else.
			if _, err := tc.d.Generate(1, runStart); err == nil {
				t.Error("Generate accepted what Validate refused")
			}
			if _, err := tc.d.Preview(1, runStart, 2); err == nil {
				t.Error("Preview accepted what Validate refused")
			}
		})
	}
}

// equalRows compares two datasets by value.
func equalRows(a, b []map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for k, v := range a[i] {
			if b[i][k] != v {
				return false
			}
		}
	}
	return true
}

// A described dataset has to drive the model, not just fill a table: the point of
// "a random amount" is that some of the amounts take the other branch.
func TestAGeneratedDatasetDrivesTheModelsBranches(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	// The sandbox publishes the two numbers a dataset is measured from, so what the
	// panel previews and what the run carries come from the same place.
	if got := sb.Seed(); got != 1 {
		t.Errorf("seed = %d, want the 1 the sandbox was opened with", got)
	}
	if got := sb.StartedAt(); !got.Equal(simStart) {
		t.Errorf("started at %s, want %s", got, simStart)
	}

	d := playground.Dataset{
		Count:  100,
		Fields: []playground.Field{{Name: "amount", Kind: playground.FieldInt, Min: 0, Max: 2000}},
	}
	generated, err := d.Generate(sb.Seed(), sb.StartedAt())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	runPlan(t, sb, playground.Plan{Cases: asVariables(t, generated)})

	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Cases != 100 || rep.Completed != 100 {
		t.Fatalf("report = %d cases, %d completed; want 100 of each", rep.Cases, rep.Completed)
	}
	// Amounts spread over 0..2000 against a threshold at 1000: both branches run,
	// which is exactly what a list of two hand-typed cases cannot tell you.
	if rep.Visits["review"] < 20 || rep.Visits["autopay"] < 20 {
		t.Errorf("branches ran %d and %d times; the amounts did not spread over the decision",
			rep.Visits["review"], rep.Visits["autopay"])
	}
	if rep.Visits["review"]+rep.Visits["autopay"] != 100 {
		t.Errorf("the branches took %d cases between them, want 100",
			rep.Visits["review"]+rep.Visits["autopay"])
	}
}

// asVariables converts generated rows to start variables the way the API's start
// path does, so the test drives the engine with what a real run would.
func asVariables(t *testing.T, generated []map[string]any) [][]model.VariableValue {
	t.Helper()
	out := make([][]model.VariableValue, 0, len(generated))
	for _, row := range generated {
		vars := make([]model.VariableValue, 0, len(row))
		for name, raw := range row {
			switch v := raw.(type) {
			case json.Number:
				vars = append(vars, model.VariableValue{Name: name, Kind: model.VarNumber, Text: v.String()})
			case bool:
				vars = append(vars, model.VariableValue{Name: name, Kind: model.VarBool, Bool: v})
			case string:
				vars = append(vars, model.VariableValue{Name: name, Kind: model.VarString, Text: v})
			default:
				t.Fatalf("variable %q has unexpected type %T", name, raw)
			}
		}
		out = append(out, vars)
	}
	return out
}
