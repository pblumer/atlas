package calendar

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/go-ntlmssp"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/limits"
)

// EWS authentication schemes a bundle may name.
const (
	// EWSAuthNTLM is NTLMv2, offered by Exchange Server's EWS virtual directory by
	// default, inside Negotiate or on its own. It is the default.
	EWSAuthNTLM = "ntlm"
	// EWSAuthBasic is Basic over TLS, for a virtual directory where an administrator
	// enabled it.
	EWSAuthBasic = "basic"
)

// ewsKeyProperty is the extended MAPI property the create key is written into: a named
// string property in a property set of atlas's own, so it collides with nothing and
// every client ignores it. EWS has no idempotency token of its own (ADR-0552).
const (
	ewsKeyPropertySet  = "8f3c2d0e-5a41-4b7e-9b1a-a71a5c0b2e10"
	ewsKeyPropertyName = "AtlasCreateKey"
)

// ewsServerVersion is the schema version every request names. Exchange 2013 SP1 is the
// oldest schema Exchange Server 2016, 2019 and Subscription Edition all accept, and it
// has everything this backend sends.
const ewsServerVersion = "Exchange2013_SP1"

// ewsMaxEntries bounds one calendar view, as the other backends bound a window.
const ewsMaxEntries = maxGraphPages * graphPageSize

// ews is the Exchange Web Services backend for Exchange Server on premises, which
// Microsoft Graph does not reach. The Worker's endpoint is the server's EWS URL
// (https://mail.contoso.ch/EWS/Exchange.asmx); a task names a calendar by the SMTP
// address of its mailbox — a resource mailbox by default — and its default calendar is
// used. The account is a service account with delegate access to the booking
// mailboxes (Editor on the calendar, or full access), not impersonation.
type ews struct {
	endpoint *url.URL
	username string
	password string
	basic    bool
	httpc    *http.Client
}

func newEWS(endpoint string, b Bundle, opts Options) (*ews, error) {
	if strings.TrimSpace(b.Username) == "" || b.Password == "" {
		return nil, fmt.Errorf("calendar: an Exchange Web Services Worker needs the username (DOMAIN\\account or account@domain) and password of a service account with delegate access to the booking mailboxes")
	}
	auth := strings.ToLower(strings.TrimSpace(b.Authentication))
	switch auth {
	case "", EWSAuthNTLM:
		auth = EWSAuthNTLM
	case EWSAuthBasic:
	case "kerberos", "negotiate":
		return nil, fmt.Errorf("calendar: authentication %q: this Worker authenticates to EWS with NTLM (inside Negotiate where the server offers it) or Basic; Kerberos alone is not supported", b.Authentication)
	default:
		return nil, fmt.Errorf("calendar: unknown authentication %q (want %s or %s)", clip(b.Authentication), EWSAuthNTLM, EWSAuthBasic)
	}
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return nil, fmt.Errorf("calendar: an Exchange Web Services Worker needs an endpoint, the server's EWS URL (https://mail.contoso.ch/EWS/Exchange.asmx)")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname()))) {
		return nil, fmt.Errorf("calendar: the endpoint %q is not an https URL of an EWS endpoint", clip(endpoint))
	}
	base := opts.HTTPClient
	if base == nil {
		base = nettimeout.HTTPClient()
	}
	httpc := *base
	if auth == EWSAuthNTLM {
		// The negotiator turns the request's Basic credentials into an NTLM handshake
		// and never sends them as Basic: a server that offers only Basic gets nothing.
		httpc.Transport = ntlmssp.Negotiator{RoundTripper: base.Transport}
	}
	httpc.CheckRedirect = func(*http.Request, []*http.Request) error {
		// EWS does not redirect a SOAP POST; a server that does is pointing the
		// credential somewhere else.
		return fmt.Errorf("the EWS endpoint redirected; refused, the credential goes to the endpoint only")
	}
	return &ews{endpoint: u, username: strings.TrimSpace(b.Username), password: b.Password, basic: auth == EWSAuthBasic, httpc: &httpc}, nil
}

// ewsMailbox checks a calendar name: the SMTP address of a mailbox, which Exchange
// resolves and refuses when it names none. A name with white space or markup in it is
// refused here, since it is written into every request.
func ewsMailbox(cal string) (string, error) {
	cal = strings.TrimSpace(cal)
	if cal == "" || strings.ContainsAny(cal, " \t\r\n<>\"&") {
		return "", &Error{Kind: KindCalendarNotFound, Message: "an Exchange calendar is named by the SMTP address of its mailbox"}
	}
	return cal, nil
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func ewsFolder(mailbox string) string {
	return `<t:DistinguishedFolderId Id="calendar"><t:Mailbox><t:EmailAddress>` + xmlText(mailbox) + `</t:EmailAddress></t:Mailbox></t:DistinguishedFolderId>`
}

func ewsKeyURI() string {
	return `<t:ExtendedFieldURI PropertySetId="` + ewsKeyPropertySet + `" PropertyName="` + ewsKeyPropertyName + `" PropertyType="String"/>`
}

// ewsShape is every property the backend reads of an item: times, availability,
// cancellation and the create key. Subject, body, organizer and attendees of other
// people's appointments are never asked for.
func ewsShape() string {
	return `<m:ItemShape><t:BaseShape>IdOnly</t:BaseShape><t:AdditionalProperties>` +
		`<t:FieldURI FieldURI="calendar:Start"/><t:FieldURI FieldURI="calendar:End"/>` +
		`<t:FieldURI FieldURI="calendar:LegacyFreeBusyStatus"/><t:FieldURI FieldURI="calendar:IsCancelled"/>` +
		ewsKeyURI() + `</t:AdditionalProperties></m:ItemShape>`
}

func ewsItemID(id string) string { return `<t:ItemId Id="` + xmlAttr(id) + `"/>` }

func xmlAttr(s string) string {
	return strings.NewReplacer(`"`, "&quot;").Replace(xmlText(s))
}

func ewsTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// ewsEnvelope wraps an operation. Every time is sent and asked for in UTC.
func ewsEnvelope(body string) []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>` +
		`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types" xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">` +
		`<soap:Header><t:RequestServerVersion Version="` + ewsServerVersion + `"/><t:TimeZoneContext><t:TimeZoneDefinition Id="UTC"/></t:TimeZoneContext></soap:Header>` +
		`<soap:Body>` + body + `</soap:Body></soap:Envelope>`)
}

type ewsItem struct {
	ItemID struct {
		ID        string `xml:"Id,attr"`
		ChangeKey string `xml:"ChangeKey,attr"`
	} `xml:"ItemId"`
	Start       string `xml:"Start"`
	End         string `xml:"End"`
	FreeBusy    string `xml:"LegacyFreeBusyStatus"`
	IsCancelled bool   `xml:"IsCancelled"`
	Extended    []struct {
		URI struct {
			Name string `xml:"PropertyName,attr"`
			Set  string `xml:"PropertySetId,attr"`
		} `xml:"ExtendedFieldURI"`
		Value string `xml:"Value"`
	} `xml:"ExtendedProperty"`
}

func (it ewsItem) key() string {
	for _, x := range it.Extended {
		if x.URI.Name == ewsKeyPropertyName && strings.EqualFold(x.URI.Set, ewsKeyPropertySet) {
			return x.Value
		}
	}
	return ""
}

// event is the item as the backend reads it. The contracts' rule: busy unless marked
// free, or cancelled; tentative, out of office, working elsewhere and no data block
// their time.
func (it ewsItem) event() (Event, error) {
	start, err := time.Parse(time.RFC3339, strings.TrimSpace(it.Start))
	if err != nil {
		return Event{}, fmt.Errorf("calendar: EWS answered a start that is not a UTC time")
	}
	end, err := time.Parse(time.RFC3339, strings.TrimSpace(it.End))
	if err != nil {
		return Event{}, fmt.Errorf("calendar: EWS answered an end that is not a UTC time")
	}
	busy := !it.IsCancelled && !strings.EqualFold(strings.TrimSpace(it.FreeBusy), "Free")
	return Event{ID: it.ItemID.ID, Key: it.key(), Start: start.UTC(), End: end.UTC(), Busy: busy}, nil
}

// ewsMessage is one response message: FindItemResponseMessage, CreateItemResponseMessage
// and the others share this shape.
type ewsMessage struct {
	Class string `xml:"ResponseClass,attr"`
	Code  string `xml:"ResponseCode"`
	Root  struct {
		Last  string    `xml:"IncludesLastItemInRange,attr"`
		Items []ewsItem `xml:"Items>CalendarItem"`
	} `xml:"RootFolder"`
	Items []ewsItem `xml:"Items>CalendarItem"`
}

// ewsFailure is a response message, or a SOAP fault, that is not a success. Only the
// response code is kept: EWS's message text can quote addresses.
type ewsFailure struct {
	op   string
	code string
}

func (e *ewsFailure) Error() string {
	return fmt.Sprintf("calendar: EWS %s answered %s", e.op, e.code)
}

// call sends one operation and answers its single response message, or the failure it
// reported.
func (c *ews) call(ctx context.Context, op, body string) (*ewsMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String(), bytes.NewReader(ewsEnvelope(body)))
	if err != nil {
		return nil, fmt.Errorf("calendar: build the EWS %s request: %w", op, err)
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("User-Agent", "atlas-calendar-worker")
	req.SetBasicAuth(c.username, c.password)
	resp, err := c.httpc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("calendar: EWS %s: %v", op, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limits.Default().Payload+1))
	if err != nil {
		return nil, fmt.Errorf("calendar: read EWS's answer: %w", err)
	}
	if int64(len(raw)) > limits.Default().Payload {
		return nil, fmt.Errorf("calendar: EWS's answer exceeds the payload limit; read a shorter window")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("calendar: Exchange refused the credential (HTTP 401; the server offers %s); check the account, its password and the bundle's authentication", schemesOf(resp.Header.Values("WWW-Authenticate")))
	}
	msg, fault, err := parseEWS(raw)
	if err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("calendar: EWS %s answered HTTP %d", op, resp.StatusCode)
		}
		return nil, err
	}
	if fault != "" {
		return nil, &ewsFailure{op: op, code: fault}
	}
	if msg.Class != "Success" || (msg.Code != "" && msg.Code != "NoError") {
		code := msg.Code
		if code == "" {
			code = msg.Class
		}
		return nil, &ewsFailure{op: op, code: code}
	}
	return msg, nil
}

// schemesOf names the schemes a 401 offers, without their tokens.
func schemesOf(values []string) string {
	var names []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			name, _, _ := strings.Cut(strings.TrimSpace(part), " ")
			if name != "" && !strings.Contains(name, "=") {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return "no scheme"
	}
	return strings.Join(names, ", ")
}

// parseEWS reads a SOAP answer: the first response message, or the response code of a
// fault (its faultcode when it carries none).
func parseEWS(raw []byte) (*ewsMessage, string, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var inFault bool
	var faultCode string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("calendar: EWS answered something that is not a SOAP envelope")
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch name := se.Name.Local; {
		case name == "Fault":
			inFault = true
		case inFault && name == "ResponseCode":
			var code string
			if err := dec.DecodeElement(&code, &se); err == nil {
				return nil, strings.TrimSpace(code), nil
			}
		case inFault && name == "faultcode":
			_ = dec.DecodeElement(&faultCode, &se)
		case !inFault && strings.HasSuffix(name, "ResponseMessage"):
			var m ewsMessage
			if err := dec.DecodeElement(&m, &se); err != nil {
				return nil, "", fmt.Errorf("calendar: EWS answered a response message of an unexpected shape")
			}
			return &m, "", nil
		}
	}
	if inFault {
		if faultCode == "" {
			faultCode = "a SOAP fault"
		}
		return nil, strings.TrimSpace(faultCode), nil
	}
	return nil, "", fmt.Errorf("calendar: EWS answered something that is not a SOAP envelope")
}

// classify turns EWS's response codes into the kinds a process can act on. The codes
// are Exchange's documented ones, not observed from a server.
func (c *ews) classify(err error, write bool) error {
	f, ok := err.(*ewsFailure)
	if !ok {
		return err
	}
	switch f.code {
	case "ErrorNonExistentMailbox", "ErrorInvalidSmtpAddress", "ErrorFolderNotFound", "ErrorMailboxMoveInProgress", "ErrorMailRecipientNotFound":
		return &Error{Kind: KindCalendarNotFound, Message: "the calendar does not exist or this Worker cannot see it (" + f.code + ")"}
	case "ErrorAccessDenied", "ErrorInsufficientPermissions", "ErrorDelegateNoUser":
		if write {
			return &Error{Kind: KindCalendarNotWritable, Message: "this Worker may not write to the calendar (" + f.code + ")"}
		}
		return &Error{Kind: KindCalendarNotFound, Message: "this Worker may not read the calendar (" + f.code + ")"}
	case "ErrorSendAsDenied":
		return &Error{Kind: KindAttendeesNotAllowed, Message: "the service account may not send invitations for this mailbox (" + f.code + "); grant it Send As or Send on Behalf, or let the confirmation mail come from the process"}
	case "ErrorItemNotFound", "ErrorInvalidIdMalformed", "ErrorInvalidIdNotAnItemAttachmentId", "ErrorInvalidIdEmpty", "ErrorInvalidId":
		return &Error{Kind: KindBookingNotFound, Message: "the booking does not exist in this calendar (" + f.code + ")"}
	}
	return err
}

// onItem classifies a failure on one item. EWS answers a missing item the same way
// whether the item or the mailbox is gone only in some versions, so on a missing item
// the calendar is asked.
func (c *ews) onItem(ctx context.Context, mailbox string, err error, write bool) error {
	e := c.classify(err, write)
	if kindOf(e) != KindBookingNotFound {
		return e
	}
	if gone := c.calendarGone(ctx, mailbox); gone != nil {
		return gone
	}
	return e
}

// calendarGone asks for the mailbox's calendar folder: nil when it is there, the
// calendar's failure otherwise.
func (c *ews) calendarGone(ctx context.Context, mailbox string) error {
	_, err := c.call(ctx, "GetFolder", `<m:GetFolder><m:FolderShape><t:BaseShape>IdOnly</t:BaseShape></m:FolderShape><m:FolderIds>`+ewsFolder(mailbox)+`</m:FolderIds></m:GetFolder>`)
	if err == nil {
		return nil
	}
	e := c.classify(err, false)
	if kindOf(e) == KindBookingNotFound {
		return &Error{Kind: KindCalendarNotFound, Message: "the calendar does not exist or this Worker cannot see it"}
	}
	return e
}

// Events implements [Backend] over a calendar view, which expands recurring
// appointments into their occurrences; a FindItem without one answers the series.
func (c *ews) Events(ctx context.Context, cal string, from, to time.Time) ([]Event, error) {
	mb, err := ewsMailbox(cal)
	if err != nil {
		return nil, err
	}
	m, err := c.call(ctx, "FindItem", `<m:FindItem Traversal="Shallow">`+ewsShape()+
		fmt.Sprintf(`<m:CalendarView MaxEntriesReturned="%d" StartDate="%s" EndDate="%s"/>`, ewsMaxEntries, ewsTime(from), ewsTime(to))+
		`<m:ParentFolderIds>`+ewsFolder(mb)+`</m:ParentFolderIds></m:FindItem>`)
	if err != nil {
		return nil, c.classify(err, false)
	}
	if strings.EqualFold(m.Root.Last, "false") {
		return nil, fmt.Errorf("calendar: the window holds more than %d appointments; read a shorter window", ewsMaxEntries)
	}
	var out []Event
	for _, it := range m.Root.Items {
		ev, err := it.event()
		if err != nil {
			return nil, err
		}
		if ev.overlaps(from, to) {
			out = append(out, ev)
		}
	}
	return out, nil
}

// item reads one item.
func (c *ews) item(ctx context.Context, mb, id string, write bool) (ewsItem, error) {
	if strings.TrimSpace(id) == "" {
		return ewsItem{}, &Error{Kind: KindBookingNotFound, Message: "the booking id is empty"}
	}
	m, err := c.call(ctx, "GetItem", `<m:GetItem>`+ewsShape()+`<m:ItemIds>`+ewsItemID(id)+`</m:ItemIds></m:GetItem>`)
	if err != nil {
		return ewsItem{}, c.onItem(ctx, mb, err, write)
	}
	if len(m.Items) != 1 {
		return ewsItem{}, fmt.Errorf("calendar: EWS answered GetItem with %d items", len(m.Items))
	}
	return m.Items[0], nil
}

// Create implements [Backend]. EWS has no idempotency token, so the create key is
// written into an extended property and the slot is searched for it first: a repeated
// delivery finds its own reservation (ADR-0552, `check`). A reservation the conflict
// rule gave up was deleted, so a later delivery creates it afresh and the rule runs on
// it again. The elements follow the order the EWS schema prescribes, which Exchange
// enforces.
func (c *ews) Create(ctx context.Context, cal string, ne NewEvent) (Event, error) {
	mb, err := ewsMailbox(cal)
	if err != nil {
		return Event{}, err
	}
	existing, err := c.Events(ctx, cal, ne.Start, ne.End)
	if err != nil {
		return Event{}, err
	}
	for _, e := range existing {
		if ne.Key != "" && e.Key == ne.Key {
			return e, nil
		}
	}
	item := `<t:CalendarItem><t:Subject>` + xmlText(ne.Subject) + `</t:Subject><t:ReminderIsSet>false</t:ReminderIsSet>` +
		`<t:ExtendedProperty>` + ewsKeyURI() + `<t:Value>` + xmlText(ne.Key) + `</t:Value></t:ExtendedProperty>` +
		`<t:Start>` + ewsTime(ne.Start) + `</t:Start><t:End>` + ewsTime(ne.End) + `</t:End>` +
		`<t:LegacyFreeBusyStatus>Busy</t:LegacyFreeBusyStatus></t:CalendarItem>`
	m, err := c.call(ctx, "CreateItem", `<m:CreateItem SendMeetingInvitations="SendToNone"><m:SavedItemFolderId>`+ewsFolder(mb)+
		`</m:SavedItemFolderId><m:Items>`+item+`</m:Items></m:CreateItem>`)
	if err != nil {
		return Event{}, c.classify(err, true)
	}
	if len(m.Items) != 1 || m.Items[0].ItemID.ID == "" {
		return Event{}, fmt.Errorf("calendar: EWS answered CreateItem without the item's id")
	}
	return Event{ID: m.Items[0].ItemID.ID, Key: ne.Key, Start: ne.Start, End: ne.End, Busy: true}, nil
}

// Update implements [Backend]. The item is read first, so a booking cancelled in the
// meantime is told apart; subject, body and the required attendees are then set, and
// Exchange sends the attendees an invitation from the booking mailbox — which needs the
// service account to hold Send As or Send on Behalf for it.
func (c *ews) Update(ctx context.Context, cal, id string, d Details) (Event, error) {
	mb, err := ewsMailbox(cal)
	if err != nil {
		return Event{}, err
	}
	it, err := c.item(ctx, mb, id, true)
	if err != nil {
		return Event{}, err
	}
	if it.IsCancelled {
		return Event{}, &Error{Kind: KindBookingNotFound, Message: "the booking was cancelled in the calendar"}
	}
	var sets strings.Builder
	if d.Subject != "" {
		sets.WriteString(`<t:SetItemField><t:FieldURI FieldURI="item:Subject"/><t:CalendarItem><t:Subject>` + xmlText(d.Subject) + `</t:Subject></t:CalendarItem></t:SetItemField>`)
	}
	if d.Body != "" {
		sets.WriteString(`<t:SetItemField><t:FieldURI FieldURI="item:Body"/><t:CalendarItem><t:Body BodyType="Text">` + xmlText(d.Body) + `</t:Body></t:CalendarItem></t:SetItemField>`)
	}
	send := "SendToNone"
	if len(d.Attendees) > 0 {
		send = "SendToAllAndSaveCopy"
		sets.WriteString(`<t:SetItemField><t:FieldURI FieldURI="calendar:RequiredAttendees"/><t:CalendarItem><t:RequiredAttendees>`)
		for _, a := range d.Attendees {
			sets.WriteString(`<t:Attendee><t:Mailbox>`)
			if a.Name != "" {
				sets.WriteString(`<t:Name>` + xmlText(a.Name) + `</t:Name>`)
			}
			sets.WriteString(`<t:EmailAddress>` + xmlText(a.Email) + `</t:EmailAddress></t:Mailbox></t:Attendee>`)
		}
		sets.WriteString(`</t:RequiredAttendees></t:CalendarItem></t:SetItemField>`)
	}
	ev, err := it.event()
	if err != nil {
		return Event{}, err
	}
	if sets.Len() == 0 {
		return ev, nil
	}
	ref := `<t:ItemId Id="` + xmlAttr(it.ItemID.ID) + `" ChangeKey="` + xmlAttr(it.ItemID.ChangeKey) + `"/>`
	if _, err := c.call(ctx, "UpdateItem", `<m:UpdateItem ConflictResolution="AlwaysOverwrite" SendMeetingInvitationsOrCancellations="`+send+`">`+
		`<m:ItemChanges><t:ItemChange>`+ref+`<t:Updates>`+sets.String()+`</t:Updates></t:ItemChange></m:ItemChanges></m:UpdateItem>`); err != nil {
		return Event{}, c.onItem(ctx, mb, err, true)
	}
	return ev, nil
}

// Delete implements [Backend]. The item moves to the mailbox's deleted items, and an
// attendee of a confirmed booking is sent the cancellation. An item already gone is
// success.
func (c *ews) Delete(ctx context.Context, cal, id string) error {
	mb, err := ewsMailbox(cal)
	if err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return nil
	}
	_, err = c.call(ctx, "DeleteItem", `<m:DeleteItem DeleteType="MoveToDeletedItems" SendMeetingCancellations="SendToAllAndSaveCopy"><m:ItemIds>`+ewsItemID(id)+`</m:ItemIds></m:DeleteItem>`)
	if err == nil {
		return nil
	}
	e := c.onItem(ctx, mb, err, true)
	if kindOf(e) == KindBookingNotFound {
		return nil
	}
	return e
}

// Get implements [Backend]. A cancelled item is one that no longer exists.
func (c *ews) Get(ctx context.Context, cal, id string) (Event, bool, error) {
	mb, err := ewsMailbox(cal)
	if err != nil {
		return Event{}, false, err
	}
	it, err := c.item(ctx, mb, id, false)
	if kindOf(err) == KindBookingNotFound {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, err
	}
	ev, err := it.event()
	if err != nil {
		return Event{}, false, err
	}
	return ev, !it.IsCancelled, nil
}
