package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// A caller that starts an instance can learn which one it started.
//
// The key is minted while the command is processed, so the API start could not
// know it and answered with the definition's key alone. Two creations folded into
// one batch are the case that matters: each caller must get its own instance and
// not whichever the batch happened to create last.
func TestCreateInstanceReportingNamesEachCallersInstance(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := linearProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	var a, b uint64
	p.CreateInstanceReporting(cp.Key, &a, model.VariableValue{Name: "who", Kind: model.VarString, Text: "a"})
	p.CreateInstanceReporting(cp.Key, &b, model.VariableValue{Name: "who", Kind: model.VarString, Text: "b"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if a == 0 || b == 0 || a == b {
		t.Fatalf("reported keys a=%d b=%d, want two distinct non-zero keys", a, b)
	}
	for key, want := range map[uint64]string{a: "a", b: "b"} {
		v := readVar(t, h.store, key, "who")
		if v == nil || v.Text != want {
			t.Errorf("instance %d carries who=%v, want %q: the key reported is not the "+
				"instance this caller started", key, v, want)
		}
	}
}

// TestCreateInstanceWithoutReportingIsUnchanged: the ordinary creation carries no
// pointer and writes nothing anywhere — the field is opt-in.
func TestCreateInstanceWithoutReportingIsUnchanged(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := linearProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n, _ := counts(t, h.store); n != 1 {
		t.Fatalf("active instances = %d, want 1", n)
	}
}
