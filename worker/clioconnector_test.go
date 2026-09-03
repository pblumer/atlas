package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pblumer/atlas/connector/clio"
)

// recordingClioClient stands in for an event store: it records what a resolved job
// asked for and answers what the store would.
type recordingClioClient struct {
	written []clio.Event
	events  []clio.InboundEvent
	state   map[string]any
	queried []string
}

func (c *recordingClioClient) WriteEvent(_ context.Context, e clio.Event) error {
	c.written = append(c.written, e)
	return nil
}

func (c *recordingClioClient) ReadEvents(_ context.Context, _ clio.ReadEventsRequest) ([]clio.InboundEvent, error) {
	return c.events, nil
}

func (c *recordingClioClient) GetState(_ context.Context, _ string, _ string) (map[string]any, error) {
	return c.state, nil
}

func (c *recordingClioClient) Query(_ context.Context, _ string, where string) (any, error) {
	c.queried = append(c.queried, where)
	return c.events, nil
}

func clioJobFrom(t *testing.T, task clio.Job) Job {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	return Job{Connector: &ConnectorPayload{Kind: "clio", Fields: fields}}
}

func clioRegistryWith(client clio.Client) *clio.Registry {
	reg := clio.NewRegistry()
	reg.Register("events", client)
	return reg
}

// A write appends what the engine resolved — the body it built from the task's scope,
// under the idempotency key that de-duplicates a retry — and completes with no
// variables, because a write has nothing to write back.
func TestRunClioJobWritesTheResolvedEvent(t *testing.T) {
	client := &recordingClioClient{}
	vars, err := RunClioJob(context.Background(), clioJobFrom(t, clio.Job{
		Connector: "events", Operation: clio.OpWrite,
		Subject: "/kunden/42", EventType: "kunde.angelegt",
		Data:           map[string]any{"name": "Ada"},
		IdempotencyKey: "77",
	}), clioRegistryWith(client))
	if err != nil {
		t.Fatalf("RunClioJob: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("variables = %v, want none for a write", vars)
	}
	if len(client.written) != 1 {
		t.Fatalf("writes = %d, want 1", len(client.written))
	}
	e := client.written[0]
	if e.Subject != "/kunden/42" || e.Type != "kunde.angelegt" || e.IdempotencyKey != "77" {
		t.Errorf("event = %+v, want the resolved subject, type and idempotency key", e)
	}
	if name, _ := e.Data["name"].(string); name != "Ada" {
		t.Errorf("event data = %v, want the body the engine resolved", e.Data)
	}
}

// A read writes its rows into the task's result variable, in the shape the in-process
// path writes them — four keys per event, decided in clio.Run and not by field tags.
func TestRunClioJobReadsIntoTheResultVariable(t *testing.T) {
	client := &recordingClioClient{events: []clio.InboundEvent{{ID: "e1", Type: "kunde.angelegt", Subject: "/kunden/42"}}}
	vars, err := RunClioJob(context.Background(), clioJobFrom(t, clio.Job{
		Connector: "events", Operation: clio.OpRead, Subject: "/kunden/42", Limit: 10,
		Result: "ereignisse",
	}), clioRegistryWith(client))
	if err != nil {
		t.Fatalf("RunClioJob: %v", err)
	}
	rows, ok := vars["ereignisse"].([]any)
	if !ok {
		t.Fatalf("variables = %#v, want the events under the result variable", vars)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want the one event the store held", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	if row["id"] != "e1" || row["type"] != "kunde.angelegt" {
		t.Errorf("row = %v, want the event's id and type", row)
	}
}

// A query with a predicate runs it; without one it folds the subject's state. Both
// land under the result variable, and the choice is the resolved job's, not the
// worker's.
func TestRunClioJobQueriesOrFoldsState(t *testing.T) {
	client := &recordingClioClient{state: map[string]any{"status": "aktiv"}}
	vars, err := RunClioJob(context.Background(), clioJobFrom(t, clio.Job{
		Connector: "events", Operation: clio.OpQuery, Subject: "/kunden/42", Result: "zustand",
	}), clioRegistryWith(client))
	if err != nil {
		t.Fatalf("RunClioJob: %v", err)
	}
	state, _ := vars["zustand"].(map[string]any)
	if state["status"] != "aktiv" {
		t.Errorf("state = %v, want the folded projection", vars["zustand"])
	}
	if len(client.queried) != 0 {
		t.Errorf("run_query was called with %v, want get_state for a task with no predicate", client.queried)
	}

	client = &recordingClioClient{events: []clio.InboundEvent{{ID: "e1"}}}
	if _, err = RunClioJob(context.Background(), clioJobFrom(t, clio.Job{
		Connector: "events", Operation: clio.OpQuery, Subject: "/kunden/42",
		Query: `type == "kunde.angelegt"`, Result: "treffer",
	}), clioRegistryWith(client)); err != nil {
		t.Fatalf("RunClioJob: %v", err)
	}
	if len(client.queried) != 1 || client.queried[0] != `type == "kunde.angelegt"` {
		t.Errorf("queries = %v, want the authored predicate run", client.queried)
	}
}

// A store the worker does not hold fails the job naming it, rather than completing it
// as if nothing had to happen.
func TestRunClioJobFailsOnAnUnknownStore(t *testing.T) {
	_, err := RunClioJob(context.Background(), clioJobFrom(t, clio.Job{
		Connector: "woanders", Operation: clio.OpRead, Subject: "/x",
	}), clioRegistryWith(&recordingClioClient{}))
	if err == nil {
		t.Fatal("RunClioJob error = nil, want the unresolved store reported")
	}
}

// A named store missing its endpoint is a mistake to report at startup: the operator
// named it, so it is not "unconfigured" but misconfigured.
func TestClioRegistryFromEnvRefusesANamedStoreWithNoEndpoint(t *testing.T) {
	env := map[string]string{"ATLAS_CLIO_CONNECTORS": "events"}
	if _, _, err := clioRegistryFromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("error = nil, want the named store's missing ENDPOINT reported")
	}
	// Nothing named at all is unconfigured, not misconfigured: a nil registry and no
	// error, so a worker serving other kinds still starts.
	reg, names, err := clioRegistryFromEnv(func(string) string { return "" })
	if err != nil || reg != nil || len(names) != 0 {
		t.Fatalf("empty environment = %v/%v/%v, want an unconfigured kind", reg, names, err)
	}
}
