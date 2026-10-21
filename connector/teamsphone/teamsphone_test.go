package teamsphone_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/teamsphone"
	"github.com/pblumer/atlas/job"
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
	// The settings the model left out carry their defaults, for a queue this run
	// creates, but only what the model named is what an existing queue is brought to.
	if got, want := c.reqs[0].Reconcile, []string{"agents", "allowOptOut", "authorizedUsers", "routingMethod"}; !reflect.DeepEqual(got, want) {
		t.Errorf("reconcile = %v, want %v", got, want)
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
		"unknown operation":   {teamsphone.Job{Operation: "create-phone"}, "unknown operation"},
		"value not used":      {teamsphone.Job{Operation: "resolve-users", Args: map[string]any{"users": "a", "timeZone": "UTC"}}, `does not use "timeZone"`},
		"empty required":      {teamsphone.Job{Operation: "ensure-call-queue", Args: map[string]any{"name": nil, "resourceAccountId": "r"}}, "needs name"},
		"list as a name":      {teamsphone.Job{Operation: "remove-call-queue", Args: map[string]any{"resourceAccountId": []any{"a", "b"}}}, "must be one value"},
		"too many agents":     {teamsphone.Job{Operation: "ensure-call-queue", Args: map[string]any{"name": "n", "resourceAccountId": "r", "agents": agents}}, "at most 20"},
		"agents unset":        {teamsphone.Job{Operation: "ensure-call-queue", Args: map[string]any{"name": "n", "resourceAccountId": "r", "agents": nil}}, "agents resolved to nothing"},
		"required list unset": {teamsphone.Job{Operation: "remove-hotline", Args: map[string]any{"resourceAccountIds": nil}}, "needs resourceAccountIds"},
		"unknown setting":     {teamsphone.Job{Operation: "ensure-resource-account", Args: map[string]any{"userPrincipalName": "u", "displayName": "d", "accountType": "queue"}}, `accountType "queue"`},
		"no account type":     {teamsphone.Job{Operation: "ensure-resource-account", Args: map[string]any{"userPrincipalName": "u", "displayName": "d"}}, `accountType ""`},
		"holiday no date":     {attendant(`[{"name":"Weihnachten","start":"25.12.2026"}]`), "YYYY-MM-DD"},
		"holiday backwards":   {attendant(`[{"name":"Weihnachten","start":"2026-12-25","end":"2026-12-25"}]`), "not after its start"},
		"holiday twice":       {attendant(`[{"name":"A","start":"2026-12-25"},{"name":"a","start":"2026-12-26"}]`), "named twice"},
		"holiday not list":    {attendant(`{"name":"A"}`), "list of {name, start, end}"},
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

// The lists a model names are a queue's lists, so the worker tells three cases apart
// that a list alone cannot: a list not named at all leaves an existing queue's list as
// it is, an empty list is a list, and a value that resolved to nothing — a variable read
// before anybody set it — is refused rather than read as "nobody". A setting the model
// left out still carries its default, for a queue this run creates, but is not one an
// existing queue is brought to.
func TestUnnamedValuesAreNotEmptyValues(t *testing.T) {
	queue := func(args map[string]any) teamsphone.Job {
		args["name"], args["resourceAccountId"] = "CQ Support", "ra-1"
		return teamsphone.Job{Operation: "ensure-call-queue", Args: args}
	}
	req, err := teamsphone.Prepare(queue(map[string]any{}))
	if err != nil {
		t.Fatalf("Prepare without lists: %v", err)
	}
	for _, name := range []string{"agents", "authorizedUsers"} {
		if v, ok := req.Args[name]; ok {
			t.Errorf("%s nobody named reached the client as %#v; want it left out", name, v)
		}
	}
	if req.Args["routingMethod"] != "attendant" || req.Args["presenceBasedRouting"] != true {
		t.Errorf("settings nobody named = %v; want their defaults for a new queue", req.Args)
	}
	if len(req.Reconcile) != 0 {
		t.Errorf("reconcile = %v, want nothing the model did not name", req.Reconcile)
	}
	req, err = teamsphone.Prepare(queue(map[string]any{"agents": []any{}, "authorizedUsers": " , "}))
	if err != nil {
		t.Fatalf("Prepare with empty lists: %v", err)
	}
	for _, name := range []string{"agents", "authorizedUsers"} {
		if v, ok := req.Args[name].([]string); !ok || len(v) != 0 {
			t.Errorf("an empty %s reached the client as %#v; want an empty list", name, req.Args[name])
		}
	}
	if !reflect.DeepEqual(req.Reconcile, []string{"agents", "authorizedUsers"}) {
		t.Errorf("reconcile = %v, want the two lists named", req.Reconcile)
	}

	for _, attr := range []string{"agents", "authorizedUsers"} {
		j := resolve(t, `connector="contoso" operation="ensure-call-queue" name="CQ Support" resourceAccountId="ra-1" `+attr+`="=ids" resultVariable="queue"`)
		c := &recordingClient{}
		reg := teamsphone.NewRegistry()
		reg.Register("contoso", c)
		if _, err := teamsphone.Run(context.Background(), j, reg); err == nil || !strings.Contains(err.Error(), attr+" resolved to nothing") {
			t.Errorf("an unset %s variable: %v; want it refused", attr, err)
		}
		if len(c.reqs) != 0 {
			t.Errorf("an unset %s variable reached the client", attr)
		}
	}
}

// ensure-call-queue brings an existing queue to what a model names — its agents, its
// authorized users and its routing settings — so a process that runs it again after a
// change changes the queue instead of reporting success and leaving it as it was. It does
// so through the queue's account and through its name alike, compares ids as Teams does
// (without regard to case), lets order count only where serial routing rings in it, and
// leaves alone what the model does not name. It never empties a list: a Resolve users
// that found nobody reads exactly like a queue nobody staffs.
func TestEnsureCallQueueReconcilesWhatItNames(t *testing.T) {
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

	first := ensure(map[string]any{"agents": "A-1, a-2", "authorizedUsers": "owner"})
	if first["created"] != true || !reflect.DeepEqual(first["changed"], []string{}) {
		t.Fatalf("first ensure-call-queue = %v, want created with nothing to change", first)
	}
	queueID := first["id"].(string)
	created := teamsphone.MockQueue{Agents: []string{"A-1", "a-2"}, AuthorizedUsers: []string{"owner"},
		RoutingMethod: "attendant", PresenceBasedRouting: true, ConferenceMode: true, AllowOptOut: true}
	if got := m.QueueOf(queueID); !reflect.DeepEqual(got, created) {
		t.Fatalf("the new queue is %+v, want %+v", got, created)
	}
	steps := []struct {
		name        string
		args        map[string]any
		wantChanged []string
		want        func(q *teamsphone.MockQueue)
	}{
		{"the same agents in another case and order", map[string]any{"agents": []any{"a-2", "a-1"}}, []string{}, nil},
		{"nothing named", map[string]any{}, []string{}, nil},
		{"a change of staff", map[string]any{"agents": "a-2, a-3"}, []string{"agents"},
			func(q *teamsphone.MockQueue) { q.Agents = []string{"a-2", "a-3"} }},
		{"the same change again", map[string]any{"agents": "a-2, a-3"}, []string{}, nil},
		{"a new deputy", map[string]any{"authorizedUsers": []any{"OWNER", "deputy"}}, []string{"authorizedUsers"},
			func(q *teamsphone.MockQueue) { q.AuthorizedUsers = []string{"OWNER", "deputy"} }},
		{"the deputy again, in another order", map[string]any{"authorizedUsers": "deputy, owner"}, []string{}, nil},
		{"routing named as it is", map[string]any{"routingMethod": "attendant", "allowOptOut": "true"}, []string{}, nil},
		{"routing changed", map[string]any{"routingMethod": "longest-idle", "presenceBasedRouting": "false", "conferenceMode": "false"},
			[]string{"conferenceMode", "presenceBasedRouting", "routingMethod"},
			func(q *teamsphone.MockQueue) {
				q.RoutingMethod, q.PresenceBasedRouting, q.ConferenceMode = "longest-idle", false, false
			}},
		{"settings not named stay", map[string]any{"agents": "a-2, a-3"}, []string{}, nil},
		{"another order under serial routing", map[string]any{"agents": "a-3, a-2", "routingMethod": "serial"}, []string{"agents", "routingMethod"},
			func(q *teamsphone.MockQueue) { q.Agents, q.RoutingMethod = []string{"a-3", "a-2"}, "serial" }},
		// The queue rings serially now, so its order counts even when the model does not
		// name the routing.
		{"another order on a serial queue", map[string]any{"agents": "a-2, a-3"}, []string{"agents"},
			func(q *teamsphone.MockQueue) { q.Agents = []string{"a-2", "a-3"} }},
	}
	want := created
	for _, s := range steps {
		got := ensure(s.args)
		if s.want != nil {
			s.want(&want)
		}
		if got["created"] != false || got["id"] != queueID || !reflect.DeepEqual(got["changed"], s.wantChanged) {
			t.Errorf("%s: ensure-call-queue = %v, want the queue %s with changed=%v", s.name, got, queueID, s.wantChanged)
		}
		if got["agentsChanged"] != slices.Contains(s.wantChanged, "agents") {
			t.Errorf("%s: agentsChanged = %v, want what 0.16.0 answered: whether the agents changed", s.name, got["agentsChanged"])
		}
		if q := m.QueueOf(queueID); !reflect.DeepEqual(q, want) {
			t.Errorf("%s: the queue is %+v, want %+v", s.name, q, want)
		}
	}

	for _, attr := range []string{"agents", "authorizedUsers"} {
		_, err := run("ensure-call-queue", map[string]any{"name": "CQ Support", "resourceAccountId": accountID, attr: []any{}, "routingMethod": "round-robin"})
		var refusal *teamsphone.Refusal
		if !errors.As(err, &refusal) || refusal.Code != teamsphone.CodeWouldEmptyList || !strings.Contains(err.Error(), "would remove every") {
			t.Errorf("an empty %s for a queue that has some: %v; want it refused as %s", attr, err, teamsphone.CodeWouldEmptyList)
		}
	}
	if q := m.QueueOf(queueID); !reflect.DeepEqual(q, want) {
		t.Errorf("a refused reconciliation changed the queue to %+v; the routing named beside the empty list must not apply either", q)
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
	if byName["created"] != false || byName["id"] != queueID || !reflect.DeepEqual(byName["changed"], []string{"agents"}) {
		t.Errorf("ensure-call-queue by name = %v, want the queue %s found and its agents changed", byName, queueID)
	}
	if q := m.QueueOf(queueID); !reflect.DeepEqual(q.Agents, []string{"a-4"}) {
		t.Errorf("the queue found by name has the agents %v, want [a-4]", q.Agents)
	}

	// A queue without agents is not emptied by an empty list either: there is nothing to
	// take away, so there is nothing to refuse.
	emptyAccount := account("cq_empty@contoso.example")
	emptyQueue := func() map[string]any {
		return map[string]any{"name": "CQ Empty", "resourceAccountId": emptyAccount, "agents": []any{}, "authorizedUsers": []any{}}
	}
	made, err := run("ensure-call-queue", emptyQueue())
	if err != nil || made["created"] != true {
		t.Fatalf("a queue without agents = %v, %v", made, err)
	}
	if again, err := run("ensure-call-queue", emptyQueue()); err != nil || !reflect.DeepEqual(again["changed"], []string{}) || again["id"] != made["id"] {
		t.Errorf("empty lists for a queue without any = %v, %v; want it unchanged", again, err)
	}
}

// A refusal the process may have a path for carries a stable code, so a model can draw
// an error boundary for it. Each refusal the mock makes carries the code the shipped
// PowerShell gives the same refusal.
func TestRefusalsCarryTheirCodes(t *testing.T) {
	ctx := context.Background()
	m := teamsphone.NewMockTenant("anna@contoso.example")
	do := func(op string, args map[string]any) error {
		_, err := m.Do(ctx, teamsphone.Request{Operation: op, Args: args})
		return err
	}
	acc, err := m.Do(ctx, teamsphone.Request{Operation: "ensure-resource-account", Args: map[string]any{
		"userPrincipalName": "aa_x@contoso.example", "displayName": "X", "accountType": "auto-attendant"}})
	if err != nil {
		t.Fatal(err)
	}
	aaID := acc["objectId"].(string)
	// The second ensure finds the account in Teams, which knows what it was made for;
	// before that, Entra alone holds it and cannot say.
	if _, err := m.Do(ctx, teamsphone.Request{Operation: "ensure-resource-account", Args: map[string]any{
		"userPrincipalName": "aa_x@contoso.example", "displayName": "X", "accountType": "auto-attendant"}}); err != nil {
		t.Fatal(err)
	}
	agents := make([]any, teamsphone.MaxAgents+1)
	for i := range agents {
		agents[i] = fmt.Sprintf("agent-%d", i)
	}
	_, tooMany := teamsphone.Prepare(teamsphone.Job{Operation: "ensure-call-queue", Args: map[string]any{"name": "n", "resourceAccountId": "r", "agents": agents}})
	cases := map[string]struct {
		err  error
		code string
	}{
		"an enabled user's address": {do("ensure-resource-account", map[string]any{
			"userPrincipalName": "anna@contoso.example", "displayName": "X", "accountType": "call-queue"}), teamsphone.CodeUPNTaken},
		"an account of the other kind": {do("ensure-resource-account", map[string]any{
			"userPrincipalName": "aa_x@contoso.example", "displayName": "X", "accountType": "call-queue"}), teamsphone.CodeAccountTypeMismatch},
		"a queue on an attendant's account": {do("ensure-call-queue", map[string]any{"name": "CQ", "resourceAccountId": aaID}), teamsphone.CodeAccountTypeMismatch},
		"an account Teams does not show":    {do("ensure-call-queue", map[string]any{"name": "CQ", "resourceAccountId": "nobody"}), teamsphone.CodeAccountNotVisible},
		"too many agents":                   {tooMany, teamsphone.CodeTooManyAgents},
	}
	for name, c := range cases {
		var refusal *teamsphone.Refusal
		if !errors.As(c.err, &refusal) || refusal.Code != c.code {
			t.Errorf("%s: %v; want a refusal coded %s", name, c.err, c.code)
		}
	}
	// A remove of the other kind of voice app is refused, not answered with removed=false:
	// the account is in use, which a leaver path should hear about.
	if err := do("ensure-auto-attendant", map[string]any{"name": "AA", "resourceAccountId": aaID, "transferTarget": aaID}); err != nil {
		t.Fatal(err)
	}
	var refusal *teamsphone.Refusal
	if err := do("remove-call-queue", map[string]any{"resourceAccountId": aaID}); !errors.As(err, &refusal) || refusal.Code != teamsphone.CodeAccountInUse {
		t.Errorf("remove-call-queue on an attendant's account: %v; want a refusal coded %s", err, teamsphone.CodeAccountInUse)
	}
	for _, code := range []string{teamsphone.CodeUPNTaken, teamsphone.CodeAccountTypeMismatch, teamsphone.CodeAccountInUse,
		teamsphone.CodeAccountNotVisible, teamsphone.CodeWouldEmptyList, teamsphone.CodeTooManyAgents,
		teamsphone.CodeNumberInUse, teamsphone.CodeNumberNotFound} {
		if !slices.Contains(teamsphone.Codes, code) {
			t.Errorf("%s is missing from Codes, the list the handbook is held to", code)
		}
	}
}

// A refusal is a BPMN error only when the task's process catches its code (ADR-0461's
// rule): then the instance takes the boundary's path at once. A code the process does
// not catch stays a failure and keeps the retries a transient fault is owed.
func TestACaughtCodeIsThrownAndAnUncaughtOneFails(t *testing.T) {
	cp, err := compiler.Parse(7, 1, strings.NewReader(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:error id="E" name="address taken" errorCode="TEAMSPHONE_UPN_TAKEN"/>
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:teamsPhoneConnector connector="contoso" operation="ensure-resource-account" userPrincipalName="=upn" displayName="X" accountType="call-queue" resultVariable="r"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:boundaryEvent id="b" attachedToRef="t"><bpmn:errorEventDefinition errorRef="E"/></bpmn:boundaryEvent>
    <bpmn:endEvent id="e"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <bpmn:sequenceFlow id="f3" sourceRef="b" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	rd := &fakeReader{
		ei:   &model.ElementInstanceValue{ProcessInstanceKey: 500, ProcessDefKey: 7, ElementId: task, FlowScopeKey: 500},
		vars: []model.VariableValue{{Name: "upn", Kind: model.VarString, Text: "anna@contoso.example"}},
	}
	j, err := teamsphone.Resolve(rd, cp, cp.ConnectorTask(cp.Node(task).Detail), rd.ei, 42)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(j.ErrorCodes, []string{teamsphone.CodeUPNTaken}) {
		t.Fatalf("the job carries the caught codes %v, want [%s]", j.ErrorCodes, teamsphone.CodeUPNTaken)
	}
	reg := teamsphone.NewRegistry()
	reg.Register("contoso", teamsphone.NewMockTenant("anna@contoso.example"))
	_, err = teamsphone.Run(context.Background(), j, reg)
	var thrown *job.BPMNError
	if !errors.As(err, &thrown) || thrown.Code != teamsphone.CodeUPNTaken || !strings.Contains(thrown.Message, "enabled user account") {
		t.Errorf("a refusal the process catches: %v; want the BPMN error %s with the refusal's words", err, teamsphone.CodeUPNTaken)
	}
	j.ErrorCodes = nil
	_, err = teamsphone.Run(context.Background(), j, reg)
	if errors.As(err, &thrown) || err == nil {
		t.Errorf("a refusal the process does not catch: %v; want a failure, not a BPMN error", err)
	}
}

// A phone number is read the way people write one — with spaces, dashes or brackets, or
// as a tel: URI — and handed on as Teams takes it; something that is no number at all is
// refused before any PowerShell starts.
func TestPhoneNumbersAreNormalized(t *testing.T) {
	job := func(number string) teamsphone.Job {
		return teamsphone.Job{Operation: "ensure-phone-number", Args: map[string]any{
			"identity": "anna@contoso.example", "phoneNumber": number, "numberType": "calling-plan"}}
	}
	for written, want := range map[string]string{
		"+41 44 123 45 67":        "+41441234567",
		"tel:+41-44-123-45-67":    "+41441234567",
		"+1 (425) 555-1225":       "+14255551225",
		"+41441234500;ext=12":     "+41441234500;ext=12",
		"41441234567":             "41441234567",
		" +41 (0)44 123 45 67":    "",
		"Zentrale":                "",
		"+41441234567;extension=": "",
	} {
		req, err := teamsphone.Prepare(job(written))
		if want == "" {
			if err == nil || !strings.Contains(err.Error(), "phoneNumber") {
				t.Errorf("%q: %v, %v; want it refused, naming phoneNumber", written, req.Args["phoneNumber"], err)
			}
			continue
		}
		if err != nil || req.Args["phoneNumber"] != want {
			t.Errorf("%q = %v, %v; want %q", written, req.Args["phoneNumber"], err, want)
		}
	}
	if req, err := teamsphone.Prepare(job("+41441234567")); err != nil || req.Args["numberType"] != "calling-plan" {
		t.Errorf("numberType = %v, %v; want the setting carried", req.Args["numberType"], err)
	}
}

// A phone number belongs to one user or resource account. ensure-phone-number gives the
// target the number it names — replacing the one it had, which the answer reports — and
// answers assigned=false when the target has it already; it refuses a number somebody
// else holds, and a Calling Plan or Operator Connect number the tenant does not have. A
// Direct Routing number needs no inventory: the operator's own carrier serves it.
func TestPhoneNumbersFollowTheirTarget(t *testing.T) {
	m := teamsphone.NewMockTenant("anna@contoso.example", "beat@contoso.example").WithNumbers("+41441110000", "+41441110001")
	reg := teamsphone.NewRegistry()
	reg.Register("contoso", m)
	run := func(op string, args map[string]any) (map[string]any, error) {
		out, err := teamsphone.Run(context.Background(), teamsphone.Job{Connector: "contoso", Operation: op, Args: args, ResultVariable: "r"}, reg)
		if err != nil {
			return nil, err
		}
		return out["r"].(map[string]any), nil
	}
	ensure := func(identity, number, kind string) (map[string]any, error) {
		return run("ensure-phone-number", map[string]any{"identity": identity, "phoneNumber": number, "numberType": kind})
	}
	acc, err := run("ensure-resource-account", map[string]any{"userPrincipalName": "cq_x@contoso.example", "displayName": "X", "accountType": "call-queue"})
	if err != nil {
		t.Fatal(err)
	}
	cq := acc["objectId"].(string)

	steps := []struct {
		name, identity, number, kind string
		want                         map[string]any
	}{
		{"a Calling Plan number for a person", "anna@contoso.example", "+41 44 111 00 00", "calling-plan",
			map[string]any{"phoneNumber": "+41441110000", "identity": "anna@contoso.example", "assigned": true, "previousNumber": ""}},
		{"the same again, by another spelling", "ANNA@contoso.example", "+41-44-111-00-00", "calling-plan",
			map[string]any{"phoneNumber": "+41441110000", "identity": "ANNA@contoso.example", "assigned": false, "previousNumber": ""}},
		{"a Direct Routing number for a queue's account", cq, "+41445550000", "direct-routing",
			map[string]any{"phoneNumber": "+41445550000", "identity": cq, "assigned": true, "previousNumber": ""}},
		{"the account by its address", "cq_x@contoso.example", "+41445550000", "direct-routing",
			map[string]any{"phoneNumber": "+41445550000", "identity": "cq_x@contoso.example", "assigned": false, "previousNumber": ""}},
		{"a new number replaces the old one", "anna@contoso.example", "+41441110001", "calling-plan",
			map[string]any{"phoneNumber": "+41441110001", "identity": "anna@contoso.example", "assigned": true, "previousNumber": "+41441110000"}},
		{"the released number is free for somebody else", "beat@contoso.example", "+41441110000", "calling-plan",
			map[string]any{"phoneNumber": "+41441110000", "identity": "beat@contoso.example", "assigned": true, "previousNumber": ""}},
	}
	for _, s := range steps {
		got, err := ensure(s.identity, s.number, s.kind)
		if err != nil || !reflect.DeepEqual(got, s.want) {
			t.Errorf("%s: %v, %v; want %v", s.name, got, err, s.want)
		}
	}

	refusals := []struct {
		name, identity, number, kind, code string
	}{
		{"a number another person holds", "anna@contoso.example", "+41441110000", "calling-plan", teamsphone.CodeNumberInUse},
		{"a number a queue's account holds", "beat@contoso.example", "+41445550000", "direct-routing", teamsphone.CodeNumberInUse},
		{"a Calling Plan number the tenant lacks", "beat@contoso.example", "+41449999999", "calling-plan", teamsphone.CodeNumberNotFound},
		{"an Operator Connect number the tenant lacks", "beat@contoso.example", "+41449999999", "operator-connect", teamsphone.CodeNumberNotFound},
	}
	for _, r := range refusals {
		_, err := ensure(r.identity, r.number, r.kind)
		var refusal *teamsphone.Refusal
		if !errors.As(err, &refusal) || refusal.Code != r.code {
			t.Errorf("%s: %v; want a refusal coded %s", r.name, err, r.code)
		}
	}
	if got, _ := ensure("anna@contoso.example", "+41441110001", "calling-plan"); got["assigned"] != false {
		t.Errorf("a refused request moved a number: anna now answers %v", got)
	}

	// The leaver path: a named number is taken away from its holder, all of them without a
	// name, and a repeat answers removed=false.
	removals := []struct {
		name string
		args map[string]any
		want map[string]any
	}{
		{"a number its holder does not have", map[string]any{"identity": "anna@contoso.example", "phoneNumber": "+41441110000"},
			map[string]any{"removed": false, "phoneNumbers": []string{}}},
		{"the holder's number", map[string]any{"identity": "anna@contoso.example", "phoneNumber": "+41 44 111 00 01"},
			map[string]any{"removed": true, "phoneNumbers": []string{"+41441110001"}}},
		{"every number of an account", map[string]any{"identity": cq},
			map[string]any{"removed": true, "phoneNumbers": []string{"+41445550000"}}},
		{"again", map[string]any{"identity": cq},
			map[string]any{"removed": false, "phoneNumbers": []string{}}},
	}
	for _, r := range removals {
		got, err := run("remove-phone-number", r.args)
		if err != nil || !reflect.DeepEqual(got, r.want) {
			t.Errorf("remove-phone-number, %s: %v, %v; want %v", r.name, got, err, r.want)
		}
	}
	if got, err := ensure("anna@contoso.example", "+41441110001", "calling-plan"); err != nil || got["assigned"] != true {
		t.Errorf("a removed number could not be given out again: %v, %v", got, err)
	}
	if _, err := ensure("nobody@contoso.example", "+41445550001", "direct-routing"); err == nil {
		t.Error("a number was given to somebody the tenant does not know")
	}
}

// A resource account that is deleted releases its phone numbers first: a number left on a
// deleted account is a number nobody can give out again until Teams notices. So the
// leaver path is one task, and the answer says which numbers went with the account.
func TestRemovingAResourceAccountReleasesItsNumbers(t *testing.T) {
	m := teamsphone.NewMockTenant("anna@contoso.example")
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
	id := run("ensure-resource-account", map[string]any{"userPrincipalName": "cq_x@contoso.example", "displayName": "X", "accountType": "call-queue"})["objectId"].(string)
	run("ensure-phone-number", map[string]any{"identity": id, "phoneNumber": "+41445550000", "numberType": "direct-routing"})
	got := run("remove-resource-account", map[string]any{"resourceAccountId": id})
	if want := map[string]any{"removed": true, "objectId": id, "phoneNumbers": []string{"+41445550000"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("remove-resource-account = %v, want %v", got, want)
	}
	if again := run("remove-resource-account", map[string]any{"resourceAccountId": id}); again["removed"] != false || !reflect.DeepEqual(again["phoneNumbers"], []string{}) {
		t.Errorf("remove-resource-account repeated = %v, want removed=false and no numbers", again)
	}
	if out := run("ensure-phone-number", map[string]any{"identity": "anna@contoso.example", "phoneNumber": "+41445550000", "numberType": "direct-routing"}); out["assigned"] != true {
		t.Errorf("the released number could not be given out again: %v", out)
	}
}

// remove-hotline takes a whole hotline away in one task, in the one order that never
// leaves a caller ringing into nothing: the auto attendants first, then the queues they
// hand calls to, then each account's numbers and the account itself. It refuses before
// changing anything when a voice app also answers for an account the hotline does not
// name, finds a voice app an earlier run left without its account by name, and answers
// removed=false the second time.
func TestRemoveHotlineTakesTheWholeHotlineAway(t *testing.T) {
	m := teamsphone.NewMockTenant("anna@contoso.example")
	reg := teamsphone.NewRegistry()
	reg.Register("contoso", m)
	run := func(op string, args map[string]any) (map[string]any, error) {
		out, err := teamsphone.Run(context.Background(), teamsphone.Job{Connector: "contoso", Operation: op, Args: args, ResultVariable: "r"}, reg)
		if err != nil {
			return nil, err
		}
		return out["r"].(map[string]any), nil
	}
	must := func(op string, args map[string]any) map[string]any {
		t.Helper()
		out, err := run(op, args)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return out
	}
	account := func(upn, kind string) string {
		return must("ensure-resource-account", map[string]any{"userPrincipalName": upn, "displayName": "Support", "accountType": kind})["objectId"].(string)
	}
	hotline := func(suffix string) (cq, aa, queue, attendant string) {
		cq, aa = account("cq_"+suffix+"@contoso.example", "call-queue"), account("aa_"+suffix+"@contoso.example", "auto-attendant")
		queue = must("ensure-call-queue", map[string]any{"name": "CQ " + suffix, "resourceAccountId": cq, "agents": "anna@contoso.example"})["id"].(string)
		attendant = must("ensure-auto-attendant", map[string]any{"name": "AA " + suffix, "resourceAccountId": aa, "transferTarget": cq,
			"languageId": "de-CH", "timeZone": "W. Europe Standard Time"})["id"].(string)
		must("ensure-phone-number", map[string]any{"identity": aa, "phoneNumber": "+4144555" + suffix, "numberType": "direct-routing"})
		return
	}
	cq, aa, queue, attendant := hotline("0001")

	// A queue that also answers for an account the hotline does not name is somebody
	// else's too: nothing is removed.
	other := account("cq_other@contoso.example", "call-queue")
	must("ensure-call-queue", map[string]any{"name": "CQ 0001", "resourceAccountId": other})
	_, err := run("remove-hotline", map[string]any{"resourceAccountIds": []any{aa, cq}})
	var refusal *teamsphone.Refusal
	if !errors.As(err, &refusal) || refusal.Code != teamsphone.CodeAccountInUse || !strings.Contains(err.Error(), other) {
		t.Errorf("a hotline whose queue also answers for another account: %v; want it refused as %s, naming that account", err, teamsphone.CodeAccountInUse)
	}
	if got := must("ensure-auto-attendant", map[string]any{"name": "AA 0001", "resourceAccountId": aa, "transferTarget": cq,
		"languageId": "de-CH", "timeZone": "W. Europe Standard Time"}); got["created"] != false {
		t.Errorf("a refused remove-hotline took the auto attendant away: %v", got)
	}
	must("remove-resource-account", map[string]any{"resourceAccountId": other})

	got := must("remove-hotline", map[string]any{"resourceAccountIds": []any{cq, aa}})
	want := map[string]any{
		"removed": true, "attendants": []string{attendant}, "queues": []string{queue},
		"accounts": sorted(cq, aa), "phoneNumbers": []string{"+41445550001"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("remove-hotline = %v, want %v", got, want)
	}
	again := must("remove-hotline", map[string]any{"resourceAccountIds": []any{cq, aa}})
	if want := map[string]any{"removed": false, "attendants": []string{}, "queues": []string{}, "accounts": []string{}, "phoneNumbers": []string{}}; !reflect.DeepEqual(again, want) {
		t.Errorf("remove-hotline repeated = %v, want %v", again, want)
	}
	if out := must("ensure-phone-number", map[string]any{"identity": "anna@contoso.example", "phoneNumber": "+41445550001", "numberType": "direct-routing"}); out["assigned"] != true {
		t.Errorf("the hotline's number was not released: %v", out)
	}

	// An earlier run removed the attendant's account and then failed: the attendant is
	// left without an account, and only its name finds it again.
	cq2, aa2, queue2, attendant2 := hotline("0002")
	must("remove-resource-account", map[string]any{"resourceAccountId": aa2})
	got = must("remove-hotline", map[string]any{"resourceAccountIds": []any{cq2, aa2}, "names": "AA 0002, CQ 0002"})
	if got["removed"] != true || !reflect.DeepEqual(got["attendants"], []string{attendant2}) || !reflect.DeepEqual(got["queues"], []string{queue2}) ||
		!reflect.DeepEqual(got["accounts"], []string{cq2}) {
		t.Errorf("remove-hotline after a failed run = %v, want the orphaned attendant %s, the queue %s and the account %s", got, attendant2, queue2, cq2)
	}
}

func sorted(ids ...string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

func attendant(holidays string) teamsphone.Job {
	return teamsphone.Job{Operation: "ensure-auto-attendant", Args: map[string]any{
		"name": "AA Support", "resourceAccountId": "r", "transferTarget": "q",
		"languageId": "de-CH", "timeZone": "W. Europe Standard Time", "holidays": holidays,
	}}
}

// A holiday without an end is the one day it starts on, and a FEEL list of contexts
// reads like the JSON text of one.
// list-free-phone-numbers offers the inventory's numbers nobody holds, sorted, under the
// prefix the model names, and says when it stopped short. A number given out is no longer
// offered, and one released is offered again.
func TestFreeNumbersAreTheInventorysUnheldOnes(t *testing.T) {
	m := teamsphone.NewMockTenant("anna@contoso.example").WithNumbers("+41 44 111 00 02", "+41441110000", "+41441110001", "+41442220000")
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
	free := func(extra map[string]any) map[string]any {
		args := map[string]any{"numberType": "calling-plan", "assignableTo": "user"}
		for k, v := range extra {
			args[k] = v
		}
		return run("list-free-phone-numbers", args)
	}
	run("ensure-phone-number", map[string]any{"identity": "anna@contoso.example", "phoneNumber": "+41441110001", "numberType": "calling-plan"})
	got := free(map[string]any{"numberPrefix": "+41 44 111"})
	if !slices.Equal(got["numbers"].([]string), []string{"+41441110000", "+41441110002"}) || got["count"] != 2 || got["truncated"] != false {
		t.Errorf("free numbers under +4144111 = %v; want the two nobody holds, sorted", got)
	}
	if entries := got["entries"].([]map[string]any); len(entries) != 2 || entries[0]["phoneNumber"] != "+41441110000" || entries[0]["numberType"] != "calling-plan" {
		t.Errorf("entries = %v; want one per number, in the same order, with its type", entries)
	}
	if got := free(map[string]any{"max": json.Number("2")}); !slices.Equal(got["numbers"].([]string), []string{"+41441110000", "+41441110002"}) || got["truncated"] != true {
		t.Errorf("two of three free numbers = %v; want the first two and truncated", got)
	}
	run("remove-phone-number", map[string]any{"identity": "anna@contoso.example"})
	if got := free(nil); got["count"] != 4 {
		t.Errorf("after the release = %v; want the released number offered again", got)
	}
	everything := teamsphone.NewRegistry()
	everything.Register("contoso", teamsphone.NewMockTenant())
	out, err := teamsphone.Run(context.Background(), teamsphone.Job{Connector: "contoso", Operation: "list-free-phone-numbers",
		Args: map[string]any{"numberType": "direct-routing", "assignableTo": "resource-account"}, ResultVariable: "r"}, everything)
	if err != nil || out["r"].(map[string]any)["count"] != 0 {
		t.Errorf("a mock tenant that has every number = %v, %v; want nothing to list", out, err)
	}
}

// What a free-number list may ask for: both settings, because a number listed for the
// wrong type or the wrong kind of holder is one Teams then refuses; a prefix read like a
// number; and a page of at most MaxFreeNumbers, DefaultFreeNumbers when none is named.
func TestAFreeNumberListIsBounded(t *testing.T) {
	prepare := func(args map[string]any) (teamsphone.Request, error) {
		return teamsphone.Prepare(teamsphone.Job{Operation: "list-free-phone-numbers", Args: args})
	}
	base := func(extra map[string]any) map[string]any {
		args := map[string]any{"numberType": "direct-routing", "assignableTo": "resource-account"}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}
	req, err := prepare(base(map[string]any{"numberPrefix": "+41 44 555"}))
	if err != nil || req.Args["max"] != teamsphone.DefaultFreeNumbers || req.Args["numberPrefix"] != "+4144555" || req.Args["assignableTo"] != "resource-account" {
		t.Errorf("Prepare = %v, %v; want the default page, the prefix as Teams takes it and the setting carried", req.Args, err)
	}
	if _, present := req.Args["identity"]; present {
		t.Errorf("Prepare carries %v; want only what the operation takes", req.Args)
	}
	for _, max := range []any{json.Number("500"), 1.0, "7"} {
		if _, err := prepare(base(map[string]any{"max": max})); err != nil {
			t.Errorf("max %v: %v; want it taken", max, err)
		}
	}
	for _, max := range []any{0.0, json.Number("501"), "2.5", "viele"} {
		if _, err := prepare(base(map[string]any{"max": max})); err == nil || !strings.Contains(err.Error(), "max") {
			t.Errorf("max %v: %v; want it refused, naming max", max, err)
		}
	}
	if _, err := prepare(base(map[string]any{"numberPrefix": "+41 (0)44"})); err == nil {
		t.Error("a prefix with (0) after the country code was taken; want it refused like a number")
	}
	for _, setting := range []string{"numberType", "assignableTo"} {
		args := base(nil)
		delete(args, setting)
		if _, err := prepare(args); err == nil || !strings.Contains(err.Error(), setting) {
			t.Errorf("without %s: %v; want it refused, naming it", setting, err)
		}
	}
}

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
	sample := map[teamsphone.Shape]string{teamsphone.Text: "x", teamsphone.List: "a,b", teamsphone.Holidays: `=[]`, teamsphone.PhoneNumber: "+41441234567", teamsphone.Limit: "10"}
	for name, shape := range teamsphone.Attributes {
		if sample[shape] == "" {
			t.Fatalf("no sample for %s's shape: an attribute without one reads as unset, and every operation would seem to refuse it", name)
		}
	}
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
				// not take would be dropped, and its absence read as "leave it". A
				// required value is always named, and a setting without a default always
				// authored, so neither could ever say "leave it".
				optional := slices.Contains(spec.Optional, name) ||
					(slices.Contains(spec.Settings, name) && teamsphone.Settings[name].Default != "")
				if !optional {
					t.Errorf("%s reconciles %s, which is neither an optional value nor a defaulted setting of it", op, name)
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

// The compiler knows this package's values, and leaves a defaulted setting the model did
// not author to this package: a setting that drifted would be compiled to one word and
// read as another, and a default the compiler invented would read as one the model named.
func TestTheCompilersSettingsAreThisPackages(t *testing.T) {
	for name, set := range teamsphone.Settings {
		op := "ensure-call-queue"
		base := `connector="c" operation="ensure-call-queue" name="n" resourceAccountId="r" resultVariable="q"`
		switch name {
		case "accountType":
			op = "ensure-resource-account"
			base = `connector="c" operation="ensure-resource-account" userPrincipalName="u" displayName="d" resultVariable="q"`
		case "numberType":
			op = "ensure-phone-number"
			base = `connector="c" operation="ensure-phone-number" identity="u" phoneNumber="+41441234567" resultVariable="q"`
		case "assignableTo":
			op = "list-free-phone-numbers"
			base = `connector="c" operation="list-free-phone-numbers" numberType="calling-plan" resultVariable="q"`
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
		for _, kv := range d.TeamsPhoneArgs {
			if kv.Name == name {
				t.Errorf("the compiler sends %s=%q, which the model did not author", name, kv.Val.Literal)
			}
		}
		req, err := teamsphone.Prepare(teamsphone.Job{Operation: op, Args: map[string]any{"name": "n", "resourceAccountId": "r"}})
		if err != nil {
			t.Fatalf("Prepare %s: %v", op, err)
		}
		want := any(set.Default)
		if set.Default == "true" || set.Default == "false" {
			want = set.Default == "true"
		}
		if req.Args[name] != want {
			t.Errorf("a new queue gets %s=%v, want this package's default %v", name, req.Args[name], want)
		}
	}
}
