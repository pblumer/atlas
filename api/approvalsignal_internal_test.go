package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/eventcatalog"
)

// approvalListenerBPMN is what an installation deploys to hear that an approval waits
// (ADR-0435): a signal start on atlas.approval.requested, then a task that holds the
// instance open so its variables can be read.
const approvalListenerBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             id="defs_genehmigung_melden" targetNamespace="http://atlas/examples">
  <signal id="sig_angefragt" name="atlas.approval.requested"/>
  <process id="proc_genehmigung_melden" name="Genehmigung im Chat melden" isExecutable="true">
    <startEvent id="start"><outgoing>f1</outgoing><signalEventDefinition signalRef="sig_angefragt"/></startEvent>
    <userTask id="lesen" name="Lesen"><incoming>f1</incoming><outgoing>f2</outgoing></userTask>
    <endEvent id="ende"><incoming>f2</incoming></endEvent>
    <sequenceFlow id="f1" sourceRef="start" targetRef="lesen"/>
    <sequenceFlow id="f2" sourceRef="lesen" targetRef="ende"/>
  </process>
</definitions>`

// TestTheApprovalProcessesAnnounceTheRequestAsASignal drives each of the shop's three
// approval processes with a listener deployed beside it. Each must start exactly one
// listener instance, carrying exactly what the event catalogue declares — the rule,
// who decides, and the instance that asked — while the approval itself still waits at
// its task. The throw sits before the task, so no decision, reason or secret can have
// reached the listener.
func TestTheApprovalProcessesAnnounceTheRequestAsASignal(t *testing.T) {
	srv, _ := newSystemServer(t, WithSystemProcesses())
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		t.Helper()
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	listenerKey := deployBPMN(t, h, approvalListenerBPMN)
	keys := map[string]uint64{}
	for _, r := range listProcesses(t, h) {
		keys[r.ProcessID] = r.Key
	}

	entry, ok := eventcatalog.Lookup(eventcatalog.ApprovalRequested)
	if !ok {
		t.Fatal("the event catalogue has no entry for the approval signal")
	}
	declared := map[string]eventcatalog.Field{}
	for _, f := range entry.Payload {
		declared[f.Name] = f
	}

	type variable struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	type row struct {
		Key       uint64     `json:"key"`
		Variables []variable `json:"variables"`
	}
	listeners := func() []row {
		t.Helper()
		code, raw := do(http.MethodGet, "/api/v1/instances?process="+strconv.FormatUint(listenerKey, 10), "")
		if code != http.StatusOK {
			t.Fatalf("list listener instances: %d %s", code, raw)
		}
		var rows []row
		if err := json.Unmarshal(listRows(t, raw), &rows); err != nil {
			t.Fatalf("decode listener instances: %v (%s)", err, raw)
		}
		return rows
	}

	for _, c := range []struct {
		process, kind, approvalRef, approver string
	}{
		{"atlas-genehmigung-fix", "fixed", "anna", "anna"},
		{"atlas-genehmigung-rolle", "role", "einkauf", "einkauf"},
		{"atlas-genehmigung-vorgesetzter", "superior", "", "chefin"},
	} {
		t.Run(c.kind, func(t *testing.T) {
			defKey := keys[c.process]
			if defKey == 0 {
				t.Fatalf("%s is not deployed", c.process)
			}
			seen := map[uint64]bool{}
			for _, r := range listeners() {
				seen[r.Key] = true
			}
			vars, _ := json.Marshal(map[string]string{
				"orderId": "ord-1", "itemId": "laptop", "positionId": "laptop", "recipient": "bert",
				"orderer": "bert", "provisionProcess": "prov-laptop", "approvalRef": c.approvalRef,
				"atlasApiBase": "http://127.0.0.1:8080", "portalBaseUrl": "", "variantId": "",
			})
			code, raw := do(http.MethodPost, "/api/v1/processes/"+strconv.FormatUint(defKey, 10)+"/instances",
				`{"variables":`+string(vars)+`}`)
			if code != http.StatusOK {
				t.Fatalf("start %s: %d %s", c.process, code, raw)
			}
			var started struct {
				Key uint64 `json:"instanceKey"`
			}
			if err := json.Unmarshal(raw, &started); err != nil || started.Key == 0 {
				t.Fatalf("decode start of %s: %v (%s)", c.process, err, raw)
			}
			if c.kind == "superior" {
				// The directory answers who the orderer's manager is.
				job := lease(t, srv, "ad")
				body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,"variables":{"entries":[{"manager":"chefin"}]}}`, job.LeaseToken)
				if code, raw := completeJob(t, srv, job, body); code != http.StatusOK && code != http.StatusNoContent {
					t.Fatalf("complete the directory lookup: %d %s", code, raw)
				}
			}

			var fresh []row
			for _, r := range listeners() {
				if !seen[r.Key] {
					fresh = append(fresh, r)
				}
			}
			if len(fresh) != 1 {
				t.Fatalf("one approval started %d listener instances, want exactly 1", len(fresh))
			}
			got := map[string]string{}
			for _, v := range fresh[0].Variables {
				got[v.Name] = v.Value
			}
			for name, want := range map[string]string{
				"approvalKind":  c.kind,
				"approver":      c.approver,
				"orderId":       "ord-1",
				"atlasInstance": strconv.FormatUint(started.Key, 10),
			} {
				if got[name] != want {
					t.Errorf("listener variable %s = %s, want %q", name, got[name], want)
				}
			}
			// What the listener receives is what the catalogue promises: every field it
			// names as always there, and nothing it does not name.
			for name, f := range declared {
				if _, there := got[name]; f.Always && !there {
					t.Errorf("the catalogue promises %s on every %s, and the listener did not receive it", name, entry.Type)
				}
			}
			for name := range got {
				if _, ok := declared[name]; !ok {
					t.Errorf("the listener received %s, which the catalogue's payload for %s does not declare", name, entry.Type)
				}
			}
			if _, there := got["vorgesetzter"]; there != (c.kind == "superior") {
				t.Errorf("vorgesetzter received = %v; only the superior approval has it", there)
			}

			// The approval did not wait on the notice: its task is open.
			code, raw = do(http.MethodGet, "/api/v1/tasks", "")
			if code != http.StatusOK {
				t.Fatalf("list tasks: %d %s", code, raw)
			}
			var tasks []struct {
				ElementID          string `json:"elementId"`
				ProcessInstanceKey uint64 `json:"processInstanceKey"`
			}
			if err := json.Unmarshal(listRows(t, raw), &tasks); err != nil {
				t.Fatalf("decode tasks: %v (%s)", err, raw)
			}
			open := false
			for _, it := range tasks {
				open = open || (it.ProcessInstanceKey == started.Key && it.ElementID == "Genehmigen")
			}
			if !open {
				t.Errorf("the approval of %s does not wait at Genehmigen after announcing itself", c.process)
			}
		})
	}
}
