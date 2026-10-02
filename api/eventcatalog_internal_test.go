package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/eventcatalog"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The drift tests of ADR-0435 §5 that need the server: the catalogue in
// package eventcatalog is held equal to what the system processes throw, what the
// order service publishes and what the feed delivers. An event nobody catalogued
// fails here, and so does an entry nothing emits.

// emitted is one place a system process throws or receives an Atlas-named event.
type emitted struct {
	channel          eventcatalog.Channel
	name             string
	process, element string
}

// systemProcessEvents compiles every embedded system process and lists the signals
// it throws and the Atlas-named messages it throws or receives.
func systemProcessEvents(t *testing.T) []emitted {
	t.Helper()
	procs, _, err := loadSystemBundle(systemProcessesFS)
	if err != nil {
		t.Fatal(err)
	}
	var out []emitted
	for _, sp := range procs {
		deps, err := compiler.ParseAll(1, 1, bytes.NewReader(sp.xml))
		if err != nil {
			t.Fatalf("%s: %v", sp.processID, err)
		}
		for _, d := range deps {
			cp := d.Process
			pid := cp.ProcessId()
			for _, u := range cp.SignalThrowers() {
				out = append(out, emitted{eventcatalog.Signal, u.SignalName, pid, cp.ElementBpmnId(u.ElementId)})
			}
			for id := 0; id < cp.NodeCount(); id++ {
				n := cp.Node(int32(id))
				var name string
				switch n.Type {
				case compiler.TypeMessageStartEvent:
					name = cp.MessageStart(n.Detail).MessageName
				case compiler.TypeMessageCatchEvent:
					name = cp.MessageCatch(n.Detail).MessageName
				case compiler.TypeMessageThrowEvent, compiler.TypeMessageEndEvent:
					name = cp.MessageThrow(n.Detail).MessageName
				}
				if strings.HasPrefix(name, "atlas.") {
					out = append(out, emitted{eventcatalog.Message, name, pid, cp.ElementBpmnId(int32(id))})
				}
			}
		}
	}
	return out
}

// TestSystemProcessEventsAreCatalogued holds both directions: every signal a system
// process throws, and every Atlas-named message one throws or receives, is a domain
// entry naming that process and element; and every such entry's moment is where the
// models say it is.
func TestSystemProcessEventsAreCatalogued(t *testing.T) {
	found := map[string]bool{} // channel|name|process|element
	for _, e := range systemProcessEvents(t) {
		key := string(e.channel) + "|" + e.name + "|" + e.process + "|" + e.element
		found[key] = true
		entry, ok := eventcatalog.Lookup(e.name)
		switch {
		case !ok:
			t.Errorf("%s %s at %s/%s: not in the event catalogue", e.channel, e.name, e.process, e.element)
			continue
		case entry.Kind != eventcatalog.Domain || !entry.Has(e.channel):
			t.Errorf("%s: catalogued as %s on %v, but %s/%s carries it as a %s", e.name, entry.Kind, entry.Channels, e.process, e.element, e.channel)
		}
		var at bool
		for _, m := range entry.Moments {
			at = at || (m.Process == e.process && m.Element == e.element)
		}
		if !at {
			t.Errorf("%s: %s/%s carries it, and the catalogue does not name that moment", e.name, e.process, e.element)
		}
	}
	for _, entry := range eventcatalog.Entries {
		for _, ch := range []eventcatalog.Channel{eventcatalog.Signal, eventcatalog.Message} {
			if !entry.Has(ch) {
				continue
			}
			for _, m := range entry.Moments {
				if !found[string(ch)+"|"+entry.Type+"|"+m.Process+"|"+m.Element] {
					t.Errorf("%s: the catalogue says %s/%s carries it as a %s; the model does not", entry.Type, m.Process, m.Element, ch)
				}
			}
		}
	}
}

// TestOrderMessagesCarryTheirCataloguedPayload ties the two messages the order
// service publishes to their entries: the names it publishes under, and the
// variables a placed order starts the fulfilment process with. Those variables are
// strings the order service builds; none of them is a secret, and a new one is a
// catalogue change.
func TestOrderMessagesCarryTheirCataloguedPayload(t *testing.T) {
	placed, ok := eventcatalog.Lookup(order.PlacedMessage)
	if !ok {
		t.Fatalf("%s is not catalogued", order.PlacedMessage)
	}
	if _, ok := eventcatalog.Lookup(order.AdvancedMessage); !ok {
		t.Fatalf("%s is not catalogued", order.AdvancedMessage)
	}
	vars := order.PlacedVariables(order.Order{ID: "o1", Orderer: "a", Recipient: "b"}, "https://portal", "https://api")
	var got []string
	for name := range vars {
		got = append(got, name)
	}
	assertSameFields(t, placed, got)
}

// TestFeedTypesAreCatalogued renders one row of every kind the feed holds, with every
// optional field set, and holds the CloudEvent it becomes to the catalogue: its type
// is an entry with the feed channel, and its data carries exactly the declared fields.
func TestFeedTypesAreCatalogued(t *testing.T) {
	rows := []state.FeedEntry{
		{Kind: state.FeedOutcome, Outcome: &model.ActionOutcomeValue{
			OrderID: "o1", Position: "p1", CommandID: "c1", Action: "extend", Effect: "modify",
			Outcome: "completed", Source: "process", Principal: "u1", ItemID: "i1", At: 1,
			VariantID: "v1", InstanceKey: 7, Result: `{"ok":true}`, EventType: "storage.extend.completed",
		}},
		{Kind: state.FeedGranted, Granted: &model.EntitlementValue{
			Principal: "u1", ItemID: "i1", OrderID: "o1", Since: 1, VariantID: "v1", Until: 2,
		}},
		{Kind: state.FeedRevoked, Revoked: &model.EntitlementHistoryValue{
			Principal: "u1", ItemID: "i1", OrderID: "o1", Since: 1, EndedAt: 2, EndedBy: "u2", VariantID: "v1",
		}},
	}
	produced := map[string]bool{}
	for _, row := range rows {
		ev := feedEnvelope(row, "node", "urn:test", "home")
		var entry eventcatalog.Entry
		if row.Outcome != nil {
			// An outcome is published under the name its product declared; the
			// catalogue describes the shape of all of them once.
			entry = shapeEntry(t)
		} else {
			var ok bool
			if entry, ok = eventcatalog.Lookup(ev.Type); !ok {
				t.Errorf("the feed delivers %s; it is not in the event catalogue", ev.Type)
				continue
			}
		}
		if !entry.Has(eventcatalog.Feed) {
			t.Errorf("%s: delivered by the feed, catalogued without the feed channel", entry.Type)
		}
		produced[entry.Type] = true
		data, _ := ev.Data.(map[string]any)
		var got []string
		for name := range data {
			got = append(got, name)
		}
		assertSameFields(t, entry, got)
	}
	for _, e := range eventcatalog.Entries {
		if e.Has(eventcatalog.Feed) && !produced[e.Type] {
			t.Errorf("%s: catalogued on the feed, and the feed produces no such event", e.Type)
		}
	}

	// A row recorded without a name is published under an Atlas name of the shape's
	// own form, never under an empty type.
	bare := rows[0]
	o := *bare.Outcome
	o.EventType = ""
	bare.Outcome = &o
	if typ := feedEnvelope(bare, "node", "urn:test", "").Type; !eventcatalog.WellNamed(typ) || !strings.HasPrefix(typ, "atlas.action.") {
		t.Errorf("an outcome recorded without a name is published as %q", typ)
	}
}

func shapeEntry(t *testing.T) eventcatalog.Entry {
	t.Helper()
	for _, e := range eventcatalog.Entries {
		if e.Shape && e.Has(eventcatalog.Feed) {
			return e
		}
	}
	t.Fatal("the catalogue does not describe the shape of an action outcome")
	return eventcatalog.Entry{}
}

// assertSameFields holds a payload's field names to an entry's: every field it
// carries is declared, and every field the entry promises always is carried.
func assertSameFields(t *testing.T, e eventcatalog.Entry, got []string) {
	t.Helper()
	declared := map[string]eventcatalog.Field{}
	for _, f := range e.Payload {
		declared[f.Name] = f
	}
	have := map[string]bool{}
	for _, name := range got {
		have[name] = true
		if _, ok := declared[name]; !ok {
			t.Errorf("%s carries %s; the catalogue does not declare it", e.Type, name)
		}
	}
	for _, f := range e.Payload {
		if f.Presence == eventcatalog.Always && !have[f.Name] {
			t.Errorf("%s: the catalogue promises %s always; the payload does not carry it", e.Type, f.Name)
		}
	}
}

// TestCatalogueRolesAreTheServersRoles holds the role names the catalogue states to
// the ones the server checks.
func TestCatalogueRolesAreTheServersRoles(t *testing.T) {
	for got, want := range map[string]string{
		eventcatalog.RoleAdmin: RoleAdmin, eventcatalog.RoleModeler: RoleModeler,
		eventcatalog.RoleFeedReader: RoleFeedReader,
	} {
		if got != want {
			t.Errorf("catalogue role %q, server role %q", got, want)
		}
	}
}

// TestEverySecretGuardIsATest holds each entry's SecretGuard to a test that exists, so
// the name cannot outlive the test it names.
func TestEverySecretGuardIsATest(t *testing.T) {
	for _, e := range eventcatalog.Entries {
		pkg, name, ok := strings.Cut(e.SecretGuard, ".")
		if !ok {
			t.Errorf("%s: secret guard %q is not package.Test", e.Type, e.SecretGuard)
			continue
		}
		dir := "."
		if pkg != "api" {
			dir = filepath.Join("..", pkg)
		}
		if !testDeclared(t, dir, name) {
			t.Errorf("%s: secret guard %s is not a test in %s", e.Type, name, dir)
		}
	}
}

func testDeclared(t *testing.T, dir, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(t \*testing\.T\)`)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if decl.Match(src) {
			return true
		}
	}
	return false
}

// approvalListenerBPMN is what an installation deploys to hear of a pending approval:
// a signal start on atlas.approval.requested and nothing else.
const approvalListenerBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             id="defs_genehmigung_hoeren" targetNamespace="http://atlas/examples">
  <signal id="sig_approval_requested" name="atlas.approval.requested"/>
  <process id="proc_genehmigung_hoeren" isExecutable="true">
    <startEvent id="start"><signalEventDefinition signalRef="sig_approval_requested"/></startEvent>
    <userTask id="ansehen"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="ansehen"/>
  </process>
</definitions>`

// TestApprovalAnnouncesTheRequestAsASignal starts each of the three shop approval
// processes the way the order does, with the variables the order hands them, and
// holds what a listener receives to the catalogue: every declared field it promises,
// nothing it does not declare, the approval's own instance key in atlasInstance, and
// the approval task waiting. Nothing undeclared reaching the listener is what makes
// the payload safe to describe: no variable at the throw is a secret.
func TestApprovalAnnouncesTheRequestAsASignal(t *testing.T) {
	entry, ok := eventcatalog.Lookup("atlas.approval.requested")
	if !ok {
		t.Fatal("atlas.approval.requested is not catalogued")
	}
	for _, kind := range []string{"fixed", "role", "superior"} {
		t.Run(kind, func(t *testing.T) {
			srv, _ := newSystemServer(t, WithSystemProcesses())
			code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", approvalListenerBPMN, "application/xml")
			if code != http.StatusOK {
				t.Fatalf("deploy listener: %d %s", code, raw)
			}
			var dep struct {
				Key uint64 `json:"key"`
			}
			if err := json.Unmarshal(raw, &dep); err != nil || dep.Key == 0 {
				t.Fatalf("decode listener deployment: %v (%s)", err, raw)
			}

			ord := order.Order{ID: "ord-1", Orderer: "besteller@example.org", Recipient: "empfaenger@example.org"}
			line := order.Line{ItemID: "laptop", ProvisionProcess: "proc_laptop",
				Approval: order.Approval{Kind: kind, Ref: "genehmiger"}}
			approval := line.ApprovalProcess()
			key, err := srv.startBinding(catalog.Binding{Process: approval}, "", srv.positionStartVars(ord, line, "laptop"))
			if err != nil {
				t.Fatalf("start %s: %v", approval, err)
			}
			if kind == "superior" {
				ad := lease(t, srv, "ad")
				body := `{"worker":"w1","leaseToken":` + strconv.FormatUint(ad.LeaseToken, 10) +
					`,"variables":{"entries":[{"manager":"chefin@example.org"}]}}`
				if code, raw := completeJob(t, srv, ad, body); code != http.StatusOK && code != http.StatusNoContent {
					t.Fatalf("complete directory lookup: %d %s", code, raw)
				}
			}

			code, raw = serveInternal(t, srv, http.MethodGet, "/api/v1/instances?process="+strconv.FormatUint(dep.Key, 10), "", "")
			if code != http.StatusOK {
				t.Fatalf("list listener instances: %d %s", code, raw)
			}
			var rows []struct {
				Variables []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"variables"`
			}
			if err := json.Unmarshal(listRows(t, raw), &rows); err != nil {
				t.Fatalf("decode listener instances: %v (%s)", err, raw)
			}
			if len(rows) != 1 {
				t.Fatalf("one approval started %d listener instances, want exactly 1", len(rows))
			}
			got := map[string]string{}
			var names []string
			for _, v := range rows[0].Variables {
				got[v.Name] = v.Value
				names = append(names, v.Name)
			}
			sort.Strings(names)
			assertSameFields(t, entry, names)
			if want := strconv.FormatUint(key, 10); !strings.Contains(got["atlasInstance"], want) {
				t.Errorf("atlasInstance = %q, want the approval's instance key %s", got["atlasInstance"], want)
			}
			if _, has := got["vorgesetzter"]; has != (kind == "superior") {
				t.Errorf("vorgesetzter present = %v for kind %s", has, kind)
			}

			// The approval did not wait on the notice: its task is open.
			code, raw = serveInternal(t, srv, http.MethodGet, "/api/v1/tasks", "", "")
			if code != http.StatusOK {
				t.Fatalf("list tasks: %d %s", code, raw)
			}
			var tasks []struct {
				ProcessID string `json:"processId"`
				ElementID string `json:"elementId"`
			}
			if err := json.Unmarshal(listRows(t, raw), &tasks); err != nil {
				t.Fatalf("decode tasks: %v (%s)", err, raw)
			}
			var waiting bool
			for _, it := range tasks {
				waiting = waiting || (it.ProcessID == approval && it.ElementID == "Genehmigen")
			}
			if !waiting {
				t.Errorf("%s is not waiting at Genehmigen after the signal: %s", approval, raw)
			}
		})
	}
}
