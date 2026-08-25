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
)

// These tests are about one question the Modeler asks and could not previously get
// an answer to: for the connector kind an author is about to choose, which process
// runs the call — this engine, or a worker? The picker used to answer it from a
// constant, and the constant was written when "every kind but the plain job worker
// runs in the engine" was true. It stopped being true twice over: kinds were moved
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
	for _, id := range []string{"rest", "scim", "ldap", "soap", "ad", "mail", "csv", "clio"} {
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

// modelerCatalogKindIDs reads the ids out of editor.js's SERVICE_TASK_KINDS. It is
// bounded to that array on purpose: the send task's Message kind is declared after
// it and is not a connector.
func modelerCatalogKindIDs(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatalf("read editor.js: %v", err)
	}
	const open = "const SERVICE_TASK_KINDS = ["
	start := strings.Index(string(src), open)
	if start < 0 {
		t.Fatal("editor.js has no SERVICE_TASK_KINDS array; the catalog must have been renamed")
	}
	rest := string(src)[start+len(open):]
	end := strings.Index(rest, "\n].map(")
	if end < 0 {
		t.Fatal("could not find the end of SERVICE_TASK_KINDS in editor.js")
	}
	var ids []string
	for _, m := range catalogKindIDRe.FindAllStringSubmatch(rest[:end], -1) {
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
		if _, ok := catalogKindJobTypes[id]; ok {
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
			"The picker then says nothing about where that kind runs. Add it to catalogKindJobTypes, "+
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
	for id, types := range catalogKindJobTypes {
		offloadable, ok := offloadableKinds[id]
		if !ok {
			continue // born on a worker, or engine-only: nothing to agree with
		}
		if !sameJobTypes(types, offloadable) {
			t.Errorf("catalogKindJobTypes[%q] = %v but offloadableKinds[%q] = %v; "+
				"the picker would report a placement for job types the operator is not moving", id, types, id, offloadable)
		}
	}
}

// Every kind the engine can be told to run itself must be one the picker knows, or
// the operator can move a kind whose badge never changes.
func TestEveryOffloadableConnectorKindIsInTheCatalog(t *testing.T) {
	// Job types authored somewhere other than the service-task picker: a business
	// rule task's decision binding and a script task's language.
	elsewhere := map[string]string{
		connectorKindTemis: "a business rule task's central-DMN binding, configured in the decision panel (ADR-0050)",
		"dmn":              "the embedded decision worker behind a business rule task, not a service-task kind",
		"script":           "a script task's language, chosen in the script panel rather than the connector picker (ADR-0047)",
	}
	for id := range offloadableKinds {
		if _, ok := catalogKindJobTypes[id]; ok {
			continue
		}
		if _, ok := elsewhere[id]; ok {
			continue
		}
		t.Errorf("--offload-connectors accepts %q but the Modeler's catalog has no placement for it, so its badge cannot follow the move", id)
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
		if !sameJobTypes(catalogKindJobTypes[id], want) {
			t.Errorf("catalogKindJobTypes[%q] = %v, want %v", id, catalogKindJobTypes[id], want)
		}
	}
}
