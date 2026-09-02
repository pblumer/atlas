package playground

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

// FieldKind is how one field's value is produced.
type FieldKind string

const (
	// FieldInt draws a whole number in [Min, Max].
	FieldInt FieldKind = "int"
	// FieldDecimal draws a number in [Min, Max] to Decimals places.
	FieldDecimal FieldKind = "decimal"
	// FieldBool is true PercentTrue of the time.
	FieldBool FieldKind = "bool"
	// FieldChoice picks one of Choices, by weight.
	FieldChoice FieldKind = "choice"
	// FieldConstant writes Value into every case.
	FieldConstant FieldKind = "constant"
	// FieldSequence numbers the cases: Prefix and the case's position.
	FieldSequence FieldKind = "sequence"
	// FieldTimestamp draws an instant in a window around the run's own start.
	FieldTimestamp FieldKind = "timestamp"
)

// maxDrawSteps bounds how many distinct values one draw may span. It is the
// 2^53 a float64 — and therefore a JSON number — holds exactly: a field whose
// range is wider than that could not be drawn without rounding, and a bound
// nobody can hit is worse than a refusal that says so.
const maxDrawSteps = float64(1 << 53)

// maxDecimals is how many places a decimal field may ask for. Six is the point
// past which a "random amount" stops being an amount.
const maxDecimals = 6

// generatorSalt separates the field draws from the stub draws.
//
// Both go through [newDraw], but over different domains: a stub draws on a job's
// key counter, a field on a case's position in the dataset. The offset keeps the
// two apart even where those numbers collide, so the amount a case carries is not
// correlated with how long a job in it happens to take.
const generatorSalt = 1 << 20

// Choice is one option of a FieldChoice field.
type Choice struct {
	// Value is what the case carries. It is used as given, so a choice of numbers
	// arrives as a number and a choice of labels as a string.
	Value any
	// Weight is how often this option comes up relative to the others. Zero on
	// every option means they are equally likely; zero on one of several weighted
	// options means that one never comes up, which is how an option is kept in the
	// list while being switched off.
	Weight int
}

// Field is one variable of a generated dataset, and how its value is drawn.
//
// It is one structure for every kind rather than one per kind because that is
// what it is on the wire and on screen: a row in a small table, with a name, a
// kind, and the parameters that kind reads. Each parameter below says which kinds
// read it; the rest are ignored.
type Field struct {
	// Name is the start variable this field writes.
	Name string
	Kind FieldKind
	// Min and Max bound FieldInt and FieldDecimal, inclusive. They are float64
	// because that is what a JSON number is; FieldInt requires whole ones.
	Min, Max float64
	// Decimals is how many places FieldDecimal draws to.
	Decimals int
	// PercentTrue is how often FieldBool is true.
	PercentTrue int
	// Choices are the options of FieldChoice.
	Choices []Choice
	// Value is what FieldConstant writes.
	Value any
	// Prefix goes in front of FieldSequence's number.
	Prefix string
	// MinOffset and MaxOffset bound FieldTimestamp, as offsets from the run's own
	// simulated start. They are relative because a Playground run happens on a
	// virtual clock: an absolute date typed in once is stale the next time the
	// scenario runs, while "some time in the last thirty days" stays true.
	MinOffset, MaxOffset time.Duration
	// OnlyDate writes a FieldTimestamp as a calendar date rather than an instant.
	OnlyDate bool
}

// Dataset is a dataset described rather than listed: how many cases, and how each
// field of one is produced.
//
// It is what a list of cases cannot be at this scale. Fifty thousand cases typed
// into a panel are not a dataset anybody wrote, and fifty thousand rows uploaded
// as a file cannot be stored in a scenario — but the twenty lines that describe
// them can, which is what makes a generated run repeatable and reviewable.
type Dataset struct {
	// Count is how many cases to produce.
	Count int
	// Fields are the start variables each case carries. None is allowed: that is a
	// run of N cases with no input, which is a load test of the model's own shape.
	Fields []Field
}

// Validate reports what is wrong with the description before anything is drawn
// from it, so a mistake in a scenario is caught where it was typed rather than
// three hundred cases into a run.
func (d Dataset) Validate() error {
	if d.Count < 1 {
		return fmt.Errorf("playground: a generated dataset needs at least one case, not %d", d.Count)
	}
	seen := make(map[string]bool, len(d.Fields))
	for i, f := range d.Fields {
		if f.Name == "" {
			return fmt.Errorf("playground: field %d has no name", i+1)
		}
		if seen[f.Name] {
			return fmt.Errorf("playground: field %q is described twice; a case carries one value per name", f.Name)
		}
		seen[f.Name] = true
		if err := f.validate(); err != nil {
			return err
		}
	}
	return nil
}

// validate checks one field against the parameters its kind reads.
func (f Field) validate() error {
	switch f.Kind {
	case FieldInt:
		if f.Min != math.Trunc(f.Min) || f.Max != math.Trunc(f.Max) {
			return fmt.Errorf("playground: field %q draws whole numbers, so its bounds have to be whole: %v to %v", f.Name, f.Min, f.Max)
		}
		return f.validateRange(1)
	case FieldDecimal:
		if f.Decimals < 0 || f.Decimals > maxDecimals {
			return fmt.Errorf("playground: field %q asks for %d decimal places; 0 to %d", f.Name, f.Decimals, maxDecimals)
		}
		return f.validateRange(math.Pow(10, float64(f.Decimals)))
	case FieldBool:
		if f.PercentTrue < 0 || f.PercentTrue > 100 {
			return fmt.Errorf("playground: field %q is true %d%% of the time; 0 to 100", f.Name, f.PercentTrue)
		}
	case FieldChoice:
		if len(f.Choices) == 0 {
			return fmt.Errorf("playground: field %q chooses between no options", f.Name)
		}
		for _, c := range f.Choices {
			if c.Weight < 0 {
				return fmt.Errorf("playground: field %q gives an option a weight of %d; a weight is how often it comes up, so it cannot be negative", f.Name, c.Weight)
			}
		}
	case FieldConstant, FieldSequence:
		// Nothing is drawn, so there is nothing to bound.
	case FieldTimestamp:
		if f.MaxOffset < f.MinOffset {
			return fmt.Errorf("playground: field %q ends before it begins: %s to %s", f.Name, f.MinOffset, f.MaxOffset)
		}
	default:
		return fmt.Errorf("playground: field %q has kind %q, which is not one of int, decimal, bool, choice, constant, sequence, timestamp", f.Name, f.Kind)
	}
	return nil
}

// validateRange checks a numeric field's bounds, scaled by the steps one unit is
// divided into.
//
// Both the bounds themselves and the distance between them have to stay inside
// 2^53. The span is the obvious one — a range with more values than that cannot be
// drawn without rounding. The bounds matter for their own reason: a whole-number
// field bounded at 1e300 passes every other check here and then converts to an
// int64 that Go does not define, so the run carries a number nobody asked for and
// nothing said why.
func (f Field) validateRange(perUnit float64) error {
	if f.Max < f.Min {
		return fmt.Errorf("playground: field %q has a maximum below its minimum: %v to %v", f.Name, f.Min, f.Max)
	}
	if math.Abs(f.Min) >= maxDrawSteps || math.Abs(f.Max) >= maxDrawSteps || (f.Max-f.Min)*perUnit >= maxDrawSteps {
		return fmt.Errorf("playground: field %q asks for numbers that cannot be drawn exactly: %v to %v. "+
			"The limit is 2^53, what a number holds without rounding — narrow the range or ask for fewer decimals",
			f.Name, f.Min, f.Max)
	}
	return nil
}

// Generate produces the whole dataset: one row per case, in the shape a case list
// sent inline or parsed out of a CSV already has, so a generated case is seeded
// exactly as a typed-in one is.
//
// It is a pure function of the description, the seed and the run's start. Nothing
// here reads a global random source, so the same scenario run twice produces the
// same three hundred amounts — which is what makes a generated run something a
// review can quote and a build can compare against a baseline.
func (d Dataset) Generate(seed int64, start time.Time) ([]map[string]any, error) {
	return d.rows(seed, start, d.Count)
}

// Preview produces the first n rows, which are the first n rows Generate would
// produce: each case draws on its own position, so no row depends on how many
// follow it. That is what lets a panel show what a run will carry before running
// fifty thousand of them.
func (d Dataset) Preview(seed int64, start time.Time, n int) ([]map[string]any, error) {
	if n > d.Count {
		n = d.Count
	}
	return d.rows(seed, start, n)
}

// rows draws the first upTo cases of the dataset.
func (d Dataset) rows(seed int64, start time.Time, upTo int) ([]map[string]any, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	if upTo < 0 {
		upTo = 0
	}
	// The sequence numbers are padded to the width of the whole dataset, not of
	// the page being drawn, so a previewed row carries the identifier the run will
	// give it.
	width := len(strconv.Itoa(d.Count))
	out := make([]map[string]any, 0, upTo)
	for i := 0; i < upTo; i++ {
		row := make(map[string]any, len(d.Fields))
		for j, f := range d.Fields {
			row[f.Name] = f.valueFor(newDraw(seed, uint64(i), generatorSalt+uint64(j)), i, width, start)
		}
		out = append(out, row)
	}
	return out, nil
}

// valueFor is one field's value for one case. index is the case's position, width
// the padding a sequence number gets, and start the run's simulated beginning.
func (f Field) valueFor(d draw, index, width int, start time.Time) any {
	switch f.Kind {
	case FieldInt:
		return json.Number(strconv.FormatInt(int64(f.Min)+int64(d.below(uint64(f.Max-f.Min)+1)), 10))
	case FieldDecimal:
		steps := math.Pow(10, float64(f.Decimals))
		v := f.Min + float64(d.below(uint64((f.Max-f.Min)*steps)+1))/steps
		return json.Number(strconv.FormatFloat(v, 'f', f.Decimals, 64))
	case FieldBool:
		return d.below(100) < uint64(f.PercentTrue)
	case FieldChoice:
		return f.choose(d)
	case FieldSequence:
		return f.Prefix + fmt.Sprintf("%0*d", width, index+1)
	case FieldTimestamp:
		at := start.Add(f.MinOffset + time.Duration(d.below(uint64(f.MaxOffset-f.MinOffset)+1))).UTC()
		if f.OnlyDate {
			return at.Format(time.DateOnly)
		}
		return at.Format(time.RFC3339)
	default: // FieldConstant, the only kind left once Validate has passed
		return f.Value
	}
}

// choose picks one option by weight. Unweighted options are equally likely: a
// list somebody typed without thinking about proportions should behave the
// obvious way rather than refusing to run.
func (f Field) choose(d draw) any {
	total := 0
	for _, c := range f.Choices {
		total += c.Weight
	}
	if total == 0 {
		return f.Choices[d.below(uint64(len(f.Choices)))].Value
	}
	// The draw lands in [0, total), so the last option takes whatever the ones
	// before it did not: walking all of them would leave a return nothing reaches.
	at := int(d.below(uint64(total)))
	for _, c := range f.Choices[:len(f.Choices)-1] {
		if at < c.Weight {
			return c.Value
		}
		at -= c.Weight
	}
	return f.Choices[len(f.Choices)-1].Value
}
