package capability

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := Capability{Key: "loan-underwriting", Name: "Loan Underwriting", State: StateActive,
		Owner:    Owner{Name: "Head of Credit Risk", Role: "Head of Credit Risk"},
		Requires: []string{"credit-scoring"}}
	if err := store.Save(c); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get("loan-underwriting")
	if err != nil || !ok {
		t.Fatalf("Get: %v ok=%v", err, ok)
	}
	if got.Name != c.Name || got.Owner.Name != c.Owner.Name || len(got.Requires) != 1 {
		t.Errorf("round trip lost fields: %+v", got)
	}
}

// TestStoreFilenameIsTheKey is what makes the map readable on disk and in an export.
// A person opening a design-time archive should find `loan-underwriting.json`, not a
// hex string — that is the difference between a map that can be diffed in review and
// one that can only be read through the API.
func TestStoreFilenameIsTheKey(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(Capability{Key: "loan-underwriting", Name: "X"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "loan-underwriting.json")); err != nil {
		entries, _ := os.ReadDir(dir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected loan-underwriting.json, directory holds %v", names)
	}
}

// TestStoreRefusesAKeyThatCouldNameAPath is the store's own half of the key rule.
// Validation refuses such a key with a message; this is the guard underneath, so a
// path could not be written even by a caller that skipped validation.
func TestStoreRefusesAKeyThatCouldNameAPath(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../escape", "with/slash", "", "UPPER"} {
		if err := store.Save(Capability{Key: key, Name: "X"}); err == nil {
			t.Errorf("saved a record under key %q", key)
		}
		if _, ok, err := store.Get(key); ok || err != nil {
			t.Errorf("Get(%q) = ok %v, err %v; want a miss", key, ok, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the directory is not empty after four refused writes: %d entries", len(entries))
	}
}

func TestStoreListsByName(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Capability{
		{Key: "z", Name: "Address Check"},
		{Key: "a", Name: "underwriting"},
		{Key: "m", Name: "Billing"},
	} {
		if err := store.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Address Check", "Billing", "underwriting"}
	if len(all) != len(want) {
		t.Fatalf("got %d records", len(all))
	}
	for i, name := range want {
		if all[i].Name != name {
			t.Errorf("record %d is %q, want %q — the list is sorted by name, case-insensitively",
				i, all[i].Name, name)
		}
	}
}

func TestStoreDeleteIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(Capability{Key: "gone", Name: "Gone"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := store.Delete("gone"); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
	if _, ok, _ := store.Get("gone"); ok {
		t.Error("record survived deletion")
	}
}

func TestStreamStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStreamStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	v := ValueStream{Key: "consumer-loan", Name: "Consumer Loan", Stages: []Stage{
		{Key: "apply", Name: "Application submission", Capabilities: []string{"onboarding"}},
		{Key: "underwrite", Name: "Credit evaluation", Capabilities: []string{"underwriting"}},
	}}
	if err := store.Save(v); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get("consumer-loan")
	if err != nil || !ok {
		t.Fatalf("Get: %v ok=%v", err, ok)
	}
	if len(got.Stages) != 2 || got.Stages[0].Key != "apply" || got.Stages[1].Key != "underwrite" {
		t.Errorf("stage order is not preserved: %+v", got.Stages)
	}
	if _, err := os.Stat(filepath.Join(dir, "consumer-loan.json")); err != nil {
		t.Errorf("value stream is not filed under its key: %v", err)
	}
}

func TestStreamStoreRefusesAnUnsafeKey(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStreamStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ValueStream{Key: "../x", Name: "X"}); err == nil {
		t.Error("saved a value stream under a path-shaped key")
	}
}

// Two records sharing a name still list in a stable order, or a UI reshuffles between
// two reads of the same data.
func TestStoreBreaksANameTieByKey(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"z-team", "a-team"} {
		if err := store.Save(Capability{Key: key, Name: "Billing"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Key != "a-team" || all[1].Key != "z-team" {
		t.Errorf("order = %+v", all)
	}

	streamDir := t.TempDir()
	streams, err := NewStreamStore(streamDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"z-stream", "a-stream"} {
		if err := streams.Save(ValueStream{Key: key, Name: "Loan"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := streams.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "a-stream" || got[1].Key != "z-stream" {
		t.Errorf("order = %+v", got)
	}
}
