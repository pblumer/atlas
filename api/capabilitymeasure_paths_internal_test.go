package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/capability"
)

// capabilityMeasurePathsBPMN is one case that waits at a user task, so its cycle time
// is whatever the clock says between its start and the task's completion.
const capabilityMeasurePathsBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="measured" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="wait"/>
    <endEvent id="done"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="done"/>
  </process>
</definitions>`

// TestCapabilityMeasureReadsTheWindowAndOnlyTheWindow: a case that started at 1000s
// and finished at 1100s took 100 seconds. A window that ends before it finished, or
// starts after, does not count it — the first is skipped on the way down the
// completion index, the second is where the walk stops. A realisation nothing here
// deploys is reported as such, with nothing measured.
func TestCapabilityMeasureReadsTheWindowAndOnlyTheWindow(t *testing.T) {
	clk := &testClock{t: int64(1000 * time.Second)}
	srv := newServerWithClock(t, clk)
	defKey := forkPathsDeploy(t, srv, capabilityMeasurePathsBPMN)
	if code, body := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), `{}`, "application/json"); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, body)
	}
	taskKey, _ := openUserTask(t, srv)
	srv.do(func() { clk.t = int64(1100 * time.Second) })
	if code, body := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", taskKey), `{}`, "application/json"); code != http.StatusOK {
		t.Fatalf("complete: %d (%s)", code, body)
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	measure := func(w capability.Window) []capability.RecordedProcess {
		t.Helper()
		got, err := srv.measureCapability(r, []string{"measured", "nowhere"}, w)
		if err != nil || len(got) != 2 {
			t.Fatalf("measure %+v: %+v (%v)", w, got, err)
		}
		if got[1].Deployed || got[1].Durations != nil || got[1].EndEvents != nil {
			t.Errorf("an undeployed realisation was measured: %+v", got[1])
		}
		return got
	}

	if got := measure(capability.Window{From: 1000, To: 1200}); !got[0].Deployed || len(got[0].Durations) != 1 || got[0].Durations[0] != 100 {
		t.Errorf("inside the window: %+v, want one case of 100 seconds", got[0])
	}
	for name, w := range map[string]capability.Window{
		"ending before it finished": {From: 0, To: 1050},
		"starting after it":         {From: 1150, To: 1200},
	} {
		if got := measure(w); len(got[0].Durations) != 0 {
			t.Errorf("window %s: durations = %v, want none", name, got[0].Durations)
		}
	}

	// An unreadable application store is not a statement about this capability: the
	// realisation is still measured rather than the whole reading failing.
	approvalsPathsDirAsFile(t, srv.projects.Dir())
	if got := measure(capability.Window{From: 1000, To: 1200}); !got[0].Deployed || len(got[0].Durations) != 1 {
		t.Errorf("with the applications unreadable: %+v, want the same reading", got[0])
	}
}

// TestCapabilityMeasureDuringShutdown: with the loop gone nothing was looked up, and an
// empty reading would say none of the realisations is deployed.
func TestCapabilityMeasureDuringShutdown(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()
	got, err := srv.measureCapability(httptest.NewRequest(http.MethodGet, "/", nil), []string{"measured"}, capability.Window{From: 0, To: 1})
	if err != errLoopClosing || got != nil {
		t.Errorf("measure during shutdown = %+v, %v; want errLoopClosing", got, err)
	}
}
