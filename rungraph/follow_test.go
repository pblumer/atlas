package rungraph

import (
	"errors"
	"fmt"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// W4 of ADR-0404 §4, second half: *"keep current from the tailer."*
//
// **What "keep current" can mean here is decided by the structure, and the measurement that
// decided it is written into the record rather than left implicit.** An increment can add and
// cannot remove:
//
//   - a completing element instance is *deleted* from state (engine/apply.go, on
//     IntentCompleted and IntentTerminated), so the node set shrinks as fast as it grows in
//     any steady-state installation;
//   - the CSR is packed and undirected, so a new edge has to be inserted into *both*
//     endpoints' adjacency lists, and one of them is in the middle of a two-gigabyte array;
//   - union-find merges incrementally by design and cannot un-merge, so a removed edge
//     cannot be undone without the component pass running again.
//
// So the follower does not mutate the graph. It measures **drift** — how many node-set
// changes the log holds that the projection does not reflect — and that number decides when a
// rebuild is due. The projection therefore stays exactly as of its position, which is the one
// property every consumer needs (§5 forbids a picture that cannot say as of when) and the one
// an in-place mutation would destroy.

// fakeLog hands out pre-encoded records the way the tailer would, and always from the
// start of the log.
//
// That is not a shortcut around wal.Cursor's unexported fields — it is the worst case the
// real tailer permits, and the one the follower has to survive. A Cursor is valid within
// one process run and a restart resumes from genesis by design (ADR-0114), so a follower
// will be handed records it has already counted. Re-delivering everything on every read
// makes every test below also a test that the follower counts each record exactly once.
type fakeLog struct {
	records [][]byte
	failAt  int // 1-based index to fail on; 0 never fails
	reads   int
}

func (l *fakeLog) Read(from wal.Cursor, fn func(data []byte) (bool, error)) (wal.Cursor, error) {
	l.reads++
	for i := range l.records {
		if l.failAt != 0 && i+1 == l.failAt {
			return from, errors.New("log read failed")
		}
		stop, err := fn(l.records[i])
		if err != nil {
			return from, err
		}
		if stop {
			return from, nil
		}
	}
	return from, nil
}

// rec encodes one log record of the kind the follower reads.
func rec(pos uint64, vt model.ValueType, intent model.Intent) []byte {
	r := model.Record{Header: model.RecordHeader{
		Position:   pos,
		Key:        pos * 10,
		RecordType: model.RecordEvent,
		ValueType:  vt,
		Intent:     intent,
	}}
	switch vt {
	case model.VTElementInstance:
		r.Value = &model.ElementInstanceValue{ProcessInstanceKey: 1}
	case model.VTVariable:
		r.Value = &model.VariableValue{Name: "x", Kind: model.VarString, Text: "y"}
	}
	return model.AppendRecord(nil, &r)
}

func elemIn(pos uint64) []byte  { return rec(pos, model.VTElementInstance, model.IntentActivated) }
func elemOut(pos uint64) []byte { return rec(pos, model.VTElementInstance, model.IntentCompleted) }
func elemKill(pos uint64) []byte {
	return rec(pos, model.VTElementInstance, model.IntentTerminated)
}
func noise(pos uint64) []byte { return rec(pos, model.VTVariable, model.IntentVariableCreated) }

// TestDriftCountsNodeChangesAndNothingElse is the definition of drift. A projection is stale
// by the number of nodes that have appeared or gone since it was seeded — not by the number
// of records, which in a busy installation is dominated by variables, jobs and timers that
// change no node at all. Counting records would overstate staleness by orders of magnitude and
// trigger rebuilds nothing needed.
func TestDriftCountsNodeChangesAndNothingElse(t *testing.T) {
	log := &fakeLog{records: [][]byte{
		elemIn(1), noise(2), elemIn(3), noise(4), elemOut(5), elemKill(6), noise(7),
	}}
	f := NewFollower(log, 0)
	d, err := f.Follow(7)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if d.Arrived != 2 {
		t.Errorf("Arrived = %d, want 2", d.Arrived)
	}
	if d.Departed != 2 {
		t.Errorf("Departed = %d, want 2 (one completed, one terminated)", d.Departed)
	}
	if d.Changes() != 4 {
		t.Errorf("Changes() = %d, want 4", d.Changes())
	}
	if d.Durable != 7 {
		t.Errorf("Durable = %d, want 7", d.Durable)
	}
	if d.Gap {
		t.Error("Gap reported over a log that starts at the seed")
	}
}

// TestTheSeedsOwnRecordsAreSkipped is how the follower starts "at the snapshot's position"
// with a cursor it cannot construct: wal.Cursor's fields are unexported and there is no seek,
// so the follower reads from the log's start and skips what the seed already contains — the
// same re-derivation the OpenSearch exporter does from its high-water mark. The cost is a
// sequential pass with a header decode per record, not an application, and the surviving log
// is bounded by compaction.
func TestTheSeedsOwnRecordsAreSkipped(t *testing.T) {
	log := &fakeLog{records: [][]byte{
		elemIn(1), elemIn(2), elemIn(3), elemIn(4),
	}}
	f := NewFollower(log, 2) // the projection already contains records 1 and 2
	d, err := f.Follow(4)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if d.Arrived != 2 {
		t.Errorf("Arrived = %d, want 2 — records 1 and 2 are in the seed", d.Arrived)
	}
	if d.Position != 2 {
		t.Errorf("Position = %d, want the seed's 2", d.Position)
	}
}

// TestFollowStopsAtTheDurableBound keeps the follower from counting what a reader could not
// see. A record staged but not yet fsynced is not durable, and the watermark is the caller's
// statement of how far durability reaches (ADR-0114's discipline, ADR-0005's fsync).
func TestFollowStopsAtTheDurableBound(t *testing.T) {
	log := &fakeLog{records: [][]byte{elemIn(1), elemIn(2), elemIn(3)}}
	f := NewFollower(log, 0)
	d, err := f.Follow(2)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if d.Arrived != 2 || d.Durable != 2 {
		t.Fatalf("Arrived = %d, Durable = %d, want 2 and 2", d.Arrived, d.Durable)
	}
	// And the third record is not lost: the next call delivers it, because the stopping
	// record is not consumed.
	d, err = f.Follow(3)
	if err != nil {
		t.Fatalf("Follow (second): %v", err)
	}
	if d.Arrived != 3 {
		t.Errorf("Arrived = %d after the second call, want 3 cumulative", d.Arrived)
	}
	if d.Durable != 3 {
		t.Errorf("Durable = %d, want 3", d.Durable)
	}
}

// TestDriftIsCumulativeAcrossCalls is what makes the number usable as a rebuild trigger: it is
// the distance from the *seed*, not from the last poll.
func TestDriftIsCumulativeAcrossCalls(t *testing.T) {
	log := &fakeLog{records: [][]byte{elemIn(1), elemOut(2)}}
	f := NewFollower(log, 0)
	if d, err := f.Follow(1); err != nil || d.Changes() != 1 {
		t.Fatalf("first Follow: %v, changes %d", err, d.Changes())
	}
	d, err := f.Follow(2)
	if err != nil {
		t.Fatalf("second Follow: %v", err)
	}
	if d.Arrived != 1 || d.Departed != 1 || d.Changes() != 2 {
		t.Errorf("drift = %+v, want one arrival and one departure", d)
	}
}

// TestAGapMeansRebuildRatherThanCatchUp is the third bullet of §4 — "rebuild whenever the two
// cannot be reconciled" — made detectable. Positions are a dense monotonic sequence
// (engine/context.go increments one counter per record), so if the oldest surviving record's
// position is more than one past the seed, the records in between are gone: compaction
// (ADR-0131) removed them, and nothing can supply them. Catching up from there would produce
// a projection missing changes with nothing to say so.
func TestAGapMeansRebuildRatherThanCatchUp(t *testing.T) {
	log := &fakeLog{records: [][]byte{elemIn(9), elemIn(10)}}
	f := NewFollower(log, 3) // the seed is at 3; the log starts at 9
	d, err := f.Follow(10)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if !d.Gap {
		t.Fatal("no gap reported although records 4..8 are not in the log")
	}
	if !d.RebuildDue(1000, 0.5) {
		t.Error("a gap does not make a rebuild due, which would leave the projection wrong with nothing saying so")
	}
	// The counts are still reported — a reader deciding what to do wants to know how much it
	// is missing, even when the answer is "rebuild".
	if d.Arrived != 2 {
		t.Errorf("Arrived = %d, want the 2 it could see", d.Arrived)
	}
}

// TestNoGapWhenTheLogHoldsNothingNew is the ordinary quiet case, and it must not read as a
// gap: an installation where nothing has happened since the seed is up to date, not
// unreconcilable.
func TestNoGapWhenTheLogHoldsNothingNew(t *testing.T) {
	log := &fakeLog{records: [][]byte{elemIn(1), elemIn(2)}}
	f := NewFollower(log, 2)
	d, err := f.Follow(2)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if d.Gap || d.Changes() != 0 {
		t.Errorf("drift = %+v, want no gap and no change", d)
	}
	if d.RebuildDue(10, 0.5) {
		t.Error("a rebuild is due over an up-to-date projection")
	}
}

// TestRebuildDueIsAFractionOfTheProjection is why the threshold is relative. A thousand
// changes are nothing to a hundred-million-node projection and everything to one of two
// thousand, so an absolute number would be wrong at one end or the other — the same reason
// §9's budget is stated in bytes rather than in nodes.
func TestRebuildDueIsAFractionOfTheProjection(t *testing.T) {
	log := &fakeLog{records: [][]byte{elemIn(1), elemIn(2), elemIn(3), elemIn(4)}}
	f := NewFollower(log, 0)
	d, err := f.Follow(4)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if d.Changes() != 4 {
		t.Fatalf("Changes() = %d, want 4", d.Changes())
	}
	if d.RebuildDue(100, 0.10) {
		t.Error("4 changes over 100 nodes is 4%, under a 10% threshold")
	}
	if !d.RebuildDue(100, 0.02) {
		t.Error("4 changes over 100 nodes is 4%, over a 2% threshold")
	}
	// An empty projection is a special case worth being explicit about: any change at all
	// makes it wrong, and a fraction of zero is not a number.
	if !d.RebuildDue(0, 0.5) {
		t.Error("a change against an empty projection does not make a rebuild due")
	}
}

// TestAFailedReadDoesNotAdvanceTheFollower keeps a partial read from being counted as
// progress: the cursor and the counts must stay where they were, so the next call re-reads
// rather than skipping what it never saw.
func TestAFailedReadDoesNotAdvanceTheFollower(t *testing.T) {
	log := &fakeLog{records: [][]byte{elemIn(1), elemIn(2), elemIn(3)}, failAt: 3}
	f := NewFollower(log, 0)
	if _, err := f.Follow(3); err == nil {
		t.Fatal("Follow returned no error over a failing log")
	}
	log.failAt = 0
	d, err := f.Follow(3)
	if err != nil {
		t.Fatalf("Follow after the failure: %v", err)
	}
	if d.Arrived != 3 {
		t.Errorf("Arrived = %d, want 3 — the failed read must not have consumed records", d.Arrived)
	}
}

// TestAnUndecodableRecordIsAnErrorNotASilentSkip keeps a corrupt record from reading as "no
// node changed". The follower's whole output is a number a caller trusts enough to skip a
// rebuild on, so a record it cannot read has to break the measurement rather than quietly
// lower it.
func TestAnUndecodableRecordIsAnErrorNotASilentSkip(t *testing.T) {
	log := &fakeLog{records: [][]byte{elemIn(1), {0xff, 0x02}}}
	f := NewFollower(log, 0)
	if _, err := f.Follow(2); err == nil {
		t.Fatal("Follow accepted a record it could not decode")
	}
	// And nothing was adopted: the counts are still at the seed, so a later read over a
	// repaired log measures the whole interval.
	log.records = log.records[:1]
	d, err := f.Follow(1)
	if err != nil {
		t.Fatalf("Follow after the bad record: %v", err)
	}
	if d.Arrived != 1 {
		t.Errorf("Arrived = %d, want 1", d.Arrived)
	}
}

// TestARestartedCursorDoesNotDoubleCount names the property the fake exercises everywhere
// else implicitly, because it is the one a real deployment depends on and the one a
// position-blind follower would get wrong. Every read here re-delivers the whole log, which is
// what a restart does (ADR-0114), and the drift must not move.
func TestARestartedCursorDoesNotDoubleCount(t *testing.T) {
	log := &fakeLog{records: [][]byte{elemIn(1), elemOut(2), elemIn(3)}}
	f := NewFollower(log, 0)
	first, err := f.Follow(3)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	for range 3 {
		again, err := f.Follow(3)
		if err != nil {
			t.Fatalf("Follow (repeat): %v", err)
		}
		if again != first {
			t.Fatalf("drift moved on re-delivery: %+v then %+v", first, again)
		}
	}
	if log.reads != 4 {
		t.Errorf("reads = %d, want 4 — the test does not exercise re-delivery", log.reads)
	}
}

// TestTheDriftNetMatchesTheStateStoresOwnNodeCount is W4's acceptance test, and the reason it
// is worth its runtime is that it checks the log-derived number against something that did not
// come from the log. Arrived minus Departed is a claim about the node set, and the state store
// *is* the node set: if the follower counted the wrong records, skipped the wrong ones, or
// double-counted a re-delivery, the two numbers part company.
//
// It also pins the one thing the unit tests cannot: that the intents the engine actually
// writes for a node appearing and going are the intents the follower counts.
func TestTheDriftNetMatchesTheStateStoresOwnNodeCount(t *testing.T) {
	p, store, walDir := engineFixtureIn(t)
	cp := parkingWorkload(t)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// The seed: a first cohort, a snapshot, a graph that says what position it is as of.
	const first = 6
	for range first {
		p.CreateInstance(cp.Key)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	seedView := store.ReadView()
	defer seedView.Close()
	seeded, err := Build(seedView)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	nodesAtSeed := seeded.Ordinals.Len()
	if nodesAtSeed == 0 {
		t.Fatal("the seed is empty, so the net delta below is not a measurement")
	}

	// One of the seed's own instances, remembered before anything else happens, so the
	// departure below is a node the projection *contains* rather than one that came and went
	// after it. Those are the two different cases, and only this one can make the node set
	// shrink below the seed.
	victim := anActiveInstance(t, seedView)

	// The follower starts from the graph's own position — the only seed it could honestly use.
	f := seeded.Follower(wal.NewTailer(walDir))

	// More instances: each one's start events activate and then complete, so this alone puts
	// both intents in the log.
	const second = 5
	for range second {
		p.CreateInstance(cp.Key)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// And the seed instance is cancelled, which terminates every element instance parked under
	// it — the case where the node set shrinks past where the seed found it.
	p.CancelInstance(victim)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}

	liveView := store.ReadView()
	defer liveView.Close()
	durable, err := liveView.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	nodesNow := 0
	if err := liveView.ActiveElementInstances(func(uint64, *model.ElementInstanceValue) error {
		nodesNow++
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}

	d, err := f.Follow(durable)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if d.Gap {
		t.Error("Gap over a log nothing compacted")
	}
	if d.Position != seeded.Position() || d.Durable != durable {
		t.Errorf("drift interval = %d..%d, want %d..%d", d.Position, d.Durable, seeded.Position(), durable)
	}
	if d.Arrived == 0 || d.Departed == 0 {
		t.Fatalf("Arrived = %d, Departed = %d — one of the two counters is untested",
			d.Arrived, d.Departed)
	}
	net := int64(d.Arrived) - int64(d.Departed)
	if want := int64(nodesNow) - int64(nodesAtSeed); net != want {
		t.Errorf("Arrived-Departed = %d, but the store's node set moved by %d (%d → %d)",
			net, want, nodesAtSeed, nodesNow)
	}
	t.Logf("seed at %d with %d nodes; store at %d with %d nodes; drift +%d/-%d",
		seeded.Position(), nodesAtSeed, durable, nodesNow, d.Arrived, d.Departed)
}

// anActiveInstance returns the key of one live process instance in view, failing the test if
// there is none.
func anActiveInstance(t *testing.T, view *state.ReadView) uint64 {
	t.Helper()
	var key uint64
	if err := view.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		if key == 0 {
			key = k
		}
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if key == 0 {
		t.Fatal("no live process instance to work with")
	}
	return key
}

// TestTheLogsPositionsAreDenseWhichIsWhatAGapDetects checks the engine-side assumption gap
// detection rests on, because it is an assumption about the *writer*, not about the follower.
// If positions were ever sparse — a counter bumped for something the log does not carry, a
// record written without one — the follower would report gaps over an intact log and every
// consumer would rebuild for nothing.
func TestTheLogsPositionsAreDenseWhichIsWhatAGapDetects(t *testing.T) {
	p, store, walDir := engineFixtureIn(t)
	cp := parkingWorkload(t)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	for range 4 {
		p.CreateInstance(cp.Key)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// A cancellation as well, so the log carries terminations and not only the activations of
	// a single straight run.
	live := store.ReadView()
	p.CancelInstance(anActiveInstance(t, live))
	_ = live.Close()
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}

	view := store.ReadView()
	defer view.Close()
	durable, err := view.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}

	var want uint64 = 1
	if _, err := wal.NewTailer(walDir).Read(wal.Cursor{}, func(data []byte) (bool, error) {
		h, err := model.ReadHeader(data)
		if err != nil {
			return false, err
		}
		if h.Position > durable {
			return true, nil
		}
		if h.Position != want {
			return false, fmt.Errorf("position %d where %d was due: the sequence is not dense", h.Position, want)
		}
		want++
		return false, nil
	}); err != nil {
		t.Fatalf("tailing the log: %v", err)
	}
	if want-1 != durable {
		t.Errorf("the log ends at position %d but state is applied through %d", want-1, durable)
	}
	t.Logf("%d records, positions 1..%d with no hole", durable, durable)
}
