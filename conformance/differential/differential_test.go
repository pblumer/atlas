//go:build differential

// Live cross-engine differential. Opt-in — run with:
//
//	cd conformance/differential/reference && npm ci && cd -
//	go test -tags differential ./conformance/differential/
//
// It runs each translated scenario on Atlas and on the reference bpmn-engine and
// asserts their normalized projections agree. Kept behind the build tag so the
// default `go test ./...` and the coverage gate never need Node.
package differential

import (
	"encoding/json"
	"sort"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/conformance"
)

// subset maps an Atlas scenario name to its reference model file under
// reference/models/. Only pure control-flow scenarios are translated so far — the
// executable dialect is not portable, so each reference model is a hand-translation
// that must keep the same element ids and control flow.
var subset = map[string]string{
	"sequence":             "sequence.bpmn",
	"linear-independent":   "linear-independent.bpmn",
	"exclusive-gateway":    "exclusive-gateway.bpmn",
	"parallel-independent": "parallel-independent.bpmn",
	"inclusive-gateway":    "inclusive-gateway.bpmn",
}

func TestDifferential(t *testing.T) {
	required := os.Getenv("ATLAS_DIFFERENTIAL_REQUIRED") == "1"
	if _, err := exec.LookPath("node"); err != nil {
		if required {
			t.Fatalf("CON4 independent reference is required: node missing: %v", err)
		}
		t.Skip("node not found on PATH; install Node to run the reference")
	}
	if _, err := os.Stat(filepath.Join("reference", "node_modules")); err != nil {
		if required {
			t.Fatalf("CON4 independent reference is required: npm dependencies absent: %v", err)
		}
		t.Skip("reference/node_modules missing; run npm ci in conformance/differential/reference")
	}

	byName := map[string]conformance.Scenario{}
	for _, sc := range conformance.Scenarios {
		byName[sc.Name] = sc
	}

	names := make([]string, 0, len(subset))
	for name := range subset {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		refModel := subset[name]
		sc, ok := byName[name]
		if !ok {
			t.Fatalf("subset names scenario %q which is not registered", name)
		}
		t.Run(name, func(t *testing.T) {
			atlas := runAtlas(t, sc)
			reference := runReference(t, refModel)
			// Check both the independently executed reference and the portable
			// TCK expected projection; this catches a stale translation even
			// if atlas's own golden happens to change.
			tckFile := filepath.Join("..", "tck", "cases", name, "expected.json")
			data, err := os.ReadFile(tckFile)
			if err != nil {
				t.Fatalf("read portable TCK %s: %v", tckFile, err)
			}
			var expected struct {
				Completed bool     `json:"completed"`
				Activities []string `json:"activities"`
			}
			if err := json.Unmarshal(data, &expected); err != nil {
				t.Fatalf("decode portable TCK %s: %v", tckFile, err)
			}
			portable := Project(expected.Completed, expected.Activities)
			if !reference.Equal(portable) {
				t.Errorf("independent reference disagrees with portable TCK for %s: reference=%+v tck=%+v",
					name, reference, portable)
			}
			if !atlas.Equal(reference) {
				t.Errorf("differential mismatch for %s:\n atlas     = %+v\n reference = %+v", name, atlas, reference)
			}
		})
	}
}

func runAtlas(t *testing.T, sc conformance.Scenario) Projection {
	t.Helper()
	model, err := os.ReadFile(filepath.Join("..", "models", sc.Model))
	if err != nil {
		t.Fatalf("read atlas model: %v", err)
	}
	res, err := conformance.Run(t.TempDir(), model, sc.Root, sc.Start, sc.Driver)
	if err != nil {
		t.Fatalf("atlas run: %v", err)
	}
	return Project(res.State.String() == "completed", res.Path)
}

func runReference(t *testing.T, refModel string) Projection {
	t.Helper()
	cmd := exec.Command("node", "runner.js", filepath.Join("models", refModel))
	cmd.Dir = "reference"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reference run %s: %v, output: %s", refModel, err, out)
	}
	var o struct {
		Completed []string `json:"completed"`
		Ended     bool     `json:"ended"`
	}
	if err := json.Unmarshal(out, &o); err != nil {
		t.Fatalf("parse reference output: %v (%s)", err, out)
	}
	return Project(o.Ended, o.Completed)
}
