package conformance

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update regenerates golden trace files and COVERAGE.md instead of asserting
// against them: `go test ./conformance -update`. A regenerated golden is a
// behavior change and must be reviewed in the diff before committing.
var update = flag.Bool("update", false, "update golden files and COVERAGE.md")

// TestScenarios runs every registered scenario through the golden, replay, and
// structural-invariant oracles. Run and its callees enforce replay equivalence
// (I4) and the no-orphan-tokens invariant; this test adds the golden comparison.
func TestScenarios(t *testing.T) {
	for _, sc := range Scenarios {
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			modelXML, err := sc.load()
			if err != nil {
				t.Fatalf("load model: %v", err)
			}
			res, err := Run(t.TempDir(), modelXML)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			goldenPath := filepath.Join("golden", sc.Name+".trace")
			got := res.Golden()
			if *update {
				writeFile(t, goldenPath, got)
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run `go test ./conformance -update` to create it): %v", err)
			}
			if got != string(want) {
				t.Errorf("trace mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", sc.Name, got, want)
			}
		})
	}
}

// TestMetamorphic checks that scenarios sharing an EquivClass reach the same
// effect projection despite different control-flow shapes — a correctness oracle
// that needs no reference engine.
func TestMetamorphic(t *testing.T) {
	byClass := map[string][]Scenario{}
	for _, sc := range Scenarios {
		if sc.EquivClass != "" {
			byClass[sc.EquivClass] = append(byClass[sc.EquivClass], sc)
		}
	}
	if len(byClass) == 0 {
		t.Skip("no metamorphic groups registered")
	}

	for class, members := range byClass {
		class, members := class, members
		t.Run(class, func(t *testing.T) {
			if len(members) < 2 {
				t.Fatalf("equivalence class %q has %d member(s); a metamorphic group needs at least 2", class, len(members))
			}
			var wantEffect, wantFrom string
			for _, sc := range members {
				modelXML, err := sc.load()
				if err != nil {
					t.Fatalf("load %s: %v", sc.Name, err)
				}
				res, err := Run(t.TempDir(), modelXML)
				if err != nil {
					t.Fatalf("run %s: %v", sc.Name, err)
				}
				effect := res.Effect()
				if wantFrom == "" {
					wantEffect, wantFrom = effect, sc.Name
					continue
				}
				if effect != wantEffect {
					t.Errorf("effect mismatch in class %q:\n %s: %s\n %s: %s",
						class, wantFrom, wantEffect, sc.Name, effect)
				}
			}
		})
	}
}

// TestCoverageReportUpToDate keeps COVERAGE.md in sync with the register, the way
// gofmt keeps formatting in sync — the report is generated, never hand-edited.
func TestCoverageReportUpToDate(t *testing.T) {
	got := CoverageReport()
	const path = "COVERAGE.md"
	if *update {
		writeFile(t, path, got)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read COVERAGE.md (run `go test ./conformance -update` to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("COVERAGE.md is stale; run `go test ./conformance -update`")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
