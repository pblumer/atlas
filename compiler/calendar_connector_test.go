package compiler

import (
	"html"
	"strings"
	"testing"
)

func calendarTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:calendarConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func calendarInput(json string) string { return `input="` + html.EscapeString(json) + `"` }

// A service task bearing <atlas:calendarConnector> is a calendar task: it names a Worker,
// an operation and one input object — never a provider or a credential.
func TestParseCalendarConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(calendarTaskBPMN(`connector="=konto" operation="Find-Free-Slots" retries="2" resultVariable="frei" `+
		calendarInput(`{"calendars":["werkstatt"],"from":"2030-03-25T00:00:00+01:00","to":"=bis","durationMinutes":60,`+
			`"timeZone":"Europe/Zurich","openingHours":{"mon":[{"from":"08:00","to":"12:00"}]},"require":"any","maxResults":50}`))))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.ConnectorTask(cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target).Detail)
	if d.JobType != CalendarJobTypeIndex || cp.Intern(d.JobType) != CalendarJobType {
		t.Errorf("jobType = %d %q", d.JobType, cp.Intern(d.JobType))
	}
	if d.CalendarConnector.Expr == nil || cp.Intern(d.CalendarOp) != "find-free-slots" || d.CalendarInput.Expr == nil {
		t.Errorf("detail %+v", d)
	}
	if cp.Intern(d.ResultVar) != "frei" || d.Retries != 2 || d.Method != -1 || d.Auth != -1 {
		t.Errorf("result %q retries %d method %d auth %d", cp.Intern(d.ResultVar), d.Retries, d.Method, d.Auth)
	}
}

func TestCalendarTaskRefusals(t *testing.T) {
	get := `{"calendar":"c","bookingId":"x"}`
	for name, tc := range map[string]struct{ attrs, want string }{
		"no connector":          {`operation="get-booking" resultVariable="b" ` + calendarInput(get), "needs a connector"},
		"no operation":          {`connector="c" ` + calendarInput(get), "needs an operation"},
		"unknown operation":     {`connector="c" operation="book-everything" ` + calendarInput(get), "unknown operation"},
		"no result":             {`connector="c" operation="get-booking" ` + calendarInput(get), "needs a resultVariable"},
		"result on cancel":      {`connector="c" operation="cancel-booking" resultVariable="x" ` + calendarInput(get), "returns nothing"},
		"no input":              {`connector="c" operation="get-booking" resultVariable="b"`, "needs an input object"},
		"input not JSON":        {`connector="c" operation="get-booking" resultVariable="b" ` + calendarInput(`{`), "not JSON"},
		"input not object":      {`connector="c" operation="get-booking" resultVariable="b" ` + calendarInput(`[1]`), "must be a JSON object"},
		"unknown key":           {`connector="c" operation="get-booking" resultVariable="b" ` + calendarInput(`{"calendar":"c","bookingId":"x","zone":"y"}`), `does not take "zone"`},
		"missing key":           {`connector="c" operation="get-booking" resultVariable="b" ` + calendarInput(`{"calendar":"c"}`), `needs "bookingId"`},
		"bad zone":              {`connector="c" operation="get-booking" resultVariable="b" ` + calendarInput(`{"calendar":"c","bookingId":"x","timeZone":"Bern"}`), "IANA"},
		"bad instant":           {`connector="c" operation="reserve-slot" resultVariable="b" ` + calendarInput(`{"calendar":"c","start":"morgen","end":"=e"}`), "RFC 3339"},
		"bad require":           {`connector="c" operation="find-free-slots" resultVariable="b" ` + calendarInput(`{"calendars":["c"],"from":"=a","to":"=b","durationMinutes":30,"timeZone":"UTC","openingHours":"=o","require":"most"}`), "require"},
		"duration too short":    {`connector="c" operation="find-free-slots" resultVariable="b" ` + calendarInput(`{"calendars":["c"],"from":"=a","to":"=b","durationMinutes":2,"timeZone":"UTC","openingHours":"=o"}`), "durationMinutes"},
		"too many results":      {`connector="c" operation="find-free-slots" resultVariable="b" ` + calendarInput(`{"calendars":["c"],"from":"=a","to":"=b","durationMinutes":30,"timeZone":"UTC","openingHours":"=o","maxResults":5000}`), "maxResults"},
		"unknown weekday":       {`connector="c" operation="find-free-slots" resultVariable="b" ` + calendarInput(`{"calendars":["c"],"from":"=a","to":"=b","durationMinutes":30,"timeZone":"UTC","openingHours":{"montag":[]}}`), "montag"},
		"empty expression":      {`connector="c" operation="get-booking" resultVariable="b" ` + calendarInput(`{"calendar":"=","bookingId":"x"}`), "empty =expression"},
		"bad expression":        {`connector="c" operation="get-booking" resultVariable="b" ` + calendarInput(`{"calendar":"=1 +","bookingId":"x"}`), "did not compile"},
		"bad worker expression": {`connector="=1 +" operation="get-booking" resultVariable="b" ` + calendarInput(get), "connector"},
	} {
		_, err := Parse(1, 1, strings.NewReader(calendarTaskBPMN(tc.attrs)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
