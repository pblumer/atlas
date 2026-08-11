package conformance

import (
	"strings"
	"testing"
	"time"
)

// TestDriverRejectsUnknownElement proves a mis-authored Complete step fails loudly
// (naming the step and the whole driver) instead of silently driving the wrong
// token or hanging. It also exercises the jobForElement no-match path.
func TestDriverRejectsUnknownElement(t *testing.T) {
	model, err := scenarioByName(t, "user-task").load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = Run(t.TempDir(), model, []Step{Complete("does-not-exist")})
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
