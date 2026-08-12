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
	{"WCP-6", "Multi-Choice"},
	{"WCP-7", "Structured Synchronizing Merge"},
	{"WCP-16", "Deferred Choice"},
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
	{"receive-task", "Receive task (message wait as an activity)", nil},
	{"boundary-timer-interrupting", "Interrupting boundary timer event", nil},
	{"boundary-message-noninterrupting", "Non-interrupting boundary message event", nil},
	{"event-based-gateway", "Event-based gateway (deferred choice)", []string{"WCP-16"}},
	{"message-start", "Message start event", nil},
	{"timer-start", "Timer start event", nil},
	{"incident", "Job failure raises an incident; resolve resumes it", nil},
	{"boundary-error", "Interrupting boundary error event", nil},
	{"signal", "Signal throw and catch (1:n broadcast)", nil},
	{"embedded-subprocess", "Embedded subprocess", nil},
	{"multi-instance", "Parallel multi-instance activity with output collection", nil},
	{"multi-instance-sequential", "Sequential multi-instance activity", nil},
	{"call-activity", "Call activity invoking a child process", nil},
	{"compensation", "Compensation via a boundary and a compensation throw", nil},
	{"signal-boundary", "Interrupting boundary signal event", nil},
	{"inclusive-gateway", "Inclusive (OR) gateway split and synchronizing join", []string{"WCP-6", "WCP-7"}},
	{"signal-start", "Signal start event (broadcast births an instance)", nil},
	{"data-object", "First-class data object: output/input associations and data state", nil},
	{"field-level-data-object", "Field-level data-object writes (accrue members)", nil},
	{"collection-data-object", "Collection data object (isCollection list)", nil},
}

// Scenario binds a BPMN model to the features it exercises, how its instance is
// born, and the driver steps that carry it through any parked waits. A non-empty
// EquivClass marks it as one of a metamorphic group: every scenario sharing that
// class must produce the same effect projection (see RunResult.Effect).
type Scenario struct {
	Name       string   // model base name; also the golden file base name
	Model      string   // file under models/ (several scenarios may share one model)
	Features   []string // feature IDs this scenario exercises
	EquivClass string   // non-empty: metamorphic equivalence group
	Root       string   // BPMN id of the process to instantiate; "" = the sole process
	Start      Start    // how the instance is born; zero value = explicit CreateInstance
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
	{Name: "receive-task", Model: "receive-task.bpmn", Features: []string{"receive-task"},
		Driver: []Step{Publish("reply", "K")}},
	{Name: "boundary-timer-interrupting", Model: "boundary-timer-interrupting.bpmn", Features: []string{"boundary-timer-interrupting"},
		Driver: []Step{Wait(31 * time.Second)}},
	{Name: "boundary-message-noninterrupting", Model: "boundary-message-noninterrupting.bpmn", Features: []string{"boundary-message-noninterrupting"},
		Driver: []Step{Publish("ping", "K"), Complete("review")}},

	// Event-based gateway: one model, two races — the driver decides the winner.
	{Name: "event-gateway-message", Model: "event-based-gateway.bpmn", Features: []string{"event-based-gateway"},
		Driver: []Step{Publish("go", "")}},
	{Name: "event-gateway-timer", Model: "event-based-gateway.bpmn", Features: []string{"event-based-gateway"},
		Driver: []Step{Wait(31 * time.Second)}},

	// Start events: the instance is born from a trigger, not CreateInstance.
	{Name: "message-start", Model: "message-start.bpmn", Features: []string{"message-start"},
		Start: MessageStart("order-placed", "")},
	{Name: "timer-start", Model: "timer-start.bpmn", Features: []string{"timer-start"},
		Start: TimerStart(31 * time.Second)},

	// Incident lifecycle: fail with no retries (raise), resolve (resume), complete.
	{Name: "incident", Model: "incident.bpmn", Features: []string{"incident"},
		Driver: []Step{Fail("risky", "boom"), Resolve("risky"), Complete("risky")}},

	// Boundary error: the job throws a business error the boundary catches.
	{Name: "boundary-error", Model: "boundary-error.bpmn", Features: []string{"boundary-error"},
		Driver: []Step{ThrowError("call", "BOOM")}},

	// Signal: one branch throws, the other catches — self-completing.
	{Name: "signal-throw-catch", Model: "signal-throw-catch.bpmn", Features: []string{"signal"}},

	// Structure: embedded subprocess and multi-instance (parallel and sequential).
	{Name: "subprocess", Model: "subprocess.bpmn", Features: []string{"embedded-subprocess"}},
	{Name: "multi-instance", Model: "multi-instance.bpmn", Features: []string{"multi-instance"}},
	{Name: "multi-instance-sequential", Model: "multi-instance-sequential.bpmn", Features: []string{"multi-instance-sequential"},
		Driver: []Step{Complete("step"), Complete("step"), Complete("step")}},

	// Call activity: a two-process model; instantiate the parent, child self-completes.
	{Name: "call-activity", Model: "call-activity.bpmn", Features: []string{"call-activity"}, Root: "call-parent"},

	// Compensation: complete the compensable activity, then the throw runs its handler.
	{Name: "compensation", Model: "compensation.bpmn", Features: []string{"compensation"},
		Driver: []Step{Complete("charge"), Complete("refund")}},

	// Signal boundary: a thrown signal interrupts a parked user task — self-completing.
	{Name: "signal-boundary", Model: "signal-boundary.bpmn", Features: []string{"signal-boundary"}},

	// Inclusive (OR) gateway: open several branches by condition, join synchronizes them.
	{Name: "inclusive-gateway", Model: "inclusive-gateway.bpmn", Features: []string{"inclusive-gateway"}},

	// Signal start: instantiate the thrower; its broadcast births the root instance.
	{Name: "signal-start", Model: "signal-start.bpmn", Features: []string{"signal-start"},
		Root: "on-signal", Start: SignalStart("thrower")},

	// Data object: write it via an output association, read it back via an input one.
	{Name: "data-object", Model: "data-object.bpmn", Features: []string{"data-object"}},

	// Field-level data object: accrue members across steps into one structured object.
	{Name: "data-object-fields", Model: "data-object-fields.bpmn", Features: []string{"field-level-data-object"}},

	// Collection data object: a list-valued object round-trips, marked as a collection.
	{Name: "collection-data-object", Model: "collection-data-object.bpmn", Features: []string{"collection-data-object"}},
}

// NegativeModel is a well-formed BPMN model that is nonetheless invalid and must be
// rejected at compile — proving the engine refuses garbage at deploy rather than
// running it into undefined behavior (invariant 5). Reason documents why.
type NegativeModel struct {
	Name   string
	Model  string
	Reason string
}

// NegativeModels is the adversarial half of the collection: each must fail to
// compile. TestNegativeModels asserts it; COVERAGE.md lists them.
var NegativeModels = []NegativeModel{
	{"neg-dangling-flow", "neg-dangling-flow.bpmn", "a sequence flow targets an element that does not exist"},
	{"neg-boundary-bad-host", "neg-boundary-bad-host.bpmn", "a boundary event attaches to a host that does not exist"},
	{"neg-unknown-message", "neg-unknown-message.bpmn", "a receive task references a message that is not declared"},
	{"neg-terminate-end", "neg-terminate-end.bpmn", "a terminate end event is unsupported and must not silently degrade to a plain end"},
}

func (n NegativeModel) load() ([]byte, error) {
	return modelsFS.ReadFile("models/" + n.Model)
}

// load reads the scenario's embedded BPMN model.
func (s Scenario) load() ([]byte, error) {
	return modelsFS.ReadFile("models/" + s.Model)
}
