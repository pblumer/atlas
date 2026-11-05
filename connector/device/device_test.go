package device

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// A relay board on line 17 (active low, ten-second limit), a fan on line 22 that is
// safe running, and a button to ground on line 27.
func testConfig() Config {
	return Config{
		Chip: "gpiochip0",
		Outputs: []Output{
			{Name: "relais", Line: 17, ActiveLow: true, MaxOnMs: 10_000},
			{Name: "luefter", Line: 22, Safe: "on"},
			{Name: "lampe", Line: 23},
		},
		Inputs: []Input{{Name: "taster", Line: 27, ActiveLow: true, Bias: "pull-up"}},
	}
}

func openTest(t *testing.T) (*Device, *FakeChip, *fakeClock) {
	t.Helper()
	chip, clock := NewFakeChip(), newFakeClock()
	d, err := Open(testConfig(), chip, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, chip, clock
}

func mustRead(t *testing.T, d *Device, name string) bool {
	t.Helper()
	on, err := d.Read(name)
	if err != nil {
		t.Fatal(err)
	}
	return on
}

func TestOpenClaimsOutputsAlreadyInTheirSafeState(t *testing.T) {
	_, chip, _ := openTest(t)
	// Every output's first and only level is its safe one: relais off is a high wire
	// (active low), luefter safe on is high, lampe off is low.
	want := []Change{{17, true}, {22, true}, {23, false}}
	if got := chip.History(); !slices.Equal(got, want) {
		t.Fatalf("history = %v, want %v", got, want)
	}
	if !chip.Held(27) || chip.Bias(27) != BiasPullUp {
		t.Fatal("the button is claimed with its pull-up")
	}
}

func TestOpenRefusesAnInvalidConfiguration(t *testing.T) {
	if _, err := Open(Config{Chip: "gpiochip0"}, NewFakeChip(), newFakeClock()); err == nil {
		t.Fatal("an empty configuration was opened")
	}
}

func TestOpenReleasesWhatItClaimedWhenALineIsBusy(t *testing.T) {
	for _, busy := range []int{22, 27} {
		chip := NewFakeChip()
		chip.FailRequest = map[int]error{busy: errors.New("device or resource busy")}
		_, err := Open(testConfig(), chip, newFakeClock())
		if err == nil || !strings.Contains(err.Error(), "busy") {
			t.Fatalf("line %d: err = %v", busy, err)
		}
		for _, line := range []int{17, 22, 23, 27} {
			if chip.Held(line) {
				t.Errorf("line %d busy: line %d is still held", busy, line)
			}
		}
	}
}

func TestWriteDrivesTheWireAsTheOutputReadsIt(t *testing.T) {
	d, chip, _ := openTest(t)
	ctx := context.Background()
	if err := d.Write(ctx, "relais", true); err != nil {
		t.Fatal(err)
	}
	if chip.Level(17) || !mustRead(t, d, "relais") {
		t.Fatal("relais on is a low wire on an active-low board")
	}
	if err := d.Write(ctx, "lampe", true); err != nil {
		t.Fatal(err)
	}
	if !chip.Level(23) {
		t.Fatal("lampe on is a high wire")
	}
	if err := d.Write(ctx, "relais", false); err != nil {
		t.Fatal(err)
	}
	if !chip.Level(17) || mustRead(t, d, "relais") {
		t.Fatal("relais off again")
	}
}

func TestWriteRefusesALineItDoesNotHave(t *testing.T) {
	d, _, _ := openTest(t)
	for _, name := range []string{"pumpe", "taster"} {
		if err := d.Write(context.Background(), name, true); !IsRefused(err) {
			t.Errorf("Write(%s) = %v, want a refusal", name, err)
		}
	}
}

func TestWriteReportsALineThatFails(t *testing.T) {
	d, chip, _ := openTest(t)
	chip.FailSet = map[int]error{23: errors.New("no such device")}
	err := d.Write(context.Background(), "lampe", true)
	if err == nil || IsRefused(err) || mustRead(t, d, "lampe") {
		t.Fatalf("err = %v; a failed write is retried and changes nothing", err)
	}
}

func TestTheWatchdogReturnsAnOutputToItsSafeState(t *testing.T) {
	d, chip, clock := openTest(t)
	if err := d.Write(context.Background(), "relais", true); err != nil {
		t.Fatal(err)
	}
	clock.awaitArmed(t, 1)
	clock.Advance(9999 * time.Millisecond)
	if !mustRead(t, d, "relais") {
		t.Fatal("tripped before its limit")
	}
	clock.Advance(time.Millisecond)
	if mustRead(t, d, "relais") || !chip.Level(17) || d.Trips() != 1 {
		t.Fatalf("after its limit: on=%v wire=%v trips=%d", mustRead(t, d, "relais"), chip.Level(17), d.Trips())
	}
}

func TestTheWatchdogIsDisarmedAndRearmedByWrites(t *testing.T) {
	d, _, clock := openTest(t)
	ctx := context.Background()
	d.Write(ctx, "relais", true)
	clock.awaitArmed(t, 1)
	d.Write(ctx, "relais", false)
	clock.Advance(time.Minute)
	if d.Trips() != 0 {
		t.Fatal("an output back in its safe state tripped")
	}

	d.Write(ctx, "relais", true)
	clock.awaitArmed(t, 1)
	clock.Advance(6 * time.Second)
	d.Write(ctx, "relais", true) // asked again: the limit starts again
	clock.awaitArmed(t, 1)
	clock.Advance(6 * time.Second)
	if !mustRead(t, d, "relais") || d.Trips() != 0 {
		t.Fatal("the first watchdog fired after a rewrite rearmed it")
	}
	clock.Advance(4 * time.Second)
	if mustRead(t, d, "relais") || d.Trips() != 1 {
		t.Fatal("the rearmed watchdog did not fire")
	}
}

func TestAnOutputWithoutALimitHasNoWatchdog(t *testing.T) {
	d, _, clock := openTest(t)
	d.Write(context.Background(), "lampe", true)
	clock.Advance(48 * time.Hour)
	if !mustRead(t, d, "lampe") {
		t.Fatal("lampe has no limit")
	}
	select {
	case <-clock.armed:
		t.Fatal("a watchdog was armed for an output without a limit")
	default:
	}
}

func TestAPulseDrivesThenReturnsToTheSafeState(t *testing.T) {
	d, chip, clock := openTest(t)
	result := make(chan error, 1)
	go func() {
		dedup, err := d.Pulse(context.Background(), 41, "lampe", true, 1500*time.Millisecond)
		if dedup {
			err = errors.New("the first pulse was deduplicated")
		}
		result <- err
	}()
	clock.awaitArmed(t, 1)
	if !chip.Level(23) {
		t.Fatal("lampe is not on during the pulse")
	}
	clock.Advance(1499 * time.Millisecond)
	select {
	case err := <-result:
		t.Fatalf("the pulse ended early: %v", err)
	default:
	}
	clock.Advance(time.Millisecond)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	want := []Change{{23, false}, {23, true}, {23, false}}
	var got []Change
	for _, c := range chip.History() {
		if c.Line == 23 {
			got = append(got, c)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("lampe history = %v, want %v", got, want)
	}
}

func TestAPulseRunsOncePerJob(t *testing.T) {
	d, chip, clock := openTest(t)
	pulse := func() (bool, error) {
		done := make(chan struct{})
		var dedup bool
		var err error
		go func() { dedup, err = d.Pulse(context.Background(), 7, "lampe", true, time.Second); close(done) }()
		return dedup, waitPulse(t, clock, done, &err)
	}
	if dedup, err := pulse(); dedup || err != nil {
		t.Fatalf("first pulse: dedup=%v err=%v", dedup, err)
	}
	before := len(chip.History())
	dedup, err := d.Pulse(context.Background(), 7, "lampe", true, time.Second)
	if !dedup || err != nil {
		t.Fatalf("re-leased job: dedup=%v err=%v", dedup, err)
	}
	if len(chip.History()) != before {
		t.Fatal("a re-leased job pulsed again")
	}
}

// waitPulse lets a pulse that is scheduling its end finish.
func waitPulse(t *testing.T, clock *fakeClock, done chan struct{}, err *error) error {
	t.Helper()
	clock.awaitArmed(t, 1)
	clock.Advance(time.Minute)
	<-done
	return *err
}

func TestAPulseIsRefusedWhenItCannotBeRight(t *testing.T) {
	d, _, _ := openTest(t)
	ctx := context.Background()
	cases := map[string]struct {
		name string
		on   bool
		dur  time.Duration
	}{
		"unknown output":    {"pumpe", true, time.Second},
		"no duration":       {"lampe", true, 0},
		"sub-millisecond":   {"lampe", true, 500 * time.Microsecond},
		"fractional":        {"lampe", true, 1500 * time.Microsecond},
		"longer than a job": {"lampe", true, 61 * time.Second},
		"longer than limit": {"relais", true, 11 * time.Second},
		"to the safe state": {"luefter", true, time.Second},
		"off when safe off": {"lampe", false, time.Second},
	}
	for label, tc := range cases {
		if _, err := d.Pulse(ctx, 1, tc.name, tc.on, tc.dur); !IsRefused(err) {
			t.Errorf("%s: err = %v, want a refusal", label, err)
		}
	}
}

func TestAPulseToOffOnAnOutputThatIsSafeOn(t *testing.T) {
	d, chip, clock := openTest(t)
	done := make(chan struct{})
	var err error
	go func() { _, err = d.Pulse(context.Background(), 3, "luefter", false, time.Second); close(done) }()
	if err := waitPulse(t, clock, done, &err); err != nil {
		t.Fatal(err)
	}
	if !chip.Level(22) {
		t.Fatal("luefter is back on")
	}
}

func TestACancelledPulseReturnsToSafeAndIsNotRepeated(t *testing.T) {
	d, chip, clock := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := d.Pulse(ctx, 9, "lampe", true, 30*time.Second)
		result <- err
	}()
	clock.awaitArmed(t, 1)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if chip.Level(23) {
		t.Fatal("lampe stayed on after the pulse was cancelled")
	}
	if dedup, _ := d.Pulse(context.Background(), 9, "lampe", true, time.Second); !dedup {
		t.Fatal("a pulse that had begun was repeated on retry")
	}
}

func TestAPulseThatCouldNotDriveMayBeRetried(t *testing.T) {
	d, chip, clock := openTest(t)
	chip.FailSet = map[int]error{23: errors.New("no such device")}
	if _, err := d.Pulse(context.Background(), 5, "lampe", true, time.Second); err == nil || IsRefused(err) {
		t.Fatalf("err = %v", err)
	}
	chip.FailSet = nil
	done := make(chan struct{})
	var dedup bool
	var err error
	go func() { dedup, err = d.Pulse(context.Background(), 5, "lampe", true, time.Second); close(done) }()
	if err := waitPulse(t, clock, done, &err); err != nil || dedup {
		t.Fatalf("retry: dedup=%v err=%v", dedup, err)
	}
}

func TestAPulseThatCannotReturnToSafeSaysSo(t *testing.T) {
	d, chip, clock := openTest(t)
	result := make(chan error, 1)
	go func() {
		_, err := d.Pulse(context.Background(), 6, "lampe", true, time.Second)
		result <- err
	}()
	clock.awaitArmed(t, 1)
	chip.mu.Lock()
	chip.FailSet = map[int]error{23: errors.New("no such device")}
	chip.mu.Unlock()
	clock.Advance(time.Second)
	if err := <-result; err == nil || !strings.Contains(err.Error(), "did not return to its safe state") {
		t.Fatalf("err = %v", err)
	}
}

func TestAWriteWaitsForAPulseOnTheSameOutput(t *testing.T) {
	d, _, clock := openTest(t)
	result := make(chan error, 1)
	go func() {
		_, err := d.Pulse(context.Background(), 8, "lampe", true, time.Second)
		result <- err
	}()
	clock.awaitArmed(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Write(ctx, "lampe", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("a write during a pulse did not wait: %v", err)
	}
	if _, err := d.Pulse(ctx, 99, "lampe", true, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("a second pulse during a pulse did not wait: %v", err)
	}
	clock.Advance(time.Second)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := d.Write(context.Background(), "lampe", true); err != nil {
		t.Fatal(err)
	}
}

func TestReadReportsAnInputAsItsActiveLowReadsIt(t *testing.T) {
	d, chip, _ := openTest(t)
	if mustRead(t, d, "taster") {
		t.Fatal("an open button against its pull-up is off")
	}
	chip.SetInput(27, false)
	if !mustRead(t, d, "taster") {
		t.Fatal("a pressed button pulls the line low, which is on")
	}
	if !mustRead(t, d, "luefter") {
		t.Fatal("an output reads its driven state")
	}
	if _, err := d.Read("pumpe"); !IsRefused(err) {
		t.Fatalf("err = %v", err)
	}
	chip.FailValue = map[int]error{27: errors.New("i/o error")}
	if _, err := d.Read("taster"); err == nil || IsRefused(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestSafeDrivesEveryOutputBackAndDisarms(t *testing.T) {
	d, chip, clock := openTest(t)
	ctx := context.Background()
	d.Write(ctx, "relais", true)
	clock.awaitArmed(t, 1)
	d.Write(ctx, "luefter", false)
	d.Write(ctx, "lampe", true)
	if err := d.Safe(); err != nil {
		t.Fatal(err)
	}
	if !chip.Level(17) || !chip.Level(22) || chip.Level(23) {
		t.Fatal("not every output is in its safe state")
	}
	clock.Advance(time.Hour)
	if d.Trips() != 0 {
		t.Fatal("Safe left a watchdog armed")
	}
	chip.FailSet = map[int]error{23: errors.New("gone")}
	d.Write(ctx, "relais", true)
	if err := d.Safe(); err == nil || !strings.Contains(err.Error(), "lampe") {
		t.Fatalf("err = %v", err)
	}
	if mustRead(t, d, "relais") {
		t.Fatal("one failing output kept the others from their safe state")
	}
}

func TestCloseLeavesEveryOutputSafeAndReleasesTheLines(t *testing.T) {
	chip, clock := NewFakeChip(), newFakeClock()
	d, err := Open(testConfig(), chip, clock)
	if err != nil {
		t.Fatal(err)
	}
	d.Write(context.Background(), "lampe", true)
	d.Write(context.Background(), "relais", true)
	clock.awaitArmed(t, 1)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if chip.Level(23) || !chip.Level(17) {
		t.Fatal("an output was left away from its safe state")
	}
	for _, line := range []int{17, 22, 23, 27} {
		if chip.Held(line) {
			t.Errorf("line %d is still held", line)
		}
	}
	clock.Advance(time.Hour) // a watchdog firing after Close does nothing
	if d.Trips() != 0 {
		t.Fatal("a watchdog fired on a closed device")
	}
	if err := d.Close(); err != nil {
		t.Fatal("a second Close is not an error")
	}
	if err := d.Write(context.Background(), "lampe", true); !errors.Is(err, ErrClosed) {
		t.Fatalf("Write after Close = %v", err)
	}
	if _, err := d.Read("lampe"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Read after Close = %v", err)
	}
	if err := d.Safe(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Safe after Close = %v", err)
	}
}

func TestTheDeduplicationWindowIsBounded(t *testing.T) {
	d, _, _ := openTest(t)
	for k := uint64(1); k <= dedupWindow+1; k++ {
		if !d.remember(k) {
			t.Fatalf("key %d was already remembered", k)
		}
	}
	if !d.remember(1) {
		t.Fatal("the oldest key was not let go")
	}
	if d.remember(dedupWindow + 1) {
		t.Fatal("a recent key was let go")
	}
	d.forget(dedupWindow + 1)
	if !d.remember(dedupWindow + 1) {
		t.Fatal("a forgotten key is still remembered")
	}
}

func TestAStaleWatchdogFiringDoesNothing(t *testing.T) {
	d, _, _ := openTest(t)
	d.Write(context.Background(), "relais", true)
	o := d.outputs["relais"]
	d.mu.Lock()
	stale := o.gen - 1
	d.mu.Unlock()
	d.trip(o, stale)
	if !mustRead(t, d, "relais") || d.Trips() != 0 {
		t.Fatal("a firing from before the last write acted")
	}
}

func TestAWatchdogWhoseLineFailsStillCounts(t *testing.T) {
	d, chip, clock := openTest(t)
	d.Write(context.Background(), "relais", true)
	clock.awaitArmed(t, 1)
	chip.mu.Lock()
	chip.FailSet = map[int]error{17: errors.New("gone")}
	chip.mu.Unlock()
	clock.Advance(10 * time.Second)
	if d.Trips() != 1 || !mustRead(t, d, "relais") {
		t.Fatal("a trip that could not drive must be counted and must not claim the output is safe")
	}
}

func TestReleaseRunsOnce(t *testing.T) {
	d, _, _ := openTest(t)
	if err := d.release(); err != nil {
		t.Fatal(err)
	}
	if err := d.release(); err != nil {
		t.Fatal("a second release is not an error")
	}
}

func TestTheFakeChipRefusesWhatARealOneWould(t *testing.T) {
	chip := NewFakeChip()
	out, err := chip.RequestOutput(5, false, "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chip.RequestOutput(5, false, "atlas"); err == nil {
		t.Fatal("a held line was claimed twice")
	}
	in, err := chip.RequestInput(6, BiasPullDown, "atlas")
	if err != nil {
		t.Fatal(err)
	}
	out.Close()
	in.Close()
	if err := out.Set(true); err == nil {
		t.Fatal("a released line was driven")
	}
	if _, err := in.Value(); err == nil {
		t.Fatal("a released line was read")
	}
	chip.Close()
	if _, err := chip.RequestInput(7, BiasNone, "atlas"); err == nil {
		t.Fatal("a closed chip handed out a line")
	}
}
