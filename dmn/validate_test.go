package dmn_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// brokenModel is valid XML but its decision's FEEL literal does not compile, so
// temis reports an error diagnostic — the "resolved but invalid" case.
const brokenModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="d" name="broken" namespace="http://atlas/dmn">
  <decision id="Bad" name="Bad">
    <literalExpression id="le"><text>1 +</text></literalExpression>
  </decision>
</definitions>`

func writeModel(t *testing.T, dir, name, xml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(xml), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestValidatorValidModel(t *testing.T) {
	dir := t.TempDir()
	writeModel(t, dir, "dish.dmn", dishModel)
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	res, err := v.Validate(context.Background(), "dish")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Resolved || !res.Valid {
		t.Fatalf("res = %+v, want resolved and valid", res)
	}
	if res.ModelName != "dish" {
		t.Errorf("ModelName = %q, want dish", res.ModelName)
	}
	found := false
	for _, d := range res.Decisions {
		if d == "Dish" {
			found = true
		}
	}
	if !found {
		t.Errorf("Decisions = %v, want it to include Dish", res.Decisions)
	}
}

func TestValidatorInvalidModel(t *testing.T) {
	dir := t.TempDir()
	writeModel(t, dir, "broken.dmn", brokenModel)
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	res, err := v.Validate(context.Background(), "broken")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Resolved {
		t.Fatalf("res = %+v, want resolved (the XML was found)", res)
	}
	if res.Valid {
		t.Fatalf("res = %+v, want invalid (bad FEEL)", res)
	}
	if res.Message == "" {
		t.Error("want a non-empty diagnostic message for an invalid model")
	}
}

func TestValidatorUnresolved(t *testing.T) {
	v := dmn.NewValidator(dmn.DirResolver{Dir: t.TempDir()})
	res, err := v.Validate(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Resolved || res.Valid || res.Message == "" {
		t.Fatalf("res = %+v, want unresolved with a message", res)
	}
}

func TestValidatorInfraErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	// A directory where the model file is expected makes the resolver fail with a
	// real I/O error, which Validate must propagate (not swallow as "unresolved").
	if err := os.MkdirAll(filepath.Join(dir, "busy.dmn"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})
	if _, err := v.Validate(context.Background(), "busy"); err == nil {
		t.Fatal("Validate over a broken source: want an error")
	}
}
