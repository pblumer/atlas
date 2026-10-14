package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// dmnRefImpactPathsPlainBPMN is a process with no business rule task at all, which
// nonetheless names the decision in its documentation — the prefilter admits it, and
// compiling it must then clear it.
const dmnRefImpactPathsPlainBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="menu" isExecutable="true">
    <documentation>Serves the Dish of the day.</documentation>
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// dmnRefImpactPathsSeed files references and drafts directly, on the loop.
func dmnRefImpactPathsSeed(t *testing.T, srv *Server, refs []dmnRef, drafts []draft) {
	t.Helper()
	var err error
	srv.do(func() {
		for _, r := range refs {
			if err = srv.dmnrefs.Save(r); err != nil {
				return
			}
		}
		for _, d := range drafts {
			if err = srv.drafts.Save(d); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// dmnRefImpactPathsUnreadableModel makes a model handle resolve to a folder.
func dmnRefImpactPathsUnreadableModel(t *testing.T, srv *Server, handle string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dmnModelListPathsDir(t, srv), handle+".dmn", "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestDmnRefImpactWarnsOnlyAboutWhatWouldBreak: deleting the only reference that
// provides "Dish" makes it exclusive, and the dialog then looks for what would break.
// A reference whose model does not compile provides nothing; a deployed process with
// no business rule task, a draft that does not compile, and a draft that only
// mentions the name in prose are all passed over — none of them would stop working.
func TestDmnRefImpactWarnsOnlyAboutWhatWouldBreak(t *testing.T) {
	srv, _ := newValidateServer(t)
	dmnRefImpactPathsSeed(t, srv,
		[]dmnRef{{ID: "r-dish", Name: "Dish", ModelRef: "dish"}, {ID: "r-broken", Name: "Broken", ModelRef: "broken"}},
		[]draft{
			{ProcessID: "menu", Name: "Menu", XML: dmnRefImpactPathsPlainBPMN},
			{ProcessID: "scrap", Name: "Scrap", XML: "<definitions>Dish</definitions>"},
		})
	forkPathsDeploy(t, srv, strings.Replace(dmnRefImpactPathsPlainBPMN, `id="menu"`, `id="deployed-menu"`, 1))

	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/dmnrefs/r-dish/impact", "", "")
	if code != http.StatusOK {
		t.Fatalf("impact: %d (%s)", code, body)
	}
	var got refImpactResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(got.Exclusive) != 1 || got.Exclusive[0] != "Dish" {
		t.Errorf("exclusive = %v, want Dish — the broken model provides nothing", got.Exclusive)
	}
	if len(got.Blocked) != 0 || got.BlockedHidden != 0 {
		t.Errorf("blocked = %+v (+%d hidden), want nothing: no business rule task names Dish", got.Blocked, got.BlockedHidden)
	}
}

// TestDmnRefImpactFailsRatherThanUnderWarn: the dialog answers "is it safe to delete
// this reference". Every input it cannot read — the other references, the drafts, the
// applications, this model or another — could hide exactly the artifact that would
// break, so each one fails the answer instead of shrinking it to "nothing affected".
func TestDmnRefImpactFailsRatherThanUnderWarn(t *testing.T) {
	for name, tc := range map[string]struct {
		breakIt func(*testing.T, *Server)
		says    string
	}{
		"another reference": {func(t *testing.T, s *Server) { corrupt(t, s.dmnrefs.Dir(), "r-zzz") }, "read dmn reference"},
		"the drafts":        {func(t *testing.T, s *Server) { approvalsPathsDirAsFile(t, s.drafts.Dir()) }, "read dmn reference"},
		"the applications":  {func(t *testing.T, s *Server) { approvalsPathsDirAsFile(t, s.projects.Dir()) }, "read dmn reference"},
		"this model":        {func(t *testing.T, s *Server) { dmnRefImpactPathsUnreadableModel(t, s, "mine") }, "resolve dmn model"},
		"another model":     {func(t *testing.T, s *Server) { dmnRefImpactPathsUnreadableModel(t, s, "theirs") }, "resolve dmn model"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, dir := newValidateServer(t)
			// "mine" is a readable copy of dish unless this case breaks it.
			if err := os.WriteFile(filepath.Join(dir, "dmn-models", "mine.xml"), []byte(validDMNModel), 0o644); err != nil {
				t.Fatal(err)
			}
			dmnRefImpactPathsSeed(t, srv, []dmnRef{
				{ID: "r-mine", Name: "Mine", ModelRef: "mine"},
				{ID: "r-theirs", Name: "Theirs", ModelRef: "theirs"},
			}, nil)
			tc.breakIt(t, srv)
			code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/dmnrefs/r-mine/impact", "", "")
			if code != http.StatusInternalServerError || !strings.Contains(string(body), tc.says) {
				t.Errorf("%s unreadable: %d (%s), want 500 %q", name, code, body, tc.says)
			}
		})
	}
}

// TestDmnRefImpactHidesAReferenceFromWhoeverCannotSeeIt: asking what deleting a
// reference would break is a read of the reference, and a personal reference of
// somebody else's is not there for the caller — the answer must not confirm it exists.
func TestDmnRefImpactHidesAReferenceFromWhoeverCannotSeeIt(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	dmnRefImpactPathsSeed(t, srv, []dmnRef{{ID: "anns-ref", Name: "Ann's", ModelRef: "anns", OwnerID: "usr_ann"}}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dmnrefs/anns-ref/impact", nil)
	req.SetPathValue("id", "anns-ref")
	req = req.WithContext(httpapi.WithPrincipal(req.Context(),
		&httpapi.Principal{UserID: "usr_bob", Username: "bob", Roles: []string{RoleModeler}}))
	rec := httptest.NewRecorder()
	srv.handleDmnRefImpact(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("bob asking about ann's reference: %d (%s), want 404", rec.Code, rec.Body)
	}
}
