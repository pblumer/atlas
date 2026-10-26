package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	netmail "net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/limits"
)

// imapMailbox is an SMTP Worker's mailbox, read over IMAP with the same mailbox
// identity: the sender is the login and the worker's secret is the password. Each
// operation is one short session — connect, log in, select, act, log out — so nothing
// holds a connection between a watch's polls or a task's calls.
//
// The id an operation addresses is "<uidvalidity>/<uid>/<folder>". A UID only means
// something within one folder and one UIDVALIDITY, so an id carrying both cannot
// silently address a different message after the server renumbered the folder: it is
// refused instead.
type imapMailbox struct {
	ep       imapEndpoint
	user     string
	password string
	// send delivers a reply through the worker's SMTP side; IMAP cannot send.
	send func(context.Context, Message) error
	// tlsCfg overrides the TLS configuration; nil in production, a test's trust
	// store in tests.
	tlsCfg *tls.Config
}

// imapBodyFetchBytes is how much of a body part one fetch asks for. A body is cut at
// MaxBodyBytes after decoding, and base64 or quoted-printable can take a third more
// than that on the wire; asking for eight times the cap reads every body a process
// can receive in full and never pulls a megabyte part across to throw it away.
const imapBodyFetchBytes = 8 * MaxBodyBytes

// imapHTMLFetchBytes is the same for an HTML part, which is cut at MaxHTMLBytes only
// once it is cleaned — comments, scripts and styles a mail carries can be most of it.
const imapHTMLFetchBytes = 4 * MaxHTMLBytes

func newIMAPMailbox(endpoint, user, password string, send func(context.Context, Message) error) (*imapMailbox, error) {
	ep, err := parseIMAPEndpoint(endpoint)
	if err != nil {
		return nil, fmt.Errorf("mail: %w", err)
	}
	return &imapMailbox{ep: ep, user: user, password: password, send: send}, nil
}

// session runs fn inside one logged-in IMAP session.
func (m *imapMailbox) session(ctx context.Context, fn func(*imapConn) error) error {
	ic, err := dialIMAP(ctx, m.ep, m.tlsCfg)
	if err != nil {
		return fmt.Errorf("mail: imap: %w", err)
	}
	defer ic.logout()
	if err := ic.login(m.user, m.password); err != nil {
		return fmt.Errorf("mail: imap: %w", err)
	}
	if err := fn(ic); err != nil {
		return fmt.Errorf("mail: imap: %w", err)
	}
	return nil
}

// imapFolder is what selecting a folder told us.
type imapFolder struct {
	validity uint32
	uidNext  uint32
}

// selectFolder opens a folder: read-only (EXAMINE) for anything that only reads, so a
// watch or a get can never change a flag even by accident, read-write (SELECT) for an
// operation that changes the mailbox.
func selectFolder(ic *imapConn, folder string, write bool) (imapFolder, error) {
	verb := "EXAMINE "
	if write {
		verb = "SELECT "
	}
	resp, err := ic.cmd(verb + imapFolderName(folder))
	if err != nil {
		return imapFolder{}, fmt.Errorf("open folder %q: %w", folder, err)
	}
	var f imapFolder
	for _, r := range resp {
		if len(r.Code) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(r.Code[1], 10, 32)
		switch strings.ToUpper(r.Code[0]) {
		case "UIDVALIDITY":
			f.validity = uint32(n)
		case "UIDNEXT":
			f.uidNext = uint32(n)
		}
	}
	if f.validity == 0 {
		return imapFolder{}, fmt.Errorf("folder %q: the server reported no UIDVALIDITY", folder)
	}
	return f, nil
}

// searchUIDs runs a UID SEARCH and answers the UIDs ascending.
func searchUIDs(ic *imapConn, criteria string) ([]uint32, error) {
	resp, err := ic.cmd("UID SEARCH " + criteria)
	if err != nil {
		return nil, err
	}
	var out []uint32
	for _, r := range resp {
		if len(r.Fields) == 0 || !strings.EqualFold(fieldString(r.Fields[0]), "SEARCH") {
			continue
		}
		for _, f := range r.Fields[1:] {
			if n, err := strconv.ParseUint(fieldString(f), 10, 32); err == nil && n > 0 {
				out = append(out, uint32(n))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func imapMessageID(validity, uid uint32, folder string) string {
	return fmt.Sprintf("%d/%d/%s", validity, uid, folder)
}

func parseIMAPMessageID(id string) (validity, uid uint32, folder string, err error) {
	parts := strings.SplitN(strings.TrimSpace(id), "/", 3)
	if len(parts) == 3 && parts[2] != "" {
		v, verr := strconv.ParseUint(parts[0], 10, 32)
		u, uerr := strconv.ParseUint(parts[1], 10, 32)
		if verr == nil && uerr == nil && v > 0 && u > 0 {
			return uint32(v), uint32(u), parts[2], nil
		}
	}
	return 0, 0, "", fmt.Errorf("mail: %q is not an IMAP message id (want the messageId a watch or a list answered)", id)
}

// openMessage selects the folder an id names and checks it is still the numbering the
// id was issued under.
func openMessage(ic *imapConn, id string, write bool) (uint32, string, error) {
	validity, uid, folder, err := parseIMAPMessageID(id)
	if err != nil {
		return 0, "", err
	}
	f, err := selectFolder(ic, folder, write)
	if err != nil {
		return 0, "", err
	}
	if f.validity != validity {
		return 0, "", fmt.Errorf("folder %q has been renumbered since message %s was read (UIDVALIDITY %d, now %d); read it again",
			folder, id, validity, f.validity)
	}
	return uid, folder, nil
}

// --- the watch ---

// imapCursor is "<uidvalidity>:<last uid>".
func imapCursor(validity, uid uint32) string {
	return strconv.FormatUint(uint64(validity), 10) + ":" + strconv.FormatUint(uint64(uid), 10)
}

func parseIMAPCursor(c string) (validity, uid uint32, ok bool) {
	v, u, found := strings.Cut(strings.TrimSpace(c), ":")
	if !found {
		return 0, 0, false
	}
	vn, err1 := strconv.ParseUint(v, 10, 32)
	un, err2 := strconv.ParseUint(u, 10, 32)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return uint32(vn), uint32(un), true
}

func (m *imapMailbox) WatchTip(ctx context.Context, folder string) (string, error) {
	var cursor string
	err := m.session(ctx, func(ic *imapConn) error {
		f, err := selectFolder(ic, folder, false)
		if err != nil {
			return err
		}
		cursor, err = imapTip(ic, f)
		return err
	})
	return cursor, err
}

// imapTip is the cursor of a folder's newest message. UIDNEXT says it without reading
// anything; a server that does not announce it is asked for the highest UID instead.
func imapTip(ic *imapConn, f imapFolder) (string, error) {
	if f.uidNext > 0 {
		return imapCursor(f.validity, f.uidNext-1), nil
	}
	uids, err := searchUIDs(ic, "ALL")
	if err != nil {
		return "", err
	}
	last := uint32(0)
	if len(uids) > 0 {
		last = uids[len(uids)-1]
	}
	return imapCursor(f.validity, last), nil
}

func (m *imapMailbox) WatchSince(ctx context.Context, req WatchRequest) (WatchPage, error) {
	var page WatchPage
	err := m.session(ctx, func(ic *imapConn) error {
		f, err := selectFolder(ic, req.Folder, false)
		if err != nil {
			return err
		}
		validity, last, ok := parseIMAPCursor(req.Cursor)
		if !ok {
			// No cursor: a watch reading from the beginning (a backfill).
			validity, last = f.validity, 0
		}
		if validity != f.validity {
			// The server renumbered the folder. Every UID the cursor knew is void, and
			// reading from 1 under a fresh mark would publish the whole folder again as
			// new mail. Resume at the tip instead, and say what was skipped.
			tip, err := imapTip(ic, f)
			if err != nil {
				return err
			}
			page.Cursor = tip
			page.Gap = fmt.Sprintf("IMAP folder %q was renumbered (UIDVALIDITY %d, now %d); mail that arrived "+
				"in between was not delivered and the watch resumes at the newest message", req.Folder, validity, f.validity)
			return nil
		}
		// "n:*" always includes the highest UID, even one below n (RFC 3501 §6.4.8),
		// so what the search answers is filtered again here.
		uids, err := searchUIDs(ic, "UID "+strconv.FormatUint(uint64(last)+1, 10)+":*")
		if err != nil {
			return err
		}
		var fresh []uint32
		for _, u := range uids {
			if u > last {
				fresh = append(fresh, u)
			}
		}
		if req.Limit > 0 && len(fresh) > req.Limit {
			fresh = fresh[:req.Limit]
		}
		if len(fresh) == 0 {
			return nil
		}
		envs, err := fetchEnvelopes(ic, req.Folder, f.validity, fresh, readOf(req.IncludeBody))
		if err != nil {
			return err
		}
		// One mark per folder and numbering: the UID is the sequence, so a renumbered
		// folder must not compare its new UIDs against the old epoch's mark.
		markKey := req.Folder + "#" + strconv.FormatUint(uint64(f.validity), 10)
		for _, e := range envs {
			_, uid, _, _ := parseIMAPMessageID(e.ID)
			page.Items = append(page.Items, Received{MarkKey: markKey, Seq: uint64(uid), Envelope: e})
		}
		page.Cursor = imapCursor(f.validity, fresh[len(fresh)-1])
		return nil
	})
	return page, err
}

// --- reading ---

func (m *imapMailbox) List(ctx context.Context, req ListRequest) ([]Envelope, error) {
	req = req.normalized()
	var out []Envelope
	err := m.session(ctx, func(ic *imapConn) error {
		f, err := selectFolder(ic, req.Folder, false)
		if err != nil {
			return err
		}
		criteria := "ALL"
		if req.UnreadOnly {
			criteria = "UNSEEN"
		}
		uids, err := searchUIDs(ic, criteria)
		if err != nil {
			return err
		}
		if len(uids) > req.MaxResults {
			uids = uids[len(uids)-req.MaxResults:] // UIDs rise with arrival: the newest are last
		}
		if len(uids) == 0 {
			return nil
		}
		envs, err := fetchEnvelopes(ic, req.Folder, f.validity, uids, readOf(req.IncludeBody))
		if err != nil {
			return err
		}
		for i := len(envs) - 1; i >= 0; i-- {
			out = append(out, envs[i])
		}
		return nil
	})
	return out, err
}

func (m *imapMailbox) Get(ctx context.Context, id string, includeBody bool) (Envelope, error) {
	return m.get(ctx, id, readOf(includeBody))
}

// GetHTML implements [HTMLMailbox].
func (m *imapMailbox) GetHTML(ctx context.Context, id string) (Envelope, error) {
	return m.get(ctx, id, readHTML)
}

func (m *imapMailbox) get(ctx context.Context, id string, body bodyRead) (Envelope, error) {
	var out Envelope
	err := m.session(ctx, func(ic *imapConn) error {
		uid, folder, err := openMessage(ic, id, false)
		if err != nil {
			return err
		}
		validity, _, _, _ := parseIMAPMessageID(id)
		envs, err := fetchEnvelopes(ic, folder, validity, []uint32{uid}, body)
		if err != nil {
			return err
		}
		if len(envs) == 0 {
			return fmt.Errorf("message %s no longer exists", id)
		}
		out = envs[0]
		return nil
	})
	return out, err
}

// fetchEnvelopes fetches headers, flags, arrival time and structure for a set of
// UIDs in one round trip, then each body that was asked for in one more. BODY.PEEK is
// used throughout, so reading a message never marks it read.
func fetchEnvelopes(ic *imapConn, folder string, validity uint32, uids []uint32, body bodyRead) ([]Envelope, error) {
	set := make([]string, len(uids))
	for i, u := range uids {
		set[i] = strconv.FormatUint(uint64(u), 10)
	}
	resp, err := ic.cmd("UID FETCH " + strings.Join(set, ",") + " (UID FLAGS INTERNALDATE BODY.PEEK[HEADER] BODYSTRUCTURE)")
	if err != nil {
		return nil, err
	}
	type fetched struct {
		env  Envelope
		root mimePart
		uid  uint32
	}
	byUID := map[uint32]fetched{}
	for _, r := range resp {
		items := fetchItems(r)
		if items == nil {
			continue
		}
		uid64, err := strconv.ParseUint(fieldString(items["UID"]), 10, 32)
		if err != nil || uid64 == 0 {
			continue
		}
		uid := uint32(uid64)
		env := envelopeFromHeader(fieldString(items["BODY[HEADER]"]))
		env.ID = imapMessageID(validity, uid, folder)
		env.Folder = folder
		env.Unread = true
		if flags, ok := items["FLAGS"].([]any); ok {
			for _, fl := range flags {
				if strings.EqualFold(fieldString(fl), `\Seen`) {
					env.Unread = false
				}
			}
		}
		if t, err := time.Parse("_2-Jan-2006 15:04:05 -0700", strings.TrimSpace(fieldString(items["INTERNALDATE"]))); err == nil {
			env.ReceivedAt = t
		}
		root := parseBodyStructure(items["BODYSTRUCTURE"], "", true)
		env.Attachments = root.attachments()
		byUID[uid] = fetched{env: env, root: root, uid: uid}
	}
	out := make([]Envelope, 0, len(uids))
	for _, u := range uids {
		f, ok := byUID[u]
		if !ok {
			continue // expunged between the search and the fetch
		}
		if body >= readText {
			plain, htmlPart := f.root.bodyParts()
			var cut bool
			text, err := bodyFromParts(plain, htmlPart, func(p *mimePart) ([]byte, error) {
				raw, err := fetchSection(ic, f.uid, p.Section, imapBodyFetchBytes)
				cut = p.Size > imapBodyFetchBytes
				return raw, err
			})
			if err != nil {
				return nil, err
			}
			f.env.Body, f.env.BodyTruncated = capBody(text)
			f.env.BodyTruncated = f.env.BodyTruncated || cut
			if body == readHTML {
				var htmlCut bool
				markup, err := htmlFromPart(htmlPart, func(p *mimePart) ([]byte, error) {
					raw, err := fetchSection(ic, f.uid, p.Section, imapHTMLFetchBytes)
					htmlCut = p.Size > imapHTMLFetchBytes
					return raw, err
				})
				if err != nil {
					return nil, err
				}
				f.env.setHTML(markup, htmlCut)
			}
		}
		out = append(out, f.env)
	}
	return out, nil
}

// fetchItems turns "* n FETCH (KEY value KEY value …)" into a map keyed by the
// upper-cased item name, or nil for any other response.
func fetchItems(r imapResponse) map[string]any {
	if r.Tag != "*" || len(r.Fields) < 3 || !strings.EqualFold(fieldString(r.Fields[1]), "FETCH") {
		return nil
	}
	list, ok := r.Fields[2].([]any)
	if !ok {
		return nil
	}
	items := map[string]any{}
	for i := 0; i+1 < len(list); i += 2 {
		items[strings.ToUpper(fieldString(list[i]))] = list[i+1]
	}
	return items
}

// fetchSection reads the first imapBodyFetchBytes of one body part.
func fetchSection(ic *imapConn, uid uint32, section string, limit int) ([]byte, error) {
	resp, err := ic.cmd(fmt.Sprintf("UID FETCH %d (BODY.PEEK[%s]<0.%d>)", uid, section, limit))
	if err != nil {
		return nil, err
	}
	prefix := "BODY[" + strings.ToUpper(section) + "]"
	for _, r := range resp {
		for k, v := range fetchItems(r) {
			if strings.HasPrefix(k, prefix) {
				return []byte(fieldString(v)), nil
			}
		}
	}
	return nil, nil
}

// envelopeFromHeader reads the envelope out of a raw header block.
func envelopeFromHeader(raw string) Envelope {
	msg, err := netmail.ReadMessage(strings.NewReader(strings.TrimRight(raw, "\r\n") + "\r\n\r\n"))
	if err != nil {
		return Envelope{}
	}
	h := msg.Header
	var e Envelope
	e.From, e.FromName = parseAddress(h.Get("From"))
	e.ReplyTo, _ = parseAddress(h.Get("Reply-To"))
	e.To = parseAddressList(h.Get("To"))
	e.Cc = parseAddressList(h.Get("Cc"))
	e.Subject = decodeHeader(h.Get("Subject"))
	e.InternetMessageID = strings.TrimSpace(h.Get("Message-ID"))
	e.References = strings.TrimSpace(h.Get("References"))
	e.Auth = parseAuthResults(h["Authentication-Results"])
	if d, err := h.Date(); err == nil {
		e.ReceivedAt = d // replaced by INTERNALDATE, which is when it arrived here
	}
	return e
}

// parseBodyStructure turns a BODYSTRUCTURE into a mimePart. prefix is the parent's
// section; a single-part message's only part is section 1 (RFC 3501 §6.4.5).
func parseBodyStructure(v any, prefix string, root bool) mimePart {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return mimePart{}
	}
	if _, multi := list[0].([]any); multi {
		p := mimePart{Type: "multipart"}
		n := 0
		for i, el := range list {
			child, ok := el.([]any)
			if !ok {
				p.Subtype = strings.ToLower(fieldString(list[i]))
				if i+2 < len(list) {
					p.Disposition, p.DispParams = imapDisposition(list[i+2])
				}
				break
			}
			n++
			p.Children = append(p.Children, parseBodyStructure(child, joinSection(prefix, n), false))
		}
		p.Section = prefix
		return p
	}
	section := prefix
	if root {
		section = "1"
	}
	p := mimePart{Section: section}
	at := func(i int) any {
		if i < len(list) {
			return list[i]
		}
		return nil
	}
	p.Type = strings.ToLower(fieldString(at(0)))
	p.Subtype = strings.ToLower(fieldString(at(1)))
	p.Params = imapParams(at(2))
	p.Encoding = strings.ToLower(fieldString(at(5)))
	p.Size, _ = strconv.ParseInt(fieldString(at(6)), 10, 64)
	// Where the disposition sits depends on the type, because text and message parts
	// carry extra fields before the extension data.
	disp := 8 // basic: md5 at 7, disposition at 8
	switch {
	case p.Type == "text":
		disp = 9 // lines at 7, md5 at 8
	case p.Type == "message" && p.Subtype == "rfc822":
		disp = 11 // envelope, body, lines at 7–9, md5 at 10
	}
	p.Disposition, p.DispParams = imapDisposition(at(disp))
	return p
}

func joinSection(prefix string, n int) string {
	if prefix == "" {
		return strconv.Itoa(n)
	}
	return prefix + "." + strconv.Itoa(n)
}

// imapParams reads a ("key" "value" …) parameter list into a map with lower-cased keys.
func imapParams(v any) map[string]string {
	list, _ := v.([]any)
	out := map[string]string{}
	for i := 0; i+1 < len(list); i += 2 {
		out[strings.ToLower(fieldString(list[i]))] = fieldString(list[i+1])
	}
	return out
}

// imapDisposition reads ("attachment" ("filename" "a.pdf")).
func imapDisposition(v any) (string, map[string]string) {
	list, _ := v.([]any)
	if len(list) == 0 {
		return "", nil
	}
	return strings.ToLower(fieldString(list[0])), imapParams(at1(list))
}

func at1(list []any) any {
	if len(list) > 1 {
		return list[1]
	}
	return nil
}

// --- changing the mailbox ---

func (m *imapMailbox) Move(ctx context.Context, id, destination string) (string, error) {
	var newID string
	err := m.session(ctx, func(ic *imapConn) error {
		uid, _, err := openMessage(ic, id, true)
		if err != nil {
			return err
		}
		newID, err = moveUID(ic, uid, imapFolderName(destination), destination)
		return err
	})
	return newID, err
}

// moveUID moves one message. With MOVE (RFC 6851) that is one command. Without it, it
// is a copy, the \Deleted flag and — only with UIDPLUS — an expunge of that one UID: a
// plain EXPUNGE would also remove every other message somebody flagged \Deleted in the
// folder, which is not this task's to decide. The id answered is the message's id in
// the destination, when the server says it (COPYUID); "" when it does not.
func moveUID(ic *imapConn, uid uint32, wireDest, destination string) (string, error) {
	if err := ic.ensureCaps(); err != nil {
		return "", err
	}
	u := strconv.FormatUint(uint64(uid), 10)
	var resp []imapResponse
	var err error
	if ic.caps["MOVE"] {
		if resp, err = ic.cmd("UID MOVE " + u + " " + wireDest); err != nil {
			return "", err
		}
	} else {
		if resp, err = ic.cmd("UID COPY " + u + " " + wireDest); err != nil {
			return "", err
		}
		if _, err := ic.cmd("UID STORE " + u + ` +FLAGS.SILENT (\Deleted)`); err != nil {
			return "", err
		}
		if ic.caps["UIDPLUS"] {
			if _, err := ic.cmd("UID EXPUNGE " + u); err != nil {
				return "", err
			}
		}
	}
	for _, r := range resp {
		if len(r.Code) >= 4 && strings.EqualFold(r.Code[0], "COPYUID") {
			v, err1 := strconv.ParseUint(r.Code[1], 10, 32)
			n, err2 := strconv.ParseUint(r.Code[3], 10, 32)
			if err1 == nil && err2 == nil {
				return imapMessageID(uint32(v), uint32(n), destination), nil
			}
		}
	}
	return "", nil
}

func (m *imapMailbox) SetRead(ctx context.Context, id string, read bool) error {
	return m.session(ctx, func(ic *imapConn) error {
		uid, _, err := openMessage(ic, id, true)
		if err != nil {
			return err
		}
		op := "-FLAGS.SILENT"
		if read {
			op = "+FLAGS.SILENT"
		}
		_, err = ic.cmd(fmt.Sprintf(`UID STORE %d %s (\Seen)`, uid, op))
		return err
	})
}

// Delete moves a message to the folder the server marks \Trash (RFC 6154). A server
// with no such folder gets the \Deleted flag and nothing more — deleting for good is
// an EXPUNGE, and that is a decision for whoever reads the mailbox, not for a task.
func (m *imapMailbox) Delete(ctx context.Context, id string) error {
	return m.session(ctx, func(ic *imapConn) error {
		uid, folder, err := openMessage(ic, id, true)
		if err != nil {
			return err
		}
		trash, err := trashFolder(ic)
		if err != nil {
			return err
		}
		if trash != "" && trash != imapFolderWire(folder) {
			_, err = moveUID(ic, uid, imapQuote(trash), trash)
			return err
		}
		_, err = ic.cmd(fmt.Sprintf(`UID STORE %d +FLAGS.SILENT (\Deleted)`, uid))
		return err
	})
}

// imapFolderWire is a folder name as the server spells it (modified UTF-7), unquoted.
func imapFolderWire(folder string) string {
	q := imapFolderName(folder)
	return strings.TrimSuffix(strings.TrimPrefix(q, `"`), `"`)
}

// trashFolder answers the wire name of the folder marked \Trash, or "".
func trashFolder(ic *imapConn) (string, error) {
	resp, err := ic.cmd(`LIST "" "*"`)
	if err != nil {
		return "", err
	}
	for _, r := range resp {
		if len(r.Fields) < 4 || !strings.EqualFold(fieldString(r.Fields[0]), "LIST") {
			continue
		}
		attrs, _ := r.Fields[1].([]any)
		for _, a := range attrs {
			if strings.EqualFold(fieldString(a), `\Trash`) {
				return fieldString(r.Fields[3]), nil
			}
		}
	}
	return "", nil
}

// findSection is the part of a structure a section names.
func (p *mimePart) findSection(section string) (*mimePart, bool) {
	if p.Section == section && len(p.Children) == 0 {
		return p, true
	}
	for i := range p.Children {
		if found, ok := p.Children[i].findSection(section); ok {
			return found, true
		}
	}
	return nil, false
}

// documentWireLimit is how large a document may be on the wire, where base64 has
// grown it by a third: the literal a session fetching one must accept.
func documentWireLimit() int {
	l := limits.Default()
	return int(l.Document/3*4 + l.Request)
}

// Attachment reads one attachment, addressed by its body section. The structure is
// fetched first, so the section is known to be a leaf and its encoded size is checked
// before its content is asked for.
func (m *imapMailbox) Attachment(ctx context.Context, id, attachmentID string) (Document, error) {
	section := strings.TrimSpace(attachmentID)
	if section == "" || strings.Trim(section, "0123456789.") != "" {
		return Document{}, fmt.Errorf("mail: imap: %q is not a body section (use an attachment id the envelope listed)", attachmentID)
	}
	var out Document
	err := m.session(ctx, func(ic *imapConn) error {
		uid, _, err := openMessage(ic, id, false)
		if err != nil {
			return err
		}
		resp, err := ic.cmd(fmt.Sprintf("UID FETCH %d (BODYSTRUCTURE)", uid))
		if err != nil {
			return err
		}
		var root mimePart
		for _, r := range resp {
			if items := fetchItems(r); items != nil && items["BODYSTRUCTURE"] != nil {
				root = parseBodyStructure(items["BODYSTRUCTURE"], "", true)
			}
		}
		part, ok := root.findSection(section)
		if !ok {
			return fmt.Errorf("message %s has no body section %s", id, section)
		}
		name := part.filename()
		if name == "" {
			name = "attachment"
		}
		wire := documentWireLimit()
		if part.Size > int64(wire) {
			return documentTooLarge(name, part.Size, limits.Default().Document)
		}
		ic.maxLiteral = wire
		resp, err = ic.cmd(fmt.Sprintf("UID FETCH %d (BODY.PEEK[%s])", uid, section))
		if err != nil {
			return err
		}
		var raw []byte
		key := "BODY[" + section + "]"
		for _, r := range resp {
			if v, ok := fetchItems(r)[key]; ok {
				raw = []byte(fieldString(v))
			}
		}
		data, err := decodeTransfer(raw, part.Encoding)
		if err != nil {
			return err
		}
		if limit := limits.Default().Document; int64(len(data)) > limit {
			return documentTooLarge(name, int64(len(data)), limit)
		}
		out = Document{ID: section, Name: name, ContentType: part.contentType(), Data: data}
		return nil
	})
	return out, err
}

// Forward reads the whole message, frames the forward from it and sends it through
// the worker's SMTP side. The IMAP session is closed before the send, so a slow
// submission server does not hold the mailbox.
func (m *imapMailbox) Forward(ctx context.Context, id string, f Forward) error {
	if len(f.To) == 0 {
		return fmt.Errorf("mail: a forward needs a recipient")
	}
	if m.send == nil {
		return errors.New("mail: imap: this worker cannot send a forward")
	}
	var raw []byte
	err := m.session(ctx, func(ic *imapConn) error {
		uid, _, err := openMessage(ic, id, false)
		if err != nil {
			return err
		}
		ic.maxLiteral = int(limits.Default().Document)
		resp, err := ic.cmd(fmt.Sprintf("UID FETCH %d (BODY.PEEK[])", uid))
		if err != nil {
			return err
		}
		for _, r := range resp {
			if v, ok := fetchItems(r)["BODY[]"]; ok {
				raw = []byte(fieldString(v))
			}
		}
		if raw == nil {
			return fmt.Errorf("message %s no longer exists", id)
		}
		return nil
	})
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
	msg, err := forwardMessage(raw, f, m.user)
	if err != nil {
		return err
	}
	return m.send(ctx, Message{From: m.user, To: f.To, Cc: f.Cc, Bcc: f.Bcc, MessageID: f.MessageID, Raw: msg})
}

// Reply answers a message through the worker's SMTP side, threaded under it.
func (m *imapMailbox) Reply(ctx context.Context, id string, r Reply) error {
	orig, err := m.Get(ctx, id, false)
	if err != nil {
		return err
	}
	msg, err := replyMessage(orig, r)
	if err != nil {
		return err
	}
	if m.send == nil {
		return errors.New("mail: imap: this worker cannot send a reply")
	}
	return m.send(ctx, msg)
}

// replyMessage frames a reply to orig: to its Reply-To, else its From; "Re: " in
// front of a subject that does not already carry it; threaded by In-Reply-To and
// References. Shared by the IMAP and Gmail mailboxes, which both send a framed
// message; Graph has a reply action of its own.
func replyMessage(orig Envelope, r Reply) (Message, error) {
	to := orig.ReplyTo
	if to == "" {
		to = orig.From
	}
	if to == "" {
		return Message{}, errors.New("mail: the message has neither a Reply-To nor a From to answer")
	}
	if strings.TrimSpace(r.Body) == "" && strings.TrimSpace(r.HTML) == "" {
		return Message{}, errors.New("mail: a reply needs a body or an HTML body")
	}
	subject := orig.Subject
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "re:") {
		subject = "Re: " + subject
	}
	refs := strings.TrimSpace(orig.References + " " + orig.InternetMessageID)
	return Message{
		To:         []string{to},
		Subject:    subject,
		Body:       r.Body,
		HTML:       r.HTML,
		MessageID:  r.MessageID,
		InReplyTo:  orig.InternetMessageID,
		References: refs,
	}, nil
}
