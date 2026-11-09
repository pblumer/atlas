package workertype

import (
	"encoding/json"
	"strings"
	"testing"
)

const echoConfiguration = `{"type":"object","required":["greeting"],"additionalProperties":false,
"properties":{"greeting":{"type":"string","minLength":1},"token":{"type":"string","writeOnly":true}}}`

func TestConfigurationSchema(t *testing.T) {
	m := validManifest()
	if c, err := m.Configuration(); c != nil || err != nil {
		t.Errorf("no configuration = %v, %v; want none", c, err)
	}
	m.Spec.Configuration = json.RawMessage(echoConfiguration)
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	c, err := m.Configuration()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Secrets(); len(got) != 1 || got[0] != "token" || !c.IsSecret("token") || c.IsSecret("greeting") {
		t.Errorf("secrets = %v", got)
	}
	if err := c.Validate(map[string]any{"greeting": "Hallo", "token": "t"}); err != nil {
		t.Errorf("a valid configuration: %v", err)
	}
	for _, bad := range []map[string]any{{}, {"greeting": ""}, {"greeting": "x", "other": 1}, {"greeting": 3}} {
		if err := c.Validate(bad); err == nil {
			t.Errorf("Validate(%v) = nil, want a refusal", bad)
		}
	}
}

func TestAConfigurationSchemaMustBeAnObjectSchemaThatCompiles(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"type":"string"}`, "type"},
		{`{"type":"object","properties":{"a":{"$ref":"https://example.com/s.json"}}}`, "configuration"},
		{`{"type":"object","properties":{"a":{"type":"nonsense"}}}`, "configuration"},
		{`[`, "configuration"},
		{`{"type":"object","description":"` + strings.Repeat("x", MaxConfigurationSchemaBytes) + `"}`, "larger than"},
	} {
		m := validManifest()
		m.Spec.Configuration = json.RawMessage(tc.raw)
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Validate(%.40s) = %v, want an error naming %q", tc.raw, err, tc.want)
		}
	}
}
