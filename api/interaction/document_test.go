package interaction

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// mixedBPMN is the ordinary case (ADR-0559, amendment of 2026-11-25): the order
// process modelled here, shipping as a reference to a process deployed elsewhere,
// the customer as an external party, and a pool somebody dragged in and left empty.
const mixedBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <collaboration id="C">
    <participant id="P_order" name="Bestellung" processRef="order"/>
    <participant id="P_ship" name="Versand" atlas:processId="shipping"/>
    <participant id="P_customer" name="Kunde"/>
    <participant id="P_new" name="Neu" processRef="process_new"/>
    <messageFlow id="F_release" sourceRef="release" targetRef="P_ship"/>
    <messageFlow id="F_shipped" sourceRef="P_ship" targetRef="wait" messageRef="Msg_shipped"/>
    <messageFlow id="F_order" name="Bestellung" sourceRef="P_customer" targetRef="P_order"/>
    <messageFlow id="F_bill" sourceRef="P_order" targetRef="P_ship" messageRef="Msg_bill"/>
  </collaboration>
  <message id="Msg_release" name="OrderReleased"/>
  <message id="Msg_shipped" name="Shipped"/>
  <message id="Msg_bill" name="Bill"/>
  <process id="order" isExecutable="true">
    <startEvent id="start"/>
    <subProcess id="sub">
      <intermediateThrowEvent id="release"><messageEventDefinition messageRef="Msg_release"/></intermediateThrowEvent>
    </subProcess>
    <receiveTask id="wait" messageRef="Msg_shipped"/>
    <endEvent id="end"/>
  </process>
  <process id="process_new" isExecutable="true">
    <documentation>nothing drawn yet</documentation>
  </process>
</definitions>`

func poolKinds(d Document) map[string]string {
	out := map[string]string{}
	for _, p := range d.Pools {
		out[p.ID] = p.Kind + ":" + p.ProcessID
	}
	return out
}

// TestAPoolsKindIsReadFromItsContent: modelled here when its process holds flow
// elements, a reference when it names a process and holds none, an external party
// otherwise — also when bpmn-js gave it an empty process.
func TestAPoolsKindIsReadFromItsContent(t *testing.T) {
	d, err := ParseDocument([]byte(mixedBPMN))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	want := map[string]string{
		"P_order":    "modelled:order",
		"P_ship":     "reference:shipping",
		"P_customer": "external:",
		"P_new":      "external:",
	}
	if got := poolKinds(d); !reflect.DeepEqual(got, want) {
		t.Errorf("pools = %v, want %v", got, want)
	}
	if !d.Collaboration {
		t.Error("a file with four participants is not listed as a collaboration")
	}
	// A flow is between pools, wherever it docks, and is named by its messageRef, else
	// by the element it docks on.
	flows := map[string]Flow{}
	for _, f := range d.Flows {
		flows[f.ID] = f
	}
	for id, want := range map[string]Flow{
		"F_release": {ID: "F_release", From: "P_order", To: "P_ship", Message: "OrderReleased"},
		"F_shipped": {ID: "F_shipped", From: "P_ship", To: "P_order", Message: "Shipped"},
		"F_order":   {ID: "F_order", From: "P_customer", To: "P_order", Name: "Bestellung"},
		"F_bill":    {ID: "F_bill", From: "P_order", To: "P_ship", Message: "Bill"},
	} {
		if flows[id] != want {
			t.Errorf("flow %s = %+v, want %+v", id, flows[id], want)
		}
	}
}

// TestAFileWithoutPoolsIsAProcess: no collaboration, or one pool around the
// process, is a process; a pool that both names a process and holds flow elements
// is modelled here.
func TestAFileWithoutPoolsIsAProcess(t *testing.T) {
	plain, err := ParseDocument([]byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p"><startEvent id="s"/></process></definitions>`))
	if err != nil || plain.Collaboration || len(plain.Pools) != 0 || len(plain.Flows) != 0 {
		t.Errorf("a plain process = %+v, %v", plain, err)
	}
	one, err := ParseDocument([]byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <collaboration id="C"><participant id="P" processRef="p" atlas:processId="elsewhere"/></collaboration>
  <process id="p"><task id="t"/></process></definitions>`))
	if err != nil || one.Collaboration || len(one.Pools) != 1 || one.Pools[0].Kind != PoolModelled || one.Pools[0].ProcessID != "p" {
		t.Errorf("one pool around a process = %+v, %v", one, err)
	}
	if _, err := ParseDocument([]byte(`<definitions><process`)); err == nil {
		t.Error("broken XML parsed")
	}
}

func rulesOf(r Report) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Rule+" "+f.Pool+f.Flow)
	}
	sort.Strings(out)
	return out
}

// The deployed side of mixedBPMN: shipping is started by the release and reports
// back; an archive process also waits for the release.
func deployedForMixed() []Definition {
	return []Definition{
		{Key: 10, ProcessID: "shipping", Name: "Versand", Version: 3, Newest: true, Points: []Point{
			{Element: "s", Message: "OrderReleased", Role: "start"},
			{Element: "report", Message: "Shipped", Role: "throw"},
			{Element: "status", Message: "Status", Role: "throw"},
		}},
		{Key: 20, ProcessID: "archive", Name: "Archiv", Version: 1, Newest: true, Points: []Point{
			{Element: "a", Message: "OrderReleased", Role: "start"},
		}},
		{Key: 21, ProcessID: "old-archive", Name: "Altarchiv", Version: 1, Newest: true, Inactive: true, Points: []Point{
			{Element: "a", Message: "OrderReleased", Role: "start"},
		}},
	}
}

// TestTheFindingsCompareTheDrawingWithWhatIsDeployed: the reference resolves to the
// newest deployed version; the bill nobody waits for, the release the archive also
// receives and the unnamed flow from the customer are each said once; the flows that
// match say nothing.
func TestTheFindingsCompareTheDrawingWithWhatIsDeployed(t *testing.T) {
	d, err := ParseDocument([]byte(mixedBPMN))
	if err != nil {
		t.Fatal(err)
	}
	r := Check(d, deployedForMixed())

	var ship PoolView
	for _, p := range r.Pools {
		if p.ID == "P_ship" {
			ship = p
		}
	}
	if ship.Resolved == nil || ship.Resolved.DefinitionKey != 10 || ship.Resolved.Version != 3 || ship.Resolved.Name != "Versand" {
		t.Errorf("shipping resolves to %+v, want the newest deployed version", ship.Resolved)
	}
	want := []string{
		"collaboration.flow-not-received F_bill",
		"collaboration.flow-not-sent F_bill",
		"collaboration.flow-unnamed F_order",
		"collaboration.reaches-outside F_release",
	}
	if got := rulesOf(r); !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
	for _, f := range r.Findings {
		if f.Rule == FindingReachesOutside && (!strings.Contains(f.Message, "Archiv") || strings.Contains(f.Message, "Altarchiv")) {
			t.Errorf("reaches-outside names %q, want the active archive alone", f.Message)
		}
		if f.Message == "" {
			t.Errorf("finding %s says nothing", f.Rule)
		}
	}
}

// TestAReferenceNothingDeploysAndAnExchangeNobodyDrew: a reference to a process
// that is not deployed is said, and its flows are not checked against nothing; two
// white boxes that could exchange a name no flow shows are told so.
func TestAReferenceNothingDeploysAndAnExchangeNobodyDrew(t *testing.T) {
	d, err := ParseDocument([]byte(strings.ReplaceAll(mixedBPMN, `atlas:processId="shipping"`, `atlas:processId="nowhere"`)))
	if err != nil {
		t.Fatal(err)
	}
	r := Check(d, deployedForMixed())
	if got := rulesOf(r); !reflect.DeepEqual(got, []string{
		"collaboration.flow-not-sent F_bill", // the order, modelled here, sends no Bill
		"collaboration.flow-unnamed F_order",
		"collaboration.participant-unresolved P_ship",
		"collaboration.reaches-outside F_release",
	}) {
		t.Errorf("findings with an unresolved reference = %v", got)
	}

	// Without the two flows between order and shipping, both exchanges are undrawn.
	bare := mixedBPMN
	for _, id := range []string{"F_release", "F_shipped", "F_bill"} {
		i := strings.Index(bare, `<messageFlow id="`+id)
		j := i + strings.Index(bare[i:], "/>") + 2
		bare = bare[:i] + bare[j:]
	}
	d, err = ParseDocument([]byte(bare))
	if err != nil {
		t.Fatal(err)
	}
	var undrawn []string
	for _, f := range Check(d, deployedForMixed()).Findings {
		if f.Rule == FindingUndrawnExchange {
			undrawn = append(undrawn, f.Message)
		}
	}
	sort.Strings(undrawn)
	if len(undrawn) != 2 || !strings.Contains(undrawn[0], "OrderReleased") || !strings.Contains(undrawn[1], "Shipped") {
		t.Errorf("undrawn exchanges = %v, want the release and the report", undrawn)
	}
}

// TestAPlainProcessHasNoFindings.
func TestAPlainProcessHasNoFindings(t *testing.T) {
	d, _ := ParseDocument([]byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p"><startEvent id="s"/></process></definitions>`))
	if r := Check(d, deployedForMixed()); r.Collaboration || len(r.Findings) != 0 || len(r.Pools) != 0 {
		t.Errorf("report of a plain process = %+v", r)
	}
}

// TestEveryMessageEventOfAModelledPoolIsRead: a message start, a boundary, a catch
// and a send task are each a point of the pool; an event nobody gave a message yet is
// none; a flow that docks on a waiting element is named by it; a flow whose end is
// nowhere in the file is left out; and a pool or a process without a name is said by
// its id.
func TestEveryMessageEventOfAModelledPoolIsRead(t *testing.T) {
	d, err := ParseDocument([]byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <collaboration id="C">
    <participant id="P_a" processRef="a"/>
    <participant id="P_b" atlas:processId="b"/>
    <messageFlow id="F_in" sourceRef="P_b" targetRef="catch"/>
    <messageFlow id="F_lost" sourceRef="P_b" targetRef="gone"/>
    <messageFlow id="F_out" sourceRef="send" targetRef="P_b"/>
  </collaboration>
  <message id="M1" name="One"/><message id="M2" name="Two"/><message id="M3" name="Three"/><message id="M4" name="Four"/>
  <process id="a">
    <startEvent id="s"><messageEventDefinition messageRef="M1"/></startEvent>
    <task id="t"/>
    <boundaryEvent id="b" attachedToRef="t"><messageEventDefinition messageRef="M2"/></boundaryEvent>
    <intermediateCatchEvent id="catch"><messageEventDefinition messageRef="M3"/></intermediateCatchEvent>
    <intermediateCatchEvent id="unset"><messageEventDefinition/></intermediateCatchEvent>
    <sendTask id="send" messageRef="M4"/>
  </process>
</definitions>`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, pt := range d.points["P_a"] {
		got[pt.Element] = pt.Role + " " + pt.Message
	}
	want := map[string]string{"s": "start One", "b": "boundary Two", "catch": "catch Three", "send": "throw Four"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("points = %v, want %v", got, want)
	}
	if len(d.Flows) != 2 || d.Flows[0].Message != "Three" || d.Flows[1].Message != "Four" {
		t.Errorf("flows = %+v, want the two whose ends are in the file, named by the elements they dock on", d.Flows)
	}

	r := Check(d, []Definition{
		{Key: 1, ProcessID: "b", Version: 1, Newest: true},
		{Key: 2, ProcessID: "c", Version: 1, Newest: true, Points: []Point{{Element: "x", Message: "Four", Role: "catch"}}},
	})
	var texts []string
	for _, f := range r.Findings {
		texts = append(texts, f.Rule+": "+f.Message)
	}
	sort.Strings(texts)
	wantTexts := []string{
		"collaboration.flow-not-received: P_b waits for no message Four.",
		"collaboration.flow-not-sent: P_b sends no message Three.",
		"collaboration.reaches-outside: Four is also received outside this collaboration: c.",
	}
	if !reflect.DeepEqual(texts, wantTexts) {
		t.Errorf("findings = %q, want %q", texts, wantTexts)
	}
	if empty := Check(Document{}, nil); empty.Flows == nil || empty.Pools == nil || empty.Findings == nil {
		t.Errorf("the report of nothing = %+v, want empty lists, not null", empty)
	}
}
