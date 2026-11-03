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
		{`mode="relay" action="extend" outcome="completed"`, false, `has mode "relay"`},
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

// TestAShopCommandTaskNamesWhatItCommands: a command task compiles to the shop
// command job type with the product and the action as literals and the order and the
// position as values the instance computes; it is not a point that answers an action.
// One that also states an outcome, or leaves out what it acts on, is refused.
func TestAShopCommandTaskNamesWhatItCommands(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(shopModel(
		`mode="command" product="mailbox" action="deprovision" order="= leaver.orderId" position="mailbox" resultVariable="returned"`, false)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	idx, _ := cp.ElementIndexOf("Told")
	d, err := cp.ConnectorTaskOf(idx)
	if err != nil {
		t.Fatalf("ConnectorTaskOf: %v", err)
	}
	if d.JobType != compiler.ShopCommandJobTypeIndex || d.ShopMode != compiler.ShopModeCommand ||
		d.ShopProduct != "mailbox" || d.ShopAction != "deprovision" || d.ShopResultVar != "returned" {
		t.Fatalf("detail = %+v", d)
	}
	if d.ShopOrder.Expr == nil || d.ShopPosition.Expr != nil || d.ShopPosition.Literal != "mailbox" {
		t.Fatalf("order %+v, position %+v: want an expression and a literal", d.ShopOrder, d.ShopPosition)
	}
	if got := cp.ShopOutcomePoints(); len(got) != 0 {
		t.Fatalf("a command task was listed as answering an action: %+v", got)
	}

	for _, c := range []struct{ attrs, want string }{
		{`mode="command" product="mailbox" action="reset" order="o" position="p" outcome="completed"`, "states no outcome"},
		{`mode="command" action="reset" order="o" position="p"`, "needs the product"},
		{`mode="command" product="mailbox" action="reset" position="p"`, "needs the order and the position"},
		{`mode="command" product="mailbox" action="reset" order="o"`, "needs the order and the position"},
		{`mode="command" product="mailbox" action="reset" order="= (" position="p"`, "Told"},
		{`mode="command" product="mailbox" action="reset" order="o" position="= )"`, "Told"},
		{`mode="command" product="mailbox" action="reset" order="o" position="p" retries="x"`, "Told"},
	} {
		_, err := compiler.Parse(1, 1, strings.NewReader(shopModel(c.attrs, false)))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want a refusal naming %q", c.attrs, err, c.want)
		}
	}
}

// TestAShopOrderTaskNamesWhatItOrders: an `order` task rides the command task's job
// type with its catalogue and product as literals and its recipient and answers as
// values evaluated when it runs; everything it would ignore, or cannot do without, is
// a deploy error.
func TestAShopOrderTaskNamesWhatItOrders(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(shopModel(
		`mode="order" catalog="cat-hr" product="m365-eintritt" recipient="= konto.id" answers="= antworten" resultVariable="auftrag" retries="1"`, false)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	idx, _ := cp.ElementIndexOf("Told")
	d, err := cp.ConnectorTaskOf(idx)
	if err != nil {
		t.Fatal(err)
	}
	if d.JobType != compiler.ShopCommandJobTypeIndex || d.ShopMode != compiler.ShopModeOrder || d.ShopCatalog != "cat-hr" ||
		d.ShopProduct != "m365-eintritt" || d.ShopRecipient.Expr == nil || d.ShopAnswers.Expr == nil ||
		d.ShopResultVar != "auftrag" || d.Retries != 1 {
		t.Fatalf("detail = %+v", d)
	}
	if len(cp.ShopOutcomePoints()) != 0 {
		t.Error("an order task was listed as answering an action")
	}
	if _, err := compiler.Parse(1, 1, strings.NewReader(shopModel(`mode="order" catalog="c" product="p" recipient="usr_1"`, false))); err != nil {
		t.Errorf("a literal recipient and no answers: %v", err)
	}
	for name, attrs := range map[string]string{
		"no catalogue":         `mode="order" product="p" recipient="= r"`,
		"no product":           `mode="order" catalog="c" recipient="= r"`,
		"a product with space": `mode="order" catalog="c" product="a b" recipient="= r"`,
		"no recipient":         `mode="order" catalog="c" product="p"`,
		"an action":            `mode="order" catalog="c" product="p" recipient="= r" action="provision"`,
		"an outcome":           `mode="order" catalog="c" product="p" recipient="= r" outcome="completed"`,
		"an order":             `mode="order" catalog="c" product="p" recipient="= r" order="= o"`,
		"a bad expression":     `mode="order" catalog="c" product="p" recipient="= (("`,
	} {
		if _, err := compiler.Parse(1, 1, strings.NewReader(shopModel(attrs, false))); err == nil {
			t.Errorf("%s: compiled", name)
		}
	}
}

// TestAShopCommandTaskMayNameARecipient: a command addresses either an order and a
// position or what a recipient holds — never both, and never neither.
func TestAShopCommandTaskMayNameARecipient(t *testing.T) {
	cp, err := compiler.Parse(1, 1, strings.NewReader(shopModel(
		`mode="command" product="ad-admin-konto" action="deprovision" recipient="= konto.id" resultVariable="asked"`, false)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	idx, _ := cp.ElementIndexOf("Told")
	d, err := cp.ConnectorTaskOf(idx)
	if err != nil {
		t.Fatal(err)
	}
	if d.JobType != compiler.ShopCommandJobTypeIndex || d.ShopMode != compiler.ShopModeCommand || d.ShopRecipient.Expr == nil ||
		d.ShopOrder.Expr != nil || d.ShopOrder.Literal != "" || d.ShopResultVar != "asked" {
		t.Fatalf("detail = %+v", d)
	}
	for name, attrs := range map[string]string{
		"both":    `mode="command" product="p" action="deprovision" recipient="= r" order="= o" position="p"`,
		"neither": `mode="command" product="p" action="deprovision"`,
		"bad":     `mode="command" product="p" action="deprovision" recipient="= (("`,
	} {
		if _, err := compiler.Parse(1, 1, strings.NewReader(shopModel(attrs, false))); err == nil {
			t.Errorf("%s: compiled", name)
		}
	}
}
