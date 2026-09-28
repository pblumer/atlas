package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// forkedWaitBPMN parks an instance on one of two named waits, chosen by a start
// variable. It gives a test two elements of one version with different instances
// sitting on each — the shape the Operations filter is about.
const forkedWaitBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <process id="forked" name="Forked" isExecutable="true">
    <startEvent id="start"/>
    <exclusiveGateway id="choose" default="toRight"/>
    <intermediateCatchEvent id="left"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <intermediateCatchEvent id="right"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="choose"/>
    <sequenceFlow id="toLeft" sourceRef="choose" targetRef="left">
      <conditionExpression xsi:type="tFormalExpression">= branch = "left"</conditionExpression>
    </sequenceFlow>
    <sequenceFlow id="toRight" sourceRef="choose" targetRef="right"/>
    <sequenceFlow id="f4" sourceRef="left" targetRef="end"/>
    <sequenceFlow id="f5" sourceRef="right" targetRef="end"/>
  </process>
</definitions>`

// listedKeys decodes an instance listing into its keys, in the order returned.
func listedKeys(t *testing.T, body []byte) []uint64 {
	t.Helper()
	var rows []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	out := make([]uint64, 0, len(rows))
	for _, r := range rows {
		if r.State != "active" {
			t.Errorf("instance %d is %q — an element filter can only match a live token", r.Key, r.State)
		}
		out = append(out, r.Key)
	}
	return out
}

// TestListInstancesByElement covers the Operations filter end to end: clicking a
// task lists exactly the instances whose token is sitting on it, another element
// lists its own, and no element filter lists everything (ADR-0261).
func TestListInstancesByElement(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", forkedWaitBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	start := func(branch string) {
		t.Helper()
		payload := fmt.Sprintf(`{"variables":{"branch":%q}}`, branch)
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), payload, "application/json"); code != http.StatusOK {
			t.Fatalf("create instance on %s: status=%d body=%s", branch, code, b)
		}
	}
	start("left")
	start("right")
	start("left")

	list := func(query string) []uint64 {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, query, "", "")
		if code != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", query, code, body)
		}
		return listedKeys(t, body)
	}

	all := list(fmt.Sprintf("/api/v1/instances?process=%d&state=active", dep.Key))
	if len(all) != 3 {
		t.Fatalf("unfiltered listing = %v, want three instances", all)
	}
	// The listing is newest first, so the three starts are all[0]=third, all[1]=second,
	// all[2]=first: two on "left" and one on "right".
	left := list(fmt.Sprintf("/api/v1/instances?process=%d&element=left", dep.Key))
	if want := []uint64{all[0], all[2]}; !equalKeys(left, want) {
		t.Errorf("instances on element left = %v, want %v", left, want)
	}
	right := list(fmt.Sprintf("/api/v1/instances?process=%d&element=right", dep.Key))
	if want := []uint64{all[1]}; !equalKeys(right, want) {
		t.Errorf("instances on element right = %v, want %v", right, want)
	}
	// An element no token ever reaches lists nothing — which is a fact about the
	// tokens, not an error.
	if got := list(fmt.Sprintf("/api/v1/instances?process=%d&element=end", dep.Key)); len(got) != 0 {
		t.Errorf("instances on the end event = %v, want none", got)
	}
	// A token lives only in a running instance, so the finished half of a filtered
	// listing is empty by construction rather than by chance.
	if got := list(fmt.Sprintf("/api/v1/instances?process=%d&element=left&state=finished", dep.Key)); len(got) != 0 {
		t.Errorf("finished instances on element left = %v, want none", got)
	}

	// Paging: the filter's page is capped and resumes through the same cursor the
	// unfiltered active half uses.
	res, err := http.Get(ts.URL + fmt.Sprintf("/api/v1/instances?process=%d&element=left&state=active&limit=1", dep.Key))
	if err != nil {
		t.Fatalf("GET capped page: %v", err)
	}
	facts := readPage(t, res)
	res.Body.Close()
	var page []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(facts.Items, &page); err != nil {
		t.Fatalf("decode capped page: %v", err)
	}
	if len(page) != 1 || page[0].Key != all[0] {
		t.Fatalf("capped page = %+v, want the newest match", page)
	}
	if !facts.Truncated || facts.NextCursor == "" {
		t.Fatalf("capped page says truncated=%v cursor=%q, want true and a cursor", facts.Truncated, facts.NextCursor)
	}
	next := list(fmt.Sprintf("/api/v1/instances?process=%d&element=left&state=active&before=%s", dep.Key, facts.NextCursor))
	if want := []uint64{all[2]}; !equalKeys(next, want) {
		t.Errorf("next page = %v, want %v", next, want)
	}
}

// TestListInstancesByElementRejectsBadRequests covers the two ways the filter can be
// asked for something that cannot be answered. Both are told apart from "nothing is
// there", because an empty list reads as a fact about the process.
func TestListInstancesByElementRejectsBadRequests(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", forkedWaitBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}

	// An element id the version does not define.
	if code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances?process=%d&element=nope", dep.Key), "", ""); code != http.StatusBadRequest {
		t.Errorf("unknown element: status=%d body=%s, want 400", code, body)
	}
	// An element id with no version to resolve it against.
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances?element=left", "", ""); code != http.StatusBadRequest {
		t.Errorf("element without process: status=%d body=%s, want 400", code, body)
	}
	// A definition that is not deployed here is an empty answer, not a bad request:
	// nothing is running on it.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances?process=999999&element=left", "", "")
	if code != http.StatusOK {
		t.Fatalf("unknown definition: status=%d body=%s, want 200", code, body)
	}
	if got := listedKeys(t, body); len(got) != 0 {
		t.Errorf("unknown definition listed %v, want nothing", got)
	}
}

// equalKeys compares two key slices element-wise.
func equalKeys(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// listedRows decodes an instance listing into its keys and states, in the order
// returned — for the history filters, whose rows may be running or finished.
func listedRows(t *testing.T, body []byte) (keys []uint64, states []string) {
	t.Helper()
	var rows []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	for _, r := range rows {
		keys = append(keys, r.Key)
		states = append(states, r.State)
	}
	return keys, states
}

// TestListInstancesThatLeftAnElement covers the history half of the Operations filter:
// ?at=passed lists the instances a token completed the element in and moved on from,
// ?at=cancelled the ones cancelled there, and ?at=live (the default) the ones sitting
// there now — the instances behind the gray, the amber and the green count
// (ADR-draft-instances-that-left-an-element).
func TestListInstancesThatLeftAnElement(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", forkedWaitBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	for _, branch := range []string{"left", "right", "left"} {
		payload := fmt.Sprintf(`{"variables":{"branch":%q}}`, branch)
		if code, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), payload, "application/json"); code != http.StatusOK {
			t.Fatalf("create instance on %s: status=%d body=%s", branch, code, b)
		}
	}
	list := func(query string) ([]uint64, []string) {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances?process=%d&%s", dep.Key, query), "", "")
		if code != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", query, code, body)
		}
		return listedRows(t, body)
	}
	all, _ := list("state=active")
	if len(all) != 3 {
		t.Fatalf("unfiltered listing = %v, want three instances", all)
	}
	newest, middle, oldest := all[0], all[1], all[2] // left, right, left

	// Every instance got through the start event and the gateway; nobody sits there.
	for _, el := range []string{"start", "choose"} {
		if got, _ := list("element=" + el + "&at=passed"); !equalKeys(got, all) {
			t.Errorf("passed %s = %v, want every instance %v", el, got, all)
		}
		if got, _ := list("element=" + el + "&at=live"); len(got) != 0 {
			t.Errorf("live on %s = %v, want none", el, got)
		}
	}
	// Nobody has left the timers yet: two sit on "left", and none completed it.
	if got, _ := list("element=left&at=passed"); len(got) != 0 {
		t.Errorf("passed left before anything left it = %v, want none", got)
	}

	// Cancel the oldest instance while its token sits on "left".
	if code, b := doReq(t, ts, http.MethodDelete, fmt.Sprintf("/api/v1/instances/%d", oldest), "", ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("cancel: status=%d body=%s", code, b)
	}
	keys, states := list("element=left&at=cancelled")
	if !equalKeys(keys, []uint64{oldest}) {
		t.Fatalf("cancelled at left = %v, want %v", keys, []uint64{oldest})
	}
	// A history row is whatever the instance is now — here, finished — and the listing
	// says so rather than presenting it as running.
	if states[0] == "active" {
		t.Errorf("the cancelled instance is listed as %q, want its terminal state", states[0])
	}
	if got, _ := list("element=left"); !equalKeys(got, []uint64{newest}) {
		t.Errorf("live on left after the cancel = %v, want %v", got, []uint64{newest})
	}
	if got, _ := list("element=right&at=cancelled"); len(got) != 0 {
		t.Errorf("cancelled at right = %v, want none (%d is still waiting there)", got, middle)
	}

	// Paging: capped, and resumed through a bare-key cursor with no half named — the
	// index lists running and finished instances in one order.
	res, err := http.Get(ts.URL + fmt.Sprintf("/api/v1/instances?process=%d&element=choose&at=passed&limit=2", dep.Key))
	if err != nil {
		t.Fatalf("GET capped page: %v", err)
	}
	facts := readPage(t, res)
	res.Body.Close()
	if !facts.Truncated || facts.NextCursor == "" {
		t.Fatalf("capped page says truncated=%v cursor=%q, want true and a cursor", facts.Truncated, facts.NextCursor)
	}
	if got, _ := list("element=choose&at=passed&before=" + facts.NextCursor); !equalKeys(got, []uint64{oldest}) {
		t.Errorf("next page = %v, want %v", got, []uint64{oldest})
	}
}

// TestListInstancesThatLeftAnElementRejectsBadRequests pins what ?at= refuses rather
// than quietly answering something else.
func TestListInstancesThatLeftAnElementRejectsBadRequests(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", forkedWaitBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	for _, q := range []string{
		"at=bogus&element=left",                    // not a relation to an element
		"at=passed",                                // asks about an element and names none
		"at=live",                                  // likewise
		"at=passed&element=left&state=active",      // the index is not split into halves
		"at=cancelled&element=left&state=finished", // likewise
		"at=passed&element=left&before=nope",       // a cursor is an instance key
		"at=passed&element=nope",                   // an element the version does not define
	} {
		code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances?process=%d&%s", dep.Key, q), "", "")
		if code != http.StatusBadRequest {
			t.Errorf("GET instances?%s = %d, want 400 (%s)", q, code, body)
		}
	}
	// The live filter keeps the shape it had: at=live is the default spelled out.
	if code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances?process=%d&element=left&at=live&state=active", dep.Key), "", ""); code != http.StatusOK {
		t.Errorf("at=live with a half = %d, want 200 (%s)", code, body)
	}
}
