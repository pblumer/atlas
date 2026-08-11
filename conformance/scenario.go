package conformance

import (
	"embed"
	"time"
)

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
	{"user-task", "User task (human-completed job)", nil},
	{"service-task", "Service task (worker-completed job with outputs)", nil},
	{"message-catch", "Intermediate message catch event", nil},
	{"timer-catch", "Intermediate timer catch event", nil},
}

// Scenario binds a BPMN model to the features it exercises and the driver steps
// that carry it through any parked waits. A non-empty EquivClass marks it as one
// of a metamorphic group: every scenario sharing that class must produce the same
// effect projection (see RunResult.Effect).
type Scenario struct {
	Name       string   // model base name; also the golden file base name
	Model      string   // file under models/
	Features   []string // feature IDs this scenario exercises
	EquivClass string   // non-empty: metamorphic equivalence group
	Driver     []Step   // ordered actions that drive parked tokens; nil = self-completing
}

// Scenarios is the curated collection. Self-completing models (inline scripts)
// carry a nil Driver; models that park a token carry the deterministic steps that
// advance them — a completed job, a delivered message, an elapsed timer.
var Scenarios = []Scenario{
	{Name: "sequence", Model: "sequence.bpmn", Features: []string{"start-end-event", "sequence-flow", "script-task"}},
	{Name: "exclusive-gateway", Model: "exclusive-gateway.bpmn", Features: []string{"exclusive-gateway", "script-task"}},
	{Name: "parallel-independent", Model: "parallel-independent.bpmn", Features: []string{"parallel-gateway", "script-task"}, EquivClass: "independent-effects"},
	{Name: "linear-independent", Model: "linear-independent.bpmn", Features: []string{"sequence-flow", "script-task"}, EquivClass: "independent-effects"},

	// Parked features, driven to completion.
	{Name: "user-task", Model: "user-task.bpmn", Features: []string{"user-task"},
		Driver: []Step{Complete("approve")}},
	{Name: "service-task", Model: "service-task.bpmn", Features: []string{"service-task"},
		Driver: []Step{Complete("charge", Str("status", "captured"))}},
	{Name: "message-catch", Model: "message-catch.bpmn", Features: []string{"message-catch"},
		Driver: []Step{Publish("payment-received", "K")}},
	{Name: "timer-catch", Model: "timer-catch.bpmn", Features: []string{"timer-catch"},
		Driver: []Step{Wait(31 * time.Second)}},
}

// load reads the scenario's embedded BPMN model.
func (s Scenario) load() ([]byte, error) {
	return modelsFS.ReadFile("models/" + s.Model)
}
