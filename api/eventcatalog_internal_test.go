package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/eventcatalog"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The event catalogue (ADR-0435) held to what the code emits, in both directions:
// what a system process throws or receives under an atlas.* name is an entry that
// says so, an entry is emitted where it says, and what the feed and the order
// messages carry is exactly the payload the entry promises.

// systemPoint is where a system process throws or receives an atlas.* name.
type systemPoint struct {
	name    string
	channel eventcatalog.Channel
	process string
	element string
	throws  bool
}

// systemEventPoints compiles every system process and lists its atlas.* signals and
// messages.
func systemEventPoints(t *testing.T) []systemPoint {
	t.Helper()
	procs, _, err := loadSystemBundle(systemProcessesFS)
	if err != nil {
		t.Fatalf("load the system bundle: %v", err)
	}
	var out []systemPoint
	for _, p := range procs {
		ds, err := compiler.ParseAll(1000, 1, bytes.NewReader(p.xml))
		if err != nil {
			t.Fatalf("compile %s: %v", p.processID, err)
		}
		for _, d := range ds {
			cp := d.Process
			for _, sp := range cp.SignalPoints() {
				if eventcatalog.IsAtlasName(sp.SignalName) {
					out = append(out, systemPoint{sp.SignalName, eventcatalog.Signal, cp.ProcessId(), sp.Element, !sp.Receives()})
				}
			}
			for _, mp := range cp.MessageReceivers() {
				if eventcatalog.IsAtlasName(mp.MessageName) {
					out = append(out, systemPoint{mp.MessageName, eventcatalog.Message, cp.ProcessId(), mp.Element, false})
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no system process throws or receives an atlas.* name; this guard would pass vacuously")
	}
	return out
}

// TestEverySystemProcessEventIsCatalogued: a signal a system process throws is a
// domain entry with that moment, and a message one receives is an entry naming it as
// the receiver; and every such entry is thrown, or received, where it says. Atlas's
// own processes do not listen to a signal of Atlas's: a platform fact is not a signal,
// and a system process that waited on a domain one would be a second producer.
func TestEverySystemProcessEventIsCatalogued(t *testing.T) {
	points := systemEventPoints(t)
	for _, p := range points {
		e, ok := eventcatalog.Lookup(p.name)
		switch {
		case !ok:
			t.Errorf("%s %s at %s/%s, and the event catalogue has no entry for it", p.channel, p.name, p.process, p.element)
		case !e.Has(p.channel):
			t.Errorf("%s is a %s at %s/%s, and its entry does not name that channel", p.name, p.channel, p.process, p.element)
		case e.Kind != eventcatalog.Domain:
			t.Errorf("%s is emitted by a system process, so it is a domain entry, not %s", p.name, e.Kind)
		case !slices.Contains(e.Moment.Places, eventcatalog.At(p.process, p.element)):
			t.Errorf("%s is at %s/%s, and its entry says %+v", p.name, p.process, p.element, e.Moment.Places)
		case p.channel == eventcatalog.Signal && !p.throws:
			t.Errorf("system process %s listens to the signal %s; Atlas's own processes do not", p.process, p.name)
		case e.ServiceCatalogue != catalogueSystemProcesses[p.process]:
			t.Errorf("%s at %s: serviceCatalogue=%v, and the process is the catalogue's: %v",
				p.name, p.process, e.ServiceCatalogue, catalogueSystemProcesses[p.process])
		}
	}
	for _, e := range eventcatalog.Entries {
		for _, ch := range []eventcatalog.Channel{eventcatalog.Signal, eventcatalog.Message} {
			if !e.Has(ch) {
				continue
			}
			for _, pl := range e.Moment.Places {
				found := slices.ContainsFunc(points, func(p systemPoint) bool {
					return p.name == e.Type && p.channel == ch && p.process == pl.Process && p.element == pl.Element
				})
				if !found {
					t.Errorf("%s says it is a %s at %s/%s, and no system process has it there", e.Type, ch, pl.Process, pl.Element)
				}
			}
		}
	}
	// The messages the order service publishes are the catalogue's names.
	for _, name := range []string{order.PlacedMessage, order.AdvancedMessage} {
		if e, ok := eventcatalog.Lookup(name); !ok || !e.Has(eventcatalog.Message) {
			t.Errorf("the order service publishes %s, which is no message entry", name)
		}
	}
}

// fullFeedRows are one row of every kind the feed writes, every optional field set,
// so the envelope shows every field it can carry.
func fullFeedRows() []state.FeedEntry {
	return []state.FeedEntry{
		{Partition: 1, Position: 1, At: 1, Outcome: &model.ActionOutcomeValue{
			OrderID: "o", Position: "p", CommandID: "c", Action: "sperren", Effect: "change", Outcome: "completed",
			Source: "shop-task", Principal: "u", ItemID: "i", VariantID: "v", InstanceKey: 7, Result: `{"ok":true}`, At: 1,
		}},
		{Partition: 1, Position: 2, At: 2, Granted: &model.EntitlementValue{
			Principal: "u", ItemID: "i", VariantID: "v", OrderID: "o", Since: 1, Until: 9,
		}},
		{Partition: 1, Position: 3, At: 3, Revoked: &model.EntitlementHistoryValue{
			Principal: "u", ItemID: "i", VariantID: "v", OrderID: "o", Since: 1, EndedAt: 2, EndedBy: "x",
		}},
		{Partition: 1, Position: 4, At: 4, Kind: state.FeedIncidentRaised, Definition: feedTestDefinition, Incident: &model.IncidentValue{
			ProcessInstanceKey: 40, ElementInstanceKey: 41, JobKey: 42, ElementId: 0, RaisedAt: 4,
			Message: "worker said: user anna@example.org has password hunter2",
		}},
		{Partition: 1, Position: 5, At: 5, Kind: state.FeedIncidentResolved, Definition: feedTestDefinition, Incident: &model.IncidentValue{
			ProcessInstanceKey: 40, ElementInstanceKey: 41, JobKey: 42, ElementId: 0, RaisedAt: 4,
			Message: "worker said: user anna@example.org has password hunter2",
		}},
	}
}

// feedTestDefinition is the definition the incident rows above name, and
// feedTestDefs the deployment it stands for, so the envelope can name the process and
// the element as it does while a definition is deployed.
const feedTestDefinition = 77

func feedTestDefs(t *testing.T) defIndex {
	t.Helper()
	cp, err := compiler.Parse(feedTestDefinition, 2, strings.NewReader(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true"><startEvent id="s"/></process></definitions>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return defIndex{feedTestDefinition: {ProcessID: "p", Version: 2, cp: cp}}
}

// TestTheFeedCarriesNoSecret: every type the feed names is an entry with the feed
// channel, every non-shape feed entry is a type the feed produces, and the data of
// each carries exactly the fields its entry declares — none of them a secret, and
// none the entry does not know of.
func TestTheFeedCarriesNoSecret(t *testing.T) {
	produced := map[string]bool{}
	defs := feedTestDefs(t)
	sources := feedSources{catalogue: "urn:test:catalog", engine: "urn:test:engine"}
	for _, row := range fullFeedRows() {
		ev := feedEnvelope(row, "n1", sources, "cat-home", defs)
		if want := map[bool]string{true: sources.catalogue, false: sources.engine}[row.IsCatalogue()]; ev.Source != want {
			t.Errorf("%s is from %s, want %s", ev.Type, ev.Source, want)
		}
		if data, _ := ev.Data.(map[string]any); data != nil {
			for k, v := range data {
				if s, ok := v.(string); ok && strings.Contains(s, "hunter2") {
					t.Errorf("%s carries the incident's message in %s", ev.Type, k)
				}
			}
		}
		var entry eventcatalog.Entry
		switch {
		case strings.HasPrefix(ev.Type, eventcatalog.ActionOutcomePrefix) || row.Outcome != nil:
			for _, e := range eventcatalog.Entries {
				if e.Shaped && e.Has(eventcatalog.Feed) {
					entry = e
				}
			}
		default:
			e, ok := eventcatalog.Lookup(ev.Type)
			if !ok || !e.Has(eventcatalog.Feed) {
				t.Errorf("the feed produces %s, which is no feed entry", ev.Type)
				continue
			}
			entry = e
			produced[ev.Type] = true
		}
		if entry.ServiceCatalogue != row.IsCatalogue() {
			t.Errorf("%s: the catalogue says serviceCatalogue=%v, and the feed treats its row as a catalogue fact: %v",
				ev.Type, entry.ServiceCatalogue, row.IsCatalogue())
		}
		data, _ := ev.Data.(map[string]any)
		declared := map[string]bool{}
		for _, f := range entry.Payload {
			declared[f.Name] = true
			if _, there := data[f.Name]; !there {
				t.Errorf("%s: the catalogue declares %s and the feed did not carry it", ev.Type, f.Name)
			}
		}
		for k := range data {
			if !declared[k] {
				t.Errorf("%s: the feed carries %s, which the catalogue does not declare", ev.Type, k)
			}
		}
	}
	for _, e := range eventcatalog.Entries {
		if e.Has(eventcatalog.Feed) && !e.Shaped && !produced[e.Type] {
			t.Errorf("%s says it is in the feed, and the feed never produces it", e.Type)
		}
	}
	// An outcome whose action names no event type takes Atlas's own name.
	row := fullFeedRows()[0]
	row.Outcome.EventType = ""
	if ev := feedEnvelope(row, "n1", sources, "", defs); ev.Type != eventcatalog.ActionOutcomePrefix+"completed" {
		t.Errorf("an undeclared outcome is typed %s", ev.Type)
	}
}

// TestTheOrderMessagesCarryNoSecret: the fulfilment process is started with exactly
// the fields the catalogue declares for atlas.order.placed, and the advance carries
// none — it is correlated on the order id.
func TestTheOrderMessagesCarryNoSecret(t *testing.T) {
	placed, _ := eventcatalog.Lookup(eventcatalog.OrderPlaced)
	var want []string
	for _, f := range placed.Payload {
		want = append(want, f.Name)
	}
	var got []string
	for k := range order.PlacedVariables(order.Order{ID: "o", Orderer: "a", Recipient: "b"}, "https://shop", "https://api") {
		got = append(got, k)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Errorf("atlas.order.placed carries %v; the catalogue declares %v", got, want)
	}
	if advanced, _ := eventcatalog.Lookup(eventcatalog.OrderAdvanced); len(advanced.Payload) != 0 {
		t.Errorf("atlas.order.advanced declares %v; the order service publishes it with none", advanced.Payload)
	}
}

// TestTheEventCatalogueIsServedAndItsListenersAreAnAdministratorsView: a modeler reads
// the entries; who listens — Atlas's own fulfilment process on its messages, and a
// model of the installation on the intake signal, with the personal data it receives —
// is the listeners route, which the route table holds to administrators. Neither is
// switched off with the shop.
func TestTheEventCatalogueIsServedAndItsListenersAreAnAdministratorsView(t *testing.T) {
	srv := eventServer(t, WithSystemProcesses())
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", intakeListenerBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy the listener: %d %s", code, body)
	}

	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/event-catalog", "", "")
	var cat eventCatalogResp
	if code != http.StatusOK || json.Unmarshal(body, &cat) != nil || len(cat.Entries) != len(eventcatalog.Entries) {
		t.Fatalf("event catalogue = %d %s", code, body)
	}

	code, body = serveInternal(t, srv, http.MethodGet, "/api/v1/event-catalog/listeners", "", "")
	var ls eventListenersResp
	if code != http.StatusOK || json.Unmarshal(body, &ls) != nil {
		t.Fatalf("listeners = %d %s", code, body)
	}
	var sawSystem, sawIntake bool
	for _, l := range ls.Processes {
		if l.System && l.Type == eventcatalog.OrderPlaced && l.Role == "start" && l.Channel == eventcatalog.Message {
			sawSystem = true
		}
		if !l.System && l.Type == eventcatalog.UserRequested && l.Role == "start" && l.Catalogued {
			sawIntake = slices.Contains(l.Personal, "email") && !slices.Contains(l.Personal, "atlasInstance")
		}
	}
	if !sawSystem || !sawIntake {
		t.Fatalf("listeners = %+v, want the fulfilment process on atlas.order.placed and the installation's intake listener with its personal fields", ls.Processes)
	}
	if !slices.Contains(ls.FeedTypes, eventcatalog.EntitlementGranted) || !slices.Contains(ls.FeedTypes, eventcatalog.IncidentRaised) || ls.CatalogueWithheld {
		t.Errorf("feed types = %v, catalogue withheld = %v", ls.FeedTypes, ls.CatalogueWithheld)
	}
	if role := routeRoleOf(t, srv, "GET /api/v1/event-catalog/listeners"); role != RoleAdmin {
		t.Errorf("the listeners route is %s, want admin", role)
	}
	if role := routeRoleOf(t, srv, "GET /api/v1/event-catalog"); role != RoleModeler {
		t.Errorf("the catalogue route is %s, want modeler", role)
	}

	off := eventServer(t, WithoutCatalogue())
	if code, body := serveInternal(t, off, http.MethodGet, "/api/v1/event-catalog", "", ""); code != http.StatusOK {
		t.Errorf("with the shop switched off the event catalogue answers %d %s", code, body)
	}
	code, body = serveInternal(t, off, http.MethodGet, "/api/v1/event-catalog/listeners", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"catalogueWithheld":true`) {
		t.Errorf("with the shop switched off the listeners answer %d %s", code, body)
	}
}

// routeRoleOf is the role a route declares.
func routeRoleOf(t *testing.T, srv *Server, route string) string {
	t.Helper()
	for _, r := range srv.apiRoutes() {
		if r.method+" "+r.pattern == route {
			return r.op.role
		}
	}
	t.Fatalf("no route %s", route)
	return ""
}

// eventServer is a server built with the options a case needs.
func eventServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	dir := t.TempDir()
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
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return srv
}
