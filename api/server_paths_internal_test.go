package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/promquery"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// Startup refusals, the half TestNewRefusesToStartOnAStoreItCannotOpen does not reach.
//
// That test occupies a store's directory with a file, which stops New at the store's
// constructor. Most of what New reads at startup is past that point: the records the
// stores already hold — the key floor, the deployments, the tokens, the node identity —
// are loaded into memory before the loop serves anything, and each of those loads ends
// in `if err != nil { return nil, err }`. A record that cannot be read there is the
// same startup property as a directory that cannot be opened: an operator watching the
// process start is told, rather than a server coming up with a peer's deploy token, a
// release counter or a definition silently missing.
//
// The faults are real shapes a data directory takes — a record cut short by a full disk
// or a bad restore, a key file holding something that is not a key — and none of them
// needs a permission trick, so they behave the same on every platform CI runs.

// newServerPathsAtDir is New over a data directory the caller has already arranged, with
// the engine and log a server needs and the options a case asks for.
func newServerPathsAtDir(t *testing.T, dir string, opts ...Option) (*Server, error) {
	t.Helper()
	lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	proc := engine.New(1, lg, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return New(proc, store, dir, opts...)
}

// serverWriteFile writes one file under the data directory, creating its parents.
func serverWriteFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// serverBlockWrite makes the next atomic write of one record fail while the record
// itself stays readable: a directory where the write's temp file has to go. Open
// refuses to create a file over a directory on every platform, so this is a write
// fault without a permission trick.
func serverBlockWrite(t *testing.T, recordPath string) {
	t.Helper()
	if err := os.MkdirAll(recordPath+".tmp", 0o755); err != nil {
		t.Fatalf("block write of %s: %v", recordPath, err)
	}
}

// serverHexName is the default sidecar filename for a key.
func serverHexName(key string) string { return hex.EncodeToString([]byte(key)) + ".json" }

// TestServerNewRefusesARecordItCannotLoad walks every startup load New performs after
// its stores are open. Each case leaves exactly one thing broken and expects New to
// refuse with a message that names it.
func TestServerNewRefusesARecordItCannotLoad(t *testing.T) {
	const cut = `{"id":` // a record cut short mid-write
	cases := []struct {
		name    string
		opts    []Option
		arrange func(t *testing.T, dir string)
		want    string // what the message must name for an operator to act on it
	}{
		{name: "vault key file", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, "vault.key", "this is not a key")
		}, want: "vault: key file"},
		{name: "vault directory", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, "vault", "not a directory")
		}, want: "vault: create dir"},
		{name: "node identity", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("settings", "node.json"), cut)
		}, want: "decode node"},
		{name: "system project", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("projects", serverHexName(systemProjectID)), cut)
		}, want: "projectstore"},
		{name: "configured worker", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("connectors", serverHexName("w1")), cut)
		}, want: "connectorstore"},
		{name: "definition key floor", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("keyspace", serverHexName(keySpaceMarkID)), cut)
		}, want: "definition key floor"},
		{name: "process deployment", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("deployments", "10.json"), cut)
		}, want: "deploystore"},
		{name: "decision deployment", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("decisions", "10.json"), cut)
		}, want: "decisionstore"},
		{name: "release", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("releases", serverHexName("r1")), cut)
		}, want: "releasestore"},
		{name: "process documentation", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("process-docs", "10.json"), cut)
		}, want: "processdocstore"},
		{name: "decision documentation", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("decision-docs", "10.json"), cut)
		}, want: "decisiondocstore"},
		{name: "api token", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("api-tokens", serverHexName("t1")), cut)
		}, want: "apitokenstore"},
		{name: "deploy token", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("deploy-tokens", serverHexName("t1")), cut)
		}, want: "deploytokenstore"},
		{name: "oauth client", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("oauth-clients", serverHexName("c1")), cut)
		}, want: "oauthclientstore"},
		{name: "call override", arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("call-overrides", serverHexName("child")), cut)
		}, want: "calloverridestore"},
		{name: "account under auth", opts: []Option{WithAuth()}, arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("users", serverHexName("usr_1")), cut)
		}, want: "userstore"},
		{
			// A readable account written before roles were enforced, whose upgrade
			// cannot be written back: the bootstrap reads it fine, so the refusal can
			// only come from the upgrade that rewrites it.
			name: "legacy role upgrade", opts: []Option{WithAuth()},
			arrange: func(t *testing.T, dir string) {
				rel := filepath.Join("users", serverHexName("usr_1"))
				serverWriteFile(t, dir, rel, `{"id":"usr_1","username":"ada","roles":["user"],"source":"local","createdAt":1}`)
				serverBlockWrite(t, filepath.Join(dir, rel))
			},
			want: "sidecar: open temp",
		},
		{name: "registration setting", opts: []Option{WithSystemProcesses()}, arrange: func(t *testing.T, dir string) {
			serverWriteFile(t, dir, filepath.Join("settings", "registration.json"), cut)
		}, want: "decode registration"},
		{
			// The first system process takes definition key 1 on an empty directory, so
			// blocking that one record's write is a failed bootstrap deploy.
			name: "system process deploy", opts: []Option{WithSystemProcesses()},
			arrange: func(t *testing.T, dir string) {
				serverBlockWrite(t, filepath.Join(dir, "deployments", "1.json"))
			},
			want: "deploy system process",
		},
		{
			// A corrupt high-water mark is a refusal, not a reset to genesis: starting
			// over would re-export everything the cluster already holds.
			name: "exporter position", opts: []Option{WithOpenSearchExporter(opensearch.Config{URL: "http://127.0.0.1:9"})},
			arrange: func(t *testing.T, dir string) {
				serverWriteFile(t, dir, filepath.Join("exporter", "opensearch.pos"), "not a position")
			},
			want: "opensearch: parse position",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The vault key comes from the environment when one is set there; these cases
			// are about the data directory, so take the environment out of the picture.
			t.Setenv(vault.KeyEnv, "")
			t.Setenv(vault.KeyFileEnv, "")
			dir := t.TempDir()
			tc.arrange(t, dir)
			srv, err := newServerPathsAtDir(t, dir, tc.opts...)
			if err == nil {
				srv.Close()
				t.Fatalf("New started with a broken %s: the load's error was dropped, so the server "+
					"would serve without what that record holds", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("New: %v, want the message to name %q", err, tc.want)
			}
		})
	}
}

// serverStoreDirsUnlisted are the data-directory subdirectories New opens a store in
// that serverStoreDirs does not list — the stores added to New after that list was
// written, which is the gap its own comment predicts.
var serverStoreDirsUnlisted = []string{
	"catalog", "orders", "task-folders", "capabilities", "value-streams", "favourites",
	"directory-sync", "inventory-loads", "discrepancies", "recertification-campaigns",
	"recertification-rows",
}

// TestServerNewRefusesTheStoresTheFirstListMissed is TestNewRefusesToStartOnAStoreItCannotOpen
// for the stores its list does not name: each is a store an unusable path stops the
// server over, with a message naming the path an operator has to fix.
func TestServerNewRefusesTheStoresTheFirstListMissed(t *testing.T) {
	for _, sub := range serverStoreDirsUnlisted {
		t.Run(sub, func(t *testing.T) {
			dir := t.TempDir()
			serverWriteFile(t, dir, sub, "not a directory")
			srv, err := newServerPathsAtDir(t, dir)
			if err == nil {
				srv.Close()
				t.Fatalf("New started with %q occupied by a file: the store's error was dropped", sub)
			}
			if !strings.Contains(err.Error(), sub) {
				t.Errorf("New: %v, want the message to name %q", err, sub)
			}
		})
	}
}

// TestServerMCPTransportStripsAForgedMarkerWithoutAuth: the transport marker tells the
// boundary that a request came through the MCP adapter, and it is only ever a value
// this server stamped. With authentication off there is no secret to stamp, so a
// marker the caller sent is removed rather than passed on — otherwise "no secret" and
// "matched" would read the same to everything downstream.
func TestServerMCPTransportStripsAForgedMarkerWithoutAuth(t *testing.T) {
	var seen []string
	adapter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get(mcpTransportHeader))
		w.WriteHeader(http.StatusNoContent)
	})
	srv, _ := newValidateServer(t, WithMCP(adapter))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set(mcpTransportHeader, "forged")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || len(seen) != 1 {
		t.Fatalf("POST /mcp: status=%d, adapter reached %d time(s); want it reached once", rec.Code, len(seen))
	}
	if seen[0] != "" {
		t.Errorf("the adapter saw the caller's marker %q; with no internal token it must be removed", seen[0])
	}
}

// TestServerMetricsQueryOptionWiresTheStore: without a metrics store Panorama's
// historical context answers "not configured" and asks nothing. WithMetricsQuery is what
// changes that answer — for a value that names no node the store is now consulted far
// enough to say the value cannot be identified, which is a different fact from there
// being nowhere to look.
func TestServerMetricsQueryOptionWiresTheStore(t *testing.T) {
	target := contextTarget{metricReason: "this value names no node"}
	plain, _ := newValidateServer(t)
	if got := plain.metricContext(context.Background(), target); got.State != panorama.ContextNotConfigured {
		t.Fatalf("without the option the metrics context is %q, want %q", got.State, panorama.ContextNotConfigured)
	}
	wired, _ := newValidateServer(t, WithMetricsQuery(promquery.Config{URL: "http://metrics.invalid"}))
	got := wired.metricContext(context.Background(), target)
	if got.State != panorama.ContextUnidentifiable || got.Reason != target.metricReason {
		t.Errorf("with the option the metrics context is %q (%q), want %q with the target's reason",
			got.State, got.Reason, panorama.ContextUnidentifiable)
	}
}

// TestServerPlaygroundModelRefusesAnUnknownSource: the Playground service filters the
// source kinds it sends, but the server's resolver is the one that answers with a model
// or a status, and a zero status means "here is your model". An unknown kind must come
// back as a refusal, never as a nil model with nothing said.
func TestServerPlaygroundModelRefusesAnUnknownSource(t *testing.T) {
	srv, _ := newValidateServer(t)
	xml, status, msg := srv.playgroundModel(httptest.NewRequest(http.MethodPost, "/", nil), "archive", "x")
	if xml != nil || status != http.StatusBadRequest || !strings.Contains(msg, "unknown model source") {
		t.Errorf("playgroundModel(archive) = (%q, %d, %q), want a 400 naming the unknown source", xml, status, msg)
	}
}

// TestServerRetentionDefinitionTTLLongerThanTheGlobalAge: a definition's history TTL
// overrides the server-wide max age in both directions (ADR-0144). The existing test
// covers a TTL shorter than the age; here the TTL is the longer one, and the age must
// not reach past it to purge the definition's instances early, while it still purges
// the instances of a definition that declares nothing.
func TestServerRetentionDefinitionTTLLongerThanTheGlobalAge(t *testing.T) {
	const t0 = int64(10 * time.Hour)
	const globalAge = 10 * time.Minute
	const ttl = 30 * time.Minute // ttlRetentionBPMN's PT30M
	h := newRetentionHarness(t, t0, WithRetention(globalAge))
	ttlKey := h.deployXML(ttlRetentionBPMN)
	plainKey := h.deployXML(retentionBPMN)
	withTTL := h.createParkedOf(ttlKey)
	withoutTTL := h.createParkedOf(plainKey)
	h.terminate(withTTL)
	h.terminate(withoutTTL)

	h.clk.set(t0 + int64(globalAge))
	h.sweep()
	if h.has(withoutTTL) {
		t.Error("the server-wide age did not purge the instance of a definition without a TTL")
	}
	if !h.has(withTTL) {
		t.Fatal("the shorter server-wide age purged an instance whose definition keeps it for longer")
	}

	h.clk.set(t0 + int64(ttl))
	h.sweep()
	if h.has(withTTL) {
		t.Error("the instance outlived its definition's history TTL")
	}
}

// TestServerRestartWithoutAKeyFloorKeepsADecisionsKey: an installation that predates
// the durable key floor (ADR-0339) restarts with nothing but its records to say which
// definition keys are taken. When the only record is a decision deployment, its key
// still has to be counted — otherwise the next process deploy is handed the same key,
// and two definitions answer to one number.
func TestServerRestartWithoutAKeyFloorKeepsADecisionsKey(t *testing.T) {
	dir := t.TempDir()
	boot := func() (*Server, func()) {
		lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
		if err != nil {
			t.Fatalf("wal.Open: %v", err)
		}
		store, err := state.Open(filepath.Join(dir, "state"))
		if err != nil {
			t.Fatalf("state.Open: %v", err)
		}
		proc := engine.New(1, lg, store, nil)
		if err := proc.Recover(); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		srv, err := New(proc, store, dir)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return srv, func() { srv.Close(); _ = store.Close(); _ = lg.Close() }
	}

	first, stop := boot()
	code, body := deployTestHarness{t, first.Handler()}.do(http.MethodPost, "/api/v1/decision-deployments", validDMNModel)
	if code != http.StatusOK && code != http.StatusCreated {
		stop()
		t.Fatalf("deploy decision: status=%d body=%s", code, body)
	}
	var dec struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dec); err != nil || dec.Key == 0 {
		stop()
		t.Fatalf("decode decision deployment: %v (%s)", err, body)
	}
	stop()

	// The installation before the floor existed: no key-space record at all.
	if err := os.RemoveAll(filepath.Join(dir, "keyspace")); err != nil {
		t.Fatalf("remove key floor: %v", err)
	}
	second, stop := boot()
	defer stop()
	code, body = deployTestHarness{t, second.Handler()}.do(http.MethodPost, "/api/v1/deployments", serverPlainBPMN)
	if code != http.StatusOK {
		t.Fatalf("deploy process: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if dep.Key <= dec.Key {
		t.Errorf("the process was given key %d after a restart, but the decision deployment holds %d", dep.Key, dec.Key)
	}
}

// serverPlainBPMN is a one-flow process for the tests in this file.
const serverPlainBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="plain" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// serverInProcessModel runs one service task carrying a Worker Type's element and then
// parks at a user task, so the task's result can be read off the instance.
func serverInProcessModel(element string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs-inproc">
  <bpmn:process id="inproc" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>%s</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:userTask id="park"/>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="park"/>
    <bpmn:sequenceFlow id="f3" sourceRef="park" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`, element)
}

// TestServerRunsWorkerTypesInProcessByDefault: with nothing offloaded, New registers an
// in-process worker for each Worker Type that can run beside the engine, so a task of
// that type is worked as the instance reaches it and its result lands in the instance —
// no external worker involved. Each case points the task at a fake endpoint (or, for
// LDIF, at nothing: it is a pure transform) and reads the result variable back.
func TestServerRunsWorkerTypesInProcessByDefault(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/scim/"):
			w.Header().Set("Content-Type", "application/scim+json")
			_, _ = w.Write([]byte(`{"totalResults":1,"Resources":[{"userName":"ada-scim"}]}`))
		case r.URL.Path == "/soap":
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">` +
				`<soapenv:Body><GetRateResponse><rate>rate-from-soap</rate></GetRateResponse></soapenv:Body></soapenv:Envelope>`))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><p class="price">price-from-page</p></body></html>`))
		}
	}))
	defer endpoint.Close()

	for _, tc := range []struct {
		name, element, vars, result, want string
	}{
		{"ldif", `<atlas:ldifConnector format="ldif" source="ldifText" resultVariable="entries"/>`,
			`{"variables":{"ldifText":"dn: cn=ada-ldif,dc=example,dc=com\ncn: ada-ldif\n"}}`, "entries", "ada-ldif"},
		{"webscrape", `<atlas:webscrapeConnector url="` + endpoint.URL + `/page" selector=".price" resultVariable="hits"/>`,
			`{}`, "hits", "price-from-page"},
		{"soap", `<atlas:soapConnector endpoint="` + endpoint.URL + `/soap" operation="GetRate" body="&lt;GetRate/&gt;" resultVariable="kurs"/>`,
			`{}`, "kurs", "rate-from-soap"},
		{"scim", `<atlas:scimConnector baseUrl="` + endpoint.URL + `/scim/v2" resource="Users" operation="search" filter="userName eq &quot;ada&quot;" resultVariable="konten"/>`,
			`{}`, "konten", "ada-scim"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t)
			x := deployTestHarness{t, srv.Handler()}
			code, body := x.do(http.MethodPost, "/api/v1/deployments", serverInProcessModel(tc.element))
			if code != http.StatusOK {
				t.Fatalf("deploy: status=%d body=%s", code, body)
			}
			code, body = x.do(http.MethodPost, "/api/v1/processes/1/instances", tc.vars)
			if code != http.StatusOK {
				t.Fatalf("start: status=%d body=%s", code, body)
			}
			var started struct {
				InstanceKey uint64 `json:"instanceKey"`
			}
			if err := json.Unmarshal(body, &started); err != nil || started.InstanceKey == 0 {
				t.Fatalf("decode start: %v (%s)", err, body)
			}
			code, body = x.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", started.InstanceKey), "")
			if code != http.StatusOK {
				t.Fatalf("variables: status=%d body=%s", code, body)
			}
			var vars map[string]any
			if err := json.Unmarshal(body, &vars); err != nil {
				t.Fatalf("decode variables: %v (%s)", err, body)
			}
			got, ok := vars[tc.result]
			if !ok {
				t.Fatalf("no %q after the task ran in process; variables=%s", tc.result, body)
			}
			if raw, _ := json.Marshal(got); !strings.Contains(string(raw), tc.want) {
				t.Errorf("%s = %s, want it to carry %q", tc.result, raw, tc.want)
			}
		})
	}
}

// TestServerInProcessDirectoryTasksFailIntoAnIncident: the two directory Worker Types
// are served in process as well when nothing is offloaded. A directory that cannot be
// reached is therefore the server's own failure to report — an incident naming the dial
// — and not a job left waiting for an external worker nobody runs.
func TestServerInProcessDirectoryTasksFailIntoAnIncident(t *testing.T) {
	// A port that was just listened on and released: nothing answers there.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closed := "ldap://" + l.Addr().String()
	_ = l.Close()

	for _, tc := range []struct{ name, element, want string }{
		{"ldap", `<atlas:ldapConnector url="` + closed + `" operation="search" baseDN="ou=people,dc=example,dc=com" scope="sub" filter="(uid=ada)" resultVariable="treffer"/>`, "ldap: dial"},
		{"ad", `<atlas:adConnector url="` + closed + `" operation="disable" dn="cn=Arno,dc=example,dc=com"/>`, "ad: dial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t)
			x := deployTestHarness{t, srv.Handler()}
			if code, body := x.do(http.MethodPost, "/api/v1/deployments", serverInProcessModel(tc.element)); code != http.StatusOK {
				t.Fatalf("deploy: status=%d body=%s", code, body)
			}
			if code, body := x.do(http.MethodPost, "/api/v1/processes/1/instances", `{}`); code != http.StatusOK {
				t.Fatalf("start: status=%d body=%s", code, body)
			}
			code, body := x.do(http.MethodGet, "/api/v1/incidents", "")
			if code != http.StatusOK {
				t.Fatalf("incidents: status=%d body=%s", code, body)
			}
			var page struct {
				Items []struct {
					ElementID string `json:"elementId"`
					Message   string `json:"message"`
				} `json:"items"`
			}
			if err := json.Unmarshal(body, &page); err != nil {
				t.Fatalf("decode incidents: %v (%s)", err, body)
			}
			if len(page.Items) != 1 || page.Items[0].ElementID != "t" || !strings.Contains(page.Items[0].Message, tc.want) {
				t.Errorf("incidents = %s, want one on the task naming %q", body, tc.want)
			}
		})
	}
}
