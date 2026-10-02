package api

import (
	"encoding/json"
	"fmt"
	"net/http"
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

func startWaiting(t *testing.T, srv *Server, id string, n int) {
	t.Helper()
	if code, b := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", waitingProcess(id), "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy %s: %d (%s)", id, code, b)
	}
	for i := 0; i < n; i++ {
		if code, b := serveInternal(t, srv, http.MethodPost, "/api/v1/instances",
			`{"processId":"`+id+`"}`, "application/json"); code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("start %s: %d (%s)", id, code, b)
		}
	}
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
