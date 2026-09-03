package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/temis"
)

// recordingTemisClient stands in for a decision service: it records what it was asked
// and answers with fixed outputs.
type recordingTemisClient struct {
	decisionID string
	inputs     map[string]any
	outputs    map[string]any
	err        error
}

func (c *recordingTemisClient) Evaluate(_ context.Context, decisionID string, inputs map[string]any) (map[string]any, error) {
	c.decisionID, c.inputs = decisionID, inputs
	return c.outputs, c.err
}

func temisJobFrom(t *testing.T, task temis.Job) Job {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	return Job{Connector: &ConnectorPayload{Kind: "temis", Fields: fields}}
}

func temisRegistryWith(client temis.Client) *temis.Registry {
	reg := temis.NewRegistry()
	reg.Register("rules", client)
	return reg
}

// The outcome has two halves, and this is the whole reason temis needed a wider
// contract than the other twelve kinds: the variables the task writes back, *and* the
// evaluation the engine retains (ADR-0066). A runner returning only the first would
// complete the job and lose the record.
func TestRunTemisJobReportsBothHalvesOfTheOutcome(t *testing.T) {
	client := &recordingTemisClient{outputs: map[string]any{"zins": 1.13}}
	job := temisJobFrom(t, temis.Job{
		Connector: "rules", DecisionID: "Hypothekarzins",
		Inputs: map[string]any{"laufzeit": 10}, Result: "zins",
	})

	out, err := RunTemisJob(context.Background(), job, temisRegistryWith(client))
	if err != nil {
		t.Fatalf("RunTemisJob: %v", err)
	}
	if client.decisionID != "Hypothekarzins" {
		t.Errorf("asked for %q, want the authored decision", client.decisionID)
	}
	if got := client.inputs["laufzeit"]; got != float64(10) && got != 10 {
		t.Errorf("inputs = %#v, want the context the engine built", client.inputs)
	}
	// Half one: a single-output decision reaches the model as a scalar, because that
	// is what a gateway condition on it reads.
	if got := out.Variables["zins"]; got == nil {
		t.Errorf("variables = %#v, want the decision's output under its result variable", out.Variables)
	}
	// Half two: the evaluation, with the inputs echoed as asked rather than rebuilt —
	// they are what *this* evaluation was given, and the engine cannot reconstruct
	// them at completion because the instance has moved on since the lease.
	if out.Decision == nil {
		t.Fatal("no decision reported: the evaluation would be lost and the audit trail would have a hole")
	}
	if out.Decision.DecisionID != "Hypothekarzins" {
		t.Errorf("reported decision = %q, want the one evaluated", out.Decision.DecisionID)
	}
	if out.Decision.Outputs["zins"] != 1.13 {
		t.Errorf("reported outputs = %#v, want the service's answer", out.Decision.Outputs)
	}
	if _, ok := out.Decision.Inputs["laufzeit"]; !ok {
		t.Errorf("reported inputs = %#v, want what the evaluation was actually asked", out.Decision.Inputs)
	}
}

// A decision with no result variable still reports its evaluation. The two halves are
// independent: a model may route on a decision without keeping it, and the record is
// kept regardless — it is how the routing is explained afterwards.
func TestRunTemisJobReportsTheEvaluationEvenWithNoResultVariable(t *testing.T) {
	client := &recordingTemisClient{outputs: map[string]any{"zins": 1.13}}
	job := temisJobFrom(t, temis.Job{Connector: "rules", DecisionID: "Hypothekarzins"})

	out, err := RunTemisJob(context.Background(), job, temisRegistryWith(client))
	if err != nil {
		t.Fatalf("RunTemisJob: %v", err)
	}
	if len(out.Variables) != 0 {
		t.Errorf("variables = %#v, want none: the model kept no result", out.Variables)
	}
	if out.Decision == nil {
		t.Fatal("the evaluation was not reported; a decision nobody stored is still a decision that was made")
	}
}

// A service that fails leaves the job pending rather than completing it with an
// empty decision — the outcome would otherwise record that a decision was made when
// none was.
func TestRunTemisJobFailsWhenTheServiceDoes(t *testing.T) {
	boom := errors.New("decision service unreachable")
	job := temisJobFrom(t, temis.Job{Connector: "rules", DecisionID: "Hypothekarzins", Result: "zins"})

	out, err := RunTemisJob(context.Background(), job, temisRegistryWith(&recordingTemisClient{err: boom}))
	if err == nil {
		t.Fatal("a failed evaluation completed the job")
	}
	if out.Decision != nil {
		t.Errorf("decision = %#v, want none: nothing was evaluated", out.Decision)
	}
}

// A job naming a service this worker does not hold says which one, so an operator
// reads a name they can act on rather than a nil dereference.
func TestRunTemisJobNamesAServiceItDoesNotHold(t *testing.T) {
	job := temisJobFrom(t, temis.Job{Connector: "anderswo", DecisionID: "Hypothekarzins"})

	_, err := RunTemisJob(context.Background(), job, temisRegistryWith(&recordingTemisClient{}))
	if err == nil {
		t.Fatal("a job naming an unknown service was accepted")
	}
	if !strings.Contains(err.Error(), "anderswo") {
		t.Errorf("error = %v, want it to name the service the job asked for", err)
	}
}

// The two refusals before any service is reached.
func TestRunTemisJobRefusesWhatItCannotAct(t *testing.T) {
	if _, err := RunTemisJob(context.Background(), Job{}, nil); err == nil {
		t.Fatal("a job with no resolved detail was accepted")
	}
	job := Job{Connector: &ConnectorPayload{Kind: "temis", Fields: map[string]any{
		"connector": "rules", "decisionId": []any{"not", "a", "string"},
	}}}
	_, err := RunTemisJob(context.Background(), job, temisRegistryWith(&recordingTemisClient{}))
	if err == nil {
		t.Fatal("a payload with a mistyped field was accepted")
	}
	if !strings.Contains(err.Error(), "cannot read the resolved detail") {
		t.Errorf("error = %v, want it to say the resolved detail could not be read", err)
	}
}

// The registry this worker builds from its own environment: the names it was given,
// each with a URL, and a token only where one is set — a decision service reachable
// without one is still served (clio's rule, not Remedy's).
func TestTemisRegistryFromEnv(t *testing.T) {
	reg, names, err := temisRegistryFromEnv(envFrom(map[string]string{
		"ATLAS_TEMIS_CONNECTORS":  "rules, offen",
		"ATLAS_TEMIS_RULES_URL":   "https://rules.example.com",
		"ATLAS_TEMIS_RULES_TOKEN": "s3cr3t",
		"ATLAS_TEMIS_OFFEN_URL":   "http://localhost:9000",
	}))
	if err != nil {
		t.Fatalf("temisRegistryFromEnv: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("names = %v, want both services", names)
	}
	for _, name := range names {
		if _, ok := reg.Client(name); !ok {
			t.Errorf("service %q was listed but not registered", name)
		}
	}
}

// A worker told to serve temis and given no services is unconfigured, not broken: it
// simply does not subscribe, so its other kinds keep working.
func TestTemisRegistryFromEnvWithNothingConfigured(t *testing.T) {
	reg, names, err := temisRegistryFromEnv(envFrom(nil))
	if err != nil || reg != nil || len(names) != 0 {
		t.Errorf("reg=%v names=%v err=%v, want an unconfigured kind rather than an error", reg, names, err)
	}
}

// A service an operator *named* and left without a URL is a mistake to report at
// startup, while the operator is still watching — not a queue to lease work from and
// then fail one job at a time.
func TestTemisRegistryFromEnvRefusesANamedServiceWithNoURL(t *testing.T) {
	_, _, err := temisRegistryFromEnv(envFrom(map[string]string{"ATLAS_TEMIS_CONNECTORS": "rules"}))
	if err == nil {
		t.Fatal("a named service with no URL was accepted")
	}
	if !strings.Contains(err.Error(), "ATLAS_TEMIS_RULES_URL") {
		t.Errorf("error = %v, want it to name the variable an operator must set", err)
	}
}
