package workertype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// MaxTechnicalSchemaInstanceBytes bounds fixture payloads during offline
// conformance checks. Schema compilation and validation are never job hot-path operations.
const MaxTechnicalSchemaInstanceBytes = 1 << 20

const maxTechnicalSchemaFixtures = 256

// CompiledTechnicalContract validates JSON documents against both complete
// Draft 2020-12 schemas. Its results are NOT provider conformance certificates,
// evidence of a verified Worker, or authorization to route a job.
type CompiledTechnicalContract struct {
	input  *jsonschema.Schema
	output *jsonschema.Schema
}

// offlineTechnicalSchemaLoader denies all resolution outside in-memory
// registered resources. Neither HTTP, file, nor other I/O is permitted.
type offlineTechnicalSchemaLoader struct{}

func (offlineTechnicalSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external JSON Schema resource %q is disabled", url)
}

func compileTechnicalSchema(raw json.RawMessage, direction string) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%s schema JSON: %w", direction, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineTechnicalSchemaLoader{})
	compiler.AssertFormat()
	location := "https://atlas.invalid/technical-capability/" + direction + ".json"
	if err := compiler.AddResource(location, doc); err != nil {
		return nil, fmt.Errorf("%s schema registration: %w", direction, err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		return nil, fmt.Errorf("%s schema compilation: %w", direction, err)
	}
	return schema, nil
}

// CompileTechnicalContract compiles both JSON Schemas and verifies keyword
// structure, references and metadata offline. All caller-provided references
// are restricted to the existing envelope's local #/$defs/ convention.
// This is a design-time-only operation, never called from the processor.
func CompileTechnicalContract(contract TechnicalContract) (*CompiledTechnicalContract, error) {
	if err := contract.Validate(); err != nil {
		return nil, err
	}
	input, err := compileTechnicalSchema(contract.Spec.InputSchema, "input")
	if err != nil {
		return nil, err
	}
	output, err := compileTechnicalSchema(contract.Spec.OutputSchema, "output")
	if err != nil {
		return nil, err
	}
	return &CompiledTechnicalContract{input: input, output: output}, nil
}

func decodeTechnicalSchemaInstance(raw []byte) (any, error) {
	if len(raw) == 0 || len(raw) > MaxTechnicalSchemaInstanceBytes {
		return nil, fmt.Errorf("JSON Schema instance must be 1..%d bytes", MaxTechnicalSchemaInstanceBytes)
	}
	// encoding/json would silently replace invalid bytes with U+FFFD and
	// validate a different document than the one supplied.
	if !utf8.Valid(raw) {
		return nil, errors.New("JSON Schema instance is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode schema instance: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("schema instance contains multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode schema instance trailer: %w", err)
	}
	return value, nil
}

// ValidateInputJSON checks one bounded JSON payload against all input schema
// keywords. Success demonstrates a matching JSON shape, NOT provider behavior.
func (compiled *CompiledTechnicalContract) ValidateInputJSON(raw []byte) error {
	value, err := decodeTechnicalSchemaInstance(raw)
	if err != nil {
		return err
	}
	if err := compiled.input.Validate(value); err != nil {
		return fmt.Errorf("input schema: %w", err)
	}
	return nil
}

// ValidateOutputJSON checks one bounded JSON payload against all output schema
// keywords. It is independent of the provider's actual returned errors/effects.
func (compiled *CompiledTechnicalContract) ValidateOutputJSON(raw []byte) error {
	value, err := decodeTechnicalSchemaInstance(raw)
	if err != nil {
		return err
	}
	if err := compiled.output.Validate(value); err != nil {
		return fmt.Errorf("output schema: %w", err)
	}
	return nil
}

// TechnicalSchemaFixture is a fixture for schema validation only. Valid=false
// means the document must be rejected by its declared input/output schema;
// malformed JSON is never a meaningful negative fixture.
type TechnicalSchemaFixture struct {
	Name      string          `json:"name"`
	Direction string          `json:"direction"`
	Document  json.RawMessage `json:"document"`
	Valid     bool            `json:"valid"`
}

// TechnicalSchemaFixtureResult records whether one offline check met the
// expected schema outcome, without claiming implementation conformance.
type TechnicalSchemaFixtureResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// TechnicalSchemaReport is deliberately schema-only. No verification,
// publication, package identity, claim approval or routing eligibility is
// derived from it; these require the separate ADR-0483 review/evidence gates.
type TechnicalSchemaReport struct {
	ContractID      string                         `json:"contractId"`
	ContractVersion string                         `json:"contractVersion"`
	Profile         string                         `json:"profile,omitempty"`
	SchemaOnly      bool                           `json:"schemaOnly"`
	Results         []TechnicalSchemaFixtureResult `json:"results"`
}

// AllPassed reports whether every executed schema fixture matched its
// expectation. It is not a provider verification result.
func (report TechnicalSchemaReport) AllPassed() bool {
	if len(report.Results) == 0 {
		return false
	}
	for _, result := range report.Results {
		if !result.Passed {
			return false
		}
	}
	return true
}

// EvaluateTechnicalSchemaFixtures checks bounded, well-formed positive and
// negative input/output cases. A completed report cannot upgrade a Worker
// Type claim to "verified": that requires independent provider evidence.
func EvaluateTechnicalSchemaFixtures(contract TechnicalContract, fixtures []TechnicalSchemaFixture) (TechnicalSchemaReport, error) {
	if len(fixtures) < 4 || len(fixtures) > maxTechnicalSchemaFixtures {
		return TechnicalSchemaReport{}, fmt.Errorf("schema fixtures must contain 4..%d cases", maxTechnicalSchemaFixtures)
	}
	var coverage [2][2]bool // direction input/output, expected invalid/valid
	names := make(map[string]struct{}, len(fixtures))
	for _, fixture := range fixtures {
		if strings.TrimSpace(fixture.Name) == "" {
			return TechnicalSchemaReport{}, errors.New("schema fixture requires a name")
		}
		if _, exists := names[fixture.Name]; exists {
			return TechnicalSchemaReport{}, fmt.Errorf("duplicate schema fixture %q", fixture.Name)
		}
		names[fixture.Name] = struct{}{}
		direction := 0
		switch fixture.Direction {
		case "input":
		case "output":
			direction = 1
		default:
			return TechnicalSchemaReport{}, fmt.Errorf("fixture %q has unknown direction %q", fixture.Name, fixture.Direction)
		}
		if _, err := decodeTechnicalSchemaInstance(fixture.Document); err != nil {
			return TechnicalSchemaReport{}, fmt.Errorf("fixture %q: %w", fixture.Name, err)
		}
		valid := 0
		if fixture.Valid {
			valid = 1
		}
		coverage[direction][valid] = true
	}
	for direction := range coverage {
		if !coverage[direction][0] || !coverage[direction][1] {
			return TechnicalSchemaReport{}, errors.New("schema fixture set must contain positive and negative input and output cases")
		}
	}
	compiled, err := CompileTechnicalContract(contract)
	if err != nil {
		return TechnicalSchemaReport{}, err
	}
	report := TechnicalSchemaReport{
		ContractID:      contract.Metadata.ID,
		ContractVersion: contract.Metadata.Version,
		Profile:         contract.Metadata.Profile,
		SchemaOnly:      true,
		Results:         make([]TechnicalSchemaFixtureResult, 0, len(fixtures)),
	}
	for _, fixture := range fixtures {
		var failure error
		if fixture.Direction == "input" {
			failure = compiled.ValidateInputJSON(fixture.Document)
		} else {
			failure = compiled.ValidateOutputJSON(fixture.Document)
		}
		result := TechnicalSchemaFixtureResult{
			Name:   fixture.Name,
			Passed: (failure == nil) == fixture.Valid,
		}
		if !result.Passed {
			if failure != nil {
				result.Message = failure.Error()
			} else {
				result.Message = "document unexpectedly satisfied the schema"
			}
		}
		report.Results = append(report.Results, result)
	}
	return report, nil
}
