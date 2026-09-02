package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// An artifact's id is its store key: a draft is filed under its process id and a form
// under its own id. So retyping that id in the Modeler is a rename of the artifact,
// which used to leave the original behind as a second copy — and, when something else
// already held the new id, overwrite that instead. These cover the identity-aware save
// that replaces both behaviours (ADR-draft-artifact-id-renames).

// idBPMN is a minimal diagram carrying the given process id, which is what
// handleSaveDraft keys the draft by.
func idBPMN(id string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="` + id + `" name="Diagram ` + id + `"><bpmn:startEvent id="s"/></bpmn:process>
</bpmn:definitions>`
}

// draftPath builds POST /api/v1/drafts with the ?from= the Modeler sends. A `from` of
// "-" means "omit the parameter entirely" — the legacy upsert an import or an agent uses.
func draftPath(from string, extra ...string) string {
	p := "/api/v1/drafts"
	if from != "-" {
		p += "?from=" + url.QueryEscape(from)
		for _, e := range extra {
			p += "&" + e
		}
		return p
	}
	if len(extra) > 0 {
		p += "?" + strings.Join(extra, "&")
	}
	return p
}

func draftIDs(t *testing.T, ts *httptest.Server) []string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/drafts", "", "")
	if code != http.StatusOK {
		t.Fatalf("list drafts: status=%d body=%s", code, body)
	}
	var list []struct {
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode drafts: %v (%s)", err, body)
	}
	ids := make([]string, 0, len(list))
	for _, d := range list {
		ids = append(ids, d.ProcessID)
	}
	return ids
}

// TestDraftRenameMovesTheRecord is the reported bug: renaming a diagram's process id
// and saving left the old draft in place, so the Modeler home listed the same diagram
// twice. The save now moves the record and says which id it came from.
func TestDraftRenameMovesTheRecord(t *testing.T) {
	ts := newTestServer(t)

	if code, body := doReq(t, ts, http.MethodPost, draftPath(""), idBPMN("Process_a1b2"), "application/xml"); code != http.StatusOK {
		t.Fatalf("first save: status=%d body=%s", code, body)
	}
	code, body := doReq(t, ts, http.MethodPost, draftPath("Process_a1b2"), idBPMN("order-fulfillment"), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("rename save: status=%d body=%s", code, body)
	}
	var saved struct {
		ProcessID   string `json:"processId"`
		RenamedFrom string `json:"renamedFrom"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode save: %v (%s)", err, body)
	}
	if saved.ProcessID != "order-fulfillment" || saved.RenamedFrom != "Process_a1b2" {
		t.Fatalf("saved = %+v, want order-fulfillment renamed from Process_a1b2", saved)
	}
	if ids := draftIDs(t, ts); len(ids) != 1 || ids[0] != "order-fulfillment" {
		t.Fatalf("drafts after rename = %v, want only order-fulfillment", ids)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/drafts/Process_a1b2/xml", "", ""); code != http.StatusNotFound {
		t.Fatalf("old draft still readable: status=%d", code)
	}
}

// TestDraftRenameOntoTakenIDRefuses is the half of the bug that lost work: renaming
// onto an id another draft already holds used to overwrite that draft.
func TestDraftRenameOntoTakenIDRefuses(t *testing.T) {
	ts := newTestServer(t)
	for _, id := range []string{"keeper", "Process_tmp"} {
		if code, body := doReq(t, ts, http.MethodPost, draftPath(""), idBPMN(id), "application/xml"); code != http.StatusOK {
			t.Fatalf("seed %s: status=%d body=%s", id, code, body)
		}
	}
	code, body := doReq(t, ts, http.MethodPost, draftPath("Process_tmp"), idBPMN("keeper"), "application/xml")
	if code != http.StatusConflict {
		t.Fatalf("rename onto taken id: status=%d body=%s, want 409", code, body)
	}
	if !strings.Contains(string(body), "keeper") {
		t.Errorf("409 should name the id in the way, got %s", body)
	}
	// Neither draft moved, and the one that was in the way still holds its own diagram.
	ids := draftIDs(t, ts)
	if len(ids) != 2 {
		t.Fatalf("drafts after refusal = %v, want both kept", ids)
	}
	_, xml := doReq(t, ts, http.MethodGet, "/api/v1/drafts/keeper/xml", "", "")
	if !strings.Contains(string(xml), `name="Diagram keeper"`) {
		t.Fatalf("the draft in the way was overwritten: %s", xml)
	}
}

// TestDraftCreateOntoTakenIDRefuses covers the first save of a never-saved diagram:
// ?from= is present but empty, so an id that already exists is somebody else's.
func TestDraftCreateOntoTakenIDRefuses(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodPost, draftPath(""), idBPMN("keeper"), "application/xml"); code != http.StatusOK {
		t.Fatal("seed failed")
	}
	if code, body := doReq(t, ts, http.MethodPost, draftPath(""), idBPMN("keeper"), "application/xml"); code != http.StatusConflict {
		t.Fatalf("create onto taken id: status=%d body=%s, want 409", code, body)
	}
	// Re-saving the same draft under the id it already has is an update, not a clash.
	if code, body := doReq(t, ts, http.MethodPost, draftPath("keeper"), idBPMN("keeper"), "application/xml"); code != http.StatusOK {
		t.Fatalf("re-save: status=%d body=%s", code, body)
	}
}

// TestDraftSaveWithoutFromStillUpserts keeps the contract every non-interactive writer
// depends on — an import, a source-tree apply, the MCP authoring tools — where saving
// the same id again is the documented way to update it.
func TestDraftSaveWithoutFromStillUpserts(t *testing.T) {
	ts := newTestServer(t)
	for i := 0; i < 2; i++ {
		if code, body := doReq(t, ts, http.MethodPost, draftPath("-"), idBPMN("wip"), "application/xml"); code != http.StatusOK {
			t.Fatalf("save %d: status=%d body=%s", i, code, body)
		}
	}
	if ids := draftIDs(t, ts); len(ids) != 1 {
		t.Fatalf("drafts = %v, want one (upsert, not a version)", ids)
	}
}

// TestDraftRenameKeepsItsApplication guards the quieter half of a rename: the record
// moves with its project, rather than the renamed diagram reappearing as Ungrouped.
func TestDraftRenameKeepsItsApplication(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Onboarding"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application: status=%d body=%s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v (%s)", err, body)
	}
	filed := "projectId=" + url.QueryEscape(app.ID)
	if code, body := doReq(t, ts, http.MethodPost, draftPath("", filed), idBPMN("Process_seed"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save into application: status=%d body=%s", code, body)
	}
	// The Modeler re-sends the project it resolved for the draft; the rename must keep
	// it either way, so this asserts the record's project, not the parameter's.
	code, body = doReq(t, ts, http.MethodPost, draftPath("Process_seed"), idBPMN("welcome-mail"), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("rename: status=%d body=%s", code, body)
	}
	var saved struct {
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode save: %v (%s)", err, body)
	}
	if saved.ProjectID != app.ID {
		t.Fatalf("renamed draft projectId = %q, want %q", saved.ProjectID, app.ID)
	}
}

// TestDraftIDAvailability covers the live check behind the Process ID field.
func TestDraftIDAvailability(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodPost, draftPath(""), idBPMN("taken-id"), "application/xml"); code != http.StatusOK {
		t.Fatal("seed failed")
	}
	var av struct {
		ID        string `json:"id"`
		Available bool   `json:"available"`
		UsedBy    string `json:"usedBy"`
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/drafts/taken-id/availability", "", "")
	if code != http.StatusOK {
		t.Fatalf("availability: status=%d body=%s", code, body)
	}
	if err := json.Unmarshal(body, &av); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if av.Available || av.UsedBy != "Diagram taken-id" {
		t.Fatalf("availability = %+v, want taken and named", av)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/drafts/free-id/availability", "", "")
	if err := json.Unmarshal(body, &av); code != http.StatusOK || err != nil {
		t.Fatalf("availability of a free id: status=%d body=%s err=%v", code, body, err)
	}
	if !av.Available {
		t.Fatalf("availability = %+v, want free", av)
	}
}

// ---- Forms ----------------------------------------------------------------

const idFormSchema = `{"type":"default","components":[]}`

func saveFormBody(id, name, from string) string {
	body := `{"id":"` + id + `","name":"` + name + `","schema":` + idFormSchema
	if from != "-" {
		body += `,"from":"` + from + `"`
	}
	return body + `}`
}

func formIDs(t *testing.T, ts *httptest.Server) []string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/forms", "", "")
	if code != http.StatusOK {
		t.Fatalf("list forms: status=%d body=%s", code, body)
	}
	var list []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode forms: %v (%s)", err, body)
	}
	ids := make([]string, 0, len(list))
	for _, f := range list {
		ids = append(ids, f.ID)
	}
	return ids
}

// TestFormRenameMovesTheRecord: a form's id is what a user task binds to, so changing
// it in the editor renames the form rather than leaving a second one behind.
func TestFormRenameMovesTheRecord(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms", saveFormBody("form-mtjs4", "JIRA Ticket", ""), "application/json"); code != http.StatusOK {
		t.Fatalf("first save: status=%d body=%s", code, body)
	}
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms", saveFormBody("frm_jira_ticket_new", "JIRA Ticket", "form-mtjs4"), "application/json")
	if code != http.StatusOK {
		t.Fatalf("rename: status=%d body=%s", code, body)
	}
	var saved struct {
		ID          string `json:"id"`
		RenamedFrom string `json:"renamedFrom"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if saved.ID != "frm_jira_ticket_new" || saved.RenamedFrom != "form-mtjs4" {
		t.Fatalf("saved = %+v, want the renamed identity", saved)
	}
	if ids := formIDs(t, ts); len(ids) != 1 || ids[0] != "frm_jira_ticket_new" {
		t.Fatalf("forms after rename = %v, want only frm_jira_ticket_new", ids)
	}
}

// TestFormRenameOntoTakenIDRefuses: the id another form holds is never overwritten.
func TestFormRenameOntoTakenIDRefuses(t *testing.T) {
	ts := newTestServer(t)
	for _, id := range []string{"keeper", "form-tmp"} {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms", saveFormBody(id, "Form "+id, ""), "application/json"); code != http.StatusOK {
			t.Fatalf("seed %s: status=%d body=%s", id, code, body)
		}
	}
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms", saveFormBody("keeper", "Form form-tmp", "form-tmp"), "application/json")
	if code != http.StatusConflict {
		t.Fatalf("rename onto taken id: status=%d body=%s, want 409", code, body)
	}
	if ids := formIDs(t, ts); len(ids) != 2 {
		t.Fatalf("forms after refusal = %v, want both kept", ids)
	}
	// The form that was in the way kept its own name — nothing was written over it.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/forms/keeper", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"name":"Form keeper"`) {
		t.Fatalf("the form in the way was overwritten: status=%d body=%s", code, body)
	}
}

// TestFormSaveWithoutFromStillUpserts keeps the plain overwrite-by-id the MCP
// authoring tools and the source-tree import document.
func TestFormSaveWithoutFromStillUpserts(t *testing.T) {
	ts := newTestServer(t)
	for i := 0; i < 2; i++ {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms", saveFormBody("shared", "Shared", "-"), "application/json"); code != http.StatusOK {
			t.Fatalf("save %d: status=%d body=%s", i, code, body)
		}
	}
	if ids := formIDs(t, ts); len(ids) != 1 {
		t.Fatalf("forms = %v, want one", ids)
	}
}

// TestFormIDAvailability covers the live check behind the form editor's ID field.
func TestFormIDAvailability(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/forms", saveFormBody("in-use", "Approval", ""), "application/json"); code != http.StatusOK {
		t.Fatal("seed failed")
	}
	var av struct {
		Available bool   `json:"available"`
		UsedBy    string `json:"usedBy"`
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/forms/in-use/availability", "", "")
	if err := json.Unmarshal(body, &av); code != http.StatusOK || err != nil {
		t.Fatalf("availability: status=%d body=%s err=%v", code, body, err)
	}
	if av.Available || av.UsedBy != "Approval" {
		t.Fatalf("availability = %+v, want taken by Approval", av)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/forms/unused/availability", "", "")
	if err := json.Unmarshal(body, &av); code != http.StatusOK || err != nil {
		t.Fatalf("availability of a free id: status=%d body=%s err=%v", code, body, err)
	}
	if !av.Available {
		t.Fatalf("availability = %+v, want free", av)
	}
}
