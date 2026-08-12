package conformance

import (
	"encoding/json"
	"sort"
	"time"
)

// This file gives the driver spec a portable, language-neutral JSON form. The TCK
// case format (conformance/tck/) is built from it: a case is data — model.bpmn plus
// a case.json describing how the instance is born and driven — so an engine other
// than Atlas can run it without reading Go. See conformance/tck/README.md.

// MarshalJSON renders a driver step as a tagged object, e.g.
// {"action":"complete","element":"charge","vars":{"status":"captured"}}.
func (s Step) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	switch s.kind {
	case stepComplete:
		m["action"] = "complete"
		m["element"] = s.element
	case stepPublish:
		m["action"] = "publish"
		m["message"] = s.message
		m["correlation"] = s.correlation
	case stepWait:
		m["action"] = "wait"
		m["afterMs"] = s.after / time.Millisecond
	case stepFail:
		m["action"] = "fail"
		m["element"] = s.element
		m["message"] = s.message
	case stepResolve:
		m["action"] = "resolve"
		m["element"] = s.element
	case stepThrowError:
		m["action"] = "throwError"
		m["element"] = s.element
		m["code"] = s.message
	default:
		m["action"] = "unknown"
	}
	if vars := varsToMap(s.vars); vars != nil {
		m["vars"] = vars
	}
	return json.Marshal(m)
}

// MarshalJSON renders how the instance is born, e.g. {"kind":"signal","trigger":"thrower"}.
func (s Start) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	switch s.kind {
	case startExplicit:
		m["kind"] = "explicit"
	case startMessage:
		m["kind"] = "message"
		m["name"] = s.message
		m["correlation"] = s.correlation
	case startTimer:
		m["kind"] = "timer"
		m["afterMs"] = s.after / time.Millisecond
	case startSignal:
		m["kind"] = "signal"
		m["trigger"] = s.trigger
	default:
		m["kind"] = "unknown"
	}
	return json.Marshal(m)
}

func varsToMap(vs []Var) map[string]string {
	if len(vs) == 0 {
		return nil
	}
	m := make(map[string]string, len(vs))
	for _, v := range vs {
		m[v.Name] = v.Text
	}
	return m
}

// PatternsOf returns the sorted-unique control-flow patterns a scenario realizes,
// resolved through the features it exercises.
func (s Scenario) PatternsOf() []string {
	byID := map[string][]string{}
	for _, f := range Features {
		byID[f.ID] = f.Patterns
	}
	seen := map[string]bool{}
	var out []string
	for _, fid := range s.Features {
		for _, p := range byID[fid] {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// LoadModel exposes a scenario's embedded BPMN model for tooling (the TCK generator,
// the catalog) that lives outside this package.
func (s Scenario) LoadModel() ([]byte, error) { return s.load() }
