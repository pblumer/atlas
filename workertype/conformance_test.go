package workertype

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixtureContract(t *testing.T, filename string) TechnicalContract {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + filename)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseTechnicalContractJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTechnicalSchemaCompilerChecksFullKeywords(t *testing.T) {
	c := fixtureContract(t, "technical-account-create.json")
	compiled, err := CompileTechnicalContract(c)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		label, direction, raw string
		valid                 bool
	}{
		{"input positive", "input", `{"userName":"alice","enabled":true}`, true},
		{"required", "input", `{"enabled":true}`, false},
		{"wrong type", "input", `{"userName":"alice","enabled":"true"}`, false},
		{"extra property", "input", `{"userName":"alice","admin":true}`, false},
		{"output positive", "output", `{"accountId":"A-1"}`, true},
		{"output required", "output", `{}`, false},
		{"output wrong type", "output", `{"accountId":3}`, false},
		{"output extra property", "output", `{"accountId":"1","token":"private"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			var err error
			if tc.direction == "input" {
				err = compiled.ValidateInputJSON([]byte(tc.raw))
			} else {
				err = compiled.ValidateOutputJSON([]byte(tc.raw))
			}
			if (err == nil) != tc.valid {
				t.Fatalf("Validate %s: %v, want valid=%v", tc.raw, err, tc.valid)
			}
		})
	}
}

func TestTechnicalSchemaAccountAndContactRemainDifferent(t *testing.T) {
	account, err := CompileTechnicalContract(fixtureContract(t, "technical-account-create.json"))
	if err != nil {
		t.Fatal(err)
	}
	contact, err := CompileTechnicalContract(fixtureContract(t, "technical-contact-create.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := account.ValidateInputJSON([]byte(`{"displayName":"Alice","email":"a@example.org"}`)); err == nil {
		t.Fatal("contact is not an account")
	}
	if err := contact.ValidateInputJSON([]byte(`{"userName":"alice"}`)); err == nil {
		t.Fatal("account is not a contact")
	}
}

func TestTechnicalSchemaRefusesBrokenOrExternalReferences(t *testing.T) {
	cases := []string{
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":"wrong"}`,
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"x":{"type":"not-a-type"}}}`,
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"x":{"$ref":"#/$defs/missing"}}}`,
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"x":{"$ref":"https://example.org/foreign.json"}}}`,
	}
	for _, schema := range cases {
		c := validTechnicalContract()
		c.Spec.InputSchema = json.RawMessage(schema)
		if _, err := CompileTechnicalContract(c); err == nil {
			t.Fatalf("bad schema accepted: %s", schema)
		}
	}
}

func TestTechnicalSchemaLocalDefsAndNullOutput(t *testing.T) {
	c := validTechnicalContract()
	c.Spec.InputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"name":{"$ref":"#/$defs/n"}},"required":["name"],"$defs":{"n":{"type":"string","minLength":3,"pattern":"^[a-z]+$"}},"additionalProperties":false}`)
	c.Spec.OutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"null"}`)
	s, err := CompileTechnicalContract(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateInputJSON([]byte(`{"name":"alice"}`)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"name":"ab"}`, `{"name":"ALICE"}`, `{"name":"alice","extra":true}`} {
		if err := s.ValidateInputJSON([]byte(bad)); err == nil {
			t.Fatalf("bad input accepted: %s", bad)
		}
	}
	if err := s.ValidateOutputJSON([]byte("null")); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateOutputJSON([]byte("{}")); err == nil {
		t.Fatal("null != object")
	}
}

func TestTechnicalSchemaInvalidInstances(t *testing.T) {
	s, err := CompileTechnicalContract(validTechnicalContract())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"{", `{"email":"x"} {"email":"y"}`, "", strings.Repeat("x", MaxTechnicalSchemaInstanceBytes+1)} {
		if err := s.ValidateInputJSON([]byte(bad)); err == nil {
			t.Fatalf("bad payload (len %d) accepted", len(bad))
		}
	}
}

func TestTechnicalSchemaFixturesAreOnlySchemaChecks(t *testing.T) {
	c := fixtureContract(t, "technical-account-create.json")
	cases := []TechnicalSchemaFixture{
		{Name: "input valid", Direction: "input", Document: json.RawMessage(`{"userName":"alice"}`), Valid: true},
		{Name: "input required", Direction: "input", Document: json.RawMessage(`{"enabled":true}`), Valid: false},
		{Name: "output valid", Direction: "output", Document: json.RawMessage(`{"accountId":"A-1"}`), Valid: true},
		{Name: "output required", Direction: "output", Document: json.RawMessage(`{}`), Valid: false},
	}
	report, err := EvaluateTechnicalSchemaFixtures(c, cases)
	if err != nil {
		t.Fatal(err)
	}
	if !report.SchemaOnly || !report.AllPassed() || len(report.Results) != 4 || report.ContractID != c.Metadata.ID {
		t.Fatalf("report must be schema-only: %+v", report)
	}
	cases[0].Document = json.RawMessage(`{"displayName":"Alice"}`)
	report, err = EvaluateTechnicalSchemaFixtures(c, cases)
	if err != nil {
		t.Fatal(err)
	}
	if report.AllPassed() || report.Results[0].Passed {
		t.Fatalf("false positive: %+v", report)
	}
}

func TestTechnicalSchemaFixturesNeedNegativeAndPositiveCoverage(t *testing.T) {
	c := validTechnicalContract()
	emptyOrMissing := [][]TechnicalSchemaFixture{
		nil,
		{{Name: "only positive", Direction: "input", Document: json.RawMessage(`{}`), Valid: true}},
		{
			{Name: "i+", Direction: "input", Document: json.RawMessage(`{}`), Valid: true},
			{Name: "i-", Direction: "input", Document: json.RawMessage(`[]`), Valid: false},
			{Name: "o+", Direction: "output", Document: json.RawMessage(`{}`), Valid: true},
		},
		{
			{Name: "i+", Direction: "input", Document: json.RawMessage(`{}`), Valid: true},
			{Name: "i-", Direction: "input", Document: json.RawMessage("{"), Valid: false},
			{Name: "o+", Direction: "output", Document: json.RawMessage(`{}`), Valid: true},
			{Name: "o-", Direction: "output", Document: json.RawMessage(`[]`), Valid: false},
		},
	}
	for _, fixtures := range emptyOrMissing {
		if _, err := EvaluateTechnicalSchemaFixtures(c, fixtures); err == nil {
			t.Fatal("missing or malformed fixture coverage must fail")
		}
	}
}

// TestTechnicalSchemaLoaderRefusesEmbeddedForeignMetaschemas reaches the
// offline loader itself: Validate checks only $ref and the root $schema, so an
// embedded resource naming its own metaschema is refused by the loader, never
// fetched over the network or read from disk.
func TestTechnicalSchemaLoaderRefusesEmbeddedForeignMetaschemas(t *testing.T) {
	for _, meta := range []string{"https://external.example/meta.json", "file:///etc/passwd"} {
		c := validTechnicalContract()
		c.Spec.InputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"x":{"$ref":"#/$defs/x"}},"$defs":{"x":{"$id":"https://external.example/x.json","$schema":"` + meta + `","type":"string"}}}`)
		if err := c.Validate(); err != nil {
			t.Fatalf("envelope must leave this case to the loader: %v", err)
		}
		_, err := CompileTechnicalContract(c)
		if err == nil || !strings.Contains(err.Error(), "is disabled") {
			t.Fatalf("metaschema %s: want offline loader refusal, got %v", meta, err)
		}
	}
}

func TestTechnicalSchemaInstanceRejectsInvalidUTF8(t *testing.T) {
	compiled, err := CompileTechnicalContract(validTechnicalContract())
	if err != nil {
		t.Fatal(err)
	}
	err = compiled.ValidateInputJSON([]byte("{\"name\":\"a\xffb\"}"))
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid UTF-8 instance: want UTF-8 refusal, got %v", err)
	}
}
