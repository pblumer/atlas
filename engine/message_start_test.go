package engine_test

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// parkingResponder: messageStart("request") → messageCatch("never", keyless) → End.
// Instantiated by a "request" message, it parks at a catch that never correlates,
// so the instance the message created stays observable.
func parkingResponder(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "responder", 1)
	ms := b.AddMessageStartEvent("request", nil, false)
	park := b.AddMessageCatchEvent("never", nil)
	end := b.AddEndEvent()
	b.Connect(ms, park)
	b.Connect(park, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// singleActiveInstance returns the key of the one active process instance,
// failing if there is not exactly one.
func singleActiveInstance(t *testing.T, s *state.Store) uint64 {
	t.Helper()
	var keys []uint64
	if err := s.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("active instances = %d, want exactly 1", len(keys))
	}
	return keys[0]
}

// TestMessageStartInstantiates proves a message start event turns an incoming
// message into a fresh process instance seeded with the message payload, and
// that a message whose name matches no start event creates nothing (ADR-0035).
func TestMessageStartInstantiates(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	responder := parkingResponder(t, 7)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(responder)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// No instances until a message arrives — a message start event does not run
	// on deploy, only on correlation.
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("before any message: active=%d, want 0", pi)
	}

	// A message whose name matches no start event instantiates nothing.
	p.PublishMessage("unrelated", "")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (unrelated): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after unrelated message: active=%d, want 0", pi)
	}

	// A matching message creates an instance, seeded with the payload.
	p.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (request): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after request: active=%d, want 1 (message start instantiated)", pi)
	}
	inst := singleActiveInstance(t, h.store)
	if got := readVar(t, h.store, inst, "orderId"); got == nil || got.Text != "42" {
		t.Fatalf("new instance orderId = %+v, want 42 (payload seeded)", got)
	}
}

// TestMessageStartUndeployStopsInstantiation proves undeploying a message-start
// definition drops its start subscription: a message that would have
// instantiated it before now creates nothing (ADR-0035).
func TestMessageStartUndeployStopsInstantiation(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	responder := parkingResponder(t, 7)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(responder)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.Undeploy(responder.Key)
	p.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after request to undeployed responder: active=%d, want 0", pi)
	}
}

// TestMessageStartInstanceRecovers proves an instance created by a message start
// event recovers from the log: after replaying into a fresh store the instance
// is present and still parked, seeded variable intact. The message-start index is
// deploy-derived and rebuilt by Deploy, but the created instance recovers purely
// from the events it emitted (invariant I4).
func TestMessageStartInstanceRecovers(t *testing.T) {
	dir := t.TempDir()
	responder := parkingResponder(t, 7)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(responder)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	created := singleActiveInstance(t, h1.store)
	h1.close(t)

	// Replay into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(responder)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1 (parked instance restored)", pi, ei)
	}
	if got := readVar(t, store2, created, "orderId"); got == nil || got.Text != "42" {
		t.Fatalf("recovered instance orderId = %+v, want 42", got)
	}
}

// keyedResponder is parkingResponder with a correlation key on the start event:
// messageStart("request", key = orderId) → messageCatch("never") → End. The
// created instance parks and records the key its start event evaluated over the
// payload.
func keyedResponder(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "keyed-responder", 1)
	ms := b.AddMessageStartEvent("request", mustCompile(t, "orderId"), false)
	park := b.AddMessageCatchEvent("never", nil)
	end := b.AddEndEvent()
	b.Connect(ms, park)
	b.Connect(park, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestMessageStartRecordsCorrelationKey proves a message-start instance records
// the correlation key its start event evaluates over the incoming payload, and
// that the key survives replay (it is written into the activation event, so
// recovery rebuilds it — invariants I4/I6). An instance whose start event
// declares no key records "".
func TestMessageStartRecordsCorrelationKey(t *testing.T) {
	dir := t.TempDir()
	responder := keyedResponder(t, 7)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(responder)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	created := singleActiveInstance(t, h1.store)
	live := activeInstance(t, h1.store, created)
	if live == nil {
		t.Fatal("created instance not active")
	}
	if live.CorrelationKey != "42" {
		t.Fatalf("CorrelationKey = %q, want %q (orderId evaluated over payload)", live.CorrelationKey, "42")
	}
	h1.close(t)

	// Replay into a fresh, empty store: the key must reappear unchanged.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(responder)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if replayed := activeInstance(t, store2, created); replayed == nil || replayed.CorrelationKey != "42" {
		t.Fatalf("replayed CorrelationKey = %+v, want %q", replayed, "42")
	}
}

// TestMessageStartWithoutCorrelationKeyRecordsEmpty proves a message-start event
// with no declared correlation key produces an instance with an empty key — the
// nil expression evaluates to "" rather than erroring.
func TestMessageStartWithoutCorrelationKeyRecordsEmpty(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	responder := parkingResponder(t, 7) // start event declares no correlation key

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(responder)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	created := singleActiveInstance(t, h.store)
	if live := activeInstance(t, h.store, created); live == nil || live.CorrelationKey != "" {
		t.Fatalf("CorrelationKey = %+v, want empty (no key declared)", live)
	}
}

// responderVersion builds parkingResponder's shape at a given def key and version,
// all under the same bpmn process id "responder", so deploying two is deploying two
// versions of one process (as redeploying a model in the modeler does).
func responderVersion(t testing.TB, key uint64, version int32) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "responder", version)
	ms := b.AddMessageStartEvent("request", nil, false)
	park := b.AddMessageCatchEvent("never", nil)
	end := b.AddEndEvent()
	b.Connect(ms, park)
	b.Connect(park, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestMessageStartNewVersionSupersedes proves deploying a new version of a
// message-start process retires the prior version's start subscription, so one
// incoming message starts exactly one instance — the latest version — not one per
// deployed version. Redeploying the same model (e.g. iterating in the modeler) must
// not multiply the instances, and thus the side effects (a welcome e-mail), a single
// event produces. Timer starts already supersede this way (ADR-0051); message starts
// must match (ADR-0035/0076).
func TestMessageStartNewVersionSupersedes(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	v1 := responderVersion(t, 700, 1)
	v2 := responderVersion(t, 701, 2)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(v1)
	p.Deploy(v2) // same process id: supersedes v1's message-start subscription
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if pi := activeProcs(t, h.store); pi != 1 {
		t.Fatalf("after one request across two deployed versions: active=%d, want 1 (only the latest version starts)", pi)
	}
	inst := singleActiveInstance(t, h.store)
	if live := activeInstance(t, h.store, inst); live == nil || live.ProcessDefKey != v2.Key {
		t.Fatalf("started instance defKey=%+v, want v2 (%d)", live, v2.Key)
	}
}

// TestMessageStartSupersedeRecovers proves the supersession is deterministic across
// recovery: deployments reload oldest-first, so replaying v1 then v2 rebuilds the
// message-start index with only v2 — one message still starts exactly one instance
// after a restart (invariant I4).
func TestMessageStartSupersedeRecovers(t *testing.T) {
	dir := t.TempDir()
	v1 := responderVersion(t, 700, 1)
	v2 := responderVersion(t, 701, 2)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(v1)
	p1.Deploy(v2)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi := activeProcs(t, h1.store); pi != 1 {
		t.Fatalf("live: active=%d, want 1", pi)
	}
	h1.close(t)

	// Replay into a fresh store: deploys reload oldest-first (v1 then v2).
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(v1)
	p2.Deploy(v2)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	// A second message after recovery still starts exactly one more instance.
	p2.PublishMessage("request", "", numVar("orderId", "43"))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi := activeProcs(t, store2); pi != 2 {
		t.Fatalf("after replay + one more request: active=%d, want 2 (one per message, latest version only)", pi)
	}
}

// TestProcessInstanceKeyBuiltin proves the reserved FEEL identifier
// processInstanceKey resolves to the evaluating instance's own key, as a string
// (so the full 64-bit key is exact), usable in any expression (ADR-0035).
func TestProcessInstanceKeyBuiltin(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(7, "keyholder", 1)
	start := b.AddStartEvent()
	seed := b.AddScriptTask(mustCompile(t, "processInstanceKey"), "pik")
	end := b.AddEndEvent()
	b.Connect(start, seed)
	b.Connect(seed, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	instKey := model.NewKey(1, 1) // first key minted for the first instance
	got := readVar(t, h.store, instKey, "pik")
	if got == nil || got.Kind != model.VarString {
		t.Fatalf("pik = %+v, want a string", got)
	}
	if want := strconv.FormatUint(instKey, 10); got.Text != want {
		t.Fatalf("pik = %q, want %q (the instance's own key)", got.Text, want)
	}
}

// TestMessageStartRequestResponse is the full two-pool request/response the whole
// feature exists for: a requester stamps its own instance key as senderId
// (processInstanceKey), throws "request" carrying it, then waits for "reply"
// correlated on senderId. The "request" instantiates the responder (message
// start), which is seeded with senderId from the payload and throws "reply"
// correlated on it — waking the exact requester that asked. Both instances finish.
func TestMessageStartRequestResponse(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	// Responder: messageStart("request") → throw("reply", key = senderId) → End.
	rb := compiler.NewBuilder(20, "responder", 1)
	rms := rb.AddMessageStartEvent("request", nil, false)
	rthrow := rb.AddMessageThrowEvent("reply", mustCompile(t, "senderId"))
	rend := rb.AddEndEvent()
	rb.Connect(rms, rthrow)
	rb.Connect(rthrow, rend)
	responder, err := rb.Build()
	if err != nil {
		t.Fatalf("Build responder: %v", err)
	}

	// Requester: Start → script(senderId = processInstanceKey) → throw("request")
	//            → catch("reply", key = senderId) → End.
	qb := compiler.NewBuilder(21, "requester", 1)
	qstart := qb.AddStartEvent()
	qseed := qb.AddScriptTask(mustCompile(t, "processInstanceKey"), "senderId")
	qthrow := qb.AddMessageThrowEvent("request", nil)
	qcatch := qb.AddMessageCatchEvent("reply", mustCompile(t, "senderId"))
	qend := qb.AddEndEvent()
	qb.Connect(qstart, qseed)
	qb.Connect(qseed, qthrow)
	qb.Connect(qthrow, qcatch)
	qb.Connect(qcatch, qend)
	requester, err := qb.Build()
	if err != nil {
		t.Fatalf("Build requester: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(responder)
	p.Deploy(requester)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Start one requester; the whole exchange runs to completion in one drain.
	p.CreateInstance(requester.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Both the requester and the message-started responder have finished; nothing
	// is left parked, which is only possible if "reply" correlated back to the
	// requester on its own instance key.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after exchange: process=%d element=%d, want 0 and 0 (reply correlated home)", pi, ei)
	}
}
