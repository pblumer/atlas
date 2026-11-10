package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/calendar/catalog"
)

// compileCalendarConnectorTask compiles an <atlas:calendarConnector> task: one operation
// on a calendar through a Worker an operator configured (ADR-0552).
//
// The task carries one input object instead of an attribute per value: the object is
// the operation's technical capability contract input, so the model, the job and the
// conformance suites speak the same shape. Its keys are checked here against the
// operation's row in connector/calendar/catalog — the rows the worker runs — and the
// literal values a deploy can judge are checked too; a value given as an =expression
// is checked by the worker when it has one.
func compileCalendarConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Calendar
	fail := func(format string, args ...any) (int32, error) {
		return 0, fmt.Errorf("compiler: calendar task %q "+format, append([]any{st.Id}, args...)...)
	}
	if strings.TrimSpace(cn.Connector) == "" {
		return fail("needs a connector attribute naming the calendar Worker whose calendars it uses")
	}
	workerExpr, err := connectorValue(b.gate(), st.Id, "calendar worker", "connector", cn.Connector)
	if err != nil {
		return 0, err
	}
	name := strings.ToLower(strings.TrimSpace(cn.Operation))
	if name == "" {
		return fail("needs an operation (%s)", strings.Join(catalog.Names(), ", "))
	}
	op, ok := catalog.Lookup(name)
	if !ok {
		return fail("has an unknown operation %q (want %s)", cn.Operation, strings.Join(catalog.Names(), ", "))
	}
	result := strings.TrimSpace(cn.ResultVariable)
	switch {
	case op.NeedsResult && result == "":
		return fail("operation %q needs a resultVariable (its answer is what the process goes on with)", name)
	case !op.TakesResult() && result != "":
		return fail("operation %q returns nothing, so a resultVariable would name a value that is never written; remove it", name)
	}
	if strings.TrimSpace(cn.Input) == "" {
		return fail("needs an input object (%s)", strings.Join(op.Required, ", "))
	}
	dec := json.NewDecoder(strings.NewReader(cn.Input))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return fail("has an input that is not JSON: %v", err)
	}
	input, ok := raw.(map[string]any)
	if !ok {
		return fail("input must be a JSON object of the operation's values, not a %T", raw)
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if err := catalog.CheckInputKeys(name, keys); err != nil {
		return fail("%s", strings.TrimPrefix(err.Error(), "calendar: "))
	}
	if err := checkCalendarLiterals(input); err != nil {
		return fail("%v", err)
	}
	feel, err := jsonTemplateToFeel(input)
	if err != nil {
		return fail("input: %v", err)
	}
	e, err := b.gate().compileFEEL(feel)
	if err != nil {
		return fail("input did not compile (a value's =expression may be malformed): %v", err)
	}
	return b.AddCalendarConnectorTask(CalendarConfig{
		Worker:     strings.TrimSpace(cn.Connector),
		WorkerExpr: workerExpr,
		Op:         name,
		Input:      RestExpr{Expr: e},
		ResultVar:  result,
		Retries:    retries,
	}), nil
}

// checkCalendarLiterals checks the values written out literally, where it is still the
// author reading the message. An =expression is the worker's to check.
func checkCalendarLiterals(input map[string]any) error {
	literal := func(k string) (string, bool) {
		s, ok := input[k].(string)
		if !ok || strings.HasPrefix(strings.TrimSpace(s), "=") {
			return "", false
		}
		return s, true
	}
	if tz, ok := literal("timeZone"); ok {
		if _, err := time.LoadLocation(tz); err != nil || strings.TrimSpace(tz) == "" {
			return fmt.Errorf("timeZone %q is not an IANA time zone such as Europe/Zurich", tz)
		}
	}
	for _, k := range []string{"from", "to", "start", "end"} {
		if v, ok := literal(k); ok {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				return fmt.Errorf("%s %q is not a date and time with an offset (RFC 3339)", k, v)
			}
		}
	}
	if v, ok := literal("require"); ok && v != catalog.RequireAny && v != catalog.RequireAll {
		return fmt.Errorf("require %q is neither %q nor %q", v, catalog.RequireAny, catalog.RequireAll)
	}
	for k, bound := range map[string][2]int64{
		"durationMinutes": {catalog.MinMinutes, catalog.MaxMinutes},
		"gridMinutes":     {catalog.MinMinutes, catalog.MaxMinutes},
		"bufferMinutes":   {0, catalog.MaxBufferMinutes},
		"maxResults":      {1, int64(catalog.SlotBounds.Max)},
	} {
		n, ok := input[k].(json.Number)
		if !ok {
			continue
		}
		v, err := n.Int64()
		if err != nil || v < bound[0] || v > bound[1] {
			return fmt.Errorf("%s is %s; it must be a whole number between %d and %d", k, n, bound[0], bound[1])
		}
	}
	if oh, ok := input["openingHours"].(map[string]any); ok {
		for day := range oh {
			if !containsString(catalog.Weekdays, day) {
				return fmt.Errorf("openingHours names %q, which is not one of %s", day, strings.Join(catalog.Weekdays, ", "))
			}
		}
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
