package examples

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type monatsberichtClock struct{ t int64 }

func (c *monatsberichtClock) Now() int64 { return c.t }

// TestMonatsberichtNachfassen runs examples/monatsbericht through the engine: the
// distribution list is read from its text, each recipient gets the report as a task,
// completing it acknowledges it, the three-day deadline marks a recipient as not having
// read it, the person responsible adjusts the list, and the next round delivers the
// same snapshot to the adjusted list (ADR-0555 §5).
func TestMonatsberichtNachfassen(t *testing.T) {
	f, err := os.Open("monatsbericht/monatsbericht.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cp, err := compiler.Parse(70, 1, f)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	clk := &monatsberichtClock{t: time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC).UnixNano()}
	p := engine.New(1, log, store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		if err := p.RunUntilIdle(); err != nil {
			t.Fatal(err)
		}
	}
	jobs := func(jobType int32) []uint64 {
		t.Helper()
		var keys []uint64
		if err := store.ActivatableJobs(jobType, func(k uint64) error { keys = append(keys, k); return nil }); err != nil {
			t.Fatal(err)
		}
		return keys
	}
	visible := func(jobKey uint64, name string) string {
		t.Helper()
		j, ok, err := store.GetJob(jobKey)
		if err != nil || !ok {
			t.Fatalf("job %d: %v", jobKey, err)
		}
		vars, err := state.VisibleVariablesMap(store, j.ElementInstanceKey)
		if err != nil {
			t.Fatal(err)
		}
		return vars[name].Text
	}
	// Each user task waiting, by the recipient its form shows the report to.
	tasksByRecipient := func(name string) map[string]uint64 {
		t.Helper()
		out := map[string]uint64{}
		for _, k := range jobs(compiler.UserTaskJobTypeIndex) {
			out[visible(k, name)] = k
		}
		return out
	}
	str := func(name, v string) model.VariableValue {
		return model.VariableValue{Name: name, Kind: model.VarString, Text: v}
	}

	var pi uint64
	p.CreateInstanceReporting(cp.Key, &pi,
		str("verteilerText", " anna\nbert, anna\n\n"),
		str("verantwortlich", "chef"))
	run()

	// The two reports are read through the Atlas report Worker, one after the other.
	for _, name := range []string{"bericht", "incidents"} {
		ks := jobs(compiler.AtlasReportJobTypeIndex)
		if len(ks) != 1 {
			t.Fatalf("%s: %d report jobs waiting, want 1", name, len(ks))
		}
		p.CompleteJob(ks[0], model.VariableValue{Name: name, Kind: model.VarJSON,
			Text: `{"schema":"atlas.report/1","title":"` + name + `","generatedAt":"2026-11-02T08:00:00Z","datasets":{"active":{"unit":"count","value":3}}}`})
		run()
	}

	first := tasksByRecipient("empfaenger")
	if got := recipientsOf(first); len(got) != 2 || got[0] != "anna" || got[1] != "bert" {
		t.Fatalf("first round goes to %v, want [anna bert]: one task per distinct name, trimmed", got)
	}
	if v := visible(first["anna"], "bericht"); v == "" {
		t.Fatal("the report task does not see the report it shows")
	}

	// anna reads it; bert does not, and his deadline passes.
	p.CompleteUserTask(first["anna"], "anna", "")
	run()
	clk.t += (72*time.Hour + time.Minute).Nanoseconds()
	if err := p.TickTimers(); err != nil {
		t.Fatal(err)
	}
	run()

	adjust := tasksByRecipient("nichtGelesen")
	if len(adjust) != 1 {
		t.Fatalf("after the deadline %d tasks wait, want only the adjustment: %v", len(adjust), adjust)
	}
	var adjustKey uint64
	for who, k := range adjust {
		if who != "bert" {
			t.Fatalf("the adjustment names %q as not having read, want bert", who)
		}
		adjustKey = k
	}
	if got := visible(adjustKey, "verteilerText"); got != "bert" {
		t.Fatalf("the proposed list is %q, want the one who did not read", got)
	}

	// The responsible person sends it to bert's deputy instead.
	p.CompleteUserTask(adjustKey, "chef", "", str("verteilerText", "carla"), model.VariableValue{Name: "abschliessen", Kind: model.VarBool})
	run()
	second := tasksByRecipient("empfaenger")
	if got := recipientsOf(second); len(got) != 1 || got[0] != "carla" {
		t.Fatalf("second round goes to %v, want [carla]", got)
	}
	if jobs(compiler.AtlasReportJobTypeIndex) != nil {
		t.Fatal("the second round read the figures again; it must deliver the same snapshot")
	}
	p.CompleteUserTask(second["carla"], "carla", "")
	run()

	done := map[uint64]bool{}
	if err := store.CompletedProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error { done[k] = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !done[pi] {
		t.Fatal("the instance did not complete once everyone on the adjusted list had read the report")
	}
}

func recipientsOf(m map[string]uint64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
