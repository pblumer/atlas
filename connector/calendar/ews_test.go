package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/calendar/catalog"
	"github.com/pblumer/atlas/connector/calendar/ewsfake"
)

func ewsRegistry(t *testing.T, boxes map[string]ewsfake.Mailbox) (*Registry, *ewsfake.Server) {
	t.Helper()
	fake := ewsfake.New(boxes)
	t.Cleanup(fake.Close)
	b, err := NewBackend(fake.URL(), ewsfake.Bundle(""), Options{HTTPClient: fake.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("shop", b)
	return reg, fake
}

func TestTheEWSBundleIsReadStrictly(t *testing.T) {
	const ep = "https://mail.contoso.ch/EWS/Exchange.asmx"
	for name, tc := range map[string]struct{ endpoint, bundle, want string }{
		"no password":       {ep, `{"provider":"exchange-ews","username":"u"}`, "needs the username"},
		"no endpoint":       {"", `{"provider":"exchange-ews","username":"u","password":"p"}`, "needs an endpoint"},
		"http":              {"http://mail.contoso.ch/EWS/Exchange.asmx", `{"provider":"exchange-ews","username":"u","password":"p"}`, "not an https URL"},
		"kerberos":          {ep, `{"provider":"exchange-ews","username":"u","password":"p","authentication":"Kerberos"}`, "Kerberos alone is not supported"},
		"unknown scheme":    {ep, `{"provider":"exchange-ews","username":"u","password":"p","authentication":"digest"}`, "unknown authentication"},
		"a CalDAV field":    {ep, `{"provider":"exchange-ews","username":"u","password":"p","timeZone":"Europe/Zurich"}`, "timeZone belongs to another provider"},
		"an EWS field else": {"https://dav.example.ch/cal/", `{"provider":"caldav","username":"u","password":"p","authentication":"basic"}`, "authentication belongs to another provider"},
	} {
		if err := CheckBundle(tc.endpoint, tc.bundle); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	for _, auth := range []string{"", "ntlm", "NTLM", "basic"} {
		if err := CheckBundle(ep, ewsfake.Bundle(auth)); err != nil {
			t.Errorf("authentication %q: %v", auth, err)
		}
	}
}

func TestEWSWalksABookingFromReservationToCancellation(t *testing.T) {
	reg, fake := ewsRegistry(t, map[string]ewsfake.Mailbox{"werkstatt@firma.ch": {Busy: []ewsfake.Busy{
		{Start: at(25, 10, 0), End: at(25, 11, 0)},
		{Start: at(25, 13, 30), End: at(25, 18, 0), FreeBusy: "Free"},
		{Start: at(26, 0, 0), End: at(27, 0, 0), FreeBusy: "OOF"},
		{Start: at(18, 8, 0), End: at(18, 9, 0), Weekly: 3},
	}}})
	ctx := context.Background()
	out, err := RunAt(ctx, findJob("werkstatt@firma.ch"), reg, now)
	if err != nil {
		t.Fatal(err)
	}
	// 08:00 is taken by the weekly series' occurrence on the 25th, 10:00 by the
	// appointment, Tuesday by the out-of-office day; the afternoon marked free is offered.
	if got := times(slotsOf(t, out)); got != "25 09:00,25 11:00,25 13:30,25 14:30,25 15:30,25 16:30" {
		t.Fatalf("slots %s", got)
	}

	res := reserveJob("k1", at(25, 9, 0), at(25, 10, 0))
	res.Calendar = "werkstatt@firma.ch"
	out, err = RunAt(ctx, res, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	id := out["booking"].(map[string]any)["bookingId"].(string)
	if again, err := RunAt(ctx, res, reg, now); err != nil || again["booking"].(map[string]any)["bookingId"] != id {
		t.Fatalf("a repeated delivery: %v %v", again, err)
	}
	if fake.Requests["CreateItem"] != 1 {
		t.Fatalf("a repeated delivery created again: %d CreateItem", fake.Requests["CreateItem"])
	}
	base := Job{Connector: "shop", Calendar: "werkstatt@firma.ch", BookingID: id, TimeZone: "Europe/Zurich", ResultVariable: "b", ErrorCodes: Codes}

	confirm := base
	confirm.Operation = catalog.OpConfirmBooking
	confirm.Subject, confirm.Body = "Veloservice <Muster> & E-Bike", "Bremsen quietschen\nseit Montag"
	confirm.Attendees = []Attendee{{Email: "kundin@example.ch", Name: `Eva "Evi" Muster`}}
	if _, err := RunAt(ctx, confirm, reg, now); err != nil {
		t.Fatal(err)
	}
	var mine ewsfake.Item
	for _, it := range fake.Items("werkstatt@firma.ch") {
		if it.ID == id {
			mine = it
		}
	}
	if mine.Subject != "Veloservice <Muster> & E-Bike" || mine.Body != "Bremsen quietschen\nseit Montag" || mine.Key != "k1" ||
		len(mine.Attendees) != 1 || mine.Attendees[0] != (ewsfake.Attendee{Email: "kundin@example.ch", Name: `Eva "Evi" Muster`}) || !mine.Invited {
		t.Fatalf("confirmed item %+v", mine)
	}

	get := base
	get.Operation = catalog.OpGetBooking
	out, err = RunAt(ctx, get, reg, now)
	if err != nil || out["b"].(map[string]any)["exists"] != true || out["b"].(map[string]any)["start"] != "2030-03-25T09:00:00+01:00" {
		t.Fatalf("get: %v %v", out, err)
	}
	cancel := base
	cancel.Operation, cancel.ResultVariable = catalog.OpCancelBooking, ""
	for i := 0; i < 2; i++ {
		if _, err := RunAt(ctx, cancel, reg, now); err != nil {
			t.Fatalf("cancel %d: %v", i, err)
		}
	}
	if out, err = RunAt(ctx, get, reg, now); err != nil || out["b"].(map[string]any)["exists"] != false {
		t.Fatalf("get after cancel: %v %v", out, err)
	}
	if _, err := RunAt(ctx, confirm, reg, now); bpmnCode(err) != CodeBookingNotFound {
		t.Fatalf("confirm after cancel: %v", err)
	}
}

func TestEWSTellsTheFailuresAProcessActsOn(t *testing.T) {
	fake := ewsfake.New(map[string]ewsfake.Mailbox{
		"werkstatt@firma.ch": {},
		"lesen@firma.ch":     {ReadOnly: true},
		"ohne@firma.ch":      {SendAsDenied: true},
	})
	t.Cleanup(fake.Close)
	b, err := NewBackend(fake.URL(), ewsfake.Bundle("ntlm"), Options{HTTPClient: fake.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	from, to := at(25, 9, 0), at(25, 10, 0)
	if _, err := b.Events(ctx, "fehlt@firma.ch", from, to); kindOf(err) != KindCalendarNotFound {
		t.Errorf("events of a missing mailbox: %v", err)
	}
	if _, err := b.Events(ctx, "two words", from, to); kindOf(err) != KindCalendarNotFound {
		t.Errorf("a name that is no address: %v", err)
	}
	if _, err := b.Create(ctx, "lesen@firma.ch", NewEvent{Key: "k", Subject: "s", Start: from, End: to}); kindOf(err) != KindCalendarNotWritable {
		t.Errorf("create in a read-only calendar: %v", err)
	}
	if _, ok, err := b.Get(ctx, "werkstatt@firma.ch", "AAMkAD-gone"); ok || err != nil {
		t.Errorf("get of a missing booking: %v %v", ok, err)
	}
	if _, ok, err := b.Get(ctx, "werkstatt@firma.ch", ""); ok || err != nil {
		t.Errorf("get of an empty id: %v %v", ok, err)
	}
	if _, _, err := b.Get(ctx, "fehlt@firma.ch", "AAMkAD-gone"); kindOf(err) != KindCalendarNotFound {
		t.Errorf("get in a missing mailbox: %v", err)
	}
	if err := b.Delete(ctx, "werkstatt@firma.ch", "AAMkAD-gone"); err != nil {
		t.Errorf("delete of a missing booking: %v", err)
	}
	if err := b.Delete(ctx, "werkstatt@firma.ch", ""); err != nil {
		t.Errorf("delete of an empty id: %v", err)
	}
	if err := b.Delete(ctx, "fehlt@firma.ch", "AAMkAD-gone"); kindOf(err) != KindCalendarNotFound {
		t.Errorf("delete in a missing mailbox: %v", err)
	}
	ev, err := b.Create(ctx, "ohne@firma.ch", NewEvent{Key: "k", Subject: "s", Start: from, End: to})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Update(ctx, "ohne@firma.ch", ev.ID, Details{Subject: "x", Attendees: []Attendee{{Email: "a@example.ch"}}}); kindOf(err) != KindAttendeesNotAllowed {
		t.Errorf("attendees without Send As: %v", err)
	}
	if got, err := b.Update(ctx, "ohne@firma.ch", ev.ID, Details{}); err != nil || got.ID != ev.ID {
		t.Errorf("an update that sets nothing: %+v %v", got, err)
	}
	if _, err := b.Update(ctx, "ohne@firma.ch", ev.ID, Details{Subject: "ohne Einladung"}); err != nil {
		t.Errorf("an update without attendees needs no Send As: %v", err)
	}
	if _, err := b.Update(ctx, "two words", ev.ID, Details{}); kindOf(err) != KindCalendarNotFound {
		t.Errorf("update with a name that is no address: %v", err)
	}
	if err := b.Delete(ctx, "two words", ev.ID); kindOf(err) != KindCalendarNotFound {
		t.Errorf("delete with a name that is no address: %v", err)
	}
	if _, _, err := b.Get(ctx, "two words", ev.ID); kindOf(err) != KindCalendarNotFound {
		t.Errorf("get with a name that is no address: %v", err)
	}
	if _, err := b.Create(ctx, "two words", NewEvent{Key: "k"}); kindOf(err) != KindCalendarNotFound {
		t.Errorf("create with a name that is no address: %v", err)
	}
}

func TestEWSAuthenticatesAsTheBundleSays(t *testing.T) {
	ctx := context.Background()
	from, to := at(25, 9, 0), at(25, 10, 0)

	fake := ewsfake.New(map[string]ewsfake.Mailbox{"werkstatt@firma.ch": {}})
	t.Cleanup(fake.Close)
	wrong := strings.Replace(ewsfake.Bundle(""), ewsfake.Password, "falsch", 1)
	b, err := NewBackend(fake.URL(), wrong, Options{HTTPClient: fake.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Events(ctx, "werkstatt@firma.ch", from, to); err == nil || !strings.Contains(err.Error(), "refused the credential") || !strings.Contains(err.Error(), "NTLM") || strings.Contains(err.Error(), "falsch") {
		t.Errorf("a wrong password: %v", err)
	}

	basic := ewsfake.New(map[string]ewsfake.Mailbox{"werkstatt@firma.ch": {}})
	basic.Basic = true
	t.Cleanup(basic.Close)
	// An NTLM Worker never sends its password as Basic, even to a server that asks.
	b, _ = NewBackend(basic.URL(), ewsfake.Bundle(""), Options{HTTPClient: basic.HTTPClient()})
	if _, err := b.Events(ctx, "werkstatt@firma.ch", from, to); err == nil || !strings.Contains(err.Error(), "offers Basic") {
		t.Errorf("an NTLM Worker against a Basic server: %v", err)
	}
	b, _ = NewBackend(basic.URL(), ewsfake.Bundle("basic"), Options{HTTPClient: basic.HTTPClient()})
	if _, err := b.Events(ctx, "werkstatt@firma.ch", from, to); err != nil {
		t.Errorf("a Basic Worker against a Basic server: %v", err)
	}
}

// ewsScripted is a Basic-authenticated server answering each operation with a fixed
// status and body.
func ewsScripted(t *testing.T, status map[string]int, body map[string]string) Backend {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, 4096)
		n, _ := r.Body.Read(raw)
		op := ""
		for _, name := range []string{"FindItem", "GetItem", "CreateItem", "UpdateItem", "DeleteItem", "GetFolder"} {
			if strings.Contains(string(raw[:n]), "<m:"+name) {
				op = name
			}
		}
		if s := status[op]; s != 0 {
			w.WriteHeader(s)
		}
		_, _ = w.Write([]byte(body[op]))
	}))
	t.Cleanup(srv.Close)
	b, err := NewBackend(srv.URL+"/EWS/Exchange.asmx", ewsfake.Bundle("basic"), Options{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ewsOK(op, inner string) string {
	return `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><m:` + op + `Response xmlns:m="m" xmlns:t="t"><m:ResponseMessages><m:` + op + `ResponseMessage ResponseClass="Success"><m:ResponseCode>NoError</m:ResponseCode>` + inner + `</m:` + op + `ResponseMessage></m:ResponseMessages></m:` + op + `Response></s:Body></s:Envelope>`
}

func ewsErr(op, code string) string {
	return `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><m:` + op + `Response xmlns:m="m"><m:ResponseMessages><m:` + op + `ResponseMessage ResponseClass="Error"><m:MessageText>mailbox kundin@example.ch</m:MessageText><m:ResponseCode>` + code + `</m:ResponseCode></m:` + op + `ResponseMessage></m:ResponseMessages></m:` + op + `Response></s:Body></s:Envelope>`
}

func TestEWSReadsEveryAnswerAServerMayGive(t *testing.T) {
	ctx := context.Background()
	from, to := at(25, 9, 0), at(25, 10, 0)
	const item = `<t:CalendarItem><t:ItemId Id="A1" ChangeKey="C"/><t:Start>2030-03-25T08:00:00Z</t:Start><t:End>2030-03-25T09:00:00Z</t:End><t:LegacyFreeBusyStatus>Busy</t:LegacyFreeBusyStatus><t:IsCancelled>true</t:IsCancelled></t:CalendarItem>`
	for name, tc := range map[string]struct {
		status map[string]int
		body   map[string]string
		run    func(Backend) error
		want   string
	}{
		"a fault with a response code": {map[string]int{"FindItem": 500}, map[string]string{"FindItem": `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>a:ErrorSchemaValidation</faultcode><detail><e:ResponseCode xmlns:e="e">ErrorSchemaValidation</e:ResponseCode></detail></s:Fault></s:Body></s:Envelope>`},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "FindItem answered ErrorSchemaValidation"},
		"a fault without one": {map[string]int{"FindItem": 500}, map[string]string{"FindItem": `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode></s:Fault></s:Body></s:Envelope>`},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "answered s:Client"},
		"a bare fault": {map[string]int{"FindItem": 500}, map[string]string{"FindItem": `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault/></s:Body></s:Envelope>`},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "answered a SOAP fault"},
		"no envelope at all": {map[string]int{"FindItem": 503}, map[string]string{"FindItem": `Service Unavailable`},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "answered HTTP 503"},
		"no envelope with 200": {nil, map[string]string{"FindItem": `<html>`},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "not a SOAP envelope"},
		"an envelope without a message": {nil, map[string]string{"FindItem": `<s:Envelope xmlns:s="x"><s:Body/></s:Envelope>`},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "not a SOAP envelope"},
		"a message of another shape": {nil, map[string]string{"FindItem": `<s:Envelope xmlns:s="x"><s:Body><m:FindItemResponseMessage ResponseClass="Success"><m:RootFolder><t:Items><t:CalendarItem><t:IsCancelled>vielleicht</t:IsCancelled></t:CalendarItem></t:Items></m:RootFolder></m:FindItemResponseMessage></s:Body></s:Envelope>`},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "unexpected shape"},
		"a warning without a code": {nil, map[string]string{"FindItem": `<s:Envelope xmlns:s="x"><s:Body><m:FindItemResponseMessage ResponseClass="Warning"/></s:Body></s:Envelope>`},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "FindItem answered Warning"},
		"a busy server": {nil, map[string]string{"FindItem": ewsErr("FindItem", "ErrorServerBusy")},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "FindItem answered ErrorServerBusy"},
		"a window too full": {nil, map[string]string{"FindItem": ewsOK("FindItem", `<m:RootFolder IncludesLastItemInRange="false"><t:Items/></m:RootFolder>`)},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "read a shorter window"},
		"a start that is no time": {nil, map[string]string{"FindItem": ewsOK("FindItem", `<m:RootFolder IncludesLastItemInRange="true"><t:Items><t:CalendarItem><t:Start>morgen</t:Start></t:CalendarItem></t:Items></m:RootFolder>`)},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "start that is not a UTC time"},
		"an end that is no time": {nil, map[string]string{"FindItem": ewsOK("FindItem", `<m:RootFolder IncludesLastItemInRange="true"><t:Items><t:CalendarItem><t:Start>2030-03-25T08:00:00Z</t:Start><t:End>bald</t:End></t:CalendarItem></t:Items></m:RootFolder>`)},
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "end that is not a UTC time"},
		"a create without an id": {nil, map[string]string{"FindItem": ewsOK("FindItem", `<m:RootFolder IncludesLastItemInRange="true"><t:Items/></m:RootFolder>`), "CreateItem": ewsOK("CreateItem", `<m:Items/>`)},
			func(b Backend) error {
				_, err := b.Create(ctx, "w@f.ch", NewEvent{Key: "k", Start: from, End: to})
				return err
			}, "without the item's id"},
		"a create that fails while the window reads": {nil, map[string]string{"FindItem": ewsErr("FindItem", "ErrorServerBusy")},
			func(b Backend) error {
				_, err := b.Create(ctx, "w@f.ch", NewEvent{Key: "k", Start: from, End: to})
				return err
			}, "ErrorServerBusy"},
		"a get of two items": {nil, map[string]string{"GetItem": ewsOK("GetItem", `<m:Items>`+item+item+`</m:Items>`)},
			func(b Backend) error { _, _, err := b.Get(ctx, "w@f.ch", "A1"); return err }, "with 2 items"},
		"a get whose time is broken": {nil, map[string]string{"GetItem": ewsOK("GetItem", `<m:Items><t:CalendarItem><t:Start>x</t:Start></t:CalendarItem></m:Items>`)},
			func(b Backend) error { _, _, err := b.Get(ctx, "w@f.ch", "A1"); return err }, "not a UTC time"},
		"a get refused": {nil, map[string]string{"GetItem": ewsErr("GetItem", "ErrorAccessDenied")},
			func(b Backend) error { _, _, err := b.Get(ctx, "w@f.ch", "A1"); return err }, "may not read the calendar"},
		"a confirm of a cancelled item": {nil, map[string]string{"GetItem": ewsOK("GetItem", `<m:Items>`+item+`</m:Items>`)},
			func(b Backend) error { _, err := b.Update(ctx, "w@f.ch", "A1", Details{Subject: "s"}); return err }, "was cancelled"},
		"a confirm whose time is broken": {nil, map[string]string{"GetItem": ewsOK("GetItem", `<m:Items><t:CalendarItem><t:Start>x</t:Start></t:CalendarItem></m:Items>`)},
			func(b Backend) error { _, err := b.Update(ctx, "w@f.ch", "A1", Details{Subject: "s"}); return err }, "not a UTC time"},
		"a confirm refused": {nil, map[string]string{"GetItem": ewsOK("GetItem", `<m:Items>`+strings.Replace(item, "true", "false", 1)+`</m:Items>`), "UpdateItem": ewsErr("UpdateItem", "ErrorAccessDenied")},
			func(b Backend) error { _, err := b.Update(ctx, "w@f.ch", "A1", Details{Subject: "s"}); return err }, "may not write"},
		"a delete refused": {nil, map[string]string{"DeleteItem": ewsErr("DeleteItem", "ErrorAccessDenied")},
			func(b Backend) error { return b.Delete(ctx, "w@f.ch", "A1") }, "may not write"},
		"a missing item whose mailbox probe fails": {nil, map[string]string{"GetItem": ewsErr("GetItem", "ErrorItemNotFound"), "GetFolder": ewsErr("GetFolder", "ErrorServerBusy")},
			func(b Backend) error { _, _, err := b.Get(ctx, "w@f.ch", "A1"); return err }, "GetFolder answered ErrorServerBusy"},
		"a missing item whose folder is no item": {nil, map[string]string{"GetItem": ewsErr("GetItem", "ErrorItemNotFound"), "GetFolder": ewsErr("GetFolder", "ErrorItemNotFound")},
			func(b Backend) error { _, _, err := b.Get(ctx, "w@f.ch", "A1"); return err }, "calendar does not exist"},
		"a redirect": {map[string]int{"FindItem": http.StatusFound}, nil,
			func(b Backend) error { _, err := b.Events(ctx, "w@f.ch", from, to); return err }, "redirected; refused"},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.status != nil && tc.status["FindItem"] == http.StatusFound {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "https://elsewhere.example/EWS/Exchange.asmx", http.StatusFound)
				}))
				defer srv.Close()
				b, err := NewBackend(srv.URL+"/EWS/Exchange.asmx", ewsfake.Bundle("basic"), Options{HTTPClient: srv.Client()})
				if err != nil {
					t.Fatal(err)
				}
				if err := tc.run(b); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("%v, want %q", err, tc.want)
				}
				return
			}
			err := tc.run(ewsScripted(t, tc.status, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "kundin@example.ch") {
				t.Fatalf("the message text of a failure reached the error: %v", err)
			}
		})
	}
}

func TestEWSReadsCancelledAndFreeItemsAsNotBusy(t *testing.T) {
	for fb, busy := range map[string]bool{"Free": false, "free": false, "Busy": true, "Tentative": true, "OOF": true, "WorkingElsewhere": true, "NoData": true} {
		it := ewsItem{Start: "2030-03-25T08:00:00Z", End: "2030-03-25T09:00:00Z", FreeBusy: fb}
		if ev, err := it.event(); err != nil || ev.Busy != busy {
			t.Errorf("%s: %+v %v", fb, ev, err)
		}
	}
	it := ewsItem{Start: "2030-03-25T08:00:00Z", End: "2030-03-25T09:00:00Z", FreeBusy: "Busy", IsCancelled: true}
	if ev, _ := it.event(); ev.Busy {
		t.Error("a cancelled item is busy")
	}
	it.Extended = append(it.Extended, it.Extended...)
	if it.key() != "" {
		t.Error("an item without the property has a key")
	}
}

func TestEWSNamesTheSchemesAServerOffers(t *testing.T) {
	for in, want := range map[string]string{
		"":                                "no scheme",
		`Negotiate`:                       "Negotiate",
		`Basic realm="exch01"`:            "Basic",
		`NTLM TlRMTVNTUAACAAAA, Bearer x`: "NTLM, Bearer",
	} {
		var values []string
		if in != "" {
			values = []string{in}
		}
		if got := schemesOf(values); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if got := xmlAttr(`a"b<c&`); got != "a&quot;b&lt;c&amp;" && got != "a&#34;b&lt;c&amp;" {
		t.Errorf("attribute %q", got)
	}
	if ewsTime(time.Date(2030, 3, 25, 9, 0, 0, 0, zurichLoc(t))) != "2030-03-25T08:00:00Z" {
		t.Error("an EWS time is not in UTC")
	}
}

func zurichLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
