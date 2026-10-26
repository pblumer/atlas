package panorama

import (
	"os"
	"testing"
)

// TestApplicationModelsOnLoop covers the two hooks an application purge uses
// (ADR-0468): listing one application's
// models for the preview, and removing them with it — only its own, idempotently, and
// with a store that cannot be read reported rather than read as empty.
func TestApplicationModelsOnLoop(t *testing.T) {
	fx := newServiceFixture(t)
	for _, m := range []Model{
		{ID: "aa01", ApplicationID: "app-1", Name: "Landscape"},
		{ID: "aa02", ApplicationID: "app-1", Name: "Integration"},
		{ID: "bb01", ApplicationID: "other", Name: "Not ours"},
	} {
		if err := fx.store.Save(m); err != nil {
			t.Fatalf("save %s: %v", m.ID, err)
		}
	}

	listed, err := fx.service.ModelsForApplicationOnLoop("app-1")
	if err != nil || len(listed) != 2 {
		t.Fatalf("models of app-1 = %+v, %v; want two", listed, err)
	}
	for _, m := range listed {
		if m.ApplicationID != "app-1" || m.Name == "" {
			t.Errorf("listed %+v, want a named model of app-1", m)
		}
	}

	for range 2 { // the second removal finds nothing, which is what was asked for
		if err := fx.service.DeleteForApplicationOnLoop("app-1"); err != nil {
			t.Fatalf("delete models of app-1: %v", err)
		}
	}
	if left, _ := fx.service.ModelsForApplicationOnLoop("app-1"); len(left) != 0 {
		t.Errorf("app-1 still owns %+v", left)
	}
	if kept, _ := fx.service.ModelsForApplicationOnLoop("other"); len(kept) != 1 {
		t.Errorf("another application's models = %+v, want its one model kept", kept)
	}

	if err := os.RemoveAll(fx.store.Dir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx.store.Dir(), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.service.ModelsForApplicationOnLoop("other"); err == nil {
		t.Error("listing from an unreadable store reported no error")
	}
	if err := fx.service.DeleteForApplicationOnLoop("other"); err == nil {
		t.Error("deleting from an unreadable store reported no error")
	}
}
