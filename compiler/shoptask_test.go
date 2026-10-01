package compiler_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// shopModel is a process whose send task carries the given <atlas:shopTask> attributes
// — or, with service, the same extension on a service task.
func shopModel(attrs string, service bool) string {
	task := `<sendTask id="Told"><extensionElements><atlas:shopTask ` + attrs + `/></extensionElements></sendTask>`
	if service {
		task = `<serviceTask id="Told"><extensionElements><atlas:shopTask ` + attrs + `/></extensionElements></serviceTask>`
	}
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="S"/>` + task + `<endEvent id="E"/>
    <sequenceFlow id="f1" sourceRef="S" targetRef="Told"/>
    <sequenceFlow id="f2" sourceRef="Told" targetRef="E"/>
  </process>
</definitions>`
}

// TestAShopSendTaskCompilesToTheServersOwnJob: a send task that states an outcome is
// a connector task under the reserved shop job type, carrying the action and the
// ending as literals, and the process lists it as a point that answers the action.
func TestAShopSendTaskCompilesToTheServersOwnJob(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(shopModel(`mode="outcome" action="password-reset" outcome="completed" retries="2"`, false)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	idx, ok := cp.ElementIndexOf("Told")
	if !ok {
		t.Fatal("no element Told")
	}
	d, err := cp.ConnectorTaskOf(idx)
	if err != nil {
		t.Fatalf("ConnectorTaskOf: %v", err)
	}
	if d.JobType != compiler.ShopJobTypeIndex || d.ShopMode != "outcome" || d.ShopAction != "password-reset" ||
		d.ShopOutcome != "completed" || d.Retries != 2 {
		t.Fatalf("detail = %+v, want the shop job carrying the action and the ending", d)
	}
	got := cp.ShopOutcomePoints()
	if len(got) != 1 || got[0] != (compiler.ShopOutcomePoint{Element: "Told", Action: "password-reset", Outcome: "completed"}) {
		t.Fatalf("ShopOutcomePoints = %+v", got)
	}

	// The mode may be left out: stating an outcome is the only one there is.
	cp, err = compiler.Parse(1, 1, strings.NewReader(shopModel(`action="extend" outcome="failed"`, false)))
	if err != nil || len(cp.ShopOutcomePoints()) != 1 {
		t.Fatalf("without a mode: %v", err)
	}
}

// TestAShopSendTaskIsCheckedAtDeploy: an unknown mode, an action key that is not one,
// an ending outside the closed set, a bad retry budget and the extension on a service
// task are each refused with the element named.
func TestAShopSendTaskIsCheckedAtDeploy(t *testing.T) {
	for _, c := range []struct {
		attrs   string
		service bool
		want    string
	}{
		{`mode="command" action="extend" outcome="completed"`, false, `has mode "command"`},
		{`action="Extend Storage" outcome="completed"`, false, `names action "Extend Storage"`},
		{`action="" outcome="completed"`, false, `names action ""`},
		{`action="extend" outcome="done"`, false, `states outcome "done"`},
		{`action="extend" outcome="completed" retries="-1"`, false, `Told`},
		{`action="extend" outcome="completed"`, true, "a shop task is a send task"},
	} {
		_, err := compiler.Parse(1, 1, strings.NewReader(shopModel(c.attrs, c.service)))
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "Told") {
			t.Errorf("%s (service %v): %v, want a refusal naming %q", c.attrs, c.service, err, c.want)
		}
	}
}
