package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/connector/jira"
	"github.com/pblumer/atlas/connector/ldap"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/connector/remedy"
	"github.com/pblumer/atlas/connector/rest"
	"github.com/pblumer/atlas/connector/scim"
	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/connector/soap"
	"github.com/pblumer/atlas/connector/sqldb"
	"github.com/pblumer/atlas/connector/webscrape"
)

// The engine→worker payload contract (ADR-0168): when a worker leases a worker
// job, the engine has already resolved the task's authored detail against the
// instance's variables, and only the resulting *values* travel. The credential and
// the endpoint stay with the worker.
//
// resolveConnectorTask is one switch with an arm per Worker Type, and each arm
// names the exact field set its kind puts on the wire. That field set is a contract
// with the worker on the far side: rename a key here and the worker reads a zero
// value, with nothing failing at compile time on either side. Only mail and csv had
// a test that leased a real job and looked at the payload, so the other arms could
// have been renamed silently.
//
// These pin one lease per kind: the payload arrives, it carries the kind's own name,
// and the fields a worker dereferences are populated from what the model authored.

// connectorTaskModel is the smallest model that puts one leasable worker job in
// the queue: a start event, one service task carrying the authored element, an end.
func connectorTaskModel(procID, element string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs-%s">
  <bpmn:process id="%s" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>%s</bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`, procID, procID, element)
}

// leaseConnectorPayload deploys a one-task model, starts an instance of it, and
// leases the job as an external worker would — returning the worker payload the
// engine resolved for it. Every offloadable kind is offloaded so that each kind is
// leasable from outside rather than served in process.
func leaseConnectorPayload(t *testing.T, procID, element, jobType, variables string) connectorPayload {
	t.Helper()
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds(offloadableKindNames()))

	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments",
		connectorTaskModel(procID, element), "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy %s: status=%d body=%s", procID, code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		variables, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance for %s: status=%d body=%s", procID, code, raw)
	}

	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		fmt.Sprintf(`{"type":%q,"worker":"w1"}`, jobType), "application/json")
	if code != http.StatusOK {
		t.Fatalf("lease %s: status=%d body=%s", jobType, code, raw)
	}
	var out struct {
		Jobs []struct {
			Connector *connectorPayload `json:"connector"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode lease of %s: %v (%s)", jobType, err, raw)
	}
	if len(out.Jobs) != 1 {
		t.Fatalf("leased %d jobs of type %s, want 1; body=%s", len(out.Jobs), jobType, raw)
	}
	if out.Jobs[0].Connector == nil {
		t.Fatalf("%s job leased with no worker payload: the worker has nothing to act on", jobType)
	}
	return *out.Jobs[0].Connector
}

// TestEachConnectorKindResolvesItsOwnPayload walks the kinds whose arm had no lease
// behind it. Each asserts the kind name the worker dispatches on, plus the fields it
// dereferences — including at least one resolved from a FEEL expression, since
// resolving against the instance's variables is the whole reason this happens in the
// engine rather than at the worker.
func TestEachConnectorKindResolvesItsOwnPayload(t *testing.T) {
	const vars = `{"variables":{"userDN":"cn=ada,dc=example,dc=com","tenant":"contoso","impact":"2-Significant","ldifText":"dn: cn=ada\\nobjectClass: person"}}`

	for _, tc := range []struct {
		name    string
		element string
		jobType string
		want    string         // payload kind, which is what the worker dispatches on
		fields  map[string]any // fields a worker reads, and what the model authored
	}{
		{
			name:    "rest",
			element: `<atlas:restConnector method="post" url="https://api.example.com/customers" resultVariable="created"/>`,
			jobType: compiler.RestJobType,
			want:    "rest",
			fields:  map[string]any{"method": "POST", "url": "https://api.example.com/customers"},
		},
		{
			name:    "ad",
			element: `<atlas:adConnector url="ldaps://dc.example.com" operation="add-group-member" dn="cn=Admins,dc=example,dc=com" memberDN="=userDN"/>`,
			jobType: compiler.AdJobType,
			want:    "ad",
			// memberDN is authored as a FEEL expression: the engine resolved it
			// against the instance's variables before the payload left.
			fields: map[string]any{"operation": "add-group-member", "memberDN": "cn=ada,dc=example,dc=com"},
		},
		{
			// A task that names a Console-configured directory instead of carrying its
			// own url (ADR-0206). The *name* is what has to travel: the worker holds
			// that directory's URL and bind credentials under it, so a payload that
			// dropped the name would leave the worker with nothing to dial.
			name:    "ad-named-directory",
			element: `<atlas:adConnector connector="prod-forest" operation="disable" dn="cn=Arno,dc=example,dc=com"/>`,
			jobType: compiler.AdJobType,
			want:    "ad",
			fields:  map[string]any{"connector": "prod-forest", "operation": "disable"},
		},
		{
			// The search a membership change does first. The scope travels with it,
			// because "is this group anywhere under here" and "is it directly under
			// here" are different questions (ADR-0166, amended a fifth time).
			name:    "ad-search",
			element: `<atlas:adConnector url="ldaps://dc.example.com" operation="search" baseDN="ou=groups,dc=example,dc=com" scope="one" filter="(cn=Vertrieb)" resultVariable="gruppe"/>`,
			jobType: compiler.AdJobType,
			want:    "ad",
			fields: map[string]any{
				"operation": "search", "baseDN": "ou=groups,dc=example,dc=com",
				"scope": "one", "filter": "(cn=Vertrieb)", "resultVariable": "gruppe",
			},
		},
		{
			// A clio write. The event *body* is the one payload field no worker could
			// reconstruct: it is the task's input mappings, or every variable it sees,
			// which lives only in engine state (ADR-0036/0174). The idempotency key
			// travels for the same reason REST's does — it is the job key, and a retry
			// must write the same event once.
			name:    "clio-write",
			element: `<atlas:clioConnector connector="events" operation="write" subject="/kunden/42" eventType="kunde.angelegt"/>`,
			jobType: compiler.ClioWriteJobType,
			want:    "clio",
			fields: map[string]any{
				"connector": "events", "operation": "write",
				"subject": "/kunden/42", "eventType": "kunde.angelegt",
				"data": map[string]any{
					"userDN": "cn=ada,dc=example,dc=com", "tenant": "contoso",
					"impact": "2-Significant", "ldifText": `dn: cn=ada\nobjectClass: person`,
				},
			},
		},
		{
			// A clio read: no body, a limit, and a result variable — the half a write
			// does not have, and the half the worker writes back.
			name:    "clio-read",
			element: `<atlas:clioConnector connector="events" operation="read" subject="/kunden/42" limit="25" resultVariable="ereignisse"/>`,
			jobType: compiler.ClioReadJobType,
			want:    "clio",
			fields: map[string]any{
				"connector": "events", "operation": "read", "subject": "/kunden/42",
				"limit": float64(25), "resultVariable": "ereignisse",
			},
		},
		{
			name:    "webscrape",
			element: `<atlas:webscrapeConnector url="https://example.com" selector=".price" resultVariable="hits"/>`,
			jobType: compiler.WebScrapeJobType,
			want:    "webscrape",
			fields:  map[string]any{"url": "https://example.com", "selector": ".price"},
		},
		{
			// A feed scrape. format and maxItems are compile-time structural data
			// (ADR-0190) and decide what the worker *does*: without the format it
			// fetches the feed as HTML and applies a CSS selector to XML.
			name:    "webscrape-feed",
			element: `<atlas:webscrapeConnector url="https://example.com/rss" format="rss" maxItems="15" plainText="true" resultVariable="schlagzeilen"/>`,
			jobType: compiler.WebScrapeJobType,
			want:    "webscrape",
			fields: map[string]any{
				"url": "https://example.com/rss", "format": "rss", "maxItems": float64(15),
				"plainText": true,
			},
		},
		{
			// A structured HTML scrape. The field list decides the result's *shape*:
			// a worker that receives the item selector without the fields returns the
			// items' text as strings, and the model reads .zins off a string
			// (ADR-0231).
			name: "webscrape-fields",
			element: `<atlas:webscrapeConnector url="https://example.com/zinsen" selector="tr.row" absoluteLinks="true" resultVariable="zinsen">` +
				`<atlas:scrapeField name="laufzeit" selector="td.term"/>` +
				`<atlas:scrapeField name="link" selector="a" attribute="href"/>` +
				`</atlas:webscrapeConnector>`,
			jobType: compiler.WebScrapeJobType,
			want:    "webscrape",
			fields: map[string]any{
				"url": "https://example.com/zinsen", "selector": "tr.row", "absoluteLinks": true,
				"fields": []any{
					map[string]any{"name": "laufzeit", "selector": "td.term"},
					map[string]any{"name": "link", "selector": "a", "attribute": "href"},
				},
			},
		},
		{
			name:    "entra",
			element: `<atlas:entraConnector connector="=tenant" operation="list-users" resultVariable="users"/>`,
			jobType: compiler.EntraJobType,
			want:    "entra",
			// The worker *name* travels, never the client secret (ADR-0168), and
			// the name itself may be authored as FEEL.
			fields: map[string]any{"connector": "contoso", "operation": "list-users"},
		},
		{
			name:    "remedy",
			element: `<atlas:remedyConnector connector="helix-itsm" form="HPD:IncidentInterface_Create" resultVariable="incidentNumber"><atlas:remedyField name="Description" value="Disk full on server"/><atlas:remedyField name="Impact" value="=impact"/></atlas:remedyConnector>`,
			jobType: compiler.RemedyJobType,
			want:    "remedy",
			fields:  map[string]any{"connector": "helix-itsm", "form": "HPD:IncidentInterface_Create"},
		},
		{
			name:    "ldif",
			element: `<atlas:ldifConnector format="ldif" source="ldifText" resultVariable="entries"/>`,
			jobType: compiler.LdifJobType,
			want:    "ldif",
			fields:  map[string]any{"format": "ldif"},
		},
		{
			name:    "postgres",
			element: `<atlas:postgresConnector connector="hr-db" operation="query" statement="SELECT 1" resultVariable="rows"/>`,
			jobType: compiler.PostgresJobType,
			// The SQL arm is shared by all three products and reports which one it is
			// in the payload kind, so a worker holding one DSN cannot be handed
			// another product's statement.
			want:   "postgres",
			fields: map[string]any{"connector": "hr-db", "operation": "query", "statement": "SELECT 1", "product": "postgres"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := leaseConnectorPayload(t, tc.name+"-proc", tc.element, tc.jobType, vars)
			if got.Kind != tc.want {
				t.Errorf("payload kind = %q, want %q: the worker dispatches on this", got.Kind, tc.want)
			}
			for k, want := range tc.fields {
				// DeepEqual, not ==: a payload field can be a list (a scrape's item
				// fields), and comparing those with == panics rather than failing.
				if !reflect.DeepEqual(got.Fields[k], want) {
					t.Errorf("field %q = %#v, want %#v", k, got.Fields[k], want)
				}
			}
		})
	}
}

// scriptJobTaskModel is a script task authored in a general-purpose language, which
// is its own node type rather than a task (ADR-0047) but resolves through
// the same switch and for the same reason: the source lives in the compiled process
// and the variables it sees come from walking the scope chain, neither of which a
// worker has.
func scriptJobTaskModel() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs-script">
  <bpmn:process id="script-proc" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:scriptTask id="t">
      <bpmn:extensionElements>
        <atlas:jobScript language="javascript" resultVariable="total">result = 1 + 1;</atlas:jobScript>
      </bpmn:extensionElements>
    </bpmn:scriptTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

// What a script task needs on the far side is an interpreter, not a credential, so
// the source itself travels — the one payload where that is true.
func TestScriptJobTaskResolvesItsSource(t *testing.T) {
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds(offloadableKindNames()))

	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments",
		scriptJobTaskModel(), "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy script model: status=%d body=%s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}

	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		fmt.Sprintf(`{"type":%q,"worker":"js-1"}`, compiler.JsJobType), "application/json")
	if code != http.StatusOK {
		t.Fatalf("lease script job: status=%d body=%s", code, raw)
	}
	var out struct {
		Jobs []struct {
			Connector *connectorPayload `json:"connector"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode lease: %v (%s)", err, raw)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].Connector == nil {
		t.Fatalf("script job leased without a payload; body=%s", raw)
	}
	got := *out.Jobs[0].Connector
	if got.Kind != "script" {
		t.Errorf("payload kind = %q, want \"script\"", got.Kind)
	}
	if got.Fields["source"] != "result = 1 + 1;" {
		t.Errorf("source = %#v, want the authored script body", got.Fields["source"])
	}
}

// A FEEL expression that resolves to nothing does not refuse the lease and does not
// fail the job in the engine: the field travels as null and the worker on the far
// side fails it with a message an operator can read. That is the deliberate split —
// refusing here would park the token with nothing said about why — and it is what
// makes the arms' `return nil` on a resolve error a defensive path rather than the
// normal one for a bad expression. Pinned because it is easy to "fix" the null away
// into a lease refusal and be sure that is an improvement.
func TestAnUnresolvableFeelFieldTravelsAsNullRatherThanBlockingTheLease(t *testing.T) {
	for _, tc := range []struct {
		name    string
		element string
		jobType string
		field   string
	}{
		{
			name:    "mail",
			element: `<atlas:mailConnector connector="office365" to="=missing" subject="Hi" body="There"/>`,
			jobType: compiler.MailJobType,
			field:   "to",
		},
		{
			name:    "rest",
			element: `<atlas:restConnector method="get" url="=missing" resultVariable="r"/>`,
			jobType: compiler.RestJobType,
			field:   "url",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := leaseConnectorPayload(t, tc.name+"-null", tc.element, tc.jobType, `{"variables":{}}`)
			if v, present := got.Fields[tc.field]; !present || (v != nil && v != "") {
				t.Errorf("field %q = %#v, want the zero value: an unresolved expression must not invent one", tc.field, v)
			}
		})
	}
}

// Not every Worker Type has an arm, and that is deliberate: a kind still served
// in process needs no payload, because the engine makes the call itself and never
// hands the task to anyone. The switch falls through to nil for those, and a worker
// that leases one is expected to hold the whole configuration itself.
//
// This is the case that turns "the switch has no arm for X" from a silent omission
// into a stated one: if a kind is later offloaded without adding its arm, the worker
// gets a job with nothing on it, and this test is where that shows up.
func TestAKindWithNoArmResolvesToNoPayload(t *testing.T) {
	// temis, the last kind ADR-0233's table still owes a worker half. This test has
	// stood on ldap, then soap, then sharepoint, then scim, and moved each time one of
	// them got its slice — the table shrinking, working as intended. When temis moves
	// too, there is no kind left for it to name and it goes with that slice.
	//
	// It needs its own model rather than the shared one: a central decision is a
	// *business rule* task, not a service task, which is also why its slice will be
	// the one that does not simply copy the others.
	got := leaseModelPayloadOrNil(t, centralDecisionPayloadBPMN,
		compiler.TemisDecisionJobType, `{"variables":{}}`)
	if got != nil {
		t.Errorf("payload = %#v, want none: this kind has no arm in resolveConnectorTask", *got)
	}
}

// centralDecisionPayloadBPMN is Start → BusinessRuleTask(central) → End: the one
// shape that reaches a kind still evaluated in the engine.
const centralDecisionPayloadBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs-temis-proc">
  <bpmn:process id="temis-proc" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:businessRuleTask id="decide">
      <bpmn:extensionElements>
        <zeebe:calledDecision decisionId="Rate" resultVariable="found"/>
        <atlas:temisConnector connector="rules"/>
      </bpmn:extensionElements>
    </bpmn:businessRuleTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="decide"/>
    <bpmn:sequenceFlow id="f2" sourceRef="decide" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// leaseModelPayloadOrNil is leaseConnectorPayload without the insistence that a
// payload came back, and taking a whole model rather than one element — the shape a
// kind with no arm needs, since the last of them is not a service task at all.
func leaseModelPayloadOrNil(t *testing.T, modelXML, jobType, variables string) *connectorPayload {
	t.Helper()
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds(offloadableKindNames()))

	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", modelXML, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		variables, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		fmt.Sprintf(`{"type":%q,"worker":"w1"}`, jobType), "application/json")
	if code != http.StatusOK {
		t.Fatalf("lease %s: status=%d body=%s", jobType, code, raw)
	}
	var out struct {
		Jobs []struct {
			Connector *connectorPayload `json:"connector"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode lease of %s: %v (%s)", jobType, err, raw)
	}
	if len(out.Jobs) != 1 {
		t.Fatalf("leased %d jobs of type %s, want 1; body=%s", len(out.Jobs), jobType, raw)
	}
	return out.Jobs[0].Connector
}

// The check the two bugs above needed, and neither had.
//
// Every arm of resolveConnectorTask writes a map literal by hand, and every kind
// already has a struct that says exactly what a resolved job is — the `json` tags the
// worker unmarshals into. The map and the struct are two spellings of one contract,
// kept in step by nothing: a field added to the struct and to the in-process handler
// but not to the map is invisible on both sides. It compiles, it deploys, it leases,
// and the worker reads a zero value.
//
// It happened twice. `ad.Job.Connector` (ADR-0206) never reached the map, so a task
// naming a Console-configured directory failed on the worker with "empty url".
// `webscrape.Job.Format` and `MaxItems` (ADR-0190) never reached it either, so an
// offloaded feed scrape was fetched as HTML and failed compiling a CSS selector the
// task had not authored — and webscrape is offloaded by default, so that is the path
// a shipped feed model actually took.
//
// The rule this pins: **the payload is exactly the resolved job.** Not a subset a
// reviewer has to notice is short, and not a superset carrying keys nothing reads.
func TestEveryPayloadArmSendsTheWholeResolvedJob(t *testing.T) {
	arms := payloadArms(t)
	for _, tc := range []struct {
		arm string // the job-type case the arm answers
		job any    // the resolved-job struct a worker unmarshals into
	}{
		{"compiler.MailJobTypeIndex", mail.Job{}},
		{"compiler.RemedyJobTypeIndex", remedy.Job{}},
		{"compiler.JiraJobTypeIndex", jira.Job{}},
		{"compiler.MsSqlJobTypeIndex", sqldb.Job{}},
		{"compiler.AdJobTypeIndex", ad.Job{}},
		{"compiler.LdapJobTypeIndex", ldap.Job{}},
		{"compiler.EntraJobTypeIndex", entra.Job{}},
		{"compiler.ClioWriteJobTypeIndex", clio.Job{}},
		{"compiler.WebScrapeJobTypeIndex", webscrape.Job{}},
		{"compiler.RestJobTypeIndex", rest.Job{}},
		{"compiler.SoapJobTypeIndex", soap.Job{}},
		{"compiler.SharePointJobTypeIndex", sharepoint.Job{}},
		{"compiler.ScimJobTypeIndex", scim.Job{}},
	} {
		t.Run(tc.arm, func(t *testing.T) {
			sent, ok := arms[tc.arm]
			if !ok {
				t.Fatalf("no payload arm for %s; the switch or the pattern must have changed", tc.arm)
			}
			carried := jsonFieldNames(t, tc.job)
			for name := range carried {
				if !sent[name] {
					t.Errorf("the resolved job carries %q, but the payload arm does not send it — "+
						"the worker unmarshals a zero value and acts on it", name)
				}
			}
			for name := range sent {
				if !carried[name] {
					t.Errorf("the payload arm sends %q, which the resolved job has no field for — "+
						"nothing on the far side reads it", name)
				}
			}
		})
	}
}

// payloadArmRe finds one arm of resolveConnectorTask: the job-type case it answers,
// and the field map it puts on the wire. Source-read rather than exercised, because a
// map literal is not something reflection can see — and reading it is the same move
// the moddle drift tests make over compiler/parse.go.
var payloadArmRe = regexp.MustCompile(`(?s)case (compiler\.[A-Za-z]+JobTypeIndex)[^\n]*:.*?connectorPayload\{Kind:[^,]+, Fields: map\[string\]any\{(.*?)\n\t\t\}\}`)

// payloadKeyRe matches one key of such a map.
var payloadKeyRe = regexp.MustCompile(`"([a-zA-Z0-9_]+)":`)

// payloadArms reads the field set each arm sends, keyed by its job-type case.
func payloadArms(t *testing.T) map[string]map[string]bool {
	t.Helper()
	src, err := os.ReadFile("handlers.go")
	if err != nil {
		t.Fatalf("read handlers.go: %v", err)
	}
	out := map[string]map[string]bool{}
	for _, m := range payloadArmRe.FindAllStringSubmatch(string(src), -1) {
		keys := map[string]bool{}
		for _, k := range payloadKeyRe.FindAllStringSubmatch(m[2], -1) {
			keys[k[1]] = true
		}
		out[m[1]] = keys
	}
	if len(out) == 0 {
		t.Fatal("found no payload arms in handlers.go; the pattern must have changed")
	}
	return out
}

// jsonFieldNames is the wire contract a resolved-job struct declares: the names its
// `json` tags give, which is what a worker unmarshals by.
func jsonFieldNames(t *testing.T, v any) map[string]bool {
	t.Helper()
	rt := reflect.TypeOf(v)
	out := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	if len(out) == 0 {
		t.Fatalf("%s declares no json fields; it is not a resolved-job struct", rt)
	}
	return out
}
