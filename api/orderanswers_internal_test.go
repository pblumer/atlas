package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// A position's answers reach its processes
// (ADR-0441): every field of the product's
// form as a variable of its own, sealed where the model declares it personal, never
// in place of what the order says, and not to Atlas's own approval models — whose
// approver reads them on the approval instead.

// parkingForm is a configuration form with two fields: one a model declares personal,
// one it does not.
const parkingForm = `{"id":"park-form","name":"Parkplatz","schema":{"type":"default","components":[
  {"type":"textfield","key":"kennzeichen","label":"Kontrollschild"},
  {"type":"textfield","key":"fahrzeug","label":"Fahrzeug"}]}}`

// parkingProvisionBPMN provisions a parking space by hand. It declares the plate
// personal data of the recipient, so the start act must seal it.
func parkingProvisionBPMN(id string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="` + id + `" isExecutable="true" atlas:personal="kennzeichen" atlas:dataSubject="recipient">
    <startEvent id="s"/><userTask id="zuteilen" name="Zuteilen"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="zuteilen"/>
    <sequenceFlow id="f2" sourceRef="zuteilen" targetRef="e"/>
  </process>
</definitions>`
}

// parkingOrder is an order of one parking space, with the orderer's answers — and two
// that nobody asked for: one under a key the form does not have, and one that tries
// to name a different recipient.
func parkingOrder(id string, approval order.Approval, process string) order.Order {
	return order.Order{ID: id, Orderer: "usr_ada", Recipient: "usr_ada",
		Lines: []order.Line{{ItemID: "park", Status: order.StatusPending, ProvisionProcess: process,
			DeprovisionProcess: process, Approval: approval, ConfigForm: "park-form",
			Config: map[string]string{"kennzeichen": "ZH 123456 Muster", "fahrzeug": "Velo",
				"stray": "not asked", "recipient": "usr_mallory"}}}}
}

// answersServer is a server with the forms, the processes and the vault an answered
// order needs.
func answersServer(t *testing.T, opts ...Option) *Server {
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
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/forms", parkingForm, "application/json"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("save form: %d %s", code, body)
	}
	for _, id := range []string{"prov-park", "eigene-genehmigung"} {
		if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", parkingProvisionBPMN(id), "application/xml"); code != http.StatusOK {
			t.Fatalf("deploy %s: %d %s", id, code, body)
		}
	}
	return srv
}

// startLine asks the order to start its one position and returns the instance.
func startLine(t *testing.T, srv *Server, orderID string) uint64 {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/orders/"+orderID+"/lines/park/start", "", "")
	var resp lineStartResp
	if code != http.StatusOK || json.Unmarshal(body, &resp) != nil || resp.InstanceKey == 0 {
		t.Fatalf("start %s = %d %s", orderID, code, body)
	}
	return resp.InstanceKey
}

// rootVars reads every variable of an instance's root scope as it is stored.
func rootVars(t *testing.T, srv *Server, instKey uint64) map[string]model.VariableValue {
	t.Helper()
	out := map[string]model.VariableValue{}
	srv.do(func() {
		_ = srv.store.VariablesOfScope(instKey, func(v *model.VariableValue) error {
			out[v.Name] = *v
			return nil
		})
	})
	return out
}

// TestTheProvisioningIsGivenTheAnswersAndTheModelSealsThem: the plate the model
// declares personal is stored sealed under the recipient's key, the vehicle it does
// not declare is stored as given, a key the form does not ask is not passed, and an
// answer named like one of the order's own variables does not replace it.
func TestTheProvisioningIsGivenTheAnswersAndTheModelSealsThem(t *testing.T) {
	srv := answersServer(t)
	orderCancelPathsSave(t, srv, parkingOrder("ord_a1", order.Approval{Kind: "none"}, "prov-park"))
	vars := rootVars(t, srv, startLine(t, srv, "ord_a1"))

	plate := vars["kennzeichen"]
	if !vault.IsEnciphered(plate.Text) || strings.Contains(plate.Text, "ZH 123456 Muster") {
		t.Fatalf("the plate is stored as %q, want it sealed", plate.Text)
	}
	if env, _ := vault.ParseEnvelope(plate.Text); env.Subject != "usr_ada" {
		t.Errorf("the plate is sealed under %q, want the recipient usr_ada", env.Subject)
	}
	if got := vars["fahrzeug"]; got.Kind != model.VarString || got.Text != "Velo" {
		t.Errorf("fahrzeug = %+v, want the answer as given", got)
	}
	if got := vars["recipient"].Text; got != "usr_ada" {
		t.Errorf("recipient = %q: an answer stood in for who the position is for", got)
	}
	if _, ok := vars["stray"]; ok {
		t.Error("an answer to a field the form does not have reached the process")
	}
}

// TestOnlyAnApprovalTheProductChoseIsGivenTheAnswers: an approval model the
// installation binds by name is the product's, like its provisioning, and is given
// the answers. Atlas's own approval models are not: they declare nothing, so the
// answers would sit in their history in the clear.
func TestOnlyAnApprovalTheProductChoseIsGivenTheAnswers(t *testing.T) {
	srv := answersServer(t, WithSystemProcesses())

	orderCancelPathsSave(t, srv, parkingOrder("ord_a2", order.Approval{Kind: "eigene-genehmigung", Ref: "alice"}, "prov-park"))
	if vars := rootVars(t, srv, startLine(t, srv, "ord_a2")); !vault.IsEnciphered(vars["kennzeichen"].Text) {
		t.Errorf("the product's own approval model was not given the plate: %+v", vars["kennzeichen"])
	}

	orderCancelPathsSave(t, srv, parkingOrder("ord_a3", order.Approval{Kind: "fixed", Ref: "alice"}, "prov-park"))
	vars := rootVars(t, srv, startLine(t, srv, "ord_a3"))
	if vars["approvalRef"].Text != "alice" {
		t.Fatalf("this is not the shipped approval's instance: %+v", vars)
	}
	for _, name := range []string{"kennzeichen", "fahrzeug"} {
		if _, ok := vars[name]; ok {
			t.Errorf("Atlas's own approval model was given %s", name)
		}
	}
}

// TestTheApproverReadsTheAnswersFromTheOrder: the approval carries the answers as
// the form labels them, in the form's order, and after them what the form no longer
// asks — the order is the record of what was answered — and says when they were
// corrected after the order was placed.
func TestTheApproverReadsTheAnswersFromTheOrder(t *testing.T) {
	srv := answersServer(t)
	line := parkingOrder("ord_a4", order.Approval{Kind: "none"}, "prov-park").Lines[0]
	got := srv.answersFor(line)
	want := []approvalAnswer{
		{Key: "kennzeichen", Label: "Kontrollschild", Value: "ZH 123456 Muster"},
		{Key: "fahrzeug", Label: "Fahrzeug", Value: "Velo"},
		{Key: "recipient", Value: "usr_mallory"},
		{Key: "stray", Value: "not asked"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("answers = %+v\nwant %+v", got, want)
	}

	line.ConfigForm = "gone"
	if got := srv.answersFor(line); len(got) != 4 || got[0].Label != "" {
		t.Errorf("with the form gone the answers are still the record: %+v", got)
	}
	if got := srv.answersFor(order.Line{}); got != nil {
		t.Errorf("a line without answers = %+v", got)
	}
	if vars, err := srv.orderAnswerVars(line, nil); err != nil || vars != nil {
		t.Errorf("with the form gone no key can be told asked: %+v %v", vars, err)
	}
}

// TestTheOrchestrationDoesNotReadTheAnswers: what /next answers is kept by the
// fulfilment orchestration as its own variables, in the clear, and it never reads a
// form; so the answers are not in it.
func TestTheOrchestrationDoesNotReadTheAnswers(t *testing.T) {
	srv := answersServer(t)
	orderCancelPathsSave(t, srv, parkingOrder("ord_a5", order.Approval{Kind: "none"}, "prov-park"))
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/orders/ord_a5/next", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"id":"park"`) {
		t.Fatalf("next = %d %s", code, body)
	}
	if strings.Contains(string(body), "ZH 123456") || strings.Contains(string(body), `"config"`) {
		t.Errorf("next hands the orchestration the answers: %s", body)
	}
}

// TestPublishingRefusesAFieldNamedLikeTheOrdersOwn: a configuration form with a field
// called recipient would have its answer left out at every start, and the product
// manager who named it would look for it in vain.
func TestPublishingRefusesAFieldNamedLikeTheOrdersOwn(t *testing.T) {
	srv := answersServer(t)
	clash := `{"id":"clash-form","name":"Clash","schema":{"type":"default","components":[
	  {"type":"textfield","key":"recipient","label":"Für wen"},{"type":"textfield","key":"orderId.x"}]}}`
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/forms", clash, "application/json"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("save form: %d %s", code, body)
	}
	look := processLookup{s: srv}
	items := []catalog.Item{
		{ID: "a", ConfigForm: "clash-form"}, {ID: "b", ConfigForm: "park-form"},
		{ID: "c", ConfigForm: "nowhere"}, {ID: "d"},
	}
	problems := catalog.OrderFormProblems(items, look)
	if len(problems) != 1 || problems[0].Item != "a" || !strings.Contains(problems[0].Message, "orderId, recipient") {
		t.Fatalf("problems = %+v, want product a refused for orderId and recipient", problems)
	}
	if keys, found := look.FormFields("park-form"); !found || fmt.Sprint(keys) != "[fahrzeug kennzeichen]" {
		t.Errorf("FormFields = %v %v", keys, found)
	}
	if catalog.OrderFormProblems(items, nil) != nil {
		t.Error("a service without a form lookup refused something")
	}
}

// TestEveryVariableTheOrderGivesIsReserved: the names a field may not take are the
// names the order's start acts actually set, so the list cannot fall behind them.
func TestEveryVariableTheOrderGivesIsReserved(t *testing.T) {
	srv := answersServer(t)
	o := parkingOrder("ord_a6", order.Approval{Kind: "fixed", Ref: "alice"}, "prov-park")
	names := []string{commandIDVar, "reason", progressOrderVar, progressPositionVar}
	for _, v := range srv.positionStartVars(o, o.Lines[0], "park") {
		names = append(names, v.Name)
	}
	for _, name := range names {
		if !catalog.OrderVariables[name] {
			t.Errorf("the order sets %s, which catalog.OrderVariables does not reserve", name)
		}
	}
}

// TestAReturnIsGivenTheAnswersToo: a return starts a process of its own, which has
// not seen the answers; freeing the parking space needs to know which car it was.
func TestAReturnIsGivenTheAnswersToo(t *testing.T) {
	srv := answersServer(t)
	o := parkingOrder("ord_a7", order.Approval{Kind: "none"}, "prov-park")
	o.Lines[0].Status = order.StatusDone
	orderCancelPathsSave(t, srv, o)

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/orders/ord_a7/lines/park/return", "", "")
	if code != http.StatusOK {
		t.Fatalf("return = %d %s", code, body)
	}
	var key uint64
	for _, in := range orderCancelPathsGet(t, srv, "ord_a7").Lines[0].Instances {
		if in.Operation == catalog.OpDeprovision {
			key = in.Key
		}
	}
	vars := rootVars(t, srv, key)
	if key == 0 || !vault.IsEnciphered(vars["kennzeichen"].Text) || vars["fahrzeug"].Text != "Velo" {
		t.Fatalf("the return's process (%d) was not given the answers: %+v", key, vars)
	}
}

// parkingLifecycleBPMN takes a per-operation action at a start of its own, and
// declares the plate personal like the provisioning.
const parkingLifecycleBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <message id="m_sperren" name="park.sperren"/>
  <process id="park-life" isExecutable="true" atlas:personal="kennzeichen" atlas:dataSubject="recipient">
    <startEvent id="sperren"><messageEventDefinition messageRef="m_sperren"/></startEvent>
    <userTask id="t" name="Sperren"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="sperren" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`

// TestAnActionThatStartsItsProcessIsGivenTheAnswers, under what the action itself
// was given: its own form's input is newer than the order's, and asked for this act.
func TestAnActionThatStartsItsProcessIsGivenTheAnswers(t *testing.T) {
	srv := answersServer(t)
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", parkingLifecycleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	o := parkingOrder("ord_a8", order.Approval{Kind: "none"}, "")
	l := &o.Lines[0]
	l.Status, l.ProvisionProcess, l.DeprovisionProcess = order.StatusDone, "", ""
	l.LifecycleProcess = "park-life"
	l.Actions = []catalog.Action{{Key: "sperren", Message: "park.sperren", Effect: catalog.EffectChange,
		Triggers: []string{catalog.TriggerCustomer}}}
	extra := []model.VariableValue{{Name: "fahrzeug", Kind: model.VarString, Text: "Lieferwagen"}}

	resp, code, msg := srv.fireAction(heldLine{ord: o, line: *l, position: "park"}, "ord_a8", "sperren", "c-1", "", extra)
	if code != http.StatusOK {
		t.Fatalf("action = %d %s", code, msg)
	}
	vars := rootVars(t, srv, resp.InstanceKey)
	if !vault.IsEnciphered(vars["kennzeichen"].Text) {
		t.Errorf("the action's process was not given the plate: %+v", vars["kennzeichen"])
	}
	if got := vars["fahrzeug"].Text; got != "Lieferwagen" {
		t.Errorf("fahrzeug = %q: the order's answer replaced what the action was given", got)
	}
}
