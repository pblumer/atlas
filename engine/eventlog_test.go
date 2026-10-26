package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The event log (ADR-0472): every send, every reception, every start a send
// caused, folded from the log into a table, oldest first here.

func eventLog(t *testing.T, s *state.Store) []state.EventLogEntry {
	t.Helper()
	var out []state.EventLogEntry
	if err := s.EventLogAfter(1, 0, func(e state.EventLogEntry) error {
		out = append(out, e)
		return nil
	}); err != nil {
		t.Fatalf("EventLogAfter: %v", err)
	}
	return out
}

func kinds(rows []state.EventLogEntry) []state.EventLogKind {
	out := make([]state.EventLogKind, len(rows))
	for i, r := range rows {
		out[i] = r.Kind
	}
	return out
}

// signalStarter: a signal start "cancelled" → End.
func signalStarter(t testing.TB, key uint64) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(key, "starter", 1)
	start := b.AddSignalStartEvent("cancelled")
	end := b.AddEndEvent()
	b.Connect(start, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestTheEventLogRecordsABroadcastWhereItArrivedAndWhatItStarted: a throw's reception
// names the thrower, the start it caused is a row, and the send comes last with how far
// it reached. All three share the cause, and a replay into an empty store folds the
// identical rows (I4).
func TestTheEventLogRecordsABroadcastWhereItArrivedAndWhatItStarted(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	catcher, thrower, starter := signalCatcher(t, 8), signalThrower(t, 9), signalStarter(t, 10)
	clock := &manualClock{}
	p := engine.New(1, h.log, h.store, clock)
	for _, cp := range []*compiler.CompiledProcess{catcher, thrower, starter} {
		p.Deploy(cp)
	}
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(catcher.Key)
	runIdle(t, p, "catcher")
	catcherKey := activeInstanceOf(t, h.store, catcher.Key)
	p.CreateInstance(thrower.Key, model.VariableValue{Name: "secret", Kind: model.VarString, Text: "never in the log"})
	runIdle(t, p, "throw")

	rows := eventLog(t, h.store)
	want := []state.EventLogKind{state.EventLogSignalReceived, state.EventLogSignalStarted, state.EventLogDispatched}
	if got := kinds(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("event log kinds = %v, want %v", got, want)
	}
	recv, started, sent := rows[0], rows[1], rows[2]
	if recv.Signal.ProcessInstanceKey != catcherKey || recv.Signal.SignalName != "cancelled" || recv.Signal.Stale {
		t.Errorf("reception = %+v, want the catcher's instance %d", recv.Signal, catcherKey)
	}
	if started.Signal.ProcessDefKey != starter.Key || started.Signal.ProcessInstanceKey != 0 {
		t.Errorf("start = %+v, want the starter's definition and no instance yet", started.Signal)
	}
	d := sent.Dispatch
	if d.Channel != model.DispatchSignal || d.Source != model.DispatchFromProcess || d.Name != "cancelled" ||
		d.SenderProcessDefKey != thrower.Key || d.Reached != 1 || d.Started != 1 {
		t.Errorf("send = %+v, want the thrower's broadcast that reached one and started one", d)
	}
	if recv.Signal.SenderProcessInstanceKey != d.SenderProcessInstanceKey || started.Signal.SenderProcessInstanceKey != d.SenderProcessInstanceKey {
		t.Errorf("the reception and the start name sender %d and %d, the send %d",
			recv.Signal.SenderProcessInstanceKey, started.Signal.SenderProcessInstanceKey, d.SenderProcessInstanceKey)
	}
	if recv.Cause != sent.Cause || started.Cause != sent.Cause || sent.Cause == 0 {
		t.Errorf("causes = %d, %d, %d, want one shared cause", recv.Cause, started.Cause, sent.Cause)
	}
	h.close(t)

	// Replay into an empty store: the same rows under the same keys.
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
	for _, cp := range []*compiler.CompiledProcess{catcher, thrower, starter} {
		p2.Deploy(cp)
	}
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if replayed := eventLog(t, store2); !reflect.DeepEqual(replayed, rows) {
		t.Fatalf("replayed event log differs:\n got %+v\nwant %+v", replayed, rows)
	}
}

// TestAThrowThatReachesNobodyIsLogged: the case the log exists for — a send with both
// counts zero, and no reception.
func TestAThrowThatReachesNobodyIsLogged(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	thrower := signalThrower(t, 9)
	p := bufferProcessor(t, h, &manualClock{}, thrower)
	p.CreateInstance(thrower.Key)
	runIdle(t, p, "throw")
	rows := eventLog(t, h.store)
	if len(rows) != 1 || rows[0].Kind != state.EventLogDispatched || rows[0].Dispatch.Reached != 0 || rows[0].Dispatch.Started != 0 {
		t.Fatalf("event log = %+v, want one send that reached nobody", rows)
	}
}

// TestAStaleSubscriptionIsNoReception: a boundary whose host finished leaves its
// subscription behind until a later broadcast clears it (lazy cleanup). That clearing is
// no delivery: the correlation says it is stale, the log has no reception, and the send
// counts nobody reached (ADR-0472 §2).
func TestAStaleSubscriptionIsNoReception(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	b := compiler.NewBuilder(11, "guarded", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("work", 3)
	bound := b.AddBoundarySignalEvent(task, false, "cancelled")
	end, end2 := b.AddEndEvent(), b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.Connect(bound, end2)
	guarded, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := guarded.ServiceTask(guarded.Node(task).Detail).JobType
	thrower := signalThrower(t, 9)
	p := bufferProcessor(t, h, &manualClock{}, guarded, thrower)

	p.CreateInstance(guarded.Key)
	runIdle(t, p, "guarded")
	p.CompleteJob(singleActivatableJob(t, h.store, jobType))
	runIdle(t, p, "host done")
	if pi, _ := counts(t, h.store); pi != 0 {
		t.Fatalf("the guarded instance did not finish: %d instances", pi)
	}
	before := correlatedSignals(t, h)

	p.CreateInstance(thrower.Key)
	runIdle(t, p, "throw")
	stale := correlatedSignals(t, h)[len(before):]
	if len(stale) != 1 || !stale[0].Stale || stale[0].SenderProcessInstanceKey == 0 {
		t.Fatalf("correlations written by the throw = %+v, want the boundary's left-behind subscription, stale", stale)
	}
	rows := eventLog(t, h.store)
	if len(rows) != 1 || rows[0].Kind != state.EventLogDispatched || rows[0].Dispatch.Reached != 0 {
		t.Fatalf("event log = %+v, want the send alone, reaching nobody", rows)
	}
}

// correlatedSignals is every SubscriptionCorrelated signal record on the log.
func correlatedSignals(t *testing.T, h *harness) []model.SignalSubscriptionValue {
	t.Helper()
	var out []model.SignalSubscriptionValue
	if err := h.log.Replay(func(raw []byte) error {
		rec, err := model.ReadRecord(raw)
		if err != nil {
			return err
		}
		if rec.Header.ValueType == model.VTSignal && rec.Header.Intent == model.IntentSubscriptionCorrelated {
			out = append(out, *rec.Value.(*model.SignalSubscriptionValue))
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return out
}

// TestTheEventLogFollowsAMessageThroughTheBuffer: a publish with a time to live that
// nothing receives is a send that reached nobody and a message kept; the subscription
// that opens later takes it, and that is a row and a delivery; a second one expires
// unreceived, and that is a row too. Nothing of the payload is written.
func TestTheEventLogFollowsAMessageThroughTheBuffer(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	catcher := catcherProcess(t, 7)
	clk := &fixedClock{t: 0}
	p := bufferProcessor(t, h, clk, catcher)

	p.PublishMessageWithTTL("order", "42", "", time.Minute, model.VariableValue{Name: "payload", Kind: model.VarString, Text: "early"})
	runIdle(t, p, "publish")
	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	runIdle(t, p, "catcher")
	p.PublishMessageWithTTL("order", "7", "", time.Second)
	runIdle(t, p, "publish unreceived")
	clk.t = int64(2 * time.Second)
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}

	rows := eventLog(t, h.store)
	want := []state.EventLogKind{
		state.EventLogDispatched, state.EventLogMessageKept, // the first publish
		state.EventLogMessageTaken, state.EventLogMessageDelivered, // the catcher takes it
		state.EventLogDispatched, state.EventLogMessageKept, // the second publish
		state.EventLogMessageExpired, // and its expiry
	}
	if got := kinds(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("event log kinds = %v, want %v", got, want)
	}
	if d := rows[0].Dispatch; d.Channel != model.DispatchMessage || d.Source != model.DispatchFromPublish || d.Reached != 0 || d.CorrelationKey != "42" {
		t.Errorf("first send = %+v, want a publish of key 42 that reached nobody", d)
	}
	if rows[0].Cause != rows[1].Cause || rows[2].Cause != rows[3].Cause {
		t.Errorf("causes = %d %d / %d %d, want a send with its keeping and a taking with its delivery to share one",
			rows[0].Cause, rows[1].Cause, rows[2].Cause, rows[3].Cause)
	}
	for _, r := range rows {
		if r.Message != nil && len(r.Message.Variables) != 0 {
			t.Errorf("row %d carries the payload %+v", r.Position, r.Message.Variables)
		}
	}
	if f := rows[3].Flow; f.ReceiverProcessDefKey != catcher.Key || f.CorrelationKey != "42" {
		t.Errorf("delivery = %+v, want the catcher on key 42", f)
	}
}

// TestADeliveredMessageWithAnIdLeavesNoExpiry: a message delivered at once with an id
// is remembered until its expiry, and that expiry drops only the id: it is no row.
func TestADeliveredMessageWithAnIdLeavesNoExpiry(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	catcher := catcherProcess(t, 7)
	clk := &fixedClock{t: 0}
	p := bufferProcessor(t, h, clk, catcher)
	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	runIdle(t, p, "catcher")
	p.PublishMessageWithTTL("order", "42", "id-1", time.Second)
	runIdle(t, p, "publish")
	clk.t = int64(2 * time.Second)
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	want := []state.EventLogKind{state.EventLogMessageDelivered, state.EventLogDispatched}
	if got := kinds(eventLog(t, h.store)); !reflect.DeepEqual(got, want) {
		t.Fatalf("event log kinds = %v, want %v", got, want)
	}
}

// TestTheEventLogIsPrunedByAFact: a prune through a position drops the rows up to it
// and remembers the cut (ADR-0472 §4).
func TestTheEventLogIsPrunedByAFact(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	thrower := signalThrower(t, 9)
	p := bufferProcessor(t, h, &manualClock{}, thrower)
	for i := 0; i < 3; i++ {
		p.CreateInstance(thrower.Key)
		runIdle(t, p, "throw")
	}
	rows := eventLog(t, h.store)
	if len(rows) != 3 {
		t.Fatalf("event log = %d rows, want 3 sends", len(rows))
	}
	p.PruneEventLog(rows[1].Position)
	runIdle(t, p, "prune")
	left := eventLog(t, h.store)
	if len(left) != 1 || left[0].Position != rows[2].Position {
		t.Fatalf("after the prune = %+v, want the last send alone", left)
	}
	if through, err := h.store.EventLogPrunedThrough(1); err != nil || through != rows[1].Position {
		t.Fatalf("pruned through = %d, %v; want %d", through, err, rows[1].Position)
	}
}
