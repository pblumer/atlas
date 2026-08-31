package playground_test

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// Without a pool, ten cases of a one-hour task all finish an hour after they
// started: nothing is competing for anything.
func TestWithoutAPoolEveryCaseWorksInParallel(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Hour, Max: time.Hour},
	})
	for i := 0; i < 10; i++ {
		if _, err := sb.StartCase(); err != nil {
			t.Fatalf("start case %d: %v", i, err)
		}
	}
	if _, err := sb.Run(playground.DefaultBudget()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := sb.Now(), simStart.Add(time.Hour); !got.Equal(want) {
		t.Errorf("simulated end = %s, want %s — unpooled work does not queue", got, want)
	}
}

// One clerk and ten one-hour cases take ten hours: the pool is what turns a
// duration into a queue, and a queue is what makes the waiting time a statement
// about capacity rather than a sum of the durations somebody typed in.
func TestAPoolOfOneSerializesTheWork(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human:  &playground.Stub{Min: time.Hour, Max: time.Hour},
		Pools:  map[string]playground.Pool{"clerks": {Capacity: 1}},
		PoolOf: map[string]string{"approve": "clerks"},
	})
	for i := 0; i < 10; i++ {
		if _, err := sb.StartCase(); err != nil {
			t.Fatalf("start case %d: %v", i, err)
		}
	}
	if _, err := sb.Run(playground.DefaultBudget()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := sb.Now(), simStart.Add(10*time.Hour); !got.Equal(want) {
		t.Errorf("simulated end = %s, want %s — one clerk, ten hours of work", got, want)
	}
}

// Three clerks and nine one-hour cases take three hours, and the pool reports how
// busy it was and how long the queue got.
func TestAPoolReportsItsUtilisationAndQueue(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human:  &playground.Stub{Min: time.Hour, Max: time.Hour},
		Pools:  map[string]playground.Pool{"clerks": {Capacity: 3}},
		PoolOf: map[string]string{"approve": "clerks"},
	})
	for i := 0; i < 9; i++ {
		if _, err := sb.StartCase(); err != nil {
			t.Fatalf("start case %d: %v", i, err)
		}
	}
	if _, err := sb.Run(playground.DefaultBudget()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := sb.Now(), simStart.Add(3*time.Hour); !got.Equal(want) {
		t.Errorf("simulated end = %s, want %s", got, want)
	}

	stats := sb.PoolStats()["clerks"]
	if stats.Served != 9 {
		t.Errorf("served = %d, want 9", stats.Served)
	}
	// Nine hours of work over three hours with three seats: fully busy throughout.
	if stats.BusyTime != 9*time.Hour {
		t.Errorf("busy = %s, want 9h of seat time", stats.BusyTime)
	}
	// Six cases wait at the start; the queue never gets longer than that.
	if stats.MaxQueue != 6 {
		t.Errorf("max queue = %d, want 6", stats.MaxQueue)
	}
}

// A pool that only works during the day does not work at night: the case that
// arrives at half past four waits for the morning.
func TestAPoolOnlyWorksWhileItsCalendarIsOpen(t *testing.T) {
	// simStart is 08:00. A window of 08:00–17:00 leaves an hour of work to do when
	// a two-hour case starts at 16:00.
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: 2 * time.Hour, Max: 2 * time.Hour},
		Pools: map[string]playground.Pool{"clerks": {
			Capacity: 1,
			Calendar: playground.Calendar{Open: []playground.Window{{From: 8 * time.Hour, To: 17 * time.Hour}}},
		}},
		PoolOf: map[string]string{"approve": "clerks"},
	})
	// Five two-hour cases on one seat: 08–10, 10–12, 12–14, 14–16, and the fifth
	// gets an hour in before the pool closes at 17:00 and finishes its second hour
	// when the pool opens again — the case stays on that clerk's desk overnight.
	for i := 0; i < 5; i++ {
		if _, err := sb.StartCase(); err != nil {
			t.Fatalf("start case %d: %v", i, err)
		}
	}
	if _, err := sb.Run(playground.DefaultBudget()); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := simStart.Add(25 * time.Hour) // next day, 09:00
	if got := sb.Now(); !got.Equal(want) {
		t.Errorf("simulated end = %s, want %s — the last hour of work waits for the morning", got, want)
	}
}

// The report splits an element's elapsed time into what it waited for a seat and
// what the work itself took. Without the split the bottleneck ranking says
// nothing the author did not already type in.
func TestAnElementReportsWaitingSeparatelyFromWork(t *testing.T) {
	sb := openSandbox(t, "user-task.bpmn", playground.StubSet{
		Human:  &playground.Stub{Min: time.Hour, Max: time.Hour},
		Pools:  map[string]playground.Pool{"clerks": {Capacity: 1}},
		PoolOf: map[string]string{"approve": "clerks"},
	})
	for i := 0; i < 3; i++ {
		if _, err := sb.StartCase(); err != nil {
			t.Fatalf("start case %d: %v", i, err)
		}
	}
	if _, err := sb.Run(playground.DefaultBudget()); err != nil {
		t.Fatalf("run: %v", err)
	}

	el := sb.ElementStats()["approve"]
	if el.Runs != 3 {
		t.Errorf("runs = %d, want 3", el.Runs)
	}
	if el.Work != 3*time.Hour {
		t.Errorf("work = %s, want 3h — an hour of work three times", el.Work)
	}
	// The three cases wait 0h, 1h and 2h for the single seat.
	if el.Wait != 3*time.Hour {
		t.Errorf("wait = %s, want 3h in total (0 + 1 + 2)", el.Wait)
	}
	if el.MaxWait != 2*time.Hour {
		t.Errorf("longest wait = %s, want 2h", el.MaxWait)
	}
}

// An unpooled element still reports its work, so a model with no pools at all
// still gets a bottleneck ranking — every wait is simply zero.
func TestAnUnpooledElementReportsWorkAndNoWait(t *testing.T) {
	sb := openSandbox(t, "service-task.bpmn", playground.StubSet{
		Default: &playground.Stub{Min: 30 * time.Minute, Max: 30 * time.Minute,
			Outputs: []model.VariableValue{{Name: "status", Kind: model.VarString, Text: "ok"}}},
	})
	if _, err := sb.StartCase(); err != nil {
		t.Fatalf("start case: %v", err)
	}
	if _, err := sb.Run(playground.DefaultBudget()); err != nil {
		t.Fatalf("run: %v", err)
	}
	el := sb.ElementStats()["charge"]
	if el.Work != 30*time.Minute || el.Wait != 0 {
		t.Errorf("charge = %+v, want 30m of work and no waiting", el)
	}
}

// A pool nobody is assigned to is a configuration mistake worth reporting at the
// start rather than a silently ignored line in the report.
func TestAnElementPointingAtAnUnknownPoolIsRefused(t *testing.T) {
	_, err := playground.Open(playground.Options{
		ModelXML: fixture(t, "user-task.bpmn"), BaseDir: t.TempDir(), StartTime: simStart,
		Stubs: playground.StubSet{
			Human:  &playground.Stub{Min: time.Hour, Max: time.Hour},
			PoolOf: map[string]string{"approve": "nobody"},
		},
	})
	if err == nil {
		t.Fatal("an element assigned to a pool that does not exist should be refused")
	}
}

// A pool with no capacity would hold every case for ever, which is a mistake
// rather than a model of anything.
func TestAPoolNeedsCapacity(t *testing.T) {
	_, err := playground.Open(playground.Options{
		ModelXML: fixture(t, "user-task.bpmn"), BaseDir: t.TempDir(), StartTime: simStart,
		Stubs: playground.StubSet{Pools: map[string]playground.Pool{"clerks": {Capacity: 0}}},
	})
	if err == nil {
		t.Fatal("a pool with no seats should be refused")
	}
}
