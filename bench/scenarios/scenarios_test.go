package scenarios_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/bench/scenarios"
	"github.com/pblumer/atlas/compiler"
)

// TestCatalogueCompiles pins that every scenario produces a valid compiled
// process programmatically (level 1) and a valid one from its BPMN XML (level
// 2). If the two representations drift so far that one stops compiling, this
// fails before any benchmark runs. Behavioral equivalence — that both actually
// run to completion — is covered in package bench (level 1) and package e2e
// (level 2).
func TestCatalogueCompiles(t *testing.T) {
	all := scenarios.All()
	if len(all) == 0 {
		t.Fatal("catalogue is empty")
	}

	seen := map[string]bool{}
	for _, s := range all {
		if s.Name == "" {
			t.Fatalf("scenario %q has no name", s.Feature)
		}
		if seen[s.Name] {
			t.Fatalf("duplicate scenario name %q", s.Name)
		}
		seen[s.Name] = true

		t.Run(s.Name, func(t *testing.T) {
			if s.Feature == "" || s.Description == "" {
				t.Errorf("scenario %q missing feature/description metadata", s.Name)
			}

			// Level 1: the programmatic build must produce a process.
			cp, jobTypes := s.Compile(7)
			if cp == nil {
				t.Fatalf("Compile returned a nil process")
			}
			if cp.Key != 7 {
				t.Errorf("compiled key = %d, want 7", cp.Key)
			}

			// Level 2: the BPMN XML must parse into a process too.
			if strings.TrimSpace(s.BPMN) == "" {
				t.Fatalf("scenario has no BPMN XML")
			}
			if _, err := compiler.Parse(7, 1, strings.NewReader(s.BPMN)); err != nil {
				t.Fatalf("BPMN does not parse: %v", err)
			}

			// The service-task scenario needs exactly one job type to drive; the
			// gateway/script-only scenarios need none.
			switch s.Name {
			case "linear":
				if len(jobTypes) != 1 {
					t.Errorf("linear jobTypes = %v, want exactly one", jobTypes)
				}
			default:
				if len(jobTypes) != 0 {
					t.Errorf("%s jobTypes = %v, want none (auto-completing)", s.Name, jobTypes)
				}
			}
		})
	}
}

// TestRoutingScenariosCarryVariables guards that the data-routed scenarios ship
// the start variable their gateway condition reads. Without it the FEEL
// condition sees no `amount`, the default branch is taken, and the benchmark
// would silently measure the wrong path.
func TestRoutingScenariosCarryVariables(t *testing.T) {
	for _, s := range scenarios.All() {
		if s.Name != "exclusive" && s.Name != "mixed" {
			continue
		}
		var hasAmount bool
		for _, v := range s.StartVars {
			if v.Name == "amount" {
				hasAmount = true
			}
		}
		if !hasAmount {
			t.Errorf("scenario %q routes on `amount` but sets no such start variable", s.Name)
		}
	}
}
