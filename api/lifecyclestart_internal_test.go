package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// TestEveryTriggerOutcomeHasAnAnswer: each way a directed trigger starts nothing is
// said in words an operator reading a failed job can act on (ADR-0425).
func TestEveryTriggerOutcomeHasAnAnswer(t *testing.T) {
	for _, c := range []struct {
		outcome engine.TriggerOutcome
		want    string
	}{
		{engine.TriggerCreated, ""},
		{engine.TriggerReplayed, ""},
		{engine.TriggerSingletonTaken, "already running"},
		{engine.TriggerInactive, "deactivated"},
		{engine.TriggerNoSuchStart, "no message start event"},
		{engine.TriggerNotProcessed, "not processed"},
	} {
		got := triggerRefusal(engine.TriggerResult{Outcome: c.outcome}, "p", "m")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("outcome %d: %q, want %q", c.outcome, got, c.want)
		}
	}
	if err := (errTriggerRefused{"why"}); err.Error() != "why" {
		t.Errorf("Error() = %q", err.Error())
	}
}

const twoTriggers = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="m_a" name="t.a"/>
  <message id="m_b" name="t.b"/>
  <process id="two-triggers" isExecutable="true">
    <startEvent id="A"><messageEventDefinition messageRef="m_a"/></startEvent>
    <startEvent id="B"><messageEventDefinition messageRef="m_b"/></startEvent>
    <endEvent id="AE"/>
    <endEvent id="BE"/>
    <sequenceFlow id="f1" sourceRef="A" targetRef="AE"/>
    <sequenceFlow id="f2" sourceRef="B" targetRef="BE"/>
  </process>
</definitions>`

// TestReceiptsArePrunedOnTheRetentionSweep: the sweep drops a receipt older than
// its retention, at most once an hour, and a retry after that starts anew.
func TestReceiptsArePrunedOnTheRetentionSweep(t *testing.T) {
	srv := newServerForErrors(t)
	WithTriggerReceiptRetention(time.Nanosecond)(srv)
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", twoTriggers, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, body)
	}
	var d struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatal(err)
	}
	if res, err := srv.fireTrigger(d.Key, "t.a", "test", "one", nil); err != nil || res.Outcome != engine.TriggerCreated {
		t.Fatalf("trigger: %+v (%v)", res, err)
	}
	later := time.Now().Add(2 * time.Hour).UnixNano()
	srv.do(func() { srv.pruneTriggerReceipts(later) })
	if n, err := srv.store.TriggerReceiptCount(); err != nil || n != 0 {
		t.Fatalf("receipts after the sweep = %d (%v), want 0", n, err)
	}
	// Within the hour the sweep does not look again.
	if res, _ := srv.fireTrigger(d.Key, "t.a", "test", "two", nil); res.Outcome != engine.TriggerCreated {
		t.Fatalf("second trigger: %+v", res)
	}
	srv.do(func() { srv.pruneTriggerReceipts(later + int64(time.Minute)) })
	if n, _ := srv.store.TriggerReceiptCount(); n != 1 {
		t.Fatalf("receipts = %d, want 1 — the sweep ran twice within an hour", n)
	}
	if res, _ := srv.fireTrigger(d.Key, "t.a", "test", "one", nil); res.Outcome != engine.TriggerCreated {
		t.Fatalf("retry after its receipt was pruned = %+v, want a new instance", res)
	}
}

// TestStartBindingRefusesWhatCannotStart: an unbound operation, an undeployed
// process, and a trigger the engine refuses each come back as an error.
func TestStartBindingRefusesWhatCannotStart(t *testing.T) {
	srv := newServerForErrors(t)
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", twoTriggers, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, body)
	}
	if _, err := srv.startBinding(bindingOf("", ""), "", nil); err == nil {
		t.Error("an unbound operation started")
	}
	if _, err := srv.startBinding(bindingOf("nowhere", ""), "", nil); err == nil {
		t.Error("an undeployed process started")
	}
	if _, err := srv.startBinding(bindingOf("two-triggers", "t.none"), "x", nil); err == nil {
		t.Error("a missing start event started")
	}
	if _, err := srv.startBinding(bindingOf("two-triggers", ""), "", nil); err == nil {
		t.Error("a process only its triggers can start was started by hand")
	}
	if key, err := srv.startBinding(bindingOf("two-triggers", "t.b"), "y", nil); err != nil || key == 0 {
		t.Errorf("a triggered start = %d (%v)", key, err)
	}
}

func bindingOf(process, message string) catalog.Binding {
	return catalog.Binding{Process: process, Message: message}
}

// TestEntryPointsReadTheNewestVersion: what publishing asks of a lifecycle process —
// its message starts, whether it has a none start, whether it is deployed at all.
func TestEntryPointsReadTheNewestVersion(t *testing.T) {
	srv := newServerForErrors(t)
	look := processLookup{s: srv}
	if _, _, deployed := look.EntryPoints(" "); deployed {
		t.Error("a blank id is deployed")
	}
	if _, _, deployed := look.EntryPoints("nowhere"); deployed {
		t.Error("an undeployed id is deployed")
	}
	withNone := strings.Replace(twoTriggers, `<endEvent id="AE"/>`,
		`<startEvent id="N"/><endEvent id="NE"/><sequenceFlow id="f3" sourceRef="N" targetRef="NE"/><endEvent id="AE"/>`, 1)
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", withNone, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, body)
	}
	msgs, hasNone, deployed := look.EntryPoints("two-triggers")
	if !deployed || !hasNone || strings.Join(msgs, ",") != "t.a,t.b" {
		t.Fatalf("EntryPoints = %v none=%v deployed=%v", msgs, hasNone, deployed)
	}
}

// TestReconciliationDeprovisionsThroughALifecycleProcess: an unmanaged right of a
// product that binds a lifecycle process is revoked at the process's deprovision
// start (ADR-0425), and a product whose deprovision start does not exist says so
// instead of starting anything.
func TestReconciliationDeprovisionsThroughALifecycleProcess(t *testing.T) {
	srv, _ := newValidateServer(t)
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", twoTriggers, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, body)
	}
	save := func(deprov string) {
		t.Helper()
		var err error
		srv.do(func() {
			err = srv.catalogStore.SaveItem(catalog.Item{ID: "vpn-access", HomeCatalog: "cat",
				State: catalog.StateActive, LifecycleProcess: "two-triggers",
				Operations: map[string]string{catalog.OpProvision: "t.a", catalog.OpDeprovision: deprov}})
		})
		if err != nil {
			t.Fatalf("save product: %v", err)
		}
	}

	save("t.missing")
	seedDiscrepancy(t, srv, openUnmanaged("r1"))
	if code, body := deprovision(t, srv, "r1"); code == http.StatusOK || !strings.Contains(body, "t.missing") {
		t.Fatalf("a missing deprovision start: %d (%s), want a refusal naming it", code, body)
	}

	save("t.b")
	seedDiscrepancy(t, srv, openUnmanaged("r2"))
	if code, body := deprovision(t, srv, "r2"); code != http.StatusOK {
		t.Fatalf("deprovision: %d (%s)", code, body)
	}
	// The deprovision branch runs straight to its end, so the instance it started is
	// among the finished ones.
	var finished int
	srv.do(func() {
		_ = srv.store.CompletedProcessInstances(func(uint64, *model.ProcessInstanceValue) error {
			finished++
			return nil
		})
	})
	if finished != 1 {
		t.Fatalf("finished instances = %d, want the one deprovisioning", finished)
	}
}
