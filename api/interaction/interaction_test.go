package interaction

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The fixture: an order process in two versions that releases an order and waits for
// it to be shipped; a shipping process started by the release that reports back; an
// invoicing process that would be started too but is deactivated; and an audit
// process that has nothing to do with the order.
func fixture() []Definition {
	return []Definition{
		{Key: 1, ProcessID: "order", Name: "Bestellung (alt)", Version: 1,
			Points: []Point{{Element: "send", Message: "OrderReleased", Role: "throw"}}},
		{Key: 2, ProcessID: "order", Name: "Bestellung", Version: 2, Newest: true, ProjectID: "p-sales",
			Points: []Point{
				{Element: "send", Message: "OrderReleased", Role: "throw"},
				{Element: "wait", Message: "Shipped", Role: "catch"},
				{Element: "timeout", Message: "Shipped", Role: "boundary"},
			}},
		{Key: 10, ProcessID: "shipping", Name: "Versand", Version: 1, Newest: true, ProjectID: "p-logistics",
			Points: []Point{
				{Element: "start", Message: "OrderReleased", Role: "start"},
				{Element: "notify", Message: "Shipped", Role: "throw"},
			}},
		{Key: 20, ProcessID: "invoice", Name: "Rechnung", Version: 1, Newest: true, Inactive: true,
			Points: []Point{{Element: "start", Message: "OrderReleased", Role: "start"}}},
		{Key: 30, ProcessID: "audit", Name: "Audit", Version: 1, Newest: true,
			Points: []Point{{Element: "start", Message: "Other", Role: "start"}, {Element: "out", Message: "Other", Role: "throw"}}},
	}
}

func sources() []Source {
	return []Source{
		{Kind: KindInboundWatch, Message: "Shipped", Enabled: true, ConnectorID: "c1", ConnectorName: "Lager-Jira", WorkerKind: "jira"},
		{Kind: KindInboundWatch, Message: "Shipped", Enabled: false, ConnectorID: "c2", ConnectorName: "Alt"},
		{Kind: KindProductAction, Message: "Shipped", Enabled: true, ProductID: "prod", ProductName: "Lieferung", Action: "ship"},
		{Kind: KindProductAction, Message: "Unrelated", Enabled: true, ProductID: "other", Action: "x"},
	}
}

var t0 = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixNano()

func at(minutes int) int64 { return t0 + int64(minutes)*int64(time.Minute) }

func send(pos, cause uint64, when int64, d model.DispatchValue) state.EventLogEntry {
	d.Channel = model.DispatchMessage
	return state.EventLogEntry{Position: pos, Cause: cause, At: when, Kind: state.EventLogDispatched, Dispatch: &d}
}

func recv(pos, cause uint64, when int64, f model.MessageFlowValue) state.EventLogEntry {
	return state.EventLogEntry{Position: pos, Cause: cause, At: when, Kind: state.EventLogMessageDelivered, Flow: &f}
}

// scanOf walks rows as the event log does: newest first.
func scanOf(rows ...state.EventLogEntry) func(func(state.EventLogEntry) error) error {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Position > rows[j].Position })
	return func(fn func(state.EventLogEntry) error) error {
		for _, r := range rows {
			if err := fn(r); err != nil {
				return err
			}
		}
		return nil
	}
}

func exchangesOf(g Graph) map[string]Exchange {
	out := map[string]Exchange{}
	for _, e := range g.Exchanges {
		out[e.From+" -> "+e.To+" : "+e.Message] = e
	}
	return out
}

func idsOf(g Graph) []string {
	out := make([]string, len(g.Participants))
	for i, p := range g.Participants {
		out[i] = p.ID
	}
	return out
}

func build(t *testing.T, in Input) Graph {
	t.Helper()
	if in.ProcessID == "" {
		in.ProcessID = "order"
	}
	if in.Definitions == nil {
		in.Definitions = fixture()
	}
	if in.ScanLimit == 0 {
		in.ScanLimit = 1000
	}
	if in.Now == 0 {
		in.Now = at(60)
	}
	g, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

// TestThePossibleLayerIsReadFromTheNewestActiveDefinitions: around the order
// process, the shipping process is possible both ways, the enabled watch and the
// product action may send what the order waits for, and the deactivated invoicing
// process, the disabled watch and the audit process are not part of it.
func TestThePossibleLayerIsReadFromTheNewestActiveDefinitions(t *testing.T) {
	g := build(t, Input{Sources: sources()})

	if got, want := idsOf(g), []string{"process:order", "process:shipping", "inbound-watch:c1", "product-action:prod:ship"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("participants = %v, want %v", got, want)
	}
	center := g.Participants[0]
	if !center.Center || center.Name != "Bestellung" || center.Version != 2 || center.DefinitionKey != 2 || center.ProjectID != "p-sales" {
		t.Errorf("center = %+v, want the newest version of order, marked as the center", center)
	}
	if w := g.Participants[2]; w.Kind != KindInboundWatch || w.Name != "Lager-Jira" || w.WorkerKind != "jira" {
		t.Errorf("watch = %+v", w)
	}

	ex := exchangesOf(g)
	for _, k := range []string{
		"process:order -> process:shipping : OrderReleased",
		"process:shipping -> process:order : Shipped",
		"inbound-watch:c1 -> process:order : Shipped",
		"product-action:prod:ship -> process:order : Shipped",
	} {
		e, ok := ex[k]
		if !ok || !e.Possible || e.Observed != nil {
			t.Errorf("%s = %+v (present %v), want possible and not observed", k, e, ok)
		}
	}
	if len(ex) != 4 {
		t.Errorf("exchanges = %v, want the four above", ex)
	}

	want := []Port{
		{Direction: "out", Message: "OrderReleased", Elements: []string{"send"}, Roles: []string{"throw"}},
		{Direction: "in", Message: "Shipped", Elements: []string{"wait", "timeout"}, Roles: []string{"catch", "boundary"}},
	}
	if !reflect.DeepEqual(g.Ports, want) {
		t.Errorf("ports = %+v, want %+v", g.Ports, want)
	}
	if g.Observed.Rows != 0 || g.Observed.Partial || g.Observed.ReadFrom != g.Observed.From {
		t.Errorf("observed window without a log = %+v", g.Observed)
	}
}

func TestAProcessWithNoReadableDefinitionIsUnknown(t *testing.T) {
	_, err := Build(Input{ProcessID: "nowhere", Definitions: fixture()})
	if !errors.Is(err, ErrUnknownProcess) {
		t.Fatalf("Build(nowhere) = %v, want ErrUnknownProcess", err)
	}
	// A process whose only definitions are not the newest of anything is unknown
	// too: Build reads the newest version, and there is none to read.
	_, err = Build(Input{ProcessID: "order", Definitions: fixture()[:1]})
	if !errors.Is(err, ErrUnknownProcess) {
		t.Fatalf("Build with no newest version = %v, want ErrUnknownProcess", err)
	}
}

// logRows is one window of the event log around the order process.
func logRows() []state.EventLogEntry {
	return []state.EventLogEntry{
		// The old version of order released an order, which started a shipping instance.
		send(101, 100, at(50), model.DispatchValue{Source: model.DispatchFromProcess, Name: "OrderReleased",
			SenderProcessInstanceKey: 501, SenderProcessDefKey: 1, Started: 1}),
		recv(100, 100, at(50), model.MessageFlowValue{SenderProcessInstanceKey: 501, ReceiverProcessDefKey: 10,
			MessageName: "OrderReleased"}),
		// The newest version released one that nothing received.
		send(90, 90, at(40), model.DispatchValue{Source: model.DispatchFromProcess, Name: "OrderReleased",
			SenderProcessInstanceKey: 502, SenderProcessDefKey: 2}),
		// A publish through the API reached a waiting order.
		send(81, 80, at(30), model.DispatchValue{Source: model.DispatchFromPublish, Name: "Shipped", Reached: 1}),
		recv(80, 80, at(30), model.MessageFlowValue{ReceiverProcessInstanceKey: 502, ReceiverProcessDefKey: 2,
			MessageName: "Shipped"}),
		// The audit process talked to itself, which is not the order's business.
		send(71, 70, at(20), model.DispatchValue{Source: model.DispatchFromProcess, Name: "Other",
			SenderProcessInstanceKey: 600, SenderProcessDefKey: 30, Started: 1}),
		recv(70, 70, at(20), model.MessageFlowValue{SenderProcessInstanceKey: 600, ReceiverProcessDefKey: 30,
			MessageName: "Other"}),
		// A kept message was taken by an order that started waiting later: no send in
		// that command, and no sending instance.
		{Position: 61, Cause: 60, At: at(10), Kind: state.EventLogMessageTaken, Message: &model.MessageValue{MessageName: "Shipped"}},
		recv(60, 60, at(10), model.MessageFlowValue{ReceiverProcessInstanceKey: 503, ReceiverProcessDefKey: 2,
			MessageName: "Shipped"}),
		// Before the window.
		send(5, 5, at(-10), model.DispatchValue{Source: model.DispatchFromProcess, Name: "OrderReleased",
			SenderProcessInstanceKey: 504, SenderProcessDefKey: 2}),
	}
}

// TestTheObservedLayerIsWhatTheLogShows: an operator reads every row. The old
// version's release counts on the order process, the send that reached nobody is
// listed as unreceived, the publish and the kept message both arrive from the API,
// the audit traffic stays out, and the row before the window is not read.
func TestTheObservedLayerIsWhatTheLogShows(t *testing.T) {
	g := build(t, Input{Scan: scanOf(logRows()...), ReadsAll: true, From: at(0)})

	ex := exchangesOf(g)
	release := ex["process:order -> process:shipping : OrderReleased"]
	if !release.Possible || release.Observed == nil || release.Observed.Received != 1 || release.Observed.Started != 1 ||
		release.Observed.LastAt != stamp(at(50)) {
		t.Errorf("release = %+v (%+v), want possible and observed once, starting an instance", release, release.Observed)
	}
	api := ex["publish -> process:order : Shipped"]
	if api.Possible || api.Observed == nil || api.Observed.Received != 2 || api.Observed.Started != 0 ||
		api.Observed.LastAt != stamp(at(30)) {
		t.Errorf("publish = %+v (%+v), want observed twice and never possible", api, api.Observed)
	}
	if _, ok := ex["process:audit -> process:audit : Other"]; ok {
		t.Error("the audit process's own traffic is in the order's graph")
	}
	for _, p := range g.Participants {
		if p.ID == "process:audit" {
			t.Error("the audit process is a participant of the order's graph")
		}
	}
	if want := []Unreceived{{From: "process:order", Message: "OrderReleased", Count: 1, LastAt: stamp(at(40))}}; !reflect.DeepEqual(g.Unreceived, want) {
		t.Errorf("unreceived = %+v, want %+v", g.Unreceived, want)
	}
	if g.Observed.Rows != 9 || g.Observed.Partial || g.Observed.ReadFrom != stamp(at(0)) || g.Observed.To != stamp(at(60)) {
		t.Errorf("window = %+v, want nine rows read, complete back to the window's start", g.Observed)
	}
}

// TestAReaderSeesOnlyTheirOwnRows: a member of the order's project reads its rows
// and not the shipping process's. The release that started a shipping instance was
// received outside their view, and so was the publish that reached the order — they
// may not read the publish row, so they do not learn that it came from the API.
func TestAReaderSeesOnlyTheirOwnRows(t *testing.T) {
	visible := func(key uint64) bool { return key == 1 || key == 2 }
	g := build(t, Input{Scan: scanOf(logRows()...), Visible: visible, From: at(0)})

	ex := exchangesOf(g)
	if e := ex["process:order -> process:shipping : OrderReleased"]; !e.Possible || e.Observed != nil {
		t.Errorf("release to shipping = %+v, want possible, and nothing the member may read observed on it", e)
	}
	out := ex["process:order -> outside-view : OrderReleased"]
	if out.Observed == nil || out.Observed.Received != 1 || out.Observed.Started != 1 || out.Possible {
		t.Errorf("release outside the view = %+v (%+v), want one arrival, an instance started", out, out.Observed)
	}
	in := ex["outside-view -> process:order : Shipped"]
	if in.Observed == nil || in.Observed.Received != 2 {
		t.Errorf("arrivals from outside the view = %+v (%+v), want two", in, in.Observed)
	}
	if _, ok := ex["publish -> process:order : Shipped"]; ok {
		t.Error("a member learned that a message came from the publish route")
	}
	if len(g.Unreceived) != 1 {
		t.Errorf("unreceived = %+v, want the member's own send that reached nobody", g.Unreceived)
	}
	for _, p := range g.Participants {
		if p.Kind == KindOutsideView && (p.Name != "" || p.ProcessID != "") {
			t.Errorf("the participant outside the view carries a name: %+v", p)
		}
	}
}

// TestTheScanIsBoundedAndSaysWhereItStopped: with room for three rows, the read
// stops in the middle of the window, says so, and dates how far it got.
func TestTheScanIsBoundedAndSaysWhereItStopped(t *testing.T) {
	g := build(t, Input{Scan: scanOf(logRows()...), ReadsAll: true, From: at(0), ScanLimit: 3})
	if !g.Observed.Partial || g.Observed.Rows != 3 || g.Observed.ReadFrom != stamp(at(40)) {
		t.Fatalf("window = %+v, want three rows, partial, read back to the third row's time", g.Observed)
	}
	if len(g.Unreceived) != 1 {
		t.Errorf("unreceived = %+v, want the send within the rows read", g.Unreceived)
	}
	if _, ok := exchangesOf(g)["publish -> process:order : Shipped"]; ok {
		t.Error("a row beyond the cut was folded")
	}
}

func TestAScanErrorIsReturned(t *testing.T) {
	boom := errors.New("disk")
	_, err := Build(Input{ProcessID: "order", Definitions: fixture(), ScanLimit: 10, Now: at(60),
		Scan: func(func(state.EventLogEntry) error) error { return boom }})
	if !errors.Is(err, boom) {
		t.Fatalf("Build = %v, want the scan's error", err)
	}
}

// TestSpecialSenders: a definition no longer deployed, an inbound watch and a
// directed delivery are each a participant of their own; a process that sends a name
// it also waits for exchanges it with itself.
func TestSpecialSenders(t *testing.T) {
	defs := append(fixture(), Definition{Key: 40, ProcessID: "loop", Name: "Schleife", Version: 1, Newest: true,
		Points: []Point{{Element: "out", Message: "Again", Role: "throw"}, {Element: "in", Message: "Again", Role: "catch"}}})
	rows := []state.EventLogEntry{
		send(31, 30, at(30), model.DispatchValue{Source: model.DispatchFromProcess, Name: "Shipped",
			SenderProcessInstanceKey: 9, SenderProcessDefKey: 99, Reached: 1}),
		recv(30, 30, at(30), model.MessageFlowValue{SenderProcessInstanceKey: 9, ReceiverProcessInstanceKey: 7,
			ReceiverProcessDefKey: 2, MessageName: "Shipped"}),
		send(21, 20, at(20), model.DispatchValue{Source: model.DispatchFromInbound, Name: "Shipped", Reached: 1}),
		recv(20, 20, at(20), model.MessageFlowValue{ReceiverProcessInstanceKey: 7, ReceiverProcessDefKey: 2, MessageName: "Shipped"}),
		send(11, 10, at(10), model.DispatchValue{Source: model.DispatchDirected, Name: "Shipped", Reached: 1,
			TargetProcessInstanceKey: 7, TargetProcessDefKey: 2}),
		recv(10, 10, at(10), model.MessageFlowValue{ReceiverProcessInstanceKey: 7, ReceiverProcessDefKey: 2, MessageName: "Shipped"}),
	}
	g := build(t, Input{Definitions: defs, Scan: scanOf(rows...), ReadsAll: true, From: at(0)})
	ex := exchangesOf(g)
	for _, k := range []string{
		"removed -> process:order : Shipped",
		"inbound -> process:order : Shipped",
		"directed -> process:order : Shipped",
	} {
		if e := ex[k]; e.Observed == nil || e.Observed.Received != 1 {
			t.Errorf("%s = %+v, want observed once", k, e)
		}
	}

	self := build(t, Input{ProcessID: "loop", Definitions: defs})
	if e, ok := exchangesOf(self)["process:loop -> process:loop : Again"]; !ok || !e.Possible {
		t.Errorf("loop's exchange with itself = %+v (present %v)", e, ok)
	}
	if len(self.Participants) != 1 {
		t.Errorf("loop's participants = %v, want itself alone", idsOf(self))
	}
}

// TestADeactivatedCenterKeepsItsPortsButMakesNothingPossible.
func TestADeactivatedCenterKeepsItsPortsButMakesNothingPossible(t *testing.T) {
	g := build(t, Input{ProcessID: "invoice", Sources: sources()})
	if len(g.Exchanges) != 0 {
		t.Errorf("exchanges of a deactivated process = %+v, want none possible", g.Exchanges)
	}
	if len(g.Ports) != 1 || g.Ports[0].Direction != "in" || g.Ports[0].Message != "OrderReleased" {
		t.Errorf("ports = %+v, want its start", g.Ports)
	}
	if !g.Participants[0].Inactive {
		t.Errorf("center = %+v, want it marked inactive", g.Participants[0])
	}
}

// TestTheAnswerIsStable: two builds of the same input answer the same graph, in
// the same order.
func TestTheAnswerIsStable(t *testing.T) {
	a := build(t, Input{Sources: sources(), Scan: scanOf(logRows()...), ReadsAll: true, From: at(0)})
	for i := 0; i < 20; i++ {
		b := build(t, Input{Sources: sources(), Scan: scanOf(logRows()...), ReadsAll: true, From: at(0)})
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Fatalf("build %d differs:\n%s\n%s", i, ja, jb)
		}
	}
}

// TestSeveralNamesBetweenTheSameParticipantsAreKeptApartAndOrdered: two names the
// order process sends, two it sends to nobody, a receiver that is no longer deployed
// and a source of a kind the graph does not know.
func TestSeveralNamesBetweenTheSameParticipantsAreKeptApartAndOrdered(t *testing.T) {
	defs := []Definition{
		{Key: 2, ProcessID: "order", Name: "Bestellung", Version: 1, Newest: true, Points: []Point{
			{Element: "b", Message: "Storno", Role: "throw"},
			{Element: "a", Message: "Auftrag", Role: "throw"},
			{Element: "w", Message: "Rueckfrage", Role: "catch"},
		}},
		{Key: 10, ProcessID: "shipping", Name: "Versand", Version: 1, Newest: true, Points: []Point{
			{Element: "s1", Message: "Auftrag", Role: "start"},
			{Element: "s2", Message: "Storno", Role: "catch"},
		}},
	}
	rows := []state.EventLogEntry{
		send(41, 40, at(40), model.DispatchValue{Source: model.DispatchFromProcess, Name: "Zwei", SenderProcessInstanceKey: 1, SenderProcessDefKey: 2}),
		send(31, 30, at(30), model.DispatchValue{Source: model.DispatchFromProcess, Name: "Eins", SenderProcessInstanceKey: 1, SenderProcessDefKey: 2}),
		send(21, 20, at(20), model.DispatchValue{Source: model.DispatchFromProcess, Name: "Auftrag", SenderProcessInstanceKey: 1, SenderProcessDefKey: 2, Reached: 1}),
		recv(20, 20, at(20), model.MessageFlowValue{SenderProcessInstanceKey: 1, ReceiverProcessInstanceKey: 9, ReceiverProcessDefKey: 77, MessageName: "Auftrag"}),
	}
	g := build(t, Input{Definitions: defs, Sources: []Source{{Kind: "carrier-pigeon", Message: "Rueckfrage", Enabled: true}},
		Scan: scanOf(rows...), ReadsAll: true, From: at(0)})

	var got []string
	for _, e := range g.Exchanges {
		got = append(got, e.From+" -> "+e.To+" : "+e.Message)
	}
	want := []string{
		"process:order -> process:shipping : Auftrag",
		"process:order -> process:shipping : Storno",
		"process:order -> removed : Auftrag",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("exchanges = %v, want %v, ordered by sender, receiver and name", got, want)
	}
	if len(g.Unreceived) != 2 || g.Unreceived[0].Message != "Eins" || g.Unreceived[1].Message != "Zwei" {
		t.Errorf("unreceived = %+v, want Eins before Zwei", g.Unreceived)
	}
	if len(g.Ports) != 3 || g.Ports[0].Message != "Auftrag" || g.Ports[1].Message != "Storno" || g.Ports[2].Message != "Rueckfrage" {
		t.Errorf("ports = %+v, want the two outgoing names in order, then the incoming one", g.Ports)
	}
	for _, p := range g.Participants {
		if p.Kind == "carrier-pigeon" || p.ID == "" {
			t.Errorf("a source of an unknown kind became a participant: %+v", p)
		}
	}
}
