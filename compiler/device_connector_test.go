package compiler

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/device/catalog"
)

func deviceTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:deviceConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func deviceDetail(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(deviceTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	return cp, cp.ConnectorTask(node.Detail)
}

// A device task names a device Worker, an operation and the named line it acts on —
// never a pin: which pin a name is wired to is the device's configuration.
func TestParseDeviceConnectorTask(t *testing.T) {
	cp, d := deviceDetail(t, `connector="pi-gewaechshaus" operation="digital.write" output="relais" on="=bewaessern" retries="2"`)
	if d.JobType != DeviceJobTypeIndex || cp.Intern(d.JobType) != DeviceJobType {
		t.Errorf("jobType = %d %q", d.JobType, cp.Intern(d.JobType))
	}
	if got := cp.Intern(d.Connector); got != "pi-gewaechshaus" || d.DeviceConnector.Expr != nil {
		t.Errorf("worker = %q (expr %v)", got, d.DeviceConnector.Expr)
	}
	if got := cp.Intern(d.DeviceOp); got != catalog.OpDigitalWrite {
		t.Errorf("operation = %q", got)
	}
	if d.DeviceOutput.Literal != "relais" || d.DeviceOn.Expr == nil {
		t.Errorf("output %+v, on %+v", d.DeviceOutput, d.DeviceOn)
	}
	if d.Retries != 2 {
		t.Errorf("retries = %d", d.Retries)
	}
}

func TestADeviceTaskMayChooseItsDeviceAtRunTime(t *testing.T) {
	cp, d := deviceDetail(t, `connector="=standort.geraet" operation="pulse" output="tuer" durationMs="=dauer"`)
	if d.DeviceConnector.Expr == nil || d.DeviceDuration.Expr == nil {
		t.Fatal("a FEEL worker or duration compiled no expression")
	}
	if cp.Intern(d.Connector) != "=standort.geraet" {
		t.Errorf("the authored worker is kept as written: %q", cp.Intern(d.Connector))
	}
}

func TestEveryDeviceOperationCompilesFromItsNeeds(t *testing.T) {
	values := map[string]string{
		catalog.AttrOutput:   `output="relais"`,
		catalog.AttrLine:     `line="taster"`,
		catalog.AttrOn:       `on="true"`,
		catalog.AttrDuration: `durationMs="500"`,
	}
	for _, name := range catalog.Names() {
		op, _ := catalog.Lookup(name)
		attrs := []string{`connector="pi"`, `operation="` + name + `"`}
		for _, a := range op.Needs {
			attrs = append(attrs, values[a])
		}
		if op.Returns {
			attrs = append(attrs, `resultVariable="zustand"`)
		}
		_, d := deviceDetail(t, strings.Join(attrs, " "))
		if d.JobType != DeviceJobTypeIndex {
			t.Errorf("%s: job type %d", name, d.JobType)
		}
	}
}

func TestADeviceTaskIsRefusedWhenItCannotBeRight(t *testing.T) {
	cases := map[string]struct{ attrs, want string }{
		"no worker":           {`operation="digital.write" output="relais" on="true"`, "needs a connector"},
		"no operation":        {`connector="pi"`, "needs an operation"},
		"unknown operation":   {`connector="pi" operation="digital.toggle"`, "unknown operation"},
		"missing output":      {`connector="pi" operation="digital.write" on="true"`, "needs output"},
		"missing state":       {`connector="pi" operation="digital.write" output="relais"`, "needs on"},
		"unused line":         {`connector="pi" operation="digital.write" output="relais" on="true" line="x"`, "does not use line"},
		"unused duration":     {`connector="pi" operation="digital.write" output="relais" on="true" durationMs="5"`, "does not use durationMs"},
		"read without result": {`connector="pi" operation="digital.read" line="taster"`, "needs a resultVariable"},
		"write with result":   {`connector="pi" operation="digital.write" output="relais" on="true" resultVariable="x"`, "returns nothing"},
		"a pin, not a name":   {`connector="pi" operation="digital.write" output="17" on="true"`, "not a line name"},
		"state not boolean":   {`connector="pi" operation="digital.write" output="relais" on="yes"`, "write true or false"},
		"duration not number": {`connector="pi" operation="pulse" output="tuer" durationMs="kurz"`, "not a whole number"},
		"duration too long":   {`connector="pi" operation="pulse" output="tuer" durationMs="120000"`, "timer"},
		"empty FEEL":          {`connector="pi" operation="digital.write" output="relais" on="="`, "empty FEEL"},
		"broken FEEL":         {`connector="pi" operation="digital.write" output="relais" on="=1 +"`, "on"},
		"broken FEEL worker":  {`connector="=(" operation="digital.write" output="relais" on="true"`, "connector"},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(deviceTaskBPMN(tc.attrs)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
