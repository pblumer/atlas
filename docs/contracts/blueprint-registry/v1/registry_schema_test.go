package registryv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// No remote resources are needed to compile the proposed static registry schema.
type offlineSchemaLoader struct{}

func (offlineSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external registry schema reference %q is forbidden", url)
}

func testSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile("index.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(offlineSchemaLoader{})
	const location = "https://atlas.invalid/blueprint-registry/v1/index.schema.json"
	if err := c.AddResource(location, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validateFixture(t *testing.T, schema *jsonschema.Schema, raw []byte, wantValid bool) {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	err := schema.Validate(value)
	if (err == nil) != wantValid {
		t.Fatalf("schema validation returned %v; want valid=%v\n%s", err, wantValid, raw)
	}
}

// The fixtures are shape validation only, not evidence of registry signature,
// package integrity, SSRF defence, or permission to publish a Blueprint.
func TestRegistryV1SchemaFixtures(t *testing.T) {
	s := testSchema(t)
	for _, tc := range []struct {
		filename string
		valid    bool
	}{
		{"valid-index.json", true},
		{"reject-path-traversal.json", false},
		{"reject-digest.json", false},
	} {
		t.Run(tc.filename, func(t *testing.T) {
			raw, err := os.ReadFile("examples/" + tc.filename)
			if err != nil {
				t.Fatal(err)
			}
			validateFixture(t, s, raw, tc.valid)
		})
	}
}

func TestRegistryV1SchemaRejectsBadShapeAndLimits(t *testing.T) {
	s := testSchema(t)
	raw, err := os.ReadFile("examples/valid-index.json")
	if err != nil {
		t.Fatal(err)
	}
	// Fresh decoding per case prevents mutations from leaking between tests.
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unsupported schema version", func(v map[string]any) { v["schemaVersion"] = 2 }},
		{"unknown root key", func(v map[string]any) { v["authToken"] = "must-not-appear" }},
		{"too many releases", func(v map[string]any) {
			entry := v["releases"].([]any)[0]
			entries := make([]any, 513)
			for i := range entries {
				entries[i] = entry
			}
			v["releases"] = entries
		}},
		{"missing publisher", func(v map[string]any) { delete(v["releases"].([]any)[0].(map[string]any), "publisherId") }},
		{"unexpected release field", func(v map[string]any) { v["releases"].([]any)[0].(map[string]any)["credentials"] = "secret" }},
		{"oversized compressed package", func(v map[string]any) {
			v["releases"].([]any)[0].(map[string]any)["artifact"].(map[string]any)["sizeBytes"] = 52428801
		}},
		{"missing digest", func(v map[string]any) {
			delete(v["releases"].([]any)[0].(map[string]any)["artifact"].(map[string]any), "digest")
		}},
		{"absolute artifact URL", func(v map[string]any) {
			v["releases"].([]any)[0].(map[string]any)["artifact"].(map[string]any)["path"] = "https://attacker.invalid/p.tar.gz"
		}},
		{"escaped traversal artifact", func(v map[string]any) {
			v["releases"].([]any)[0].(map[string]any)["artifact"].(map[string]any)["path"] = "packages/%2e%2e/p.tar.gz"
		}},
		{"blank license", func(v map[string]any) { v["releases"].([]any)[0].(map[string]any)["license"] = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var v map[string]any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatal(err)
			}
			tc.mutate(v)
			d, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			validateFixture(t, s, d, false)
		})
	}
}

func TestRegistryV1SchemaRejectsMissingFields(t *testing.T) {
	s := testSchema(t)
	for _, field := range []string{"schemaVersion", "registryId", "releases"} {
		t.Run(field, func(t *testing.T) {
			raw, err := os.ReadFile("examples/valid-index.json")
			if err != nil {
				t.Fatal(err)
			}
			var v map[string]any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatal(err)
			}
			delete(v, field)
			encoded, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			validateFixture(t, s, encoded, false)
		})
	}
}
