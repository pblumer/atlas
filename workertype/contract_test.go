package workertype

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func validTechnicalContract() TechnicalContract {
	return TechnicalContract{
		APIVersion: TechnicalContractAPIVersion,
		Kind:       TechnicalContractKind,
		Metadata: ContractMetadata{
			ID: "io.atlas.contact.record.create", Version: "1.0.0",
			Profile: "address-book", Name: "Create address-book contact",
		},
		Spec: ContractSpec{
			ResourceKind:  "contact-record",
			IdentityScope: "address-book",
			Verb:          "create",
			InputSchema:   json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"email":{"type":"string"}}}`),
			OutputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["id"]}`),
			Errors:        []ContractError{{Code: "CONTACT_ALREADY_EXISTS", Description: "Contact exists", Retryable: false}},
			Idempotency:   "non-idempotent",
			Consistency:   "provider-defined",
			Pagination:    "none",
		},
	}
}

func TestTechnicalContractRoundTrip(t *testing.T) {
	want := validTechnicalContract()
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseTechnicalContractJSON(raw)
	if err != nil {
		t.Fatalf("ParseTechnicalContractJSON: %v", err)
	}
	if got.Metadata != want.Metadata || got.Spec.ResourceKind != "contact-record" ||
		got.Spec.Idempotency != "non-idempotent" || len(got.Spec.Errors) != 1 {
		t.Fatalf("unexpected round trip: %+v", got)
	}
	if !strings.Contains(string(got.Spec.InputSchema), "email") {
		t.Fatalf("input schema lost: %s", got.Spec.InputSchema)
	}
}

func TestTechnicalContractValidationRejectsInvalidSemantics(t *testing.T) {
	tests := []struct {
		name string
		edit func(*TechnicalContract)
		want string
	}{
		{"api version", func(c *TechnicalContract) { c.APIVersion = "atlas.io/v1" }, "apiVersion"},
		{"kind", func(c *TechnicalContract) { c.Kind = "WorkerType" }, "kind"},
		{"identity", func(c *TechnicalContract) { c.Metadata.ID = "create contact" }, "metadata.id"},
		{"version", func(c *TechnicalContract) { c.Metadata.Version = "latest" }, "metadata.version"},
		{"profile", func(c *TechnicalContract) { c.Metadata.Profile = "Bad Profile" }, "metadata.profile"},
		{"name", func(c *TechnicalContract) { c.Metadata.Name = " " }, "metadata.name"},
		{"resource", func(c *TechnicalContract) { c.Spec.ResourceKind = "user account" }, "resourceKind"},
		{"scope", func(c *TechnicalContract) { c.Spec.IdentityScope = "" }, "identityScope"},
		{"verb", func(c *TechnicalContract) { c.Spec.Verb = "CREATE" }, "verb"},
		{"input missing", func(c *TechnicalContract) { c.Spec.InputSchema = nil }, "inputSchema"},
		{"input malformed", func(c *TechnicalContract) { c.Spec.InputSchema = json.RawMessage(`{"$schema":`) }, "inputSchema"},
		{"input dialect", func(c *TechnicalContract) { c.Spec.InputSchema = json.RawMessage(`{"type":"object"}`) }, "inputSchema"},
		{"output root", func(c *TechnicalContract) {
			c.Spec.OutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"integer"}`)
		}, "outputSchema"},
		{"external reference", func(c *TechnicalContract) {
			c.Spec.InputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"id":{"$ref":"https://evil.example/schema.json"}}}`)
		}, "$ref"},
		{"idempotency", func(c *TechnicalContract) { c.Spec.Idempotency = "exactly-once" }, "idempotency"},
		{"consistency", func(c *TechnicalContract) { c.Spec.Consistency = "magic" }, "consistency"},
		{"pagination", func(c *TechnicalContract) { c.Spec.Pagination = "all-at-once" }, "pagination"},
		{"missing page limit", func(c *TechnicalContract) { c.Spec.Pagination = "cursor" }, "maxResults"},
		{"unneeded page limit", func(c *TechnicalContract) { c.Spec.MaxResults = 2 }, "maxResults"},
		{"too many results", func(c *TechnicalContract) { c.Spec.Pagination = "offset"; c.Spec.MaxResults = 100001 }, "maxResults"},
		{"bad error code", func(c *TechnicalContract) { c.Spec.Errors[0].Code = "not found" }, "errors"},
		{"missing error meaning", func(c *TechnicalContract) { c.Spec.Errors[0].Description = "" }, "errors"},
		{"duplicate errors", func(c *TechnicalContract) {
			c.Spec.Errors = append(c.Spec.Errors, c.Spec.Errors[0])
		}, "duplicate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validTechnicalContract()
			tc.edit(&c)
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTechnicalContractAcceptsLocalSchemaReferencesAndNullOutput(t *testing.T) {
	c := validTechnicalContract()
	c.Spec.InputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"id":{"$ref":"#/$defs/id"}},"$defs":{"id":{"type":"string"}}}`)
	c.Spec.OutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"null"}`)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTechnicalContractParserIsStrict(t *testing.T) {
	raw, err := json.Marshal(validTechnicalContract())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, json, want string }{
		{"unknown property", strings.Replace(string(raw), "\"kind\":", "\"untrusted\":true,\"kind\":", 1), "unknown field"},
		{"extra JSON", string(raw) + " {}", "multiple JSON values"},
		{"invalid JSON", "{", "decode technical capability contract"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseTechnicalContractJSON([]byte(tc.json)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseTechnicalContractJSON() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTechnicalContractsRefuseDuplicateIdentity(t *testing.T) {
	a := validTechnicalContract()
	b := validTechnicalContract()
	if err := ValidateTechnicalContracts([]TechnicalContract{a, b}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate contract identity must be refused: %v", err)
	}
	b.Metadata.Profile = "another-profile"
	if err := ValidateTechnicalContracts([]TechnicalContract{a, b}); err != nil {
		t.Fatalf("different profiles have different identities: %v", err)
	}
}

func TestTechnicalContractDoesNotInferAccountFromContact(t *testing.T) {
	c := validTechnicalContract()
	if c.Spec.ResourceKind == "account" {
		t.Fatal("a contact-record contract must not be treated as an account")
	}
	// A contract description is not a Worker Type claim, an authorization
	// decision or a provider conformance certificate.
}

func TestTechnicalContractFixturesAreDistinctResourceKinds(t *testing.T) {
	paths := []string{"testdata/technical-contact-create.json", "testdata/technical-account-create.json"}
	var contracts []TechnicalContract
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		contract, err := ParseTechnicalContractJSON(raw)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		contracts = append(contracts, contract)
	}
	if err := ValidateTechnicalContracts(contracts); err != nil {
		t.Fatalf("sample contracts are not a valid distinct set: %v", err)
	}
	if contracts[0].Spec.ResourceKind != "contact-record" || contracts[1].Spec.ResourceKind != "account" {
		t.Fatalf("creation verbs must not erase distinct resource semantics: %+v, %+v", contracts[0].Spec, contracts[1].Spec)
	}
}
