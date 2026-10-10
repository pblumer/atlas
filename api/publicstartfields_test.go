package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// instanceCount is how many instances the server lists.
func instanceCount(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d %s", code, body)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	return len(rows)
}

// TestPublicStartTakesOnlyTheFormsFields is the gap the status report of
// 2026-10-10 found in ADR-0029: the one anonymous write into an instance accepted
// any variable a caller cared to name, including the one a gateway decides on. The
// record promised a submission "validated against the form schema"; a name the
// form does not ask for is refused, whole, and nothing starts.
func TestPublicStartTakesOnlyTheFormsFields(t *testing.T) {
	ts := newTestServer(t)
	token := publish(t, ts)
	start := "/public/forms/" + token + "/start"

	code, body := doReq(t, ts, http.MethodPost, start,
		`{"variables":{"customer":"Acme","approved":true,"zzz":1}}`, "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("start with fields the form does not ask for = %d %s, want 400", code, body)
	}
	// The caller is told which names were refused, in a stable order, so a person
	// embedding the form can fix their page.
	if !strings.Contains(string(body), `\"approved\", \"zzz\"`) {
		t.Errorf("refusal = %s, want it to name approved and zzz", body)
	}
	if n := instanceCount(t, ts); n != 0 {
		t.Fatalf("a refused submission started %d instance(s)", n)
	}

	// The form's own fields, and an empty submission, still start.
	for _, ok := range []string{`{"variables":{"customer":"Acme"}}`, `{}`} {
		if code, body := doReq(t, ts, http.MethodPost, start, ok, "application/json"); code != http.StatusOK {
			t.Errorf("start %s = %d %s, want 200", ok, code, body)
		}
	}
	if n := instanceCount(t, ts); n != 2 {
		t.Errorf("instances = %d, want the 2 accepted submissions", n)
	}
}

// TestPublicStartFollowsAGroupsPath: a form-js group or dynamic list with a path
// nests its fields under that path, so what the form submits is the path's root
// variable and not the field keys inside it. The check must accept exactly what the
// form itself sends.
func TestPublicStartFollowsAGroupsPath(t *testing.T) {
	ts := newTestServer(t)
	form := `{"id":"onboarding-form","name":"Onboarding","schema":{"type":"default","components":[` +
		`{"type":"group","path":"address","components":[{"type":"textfield","key":"street"}]},` +
		`{"type":"dynamiclist","path":"items","components":[{"type":"textfield","key":"sku"}]}]}}`
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms", form, "application/json"); code != http.StatusOK {
		t.Fatalf("save form: %d %s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", startFormBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"onboard"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create link: %d %s", code, body)
	}
	var link struct{ Token string }
	if err := json.Unmarshal(body, &link); err != nil {
		t.Fatalf("decode link: %v", err)
	}
	start := "/public/forms/" + link.Token + "/start"

	if code, body := doReq(t, ts, http.MethodPost, start,
		`{"variables":{"address":{"street":"Hauptgasse 1"},"items":[{"sku":"A-1"}]}}`, "application/json"); code != http.StatusOK {
		t.Errorf("start with the form's own nested data = %d %s, want 200", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, start, `{"variables":{"street":"Hauptgasse 1"}}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("start with a field key outside its group's path = %d %s, want 400", code, body)
	}
}
