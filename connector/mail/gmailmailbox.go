package mail

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/limits"
)

// Gmail scopes the mailbox half asks for. Reading and changing are separate tokens on
// purpose: a domain-wide delegation that grants only gmail.readonly then serves a
// watch, a list and a get, and refuses a move with Google's own error — rather than
// the whole Worker failing because one token asked for more than it was given.
const (
	gmailReadScope   = "https://www.googleapis.com/auth/gmail.readonly"
	gmailModifyScope = "https://www.googleapis.com/auth/gmail.modify"
)

// gmailMailbox is a Gmail mailbox read through the Gmail API as the authenticated
// user — the impersonated sender under a service account, the consenting user under a
// refresh token. A "folder" is a label id: INBOX and the other system labels by name,
// a user label by its id (Label_…), which is what the API addresses.
type gmailMailbox struct{ c *GmailClient }

// WithMailboxTokens gives the client the tokens its mailbox half reads and changes
// with. A client built without them uses its send token for everything, which is what
// a credential whose scope the operator set explicitly asks for.
func (c *GmailClient) WithMailboxTokens(read, modify TokenSource) *GmailClient {
	c.readTokens, c.modifyTokens = read, modify
	return c
}

// Mailbox answers the Gmail mailbox this worker authenticates as.
func (c *GmailClient) Mailbox() (Mailbox, error) { return gmailMailbox{c: c}, nil }

func (m gmailMailbox) read() TokenSource {
	if m.c.readTokens != nil {
		return m.c.readTokens
	}
	return m.c.tokens
}

func (m gmailMailbox) modify() TokenSource {
	if m.c.modifyTokens != nil {
		return m.c.modifyTokens
	}
	return m.c.tokens
}

func (m gmailMailbox) base() string { return strings.TrimRight(m.c.baseURL, "/") + "/users/me" }

func gmailLabel(folder string) string {
	if f := strings.TrimSpace(folder); f != "" {
		return f
	}
	return DefaultFolder
}

// --- the watch ---

// Gmail's history is a log: every change to the mailbox has a history id, and a
// messageAdded record says a message arrived. So the watch reads it like one — one
// mark per watch, sequenced by the history id. Several messages can share one record,
// so the message's position in it is appended in the low bits; a record never carries
// anywhere near the 1024 that leaves room for.
const gmailSeqShift = 10

type gmailHistoryPage struct {
	History []struct {
		ID            string `json:"id"`
		MessagesAdded []struct {
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
		} `json:"messagesAdded"`
	} `json:"history"`
	HistoryID     string `json:"historyId"`
	NextPageToken string `json:"nextPageToken"`
}

func (m gmailMailbox) WatchTip(ctx context.Context, _ string) (string, error) {
	var profile struct {
		HistoryID string `json:"historyId"`
	}
	if err := apiJSON(ctx, m.c.http, m.read(), http.MethodGet, m.base()+"/profile", "", nil, &profile, "gmail profile"); err != nil {
		return "", err
	}
	if _, err := strconv.ParseUint(profile.HistoryID, 10, 64); err != nil {
		return "", fmt.Errorf("mail: gmail profile answered no history id")
	}
	return profile.HistoryID, nil
}

func (m gmailMailbox) WatchSince(ctx context.Context, req WatchRequest) (WatchPage, error) {
	label := gmailLabel(req.Folder)
	start, err := strconv.ParseUint(strings.TrimSpace(req.Cursor), 10, 64)
	if err != nil || start == 0 {
		// Gmail's history has no beginning to read from: it is a window of about a
		// week. A watch without a cursor starts at the newest message and says so.
		tip, err := m.WatchTip(ctx, label)
		if err != nil {
			return WatchPage{}, err
		}
		return WatchPage{Cursor: tip, Gap: "Gmail cannot replay a mailbox from its beginning; " +
			"the watch starts at the newest message"}, nil
	}
	limit := req.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	q := url.Values{}
	q.Set("startHistoryId", strconv.FormatUint(start, 10))
	q.Set("historyTypes", "messageAdded")
	q.Set("labelId", label)
	q.Set("maxResults", strconv.Itoa(limit))
	var hist gmailHistoryPage
	err = apiJSON(ctx, m.c.http, m.read(), http.MethodGet, m.base()+"/history?"+q.Encode(), "", nil, &hist, "gmail history")
	var se *apiStatusError
	if errors.As(err, &se) && se.Status == http.StatusNotFound {
		// The start id is older than the history Gmail keeps. What arrived in between
		// cannot be listed any more; resuming at the tip and saying so is the only
		// answer that does not replay the mailbox or stop the watch for good.
		tip, terr := m.WatchTip(ctx, label)
		if terr != nil {
			return WatchPage{}, terr
		}
		return WatchPage{Cursor: tip, Gap: fmt.Sprintf("Gmail no longer keeps history from %d; mail that arrived "+
			"before the watch resumed was not delivered", start)}, nil
	}
	if err != nil {
		return WatchPage{}, err
	}
	var page WatchPage
	var lastRecord uint64
	for _, h := range hist.History {
		rec, err := strconv.ParseUint(h.ID, 10, 64)
		if err != nil || rec <= start {
			continue
		}
		lastRecord = rec
		for i, added := range h.MessagesAdded {
			if i >= 1<<gmailSeqShift || added.Message.ID == "" {
				continue
			}
			e, err := m.get(ctx, added.Message.ID, req.IncludeBody)
			if errors.As(err, &se) && se.Status == http.StatusNotFound {
				continue // deleted before this read: there is nothing left to deliver
			}
			if err != nil {
				return WatchPage{}, err
			}
			e.Folder = label
			page.Items = append(page.Items, Received{
				MarkKey:  "label:" + label,
				Seq:      rec<<gmailSeqShift | uint64(i),
				Envelope: e,
			})
		}
	}
	switch {
	case hist.NextPageToken != "" && lastRecord > 0:
		page.Cursor = strconv.FormatUint(lastRecord, 10)
	case hist.HistoryID != "":
		// Read to the end: resume from the mailbox's current history id, so a quiet
		// label does not leave the cursor behind until it falls out of Gmail's window.
		page.Cursor = hist.HistoryID
	}
	if page.Cursor == strings.TrimSpace(req.Cursor) {
		page.Cursor = ""
	}
	return page, nil
}

// --- reading ---

type gmailPart struct {
	PartID   string `json:"partId"`
	MimeType string `json:"mimeType"`
	Filename string `json:"filename"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		Size         int64  `json:"size"`
		Data         string `json:"data"`
		AttachmentID string `json:"attachmentId"`
	} `json:"body"`
	Parts []gmailPart `json:"parts"`
}

type gmailMessage struct {
	ID           string    `json:"id"`
	ThreadID     string    `json:"threadId"`
	LabelIDs     []string  `json:"labelIds"`
	InternalDate string    `json:"internalDate"`
	Payload      gmailPart `json:"payload"`
}

func (p gmailPart) header(name string) []string {
	var out []string
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			out = append(out, h.Value)
		}
	}
	return out
}

func first(vals []string) string {
	if len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// toMIME turns Gmail's part tree into a mimePart. Gmail has already undone the
// transfer encoding, so the data is the part's content in its own charset.
func (p gmailPart) toMIME() mimePart {
	mt, params, _ := mime.ParseMediaType(first(p.header("Content-Type")))
	if mt == "" {
		mt = strings.ToLower(p.MimeType)
	}
	typ, sub, _ := strings.Cut(mt, "/")
	out := mimePart{Type: typ, Subtype: sub, Params: params, Size: p.Body.Size, Section: p.PartID}
	if out.Params == nil {
		out.Params = map[string]string{}
	}
	if d, dp, err := mime.ParseMediaType(first(p.header("Content-Disposition"))); err == nil {
		out.Disposition, out.DispParams = d, dp
	}
	if p.Filename != "" {
		if out.DispParams == nil {
			out.DispParams = map[string]string{}
		}
		out.DispParams["filename"] = p.Filename
	}
	if p.Body.Data != "" {
		if data, err := base64.URLEncoding.DecodeString(p.Body.Data); err == nil {
			out.Data = data
		} else if data, err := base64.RawURLEncoding.DecodeString(p.Body.Data); err == nil {
			out.Data = data
		}
	}
	if p.Body.AttachmentID != "" && out.Data == nil {
		out.Params["x-gmail-attachment-id"] = p.Body.AttachmentID
	}
	for _, c := range p.Parts {
		out.Children = append(out.Children, c.toMIME())
	}
	return out
}

func (m gmailMailbox) get(ctx context.Context, id string, includeBody bool) (Envelope, error) {
	e, _, err := m.getWithThread(ctx, id, readOf(includeBody))
	return e, err
}

// getWithThread reads one message and also answers its thread, which a reply posts
// into.
func (m gmailMailbox) getWithThread(ctx context.Context, id string, body bodyRead) (Envelope, string, error) {
	var g gmailMessage
	u := m.base() + "/messages/" + url.PathEscape(strings.TrimSpace(id)) + "?format=full"
	if err := apiJSON(ctx, m.c.http, m.read(), http.MethodGet, u, "", nil, &g, "gmail get message"); err != nil {
		return Envelope{}, "", err
	}
	h := g.Payload
	e := Envelope{
		ID:                g.ID,
		InternetMessageID: strings.TrimSpace(first(h.header("Message-ID"))),
		Subject:           decodeHeader(first(h.header("Subject"))),
		To:                parseAddressList(first(h.header("To"))),
		Cc:                parseAddressList(first(h.header("Cc"))),
		References:        strings.TrimSpace(first(h.header("References"))),
		Auth:              parseAuthResults(h.header("Authentication-Results")),
	}
	e.From, e.FromName = parseAddress(first(h.header("From")))
	e.ReplyTo, _ = parseAddress(first(h.header("Reply-To")))
	if ms, err := strconv.ParseInt(g.InternalDate, 10, 64); err == nil && ms > 0 {
		e.ReceivedAt = time.UnixMilli(ms).UTC()
	}
	for _, l := range g.LabelIDs {
		if l == "UNREAD" {
			e.Unread = true
		}
	}
	root := h.toMIME()
	e.Attachments = root.attachments()
	if body >= readText {
		plain, htmlPart := root.bodyParts()
		read := func(p *mimePart) ([]byte, error) {
			return m.attachmentData(ctx, g.ID, p.Params["x-gmail-attachment-id"])
		}
		text, err := bodyFromParts(plain, htmlPart, read)
		if err != nil {
			return Envelope{}, "", err
		}
		e.Body, e.BodyTruncated = capBody(text)
		if body == readHTML {
			markup, err := htmlFromPart(htmlPart, read)
			if err != nil {
				return Envelope{}, "", err
			}
			e.setHTML(markup, false)
		}
	}
	return e, g.ThreadID, nil
}

// attachmentData reads a body part Gmail did not inline. It is only ever asked for a
// text part chosen as the body — never for an attachment.
func (m gmailMailbox) attachmentData(ctx context.Context, msgID, attID string) ([]byte, error) {
	if attID == "" {
		return nil, nil
	}
	var a struct {
		Data string `json:"data"`
	}
	u := m.base() + "/messages/" + url.PathEscape(msgID) + "/attachments/" + url.PathEscape(attID)
	if err := apiJSON(ctx, m.c.http, m.read(), http.MethodGet, u, "", nil, &a, "gmail get body part"); err != nil {
		return nil, err
	}
	if data, err := base64.URLEncoding.DecodeString(a.Data); err == nil {
		return data, nil
	}
	return base64.RawURLEncoding.DecodeString(a.Data)
}

func (m gmailMailbox) List(ctx context.Context, req ListRequest) ([]Envelope, error) {
	req = req.normalized()
	q := url.Values{}
	q.Set("labelIds", gmailLabel(req.Folder))
	q.Set("maxResults", strconv.Itoa(req.MaxResults))
	if req.UnreadOnly {
		q.Set("q", "is:unread")
	}
	var list struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := apiJSON(ctx, m.c.http, m.read(), http.MethodGet, m.base()+"/messages?"+q.Encode(), "", nil, &list, "gmail list messages"); err != nil {
		return nil, err
	}
	out := make([]Envelope, 0, len(list.Messages))
	for _, it := range list.Messages {
		e, err := m.get(ctx, it.ID, req.IncludeBody)
		var se *apiStatusError
		if errors.As(err, &se) && se.Status == http.StatusNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		e.Folder = gmailLabel(req.Folder)
		out = append(out, e)
	}
	return out, nil
}

func (m gmailMailbox) Get(ctx context.Context, id string, includeBody bool) (Envelope, error) {
	return m.get(ctx, id, includeBody)
}

// GetHTML implements [HTMLMailbox].
func (m gmailMailbox) GetHTML(ctx context.Context, id string) (Envelope, error) {
	e, _, err := m.getWithThread(ctx, id, readHTML)
	return e, err
}

// --- changing the mailbox ---

func (m gmailMailbox) modifyLabels(ctx context.Context, id string, add, remove []string) error {
	body := map[string][]string{}
	if len(add) > 0 {
		body["addLabelIds"] = add
	}
	if len(remove) > 0 {
		body["removeLabelIds"] = remove
	}
	u := m.base() + "/messages/" + url.PathEscape(strings.TrimSpace(id)) + "/modify"
	return apiJSON(ctx, m.c.http, m.modify(), http.MethodPost, u, "", body, nil, "gmail modify labels")
}

// Move gives the message the destination label and takes it out of the inbox, which
// is what moving means in Gmail: a message is in every label it carries, and the one
// a watch reads is the inbox. The id does not change.
func (m gmailMailbox) Move(ctx context.Context, id, destination string) (string, error) {
	dest := strings.TrimSpace(destination)
	if dest == "" {
		return "", fmt.Errorf("mail: gmail: move needs a destination label")
	}
	var remove []string
	if !strings.EqualFold(dest, DefaultFolder) {
		remove = []string{DefaultFolder}
	}
	if err := m.modifyLabels(ctx, id, []string{dest}, remove); err != nil {
		return "", err
	}
	return id, nil
}

func (m gmailMailbox) SetRead(ctx context.Context, id string, read bool) error {
	if read {
		return m.modifyLabels(ctx, id, nil, []string{"UNREAD"})
	}
	return m.modifyLabels(ctx, id, []string{"UNREAD"}, nil)
}

func (m gmailMailbox) Delete(ctx context.Context, id string) error {
	u := m.base() + "/messages/" + url.PathEscape(strings.TrimSpace(id)) + "/trash"
	return apiJSON(ctx, m.c.http, m.modify(), http.MethodPost, u, "", map[string]string{}, nil, "gmail trash")
}

// findPart is the part of a payload tree a part id names.
func (p gmailPart) findPart(id string) (gmailPart, bool) {
	if p.PartID == id {
		return p, true
	}
	for _, c := range p.Parts {
		if found, ok := c.findPart(id); ok {
			return found, true
		}
	}
	return gmailPart{}, false
}

// Attachment reads one attachment, addressed by its part id. The part's attachmentId is
// read fresh from the message, because Gmail mints a new one on every read — the part
// id is the address that lasts.
func (m gmailMailbox) Attachment(ctx context.Context, id, attachmentID string) (Document, error) {
	var g gmailMessage
	u := m.base() + "/messages/" + url.PathEscape(strings.TrimSpace(id)) + "?format=full"
	if err := apiJSON(ctx, m.c.http, m.read(), http.MethodGet, u, "", nil, &g, "gmail get message"); err != nil {
		return Document{}, err
	}
	part, ok := g.Payload.findPart(strings.TrimSpace(attachmentID))
	if !ok {
		return Document{}, fmt.Errorf("mail: gmail: message %s has no part %q (use an attachment id the envelope listed)", id, attachmentID)
	}
	mp := part.toMIME()
	name := mp.filename()
	if name == "" {
		name = "attachment"
	}
	limit := limits.Default().Document
	if part.Body.Size > limit {
		return Document{}, documentTooLarge(name, part.Body.Size, limit)
	}
	data := mp.Data
	if data == nil && part.Body.AttachmentID != "" {
		var a struct {
			Data string `json:"data"`
		}
		au := m.base() + "/messages/" + url.PathEscape(g.ID) + "/attachments/" + url.PathEscape(part.Body.AttachmentID)
		if err := apiJSONLimit(ctx, m.c.http, m.read(), http.MethodGet, au, "", nil, &a, documentJSONLimit(), "gmail get attachment"); err != nil {
			return Document{}, err
		}
		var err error
		if data, err = decodeBase64URL(a.Data); err != nil {
			return Document{}, fmt.Errorf("mail: gmail: attachment %q does not decode: %w", name, err)
		}
	}
	if int64(len(data)) > limit {
		return Document{}, documentTooLarge(name, int64(len(data)), limit)
	}
	return Document{ID: part.PartID, Name: name, ContentType: mp.contentType(), Data: data}, nil
}

func decodeBase64URL(s string) ([]byte, error) {
	if data, err := base64.URLEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

// raw reads a message as Gmail stores it, RFC 5322 whole.
func (m gmailMailbox) raw(ctx context.Context, id string) ([]byte, string, error) {
	var g struct {
		Raw      string `json:"raw"`
		ThreadID string `json:"threadId"`
	}
	u := m.base() + "/messages/" + url.PathEscape(strings.TrimSpace(id)) + "?format=raw"
	if err := apiJSONLimit(ctx, m.c.http, m.read(), http.MethodGet, u, "", nil, &g, documentJSONLimit(), "gmail get raw message"); err != nil {
		return nil, "", err
	}
	data, err := decodeBase64URL(g.Raw)
	if err != nil {
		return nil, "", fmt.Errorf("mail: gmail: message %s does not decode: %w", id, err)
	}
	if limit := limits.Default().Document; int64(len(data)) > limit {
		return nil, "", fmt.Errorf("mail: gmail: message %s is %d bytes, more than the %d a Worker carries in one piece (ATLAS_LIMIT_DOCUMENT)", id, len(data), limit)
	}
	return data, g.ThreadID, nil
}

// Forward frames the forward from the raw message and sends it through Gmail's media
// upload, which is the send Google documents for a message carrying attachments; the
// metadata send the other operations use takes the message inside a JSON string.
func (m gmailMailbox) Forward(ctx context.Context, id string, f Forward) error {
	if len(f.To) == 0 {
		return fmt.Errorf("mail: a forward needs a recipient")
	}
	from := firstNonEmpty(m.c.sender)
	if from == "" {
		return fmt.Errorf("mail: gmail: no sender configured to forward as")
	}
	raw, _, err := m.raw(ctx, id)
	if err != nil {
		return err
	}
	if f.Inspect != nil {
		docs, err := rawDocuments(raw)
		if err != nil {
			return err
		}
		for _, d := range docs {
			if err := f.Inspect(d); err != nil {
				return err
			}
		}
	}
	msg, err := forwardMessage(raw, f, from)
	if err != nil {
		return err
	}
	// Bcc is the envelope, not a header: Gmail reads it from the message it is handed
	// and strips it before delivery, which is the only way to give it one.
	if len(f.Bcc) > 0 {
		msg = append([]byte("Bcc: "+headerSafe(strings.Join(f.Bcc, ", "))+"\r\n"), msg...)
	}
	return apiUpload(ctx, m.c.http, m.c.tokens, m.uploadBase()+"/users/me/messages/send?uploadType=media", "message/rfc822", msg, "gmail forward")
}

// uploadBase is the API base with Google's /upload prefix, where media uploads go:
// https://gmail.googleapis.com/upload/gmail/v1 for the default base.
func (m gmailMailbox) uploadBase() string {
	base := strings.TrimRight(m.c.baseURL, "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base
	}
	u.Path = "/upload" + u.Path
	return strings.TrimRight(u.String(), "/")
}

// Reply frames the reply like a send and posts it into the original's thread.
func (m gmailMailbox) Reply(ctx context.Context, id string, r Reply) error {
	orig, thread, err := m.getWithThread(ctx, id, readEnvelope)
	if err != nil {
		return err
	}
	msg, err := replyMessage(orig, r)
	if err != nil {
		return err
	}
	from := firstNonEmpty(m.c.sender)
	if from == "" {
		return fmt.Errorf("mail: gmail: no sender configured to reply as")
	}
	raw := base64.URLEncoding.EncodeToString(buildRFC822(msg, from))
	body := map[string]string{"raw": raw}
	if thread != "" {
		body["threadId"] = thread
	}
	return apiJSON(ctx, m.c.http, m.c.tokens, http.MethodPost, m.base()+"/messages/send", "", body, nil, "gmail reply")
}
