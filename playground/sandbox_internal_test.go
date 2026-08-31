package playground

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
)

// A sandbox reads its own state store on every call. When a record in it cannot be
// decoded, each of those calls has to *report* it: a run whose numbers silently
// skip the rows they could not read is worse than one that stops, because the
// report still looks complete.
//
// The store's Inject* affordances are the repo's own way to reach these branches
// from outside; nothing in production writes records that way.
func openInternal(t *testing.T, fixtureName string) *Sandbox {
	t.Helper()
	xml, err := os.ReadFile(filepath.Join("testdata", fixtureName))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sb, err := Open(Options{ModelXML: xml, BaseDir: t.TempDir(), Stubs: DefaultStubs()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Close() })
	return sb
}

func TestAnUnreadableProcessInstanceIsReported(t *testing.T) {
	sb := openInternal(t, "user-task.bpmn")
	key, err := sb.StartCase()
	if err != nil {
		t.Fatalf("start case: %v", err)
	}
	if err := sb.store.InjectCorruptProcessInstance(key); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := sb.Case(key); err == nil {
		t.Error("reading a case whose record cannot be decoded should be an error")
	}
	// Starting another case has to scan for the newest one, and walks over the
	// unreadable record on the way.
	if _, err := sb.StartCase(); err == nil {
		t.Error("starting a case should report a store it cannot read")
	}
	// Every job in the sandbox belongs to an instance, so resolving one now fails too.
	if _, err := sb.OpenTasks(); err == nil {
		t.Error("listing open tasks should report the unreadable instance")
	}
	if _, err := sb.Run(DefaultBudget()); err == nil {
		t.Error("a run should stop on a store it cannot read")
	}
	if _, _, err := sb.Step(); err == nil {
		t.Error("a step should stop on a store it cannot read")
	}
	if err := sb.Advance(time.Hour); err == nil {
		t.Error("advancing the clock should stop on a store it cannot read")
	}
	if err := sb.PublishMessage("m", "K"); err == nil {
		t.Error("publishing should stop on a store it cannot read")
	}
}

func TestAnUnreadableElementInstanceIsReported(t *testing.T) {
	sb := openInternal(t, "user-task.bpmn")
	if _, err := sb.StartCase(); err != nil {
		t.Fatalf("start case: %v", err)
	}
	tasks, err := sb.OpenTasks()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("open tasks = %+v, err %v", tasks, err)
	}
	jv, ok, err := sb.store.GetJob(tasks[0].JobKey)
	if err != nil || !ok {
		t.Fatalf("read job: ok=%v err=%v", ok, err)
	}
	if err := sb.store.InjectCorruptElementInstance(jv.ElementInstanceKey); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, err := sb.OpenTasks(); err == nil {
		t.Error("a task whose element instance cannot be decoded should be reported")
	}
	if _, err := sb.Run(DefaultBudget()); err == nil {
		t.Error("a run should stop when it cannot resolve the element a job sits on")
	}
}

func TestAnUnreadableIncidentIsReported(t *testing.T) {
	sb := openInternal(t, "user-task.bpmn")
	key, err := sb.StartCase()
	if err != nil {
		t.Fatalf("start case: %v", err)
	}
	tasks, err := sb.OpenTasks()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("open tasks = %+v, err %v", tasks, err)
	}
	jv, _, err := sb.store.GetJob(tasks[0].JobKey)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if err := sb.store.InjectCorruptIncident(jv.ElementInstanceKey); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, err := sb.Case(key); err == nil {
		t.Error("a case whose incidents cannot be decoded should be reported")
	}
}

// A failing stub with no message of its own still names the element it failed on:
// an anonymous incident tells the author nothing.
func TestASimulatedFailureIsNeverAnonymous(t *testing.T) {
	xml, err := os.ReadFile(filepath.Join("testdata", "service-task.bpmn"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sb, err := Open(Options{
		ModelXML: xml, BaseDir: t.TempDir(),
		Stubs: StubSet{Default: &Stub{FailPerMillion: 1_000_000}},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Close() })

	key, err := sb.StartCase()
	if err != nil {
		t.Fatalf("start case: %v", err)
	}
	if _, err := sb.Run(DefaultBudget()); err != nil {
		t.Fatalf("run: %v", err)
	}
	found := false
	if err := sb.store.Incidents(func(_ uint64, v *model.IncidentValue) error {
		if v.ProcessInstanceKey == key && strings.Contains(v.Message, "charge") {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("incidents: %v", err)
	}
	if !found {
		t.Error("a stub failure with no message of its own should name its element")
	}
}

// The scan for a new case's key walks both the live and the finished instances, so
// an unreadable record on either side has to stop it rather than silently making
// the newest case look like an older one.
func TestAnUnreadableFinishedCaseStopsTheScanForANewOne(t *testing.T) {
	sb := openInternal(t, "sequence.bpmn") // self-completing: the case is finished at once
	key, err := sb.StartCase()
	if err != nil {
		t.Fatalf("start case: %v", err)
	}
	if err := sb.store.InjectCorruptProcessInstance(key); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, err := sb.StartCase(); err == nil {
		t.Error("the scan over finished instances should report a record it cannot read")
	}
}

func TestAnUnreadableLiveCaseStopsTheScanForANewOne(t *testing.T) {
	// A model that parks on a timer holds a live instance and creates no job, so the
	// settle before the scan stays clean and the scan itself is what fails.
	sb := openInternal(t, "timer-catch.bpmn")
	key, err := sb.StartCase()
	if err != nil {
		t.Fatalf("start case: %v", err)
	}
	if err := sb.store.InjectCorruptProcessInstance(key); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, err := sb.StartCase(); err == nil {
		t.Error("the scan over live instances should report a record it cannot read")
	}
}
