package sharepoint_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The two processes in examples/sharepoint-fallakte are run here end to end: the real
// compiler, the real engine and the real Graph client, against a SharePoint site kept in
// memory. A shipped example that compiles and lays out cleanly can still be wrong about
// what it asks of Graph — a path one segment off, a column the result does not carry, a
// loop that never ends — and only running it says so.

// exampleSiteID is the Graph id the site URL in the examples resolves to.
const exampleSiteID = "contoso.sharepoint.com,aaaa,bbbb"

type staticToken struct{}

func (staticToken) Token(context.Context) (string, error) { return "token", nil }

// fakeSite is one SharePoint site: its lists, its document libraries, and how often
// each kind of request reached it.
type fakeSite struct {
	mu      sync.Mutex
	lists   map[string]map[int]map[string]any // list → id → columns
	files   map[string]map[string]*fakeFile   // library → path → file or folder
	nextID  int
	uploads int
	resumed int // list requests that carried a $skiptoken
	url     string
}

type fakeFile struct {
	id      string
	folder  bool
	content []byte
	fields  map[string]any
}

func newFakeSite() *fakeSite {
	return &fakeSite{lists: map[string]map[int]map[string]any{}, files: map[string]map[string]*fakeFile{}, nextID: 100}
}

func (s *fakeSite) addItem(list string, id int, fields map[string]any) {
	if s.lists[list] == nil {
		s.lists[list] = map[int]map[string]any{}
	}
	s.lists[list][id] = fields
}

func (s *fakeSite) addFile(library, path string, f *fakeFile) {
	if s.files[library] == nil {
		s.files[library] = map[string]*fakeFile{}
	}
	if f.id == "" {
		s.nextID++
		f.id = "F" + strconv.Itoa(s.nextID)
	}
	s.files[library][path] = f
}

func (s *fakeSite) item(list string, id int, fields map[string]any) map[string]any {
	return map[string]any{"id": strconv.Itoa(id), "webUrl": s.url + "/lists/" + list + "/" + strconv.Itoa(id), "fields": fields}
}

func (s *fakeSite) driveItem(library, path string, f *fakeFile) map[string]any {
	m := map[string]any{
		"id": f.id, "name": path[strings.LastIndex(path, "/")+1:],
		"webUrl":                       s.url + "/" + library + "/" + path,
		"@microsoft.graph.downloadUrl": "https://download.example/secret",
	}
	if f.folder {
		m["folder"] = map[string]any{}
	} else {
		m["file"] = map[string]any{}
		m["size"] = len(f.content)
	}
	return m
}

var (
	siteLookupRe = regexp.MustCompile(`^/sites/contoso\.sharepoint\.com:/sites/verwaltung$`)
	itemsRe      = regexp.MustCompile(`^/sites/` + regexp.QuoteMeta(exampleSiteID) + `/lists/([^/]+)/items(?:/(\d+))?(/fields)?$`)
	driveRe      = regexp.MustCompile(`^/sites/` + regexp.QuoteMeta(exampleSiteID) + `/lists/([^/]+)/drive/(.*)$`)
	filterRe     = regexp.MustCompile(`^fields/(\w+) eq '(.*)'$`)
)

func (s *fakeSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	answer := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if v != nil {
			_ = json.NewEncoder(w).Encode(v)
		}
	}
	notFound := func() { answer(http.StatusNotFound, map[string]any{"error": map[string]any{"code": "itemNotFound"}}) }
	path := r.URL.Path

	if siteLookupRe.MatchString(path) && r.Method == http.MethodGet {
		answer(http.StatusOK, map[string]any{"id": exampleSiteID})
		return
	}
	if m := itemsRe.FindStringSubmatch(path); m != nil {
		list := s.lists[m[1]]
		if list == nil {
			notFound()
			return
		}
		switch {
		case m[2] == "" && r.Method == http.MethodPost:
			var req struct{ Fields map[string]any }
			_ = json.Unmarshal(body, &req)
			s.nextID++
			list[s.nextID] = req.Fields
			answer(http.StatusCreated, s.item(m[1], s.nextID, req.Fields))
		case m[2] == "" && r.Method == http.MethodGet:
			s.listItems(w, r, m[1], list, answer)
		case m[3] != "" && r.Method == http.MethodPatch:
			id, _ := strconv.Atoi(m[2])
			fields, ok := list[id]
			if !ok {
				notFound()
				return
			}
			var patch map[string]any
			_ = json.Unmarshal(body, &patch)
			for k, v := range patch {
				fields[k] = v
			}
			answer(http.StatusOK, fields)
		case r.Method == http.MethodGet:
			id, _ := strconv.Atoi(m[2])
			fields, ok := list[id]
			if !ok {
				notFound()
				return
			}
			answer(http.StatusOK, s.item(m[1], id, fields))
		case r.Method == http.MethodDelete:
			id, _ := strconv.Atoi(m[2])
			if _, ok := list[id]; !ok {
				notFound()
				return
			}
			delete(list, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			answer(http.StatusMethodNotAllowed, nil)
		}
		return
	}
	if m := driveRe.FindStringSubmatch(path); m != nil {
		library, rest := m[1], m[2]
		files := s.files[library]
		if files == nil {
			files = map[string]*fakeFile{}
			s.files[library] = files
		}
		switch {
		case strings.HasPrefix(rest, "items/") && strings.HasSuffix(rest, "/listItem/fields") && r.Method == http.MethodPatch:
			id := strings.TrimSuffix(strings.TrimPrefix(rest, "items/"), "/listItem/fields")
			for _, f := range files {
				if f.id == id {
					var patch map[string]any
					_ = json.Unmarshal(body, &patch)
					f.fields = patch
					answer(http.StatusOK, patch)
					return
				}
			}
			notFound()
		case rest == "root/children" || (strings.HasPrefix(rest, "root:/") && strings.HasSuffix(rest, ":/children")):
			parent := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(rest, "root/children"), "root:/"), ":/children")
			if r.Method == http.MethodPost {
				var req struct {
					Name     string `json:"name"`
					Conflict string `json:"@microsoft.graph.conflictBehavior"`
				}
				_ = json.Unmarshal(body, &req)
				p := strings.TrimPrefix(parent+"/"+req.Name, "/")
				if _, exists := files[p]; exists {
					answer(http.StatusConflict, map[string]any{"error": map[string]any{"code": "nameAlreadyExists"}})
					return
				}
				f := &fakeFile{folder: true}
				s.addFile(library, p, f)
				answer(http.StatusCreated, s.driveItem(library, p, f))
				return
			}
			answer(http.StatusMethodNotAllowed, nil)
		case strings.HasPrefix(rest, "root:/") && strings.HasSuffix(rest, ":/content"):
			p := strings.TrimSuffix(strings.TrimPrefix(rest, "root:/"), ":/content")
			switch r.Method {
			case http.MethodPut:
				s.uploads++
				f, ok := files[p]
				if !ok {
					f = &fakeFile{}
					s.addFile(library, p, f)
				}
				f.content = body
				answer(http.StatusCreated, s.driveItem(library, p, f))
			case http.MethodGet:
				f, ok := files[p]
				if !ok || f.folder {
					notFound()
					return
				}
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write(f.content)
			}
		case strings.HasPrefix(rest, "root:/"):
			p := strings.TrimPrefix(rest, "root:/")
			f, ok := files[p]
			if !ok {
				notFound()
				return
			}
			switch r.Method {
			case http.MethodGet:
				m := s.driveItem(library, p, f)
				m["listItem"] = map[string]any{"fields": f.fields}
				answer(http.StatusOK, m)
			case http.MethodDelete:
				for q := range files {
					if q == p || strings.HasPrefix(q, p+"/") {
						delete(files, q)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			notFound()
		}
		return
	}
	notFound()
}

// listItems answers one page of a list, honouring the filter, $top and a $skiptoken
// that is the last id of the previous page — the shape SharePoint's own token has.
func (s *fakeSite) listItems(w http.ResponseWriter, r *http.Request, name string, list map[int]map[string]any, answer func(int, any)) {
	q := r.URL.Query()
	if q.Get("$filter") != "" && r.Header.Get("Prefer") == "" {
		answer(http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalidRequest", "message": "Field cannot be referenced in filter as it is not indexed"}})
		return
	}
	var col, want string
	if m := filterRe.FindStringSubmatch(q.Get("$filter")); m != nil {
		col, want = m[1], m[2]
	}
	top, _ := strconv.Atoi(q.Get("$top"))
	after, _ := strconv.Atoi(q.Get("$skiptoken"))
	if q.Get("$skiptoken") != "" {
		s.resumed++
	}
	ids := make([]int, 0, len(list))
	for id, fields := range list {
		if id > after && (col == "" || fields[col] == want) {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	page := []any{}
	for _, id := range ids {
		if len(page) == top {
			break
		}
		page = append(page, s.item(name, id, list[id]))
	}
	out := map[string]any{"value": page}
	if len(ids) > len(page) {
		last := page[len(page)-1].(map[string]any)["id"].(string)
		q.Set("$skiptoken", last)
		out["@odata.nextLink"] = "https://graph.example/v1.0" + r.URL.Path + "?" + q.Encode()
	}
	answer(http.StatusOK, out)
}

// runExample deploys one example model and runs it to completion against the site.
func runExample(t *testing.T, file string, site *fakeSite, vars ...model.VariableValue) (*state.Store, *compiler.CompiledProcess) {
	t.Helper()
	srv := httptest.NewServer(site)
	t.Cleanup(srv.Close)
	site.url = "https://contoso.sharepoint.com/sites/verwaltung"
	return runExampleOn(t, file, sharepoint.NewGraphClient(staticToken{}, srv.URL), vars...)
}

// runExampleOn deploys one example model and runs it to completion with the Worker
// "intranet" bound to client.
func runExampleOn(t *testing.T, file string, client sharepoint.Client, vars ...model.VariableValue) (*state.Store, *compiler.CompiledProcess) {
	t.Helper()
	f, err := os.Open("../../examples/sharepoint-fallakte/" + file)
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
	}
	defer f.Close()
	cp, err := compiler.Parse(spDefKey, 1, f)
	if err != nil {
		t.Fatalf("compile %s: %v", file, err)
	}
	reg := sharepoint.NewRegistry()
	reg.Register("intranet", client)

	log, store := openStore(t)
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	for _, jt := range []int32{compiler.SharePointJobTypeIndex, compiler.SharePointOpsJobTypeIndex} {
		runner.HandleWithOutput(jt, func(rd state.Reader) job.OutputHandler {
			return sharepoint.Handler(rd, func(uint64) *compiler.CompiledProcess { return cp }, reg)
		})
	}
	p.CreateInstance(cp.Key, vars...)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if n := mustActiveProcs(t, store); n != 0 {
		t.Fatalf("%s: %d instance(s) still active — a task failed or parked", file, n)
	}
	return store, cp
}

func TestExampleFallakteRunsEndToEnd(t *testing.T) {
	site := newFakeSite()
	site.addItem("Antraege", 17, map[string]any{"Title": "Zugang Buchhaltung", "Kunde": "Muster AG", "Betrag": 12500, "Status": "Neu"})
	site.addItem("Protokoll", 1, map[string]any{"Title": "älterer Eintrag"})
	site.addFile("Vorlagen", "eingangsbestaetigung.txt", &fakeFile{content: []byte("Guten Tag %KUNDE%\nIhr Antrag %NUMMER% ist eingegangen.")})
	antragID := model.VariableValue{Name: "antragId", Kind: model.VarString, Text: "17"}

	runExample(t, "sharepoint-fallakte.bpmn", site, antragID)

	brief, ok := site.files["Fallakten"]["Faelle/17/eingangsbestaetigung.txt"]
	if !ok {
		t.Fatalf("no confirmation letter in the case folder; library holds %v", keys(site.files["Fallakten"]))
	}
	if got := string(brief.content); got != "Guten Tag Muster AG\nIhr Antrag 17 ist eingegangen." {
		t.Errorf("letter = %q, want the template filled in", got)
	}
	if brief.fields["Kunde"] != "Muster AG" || brief.fields["AntragNr"] != float64(17) {
		t.Errorf("letter properties = %#v, want the customer and the request number as a number", brief.fields)
	}
	if folder := site.files["Fallakten"]["Faelle/17"]; folder == nil || !folder.folder {
		t.Error("the case folder Faelle/17 was not created")
	}
	antrag := site.lists["Antraege"][17]
	if antrag["Status"] != "In Bearbeitung" || antrag["Dringend"] != true {
		t.Errorf("request = %#v, want status In Bearbeitung and Dringend as a boolean", antrag)
	}
	if url, _ := antrag["Fallordner"].(string); !strings.HasSuffix(url, "/Fallakten/Faelle/17") {
		t.Errorf("Fallordner = %#v, want the folder's webUrl", antrag["Fallordner"])
	}
	if len(site.lists["Protokoll"]) != 2 {
		t.Errorf("protocol holds %d entries, want the old one and a new one", len(site.lists["Protokoll"]))
	}
	for id, e := range site.lists["Protokoll"] {
		if id != 1 && (e["Title"] != "Fallakte angelegt: Zugang Buchhaltung" || e["AntragNr"] != float64(17)) {
			t.Errorf("protocol entry = %#v", e)
		}
	}

	// A second run for the same request finds the folder and the letter there: it
	// creates nothing twice except the protocol entry, which the model says is the one
	// step that is not safe to repeat.
	brief.content = []byte("von Hand angepasst")
	runExample(t, "sharepoint-fallakte.bpmn", site, antragID)
	if site.uploads != 1 {
		t.Errorf("uploads = %d, want the letter uploaded once and left alone the second time", site.uploads)
	}
	if got := string(site.files["Fallakten"]["Faelle/17/eingangsbestaetigung.txt"].content); got != "von Hand angepasst" {
		t.Errorf("letter = %q, want the hand-edited one kept", got)
	}
	if len(site.lists["Protokoll"]) != 3 {
		t.Errorf("protocol holds %d entries after two runs, want 3", len(site.lists["Protokoll"]))
	}
}

func TestExampleAufraeumenPagesAndDeletes(t *testing.T) {
	site := newFakeSite()
	// 25 settled requests — more than one page of 20 — and two still open.
	for id := 1; id <= 25; id++ {
		site.addItem("Antraege", id, map[string]any{"Title": fmt.Sprintf("Antrag %d", id), "Status": "Erledigt"})
		if id%5 == 0 {
			site.addFile("Fallakten", fmt.Sprintf("Faelle/%d", id), &fakeFile{folder: true})
			site.addFile("Fallakten", fmt.Sprintf("Faelle/%d/eingangsbestaetigung.txt", id), &fakeFile{content: []byte("x")})
		}
	}
	site.addItem("Antraege", 26, map[string]any{"Title": "offen", "Status": "In Bearbeitung"})
	site.addItem("Antraege", 27, map[string]any{"Title": "neu", "Status": "Neu"})
	site.addFile("Fallakten", "Faelle/26", &fakeFile{folder: true})

	runExample(t, "sharepoint-aufraeumen.bpmn", site)

	if got := keysInt(site.lists["Antraege"]); fmt.Sprint(got) != "[26 27]" {
		t.Errorf("requests left = %v, want only the two that are not settled", got)
	}
	if got := keys(site.files["Fallakten"]); fmt.Sprint(got) != "[Faelle/26]" {
		t.Errorf("library holds %v, want only the open request's folder", got)
	}
	// Re-reading the first page would also empty the list here, because what a page
	// finds is deleted before the next one is read. The model pages with the token, and
	// this is what says it does.
	if site.resumed != 1 {
		t.Errorf("list requests resumed with a page token = %d, want 1 (the second page)", site.resumed)
	}
}

func keys(m map[string]*fakeFile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keysInt(m map[int]map[string]any) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
