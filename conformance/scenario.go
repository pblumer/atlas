package conformance

import "embed"

//go:embed models/*.bpmn
var modelsFS embed.FS

// Pattern is a workflow control-flow pattern (van der Aalst et al.) — the
// external yardstick the collection measures its coverage against, so "we cover
// all features" is a claim against a recognized catalog rather than a
// self-defined checklist.
type Pattern struct {
	ID   string // e.g. "WCP-1"
	Name string
}

// Patterns is the subset of control-flow patterns the suite tracks so far. Grow
// it as scenarios reach further into the catalog.
var Patterns = []Pattern{
	{"WCP-1", "Sequence"},
	{"WCP-2", "Parallel Split"},
	{"WCP-3", "Synchronization"},
	{"WCP-4", "Exclusive Choice"},
	{"WCP-5", "Simple Merge"},
}

// Feature is one BPMN execution feature the engine must cover. Patterns lists the
// control-flow patterns it realizes, if any (many features — inline scripts,
// events — are not control-flow patterns and leave it empty).
type Feature struct {
	ID       string
	Name     string
	Patterns []string
}

// Features is the register of execution features. A feature with no covering
// scenario surfaces as a gap in COVERAGE.md — that visibility is the point.
var Features = []Feature{
	{"start-end-event", "None start and end events", nil},
	{"sequence-flow", "Sequence flow between activities", []string{"WCP-1"}},
	{"script-task", "Inline FEEL script task (in-engine, no worker)", nil},
	{"exclusive-gateway", "Data-based exclusive gateway with default flow", []string{"WCP-4", "WCP-5"}},
	{"parallel-gateway", "Parallel fork and synchronizing join", []string{"WCP-2", "WCP-3"}},
}

// Scenario binds a BPMN model to the features it exercises. A non-empty
// EquivClass marks it as one of a metamorphic group: every scenario sharing that
// class must produce the same effect projection (see RunResult.Effect).
type Scenario struct {
	Name       string   // model base name; also the golden file base name
	Model      string   // file under models/
	Features   []string // feature IDs this scenario exercises
	EquivClass string   // non-empty: metamorphic equivalence group
}

// Scenarios is the curated collection. Keep it self-completing (inline scripts,
// no parked tokens) so every run is deterministic; a driver for parking features
// (user/service tasks, timers, messages) is the next extension.
var Scenarios = []Scenario{
	{"sequence", "sequence.bpmn", []string{"start-end-event", "sequence-flow", "script-task"}, ""},
	{"exclusive-gateway", "exclusive-gateway.bpmn", []string{"exclusive-gateway", "script-task"}, ""},
	{"parallel-independent", "parallel-independent.bpmn", []string{"parallel-gateway", "script-task"}, "independent-effects"},
	{"linear-independent", "linear-independent.bpmn", []string{"sequence-flow", "script-task"}, "independent-effects"},
}

// load reads the scenario's embedded BPMN model.
func (s Scenario) load() ([]byte, error) {
	return modelsFS.ReadFile("models/" + s.Model)
}
