package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// forkTargetShapes builds one process holding every shape a resume point can be wrong
// about: an element inside a subprocess, a boundary event, an event subprocess's start,
// a joining parallel gateway, and — as the control — a plain task in the root scope that
// is a perfectly good resume point.
func forkTargetShapes(t testing.TB) (cp *compiler.CompiledProcess, ids map[string]int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey+7, "shapes", 2)
	start := b.AddStartEvent()
	plain := b.AddServiceTask(jobName, 3)
	host := b.AddServiceTask("hosted", 3)
	boundary := b.AddBoundaryTimerEvent(host, true, 1_000)
	split := b.AddParallelGateway()
	left := b.AddServiceTask("left", 3)
	right := b.AddServiceTask("right", 3)
	join := b.AddParallelGateway()
	sub := b.AddSubProcess()
	b.PushScope(sub)
	inner := b.AddStartEvent()
	innerTask := b.AddServiceTask("inner", 3)
	innerEnd := b.AddEndEvent()
	b.Connect(inner, innerTask)
	b.Connect(innerTask, innerEnd)
	b.PopScope()
	handler := b.AddSubProcess()
	b.PushScope(handler)
	hStart := b.AddStartEvent()
	hEnd := b.AddEndEvent()
	b.Connect(hStart, hEnd)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{
		StartNode: hStart, Interrupting: false, Kind: compiler.BoundaryTimer,
		Schedule: compiler.TimerSchedule{Kind: compiler.TimerDuration, BaseNanos: 1_000},
	})
	end := b.AddEndEvent()
	b.Connect(start, plain)
	b.Connect(plain, host)
	b.Connect(host, split)
	b.Connect(split, left)
	b.Connect(split, right)
	b.Connect(left, join)
	b.Connect(right, join)
	b.Connect(join, sub)
	b.Connect(sub, end)
	b.Connect(boundary, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, map[string]int32{
		"plain": plain, "boundary": boundary, "split": split, "join": join,
		"sub": sub, "innerTask": innerTask, "handlerStart": hStart, "handler": handler,
	}
}

// TestValidateForkRefusesResumePointsThatCannotRun is the gate ADR-0389
// puts in front of the command: a resume point that cannot honestly seed an execution is
// refused *before* the predecessor is terminated, because afterwards there is nothing to
// go back to.
func TestValidateForkRefusesResumePointsThatCannotRun(t *testing.T) {
	to, ids := forkTargetShapes(t)
	from, err := func() (*compiler.CompiledProcess, error) {
		b := compiler.NewBuilder(defKey+6, "shapes", 1)
		s := b.AddStartEvent()
		e := b.AddEndEvent()
		b.Connect(s, e)
		return b.Build()
	}()
	if err != nil {
		t.Fatalf("Build source: %v", err)
	}

	cases := []struct {
		name   string
		resume []int32
		want   string // substring of the reason
	}{
		{"an element inside a subprocess", []int32{ids["innerTask"]}, "sits inside"},
		{"a boundary event", []int32{ids["boundary"]}, "boundary event"},
		{"an event subprocess's start", []int32{ids["handlerStart"]}, "event subprocess"},
		{"an event subprocess itself", []int32{ids["handler"]}, "event subprocess"},
		{"a joining gateway", []int32{ids["join"]}, "joining gateway"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := engine.ValidateFork(from, to, 0, tc.resume)
			if len(got) != 1 || !strings.Contains(got[0].Reason, tc.want) {
				t.Fatalf("ValidateFork = %+v, want one problem mentioning %q", got, tc.want)
			}

		})
	}

	// The control: a plain root-scope task, a splitting gateway (one incoming flow) and
	// a subprocess are all things work can legitimately resume at.
	for _, ok := range []string{"plain", "split", "sub"} {
		if got := engine.ValidateFork(from, to, 0, []int32{ids[ok]}); len(got) != 0 {
			t.Errorf("ValidateFork(%s) = %+v, want no problem", ok, got)
		}
	}
	// Several at once is how a fork resumes an instance that had several tokens.
	if got := engine.ValidateFork(from, to, 0, []int32{ids["plain"], ids["sub"]}); len(got) != 0 {
		t.Errorf("ValidateFork(two resume points) = %+v, want no problem", got)
	}
}

// TestValidateForkRefusesTheInstanceItself covers what is wrong with the fork rather
// than with a resume point — each one checked before any element is looked at, because
// none of them has an element to blame.
func TestValidateForkRefusesTheInstanceItself(t *testing.T) {
	to, ids := forkTargetShapes(t)
	from, err := func() (*compiler.CompiledProcess, error) {
		b := compiler.NewBuilder(defKey+6, "shapes", 1)
		s := b.AddStartEvent()
		e := b.AddEndEvent()
		b.Connect(s, e)
		return b.Build()
	}()
	if err != nil {
		t.Fatalf("Build source: %v", err)
	}
	other, err := func() (*compiler.CompiledProcess, error) {
		b := compiler.NewBuilder(defKey+8, "somethingelse", 1)
		s := b.AddStartEvent()
		e := b.AddEndEvent()
		b.Connect(s, e)
		return b.Build()
	}()
	if err != nil {
		t.Fatalf("Build other: %v", err)
	}
	resume := []int32{ids["plain"]}

	cases := []struct {
		name   string
		from   *compiler.CompiledProcess
		parent uint64
		resume []int32
		want   string
	}{
		{"an undeployed source", nil, 0, resume, "not deployed"},
		{"the version it already runs", to, 0, resume, "already running this version"},
		{"another process entirely", other, 0, resume, "can only continue in a version of its own process"},
		{"a call activity's child", from, 99, resume, "cannot be forked"},
		{"no resume point", from, 0, nil, "no resume point"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := engine.ValidateFork(tc.from, to, tc.parent, tc.resume)
			if len(got) != 1 || !strings.Contains(got[0].Reason, tc.want) {
				t.Fatalf("ValidateFork = %+v, want one problem mentioning %q", got, tc.want)
			}
		})
	}
}
