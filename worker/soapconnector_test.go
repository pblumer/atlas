package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/soap"
)

// recordingSoapClient stands in for a web service: it records the request a resolved
// job produced and answers with a fixed body.
type recordingSoapClient struct {
	got  soap.Request
	body any
}

func (c *recordingSoapClient) Do(_ context.Context, r soap.Request) (soap.Response, error) {
	c.got = r
	return soap.Response{Status: 200, Body: c.body}, nil
}

func soapJobFrom(t *testing.T, task soap.Job) Job {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	return Job{Connector: &ConnectorPayload{Kind: "soap", Fields: fields}}
}

// The call the worker makes is the one the engine resolved — endpoint, SOAPAction,
// version and envelope body all travel — and the response reaches the model under the
// task's result variable as a real object, not a stringified blob.
func TestRunSoapJobCallsWhatTheEngineResolved(t *testing.T) {
	client := &recordingSoapClient{body: map[string]any{"rate": "1.13"}}
	job := soapJobFrom(t, soap.Job{
		Endpoint: "https://example.com/svc", Operation: "GetRate", Action: "urn:GetRate",
		Version: "1.2", Body: "<req/>", Result: "found",
	})

	out, err := runSoap(context.Background(), job, client, nil)
	if err != nil {
		t.Fatalf("runSoap: %v", err)
	}
	if client.got.Endpoint != "https://example.com/svc" || client.got.Action != "urn:GetRate" ||
		client.got.Version != "1.2" || client.got.Body != "<req/>" {
		t.Errorf("request = %+v, want the resolved endpoint, action, version and body", client.got)
	}
	body, ok := out["found"].(map[string]any)
	if !ok || body["rate"] != "1.13" {
		t.Errorf("found = %#v, want the parsed SOAP body as a real object", out["found"])
	}
}

// A task naming no result variable completes with *nothing*. An empty object would be
// a variable the model never asked for, and the in-process path returns none either —
// which is why both halves go through soap.Result.
func TestRunSoapJobWithoutAResultVariableCompletesWithNothing(t *testing.T) {
	client := &recordingSoapClient{body: map[string]any{"rate": "1.13"}}
	job := soapJobFrom(t, soap.Job{Endpoint: "https://example.com/svc", Operation: "Ping", Body: "<p/>"})

	out, err := runSoap(context.Background(), job, client, nil)
	if err != nil {
		t.Fatalf("runSoap: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("variables = %#v, want none: the model discards this response", out)
	}
}

// The credential is read from *this worker's* environment, under the same
// ATLAS_CONNECTOR_<REF>_TOKEN name the engine uses: the authored auth travels
// encoded, the secret behind its reference does not (ADR-0041/0168).
func TestRunSoapJobAuthenticatesWithTheWorkersOwnSecret(t *testing.T) {
	client := &recordingSoapClient{}
	secret := soap.SecretResolver(func(ref string) string {
		return map[string]string{"WSDL_SVC": "s3cr3t"}[ref]
	})
	job := soapJobFrom(t, soap.Job{
		Endpoint: "https://example.com/svc", Operation: "GetRate", Body: "<req/>",
		Auth: `{"type":"bearer","secretRef":"WSDL_SVC"}`,
	})

	if _, err := runSoap(context.Background(), job, client, secret); err != nil {
		t.Fatalf("runSoap: %v", err)
	}
	if got := client.got.Headers["Authorization"]; got != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want the credential resolved from the worker's environment", got)
	}
}

// A reference nothing answers to fails the job rather than calling unauthenticated.
// A service that answers an anonymous call with a 200 and an empty result would be
// the worst possible outcome: a process that carries on with nothing.
func TestRunSoapJobFailsWhenTheReferenceIsUnset(t *testing.T) {
	client := &recordingSoapClient{}
	job := soapJobFrom(t, soap.Job{
		Endpoint: "https://example.com/svc", Operation: "GetRate", Body: "<req/>",
		Auth: `{"type":"bearer","secretRef":"WSDL_SVC"}`,
	})

	_, err := runSoap(context.Background(), job, client, soap.SecretResolver(func(string) string { return "" }))
	if err == nil {
		t.Fatal("a missing auth secret was accepted; the call would have gone out unauthenticated")
	}
	if !strings.Contains(err.Error(), "WSDL_SVC") {
		t.Errorf("error = %v, want it to name the reference an operator must configure", err)
	}
	if client.got.Endpoint != "" {
		t.Error("the service was called anyway; the credential must be resolved before the call")
	}
}

// An endpoint whose FEEL evaluated to empty fails the job saying so, rather than
// being POSTed to an empty URL. It is checked in Run so both halves fail identically.
func TestRunSoapJobRefusesAnEmptyEndpoint(t *testing.T) {
	client := &recordingSoapClient{}
	job := soapJobFrom(t, soap.Job{Operation: "GetRate", Body: "<req/>"})

	if _, err := runSoap(context.Background(), job, client, nil); err == nil {
		t.Fatal("an empty endpoint was accepted")
	}
	if client.got.Operation != "" {
		t.Error("the client was called with no endpoint")
	}
}

// A job that arrives with no resolved detail says so: it means this server is not
// offloading the kind, which is a deployment mistake rather than a service that
// happens to be unreachable.
func TestRunSoapJobWithoutAResolvedDetail(t *testing.T) {
	if _, err := runSoap(context.Background(), Job{}, &recordingSoapClient{}, nil); err == nil {
		t.Fatal("a job with no connector payload was accepted")
	}
}
