package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/model"
)

// The refusals RunClioJob makes before it reaches an event store, and the mapping
// that decides what a completed clio task actually writes back.

// A job with no resolved detail means this server is not offloading the kind — a
// deployment mistake, not a store that happens to be unreachable, and the message
// says so rather than reporting the instance "" as unconfigured.
func TestRunClioJobWithoutAResolvedDetail(t *testing.T) {
	_, err := RunClioJob(context.Background(), Job{}, nil)
	if err == nil {
		t.Fatal("a job with no connector payload was accepted")
	}
	if !strings.Contains(err.Error(), "offloading the clio kind") {
		t.Errorf("error = %v, want it to name the deployment mistake", err)
	}
}

// A payload whose field carries the wrong JSON type is reported as a payload this
// worker cannot read. Reachable rather than theoretical: a worker leases from
// whichever Atlas is in front of it, so a field whose shape changed between the two
// arrives exactly this way — and a zero Job would ask for the instance "".
func TestRunClioJobRefusesAPayloadItCannotRead(t *testing.T) {
	job := Job{Connector: &ConnectorPayload{Kind: "clio", Fields: map[string]any{
		"connector": "events", "operation": "read", "limit": "all of them",
	}}}

	_, err := RunClioJob(context.Background(), job, clioRegistryWith(&recordingClioClient{}))
	if err == nil {
		t.Fatal("a payload with a mistyped field was accepted")
	}
	if !strings.Contains(err.Error(), "cannot read the resolved detail") {
		t.Errorf("error = %v, want it to say the resolved detail could not be read", err)
	}
}

// An instance this worker does not hold is named, so the Workers view can show it
// among the names served nowhere rather than leaving a task parked with no reason.
func TestRunClioJobNamesAnInstanceItDoesNotHold(t *testing.T) {
	job := clioJobFrom(t, clio.Job{Connector: "somewhere-else", Operation: clio.OpRead})

	_, err := RunClioJob(context.Background(), job, clioRegistryWith(&recordingClioClient{}))
	if err == nil {
		t.Fatal("a job naming an instance this worker does not hold was accepted")
	}
	if !strings.Contains(err.Error(), "somewhere-else") {
		t.Errorf("error = %v, want it to name the instance the job asked for", err)
	}
}

// variableValue decides what a model sees when a clio read or query completes. Each
// stored kind has one right JSON shape, and the one that would be silently wrong is
// a number: handing back a Go float would round a value the engine stored exactly.
func TestVariableValueUnwrapsEachStoredKind(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   model.VariableValue
		want any
	}{
		{"bool", model.VariableValue{Kind: model.VarBool, Bool: true}, true},
		{"string", model.VariableValue{Kind: model.VarString, Text: "hello"}, "hello"},
		{"object", model.VariableValue{Kind: model.VarJSON, Text: `{"a":1}`}, nil}, // checked below
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := variableValue(tc.in)
			if tc.name == "object" {
				obj, ok := got.(map[string]any)
				if !ok || obj["a"] == nil {
					t.Fatalf("object = %#v, want a real map rather than JSON in a string", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("%s = %#v, want %#v", tc.name, got, tc.want)
			}
		})
	}

	// A number crosses as json.Number, not float64: the engine stored the digits the
	// process wrote, and re-parsing them as a float is how a 19-digit id loses its
	// last three.
	got := variableValue(model.VariableValue{Kind: model.VarNumber, Text: "9007199254740993"})
	if s, ok := got.(interface{ String() string }); !ok || s.String() != "9007199254740993" {
		t.Errorf("number = %#v, want the digits preserved exactly", got)
	}

	// Stored JSON that does not parse yields nil rather than a panic or the raw text:
	// the alternative is a model reading a broken value as if it were a string.
	if got := variableValue(model.VariableValue{Kind: model.VarJSON, Text: "{not json"}); got != nil {
		t.Errorf("unparseable JSON = %#v, want nil", got)
	}
}
