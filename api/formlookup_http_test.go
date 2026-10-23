package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/connector/lookup"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
	"github.com/pblumer/atlas/worker"
)

// The example process, run end to end (ADR-0473): a person
// opens the task, searches a directory of five thousand people through the Entra
// Worker, picks one, completes the task, and the next service task reads that person's
// manager — with Graph a deterministic fake, so nothing here needs a tenant.

const (
	annaID    = "11111111-0000-0000-0000-000000000001"
	beatID    = "11111111-0000-0000-0000-000000000002"
	claudiaID = "11111111-0000-0000-0000-000000000003"
	chefID    = "11111111-0000-0000-0000-0000000000ff"
	exampleID = "Process_mvjoqvisfajk"
)

// graphUser is one person in the fake directory.
type graphUser struct {
	ID, DisplayName, Mail, UPN, Department string
	Enabled                                bool
	Manager                                string
}

func (u graphUser) json() map[string]any {
	m := map[string]any{"id": u.ID, "displayName": u.DisplayName, "userPrincipalName": u.UPN,
		"department": u.Department, "accountEnabled": u.Enabled,
		// What the worker must never pass on: a property nobody selected.
		"passwordProfile": map[string]any{"password": "s3cret"}}
	if u.Mail != "" {
		m["mail"] = u.Mail
	} else {
		m["mail"] = nil
	}
	return m
}

// fakeGraph is a directory that answers the requests the Entra Worker builds: a
// filtered, paged /users listing, a user by id, and a user's manager through $expand.
type fakeGraph struct {
	mu      sync.Mutex
	users   []graphUser
	filters []string
	paths   []string
}

var (
	reStartsWith = regexp.MustCompile(`startswith\((\w+),'((?:[^']|'')*)'\)`)
	reEq         = regexp.MustCompile(`(\w+) eq '((?:[^']|'')*)'`)
)

func newFakeGraph() *fakeGraph {
	g := &fakeGraph{users: []graphUser{
		{ID: annaID, DisplayName: "Anna Müller", Mail: "anna.mueller@example.com", UPN: "anna@example.com", Department: "IT", Enabled: true, Manager: chefID},
		{ID: beatID, DisplayName: "Beat Schneider", Mail: "beat.schneider@example.com", UPN: "beat@example.com", Department: "Finance", Enabled: true, Manager: chefID},
		{ID: claudiaID, DisplayName: "Claudia Weber", UPN: "claudia@example.com", Department: "IT", Enabled: true},
		{ID: chefID, DisplayName: "Chefin Muster", Mail: "chefin@example.com", UPN: "chefin@example.com", Department: "IT", Enabled: true},
	}}
	// Five thousand more, so a search that reached for the whole directory would show.
	for i := 0; i < 5000; i++ {
		dept := []string{"IT", "Finance", "HR", "Sales"}[i%4]
		g.users = append(g.users, graphUser{
			ID:          fmt.Sprintf("22222222-0000-0000-0000-%012d", i),
			DisplayName: fmt.Sprintf("Annik Person %04d", i),
			Mail:        fmt.Sprintf("annik.%04d@example.com", i),
			UPN:         fmt.Sprintf("annik%04d@example.com", i),
			Department:  dept, Enabled: i%10 != 0,
		})
	}
	return g
}

func (g *fakeGraph) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer graph-test-token" {
		http.Error(w, `{"error":{"code":"InvalidAuthenticationToken","message":"no"}}`, http.StatusUnauthorized)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paths = append(g.paths, r.URL.RequestURI())
	q := r.URL.Query()
	switch {
	case r.URL.Path == "/v1.0/users":
		g.filters = append(g.filters, q.Get("$filter"))
		var match []graphUser
		for _, u := range g.users {
			if g.matches(u, q.Get("$filter")) {
				match = append(match, u)
			}
		}
		top, _ := strconv.Atoi(q.Get("$top"))
		if top <= 0 {
			top = 100
		}
		skip, _ := strconv.Atoi(q.Get("$skiptoken"))
		end := min(skip+top, len(match))
		page := []any{}
		for _, u := range match[skip:end] {
			page = append(page, u.json())
		}
		out := map[string]any{"value": page}
		if end < len(match) {
			q.Set("$skiptoken", strconv.Itoa(end))
			out["@odata.nextLink"] = "http://" + r.Host + "/v1.0/users?" + q.Encode()
		}
		_ = json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(r.URL.Path, "/v1.0/users/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1.0/users/")
		for _, u := range g.users {
			if u.ID != id {
				continue
			}
			if strings.HasPrefix(q.Get("$expand"), "manager") {
				out := map[string]any{"id": u.ID}
				if u.Manager != "" {
					out["manager"] = map[string]any{"@odata.type": "#microsoft.graph.user", "id": u.Manager}
				}
				_ = json.NewEncoder(w).Encode(out)
				return
			}
			_ = json.NewEncoder(w).Encode(u.json())
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"Request_ResourceNotFound","message":"Resource does not exist."}}`))
	default:
		http.NotFound(w, r)
	}
}

// matches evaluates the filter the Worker builds — prefix clauses joined by or, and-ed
// with equality clauses — the way Graph would, case-insensitively.
func (g *fakeGraph) matches(u graphUser, filter string) bool {
	prop := func(p string) string {
		switch p {
		case "displayName":
			return u.DisplayName
		case "mail":
			return u.Mail
		case "userPrincipalName":
			return u.UPN
		case "department":
			return u.Department
		}
		return ""
	}
	if prefixes := reStartsWith.FindAllStringSubmatch(filter, -1); len(prefixes) > 0 {
		any := false
		for _, m := range prefixes {
			term := strings.ReplaceAll(m[2], "''", "'")
			if strings.HasPrefix(strings.ToLower(prop(m[1])), strings.ToLower(term)) {
				any = true
			}
		}
		if !any {
			return false
		}
	}
	rest := reStartsWith.ReplaceAllString(filter, "")
	for _, m := range reEq.FindAllStringSubmatch(rest, -1) {
		if !strings.EqualFold(prop(m[1]), strings.ReplaceAll(m[2], "''", "'")) {
			return false
		}
	}
	if strings.Contains(filter, "accountEnabled eq true") && !u.Enabled {
		return false
	}
	return true
}

func (g *fakeGraph) recorded() ([]string, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.filters...), append([]string(nil), g.paths...)
}

type graphToken struct{}

func (graphToken) Token(context.Context) (string, error) { return "graph-test-token", nil }

// lookupStack is one Atlas over a data directory, with its log and store in reach so a
// test can read the log position and reopen the same directory as a restart does.
type lookupStack struct {
	ts    *httptest.Server
	store *state.Store
	close func()
}

func openLookupStack(t *testing.T, dir string) *lookupStack {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword")
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir, api.WithAuth())
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	var once sync.Once
	st := &lookupStack{ts: ts, store: store, close: func() {
		once.Do(func() {
			ts.Close()
			srv.Close()
			_ = store.Close()
			_ = log.Close()
		})
	}}
	t.Cleanup(st.close)
	return st
}

func (st *lookupStack) position(t *testing.T) uint64 {
	t.Helper()
	pos, err := st.store.LastAppliedPosition()
	if err != nil {
		t.Fatal(err)
	}
	return pos
}

// exampleFile reads one file of the shipped example.
func exampleFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "examples", "entra-benutzerauswahl", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// lookupWorld is the example installed: the Entra Worker ucc_entra open to forms, the
// form, the process with its user task addressed to anna, and an instance waiting there.
type lookupWorld struct {
	st          *lookupStack
	admin, anna *http.Client
	mallory     *http.Client
	workerToken string
	instance    uint64
	task        uint64
	workerID    string
}

func installExample(t *testing.T, st *lookupStack, bpmn string) *lookupWorld {
	t.Helper()
	ts := st.ts
	w := &lookupWorld{st: st, admin: newClient(t)}
	if login(t, w.admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	users := twoUsers(t, ts, w.admin, "anna", "mallory")
	w.anna, w.mallory = users[0], users[1]
	if code, b := cReq(t, w.admin, ts, "POST", "/api/v1/connectors",
		`{"name":"ucc_entra","kind":"entra","credentialsRef":"entra-ucc","formLookups":true}`); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create the Entra Worker: %d (%s)", code, b)
	}
	form := exampleFile(t, "form-entra-benutzerauswahl.json")
	body, _ := json.Marshal(map[string]any{"id": "entra-benutzerauswahl", "name": "Person auswählen", "schema": json.RawMessage(form)})
	if code, b := cReq(t, w.admin, ts, "POST", "/api/v1/forms", string(body)); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("save the form: %d (%s)", code, b)
	}
	if code, b := cReq(t, w.admin, ts, "POST", "/api/v1/deployments", bpmn); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	code, b := cReq(t, w.admin, ts, "POST", "/api/v1/instances", `{"processId":"`+exampleID+`"}`)
	if code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, b)
	}
	var started struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	_ = json.Unmarshal(b, &started)
	w.instance = started.InstanceKey
	w.task = taskOf(t, w.admin, ts, w.instance)
	code, b = cReq(t, w.admin, ts, "POST", "/api/v1/api-tokens", `{"name":"entra-worker","scope":"worker","expiresInDays":1}`)
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

// assignedToAnna is the example with its task addressed to one person, so the object
// question has somebody to refuse.
func assignedToAnna(bpmn string) string {
	return strings.Replace(bpmn, `<zeebe:formDefinition formId="entra-benutzerauswahl" />`,
		`<zeebe:formDefinition formId="entra-benutzerauswahl" /><zeebe:assignmentDefinition assignee="anna" />`, 1)
}

func taskOf(t *testing.T, c *http.Client, ts *httptest.Server, instance uint64) uint64 {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", fmt.Sprintf("/api/v1/tasks?processInstance=%d", instance), "")
	if code != http.StatusOK {
		t.Fatalf("tasks: %d (%s)", code, body)
	}
	var tasks []struct {
		Key       uint64 `json:"key"`
		ElementID string `json:"elementId"`
	}
	if err := json.Unmarshal(listRows(t, body), &tasks); err != nil {
		t.Fatal(err)
	}
	for _, k := range tasks {
		if k.ElementID == "Activity_chooseUser" {
			return k.Key
		}
	}
	t.Fatalf("no Activity_chooseUser task in %s", body)
	return 0
}

// startEntraWorker runs a real Worker Instance in this process: the job loop and the
// query loop, with a registry pointing at the fake Graph instead of a tenant.
func startEntraWorker(t *testing.T, st *lookupStack, token, id string, graph http.Handler, holds ...string) *worker.Worker {
	t.Helper()
	w := newEntraWorker(t, st, token, id, graph, holds...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return w
}

// newEntraWorker builds the Worker Instance without running it, for a test that drives
// its polls one at a time.
func newEntraWorker(t *testing.T, st *lookupStack, token, id string, graph http.Handler, holds ...string) *worker.Worker {
	t.Helper()
	gs := httptest.NewServer(graph)
	t.Cleanup(gs.Close)
	reg := entra.NewRegistry()
	for _, name := range holds {
		reg.Register(name, entra.NewGraphClient(graphToken{}, gs.URL+"/v1.0", gs.Client()))
	}
	w := worker.New(worker.Options{
		Server: st.ts.URL, ID: id, Token: token, Connectors: holds,
		Wait: 2 * time.Second, Retry: 50 * time.Millisecond,
		Handlers: map[string]worker.Exec{compiler.EntraJobType: worker.ExecFunc(func(ctx context.Context, j worker.Job) (map[string]any, error) {
			return worker.RunEntraJob(ctx, j, reg)
		})},
		Queries: map[string]worker.QueryFunc{compiler.EntraJobType: func(ctx context.Context, c string, q lookup.Query) (lookup.Page, error) {
			return entra.RunLookup(ctx, c, q, reg)
		}},
	})
	return w
}

type lookupReply struct {
	Items []struct {
		Value       string            `json:"value"`
		Label       string            `json:"label"`
		Description string            `json:"description"`
		Details     map[string]string `json:"details"`
	} `json:"items"`
	NextCursor *string `json:"nextCursor"`
	Code       string  `json:"code"`
	Error      string  `json:"error"`
}

func (w *lookupWorld) search(t *testing.T, c *http.Client, body string) (int, lookupReply, string) {
	t.Helper()
	code, raw := cReq(t, c, w.st.ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/form-data/user/query", w.task), body)
	var out lookupReply
	_ = json.Unmarshal(raw, &out)
	return code, out, string(raw)
}

// awaitWorker waits until a Worker Instance holding ucc_entra has polled for queries:
// until then the server answers worker_unavailable, which is the point of presence.
func (w *lookupWorld) awaitWorker(t *testing.T, c *http.Client) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, out, raw := w.search(t, c, `{"search":"Anna","filters":{"department":"IT"}}`)
		if code == http.StatusOK {
			return
		}
		if out.Code != "worker_unavailable" || time.Now().After(deadline) {
			t.Fatalf("the worker never answered: %d %s", code, raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func lookupVariables(t *testing.T, c *http.Client, ts *httptest.Server, instance uint64) map[string]any {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/variables", instance), "")
	if code != http.StatusOK {
		t.Fatalf("variables: %d (%s)", code, body)
	}
	out := map[string]any{}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, body)
	}
	return out
}

func lookupInstanceState(t *testing.T, c *http.Client, ts *httptest.Server, instance uint64) string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/timeline", instance), "")
	if code != http.StatusOK {
		t.Fatalf("timeline: %d (%s)", code, body)
	}
	var out struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(body, &out)
	return out.State
}

func awaitInstanceState(t *testing.T, c *http.Client, ts *httptest.Server, instance uint64, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		got := lookupInstanceState(t, c, ts, instance)
		if strings.EqualFold(got, want) {
			return
		}
		if time.Now().After(deadline) {
			_, inc := cReq(t, c, ts, "GET", "/api/v1/incidents", "")
			t.Fatalf("instance state %q, want %q\nincidents: %s", got, want, inc)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTheExampleSelectsAPersonThroughTheDirectoryAndReadsTheirManager(t *testing.T) {
	dir := t.TempDir()
	st := openLookupStack(t, dir)
	world := installExample(t, st, assignedToAnna(exampleFile(t, "benutzerauswahl.bpmn")))
	graph := newFakeGraph()
	startEntraWorker(t, st, world.workerToken, "entra-1", graph, "ucc_entra")
	world.awaitWorker(t, world.anna)

	before := st.position(t)

	// The search runs in the directory: one bounded page, filtered on the server by the
	// department the person picked.
	code, page, raw := world.search(t, world.anna, `{"search":"Ann","filters":{"department":"IT"}}`)
	if code != http.StatusOK {
		t.Fatalf("search: %d %s", code, raw)
	}
	if len(page.Items) != 25 {
		t.Fatalf("items = %d, want one page of 25 out of the department's hundreds", len(page.Items))
	}
	if page.Items[0].Value != annaID || page.Items[0].Label != "Anna Müller" || page.Items[0].Details["department"] != "IT" {
		t.Errorf("first item = %+v", page.Items[0])
	}
	for _, it := range page.Items {
		if it.Details["department"] != "IT" {
			t.Errorf("%s from %s: the department filter did not hold", it.Label, it.Details["department"])
		}
	}
	if page.NextCursor == nil {
		t.Fatal("no cursor although the department holds more matches")
	}
	if strings.Contains(raw, "passwordProfile") || strings.Contains(raw, "s3cret") || strings.Contains(raw, "graph-test-token") ||
		strings.Contains(raw, "/v1.0/") || strings.Contains(raw, "skiptoken") {
		t.Fatalf("the answer carries what it must not: %s", raw)
	}

	// Load more continues the same search, and only that.
	code, more, raw := world.search(t, world.anna, `{"search":"Ann","filters":{"department":"IT"},"cursor":"`+*page.NextCursor+`"}`)
	if code != http.StatusOK || len(more.Items) != 25 || more.Items[0].Value == page.Items[0].Value {
		t.Fatalf("load more: %d %d items %s", code, len(more.Items), raw)
	}

	// A user without a mailbox shows the user principal name instead.
	code, claudia, raw := world.search(t, world.anna, `{"search":"Claudia","filters":{"department":"IT"}}`)
	if code != http.StatusOK || len(claudia.Items) != 1 || claudia.Items[0].Description != "claudia@example.com" {
		t.Fatalf("claudia: %d %s", code, raw)
	}

	filters, paths := graph.recorded()
	for _, f := range filters {
		if !strings.Contains(f, "department eq 'IT'") || !strings.Contains(f, "accountEnabled eq true") {
			t.Errorf("a search reached Graph without its filters: %q", f)
		}
	}
	for _, p := range paths {
		if strings.Contains(p, "$top=") && !strings.Contains(p, "$top=25") {
			t.Errorf("a search asked Graph for another page size: %s", p)
		}
	}

	// None of that is a fact about the process: the log has not moved.
	if after := st.position(t); after != before {
		t.Fatalf("searching moved the log from %d to %d", before, after)
	}

	// The person picks Anna: only her id is submitted, and the server checks it once more
	// against the directory before the task completes.
	complete := fmt.Sprintf(`{"variables":{"abteilung":"IT","user":%q}}`, annaID)
	if code, b := cReq(t, world.anna, st.ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", world.task), complete); code != http.StatusOK {
		t.Fatalf("complete: %d (%s)", code, b)
	}
	awaitInstanceState(t, world.admin, st.ts, world.instance, "completed")

	vars := lookupVariables(t, world.admin, st.ts, world.instance)
	if vars["user"] != annaID {
		t.Errorf("user = %#v, want Anna's object id", vars["user"])
	}
	boss, _ := vars["vorgesetzter"].(map[string]any)
	if boss == nil || boss["id"] != chefID {
		t.Errorf("vorgesetzter = %#v, want the manager get-user-manager read for Anna", vars["vorgesetzter"])
	}
	if _, ok := vars["userList"]; ok {
		t.Error("the improved process still lists the directory into a variable")
	}

	// Restart over the same directory and replay: the recovered instance is the live one.
	live := map[string]any{"state": lookupInstanceState(t, world.admin, st.ts, world.instance), "vars": vars}
	st.close()
	again := openLookupStack(t, dir)
	admin := newClient(t)
	if login(t, admin, again.ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login after restart failed")
	}
	recovered := map[string]any{"state": lookupInstanceState(t, admin, again.ts, world.instance),
		"vars": lookupVariables(t, admin, again.ts, world.instance)}
	if lj, rj := canonical(live), canonical(recovered); lj != rj {
		t.Fatalf("recovered state differs from live:\n live %s\n  rec %s", lj, rj)
	}
}

// canonical renders a value with sorted keys, so two states compare by content.
func canonical(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(strconv.Quote(k) + ":" + canonical(t[k]))
		}
		b.WriteString("}")
		return b.String()
	default:
		raw, _ := json.Marshal(t)
		return string(raw)
	}
}

// The security boundary, door by door: who may search, through which Worker, with what.
func TestADirectorySearchIsGuardedAtEveryDoor(t *testing.T) {
	st := openLookupStack(t, t.TempDir())
	world := installExample(t, st, assignedToAnna(exampleFile(t, "benutzerauswahl.bpmn")))
	graph := newFakeGraph()
	startEntraWorker(t, st, world.workerToken, "entra-1", graph, "ucc_entra")
	world.awaitWorker(t, world.anna)
	ts := st.ts
	path := fmt.Sprintf("/api/v1/tasks/%d/form-data/user/query", world.task)

	t.Run("an unauthenticated caller", func(t *testing.T) {
		code, _ := cReq(t, http.DefaultClient, ts, "POST", path, `{"search":"Ann"}`)
		if code != http.StatusUnauthorized {
			t.Errorf("%d, want 401", code)
		}
	})
	t.Run("somebody else's task", func(t *testing.T) {
		code, out, raw := world.search(t, world.mallory, `{"search":"Ann","filters":{"department":"IT"}}`)
		if code != http.StatusForbidden || out.Code != "forbidden" {
			t.Errorf("%d %s, want 403", code, raw)
		}
	})
	t.Run("the client names the tenant, the resource or a URL", func(t *testing.T) {
		for _, body := range []string{
			`{"search":"Ann","worker":"contoso"}`,
			`{"search":"Ann","tenant":"contoso.onmicrosoft.com"}`,
			`{"search":"Ann","resource":"groups"}`,
			`{"search":"Ann","url":"https://graph.microsoft.com/v1.0/users?$filter=id ne null"}`,
			`{"search":"Ann","filters":{"jobTitle":"CEO"}}`,
			`{"search":"Ann","filters":{"department":"Geheim"}}`,
		} {
			if code, out, raw := world.search(t, world.anna, body); code != http.StatusBadRequest || out.Code != "invalid_query" {
				t.Errorf("%s: %d %s", body, code, raw)
			}
		}
	})
	t.Run("injected OData stays a literal", func(t *testing.T) {
		code, out, raw := world.search(t, world.anna, `{"search":"x') or startswith(id,'","filters":{"department":"IT"}}`)
		if code != http.StatusOK || len(out.Items) != 0 {
			t.Fatalf("%d %s", code, raw)
		}
		filters, _ := graph.recorded()
		last := filters[len(filters)-1]
		if !strings.Contains(last, "startswith(displayName,'x'') or startswith(id,''')") {
			t.Errorf("the term left its literal: %q", last)
		}
	})
	t.Run("a person cannot drive the worker protocol", func(t *testing.T) {
		code, _ := cReq(t, world.anna, ts, "POST", "/api/v1/worker-queries/activate", `{"type":"io.atlas.entra","worker":"me","connectors":["ucc_entra"]}`)
		if code != http.StatusForbidden {
			t.Errorf("activate as a person: %d, want 403", code)
		}
	})
	t.Run("a worker token reaches nothing but the protocol", func(t *testing.T) {
		req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(`{"search":"Ann"}`))
		req.Header.Set("Authorization", "Bearer "+world.workerToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("a worker token searching through a task: %d, want 403", res.StatusCode)
		}
	})
	t.Run("a Worker closed to forms", func(t *testing.T) {
		id := connectorID(t, world.admin, ts, "ucc_entra")
		if code, b := cReq(t, world.admin, ts, "PATCH", "/api/v1/connectors/"+id, `{"formLookups":false}`); code != http.StatusOK {
			t.Fatalf("close: %d %s", code, b)
		}
		code, out, raw := world.search(t, world.anna, `{"search":"Ann","filters":{"department":"IT"}}`)
		if code != http.StatusForbidden || out.Code != "forbidden" {
			t.Errorf("%d %s, want 403", code, raw)
		}
		if code, b := cReq(t, world.admin, ts, "PATCH", "/api/v1/connectors/"+id, `{"formLookups":true}`); code != http.StatusOK {
			t.Fatalf("reopen: %d %s", code, b)
		}
	})
	t.Run("only a kind a form can search may be opened", func(t *testing.T) {
		code, b := cReq(t, world.admin, ts, "POST", "/api/v1/connectors",
			`{"name":"mailer","kind":"mail","endpoint":"smtp://localhost:25","sender":"a@example.com","formLookups":true}`)
		if code != http.StatusBadRequest || !strings.Contains(string(b), "formLookups") {
			t.Errorf("%d %s, want 400", code, b)
		}
		// Nor can an existing Worker of such a kind be opened afterwards.
		code, b = cReq(t, world.admin, ts, "POST", "/api/v1/connectors",
			`{"name":"mailer","kind":"mail","endpoint":"smtp://localhost:25","sender":"a@example.com"}`)
		if code != http.StatusOK && code != http.StatusCreated {
			t.Fatalf("create the mail Worker: %d %s", code, b)
		}
		id := connectorID(t, world.admin, ts, "mailer")
		if code, b := cReq(t, world.admin, ts, "PATCH", "/api/v1/connectors/"+id, `{"formLookups":true}`); code != http.StatusBadRequest || !strings.Contains(string(b), "formLookups") {
			t.Errorf("open a mail Worker to forms: %d %s, want 400", code, b)
		}
	})
	t.Run("a task without a form has no directory field", func(t *testing.T) {
		const plain = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d_ohne_form" targetNamespace="http://atlas.test">
  <bpmn:process id="ohne_form" isExecutable="true">
    <bpmn:startEvent id="s"/><bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="Activity_chooseUser"/>
    <bpmn:userTask id="Activity_chooseUser" name="Ohne Formular"/><bpmn:sequenceFlow id="f2" sourceRef="Activity_chooseUser" targetRef="e"/>
    <bpmn:endEvent id="e"/>
  </bpmn:process>
</bpmn:definitions>`
		if code, b := cReq(t, world.admin, ts, "POST", "/api/v1/deployments", plain); code != http.StatusOK {
			t.Fatalf("deploy: %d (%s)", code, b)
		}
		code, b := cReq(t, world.admin, ts, "POST", "/api/v1/instances", `{"processId":"ohne_form"}`)
		if code != http.StatusOK {
			t.Fatalf("start: %d (%s)", code, b)
		}
		var started struct {
			InstanceKey uint64 `json:"instanceKey"`
		}
		_ = json.Unmarshal(b, &started)
		task := taskOf(t, world.admin, ts, started.InstanceKey)
		code, b = cReq(t, world.anna, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/form-data/user/query", task), `{"search":"Ann"}`)
		if code != http.StatusNotFound || !strings.Contains(string(b), "not_found") {
			t.Errorf("%d %s, want 404", code, b)
		}
	})
	t.Run("a form naming what the server cannot honour is not saved", func(t *testing.T) {
		bad := strings.Replace(exampleFile(t, "form-entra-benutzerauswahl.json"), `"userPrincipalName"]`, `"passwordProfile"]`, 1)
		body, _ := json.Marshal(map[string]any{"id": "bad-form", "schema": json.RawMessage(bad)})
		if code, b := cReq(t, world.admin, ts, "POST", "/api/v1/forms", string(body)); code != http.StatusBadRequest || !strings.Contains(string(b), "passwordProfile") {
			t.Errorf("%d %s, want 400 naming the property", code, b)
		}
	})
	t.Run("a submission the field cannot hold does not complete the task", func(t *testing.T) {
		for _, tc := range []struct {
			vars string
			code int
		}{
			{`{"abteilung":"IT","user":{"id":"` + annaID + `","displayName":"Anna"}}`, http.StatusBadRequest},
			{`{"abteilung":"IT","user":"anna@example.com"}`, http.StatusBadRequest},
			{`{"abteilung":"IT"}`, http.StatusBadRequest},
			{`{"abteilung":"IT","user":"` + beatID + `"}`, http.StatusUnprocessableEntity},                       // Finance, not IT
			{`{"abteilung":"IT","user":"33333333-0000-0000-0000-000000000000"}`, http.StatusUnprocessableEntity}, // nobody
		} {
			code, b := cReq(t, world.anna, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", world.task), `{"variables":`+tc.vars+`}`)
			if code != tc.code {
				t.Errorf("%s: %d %s, want %d", tc.vars, code, b, tc.code)
			}
		}
		if got := lookupInstanceState(t, world.admin, ts, world.instance); strings.EqualFold(got, "completed") {
			t.Fatal("a refused submission completed the instance")
		}
		if taskOf(t, world.admin, ts, world.instance) != world.task {
			t.Fatal("the task is no longer open after refused submissions")
		}
	})
	t.Run("a completion that says nothing is held to the field too", func(t *testing.T) {
		for _, body := range []string{``, `{}`, `{"variables":`} {
			code, b := cReq(t, world.anna, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", world.task), body)
			if code != http.StatusBadRequest {
				t.Errorf("%q: %d %s, want 400", body, code, b)
			}
		}
		if taskOf(t, world.admin, ts, world.instance) != world.task {
			t.Fatal("the task is no longer open after an empty completion")
		}
	})
	t.Run("no such task", func(t *testing.T) {
		code, b := cReq(t, world.anna, ts, "POST", "/api/v1/tasks/999999999/form-data/user/query", `{"search":"Ann"}`)
		if code != http.StatusNotFound || !strings.Contains(string(b), "not_found") {
			t.Errorf("%d %s, want 404", code, b)
		}
	})
	t.Run("the editor reads what a field can name, and only the editor", func(t *testing.T) {
		code, b := cReq(t, world.admin, ts, "GET", "/api/v1/form-data/sources", "")
		if code != http.StatusOK {
			t.Fatalf("%d %s", code, b)
		}
		var out struct {
			Providers []struct{ WorkerType, Resource string } `json:"providers"`
			Workers   []map[string]any                        `json:"workers"`
		}
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Providers) != 1 || out.Providers[0].WorkerType != "entra" || out.Providers[0].Resource != "users" {
			t.Errorf("providers = %+v", out.Providers)
		}
		if len(out.Workers) != 1 || out.Workers[0]["name"] != "ucc_entra" || out.Workers[0]["formLookups"] != true {
			t.Errorf("workers = %v", out.Workers)
		}
		for _, k := range []string{"credentialsRef", "endpoint", "secret"} {
			if strings.Contains(string(b), k) {
				t.Errorf("the listing carries %q: %s", k, b)
			}
		}
		if code, _ := cReq(t, world.anna, ts, "GET", "/api/v1/form-data/sources", ""); code != http.StatusForbidden {
			t.Errorf("a person who may not author: %d, want 403", code)
		}
	})
}

// A Worker Instance holding another tenant is not asked: the query names its Worker,
// and only an Instance reporting that Worker leases it.
func TestASearchNeverReachesAnotherTenant(t *testing.T) {
	st := openLookupStack(t, t.TempDir())
	world := installExample(t, st, exampleFile(t, "benutzerauswahl.bpmn"))
	other := newFakeGraph()
	contoso := newEntraWorker(t, st, world.workerToken, "contoso-1", other, "contoso")
	// One idle poll: the contoso Instance is present, and ucc_entra has nobody.
	if err := contoso.AnswerQueriesOnce(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	code, out, raw := world.search(t, world.anna, `{"search":"Ann","filters":{"department":"IT"}}`)
	if code != http.StatusServiceUnavailable || out.Code != "worker_unavailable" {
		t.Fatalf("%d %s, want 503 worker_unavailable", code, raw)
	}
	// Nothing was queued for it either: its next poll leases nothing.
	if err := contoso.AnswerQueriesOnce(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if _, paths := other.recorded(); len(paths) > 0 {
		t.Fatalf("the other tenant's directory was asked: %v", paths)
	}
}

func connectorID(t *testing.T, c *http.Client, ts *httptest.Server, name string) string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/connectors", "")
	if code != http.StatusOK {
		t.Fatalf("list workers: %d %s", code, body)
	}
	var recs []struct{ ID, Name string }
	if err := json.Unmarshal(body, &recs); err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Name == name {
			return r.ID
		}
	}
	t.Fatalf("no worker %q in %s", name, body)
	return ""
}

// A restart while the task waits loses nothing durable and keeps nothing ephemeral: the
// task is still open, the cursor the last process handed out is refused, and a fresh
// search and the completion work as before.
func TestARestartBeforeTheChoiceKeepsTheTaskAndForgetsTheSearch(t *testing.T) {
	dir := t.TempDir()
	st := openLookupStack(t, dir)
	world := installExample(t, st, assignedToAnna(exampleFile(t, "benutzerauswahl.bpmn")))
	graph := newFakeGraph()
	first := startEntraWorkerUntil(t, st, world.workerToken, "entra-1", graph, "ucc_entra")
	world.awaitWorker(t, world.anna)
	_, page, raw := world.search(t, world.anna, `{"search":"Ann","filters":{"department":"IT"}}`)
	if page.NextCursor == nil {
		t.Fatalf("no cursor: %s", raw)
	}
	first()
	st.close()

	again := openLookupStack(t, dir)
	world.st = again
	world.anna = newClient(t)
	if login(t, world.anna, again.ts, "anna", "password1") != http.StatusOK {
		t.Fatal("anna login after restart failed")
	}
	admin := newClient(t)
	if login(t, admin, again.ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login after restart failed")
	}
	if got := taskOf(t, admin, again.ts, world.instance); got != world.task {
		t.Fatalf("task after restart = %d, want %d", got, world.task)
	}
	startEntraWorker(t, again, world.workerToken, "entra-1", graph, "ucc_entra")
	world.awaitWorker(t, world.anna)
	if code, out, raw := world.search(t, world.anna, `{"search":"Ann","filters":{"department":"IT"},"cursor":"`+*page.NextCursor+`"}`); code != http.StatusBadRequest || out.Code != "invalid_cursor" {
		t.Fatalf("an old process's cursor: %d %s, want 400 invalid_cursor", code, raw)
	}
	before := again.position(t)
	if code, out, raw := world.search(t, world.anna, `{"search":"Anna","filters":{"department":"IT"}}`); code != http.StatusOK || len(out.Items) != 1 {
		t.Fatalf("a fresh search: %d %s", code, raw)
	}
	if after := again.position(t); after != before {
		t.Fatalf("a search after the restart moved the log from %d to %d", before, after)
	}
	if code, b := cReq(t, world.anna, again.ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", world.task),
		fmt.Sprintf(`{"variables":{"abteilung":"IT","user":%q}}`, annaID)); code != http.StatusOK {
		t.Fatalf("complete after restart: %d (%s)", code, b)
	}
	awaitInstanceState(t, admin, again.ts, world.instance, "completed")
	if v := lookupVariables(t, admin, again.ts, world.instance)["user"]; v != annaID {
		t.Fatalf("user = %v", v)
	}
}

// startEntraWorkerUntil is startEntraWorker with the stop in the caller's hands, for a
// test that stops a server under a worker and starts another.
func startEntraWorkerUntil(t *testing.T, st *lookupStack, token, id string, graph http.Handler, holds ...string) func() {
	t.Helper()
	w := newEntraWorker(t, st, token, id, graph, holds...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.Run(ctx)
	}()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}
