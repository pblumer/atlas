package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
)

// ADR-0495 slice F: a release's snapshot drawn as Panorama's starmap graph, the release
// lens, with every layer it records and nothing it does not.

// lensSnapshot records every layer, with one of each thing the lens draws.
func lensSnapshot() *archsketch.Snapshot {
	https := func(host string) *archsketch.Endpoint {
		return &archsketch.Endpoint{Scheme: "https", Host: host, Port: "443"}
	}
	s := &archsketch.Snapshot{
		FormatVersion: archsketch.FormatVersion, MappingVersion: archsketch.MappingVersion, TakenAt: 1_792_000_000,
		Processes: []archsketch.Process{{ProcessID: "rechnung", Name: "Rechnung", Version: 3}, {ProcessID: "eingang", Version: 1}},
		EntryPoints: []archsketch.EntryPoint{{Kind: archsketch.EntryMessage, Name: "auftrag", Starts: true,
			UsedBy: []archsketch.Use{{ProcessID: "rechnung", ElementID: "Start"}}}},
		Dependencies: []archsketch.Dependency{
			{Kind: archsketch.DependencyWorker, Name: "mailer", WorkerType: "mail", UsedBy: []archsketch.Use{{ProcessID: "rechnung", ElementID: "Senden"}}},
			{Kind: archsketch.DependencyRuntimeWorker, Name: "= kanal", UsedBy: []archsketch.Use{{ProcessID: "eingang", ElementID: "Weiter"}}},
			{Kind: archsketch.DependencyJobType, Name: "pdf-render", UsedBy: []archsketch.Use{{ProcessID: "rechnung", ElementID: "Pdf"}}},
			{Kind: archsketch.DependencyDecision, Name: "rabatt", UsedBy: []archsketch.Use{{ProcessID: "rechnung", ElementID: "Rule"}}},
			{Kind: archsketch.DependencyCall, Name: "mahnstufe", TargetApplicationID: "app-mahn", TargetApplication: "Mahnwesen",
				UsedBy: []archsketch.Use{{ProcessID: "rechnung", ElementID: "Mahnen"}}},
			{Kind: archsketch.DependencyCall, Name: "geheim", TargetWithheld: true, UsedBy: []archsketch.Use{{ProcessID: "eingang", ElementID: "Call"}}},
		},
		Physical: &archsketch.Physical{
			Workers: []archsketch.WorkerSite{
				{Name: "mailer", WorkerType: "mail", Placement: archsketch.PlacementEngine, Endpoint: https("smtp.example.com"),
					MailboxEndpoint: &archsketch.Endpoint{Withheld: true}},
				{Name: "scanner", WorkerType: "rest", Placement: archsketch.PlacementEngine, Endpoint: &archsketch.Endpoint{Unparseable: true}},
			},
			Inbound: []archsketch.InboundSite{{MessageName: "auftrag", Worker: "broker", WorkerType: "nats", Endpoint: https("nats.example.com")}},
		},
		Data: &archsketch.Data{
			Objects: []archsketch.DataObject{{ProcessID: "rechnung", Name: "beleg", Class: "Rechnung"}},
			Stores:  []archsketch.DataStore{{Name: "Kundenstamm", Class: "Kunde", UsedBy: []archsketch.Use{{ProcessID: "eingang", ElementID: "Lesen"}}}},
			Classes: []archsketch.DataClass{{Name: "Kunde", Modeled: true}, {Name: "Rechnung"}},
			Personal: []archsketch.PersonalDeclaration{{ProcessID: "rechnung", Variables: []string{"email"}, Subject: "kunde",
				HandedTo: []archsketch.PersonalRecipient{{Kind: archsketch.DependencyWorker, Name: "mailer"}, {Kind: archsketch.DependencyJobType, Name: "pdf-render"}}}},
		},
		Business: &archsketch.Business{
			Processes: []archsketch.BusinessProcess{{ProcessID: "rechnung"}},
			Roles:     []archsketch.BusinessRole{{Name: "buchhaltung", Kind: archsketch.RoleCandidateGroup, UsedBy: []archsketch.Use{{ProcessID: "rechnung", ElementID: "Pruefen"}}}},
			Products: []archsketch.Product{{ID: "prod-1", Name: "Rechnungsversand", CatalogID: "cat-1", CatalogName: "Finanzen", State: "published",
				Bindings: []archsketch.ProductBinding{{Op: "provision", ProcessID: "rechnung"}}}},
		},
		Strategy: &archsketch.Strategy{
			Capabilities: []archsketch.Capability{{Key: "fakturierung", Name: "Fakturierung", RealizedBy: []archsketch.Realization{
				{Kind: archsketch.RealizedByProcess, ProcessID: "rechnung"}, {Kind: archsketch.RealizedByWorker, Worker: "mailer"}}}},
			ValueStreams: []archsketch.ValueStream{{Key: "order-to-cash", Name: "Order to Cash", Stages: []archsketch.Stage{
				{Key: "s1", Name: "Fakturieren", Capabilities: []string{"fakturierung", "unbekannt"}}}}},
		},
		Coupling: &archsketch.Coupling{
			Names: []archsketch.NameUse{
				{Kind: archsketch.NameMessage, Name: "versandt", Direction: archsketch.DirectionOut, UsedBy: []archsketch.Use{{ProcessID: "rechnung", ElementID: "Versandt"}}},
				{Kind: archsketch.NameSignal, Name: "abschluss", Direction: archsketch.DirectionIn, UsedBy: []archsketch.Use{{ProcessID: "eingang", ElementID: "Abschluss"}}},
			},
			Peers: []archsketch.Peer{
				{Kind: archsketch.NameMessage, Name: "auftrag", Direction: archsketch.DirectionIn, ApplicationID: "app-shop", Application: "Shop", ProcessID: "bestellung", ElementID: "Senden"},
				{Kind: archsketch.NameMessage, Name: "versandt", Direction: archsketch.DirectionOut, ApplicationID: "app-mahn", Application: "Mahnwesen", ProcessID: "mahnlauf", ElementID: "Warten"},
				{Kind: archsketch.NameSignal, Name: "abschluss", Direction: archsketch.DirectionIn, Withheld: true},
				{Kind: archsketch.NameSignal, Name: "abschluss", Direction: archsketch.DirectionIn, Withheld: true},
			},
			Feed: archsketch.EventFeed{Subscriptions: 2, Targets: []archsketch.FeedTarget{
				{Worker: "siem", ConfiguredID: "w-siem", Endpoint: https("siem.example.com")}, {Withheld: true}}},
			PeersWithheld: 2,
		},
	}
	return s
}

var lensMembers = []releaseMember{
	{Kind: "process", Ref: "rechnung", ArtifactVer: 3, Key: 41},
	{Kind: "process", Ref: "eingang", ArtifactVer: 1, Key: 42},
	{Kind: "decision", Ref: "rabatt", ArtifactVer: 1, Key: 43},
}

func TestTheLensDrawsEveryLayerTheReleaseRecords(t *testing.T) {
	g := releaseGraph(lensSnapshot(), lensMembers, "app-1", "Fakturierung", nil)

	kinds := map[string]string{}
	for _, n := range g.Nodes {
		if _, dup := kinds[n.ID]; dup {
			t.Errorf("node %s drawn twice", n.ID)
		}
		kinds[n.ID] = n.Kind
		if n.State != panorama.StateUnbound || n.Severity != panorama.SeverityUnknown {
			t.Errorf("%s is %s/%s without the overlay, want unbound and unknown", n.ID, n.State, n.Severity)
		}
		if n.Provenance != panorama.ProvenanceDerived {
			t.Errorf("%s has provenance %q", n.ID, n.Provenance)
		}
	}
	for id, kind := range map[string]string{
		"application:app-1": "application", "process:41": "process", "process:42": "process", "decision:rabatt": "decision",
		"message:auftrag": "message", "message:versandt": "message", "signal:abschluss": "signal",
		"worker:mailer": "worker", "worker:broker": "worker", "worker:siem": "worker", "worker:scanner": "worker",
		"unresolved:worker:= kanal": "unresolved", "jobtype:pdf-render": "jobType",
		"definition:mahnstufe": "definition", "definition:mahnlauf": "definition", "definition:bestellung": "definition",
		"application:app-mahn": "application", "application:app-shop": "application",
		"feed:events":                           "feed",
		"endpoint:https://smtp.example.com:443": "endpoint", "endpoint:https://nats.example.com:443": "endpoint",
		"endpoint:https://siem.example.com:443": "endpoint",
		"role:candidateGroup:buchhaltung":       "role", "catalog:cat-1": "catalog", "product:prod-1": "product",
		"capability:fakturierung": "capability", "valuestream:order-to-cash": "valueStream",
		"dataobject:rechnung:beleg": "dataObject", "datastore:Kundenstamm": "dataObject", "personaldata:rechnung": "dataObject",
		"businessobject:Kunde": "businessObject", "businessobject:Rechnung": "businessObject",
	} {
		if kinds[id] != kind {
			t.Errorf("node %s is %q, want %q", id, kinds[id], kind)
		}
	}
	// What the reader may not see: the withheld call target, the withheld signal peers
	// (one placeholder for the name and direction), and the withheld feed endpoint.
	if g.Restricted != 3 {
		t.Errorf("restricted = %d, want 3 placeholders", g.Restricted)
	}
	// An endpoint withheld or unreadable is no node.
	for id := range kinds {
		if strings.HasPrefix(id, "endpoint:") && !strings.Contains(id, ".example.com") {
			t.Errorf("an endpoint that is not an address is drawn: %s", id)
		}
	}

	edges := map[string]bool{}
	for _, e := range g.Edges {
		key := e.From + " " + e.Kind + " " + e.To
		if edges[key] {
			t.Errorf("edge %s drawn twice", key)
		}
		edges[key] = true
		if _, ok := kinds[e.From]; !ok {
			t.Errorf("edge %s starts at no node", key)
		}
		if _, ok := kinds[e.To]; !ok {
			t.Errorf("edge %s ends at no node", key)
		}
	}
	for _, want := range []string{
		"application:app-1 contains process:41", "application:app-1 contains decision:rabatt",
		"message:auftrag receives process:41", "process:41 throws message:versandt", "signal:abschluss receives process:42",
		"definition:bestellung throws message:auftrag", "message:versandt receives definition:mahnlauf",
		"restricted:2 throws signal:abschluss", "application:app-mahn contains definition:mahnlauf",
		"process:41 uses worker:mailer", "process:42 uses unresolved:worker:= kanal", "process:41 uses jobtype:pdf-render",
		"process:41 uses decision:rabatt", "process:41 calls definition:mahnstufe", "process:42 calls restricted:1",
		"application:app-1 throws feed:events", "feed:events uses worker:siem", "feed:events uses restricted:3",
		"worker:siem reaches endpoint:https://siem.example.com:443", "worker:mailer reaches endpoint:https://smtp.example.com:443",
		"worker:broker throws message:auftrag", "worker:broker reaches endpoint:https://nats.example.com:443",
		"role:candidateGroup:buchhaltung assigned process:41", "catalog:cat-1 offers product:prod-1", "process:41 realizes product:prod-1",
		"process:41 realizes capability:fakturierung", "worker:mailer realizes capability:fakturierung",
		"capability:fakturierung serves valuestream:order-to-cash",
		"process:41 accesses dataobject:rechnung:beleg", "dataobject:rechnung:beleg realizes businessobject:Rechnung",
		"process:42 accesses datastore:Kundenstamm", "datastore:Kundenstamm realizes businessobject:Kunde",
		"process:41 accesses personaldata:rechnung", "worker:mailer accesses personaldata:rechnung", "jobtype:pdf-render accesses personaldata:rechnung",
	} {
		if !edges[want] {
			t.Errorf("no edge %s", want)
		}
	}

	if g.Status.Unknown != len(g.Nodes) || g.Status.OK+g.Status.Attention+g.Status.Critical != 0 {
		t.Errorf("status = %+v, want every node unknown without the overlay", g.Status)
	}
	// The same snapshot draws the same graph, byte for byte.
	a, _ := json.Marshal(g)
	b, _ := json.Marshal(releaseGraph(lensSnapshot(), lensMembers, "app-1", "Fakturierung", nil))
	if string(a) != string(b) {
		t.Error("the same snapshot drew two different graphs")
	}
}

func TestTheLensDrawsOnlyTheLayersTheSnapshotRecords(t *testing.T) {
	s := lensSnapshot()
	s.Physical, s.Data, s.Business, s.Strategy, s.Coupling = nil, nil, nil, nil, nil
	g := releaseGraph(s, lensMembers, "app-1", "Fakturierung", nil)
	for _, n := range g.Nodes {
		switch n.Kind {
		case lensKindEndpoint, lensKindFeed, lensKindSignal, lensKindRole, lensKindCapability, lensKindValueStream,
			lensKindDataObject, lensKindBusinessObject, panorama.KindCatalog, panorama.KindProduct:
			t.Errorf("an older snapshot draws %s", n.ID)
		}
	}
	// A member recorded without its deployment key has nothing to open in Operations.
	g = releaseGraph(s, []releaseMember{{Kind: "process", Ref: "rechnung", ArtifactVer: 3}}, "app-1", "Fakturierung", nil)
	if !slices.ContainsFunc(g.Nodes, func(n panorama.Node) bool {
		return n.ID == "definition:rechnung:3" && n.Kind == panorama.KindDefinition
	}) {
		t.Error("a process without its key is not drawn as its definition")
	}
}

func TestTheOverlayLaysTheLiveStateOverTheRelease(t *testing.T) {
	live := &releaseLive{
		processes: map[uint64]liveState{
			41: {state: panorama.StateDegraded, reason: "2 token(s) are parked behind an unresolved incident.", incidents: 2, oldest: 7,
				sites: []panorama.IncidentSite{{ElementID: "Senden", Count: 2}}, runtime: &panorama.Runtime{Running: 3}},
			42: {state: panorama.StateHealthy, reason: "No work is parked in this process.", runtime: &panorama.Runtime{}},
		},
		workers: map[string]liveState{
			"mailer": {state: panorama.StateHealthy, reason: "The engine holds a usable client for this worker."},
			"siem":   {state: panorama.StateUnbound, reason: "Its state is shown to whoever may see this worker's configuration."},
		},
		partial: true,
	}
	g := releaseGraph(lensSnapshot(), lensMembers, "app-1", "Fakturierung", live)
	byID := map[string]panorama.Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	if p := byID["process:41"]; p.State != panorama.StateDegraded || p.Severity != panorama.SeverityAttention || p.Incidents != 2 ||
		p.OldestIncident != 7 || len(p.Sites) != 1 || p.Runtime == nil || p.Runtime.Running != 3 {
		t.Errorf("process:41 = %+v, want its parked work", p)
	}
	if a := byID["application:app-1"]; a.Severity != panorama.SeverityAttention || a.SeverityFrom != "process:41" {
		t.Errorf("the application = %+v, want the worst of its processes, attributed", a)
	}
	if w := byID["worker:mailer"]; w.State != panorama.StateHealthy {
		t.Errorf("worker:mailer = %+v", w)
	}
	if w := byID["worker:broker"]; w.State != panorama.StateNotReady || !strings.Contains(w.Reason, "No worker of this name is configured") {
		t.Errorf("worker:broker = %+v, want not ready: nothing of that name is configured", w)
	}
	if w := byID["worker:siem"]; w.State != panorama.StateUnbound || w.Severity != panorama.SeverityUnknown {
		t.Errorf("worker:siem = %+v, want its state withheld", w)
	}
	// Nothing else is observed: the overlay is the release's own processes and workers.
	if m := byID["message:auftrag"]; m.State != panorama.StateUnbound || byID["definition:mahnlauf"].State != panorama.StateUnbound {
		t.Error("the overlay observes something the release does not run")
	}
	if !g.Status.Partial {
		t.Error("a partial incident scan is not said")
	}
}

// TestTheOverlayReadsTheVersionTheReleaseShipped: a version no longer deployed has
// nothing running in it to observe, and a worker's state goes only to a reader who may
// see its configuration.
func TestTheOverlayReadsTheVersionTheReleaseShipped(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	if err := srv.connectors.Save(connector{ID: "w-1", Name: "mailer", Kind: "rest", Endpoint: "https://mail.example.com", Enabled: true, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	rel := &applicationRelease{Members: lensMembers}
	read := func(roles ...string) *releaseLive {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "usr_x", Roles: roles}))
		var (
			live *releaseLive
			err  error
		)
		srv.do(func() {
			srv.deployments[41] = &deployment{Key: 41, ProcessID: "rechnung", Version: 3}
			live, err = srv.releaseLive(req, rel)
		})
		if err != nil {
			t.Fatal(err)
		}
		return live
	}

	live := read(RoleAdmin)
	if p := live.processes[41]; p.state != panorama.StateHealthy || p.runtime == nil {
		t.Errorf("the deployed version = %+v, want healthy with its instance counts", p)
	}
	if p := live.processes[42]; p.state != panorama.StateUnbound || !strings.Contains(p.reason, "no longer deployed") {
		t.Errorf("the version gone since = %+v, want unbound and why", p)
	}
	if _, ok := live.processes[43]; ok {
		t.Error("a decision member is read as a process")
	}
	if w := live.workers["mailer"]; w.state == panorama.StateUnbound || w.reason == "" {
		t.Errorf("the administrator reads the worker as %+v, want its state", w)
	}
	if w := read(RoleModeler).workers["mailer"]; w.state != panorama.StateUnbound || !strings.Contains(w.reason, "may see this worker's configuration") {
		t.Errorf("a modeller reads the worker as %+v, want its state withheld", w)
	}
}
