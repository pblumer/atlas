package ewsfake

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/go-ntlmssp"
)

const header = `<soap:Header><t:RequestServerVersion Version="Exchange2013_SP1"/><t:TimeZoneContext><t:TimeZoneDefinition Id="UTC"/></t:TimeZoneContext></soap:Header>`

func envelope(head, body string) string {
	return `<?xml version="1.0"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:t="t" xmlns:m="m">` + head + `<soap:Body>` + body + `</soap:Body></soap:Envelope>`
}

// post sends a SOAP body through the NTLM negotiator with the account's credential.
func post(t *testing.T, s *Server, password, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.URL(), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(Username, password)
	c := http.Client{Transport: ntlmssp.Negotiator{RoundTripper: s.HTTPClient().Transport}}
	if s.Basic {
		c = *s.HTTPClient()
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

const shape = `<m:ItemShape><t:BaseShape>IdOnly</t:BaseShape><t:AdditionalProperties><t:FieldURI FieldURI="calendar:Start"/><t:ExtendedFieldURI PropertyName="AtlasCreateKey"/></t:AdditionalProperties></m:ItemShape>`

func folder(addr string) string {
	return `<t:DistinguishedFolderId Id="calendar"><t:Mailbox><t:EmailAddress>` + addr + `</t:EmailAddress></t:Mailbox></t:DistinguishedFolderId>`
}

func TestTheFakeChecksThePasswordWithNTLMv2(t *testing.T) {
	s := New(map[string]Mailbox{"w@f.ch": {}})
	defer s.Close()
	body := envelope(header, `<m:GetFolder>`+`<m:FolderIds>`+folder("w@f.ch")+`</m:FolderIds></m:GetFolder>`)
	if code, out := post(t, s, Password, body); code != 200 || !strings.Contains(out, "NoError") {
		t.Fatalf("the right password: %d %s", code, out)
	}
	if code, _ := post(t, s, "falsch", body); code != http.StatusUnauthorized {
		t.Fatalf("a wrong password: %d", code)
	}
	if verifyAuthenticate([]byte("NTLMSSP\x00\x03\x00\x00\x00"), s.challenge, Username, Password) {
		t.Fatal("a truncated AUTHENTICATE passed")
	}
	if ntlmToken("Bearer x") != nil || ntlmToken("NTLM ***") != nil || ntlmMessageType([]byte("short")) != 0 || fromUTF16le([]byte{1}) != "" {
		t.Fatal("a malformed token was read")
	}
	msg := make([]byte, 64)
	copy(msg, ntlmSignature)
	binary.LittleEndian.PutUint32(msg[8:], 3)
	binary.LittleEndian.PutUint16(msg[20:], 200) // NT response beyond the message
	if field(msg, 20) != nil || field(msg[:10], 20) != nil {
		t.Fatal("a field beyond the message was read")
	}
	// A user of another domain is refused before any hash is computed.
	other := []byte("NTLMSSP\x00\x03\x00\x00\x00")
	other = append(other, make([]byte, 52)...)
	nt := bytes.Repeat([]byte{1}, 48)
	binary.LittleEndian.PutUint16(other[20:], uint16(len(nt)))
	binary.LittleEndian.PutUint32(other[24:], uint32(len(other)))
	other = append(other, nt...)
	if verifyAuthenticate(other, s.challenge, Username, Password) {
		t.Fatal("an AUTHENTICATE without the user passed")
	}
}

func TestTheFakeOffersBasicWhenSetTo(t *testing.T) {
	s := New(map[string]Mailbox{"w@f.ch": {}})
	s.Basic = true
	defer s.Close()
	req, _ := http.NewRequest(http.MethodPost, s.URL(), strings.NewReader(""))
	req.SetBasicAuth(Username, "falsch")
	resp, err := s.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	if code, out := post(t, s, Password, envelope(header, `<m:GetFolder><m:FolderIds>`+folder("w@f.ch")+`</m:FolderIds></m:GetFolder>`)); code != 200 || !strings.Contains(out, "NoError") {
		t.Fatalf("%d %s", code, out)
	}
	if !strings.Contains(Bundle("basic"), `"authentication":"basic"`) || strings.Contains(Bundle(""), "authentication") {
		t.Fatal("the bundle")
	}
}

func TestTheFakeRefusesWhatExchangeRefuses(t *testing.T) {
	start, end := time.Date(2030, 3, 25, 8, 0, 0, 0, time.UTC), time.Date(2030, 3, 25, 9, 0, 0, 0, time.UTC)
	s := New(map[string]Mailbox{"w@f.ch": {Busy: []Busy{{Start: start, End: end}}}, "r@f.ch": {ReadOnly: true}})
	defer s.Close()
	view := `<m:CalendarView MaxEntriesReturned="5" StartDate="2030-03-25T00:00:00Z" EndDate="2030-03-26T00:00:00Z"/>`
	item := `<t:CalendarItem><t:Subject>s</t:Subject><t:Start>2030-03-25T10:00:00Z</t:Start><t:End>2030-03-25T11:00:00Z</t:End></t:CalendarItem>`
	for name, tc := range map[string]struct{ head, body, want string }{
		"no version":           {`<soap:Header/>`, `<m:GetFolder/>`, "ErrorInvalidServerVersion"},
		"no UTC context":       {`<soap:Header><t:RequestServerVersion Version="Exchange2013_SP1"/></soap:Header>`, `<m:GetFolder/>`, "ErrorTimeZone"},
		"two operations":       {header, `<m:GetFolder/><m:GetFolder/>`, "ErrorSchemaValidation"},
		"an unknown operation": {header, `<m:SyncFolderItems/>`, "ErrorInvalidRequest"},
		"a wide shape":         {header, `<m:FindItem><m:ItemShape><t:BaseShape>AllProperties</t:BaseShape></m:ItemShape></m:FindItem>`, "ErrorInvalidPropertyRequest"},
		"a subject asked for": {header, `<m:FindItem><m:ItemShape><t:BaseShape>IdOnly</t:BaseShape><t:AdditionalProperties><t:FieldURI FieldURI="item:Subject"/></t:AdditionalProperties></m:ItemShape></m:FindItem>`,
			"ErrorInvalidPropertyRequest"},
		"an indexed field":                  {header, `<m:FindItem><m:ItemShape><t:BaseShape>IdOnly</t:BaseShape><t:AdditionalProperties><t:IndexedFieldURI/></t:AdditionalProperties></m:ItemShape></m:FindItem>`, "ErrorInvalidPropertyRequest"},
		"no folder":                         {header, `<m:FindItem>` + shape + `</m:FindItem>`, "ErrorInvalidRequest"},
		"another folder":                    {header, `<m:FindItem>` + shape + `<m:ParentFolderIds><t:DistinguishedFolderId Id="inbox"/></m:ParentFolderIds></m:FindItem>`, "ErrorInvalidRequest"},
		"a missing mailbox":                 {header, `<m:FindItem>` + shape + view + `<m:ParentFolderIds>` + folder("x@f.ch") + `</m:ParentFolderIds></m:FindItem>`, "ErrorNonExistentMailbox"},
		"no view":                           {header, `<m:FindItem>` + shape + `<m:ParentFolderIds>` + folder("w@f.ch") + `</m:ParentFolderIds></m:FindItem>`, "ErrorInvalidRequest"},
		"a broken view":                     {header, `<m:FindItem>` + shape + `<m:CalendarView StartDate="x"/><m:ParentFolderIds>` + folder("w@f.ch") + `</m:ParentFolderIds></m:FindItem>`, "ErrorInvalidRequest"},
		"a view that holds more":            {header, `<m:FindItem>` + shape + `<m:CalendarView MaxEntriesReturned="0" StartDate="2030-03-25T00:00:00Z" EndDate="2030-03-26T00:00:00Z"/><m:ParentFolderIds>` + folder("w@f.ch") + `</m:ParentFolderIds></m:FindItem>`, `IncludesLastItemInRange="false"`},
		"a get with a wide shape":           {header, `<m:GetItem><m:ItemShape><t:BaseShape>Default</t:BaseShape></m:ItemShape></m:GetItem>`, "ErrorInvalidPropertyRequest"},
		"a folder of another kind":          {header, `<m:GetFolder><m:FolderIds><t:FolderId Id="x"/></m:FolderIds></m:GetFolder>`, "ErrorInvalidRequest"},
		"a folder of no mailbox":            {header, `<m:GetFolder><m:FolderIds>` + folder("x@f.ch") + `</m:FolderIds></m:GetFolder>`, "ErrorNonExistentMailbox"},
		"a create without invitations said": {header, `<m:CreateItem><m:SavedItemFolderId>` + folder("w@f.ch") + `</m:SavedItemFolderId><m:Items>` + item + `</m:Items></m:CreateItem>`, "ErrorSchemaValidation"},
		"a create without folder":           {header, `<m:CreateItem SendMeetingInvitations="SendToNone"><m:Items>` + item + `</m:Items></m:CreateItem>`, "ErrorInvalidRequest"},
		"a create in no mailbox":            {header, `<m:CreateItem SendMeetingInvitations="SendToNone"><m:SavedItemFolderId>` + folder("x@f.ch") + `</m:SavedItemFolderId><m:Items>` + item + `</m:Items></m:CreateItem>`, "ErrorNonExistentMailbox"},
		"a create in a read-only mailbox":   {header, `<m:CreateItem SendMeetingInvitations="SendToNone"><m:SavedItemFolderId>` + folder("r@f.ch") + `</m:SavedItemFolderId><m:Items>` + item + `</m:Items></m:CreateItem>`, "ErrorAccessDenied"},
		"a create out of the schema's order": {header, `<m:CreateItem SendMeetingInvitations="SendToNone"><m:SavedItemFolderId>` + folder("w@f.ch") + `</m:SavedItemFolderId><m:Items><t:CalendarItem><t:Start>2030-03-25T10:00:00Z</t:Start><t:Subject>s</t:Subject></t:CalendarItem></m:Items></m:CreateItem>`,
			"ErrorSchemaValidation"},
		"a create of an unknown element": {header, `<m:CreateItem SendMeetingInvitations="SendToNone"><m:SavedItemFolderId>` + folder("w@f.ch") + `</m:SavedItemFolderId><m:Items><t:CalendarItem><t:Location>x</t:Location></t:CalendarItem></m:Items></m:CreateItem>`,
			"ErrorSchemaValidation"},
		"a create that ends first": {header, `<m:CreateItem SendMeetingInvitations="SendToNone"><m:SavedItemFolderId>` + folder("w@f.ch") + `</m:SavedItemFolderId><m:Items><t:CalendarItem><t:Start>2030-03-25T10:00:00Z</t:Start><t:End>2030-03-25T09:00:00Z</t:End></t:CalendarItem></m:Items></m:CreateItem>`,
			"ErrorCalendarEndDateIsEarlierThanStartDate"},
		"an update of nothing":  {header, `<m:UpdateItem/>`, "ErrorInvalidRequest"},
		"an update of no item":  {header, `<m:UpdateItem><m:ItemChanges><t:ItemChange><t:ItemId Id="x"/></t:ItemChange></m:ItemChanges></m:UpdateItem>`, "ErrorItemNotFound"},
		"a delete without type": {header, `<m:DeleteItem/>`, "ErrorSchemaValidation"},
		"a delete of no item":   {header, `<m:DeleteItem DeleteType="HardDelete" SendMeetingCancellations="SendToNone"><m:ItemIds><t:ItemId Id="x"/></m:ItemIds></m:DeleteItem>`, "ErrorItemNotFound"},
		"a get of no item":      {header, `<m:GetItem>` + shape + `<m:ItemIds><t:ItemId Id="x"/></m:ItemIds></m:GetItem>`, "ErrorItemNotFound"},
		"not even XML":          {"", "<", "ErrorSchemaValidation"},
	} {
		code, out := post(t, s, Password, envelope(tc.head, tc.body))
		if name == "not even XML" {
			code, out = post(t, s, Password, "<")
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: %d %s, want %s", name, code, out, tc.want)
		}
	}

	// A seeded item: read, updated with what EWS refuses, deleted from a read-only box.
	id := s.Items("w@f.ch")[0].ID
	if _, out := post(t, s, Password, envelope(header, `<m:GetItem>`+shape+`<m:ItemIds><t:ItemId Id="`+id+`"/></m:ItemIds></m:GetItem>`)); !strings.Contains(out, "2030-03-25T08:00:00Z") {
		t.Errorf("get: %s", out)
	}
	change := func(attrs, updates string) string {
		return envelope(header, `<m:UpdateItem `+attrs+`><m:ItemChanges><t:ItemChange><t:ItemId Id="`+id+`"/><t:Updates>`+updates+`</t:Updates></t:ItemChange></m:ItemChanges></m:UpdateItem>`)
	}
	for name, tc := range map[string]struct{ body, want string }{
		"without invitations said": {change(``, ``), "ErrorSchemaValidation"},
		"an append":                {change(`SendMeetingInvitationsOrCancellations="SendToNone"`, `<t:AppendToItemField/>`), "ErrorInvalidRequest"},
		"a field it does not take": {change(`SendMeetingInvitationsOrCancellations="SendToNone"`, `<t:SetItemField><t:FieldURI FieldURI="calendar:Location"/><t:CalendarItem/></t:SetItemField>`), "ErrorInvalidPropertySet"},
		"attendees, not sent":      {change(`SendMeetingInvitationsOrCancellations="SendToNone"`, `<t:SetItemField><t:FieldURI FieldURI="calendar:RequiredAttendees"/><t:CalendarItem><t:RequiredAttendees><t:Attendee><t:Mailbox><t:EmailAddress>a@b.ch</t:EmailAddress></t:Mailbox></t:Attendee></t:RequiredAttendees></t:CalendarItem></t:SetItemField>`), "NoError"},
	} {
		if _, out := post(t, s, Password, tc.body); !strings.Contains(out, tc.want) {
			t.Errorf("update %s: %s, want %s", name, out, tc.want)
		}
	}
	if it := s.Items("w@f.ch")[0]; len(it.Attendees) != 1 || it.Invited {
		t.Errorf("attendees written without invitation: %+v", it)
	}
	if s.Items("x@f.ch") != nil {
		t.Error("a missing mailbox has items")
	}

	ro := New(map[string]Mailbox{"r@f.ch": {ReadOnly: true, Busy: []Busy{{Start: start, End: end}}}})
	defer ro.Close()
	rid := ro.Items("r@f.ch")[0].ID
	if _, out := post(t, ro, Password, envelope(header, `<m:DeleteItem DeleteType="HardDelete" SendMeetingCancellations="SendToNone"><m:ItemIds><t:ItemId Id="`+rid+`"/></m:ItemIds></m:DeleteItem>`)); !strings.Contains(out, "ErrorAccessDenied") {
		t.Errorf("delete in a read-only box: %s", out)
	}
	if _, out := post(t, ro, Password, envelope(header, `<m:UpdateItem SendMeetingInvitationsOrCancellations="SendToNone"><m:ItemChanges><t:ItemChange><t:ItemId Id="`+rid+`"/></t:ItemChange></m:ItemChanges></m:UpdateItem>`)); !strings.Contains(out, "ErrorAccessDenied") {
		t.Errorf("update in a read-only box: %s", out)
	}

	resp, err := s.HTTPClient().Get(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a GET: %d", resp.StatusCode)
	}
	if got := challengeMessage(s.challenge, Domain); ntlmMessageType(got) != 2 || base64.StdEncoding.EncodeToString(got) == "" {
		t.Error("the challenge")
	}
}
