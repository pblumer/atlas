package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The TCK case format: each scenario is emitted as a self-contained, language-neutral
// case under conformance/tck/ so an engine other than Atlas can run it without reading
// Go. Positive cases carry model.bpmn + case.json (how the instance is born and driven)
// + expected.json (the outcome Atlas produces). Negative cases carry model.bpmn +
// case.json marking that the model must be rejected at compile.
//
// Regenerate with `go test ./conformance -update`; the files are reviewed artifacts,
// and a stale one fails this test the way a stale golden does.

type tckCase struct {
	Name     string   `json:"name"`
	Model    string   `json:"model"`
	Features []string `json:"features"`
	Patterns []string `json:"patterns,omitempty"`
	Root     string   `json:"root,omitempty"`
	Start    Start    `json:"start"`
	Driver   []Step   `json:"driver"`
}

type tckExpected struct {
	Completed   bool              `json:"completed"`
	Activities  []string          `json:"activities"`
	Path        []string          `json:"path"`
	Variables   map[string]string `json:"variables"`
	DataObjects []string          `json:"dataObjects"`
}

type tckNegative struct {
	Name     string `json:"name"`
	Model    string `json:"model"`
	Rejected bool   `json:"rejected"`
	Reason   string `json:"reason"`
}

func TestTCKCasesUpToDate(t *testing.T) {
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
			dir := filepath.Join("tck", "cases", sc.Name)
			caseJSON := mustJSON(t, tckCase{
				Name: sc.Name, Model: sc.Model, Features: sc.Features,
				Patterns: sc.PatternsOf(), Root: sc.Root, Start: sc.Start, Driver: sc.Driver,
			})
			expJSON := mustJSON(t, expectedOf(res))
			checkTCKFile(t, filepath.Join(dir, "model.bpmn"), model)
			checkTCKFile(t, filepath.Join(dir, "case.json"), caseJSON)
			checkTCKFile(t, filepath.Join(dir, "expected.json"), expJSON)
		})
	}

	for _, nm := range NegativeModels {
		nm := nm
		t.Run(nm.Name, func(t *testing.T) {
			model, err := nm.load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			dir := filepath.Join("tck", "negative", nm.Name)
			caseJSON := mustJSON(t, tckNegative{Name: nm.Name, Model: nm.Model, Rejected: true, Reason: nm.Reason})
			checkTCKFile(t, filepath.Join(dir, "model.bpmn"), model)
			checkTCKFile(t, filepath.Join(dir, "case.json"), caseJSON)
		})
	}
}

// TestStepStartMarshalUnknown covers the serializers' defensive default arms, which
// no real scenario reaches.
func TestStepStartMarshalUnknown(t *testing.T) {
	sb, err := json.Marshal(Step{kind: stepKind(99)})
	if err != nil || string(sb) != `{"action":"unknown"}` {
		t.Errorf("Step unknown = %s (%v)", sb, err)
	}
	stb, err := json.Marshal(Start{kind: startKind(99)})
	if err != nil || string(stb) != `{"kind":"unknown"}` {
		t.Errorf("Start unknown = %s (%v)", stb, err)
	}
}

func expectedOf(res RunResult) tckExpected {
	seen := map[string]bool{}
	var activities []string
	for _, a := range res.Path {
		if !seen[a] {
			seen[a] = true
			activities = append(activities, a)
		}
	}
	sort.Strings(activities)
	dataObjects := res.DataObjects
	if dataObjects == nil {
		dataObjects = []string{}
	}
	return tckExpected{
		Completed:   res.State.String() == "completed",
		Activities:  activities,
		Path:        res.Path,
		Variables:   res.Variables,
		DataObjects: dataObjects,
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return append(b, '\n')
}

// checkTCKFile writes the content under -update, else asserts the committed file
// matches — the same reviewed-artifact discipline as the golden traces.
func checkTCKFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if *update {
		writeFile(t, path, string(content))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run `go test ./conformance -update`): %v", path, err)
	}
	if string(want) != string(content) {
		t.Errorf("%s is stale; run `go test ./conformance -update`", path)
	}
}
