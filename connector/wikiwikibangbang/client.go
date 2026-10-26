package wikiwikibangbang

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// standingFor is how long the account's standing is trusted before it is read again:
// long enough that a busy Worker does not ask on every job, short enough that an account
// made administrator is refused within minutes.
const standingFor = 10 * time.Minute

// Client speaks the wiki's HTTP API to one configured Worker: one wiki, one account.
// Its methods are the requests the operations are built from; the operations, not the
// client, decide what a model sees.
type Client struct {
	base      *url.URL
	token     string
	http      *http.Client
	now       func() time.Time
	maxAnswer int64

	// The account's standing, read on the first call and again after standingFor.
	mu        sync.Mutex
	me        string
	checkedAt time.Time

	// Seams for the tests of two races no request sequence can produce on its own:
	// afterFind runs between a create's find and its create, and ignoreTotal reads a
	// listing as if the wiki had not said how many items match.
	afterFind   func()
	ignoreTotal bool
}

// APIError is a wiki's answer that is not a success and has no BPMN code of its own: a
// 401, a 403, a 409 while a person edits the page, a server error. It names the call and
// the status, and the wiki's own sentence with the token taken out of it.
type APIError struct {
	Call    string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("wikiwikibangbang: %s: the wiki answered %d", e.Call, e.Status)
	if e.Message != "" {
		msg += ": " + e.Message
	}
	switch e.Status {
	case http.StatusUnauthorized:
		msg += " (the token is not accepted: expired, revoked, or of another wiki)"
	case http.StatusForbidden:
		msg += " (the Worker's account lacks a role in that space, or its token may only read)"
	case http.StatusConflict:
		msg += " (somebody is editing the page; the job is tried again)"
	}
	return msg
}

// notFound is the wiki's 404: the item does not exist, or the account may not read it.
// The wiki answers both alike (its D10), and so does this error.
type notFound struct {
	call    string
	message string
}

func (e *notFound) Error() string {
	return "wikiwikibangbang: " + e.call + ": the item does not exist or may not be read by the Worker's account (" + e.message + ")"
}

// invalid is the wiki's 400: a value the wiki refused — a property of the wrong kind, a
// transition the item's state does not allow, a unique value taken, a malformed query.
// The wiki's sentence says which, and is the message.
type invalid struct {
	call    string
	message string
}

func (e *invalid) Error() string {
	return "wikiwikibangbang: " + e.call + ": the wiki refused it: " + e.message
}

// errAnswerTooLarge is an answer above the payload budget. It is refused rather than
// read short, because a short listing or a page cut off is a wrong answer.
var errAnswerTooLarge = errors.New("the answer is larger than the installation's payload budget (limits.Payload)")

// call sends one request and decodes its answer into into: a JSON answer into any
// value, a text answer into a *string. body, when not nil, is sent as JSON. The path is
// the API's, with every segment a caller supplied already escaped.
func (c *Client) call(ctx context.Context, name, method, path string, q url.Values, body, into any) error {
	// The path is joined escaped, so a property named "due date" stays one segment.
	u := *c.base
	u.RawPath = c.base.EscapedPath() + path
	p, err := url.PathUnescape(u.RawPath)
	if err != nil {
		return fmt.Errorf("wikiwikibangbang: %s: %w", name, err)
	}
	u.Path = p
	u.RawQuery = q.Encode()
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("wikiwikibangbang: %s: %w", name, err)
		}
		reqBody = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return fmt.Errorf("wikiwikibangbang: %s: %w", name, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// A *url.Error carries the URL, which carries no token — the token is a header.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("wikiwikibangbang: %s: the wiki could not be reached: %s", name, c.scrub(err.Error()))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.maxAnswer+1))
	if err != nil {
		return fmt.Errorf("wikiwikibangbang: %s: the answer broke off: %s", name, c.scrub(err.Error()))
	}
	if int64(len(raw)) > c.maxAnswer {
		return fmt.Errorf("wikiwikibangbang: %s: %w", name, errAnswerTooLarge)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := c.wikiMessage(raw)
		switch resp.StatusCode {
		case http.StatusNotFound:
			return &notFound{call: name, message: msg}
		case http.StatusBadRequest:
			return &invalid{call: name, message: msg}
		}
		return &APIError{Call: name, Status: resp.StatusCode, Message: msg}
	}
	switch v := into.(type) {
	case nil:
		return nil
	case *string:
		*v = string(raw)
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("wikiwikibangbang: %s: the wiki's answer is not what this Worker expects: %v", name, err)
	}
	return nil
}

// wikiMessage is the sentence of the wiki's {"error": …} envelope, without the token and
// clipped; a body that is not the envelope gives none.
func (c *Client) wikiMessage(raw []byte) string {
	var env struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Error == "" {
		return ""
	}
	msg := c.scrub(env.Error)
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

// scrub takes the token out of a text the wiki or the network produced, in case either
// ever quotes it. The wiki mints tokens of "wwbb_" and 64 hex digits; a value under
// eight characters is not one, and replacing it would take common letters out of the
// message.
func (c *Client) scrub(s string) string {
	if len(c.token) < 8 {
		return s
	}
	return strings.ReplaceAll(s, c.token, "[token]")
}

// Standing reads the account's own standing — GET /api/me — and refuses an account that
// holds the wiki's system role admin or erasure: an administrator is not bound by the
// wiki's restrictions (its ADR-0022), and nothing this Worker Type does needs either
// role. It answers the account's user id, which the redelivery check of add-comment
// compares a comment's author with. The answer is kept for [standingFor].
func (c *Client) Standing(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.me != "" && c.now().Sub(c.checkedAt) < standingFor {
		me := c.me
		c.mu.Unlock()
		return me, nil
	}
	c.mu.Unlock()
	var me struct {
		Admin   bool   `json:"admin"`
		Erasure bool   `json:"erasure"`
		Scope   string `json:"scope"`
		User    struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := c.call(ctx, "read the account's standing", http.MethodGet, "/api/me", nil, nil, &me); err != nil {
		return "", err
	}
	var roles []string
	if me.Admin {
		roles = append(roles, "admin")
	}
	if me.Erasure {
		roles = append(roles, "erasure")
	}
	if len(roles) > 0 {
		return "", fmt.Errorf("wikiwikibangbang: the Worker's account %q holds the wiki's system role %s; a Worker writes as an account that is an editor in the spaces it writes to and holds no system role, because an administrator is not bound by the wiki's restrictions", me.User.Username, strings.Join(roles, " and "))
	}
	if me.User.ID == "" {
		return "", fmt.Errorf("wikiwikibangbang: the wiki's answer to GET /api/me names no account")
	}
	c.mu.Lock()
	c.me, c.checkedAt = me.User.ID, c.now()
	c.mu.Unlock()
	return me.User.ID, nil
}

// itemPath is the path of an item, or of a route below it, by its id or key.
func itemPath(ref string, rest ...string) string {
	p := "/api/items/" + url.PathEscape(ref)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}
