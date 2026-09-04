package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// searchableBPMN declares two of its variables findable. It parks on a user task so
// the started instances stay live and their variables observable.
const searchableBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="identitaet" isExecutable="true" atlas:searchable="identityId,item">
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

// searchFixture deploys a definition and starts instances with the given variables,
// returning the definition key.
func searchFixture(t *testing.T, ts *httptest.Server, xml string, vars ...string) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	for _, v := range vars {
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), v, "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %s: status=%d body=%s", v, code, b)
		}
	}
	return dep.Key
}

// A declared name turns "identityId=MT-1998" into a seek. It also makes that query
// mean *exactly* that value rather than a substring of it — which changes no existing
// answer, because a declaration is the only way into this path and no model could
// carry one before it existed.
func TestSearchByDeclaredVariableIsExact(t *testing.T) {
	ts := newTestServer(t)
	def := searchFixture(t, ts, searchableBPMN,
		`{"variables":{"identityId":"MT-1998","nachname":"Testperson"}}`,
		`{"variables":{"identityId":"MT-1999","nachname":"Testperson"}}`,
		`{"variables":{"identityId":"XY-1000","nachname":"Testperson"}}`,
	)

	do := func(q string) []searchRow {
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

	rows := do("identityId=MT-1998")
	if len(rows) != 1 {
		t.Fatalf("identityId=MT-1998 matched %d rows, want exactly 1", len(rows))
	}
	if len(rows[0].Variables) != 1 || rows[0].Variables[0].Value != "MT-1998" {
		t.Errorf("row variables = %+v, want the matched identityId", rows[0].Variables)
	}
	// Exact, not substring: a value that merely contains the query is not a hit.
	if got := do("identityId=MT-19"); len(got) != 0 {
		t.Errorf("identityId=MT-19 matched %d rows, want 0 — a declared name matches exactly", len(got))
	}
	// A trailing * asks the other question an ordered index can answer.
	if got := do("identityId=MT-*"); len(got) != 2 {
		t.Errorf("identityId=MT-* matched %d rows, want 2", len(got))
	}
	// A value nothing holds.
	if got := do("identityId=ZZ-0000"); len(got) != 0 {
		t.Errorf("identityId=ZZ-0000 matched %d rows, want 0", len(got))
	}
}

// An undeclared name keeps the substring walk it always had, so nothing an operator
// already types answers differently than it did.
func TestSearchByUndeclaredVariableStillWalks(t *testing.T) {
	ts := newTestServer(t)
	def := searchFixture(t, ts, searchableBPMN,
		`{"variables":{"identityId":"MT-1998","nachname":"Testperson"}}`,
	)
	do := func(q string) []searchRow {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?process=%d&q=%s", def, url.QueryEscape(q)), "", "")
		if code != http.StatusOK {
			t.Fatalf("search %q: status=%d body=%s", q, code, body)
		}
		var rows []searchRow
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatalf("decode %q: %v", q, err)
		}
		return rows
	}

	// The walk answers by the same rule the index does. That equivalence is the point:
	// whether a name is declared searchable is a property of the model, invisible from
	// the search box, and it must not change what a query means.
	if rows := do("nachname=Testperson"); len(rows) != 1 {
		t.Errorf("nachname=Testperson matched %d rows, want 1", len(rows))
	}
	if rows := do("nachname=Test"); len(rows) != 0 {
		t.Errorf("nachname=Test matched %d rows, want 0 — a prefix is not the value", len(rows))
	}
	if rows := do("nachname=Test*"); len(rows) != 1 {
		t.Errorf("nachname=Test* matched %d rows, want 1", len(rows))
	}
}

// The index is per definition, because the declaration is: a search scoped to a
// version that declares the name uses it, and one scoped elsewhere does not see that
// version's instances.
func TestSearchByDeclaredVariableStaysScoped(t *testing.T) {
	ts := newTestServer(t)
	def := searchFixture(t, ts, searchableBPMN, `{"variables":{"identityId":"MT-1998"}}`)
	other := searchFixture(t, ts, userTaskBPMN, `{"variables":{"identityId":"MT-1998"}}`)

	get := func(scope uint64) []searchRow {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?process=%d&q=%s", scope, url.QueryEscape("identityId=MT-1998")), "", "")
		if code != http.StatusOK {
			t.Fatalf("search: status=%d body=%s", code, body)
		}
		var rows []searchRow
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return rows
	}
	if got := get(def); len(got) != 1 {
		t.Errorf("the declaring version matched %d rows, want 1", len(got))
	}
	// The other version declares nothing, so it walks — and still finds its own.
	if got := get(other); len(got) != 1 {
		t.Errorf("the undeclared version matched %d rows, want 1 (found by the walk)", len(got))
	}
}

// A declared name whose value nobody holds returns nothing, and an index that has
// been emptied by the instances finishing and being purged does not keep naming them.
// Both are the seek's answer, not a walk's — the point being that "nothing matched"
// costs the same as a hit.
func TestSearchByDeclaredVariableAfterTheValueChanges(t *testing.T) {
	ts := newTestServer(t)
	def := searchFixture(t, ts, searchableBPMN, `{"variables":{"identityId":"MT-1998"}}`)

	rows := func(q string) []searchRow {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?process=%d&q=%s", def, url.QueryEscape(q)), "", "")
		if code != http.StatusOK {
			t.Fatalf("search %q: status=%d body=%s", q, code, body)
		}
		var out []searchRow
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}
	got := rows("identityId=MT-1998")
	if len(got) != 1 {
		t.Fatalf("setup: matched %d rows, want 1", len(got))
	}
	key := got[0].Key

	// An operator correction rewrites the variable (ADR-0098). The index has to follow
	// it, or the old value would keep naming an instance that no longer holds it.
	if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/instances/%d/variables", key),
		`{"variables":{"identityId":"MT-2000"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("set variable: status=%d body=%s", code, b)
	}
	if got := rows("identityId=MT-1998"); len(got) != 0 {
		t.Errorf("the old value still matched %d rows, want 0", len(got))
	}
	if got := rows("identityId=MT-2000"); len(got) != 1 {
		t.Errorf("the new value matched %d rows, want 1", len(got))
	}
}

// The index path honours the same cap as the walk it replaced. A seek is cheap, but
// a query whose prefix matches every instance of a busy definition would still hand
// the operator a response the size of the definition, so the scan stops at the cap.
func TestSearchByDeclaredVariableStopsAtTheCap(t *testing.T) {
	ts := newTestServer(t)
	vars := make([]string, 0, 205)
	for i := range 205 {
		vars = append(vars, fmt.Sprintf(`{"variables":{"item":"widget-%03d"}}`, i))
	}
	def := searchFixture(t, ts, searchableBPMN, vars...)

	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/search?process=%d&q=%s", def, url.QueryEscape("item=widget-*")), "", "")
	if code != http.StatusOK {
		t.Fatalf("search: status=%d body=%s", code, body)
	}
	var rows []searchRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 200 {
		t.Fatalf("prefix over 205 instances returned %d rows, want the cap of 200", len(rows))
	}
}

// The index is seeked by the pattern's literal head, which for a wild pattern is only
// a neighbourhood — so what comes back has to be matched against the whole pattern
// before it is reported. Without that, "MT-1?" would answer with every MT-1 value the
// index holds.
func TestSearchByDeclaredVariableFiltersAfterTheSeek(t *testing.T) {
	ts := newTestServer(t)
	def := searchFixture(t, ts, searchableBPMN,
		`{"variables":{"identityId":"MT-100"}}`,
		`{"variables":{"identityId":"MT-1000"}}`,
		`{"variables":{"identityId":"MT-10001"}}`,
		`{"variables":{"identityId":"MT-1X"}}`,
	)

	do := func(q string) []searchRow {
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

	// The reason this change exists: an exact term is exact even against an index that
	// can only seek to a neighbourhood.
	if got := do("identityId=MT-100"); len(got) != 1 || got[0].Variables[0].Value != "MT-100" {
		t.Errorf("identityId=MT-100 matched %d rows (%+v), want only MT-100", len(got), got)
	}
	// ? is exactly one character, never none and never two: of MT-100, MT-1000,
	// MT-10001 and MT-1X, only MT-1X is "MT-1" plus one.
	if got := do("identityId=MT-1?"); len(got) != 1 || got[0].Variables[0].Value != "MT-1X" {
		t.Errorf("identityId=MT-1? matched %d rows (%+v), want only MT-1X", len(got), got)
	}
	// * is any run, so every MT-1 value qualifies.
	if got := do("identityId=MT-1*"); len(got) != 4 {
		t.Errorf("identityId=MT-1* matched %d rows, want all 4", len(got))
	}
	// A pattern with no literal head at all still works — it just reads the whole
	// name's range rather than a neighbourhood.
	if got := do("identityId=*0001"); len(got) != 1 || got[0].Variables[0].Value != "MT-10001" {
		t.Errorf("identityId=*0001 matched %d rows (%+v), want only MT-10001", len(got), got)
	}
}
