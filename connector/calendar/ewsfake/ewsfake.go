// Package ewsfake is an in-process stand-in for the part of Exchange Web Services the
// calendar Worker Type's EWS backend calls: FindItem with a calendar view, GetItem,
// CreateItem, UpdateItem, DeleteItem and GetFolder on the default calendar of a
// mailbox reached by delegate access, behind NTLM. It exists for tests and for the
// conformance suites (ADR-0483), so a record verifies anywhere, without an Exchange
// server.
//
// What it answers is written from Microsoft's EWS reference, not observed from a
// server: the response codes, the SOAP shape and the element order of a calendar item.
// What it does on purpose:
//
//   - It authenticates every request with NTLMv2 and checks the client's response
//     against the password, or with Basic when the server is set to it, and answers 401
//     otherwise — so a wrong password fails a test rather than passing it.
//   - It refuses an item shape other than IdOnly with times, availability,
//     cancellation and extended properties, so a backend that starts reading other
//     people's subjects fails its tests.
//   - It refuses a calendar item whose elements are out of the schema's order, as
//     Exchange's schema validation does.
//   - It expands a weekly series only in a calendar view and keeps no idempotency of its
//     own: EWS has none, and the backend must find its reservation by the key.
//   - It answers every read with everything written before it, the read-after-write
//     property the conflict rule needs and the record cannot yet confirm of Exchange.
package ewsfake

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The service account every fake knows.
const (
	Username = `CONTOSO\svc-booking`
	Password = "Exchange-2019!"
	Domain   = "CONTOSO"
	// Path is the EWS path below the fake's root.
	Path = "/EWS/Exchange.asmx"
)

// Bundle is the credential bundle for the fake's account, with the authentication
// named ("" for the default).
func Bundle(authentication string) string {
	b := `{"provider":"exchange-ews","username":"CONTOSO\\svc-booking","password":"Exchange-2019!"`
	if authentication != "" {
		b += `,"authentication":"` + authentication + `"`
	}
	return b + "}"
}

// Busy is an appointment a mailbox already holds. FreeBusy is its availability ("Busy"
// when empty). Weekly, above one, makes it a series of that many weekly occurrences.
type Busy struct {
	Start    time.Time
	End      time.Time
	FreeBusy string
	Weekly   int
}

// Mailbox is one mailbox the service account has delegate access to. ReadOnly makes
// every write ErrorAccessDenied (Reviewer on the calendar); SendAsDenied makes
// inviting attendees ErrorSendAsDenied.
type Mailbox struct {
	ReadOnly     bool
	SendAsDenied bool
	Busy         []Busy
}

// Attendee is one invited person, as the fake recorded it.
type Attendee struct {
	Email string
	Name  string
}

// Item is one calendar item as a test inspects it.
type Item struct {
	ID         string
	Key        string
	Start, End time.Time
	FreeBusy   string
	Subject    string
	Body       string
	Attendees  []Attendee
	// Invited says whether the attendees were sent an invitation.
	Invited bool
	weekly  int
}

// Server is the fake.
type Server struct {
	srv       *httptest.Server
	mu        sync.Mutex
	mailboxes map[string]*mailbox
	next      int
	challenge [8]byte
	// Basic makes the fake offer and accept Basic instead of NTLM.
	Basic bool
	// Requests counts the SOAP operations served, by name.
	Requests map[string]int
}

type mailbox struct {
	spec  Mailbox
	items []*Item
}

// New starts the fake with the mailboxes named, by SMTP address.
func New(boxes map[string]Mailbox) *Server {
	s := &Server{mailboxes: map[string]*mailbox{}, Requests: map[string]int{}}
	_, _ = rand.Read(s.challenge[:])
	names := make([]string, 0, len(boxes))
	for n := range boxes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		mb := &mailbox{spec: boxes[n]}
		for _, b := range boxes[n].Busy {
			fb := b.FreeBusy
			if fb == "" {
				fb = "Busy"
			}
			mb.items = append(mb.items, &Item{ID: s.newID(), Start: b.Start.UTC(), End: b.End.UTC(), FreeBusy: fb, Subject: "Privat", weekly: b.Weekly})
		}
		s.mailboxes[strings.ToLower(n)] = mb
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

func (s *Server) newID() string {
	s.next++
	return base64.StdEncoding.EncodeToString([]byte("AAMkAD-fake-item-" + strconv.Itoa(s.next)))
}

// Close stops the fake.
func (s *Server) Close() { s.srv.Close() }

// URL is the EWS endpoint the backend is pointed at.
func (s *Server) URL() string { return s.srv.URL + Path }

// HTTPClient reaches the fake.
func (s *Server) HTTPClient() *http.Client { return s.srv.Client() }

// Items returns a mailbox's items in creation order.
func (s *Server) Items(address string) []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	mb := s.mailboxes[strings.ToLower(address)]
	if mb == nil {
		return nil
	}
	out := make([]Item, len(mb.items))
	for i, it := range mb.items {
		out[i] = *it
		out[i].Attendees = append([]Attendee(nil), it.Attendees...)
	}
	return out
}

// authenticated runs the server's half of the handshake and reports whether the
// request carries a valid credential; when it does not, the answer is written.
func (s *Server) authenticated(w http.ResponseWriter, r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if s.Basic {
		u, p, ok := r.BasicAuth()
		if ok && u == Username && p == Password {
			return true
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="exch01"`)
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	tok := ntlmToken(auth)
	switch ntlmMessageType(tok) {
	case 1:
		scheme, _, _ := strings.Cut(auth, " ")
		w.Header().Set("WWW-Authenticate", scheme+" "+base64.StdEncoding.EncodeToString(challengeMessage(s.challenge, Domain)))
		w.WriteHeader(http.StatusUnauthorized)
		return false
	case 3:
		if verifyAuthenticate(tok, s.challenge, Username, Password) {
			return true
		}
	}
	w.Header().Add("WWW-Authenticate", "Negotiate")
	w.Header().Add("WWW-Authenticate", "NTLM")
	w.WriteHeader(http.StatusUnauthorized)
	return false
}

// node is a generic XML element of a request.
type node struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Nodes   []node     `xml:",any"`
	Text    string     `xml:",chardata"`
}

func (n *node) child(name string) *node {
	if n == nil {
		return nil
	}
	for i := range n.Nodes {
		if n.Nodes[i].XMLName.Local == name {
			return &n.Nodes[i]
		}
	}
	return nil
}

func (n *node) path(names ...string) *node {
	cur := n
	for _, name := range names {
		if cur = cur.child(name); cur == nil {
			return nil
		}
	}
	return cur
}

func (n *node) attr(name string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func (n *node) text() string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Text)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != Path || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if !s.authenticated(w, r) {
		_, _ = io.Copy(io.Discard, r.Body)
		return
	}
	var env node
	if err := xml.NewDecoder(r.Body).Decode(&env); err != nil {
		fault(w, "ErrorSchemaValidation")
		return
	}
	if env.path("Header", "RequestServerVersion").attr("Version") == "" {
		fault(w, "ErrorInvalidServerVersion")
		return
	}
	if env.path("Header", "TimeZoneContext", "TimeZoneDefinition").attr("Id") != "UTC" {
		fault(w, "ErrorTimeZone") // the backend always works in UTC
		return
	}
	body := env.child("Body")
	if body == nil || len(body.Nodes) != 1 {
		fault(w, "ErrorSchemaValidation")
		return
	}
	op := &body.Nodes[0]
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests[op.XMLName.Local]++
	var inner, code string
	switch op.XMLName.Local {
	case "FindItem":
		inner, code = s.findItem(op)
	case "GetItem":
		inner, code = s.getItem(op)
	case "CreateItem":
		inner, code = s.createItem(op)
	case "UpdateItem":
		inner, code = s.updateItem(op)
	case "DeleteItem":
		inner, code = s.deleteItem(op)
	case "GetFolder":
		inner, code = s.getFolder(op)
	default:
		fault(w, "ErrorInvalidRequest")
		return
	}
	answer(w, op.XMLName.Local, inner, code)
}

func fault(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode xmlns:a="http://schemas.microsoft.com/exchange/services/2006/types">a:%s</faultcode><faultstring xml:lang="en-US">The request failed (fake).</faultstring><detail><e:ResponseCode xmlns:e="http://schemas.microsoft.com/exchange/services/2006/errors">%s</e:ResponseCode></detail></s:Fault></s:Body></s:Envelope>`, code, code)
}

func answer(w http.ResponseWriter, op, inner, code string) {
	class := "Success"
	if code == "" {
		code = "NoError"
	} else {
		class = "Error"
		inner = `<m:MessageText>The request failed (fake).</m:MessageText><m:ResponseCode>` + code + `</m:ResponseCode>`
	}
	if class == "Success" {
		inner = `<m:ResponseCode>NoError</m:ResponseCode>` + inner
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Header><h:ServerVersionInfo MajorVersion="15" MinorVersion="2" MajorBuildNumber="1544" MinorBuildNumber="4" xmlns:h="http://schemas.microsoft.com/exchange/services/2006/types"/></s:Header><s:Body><m:%sResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages" xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types"><m:ResponseMessages><m:%sResponseMessage ResponseClass="%s">%s</m:%sResponseMessage></m:ResponseMessages></m:%sResponse></s:Body></s:Envelope>`,
		op, op, class, inner, op, op)
}

// allowedFields are the properties the backend may ask for.
var allowedFields = map[string]bool{"calendar:Start": true, "calendar:End": true, "calendar:LegacyFreeBusyStatus": true, "calendar:IsCancelled": true}

// shapeOK checks an item shape: IdOnly plus the allowed fields and extended properties.
func shapeOK(op *node) bool {
	shape := op.child("ItemShape")
	if shape == nil || shape.path("BaseShape").text() != "IdOnly" {
		return false
	}
	if add := shape.child("AdditionalProperties"); add != nil {
		for _, f := range add.Nodes {
			switch f.XMLName.Local {
			case "FieldURI":
				if !allowedFields[f.attr("FieldURI")] {
					return false
				}
			case "ExtendedFieldURI":
			default:
				return false
			}
		}
	}
	return true
}

// folderMailbox is the mailbox a DistinguishedFolderId names; "" when it names none.
func folderMailbox(f *node) (string, bool) {
	if f == nil {
		return "", false
	}
	d := f.child("DistinguishedFolderId")
	if d == nil || d.attr("Id") != "calendar" {
		return "", false
	}
	return strings.ToLower(d.path("Mailbox", "EmailAddress").text()), true
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func itemXML(id string, it *Item, start, end time.Time) string {
	x := `<t:CalendarItem><t:ItemId Id="` + escape(id) + `" ChangeKey="DwAAABYA"/>`
	if it.Key != "" {
		x += `<t:ExtendedProperty><t:ExtendedFieldURI PropertySetId="8f3c2d0e-5a41-4b7e-9b1a-a71a5c0b2e10" PropertyName="AtlasCreateKey" PropertyType="String"/><t:Value>` + escape(it.Key) + `</t:Value></t:ExtendedProperty>`
	}
	x += `<t:Start>` + ts(start) + `</t:Start><t:End>` + ts(end) + `</t:End><t:LegacyFreeBusyStatus>` + it.FreeBusy + `</t:LegacyFreeBusyStatus><t:IsCancelled>false</t:IsCancelled></t:CalendarItem>`
	return x
}

func (s *Server) findItem(op *node) (string, string) {
	if !shapeOK(op) {
		return "", "ErrorInvalidPropertyRequest"
	}
	addr, ok := folderMailbox(op.child("ParentFolderIds"))
	if !ok {
		return "", "ErrorInvalidRequest"
	}
	mb := s.mailboxes[addr]
	if mb == nil {
		return "", "ErrorNonExistentMailbox"
	}
	view := op.child("CalendarView")
	if view == nil {
		return "", "ErrorInvalidRequest" // the backend always reads a view; a plain FindItem would answer series
	}
	from, err1 := time.Parse(time.RFC3339, view.attr("StartDate"))
	to, err2 := time.Parse(time.RFC3339, view.attr("EndDate"))
	limit, err3 := strconv.Atoi(view.attr("MaxEntriesReturned"))
	if err1 != nil || err2 != nil || err3 != nil || !from.Before(to) {
		return "", "ErrorInvalidRequest"
	}
	var items []string
	last := true
	for _, it := range mb.items {
		n := max(it.weekly, 1)
		for k := 0; k < n; k++ {
			start, end := it.Start.AddDate(0, 0, 7*k), it.End.AddDate(0, 0, 7*k)
			if !(start.Before(to) && from.Before(end)) {
				continue
			}
			if len(items) == limit {
				last = false
				break
			}
			id := it.ID
			if it.weekly > 1 {
				id = it.ID + "-occ-" + strconv.Itoa(k)
			}
			items = append(items, itemXML(id, it, start, end))
		}
	}
	return fmt.Sprintf(`<m:RootFolder TotalItemsInView="%d" IncludesLastItemInRange="%t"><t:Items>%s</t:Items></m:RootFolder>`, len(items), last, strings.Join(items, "")), ""
}

func (s *Server) find(id string) (*mailbox, int) {
	for _, mb := range s.mailboxes {
		for i, it := range mb.items {
			if it.ID == id {
				return mb, i
			}
		}
	}
	return nil, -1
}

func (s *Server) getItem(op *node) (string, string) {
	if !shapeOK(op) {
		return "", "ErrorInvalidPropertyRequest"
	}
	id := op.path("ItemIds", "ItemId").attr("Id")
	mb, i := s.find(id)
	if mb == nil {
		return "", "ErrorItemNotFound"
	}
	it := mb.items[i]
	return `<m:Items>` + itemXML(it.ID, it, it.Start, it.End) + `</m:Items>`, ""
}

func (s *Server) getFolder(op *node) (string, string) {
	addr, ok := folderMailbox(op.child("FolderIds"))
	if !ok {
		return "", "ErrorInvalidRequest"
	}
	if s.mailboxes[addr] == nil {
		return "", "ErrorNonExistentMailbox"
	}
	return `<m:Folders><t:CalendarFolder><t:FolderId Id="AAMkAD-fake-calendar" ChangeKey="AgAAAA=="/></t:CalendarFolder></m:Folders>`, ""
}

// calendarItemOrder is the order of the CalendarItem elements the backend writes, as
// the EWS schema sequence (ItemType, then CalendarItemType) prescribes it.
var calendarItemOrder = []string{"Subject", "Body", "ReminderIsSet", "ExtendedProperty", "Start", "End", "LegacyFreeBusyStatus", "RequiredAttendees"}

func inSchemaOrder(item *node) bool {
	pos := -1
	for _, c := range item.Nodes {
		i := -1
		for j, n := range calendarItemOrder {
			if n == c.XMLName.Local {
				i = j
			}
		}
		if i < pos || i < 0 {
			return false
		}
		pos = i
	}
	return true
}

func (s *Server) createItem(op *node) (string, string) {
	if op.attr("SendMeetingInvitations") == "" {
		return "", "ErrorSchemaValidation" // required on a calendar item
	}
	addr, ok := folderMailbox(op.child("SavedItemFolderId"))
	if !ok {
		return "", "ErrorInvalidRequest"
	}
	mb := s.mailboxes[addr]
	if mb == nil {
		return "", "ErrorNonExistentMailbox"
	}
	if mb.spec.ReadOnly {
		return "", "ErrorAccessDenied"
	}
	ci := op.path("Items", "CalendarItem")
	if ci == nil || !inSchemaOrder(ci) {
		return "", "ErrorSchemaValidation"
	}
	start, err1 := time.Parse(time.RFC3339, ci.path("Start").text())
	end, err2 := time.Parse(time.RFC3339, ci.path("End").text())
	if err1 != nil || err2 != nil || !start.Before(end) {
		return "", "ErrorCalendarEndDateIsEarlierThanStartDate"
	}
	it := &Item{ID: s.newID(), Start: start.UTC(), End: end.UTC(), FreeBusy: ci.path("LegacyFreeBusyStatus").text(), Subject: ci.path("Subject").text()}
	if it.FreeBusy == "" {
		it.FreeBusy = "Busy"
	}
	if x := ci.child("ExtendedProperty"); x != nil && x.path("ExtendedFieldURI").attr("PropertyName") == "AtlasCreateKey" {
		it.Key = x.path("Value").text()
	}
	mb.items = append(mb.items, it)
	return `<m:Items><t:CalendarItem><t:ItemId Id="` + escape(it.ID) + `" ChangeKey="DwAAABYA"/></t:CalendarItem></m:Items>`, ""
}

func (s *Server) updateItem(op *node) (string, string) {
	change := op.path("ItemChanges", "ItemChange")
	if change == nil {
		return "", "ErrorInvalidRequest"
	}
	mb, i := s.find(change.path("ItemId").attr("Id"))
	if mb == nil {
		return "", "ErrorItemNotFound"
	}
	if mb.spec.ReadOnly {
		return "", "ErrorAccessDenied"
	}
	send := op.attr("SendMeetingInvitationsOrCancellations")
	if send == "" {
		return "", "ErrorSchemaValidation"
	}
	it := *mb.items[i]
	for _, set := range change.path("Updates").Nodes {
		uri := set.path("FieldURI").attr("FieldURI")
		ci := set.child("CalendarItem")
		if set.XMLName.Local != "SetItemField" || ci == nil {
			return "", "ErrorInvalidRequest"
		}
		switch uri {
		case "item:Subject":
			it.Subject = ci.path("Subject").text()
		case "item:Body":
			it.Body = ci.path("Body").text()
		case "calendar:RequiredAttendees":
			it.Attendees = nil
			for _, a := range ci.path("RequiredAttendees").Nodes {
				it.Attendees = append(it.Attendees, Attendee{Email: a.path("Mailbox", "EmailAddress").text(), Name: a.path("Mailbox", "Name").text()})
			}
		default:
			return "", "ErrorInvalidPropertySet"
		}
	}
	if len(it.Attendees) > 0 && send != "SendToNone" {
		if mb.spec.SendAsDenied {
			return "", "ErrorSendAsDenied"
		}
		it.Invited = true
	}
	*mb.items[i] = it
	return `<m:Items><t:CalendarItem><t:ItemId Id="` + escape(it.ID) + `" ChangeKey="DwAAABYB"/></t:CalendarItem></m:Items>`, ""
}

func (s *Server) deleteItem(op *node) (string, string) {
	if op.attr("DeleteType") == "" || op.attr("SendMeetingCancellations") == "" {
		return "", "ErrorSchemaValidation"
	}
	mb, i := s.find(op.path("ItemIds", "ItemId").attr("Id"))
	if mb == nil {
		return "", "ErrorItemNotFound"
	}
	if mb.spec.ReadOnly {
		return "", "ErrorAccessDenied"
	}
	mb.items = append(mb.items[:i], mb.items[i+1:]...)
	return "", ""
}
