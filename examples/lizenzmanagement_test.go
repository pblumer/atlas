package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The Lizenzmanagement keeps the state of every licence contract in one long-running
// instance of proc_lz_vertragsbuch, which takes bookings as messages one after another
// (examples/lizenzmanagement/README.md, «Wo der Stand liegt»). These tests run the two
// books on a real engine and hold them to what the design promises: two orders that
// race for the last seat cannot both get it, a booking that comes while the book is
// busy waits in the buffer instead of being lost, and every change reaches the tenant's
// book.

const lzDir = "lizenzmanagement"

// lzClock is the host clock: the books compare dates with FEEL's today(), which reads
// the host's date, so the engine's timestamps should agree with it.
type lzClock struct{}

func (lzClock) Now() int64 { return time.Now().UnixNano() }

type lzEngine struct {
	t     *testing.T
	p     *engine.Processor
	store *state.Store
	procs map[uint64]*compiler.CompiledProcess
	byID  map[string]*compiler.CompiledProcess
	// sent records what each REST task of the books would have sent, by element id:
	// the publish body the task's input mappings built.
	sent []lzSent
	// deliver makes drain act as the server behind POST /api/v1/messages: a REST task
	// whose body is a message is published, with its time to live and id.
	deliver bool
}

// publish is what POST /api/v1/messages does with a body the books' REST tasks send.
func (e *lzEngine) publish(body map[string]any) {
	e.t.Helper()
	name, _ := body["name"].(string)
	if name == "" {
		return
	}
	key, _ := body["correlationKey"].(string)
	id, _ := body["messageId"].(string)
	ttl, _ := body["timeToLiveMs"].(float64)
	vars, _ := body["variables"].(map[string]any)
	var vv []model.VariableValue
	for n, v := range vars {
		vv = append(vv, lzJSON(e.t, n, v))
	}
	e.p.PublishMessageWithTTL(name, key, id, time.Duration(ttl)*time.Millisecond, vv...)
}

type lzSent struct {
	element string
	body    map[string]any
}

func newLzEngine(t *testing.T, files ...string) *lzEngine {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = log.Close() })
	e := &lzEngine{t: t, p: engine.New(1, log, store, lzClock{}), store: store,
		procs: map[uint64]*compiler.CompiledProcess{}, byID: map[string]*compiler.CompiledProcess{}}
	for i, f := range files {
		src, err := os.Open(filepath.Join(lzDir, f))
		if err != nil {
			t.Fatal(err)
		}
		cp, err := compiler.Parse(uint64(100+i), 1, src)
		_ = src.Close()
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		e.p.Deploy(cp)
		e.procs[cp.Key] = cp
		e.byID[cp.ProcessId()] = cp
	}
	if err := e.p.Recover(); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *lzEngine) run() {
	e.t.Helper()
	if err := e.p.RunUntilIdle(); err != nil {
		e.t.Fatal(err)
	}
}

func lzJSON(t *testing.T, name string, v any) model.VariableValue {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return model.VariableValue{Name: name, Kind: model.VarJSON, Text: string(raw)}
}

func decodeVar(t *testing.T, v model.VariableValue) any {
	t.Helper()
	switch v.Kind {
	case model.VarJSON:
		var out any
		if err := json.Unmarshal([]byte(v.Text), &out); err != nil {
			t.Fatalf("variable %s: %v", v.Name, err)
		}
		return out
	case model.VarString:
		return v.Text
	case model.VarNumber:
		var n float64
		_ = json.Unmarshal([]byte(v.Text), &n)
		return n
	case model.VarBool:
		return v.Bool
	}
	return nil
}

// drain completes every REST job the books leave, recording what each would have
// sent, until none is left: the stand-in for the REST worker and for the server that
// would deliver the published message. It returns how many it completed.
func (e *lzEngine) drain() int {
	e.t.Helper()
	n := 0
	for round := 0; round < 200; round++ {
		var keys []uint64
		if err := e.store.AllActivatableJobs(func(k uint64) error { keys = append(keys, k); return nil }); err != nil {
			e.t.Fatal(err)
		}
		if len(keys) == 0 {
			return n
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		k := keys[0]
		jv, ok, err := e.store.GetJob(k)
		if err != nil || !ok {
			e.t.Fatalf("job %d: %v", k, err)
		}
		ei, ok, err := e.store.GetElementInstance(jv.ElementInstanceKey)
		if err != nil || !ok {
			e.t.Fatalf("element %d: %v", jv.ElementInstanceKey, err)
		}
		cp := e.procs[ei.ProcessDefKey]
		locals, err := state.LocalVariablesMap(e.store, jv.ElementInstanceKey)
		if err != nil {
			e.t.Fatal(err)
		}
		body := map[string]any{}
		for name, v := range locals {
			body[name] = decodeVar(e.t, v)
		}
		e.sent = append(e.sent, lzSent{element: cp.ElementBpmnId(ei.ElementId), body: body})
		if e.deliver {
			e.publish(body)
		}
		e.p.CompleteJob(k)
		e.run()
		n++
	}
	e.t.Fatal("the books keep producing jobs")
	return n
}

// answers are the answers the books published, keyed by request id.
func (e *lzEngine) answers() map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, s := range e.sent {
		if s.element != "st_antwort" {
			continue
		}
		vars, _ := s.body["variables"].(map[string]any)
		a, _ := vars["antwort"].(map[string]any)
		if id, _ := a["anfrageId"].(string); id != "" {
			out[id] = a
		}
	}
	return out
}

// rootVar reads a root-scope variable of the one active instance of process id.
func (e *lzEngine) rootVar(processID, name string) map[string]any {
	e.t.Helper()
	cp := e.byID[processID]
	var found map[string]any
	err := e.store.ActiveProcessInstances(func(key uint64, pi *model.ProcessInstanceValue) error {
		if pi.ProcessDefKey != cp.Key {
			return nil
		}
		vars, err := state.LocalVariablesMap(e.store, key)
		if err != nil {
			return err
		}
		if v, ok := vars[name]; ok {
			found, _ = decodeVar(e.t, v).(map[string]any)
		}
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if found == nil {
		e.t.Fatalf("no active %s holds %s", processID, name)
	}
	return found
}

func lzVertrag(menge int) map[string]any {
	return map[string]any{
		"nummer": "LV-4711", "kunde": "Muster AG", "projekt": nil, "psp": "P-1234-56-78",
		"beginn": "2020-01-01", "ende": "2099-12-31", "mengen": map[string]any{"E5": menge, "E3": 3},
		"integrationsManager": "im@muster.example", "exportForm": "csv", "exportAn": "finanzen@muster.example",
		"entzugsregel": "LzEntzugStandard",
	}
}

func (e *lzEngine) book(buch, id string, auftrag map[string]any) {
	if id != "" {
		auftrag["anfrageId"] = id
	}
	e.p.PublishMessageWithTTL("lz.buch.anfrage", buch, id, time.Hour, lzJSON(e.t, "auftrag", auftrag))
}

// TestTheContractBookGivesTheLastSeatOnce: three orders ask for a seat of a contract
// that has two, all at once, before the book has answered any of them. Exactly two get
// one; the third is told the contract is full, with the numbers.
func TestTheContractBookGivesTheLastSeatOnce(t *testing.T) {
	e := newLzEngine(t, "vertragsbuch.bpmn")
	e.p.PublishMessage("lz.vertragsbuch.anlegen", "", lzJSON(t, "vertrag", lzVertrag(2)))
	e.run()
	e.drain()

	for _, id := range []string{"q1", "q2", "q3"} {
		e.book("LV-4711", id, map[string]any{"aktion": "reservieren", "sku": "E5", "bezugId": "o-" + id + "/p1"})
	}
	e.run()
	e.drain()

	got := e.answers()
	reserved, full := 0, 0
	for _, id := range []string{"q1", "q2", "q3"} {
		a, ok := got[id]
		if !ok {
			t.Fatalf("request %s was never answered: a booking that arrives while the book is busy must wait, not vanish", id)
		}
		switch a["ergebnis"] {
		case "reserviert":
			reserved++
		case "voll":
			full++
			if a["grund"] == nil {
				t.Errorf("a full contract answers with the reason, got %v", a)
			}
		default:
			t.Errorf("request %s: unexpected answer %v", id, a)
		}
	}
	if reserved != 2 || full != 1 {
		t.Fatalf("two seats, three requests: want 2 reserved and 1 full, got %d and %d", reserved, full)
	}
	buch := e.rootVar("proc_lz_vertragsbuch", "buch")
	if res, _ := buch["reservierungen"].([]any); len(res) != 2 {
		t.Fatalf("the book holds %d reservations, want 2: %v", len(res), buch["reservierungen"])
	}

	// A repeated request of the same position is answered again, not booked twice.
	e.book("LV-4711", "q1b", map[string]any{"aktion": "reservieren", "sku": "E5", "bezugId": "o-q1/p1"})
	e.run()
	e.drain()
	if a := e.answers()["q1b"]; a["ergebnis"] != "reserviert" || a["reserviert"] != float64(2) {
		t.Fatalf("a repeated reservation must answer reserved without a second seat: %v", a)
	}

	// The first order is licensed: its reservation becomes a Bezug from today.
	e.book("LV-4711", "b1", map[string]any{"aktion": "belegen", "sku": "E5", "bezugId": "o-q1/p1",
		"orderId": "o-q1", "positionId": "p1", "konto": "00000000-0000-0000-0000-0000000000a1"})
	// The third order may overdraw once the tenant's responsible person agreed.
	e.book("LV-4711", "q3b", map[string]any{"aktion": "reservieren", "sku": "E5", "bezugId": "o-q3/p1", "ueberzug": true})
	e.run()
	e.drain()
	got = e.answers()
	if a := got["b1"]; a["ergebnis"] != "gebucht" || a["belegt"] != float64(1) || a["reserviert"] != float64(1) {
		t.Fatalf("belegen: want belegt 1, reserviert 1, got %v", a)
	}
	if a := got["q3b"]; a["ergebnis"] != "ueberzug" {
		t.Fatalf("an approved overdraft reserves beyond the amount: %v", a)
	}

	// Returning the licence ends the Bezug today; the seat is free again.
	e.book("LV-4711", "f1", map[string]any{"aktion": "freigeben", "bezugId": "o-q1/p1"})
	e.run()
	e.drain()
	if a := e.answers()["f1"]; a["ergebnis"] != "gebucht" {
		t.Fatalf("freigeben: %v", a)
	}
	buch = e.rootVar("proc_lz_vertragsbuch", "buch")
	bez, _ := buch["bezuege"].([]any)
	if len(bez) != 1 || bez[0].(map[string]any)["bis"] == nil {
		t.Fatalf("the returned licence keeps its Bezug with an end for the billing: %v", bez)
	}

	// Every change went to the tenant's book, without waiting for an answer.
	stands := 0
	for _, s := range e.sent {
		if s.element == "st_melden" || s.element == "st_melden0" {
			if s.body["correlationKey"] != "TENANT" {
				t.Errorf("a stand goes to the tenant's book: %v", s.body)
			}
			stands++
		}
	}
	if stands < 3 {
		t.Errorf("the opening, the Bezug, the overdraft and the return each report the stand; got %d reports", stands)
	}
}

// TestTheTenantBookSumsTheContracts: the tenant's book takes the contracts' stands and
// the daily inventory and answers what P1 and P5 ask of it.
func TestTheTenantBookSumsTheContracts(t *testing.T) {
	e := newLzEngine(t, "tenantbuch.bpmn")
	e.p.PublishMessage("lz.tenantbuch.eroeffnen", "")
	e.run()

	stand := func(nummer string, menge, belegt int) map[string]any {
		return map[string]any{"nummer": nummer, "zustand": "aktiv", "beginn": "2020-01-01", "ende": "2099-12-31",
			"skus":     []any{map[string]any{"sku": "E5", "menge": menge, "belegt": belegt, "reserviert": 0, "ueberzug": 0}},
			"bezugIds": []any{"o1/p1|E5"}}
	}
	e.book("TENANT", "", map[string]any{"aktion": "vertragsstand", "stand": stand("LV-1", 10, 4)})
	e.book("TENANT", "", map[string]any{"aktion": "vertragsstand", "stand": stand("LV-2", 5, 5)})
	e.book("TENANT", "", map[string]any{"aktion": "vertragsstand", "stand": stand("LV-1", 12, 4)})
	today := time.Now().Format("2006-01-02")
	e.book("TENANT", "s1", map[string]any{"aktion": "stand-speichern", "stand": map[string]any{"datum": today,
		"skus": []any{map[string]any{"sku": "E5", "gekauft": 20, "belegt": 9}}}})
	e.book("TENANT", "s2", map[string]any{"aktion": "schaetzen", "mengen": map[string]any{"E5": 6}})
	e.book("TENANT", "s3", map[string]any{"aktion": "wo", "bezugId": "o1/p1"})
	e.book("TENANT", "s4", map[string]any{"aktion": "nachkauf-oeffnen", "skus": []any{"E5"}})
	e.book("TENANT", "s5", map[string]any{"aktion": "nachkauf-oeffnen", "skus": []any{"E5"}})
	e.run()
	e.drain()
	got := e.answers()

	sum, _ := got["s2"]["summe"].([]any)
	if len(sum) != 1 {
		t.Fatalf("summe: %v", got["s2"])
	}
	s := sum[0].(map[string]any)
	if s["vertragsmenge"] != float64(17) || s["belegtAtlas"] != float64(9) || s["gekauft"] != float64(20) {
		t.Fatalf("a later stand of LV-1 replaces the earlier one: want 12+5 contracted, 9 used, 20 bought; got %v", s)
	}
	sch := got["s2"]["schaetzung"].([]any)[0].(map[string]any)
	if sch["frei"] != float64(-3) {
		t.Fatalf("20 bought, 17 contracted, 6 more asked: 3 missing in the EA, got %v", sch)
	}
	if wo, _ := got["s3"]["wo"].(map[string]any); wo["vertrag"] != "LV-2" || wo["sku"] != "E5" {
		t.Fatalf("wo finds the contract a position is booked on (the last stand naming it): %v", got["s3"])
	}
	if n, _ := got["s4"]["neueNachkaeufe"].([]any); len(n) != 1 {
		t.Fatalf("the first purchase task for E5 is opened: %v", got["s4"])
	}
	if n, _ := got["s5"]["neueNachkaeufe"].([]any); len(n) != 0 {
		t.Fatalf("a second one is not, while the first is open: %v", got["s5"])
	}
}

// lzRule compiles one of the Lizenzmanagement's tables and evaluates it.
func lzRule(t *testing.T, file, id string) func(in map[string]any) map[string]any {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(lzDir, file))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rule, err := dmn.CompileStandalone(ctx, src, id)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return func(in map[string]any) map[string]any {
		t.Helper()
		got, err := rule.Evaluate(ctx, in)
		if err != nil {
			t.Fatalf("%s %v: %v", id, in, err)
		}
		m, ok := got.(map[string]any)
		if !ok {
			// A table with one output answers with the value, not a context.
			return map[string]any{"_": got}
		}
		return m
	}
}

// num reads a decision's number the way a test compares it.
func num(v any) string { return fmt.Sprint(v) }

// TestTheLizenzmanagementTablesDecide holds each decision table to the rule its comment
// states, so a changed row that breaks the process is caught here and not at the
// anniversary.
func TestTheLizenzmanagementTablesDecide(t *testing.T) {
	sku := lzRule(t, "lz-sku.dmn", "LzSku")
	if m := sku(map[string]any{"variante": "erweitert", "skuId": nil}); m["kurz"] != "E5" {
		t.Errorf("LzSku variante erweitert: %v", m)
	}
	if m := sku(map[string]any{"variante": nil, "skuId": "66b55226-6b4f-492c-910c-a3b7a3c9d993"}); m["kurz"] != "F3" {
		t.Errorf("LzSku skuId of F3: %v", m)
	}

	psp := lzRule(t, "lz-psp-pruefung.dmn", "LzPspPruefung")
	for in, want := range map[string]bool{"P-1234-56-78": true, "K-123456": true, "P-12-3": false, "": false} {
		if m := psp(map[string]any{"psp": in}); m["gueltig"] != want {
			t.Errorf("LzPspPruefung %q: %v, want %v", in, m, want)
		}
	}

	k := lzRule(t, "lz-kontingent.dmn", "LzKontingent")
	for _, c := range []struct {
		ergebnis        string
		menge, ueberzug float64
		want            string
	}{
		{"reserviert", 10, 0, "reserviert"}, {"ueberzug", 10, 1, "reserviert"}, {"voll", 10, 2, "ueberzug-fragen"},
		{"voll", 10, 5, "abgelehnt"}, {"voll", 1, 1, "ueberzug-fragen"}, {"abgelehnt", 10, 0, "abgelehnt"},
		{"keine-antwort", 10, 0, "abgelehnt"},
	} {
		if m := k(map[string]any{"ergebnis": c.ergebnis, "menge": c.menge, "ueberzug": c.ueberzug}); m["weg"] != c.want {
			t.Errorf("LzKontingent %+v: %v", c, m)
		}
	}

	ab := lzRule(t, "lz-abgleich.dmn", "LzAbgleich")
	if m := ab(map[string]any{"belegtTenant": 12.0, "belegtAtlas": 12.0}); m["abweichung"] != false {
		t.Errorf("LzAbgleich equal: %v", m)
	}
	if m := ab(map[string]any{"belegtTenant": 14.0, "belegtAtlas": 12.0}); m["abweichung"] != true {
		t.Errorf("LzAbgleich more in the tenant: %v", m)
	}

	s := lzRule(t, "lz-nachkauf-schwelle.dmn", "LzNachkaufSchwelle")
	if m := s(map[string]any{"sku": "E3", "gekauft": 200.0, "frei": 30.0, "freiVertraege": 10.0}); m["nachkaufen"] != false {
		t.Errorf("E3 with 15 %% free buys nothing: %v", m)
	}
	if m := s(map[string]any{"sku": "E3", "gekauft": 200.0, "frei": 6.0, "freiVertraege": 10.0}); m["nachkaufen"] != true || num(m["empfehlung"]) != "14" {
		t.Errorf("E3 with 3 %% free buys up to 10 %%: %v", m)
	}
	if m := s(map[string]any{"sku": "E5", "gekauft": 50.0, "frei": 20.0, "freiVertraege": -4.0}); m["nachkaufen"] != true {
		t.Errorf("contracts beyond the purchase always buy: %v", m)
	}

	p := lzRule(t, "lz-preise.dmn", "LzPreise")
	if m := p(map[string]any{"sku": "E5", "monat": "2026-10-01"}); num(m["preis"]) != "57" {
		t.Errorf("LzPreise E5: %v", m)
	}
	if m := p(map[string]any{"sku": "X1", "monat": "2026-10-01"}); m != nil && m["preis"] != nil {
		t.Errorf("a SKU without a price must give no price, so the billing stops instead of charging 0: %v", m)
	}

	f := lzRule(t, "lz-export-form.dmn", "LzExportForm")
	if m := f(map[string]any{"form": "sap"}); m["trenner"] != "\t" {
		t.Errorf("LzExportForm sap: %q", m["trenner"])
	}

	e := lzRule(t, "lz-entzug-standard.dmn", "LzEntzugStandard")
	person := func(oe, fn string, extern bool, sku string, anmeldung any) map[string]any {
		return map[string]any{"organisationseinheit": oe, "funktion": fn, "tageSeitBezug": 100.0, "extern": extern,
			"sku": sku, "tageSeitAnmeldung": anmeldung, "bezogenSeit": "2026-01-01"}
	}
	for _, c := range []struct {
		in   map[string]any
		akt  string
		prio string
	}{
		{person("Geschäftsleitung", "Leiterin", true, "E5", 90.0), "behalten", "<nil>"},
		{person("Informatik", "Externer Berater", true, "E3", 1.0), "entziehen", "1"},
		{person("Informatik", "Entwickler", false, "E3", 90.0), "entziehen", "2"},
		{person("Informatik", "Entwickler", false, "E5", nil), "entziehen", "3"},
		{person("Informatik", "Entwickler", false, "E3", 3.0), "entziehen", "4"},
	} {
		if m := e(c.in); m["aktion"] != c.akt || num(m["prioritaet"]) != c.prio {
			t.Errorf("LzEntzugStandard %v: %v, want %s %v", c.in, m, c.akt, c.prio)
		}
	}

	z := lzRule(t, "lz-kunde-zuordnung.dmn", "LzKundeZuordnung")
	if m := z(map[string]any{"organisationseinheit": "Muster Informatik", "firma": nil}); m["_"] != "Muster AG" {
		t.Errorf("LzKundeZuordnung by unit: %v", m)
	}
	if m := z(map[string]any{"organisationseinheit": "Irgendwo", "firma": "Andere"}); m["_"] != "unbekannt" {
		t.Errorf("LzKundeZuordnung unknown: %v", m)
	}
}

// TestTheLizenzmanagementModelsValidate: every model of the package passes the deploy
// checks, not only the parse — a gateway without an outgoing condition, a call
// activity without a target, a timer that is not a schedule are refused at deploy.
func TestTheLizenzmanagementModelsValidate(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(lzDir, "*.bpmn"))
	if len(files) < 12 {
		t.Fatalf("found %d models in %s", len(files), lzDir)
	}
	for _, f := range files {
		src, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		cp, err := compiler.Parse(1, 1, src)
		_ = src.Close()
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		for _, p := range compiler.Validate(cp) {
			if os.Getenv("LZ_WARN") != "" {
				t.Logf("%s: %+v", f, p)
			}
			if p.Severity == compiler.SeverityError {
				t.Errorf("%s: %+v", f, p)
			}
		}
	}
}

// TestTwoOrdersAskingThroughTheHelperGetOneSeat runs the whole round trip a licence
// order makes: proc_lz_buchen publishes the request, the contract book takes it,
// answers by message, and the helper's receive task hands the answer back. Two
// helpers ask for the last seat at the same moment; one gets it.
func TestTwoOrdersAskingThroughTheHelperGetOneSeat(t *testing.T) {
	e := newLzEngine(t, "vertragsbuch.bpmn", "buchen.bpmn")
	e.deliver = true
	e.p.PublishMessage("lz.vertragsbuch.anlegen", "", lzJSON(t, "vertrag", lzVertrag(1)))
	e.run()
	e.drain()

	helper := e.byID["proc_lz_buchen"].Key
	for _, pos := range []string{"o-a/p1", "o-b/p1"} {
		e.p.CreateInstance(helper, model.VariableValue{Name: "buchId", Kind: model.VarString, Text: "LV-4711"},
			lzJSON(t, "auftrag", map[string]any{"aktion": "reservieren", "sku": "E5", "bezugId": pos, "ref": pos}))
	}
	e.run()
	e.drain()

	results := map[string]int{}
	err := e.store.CompletedProcessInstances(func(key uint64, pi *model.ProcessInstanceValue) error {
		if pi.ProcessDefKey != helper {
			return nil
		}
		vars, err := state.LocalVariablesMap(e.store, key)
		if err != nil {
			return err
		}
		a, _ := decodeVar(t, vars["antwort"]).(map[string]any)
		results[fmt.Sprint(a["ergebnis"])]++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if results["reserviert"] != 1 || results["voll"] != 1 {
		t.Fatalf("one seat, two helpers: want one reserved and one full, got %v", results)
	}
}
