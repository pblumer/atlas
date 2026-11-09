package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// A person holding a task reads a document of its instance only when a field of the
// task's form shows the reference — the same rule as for the instance's variables
// (ADR-0275, ADR-0543). A document the instance
// holds in a variable no form asks for stays theirs to never see, and a stranger sees
// neither.
func TestTaskHolderReadsOnlyTheDocumentTheirFormShows(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	reviewerID := createUserWithRoles(t, admin, ts.URL, "reviewer", `["user"]`)
	createUserWithRoles(t, admin, ts.URL, "outsider", `["user"]`)
	code, body := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Reviewers"}`)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create group = %d: %s", code, body)
	}
	var grp struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &grp)
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+grp.ID+"/members/"+reviewerID, ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add member = %d: %s", code, body)
	}
	reviewer := signInAs(t, ts.URL, "reviewer", "a-password-that-is-long")
	outsider := signInAs(t, ts.URL, "outsider", "a-password-that-is-long")

	upload := func(name, content string) string {
		code, body := cReqTyped(t, admin, ts, "POST", "/api/v1/documents?name="+name, "text/plain", content)
		if code != http.StatusCreated {
			t.Fatalf("upload: %d %s", code, body)
		}
		return string(body)
	}
	shown, hidden := upload("kommentar.txt", "sichtbar"), upload("geheim.txt", "nur fuers Projekt")
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/forms", reviewForm); code != http.StatusOK {
		t.Fatalf("save form = %d: %s", code, body)
	}
	_, body = cReq(t, admin, ts, "POST", "/api/v1/deployments", reviewBPMN)
	var dep struct{ Key uint64 }
	_ = json.Unmarshal(body, &dep)
	if code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key),
		`{"variables":{"comment":`+shown+`,"secret":`+hidden+`}}`); code != http.StatusOK {
		t.Fatalf("start = %d: %s", code, body)
	}
	idOf := func(raw string) string {
		var r struct {
			ID string `json:"$document"`
		}
		_ = json.Unmarshal([]byte(raw), &r)
		return r.ID
	}
	read := func(c *http.Client, id string) int {
		res, err := c.Get(ts.URL + "/api/v1/documents/" + id)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res.StatusCode
	}
	if code := read(reviewer, idOf(shown)); code != http.StatusOK {
		t.Errorf("the task holder could not read the document their form shows: %d", code)
	}
	if code := read(reviewer, idOf(hidden)); code != http.StatusNotFound {
		t.Errorf("the task holder read a document no form shows: %d", code)
	}
	for _, id := range []string{idOf(shown), idOf(hidden)} {
		if code := read(outsider, id); code != http.StatusNotFound {
			t.Errorf("a stranger read document %s: %d", id, code)
		}
	}
	if code := read(admin, idOf(hidden)); code != http.StatusOK {
		t.Errorf("the operator could not read the instance's document: %d", code)
	}
}
