package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/worker"
)

// The wikiwikibangbang Worker Type is worker-only, and the reason a supervised worker
// can serve it at all is that the engine hands its wikis over: a task names its Worker
// and nothing more, and the endpoint and the bundle with the token are a Worker record
// and a vault secret, which a supervised worker cannot read for itself.

// wikiBundle is a well-formed bundle, the shape an operator stores in the vault under a
// record's credentialsRef.
const wikiBundle = `{"token":"wwbb_db59d873df35653ce48f6b334cd44ea22e23f1b06c3f69fc0bfe3b7eefc60164"}`

func saveWikiWorker(t *testing.T, srv *Server, id, name, endpoint, ref string) {
	t.Helper()
	if err := srv.connectors.Save(connector{
		ID: id, Name: name, Kind: connectorKindWiki,
		Endpoint: endpoint, CredentialsRef: ref, Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("Save %s: %v", name, err)
	}
}

// What the engine renders is what a worker builds its clients from: the rendered
// environment is handed to worker.BuiltinConnectors, so a variable named differently on
// either side fails here rather than in a parked job.
func TestSupervisedWikiEnvUsesTheWorkersOwnNames(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveWikiWorker(t, srv, "1", "wiki", "https://wiki.example.ch", "wiki-bot")
	saveWikiWorker(t, srv, "2", "handbuch", "https://handbuch.example.ch/wiki", "wiki-handbuch")
	t.Setenv("ATLAS_CONNECTOR_WIKI_BOT_TOKEN", wikiBundle)
	t.Setenv("ATLAS_CONNECTOR_WIKI_HANDBUCH_TOKEN", `{"token":"wwbb_other-token-value"}`)

	env := envOf(t, srv.wikiWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindWiki)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	if !slices.Contains(built.Names, "wiki") || !slices.Contains(built.Names, "handbuch") {
		t.Errorf("the worker holds %v, want both wikis", built.Names)
	}
	if got := env["ATLAS_WIKIWIKIBANGBANG_WIKI_URL"]; got != "https://wiki.example.ch" {
		t.Errorf("the handed endpoint = %q", got)
	}
	if got := env["ATLAS_WIKIWIKIBANGBANG_WIKI_CREDENTIALS"]; got != wikiBundle {
		t.Errorf("the handed bundle = %q, want it whole", got)
	}
}

// A wiki whose endpoint or bundle does not build is left out rather than handed over:
// the worker refuses at startup on a named wiki it cannot build, which would take down
// every other one it serves.
func TestSupervisedWikiEnvSkipsAnUnusableWorker(t *testing.T) {
	for _, tc := range []struct{ name, endpoint, secret string }{
		{"never set", "https://wiki.example.ch", ""},
		{"malformed", "https://wiki.example.ch", "{"},
		{"no token", "https://wiki.example.ch", `{"token":""}`},
		{"another kind's bundle", "https://wiki.example.ch", `{"username":"u","password":"p"}`},
		{"plain http to another host", "http://wiki.example.ch", wikiBundle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
			saveWikiWorker(t, srv, "1", "wiki", tc.endpoint, "wiki")
			if tc.secret != "" {
				t.Setenv("ATLAS_CONNECTOR_WIKI_TOKEN", tc.secret)
			}
			if env := srv.wikiWorkerEnv(); env != nil {
				t.Errorf("rendered %v for a wiki that cannot be used; want nothing", env)
			}
		})
	}
}

func TestSupervisedWikiEnvSkipsAFoldingCollisionAndKeepsTheHosts(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	saveWikiWorker(t, srv, "1", "wiki-eu", "https://wiki.example.ch", "wiki")
	saveWikiWorker(t, srv, "2", "wiki_eu", "https://wiki.example.ch", "wiki")
	t.Setenv("ATLAS_CONNECTOR_WIKI_TOKEN", wikiBundle)
	t.Setenv("ATLAS_WIKIWIKIBANGBANG_CONNECTORS", "host-wiki")

	env := envOf(t, srv.wikiWorkerEnv())
	if got := env["ATLAS_WIKIWIKIBANGBANG_CONNECTORS"]; got != "host-wiki,wiki-eu" {
		t.Errorf("ATLAS_WIKIWIKIBANGBANG_CONNECTORS = %q, want the host's wiki and only the first of two names that fold together", got)
	}
}

// The intent behind the default, pinned apart from the mechanism: the kind is supervised
// from the first boot, parked until a wiki is added, and its endpoint and bundle are
// handed over once one is.
func TestWikiIsSupervisedByDefault(t *testing.T) {
	if !slices.Contains(DefaultSupervisedWorkerOnlyKinds(), connectorKindWiki) {
		t.Error("wikiwikibangbang is not supervised by default: adding a wiki in the Console would need a deployment change")
	}
	if _, provisioned := (&Server{}).provisionedConnectorKinds()[connectorKindWiki]; !provisioned {
		t.Error("wikiwikibangbang is supervised but its bundle is not handed over")
	}
}

// A record without an endpoint or a bundle is refused where the operator is looking.
func TestValidateWikiWorker(t *testing.T) {
	p := createConnectorParams{CredentialsRef: "wiki"}
	if msg := validateWikiConnector(&p); !strings.Contains(msg, "endpoint") {
		t.Errorf("validate = %q", msg)
	}
	p = createConnectorParams{Endpoint: "https://wiki.example.ch"}
	if msg := validateWikiConnector(&p); !strings.Contains(msg, "credentialsRef") {
		t.Errorf("validate = %q", msg)
	}
	p = createConnectorParams{Endpoint: "https://wiki.example.ch", CredentialsRef: "wiki", Provider: "x", Sender: "y", Model: "z"}
	if msg := validateWikiConnector(&p); msg != "" {
		t.Errorf("validate = %q", msg)
	}
	if p.Provider != "" || p.Sender != "" || p.Model != "" {
		t.Errorf("fields that do not apply were kept: %+v", p)
	}
}

// wikiAppendBPMN is a process whose one task appends a line to a page.
const wikiAppendBPMN = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs-wiki">
  <bpmn:process id="wiki-log" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:wikiwikibangbangConnector connector="wiki" operation="append-to-page" item="=seite" text="=zeile"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// The worker is told whether a job is being delivered again: on the first lease it is
// not, so an append writes without looking; once the lease elapsed and the job is
// leased anew, it is, so the append first checks whether its earlier round wrote.
func TestTheWikiPayloadSaysWhetherTheJobIsDeliveredAgain(t *testing.T) {
	srv, clk := jobPullSrvClockedWith(t, wikiAppendBPMN, `{"seite":"OPS-12","zeile":"Schritt 1 erledigt."}`)
	pullBody := fmt.Sprintf(`{"type":%q,"worker":"wiki-worker","leaseMs":60000}`, compiler.WikiwikibangbangJobType)

	code, first := pull(t, srv, pullBody)
	if code != http.StatusOK || len(first.Jobs) != 1 || first.Jobs[0].Connector == nil {
		t.Fatalf("first pull: status=%d jobs=%+v", code, first.Jobs)
	}
	fields := first.Jobs[0].Connector.Fields
	if first.Jobs[0].Connector.Kind != "wikiwikibangbang" || fields["item"] != "OPS-12" || fields["text"] != "Schritt 1 erledigt." {
		t.Errorf("payload = %s %v", first.Jobs[0].Connector.Kind, fields)
	}
	if fields["redelivered"] != false {
		t.Errorf("a first lease says redelivered = %v", fields["redelivered"])
	}
	for _, secret := range []string{"token", "endpoint", "credentialsRef"} {
		if _, ok := fields[secret]; ok {
			t.Errorf("the payload carries %q; a wiki's address and token stay on the worker", secret)
		}
	}

	expireLease(t, srv, clk)
	code, second := pull(t, srv, pullBody)
	if code != http.StatusOK || len(second.Jobs) != 1 || second.Jobs[0].Connector == nil {
		t.Fatalf("second pull: status=%d jobs=%+v", code, second.Jobs)
	}
	if second.Jobs[0].Connector.Fields["redelivered"] != true {
		t.Errorf("a job leased again says redelivered = %v", second.Jobs[0].Connector.Fields["redelivered"])
	}
}
