package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/sharepoint"
)

// recordingSharePointClient stands in for a Graph site: it records the item a
// resolved job asked to create and answers with what Graph would.
type recordingSharePointClient struct {
	got  sharepoint.ItemRequest
	item any
}

func (c *recordingSharePointClient) CreateItem(_ context.Context, req sharepoint.ItemRequest) (any, error) {
	c.got = req
	return c.item, nil
}

func sharePointJobFrom(t *testing.T, task sharepoint.Job) Job {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	return Job{Connector: &ConnectorPayload{Kind: "sharepoint", Fields: fields}}
}

func sharePointRegistryWith(name string, client sharepoint.Client) *sharepoint.Registry {
	reg := sharepoint.NewRegistry()
	reg.Register(name, client)
	return reg
}

// The item created is the one the engine resolved — site, list and fields all travel
// — and the created item reaches the model under the task's result variable as a real
// object rather than a stringified blob.
func TestRunSharePointJobCreatesWhatTheEngineResolved(t *testing.T) {
	client := &recordingSharePointClient{item: map[string]any{"id": "42"}}
	job := sharePointJobFrom(t, sharepoint.Job{
		Connector: "intranet",
		Site:      "https://contoso.sharepoint.com/sites/hr",
		List:      "Onboarding",
		Fields:    map[string]string{"Title": "Anna"},
		RequestID: "7",
		Result:    "created",
	})

	out, err := RunSharePointJob(context.Background(), job, sharePointRegistryWith("intranet", client))
	if err != nil {
		t.Fatalf("RunSharePointJob: %v", err)
	}
	if client.got.Site != "https://contoso.sharepoint.com/sites/hr" || client.got.List != "Onboarding" ||
		client.got.Fields["Title"] != "Anna" {
		t.Errorf("request = %+v, want the resolved site, list and fields", client.got)
	}
	// The job key travels as the request id, so a retry after an elapsed lease is
	// recognizable to Graph as the same request rather than a second one.
	if client.got.RequestID != "7" {
		t.Errorf("requestId = %q, want the job key the engine froze at resolve time", client.got.RequestID)
	}
	item, ok := out["created"].(map[string]any)
	if !ok || item["id"] != "42" {
		t.Errorf("created = %#v, want the item as a real object", out["created"])
	}
}

// A task naming no result variable completes with *nothing*. An empty object would be
// a variable the model never asked for, and the in-process path returns none either —
// which is why both halves go through sharepoint.Result.
func TestRunSharePointJobWithoutAResultVariableCompletesWithNothing(t *testing.T) {
	client := &recordingSharePointClient{item: map[string]any{"id": "42"}}
	job := sharePointJobFrom(t, sharepoint.Job{Connector: "intranet", Site: "s", List: "l"})

	out, err := RunSharePointJob(context.Background(), job, sharePointRegistryWith("intranet", client))
	if err != nil {
		t.Fatalf("RunSharePointJob: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("variables = %#v, want none: the model discards the created item", out)
	}
}

// A task naming an instance this worker does not hold fails saying which one, rather
// than creating the item somewhere else. The registry's own message is used, so the
// answer is the same wherever the job ran.
func TestRunSharePointJobNamesTheInstanceItCannotResolve(t *testing.T) {
	job := sharePointJobFrom(t, sharepoint.Job{Connector: "extranet", Site: "s", List: "l"})

	_, err := RunSharePointJob(context.Background(), job, sharePointRegistryWith("intranet", &recordingSharePointClient{}))
	if err == nil {
		t.Fatal("a job naming an instance this worker does not hold was accepted")
	}
	if !strings.Contains(err.Error(), "extranet") {
		t.Errorf("error = %v, want it to name the instance the task asked for", err)
	}
}

// The registry is built from this worker's own environment, and the credential
// arrives as one bundle that sharepoint.NewProviderClient parses — so a bundle the
// engine could build a client from builds the identical client here.
func TestSharePointRegistryFromEnvBuildsFromTheBundle(t *testing.T) {
	env := map[string]string{
		"ATLAS_SHAREPOINT_CONNECTORS":           "intranet",
		"ATLAS_SHAREPOINT_INTRANET_CREDENTIALS": `{"method":"clientCredentials","tenantId":"t-1","clientId":"c-1","clientSecret":"s3cr3t"}`,
		"ATLAS_SHAREPOINT_INTRANET_ENDPOINT":    "https://graph.microsoft.com/v1.0",
		"ATLAS_SHAREPOINT_UNLISTED_CREDENTIALS": `{"method":"clientCredentials"}`,
	}
	reg, names, err := sharepointRegistryFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("sharepointRegistryFromEnv: %v", err)
	}
	if len(names) != 1 || names[0] != "intranet" {
		t.Fatalf("names = %v, want only the instance CONNECTORS lists", names)
	}
	if _, ok := reg.Client("intranet"); !ok {
		t.Error("the listed instance was not registered")
	}
	// A variable for an instance CONNECTORS does not name is not an instance. The
	// list is what an operator declares; a stray variable is leftovers.
	if _, ok := reg.Client("unlisted"); ok {
		t.Error("an instance CONNECTORS does not list was registered")
	}
}

// A named instance whose bundle cannot build a client is an error at startup, not a
// queue to lease work from and then fail: the operator named it, so the omission is a
// mistake to report while they are still watching.
func TestSharePointRegistryFromEnvRefusesANamedInstanceItCannotBuild(t *testing.T) {
	env := map[string]string{
		"ATLAS_SHAREPOINT_CONNECTORS":           "intranet",
		"ATLAS_SHAREPOINT_INTRANET_CREDENTIALS": `{"method":"clientCredentials","tenantId":"t-1"}`, // no clientId/secret
	}
	_, _, err := sharepointRegistryFromEnv(func(k string) string { return env[k] })
	if err == nil {
		t.Fatal("an unusable bundle was accepted; its tasks would be leased and then failed")
	}
	if !strings.Contains(err.Error(), "intranet") {
		t.Errorf("error = %v, want it to name the instance an operator must fix", err)
	}
}

// No CONNECTORS is unconfigured, not misconfigured: a nil registry and no error, which
// the caller reports as a kind this worker does not serve.
func TestSharePointRegistryFromEnvIsUnconfiguredWithoutNames(t *testing.T) {
	reg, names, err := sharepointRegistryFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatalf("an unconfigured worker errored: %v", err)
	}
	if reg != nil || len(names) != 0 {
		t.Errorf("registry = %v, names = %v, want nothing held", reg, names)
	}
}

// A job that arrives with no resolved detail says so: it means this server is not
// offloading the kind, which is a deployment mistake rather than a site that happens
// to be unreachable.
func TestRunSharePointJobWithoutAResolvedDetail(t *testing.T) {
	if _, err := RunSharePointJob(context.Background(), Job{}, sharepoint.NewRegistry()); err == nil {
		t.Fatal("a job with no connector payload was accepted")
	}
}

// A payload whose field carries the wrong JSON type is reported as a payload this
// worker cannot read. Reachable for the reason ldap's is: a worker leases from
// whichever Atlas is in front of it, and unmarshalling into a zero Job would ask the
// registry for the instance "" rather than say what went wrong.
func TestRunSharePointJobRefusesAPayloadItCannotRead(t *testing.T) {
	job := Job{Connector: &ConnectorPayload{Kind: "sharepoint", Fields: map[string]any{
		"connector": "intranet", "fields": "not an object",
	}}}

	_, err := RunSharePointJob(context.Background(), job, nil)
	if err == nil {
		t.Fatal("a payload with a mistyped field was accepted")
	}
	if !strings.Contains(err.Error(), "cannot read the resolved detail") {
		t.Errorf("error = %v, want it to say the resolved detail could not be read", err)
	}
}
