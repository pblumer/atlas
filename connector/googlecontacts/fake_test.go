package googlecontacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakePeople is a People API in memory: enough of contacts, contact groups and the
// directory for the operations to be exercised over real HTTP, with Google's own
// answers to the cases the operations treat specially — a missing contact, a stale
// etag, a contact that cannot leave its last group.
type fakePeople struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	order    []string          // resource names in insertion order
	people   map[string]person // contact source data, by resource name
	etags    map[string]int
	groups   []group
	members  map[string][]string // group resource name → member resource names
	dir      []person
	nextID   int
	requests []*http.Request
	bodies   []string
	// conflicts makes the next n updates fail as if the contact changed after it was read.
	conflicts int
	// fail answers every call to this method name with the given status.
	fail map[string]int
	// lastOnly marks contacts Google keeps in their last group.
	lastOnly map[string]bool
	// groupPage, when set, pages contactGroups.list by this many groups.
	groupPage int
}

func newFakePeople(t *testing.T) *fakePeople {
	t.Helper()
	f := &fakePeople{
		t: t, people: map[string]person{}, etags: map[string]int{},
		members: map[string][]string{}, fail: map[string]int{}, lastOnly: map[string]bool{},
		groups: []group{
			{ResourceName: "contactGroups/myContacts", Name: "myContacts", FormattedName: "My Contacts", GroupType: "SYSTEM_CONTACT_GROUP"},
			{ResourceName: "contactGroups/starred", Name: "starred", FormattedName: "Starred", GroupType: "SYSTEM_CONTACT_GROUP"},
		},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// client is a Client for this fake with a fixed token, so a test reaches the API
// without a token exchange.
func (f *fakePeople) client() *Client {
	return &Client{tokens: staticToken("tok"), base: f.srv.URL, http: f.srv.Client()}
}

func (f *fakePeople) registry(name string) *Registry {
	r := NewRegistry()
	r.Register(name, f.client())
	return r
}

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

func (f *fakePeople) addGroup(id, name string) string {
	rn := "contactGroups/" + id
	f.groups = append(f.groups, group{ResourceName: rn, Name: name, FormattedName: name, GroupType: userGroup})
	return rn
}

// add stores a contact and returns its resource name. Labels named in groups are
// added as memberships too.
func (f *fakePeople) add(p person, groups ...string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	rn := fmt.Sprintf("people/c%d", f.nextID)
	p["resourceName"] = rn
	f.people[rn] = p
	f.order = append(f.order, rn)
	f.etags[rn] = 1
	for _, g := range append([]string{"contactGroups/myContacts"}, groups...) {
		f.members[g] = append(f.members[g], rn)
	}
	return rn
}

func (f *fakePeople) googleError(w http.ResponseWriter, code int, status, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg, "status": status}})
}

func (f *fakePeople) reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// view is a stored contact as Google answers it: the requested fields, its memberships
// and its etag.
func (f *fakePeople) view(rn string, fields string) person {
	p := f.people[rn]
	out := person{"resourceName": rn, "etag": "e" + strconv.Itoa(f.etags[rn])}
	for _, field := range strings.Split(fields, ",") {
		switch field {
		case "metadata":
			out["metadata"] = map[string]any{"sources": []any{map[string]any{"type": "CONTACT", "id": strings.TrimPrefix(rn, "people/"), "etag": out["etag"]}}}
		case "memberships":
			var ms []any
			for _, g := range f.groups {
				for _, m := range f.members[g.ResourceName] {
					if m == rn {
						ms = append(ms, membership(g.ResourceName))
					}
				}
			}
			if ms != nil {
				out["memberships"] = ms
			}
		default:
			if v, ok := p[field]; ok {
				out[field] = roundTrip(v)
			}
		}
	}
	return out
}

// roundTrip copies a value through JSON, so a contact the client holds is never the
// map the fake stores.
func roundTrip(v any) any {
	raw, _ := json.Marshal(v)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	_ = dec.Decode(&out)
	return out
}

func (f *fakePeople) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.requests = append(f.requests, r)
	f.bodies = append(f.bodies, string(body))
	if r.Header.Get("Authorization") != "Bearer tok" {
		f.googleError(w, 401, "UNAUTHENTICATED", "Request had invalid authentication credentials.")
		return
	}
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/")
	q := r.URL.Query()
	name := methodName(r.Method, path)
	if code, ok := f.fail[name]; ok {
		f.googleError(w, code, "INTERNAL", "backend error for "+name)
		return
	}
	switch name {
	case "get":
		rn, _ := url.PathUnescape(path)
		if _, ok := f.people[rn]; !ok {
			f.googleError(w, 404, "NOT_FOUND", "Requested entity was not found.")
			return
		}
		f.reply(w, f.view(rn, q.Get("personFields")))
	case "batchGet":
		var resp []any
		for _, rn := range q["resourceNames"] {
			if _, ok := f.people[rn]; ok {
				resp = append(resp, map[string]any{"person": f.view(rn, q.Get("personFields"))})
			} else {
				resp = append(resp, map[string]any{"status": map[string]any{"code": 5}})
			}
		}
		f.reply(w, map[string]any{"responses": resp})
	case "connections":
		size, _ := strconv.Atoi(q.Get("pageSize"))
		start, _ := strconv.Atoi(q.Get("pageToken"))
		order := append([]string(nil), f.order...)
		sort.SliceStable(order, func(i, j int) bool { return familyName(f.people[order[i]]) < familyName(f.people[order[j]]) })
		var page []any
		end := min(start+size, len(order))
		for _, rn := range order[start:end] {
			page = append(page, f.view(rn, q.Get("personFields")))
		}
		out := map[string]any{"connections": page, "totalItems": len(order)}
		if end < len(order) {
			out["nextPageToken"] = strconv.Itoa(end)
		}
		f.reply(w, out)
	case "search":
		var results []any
		query := strings.ToLower(q.Get("query"))
		size, _ := strconv.Atoi(q.Get("pageSize"))
		if query != "" {
			for _, rn := range f.order {
				if len(results) == size {
					break
				}
				if prefixMatch(f.people[rn], query) {
					results = append(results, map[string]any{"person": f.view(rn, q.Get("readMask"))})
				}
			}
		}
		f.reply(w, map[string]any{"results": results})
	case "directory":
		size, _ := strconv.Atoi(q.Get("pageSize"))
		var people []any
		for _, p := range f.dir {
			if len(people) < size && prefixMatch(p, strings.ToLower(q.Get("query"))) {
				people = append(people, p)
			}
		}
		f.reply(w, map[string]any{"people": people})
	case "create":
		var p person
		_ = json.Unmarshal(body, &p)
		groups := extractMemberships(p)
		f.nextID++
		rn := fmt.Sprintf("people/c%d", f.nextID)
		f.people[rn] = p
		f.order = append(f.order, rn)
		f.etags[rn] = 1
		if groups == nil {
			groups = []string{"contactGroups/myContacts"}
		}
		for _, g := range groups {
			f.members[g] = append(f.members[g], rn)
		}
		f.reply(w, f.view(rn, q.Get("personFields")))
	case "update":
		rn, _ := url.PathUnescape(strings.TrimSuffix(path, ":updateContact"))
		stored, ok := f.people[rn]
		if !ok {
			f.googleError(w, 404, "NOT_FOUND", "Requested entity was not found.")
			return
		}
		var p person
		_ = json.Unmarshal(body, &p)
		if f.conflicts > 0 {
			f.conflicts--
			f.etags[rn]++
		}
		if p["etag"] != "e"+strconv.Itoa(f.etags[rn]) {
			f.googleError(w, 400, "FAILED_PRECONDITION", "Request person.etag is different than the current person.etag. Clear local cache and get the latest person.")
			return
		}
		for _, field := range strings.Split(q.Get("updatePersonFields"), ",") {
			v, ok := p[field]
			if list, isList := v.([]any); !ok || (isList && len(list) == 0) {
				delete(stored, field)
				continue
			}
			stored[field] = stripMetadata(v)
		}
		f.etags[rn]++
		f.reply(w, f.view(rn, q.Get("personFields")))
	case "delete":
		rn, _ := url.PathUnescape(strings.TrimSuffix(path, ":deleteContact"))
		if _, ok := f.people[rn]; !ok {
			f.googleError(w, 404, "NOT_FOUND", "Requested entity was not found.")
			return
		}
		delete(f.people, rn)
		for i, o := range f.order {
			if o == rn {
				f.order = append(f.order[:i], f.order[i+1:]...)
				break
			}
		}
		f.reply(w, map[string]any{})
	case "groups":
		start, _ := strconv.Atoi(q.Get("pageToken"))
		end := len(f.groups)
		if f.groupPage > 0 {
			end = min(start+f.groupPage, end)
		}
		var gs []any
		for _, g := range f.groups[start:end] {
			gs = append(gs, map[string]any{"resourceName": g.ResourceName, "name": g.Name, "formattedName": g.FormattedName, "groupType": g.GroupType})
		}
		out := map[string]any{"contactGroups": gs}
		if end < len(f.groups) {
			out["nextPageToken"] = strconv.Itoa(end)
		}
		f.reply(w, out)
	case "group":
		gid, _ := url.PathUnescape(path)
		max, _ := strconv.Atoi(q.Get("maxMembers"))
		ms := f.members[gid]
		f.reply(w, map[string]any{"resourceName": gid, "memberCount": len(ms), "memberResourceNames": ms[:min(max, len(ms))]})
	case "modify":
		gid, _ := url.PathUnescape(strings.TrimSuffix(path, "/members:modify"))
		var req struct {
			Add    []string `json:"resourceNamesToAdd"`
			Remove []string `json:"resourceNamesToRemove"`
		}
		_ = json.Unmarshal(body, &req)
		out := map[string]any{}
		var notFound, cannot []string
		for _, rn := range req.Add {
			if _, ok := f.people[rn]; !ok {
				notFound = append(notFound, rn)
				continue
			}
			if !containsKey(f.members[gid], rn) {
				f.members[gid] = append(f.members[gid], rn)
			}
		}
		for _, rn := range req.Remove {
			if _, ok := f.people[rn]; !ok {
				notFound = append(notFound, rn)
				continue
			}
			if f.lastOnly[rn] {
				cannot = append(cannot, rn)
				continue
			}
			var kept []string
			for _, m := range f.members[gid] {
				if m != rn {
					kept = append(kept, m)
				}
			}
			f.members[gid] = kept
		}
		if notFound != nil {
			out["notFoundResourceNames"] = notFound
		}
		if cannot != nil {
			out["canNotRemoveLastContactGroupResourceNames"] = cannot
		}
		f.reply(w, out)
	default:
		f.t.Errorf("the fake People API has no %s %s", r.Method, r.URL.Path)
		f.googleError(w, 404, "NOT_FOUND", "no such method")
	}
}

// methodName names a request by the People API method it is, for routing and for the
// failure injection.
func methodName(method, path string) string {
	switch {
	case path == "people:batchGet":
		return "batchGet"
	case path == "people/me/connections":
		return "connections"
	case path == "people:searchContacts":
		return "search"
	case path == "people:searchDirectoryPeople":
		return "directory"
	case path == "people:createContact":
		return "create"
	case strings.HasSuffix(path, ":updateContact"):
		return "update"
	case strings.HasSuffix(path, ":deleteContact"):
		return "delete"
	case path == "contactGroups":
		return "groups"
	case strings.HasSuffix(path, "/members:modify"):
		return "modify"
	case strings.HasPrefix(path, "contactGroups/"):
		return "group"
	case strings.HasPrefix(path, "people/") && method == http.MethodGet:
		return "get"
	}
	return method + " " + path
}

func extractMemberships(p person) []string {
	raw, ok := p["memberships"].([]any)
	if !ok {
		return nil
	}
	delete(p, "memberships")
	var out []string
	for _, m := range raw {
		cg, _ := m.(map[string]any)["contactGroupMembership"].(map[string]any)
		out = append(out, str(cg, "contactGroupResourceName"))
	}
	return out
}

// stripMetadata drops the metadata a client sent back with the entries it read, as
// Google does — except which entry is the primary one, which a client may say with
// sourcePrimary and Google then reports as primary.
func stripMetadata(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := m["metadata"].(map[string]any)
		delete(m, "metadata")
		if b, _ := meta["sourcePrimary"].(bool); b {
			m["metadata"] = map[string]any{"primary": true, "sourcePrimary": true}
		} else if b, _ := meta["primary"].(bool); b {
			m["metadata"] = map[string]any{"primary": true, "sourcePrimary": true}
		}
	}
	return list
}

func familyName(p person) string {
	if n := primaryOf(entries(p, "names")); n != nil {
		return str(n, "familyName")
	}
	return ""
}

// prefixMatch is searchContacts' matching: a prefix of a name, an e-mail address, a
// phone number or an organization.
func prefixMatch(p person, query string) bool {
	for field, keys := range map[string][]string{
		"names": {"givenName", "familyName", "displayName"}, "emailAddresses": {"value"},
		"phoneNumbers": {"value"}, "organizations": {"name"},
	} {
		for _, e := range entries(p, field) {
			for _, k := range keys {
				if strings.HasPrefix(strings.ToLower(str(e, k)), query) {
					return true
				}
			}
		}
	}
	return false
}

// requestsTo returns the requests made to one People API method, in order.
func (f *fakePeople) requestsTo(name string) []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*http.Request
	for _, r := range f.requests {
		if methodName(r.Method, strings.TrimPrefix(r.URL.EscapedPath(), "/v1/")) == name {
			out = append(out, r)
		}
	}
	return out
}

// bodyOf returns the body of the last request to a method.
func (f *fakePeople) bodyOf(name string) person {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		r := f.requests[i]
		if methodName(r.Method, strings.TrimPrefix(r.URL.EscapedPath(), "/v1/")) == name {
			var p person
			_ = json.Unmarshal([]byte(f.bodies[i]), &p)
			return p
		}
	}
	return nil
}
