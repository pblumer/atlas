package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestInboundDeliveryIdempotentAcrossRestart is the ADR-0075 correctness crux: an
// at-least-once inbound publish that is replayed after a crash must NOT double-start
// a message-start process. PublishInbound carries the source's id and sequence; the
// engine folds a per-source high-water mark into the same durable batch as the
// correlate/start effects, so a replayed duplicate is skipped — while a genuinely
// new sequence still starts an instance.
func TestInboundDeliveryIdempotentAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	responder := parkingResponder(t, 7) // messageStart("request") → park
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(responder)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	// Deliver source "clio:orders" sequence 7 → one instance starts.
	p1.PublishInbound("clio:orders", 7, "request", "", numVar("orderId", "42"))
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	if pi := activeProcs(t, h1.store); pi != 1 {
		t.Fatalf("after delivery 7: active=%d, want 1", pi)
	}
	h1.close(t)

	// Crash and replay into a fresh store: the started instance AND the high-water
	// mark for the source both rebuild from the log.
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
	if pi := activeProcs(t, store2); pi != 1 {
		t.Fatalf("after replay: active=%d, want 1 (started instance restored)", pi)
	}

	// The bridge, resuming from a stale cursor, re-delivers sequence 7. The durable
	// high-water mark (rebuilt on replay) makes it a no-op — no second instance.
	p2.PublishInbound("clio:orders", 7, "request", "", numVar("orderId", "42"))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (duplicate 7): %v", err)
	}
	if pi := activeProcs(t, store2); pi != 1 {
		t.Fatalf("after duplicate delivery 7: active=%d, want 1 (duplicate skipped)", pi)
	}

	// A genuinely new sequence advances past the mark and starts a new instance.
	p2.PublishInbound("clio:orders", 8, "request", "", numVar("orderId", "43"))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (8): %v", err)
	}
	if pi := activeProcs(t, store2); pi != 2 {
		t.Fatalf("after delivery 8: active=%d, want 2 (new sequence started)", pi)
	}
}

// TestInboundDeliveryPerSourceHighWater proves the high-water is per source: a
// duplicate sequence from one source is skipped while the same sequence number from
// a different source is delivered (the id is opaque and independent).
func TestInboundDeliveryPerSourceHighWater(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	responder := parkingResponder(t, 7)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(responder)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.PublishInbound("source-A", 1, "request", "")
	p.PublishInbound("source-A", 1, "request", "") // duplicate for A → skipped
	p.PublishInbound("source-B", 1, "request", "") // same seq, different source → applied
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 2 {
		t.Fatalf("active=%d, want 2 (A once + B once; A's duplicate skipped)", pi)
	}
}

// TestPublishMessageUnaffectedByDedup proves a plain API/throw publish (empty source
// id) is never deduplicated: two identical publishes both correlate. This guards the
// existing message path against the new inbound guard.
func TestPublishMessageUnaffectedByDedup(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	responder := parkingResponder(t, 7)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(responder)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.PublishMessage("request", "")
	p.PublishMessage("request", "")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 2 {
		t.Fatalf("active=%d, want 2 (API publishes are never deduplicated)", pi)
	}
}
