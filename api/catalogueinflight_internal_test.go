package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The warning a server gives when it starts with the catalogue switched off while
// orders are still being worked (ADR-0434, its open question answered).
//
// Switching the area off is never refused — that is the guarantee the switch exists
// to give — but it is not silent either. A fulfilment or approval process still
// running will call the order routes the switch removed, fail there and, its retries
// spent, raise an incident. The operator learns that at start, from one line, rather
// than from the incidents one by one.

// openAt boots a Server over dir, so a test can stop it and start another over the
// same data with a different switch.
func openAt(t *testing.T, dir string, opts ...Option) (*Server, func()) {
	t.Helper()
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
	srv, err := New(proc, store, dir, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	}
	t.Cleanup(stop)
	return srv, stop
}

// waitingProcess is a process that stays live at a user task once started.
func waitingProcess(id string) string {
	return fmt.Sprintf(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="%s" isExecutable="true">
    <startEvent id="s"/><userTask id="u"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="u"/>
    <sequenceFlow id="f2" sourceRef="u" targetRef="e"/>
  </process>
</definitions>`, id)
}

// startWaiting deploys a waiting process under id, starts n instances of it and
// answers their keys.
func startWaiting(t *testing.T, srv *Server, id string, n int) []uint64 {
	t.Helper()
	if code, b := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", waitingProcess(id), "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy %s: %d (%s)", id, code, b)
	}
	var keys []uint64
	for i := 0; i < n; i++ {
		code, b := serveInternal(t, srv, http.MethodPost, "/api/v1/instances", `{"processId":"`+id+`"}`, "application/json")
		if code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("start %s: %d (%s)", id, code, b)
		}
		var created createInstanceResp
		if err := json.Unmarshal(b, &created); err != nil || created.InstanceKey == 0 {
			t.Fatalf("start %s answered %s (%v)", id, b, err)
		}
		keys = append(keys, created.InstanceKey)
	}
	return keys
}

// inFlightLines is every log line the warning wrote, decoded.
func inFlightLines(t *testing.T, sink *auditSink) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(sink.String()), "\n") {
		if !strings.Contains(line, "server.catalogue_disabled_in_flight") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// TestSwitchingTheCatalogueOffWarnsAboutWorkStillInFlight: the count is what will
// fail and nothing else. The shop's own approval process calls the order routes; so,
// by the convention every two-process product follows, does its provisioning process at
// its last step. A lifecycle process talks to its order through shop tasks, which keep
// their handlers with the area off, and an unrelated process has nothing to do with
// orders — neither is counted.
func TestSwitchingTheCatalogueOffWarnsAboutWorkStillInFlight(t *testing.T) {
	dir := t.TempDir()
	on, stop := openAt(t, dir)
	startWaiting(t, on, "atlas-genehmigung-fix", 2)
	startWaiting(t, on, "provision-vpn", 1)
	startWaiting(t, on, "tool-strand", 1)
	startWaiting(t, on, "unrelated", 3)
	for _, it := range []catalog.Item{
		{ID: "vpn", HomeCatalog: "c1", ProvisionProcess: "provision-vpn", DeprovisionProcess: "revoke-vpn"},
		{ID: "tool", HomeCatalog: "c1", LifecycleProcess: "tool-strand"},
	} {
		if err := on.catalogStore.SaveItem(it); err != nil {
			t.Fatal(err)
		}
	}
	stop()

	sink := captureAuditLog(t)
	_, stop = openAt(t, dir, WithoutCatalogue())
	stop()

	lines := inFlightLines(t, sink)
	if len(lines) != 1 {
		t.Fatalf("got %d in-flight warning(s), want 1; log:\n%s", len(lines), sink.String())
	}
	w := lines[0]
	if w["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", w["level"])
	}
	if w["shop_process_instances"] != float64(2) || w["product_process_instances"] != float64(1) {
		t.Errorf("counts = shop %v, product %v; want 2 and 1", w["shop_process_instances"], w["product_process_instances"])
	}
	if got := w["processes"]; got != "atlas-genehmigung-fix=2,provision-vpn=1" {
		t.Errorf("processes = %q, want the two that will fail, by id", got)
	}
}

// TestNoWarningWithoutCauseForOne: on, there is nothing to warn about; off with nothing
// running, there is nothing either — a line on every start of a server that never had a
// shop would teach operators to ignore it.
func TestNoWarningWithoutCauseForOne(t *testing.T) {
	dir := t.TempDir()
	on, stop := openAt(t, dir)
	startWaiting(t, on, "atlas-genehmigung-fix", 1)
	stop()

	sink := captureAuditLog(t)
	_, stop = openAt(t, dir)
	stop()
	if n := len(inFlightLines(t, sink)); n != 0 {
		t.Errorf("the catalogue is on, and %d in-flight warning(s) were written", n)
	}

	quiet := captureAuditLog(t)
	_, stop = openAt(t, t.TempDir(), WithoutCatalogue())
	stop()
	if n := len(inFlightLines(t, quiet)); n != 0 {
		t.Errorf("nothing is running, and %d in-flight warning(s) were written", n)
	}
}

// catalogueSwitchView is GET /api/v1/catalogue-switch as the Console reads it.
type catalogueSwitchView struct {
	Catalogue               bool `json:"catalogue"`
	ShopProcessInstances    int  `json:"shopProcessInstances"`
	ProductProcessInstances int  `json:"productProcessInstances"`
	Processes               []struct {
		ProcessID string `json:"processId"`
		Instances int    `json:"instances"`
	} `json:"processes"`
}

func readCatalogueSwitch(t *testing.T, srv *Server) catalogueSwitchView {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/catalogue-switch", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/catalogue-switch: %d (%s)", code, body)
	}
	var v catalogueSwitchView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

// TestTheConsoleCanAskWhatTheSwitchStrands: the start's warning is a log line, and a
// log line nobody reads at start is lost. The same count, read live, is what the
// Console's dashboard shows an administrator — and live means it goes away once the
// instances are finished or ended, rather than repeating what was true at boot.
//
// The route is not part of the catalogue: it is the one place that has something to
// say precisely when the catalogue is off, so the switch must leave it served.
func TestTheConsoleCanAskWhatTheSwitchStrands(t *testing.T) {
	dir := t.TempDir()
	on, stop := openAt(t, dir)
	if v := readCatalogueSwitch(t, on); !v.Catalogue || v.ShopProcessInstances != 0 || len(v.Processes) != 0 {
		t.Errorf("with the catalogue on = %+v, want on and nothing stranded", v)
	}
	approvals := startWaiting(t, on, "atlas-genehmigung-fix", 2)
	startWaiting(t, on, "provision-vpn", 1)
	if err := on.catalogStore.SaveItem(catalog.Item{ID: "vpn", HomeCatalog: "c1", ProvisionProcess: "provision-vpn"}); err != nil {
		t.Fatal(err)
	}
	stop()

	off, _ := openAt(t, dir, WithoutCatalogue())
	v := readCatalogueSwitch(t, off)
	if v.Catalogue || v.ShopProcessInstances != 2 || v.ProductProcessInstances != 1 {
		t.Fatalf("with the catalogue off = %+v, want off, 2 shop and 1 product instance(s)", v)
	}
	if len(v.Processes) != 2 || v.Processes[0].ProcessID != "atlas-genehmigung-fix" || v.Processes[0].Instances != 2 ||
		v.Processes[1].ProcessID != "provision-vpn" || v.Processes[1].Instances != 1 {
		t.Errorf("processes = %+v, want both by id, sorted", v.Processes)
	}

	// Live, not a snapshot of the start: end one instance and the count follows.
	if code, b := serveInternal(t, off, http.MethodDelete, fmt.Sprintf("/api/v1/instances/%d", approvals[0]), "", ""); code >= 300 {
		t.Fatalf("cancel instance %d: %d (%s)", approvals[0], code, b)
	}
	if v := readCatalogueSwitch(t, off); v.ShopProcessInstances != 1 {
		t.Errorf("after ending one instance, shop instances = %d, want 1", v.ShopProcessInstances)
	}
}

// TestAStrandedCountThatCouldNotBeReadIsNotZero: when the products could not be read
// at start, the start said so and the set of processes to count is missing. Counting
// without it would answer "nothing stranded", which is not known — so the route says
// it does not know.
func TestAStrandedCountThatCouldNotBeReadIsNotZero(t *testing.T) {
	s := &Server{catalogueOff: true}
	rec := httptest.NewRecorder()
	s.handleCatalogueSwitch(rec, httptest.NewRequest(http.MethodGet, "/api/v1/catalogue-switch", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d (%s), want 503", rec.Code, rec.Body.String())
	}
}
