package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/exchange/catalog"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// fakeBackend records the call it was asked to make and answers what the test says.
type fakeBackend struct {
	target catalog.Target
	inv    catalog.Invocation
	limit  int
	calls  int
	out    []any
	err    error
}

func (f *fakeBackend) Target() catalog.Target { return f.target }

func (f *fakeBackend) Invoke(_ context.Context, inv catalog.Invocation, limit int) ([]any, error) {
	f.inv, f.limit = inv, limit
	f.calls++
	return f.out, f.err
}

func regWith(name string, b Backend) *Registry {
	r := NewRegistry()
	r.Register(name, b)
	return r
}

func TestRunBuildsTheCallForTheWorkersTarget(t *testing.T) {
	online := &fakeBackend{target: catalog.Online, out: []any{map[string]any{"Identity": "info"}}}
	out, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "add-send-as", ResultVariable: "recht",
		Parameters: map[string]any{"Identity": "info@contoso.com", "User": "anna@contoso.com"},
	}, regWith("contoso", online))
	if err != nil {
		t.Fatal(err)
	}
	if online.inv.Cmdlet != "Add-RecipientPermission" || online.inv.Parameters["Trustee"] != "anna@contoso.com" {
		t.Errorf("online call = %#v", online.inv)
	}
	if !reflect.DeepEqual(out, map[string]any{"recht": map[string]any{"Identity": "info"}}) {
		t.Errorf("out = %#v", out)
	}

	onprem := &fakeBackend{target: catalog.OnPrem}
	if _, err := Run(context.Background(), Job{
		Connector: "intern", Operation: "add-send-as",
		Parameters: map[string]any{"Identity": "info", "User": "anna"},
	}, regWith("intern", onprem)); err != nil {
		t.Fatal(err)
	}
	if onprem.inv.Cmdlet != "Add-ADPermission" {
		t.Errorf("on-premises call = %#v", onprem.inv)
	}
}

// TestRunSizesAListingOnePastItsCap: the cmdlet is asked for one object more than the
// task may return, so an oversized listing is detected without reading all of it — and
// then fails rather than truncating, because a short list is a wrong answer a process
// would act on.
func TestRunSizesAListingOnePastItsCap(t *testing.T) {
	b := &fakeBackend{target: catalog.Online, out: []any{1, 2, 3}}
	out, err := Run(context.Background(), Job{Connector: "c", Operation: "list-mailboxes", MaxResults: 3, ResultVariable: "r"}, regWith("c", b))
	if err != nil {
		t.Fatal(err)
	}
	if b.inv.Parameters[catalog.ResultSizeParameter] != 4 || b.limit != 4 {
		t.Errorf("ResultSize %v limit %d", b.inv.Parameters[catalog.ResultSizeParameter], b.limit)
	}
	if got := out["r"].([]any); len(got) != 3 {
		t.Errorf("out = %v", out)
	}

	b.out = []any{1, 2, 3, 4}
	_, err = Run(context.Background(), Job{Connector: "c", Operation: "list-mailboxes", MaxResults: 3, ResultVariable: "r"}, regWith("c", b))
	if err == nil || !strings.Contains(err.Error(), "more than the 3") {
		t.Errorf("err = %v", err)
	}

	// A listing whose cmdlet takes no ResultSize is still bounded, by the backend.
	b.out = []any{}
	if _, err := Run(context.Background(), Job{Connector: "c", Operation: "list-folder-permissions", MaxResults: 10, ResultVariable: "r", Parameters: map[string]any{"Identity": `anna:\Kalender`}}, regWith("c", b)); err != nil {
		t.Fatal(err)
	}
	if _, sized := b.inv.Parameters[catalog.ResultSizeParameter]; sized || b.limit != 11 {
		t.Errorf("unsized listing: params %v limit %d", b.inv.Parameters, b.limit)
	}
}

// TestRunAnEmptyListingIsAnEmptyList: a process asking "who has access" must be able
// to tell "nobody" from "no answer".
func TestRunAnEmptyListingIsAnEmptyList(t *testing.T) {
	b := &fakeBackend{target: catalog.Online}
	out, err := Run(context.Background(), Job{Connector: "c", Operation: "list-mailbox-permissions", MaxResults: 5, ResultVariable: "r", Parameters: map[string]any{"Identity": "x"}}, regWith("c", b))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := out["r"].([]any); !ok || got == nil || len(got) != 0 {
		t.Errorf("out = %#v", out)
	}
}

// TestRunASingleReadIsOneObject: "the" mailbox is one object or none — and an identity
// that matched several is refused, since the process would otherwise go on with
// whichever came first.
func TestRunASingleReadIsOneObject(t *testing.T) {
	b := &fakeBackend{target: catalog.Online}
	job := Job{Connector: "c", Operation: "get-mailbox", ResultVariable: "m", Parameters: map[string]any{"Identity": "Anna"}}
	out, err := Run(context.Background(), job, regWith("c", b))
	if err != nil || out["m"] != nil {
		t.Errorf("none: out %v err %v", out, err)
	}
	if _, present := out["m"]; !present {
		t.Error("a read that found nothing must still write its variable, as null")
	}
	b.out = []any{map[string]any{"Name": "Anna Muster"}, map[string]any{"Name": "Anna Beispiel"}}
	_, err = Run(context.Background(), job, regWith("c", b))
	if err == nil || !strings.Contains(err.Error(), "matched 2") {
		t.Errorf("err = %v", err)
	}
}

// TestRunAChangeIsNotFailedAfterTheFact: a grant may answer with several entries (an
// Active Directory permission is one per right), and failing the job then would report a
// change that was made as one that was not — and its retry would meet "already present".
func TestRunAChangeIsNotFailedAfterTheFact(t *testing.T) {
	b := &fakeBackend{target: catalog.OnPrem, out: []any{map[string]any{"ExtendedRights": "Send As"}, map[string]any{"ExtendedRights": "Send As"}}}
	out, err := Run(context.Background(), Job{Connector: "c", Operation: "add-send-as", ResultVariable: "r", Parameters: map[string]any{"Identity": "info", "User": "anna"}}, regWith("c", b))
	if err != nil {
		t.Fatalf("a change that succeeded failed the job: %v", err)
	}
	if got, ok := out["r"].([]any); !ok || len(got) != 2 {
		t.Errorf("out = %#v, want both entries kept", out)
	}
}

func TestRunWriteWithoutResult(t *testing.T) {
	b := &fakeBackend{target: catalog.OnPrem, out: []any{"ignored"}}
	out, err := Run(context.Background(), Job{Connector: "c", Operation: "remove-mailbox", Parameters: map[string]any{"Identity": "x"}}, regWith("c", b))
	if err != nil || out != nil {
		t.Errorf("out %v err %v", out, err)
	}
	if b.limit != 0 {
		t.Errorf("a change was limited to %d objects", b.limit)
	}
	// A create returns the object it made, for a model that keeps it.
	b.out = []any{map[string]any{"Name": "Info"}}
	out, err = Run(context.Background(), Job{Connector: "c", Operation: "create-shared-mailbox", Parameters: map[string]any{"Name": "Info"}}, regWith("c", b))
	if err != nil || out != nil {
		t.Errorf("without resultVariable: out %v err %v", out, err)
	}
}

func TestRunGenericRead(t *testing.T) {
	b := &fakeBackend{target: catalog.OnPrem, out: []any{map[string]any{"User": "anna"}}}
	out, err := Run(context.Background(), Job{Connector: "c", Operation: catalog.GenericOp, Cmdlet: "Get-ADPermission", MaxResults: 50, ResultVariable: "r", Parameters: map[string]any{"Identity": "info"}}, regWith("c", b))
	if err != nil {
		t.Fatal(err)
	}
	if b.inv.Cmdlet != "Get-ADPermission" || b.limit != 51 {
		t.Errorf("call %#v limit %d", b.inv, b.limit)
	}
	if _, sized := b.inv.Parameters[catalog.ResultSizeParameter]; sized {
		t.Error("the generic read cannot know whether its cmdlet takes ResultSize")
	}
	if len(out["r"].([]any)) != 1 {
		t.Errorf("out = %v", out)
	}
}

func TestRunRefusals(t *testing.T) {
	b := &fakeBackend{target: catalog.OnPrem}
	reg := regWith("c", b)
	reg.ReplaceWith(map[string]Backend{"c": b}, map[string]string{"kaputt": "no connection URI"})
	cases := []struct {
		name string
		job  Job
		want string
	}{
		{"unknown worker", Job{Connector: "x", Operation: "get-mailbox"}, `no worker registered as "x"`},
		{"broken worker", Job{Connector: "kaputt", Operation: "get-mailbox"}, "no connection URI"},
		{"unknown operation", Job{Connector: "c", Operation: "new-mailbox"}, "unknown operation"},
		{"missing parameter", Job{Connector: "c", Operation: "add-full-access", Parameters: map[string]any{"Identity": "x"}}, "needs User"},
		{"not on this target", Job{Connector: "c", Operation: "list-send-as", ResultVariable: "r", Parameters: map[string]any{"Identity": "x"}}, "Get-ADPermission"},
		{"generic write", Job{Connector: "c", Operation: catalog.GenericOp, Cmdlet: "Set-Mailbox"}, "Get- cmdlets only"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b.calls = 0
			_, err := Run(context.Background(), c.job, reg)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
			if b.calls != 0 {
				t.Error("a refused job reached Exchange")
			}
		})
	}
	b.err = errors.New("exchange: Get-Mailbox failed: nope")
	if _, err := Run(context.Background(), Job{Connector: "c", Operation: "get-mailbox", ResultVariable: "r", Parameters: map[string]any{"Identity": "x"}}, reg); err == nil {
		t.Error("an Exchange error must fail the job")
	}
}

// --- the engine half ---

type fakeVarStore struct {
	vars map[uint64][]model.VariableValue
}

func (f fakeVarStore) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	for i := range f.vars[scope] {
		v := f.vars[scope][i]
		if err := fn(&v); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeVarStore) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, nil
}

func compileTask(t *testing.T, attrs string) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
	t.Helper()
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:exchangeConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	return cp, cp.ConnectorTask(node.Detail)
}

func TestResolveEvaluatesTheParameterTemplate(t *testing.T) {
	cp, d := compileTask(t, `connector="=organisation" operation="add-full-access" parameters='{"Identity":"=postfach","User":"=person.mail","AutoMapping":false}' resultVariable="ace"`)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{10: {
		{Name: "organisation", Kind: model.VarString, Text: "contoso"},
		{Name: "postfach", Kind: model.VarString, Text: "info@contoso.com"},
		{Name: "person", Kind: model.VarJSON, Text: `{"mail":"anna@contoso.com"}`},
	}}}
	j, err := Resolve(store, cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := Job{Connector: "contoso", Operation: "add-full-access", ResultVariable: "ace",
		Parameters: map[string]any{"Identity": "info@contoso.com", "User": "anna@contoso.com", "AutoMapping": false}}
	if !reflect.DeepEqual(j, want) {
		t.Errorf("job = %#v\nwant  %#v", j, want)
	}
	// The resolved job is all a worker receives, and it has nowhere to carry a
	// credential.
	raw, _ := json.Marshal(j)
	for _, forbidden := range []string{"secret", "password", "certificate"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Errorf("the resolved job carries %q: %s", forbidden, raw)
		}
	}
}

func TestResolveReadsTheParametersVariable(t *testing.T) {
	cp, d := compileTask(t, `connector="c" operation="update-mailbox" parametersVariable="aenderung"`)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{10: {
		{Name: "aenderung", Kind: model.VarJSON, Text: `{"Identity":"anna","CustomAttribute1":"4711","IssueWarningQuota":10}`},
	}}}
	j, err := Resolve(store, cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if j.Parameters["CustomAttribute1"] != "4711" || j.Parameters["IssueWarningQuota"] != json.Number("10") {
		t.Errorf("parameters = %#v", j.Parameters)
	}
}

func TestResolveCarriesTheListingShape(t *testing.T) {
	cp, d := compileTask(t, `connector="c" operation="run-get-cmdlet" cmdlet="Get-ADPermission" maxResults="20" resultVariable="r"`)
	j, err := Resolve(fakeVarStore{}, cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if j.Cmdlet != "Get-ADPermission" || j.MaxResults != 20 || j.Parameters != nil {
		t.Errorf("job = %#v", j)
	}
}

func TestResolveRefusals(t *testing.T) {
	cases := []struct {
		name, attrs string
		vars        []model.VariableValue
		want        string
	}{
		{"variable not set", `connector="c" operation="update-mailbox" parametersVariable="p"`, nil, `"p" is not set`},
		{"variable not an object", `connector="c" operation="update-mailbox" parametersVariable="p"`,
			[]model.VariableValue{{Name: "p", Kind: model.VarString, Text: "anna"}}, "object"},
		{"variable is a list", `connector="c" operation="update-mailbox" parametersVariable="p"`,
			[]model.VariableValue{{Name: "p", Kind: model.VarJSON, Text: `["anna"]`}}, "object"},
		{"worker expression empty", `connector="=org" operation="remove-mailbox" parameters='{"Identity":"x"}'`, nil, "empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cp, d := compileTask(t, c.attrs)
			_, err := Resolve(fakeVarStore{vars: map[uint64][]model.VariableValue{10: c.vars}}, cp, d, 10)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
		})
	}
	if _, err := Resolve(fakeVarStore{}, nil, nil, 10); err == nil {
		t.Error("a task without detail resolved")
	}
}

// TestResolveBindsEveryVariableKind: a template reads booleans, numbers, strings and
// objects from the instance, and the instance's own key under processInstanceKey.
func TestResolveBindsEveryVariableKind(t *testing.T) {
	cp, d := compileTask(t, `connector="c" operation="update-mailbox" parameters='{"Identity":"=string(processInstanceKey)","HiddenFromAddressListsEnabled":"=versteckt","IssueWarningQuota":"=grenze","CustomAttribute1":"=person.kst","CustomAttribute2":"=fehlt"}'`)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{42: {
		{Name: "versteckt", Kind: model.VarBool, Bool: true},
		{Name: "grenze", Kind: model.VarNumber, Text: "10"},
		{Name: "person", Kind: model.VarJSON, Text: `{"kst":"4711"}`},
		{Name: "fehlt", Kind: model.VarNull},
	}}}
	j, err := Resolve(store, cp, d, 42)
	if err != nil {
		t.Fatal(err)
	}
	if j.Parameters["Identity"] != "42" || j.Parameters["HiddenFromAddressListsEnabled"] != true ||
		j.Parameters["IssueWarningQuota"] != json.Number("10") || j.Parameters["CustomAttribute1"] != "4711" ||
		j.Parameters["CustomAttribute2"] != nil {
		t.Errorf("parameters = %#v", j.Parameters)
	}
}

func TestResolveWorkerExpressionNamesTheOrganization(t *testing.T) {
	cp, d := compileTask(t, `connector="=org" operation="remove-mailbox" parameters='{"Identity":"x"}'`)
	j, err := Resolve(fakeVarStore{vars: map[uint64][]model.VariableValue{1: {{Name: "org", Kind: model.VarString, Text: "intern"}}}}, cp, d, 1)
	if err != nil || j.Connector != "intern" {
		t.Errorf("job %+v err %v", j, err)
	}
}

func TestParameterHelpersRefuseWhatIsNoObject(t *testing.T) {
	if _, err := decodeObject("null", "x"); err == nil || !strings.Contains(err.Error(), "null") {
		t.Errorf("null: %v", err)
	}
	if _, err := decodeObject("[1]", "x"); err == nil {
		t.Error("a list decoded as an object")
	}
	cp, d := compileTask(t, `connector="c" operation="update-mailbox" parameters='{"Identity":"x"}'`)
	// A template whose evaluation fails is reported, not sent half-built.
	_ = cp
	if _, err := evalParameters(d.ExchangeParameters, 1, nil); err != nil {
		t.Errorf("a literal template failed: %v", err)
	}
	if got := resolveValue(compiler.RestExpr{Literal: "lit"}, 1, nil); got != "lit" {
		t.Errorf("literal = %q", got)
	}
	if bindVars(1, nil, nil) != nil {
		t.Error("no names must bind nothing")
	}
	for kind, want := range map[model.VarKind]expr.ValueKind{model.VarBool: expr.KindBool, model.VarNumber: expr.KindNumber, model.VarString: expr.KindString, model.VarJSON: expr.KindJSON, model.VarNull: expr.KindNull} {
		if got := toExprKind(kind); got != want {
			t.Errorf("toExprKind(%v) = %v, want %v", kind, got, want)
		}
	}
}

func TestCappedKeepsTheHeadAndSaysItOverflowed(t *testing.T) {
	c := &capped{max: 4}
	if n, _ := c.Write([]byte("ab")); n != 2 || c.over {
		t.Fatal("under the cap")
	}
	if n, _ := c.Write([]byte("cdef")); n != 4 || !c.over || c.String() != "abcd" {
		t.Errorf("at the cap: %q over=%v", c.String(), c.over)
	}
	c2 := &capped{max: 2}
	c2.Write([]byte("ab"))
	if c2.Write([]byte("c")); !c2.over || c2.String() != "ab" {
		t.Errorf("past a full buffer: %q over=%v", c2.String(), c2.over)
	}
}

func TestOnlineRefusesAContinuationThatIsNoURL(t *testing.T) {
	f := newFakeExchange(t, fakeReply{body: `{"value":[],"@odata.nextLink":"http://[::1"}`})
	if _, err := f.client("").Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0); err == nil || !strings.Contains(err.Error(), "not a URL") {
		t.Errorf("err = %v", err)
	}
}

// TestOnlineStopsAnEndlessListing: an endpoint that always offers another page fails
// the job by the page bound instead of holding the worker.
func TestOnlineStopsAnEndlessListing(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"value":[],"@odata.nextLink":"http://` + r.Host + `/again"}`))
	}))
	defer srv.Close()
	c := NewOnlineClient(OnlineConfig{Tokens: staticToken{tok: "t"}, BaseURL: srv.URL, Organization: "o", HTTPClient: srv.Client()})
	_, err := c.Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0)
	if err == nil || !strings.Contains(err.Error(), "another page") || calls != maxPages {
		t.Errorf("err %v after %d calls", err, calls)
	}
}

func TestStripAnnotationsLeavesScalars(t *testing.T) {
	if stripAnnotations("x") != "x" {
		t.Error("a scalar item was changed")
	}
}
