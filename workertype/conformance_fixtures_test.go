package workertype

import (
	"encoding/json"
	"os"
	"testing"
)

func TestTechnicalSchemaFixtureFilesAreExecutableAndSchemaOnly(t *testing.T) {
	for _, kind := range []string{"account", "contact"} {
		t.Run(kind, func(t *testing.T) {
			contract := fixtureContract(t, "technical-"+kind+"-create.json")
			raw, err := os.ReadFile("testdata/technical-" + kind + "-schema-cases.json")
			if err != nil {
				t.Fatal(err)
			}
			var fixtures []TechnicalSchemaFixture
			if err := json.Unmarshal(raw, &fixtures); err != nil {
				t.Fatal(err)
			}
			report, err := EvaluateTechnicalSchemaFixtures(contract, fixtures)
			if err != nil {
				t.Fatal(err)
			}
			if !report.SchemaOnly || !report.AllPassed() || len(report.Results) != len(fixtures) {
				t.Fatalf("fixture report must be complete and schema-only: %+v", report)
			}
		})
	}
}
