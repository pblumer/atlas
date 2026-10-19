package teamsphone_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/teamsphone"
	"github.com/pblumer/atlas/model"
)

// fakeReader is the slice of the state store Resolve reads: one element instance and
// the variables of its process-instance scope.
type fakeReader struct {
	ei   *model.ElementInstanceValue
	vars []model.VariableValue
}

func (f *fakeReader) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return f.ei, f.ei != nil, nil
}

func (f *fakeReader) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if f.ei == nil || scope != f.ei.ProcessInstanceKey {
		return nil
	}
	for i := range f.vars {
		if err := fn(&f.vars[i]); err != nil {
			return err
		}
	}
	return nil
}

func taskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:teamsPhoneConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func compileTask(attrs string) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail, int32, error) {
	cp, err := compiler.Parse(7, 1, strings.NewReader(taskBPMN(attrs)))
	if err != nil {
		return nil, nil, 0, err
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	return cp, cp.ConnectorTask(cp.Node(task).Detail), task, nil
}

func resolve(t *testing.T, attrs string, vars ...model.VariableValue) teamsphone.Job {
	t.Helper()
	cp, detail, task, err := compileTask(attrs)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rd := &fakeReader{
		ei:   &model.ElementInstanceValue{ProcessInstanceKey: 500, ProcessDefKey: 7, ElementId: task, FlowScopeKey: 500},
		vars: vars,
	}
	j, err := teamsphone.Resolve(rd, cp, detail, rd.ei, 42)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return j
}

// recordingClient captures the prepared request and answers with a canned result.
type recordingClient struct {
	reqs   []teamsphone.Request
	result map[string]any
}

func (r *recordingClient) Do(_ context.Context, req teamsphone.Request) (map[string]any, error) {
	r.reqs = append(r.reqs, req)
	return r.result, nil
}

// The engine resolves the authored values with their shape — a FEEL list stays a list —
// and the worker hands the client every value in the shape its operation takes, the
// compiled settings included.
func TestAResolvedQueueReachesTheClientInItsShapes(t *testing.T) {
	j := resolve(t,
		`connector="contoso" operation="ensure-call-queue" name="=&quot;CQ &quot; + line" resourceAccountId="=accounts.queue" agents="=agentIds" authorizedUsers="owner@contoso.example, deputy@contoso.example," routingMethod="serial" allowOptOut="false" resultVariable="queue"`,
		model.VariableValue{Name: "line", Kind: model.VarString, Text: "Support"},
		model.VariableValue{Name: "accounts", Kind: model.VarJSON, Text: `{"queue":"ra-1"}`},
		model.VariableValue{Name: "agentIds", Kind: model.VarJSON, Text: `["a-1","a-2","a-1"]`},
	)
	if j.Connector != "contoso" || j.Operation != "ensure-call-queue" || j.ResultVariable != "queue" {
		t.Fatalf("job = %+v", j)
	}
	if _, ok := j.Args["agents"].([]any); !ok {
		t.Errorf("agents resolved to %T, want the FEEL list kept a list", j.Args["agents"])
	}
	c := &recordingClient{result: map[string]any{"id": "q-1", "name": "CQ Support", "created": true}}
	reg := teamsphone.NewRegistry()
	reg.Register("contoso", c)
	out, err := teamsphone.Run(context.Background(), j, reg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out["queue"].(map[string]any)["id"]; got != "q-1" {
		t.Errorf("result variable = %v, want the client's answer", out)
	}
	args := c.reqs[0].Args
	want := map[string]any{
		"name":                 "CQ Support",
		"resourceAccountId":    "ra-1",
		"agents":               []string{"a-1", "a-2"},
		"authorizedUsers":      []string{"owner@contoso.example", "deputy@contoso.example"},
		"routingMethod":        "serial",
		"presenceBasedRouting": true,
		"conferenceMode":       true,
		"allowOptOut":          false,
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("prepared args\n got %#v\nwant %#v", args, want)
	}
}

// Nothing to resolve is an answer, and no PowerShell starts for it.
func TestResolvingNoUsersSignsNothingIn(t *testing.T) {
	j := resolve(t, `connector="c" operation="resolve-users" users="=agents" resultVariable="ids"`,
		model.VariableValue{Name: "agents", Kind: model.VarString, Text: " , "})
	c := &recordingClient{}
	reg := teamsphone.NewRegistry()
	reg.Register("c", c)
	out, err := teamsphone.Run(context.Background(), j, reg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(c.reqs) != 0 {
		t.Errorf("the client was called %d times for an empty list", len(c.reqs))
	}
	got, _ := json.Marshal(out)
	if string(got) != `{"ids":{"ids":[],"notFound":[]}}` {
		t.Errorf("result = %s", got)
	}
}

// An unconfigured tenant is the first thing reported, before anything about the
// request, because it is the fix an operator can make (ADR-0158).
func TestAnUnconfiguredTenantIsNamed(t *testing.T) {
	_, err := teamsphone.Run(context.Background(), teamsphone.Job{Connector: "nowhere", Operation: "nonsense"}, teamsphone.NewRegistry())
	if err == nil || !strings.Contains(err.Error(), `"nowhere"`) {
		t.Fatalf("want the unconfigured tenant named, got %v", err)
	}
}

// Prepare refuses what an operation cannot carry, naming the attribute.
func TestPrepareRefusals(t *testing.T) {
	agents := make([]any, teamsphone.MaxAgents+1)
	for i := range agents {
		agents[i] = fmt.Sprintf("agent-%d", i)
	}
	cases := map[string]struct {
		job  teamsphone.Job
		want string
	}{
		"unknown operation": {teamsphone.Job{Operation: "create-phone"}, "unknown operation"},
		"value not used":    {teamsphone.Job{Operation: "resolve-users", Args: map[string]any{"users": "a", "timeZone": "UTC"}}, `does not use "timeZone"`},
		"empty required":    {teamsphone.Job{Operation: "ensure-call-queue", Args: map[string]any{"name": nil, "resourceAccountId": "r"}}, "needs name"},
		"list as a name":    {teamsphone.Job{Operation: "remove-call-queue", Args: map[string]any{"resourceAccountId": []any{"a", "b"}}}, "must be one value"},
		"too many agents":   {teamsphone.Job{Operation: "ensure-call-queue", Args: map[string]any{"name": "n", "resourceAccountId": "r", "agents": agents}}, "at most 20"},
		"agents unset":      {teamsphone.Job{Operation: "ensure-call-queue", Args: map[string]any{"name": "n", "resourceAccountId": "r", "agents": nil}}, "agents resolved to nothing"},
		"unknown setting":   {teamsphone.Job{Operation: "ensure-resource-account", Args: map[string]any{"userPrincipalName": "u", "displayName": "d", "accountType": "queue"}}, `accountType "queue"`},
		"no account type":   {teamsphone.Job{Operation: "ensure-resource-account", Args: map[string]any{"userPrincipalName": "u", "displayName": "d"}}, `accountType ""`},
		"holiday no date":   {attendant(`[{"name":"Weihnachten","start":"25.12.2026"}]`), "YYYY-MM-DD"},
		"holiday backwards": {attendant(`[{"name":"Weihnachten","start":"2026-12-25","end":"2026-12-25"}]`), "not after its start"},
		"holiday twice":     {attendant(`[{"name":"A","start":"2026-12-25"},{"name":"a","start":"2026-12-26"}]`), "named twice"},
		"holiday not list":  {attendant(`{"name":"A"}`), "list of {name, start, end}"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := teamsphone.Prepare(c.job)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want an error containing %q, got %v", c.want, err)
			}
		})
	}
}

// The agents a model names are a queue's agents, so the worker tells three cases apart
// that a list alone cannot: agents not named at all leave an existing queue's agents as
// they are, an empty list is a list, and a value that resolved to nothing — a variable
// read before anybody set it — is refused rather than read as "no agents".
func TestUnnamedAgentsAreNotNoAgents(t *testing.T) {
	queue := func(args map[string]any) teamsphone.Job {
		args["name"], args["resourceAccountId"] = "CQ Support", "ra-1"
		return teamsphone.Job{Operation: "ensure-call-queue", Args: args}
	}
	req, err := teamsphone.Prepare(queue(map[string]any{}))
	if err != nil {
		t.Fatalf("Prepare without agents: %v", err)
	}
	if v, ok := req.Args["agents"]; ok {
		t.Errorf("agents nobody named reached the client as %#v; want it left out", v)
	}
	req, err = teamsphone.Prepare(queue(map[string]any{"agents": []any{}}))
	if err != nil {
		t.Fatalf("Prepare with an empty list: %v", err)
	}
	if v, ok := req.Args["agents"].([]string); !ok || len(v) != 0 {
		t.Errorf("an empty list of agents reached the client as %#v; want an empty list", req.Args["agents"])
	}

	j := resolve(t, `connector="contoso" operation="ensure-call-queue" name="CQ Support" resourceAccountId="ra-1" agents="=agentIds" resultVariable="queue"`)
	c := &recordingClient{}
	reg := teamsphone.NewRegistry()
	reg.Register("contoso", c)
	if _, err := teamsphone.Run(context.Background(), j, reg); err == nil || !strings.Contains(err.Error(), "agents resolved to nothing") {
		t.Errorf("an unset agents variable: %v; want it refused", err)
	}
	if len(c.reqs) != 0 {
		t.Error("an unset agents variable reached the client")
	}
}

// ensure-call-queue brings an existing queue's agents to the list a model names, so a
// process that runs it again after a change of staff changes the queue instead of
// reporting success and leaving it as it was. It does so through the queue's account
// and through its name alike, compares ids as Teams does (without regard to case), and
// lets order count only where serial routing rings in it. It never empties a queue: a
// Resolve users that found nobody reads exactly like a queue nobody staffs.
func TestEnsureCallQueueReconcilesItsAgents(t *testing.T) {
	m := teamsphone.NewMockTenant()
	reg := teamsphone.NewRegistry()
	reg.Register("contoso", m)
	run := func(op string, args map[string]any) (map[string]any, error) {
		out, err := teamsphone.Run(context.Background(), teamsphone.Job{Connector: "contoso", Operation: op, Args: args, ResultVariable: "r"}, reg)
		if err != nil {
			return nil, err
		}
		return out["r"].(map[string]any), nil
	}
	account := func(upn string) string {
		t.Helper()
		out, err := run("ensure-resource-account", map[string]any{"userPrincipalName": upn, "displayName": "Support", "accountType": "call-queue"})
		if err != nil {
			t.Fatal(err)
		}
		return out["objectId"].(string)
	}
	accountID := account("cq_support@contoso.example")
	ensure := func(args map[string]any) map[string]any {
		t.Helper()
		args["name"] = "CQ Support"
		if _, ok := args["resourceAccountId"]; !ok {
			args["resourceAccountId"] = accountID
		}
		out, err := run("ensure-call-queue", args)
		if err != nil {
			t.Fatalf("ensure-call-queue %v: %v", args, err)
		}
		return out
	}

	first := ensure(map[string]any{"agents": "A-1, a-2"})
	if first["created"] != true || first["agentsChanged"] != false {
		t.Fatalf("first ensure-call-queue = %v, want created with its agents and nothing to change", first)
	}
	queueID := first["id"].(string)
	steps := []struct {
		name        string
		args        map[string]any
		wantChanged bool
		wantAgents  []string
	}{
		{"the same agents in another case and order", map[string]any{"agents": []any{"a-2", "a-1"}}, false, []string{"A-1", "a-2"}},
		{"a change of staff", map[string]any{"agents": "a-2, a-3"}, true, []string{"a-2", "a-3"}},
		{"the same change again", map[string]any{"agents": "a-2, a-3"}, false, []string{"a-2", "a-3"}},
		{"agents not named", map[string]any{}, false, []string{"a-2", "a-3"}},
		{"another order under serial routing", map[string]any{"agents": "a-3, a-2", "routingMethod": "serial"}, true, []string{"a-3", "a-2"}},
	}
	for _, s := range steps {
		got := ensure(s.args)
		if got["created"] != false || got["id"] != queueID || got["agentsChanged"] != s.wantChanged {
			t.Errorf("%s: ensure-call-queue = %v, want the queue %s with agentsChanged=%v", s.name, got, queueID, s.wantChanged)
		}
		if agents := m.AgentsOf(queueID); !reflect.DeepEqual(agents, s.wantAgents) {
			t.Errorf("%s: the queue's agents are %v, want %v", s.name, agents, s.wantAgents)
		}
	}

	if _, err := run("ensure-call-queue", map[string]any{"name": "CQ Support", "resourceAccountId": accountID, "agents": []any{}}); err == nil ||
		!strings.Contains(err.Error(), "would remove every agent") {
		t.Errorf("an empty list for a staffed queue: %v; want it refused", err)
	}
	if agents := m.AgentsOf(queueID); !reflect.DeepEqual(agents, []string{"a-3", "a-2"}) {
		t.Errorf("a refused reconciliation changed the agents to %v", agents)
	}

	// A queue found by its name — its account was removed and a new one made — is
	// reconciled before the new account is associated with it.
	if _, err := run("remove-resource-account", map[string]any{"resourceAccountId": accountID}); err != nil {
		t.Fatal(err)
	}
	newAccount := account("cq_support2@contoso.example")
	if _, err := run("ensure-call-queue", map[string]any{"name": "CQ Support", "resourceAccountId": newAccount, "agents": []any{}}); err == nil {
		t.Error("an empty list for a staffed queue found by name was not refused")
	}
	byName := ensure(map[string]any{"resourceAccountId": newAccount, "agents": "a-4"})
	if byName["created"] != false || byName["id"] != queueID || byName["agentsChanged"] != true {
		t.Errorf("ensure-call-queue by name = %v, want the queue %s found and its agents changed", byName, queueID)
	}
	if agents := m.AgentsOf(queueID); !reflect.DeepEqual(agents, []string{"a-4"}) {
		t.Errorf("the queue found by name has the agents %v, want [a-4]", agents)
	}

	// A queue without agents is not emptied by an empty list either: there is nothing to
	// take away, so there is nothing to refuse.
	emptyAccount := account("cq_empty@contoso.example")
	emptyQueue := func() map[string]any {
		return map[string]any{"name": "CQ Empty", "resourceAccountId": emptyAccount, "agents": []any{}}
	}
	created, err := run("ensure-call-queue", emptyQueue())
	if err != nil || created["created"] != true {
		t.Fatalf("a queue without agents = %v, %v", created, err)
	}
	if again, err := run("ensure-call-queue", emptyQueue()); err != nil || again["agentsChanged"] != false || again["id"] != created["id"] {
		t.Errorf("an empty list for a queue without agents = %v, %v; want it unchanged", again, err)
	}
}

func attendant(holidays string) teamsphone.Job {
	return teamsphone.Job{Operation: "ensure-auto-attendant", Args: map[string]any{
		"name": "AA Support", "resourceAccountId": "r", "transferTarget": "q",
		"languageId": "de-CH", "timeZone": "W. Europe Standard Time", "holidays": holidays,
	}}
}

// A holiday without an end is the one day it starts on, and a FEEL list of contexts
// reads like the JSON text of one.
func TestHolidaysAreWholeDays(t *testing.T) {
	req, err := teamsphone.Prepare(attendant(`[{"name":"Weihnachten","start":"2026-12-25"},{"name":"Neujahr","start":"2026-12-31","end":"2027-01-03"}]`))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	want := []teamsphone.Holiday{
		{Name: "Weihnachten", Start: "2026-12-25", End: "2026-12-26"},
		{Name: "Neujahr", Start: "2026-12-31", End: "2027-01-03"},
	}
	if got := req.Args["holidays"]; !reflect.DeepEqual(got, want) {
		t.Errorf("holidays = %#v, want %#v", got, want)
	}
}

// The mock tenant keeps the rules the shipped PowerShell keeps: a provisioning run that
// is repeated converges, the Teams/Entra lag is met, and the leaver path removes what
// the joiner made and answers removed=false the second time.
func TestTheMockTenantConvergesAndUnwinds(t *testing.T) {
	m := teamsphone.NewMockTenant("anna@contoso.example", "beat@contoso.example")
	reg := teamsphone.NewRegistry()
	reg.Register("contoso", m)
	run := func(op string, args map[string]any) map[string]any {
		t.Helper()
		out, err := teamsphone.Run(context.Background(), teamsphone.Job{Connector: "contoso", Operation: op, Args: args, ResultVariable: "r"}, reg)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return out["r"].(map[string]any)
	}
	users := run("resolve-users", map[string]any{"users": "anna@contoso.example, ghost@contoso.example"})
	if len(users["ids"].([]string)) != 1 || users["notFound"].([]string)[0] != "ghost@contoso.example" {
		t.Errorf("resolve-users = %v", users)
	}

	account := func(upn, kind string) map[string]any {
		return run("ensure-resource-account", map[string]any{"userPrincipalName": upn, "displayName": "Support", "accountType": kind})
	}
	first := account("cq_support@contoso.example", "call-queue")
	again := account("cq_support@contoso.example", "call-queue")
	third := account("cq_support@contoso.example", "call-queue")
	if first["created"] != true || again["created"] != false || again["source"] != "entra" || third["source"] != "teams" {
		t.Errorf("ensure-resource-account = %v, %v, %v; want created, then adopted from Entra, then found in Teams", first, again, third)
	}
	if first["objectId"] != again["objectId"] || again["objectId"] != third["objectId"] {
		t.Error("a repeated ensure answered with another account")
	}
	if _, err := m.Do(context.Background(), teamsphone.Request{Operation: "ensure-resource-account", Args: map[string]any{
		"userPrincipalName": "anna@contoso.example", "displayName": "Support", "accountType": "call-queue"}}); err == nil {
		t.Error("an enabled user's address was taken as a resource account")
	}
	cqAccount := first["objectId"].(string)
	aaAccount := account("aa_support@contoso.example", "auto-attendant")["objectId"].(string)

	queue := run("ensure-call-queue", map[string]any{"name": "CQ Support", "resourceAccountId": cqAccount})
	if queue["created"] != true || run("ensure-call-queue", map[string]any{"name": "CQ Support", "resourceAccountId": cqAccount})["id"] != queue["id"] {
		t.Errorf("ensure-call-queue did not converge: %v", queue)
	}
	if _, err := teamsphone.Run(context.Background(), teamsphone.Job{Connector: "contoso", Operation: "ensure-call-queue", ResultVariable: "r",
		Args: map[string]any{"name": "CQ Support", "resourceAccountId": aaAccount}}, reg); err == nil {
		t.Error("an auto attendant's account carried a call queue")
	}
	attendantArgs := map[string]any{"name": "AA Support", "resourceAccountId": aaAccount, "transferTarget": cqAccount,
		"languageId": "de-CH", "timeZone": "W. Europe Standard Time"}
	if run("ensure-auto-attendant", attendantArgs)["created"] != true || run("ensure-auto-attendant", attendantArgs)["created"] != false {
		t.Error("ensure-auto-attendant did not converge")
	}

	for _, step := range []struct{ op, account string }{
		{"remove-auto-attendant", aaAccount}, {"remove-call-queue", cqAccount},
		{"remove-resource-account", aaAccount}, {"remove-resource-account", cqAccount},
	} {
		if got := run(step.op, map[string]any{"resourceAccountId": step.account}); got["removed"] != true {
			t.Errorf("%s = %v, want removed", step.op, got)
		}
		if got := run(step.op, map[string]any{"resourceAccountId": step.account}); got["removed"] != false {
			t.Errorf("%s repeated = %v, want removed=false", step.op, got)
		}
	}
}

// A voice app whose association a failed run already removed is found again by name.
func TestARemoveFindsAnOrphanByName(t *testing.T) {
	m := teamsphone.NewMockTenant()
	ctx := context.Background()
	acc, _ := m.Do(ctx, teamsphone.Request{Operation: "ensure-resource-account", Args: map[string]any{
		"userPrincipalName": "cq_x@contoso.example", "displayName": "X", "accountType": "call-queue"}})
	id := acc["objectId"].(string)
	q, _ := m.Do(ctx, teamsphone.Request{Operation: "ensure-call-queue", Args: map[string]any{"name": "CQ X", "resourceAccountId": id}})
	if _, err := m.Do(ctx, teamsphone.Request{Operation: "remove-resource-account", Args: map[string]any{"resourceAccountId": id}}); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Do(ctx, teamsphone.Request{Operation: "remove-call-queue", Args: map[string]any{"resourceAccountId": id, "name": "CQ X"}})
	if got["removed"] != true || got["id"] != q["id"] {
		t.Errorf("remove-call-queue by name = %v, want the orphaned queue %v removed", got, q["id"])
	}
}

// TestTeamsPhoneOpsMatchTheCompiler is the drift guard between Ops and the compiler's
// own copy of the rules. The compiler cannot import this package, so the check is
// behavioural: a model authoring exactly what Ops requires compiles, one missing any
// required value does not, every optional value and setting is accepted, and every
// attribute the operation does not take is refused.
func TestTeamsPhoneOpsMatchTheCompiler(t *testing.T) {
	sample := map[teamsphone.Shape]string{teamsphone.Text: "x", teamsphone.List: "a,b", teamsphone.Holidays: `=[]`}
	attrs := func(op string, spec teamsphone.Op, omit string, extra ...string) string {
		parts := []string{`connector="c"`, `operation="` + op + `"`}
		for _, name := range spec.Required {
			if name != omit {
				parts = append(parts, name+`="`+sample[teamsphone.Attributes[name]]+`"`)
			}
		}
		for _, name := range spec.Settings {
			if teamsphone.Settings[name].Default == "" && name != omit {
				parts = append(parts, name+`="`+teamsphone.Settings[name].Values[0]+`"`)
			}
		}
		if spec.NeedsResult && omit != "resultVariable" {
			parts = append(parts, `resultVariable="r"`)
		}
		return strings.Join(append(parts, extra...), " ")
	}
	all := make([]string, 0, len(teamsphone.Attributes)+len(teamsphone.Settings))
	for name := range teamsphone.Attributes {
		all = append(all, name)
	}
	for name := range teamsphone.Settings {
		all = append(all, name)
	}
	sort.Strings(all)
	for _, op := range teamsphone.OpNames() {
		spec := teamsphone.Ops[op]
		t.Run(op, func(t *testing.T) {
			for _, name := range spec.Reconciled {
				// Prepare reads only what an operation takes; a reconciled value it does
				// not take would be dropped, and its absence read as "leave it".
				if !slices.Contains(spec.Optional, name) {
					t.Errorf("%s reconciles %s, which is not one of its optional values", op, name)
				}
			}
			if _, _, _, err := compileTask(attrs(op, spec, "")); err != nil {
				t.Fatalf("the compiler rejects a model that satisfies Ops[%q]: %v", op, err)
			}
			required := append([]string{}, spec.Required...)
			if spec.NeedsResult {
				required = append(required, "resultVariable")
			}
			for _, name := range spec.Settings {
				if teamsphone.Settings[name].Default == "" {
					required = append(required, name)
				}
			}
			for _, name := range required {
				if _, _, _, err := compileTask(attrs(op, spec, name)); err == nil {
					t.Errorf("the compiler accepts %q without %s, which Ops requires", op, name)
				}
			}
			takes := map[string]bool{}
			for _, name := range append(append(append([]string{}, spec.Required...), spec.Optional...), spec.Settings...) {
				takes[name] = true
			}
			for _, name := range all {
				value := sample[teamsphone.Attributes[name]]
				if set, ok := teamsphone.Settings[name]; ok {
					value = set.Values[len(set.Values)-1]
				}
				_, _, _, err := compileTask(attrs(op, spec, name, name+`="`+value+`"`))
				if takes[name] && err != nil {
					t.Errorf("the compiler refuses %s on %q, which Ops allows: %v", name, op, err)
				}
				if !takes[name] && err == nil {
					t.Errorf("the compiler accepts %s on %q, which Ops does not take", name, op)
				}
			}
		})
	}
}

// The compiler applies this package's defaults and knows this package's values; a
// setting that drifted would be compiled to one word and read as another.
func TestTheCompilersSettingsAreThisPackages(t *testing.T) {
	for name, set := range teamsphone.Settings {
		op := "ensure-call-queue"
		base := `connector="c" operation="ensure-call-queue" name="n" resourceAccountId="r" resultVariable="q"`
		if name == "accountType" {
			op = "ensure-resource-account"
			base = `connector="c" operation="ensure-resource-account" userPrincipalName="u" displayName="d" resultVariable="q"`
		}
		for _, v := range set.Values {
			if _, _, _, err := compileTask(base + ` ` + name + `="` + v + `"`); err != nil {
				t.Errorf("%s: the compiler refuses %s=%q, which this package names: %v", op, name, v, err)
			}
		}
		if _, _, _, err := compileTask(base + ` ` + name + `="maybe"`); err == nil {
			t.Errorf("%s: the compiler accepts %s=\"maybe\"", op, name)
		}
		if set.Default == "" {
			continue
		}
		_, d, _, err := compileTask(base)
		if err != nil {
			t.Fatalf("compile %s: %v", op, err)
		}
		found := false
		for _, kv := range d.TeamsPhoneArgs {
			if kv.Name == name {
				found = true
				if kv.Val.Literal != set.Default {
					t.Errorf("the compiler defaults %s to %q, this package to %q", name, kv.Val.Literal, set.Default)
				}
			}
		}
		if !found {
			t.Errorf("the compiler applies no default for %s", name)
		}
	}
}
