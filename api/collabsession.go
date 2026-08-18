package api

import (
	"encoding/json"
	"sync"
	"time"
)

// This file implements the first slice of ADR-0139: live collaborative modeling
// sessions. A collaboration session is an ephemeral, design-time coordination
// object attached to a draft (by process id). It tracks who is present, which
// BPMN element each participant has locked, and fans out presence / lock /
// change events to every participant over Server-Sent Events so several people
// (and an AI agent joined over MCP) can co-edit one draft in real time.
//
// It is NOT event-sourced, NOT durable across a restart, and never touches the
// engine — the durable artifact remains the draft (ADR-0021); a session is only
// the live coordination around editing it. Like the login session store
// (auth.go) it is reached from concurrent HTTP handler goroutines rather than
// the run loop, so it guards itself with a mutex and sits entirely outside the
// six invariants.
//
// Concurrency model (ADR-0139, first cut): per-element soft locks. An editor
// acquires an element before mutating it; a second acquire of a held element is
// refused. Edits to different elements proceed in parallel. This is always
// correct — there is no merge algorithm to get wrong — and leaves the transport
// and session API unchanged when a later slice upgrades to an operation
// log / CRDT.

// Collaboration event types fanned out on a session's stream. Mirrors ADR-0139:
// sync is the full snapshot a newcomer receives on join; presence, lock, and
// change are the incremental frames. presence and lock frames each carry the
// *entire* current roster / lock set, so a dropped frame self-heals on the next
// one (reconnect/replay is an ADR-0139 follow-up, not needed for correctness).
const (
	collabEventSync     = "sync"
	collabEventPresence = "presence"
	collabEventLock     = "lock"
	collabEventChange   = "change"
)

// collabBufferSize bounds a participant's outgoing event buffer. A consumer that
// falls this far behind has its frame dropped rather than blocking every other
// participant; because presence and lock frames are full snapshots, the next one
// restores its view.
const collabBufferSize = 64

// Lock actions a participant may request on an element.
const (
	collabLockAcquire = "acquire"
	collabLockRelease = "release"
)

// collabEvent is one frame delivered to a participant's SSE stream. Data is the
// already-marshaled JSON payload; Seq is a per-session monotonic counter carried
// as the SSE id so a future reconnect can resume (ADR-0139 follow-up).
type collabEvent struct {
	Type string
	Data []byte
	Seq  uint64
}

// collabParticipant is one connected editor — a person in the Modeler or an AI
// agent joined over MCP. ch is its SSE outbox; selection is the element it
// currently has selected (presence).
//
// detached marks a participant with no live SSE stream (an agent that joined over
// MCP and reads by polling) versus a streaming browser. Every participant carries
// lastSeen — refreshed on each action, and for a browser also on a periodic client
// heartbeat — and the TTL reaper evicts any that falls silent past the TTL. A
// browser stream is normally reaped the instant its connection drops (a failed
// keepalive write); the TTL is the backstop for a half-open connection whose write
// keeps succeeding into a dead socket, so a forgotten tab can never wedge a lock.
type collabParticipant struct {
	ID        string
	UserID    string // resolved principal (ADR-0044); empty when auth is off
	Name      string // display name
	selection string
	ch        chan collabEvent
	detached  bool      // joined without an SSE stream (an MCP agent) rather than a browser stream
	lastSeen  time.Time // last action/heartbeat time; the TTL reaper checks it for every participant
	canEdit   bool      // project role permits editing (editor/owner); a viewer joins read-only (ADR-0071)
}

// canEdit reports whether a participant may mutate the session (lock/change/
// presence), snapshotted from its project role at join time (ADR-0139/0071). ok
// is false when the session or participant is unknown. Like ADR-0044's role
// snapshot, a role change takes effect on the participant's next join.
func (reg *collabRegistry) canEdit(draftID, participantID string) (canEdit, ok bool) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	sess := reg.sessions[draftID]
	if sess == nil {
		return false, false
	}
	p, exists := sess.participants[participantID]
	if !exists {
		return false, false
	}
	return p.canEdit, true
}

// collabLock is a soft, per-element edit lock held by one participant.
type collabLock struct {
	ElementID  string `json:"elementId"`
	HolderID   string `json:"holderId"`
	HolderName string `json:"holderName"`
}

// collabSession is the live coordination state for one draft: its participants,
// its held element locks, and a monotonic sequence counter.
type collabSession struct {
	participants map[string]*collabParticipant
	locks        map[string]collabLock // elementID → lock
	seq          uint64
}

// broadcast fans a frame out to every participant, non-blocking. A slow consumer
// (full buffer) is skipped, not waited on, so one stalled SSE stream can never
// wedge the session. Must be called with the registry mutex held.
func (sess *collabSession) broadcast(typ string, data []byte) {
	sess.broadcastExcept(typ, data, "")
}

// broadcastExcept is broadcast that skips one participant id — used for the join
// frame, which the newcomer does not need (it just received the full roster in
// its sync snapshot). An empty except sends to everyone.
func (sess *collabSession) broadcastExcept(typ string, data []byte, except string) {
	sess.seq++
	ev := collabEvent{Type: typ, Data: data, Seq: sess.seq}
	for id, p := range sess.participants {
		if id == except {
			continue
		}
		select {
		case p.ch <- ev:
		default: // slow consumer: drop; the next full snapshot restores its view
		}
	}
}

// collabPresence is one participant as seen by others in a presence snapshot.
type collabPresence struct {
	ID        string `json:"id"`
	UserID    string `json:"userId,omitempty"`
	Name      string `json:"name"`
	Selection string `json:"selection,omitempty"`
}

// collabParticipantTTL is how long any participant may go without a sign of life
// before the reaper evicts it and releases its locks. An MCP agent refreshes it by
// polling; a browser refreshes it on every action and on a periodic heartbeat (the
// client re-announces presence well inside this window). A crashed agent or a
// forgotten tab falls silent and is cleaned up so its locks never wedge an element
// forever. collabReapInterval is how often the reaper sweeps.
const (
	collabParticipantTTL = 90 * time.Second
	collabReapInterval   = 30 * time.Second
)

// collabKeepaliveInterval is how often an idle SSE session stream writes a keepalive
// comment. A failed write reveals a dropped browser connection and reaps it at once
// (via the deferred leave()); the TTL reaper above is the slower backstop for a
// half-open connection whose keepalive write still succeeds into a dead socket.
const collabKeepaliveInterval = 15 * time.Second

// collabRegistry holds every live draft session in memory. It is mutex-guarded
// because concurrent HTTP handlers (SSE streams and POSTs) reach it directly; it
// is not engine state and never persists (ADR-0139).
type collabRegistry struct {
	mu       sync.Mutex
	sessions map[string]*collabSession
	newID    func() (string, error) // participant id minter; seam for tests
	now      func() time.Time       // clock seam for the reaper (deterministic in tests)
	ttl      time.Duration          // detached-participant idle TTL
}

// newCollabRegistry builds an empty registry whose participant ids are random
// hex (unguessable, unique without a counter), matching the session-token style.
func newCollabRegistry() *collabRegistry {
	return &collabRegistry{
		sessions: map[string]*collabSession{},
		newID:    func() (string, error) { return randomHex(12) },
		now:      time.Now,
		ttl:      collabParticipantTTL,
	}
}

// rosterLocked builds the current presence roster. Caller holds the mutex.
func (sess *collabSession) rosterLocked() []collabPresence {
	out := make([]collabPresence, 0, len(sess.participants))
	for _, p := range sess.participants {
		out = append(out, collabPresence{ID: p.ID, UserID: p.UserID, Name: p.Name, Selection: p.selection})
	}
	return out
}

// lockListLocked builds the current lock set. Caller holds the mutex.
func (sess *collabSession) lockListLocked() []collabLock {
	out := make([]collabLock, 0, len(sess.locks))
	for _, l := range sess.locks {
		out = append(out, l)
	}
	return out
}

// join adds a streaming (browser SSE) participant that may edit — the default for
// an open (auth-off) session. joinStream is the scope-aware variant the HTTP
// handler uses to pass the draft's project role (ADR-0139/0071).
func (reg *collabRegistry) join(draftID, userID, name string) (*collabParticipant, []byte, func()) {
	return reg.joinStream(draftID, userID, name, true)
}

// joinStream adds a streaming (browser SSE) participant and returns the
// participant, the marshaled sync snapshot it should receive first (self id,
// roster, locks), and a leave function the caller defers to tear the participant
// down on disconnect. canEdit records whether the participant's project role lets
// it change the model (editor/owner) or only watch (viewer). Other participants
// receive a presence frame reflecting the arrival.
func (reg *collabRegistry) joinStream(draftID, userID, name string, canEdit bool) (*collabParticipant, []byte, func()) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	p, sync := reg.joinLocked(draftID, userID, name, streamingParticipant, canEdit)
	return p, sync, func() { reg.leave(draftID, p.ID) }
}

// joinDetached adds an edit-capable participant with no live stream — the default
// for an open session. joinDetachedAs is the scope-aware variant.
func (reg *collabRegistry) joinDetached(draftID, userID, name string) (*collabParticipant, []byte) {
	return reg.joinDetachedAs(draftID, userID, name, true)
}

// joinDetachedAs adds a participant with no live stream (an AI agent over MCP,
// ADR-0139 M2), carrying its edit capability. It returns the participant and its
// sync snapshot but no leave closure: such a participant leaves explicitly, or
// the reaper evicts it once it falls silent past the TTL.
func (reg *collabRegistry) joinDetachedAs(draftID, userID, name string, canEdit bool) (*collabParticipant, []byte) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	return reg.joinLocked(draftID, userID, name, detachedParticipant, canEdit)
}

// detachedParticipant / streamingParticipant name the joinLocked flag for
// readability at the call sites.
const (
	streamingParticipant = false
	detachedParticipant  = true
)

// joinLocked creates the session if needed, adds a participant, builds its sync
// snapshot, and notifies the others (the newcomer already has the roster in its
// snapshot, so it is excluded from its own join frame). Caller holds the mutex.
func (reg *collabRegistry) joinLocked(draftID, userID, name string, detached, canEdit bool) (*collabParticipant, []byte) {
	sess := reg.sessions[draftID]
	if sess == nil {
		sess = &collabSession{participants: map[string]*collabParticipant{}, locks: map[string]collabLock{}}
		reg.sessions[draftID] = sess
	}
	id, err := reg.newID()
	if err != nil {
		// A failure of crypto/rand is catastrophic and vanishingly rare; fall back
		// to a sequence-derived id so a session can still form rather than panicking.
		id = "p" + itoa(int(sess.seq)+len(sess.participants)+1)
	}
	p := &collabParticipant{
		ID: id, UserID: userID, Name: name, detached: detached, canEdit: canEdit,
		lastSeen: reg.now(), ch: make(chan collabEvent, collabBufferSize),
	}
	sess.participants[id] = p

	sync, _ := json.Marshal(struct {
		Self         string           `json:"self"`
		CanEdit      bool             `json:"canEdit"`
		Participants []collabPresence `json:"participants"`
		Locks        []collabLock     `json:"locks"`
	}{Self: id, CanEdit: canEdit, Participants: sess.rosterLocked(), Locks: sess.lockListLocked()})

	reg.broadcastPresenceExceptLocked(sess, id)
	return p, sync
}

// removeParticipantLocked deletes a participant, closes its outbox, and releases
// any locks it held, reporting whether a lock was released. It performs no
// broadcast and no empty-session cleanup — the caller (leave, reap) does that,
// since a reap sweep batches those per session. Caller holds the mutex.
func (reg *collabRegistry) removeParticipantLocked(sess *collabSession, participantID string) (released bool) {
	p, ok := sess.participants[participantID]
	if !ok {
		return false
	}
	delete(sess.participants, participantID)
	close(p.ch)
	for elem, l := range sess.locks {
		if l.HolderID == participantID {
			delete(sess.locks, elem)
			released = true
		}
	}
	return released
}

// leave removes a participant, releasing any locks it held, and notifies the
// rest. When the last participant leaves, the session is discarded so an idle
// draft holds no memory.
func (reg *collabRegistry) leave(draftID, participantID string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()

	sess := reg.sessions[draftID]
	if sess == nil {
		return
	}
	if _, ok := sess.participants[participantID]; !ok {
		return
	}
	released := reg.removeParticipantLocked(sess, participantID)
	if len(sess.participants) == 0 {
		delete(reg.sessions, draftID)
		return
	}
	reg.broadcastPresenceLocked(sess)
	if released {
		reg.broadcastLocksLocked(sess)
	}
}

// reap evicts every participant that has gone silent past the TTL, releasing its
// locks so a crashed MCP agent or a forgotten browser tab never holds an element
// forever. A browser is normally reaped the instant its stream drops; this is the
// backstop for a half-open connection — its lastSeen is kept fresh by the client's
// periodic heartbeat, so a live but idle editor is never evicted. It broadcasts an
// updated roster (and lock set, if any changed) to each affected session, discards
// any that empties, and returns how many participants it removed.
func (reg *collabRegistry) reap() int {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	cutoff := reg.now().Add(-reg.ttl)
	reaped := 0
	for draftID, sess := range reg.sessions {
		var stale []string
		for id, p := range sess.participants {
			if p.lastSeen.Before(cutoff) {
				stale = append(stale, id)
			}
		}
		if len(stale) == 0 {
			continue
		}
		released := false
		for _, id := range stale {
			if reg.removeParticipantLocked(sess, id) {
				released = true
			}
			reaped++
		}
		if len(sess.participants) == 0 {
			delete(reg.sessions, draftID)
			continue
		}
		reg.broadcastPresenceLocked(sess)
		if released {
			reg.broadcastLocksLocked(sess)
		}
	}
	return reaped
}

// presence records a participant's new selection and broadcasts the roster. It
// reports ok=false when the session or participant is unknown (a stale client).
func (reg *collabRegistry) presence(draftID, participantID, selection string) bool {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	sess := reg.sessions[draftID]
	if sess == nil {
		return false
	}
	p, ok := sess.participants[participantID]
	if !ok {
		return false
	}
	p.lastSeen = reg.now()
	p.selection = selection
	reg.broadcastPresenceLocked(sess)
	return true
}

// acquireLock grants participantID the edit lock on elementID. granted is false
// when another participant already holds it (the conflict the first-cut model
// forbids); ok is false when the session or participant is unknown. Re-acquiring
// a lock you already hold succeeds idempotently.
func (reg *collabRegistry) acquireLock(draftID, participantID, elementID string) (granted, ok bool) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	sess := reg.sessions[draftID]
	if sess == nil {
		return false, false
	}
	p, exists := sess.participants[participantID]
	if !exists {
		return false, false
	}
	p.lastSeen = reg.now()
	if l, held := sess.locks[elementID]; held && l.HolderID != participantID {
		return false, true
	}
	sess.locks[elementID] = collabLock{ElementID: elementID, HolderID: participantID, HolderName: p.Name}
	reg.broadcastLocksLocked(sess)
	return true, true
}

// releaseLock drops participantID's lock on elementID. It is idempotent: it
// succeeds whether or not the lock was held, but only touches a lock the caller
// actually owns. ok is false when the session or participant is unknown.
func (reg *collabRegistry) releaseLock(draftID, participantID, elementID string) bool {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	sess := reg.sessions[draftID]
	if sess == nil {
		return false
	}
	p, exists := sess.participants[participantID]
	if !exists {
		return false
	}
	p.lastSeen = reg.now()
	if l, held := sess.locks[elementID]; held && l.HolderID == participantID {
		delete(sess.locks, elementID)
		reg.broadcastLocksLocked(sess)
	}
	return true
}

// change fans a participant's element edit out to the session. The registry does
// not persist it — the draft's durable save path (ADR-0021) is separate — it
// only relays the change so every open canvas updates live. ok is false when the
// session or participant is unknown.
func (reg *collabRegistry) change(draftID, participantID, elementID, xml string) bool {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	sess := reg.sessions[draftID]
	if sess == nil {
		return false
	}
	p, ok := sess.participants[participantID]
	if !ok {
		return false
	}
	p.lastSeen = reg.now()
	data, _ := json.Marshal(struct {
		By        string `json:"by"`
		ByName    string `json:"byName"`
		ElementID string `json:"elementId"`
		XML       string `json:"xml,omitempty"`
	}{By: participantID, ByName: p.Name, ElementID: elementID, XML: xml})
	sess.broadcast(collabEventChange, data)
	return true
}

// pollEvent is one buffered frame handed back by poll: the same shape as an SSE
// frame, but delivered in a request/response batch for a participant that has no
// live stream (an AI agent over MCP, ADR-0139 M2).
type pollEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
	Seq  uint64          `json:"seq"`
}

// poll drains a participant's buffered frames and returns them together with the
// current roster and lock set, marshaled as JSON. It is the non-streaming
// counterpart of the SSE endpoint: an agent that cannot hold an event stream
// calls this to see what changed (and it doubles as the agent's liveness signal).
// The full roster/lock snapshot means a poll is self-correcting even if buffered
// change frames were dropped under load. ok is false for an unknown session or
// participant (a stale agent that should rejoin).
func (reg *collabRegistry) poll(draftID, participantID string) ([]byte, bool) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	sess := reg.sessions[draftID]
	if sess == nil {
		return nil, false
	}
	p, ok := sess.participants[participantID]
	if !ok {
		return nil, false
	}
	p.lastSeen = reg.now() // the poll is a detached agent's liveness signal
	events := []pollEvent{}
drain:
	for {
		select {
		case ev := <-p.ch:
			events = append(events, pollEvent{Type: ev.Type, Data: json.RawMessage(ev.Data), Seq: ev.Seq})
		default:
			break drain
		}
	}
	out, _ := json.Marshal(struct {
		Self         string           `json:"self"`
		Participants []collabPresence `json:"participants"`
		Locks        []collabLock     `json:"locks"`
		Events       []pollEvent      `json:"events"`
	}{Self: participantID, Participants: sess.rosterLocked(), Locks: sess.lockListLocked(), Events: events})
	return out, true
}

// broadcastPresenceLocked emits the full roster as a presence frame to everyone.
// Caller holds the mutex.
func (reg *collabRegistry) broadcastPresenceLocked(sess *collabSession) {
	reg.broadcastPresenceExceptLocked(sess, "")
}

// broadcastPresenceExceptLocked emits the full roster as a presence frame,
// skipping one participant id (empty = everyone). Caller holds the mutex.
func (reg *collabRegistry) broadcastPresenceExceptLocked(sess *collabSession, except string) {
	data, _ := json.Marshal(struct {
		Participants []collabPresence `json:"participants"`
	}{Participants: sess.rosterLocked()})
	sess.broadcastExcept(collabEventPresence, data, except)
}

// broadcastLocksLocked emits the full lock set as a lock frame. Caller holds the
// mutex.
func (reg *collabRegistry) broadcastLocksLocked(sess *collabSession) {
	data, _ := json.Marshal(struct {
		Locks []collabLock `json:"locks"`
	}{Locks: sess.lockListLocked()})
	sess.broadcast(collabEventLock, data)
}

// itoa is a tiny, dependency-free int→string for the crypto-rand fallback id.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
