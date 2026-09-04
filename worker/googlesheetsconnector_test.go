package worker

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/googlesheets"
)

// recordingSheetsClient stands in for Google: it records the operation a resolved job
// asked for and answers with what the API would.
type recordingSheetsClient struct {
	got    googlesheets.Request
	result any
	err    error
}

func (c *recordingSheetsClient) Do(_ context.Context, req googlesheets.Request) (any, error) {
	c.got = req
	return c.result, c.err
}

// ListFiles is the inbound half of the interface; the outbound worker never calls it.
func (c *recordingSheetsClient) ListFiles(context.Context, googlesheets.FileQuery) ([]map[string]any, error) {
	return nil, nil
}

func sheetsJobFrom(t *testing.T, task googlesheets.Job) Job {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	return Job{Connector: &ConnectorPayload{Kind: "googlesheets", Fields: fields}}
}

func sheetsRegistryWith(name string, client googlesheets.Client) *googlesheets.Registry {
	reg := googlesheets.NewRegistry()
	reg.Register(name, client)
	return reg
}

// sheetsBundle is a well-formed service-account bundle, the shape the engine hands over
// as ATLAS_GOOGLESHEETS_<NAME>_CREDENTIALS. The key is generated rather than pasted
// because NewProviderClient parses it: a placeholder would make these tests pass on a
// handover a real worker refuses.
func sheetsBundle(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	bundle, err := json.Marshal(map[string]string{
		"method":      "serviceAccount",
		"clientEmail": "atlas@x.iam.gserviceaccount.com",
		"privateKey":  string(pemKey),
	})
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	return string(bundle)
}

// The operation performed is the one the engine resolved — spreadsheet, range and the
// already-projected rows all travel — and what Google returned reaches the model under
// the task's result variable as a real object rather than a stringified blob.
func TestRunGoogleSheetsJobPerformsWhatTheEngineResolved(t *testing.T) {
	client := &recordingSheetsClient{result: map[string]any{"updates": map[string]any{"updatedRows": 2}}}
	job := sheetsJobFrom(t, googlesheets.Job{
		Worker:         "acme",
		Operation:      "append-row",
		Spreadsheet:    "1Bxi",
		Range:          "Anträge!A:C",
		Values:         [][]any{{"Anna", "42"}},
		Input:          googlesheets.InputRaw,
		RequestID:      "7",
		ResultVariable: "ergebnis",
	})

	out, err := RunGoogleSheetsJob(context.Background(), job, sheetsRegistryWith("acme", client))
	if err != nil {
		t.Fatalf("RunGoogleSheetsJob: %v", err)
	}
	if client.got.Operation != "append-row" || client.got.Spreadsheet != "1Bxi" || client.got.Range != "Anträge!A:C" {
		t.Errorf("request = %+v, want the resolved operation, spreadsheet and range", client.got)
	}
	// The rows were projected in the engine, where the columns and the FEEL scope are;
	// a worker never sees the shape the model held.
	if len(client.got.Values) != 1 || client.got.Values[0][0] != "Anna" {
		t.Errorf("values = %#v, want the rows the engine projected", client.got.Values)
	}
	if client.got.Input != googlesheets.InputRaw {
		t.Errorf("input = %q, want the mode the compiler decided", client.got.Input)
	}
	// The job key travels as the request id, so a retry after an elapsed lease carries
	// the same id for a downstream de-duplicator.
	if client.got.RequestID != "7" {
		t.Errorf("requestId = %q, want the job key the engine froze at resolve time", client.got.RequestID)
	}
	res, ok := out["ergebnis"].(map[string]any)
	if !ok {
		t.Fatalf("outputs = %#v, want what Google returned under the result variable", out)
	}
	if res["updates"] == nil {
		t.Errorf("result = %#v, want the answer kept whole", res)
	}
}

// A task that discards its answer, and an operation that answers with nothing, both
// complete with no variables — the same distinction the in-process handler makes, so an
// offloaded delete does not write a null where a read would write a value.
func TestRunGoogleSheetsJobWritesNothingWhenThereIsNothingToWrite(t *testing.T) {
	for name, task := range map[string]googlesheets.Job{
		"no result variable": {Worker: "acme", Operation: "clear-range", Spreadsheet: "1B", Range: "A2:F"},
		"nothing returned":   {Worker: "acme", Operation: "delete-sheet", Spreadsheet: "1B", Sheet: "Alt", ResultVariable: "r"},
	} {
		client := &recordingSheetsClient{} // result stays nil
		out, err := RunGoogleSheetsJob(context.Background(), sheetsJobFrom(t, task), sheetsRegistryWith("acme", client))
		if err != nil {
			t.Fatalf("%s: RunGoogleSheetsJob: %v", name, err)
		}
		if len(out) != 0 {
			t.Errorf("%s: outputs = %#v, want none", name, out)
		}
	}
}

// A job that carries no resolved detail is a server that is not offloading this kind,
// and saying so is more use than a nil dereference.
func TestRunGoogleSheetsJobWithoutResolvedDetail(t *testing.T) {
	_, err := RunGoogleSheetsJob(context.Background(), Job{}, googlesheets.NewRegistry())
	if err == nil || !strings.Contains(err.Error(), "offloading") {
		t.Errorf("a job with no detail: want an error naming the cause, got %v", err)
	}
}

// A payload this worker cannot read is a mismatch between engine and worker versions.
// It fails rather than acting on whatever survived the decode.
func TestRunGoogleSheetsJobWithAnUnreadablePayload(t *testing.T) {
	job := Job{Connector: &ConnectorPayload{Kind: "googlesheets", Fields: map[string]any{"values": "not rows"}}}
	if _, err := RunGoogleSheetsJob(context.Background(), job, googlesheets.NewRegistry()); err == nil {
		t.Error("an unreadable payload: want an error, got nil")
	}
}

// The client's failure is the job's failure: it stays pending for a retry and then an
// incident, rather than completing a token on work that did not happen.
func TestRunGoogleSheetsJobPropagatesTheClientError(t *testing.T) {
	client := &recordingSheetsClient{err: errors.New("Google said no")}
	job := sheetsJobFrom(t, googlesheets.Job{Worker: "acme", Operation: "clear-range", Spreadsheet: "1B", Range: "A1"})
	if _, err := RunGoogleSheetsJob(context.Background(), job, sheetsRegistryWith("acme", client)); err == nil {
		t.Error("a failed call: want the error propagated, got nil")
	}
}

// The registry is built from the same variables the engine renders. A name listed with
// a usable bundle becomes an identity this worker holds.
func TestGoogleSheetsRegistryFromEnv(t *testing.T) {
	env := map[string]string{
		"ATLAS_GOOGLESHEETS_CONNECTORS":         "acme, zweite",
		"ATLAS_GOOGLESHEETS_ACME_CREDENTIALS":   sheetsBundle(t),
		"ATLAS_GOOGLESHEETS_ZWEITE_CREDENTIALS": sheetsBundle(t),
		"ATLAS_GOOGLESHEETS_ZWEITE_ENDPOINT":    "https://sheets.internal",
	}
	reg, names, err := googleSheetsRegistryFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("googleSheetsRegistryFromEnv: %v", err)
	}
	if len(names) != 2 || names[0] != "acme" {
		t.Errorf("names = %v, want both identities the engine listed", names)
	}
	for _, n := range names {
		if _, ok := reg.Client(n); !ok {
			t.Errorf("the registry holds no client for %q", n)
		}
	}
}

// Told to serve the kind while holding no identity: unconfigured, not misconfigured.
// A nil registry and no error, so a worker serving other kinds still starts.
func TestGoogleSheetsRegistryFromEnvWithNothingConfigured(t *testing.T) {
	reg, names, err := googleSheetsRegistryFromEnv(func(string) string { return "" })
	if reg != nil || names != nil || err != nil {
		t.Errorf("= %v, %v, %v; want nothing and no error", reg, names, err)
	}
}

// A *named* identity whose bundle does not build is an error at startup, where the
// operator is still watching — not a queue to lease work from and then fail on.
func TestGoogleSheetsRegistryFromEnvRefusesANamedIdentityItCannotBuild(t *testing.T) {
	env := map[string]string{"ATLAS_GOOGLESHEETS_CONNECTORS": "acme"} // no credential
	_, _, err := googleSheetsRegistryFromEnv(func(k string) string { return env[k] })
	if err == nil {
		t.Fatal("a named identity with no bundle: want an error, got nil")
	}
	// The message names the variable to set, because that is the fix.
	if !strings.Contains(err.Error(), "ATLAS_GOOGLESHEETS_ACME_CREDENTIALS") {
		t.Errorf("error %q should name the variable an operator has to set", err)
	}
}

// BuiltinConnectors is what a supervised worker is actually configured through, so the
// kind must reach a handler from the environment alone.
func TestBuiltinConnectorsServesGoogleSheets(t *testing.T) {
	env := map[string]string{
		"ATLAS_GOOGLESHEETS_CONNECTORS":       "acme",
		"ATLAS_GOOGLESHEETS_ACME_CREDENTIALS": sheetsBundle(t),
	}
	built, err := BuiltinConnectors(func(k string) string { return env[k] }, "googlesheets")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := built.Handlers["io.atlas.googlesheets"]; !ok {
		t.Errorf("handlers = %v, want one under the reserved Google Sheets job type", built.Handlers)
	}
	if len(built.Names) != 1 || built.Names[0] != "acme" {
		t.Errorf("names = %v, want the identity the environment listed", built.Names)
	}
}

// Asked for the kind with nothing configured, the worker reports it as unserved rather
// than refusing to start: it very likely serves other kinds, and an identity nobody has
// configured yet must park its tasks rather than take those down.
func TestBuiltinConnectorsReportsGoogleSheetsUnconfigured(t *testing.T) {
	built, err := BuiltinConnectors(func(string) string { return "" }, "googlesheets")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if len(built.Unconfigured) != 1 || built.Unconfigured[0] != "googlesheets" {
		t.Errorf("unconfigured = %v, want the kind reported as unserved", built.Unconfigured)
	}
}
