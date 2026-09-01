package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/script"
)

// These tests are about one question the Modeler asks and could not previously get
// an answer to: for the implementation an author is about to choose — a connector, a
// script language, a decision binding — which process runs the work, this engine or a
// worker? The connector picker used to answer it from a constant, and the constant was
// written when "every kind but the plain job worker runs in the engine" was true; the
// other two panels never said anything at all. It stopped being true twice over: kinds were moved
// onto a supervised worker by default (ADR-0168), and kinds were born on a worker
// with no in-process form at all (ADR-0173). Only the server knows which, because
// --offload-connectors and --in-process-connectors are its command line.

// placementOf returns the reported placement of one catalog kind, failing the test
// when the server reports none — an unlisted kind is the drift these guard.
func placementOf(t *testing.T, srv *Server, id string) string {
	t.Helper()
	for _, k := range srv.connectorPlacements() {
		if k.ID == id {
			return k.Placement
		}
	}
	t.Fatalf("the server reported no placement for catalog kind %q", id)
	return ""
}

// A kind whose in-process handler this server registered runs here, and says so.
func TestPlacementSaysEngineForAKindThisServerRuns(t *testing.T) {
	srv := newServerWithOptions(t)
	for _, id := range []string{"rest", "scim", "ldap", "soap", "ad", "mail", "csv", "clio", "dmn", connectorKindTemis} {
		if got := placementOf(t, srv, id); got != placementEngine {
			t.Errorf("%s: placement %q, want %q — this server registered its in-process handler", id, got, placementEngine)
		}
	}
}

// The whole point of asking the server: the same kind reports differently once the
// operator has moved it. A constant in the browser cannot do this.
func TestPlacementFollowsOffloading(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{"rest", "mail"}))
	for _, id := range []string{"rest", "mail"} {
		if got := placementOf(t, srv, id); got != placementWorker {
			t.Errorf("%s: placement %q, want %q — --offload-connectors named it", id, got, placementWorker)
		}
	}
	// A kind the operator did not name is untouched, so the answer is per kind and
	// not a single server-wide flag.
	if got := placementOf(t, srv, "ldap"); got != placementEngine {
		t.Errorf("ldap: placement %q, want %q — it was not offloaded", got, placementEngine)
	}
}

// A script language is enabled per language and offloaded as one kind (the flag word is
// "script"), so each language follows the same move. A bare server registers no script
// worker at all, which is the same answer as offloaded — the job waits for a worker
// either way, and that is what an author needs to know.
func TestPlacementCoversEveryScriptLanguage(t *testing.T) {
	t.Run("offloaded", func(t *testing.T) {
		srv := newServerWithOptions(t,
			WithScriptWorker(compiler.PwshJobTypeIndex, nil),
			WithScriptWorker(compiler.PythonJobTypeIndex, nil),
			WithScriptWorker(compiler.JsJobTypeIndex, nil),
			WithOffloadedConnectorKinds([]string{"script"}))
		for _, id := range []string{"powershell", "python", "javascript"} {
			if got := placementOf(t, srv, id); got != placementWorker {
				t.Errorf("%s: placement %q, want %q", id, got, placementWorker)
			}
		}
	})
	t.Run("kept in the engine", func(t *testing.T) {
		srv := newServerWithOptions(t,
			WithScriptWorker(compiler.PwshJobTypeIndex, nil),
			WithScriptWorker(compiler.PythonJobTypeIndex, nil),
			WithScriptWorker(compiler.JsJobTypeIndex, nil))
		for _, id := range []string{"powershell", "python", "javascript"} {
			if got := placementOf(t, srv, id); got != placementEngine {
				t.Errorf("%s: placement %q, want %q", id, got, placementEngine)
			}
		}
	})
	// One language turned off while the others run: only that language waits for a
	// worker. A per-kind answer would have called it in-engine, which is the class of
	// wrong statement this whole change is about.
	t.Run("one language turned off", func(t *testing.T) {
		srv := newServerWithOptions(t, WithScriptWorker(compiler.PwshJobTypeIndex, nil))
		if got := placementOf(t, srv, "powershell"); got != placementEngine {
			t.Errorf("powershell: placement %q, want %q", got, placementEngine)
		}
		if got := placementOf(t, srv, "python"); got != placementWorker {
			t.Errorf("python: placement %q, want %q — its worker is not enabled here", got, placementWorker)
		}
	})
}

// The business rule task's two bindings are separate kinds to the operator, so they
// move separately.
func TestPlacementDistinguishesTheDecisionBindings(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{connectorKindTemis}))
	if got := placementOf(t, srv, connectorKindTemis); got != placementWorker {
		t.Errorf("temis: placement %q, want %q", got, placementWorker)
	}
	if got := placementOf(t, srv, "dmn"); got != placementEngine {
		t.Errorf("dmn: placement %q, want %q — only the temis binding was offloaded", got, placementEngine)
	}
}

// The script languages are named in two places — Go's script.Langs and this table —
// and an author picks the language by the name the *Modeler* uses. They must be the
// same word, or the picker asks about a kind the server has never heard of.
func TestScriptLanguageIDsMatchTheLanguageRegistry(t *testing.T) {
	for _, lang := range script.Langs {
		types, ok := authoredKindJobTypes[lang.Name]
		if !ok {
			t.Errorf("script language %q has no placement entry; the Modeler's language select would get no answer for it", lang.Name)
			continue
		}
		if !sameJobTypes(types, []int32{lang.JobType}) {
			t.Errorf("authoredKindJobTypes[%q] = %v, want the language's own job type %v", lang.Name, types, lang.JobType)
		}
	}
}

// A kind born on a worker (ADR-0173) has no in-process handler to turn off, so no
// configuration can make it run here. That is a different statement from "offloaded"
// and the picker says it differently.
func TestPlacementSaysWorkerOnlyForAKindBornOnAWorker(t *testing.T) {
	srv := newServerWithOptions(t)
	for _, id := range []string{"mssql", "mariadb", "postgres", "entra"} {
		if got := placementOf(t, srv, id); got != placementWorkerOnly {
			t.Errorf("%s: placement %q, want %q — the kind has no in-process handler at all", id, got, placementWorkerOnly)
		}
	}
}

// The mirror image: the user-provisioning connector mutates the run-loop-owned user
// store (ADR-0123), so there is nothing for a worker to hold. Telling its author to
// prefer a job worker — which is what the in-engine notice does — would be advice
// they cannot take.
func TestPlacementSaysEngineOnlyForTheUserProvisioningConnector(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
	}{
		{"enabled", []Option{WithUserProvisioning()}},
		{"disabled", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerWithOptions(t, tc.opts...)
			if got := placementOf(t, srv, "userconnector"); got != placementEngineOnly {
				t.Errorf("userconnector: placement %q, want %q", got, placementEngineOnly)
			}
		})
	}
}

// catalogKindIDRe matches the `id: "rest",` of one SERVICE_TASK_KINDS entry.
var catalogKindIDRe = regexp.MustCompile(`\bid:\s*"([a-zA-Z0-9_-]+)"`)

// modelerSource is editor.js, the whole Modeler panel implementation. Several drift
// guards read it, so the file is opened in one place.
func modelerSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatalf("read editor.js: %v", err)
	}
	return string(src)
}

// modelerCatalogSource is the body of editor.js's SERVICE_TASK_KINDS array. It is
// bounded to that array on purpose: the send task's Message kind is declared after
// it and is not a connector.
func modelerCatalogSource(t *testing.T) string {
	t.Helper()
	src := modelerSource(t)
	const open = "const SERVICE_TASK_KINDS = ["
	start := strings.Index(src, open)
	if start < 0 {
		t.Fatal("editor.js has no SERVICE_TASK_KINDS array; the catalog must have been renamed")
	}
	rest := src[start+len(open):]
	end := strings.Index(rest, "\n].map(")
	if end < 0 {
		t.Fatal("could not find the end of SERVICE_TASK_KINDS in editor.js")
	}
	return rest[:end]
}

// modelerCatalogKindIDs reads the ids out of editor.js's SERVICE_TASK_KINDS.
func modelerCatalogKindIDs(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, m := range catalogKindIDRe.FindAllStringSubmatch(modelerCatalogSource(t), -1) {
		ids = append(ids, m[1])
	}
	if len(ids) < 10 {
		t.Fatalf("found only %d catalog kinds in editor.js; the pattern must have changed", len(ids))
	}
	return ids
}

// TestEveryCatalogKindHasAPlacement is the drift guard the badge needs. A kind the
// server says nothing about renders with no badge at all, which is the silence this
// change exists to remove — and a new connector is exactly when it would happen.
func TestEveryCatalogKindHasAPlacement(t *testing.T) {
	var missing []string
	for _, id := range modelerCatalogKindIDs(t) {
		if _, ok := authoredKindJobTypes[id]; ok {
			continue
		}
		if _, exempt := catalogKindsWithoutJobType[id]; exempt {
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("api/web/editor.js offers %d connector kind(s) the server reports no placement for: %s\n\n"+
			"The picker then says nothing about where that kind runs. Add it to authoredKindJobTypes, "+
			"or record why it compiles to no job at all in catalogKindsWithoutJobType.",
			len(missing), strings.Join(missing, ", "))
	}
}

// TestCatalogKindsWithoutJobTypeAreReal keeps that exemption list from outliving the
// kinds it excuses, the way nonServiceTaskConnectors is kept honest.
func TestCatalogKindsWithoutJobTypeAreReal(t *testing.T) {
	known := map[string]bool{}
	for _, id := range modelerCatalogKindIDs(t) {
		known[id] = true
	}
	for id, reason := range catalogKindsWithoutJobType {
		if !known[id] {
			t.Errorf("catalogKindsWithoutJobType lists %q (%s), which the Modeler's catalog no longer offers", id, reason)
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("catalogKindsWithoutJobType[%q] records no reason", id)
		}
	}
}

// The placement table and --offload-connectors describe the same kinds from two
// sides, and they share their names. A kind whose job types disagree between them
// would report a placement for one thing while the operator moved another.
func TestPlacementJobTypesAgreeWithOffloadableKinds(t *testing.T) {
	for id, types := range authoredKindJobTypes {
		offloadable, ok := offloadableKinds[id]
		if !ok {
			continue // born on a worker, or engine-only: nothing to agree with
		}
		if !sameJobTypes(types, offloadable) {
			t.Errorf("authoredKindJobTypes[%q] = %v but offloadableKinds[%q] = %v; "+
				"the picker would report a placement for job types the operator is not moving", id, types, id, offloadable)
		}
	}
}

// Every job type an operator can move must be one the Modeler can report on, or a kind
// exists whose badge can never follow the move. This is checked per job type rather than
// per name because the two vocabularies do not line up one to one: "script" is a single
// word to the operator and three languages to an author.
func TestEveryOffloadableJobTypeHasAPlacement(t *testing.T) {
	reported := map[int32]string{}
	for id, types := range authoredKindJobTypes {
		for _, jt := range types {
			reported[jt] = id
		}
	}
	for name, types := range offloadableKinds {
		for _, jt := range types {
			if _, ok := reported[jt]; !ok {
				t.Errorf("--offload-connectors accepts %q, whose job type %d no authored kind reports, so nothing in the Modeler can follow that move", name, jt)
			}
		}
	}
}

// sameJobTypes compares two job-type sets irrespective of order.
func sameJobTypes(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]int32(nil), a...)
	y := append([]int32(nil), b...)
	sort.Slice(x, func(i, j int) bool { return x[i] < x[j] })
	sort.Slice(y, func(i, j int) bool { return y[i] < y[j] })
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// The wire shape the Modeler reads, end to end.
func TestConnectorKindsEndpointReportsPlacements(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{"webscrape"}))
	rec := httptest.NewRecorder()
	srv.handleConnectorKinds(rec, httptest.NewRequest(http.MethodGet, "/api/v1/connector-kinds", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Kinds []connectorPlacement `json:"kinds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	got := map[string]string{}
	for _, k := range body.Kinds {
		got[k.ID] = k.Placement
	}
	for id, want := range map[string]string{
		"webscrape":     placementWorker,
		"rest":          placementEngine,
		"mssql":         placementWorkerOnly,
		"userconnector": placementEngineOnly,
	} {
		if got[id] != want {
			t.Errorf("%s: %q, want %q", id, got[id], want)
		}
	}
	// The two catalog entries that compile to no job say nothing rather than
	// guessing: the picker renders no badge for them.
	for id := range catalogKindsWithoutJobType {
		if p, ok := got[id]; ok {
			t.Errorf("%s: reported placement %q, but it compiles to no job type", id, p)
		}
	}
}

// A placement is only as good as the job types behind it, and those are stable
// identifiers baked into compiled processes. A silent renumbering would move a
// badge onto the wrong kind, so pin the ones the table names.
func TestPlacementTableNamesTheReservedJobTypes(t *testing.T) {
	for id, want := range map[string][]int32{
		"rest":  {compiler.RestJobTypeIndex},
		"mail":  {compiler.MailJobTypeIndex},
		"mssql": {compiler.MsSqlJobTypeIndex},
		"clio":  {compiler.ClioWriteJobTypeIndex, compiler.ClioQueryJobTypeIndex, compiler.ClioReadJobTypeIndex},
	} {
		if !sameJobTypes(authoredKindJobTypes[id], want) {
			t.Errorf("authoredKindJobTypes[%q] = %v, want %v", id, authoredKindJobTypes[id], want)
		}
	}
}
