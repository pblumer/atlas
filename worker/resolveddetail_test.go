package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/mail"
)

// A worker runs what the engine resolved for it, and only that. The two halves ship
// separately (ADR-0157/0168), so they can disagree about a field's type — an engine one
// version ahead writes a detail this worker's struct will not accept.
//
// What must not happen then is a task built out of whatever did parse: a mail with no
// recipients, a REST call to an empty URL, a script with no source. Each runner refuses
// the job and names the kind that could not read it, so the failure points at the skew
// rather than at the far system.
func TestAResolvedDetailThisWorkerCannotReadFailsTheJobByName(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		field string // a field this kind reads as a string, sent as a number
		run   func(Job) (map[string]any, error)
	}{
		{"mail", "subject", func(j Job) (map[string]any, error) {
			return RunMailJob(context.Background(), j, mail.NewRegistry())
		}},
		{"csv", "resultVariable", func(j Job) (map[string]any, error) { return runCSV(context.Background(), j) }},
		{"ldif", "resultVariable", func(j Job) (map[string]any, error) { return runLdif(context.Background(), j) }},
		{"webscrape", "resultVariable", func(j Job) (map[string]any, error) {
			return runWebScrape(context.Background(), j, nil)
		}},
		{"script", "resultVariable", func(j Job) (map[string]any, error) { return runScript(context.Background(), j, nil) }},
		{"rest", "resultVariable", func(j Job) (map[string]any, error) { return runREST(context.Background(), j, nil, nil) }},
		{"ad", "dn", func(j Job) (map[string]any, error) {
			return RunADJob(context.Background(), j, nil, adSecretFromEnv(envMap(nil)), nil)
		}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			// The clients handed in above are nil. That is the second half of the
			// assertion: a runner that got as far as calling one would panic, not error.
			_, err := tc.run(Job{Connector: &ConnectorPayload{
				Kind:   tc.kind,
				Fields: map[string]any{tc.field: 5},
			}})
			if err == nil {
				t.Fatal("a detail this worker cannot read ran anyway")
			}
			if !strings.Contains(err.Error(), tc.kind+":") {
				t.Errorf("error = %v, want it to name the kind that could not read the detail", err)
			}
		})
	}
}
