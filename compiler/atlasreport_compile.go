package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/atlasreport/catalog"
)

// compileAtlasReportConnectorTask compiles an <atlas:atlasReportConnector> task: one of
// Atlas's own read routes, folded into a report a form's chart draws, read through a
// Worker an operator configured with a technical principal's token (ADR-0555 §3).
//
// Like the calendar task it carries one input object instead of an attribute per value.
// Its keys are checked here against the operation's row in connector/atlasreport/catalog
// — the rows the worker runs — and the literal values a deploy can judge are checked too;
// a value given as an =expression is checked by the engine when it hands the job out.
func compileAtlasReportConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.AtlasReport
	fail := func(format string, args ...any) (int32, error) {
		return 0, fmt.Errorf("compiler: Atlas report task %q "+format, append([]any{st.Id}, args...)...)
	}
	if strings.TrimSpace(cn.Connector) == "" {
		return fail("needs a connector attribute naming the Atlas report Worker it reads through")
	}
	workerExpr, err := connectorValue(b.gate(), st.Id, "Atlas report worker", "connector", cn.Connector)
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
	if op.NeedsResult && result == "" {
		return fail("operation %q needs a resultVariable: the report is what a form's chart draws", name)
	}
	cfg := AtlasReportConfig{
		Worker:     strings.TrimSpace(cn.Connector),
		WorkerExpr: workerExpr,
		Op:         name,
		ResultVar:  result,
		Retries:    retries,
	}
	if strings.TrimSpace(cn.Input) == "" {
		if err := catalog.CheckInputKeys(name, nil); err != nil {
			return fail("needs an input object: %s", strings.TrimPrefix(err.Error(), "atlasreport: "))
		}
		return b.AddAtlasReportConnectorTask(cfg), nil
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
		return fail("%s", strings.TrimPrefix(err.Error(), "atlasreport: "))
	}
	if err := checkAtlasReportLiterals(input); err != nil {
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
	cfg.Input = RestExpr{Expr: e}
	return b.AddAtlasReportConnectorTask(cfg), nil
}

// checkAtlasReportLiterals checks the values written out literally, where it is still
// the author reading the message. An =expression is the engine's to check.
func checkAtlasReportLiterals(input map[string]any) error {
	literal := func(k string) (string, bool) {
		s, ok := input[k].(string)
		if !ok || strings.HasPrefix(strings.TrimSpace(s), "=") {
			return "", false
		}
		return s, true
	}
	for _, k := range []string{catalog.InFrom, catalog.InTo} {
		if v, ok := literal(k); ok {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				if _, err := time.Parse("2006-01-02", v); err != nil {
					return fmt.Errorf("%s %q is neither a date (2026-11-01) nor a date and time with an offset (RFC 3339)", k, v)
				}
			}
		}
	}
	if v, ok := literal(catalog.InBucket); ok {
		if err := catalog.CheckBucket(v); err != nil {
			return fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "atlasreport: "))
		}
	}
	if v, ok := literal(catalog.InLanguage); ok {
		if err := catalog.CheckLanguage(v); err != nil {
			return fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "atlasreport: "))
		}
	}
	if n, ok := input[catalog.InBuckets].(json.Number); ok {
		v, err := n.Int64()
		if err != nil {
			return fmt.Errorf("buckets is %s; it must be a whole number", n)
		}
		if err := catalog.CheckBuckets(v); err != nil {
			return fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "atlasreport: "))
		}
	}
	_, hasBuckets := input[catalog.InBuckets]
	_, hasBucket := input[catalog.InBucket]
	if hasBuckets != hasBucket {
		return fmt.Errorf("buckets and bucket go together: how many, and of which size (day, week, month)")
	}
	if _, hasFrom := input[catalog.InFrom]; hasFrom && hasBuckets {
		return fmt.Errorf("a series of buckets ends at to and counts back; from is not taken with buckets")
	}
	return nil
}
