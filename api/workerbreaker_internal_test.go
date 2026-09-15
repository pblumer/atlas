package api

import (
	"testing"
	"time"
)

// The breaker is a state machine over time, so every test here drives an explicit
// clock rather than sleeping: a test that waited out a ten-second cooldown would be a
// test nobody runs. `at` moves the clock; nothing else does.

// breakerAt returns a breaker over a clock the caller moves by hand.
func breakerAt(start int64) (*workerBreakers, func(d time.Duration)) {
	now := start
	b := newWorkerBreakers(func() int64 { return now })
	return b, func(d time.Duration) { now += int64(d) }
}

// The two interned job-type indices these tests speak in, and the names an operator
// deployed them under — the breaker keys on the index, the view renders the name.
const (
	mailType int32 = 12
	cardType int32 = 13
)

func typeName(idx int32) (string, bool) {
	switch idx {
	case mailType:
		return "io.atlas.mail", true
	case cardType:
		return "charge-card", true
	}
	return "", false
}

var mailWorker = breakerKey{jobType: mailType, connector: "Patrick Blumer"}

// fail is the common case: one failure of one instance, reported for a job of its own.
func fail(b *workerBreakers, k breakerKey, instance uint64) {
	b.failed(k, instance*10, instance, "dial tcp: connection refused")
}

// TestABreakerCostsNothingUntilSomethingFails is the property the whole design turns
// on. In the steady state nothing is open, so the gate answers per *job type* before it
// looks at a single job — and it answers from an empty map. If a breaker allocated an
// entry per job, or per job type, the dispatch path would pay for a mechanism that is
// idle almost always (I1).
func TestABreakerCostsNothingUntilSomethingFails(t *testing.T) {
	b, _ := breakerAt(0)

	if b.holdingFor(mailType) {
		t.Error("a breaker that has seen nothing is holding jobs back")
	}
	for jobKey := uint64(1); jobKey <= 1000; jobKey++ {
		if !b.allow(mailWorker, jobKey) {
			t.Fatalf("job %d refused by a breaker that has seen no failure", jobKey)
		}
	}
	if len(b.byKey) != 0 {
		t.Errorf("a thousand allowed jobs left %d entries behind, want none", len(b.byKey))
	}
	if b.succeeded(mailWorker); len(b.byKey) != 0 {
		t.Errorf("a success on a breaker that never failed allocated %d entries", len(b.byKey))
	}
}

// TestOneBadRecordNeverTripsABreaker is the distinction the trip condition exists for.
// An instance whose variables are wrong fails its whole retry budget in a row against a
// perfectly healthy host; stopping the integration over it would punish every other
// instance for one bad record. Those failures are *one* instance, however many of them
// there are.
func TestOneBadRecordNeverTripsABreaker(t *testing.T) {
	b, at := breakerAt(0)

	for i := 0; i < 20; i++ {
		b.failed(mailWorker, 99, 7, "FEEL: variable 'to' is not a string")
		at(time.Second)
	}
	if !b.allow(mailWorker, 1) {
		t.Error("twenty failures of one instance closed the gate on everyone else")
	}
	if b.holdingFor(mailType) {
		t.Error("holdingFor = true, want false — one instance is not an outage")
	}
}

// TestDistinctInstancesTripIt is the other half: a dead host fails instances that have
// nothing to do with each other, which no data fault does.
func TestDistinctInstancesTripIt(t *testing.T) {
	b, _ := breakerAt(1000)

	fail(b, mailWorker, 1)
	fail(b, mailWorker, 2)
	if !b.allow(mailWorker, 500) {
		t.Fatal("the gate closed on two instances, want three")
	}
	fail(b, mailWorker, 3)

	if b.allow(mailWorker, 501) {
		t.Error("the gate is still open after three distinct instances failed")
	}
	if !b.holdingFor(mailType) {
		t.Error("holdingFor = false, want true — the dispatch path has to know to look")
	}
}

// TestASuccessBreaksTheStreak: "consecutive" is the word the trip condition uses, and a
// completed job is the evidence that breaks it. Two failures, a success, then a third
// failure is not three consecutive failures — and treating it as such would trip
// breakers on integrations that are merely imperfect.
func TestASuccessBreaksTheStreak(t *testing.T) {
	b, _ := breakerAt(0)

	fail(b, mailWorker, 1)
	fail(b, mailWorker, 2)
	b.succeeded(mailWorker)
	fail(b, mailWorker, 3)

	if !b.allow(mailWorker, 1) {
		t.Error("a streak survived a success between its halves")
	}
}

// TestAStaleStreakDoesNotCompose bounds the streak in time as well as in outcome. Three
// failures a week apart are three ordinary faults, not an outage; without a window the
// breaker would accumulate them forever and eventually trip on nothing.
func TestAStaleStreakDoesNotCompose(t *testing.T) {
	b, at := breakerAt(0)

	fail(b, mailWorker, 1)
	at(breakerStreakWindow + time.Second)
	fail(b, mailWorker, 2)
	fail(b, mailWorker, 3)

	if !b.allow(mailWorker, 1) {
		t.Error("failures on either side of the window composed into one streak")
	}
	// The two inside the window are a live streak, so the next one does trip it.
	fail(b, mailWorker, 4)
	if b.allow(mailWorker, 1) {
		t.Error("the streak inside the window did not trip the breaker")
	}
}

// TestOpenAdmitsExactlyOneProbeAfterItsCooldown. "Exactly one" is the point: a recovering
// host that is handed the whole backlog the moment it answers is a host that goes down
// again. It needs no coordination because every touch is on the single writer (I3).
func TestOpenAdmitsExactlyOneProbeAfterItsCooldown(t *testing.T) {
	b, at := breakerAt(0)
	tripIt(b)

	at(breakerCooldown - time.Millisecond)
	if b.allow(mailWorker, 1) {
		t.Error("a job went out before the cooldown elapsed")
	}
	at(time.Millisecond)
	if !b.allow(mailWorker, 42) {
		t.Fatal("the probe was not admitted after the cooldown")
	}
	for jobKey := uint64(43); jobKey < 50; jobKey++ {
		if b.allow(mailWorker, jobKey) {
			t.Fatalf("job %d went out alongside the probe, want exactly one", jobKey)
		}
	}
	if !b.holdingFor(mailType) {
		t.Error("holdingFor = false while a probe is outstanding, want true")
	}
}

// TestASuccessfulProbeClosesAndForgets: the backlog drains by itself, and the breaker
// leaves nothing behind — a target that recovers costs exactly as much as one that never
// failed, including its map entry.
func TestASuccessfulProbeClosesAndForgets(t *testing.T) {
	b, at := breakerAt(0)
	tripIt(b)
	at(breakerCooldown)

	if !b.allow(mailWorker, 42) {
		t.Fatal("no probe to succeed")
	}
	b.succeeded(mailWorker)

	if !b.allow(mailWorker, 43) {
		t.Error("the gate is still closed after the probe succeeded")
	}
	if b.holdingFor(mailType) || len(b.byKey) != 0 {
		t.Errorf("holdingFor=%v entries=%d, want a breaker that forgot it ever tripped",
			b.holdingFor(mailType), len(b.byKey))
	}
}

// TestAFailedProbeReopensAndTheCooldownGrowsToItsCap. An outage costs one retry per
// cooldown rather than N retries per instance, and the cooldown stops growing so a
// recovered host is not ignored for an hour because it was down for one.
func TestAFailedProbeReopensAndTheCooldownGrowsToItsCap(t *testing.T) {
	b, at := breakerAt(0)
	tripIt(b)

	want := breakerCooldown
	for cycle := 0; cycle < 8; cycle++ {
		at(want - time.Millisecond)
		if b.allow(mailWorker, 900) {
			t.Fatalf("cycle %d: a probe went out before the %s cooldown", cycle, want)
		}
		at(time.Millisecond)
		if !b.allow(mailWorker, 900) {
			t.Fatalf("cycle %d: no probe after the %s cooldown", cycle, want)
		}
		b.failed(mailWorker, 900, 5, "dial tcp: connection refused")
		if want *= 2; want > breakerMaxCooldown {
			want = breakerMaxCooldown
		}
	}
	if got := b.byKey[mailWorker].cooldown; got != breakerMaxCooldown {
		t.Errorf("cooldown = %s after eight failed probes, want the cap %s", got, breakerMaxCooldown)
	}
}

// TestAStaleFailureIsNotTheProbesVerdict. A job leased *before* the trip can report long
// after it, because a lease runs for minutes and the first cooldown for seconds. Letting
// that old news re-open the breaker would double the cooldown over a call that was
// already in flight when the target was last known to be down.
func TestAStaleFailureIsNotTheProbesVerdict(t *testing.T) {
	b, at := breakerAt(0)
	tripIt(b)
	at(breakerCooldown)
	if !b.allow(mailWorker, 42) {
		t.Fatal("no probe to test against")
	}

	b.failed(mailWorker, 8888, 9, "dial tcp: connection refused") // leased before the trip
	if got := b.byKey[mailWorker].cooldown; got != breakerCooldown {
		t.Errorf("cooldown = %s after a stale failure, want the probe's %s", got, breakerCooldown)
	}
	if got := b.byKey[mailWorker].state; got != breakerHalfOpen {
		t.Errorf("state = %s after a stale failure, want the probe still out", got)
	}
	// And the probe's own verdict still lands.
	b.succeeded(mailWorker)
	if !b.allow(mailWorker, 43) {
		t.Error("the probe succeeded and the gate stayed shut")
	}
}

// TestAFailureWhileOpenIsNoNews. Between the trip and the first probe, every job that
// was already leased is still out there and will report. None of it is new information —
// the breaker tripped on exactly this — and counting it would push the cooldown up by
// however many jobs happened to be in flight when the target died.
func TestAFailureWhileOpenIsNoNews(t *testing.T) {
	b, at := breakerAt(0)
	tripIt(b)
	e := b.byKey[mailWorker]
	trippedAt, probeAt := e.trippedAt, e.probeAt

	at(time.Second)
	for job := uint64(7000); job < 7050; job++ {
		b.failed(mailWorker, job, job, "dial tcp: connection refused")
	}

	if e.cooldown != breakerCooldown || e.probeAt != probeAt || e.trippedAt != trippedAt {
		t.Errorf("fifty in-flight failures moved the breaker to cooldown=%s probeAt=%d trippedAt=%d, want %s/%d/%d",
			e.cooldown, e.probeAt, e.trippedAt, breakerCooldown, probeAt, trippedAt)
	}
}

// TestASuccessFromAnyJobClosesTheBreaker is a deliberate asymmetry. Holding healthy work
// back is the expensive error — it is the very thing the breaker exists to avoid doing
// by accident — and a wrong close costs at most one more trip, which is three failures.
// So any completion is taken as evidence the target answers, whoever's job it was.
func TestASuccessFromAnyJobClosesTheBreaker(t *testing.T) {
	b, _ := breakerAt(0)
	tripIt(b)

	b.succeeded(mailWorker) // a job leased before the trip, finishing late
	if !b.allow(mailWorker, 1) {
		t.Error("a completed call against the target did not re-open the gate")
	}
}

// TestALostProbeIsWrittenOffWithoutEscalating. A worker that takes the probe and never
// reports — it crashed, or the lease simply expired — must not wedge the breaker half
// open forever. It also must not be read as a failure: nothing was learned, so the
// cooldown does not grow.
func TestALostProbeIsWrittenOffWithoutEscalating(t *testing.T) {
	b, at := breakerAt(0)
	tripIt(b)
	at(breakerCooldown)
	if !b.allow(mailWorker, 42) {
		t.Fatal("no probe to lose")
	}

	at(breakerProbeTimeout - time.Millisecond)
	if b.allow(mailWorker, 43) {
		t.Error("a second probe went out while the first was still within its lease")
	}
	at(time.Millisecond)
	if !b.allow(mailWorker, 44) {
		t.Fatal("the breaker is wedged half open after a probe that never reported")
	}
	if got := b.byKey[mailWorker].cooldown; got != breakerCooldown {
		t.Errorf("cooldown = %s after a lost probe, want an unchanged %s — nothing was learned",
			got, breakerCooldown)
	}
}

// TestOneWorkersOutageLeavesAnothersJobsAlone is the reason the breaker is keyed by
// Worker and not by job type. Every mail task in the estate compiles to the one reserved
// mail job type, so a job-type breaker would let one dead SMTP host stop mail for every
// process on the server.
func TestOneWorkersOutageLeavesAnothersJobsAlone(t *testing.T) {
	other := breakerKey{jobType: mailWorker.jobType, connector: "Marketing SMTP"}
	b, _ := breakerAt(0)
	tripIt(b)

	if b.allow(mailWorker, 1) {
		t.Fatal("setup: the dead worker is not held")
	}
	if !b.allow(other, 2) {
		t.Error("a healthy worker's jobs are held because a neighbour sharing the job type is down")
	}
	// The job type is still the cheap pre-filter, so it says "look closer", not "hold".
	if !b.holdingFor(mailWorker.jobType) {
		t.Error("holdingFor = false, want true — something under this type is open")
	}
}

// TestATaskThatNamesNoWorkerIsItsOwnTarget. A plain job type an external worker serves by
// name alone has no worker record to key on; the type *is* the target as far as this
// engine can see, and a breaker on it must not spill onto types it shares nothing with.
func TestATaskThatNamesNoWorkerIsItsOwnTarget(t *testing.T) {
	plain := breakerKey{jobType: cardType}
	b, _ := breakerAt(0)

	fail(b, plain, 1)
	fail(b, plain, 2)
	fail(b, plain, 3)

	if b.allow(plain, 1) {
		t.Error("a breaker on a worker-less job type did not trip")
	}
	if b.holdingFor(mailType) {
		t.Error("holdingFor(io.atlas.mail) = true, want false — a different type entirely")
	}
}

// TestCloseNowEndsTheHold is the operator's override: someone who has fixed the endpoint
// does not wait out a cooldown, and does not have to guess whether they did.
func TestCloseNowEndsTheHold(t *testing.T) {
	b, _ := breakerAt(0)
	tripIt(b)

	if !b.closeNow(mailWorker) {
		t.Error("closeNow reported nothing to close on an open breaker")
	}
	if !b.allow(mailWorker, 1) {
		t.Error("the gate is still shut after an operator closed it")
	}
	if b.closeNow(mailWorker) {
		t.Error("closeNow reported a close on a breaker that was already closed")
	}
}

// TestOpenBreakersSayWhatAnOperatorNeedsToKnow. Work that silently does not happen is the
// one failure mode an operator cannot diagnose, so the breaker carries its own account:
// which target, since when, on what, when the next probe goes, and how much it has turned
// away since.
func TestOpenBreakersSayWhatAnOperatorNeedsToKnow(t *testing.T) {
	b, at := breakerAt(5000)
	b.failed(mailWorker, 10, 1, "dial tcp 10.0.0.9:587: connect: connection refused")
	b.failed(mailWorker, 20, 2, "dial tcp 10.0.0.9:587: connect: connection refused")
	b.failed(mailWorker, 30, 3, "i/o timeout")
	at(time.Second)
	b.allow(mailWorker, 40)
	b.allow(mailWorker, 41)

	open := b.openBreakers(typeName)
	if len(open) != 1 {
		t.Fatalf("openBreakers = %+v, want the one that is open", open)
	}
	got := open[0]
	if got.JobType != "io.atlas.mail" || got.Connector != mailWorker.connector {
		t.Errorf("breaker names %q/%q, want io.atlas.mail/%q", got.JobType, got.Connector,
			mailWorker.connector)
	}
	if got.Reason != "i/o timeout" {
		t.Errorf("reason = %q, want the failure that tripped it", got.Reason)
	}
	if got.TrippedAt != 5000 {
		t.Errorf("trippedAt = %d, want 5000 — when the third instance failed", got.TrippedAt)
	}
	if want := int64(5000 + breakerCooldown); got.ProbeAt != want {
		t.Errorf("probeAt = %d, want %d — one cooldown after the trip", got.ProbeAt, want)
	}
	if got.Refused != 2 {
		t.Errorf("refused = %d, want 2 — the jobs the gate turned away", got.Refused)
	}
	if got.State != "open" {
		t.Errorf("state = %q while the cooldown runs, want open", got.State)
	}
	// Once a probe is out the state is a different answer to "what is happening",
	// and an operator watching a recovery needs to see the difference.
	at(breakerCooldown)
	b.allow(mailWorker, 42)
	if got := b.openBreakers(typeName)[0].State; got != "probing" {
		t.Errorf("state = %q with a probe out, want probing", got)
	}
	if got := breakerClosed.String(); got != "closed" {
		t.Errorf("breakerClosed = %q, want closed", got)
	}

	// A breaker in the middle of a failure streak is not open, and does not appear:
	// the view answers "what is being held", not "what has ever failed".
	b.failed(breakerKey{jobType: cardType}, 1, 1, "boom")
	if open := b.openBreakers(typeName); len(open) != 1 {
		t.Errorf("openBreakers = %+v, want a streak that has not tripped left out", open)
	}
}

// TestTheGateDoesNotAllocateWhenNothingIsWrong pins the claim ADR-0340's amendment rests
// on. The gate sits in the dispatch path on the single writer, and it is consulted for
// every job type on every round for the entire life of a server that is working
// perfectly. If asking it cost an allocation, the mechanism would be paid for constantly
// and used almost never (I1).
func TestTheGateDoesNotAllocateWhenNothingIsWrong(t *testing.T) {
	b, _ := breakerAt(0)
	tripIt(b) // something is open, so the map is not trivially empty

	healthy := breakerKey{jobType: cardType, connector: "Acquirer"}
	if n := testing.AllocsPerRun(200, func() {
		if b.holdingFor(cardType) {
			t.Fatal("holdingFor reported a hold on a type with no breaker")
		}
		b.allow(healthy, 1)
	}); n != 0 {
		t.Errorf("the gate allocates %.1f times per job, want none", n)
	}
}

// TestTheDefaultClockIsWallTime covers the constructor's own default: a breaker built
// without a clock still stamps real times, so a caller that passes none gets a working
// breaker rather than one frozen at zero.
func TestTheDefaultClockIsWallTime(t *testing.T) {
	before := time.Now().UnixNano()
	b := newWorkerBreakers(nil)
	tripIt(b)

	got := b.byKey[mailWorker].trippedAt
	if got < before || got > time.Now().UnixNano() {
		t.Errorf("trippedAt = %d, want a stamp between %d and now", got, before)
	}
}

// TestOpenBreakersAreOrderedByWhatTheOperatorReads keeps the console from reshuffling
// rows between polls, which a map iteration would do on every read — and orders by the
// job type's *name* rather than its interned index, because the index is this engine's
// bookkeeping and an alphabetical list of numbers is not a list an operator can scan.
func TestOpenBreakersAreOrderedByWhatTheOperatorReads(t *testing.T) {
	b, _ := breakerAt(0)
	// mailType (12) sorts before cardType (13) by index and after it by name, so an
	// implementation that ordered by the key would fail this.
	unnamed := breakerKey{jobType: 77, connector: "Somebody"}
	for _, k := range []breakerKey{
		{jobType: mailType, connector: "Zulu"},
		{jobType: cardType},
		unnamed,
		{jobType: mailType, connector: "Alpha"},
	} {
		fail(b, k, 1)
		fail(b, k, 2)
		fail(b, k, 3)
	}

	open := b.openBreakers(typeName)
	want := []struct{ jobType, connector string }{
		{"", "Somebody"}, // a type the resolver cannot name keeps its row, without a number
		{"charge-card", ""},
		{"io.atlas.mail", "Alpha"},
		{"io.atlas.mail", "Zulu"},
	}
	if len(open) != len(want) {
		t.Fatalf("openBreakers = %+v, want %d", open, len(want))
	}
	for i, w := range want {
		if open[i].JobType != w.jobType || open[i].Connector != w.connector {
			t.Errorf("row %d = %q/%q, want %q/%q", i, open[i].JobType, open[i].Connector,
				w.jobType, w.connector)
		}
	}
	// Without a resolver the rows are still there and still ordered — the view simply
	// has no name to print.
	if bare := b.openBreakers(nil); len(bare) != len(want) || bare[0].JobType != "" {
		t.Errorf("openBreakers(nil) = %+v, want the same rows with empty names", bare)
	}
}

// TestTheHoldingIndexIsExact guards the cheap pre-filter against the failure that would
// make it useless in either direction: a stuck count keeps the dispatch path resolving
// workers forever, and a lost one lets a dead target through.
func TestTheHoldingIndexIsExact(t *testing.T) {
	a := breakerKey{jobType: mailType, connector: "A"}
	c := breakerKey{jobType: mailType, connector: "C"}
	b, _ := breakerAt(0)

	for _, k := range []breakerKey{a, c} {
		fail(b, k, 1)
		fail(b, k, 2)
		fail(b, k, 3)
	}
	if b.holding[mailType] != 2 {
		t.Fatalf("holding = %d, want 2 open under the type", b.holding[mailType])
	}
	b.succeeded(a)
	if !b.holdingFor(mailType) {
		t.Error("the type stopped being watched while one of its workers is still down")
	}
	b.closeNow(c)
	if b.holdingFor(mailType) {
		t.Error("holdingFor = true with nothing open")
	}
	if len(b.holding) != 0 {
		t.Errorf("holding kept %d empty rows, want none", len(b.holding))
	}
	// Reporting an outcome for a target the breaker never heard of is not an error and
	// must not leave anything behind — a completion arrives for every healthy job.
	b.succeeded(breakerKey{jobType: 99})
	if len(b.byKey) != 0 || len(b.holding) != 0 {
		t.Errorf("an unknown success left %d entries and %d holding rows", len(b.byKey), len(b.holding))
	}
}

// TestATrippedBreakerStartsOverAfterItRecovers: a target that was down an hour and has
// since been healthy should not be treated as a repeat offender the next time it blips.
// Forgetting the entry is what makes that true, and this pins it.
func TestATrippedBreakerStartsOverAfterItRecovers(t *testing.T) {
	b, at := breakerAt(0)
	tripIt(b)
	at(breakerCooldown)
	b.allow(mailWorker, 42)
	b.failed(mailWorker, 42, 5, "still down") // one failed probe: the cooldown doubled
	at(2 * breakerCooldown)
	b.allow(mailWorker, 43)
	b.succeeded(mailWorker)

	at(time.Hour)
	tripIt(b)
	if got := b.byKey[mailWorker].cooldown; got != breakerCooldown {
		t.Errorf("cooldown = %s on a fresh trip, want the base %s", got, breakerCooldown)
	}
}

// tripIt drives a breaker open the way an outage does: distinct instances, failing in a
// row.
func tripIt(b *workerBreakers) {
	for i := uint64(1); i <= breakerThreshold; i++ {
		fail(b, mailWorker, i)
	}
}
