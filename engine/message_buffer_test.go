package engine_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// bufferedMessages lists what the message buffer holds (ADR-0370).
func bufferedMessages(t *testing.T, s *state.Store) []model.MessageValue {
	t.Helper()
	var out []model.MessageValue
	if err := s.BufferedMessages(func(_ uint64, v *model.MessageValue) error {
		out = append(out, *v)
		return nil
	}); err != nil {
		t.Fatalf("BufferedMessages: %v", err)
	}
	return out
}

// bufferProcessor opens a processor over a fresh harness with the given clock and
// definitions, recovered and ready.
func bufferProcessor(t *testing.T, h *harness, clk engine.Clock, cps ...*compiler.CompiledProcess) *engine.Processor {
	t.Helper()
	p := engine.New(1, h.log, h.store, clk)
	for _, cp := range cps {
		p.Deploy(cp)
	}
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return p
}

func runIdle(t *testing.T, p *engine.Processor, what string) {
	t.Helper()
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (%s): %v", what, err)
	}
}

// A message published with a time to live, before anything waits for it, is kept,
// and the first subscription that opens on its name and key receives it — once
// (ADR-0370). Before, it was gone the moment nothing matched (ADR-0020).
func TestAMessagePublishedBeforeItsSubscriberWaitsForIt(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	catcher := catcherProcess(t, 7)
	p := bufferProcessor(t, h, &fixedClock{t: 1_000}, catcher)

	p.PublishMessageWithTTL("order", "42", "", time.Minute, model.VariableValue{Name: "payload", Kind: model.VarString, Text: "early"})
	runIdle(t, p, "publish")
	if got := bufferedMessages(t, h.store); len(got) != 1 || got[0].MessageName != "order" || got[0].CorrelationKey != "42" {
		t.Fatalf("buffer after publish = %+v, want the one order message for key 42", got)
	}

	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	runIdle(t, p, "first catcher")
	first := model.NewKey(1, 2) // the buffered message took key 1
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the catcher subscribed: process=%d element=%d, want 0 and 0 (the buffered message correlated)", pi, ei)
	}
	if got := readVar(t, h.store, first, "payload"); got == nil || got.Text != "early" {
		t.Fatalf("payload in the catcher = %+v, want the buffered message's \"early\"", got)
	}
	if got := bufferedMessages(t, h.store); len(got) != 0 {
		t.Fatalf("buffer after delivery = %+v, want empty", got)
	}

	// Delivered once: a second instance on the same key waits like any other.
	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	runIdle(t, p, "second catcher")
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Fatalf("second catcher: process=%d, want 1 still waiting", pi)
	}
}

// Without a time to live nothing changes: an unmatched publish is a no-op, as every
// deployed model relies on (ADR-0020, ADR-0370 "TTL defaults to zero").
func TestAMessageWithoutATimeToLiveIsStillLost(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	catcher := catcherProcess(t, 7)
	p := bufferProcessor(t, h, &fixedClock{t: 1_000}, catcher)

	p.PublishMessage("order", "42")
	runIdle(t, p, "publish")
	if got := bufferedMessages(t, h.store); len(got) != 0 {
		t.Fatalf("buffer after a publish without TTL = %+v, want empty", got)
	}
	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	runIdle(t, p, "catcher")
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Fatalf("catcher: process=%d, want 1 still waiting", pi)
	}
}

// The buffer is state built from events, so a replay of the log rebuilds it, and a
// subscription opened after the restart receives the message (I4).
func TestTheMessageBufferRecovers(t *testing.T) {
	dir := t.TempDir()
	catcher := catcherProcess(t, 7)

	h1 := openHarness(t, dir)
	p1 := bufferProcessor(t, h1, &manualClock{}, catcher)
	p1.PublishMessageWithTTL("order", "42", "m-1", time.Hour, model.VariableValue{Name: "payload", Kind: model.VarString, Text: "kept"})
	runIdle(t, p1, "publish")
	live := bufferedMessages(t, h1.store)
	h1.close(t)

	p2, store2 := replayed(t, dir, catcher)
	got := bufferedMessages(t, store2)
	if len(got) != 1 || len(live) != 1 || got[0].MessageId != live[0].MessageId || got[0].ExpiresAt != live[0].ExpiresAt ||
		len(got[0].Variables) != 1 || got[0].Variables[0].Text != "kept" {
		t.Fatalf("buffer after replay = %+v, want the live one %+v", got, live)
	}
	p2.CreateInstance(catcher.Key, numVar("orderId", "42"))
	runIdle(t, p2, "catcher after replay")
	if pi, _ := counts(t, store2); pi != 0 {
		t.Fatalf("catcher after replay: process=%d, want 0 (received the buffered message)", pi)
	}
	// The id outlives the delivery: the same message again is the same message.
	p2.PublishMessageWithTTL("order", "42", "m-1", time.Hour)
	runIdle(t, p2, "republish")
	if got := bufferedMessages(t, store2); len(got) != 0 {
		t.Fatalf("buffer after a repeated id = %+v, want empty (a duplicate)", got)
	}
}

// A message id is taken once while its message lives: a repeat neither buffers a
// second copy nor reaches a subscriber the first one did not (ADR-0370 idempotency).
// That holds whether the first copy waited in the buffer or was delivered at once.
func TestAMessageIdIsTakenOnce(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	catcher := catcherProcess(t, 7)
	p := bufferProcessor(t, h, &fixedClock{t: 1_000}, catcher)

	// Buffered twice under one id: one copy.
	p.PublishMessageWithTTL("order", "1", "dup", time.Minute)
	p.PublishMessageWithTTL("order", "1", "dup", time.Minute)
	runIdle(t, p, "buffered twice")
	if got := bufferedMessages(t, h.store); len(got) != 1 {
		t.Fatalf("buffer after two publishes of one id = %+v, want one copy", got)
	}

	// Delivered at once, then repeated: the repeat reaches nobody new.
	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	runIdle(t, p, "first catcher")
	p.PublishMessageWithTTL("order", "42", "once", time.Minute)
	runIdle(t, p, "delivered at once")
	if pi, _ := counts(t, h.store); pi != 0 {
		t.Fatalf("first catcher: process=%d, want 0 (delivered)", pi)
	}
	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	runIdle(t, p, "second catcher")
	p.PublishMessageWithTTL("order", "42", "once", time.Minute)
	runIdle(t, p, "repeat")
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Fatalf("second catcher after the repeat: process=%d, want 1 still waiting (the repeat is a duplicate)", pi)
	}
	for _, m := range bufferedMessages(t, h.store) {
		if m.MessageId == "once" {
			t.Fatalf("the repeated id was buffered: %+v", m)
		}
	}
}

// A buffered message ends at its expiry: a subscriber that opens after it receives
// nothing, even before the sweep removed the entry, and the sweep then removes it and
// forgets its id (ADR-0370 expiry, never a clock read in applyToState).
func TestABufferedMessageExpires(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	catcher := catcherProcess(t, 7)
	clk := &fixedClock{t: 0}
	p := bufferProcessor(t, h, clk, catcher)

	p.PublishMessageWithTTL("order", "42", "late", time.Second)
	runIdle(t, p, "publish")

	clk.t = int64(2 * time.Second)
	p.CreateInstance(catcher.Key, numVar("orderId", "42"))
	runIdle(t, p, "catcher after expiry")
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Fatalf("catcher after expiry: process=%d, want 1 still waiting (the message had expired)", pi)
	}
	if got := bufferedMessages(t, h.store); len(got) != 1 {
		t.Fatalf("buffer before the sweep = %d entries, want the expired one still there", len(got))
	}

	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if got := bufferedMessages(t, h.store); len(got) != 0 {
		t.Fatalf("buffer after the sweep = %+v, want empty", got)
	}
	// The id is forgotten with the message: the same id is new again.
	p.PublishMessageWithTTL("order", "7", "late", time.Second)
	runIdle(t, p, "republish")
	if got := bufferedMessages(t, h.store); len(got) != 1 {
		t.Fatalf("buffer after reusing an expired id = %d entries, want 1", len(got))
	}
}

// The buffer feeds the subscriptions that open later, not message start events: a
// process deployed with a matching start after the message was buffered does not start
// from it (ADR-0370 amendment of 2026-10-13).
func TestABufferedMessageStartsNothing(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := bufferProcessor(t, h, &fixedClock{t: 1_000})

	p.PublishMessageWithTTL("order", "42", "", time.Minute)
	runIdle(t, p, "publish")

	b := compiler.NewBuilder(30, "starter", 1)
	start := b.AddMessageStartEvent("order", nil, false)
	end := b.AddEndEvent()
	b.Connect(start, end)
	starter, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p.Deploy(starter)
	runIdle(t, p, "after deploy")
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after deploying a message start: process=%d element=%d, want nothing started", pi, ei)
	}
	if got := bufferedMessages(t, h.store); len(got) != 1 {
		t.Fatalf("buffer = %d entries, want the message still waiting", len(got))
	}
}

// An event-based gateway is a race (ADR-0110): when messages for several of its
// catches already wait, the first catch in flow order takes its message and the
// others' stay in the buffer, rather than being taken by a catch the winner cancels.
func TestAnEventGatewayTakesOneBufferedMessage(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	b := compiler.NewBuilder(40, "race", 1)
	start := b.AddStartEvent()
	gw := b.AddEventBasedGateway()
	a := b.AddMessageCatchEvent("a", mustCompile(t, "orderId"))
	bb := b.AddMessageCatchEvent("b", mustCompile(t, "orderId"))
	endA := b.AddEndEvent()
	endB := b.AddEndEvent()
	b.Connect(start, gw)
	b.Connect(gw, a)
	b.Connect(gw, bb)
	b.Connect(a, endA)
	b.Connect(bb, endB)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := bufferProcessor(t, h, &fixedClock{t: 1_000}, cp)

	p.PublishMessageWithTTL("a", "42", "", time.Minute)
	p.PublishMessageWithTTL("b", "42", "", time.Minute)
	runIdle(t, p, "publish both")
	p.CreateInstance(cp.Key, numVar("orderId", "42"))
	runIdle(t, p, "race")

	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the race: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key); v[endA] != 1 || v[endB] != 0 {
		t.Fatalf("visits endA=%d endB=%d, want the first catch in flow order to win", v[endA], v[endB])
	}
	if got := bufferedMessages(t, h.store); len(got) != 1 || got[0].MessageName != "b" {
		t.Fatalf("buffer after the race = %+v, want b's message still waiting", got)
	}
}

// A boundary event opens its subscription when its host activates, so a buffered
// message fires it at once: an interrupting boundary cancels the host before any
// worker sees its job.
func TestABufferedMessageFiresABoundary(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	b := compiler.NewBuilder(50, "cancellable", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	cancel := b.AddBoundaryMessageEvent(work, true, "cancel", mustCompile(t, "orderId"))
	normalEnd := b.AddEndEvent()
	cancelEnd := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, normalEnd)
	b.Connect(cancel, cancelEnd)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(work).Detail).JobType
	p := bufferProcessor(t, h, &fixedClock{t: 1_000}, cp)

	p.PublishMessageWithTTL("cancel", "42", "", time.Minute)
	runIdle(t, p, "publish")
	p.CreateInstance(cp.Key, numVar("orderId", "42"))
	runIdle(t, p, "instance")

	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after the boundary fired: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key); v[cancelEnd] != 1 || v[normalEnd] != 0 {
		t.Fatalf("visits cancelEnd=%d normalEnd=%d, want the boundary path", v[cancelEnd], v[normalEnd])
	}
	if !jobGone(t, h.store, jobType) {
		t.Fatal("the host's job is still offered after the boundary interrupted it")
	}
}

// Two sweeps that both find a message due drop it once: the second finds its entry
// gone and writes nothing (ADR-0370).
func TestTwoSweepsExpireAMessageOnce(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 0}
	p := bufferProcessor(t, h, clk)

	p.PublishMessageWithTTL("order", "42", "", time.Second)
	runIdle(t, p, "publish")
	clk.t = int64(2 * time.Second)
	if err := p.TriggerDueMessageExpiries(); err != nil {
		t.Fatalf("TriggerDueMessageExpiries 1: %v", err)
	}
	if err := p.TriggerDueMessageExpiries(); err != nil {
		t.Fatalf("TriggerDueMessageExpiries 2: %v", err)
	}
	before := logRecords(t, h)
	runIdle(t, p, "two sweeps")
	if got := bufferedMessages(t, h.store); len(got) != 0 {
		t.Fatalf("buffer after two sweeps = %+v, want empty", got)
	}
	if written := logRecords(t, h) - before; written != 1 {
		t.Fatalf("records written by two sweeps = %d, want 1 (one expiry)", written)
	}
}

// bufferedCount is the maintained buffered-message total, failing unless it agrees with
// what the buffer holds.
func bufferedCount(t *testing.T, s *state.Store, what string) int64 {
	t.Helper()
	n, err := s.BufferedMessageCount()
	if err != nil {
		t.Fatalf("BufferedMessageCount (%s): %v", what, err)
	}
	if held := int64(len(bufferedMessages(t, s))); n != held {
		t.Fatalf("buffered count (%s) = %d, but the buffer holds %d", what, n, held)
	}
	return n
}

// The buffered-message count follows the buffer through a delivery and an expiry, and
// a rebuild from the log alone lands on the number the live run kept (ADR-0142's
// pairing, ADR-0370). A message delivered with an id leaves its expiry entry behind;
// the expiry that later sweeps it finds nothing in the buffer and must not count it out
// a second time.
func TestTheBufferedMessageCountSurvivesReplay(t *testing.T) {
	dir := t.TempDir()
	catcher := catcherProcess(t, 7)
	clk := &fixedClock{t: 0}

	h1 := openHarness(t, dir)
	p1 := bufferProcessor(t, h1, clk, catcher)
	p1.PublishMessageWithTTL("order", "1", "with-id", time.Second)
	p1.PublishMessageWithTTL("order", "2", "", time.Second)
	p1.PublishMessageWithTTL("order", "3", "", time.Second)
	runIdle(t, p1, "publish three")
	if n := bufferedCount(t, h1.store, "after three"); n != 3 {
		t.Fatalf("buffered count after three publishes = %d, want 3", n)
	}

	p1.CreateInstance(catcher.Key, numVar("orderId", "1"))
	runIdle(t, p1, "catcher takes one")
	if n := bufferedCount(t, h1.store, "after a delivery"); n != 2 {
		t.Fatalf("buffered count after a delivery = %d, want 2", n)
	}

	clk.t = int64(2 * time.Second)
	if err := p1.TriggerDueMessageExpiries(); err != nil {
		t.Fatalf("TriggerDueMessageExpiries: %v", err)
	}
	runIdle(t, p1, "sweep")
	if n := bufferedCount(t, h1.store, "after the sweep"); n != 0 {
		t.Fatalf("buffered count after the sweep = %d, want 0 (the delivered one is not counted out twice)", n)
	}

	p1.PublishMessageWithTTL("order", "4", "", time.Hour)
	runIdle(t, p1, "publish one more")
	live := bufferedCount(t, h1.store, "live")
	h1.close(t)

	_, store2 := replayed(t, dir, catcher)
	if got := bufferedCount(t, store2, "replayed"); got != live || got != 1 {
		t.Fatalf("buffered count after replay = %d, want the live %d (1)", got, live)
	}
}
