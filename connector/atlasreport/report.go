// Package atlasreport is the Atlas report Worker Type (ADR-0555 §3): it reads one of
// Atlas's own read routes with an API token of a technical principal and folds the
// answer into a report in the contract atlas.report/1, which the chart form component
// draws (§2, §4).
//
// The engine half (Resolve) evaluates the task's input object when the job is handed
// out; the worker half (Run) calls the route and folds. The token lives only on the
// Worker Instance, as every Worker's credential does (ADR-0168): the job carries none.
package atlasreport

import (
	"fmt"
	"math"
	"sort"
)

// Schema is the contract a report is written in.
const Schema = "atlas.report/1"

// The contract's limits (ADR-0555 §2). A report past them is an aggregation mistake, not
// a chart to draw; the folds below stay inside them by folding the rest into "other".
const (
	MaxCategories = 50
	MaxSeries     = 8
	MaxDatasets   = 20
)

// Report is one report as a task's result variable holds it.
type Report struct {
	Schema      string             `json:"schema"`
	Title       string             `json:"title,omitempty"`
	GeneratedAt string             `json:"generatedAt"`
	Period      *Period            `json:"period,omitempty"`
	Notes       []string           `json:"notes,omitempty"`
	Datasets    map[string]Dataset `json:"datasets"`
}

// Period is the span a report measures, as RFC 3339 instants or dates.
type Period struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Dataset is a value ({value, previous?, target?}) or a series table ({categories,
// series}), each with a unit.
type Dataset struct {
	Unit       string   `json:"unit"`
	Value      *float64 `json:"value,omitempty"`
	Previous   *float64 `json:"previous,omitempty"`
	Target     *float64 `json:"target,omitempty"`
	Categories []string `json:"categories,omitempty"`
	Series     []Series `json:"series,omitempty"`
}

// Series is one named row of values; a nil value is a gap.
type Series struct {
	Name   string     `json:"name"`
	Values []*float64 `json:"values"`
}

func num(v float64) *float64 { return &v }

// valueSet is a value dataset. Its value is a pointer, so a value of zero is still
// written.
func valueSet(unit string, v float64) Dataset {
	return Dataset{Unit: unit, Value: num(v)}
}

// Check holds a report to the contract's limits.
func (r Report) Check() error {
	if r.Schema != Schema {
		return fmt.Errorf("atlasreport: schema %q, want %q", r.Schema, Schema)
	}
	if len(r.Datasets) == 0 || len(r.Datasets) > MaxDatasets {
		return fmt.Errorf("atlasreport: %d datasets, want 1 to %d", len(r.Datasets), MaxDatasets)
	}
	for name, d := range r.Datasets {
		if d.Value != nil {
			continue
		}
		if len(d.Categories) > MaxCategories {
			return fmt.Errorf("atlasreport: dataset %q has %d categories, more than %d", name, len(d.Categories), MaxCategories)
		}
		if len(d.Series) == 0 || len(d.Series) > MaxSeries {
			return fmt.Errorf("atlasreport: dataset %q has %d series, want 1 to %d", name, len(d.Series), MaxSeries)
		}
		for _, s := range d.Series {
			if len(s.Values) != len(d.Categories) {
				return fmt.Errorf("atlasreport: dataset %q series %q has %d values for %d categories", name, s.Name, len(s.Values), len(d.Categories))
			}
		}
	}
	return nil
}

// row is one category with its values, before it becomes a table.
type row struct {
	label  string
	values []*float64
}

// table builds a series table from rows, the rows sorted by sortBy (descending, the
// first series when -1 means none), and everything past MaxCategories-1 folded into one
// "other" row whose counts are the sums — a part never passes for the whole. It reports
// how many rows were folded.
func table(unit string, names []string, rows []row, sortBy int, other string, summable bool) (Dataset, int) {
	if sortBy >= 0 {
		sort.SliceStable(rows, func(i, j int) bool { return valOr(rows[i].values[sortBy]) > valOr(rows[j].values[sortBy]) })
	}
	folded := 0
	if len(rows) > MaxCategories {
		keep := rows[:MaxCategories-1]
		rest := rows[MaxCategories-1:]
		folded = len(rest)
		agg := row{label: other, values: make([]*float64, len(names))}
		for i := range names {
			if !summable {
				continue
			}
			sum := 0.0
			for _, r := range rest {
				if v := r.values[i]; v != nil {
					sum += *v
				}
			}
			agg.values[i] = num(sum)
		}
		rows = append(append([]row(nil), keep...), agg)
	}
	d := Dataset{Unit: unit, Categories: make([]string, len(rows)), Series: make([]Series, len(names))}
	for i, n := range names {
		d.Series[i] = Series{Name: n, Values: make([]*float64, len(rows))}
	}
	for r, rw := range rows {
		d.Categories[r] = rw.label
		for i := range names {
			d.Series[i].Values[r] = rw.values[i]
		}
	}
	return d, folded
}

// valOr orders a value for sorting: a gap sorts last.
func valOr(v *float64) float64 {
	if v == nil || math.IsNaN(*v) {
		return math.Inf(-1)
	}
	return *v
}
