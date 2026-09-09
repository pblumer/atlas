package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// plainBPMN is searchableBPMN's predecessor: the same process, declaring nothing. The
// pair is what an operator actually has when they discover the declaration exists —
// running instances on a version that never had it, and a new version that does.
const plainBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="identitaet" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="review" name="Review">
      <extensionElements>
        <zeebe:assignmentDefinition assignee="editor" candidateGroups="reviewers"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="end"/>
  </process>
</definitions>`

// searchInVersion runs one search scoped to a version, which is the only scope the
// value index answers under (the declaration is per definition).
func searchInVersion(t *testing.T, ts *httptest.Server, def uint64, q string) []searchRow {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?process=%d&q=%s", def, url.QueryEscape(q)), "", "")
	if code != http.StatusOK {
		t.Fatalf("search %q: status=%d body=%s", q, code, body)
	}
	var rows []searchRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode %q: %v (%s)", q, err, body)
	}
	return rows
}

// This is the operator's actual sequence, end to end: instances are running on a
// version that declares nothing, a version that declares identityId is deployed, and
// the instances are migrated onto it. The values they already hold were stamped by the
// version that wrote them, so without the migration correcting their membership the
// version-scoped search — which for a declared name is answered from the index alone —
// would find nothing, a wrong answer rather than a slow one (ADR-0244).
func TestMigratedInstancesAreFoundByTheNewDeclaration(t *testing.T) {
	ts := newTestServer(t)
	old := searchFixture(t, ts, plainBPMN,
		`{"variables":{"identityId":"MT-1998","nachname":"Testperson"}}`,
		`{"variables":{"identityId":"MT-1999","nachname":"Testperson"}}`,
	)
	declaring := deployXML(t, ts, searchableBPMN)

	body := fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"declare identityId"}`, declaring)
	code, resp := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/migrate-instances", old), body, "application/json")
	if code != http.StatusOK {
		t.Fatalf("migrate-instances: status=%d body=%s", code, resp)
	}
	var mig struct {
		Migrated int `json:"migrated"`
	}
	if err := json.Unmarshal(resp, &mig); err != nil {
		t.Fatalf("decode migrate: %v (%s)", err, resp)
	}
	if mig.Migrated != 2 {
		t.Fatalf("migrated = %d, want 2 (%s)", mig.Migrated, resp)
	}

	rows := searchInVersion(t, ts, declaring, "identityId=MT-1998")
	if len(rows) != 1 {
		t.Fatalf("identityId=MT-1998 on the new version matched %d rows, want 1", len(rows))
	}
	if len(rows[0].Variables) != 1 || rows[0].Variables[0].Value != "MT-1998" {
		t.Errorf("row variables = %+v, want the matched identityId", rows[0].Variables)
	}
	// The prefix half of what an index can answer works on the migrated values too.
	if got := searchInVersion(t, ts, declaring, "identityId=MT-*"); len(got) != 2 {
		t.Errorf("identityId=MT-* matched %d rows, want 2", len(got))
	}
}

// The repair an operator asks for over a whole version: bounded, repeatable, and
// reporting what the definition declares so they can see what the index will answer
// for. Submitted counts instances the command was queued for — an instance already in
// step emits nothing, which is what makes running it twice free.
func TestReindexInstancesOfProcess(t *testing.T) {
	ts := newTestServer(t)
	def := searchFixture(t, ts, searchableBPMN,
		`{"variables":{"identityId":"MT-1998"}}`,
		`{"variables":{"identityId":"MT-1999"}}`,
		`{"variables":{"identityId":"XY-1000"}}`,
	)

	var got struct {
		ProcessDefKey uint64   `json:"processDefKey"`
		Searchable    []string `json:"searchable"`
		Submitted     int      `json:"submitted"`
		Remaining     bool     `json:"remaining"`
	}
	code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/reindex-instances", def), "", "")
	if code != http.StatusOK {
		t.Fatalf("reindex: status=%d body=%s", code, body)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if got.ProcessDefKey != def || got.Submitted != 3 || got.Remaining {
		t.Errorf("reindex = %+v, want the definition, 3 submitted and nothing remaining", got)
	}
	if len(got.Searchable) != 2 || got.Searchable[0] != "identityId" || got.Searchable[1] != "item" {
		t.Errorf("searchable = %v, want the declaration [identityId item]", got.Searchable)
	}
	// The repair changes nothing an instance already in step: the search answers the
	// same before and after.
	if rows := searchInVersion(t, ts, def, "identityId=MT-1998"); len(rows) != 1 {
		t.Errorf("identityId=MT-1998 matched %d rows after the repair, want 1", len(rows))
	}

	// A page bounds the run rather than truncating the answer: the caller repeats while
	// remaining says there is more.
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/reindex-instances?limit=2", def), "", "")
	if code != http.StatusOK {
		t.Fatalf("reindex limit=2: status=%d body=%s", code, body)
	}
	got.Submitted, got.Remaining = 0, false
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode limit=2: %v (%s)", err, body)
	}
	if got.Submitted != 2 || !got.Remaining {
		t.Errorf("limit=2 = %+v, want 2 submitted and remaining=true", got)
	}
}

// The refusals, so a mistyped request is answered rather than silently doing nothing.
func TestReindexInstancesRefusals(t *testing.T) {
	ts := newTestServer(t)
	def := searchFixture(t, ts, searchableBPMN, `{"variables":{"identityId":"MT-1998"}}`)

	for _, tc := range []struct {
		name, path string
		want       int
	}{
		{"unknown definition", "/api/v1/processes/999999/reindex-instances", http.StatusNotFound},
		{"unparsable key", "/api/v1/processes/nope/reindex-instances", http.StatusBadRequest},
		{"invalid limit", fmt.Sprintf("/api/v1/processes/%d/reindex-instances?limit=abc", def), http.StatusBadRequest},
		{"zero limit", fmt.Sprintf("/api/v1/processes/%d/reindex-instances?limit=0", def), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := doReq(t, ts, http.MethodPost, tc.path, "", ""); code != tc.want {
				t.Errorf("status = %d, want %d (%s)", code, tc.want, body)
			}
		})
	}
}
