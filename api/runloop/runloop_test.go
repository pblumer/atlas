package runloop

import (
	"context"
	"sync"
	"testing"
	"time"
)

// start builds a loop and runs it, returning the loop and a stop function that
// closes it down and waits for the goroutine to finish.
func start(t *testing.T) (*Loop, func()) {
	t.Helper()
	quit := make(chan struct{})
	l := New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		l.Run()
	}()
	stopped := false
	return l, func() {
		if stopped {
			return
		}
		stopped = true
		close(quit)
		wg.Wait()
	}
}

// TestDoRunsTheClosure is the base case: Do runs what it is given.
func TestDoRunsTheClosure(t *testing.T) {
	l, stop := start(t)
	defer stop()

	ran := false
	l.Do(func() { ran = true })
	if !ran {
		t.Error("Do did not run the closure")
	}
}

// TestDoBlocksUntilTheClosureCompletes is what lets a caller read a result out of
// the closure by assignment: Do must not return before the closure has finished
// writing.
func TestDoBlocksUntilTheClosureCompletes(t *testing.T) {
	l, stop := start(t)
	defer stop()

	var got string
	l.Do(func() {
		time.Sleep(10 * time.Millisecond)
		got = "produced"
	})
	if got != "produced" {
		t.Errorf("Do returned before the closure finished: got %q", got)
	}
}

// TestDoSerializesConcurrentCallers is the single-writer invariant itself (I3,
// ADR-0002). Many concurrent callers mutate a variable guarded by nothing at all;
// under -race, any overlap is a data race and fails the test. This is the reason
// the loop exists, so it is tested directly rather than inferred from the
// absence of races elsewhere.
func TestDoSerializesConcurrentCallers(t *testing.T) {
	l, stop := start(t)
	defer stop()

	shared := 0 // deliberately unguarded: the loop is the only writer
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Do(func() { shared++ })
		}()
	}
	wg.Wait()
	if shared != 50 {
		t.Errorf("shared = %d, want 50", shared)
	}
}

// TestDoAfterQuitDoesNotRun covers the shutdown contract callers depend on: once
// the loop is closing, the closure does not run and Do returns rather than
// blocking forever. Callers must then read their result variables' zero values
// as "not produced".
func TestDoAfterQuitDoesNotRun(t *testing.T) {
	l, stop := start(t)
	stop() // close the loop before dispatching

	ran := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.Do(func() { ran = true })
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Do blocked after the loop closed")
	}
	if ran {
		t.Error("the closure ran after the loop closed")
	}
}

// TestRunReturnsOnQuit proves the goroutine is not leaked: closing quit ends Run.
func TestRunReturnsOnQuit(t *testing.T) {
	quit := make(chan struct{})
	l := New(quit)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		l.Run()
	}()
	close(quit)
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return when quit closed")
	}
}

// TestDoDuringShutdownDoesNotDeadlock covers the race the select in Do exists
// for: a caller dispatching at the same moment the loop is closing must either
// run or be dropped, never hang. Without the quit arm this deadlocks.
func TestDoDuringShutdownDoesNotDeadlock(t *testing.T) {
	quit := make(chan struct{})
	l := New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		l.Run()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			l.Do(func() {})
		}
	}()
	close(quit) // concurrent with the dispatch loop above
	wg.Wait()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a caller hung dispatching into a closing loop")
	}
}

// TestPingReachesARunningLoop is the readiness probe's happy path.
func TestPingReachesARunningLoop(t *testing.T) {
	l, stop := start(t)
	defer stop()
	if !l.Ping(context.Background(), 2*time.Second) {
		t.Error("Ping on a running loop = false, want true")
	}
}

// TestPingGivesUpOnAWedgedLoop is why Ping exists rather than Do with a timeout:
// a loop busy in a long closure must be reported unreachable rather than block
// the prober until it frees up.
func TestPingGivesUpOnAWedgedLoop(t *testing.T) {
	l, stop := start(t)
	defer stop()

	release := make(chan struct{})
	occupied := make(chan struct{})
	go l.Do(func() { close(occupied); <-release })
	<-occupied // the loop is now busy and cannot take another closure

	if l.Ping(context.Background(), 50*time.Millisecond) {
		t.Error("Ping on a wedged loop = true, want false")
	}
	close(release)
}

// TestPingGivesUpOnAStoppedLoop covers the case the deadline also has to cover: a
// loop that is not draining at all.
func TestPingGivesUpOnAStoppedLoop(t *testing.T) {
	l := New(make(chan struct{})) // never run
	if l.Ping(context.Background(), 50*time.Millisecond) {
		t.Error("Ping on a loop that was never run = true, want false")
	}
}

// TestPingHonorsAHungUpCaller proves a prober that cancelled is answered at once
// rather than at the deadline.
func TestPingHonorsAHungUpCaller(t *testing.T) {
	l := New(make(chan struct{})) // never run, so the send blocks
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if l.Ping(ctx, time.Hour) {
		t.Error("Ping with a cancelled context = true, want false")
	}
}
