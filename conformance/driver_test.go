package conformance

import (
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// TestDriverRejectsUnknownElement proves a mis-authored Complete step fails loudly
// (naming the step and the whole driver) instead of silently driving the wrong
// token or hanging. It also exercises the jobForElement no-match path.
func TestDriverRejectsUnknownElement(t *testing.T) {
	model, err := scenarioByName(t, "user-task").load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = Run(t.TempDir(), model, Start{}, []Step{Complete("does-not-exist")})
	if err == nil {
		t.Fatal("driver accepted a Complete for an unknown element; want an error")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error should name the offending element: %v", err)
	}
}

// TestStepDescribeAndList covers the human-readable renderings used in driver
// error messages, across every step kind.
func TestStepDescribeAndList(t *testing.T) {
	cases := []struct {
		step Step
		want string
	}{
		{Complete("approve"), "complete approve"},
		{Publish("paid", "K"), "publish paid/K"},
		{Wait(time.Second), "wait 1s"},
		{Fail("risky", "boom"), "fail risky"},
		{Resolve("risky"), "resolve risky"},
	}
	for _, c := range cases {
		if got := c.step.describe(); got != c.want {
			t.Errorf("describe() = %q, want %q", got, c.want)
		}
	}
	if got := stepList(nil); got != "(none)" {
		t.Errorf("stepList(nil) = %q, want (none)", got)
	}
	if got := stepList([]Step{Complete("a"), Wait(time.Second)}); got != "complete a, wait 1s" {
		t.Errorf("stepList = %q", got)
	}
	if got := (Step{kind: stepKind(99)}).describe(); got != "unknown" {
		t.Errorf("describe(unknown) = %q, want unknown", got)
	}
}

// TestDriverResolveWithoutIncident proves a Resolve step is self-verifying: with
// no incident raised it errors rather than silently no-op'ing, so a scenario can't
// claim to exercise the incident path without one.
func TestDriverResolveWithoutIncident(t *testing.T) {
	model, err := scenarioByName(t, "incident").load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = Run(t.TempDir(), model, Start{}, []Step{Resolve("risky")})
	if err == nil {
		t.Fatal("Resolve without an incident should error")
	}
	if !strings.Contains(err.Error(), "no incident") {
		t.Errorf("error should explain the missing incident: %v", err)
	}
}

// TestBeginUnknownStartKind guards the begin switch's default.
func TestBeginUnknownStartKind(t *testing.T) {
	d := &driver{}
	if err := d.begin(Start{kind: startKind(99)}); err == nil {
		t.Fatal("begin accepted an unknown start kind; want an error")
	}
}

// TestSingleInstanceEmpty proves an empty store yields a clear "nothing was
// created" error rather than a silently empty trace.
func TestSingleInstanceEmpty(t *testing.T) {
	log, store, err := openStores(t.TempDir())
	if err != nil {
		t.Fatalf("openStores: %v", err)
	}
	defer closeStores(log, store)
	if _, _, err := singleInstance(store); err == nil {
		t.Fatal("singleInstance on an empty store returned no error")
	}
}

// TestSingleInstanceRejectsMany proves the single-instance assumption is enforced:
// two instances in one store must be an error, not a silent pick of one.
func TestSingleInstanceRejectsMany(t *testing.T) {
	cp := mustCompile(t, scenarioByName(t, "sequence"))
	log, store, err := openStores(t.TempDir())
	if err != nil {
		t.Fatalf("openStores: %v", err)
	}
	defer closeStores(log, store)
	p := engine.New(1, log, store, &driverClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, _, err := singleInstance(store); err == nil {
		t.Fatal("singleInstance accepted multiple instances; want an error")
	}
}

// TestSingleInstanceSurfacesCorruption proves a store read error propagates rather
// than being swallowed: an undecodable instance record makes the enumeration fail.
func TestSingleInstanceSurfacesCorruption(t *testing.T) {
	cp := mustCompile(t, scenarioByName(t, "user-task"))
	log, store, err := openStores(t.TempDir())
	if err != nil {
		t.Fatalf("openStores: %v", err)
	}
	defer closeStores(log, store)
	p := engine.New(1, log, store, &driverClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("run: %v", err)
	}
	// The instance parks on the user task, so it lives in the active-instance
	// column family; corrupt its record there.
	if err := store.InjectCorruptProcessInstance(model.NewKey(1, 1)); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, _, err := singleInstance(store); err == nil {
		t.Error("singleInstance ignored a corrupt active-instance record")
	}
}

// TestCaptureSurfacesCorruption proves capture propagates a store read error
// (through singleInstance) rather than returning an empty trace.
func TestCaptureSurfacesCorruption(t *testing.T) {
	cp := mustCompile(t, scenarioByName(t, "user-task"))
	log, store, err := openStores(t.TempDir())
	if err != nil {
		t.Fatalf("openStores: %v", err)
	}
	defer closeStores(log, store)
	p := engine.New(1, log, store, &driverClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := store.InjectCorruptProcessInstance(model.NewKey(1, 1)); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, err := capture(store, cp); err == nil {
		t.Error("capture ignored a corrupt instance record")
	}
}

// TestJobForElementSurfacesCorruption proves the job→element resolution surfaces a
// decode error on the element-instance read rather than silently missing the job.
func TestJobForElementSurfacesCorruption(t *testing.T) {
	cp := mustCompile(t, scenarioByName(t, "user-task"))
	log, store, err := openStores(t.TempDir())
	if err != nil {
		t.Fatalf("openStores: %v", err)
	}
	defer closeStores(log, store)
	p := engine.New(1, log, store, &driverClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Corrupt the element instance the parked job points to.
	var elKey uint64
	if err := store.AllActivatableJobs(func(k uint64) error {
		jv, _, err := store.GetJob(k)
		if err != nil {
			return err
		}
		elKey = jv.ElementInstanceKey
		return nil
	}); err != nil {
		t.Fatalf("scan jobs: %v", err)
	}
	if err := store.InjectCorruptElementInstance(elKey); err != nil {
		t.Fatalf("inject: %v", err)
	}
	d := &driver{p: p, store: store, cp: cp, clock: &driverClock{}}
	if _, err := d.jobForElement("approve"); err == nil {
		t.Error("jobForElement ignored a corrupt element-instance record")
	}
}

// TestDriverUnknownStepKind guards the apply switch's default: a step kind with no
// handler must error rather than silently do nothing.
func TestDriverUnknownStepKind(t *testing.T) {
	d := &driver{}
	if err := d.apply(Step{kind: stepKind(99)}); err == nil {
		t.Fatal("apply accepted an unknown step kind; want an error")
	}
}

func scenarioByName(t *testing.T, name string) Scenario {
	t.Helper()
	for _, sc := range Scenarios {
		if sc.Name == name {
			return sc
		}
	}
	t.Fatalf("no scenario named %q", name)
	return Scenario{}
}
