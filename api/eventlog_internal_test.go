package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestTheEventLogIsPrunedByTheRetentionSweep: the sweep drops the rows older than the
// retention through a prune fact, at most once an hour, and the page says how far it
// was pruned (ADR-0472 §4).
func TestTheEventLogIsPrunedByTheRetentionSweep(t *testing.T) {
	srv := eventServer(t, WithEventLogRetention(time.Hour))
	var compErr, persistErr error
	var key uint64
	srv.do(func() {
		var dps []deployedProcess
		dps, compErr, persistErr = srv.deployModel([]byte(pingThrower), nil, 1, "", "")
		if len(dps) == 1 {
			key = dps[0].Key
		}
	})
	if compErr != nil || persistErr != nil || key == 0 {
		t.Fatalf("deploy: %v %v (key %d)", compErr, persistErr, key)
	}
	for i := 0; i < 2; i++ {
		if code, body := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), `{}`, "application/json"); code != http.StatusOK {
			t.Fatalf("start = %d %s", code, body)
		}
	}
	read := func() eventLogResp {
		t.Helper()
		code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/event-log", "", "")
		var page eventLogResp
		if code != http.StatusOK || json.Unmarshal(body, &page) != nil {
			t.Fatalf("event log = %d %s", code, body)
		}
		return page
	}
	if page := read(); len(page.Rows) != 2 || page.PrunedThrough != "0" {
		t.Fatalf("before the sweep = %+v, want two throws and no cut", page)
	}

	later := time.Now().Add(2 * time.Hour).UnixNano()
	srv.do(func() { srv.pruneEventLog(later) })
	page := read()
	if len(page.Rows) != 0 || page.PrunedThrough == "0" {
		t.Fatalf("after the sweep = %+v, want no rows and the cut", page)
	}

	// Within the hour the sweep does not run again, whatever is old enough.
	if code, body := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), `{}`, "application/json"); code != http.StatusOK {
		t.Fatalf("start = %d %s", code, body)
	}
	srv.do(func() { srv.pruneEventLog(later + int64(time.Minute)) })
	if page := read(); len(page.Rows) != 1 {
		t.Fatalf("a second sweep within the hour pruned: %+v", page)
	}
}

const pingThrower = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <signal id="S" name="evt.ping"/>
  <process id="ping-thrower" isExecutable="true">
    <startEvent id="s"/>
    <intermediateThrowEvent id="ping"><signalEventDefinition signalRef="S"/></intermediateThrowEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="ping"/>
    <sequenceFlow id="f2" sourceRef="ping" targetRef="e"/>
  </process>
</definitions>`
