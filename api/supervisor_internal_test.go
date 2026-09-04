package api

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// waitFor polls until cond holds, so a test never depends on a sleep being long
// enough. Child processes start when the operating system gets to them.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A supervised worker runs, is reported as running, and stops when the server does.
func TestSupervisorRunsAndStopsAChild(t *testing.T) {
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe = "sh"
	sup.add(SuperviseSpec{ID: "mailer-1", Kinds: []string{"send-email"}}, []string{"-c", "echo up; sleep 30"}, nil)
	sup.start()

	waitFor(t, "the child to report running", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].State == "running" && list[0].PID != 0
	})
	got := sup.list()[0]
	if got.ID != "mailer-1" || strings.Join(got.Kinds, ",") != "send-email" {
		t.Errorf("child = %+v, want mailer-1 serving send-email", got)
	}
	waitFor(t, "the child's output to be captured", func() bool {
		return strings.Contains(strings.Join(sup.list()[0].Log, "\n"), "up")
	})

	close(quit)
	sup.wait()
	if s := sup.list()[0].State; s != "stopped" {
		t.Errorf("state after shutdown = %q, want stopped", s)
	}
}

// A child that exits is brought back: that is the whole point of supervising one.
// The restart count is what tells an operator it is flapping rather than fine.
func TestSupervisorRestartsAChildThatExits(t *testing.T) {
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe = "sh"
	sup.backoff = time.Millisecond // the policy is tested separately
	sup.add(SuperviseSpec{ID: "flappy", Kinds: []string{"send-email"}}, []string{"-c", "exit 1"}, nil)
	sup.start()
	defer func() { close(quit); sup.wait() }()

	waitFor(t, "the child to be restarted several times", func() bool {
		return sup.list()[0].Starts >= 3
	})
	if last := sup.list()[0].LastExit; last == "" {
		t.Error("no exit reason recorded for a child that keeps failing")
	}
}

// Backoff grows with consecutive failures and is capped, so a child that cannot
// start does not spin the machine — and resets once one stays up, so a single blip
// does not punish a healthy worker forever.
func TestRestartBackoffGrowsAndResets(t *testing.T) {
	base, max := 100*time.Millisecond, 2*time.Second
	var prev time.Duration
	for attempt := 1; attempt <= 8; attempt++ {
		got := restartDelay(base, max, attempt)
		if got > max {
			t.Errorf("delay after %d failures = %v, want it capped at %v", attempt, got, max)
		}
		if got < prev {
			t.Errorf("delay after %d failures = %v, shorter than the previous %v", attempt, got, prev)
		}
		prev = got
	}
	if got := restartDelay(base, max, 0); got != base {
		t.Errorf("delay for a first start = %v, want the base %v", got, base)
	}
}

// The log is bounded: a chatty worker must not fill the server's memory, and what
// an operator wants is the tail anyway.
func TestChildLogKeepsOnlyTheTail(t *testing.T) {
	r := newLogRing(3)
	for _, line := range []string{"one", "two", "three", "four", "five"} {
		r.add(line)
	}
	got := strings.Join(r.lines(), ",")
	if got != "three,four,five" {
		t.Errorf("log tail = %q, want the last three lines", got)
	}
}

// Restarting on request kills the current child; the supervisor's own loop brings
// it back, which is what makes the console's button honest rather than a special
// path that could diverge.
func TestRestartOnRequestCyclesTheChild(t *testing.T) {
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe = "sh"
	sup.backoff = time.Millisecond
	sup.add(SuperviseSpec{ID: "mailer-1", Kinds: []string{"send-email"}}, []string{"-c", "sleep 30"}, nil)
	sup.start()
	defer func() { close(quit); sup.wait() }()

	waitFor(t, "the first child", func() bool { return sup.list()[0].PID != 0 })
	first := sup.list()[0].PID

	if err := sup.restart("mailer-1"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	waitFor(t, "a replacement child", func() bool {
		got := sup.list()[0]
		return got.PID != 0 && got.PID != first && got.State == "running"
	})
	if err := sup.restart("nobody"); err == nil {
		t.Error("restarting an unknown worker succeeded, want an error")
	}
}

// A restart asked for while the child is in its backoff must cut the wait short.
// Otherwise the button does nothing for up to the cap — the operator has just been
// told the worker is failing, and "try again now" is exactly what they mean.
func TestRestartDuringBackoffTriesAgainImmediately(t *testing.T) {
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe = "sh"
	sup.backoff = 30 * time.Second // long enough that only a restart can shorten it
	sup.add(SuperviseSpec{ID: "flappy", Kinds: []string{"send-email"}}, []string{"-c", "exit 1"}, nil)
	sup.start()
	defer func() { close(quit); sup.wait() }()

	waitFor(t, "the first failure", func() bool { return sup.list()[0].Starts >= 1 })
	before := sup.list()[0].Starts

	if err := sup.restart("flappy"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	waitFor(t, "another attempt without waiting out the backoff", func() bool {
		return sup.list()[0].Starts > before
	})
}

// A supervised worker whose command cannot start is reported as failed, with the
// reason, rather than quietly missing from the view. This is the first thing an
// operator hits after a typo in --supervise, and the Workers view is where they
// will look for it.
func TestAChildThatCannotStartIsReportedWithItsReason(t *testing.T) {
	quit := make(chan struct{})
	defer close(quit)
	sup := newSupervisor(quit)
	sup.exe = filepath.Join(t.TempDir(), "no-such-binary")
	sup.backoff = time.Millisecond
	sup.add(SuperviseSpec{ID: "typo-1", Kinds: []string{"send-email"}}, []string{"worker"}, nil)
	sup.start()

	waitFor(t, "the failure to be reported", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].State == "failed" && list[0].LastExit != ""
	})
	got := sup.list()[0]
	if !strings.Contains(got.LastExit, "no-such-binary") {
		t.Errorf("lastExit = %q, want it to name the command that could not start", got.LastExit)
	}
	// It keeps trying rather than giving up: the binary may appear, and an operator
	// who fixes the path should not also have to restart the server.
	waitFor(t, "the supervisor to try again", func() bool { return sup.list()[0].Starts > 1 })
}

// A worker that exits zero is still a failure from the supervisor's side: `atlas
// worker` runs until it is stopped, so a clean exit means it stopped working and
// nobody asked it to. Saying "exited without an error" is more useful than an empty
// reason, which reads as "no problem".
func TestAChildThatExitsCleanlyIsStillAFailure(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	quit := make(chan struct{})
	defer close(quit)
	sup := newSupervisor(quit)
	sup.exe = "sh"
	sup.backoff = time.Millisecond
	sup.add(SuperviseSpec{ID: "quitter-1"}, []string{"-c", "exit 0"}, nil)
	sup.start()

	waitFor(t, "the clean exit to be reported as a failure", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].State == "failed" && list[0].LastExit != ""
	})
	if got := sup.list()[0].LastExit; !strings.Contains(got, "should not do") {
		t.Errorf("lastExit = %q, want it to say a worker should not exit on its own", got)
	}
}

// A worker with nothing to serve is parked, not restarted. Before the default
// supervised one worker per kind this could not happen — a combined worker always had
// the other kinds to serve — and with it, a mail worker on a server whose operator has
// not configured a mail worker yet would restart forever on a backoff, filling the
// console with red for an ordinary state.
func TestAWorkerWithNothingToServeIsParkedRatherThanRestarted(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe, sup.backoff = "sh", time.Millisecond
	sup.add(SuperviseSpec{ID: "mail", Connectors: []string{connectorKindMail}},
		[]string{"-c", "exit " + strconv.Itoa(ExitNothingToServe)}, nil)
	sup.start()
	t.Cleanup(func() { close(quit); sup.wait() })

	waitFor(t, "the child to park", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].State == "idle"
	})
	parked := sup.list()[0].Starts

	// It stays parked: a backoff loop would be visible as the count climbing.
	time.Sleep(50 * sup.backoff)
	if got := sup.list()[0].Starts; got != parked {
		t.Errorf("starts = %d after parking at %d — it is restarting into the same emptiness", got, parked)
	}
	if why := sup.list()[0].LastExit; !strings.Contains(why, "nothing to serve") {
		t.Errorf("lastExit = %q, want it to say why it is waiting", why)
	}

	// And the restart button brings it back, so an operator who has just configured
	// the kind is not made to restart Atlas.
	if err := sup.restart("mail"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	waitFor(t, "the parked child to be started again", func() bool {
		return sup.list()[0].Starts > parked
	})
}

// A worker that exits for any other reason is still a failure and is still restarted:
// parking is for the one status that says "nothing here to do", not for crashes.
func TestAWorkerThatCrashesIsStillRestarted(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe, sup.backoff = "sh", time.Millisecond
	sup.add(SuperviseSpec{ID: "crasher"}, []string{"-c", "exit 1"}, nil)
	sup.start()
	t.Cleanup(func() { close(quit); sup.wait() })

	waitFor(t, "the child to be restarted after crashing", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].Starts > 1 && list[0].State == "failed"
	})
}

// A supervisor asked to start after the server has already quit spawns nothing. The
// existing shutdown tests close quit while a child is running, so which iteration of
// the supervise loop notices is a race — this one closes it first, which makes the
// loop's own quit branch the only path there is.
func TestSupervisorStartsNothingAfterQuit(t *testing.T) {
	quit := make(chan struct{})
	close(quit)
	sup := newSupervisor(quit)
	sup.exe = "sh"
	sup.add(SuperviseSpec{ID: "mailer-1", Kinds: []string{"send-email"}}, []string{"-c", "echo up; sleep 30"}, nil)
	sup.start()
	sup.wait()

	got := sup.list()
	if len(got) != 1 {
		t.Fatalf("list() = %d children, want 1", len(got))
	}
	if got[0].State != "stopped" || got[0].PID != 0 {
		t.Errorf("child = state %q pid %d, want stopped with no pid", got[0].State, got[0].PID)
	}
	if got[0].Starts != 0 {
		t.Errorf("starts = %d, want 0 — nothing should have been spawned", got[0].Starts)
	}
}
