package workertype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// MaxConfigurationSchemaBytes bounds an inline configuration schema.
const MaxConfigurationSchemaBytes = 64 << 10

// ConfigurationSchema is a Worker Type's configuration contract (ADR-0535 §4): the JSON
// Schema a configured Worker's settings must satisfy, inline in the manifest under
// spec.configuration. A top-level property marked "writeOnly": true is a secret: a
// configured Worker names a vault entry for it and never holds its value, and only the
// launched instance receives the value.
type ConfigurationSchema struct {
	schema  *jsonschema.Schema
	secrets []string
}

// Configuration compiles the manifest's inline configuration schema. A manifest without
// one has no configuration: nil, nil.
func (m Manifest) Configuration() (*ConfigurationSchema, error) {
	if len(bytes.TrimSpace(m.Spec.Configuration)) == 0 {
		return nil, nil
	}
	return CompileConfiguration(m.Spec.Configuration)
}

// CompileConfiguration compiles a configuration schema offline: no reference may reach
// outside the document.
func CompileConfiguration(raw json.RawMessage) (*ConfigurationSchema, error) {
	if len(raw) > MaxConfigurationSchemaBytes {
		return nil, fmt.Errorf("spec.configuration is larger than %d bytes", MaxConfigurationSchemaBytes)
	}
	var top struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			WriteOnly bool `json:"writeOnly"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("spec.configuration: %w", err)
	}
	if top.Type != "object" {
		return nil, errors.New(`spec.configuration must be a JSON Schema of "type": "object"`)
	}
	schema, err := compileTechnicalSchema(raw, "configuration")
	if err != nil {
		return nil, fmt.Errorf("spec.configuration: %w", err)
	}
	c := &ConfigurationSchema{schema: schema}
	for name, p := range top.Properties {
		if p.WriteOnly {
			c.secrets = append(c.secrets, name)
		}
	}
	sort.Strings(c.secrets)
	return c, nil
}

// Secrets are the top-level properties the schema marks writeOnly, sorted.
func (c *ConfigurationSchema) Secrets() []string { return append([]string(nil), c.secrets...) }

// IsSecret reports whether name is one of the schema's secret properties.
func (c *ConfigurationSchema) IsSecret(name string) bool {
	for _, s := range c.secrets {
		if s == name {
			return true
		}
	}
	return false
}

// Validate checks a complete configuration — secrets included — against the schema.
func (c *ConfigurationSchema) Validate(config map[string]any) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if err := c.schema.Validate(doc); err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	return nil
}
