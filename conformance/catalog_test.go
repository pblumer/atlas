package conformance

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The catalog: a human-readable page per scenario under conformance/catalog/scenarios/,
// each carrying the model's description, its rendered diagram, how it is driven, and the
// outcome Atlas produces. The pages are generated from scenario.go + the run result, so
// they never drift from the executable suite — a stale page fails this test exactly like a
// stale golden or TCK case.
//
// Regenerate with `go test ./conformance -update`.

func TestCatalogPagesUpToDate(t *testing.T) {
	for _, sc := range Scenarios {
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			model, err := sc.LoadModel()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			res, err := Run(t.TempDir(), model, sc.Root, sc.Start, sc.Driver)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			page := catalogPage(sc, res, modelDescription(model))
			checkTCKFile(t, filepath.Join("catalog", "scenarios", sc.Name+".md"), []byte(page))
		})
	}
	checkTCKFile(t, filepath.Join("catalog", "scenarios", "README.md"), []byte(catalogIndex()))
}

// featureNames indexes the feature register by id for page rendering.
func featureNames() map[string]string {
	m := make(map[string]string, len(Features))
	for _, f := range Features {
		m[f.ID] = f.Name
	}
	return m
}

// modelDescription lifts the leading XML comment out of a fixture — the prose block
// every model opens with — and reflows it into paragraphs for the page body.
func modelDescription(model []byte) string {
	s := string(model)
	i := strings.Index(s, "<!--")
	j := strings.Index(s, "-->")
	if i < 0 || j < 0 || j < i {
		return ""
	}
	body := s[i+len("<!--") : j]
	var paras []string
	var cur []string
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			if len(cur) > 0 {
				paras = append(paras, strings.Join(cur, " "))
				cur = nil
			}
			continue
		}
		cur = append(cur, ln)
	}
	if len(cur) > 0 {
		paras = append(paras, strings.Join(cur, " "))
	}
	// Escape angle brackets so literal BPMN tags in the prose (<dataObject>,
	// <association>, ...) render as text rather than being swallowed as HTML.
	out := strings.Join(paras, "\n\n")
	out = strings.ReplaceAll(out, "&", "&amp;")
	out = strings.ReplaceAll(out, "<", "&lt;")
	out = strings.ReplaceAll(out, ">", "&gt;")
	return out
}

func catalogPage(sc Scenario, res RunResult, desc string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", sc.Name)
	if desc != "" {
		fmt.Fprintf(&b, "%s\n\n", desc)
	}

	base := strings.TrimSuffix(sc.Model, ".bpmn")
	fmt.Fprintf(&b, "![%s diagram](../diagrams/%s.png)\n\n", sc.Name, base)

	fmt.Fprintf(&b, "- **Model:** [`%s`](../../models/%s)\n", sc.Model, sc.Model)
	names := featureNames()
	var feats []string
	for _, id := range sc.Features {
		if n := names[id]; n != "" {
			feats = append(feats, fmt.Sprintf("`%s` (%s)", id, n))
		} else {
			feats = append(feats, fmt.Sprintf("`%s`", id))
		}
	}
	fmt.Fprintf(&b, "- **Features:** %s\n", strings.Join(feats, ", "))
	if pats := sc.PatternsOf(); len(pats) > 0 {
		var pp []string
		for _, p := range pats {
			pp = append(pp, patternLabel(p))
		}
		fmt.Fprintf(&b, "- **Control-flow patterns:** %s\n", strings.Join(pp, ", "))
	}
	if sc.EquivClass != "" {
		fmt.Fprintf(&b, "- **Metamorphic class:** `%s`\n", sc.EquivClass)
	}

	b.WriteString("\n## How it is driven\n\n")
	fmt.Fprintf(&b, "**Start:** %s\n\n", describeStart(sc.Start))
	if len(sc.Driver) == 0 {
		b.WriteString("**Steps:** _self-completing — the model runs to completion on its own (in-engine scripts, no parked token)._\n")
	} else {
		b.WriteString("**Steps:**\n\n")
		for i, st := range sc.Driver {
			fmt.Fprintf(&b, "%d. %s\n", i+1, describeStep(st))
		}
	}

	b.WriteString("\n## Expected outcome\n\n")
	completed := "no"
	if res.State.String() == "completed" {
		completed = "yes"
	}
	fmt.Fprintf(&b, "- **Completed:** %s\n", completed)
	if len(res.Path) > 0 {
		var q []string
		for _, p := range res.Path {
			q = append(q, "`"+p+"`")
		}
		fmt.Fprintf(&b, "- **Path:** %s\n", strings.Join(q, " → "))
	}
	if len(res.Variables) > 0 {
		keys := make([]string, 0, len(res.Variables))
		for k := range res.Variables {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var vv []string
		for _, k := range keys {
			vv = append(vv, fmt.Sprintf("`%s = %s`", k, res.Variables[k]))
		}
		fmt.Fprintf(&b, "- **Variables:** %s\n", strings.Join(vv, ", "))
	}
	if len(res.DataObjects) > 0 {
		var dd []string
		for _, d := range res.DataObjects {
			dd = append(dd, "`"+d+"`")
		}
		fmt.Fprintf(&b, "- **Data objects:** %s\n", strings.Join(dd, ", "))
	}

	b.WriteString("\n---\n_Generated from `conformance/scenario.go` by `go test ./conformance -update`. Do not edit by hand._\n")
	return b.String()
}

func patternLabel(id string) string {
	for _, p := range Patterns {
		if p.ID == id {
			return fmt.Sprintf("%s (%s)", p.ID, p.Name)
		}
	}
	return id
}

func describeStart(s Start) string {
	switch s.kind {
	case startMessage:
		if s.correlation != "" {
			return fmt.Sprintf("message start — publishing `%s` (key `%s`) births the instance.", s.message, s.correlation)
		}
		return fmt.Sprintf("message start — publishing `%s` births the instance.", s.message)
	case startTimer:
		return fmt.Sprintf("timer start — an armed timer firing after `%s` births the instance.", s.after)
	case startSignal:
		return fmt.Sprintf("signal start — instantiating the `%s` process broadcasts the signal that births the instance.", s.trigger)
	default:
		return "explicit `CreateInstance` on the root process."
	}
}

func describeStep(s Step) string {
	switch s.kind {
	case stepPublish:
		out := fmt.Sprintf("Publish message `%s`", s.message)
		if s.correlation != "" {
			out += fmt.Sprintf(" (key `%s`)", s.correlation)
		}
		return out + varsSuffix(s.vars) + "."
	case stepWait:
		return fmt.Sprintf("Advance the clock by `%s`, firing any timer that comes due.", s.after)
	case stepFail:
		return fmt.Sprintf("Fail job `%s` with no retries — raises an incident (`%s`).", s.element, s.message)
	case stepResolve:
		return fmt.Sprintf("Resolve the incident on `%s`, re-activating its job.", s.element)
	case stepThrowError:
		return fmt.Sprintf("Throw business error `%s` from job `%s` so a boundary error event catches it.", s.message, s.element)
	default: // stepComplete
		return fmt.Sprintf("Complete job `%s`", s.element) + varsSuffix(s.vars) + "."
	}
}

func varsSuffix(vs []Var) string {
	if len(vs) == 0 {
		return ""
	}
	var vv []string
	for _, v := range vs {
		vv = append(vv, fmt.Sprintf("`%s = %s`", v.Name, v.Text))
	}
	return " with " + strings.Join(vv, ", ")
}

func catalogIndex() string {
	var b strings.Builder
	b.WriteString("# Scenario pages\n\n")
	b.WriteString("Auto-generated index of the conformance scenarios. Each page carries the model's\n")
	b.WriteString("description, its diagram, how the instance is driven, and the outcome Atlas must\n")
	b.WriteString("produce. Regenerate with `go test ./conformance -update`; do not edit by hand.\n\n")
	fmt.Fprintf(&b, "## Positive scenarios (%d)\n\n", len(Scenarios))
	b.WriteString("| Scenario | Features | Patterns |\n|---|---|---|\n")
	for _, sc := range Scenarios {
		var ids []string
		for _, id := range sc.Features {
			ids = append(ids, "`"+id+"`")
		}
		pats := strings.Join(sc.PatternsOf(), ", ")
		if pats == "" {
			pats = "—"
		}
		fmt.Fprintf(&b, "| [%s](%s.md) | %s | %s |\n", sc.Name, sc.Name, strings.Join(ids, ", "), pats)
	}
	if len(NegativeModels) > 0 {
		fmt.Fprintf(&b, "\n## Negative models (%d)\n\n", len(NegativeModels))
		b.WriteString("Models that must be **rejected at compile** — the suite asserts the engine refuses\n")
		b.WriteString("them rather than executing something ill-defined.\n\n")
		b.WriteString("| Model | Why it is rejected |\n|---|---|\n")
		for _, nm := range NegativeModels {
			fmt.Fprintf(&b, "| [`%s`](../../models/%s) | %s |\n", nm.Model, nm.Model, nm.Reason)
		}
	}
	return b.String()
}
