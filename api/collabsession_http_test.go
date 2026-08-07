package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// readSSEFrame reads one Server-Sent Events frame — lines until a blank line —
// returning its event name and data payload. It fails the test if the stream
// ends first.
func readSSEFrame(t *testing.T, r *bufio.Reader) (event string, data []byte) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE frame: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "":
			if event != "" || data != nil {
				return event, data
			}
			// blank separator before any field: keep reading
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = []byte(strings.TrimPrefix(line, "data: "))
		}
	}
}

// openSession saves a draft, opens an SSE session against it, and returns the
// stream reader, the participant's own id (from the sync frame), and a cancel.
func openSession(t *testing.T, ts *httptest.Server, draftID string) (*bufio.Reader, string, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/drafts/"+draftID+"/session?name=Anja", nil)
	if err != nil {
		cancel()
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open session: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("session status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		cancel()
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	r := bufio.NewReader(resp.Body)
	event, data := readSSEFrame(t, r)
	if event != "sync" {
		cancel()
		t.Fatalf("first frame = %q, want sync", event)
	}
	var snap struct {
		Self string `json:"self"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		cancel()
		t.Fatalf("decode sync: %v", err)
	}
	if snap.Self == "" {
		cancel()
		t.Fatal("sync frame carried no self id")
	}
	return r, snap.Self, cancel
}

func saveDraft(t *testing.T, ts *httptest.Server) {
	t.Helper()
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", draftBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}
}

// TestDraftSessionRoundTrip drives the SSE + POST loop end to end: a participant
// joins, POSTs a change, and receives it back on the stream.
func TestDraftSessionRoundTrip(t *testing.T) {
	ts := newTestServer(t)
	saveDraft(t, ts)
	r, self, cancel := openSession(t, ts, "wip-order")
	defer cancel()

	// A lock the participant takes comes back as a lock frame.
	lockBody := `{"participantId":"` + self + `","elementId":"StartEvent_1","action":"acquire"}`
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/lock", lockBody, "application/json"); code != http.StatusNoContent {
		t.Fatalf("acquire lock status=%d body=%s", code, body)
	}
	if event, data := readSSEFrame(t, r); event != "lock" || !strings.Contains(string(data), "StartEvent_1") {
		t.Fatalf("lock frame = %q %s", event, data)
	}

	// A change the participant POSTs is relayed back on the stream.
	changeBody := `{"participantId":"` + self + `","elementId":"StartEvent_1","xml":"<startEvent/>"}`
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/change", changeBody, "application/json"); code != http.StatusNoContent {
		t.Fatalf("change status=%d body=%s", code, body)
	}
	event, data := readSSEFrame(t, r)
	var chg struct {
		ElementID string `json:"elementId"`
		XML       string `json:"xml"`
	}
	if event != "change" {
		t.Fatalf("expected change frame, got %q %s", event, data)
	}
	if err := json.Unmarshal(data, &chg); err != nil {
		t.Fatalf("decode change frame: %v", err)
	}
	if chg.ElementID != "StartEvent_1" || chg.XML != "<startEvent/>" {
		t.Fatalf("change frame = %+v", chg)
	}

	// Presence updates round-trip too.
	presBody := `{"participantId":"` + self + `","selection":"StartEvent_1"}`
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/presence", presBody, "application/json"); code != http.StatusNoContent {
		t.Fatalf("presence status=%d", code)
	}
	if event, _ := readSSEFrame(t, r); event != "presence" {
		t.Fatalf("expected presence frame, got %q", event)
	}
}

// TestDraftSessionTwoParticipants confirms one participant's arrival and edits
// reach another's stream.
func TestDraftSessionTwoParticipants(t *testing.T) {
	ts := newTestServer(t)
	saveDraft(t, ts)

	r1, _, cancel1 := openSession(t, ts, "wip-order")
	defer cancel1()

	// A second participant joining pushes a presence frame to the first.
	_, self2, cancel2 := openSession(t, ts, "wip-order")
	defer cancel2()
	if event, data := readSSEFrame(t, r1); event != "presence" || !strings.Contains(string(data), `"name":"Anja"`) {
		t.Fatalf("p1 first frame after p2 joined = %q %s", event, data)
	}

	// p2's change reaches p1.
	changeBody := `{"participantId":"` + self2 + `","elementId":"StartEvent_1","xml":"<x/>"}`
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/change", changeBody, "application/json"); code != http.StatusNoContent {
		t.Fatalf("p2 change status=%d", code)
	}
	if event := waitFor(t, r1, "change"); !event {
		t.Fatal("p1 never saw p2's change")
	}
}

// waitFor reads frames until it sees the wanted event type or a short deadline
// passes, so an interleaved presence frame does not fail the test.
func waitFor(t *testing.T, r *bufio.Reader, want string) bool {
	t.Helper()
	done := make(chan bool, 1)
	go func() {
		for i := 0; i < 5; i++ {
			event, _ := readSSEFrame(t, r)
			if event == want {
				done <- true
				return
			}
		}
		done <- false
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(2 * time.Second):
		return false
	}
}

// TestDraftSessionDetachedAgentFlow drives the M2 non-streaming path an AI agent
// uses over MCP: join without an SSE stream, then poll / lock / change / leave.
func TestDraftSessionDetachedAgentFlow(t *testing.T) {
	ts := newTestServer(t)
	saveDraft(t, ts)

	// A browser participant on the SSE stream, to confirm the agent's edits reach
	// live viewers. (waitFor below skips past the presence frame the agent's join
	// pushes onto this stream.)
	r, _, cancel := openSession(t, ts, "wip-order")
	defer cancel()

	// Agent joins without a stream.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/join", `{"name":"Claude"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("join status=%d body=%s", code, body)
	}
	var snap struct {
		Self         string `json:"self"`
		Participants []struct {
			Name string `json:"name"`
		} `json:"participants"`
	}
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatalf("decode join: %v", err)
	}
	if snap.Self == "" {
		t.Fatalf("join returned no self id: %s", body)
	}
	agent := snap.Self

	// The agent takes a lock and edits; the browser sees it live.
	lock := `{"participantId":"` + agent + `","elementId":"StartEvent_1","action":"acquire"}`
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/lock", lock, "application/json"); code != http.StatusNoContent {
		t.Fatalf("agent lock status=%d", code)
	}
	if !waitFor(t, r, "lock") {
		t.Fatal("browser never saw the agent's lock")
	}
	change := `{"participantId":"` + agent + `","elementId":"StartEvent_1","xml":"<startEvent/>"}`
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/change", change, "application/json"); code != http.StatusNoContent {
		t.Fatalf("agent change status=%d", code)
	}

	// The agent polls and sees the current roster and its own lock.
	code, pollBody := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/poll", `{"participantId":"`+agent+`"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("poll status=%d body=%s", code, pollBody)
	}
	var poll struct {
		Locks []struct {
			ElementID string `json:"elementId"`
		} `json:"locks"`
	}
	if err := json.Unmarshal(pollBody, &poll); err != nil {
		t.Fatalf("decode poll: %v", err)
	}
	if len(poll.Locks) != 1 || poll.Locks[0].ElementID != "StartEvent_1" {
		t.Fatalf("poll locks = %+v", poll.Locks)
	}

	// The agent leaves; a second leave is idempotent.
	leave := `{"participantId":"` + agent + `"}`
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/leave", leave, "application/json"); code != http.StatusNoContent {
		t.Fatalf("agent leave status=%d", code)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/leave", leave, "application/json"); code != http.StatusNoContent {
		t.Fatalf("idempotent leave status=%d", code)
	}
}

func TestDraftSessionDetachedValidation(t *testing.T) {
	ts := newTestServer(t)
	saveDraft(t, ts)

	cases := []struct {
		name, path, body string
		want             int
	}{
		{"join missing draft", "/api/v1/drafts/ghost/session/join", `{}`, http.StatusNotFound},
		{"join bad json", "/api/v1/drafts/wip-order/session/join", `{`, http.StatusBadRequest},
		{"poll bad json", "/api/v1/drafts/wip-order/session/poll", `{`, http.StatusBadRequest},
		{"poll no id", "/api/v1/drafts/wip-order/session/poll", `{}`, http.StatusBadRequest},
		{"poll unknown", "/api/v1/drafts/wip-order/session/poll", `{"participantId":"ghost"}`, http.StatusNotFound},
		{"leave no id", "/api/v1/drafts/wip-order/session/leave", `{}`, http.StatusBadRequest},
		{"leave bad json", "/api/v1/drafts/wip-order/session/leave", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := doReq(t, ts, http.MethodPost, tc.path, tc.body, "application/json"); code != tc.want {
				t.Fatalf("%s = %d body=%s, want %d", tc.name, code, body, tc.want)
			}
		})
	}
}

// TestDraftSessionJoinAnonymous confirms a join with no body defaults the name.
func TestDraftSessionJoinAnonymous(t *testing.T) {
	ts := newTestServer(t)
	saveDraft(t, ts)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/join", "", "")
	if code != http.StatusOK {
		t.Fatalf("anonymous join status=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"name":"agent"`) {
		t.Fatalf("default name not applied: %s", body)
	}
}

func TestDraftSessionMissingDraft(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/drafts/ghost/session", "", "")
	if code != http.StatusNotFound {
		t.Fatalf("session for missing draft = %d body=%s, want 404", code, body)
	}
}

func TestDraftSessionPostValidation(t *testing.T) {
	ts := newTestServer(t)
	saveDraft(t, ts)

	cases := []struct {
		name, path, body string
		want             int
	}{
		{"presence bad json", "/api/v1/drafts/wip-order/session/presence", "{", http.StatusBadRequest},
		{"presence no id", "/api/v1/drafts/wip-order/session/presence", `{"selection":"x"}`, http.StatusBadRequest},
		{"presence unknown", "/api/v1/drafts/wip-order/session/presence", `{"participantId":"ghost"}`, http.StatusNotFound},
		{"lock no id", "/api/v1/drafts/wip-order/session/lock", `{"action":"acquire"}`, http.StatusBadRequest},
		{"lock bad action", "/api/v1/drafts/wip-order/session/lock", `{"participantId":"p","elementId":"E","action":"nope"}`, http.StatusBadRequest},
		{"lock unknown", "/api/v1/drafts/wip-order/session/lock", `{"participantId":"ghost","elementId":"E","action":"acquire"}`, http.StatusNotFound},
		{"release unknown", "/api/v1/drafts/wip-order/session/lock", `{"participantId":"ghost","elementId":"E","action":"release"}`, http.StatusNotFound},
		{"change no element", "/api/v1/drafts/wip-order/session/change", `{"participantId":"p"}`, http.StatusBadRequest},
		{"change unknown", "/api/v1/drafts/wip-order/session/change", `{"participantId":"ghost","elementId":"E"}`, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := doReq(t, ts, http.MethodPost, tc.path, tc.body, "application/json"); code != tc.want {
				t.Fatalf("%s = %d body=%s, want %d", tc.name, code, body, tc.want)
			}
		})
	}
}

// TestDraftSessionLockConflict confirms a second participant is refused a locked
// element with 409.
func TestDraftSessionLockConflict(t *testing.T) {
	ts := newTestServer(t)
	saveDraft(t, ts)

	_, self1, cancel1 := openSession(t, ts, "wip-order")
	defer cancel1()
	_, self2, cancel2 := openSession(t, ts, "wip-order")
	defer cancel2()

	acquire := func(self string) int {
		body := `{"participantId":"` + self + `","elementId":"StartEvent_1","action":"acquire"}`
		code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/lock", body, "application/json")
		return code
	}
	if code := acquire(self1); code != http.StatusNoContent {
		t.Fatalf("p1 acquire = %d, want 204", code)
	}
	if code := acquire(self2); code != http.StatusConflict {
		t.Fatalf("p2 acquire held element = %d, want 409", code)
	}

	// p1 releases; p2 then succeeds.
	relBody := `{"participantId":"` + self1 + `","elementId":"StartEvent_1","action":"release"}`
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts/wip-order/session/lock", relBody, "application/json"); code != http.StatusNoContent {
		t.Fatalf("p1 release status=%d", code)
	}
	if code := acquire(self2); code != http.StatusNoContent {
		t.Fatalf("p2 acquire after release = %d, want 204", code)
	}
}
