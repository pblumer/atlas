package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// drainType reads frames off a participant's channel until it finds one of the
// given type (or the channel is empty), returning the last such frame's decoded
// payload. Presence/lock frames are full snapshots, so the newest one is the
// authoritative view.
func drainType(t *testing.T, ch <-chan collabEvent, typ string) (map[string]any, bool) {
	t.Helper()
	var last map[string]any
	found := false
	for {
		select {
		case ev := <-ch:
			if ev.Type != typ {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(ev.Data, &m); err != nil {
				t.Fatalf("decode %s frame: %v", typ, err)
			}
			last, found = m, true
		default:
			return last, found
		}
	}
}

func TestCollabJoinSyncAndPresence(t *testing.T) {
	reg := newCollabRegistry()

	p1, sync1, leave1 := reg.join("draft-a", "usr_1", "Anja")
	defer leave1()

	var snap struct {
		Self         string           `json:"self"`
		Participants []collabPresence `json:"participants"`
		Locks        []collabLock     `json:"locks"`
	}
	if err := json.Unmarshal(sync1, &snap); err != nil {
		t.Fatalf("decode sync: %v", err)
	}
	if snap.Self != p1.ID {
		t.Fatalf("sync self = %q, want %q", snap.Self, p1.ID)
	}
	if len(snap.Participants) != 1 || snap.Participants[0].Name != "Anja" {
		t.Fatalf("sync participants = %+v, want just Anja", snap.Participants)
	}

	// A second join broadcasts a presence frame listing both to the first.
	_, _, leave2 := reg.join("draft-a", "usr_2", "Ben")
	defer leave2()
	pres, ok := drainType(t, p1.ch, collabEventPresence)
	if !ok {
		t.Fatal("p1 received no presence frame after p2 joined")
	}
	if got := len(pres["participants"].([]any)); got != 2 {
		t.Fatalf("presence roster = %d, want 2", got)
	}
}

func TestCollabPresenceUpdate(t *testing.T) {
	reg := newCollabRegistry()
	p1, _, leave1 := reg.join("d", "u1", "Anja")
	defer leave1()
	p2, _, leave2 := reg.join("d", "u2", "Ben")
	defer leave2()
	drainType(t, p1.ch, collabEventPresence) // clear the join frame

	if !reg.presence("d", p2.ID, "Gateway_1") {
		t.Fatal("presence returned false for a live participant")
	}
	pres, ok := drainType(t, p1.ch, collabEventPresence)
	if !ok {
		t.Fatal("no presence frame after selection change")
	}
	found := false
	for _, raw := range pres["participants"].([]any) {
		m := raw.(map[string]any)
		if m["id"] == p2.ID && m["selection"] == "Gateway_1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("selection not reflected in roster: %+v", pres)
	}

	// A stale participant / unknown session are reported, not panicked.
	if reg.presence("d", "ghost", "x") {
		t.Error("presence for an unknown participant returned true")
	}
	if reg.presence("nope", p1.ID, "x") {
		t.Error("presence on an unknown session returned true")
	}
}

func TestCollabLockAcquireConflictAndRelease(t *testing.T) {
	reg := newCollabRegistry()
	p1, _, leave1 := reg.join("d", "u1", "Anja")
	defer leave1()
	p2, _, leave2 := reg.join("d", "u2", "Ben")
	defer leave2()

	// p1 takes Task_1.
	granted, ok := reg.acquireLock("d", p1.ID, "Task_1")
	if !ok || !granted {
		t.Fatalf("p1 acquire = (granted=%v, ok=%v), want both true", granted, ok)
	}
	// Re-acquiring your own lock is idempotent.
	if g, _ := reg.acquireLock("d", p1.ID, "Task_1"); !g {
		t.Error("re-acquiring own lock was refused")
	}
	// p2 cannot take the same element.
	if g, ok := reg.acquireLock("d", p2.ID, "Task_1"); !ok || g {
		t.Fatalf("p2 acquire held element = (granted=%v, ok=%v), want granted=false ok=true", g, ok)
	}
	// p2 can take a different element.
	if g, _ := reg.acquireLock("d", p2.ID, "Task_2"); !g {
		t.Error("p2 could not lock a free element")
	}
	// Unknown participant / session.
	if _, ok := reg.acquireLock("d", "ghost", "Task_3"); ok {
		t.Error("acquire for an unknown participant reported ok")
	}
	if _, ok := reg.acquireLock("nope", p1.ID, "Task_3"); ok {
		t.Error("acquire on an unknown session reported ok")
	}

	// After p1 releases, p2 can take Task_1.
	if !reg.releaseLock("d", p1.ID, "Task_1") {
		t.Fatal("release returned false for a live participant")
	}
	if g, _ := reg.acquireLock("d", p2.ID, "Task_1"); !g {
		t.Error("p2 could not lock after p1 released")
	}
	// Releasing a lock you do not hold is a harmless no-op that still succeeds.
	if !reg.releaseLock("d", p1.ID, "Task_1") {
		t.Error("idempotent release reported failure")
	}
	if reg.releaseLock("d", "ghost", "x") {
		t.Error("release for an unknown participant reported success")
	}
	if reg.releaseLock("gone", p1.ID, "x") {
		t.Error("release on an unknown session reported success")
	}
}

func TestCollabLeaveReleasesLocksAndReapsSession(t *testing.T) {
	reg := newCollabRegistry()
	p1, _, leave1 := reg.join("d", "u1", "Anja")
	p2, _, leave2 := reg.join("d", "u2", "Ben")
	defer leave2()

	if g, _ := reg.acquireLock("d", p1.ID, "Task_1"); !g {
		t.Fatal("p1 could not acquire")
	}
	drainType(t, p2.ch, collabEventLock) // clear frames seen so far

	leave1() // p1 disconnects

	// p2 sees the lock released and can take the element.
	if g, _ := reg.acquireLock("d", p2.ID, "Task_1"); !g {
		t.Error("lock not released when its holder left")
	}
	// A lock frame reflecting the release reached p2.
	if _, ok := drainType(t, p2.ch, collabEventLock); !ok {
		t.Error("no lock frame after holder left")
	}

	// When the last participant leaves, the session is discarded.
	leave2()
	reg.mu.Lock()
	_, exists := reg.sessions["d"]
	reg.mu.Unlock()
	if exists {
		t.Error("empty session was not reaped")
	}
}

func TestCollabChangeBroadcast(t *testing.T) {
	reg := newCollabRegistry()
	p1, _, leave1 := reg.join("d", "u1", "Anja")
	defer leave1()
	p2, _, leave2 := reg.join("d", "u2", "Ben")
	defer leave2()
	drainType(t, p2.ch, collabEventPresence)

	if !reg.change("d", p1.ID, "Task_1", "<task/>") {
		t.Fatal("change returned false for a live participant")
	}
	ch, ok := drainType(t, p2.ch, collabEventChange)
	if !ok {
		t.Fatal("p2 received no change frame")
	}
	if ch["by"] != p1.ID || ch["elementId"] != "Task_1" || ch["xml"] != "<task/>" {
		t.Fatalf("change frame = %+v", ch)
	}
	if reg.change("d", "ghost", "x", "") {
		t.Error("change for an unknown participant returned true")
	}
	if reg.change("nope", p1.ID, "x", "") {
		t.Error("change on an unknown session returned true")
	}
}

// TestCollabSlowConsumerDoesNotBlock fills a participant's buffer past capacity
// and confirms a broadcast still returns promptly (frames are dropped, not
// blocked on), so one stalled SSE stream cannot wedge the session.
func TestCollabSlowConsumerDoesNotBlock(t *testing.T) {
	reg := newCollabRegistry()
	_, _, leaveSlow := reg.join("d", "u1", "Slow") // never drains its channel
	defer leaveSlow()
	p2, _, leave2 := reg.join("d", "u2", "Ben")
	defer leave2()

	// Far more broadcasts than the buffer holds; must not deadlock.
	for i := 0; i < collabBufferSize*3; i++ {
		reg.change("d", p2.ID, "Task", "")
	}
}

// TestCollabJoinIDFallback exercises the crypto-rand failure path: a registry
// whose id minter errors still forms a session with a synthesized id.
func TestCollabJoinIDFallback(t *testing.T) {
	reg := newCollabRegistry()
	reg.newID = func() (string, error) { return "", errTest }
	p, _, leave := reg.join("d", "u1", "Anja")
	defer leave()
	if p.ID == "" {
		t.Fatal("fallback id was empty")
	}
}

var errTest = &collabTestErr{}

type collabTestErr struct{}

func (*collabTestErr) Error() string { return "boom" }

// TestCollabPoll covers the non-streaming read side (ADR-0140 M2): a detached
// participant drains its buffered frames and reads the current roster and locks.
func TestCollabPoll(t *testing.T) {
	reg := newCollabRegistry()
	p1, _, leave1 := reg.join("d", "u1", "Anja")
	defer leave1()
	p2, _, leave2 := reg.join("d", "u2", "Ben")
	defer leave2()

	// p2 acts; p1 polls and should see the buffered frames plus current state.
	reg.acquireLock("d", p2.ID, "Task_1")
	reg.change("d", p2.ID, "Task_1", "<t/>")

	out, ok := reg.poll("d", p1.ID)
	if !ok {
		t.Fatal("poll returned ok=false for a live participant")
	}
	var got struct {
		Self         string           `json:"self"`
		Participants []collabPresence `json:"participants"`
		Locks        []collabLock     `json:"locks"`
		Events       []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode poll: %v", err)
	}
	if got.Self != p1.ID {
		t.Fatalf("poll self = %q, want %q", got.Self, p1.ID)
	}
	if len(got.Participants) != 2 {
		t.Fatalf("poll roster = %d, want 2", len(got.Participants))
	}
	if len(got.Locks) != 1 || got.Locks[0].ElementID != "Task_1" {
		t.Fatalf("poll locks = %+v", got.Locks)
	}
	if len(got.Events) == 0 {
		t.Fatal("poll returned no buffered events")
	}
	// A second poll with nothing new drains to an empty event list.
	out2, _ := reg.poll("d", p1.ID)
	if !strings.Contains(string(out2), `"events":[]`) {
		t.Fatalf("second poll should have no events: %s", out2)
	}

	// Unknown participant / session.
	if _, ok := reg.poll("d", "ghost"); ok {
		t.Error("poll for an unknown participant reported ok")
	}
	if _, ok := reg.poll("nope", p1.ID); ok {
		t.Error("poll on an unknown session reported ok")
	}
}

// TestCollabCanEdit covers the per-participant edit capability snapshotted at
// join from the project role (ADR-0140/0071).
func TestCollabCanEdit(t *testing.T) {
	reg := newCollabRegistry()
	ed, _, _ := reg.joinStream("d", "u1", "Ed", true)
	vw, _, _ := reg.joinStream("d", "u2", "Vi", false)

	if ce, ok := reg.canEdit("d", ed.ID); !ok || !ce {
		t.Errorf("editor canEdit = (%v, ok=%v), want (true, true)", ce, ok)
	}
	if ce, ok := reg.canEdit("d", vw.ID); !ok || ce {
		t.Errorf("viewer canEdit = (%v, ok=%v), want (false, true)", ce, ok)
	}
	if _, ok := reg.canEdit("d", "ghost"); ok {
		t.Error("canEdit for an unknown participant reported ok")
	}
	if _, ok := reg.canEdit("nope", ed.ID); ok {
		t.Error("canEdit on an unknown session reported ok")
	}
	// A read-only detached agent carries canEdit=false too.
	ag, _ := reg.joinDetachedAs("d", "u3", "Agent", false)
	if ce, _ := reg.canEdit("d", ag.ID); ce {
		t.Error("read-only agent reported canEdit=true")
	}
}

// TestCollabReaper covers the TTL reaper (ADR-0140): any participant that falls
// silent past the TTL is evicted and its locks released — a detached MCP agent, and
// now a streaming browser too (the backstop for a half-open connection the keepalive
// never catches). A participant kept fresh survives: an agent by polling, a browser
// by its periodic heartbeat (a re-announced presence).
func TestCollabReaper(t *testing.T) {
	now := time.Unix(1000, 0)
	reg := newCollabRegistry()
	reg.now = func() time.Time { return now }

	live, _, _ := reg.join("d", "u1", "Anja")         // streaming: keeps heartbeating
	ghost, _, _ := reg.join("d", "u2", "Bea")         // streaming: a dead tab that falls silent
	agent, _ := reg.joinDetached("d", "u3", "Claude") // detached MCP agent that falls silent
	reg.acquireLock("d", ghost.ID, "Task_1")
	drainType(t, live.ch, collabEventLock) // clear frames seen so far

	// Within the TTL: nothing is stale yet.
	now = now.Add(reg.ttl / 2)
	if n := reg.reap(); n != 0 {
		t.Fatalf("early reap removed %d, want 0", n)
	}

	// Past the TTL for everyone — but the live browser heartbeats (re-announces
	// presence) just before the sweep, so only the silent browser and agent go.
	now = now.Add(reg.ttl + time.Second)
	reg.presence("d", live.ID, "") // the live editor's heartbeat lands in time
	if n := reg.reap(); n != 2 {
		t.Fatalf("reap removed %d, want 2 (the silent browser and agent)", n)
	}

	reg.mu.Lock()
	sess := reg.sessions["d"]
	_, ghostThere := sess.participants[ghost.ID]
	_, agentThere := sess.participants[agent.ID]
	_, liveThere := sess.participants[live.ID]
	lockCount := len(sess.locks)
	reg.mu.Unlock()
	if ghostThere {
		t.Error("silent (half-open) browser was not reaped")
	}
	if agentThere {
		t.Error("idle detached agent was not reaped")
	}
	if !liveThere {
		t.Error("heartbeating browser was wrongly reaped")
	}
	if lockCount != 0 {
		t.Errorf("reaped participant's lock was not released: %d held", lockCount)
	}
	// The surviving browser was told (presence + lock frames) the others left.
	if _, ok := drainType(t, live.ch, collabEventLock); !ok {
		t.Error("no lock frame reached the survivor after the reap")
	}
}

// TestCollabReapDiscardsEmptySession confirms reaping the last participant of a
// session drops the session, and that an empty registry reaps nothing.
func TestCollabReapDiscardsEmptySession(t *testing.T) {
	now := time.Unix(1000, 0)
	reg := newCollabRegistry()
	reg.now = func() time.Time { return now }

	if n := reg.reap(); n != 0 {
		t.Fatalf("reap of an empty registry removed %d, want 0", n)
	}
	reg.joinDetached("solo", "u1", "Claude")
	now = now.Add(2 * reg.ttl)
	if n := reg.reap(); n != 1 {
		t.Fatalf("reap removed %d, want 1", n)
	}
	reg.mu.Lock()
	_, exists := reg.sessions["solo"]
	reg.mu.Unlock()
	if exists {
		t.Error("session was not discarded after reaping its last participant")
	}
}

// TestCollabLeaveUnknown covers the defensive early returns: leaving a session
// or a participant that does not exist is a silent no-op.
func TestCollabLeaveUnknown(t *testing.T) {
	reg := newCollabRegistry()
	reg.leave("no-such-draft", "p1") // unknown session
	_, _, leave := reg.join("d", "u1", "Anja")
	defer leave()
	reg.leave("d", "ghost") // known session, unknown participant
}

func TestItoa(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{0, "0"}, {7, "7"}, {42, "42"}, {1000, "1000"}} {
		if got := itoa(tc.n); got != tc.want {
			t.Errorf("itoa(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
