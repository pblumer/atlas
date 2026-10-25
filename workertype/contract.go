package workertype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// TechnicalContractAPIVersion is the first descriptive technical capability
// contract format. It is independent of Worker Type package and job protocol
// versions; this is never consulted by the job processor.
const TechnicalContractAPIVersion = "atlas.io/technical-capability/v1alpha1"

// TechnicalContractKind identifies the semantic contract document.
const TechnicalContractKind = "TechnicalCapabilityContract"

// TechnicalContractJSONSchemaDialect pins embedded schema documents to a
// dialect. This package checks schema roots and local references, not full JSON
// Schema conformance.
const TechnicalContractJSONSchemaDialect = "https://json-schema.org/draft/2020-12/schema"

const (
	maxTechnicalContractBytes = 256 << 10
	maxTechnicalSchemaBytes   = 64 << 10
	maxTechnicalSchemaDepth   = 64
	maxTechnicalResultItems   = 100000
)

var technicalErrorCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// TechnicalContract is a versioned definition of *one* technical capability,
// never proof that a provider implements it or a permission to route a job.
// See ADR-0485 and ADR-0480.
type TechnicalContract struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   ContractMetadata `json:"metadata"`
	Spec       ContractSpec     `json:"spec"`
}

// ContractMetadata is the exact identity of a technical capability contract.
// A new meaning requires a new version or profile, not mutation in place.
type ContractMetadata struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Profile string `json:"profile,omitempty"`
	Name    string `json:"name"`
}

// ContractSpec makes provider-agnostic semantics explicit without creating
// another Worker Type operation registry.
type ContractSpec struct {
	ResourceKind  string          `json:"resourceKind"`
	IdentityScope string          `json:"identityScope"`
	Verb          string          `json:"verb"`
	InputSchema   json.RawMessage `json:"inputSchema"`
	OutputSchema  json.RawMessage `json:"outputSchema"`
	Errors        []ContractError `json:"errors,omitempty"`
	Idempotency   string          `json:"idempotency"`
	Consistency   string          `json:"consistency"`
	Pagination    string          `json:"pagination"`
	MaxResults    int             `json:"maxResults,omitempty"`
}

// ContractError describes a stable semantic outcome, not a raw vendor code.
type ContractError struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	Retryable   bool   `json:"retryable"`
}

// ParseTechnicalContractJSON decodes and validates one contract document at
// design time. Unknown envelope fields and multiple JSON values are refused.
// Successful parsing does not establish provider or JSON Schema conformance.
func ParseTechnicalContractJSON(raw []byte) (TechnicalContract, error) {
	if len(raw) > maxTechnicalContractBytes {
		return TechnicalContract{}, fmt.Errorf("technical capability contract exceeds %d bytes", maxTechnicalContractBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c TechnicalContract
	if err := dec.Decode(&c); err != nil {
		return TechnicalContract{}, fmt.Errorf("decode technical capability contract: %w", err)
	}
	if err := ensureTechnicalContractEOF(dec); err != nil {
		return TechnicalContract{}, err
	}
	if err := c.Validate(); err != nil {
		return TechnicalContract{}, err
	}
	return c, nil
}

func ensureTechnicalContractEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("decode technical capability contract: multiple JSON values")
	}
	return fmt.Errorf("decode technical capability contract: %w", err)
}

// Validate checks an independently versioned technical capability contract.
// It does not interpret any provider call, perform I/O, or validate every JSON
// Schema keyword; a separate conformance suite must do that before publication.
func (c TechnicalContract) Validate() error {
	if c.APIVersion != TechnicalContractAPIVersion {
		return fmt.Errorf("apiVersion must be %q", TechnicalContractAPIVersion)
	}
	if c.Kind != TechnicalContractKind {
		return fmt.Errorf("kind must be %q", TechnicalContractKind)
	}
	if !workerTypeIDPattern.MatchString(c.Metadata.ID) {
		return fmt.Errorf("metadata.id %q must be a lower-case dotted identifier", c.Metadata.ID)
	}
	if _, err := parseVersion(c.Metadata.Version); err != nil {
		return fmt.Errorf("metadata.version: %w", err)
	}
	if c.Metadata.Profile != "" && !capabilityProfilePattern.MatchString(c.Metadata.Profile) {
		return fmt.Errorf("metadata.profile %q must be lower-case kebab-case", c.Metadata.Profile)
	}
	if strings.TrimSpace(c.Metadata.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	for _, field := range []struct{ label, value string }{
		{"resourceKind", c.Spec.ResourceKind},
		{"identityScope", c.Spec.IdentityScope},
		{"verb", c.Spec.Verb},
	} {
		if !capabilityProfilePattern.MatchString(field.value) {
			return fmt.Errorf("spec.%s %q must be lower-case kebab-case", field.label, field.value)
		}
	}
	if err := validateTechnicalSchema("inputSchema", c.Spec.InputSchema); err != nil {
		return err
	}
	if err := validateTechnicalSchema("outputSchema", c.Spec.OutputSchema); err != nil {
		return err
	}
	switch c.Spec.Idempotency {
	case "read-only", "idempotent", "conditional", "non-idempotent", "unknown":
	default:
		return fmt.Errorf("spec.idempotency %q is unsupported", c.Spec.Idempotency)
	}
	switch c.Spec.Consistency {
	case "strong", "eventual", "provider-defined", "unknown":
	default:
		return fmt.Errorf("spec.consistency %q is unsupported", c.Spec.Consistency)
	}
	switch c.Spec.Pagination {
	case "none":
		if c.Spec.MaxResults != 0 {
			return fmt.Errorf("spec.maxResults requires pagination")
		}
	case "cursor", "offset", "provider-defined":
		if c.Spec.MaxResults <= 0 || c.Spec.MaxResults > maxTechnicalResultItems {
			return fmt.Errorf("spec.maxResults must be between 1 and %d for a paginated operation", maxTechnicalResultItems)
		}
	default:
		return fmt.Errorf("spec.pagination %q is unsupported", c.Spec.Pagination)
	}
	seen := make(map[string]struct{}, len(c.Spec.Errors))
	for _, e := range c.Spec.Errors {
		if !technicalErrorCodePattern.MatchString(e.Code) || strings.TrimSpace(e.Description) == "" {
			return fmt.Errorf("spec.errors must have upper-case codes and non-empty descriptions")
		}
		if _, exists := seen[e.Code]; exists {
			return fmt.Errorf("spec.errors contains duplicate code %q", e.Code)
		}
		seen[e.Code] = struct{}{}
	}
	return nil
}

func validateTechnicalSchema(field string, raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > maxTechnicalSchemaBytes {
		return fmt.Errorf("spec.%s must be a non-empty JSON Schema of at most %d bytes", field, maxTechnicalSchemaBytes)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("spec.%s: %w", field, err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("spec.%s must be a JSON Schema object", field)
	}
	if root["$schema"] != TechnicalContractJSONSchemaDialect {
		return fmt.Errorf("spec.%s must use JSON Schema Draft 2020-12", field)
	}
	if root["type"] != "object" && root["type"] != "null" {
		return fmt.Errorf("spec.%s root type must be object or null", field)
	}
	if err := checkLocalSchemaReferences(value, 0); err != nil {
		return fmt.Errorf("spec.%s: %w", field, err)
	}
	return nil
}

func checkLocalSchemaReferences(v any, depth int) error {
	if depth > maxTechnicalSchemaDepth {
		return fmt.Errorf("JSON Schema exceeds nesting depth %d", maxTechnicalSchemaDepth)
	}
	switch node := v.(type) {
	case map[string]any:
		for k, child := range node {
			if k == "$ref" || k == "$dynamicRef" {
				ref, ok := child.(string)
				if !ok || (ref != "#" && !strings.HasPrefix(ref, "#/$defs/")) {
					return fmt.Errorf("%s %v must be a local #/$defs/ reference", k, child)
				}
			}
			if err := checkLocalSchemaReferences(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range node {
			if err := checkLocalSchemaReferences(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateTechnicalContracts refuses duplicate contract identities in a
// design-time collection; it neither certifies claims nor selects Workers.
func ValidateTechnicalContracts(contracts []TechnicalContract) error {
	seen := make(map[string]struct{}, len(contracts))
	for _, c := range contracts {
		if err := c.Validate(); err != nil {
			return err
		}
		key := c.Metadata.ID + "\x00" + c.Metadata.Version + "\x00" + c.Metadata.Profile
		if _, found := seen[key]; found {
			return fmt.Errorf("duplicate technical capability contract %q version %q profile %q", c.Metadata.ID, c.Metadata.Version, c.Metadata.Profile)
		}
		seen[key] = struct{}{}
	}
	return nil
}
