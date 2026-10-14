package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestConnectorKindsSayWhyAConfiguredWorkerIsNotWorking: the worker list is where an
// operator learns that a stored record is not in service, before a token parks on it
// (ADR-0158). Every managed kind answers that question through its own live registry,
// so each one is asked here — a kind whose probe nobody exercised is a kind whose
// broken workers would be listed as fine. A deployed process that names no worker is
// listed against none of them.
func TestConnectorKindsSayWhyAConfiguredWorkerIsNotWorking(t *testing.T) {
	srv := newServerForErrors(t)
	forkPathsDeploy(t, srv, dmnRefImpactPathsPlainBPMN)

	recs := []connector{
		{ID: "w-remedy", Name: "itsm", Kind: connectorKindRemedy, Endpoint: "https://remedy.example"},
		{ID: "w-jira", Name: "tickets", Kind: connectorKindJira, Endpoint: "https://jira.example"},
		{ID: "w-sheets", Name: "sheets", Kind: connectorKindGoogleSheets},
		{ID: "w-discord", Name: "chat", Kind: connectorKindDiscord},
		{ID: "w-pg", Name: "warehouse", Kind: connectorKindPostgres},
		{ID: "w-temis", Name: "rules", Kind: connectorKindTemis, Enabled: true},
	}
	want := map[string]string{
		"itsm":      problemDisabled,
		"tickets":   problemDisabled,
		"sheets":    problemDisabled,
		"chat":      problemDisabled,
		"warehouse": "the worker is disabled",
		"rules":     problemNoEndpoint,
	}
	var err error
	srv.do(func() {
		for _, c := range recs {
			if err = srv.connectors.Save(c); err != nil {
				return
			}
		}
		err = srv.rebuildConnectorRegistries()
	})
	if err != nil {
		t.Fatalf("seed workers: %v", err)
	}

	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/connectors", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d (%s)", code, body)
	}
	var rows []struct {
		Name    string         `json:"name"`
		Problem string         `json:"problem"`
		UsedBy  []connectorUse `json:"usedBy"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != len(want) {
		t.Fatalf("listed %d workers, want %d: %s", len(rows), len(want), body)
	}
	for _, r := range rows {
		if r.Problem != want[r.Name] {
			t.Errorf("%s: problem = %q, want %q", r.Name, r.Problem, want[r.Name])
		}
		if len(r.UsedBy) != 0 {
			t.Errorf("%s is listed as used by %+v; the only deployed process names no worker", r.Name, r.UsedBy)
		}
	}
}
