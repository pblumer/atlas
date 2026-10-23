package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/worker"
)

// The Google Contacts example run end to end (ADR-0473, ADR-0476): a person searches
// the sales team's address book through the Google Contacts Worker, is offered only
// the contacts labelled "Kunden", picks one, and the next service task reads that
// contact — with the People API a deterministic fake, so nothing here needs a Google
// account. The Worker Instance is the one the worker binary builds from its
// environment, so the query handler under test is the one production registers.

const (
	kundeAnnaID    = "people/c1001"
	kundeHoldingID = "people/c1002"
	lieferantID    = "people/c2001"
	googleExample  = "Process_google_kundenauswahl"
)

// fakeAddressBook is the People API over one address book: the token endpoint, the
// search, the labels and the read of one contact — what a form lookup and get-contact
// call, and nothing else.
type fakeAddressBook struct {
	srv *httptest.Server

	mu         sync.Mutex
	people     map[string]map[string]any
	searches   []string
	unexpected []string
}

func contactJSON(id, name, email, org, label string) map[string]any {
	p := map[string]any{
		"resourceName": id, "etag": "e-" + id,
		"emailAddresses": []any{map[string]any{"value": email, "metadata": map[string]any{"primary": true}}},
		"organizations":  []any{map[string]any{"name": org}},
		"phoneNumbers":   []any{map[string]any{"value": "+41 44 555 01 01"}},
		"memberships": []any{map[string]any{"contactGroupMembership": map[string]any{
			"contactGroupId": strings.TrimPrefix(label, "contactGroups/"), "contactGroupResourceName": label}}},
	}
	if name != "" {
		given, family, _ := strings.Cut(name, " ")
		p["names"] = []any{map[string]any{"displayName": name, "givenName": given, "familyName": family,
			"metadata": map[string]any{"primary": true}}}
	}
	return p
}

func newFakeAddressBook(t *testing.T) *fakeAddressBook {
	t.Helper()
	b := &fakeAddressBook{people: map[string]map[string]any{
		kundeAnnaID:    contactJSON(kundeAnnaID, "Anna Annen", "anna@annen.ch", "Annen AG", "contactGroups/kunden"),
		kundeHoldingID: contactJSON(kundeHoldingID, "", "info@annen-holding.ch", "Annen Holding", "contactGroups/kunden"),
		lieferantID:    contactJSON(lieferantID, "Anna Lieferant", "anna@lieferant.ch", "Lieferant GmbH", "contactGroups/lieferanten"),
	}}
	b.srv = httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *fakeAddressBook) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/token":
		reply(map[string]any{"access_token": "people-test-token", "token_type": "Bearer", "expires_in": 3600})
		return
	case r.Header.Get("Authorization") != "Bearer people-test-token":
		w.WriteHeader(http.StatusUnauthorized)
		reply(map[string]any{"error": map[string]any{"code": 401, "status": "UNAUTHENTICATED"}})
		return
	case r.URL.Path == "/v1/people:searchContacts":
		q := strings.ToLower(r.URL.Query().Get("query"))
		if q == "" {
			reply(map[string]any{})
			return
		}
		b.searches = append(b.searches, q)
		var results []any
		for _, id := range []string{kundeAnnaID, kundeHoldingID, lieferantID} {
			if b.matches(b.people[id], q) {
				results = append(results, map[string]any{"person": b.people[id]})
			}
		}
		reply(map[string]any{"results": results})
	case r.URL.Path == "/v1/contactGroups":
		reply(map[string]any{"contactGroups": []any{
			map[string]any{"resourceName": "contactGroups/kunden", "name": "Kunden", "formattedName": "Kunden", "groupType": "USER_CONTACT_GROUP"},
			map[string]any{"resourceName": "contactGroups/lieferanten", "name": "Lieferanten", "formattedName": "Lieferanten", "groupType": "USER_CONTACT_GROUP"},
		}})
	case strings.HasPrefix(r.URL.Path, "/v1/people/c"):
		p, ok := b.people[strings.TrimPrefix(r.URL.Path, "/v1/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			reply(map[string]any{"error": map[string]any{"code": 404, "status": "NOT_FOUND", "message": "Requested entity was not found."}})
			return
		}
		reply(p)
	default:
		b.unexpected = append(b.unexpected, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		reply(map[string]any{"error": map[string]any{"code": 404, "status": "NOT_FOUND"}})
	}
}

// matches is Google's search as far as this test needs it: the start of a word of the
// name, the e-mail address or the organization.
func (b *fakeAddressBook) matches(p map[string]any, q string) bool {
	var words []string
	for _, field := range []struct{ list, key string }{{"names", "displayName"}, {"emailAddresses", "value"}, {"organizations", "name"}} {
		entries, _ := p[field.list].([]any)
		for _, e := range entries {
			if s, _ := e.(map[string]any)[field.key].(string); s != "" {
				words = append(words, strings.Fields(strings.ToLower(s))...)
			}
		}
	}
	for _, w := range words {
		if strings.HasPrefix(w, q) {
			return true
		}
	}
	return false
}

func (b *fakeAddressBook) recorded() (searches, unexpected []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.searches...), append([]string(nil), b.unexpected...)
}

// googleWorld is the Google example installed: the Worker google_verkauf open to forms,
// the form, the process with its user task addressed to anna, and an instance waiting
// there.
type googleWorld struct {
	st          *lookupStack
	admin, anna *http.Client
	workerToken string
	instance    uint64
	task        uint64
}

func googleExampleFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "examples", "google-kundenauswahl", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func installGoogleExample(t *testing.T, st *lookupStack) *googleWorld {
	t.Helper()
	ts := st.ts
	w := &googleWorld{st: st, admin: newClient(t)}
	if login(t, w.admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	w.anna = twoUsers(t, ts, w.admin, "anna", "mallory")[0]
	if code, b := cReq(t, w.admin, ts, "POST", "/api/v1/connectors",
		`{"name":"google_verkauf","kind":"googlecontacts","credentialsRef":"google_contacts_verkauf","formLookups":true}`); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create the Google Contacts Worker: %d (%s)", code, b)
	}
	form := googleExampleFile(t, "form-google-kundenauswahl.json")
	body, _ := json.Marshal(map[string]any{"id": "google-kundenauswahl", "name": "Kunde auswählen", "schema": json.RawMessage(form)})
	if code, b := cReq(t, w.admin, ts, "POST", "/api/v1/forms", string(body)); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("save the form: %d (%s)", code, b)
	}
	bpmn := strings.Replace(googleExampleFile(t, "kundenauswahl.bpmn"), `<zeebe:formDefinition formId="google-kundenauswahl" />`,
		`<zeebe:formDefinition formId="google-kundenauswahl" /><zeebe:assignmentDefinition assignee="anna" />`, 1)
	if code, b := cReq(t, w.admin, ts, "POST", "/api/v1/deployments", bpmn); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	code, b := cReq(t, w.admin, ts, "POST", "/api/v1/instances", `{"processId":"`+googleExample+`"}`)
	if code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, b)
	}
	var started struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	_ = json.Unmarshal(b, &started)
	w.instance = started.InstanceKey
	code, b = cReq(t, w.admin, ts, "GET", fmt.Sprintf("/api/v1/tasks?processInstance=%d", w.instance), "")
	if code != http.StatusOK {
		t.Fatalf("tasks: %d (%s)", code, b)
	}
	var tasks []struct {
		Key       uint64 `json:"key"`
		ElementID string `json:"elementId"`
	}
	if err := json.Unmarshal(listRows(t, b), &tasks); err != nil {
		t.Fatal(err)
	}
	for _, k := range tasks {
		if k.ElementID == "Activity_chooseCustomer" {
			w.task = k.Key
		}
	}
	if w.task == 0 {
		t.Fatalf("no Activity_chooseCustomer task in %s", b)
	}
	code, b = cReq(t, w.admin, ts, "POST", "/api/v1/api-tokens", `{"name":"google-worker","scope":"worker","expiresInDays":1}`)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("mint the worker token: %d (%s)", code, b)
	}
	var minted struct {
		Secret, Token string
	}
	_ = json.Unmarshal(b, &minted)
	w.workerToken = minted.Secret
	if w.workerToken == "" {
		w.workerToken = minted.Token
	}
	return w
}

// startGoogleWorker runs the Worker Instance the worker binary would build from its
// environment, with the address book's base and token endpoint pointing at the fake.
func startGoogleWorker(t *testing.T, st *lookupStack, token string, book *fakeAddressBook) {
	t.Helper()
	env := map[string]string{
		"ATLAS_GOOGLECONTACTS_CONNECTORS": "google_verkauf",
		"ATLAS_GOOGLECONTACTS_GOOGLE_VERKAUF_CREDENTIALS": `{"method":"refreshToken","clientId":"id","clientSecret":"s","refreshToken":"r","tokenUrl":"` +
			book.srv.URL + `/token"}`,
		"ATLAS_GOOGLECONTACTS_GOOGLE_VERKAUF_URL": book.srv.URL,
	}
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, "googlecontacts")
	if err != nil {
		t.Fatal(err)
	}
	w := worker.New(worker.Options{
		Server: st.ts.URL, ID: "google-1", Token: token, Connectors: built.Names,
		Wait: 2 * time.Second, Retry: 50 * time.Millisecond,
		Handlers: built.Handlers, Queries: built.Queries,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
}

func (w *googleWorld) search(t *testing.T, body string) (int, lookupReply, string) {
	t.Helper()
	code, raw := cReq(t, w.anna, w.st.ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/form-data/kunde/query", w.task), body)
	var out lookupReply
	_ = json.Unmarshal(raw, &out)
	return code, out, string(raw)
}

func TestTheGoogleExampleSelectsACustomerFromTheAddressBookAndReadsTheContact(t *testing.T) {
	st := openLookupStack(t, t.TempDir())
	world := installGoogleExample(t, st)
	book := newFakeAddressBook(t)
	startGoogleWorker(t, st, world.workerToken, book)

	deadline := time.Now().Add(20 * time.Second)
	var (
		code int
		page lookupReply
		raw  string
	)
	for {
		code, page, raw = world.search(t, `{"search":"Ann"}`)
		if code == http.StatusOK || page.Code != "worker_unavailable" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	before := st.position(t)

	// The search runs in the address book, and the form's label narrows it: the supplier
	// called Anna is in the same book and is not offered.
	if code != http.StatusOK {
		t.Fatalf("search: %d %s", code, raw)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %+v, want the two customers", page.Items)
	}
	anna, holding := page.Items[0], page.Items[1]
	if anna.Value != kundeAnnaID || anna.Label != "Anna Annen" || anna.Description != "anna@annen.ch" ||
		anna.Details["organization"] != "Annen AG" || anna.Details["phone"] != "+41 44 555 01 01" {
		t.Errorf("first item = %+v", anna)
	}
	if holding.Value != kundeHoldingID || holding.Label != "Annen Holding" || holding.Description != "info@annen-holding.ch" {
		t.Errorf("a contact without a name reads %+v, want its organization and e-mail address", holding)
	}
	if page.NextCursor != nil {
		t.Errorf("a search answers one window, but carries a cursor: %s", raw)
	}
	if strings.Contains(raw, "people-test-token") || strings.Contains(raw, "contactGroups/") || strings.Contains(raw, "Lieferant") {
		t.Fatalf("the answer carries what it must not: %s", raw)
	}

	// Searching is no fact about the process: the log has not moved.
	if after := st.position(t); after != before {
		t.Fatalf("searching moved the log from %d to %d", before, after)
	}

	// A supplier's id, submitted by hand, is checked against the address book and refused;
	// so is a value that is no contact id at all.
	for name, c := range map[string]struct {
		value  string
		status int
	}{
		"a supplier":    {lieferantID, http.StatusUnprocessableEntity},
		"a deleted one": {"people/c9999", http.StatusUnprocessableEntity},
		"an e-mail":     {"anna@annen.ch", http.StatusBadRequest},
	} {
		got, b := cReq(t, world.anna, st.ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", world.task),
			fmt.Sprintf(`{"variables":{"kunde":%q}}`, c.value))
		if got != c.status {
			t.Errorf("%s: %d (%s), want %d", name, got, b, c.status)
		}
	}

	// The person picks Anna Annen: only her id is submitted, the server checks it once
	// more, and get-contact reads the contact from it.
	if code, b := cReq(t, world.anna, st.ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", world.task),
		fmt.Sprintf(`{"variables":{"kunde":%q}}`, kundeAnnaID)); code != http.StatusOK {
		t.Fatalf("complete: %d (%s)", code, b)
	}
	awaitInstanceState(t, world.admin, st.ts, world.instance, "completed")
	vars := lookupVariables(t, world.admin, st.ts, world.instance)
	if vars["kunde"] != kundeAnnaID {
		t.Errorf("kunde = %#v", vars["kunde"])
	}
	contact, _ := vars["kontakt"].(map[string]any)
	if contact == nil || contact["id"] != kundeAnnaID || contact["organization"] != "Annen AG" || contact["email"] != "anna@annen.ch" {
		t.Errorf("kontakt = %#v, want the contact get-contact read for Anna Annen", vars["kontakt"])
	}

	searches, unexpected := book.recorded()
	if len(unexpected) != 0 {
		t.Errorf("the worker called what a lookup and get-contact do not need: %v", unexpected)
	}
	for _, q := range searches {
		if q != "ann" {
			t.Errorf("Google was searched for %q", q)
		}
	}
}
