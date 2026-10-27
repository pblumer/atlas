package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
)

// TestTheSnapshotSaysWhatItCouldNotRead covers the members a publish does not produce
// in an ordinary round but a snapshot still has to answer for: a decision member, a
// process member whose compiled form is not there, a local decision, and a call into
// another process of the same application that this release did not ship (ADR-0471 §1).
func TestTheSnapshotSaysWhatItCouldNotRead(t *testing.T) {
	srv := newServerWithOptions(t)

	b := compiler.NewBuilder(1, "rechnung", 1)
	start := b.AddStartEvent()
	rule, err := b.AddBusinessRuleTask("rabatt", nil, 3)
	if err != nil {
		t.Fatalf("AddBusinessRuleTask: %v", err)
	}
	b.SetElementBpmnId(rule, "Rule_Rabatt")
	call := b.AddCallActivity("nachbar", compiler.BindingLatest, false, false)
	b.SetElementBpmnId(call, "Call_Nachbar")
	end := b.AddEndEvent()
	b.Connect(start, rule)
	b.Connect(rule, call)
	b.Connect(call, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var snap *archsketch.Snapshot
	srv.do(func() {
		// The process this release calls, deployed earlier by the same application.
		srv.deployments[901] = &deployment{Key: 901, ProcessID: "nachbar", Version: 1, ProjectID: "app-1"}
		srv.versions["nachbar"] = 1
		srv.order = append(srv.order, 901)
		srv.deployments[902] = &deployment{Key: 902, ProcessID: "rechnung", Name: "Rechnung", Version: 1, ProjectID: "app-1", cp: cp}

		snap = srv.architectureSnapshot("app-1", []releaseMember{
			{Kind: "process", Ref: "rechnung", ArtifactVer: 1, Key: 902},
			{Kind: "process", Ref: "verloren", ArtifactVer: 2, Key: 999},
			{Kind: "decision", Ref: "rabatt", ArtifactVer: 1, Key: 903},
		}, 1_792_000_000)
	})

	got := *snap
	if len(got.Processes) != 2 {
		t.Fatalf("processes = %+v, want the two process members and not the decision", got.Processes)
	}
	if p := got.Processes[1]; p.ProcessID != "verloren" || !p.Unread {
		t.Errorf("missing member = %+v, want it recorded as unread", p)
	}
	if p := got.Processes[0]; p.Name != "Rechnung" || p.Unread {
		t.Errorf("read member = %+v", p)
	}
	if len(got.Dependencies) != 1 {
		t.Fatalf("dependencies = %+v, want only the local decision — a call within the application is not a way out", got.Dependencies)
	}
	d := got.Dependencies[0]
	if d.Kind != "decision" || d.Name != "rabatt" || len(d.UsedBy) != 1 || d.UsedBy[0].ElementID != "Rule_Rabatt" {
		t.Errorf("decision = %+v, want rabatt used at Rule_Rabatt", d)
	}
	if got.UnreadProcesses() != 1 || got.LocalDecisions() != 1 {
		t.Errorf("unread=%d decisions=%d, want 1 and 1", got.UnreadProcesses(), got.LocalDecisions())
	}
}

// TestAReadJudgesAWorkerThatIsGoneAsOwnerless: an endpoint recorded for a worker that
// has since been deleted was somebody's configuration, and only an administrator may
// still see it; an instance that gave no id is named as unnamed rather than as nothing.
func TestAReadJudgesAWorkerThatIsGoneAsOwnerless(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	snap := &archsketch.Snapshot{Physical: &archsketch.Physical{Workers: []archsketch.WorkerSite{
		{Name: "weg", WorkerType: "rest", Placement: archsketch.PlacementWorker, ConfiguredID: "geloescht",
			Endpoint: &archsketch.Endpoint{Scheme: "https", Host: "alt.example.com"}},
		{Name: "eigen", WorkerType: "rest", Placement: archsketch.PlacementWorker},
	}}}
	read := func(roles ...string) archsketch.Physical {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "usr_x", Roles: roles}))
		var out *archsketch.Snapshot
		srv.do(func() { out = srv.redactedArchitecture(req, snap) })
		return *out.Physical
	}
	if got := read(RoleModeler).Workers[0].Endpoint; got == nil || !got.Withheld {
		t.Errorf("a modeller reads %+v for a deleted worker, want it withheld", got)
	}
	if got := read(RoleAdmin).Workers[0].Endpoint; got == nil || got.Host != "alt.example.com" {
		t.Errorf("an administrator reads %+v, want the recorded endpoint", got)
	}
	if snap.Physical.Workers[0].Endpoint.Withheld {
		t.Error("the stored snapshot was changed by a read")
	}

	var names []string
	srv.do(func() {
		srv.workers.holdsConnectors("", []string{"eigen"}, nil)
		names = srv.workerInstances(func(st *workerStat) bool { return slices.Contains(st.Connectors, "eigen") })
	})
	if len(names) != 1 || names[0] != "an unnamed worker" {
		t.Errorf("instances = %v, want the one that gave no id, named as such", names)
	}
}

// TestAReportedEndpointIsInputNotAPromise: what a poll reports is reduced again,
// kept only for the names the same poll reported, refused when it is no address or
// absurdly long, and bounded in number.
func TestAReportedEndpointIsInputNotAPromise(t *testing.T) {
	got := reportedEndpoints([]string{"a", "b", "c", "d"}, map[string]string{
		"a":      "https://u:p@a.example.com:8443/x?y",
		"b":      "not an address",
		"c":      "https://" + strings.Repeat("c", maxReportedEndpointLen) + ".example.com",
		"absent": "https://absent.example.com",
	})
	if len(got) != 1 || got["a"] != "https://a.example.com:8443" {
		t.Errorf("reportedEndpoints = %v, want only a, reduced", got)
	}
	if reportedEndpoints([]string{"a"}, nil) != nil {
		t.Error("no report should keep nothing")
	}

	many := make([]string, 0, maxReportedEndpoints+10)
	endpoints := map[string]string{}
	for i := range maxReportedEndpoints + 10 {
		name := fmt.Sprintf("w%d", i)
		many = append(many, name)
		endpoints[name] = "relay.example.com:25"
	}
	if got := reportedEndpoints(many, endpoints); len(got) != maxReportedEndpoints {
		t.Errorf("kept %d endpoints, want the bound %d", len(got), maxReportedEndpoints)
	}

	r := newWorkerRegistry(nil)
	r.holdsConnectors("w", []string{"a"}, map[string]string{"a": "a.example.com", "b": "b.example.com"})
	if st := r.byName["w"]; len(st.Endpoints) != 1 || st.Endpoints["a"] != "a.example.com" {
		t.Errorf("registry endpoints = %v, want only the held name's", st.Endpoints)
	}
	r.holdsConnectors("w", []string{"a"}, nil)
	if st := r.byName["w"]; st.Endpoints != nil {
		t.Errorf("a later poll reporting no endpoint left %v behind", st.Endpoints)
	}
}

// TestTheRestOfAWorkersConfigurationIsRecordedAndNoSecret: a release records what else
// the configured Worker says (ADR-0495 slice B) — the transport, the scanner, the
// model, the identity, and that a credential exists — and never the credential's name.
func TestTheRestOfAWorkersConfigurationIsRecordedAndNoSecret(t *testing.T) {
	srv := newServerWithOptions(t)
	rec := connector{ID: "w-mail", Name: "smtp-main", Kind: "mail", Endpoint: "smtp://relay.example.com:587",
		CredentialsRef: "mail-relay-password", Provider: "smtp", Scanner: "clamd://av.example.com:3310",
		Model: "gpt-4o-mini", Enabled: true,
		Identity: &workerIdentity{Mode: "delegated-per-job", ActAs: actAsInitiator, Enforcement: enforcementShadow}}
	if err := srv.connectors.Save(rec); err != nil {
		t.Fatal(err)
	}
	var p *archsketch.Physical
	srv.do(func() {
		p = srv.physicalPart(map[string]bool{}, map[string]bool{},
			[]archsketch.Dependency{{Kind: archsketch.DependencyWorker, Name: "smtp-main", WorkerType: "mail"}},
			map[[2]string]string{{"smtp-main", "mail"}: archsketch.PlacementEngine})
	})
	if p == nil || len(p.Workers) != 1 {
		t.Fatalf("physical part = %+v", p)
	}
	w := p.Workers[0]
	if w.Provider != "smtp" || w.Model != "gpt-4o-mini" || !w.Credential ||
		w.ScannerEndpoint == nil || w.ScannerEndpoint.Host != "av.example.com" ||
		w.Identity == nil || w.Identity.Mode != "delegated-per-job" || w.Identity.Enforcement != "shadow" {
		t.Errorf("worker = %+v, want its configuration beyond the endpoint", w)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "mail-relay-password") {
		t.Errorf("the snapshot names the credential: %s", raw)
	}
}

// TestAnInboundSubscriptionSaysWhatItWatchesInItsWorkersTerms covers each kind of
// watch an inbound subscription can hold.
func TestAnInboundSubscriptionSaysWhatItWatchesInItsWorkersTerms(t *testing.T) {
	for _, tc := range []struct {
		sub  inboundSubscription
		want string
	}{
		{inboundSubscription{WatchedSubject: "orders/new"}, "orders/new"},
		{inboundSubscription{JQL: "project = OPS"}, "JQL project = OPS"},
		{inboundSubscription{SpreadsheetID: "sheet-1", WatchRange: "A1:D"}, "spreadsheet sheet-1 A1:D"},
		{inboundSubscription{SpreadsheetID: "sheet-1"}, "spreadsheet sheet-1"},
		{inboundSubscription{FolderID: "f-1"}, "folder f-1"},
		{inboundSubscription{ChannelID: "c-1"}, "channel c-1"},
		{inboundSubscription{MailFolder: "INBOX"}, "mail folder INBOX"},
		{inboundSubscription{}, ""},
	} {
		if got := inboundWatched(tc.sub); got != tc.want {
			t.Errorf("inboundWatched(%+v) = %q, want %q", tc.sub, got, tc.want)
		}
	}
}

// TestALayerAnOlderReleaseDoesNotRecordIsRefusedInWords: a release taken before a layer
// existed answers a read of that layer alone with a 404 that names the layers it does
// record, rather than an empty picture.
func TestALayerAnOlderReleaseDoesNotRecordIsRefusedInWords(t *testing.T) {
	srv := newServerWithOptions(t)
	older := &archsketch.Snapshot{FormatVersion: 4, MappingVersion: 4, Processes: []archsketch.Process{},
		EntryPoints: []archsketch.EntryPoint{}, Dependencies: []archsketch.Dependency{}, Physical: &archsketch.Physical{}}
	var err error
	srv.do(func() {
		if err = srv.projects.Save(project{ID: "alt", Name: "Alt"}); err == nil {
			err = srv.releases.Save(applicationRelease{ID: "rel-alt", ApplicationID: "alt", Version: 1, Architecture: older})
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/applications/alt/releases/1/architecture.svg?layers=strategy,data")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotFound ||
		!strings.Contains(string(body), "records none of the layers asked for (strategy,data); it records application,technology") {
		t.Errorf("a layer the release does not record answers %d %s, want 404 saying what it records", resp.StatusCode, body)
	}
}

// TestALifecycleProductIsRecordedWithItsActions: a product whose lifecycle process the
// release ships is recorded with each action, its message and the event type each
// outcome publishes; one whose process the release does not ship is not the release's.
func TestALifecycleProductIsRecordedWithItsActions(t *testing.T) {
	it := catalog.Item{ID: "laptop", HomeCatalog: "kat", State: "active", LifecycleProcess: "laptop-lifecycle",
		Actions: []catalog.Action{
			{Key: "provision", Message: "laptop.provision", Effect: catalog.EffectProvision},
			{Key: "reset", Message: "laptop.reset", Effect: catalog.EffectService, Triggers: []string{catalog.TriggerOperator},
				Outcomes: map[string]string{catalog.OutcomeCompleted: "laptop.zurueckgesetzt"}},
		}}
	p, ok := releaseProduct(it, map[string]bool{"laptop-lifecycle": true})
	if !ok || len(p.Bindings) != 1 || p.Bindings[0].Op != "lifecycle" || len(p.Actions) != 2 {
		t.Fatalf("product = %+v (ok=%v), want the lifecycle binding and both actions", p, ok)
	}
	if got := p.Actions[1].Outcomes[0]; got.Outcome != catalog.OutcomeCompleted || got.EventType != "laptop.zurueckgesetzt" {
		t.Errorf("reset completed = %+v, want the declared event type", got)
	}
	if _, ok := releaseProduct(it, map[string]bool{"anders": true}); ok {
		t.Error("a product whose lifecycle process the release does not ship is recorded")
	}
	if _, ok := releaseProduct(catalog.Item{ID: "x", ProvisionProcess: "p"}, map[string]bool{"q": true}); ok {
		t.Error("a two-process product binding no release process is recorded")
	}
}
