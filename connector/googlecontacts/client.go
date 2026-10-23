package googlecontacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/clientreg"
	"github.com/pblumer/atlas/connector/oauth2"
	"github.com/pblumer/atlas/limits"
)

// Client speaks the People API for one configured Worker: one address book, one
// identity. Its methods are the People API calls the operations are built from, each
// returning Google's person objects as decoded JSON — the operations, not the client,
// decide what a model sees of them.
type Client struct {
	tokens oauth2.TokenSource
	base   string
	http   *http.Client
}

// Registry resolves a Worker name to the address book behind it. It is the worker's own
// map: the engine never builds one for this kind.
type Registry = clientreg.Registry[*Client]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return clientreg.New[*Client]() }

// person is a People API person as decoded JSON. It stays untyped on purpose: an update
// reads a field, changes the entries it means to and sends the field back, and a typed
// copy would drop every sub-field this package does not model — an e-mail address's
// display name, an organization's start date — from the entries it did not touch.
type person = map[string]any

// APIError is a People API refusal: the HTTP status and Google's status code
// (NOT_FOUND, FAILED_PRECONDITION, PERMISSION_DENIED, …) beside its message. The code is
// what tells "this contact does not exist" from a 404 a proxy produced.
type APIError struct {
	Call    string
	HTTP    int
	Status  string
	Message string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.HTTP)
	}
	return fmt.Sprintf("googlecontacts: %s returned HTTP %d: %s", e.Call, e.HTTP, msg)
}

// isGoogle reports whether err is Google answering with the given status code.
func isGoogle(err error, httpStatus int, status string) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.HTTP == httpStatus && ae.Status == status
}

// call performs one authenticated request and decodes the JSON answer into out (when
// out is not nil). call names the People API method for an error message — never the
// URL, whose query string can hold the e-mail address a process was looking for.
func (c *Client) call(ctx context.Context, name, method, path string, q url.Values, body any, out any) error {
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("googlecontacts: encode %s request: %w", name, err)
		}
		rd = bytes.NewReader(raw)
	}
	endpoint := c.base + "/v1/" + path
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
	if err != nil {
		return fmt.Errorf("googlecontacts: build %s request: %w", name, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The transport's error quotes the URL; say which call failed instead.
		return fmt.Errorf("googlecontacts: %s: the People API could not be reached: %w", name, unwrapURLError(err))
	}
	defer resp.Body.Close()
	// One answer is bounded by the installation's payload budget. The largest the
	// operations ask for is a page of a thousand people with the fields a contact shows,
	// well under it; an answer cut off there fails to decode rather than being read short.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, limits.Default().Payload))
	if resp.StatusCode/100 != 2 {
		return apiError(name, resp.StatusCode, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("googlecontacts: decode %s response: %w", name, err)
	}
	return nil
}

// unwrapURLError drops the *url.Error wrapper, whose message repeats the request URL.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// apiError reads Google's error envelope; a body that is not one (a proxy's HTML page)
// is quoted whole, shortened, because it is still more use than "unknown error".
func apiError(name string, code int, raw []byte) error {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	e := &APIError{Call: name, HTTP: code}
	if json.Unmarshal(raw, &env) == nil && (env.Error.Message != "" || env.Error.Status != "") {
		e.Status, e.Message = env.Error.Status, env.Error.Message
		return e
	}
	msg := strings.TrimSpace(string(raw))
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	e.Message = msg
	return e
}

// get reads one person.
func (c *Client) get(ctx context.Context, resourceName, fields string) (person, error) {
	var p person
	err := c.call(ctx, "people.get", http.MethodGet, pathOf(resourceName),
		url.Values{"personFields": {fields}}, nil, &p)
	return p, err
}

// batchGet reads up to [batchGetMax] people in one call, in the order Google answers.
// A resource name Google cannot resolve comes back as a response without a person,
// and is left out: a member listed by a label and deleted since is not a contact.
func (c *Client) batchGet(ctx context.Context, names []string, fields string) ([]person, error) {
	q := url.Values{"personFields": {fields}}
	for _, n := range names {
		q.Add("resourceNames", n)
	}
	var out struct {
		Responses []struct {
			Person person `json:"person"`
		} `json:"responses"`
	}
	if err := c.call(ctx, "people.getBatchGet", http.MethodGet, "people:batchGet", q, nil, &out); err != nil {
		return nil, err
	}
	people := make([]person, 0, len(out.Responses))
	for _, r := range out.Responses {
		if r.Person != nil {
			people = append(people, r.Person)
		}
	}
	return people, nil
}

// batchGetMax is the most resource names one batchGet takes.
const batchGetMax = 200

// connectionsPageSize is the largest page connections.list serves.
const connectionsPageSize = 1000

// connections walks the address book page by page, handing each person to visit, until
// visit returns false or the book ends. The order is by family name, so a listing a
// person reads, or a multi-instance subprocess walks, has an order somebody chose.
func (c *Client) connections(ctx context.Context, fields string, visit func(person) bool) error {
	token := ""
	for {
		q := url.Values{
			"personFields": {fields},
			"pageSize":     {strconv.Itoa(connectionsPageSize)},
			"sortOrder":    {"LAST_NAME_ASCENDING"},
		}
		if token != "" {
			q.Set("pageToken", token)
		}
		var page struct {
			Connections   []person `json:"connections"`
			NextPageToken string   `json:"nextPageToken"`
		}
		if err := c.call(ctx, "people.connections.list", http.MethodGet, "people/me/connections", q, nil, &page); err != nil {
			return err
		}
		for _, p := range page.Connections {
			if !visit(p) {
				return nil
			}
		}
		if page.NextPageToken == "" {
			return nil
		}
		token = page.NextPageToken
	}
}

// search runs the People API's own contact search. Google answers it from a cache it
// asks to be warmed first with an empty query, so search sends that request before the
// real one; see the package doc for what it does and does not guarantee.
func (c *Client) search(ctx context.Context, query string, size int, fields string) ([]person, error) {
	warm := url.Values{"query": {""}, "readMask": {"names"}, "pageSize": {"1"}}
	if err := c.call(ctx, "people.searchContacts", http.MethodGet, "people:searchContacts", warm, nil, nil); err != nil {
		return nil, err
	}
	q := url.Values{"query": {query}, "readMask": {fields}, "pageSize": {strconv.Itoa(size)}}
	var out struct {
		Results []struct {
			Person person `json:"person"`
		} `json:"results"`
	}
	if err := c.call(ctx, "people.searchContacts", http.MethodGet, "people:searchContacts", q, nil, &out); err != nil {
		return nil, err
	}
	people := make([]person, 0, len(out.Results))
	for _, r := range out.Results {
		if r.Person != nil {
			people = append(people, r.Person)
		}
	}
	return people, nil
}

// searchDirectory searches the Workspace domain's directory: its users' profiles and
// the domain's shared contacts.
func (c *Client) searchDirectory(ctx context.Context, query string, size int, fields string) ([]person, error) {
	q := url.Values{
		"query":    {query},
		"readMask": {fields},
		"pageSize": {strconv.Itoa(size)},
		"sources":  {"DIRECTORY_SOURCE_TYPE_DOMAIN_PROFILE", "DIRECTORY_SOURCE_TYPE_DOMAIN_CONTACT"},
	}
	var out struct {
		People []person `json:"people"`
	}
	if err := c.call(ctx, "people.searchDirectoryPeople", http.MethodGet, "people:searchDirectoryPeople", q, nil, &out); err != nil {
		return nil, err
	}
	return out.People, nil
}

// create creates a contact and answers with it.
func (c *Client) create(ctx context.Context, p person, fields string) (person, error) {
	var out person
	err := c.call(ctx, "people.createContact", http.MethodPost, "people:createContact",
		url.Values{"personFields": {fields}}, p, &out)
	return out, err
}

// update replaces the named fields of a contact and answers with it. p carries the etag
// of the version it was read from; Google refuses the call with FAILED_PRECONDITION
// when the contact has changed since.
func (c *Client) update(ctx context.Context, resourceName string, p person, update []string, fields string) (person, error) {
	var out person
	err := c.call(ctx, "people.updateContact", http.MethodPatch, pathOf(resourceName)+":updateContact",
		url.Values{"updatePersonFields": {strings.Join(update, ",")}, "personFields": {fields}}, p, &out)
	return out, err
}

// remove deletes a contact.
func (c *Client) remove(ctx context.Context, resourceName string) error {
	return c.call(ctx, "people.deleteContact", http.MethodDelete, pathOf(resourceName)+":deleteContact", nil, nil, nil)
}

// group is a contact group as the operations use it.
type group struct {
	ResourceName  string `json:"resourceName"`
	Name          string `json:"name"`
	FormattedName string `json:"formattedName"`
	GroupType     string `json:"groupType"`
}

// userGroup is the type of a label a person made, as opposed to a system group
// (myContacts, starred, …).
const userGroup = "USER_CONTACT_GROUP"

// maxGroupPages bounds the label listing: twenty pages of a thousand labels is far more
// than any address book a person keeps, and a server that keeps answering with a next
// page beyond it is not ending the listing.
const maxGroupPages = 20

// groups lists every contact group of the address book.
func (c *Client) groups(ctx context.Context) ([]group, error) {
	var all []group
	token := ""
	for page := 0; ; page++ {
		if page == maxGroupPages {
			return nil, fmt.Errorf("googlecontacts: contactGroups.list returned more than %d pages of labels", maxGroupPages)
		}
		q := url.Values{"pageSize": {"1000"}, "groupFields": {"name,groupType"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		var page struct {
			ContactGroups []group `json:"contactGroups"`
			NextPageToken string  `json:"nextPageToken"`
		}
		if err := c.call(ctx, "contactGroups.list", http.MethodGet, "contactGroups", q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.ContactGroups...)
		if page.NextPageToken == "" {
			return all, nil
		}
		token = page.NextPageToken
	}
}

// members reads up to max member resource names of a group, and how many it has.
func (c *Client) members(ctx context.Context, groupName string, max int) ([]string, int, error) {
	var out struct {
		MemberResourceNames []string    `json:"memberResourceNames"`
		MemberCount         json.Number `json:"memberCount"`
	}
	err := c.call(ctx, "contactGroups.get", http.MethodGet, pathOf(groupName),
		url.Values{"maxMembers": {strconv.Itoa(max)}}, nil, &out)
	if err != nil {
		return nil, 0, err
	}
	n, _ := out.MemberCount.Int64()
	return out.MemberResourceNames, int(n), nil
}

// modifyResult is what Google says about the contacts a membership change named.
type modifyResult struct {
	NotFound     []string `json:"notFoundResourceNames"`
	CannotRemove []string `json:"canNotRemoveLastContactGroupResourceNames"`
}

// modify adds contacts to a group or removes them from it.
func (c *Client) modify(ctx context.Context, groupName string, add, remove []string) (modifyResult, error) {
	body := map[string]any{}
	if len(add) > 0 {
		body["resourceNamesToAdd"] = add
	}
	if len(remove) > 0 {
		body["resourceNamesToRemove"] = remove
	}
	var out modifyResult
	err := c.call(ctx, "contactGroups.members.modify", http.MethodPost, pathOf(groupName)+"/members:modify", nil, body, &out)
	return out, err
}

// pathOf escapes a resource name's id for a URL path. The forms were checked before
// they got here (catalog.ContactResourceName, the group lookup); escaping is the second
// line, so a name cannot reach another path even if a check is ever loosened.
func pathOf(resourceName string) string {
	kind, id, _ := strings.Cut(resourceName, "/")
	return kind + "/" + url.PathEscape(id)
}
