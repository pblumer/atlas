package compiler

import "testing"

func TestBuilderLinearProcess(t *testing.T) {
	b := NewBuilder(42, "order", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("payment", 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := cp.StartEvents(); len(got) != 1 || got[0] != start {
		t.Errorf("StartEvents = %v, want [%d]", got, start)
	}
	if cp.Node(start).Type != TypeStartEvent || cp.Node(task).Type != TypeServiceTask || cp.Node(end).Type != TypeEndEvent {
		t.Error("node types not linearized correctly")
	}

	// start → task
	out := cp.Outgoing(start)
	if len(out) != 1 || cp.Flow(out[0]).Target != task {
		t.Errorf("start outgoing = %v, want one flow to task", out)
	}
	// task → end
	out = cp.Outgoing(task)
	if len(out) != 1 || cp.Flow(out[0]).Target != end {
		t.Errorf("task outgoing = %v, want one flow to end", out)
	}
	// end has no outgoing
	if got := cp.Outgoing(end); len(got) != 0 {
		t.Errorf("end outgoing = %v, want none", got)
	}

	detail := cp.ServiceTask(cp.Node(task).Detail)
	if cp.Intern(detail.JobType) != "payment" || detail.Retries != 3 {
		t.Errorf("service task detail = %+v (jobType %q)", detail, cp.Intern(detail.JobType))
	}
}

func TestBuilderRejectsDanglingFlow(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	b.Connect(start, 999) // no such node
	if _, err := b.Build(); err == nil {
		t.Error("Build with dangling flow = nil error, want error")
	}
}

// A scrape task carries its whole target in the model (ADR-0118), so the builder is
// where a field can land in the wrong slot without anything failing to compile: the
// worker would then fetch with the selector, or extract the URL's attribute. Nothing
// downstream would catch it, because both are strings.
func TestBuilderWebScrapeTaskCarriesEveryFieldInItsOwnSlot(t *testing.T) {
	b := NewBuilder(7, "scrape", 1)
	start := b.AddStartEvent()
	scrape := b.AddWebScrapeConnectorTask(WebScrapeConfig{
		Url:       RestExpr{Literal: "https://example.com/prices"},
		Selector:  RestExpr{Literal: ".price"},
		Attribute: "data-chf",
		Result:    "preise",
		Retries:   4,
	})
	end := b.AddEndEvent()
	b.Connect(start, scrape)
	b.Connect(scrape, end)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.Node(scrape).Type; got != TypeConnectorTask {
		t.Errorf("node type = %v, want a task", got)
	}
	d := cp.ConnectorTask(cp.Node(scrape).Detail)
	if got := cp.Intern(d.JobType); got != WebScrapeJobType {
		t.Errorf("job type = %q, want %q — no other worker may pick this up", got, WebScrapeJobType)
	}
	if d.Url.Literal != "https://example.com/prices" || d.ScrapeSelector.Literal != ".price" {
		t.Errorf("url = %+v, selector = %+v; want each in its own slot", d.Url, d.ScrapeSelector)
	}
	if got := cp.Intern(d.ScrapeAttribute); got != "data-chf" {
		t.Errorf("attribute = %q, want data-chf", got)
	}
	if got := cp.Intern(d.ResultVar); got != "preise" {
		t.Errorf("result variable = %q, want preise", got)
	}
	if d.Retries != 4 {
		t.Errorf("retries = %d, want 4", d.Retries)
	}
	// A scrape names no registry worker and is not a clio task: those slots stay -1,
	// or the worker-resolution pass would look for a worker nobody configured.
	for name, got := range map[string]int32{
		"connector": d.Connector, "subject": d.Subject, "eventType": d.EventType,
		"clioQuery": d.ClioQuery, "reduceSpec": d.ReduceSpec, "method": d.Method, "auth": d.Auth,
	} {
		if got != -1 {
			t.Errorf("%s = %d, want -1: a scrape carries its target in the model", name, got)
		}
	}
}

// AddTimerCatchEvent is the duration convenience over AddTimerCatchSchedule. It has to
// stay exactly that: a catch built either way must arm the identical timer, or a model
// compiled through the short form would wait for a different moment than the same
// model compiled through the long one.
func TestBuilderTimerCatchDurationIsTheScheduleForm(t *testing.T) {
	const durNanos = int64(90e9)

	build := func(add func(*Builder) int32) TimerCatchDetail {
		t.Helper()
		b := NewBuilder(8, "wait", 1)
		start := b.AddStartEvent()
		catch := add(b)
		end := b.AddEndEvent()
		b.Connect(start, catch)
		b.Connect(catch, end)
		cp, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if got := cp.Node(catch).Type; got != TypeTimerCatchEvent {
			t.Fatalf("node type = %v, want a timer catch event", got)
		}
		return *cp.TimerCatch(cp.Node(catch).Detail)
	}

	short := build(func(b *Builder) int32 { return b.AddTimerCatchEvent(durNanos) })
	long := build(func(b *Builder) int32 {
		return b.AddTimerCatchSchedule(TimerSchedule{Kind: TimerDuration, BaseNanos: durNanos})
	})
	if short != long {
		t.Errorf("duration form built %+v, schedule form built %+v; they must be the same catch", short, long)
	}
	if short.Schedule.Kind != TimerDuration || short.Schedule.BaseNanos != durNanos {
		t.Errorf("schedule = %+v, want a %d ns duration", short.Schedule, durNanos)
	}
}

// A timer start is read two ways: TimerStartEvents scans the node table once at deploy
// time to say which elements arm a process-instantiating timer, and TimerStart resolves
// one node's detail on the hot path. They index the same table, so they must agree —
// a disagreement would arm one schedule and fire another.
func TestBuilderTimerStartDetailAgreesWithTheScan(t *testing.T) {
	want := TimerSchedule{Kind: TimerCycleInterval, BaseNanos: 3600e9, Repetitions: 5}

	b := NewBuilder(9, "nightly", 1)
	start := b.AddTimerStartEvent(want)
	end := b.AddEndEvent()
	b.Connect(start, end)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	scanned := cp.TimerStartEvents()
	if len(scanned) != 1 || scanned[0].ElementId != start {
		t.Fatalf("TimerStartEvents = %+v, want the one timer start at element %d", scanned, start)
	}
	got := cp.TimerStart(cp.Node(start).Detail)
	if got.Schedule != want {
		t.Errorf("TimerStart schedule = %+v, want %+v", got.Schedule, want)
	}
	if scanned[0].Schedule != got.Schedule {
		t.Errorf("the scan reports %+v and the detail %+v; they index the same table", scanned[0].Schedule, got.Schedule)
	}
}
