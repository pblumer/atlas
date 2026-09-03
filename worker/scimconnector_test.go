package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/scim"
)

// recordingScimClient stands in for a SCIM provider: it records the request a
// resolved job produced and answers with a fixed body.
type recordingScimClient struct {
	got  scim.Request
	body any
}

func (c *recordingScimClient) Do(_ context.Context, r scim.Request) (scim.Response, error) {
	c.got = r
	return scim.Response{Status: 200, Body: c.body}, nil
}

func scimJobFrom(t *testing.T, task scim.Job) Job {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	return Job{Connector: &ConnectorPayload{Kind: "scim", Fields: fields}}
}

// The payload carries the authored operation and operands; the worker derives the
// method and URL from them. This is the test that makes that division worth having:
// a create becomes a POST to the collection, and nothing in the payload said "POST".
func TestRunScimJobDerivesTheRequestFromTheAuthoredOperation(t *testing.T) {
	client := &recordingScimClient{body: map[string]any{"id": "42"}}
	job := scimJobFrom(t, scim.Job{
		Operation: "create", BaseURL: "https://idp.example.com/scim/v2", Resource: "Users",
		Body: map[string]any{"userName": "anna"}, IdempotencyKey: "7", Result: "created",
	})

	out, err := runScim(context.Background(), job, client, nil)
	if err != nil {
		t.Fatalf("runScim: %v", err)
	}
	if client.got.Method != "POST" {
		t.Errorf("method = %q, want POST derived from the create operation", client.got.Method)
	}
	if client.got.URL != "https://idp.example.com/scim/v2/Users" {
		t.Errorf("url = %q, want the collection endpoint", client.got.URL)
	}
	if client.got.IdempotencyKey != "7" {
		t.Errorf("idempotency key = %q, want the job key frozen at resolve time", client.got.IdempotencyKey)
	}
	if body, ok := out["created"].(map[string]any); !ok || body["id"] != "42" {
		t.Errorf("created = %#v, want the decoded response as a real object", out["created"])
	}
}

// A get addresses one resource, so the id becomes part of the path — and a get
// without one is refused rather than turned into a list of every user, which is the
// same request with a very different meaning.
func TestRunScimJobAddressesOneResourceAndRefusesAGetWithoutAnId(t *testing.T) {
	client := &recordingScimClient{}
	base := scim.Job{Operation: "get", BaseURL: "https://idp.example.com/scim/v2", Resource: "Users"}

	withID := base
	withID.ResourceID = "42"
	if _, err := runScim(context.Background(), scimJobFrom(t, withID), client, nil); err != nil {
		t.Fatalf("runScim: %v", err)
	}
	if client.got.URL != "https://idp.example.com/scim/v2/Users/42" {
		t.Errorf("url = %q, want the id in the path", client.got.URL)
	}

	client = &recordingScimClient{}
	_, err := runScim(context.Background(), scimJobFrom(t, base), client, nil)
	if err == nil {
		t.Fatal("a get with no resource id was accepted; it would have listed every user")
	}
	if !strings.Contains(err.Error(), "resource id") {
		t.Errorf("error = %v, want it to name the missing id", err)
	}
	if client.got.URL != "" {
		t.Error("the provider was called anyway")
	}
}

// A search sends its filter as the SCIM query parameter rather than in the path.
func TestRunScimJobSendsASearchFilterAsAQueryParameter(t *testing.T) {
	client := &recordingScimClient{body: map[string]any{"Resources": []any{}}}
	job := scimJobFrom(t, scim.Job{
		Operation: "search", BaseURL: "https://idp.example.com/scim/v2", Resource: "Users",
		Filter: `userName eq "anna"`, Result: "found",
	})

	if _, err := runScim(context.Background(), job, client, nil); err != nil {
		t.Fatalf("runScim: %v", err)
	}
	if got := client.got.Query["filter"]; got != `userName eq "anna"` {
		t.Errorf("filter = %q, want the authored filter as the SCIM query parameter", got)
	}
	if client.got.Method != "GET" {
		t.Errorf("method = %q, want GET: a search reads", client.got.Method)
	}
}

// A task naming no result variable completes with nothing rather than an empty
// object — both halves go through scim.Result, so they cannot disagree.
func TestRunScimJobWithoutAResultVariableCompletesWithNothing(t *testing.T) {
	client := &recordingScimClient{body: map[string]any{"id": "42"}}
	job := scimJobFrom(t, scim.Job{
		Operation: "delete", BaseURL: "https://idp.example.com/scim/v2",
		Resource: "Users", ResourceID: "42",
	})

	out, err := runScim(context.Background(), job, client, nil)
	if err != nil {
		t.Fatalf("runScim: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("variables = %#v, want none: the model discards this response", out)
	}
}

// The credential is read from this worker's own environment; the authored auth
// travels encoded, the secret behind its reference does not (ADR-0041/0168).
func TestRunScimJobAuthenticatesWithTheWorkersOwnSecret(t *testing.T) {
	client := &recordingScimClient{}
	secret := scim.SecretResolver(func(ref string) string {
		return map[string]string{"IDP_TOKEN": "s3cr3t"}[ref]
	})
	job := scimJobFrom(t, scim.Job{
		Operation: "search", BaseURL: "https://idp.example.com/scim/v2", Resource: "Users",
		Auth: `{"type":"bearer","secretRef":"IDP_TOKEN"}`,
	})

	if _, err := runScim(context.Background(), job, client, secret); err != nil {
		t.Fatalf("runScim: %v", err)
	}
	if got := client.got.Headers["Authorization"]; got != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want the credential resolved from the worker's environment", got)
	}
}

// A reference nothing answers to fails the job rather than calling unauthenticated:
// a provisioning endpoint that answers an anonymous call with an empty list would
// have a process conclude the user does not exist.
func TestRunScimJobFailsWhenTheReferenceIsUnset(t *testing.T) {
	client := &recordingScimClient{}
	job := scimJobFrom(t, scim.Job{
		Operation: "search", BaseURL: "https://idp.example.com/scim/v2", Resource: "Users",
		Auth: `{"type":"bearer","secretRef":"IDP_TOKEN"}`,
	})

	_, err := runScim(context.Background(), job, client, scim.SecretResolver(func(string) string { return "" }))
	if err == nil {
		t.Fatal("a missing auth secret was accepted; the call would have gone out unauthenticated")
	}
	if !strings.Contains(err.Error(), "IDP_TOKEN") {
		t.Errorf("error = %v, want it to name the reference an operator must configure", err)
	}
	if client.got.URL != "" {
		t.Error("the provider was called anyway; the credential must be resolved before the call")
	}
}

// The two refusals before any provider is reached: a job with no resolved detail
// means this server is not offloading the kind, and a payload with a mistyped field
// is one this worker cannot read — reachable, because a worker leases from whichever
// Atlas is in front of it.
func TestRunScimJobRefusesWhatItCannotAct(t *testing.T) {
	if _, err := runScim(context.Background(), Job{}, &recordingScimClient{}, nil); err == nil {
		t.Fatal("a job with no connector payload was accepted")
	}
	job := Job{Connector: &ConnectorPayload{Kind: "scim", Fields: map[string]any{
		"operation": "search", "baseUrl": "https://idp.example.com/scim/v2", "body": "not an object",
	}}}
	_, err := runScim(context.Background(), job, &recordingScimClient{}, nil)
	if err == nil {
		t.Fatal("a payload with a mistyped field was accepted")
	}
	if !strings.Contains(err.Error(), "cannot read the resolved detail") {
		t.Errorf("error = %v, want it to say the resolved detail could not be read", err)
	}
}
