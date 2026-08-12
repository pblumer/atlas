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
	"exclusive-gateway":    "exclusive-gateway.bpmn",
	"parallel-independent": "parallel-independent.bpmn",
	"inclusive-gateway":    "inclusive-gateway.bpmn",
}

func TestDifferential(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found on PATH; the reference engine needs Node")
	}
	if _, err := os.Stat(filepath.Join("reference", "node_modules")); err != nil {
		t.Skip("reference/node_modules missing; run `npm ci` in conformance/differential/reference")
	}

	byName := map[string]conformance.Scenario{}
	for _, sc := range conformance.Scenarios {
		byName[sc.Name] = sc
	}

	for name, refModel := range subset {
		sc, ok := byName[name]
		if !ok {
			t.Fatalf("subset names scenario %q which is not registered", name)
		}
		t.Run(name, func(t *testing.T) {
			atlas := runAtlas(t, sc)
			reference := runReference(t, refModel)
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
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("reference run: %v", err)
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
