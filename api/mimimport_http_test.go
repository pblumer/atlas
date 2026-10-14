package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
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

// mimExport wraps XOML in the Export-FIMConfig shape FIMAutomation writes, with
// the attribute's name as a child element.
func mimExport(t *testing.T, resources ...map[string]string) string {
	t.Helper()
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	var b strings.Builder
	b.WriteString("<Results>")
	for _, attrs := range resources {
		b.WriteString(`<ExportObject><ResourceManagementObject><ResourceManagementAttributes>`)
		for name, v := range attrs {
			b.WriteString(`<ResourceManagementAttribute><AttributeName>` + name +
				`</AttributeName><Value>` + esc.Replace(v) + `</Value></ResourceManagementAttribute>`)
		}
		b.WriteString(`</ResourceManagementAttributes></ResourceManagementObject></ExportObject>`)
	}
	b.WriteString("</Results>")
	return b.String()
}

// mimConflict is the 409 body an import answers with when an id is taken.
type mimConflict struct {
	Reason  string `json:"reason"`
	Impacts []struct {
		ProcessID string `json:"processId"`
		Draft     *struct {
			Name    string `json:"name"`
			SavedAt int64  `json:"savedAt"`
		} `json:"draft"`
		Deployed *struct {
			Version            int32    `json:"version"`
			ActiveInstances    int      `json:"activeInstances"`
			KeptElements       int      `json:"keptElements"`
			DroppedElements    []string `json:"droppedElements"`
			DroppedDataObjects []string `json:"droppedDataObjects"`
		} `json:"deployed"`
	} `json:"impacts"`
}

// TestImportMIMRefusesToReplaceADraftUnasked covers the rule ADR-0222 states for
// every design-time store and the MIM import did not follow: an import must not
// land on an id something else holds without being told to. Importing the same
// workflow twice used to replace the first draft silently.
func TestImportMIMRefusesToReplaceADraftUnasked(t *testing.T) {
	ts := newTestServer(t)

	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim?name=Onboarding", mimXOML, "application/xml"); code != http.StatusOK {
		t.Fatalf("first import status=%d body=%s", code, body)
	}

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim?name=Onboarding", mimXOML, "application/xml")
	if code != http.StatusConflict {
		t.Fatalf("second import status=%d, want 409; body=%s", code, body)
	}
	var conflict mimConflict
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatalf("decode conflict: %v (%s)", err, body)
	}
	if len(conflict.Impacts) != 1 || conflict.Impacts[0].ProcessID != "Onboarding" {
		t.Fatalf("conflict does not name the id: %+v", conflict)
	}
	if conflict.Impacts[0].Draft == nil || conflict.Impacts[0].Draft.SavedAt == 0 {
		t.Errorf("the draft in the way must be described: %+v", conflict.Impacts[0])
	}
	if conflict.Reason == "" {
		t.Error("a conflict needs a readable reason")
	}

	// Saying so goes through, and the response says what it replaced.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/imports/mim?name=Onboarding&overwrite=true", mimXOML, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("overwrite status=%d body=%s", code, body)
	}
	var res struct {
		Overwrote []struct {
			ProcessID string `json:"processId"`
		} `json:"overwrote"`
		Drafts []struct {
			ProcessID string `json:"processId"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode import: %v (%s)", err, body)
	}
	if len(res.Overwrote) != 1 || res.Overwrote[0].ProcessID != "Onboarding" {
		t.Errorf("an overwrite must report what it replaced: %+v", res.Overwrote)
	}
	if len(res.Drafts) != 1 {
		t.Errorf("want one draft, got %d", len(res.Drafts))
	}
}

// TestImportMIMReportsWhatADeployWouldStrand covers the check that matters beyond
// the draft: an id that is deployed. The import writes only a draft, but that
// draft is what the next deploy of the process uses, and Atlas migrates a running
// instance by matching element ids — so the elements the imported model does not
// have are the ones an instance could not be carried over on.
func TestImportMIMReportsWhatADeployWouldStrand(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}

	// Import onto the deployed process's own id.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim?name=order", mimXOML, "application/xml")
	if code != http.StatusConflict {
		t.Fatalf("import status=%d, want 409; body=%s", code, body)
	}
	var conflict mimConflict
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatalf("decode conflict: %v (%s)", err, body)
	}
	if len(conflict.Impacts) != 1 {
		t.Fatalf("want one impact, got %+v", conflict.Impacts)
	}
	d := conflict.Impacts[0].Deployed
	if d == nil {
		t.Fatalf("the deployed version must be reported: %+v", conflict.Impacts[0])
	}
	if d.Version != 1 {
		t.Errorf("version = %d, want 1", d.Version)
	}
	// The converted MIM workflow shares no element id with the sample process, so
	// every one of its elements is named as dropped.
	if len(d.DroppedElements) == 0 || d.KeptElements != 0 {
		t.Errorf("want every deployed element reported as dropped, got kept=%d dropped=%v", d.KeptElements, d.DroppedElements)
	}
	var sawTask bool
	for _, e := range d.DroppedElements {
		if e == "task" {
			sawTask = true
		}
	}
	if !sawTask {
		t.Errorf("the deployed service task should be named: %v", d.DroppedElements)
	}
}

// TestImportMIMConvertsEveryWorkflowInAnExport covers an Export-FIMConfig export
// holding more than one WorkflowDefinition: all of them convert, each into its
// own draft named after its own resource.
func TestImportMIMConvertsEveryWorkflowInAnExport(t *testing.T) {
	ts := newTestServer(t)
	export := mimExport(t,
		map[string]string{"DisplayName": "Joiner", "RequestPhase": "Action",
			"XOML": `<SequentialWorkflow><ApprovalActivity ActivityDisplayName="Freigabe"/></SequentialWorkflow>`},
		map[string]string{"DisplayName": "Leaver",
			"XOML": `<SequentialWorkflow><NotificationActivity ActivityDisplayName="Melden"/></SequentialWorkflow>`},
	)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim", export, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	var res struct {
		ProcessID string `json:"processId"`
		Drafts    []struct {
			ProcessID string `json:"processId"`
			Source    *struct {
				DisplayName  string `json:"displayName"`
				RequestPhase string `json:"requestPhase"`
			} `json:"source"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode import: %v (%s)", err, body)
	}
	if len(res.Drafts) != 2 {
		t.Fatalf("want a draft per workflow, got %d: %s", len(res.Drafts), body)
	}
	if res.ProcessID != res.Drafts[0].ProcessID {
		t.Errorf("the flat fields should echo the first draft: %q vs %q", res.ProcessID, res.Drafts[0].ProcessID)
	}
	for i, want := range []string{"Joiner", "Leaver"} {
		if res.Drafts[i].ProcessID != want {
			t.Errorf("draft %d is %q, want %q", i, res.Drafts[i].ProcessID, want)
		}
		if code, xml := doReq(t, ts, http.MethodGet, "/api/v1/drafts/"+want+"/xml", "", ""); code != http.StatusOK {
			t.Errorf("draft %q was not saved: status=%d body=%s", want, code, xml)
		}
	}
	// The resource fields MIM keeps outside the XOML reach the caller.
	if s := res.Drafts[0].Source; s == nil || s.RequestPhase != "Action" || s.DisplayName != "Joiner" {
		t.Errorf("the WorkflowDefinition's own fields were not reported: %+v", res.Drafts[0].Source)
	}
}

// TestImportMIMRefusesTwoWorkflowsOnOneID covers a collision inside a single
// import: an export can carry two workflows of the same name, a process id comes
// from that name, and the second would otherwise be saved over the first — an
// overwrite nobody asked for and one that overwrite=true cannot make right, since
// only one of the two could survive it either way.
func TestImportMIMRefusesTwoWorkflowsOnOneID(t *testing.T) {
	ts := newTestServer(t)
	export := mimExport(t,
		map[string]string{"DisplayName": "Onboarding",
			"XOML": `<SequentialWorkflow><ApprovalActivity ActivityDisplayName="Eins"/></SequentialWorkflow>`},
		map[string]string{"DisplayName": "Onboarding",
			"XOML": `<SequentialWorkflow><NotificationActivity ActivityDisplayName="Zwei"/></SequentialWorkflow>`},
	)
	for _, q := range []string{"", "?overwrite=true"} {
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/imports/mim"+q, export, "application/xml")
		if code != http.StatusConflict {
			t.Fatalf("import%q status=%d, want 409; body=%s", q, code, body)
		}
		if !strings.Contains(string(body), "would both be saved as process") {
			t.Errorf("the conflict should name the collision: %s", body)
		}
	}
	// Nothing was written.
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/drafts/Onboarding/xml", "", ""); code == http.StatusOK {
		t.Error("a refused import must write nothing")
	}
}

// TestImportMIMReportsWhatRunningInstancesWouldLose covers the two halves of the
// cost of a later deploy together: the elements a running instance stands on, and
// the process data it carries. dataObjectBPMN parks its instance on an hour-long
// timer, so the instance is still active when the import asks.
func TestImportMIMReportsWhatRunningInstancesWouldLose(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", dataObjectBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPost,
		"/api/v1/processes/"+strconv.FormatUint(deploy.Key, 10)+"/instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, b)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/imports/mim?name=withdata", mimXOML, "application/xml")
	if code != http.StatusConflict {
		t.Fatalf("import status=%d, want 409; body=%s", code, body)
	}
	var conflict mimConflict
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatalf("decode conflict: %v (%s)", err, body)
	}
	d := conflict.Impacts[0].Deployed
	if d == nil {
		t.Fatalf("the deployed version must be reported: %s", body)
	}
	if d.ActiveInstances != 1 {
		t.Errorf("activeInstances = %d, want 1", d.ActiveInstances)
	}
	if len(d.DroppedElements) == 0 {
		t.Errorf("the elements the instance stands on must be named: %+v", d)
	}
	// A MIM workflow declares no data objects, so both of the deployed version's
	// have nowhere to go — the second under the id Atlas names it by, since the
	// model gave it no name.
	if len(d.DroppedDataObjects) != 2 || d.DroppedDataObjects[0] != "order" {
		t.Errorf("droppedDataObjects = %v, want both, order first", d.DroppedDataObjects)
	}
	// The one line a caller might show on its own says the instances are at risk.
	if !strings.Contains(conflict.Reason, "running instance(s) stand on elements") {
		t.Errorf("reason should name the risk: %q", conflict.Reason)
	}
}
