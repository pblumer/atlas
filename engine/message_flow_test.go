package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// flowRow is the message-flow history read back for assertions.
type flowRow struct {
	sender      uint64
	receiver    uint64
	receiverDef uint64
	elementId   int32
	name        string
	key         string
}

// messageFlows reads a definition's retained message-flow history in time order.
func messageFlows(t *testing.T, s *state.Store, defKey uint64) []flowRow {
	t.Helper()
	var out []flowRow
	if err := s.MessageFlowHistory(defKey, func(_ int64, _ uint64, v *model.MessageFlowValue) error {
		out = append(out, flowRow{
			sender:      v.SenderProcessInstanceKey,
			receiver:    v.ReceiverProcessInstanceKey,
			receiverDef: v.ReceiverProcessDefKey,
			elementId:   v.ReceiverElementId,
			name:        v.MessageName,
			key:         v.CorrelationKey,
		})
		return nil
	}); err != nil {
		t.Fatalf("MessageFlowHistory(%d): %v", defKey, err)
	}
	return out
}

// buildKundeLieferant is the collaboration the replay feature exists for: a Kunde
// pool throws "order" (which a message start event opens the Lieferant pool with),
// then waits for "confirm"; the Lieferant throws "confirm" back, correlated on the
// order id. It exercises both delivery kinds — a message start ("order") and an
// intermediate catch ("confirm") — which are the two events that record a flow.
func buildKundeLieferant(t *testing.T) (kunde, lieferant *compiler.CompiledProcess, kundeCatch, lieferantStart int32) {
	t.Helper()

	kb := compiler.NewBuilder(30, "kunde", 1)
	kstart := kb.AddStartEvent()
	kthrow := kb.AddMessageThrowEvent("order", mustCompile(t, "orderId"))
	kcatch := kb.AddMessageCatchEvent("confirm", mustCompile(t, "orderId"))
	kend := kb.AddEndEvent()
	kb.Connect(kstart, kthrow)
	kb.Connect(kthrow, kcatch)
	kb.Connect(kcatch, kend)
	kunde, err := kb.Build()
	if err != nil {
		t.Fatalf("Build kunde: %v", err)
	}

	lb := compiler.NewBuilder(31, "lieferant", 1)
	lstart := lb.AddMessageStartEvent("order", nil)
	lthrow := lb.AddMessageThrowEvent("confirm", mustCompile(t, "orderId"))
	lend := lb.AddEndEvent()
	lb.Connect(lstart, lthrow)
	lb.Connect(lthrow, lend)
	lieferant, err = lb.Build()
	if err != nil {
		t.Fatalf("Build lieferant: %v", err)
	}
	return kunde, lieferant, kcatch, lstart
}

// TestMessageFlowRecorded proves that running the Kunde/Lieferant collaboration
// records one retained message flow per cross-pool delivery: "order" into the
// Lieferant's message start event and "confirm" into the Kunde's catch event,
// each naming its receiving element and correlation key (ADR-0038).
func TestMessageFlowRecorded(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	kunde, lieferant, kundeCatch, lieferantStart := buildKundeLieferant(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(kunde)
	p.Deploy(lieferant)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.CreateInstance(kunde.Key, numVar("orderId", "42"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The whole exchange completes only if "confirm" correlated home to the Kunde.
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after exchange: process=%d element=%d, want 0 and 0", pi, ei)
	}

	// "order" was delivered into the Lieferant's message start event. The receiver
	// instance did not exist when the flow was recorded (message start creates it
	// as a followup), so its key is 0; the sender is the Kunde instance.
	lf := messageFlows(t, h.store, lieferant.Key)
	if len(lf) != 1 {
		t.Fatalf("lieferant flows = %d, want 1", len(lf))
	}
	if lf[0].name != "order" || lf[0].key != "42" || lf[0].elementId != lieferantStart {
		t.Errorf("order flow = %+v, want name=order key=42 element=%d", lf[0], lieferantStart)
	}
	if lf[0].receiverDef != lieferant.Key || lf[0].receiver != 0 || lf[0].sender == 0 {
		t.Errorf("order flow routing = %+v, want receiverDef=%d receiver=0 sender!=0", lf[0], lieferant.Key)
	}

	// "confirm" was delivered into the Kunde's catch event, back to the Kunde
	// instance; the sender is the Lieferant instance.
	kf := messageFlows(t, h.store, kunde.Key)
	if len(kf) != 1 {
		t.Fatalf("kunde flows = %d, want 1", len(kf))
	}
	if kf[0].name != "confirm" || kf[0].key != "42" || kf[0].elementId != kundeCatch {
		t.Errorf("confirm flow = %+v, want name=confirm key=42 element=%d", kf[0], kundeCatch)
	}
	if kf[0].receiverDef != kunde.Key || kf[0].receiver == 0 || kf[0].sender == 0 {
		t.Errorf("confirm flow routing = %+v, want receiverDef=%d receiver!=0 sender!=0", kf[0], kunde.Key)
	}
	// The two pools are tied together: confirm's receiver is order's sender.
	if kf[0].receiver != lf[0].sender {
		t.Errorf("confirm receiver %d != order sender %d (should both be the Kunde instance)", kf[0].receiver, lf[0].sender)
	}
}

// TestMessageFlowHistoryRecovers is the recovery property (invariant I4) for the
// message-flow history: replaying the log into a fresh store rebuilds the exact
// same retained flows, since each is written from applyToState off the event
// alone (ADR-0038).
func TestMessageFlowHistoryRecovers(t *testing.T) {
	dir := t.TempDir()
	kunde, lieferant, _, _ := buildKundeLieferant(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(kunde)
	p1.Deploy(lieferant)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(kunde.Key, numVar("orderId", "42"))
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	wantKunde := messageFlows(t, h1.store, kunde.Key)
	wantLieferant := messageFlows(t, h1.store, lieferant.Key)
	h1.close(t)

	// Replay the log into a fresh, empty store.
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
	p2.Deploy(kunde)
	p2.Deploy(lieferant)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}

	if got := messageFlows(t, store2, kunde.Key); !equalFlows(got, wantKunde) {
		t.Errorf("recovered kunde flows = %+v, want %+v", got, wantKunde)
	}
	if got := messageFlows(t, store2, lieferant.Key); !equalFlows(got, wantLieferant) {
		t.Errorf("recovered lieferant flows = %+v, want %+v", got, wantLieferant)
	}
}

func equalFlows(a, b []flowRow) bool {
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
