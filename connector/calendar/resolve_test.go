package calendar

import (
	"context"
	"html"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/calendar/catalog"
	"github.com/pblumer/atlas/model"
)

type fakeVarStore struct {
	vars map[uint64][]model.VariableValue
}

func (f fakeVarStore) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	for i := range f.vars[scope] {
		v := f.vars[scope][i]
		if err := fn(&v); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeVarStore) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, nil
}

func parseTask(attrs string) (*compiler.CompiledProcess, error) {
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:calendarConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	return compiler.Parse(1, 1, strings.NewReader(bpmn))
}

func compileTask(t *testing.T, attrs string) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
	t.Helper()
	cp, err := parseTask(attrs)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	return cp, cp.ConnectorTask(node.Detail)
}

func input(json string) string { return `input="` + html.EscapeString(json) + `"` }

func TestResolveEvaluatesTheInput(t *testing.T) {
	cp, d := compileTask(t, `connector="filiale" operation="find-free-slots" resultVariable="freieTermine" `+input(`{
		"calendars": ["=werkstatt"], "from": "=von", "to": "2030-03-27T00:00:00+01:00",
		"durationMinutes": "=dienstleistung.dauer", "timeZone": "Europe/Zurich",
		"openingHours": "=oeffnungszeiten", "bufferMinutes": 10, "require": "any"}`))
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{10: {
		{Name: "werkstatt", Kind: model.VarString, Text: "werkstatt@firma.ch"},
		{Name: "von", Kind: model.VarString, Text: "2030-03-25T00:00:00+01:00"},
		{Name: "dienstleistung", Kind: model.VarJSON, Text: `{"dauer": 60}`},
		{Name: "oeffnungszeiten", Kind: model.VarJSON, Text: `{"mon":[{"from":"08:00","to":"12:00"}]}`},
	}}}
	j, err := Resolve(store, cp, d, 10, 77, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	want := Job{
		Connector: "filiale", Operation: "find-free-slots", Calendars: []string{"werkstatt@firma.ch"},
		From: "2030-03-25T00:00:00+01:00", To: "2030-03-27T00:00:00+01:00", DurationMinutes: 60, BufferMinutes: 10,
		TimeZone: "Europe/Zurich", OpeningHours: map[string][]Window{"mon": {{From: "08:00", To: "12:00"}}},
		Require: "any", ResultVariable: "freieTermine", ErrorCodes: []string{},
	}
	if !reflect.DeepEqual(j, want) {
		t.Errorf("job = %#v\nwant  %#v", j, want)
	}
	// The resolved job runs: the engine's half and the worker's agree on its shape.
	reg := NewRegistry()
	reg.Register("filiale", NewMockCalendars(map[string]MockCalendarSpec{"werkstatt@firma.ch": {}}))
	out, err := RunAt(context.Background(), j, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := times(slotsOf(t, map[string]any{"free": out["freieTermine"]})); got != "25 08:00,25 09:00,25 10:00,25 11:00" {
		t.Fatalf("slots %s", got)
	}
}

func TestAReservationCarriesItsJobsCreateKey(t *testing.T) {
	cp, d := compileTask(t, `connector="filiale" operation="reserve-slot" resultVariable="buchung" `+input(`{
		"calendar": "werkstatt@firma.ch", "start": "=termin.start", "end": "=termin.end", "subject": "Veloservice"}`))
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{10: {
		{Name: "termin", Kind: model.VarJSON, Text: `{"start":"2030-03-25T09:00:00+01:00","end":"2030-03-25T10:00:00+01:00"}`},
	}}}
	j, err := Resolve(store, cp, d, 10, 77, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if j.CreateKey != CreateKey("node-a", 77) || j.Start != "2030-03-25T09:00:00+01:00" {
		t.Fatalf("job %#v", j)
	}
}

func TestAnUnsetVariableIsAnAbsentValueAndAMissingRequiredOneIsRefused(t *testing.T) {
	cp, d := compileTask(t, `connector="filiale" operation="get-booking" resultVariable="b" `+input(`{
		"calendar": "werkstatt", "bookingId": "=buchung.bookingId", "timeZone": "=zone"}`))
	_, err := Resolve(fakeVarStore{}, cp, d, 10, 1, "n")
	if err == nil || !strings.Contains(err.Error(), `needs "bookingId"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestTheCompilerRefusesWhatTheOperationDoesNotTake(t *testing.T) {
	for name, tc := range map[string]struct{ attrs, want string }{
		"no connector":       {`operation="get-booking" resultVariable="b" ` + input(`{"calendar":"c","bookingId":"x"}`), "needs a connector"},
		"unknown operation":  {`connector="c" operation="book-everything" ` + input(`{}`), "unknown operation"},
		"no input":           {`connector="c" operation="get-booking" resultVariable="b"`, "needs an input object"},
		"input not object":   {`connector="c" operation="get-booking" resultVariable="b" ` + input(`[1]`), "must be a JSON object"},
		"unknown key":        {`connector="c" operation="get-booking" resultVariable="b" ` + input(`{"calendar":"c","bookingId":"x","subjekt":"y"}`), `does not take "subjekt"`},
		"missing key":        {`connector="c" operation="reserve-slot" resultVariable="b" ` + input(`{"calendar":"c","start":"2030-03-25T09:00:00+01:00"}`), `needs "end"`},
		"no result variable": {`connector="c" operation="find-free-slots" ` + input(`{}`), "needs a resultVariable"},
		"result on cancel":   {`connector="c" operation="cancel-booking" resultVariable="x" ` + input(`{"calendar":"c","bookingId":"x"}`), "returns nothing"},
		"bad zone":           {`connector="c" operation="get-booking" resultVariable="b" ` + input(`{"calendar":"c","bookingId":"x","timeZone":"Bern"}`), "IANA"},
		"bad instant":        {`connector="c" operation="reserve-slot" resultVariable="b" ` + input(`{"calendar":"c","start":"morgen","end":"2030-03-25T10:00:00+01:00"}`), "RFC 3339"},
		"bad duration": {`connector="c" operation="find-free-slots" resultVariable="b" ` + input(`{"calendars":["c"],"from":"=a","to":"=b",`+
			`"durationMinutes":2,"timeZone":"Europe/Zurich","openingHours":"=o"}`), "durationMinutes"},
		"bad weekday": {`connector="c" operation="find-free-slots" resultVariable="b" ` + input(`{"calendars":["c"],"from":"=a","to":"=b",`+
			`"durationMinutes":30,"timeZone":"Europe/Zurich","openingHours":{"montag":[]}}`), "montag"},
	} {
		_, err := parseTask(tc.attrs)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

func TestResolveReadsTheWorkerAndEveryVariableKind(t *testing.T) {
	cp, d := compileTask(t, `connector="=konto" operation="find-free-slots" resultVariable="frei" `+input(`{
		"calendars": ["=raum", "=string(processInstanceKey)"], "from": "=von", "to": "2030-03-27T00:00:00+01:00",
		"durationMinutes": "=dauer", "timeZone": "Europe/Zurich", "openingHours": "=oeffnung",
		"require": "=if alle then \"all\" else \"any\"", "maxResults": "=dauer / 2"}`))
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{10: {
		{Name: "konto", Kind: model.VarString, Text: "filiale"},
		{Name: "raum", Kind: model.VarString, Text: "raum1"},
		{Name: "von", Kind: model.VarString, Text: "2030-03-25T00:00:00+01:00"},
		{Name: "dauer", Kind: model.VarNumber, Text: "60"},
		{Name: "alle", Kind: model.VarBool, Bool: true},
		{Name: "oeffnung", Kind: model.VarJSON, Text: `{"mon":[{"from":"08:00","to":"12:00"}]}`},
		{Name: "leer", Kind: model.VarNull},
	}}}
	j, err := Resolve(store, cp, d, 10, 1, "n")
	if err != nil {
		t.Fatal(err)
	}
	if j.Connector != "filiale" || j.Require != "all" || j.DurationMinutes != 60 || j.MaxResults != 30 ||
		len(j.Calendars) != 2 || j.Calendars[1] != "10" {
		t.Fatalf("job %#v", j)
	}
}

func TestResolveRefusesWhatCannotBecomeAJob(t *testing.T) {
	for name, tc := range map[string]struct {
		attrs string
		vars  []model.VariableValue
		want  string
	}{
		"an empty worker": {`connector="=konto" operation="get-booking" resultVariable="b" ` + input(`{"calendar":"c","bookingId":"x"}`),
			nil, "empty name"},
		"a fraction of a minute": {`connector="c" operation="find-free-slots" resultVariable="b" ` + input(`{"calendars":["c"],"from":"=a","to":"=a",`+
			`"durationMinutes":"=d","timeZone":"Europe/Zurich","openingHours":{"mon":[]}}`),
			[]model.VariableValue{{Name: "a", Kind: model.VarString, Text: "x"}, {Name: "d", Kind: model.VarNumber, Text: "7.5"}}, "whole number"},
		"a value of the wrong type": {`connector="c" operation="get-booking" resultVariable="b" ` + input(`{"calendar":"=n","bookingId":"x"}`),
			[]model.VariableValue{{Name: "n", Kind: model.VarNumber, Text: "3"}}, "wrong type"},
	} {
		cp, d := compileTask(t, tc.attrs)
		_, err := Resolve(fakeVarStore{vars: map[uint64][]model.VariableValue{10: tc.vars}}, cp, d, 10, 1, "n")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := Resolve(fakeVarStore{}, nil, nil, 10, 1, "n"); err == nil {
		t.Error("a task without detail resolved")
	}
	if _, err := decodeInput("get-booking", catalog.Ops["get-booking"], "[1]"); err == nil {
		t.Error("an input that is no object was decoded")
	}
}
