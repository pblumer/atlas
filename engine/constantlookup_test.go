package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// The server asks before it lets a person start a definition whose bound constant has
// no fallback: does the definition's application, or the installation, hold the name?
func TestTheProcessorSaysWhichConstantsItHolds(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := engine.New(7, h.log, h.store, &manualClock{})
	if p.Partition() != 7 {
		t.Errorf("Partition = %d, want 7", p.Partition())
	}
	const def = 42
	if p.HasApplicationConstant(def, "TENANT_ID") {
		t.Error("a definition filed under no application holds a constant")
	}
	p.SetDefinitionApplication(def, "ucc")
	if p.HasApplicationConstant(def, "TENANT_ID") {
		t.Error("an application with no constants holds one")
	}
	p.SetApplicationConstants("ucc", map[string]engine.ConstantValue{"TENANT_ID": {Kind: model.VarString, Text: "t"}})
	if !p.HasApplicationConstant(def, "TENANT_ID") || p.HasApplicationConstant(def, "OTHER") {
		t.Error("HasApplicationConstant does not follow the application's constants")
	}
	if p.HasServerConstant("REGION") {
		t.Error("an installation with no server constants holds one")
	}
	p.SetServerConstants(map[string]engine.ConstantValue{"REGION": {Kind: model.VarString, Text: "ch"}})
	if !p.HasServerConstant("REGION") {
		t.Error("HasServerConstant does not follow the server constants")
	}
}
