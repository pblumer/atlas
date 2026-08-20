package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A small MIM/FIM XOML workflow: an approval branch and a resource write-back.
const mimXOML = `<SequentialWorkflow>
  <IfElseActivity Description="Approve?">
    <IfElseBranchActivity Description="Needs approval">
      <Condition>//WorkflowData["Dept"] = "Finance"</Condition>
      <ApprovalActivity Description="Manager approval"/>
    </IfElseBranchActivity>
    <IfElseBranchActivity Description="Auto">
      <NotificationActivity Description="Notify"/>
    </IfElseBranchActivity>
  </IfElseActivity>
  <UpdateResourceActivity Description="Write back"/>
</SequentialWorkflow>`

// TestImportMIMCreatesDraft drives the UI-facing import endpoint: an XOML upload
// must convert, land as a reopenable draft, and return a per-node report.
func TestImportMIMCreatesDraft(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim?name=Onboarding", mimXOML, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	var res struct {
		ProcessID string `json:"processId"`
		Name      string `json:"name"`
		Report    struct {
			Native       int `json:"native"`
			Preserved    int `json:"preserved"`
			ManualReview int `json:"manualReview"`
			Notes        []struct {
				NodeID string `json:"nodeId"`
				Status string `json:"status"`
			} `json:"notes"`
		} `json:"report"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode import: %v (%s)", err, body)
	}
	if res.ProcessID != "Onboarding" {
		t.Fatalf("processId = %q, want Onboarding", res.ProcessID)
	}
	if len(res.Report.Notes) == 0 {
		t.Fatal("expected a non-empty conversion report")
	}
	if res.Report.Native == 0 || res.Report.ManualReview == 0 {
		t.Errorf("report counts look wrong: %+v", res.Report)
	}

	// The import must have created a reopenable draft holding the converted BPMN.
	code, xml := doReq(t, ts, http.MethodGet, "/api/v1/drafts/"+res.ProcessID+"/xml", "", "")
	if code != http.StatusOK {
		t.Fatalf("draft xml status=%d body=%s", code, xml)
	}
	if !strings.Contains(string(xml), "<userTask") || !strings.Contains(string(xml), "atlas:mimSource") {
		t.Errorf("draft BPMN missing expected content:\n%s", xml)
	}
}

func TestImportMIMRejectsEmptyBody(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim", "", "application/xml")
	if code != http.StatusBadRequest {
		t.Fatalf("empty body status=%d, want 400", code)
	}
}

func TestImportMIMRejectsUnknownProject(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim?projectId=does-not-exist", mimXOML, "application/xml")
	if code != http.StatusBadRequest {
		t.Fatalf("unknown project status=%d, want 400", code)
	}
}

func TestImportMIMRejectsNonXOML(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim", "this is not xml <<<", "application/xml")
	if code != http.StatusBadRequest {
		t.Fatalf("non-XOML status=%d body=%s, want 400", code, body)
	}
}
