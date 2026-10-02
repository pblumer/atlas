package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// graphMailbox is a Microsoft 365 mailbox read and changed through Graph, as the
// worker's sender mailbox, with the worker's token. Whether the token may read that
// mailbox — and only that one — is decided in Entra and Exchange Online, not here
// (ADR-draft-mailbox-worker): Mail.Read or Mail.ReadWrite as an application
// permission reaches every mailbox in the tenant until RBAC for Applications confines
// it.
//
// Every request asks for immutable ids. A Graph message id otherwise changes when the
// message moves folder, and the id a watch published is what a later task addresses —
// a task that files a message and then marks it read would find it gone.
type graphMailbox struct{ c *GraphClient }

// Mailbox answers the Graph mailbox of this worker's sender.
func (c *GraphClient) Mailbox() (Mailbox, error) {
	if strings.TrimSpace(c.sender) == "" {
		return nil, fmt.Errorf("mail: graph: the worker names no sender mailbox, so there is no mailbox to read")
	}
	return graphMailbox{c: c}, nil
}

// graphPageMax is the most messages Graph answers in one page.
const graphPageMax = 1000

// graphSelect is what a message read asks Graph for. internetMessageHeaders carries
// Authentication-Results and References; the body is added only when asked for.
const graphSelect = "id,internetMessageId,subject,from,replyTo,toRecipients,ccRecipients," +
	"receivedDateTime,isRead,hasAttachments,parentFolderId,internetMessageHeaders"

func (m graphMailbox) base() string {
	return strings.TrimRight(m.c.baseURL, "/") + "/users/" + url.PathEscape(m.c.sender)
}

// graphFolder maps a folder name onto Graph: INBOX — the name every provider shares —
// is Graph's well-known "inbox"; anything else is passed as written, which is either
// another well-known name (archive, deleteditems, …) or a folder id.
func graphFolder(folder string) string {
	if f := strings.TrimSpace(folder); f != "" && !strings.EqualFold(f, DefaultFolder) {
		return f
	}
	return "inbox"
}

func graphPrefer(includeBody bool) string {
	p := `IdType="ImmutableId"`
	if includeBody {
		p += `, outlook.body-content-type="text"`
	}
	return p
}

// odataQuery renders query options with %20 for spaces: an OData filter is not a form,
// and "+" is not a space to every parser that reads it.
func odataQuery(opts [][2]string) string {
	parts := make([]string, 0, len(opts))
	for _, o := range opts {
		parts = append(parts, o[0]+"="+strings.ReplaceAll(url.QueryEscape(o[1]), "+", "%20"))
	}
	return strings.Join(parts, "&")
}

type graphEmail struct {
	EmailAddress struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

type graphMsg struct {
	ID                string       `json:"id"`
	InternetMessageID string       `json:"internetMessageId"`
	Subject           string       `json:"subject"`
	From              *graphEmail  `json:"from"`
	ReplyTo           []graphEmail `json:"replyTo"`
	To                []graphEmail `json:"toRecipients"`
	Cc                []graphEmail `json:"ccRecipients"`
	ReceivedDateTime  string       `json:"receivedDateTime"`
	IsRead            bool         `json:"isRead"`
	HasAttachments    bool         `json:"hasAttachments"`
	ParentFolderID    string       `json:"parentFolderId"`
	Headers           []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"internetMessageHeaders"`
	Body *struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
}

type graphMsgPage struct {
	Value []graphMsg `json:"value"`
}

func graphAddrs(list []graphEmail) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		if addr := strings.ToLower(strings.TrimSpace(a.EmailAddress.Address)); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

func graphTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// envelope turns a Graph message into an Envelope, fetching the attachment list when
// the message has one.
func (m graphMailbox) envelope(ctx context.Context, g graphMsg, folder string, includeBody bool) (Envelope, error) {
	e := Envelope{
		ID:                g.ID,
		InternetMessageID: g.InternetMessageID,
		Folder:            folder,
		Subject:           g.Subject,
		To:                graphAddrs(g.To),
		Cc:                graphAddrs(g.Cc),
		ReceivedAt:        graphTime(g.ReceivedDateTime),
		Unread:            !g.IsRead,
	}
	if e.Folder == "" {
		e.Folder = g.ParentFolderID
	}
	if g.From != nil {
		e.From = strings.ToLower(strings.TrimSpace(g.From.EmailAddress.Address))
		e.FromName = g.From.EmailAddress.Name
	}
	if r := graphAddrs(g.ReplyTo); len(r) > 0 {
		e.ReplyTo = r[0]
	}
	var auth []string
	for _, h := range g.Headers {
		switch strings.ToLower(h.Name) {
		case "authentication-results":
			auth = append(auth, h.Value)
		case "references":
			e.References = strings.TrimSpace(h.Value)
		}
	}
	e.Auth = parseAuthResults(auth)
	if includeBody && g.Body != nil {
		text := g.Body.Content
		if strings.EqualFold(g.Body.ContentType, "html") {
			text = htmlToText(text)
		}
		e.Body, e.BodyTruncated = capBody(text)
	}
	if g.HasAttachments {
		atts, err := m.attachments(ctx, g.ID)
		if err != nil {
			return Envelope{}, err
		}
		e.Attachments = atts
	}
	return e, nil
}

// attachments lists a message's attachments by name, type and size. The content is
// never asked for: $select keeps contentBytes out of the answer altogether.
func (m graphMailbox) attachments(ctx context.Context, id string) ([]Attachment, error) {
	var page struct {
		Value []struct {
			Name        string `json:"name"`
			ContentType string `json:"contentType"`
			Size        int64  `json:"size"`
		} `json:"value"`
	}
	u := m.base() + "/messages/" + url.PathEscape(id) + "/attachments?" + odataQuery([][2]string{{"$select", "name,contentType,size"}})
	if err := apiJSON(ctx, m.c.http, m.c.tokens, http.MethodGet, u, graphPrefer(false), nil, &page, "graph attachments"); err != nil {
		return nil, err
	}
	out := make([]Attachment, 0, len(page.Value))
	for _, a := range page.Value {
		out = append(out, Attachment{Name: a.Name, ContentType: strings.ToLower(a.ContentType), Size: a.Size})
	}
	return out, nil
}

func (m graphMailbox) list(ctx context.Context, folder string, opts [][2]string, includeBody bool) ([]graphMsg, error) {
	sel := graphSelect
	if includeBody {
		sel += ",body"
	}
	opts = append(opts, [2]string{"$select", sel})
	u := m.base() + "/mailFolders/" + url.PathEscape(graphFolder(folder)) + "/messages?" + odataQuery(opts)
	var page graphMsgPage
	if err := apiJSON(ctx, m.c.http, m.c.tokens, http.MethodGet, u, graphPrefer(includeBody), nil, &page, "graph list messages"); err != nil {
		return nil, err
	}
	return page.Value, nil
}

// --- the watch ---

// graphCursor is "<receivedDateTime>|<id>,<id>…": the newest receive time delivered,
// and the ids already delivered at exactly that time. Graph's receive time has a
// resolution of a second, so a cursor that only said "after this time" would either
// drop the second of two messages arriving in the same second or deliver the first one
// again — and a re-delivery, though the engine discards it, is still charged against
// the watch's hourly budget.
type graphCursor struct {
	at   time.Time
	seen map[string]bool
}

func parseGraphCursor(s string) (graphCursor, bool) {
	ts, ids, _ := strings.Cut(strings.TrimSpace(s), "|")
	at := graphTime(ts)
	if at.IsZero() {
		return graphCursor{}, false
	}
	c := graphCursor{at: at, seen: map[string]bool{}}
	for _, id := range strings.Split(ids, ",") {
		if id != "" {
			c.seen[id] = true
		}
	}
	return c, true
}

func (c graphCursor) String() string {
	ids := make([]string, 0, len(c.seen))
	for id := range c.seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return c.at.UTC().Format(time.RFC3339) + "|" + strings.Join(ids, ",")
}

func (m graphMailbox) WatchTip(ctx context.Context, folder string) (string, error) {
	msgs, err := m.list(ctx, folder, [][2]string{{"$orderby", "receivedDateTime desc"}, {"$top", "1"}}, false)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return "", nil // an empty folder: everything that arrives is new
	}
	at := graphTime(msgs[0].ReceivedDateTime)
	if at.IsZero() {
		return "", nil
	}
	return graphCursor{at: at, seen: map[string]bool{msgs[0].ID: true}}.String(), nil
}

func (m graphMailbox) WatchSince(ctx context.Context, req WatchRequest) (WatchPage, error) {
	cur, hasCursor := parseGraphCursor(req.Cursor)
	limit := req.Limit
	if limit <= 0 || limit > graphPageMax {
		limit = graphPageMax
	}
	opts := [][2]string{{"$orderby", "receivedDateTime asc"}}
	top := limit
	if hasCursor {
		opts = append(opts, [2]string{"$filter", "receivedDateTime ge " + cur.at.Format(time.RFC3339)})
		top += len(cur.seen) // the boundary second's delivered messages come back first
	}
	if top > graphPageMax {
		top = graphPageMax
	}
	opts = append(opts, [2]string{"$top", strconv.Itoa(top)})
	msgs, err := m.list(ctx, req.Folder, opts, req.IncludeBody)
	if err != nil {
		return WatchPage{}, err
	}
	var page WatchPage
	next := cur
	if !hasCursor {
		next = graphCursor{seen: map[string]bool{}}
	}
	for _, g := range msgs {
		if len(page.Items) >= limit {
			break
		}
		at := graphTime(g.ReceivedDateTime)
		if at.IsZero() || g.ID == "" {
			continue // no time, no sequence; no id, nothing to key a mark on
		}
		if hasCursor && (at.Before(cur.at) || (at.Equal(cur.at) && cur.seen[g.ID])) {
			continue
		}
		e, err := m.envelope(ctx, g, req.Folder, req.IncludeBody)
		if err != nil {
			return WatchPage{}, err
		}
		// One mark per message: two messages can share a receive time, and a scalar
		// mark on it would drop the second (ADR-draft-mailbox-worker).
		page.Items = append(page.Items, Received{MarkKey: g.ID, Seq: uint64(at.UnixMilli()), Envelope: e})
		if at.After(next.at) {
			next = graphCursor{at: at, seen: map[string]bool{}}
		}
		next.seen[g.ID] = true
	}
	if len(page.Items) > 0 {
		page.Cursor = next.String()
	}
	return page, nil
}

// --- operations ---

func (m graphMailbox) List(ctx context.Context, req ListRequest) ([]Envelope, error) {
	req = req.normalized()
	opts := [][2]string{{"$orderby", "receivedDateTime desc"}, {"$top", strconv.Itoa(req.MaxResults)}}
	if req.UnreadOnly {
		// Graph refuses a filter that does not lead with the property it is ordered by,
		// so the unread condition rides behind a receive-time condition that holds for
		// every message.
		opts = append(opts, [2]string{"$filter", "receivedDateTime ge 1900-01-01T00:00:00Z and isRead eq false"})
	}
	msgs, err := m.list(ctx, req.Folder, opts, req.IncludeBody)
	if err != nil {
		return nil, err
	}
	out := make([]Envelope, 0, len(msgs))
	for _, g := range msgs {
		e, err := m.envelope(ctx, g, req.Folder, req.IncludeBody)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (m graphMailbox) message(id string) string {
	return m.base() + "/messages/" + url.PathEscape(strings.TrimSpace(id))
}

func (m graphMailbox) Get(ctx context.Context, id string, includeBody bool) (Envelope, error) {
	sel := graphSelect
	if includeBody {
		sel += ",body"
	}
	var g graphMsg
	u := m.message(id) + "?" + odataQuery([][2]string{{"$select", sel}})
	if err := apiJSON(ctx, m.c.http, m.c.tokens, http.MethodGet, u, graphPrefer(includeBody), nil, &g, "graph get message"); err != nil {
		return Envelope{}, err
	}
	return m.envelope(ctx, g, "", includeBody)
}

func (m graphMailbox) Move(ctx context.Context, id, destination string) (string, error) {
	var moved struct {
		ID string `json:"id"`
	}
	body := map[string]string{"destinationId": graphFolder(destination)}
	if err := apiJSON(ctx, m.c.http, m.c.tokens, http.MethodPost, m.message(id)+"/move", graphPrefer(false), body, &moved, "graph move"); err != nil {
		return "", err
	}
	return moved.ID, nil
}

func (m graphMailbox) SetRead(ctx context.Context, id string, read bool) error {
	return apiJSON(ctx, m.c.http, m.c.tokens, http.MethodPatch, m.message(id), graphPrefer(false),
		map[string]bool{"isRead": read}, nil, "graph mark read")
}

// Delete moves the message to Deleted Items, said explicitly rather than left to what
// DELETE does with a message, so it is recoverable the way a person's own delete is.
func (m graphMailbox) Delete(ctx context.Context, id string) error {
	_, err := m.Move(ctx, id, "deleteditems")
	return err
}

// Reply uses Graph's own reply action, which threads and quotes the way Outlook does.
// Graph takes one comment, so an HTML body wins over the plain one, as it does for a
// send.
func (m graphMailbox) Reply(ctx context.Context, id string, r Reply) error {
	comment := r.Body
	if strings.TrimSpace(r.HTML) != "" {
		comment = r.HTML
	}
	if strings.TrimSpace(comment) == "" {
		return fmt.Errorf("mail: a reply needs a body or an HTML body")
	}
	return apiJSON(ctx, m.c.http, m.c.tokens, http.MethodPost, m.message(id)+"/reply", graphPrefer(false),
		map[string]string{"comment": comment}, nil, "graph reply")
}

// apiJSON performs one authenticated JSON call to a provider API: the body encoded
// when there is one, the answer decoded into out when it is wanted, and anything but
// a 2xx returned as an error carrying the provider's own words. Shared by the Graph
// and Gmail mailboxes.
func apiJSON(ctx context.Context, httpc *http.Client, tokens TokenSource, method, endpoint, prefer string, in, out any, what string) error {
	tok, err := tokens.Token(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("mail: encode %s: %w", what, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("mail: build %s request: %w", what, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if prefer != "" {
		req.Header.Set("Prefer", prefer)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return fmt.Errorf("mail: %s: %w", what, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxIMAPLiteral))
	if resp.StatusCode/100 != 2 {
		return &apiStatusError{What: what, Status: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("mail: %s answered something that is not the expected JSON: %w", what, err)
	}
	return nil
}

// apiStatusError is a provider answer outside 2xx.
type apiStatusError struct {
	What   string
	Status int
	Body   string
}

func (e *apiStatusError) Error() string {
	return fmt.Sprintf("mail: %s returned HTTP %d: %s", e.What, e.Status, e.Body)
}
