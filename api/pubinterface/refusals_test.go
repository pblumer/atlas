package pubinterface

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// The refusals of every route: what a malformed, unauthorised, stale or impossible
// request answers, and what the service answers once a write may or may not have
// reached the disk.

type refusalCase struct {
	name, user, method, path, body string
	code                           int
	contains                       string
}

func (f *fixture) expect(cases []refusalCase) {
	f.t.Helper()
	for _, c := range cases {
		code, _, body := f.call(c.user, c.method, c.path, c.body)
		if code != c.code || (c.contains != "" && !strings.Contains(body, c.contains)) {
			f.t.Errorf("%s: %s %s = %d %s, want %d containing %q", c.name, c.method, c.path, code, body, c.code, c.contains)
		}
	}
}

const base = "/api/v1/published-interfaces/kreditpruefung"

func TestEveryRouteRefusesWhatItCannotServe(t *testing.T) {
	f := newFixture(t)
	f.publish()
	f.defs[11] = Definition{Key: 11, ProcessID: "kredit", Version: 4, ApplicationID: "app_other", Process: compile(t, kreditBPMN)}
	f.defs[12] = Definition{Key: 12, ProcessID: "loose", Version: 1, Process: compile(t, kreditBPMN)}
	f.roles["app_other"] = map[string]int{"usr_editor": RankEditor, "usr_viewer": RankViewer}
	big := `{"note":"` + strings.Repeat("x", 2*1024*1024) + `","expectedRevision":1}`
	f.expect([]refusalCase{
		// reads
		{"anonymous list", "", "GET", "/api/v1/published-interfaces", "", http.StatusUnauthorized, "authentication required"},
		{"bad id", "usr_viewer", "GET", "/api/v1/published-interfaces/Bad_Id", "", http.StatusBadRequest, "invalid interface id"},
		{"missing", "usr_viewer", "GET", "/api/v1/published-interfaces/nothing", "", http.StatusNotFound, ErrNotFound.Error()},
		{"bad version", "usr_viewer", "GET", base + "/versions/x", "", http.StatusBadRequest, "invalid contract version"},
		{"no version", "usr_viewer", "GET", base + "/versions/9", "", http.StatusNotFound, ErrNoVersion.Error()},
		{"bad key", "usr_viewer", "GET", "/api/v1/processes/x/interface-candidates", "", http.StatusBadRequest, "invalid definition key"},
		{"no definition", "usr_viewer", "GET", "/api/v1/processes/99/interface-candidates", "", http.StatusNotFound, "no deployed definition"},
		{"outside an application", "usr_viewer", "GET", "/api/v1/processes/12/interface-candidates", "", http.StatusConflict, "outside an application"},
		{"anonymous candidates", "", "GET", "/api/v1/processes/7/interface-candidates", "", http.StatusUnauthorized, ""},

		// publish
		{"anonymous publish", "", "POST", base + "/versions", publishBody, http.StatusUnauthorized, ""},
		{"malformed", "usr_editor", "POST", base + "/versions", `{"definitionKey":`, http.StatusBadRequest, "invalid body"},
		{"unknown field", "usr_editor", "POST", base + "/versions", `{"definitionKey":7,"x":1}`, http.StatusBadRequest, "invalid body"},
		{"trailing", "usr_editor", "POST", base + "/versions", `{"definitionKey":7}{}`, http.StatusBadRequest, "trailing data"},
		{"no revision", "usr_editor", "POST", base + "/versions", `{"definitionKey":7}`, http.StatusBadRequest, "expectedRevision is required"},
		{"commitment on an outcome", "usr_editor", "POST", base + "/versions",
			`{"definitionKey":7,"expectedRevision":1,"entryPoints":[{"name":"a","elementId":"start"}],"outcomes":[{"name":"o","elementId":"bescheid","commitment":{}}]}`,
			http.StatusBadRequest, "does not measure commitments"},
		{"another application", "usr_editor", "POST", base + "/versions", strings.Replace(publishBody, `"definitionKey":7,"expectedRevision":0`, `"definitionKey":11,"expectedRevision":1`, 1),
			http.StatusConflict, "belongs to another application"},
		{"too large", "usr_editor", "POST", base + "/versions/1/deprecation", big, http.StatusRequestEntityTooLarge, "at most"},

		// rebind
		{"rebind bad path", "usr_editor", "PUT", "/api/v1/published-interfaces/kreditpruefung/versions/0/binding", `{}`, http.StatusBadRequest, "invalid interface id or contract version"},
		{"rebind malformed", "usr_editor", "PUT", base + "/versions/1/binding", `[`, http.StatusBadRequest, "invalid body"},
		{"rebind no revision", "usr_editor", "PUT", base + "/versions/1/binding", `{"definitionKey":7}`, http.StatusBadRequest, "expectedRevision is required"},
		{"rebind commitment", "usr_editor", "PUT", base + "/versions/1/binding", `{"definitionKey":7,"expectedRevision":1,"outcomes":[{"name":"o","commitment":{}}]}`, http.StatusBadRequest, "commitment"},
		{"rebind missing interface", "usr_editor", "PUT", "/api/v1/published-interfaces/nothing/versions/1/binding", `{"definitionKey":7,"expectedRevision":1}`, http.StatusNotFound, ErrNotFound.Error()},
		{"rebind as viewer", "usr_viewer", "PUT", base + "/versions/1/binding", `{"definitionKey":7,"expectedRevision":1}`, http.StatusForbidden, "insufficient"},
		{"rebind as stranger", "usr_reader", "PUT", base + "/versions/1/binding", `{"definitionKey":7,"expectedRevision":1}`, http.StatusNotFound, ErrNotFound.Error()},
		{"rebind no version", "usr_editor", "PUT", base + "/versions/5/binding", `{"definitionKey":7,"expectedRevision":1}`, http.StatusNotFound, ErrNoVersion.Error()},
		{"rebind no definition", "usr_editor", "PUT", base + "/versions/1/binding", `{"definitionKey":99,"expectedRevision":1}`, http.StatusNotFound, "no deployed definition"},
		{"rebind other application", "usr_editor", "PUT", base + "/versions/1/binding", `{"definitionKey":11,"expectedRevision":1}`, http.StatusConflict, "another application"},
		{"rebind stale", "usr_editor", "PUT", base + "/versions/1/binding", `{"definitionKey":7,"expectedRevision":0}`, http.StatusConflict, "currentRevision"},

		// deprecate
		{"deprecate bad path", "usr_editor", "POST", "/api/v1/published-interfaces/x_/versions/1/deprecation", `{}`, http.StatusBadRequest, ""},
		{"deprecate malformed", "usr_editor", "POST", base + "/versions/1/deprecation", `{`, http.StatusBadRequest, "invalid body"},
		{"deprecate no revision", "usr_editor", "POST", base + "/versions/1/deprecation", `{}`, http.StatusBadRequest, "expectedRevision is required"},
		{"deprecate control characters", "usr_editor", "POST", base + "/versions/1/deprecation", `{"note":"a\u0007b","expectedRevision":1}`, http.StatusBadRequest, "plain text"},
		{"deprecate as viewer", "usr_viewer", "POST", base + "/versions/1/deprecation", `{"expectedRevision":1}`, http.StatusForbidden, ""},
		{"deprecate no version", "usr_editor", "POST", base + "/versions/3/deprecation", `{"expectedRevision":1}`, http.StatusNotFound, ErrNoVersion.Error()},

		// grants
		{"grants bad id", "usr_owner", "PUT", "/api/v1/published-interfaces/X/grants", `{}`, http.StatusBadRequest, "invalid interface id"},
		{"grants malformed", "usr_owner", "PUT", base + "/grants", `{`, http.StatusBadRequest, "invalid body"},
		{"grants no revision", "usr_owner", "PUT", base + "/grants", `{"public":true}`, http.StatusBadRequest, "expectedRevision is required"},
		{"grants repeating view", "usr_owner", "PUT", base + "/grants", `{"view":{"userIds":["a","a"]},"expectedRevision":1}`, http.StatusBadRequest, "duplicate user id"},
		{"grants repeating send", "usr_owner", "PUT", base + "/grants", `{"send":{"groupIds":["g","g"]},"expectedRevision":1}`, http.StatusBadRequest, "duplicate group id"},
		{"grants bad id in audience", "usr_owner", "PUT", base + "/grants", `{"view":{"userIds":[" a"]},"expectedRevision":1}`, http.StatusBadRequest, "invalid user id"},
		{"grants unknown group", "usr_owner", "PUT", base + "/grants", `{"view":{"userIds":["usr_reader"],"groupIds":["grp_none"]},"send":{"userIds":["usr_reader"]},"expectedRevision":1}`, http.StatusBadRequest, "no group with id grp_none"},
		{"grants stale", "usr_owner", "PUT", base + "/grants", `{"expectedRevision":7}`, http.StatusConflict, "currentRevision"},

		// history
		{"history missing", "usr_owner", "GET", "/api/v1/published-interfaces/nothing/history", "", http.StatusNotFound, ""},
	})
}

func TestTheServiceTellsAFailingCollaboratorFromARefusal(t *testing.T) {
	f := newFixture(t)
	f.publish()
	f.rankErr = errors.New("application store unreadable")
	f.expect([]refusalCase{
		{"list", "usr_viewer", "GET", "/api/v1/published-interfaces", "", http.StatusInternalServerError, "application store unreadable"},
		{"get", "usr_viewer", "GET", base, "", http.StatusInternalServerError, "application store unreadable"},
		{"candidates", "usr_viewer", "GET", "/api/v1/processes/7/interface-candidates", "", http.StatusInternalServerError, "application store unreadable"},
		{"rebind", "usr_editor", "PUT", base + "/versions/1/binding", `{"definitionKey":7,"expectedRevision":1}`, http.StatusInternalServerError, "application store unreadable"},
	})
	f.rankErr = nil
	f.dirErr = errors.New("directory down")
	f.expect([]refusalCase{
		{"grant user", "usr_owner", "PUT", base + "/grants", `{"view":{"userIds":["usr_reader"]},"expectedRevision":1}`, http.StatusInternalServerError, "directory down"},
		{"grant group", "usr_owner", "PUT", base + "/grants", `{"view":{"groupIds":["grp_peers"]},"expectedRevision":1}`, http.StatusInternalServerError, "directory down"},
	})
}

func TestAnUncertainWriteRefusesEverythingUntilARestart(t *testing.T) {
	f := newFixture(t)
	f.publish()
	// The directory going away makes the next write fail with an unknown outcome.
	if err := os.RemoveAll(f.dir); err != nil {
		t.Fatal(err)
	}
	if code, _, body := f.call("usr_editor", "POST", base+"/versions/1/deprecation", `{"expectedRevision":1}`); code != http.StatusInternalServerError ||
		!strings.Contains(body, "not saved") {
		t.Fatalf("the failing write = %d %s", code, body)
	}
	unavailable := "unavailable until the server restarts"
	f.expect([]refusalCase{
		{"list", "usr_viewer", "GET", "/api/v1/published-interfaces", "", http.StatusServiceUnavailable, unavailable},
		{"get", "usr_viewer", "GET", base, "", http.StatusServiceUnavailable, unavailable},
		{"publish", "usr_editor", "POST", base + "/versions", publishBody, http.StatusServiceUnavailable, unavailable},
		{"rebind", "usr_editor", "PUT", base + "/versions/1/binding", `{"definitionKey":7,"expectedRevision":1}`, http.StatusServiceUnavailable, unavailable},
		{"grants", "usr_owner", "PUT", base + "/grants", `{"expectedRevision":1}`, http.StatusServiceUnavailable, unavailable},
	})
	s := f.svc.Store()
	if !s.Uncertain() || len(s.All()) != 0 {
		t.Fatal("an uncertain store still answers")
	}
	if _, ok := s.Get("kreditpruefung"); ok {
		t.Fatal("an uncertain store still answers one interface")
	}
	if _, err := s.SetGrants("kreditpruefung", Grants{}, meta(1)); !errors.Is(err, ErrUncertainWrite) {
		t.Fatalf("a write to an uncertain store = %v", err)
	}
}

func TestRealizeNamesEveryNamingMistake(t *testing.T) {
	c := Derive(compile(t, kreditBPMN), nil)
	outs := kreditSelection().Outcomes
	cases := map[string]Selection{
		"used twice":            {EntryPoints: []Pick{{Name: "a", ElementID: "start"}, {Name: "a", ElementID: "storno"}}, Outcomes: outs},
		"names exactly one":     {EntryPoints: []Pick{{Name: "a", ElementIDs: []string{"start", "storno"}}}, Outcomes: outs},
		"outcome name":          {EntryPoints: []Pick{{Name: "a", ElementID: "start"}}, Outcomes: append([]Pick{{Name: "-", ElementID: "x"}}, outs...)},
		"is used twice; name":   {EntryPoints: []Pick{{Name: "a", ElementID: "start"}}, Outcomes: append([]Pick{{Name: "bewilligt", ElementID: "x"}}, outs...)},
		"names no end event":    {EntryPoints: []Pick{{Name: "a", ElementID: "start"}}, Outcomes: append([]Pick{{Name: "leer"}}, outs...)},
		"named by two outcomes": {EntryPoints: []Pick{{Name: "a", ElementID: "start"}}, Outcomes: append([]Pick{{Name: "doppelt", ElementID: "bescheid"}}, outs...)},
	}
	for want, sel := range cases {
		_, _, problems := Realize(c, "kredit", 7, 1, sel)
		if !strings.Contains(strings.Join(problems, "; "), want) {
			t.Errorf("problems = %v, want one containing %q", problems, want)
		}
	}
}

func TestAMovedDefinitionAndASecondInterfaceAreReadCorrectly(t *testing.T) {
	f := newFixture(t)
	f.publish()
	second := strings.Replace(publishBody, `{"name":"antrag","elementId":"start"},`, ``, 1)
	if code, _, body := f.call("usr_editor", "POST", "/api/v1/published-interfaces/kredit-light/versions", second); code != http.StatusOK {
		t.Fatalf("second interface = %d %s", code, body)
	}
	grants := `{"view":{"groupIds":["grp_peers"]},"send":{"groupIds":["grp_peers"]},"expectedRevision":1}`
	if code, _, body := f.call("usr_owner", "PUT", base+"/grants", grants); code != http.StatusOK {
		t.Fatalf("grants = %d %s", code, body)
	}
	d := f.defs[7]
	d.ApplicationID = "app_other"
	f.defs[7] = d
	_, out, body := f.call("usr_viewer", "GET", "/api/v1/published-interfaces", "")
	if out["total"] != float64(2) || !strings.Contains(body, UnavailableOtherApplication) ||
		strings.Index(body, `"id":"kredit-light"`) > strings.Index(body, `"id":"kreditpruefung"`) {
		t.Fatalf("list = %s", body)
	}
	long := `{"note":"` + strings.Repeat("x", maxNote+1) + `","expectedRevision":2}`
	if code, _, _ := f.call("usr_editor", "POST", base+"/versions/1/deprecation", long); code != http.StatusBadRequest {
		t.Fatalf("an overlong note = %d, want 400", code)
	}
	if (Audience{GroupIDs: []string{"grp_peers"}}).admits(Principal{GroupIDs: []string{"grp_peers"}}) {
		t.Fatal("a principal without a user id was admitted")
	}
	if p := principalOf(nil); p.UserID != "" {
		t.Fatal("no principal became somebody")
	}
}

func TestWithoutAuthenticationEverythingIsReadable(t *testing.T) {
	f := newFixture(t)
	f.publish()
	f.auth = false
	code, out, body := f.call("", "GET", base, "")
	if code != http.StatusOK || out["grants"] == nil || !strings.Contains(body, `"binding"`) {
		t.Fatalf("single-user read = %d %s", code, body)
	}
	if code, _, _ := f.call("", "GET", "/api/v1/processes/7/interface-candidates", ""); code != http.StatusOK {
		t.Fatalf("single-user candidates = %d", code)
	}
}

func TestStoreRefusesWhatCannotBeStored(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	contract, binding := kreditContract(t)
	cases := map[string]error{}
	_, cases["bad id"] = s.Publish("Bad", "app", contract, binding, meta(0))
	_, cases["no actor"] = s.Publish("ok", "app", contract, binding, Meta{At: 1})
	_, cases["rebind missing"] = s.Rebind("ok", 1, binding, meta(0))
	_, cases["deprecate missing"] = s.Deprecate("ok", 1, "", meta(0))
	empty := contract.clone()
	empty.EntryPoints = nil
	_, cases["no entry point"] = s.Publish("ok", "app", empty, binding, meta(0))
	for name, err := range cases {
		if err == nil {
			t.Errorf("%s was stored", name)
		}
	}
	if _, err := s.Publish("ok", "app", contract, binding, meta(0)); err != nil {
		t.Fatal(err)
	}
	other := binding.clone()
	other.ProcessID = "anderer"
	if _, err := s.Rebind("ok", 1, other, meta(1)); !errors.Is(err, ErrOtherProcess) {
		t.Fatalf("rebind onto another process = %v", err)
	}
	if _, err := s.Rebind("ok", 2, binding, meta(1)); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("rebind of a missing version = %v", err)
	}
	if _, err := CheckAudience(Audience{UserIDs: make([]string, maxAudience+1)}); err == nil {
		t.Fatal("an oversized audience was accepted")
	}
	if _, err := CheckAudience(Audience{GroupIDs: []string{"\n"}}); err == nil {
		t.Fatal("a malformed group id was accepted")
	}
}

func TestStoreRefusesDocumentsThatDoNotAddUp(t *testing.T) {
	contract, binding := kreditContract(t)
	good := Interface{
		SchemaVersion: SchemaVersion, ID: "kreditpruefung", ProcessID: "kredit", ApplicationID: "app_kredit", Revision: 1,
		Versions: []Version{{Version: 1, Contract: contract, Binding: binding}},
		History:  []Change{{Revision: 1, At: 1, ActorID: "usr", Action: ActionPublish}},
	}
	if err := validateInterface(good); err != nil {
		t.Fatalf("a good document is refused: %v", err)
	}
	mutations := map[string]func(*Interface){
		"schema":         func(in *Interface) { in.SchemaVersion = 9 },
		"revision":       func(in *Interface) { in.Revision = 2 },
		"no version":     func(in *Interface) { in.Versions = nil },
		"version order":  func(in *Interface) { in.Versions[0].Version = 2 },
		"history order":  func(in *Interface) { in.History[0].Revision = 3 },
		"unknown action": func(in *Interface) { in.History[0].Action = "steal" },
		"bad grant":      func(in *Interface) { in.Grants.Send.UserIDs = []string{"a", "a"} },
	}
	for name, mutate := range mutations {
		in := good.clone()
		mutate(&in)
		if err := validateInterface(in); err == nil {
			t.Errorf("%s: a document that does not add up was accepted", name)
		}
	}
}

func TestContractDifferencesNameEveryKindOfChange(t *testing.T) {
	contract, _ := kreditContract(t)
	changed := contract.clone()
	changed.EntryPoints[0].Kind = KindReceiveTask
	changed.EntryPoints[1].Payload.Class = "Andere"
	changed.Returns.Errors = []string{"OTHER"}
	changed.Returns.Escalations = nil
	changed.Returns.Messages = nil
	changed.Outcomes[0].Kind = OutcomeTerminate
	diff := strings.Join(contract.Differences(changed), "; ")
	for _, want := range []string{"changes its kind", "changes its payload", "errors", "escalations", "reply messages", "terminal outcomes"} {
		if !strings.Contains(diff, want) {
			t.Errorf("differences %q lack %q", diff, want)
		}
	}
	if contract.Equal(changed) {
		t.Fatal("a changed contract is equal")
	}
	fewer := contract.clone()
	fewer.Outcomes = fewer.Outcomes[:1]
	if contract.Equal(fewer) || !strings.Contains(strings.Join(contract.Differences(fewer), ";"), "terminal outcomes") {
		t.Fatal("dropping an outcome is no difference")
	}
	fields := contract.clone()
	fields.EntryPoints[0].Payload.Fields = []string{"x"}
	if contract.Equal(fields) {
		t.Fatal("another field list is the same payload")
	}
}

func TestEveryEndEventKindIsAnOutcome(t *testing.T) {
	want := map[compiler.BpmnType]string{
		compiler.TypeEndEvent: OutcomeEnd, compiler.TypeErrorEndEvent: OutcomeError,
		compiler.TypeEscalationEndEvent: OutcomeEscalation, compiler.TypeMessageEndEvent: OutcomeMessage,
		compiler.TypeSignalEndEvent: OutcomeSignal, compiler.TypeTerminateEndEvent: OutcomeTerminate,
		compiler.TypeCompensationEndEvent: OutcomeCompensation, compiler.TypeServiceTask: "",
	}
	for typ, kind := range want {
		if got := outcomeKind(typ); got != kind {
			t.Errorf("outcomeKind(%v) = %q, want %q", typ, got, kind)
		}
	}
	if c := Derive(nil, nil); len(c.EntryPoints)+len(c.Outcomes) != 0 {
		t.Fatal("a missing process offers something")
	}
}
