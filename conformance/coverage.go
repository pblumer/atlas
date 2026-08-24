package conformance

import (
	"fmt"
	"sort"
	"strings"
)

// CoverageReport renders the feature/pattern coverage matrix as Markdown from the
// live register (Features, Patterns, Scenarios), so a feature added without a
// covering scenario shows up as an explicit gap. The report is committed as
// COVERAGE.md and a test fails if it drifts.
func CoverageReport() string {
	return buildCoverage(Features, Patterns, Scenarios, NegativeModels)
}

// buildCoverage is the pure core of CoverageReport, taking the register as
// arguments so the gap and not-covered branches are unit-testable without
// mutating package globals.
func buildCoverage(features []Feature, patterns []Pattern, scenarios []Scenario, negatives []NegativeModel) string {
	scenariosByFeature := map[string][]string{}
	for _, sc := range scenarios {
		for _, f := range sc.Features {
			scenariosByFeature[f] = append(scenariosByFeature[f], sc.Name)
		}
	}
	featureCovered := func(id string) bool { return len(scenariosByFeature[id]) > 0 }

	// A pattern is covered when at least one feature realizing it is covered.
	patternFeatures := map[string][]string{}
	for _, f := range features {
		for _, p := range f.Patterns {
			patternFeatures[p] = append(patternFeatures[p], f.ID)
		}
	}
	patternCovered := func(id string) bool {
		for _, fid := range patternFeatures[id] {
			if featureCovered(fid) {
				return true
			}
		}
		return false
	}

	var b strings.Builder
	b.WriteString("# BPMN conformance coverage\n\n")
	b.WriteString("Generated from the register in `scenario.go` by ")
	b.WriteString("`go test ./conformance -update`. Do not edit by hand.\n\n")

	b.WriteString("## Features\n\n")
	b.WriteString("| Feature | Patterns | Scenarios | Covered |\n")
	b.WriteString("|---------|----------|-----------|---------|\n")
	for _, f := range features {
		scs := scenariosByFeature[f.ID]
		sort.Strings(scs)
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
			f.Name, joinOrDash(f.Patterns), joinOrDash(scs), tick(featureCovered(f.ID))))
	}

	b.WriteString("\n## Control-flow patterns\n\n")
	b.WriteString("| Pattern | Realized by | Covered |\n")
	b.WriteString("|---------|-------------|---------|\n")
	for _, p := range patterns {
		b.WriteString(fmt.Sprintf("| %s %s | %s | %s |\n",
			p.ID, p.Name, joinOrDash(patternFeatures[p.ID]), tick(patternCovered(p.ID))))
	}

	// Gaps make the "what's still missing" answer impossible to overlook.
	var gaps []string
	for _, f := range features {
		if !featureCovered(f.ID) {
			gaps = append(gaps, "feature: "+f.ID)
		}
	}
	for _, p := range patterns {
		if !patternCovered(p.ID) {
			gaps = append(gaps, "pattern: "+p.ID)
		}
	}
	b.WriteString("\n## Gaps\n\n")
	if len(gaps) == 0 {
		b.WriteString("None — every registered feature and pattern has a covering scenario.\n")
	} else {
		for _, g := range gaps {
			b.WriteString("- " + g + "\n")
		}
	}

	// Negative models: the adversarial half — each must be rejected at compile.
	b.WriteString("\n## Negative models (rejected at compile)\n\n")
	if len(negatives) == 0 {
		b.WriteString("None registered.\n")
	} else {
		b.WriteString("| Model | Why it must be rejected |\n")
		b.WriteString("|-------|-------------------------|\n")
		for _, n := range negatives {
			b.WriteString(fmt.Sprintf("| %s | %s |\n", n.Name, n.Reason))
		}
	}
	return b.String()
}

func joinOrDash(xs []string) string {
	if len(xs) == 0 {
		return "—"
	}
	return strings.Join(xs, ", ")
}

func tick(ok bool) string {
	if ok {
		return "✅"
	}
	return "🔲"
}
