package capability

import (
	"strings"
	"testing"
)

func TestBundledRealizationsJoinTheMapAndNeverItsRecords(t *testing.T) {
	st, err := NewRealizationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(ApplicationRealizations{ApplicationID: "a1", ApplicationKey: "app", Realizations: []BundledRealization{
		{Capability: "konten", Kind: RealizationProcess, ProcessID: "p1"},
		{Capability: "konten", Kind: RealizationWorker, WorkerRef: "ucc_entra"},
	}}); err != nil {
		t.Fatal(err)
	}
	overlay, err := st.Overlay()
	if err != nil {
		t.Fatal(err)
	}
	own := Capability{Key: "konten", Name: "Konten", State: StateActive,
		Realizations: []Realization{{Kind: RealizationManual, Note: "von Hand"}, {Kind: RealizationProcess, ApplicationKey: "app", ProcessID: "p1"}}}
	got := WithBundledOne(own, overlay)
	if len(got.Realizations) != 3 {
		t.Fatalf("realizations = %+v, want the two own and the bundled worker (the bundled process is already stated)", got.Realizations)
	}
	if len(own.Realizations) != 2 {
		t.Fatal("WithBundledOne changed the record handed in")
	}
	if got.Realizations[2].BundledBy != "app" || got.Realizations[2].WorkerRef != "ucc_entra" {
		t.Fatalf("bundled = %+v", got.Realizations[2])
	}
	// What is read is never written: a record sent back as read loses what the bundle brought.
	NormalizeCapability(&got)
	if len(got.Realizations) != 2 {
		t.Fatalf("after normalize = %+v", got.Realizations)
	}
	if out := WithBundled([]Capability{{Key: "other"}}, overlay); len(out[0].Realizations) != 0 {
		t.Fatalf("another key got realizations: %+v", out)
	}
	var none *RealizationStore
	if o, err := none.Overlay(); err != nil || o != nil {
		t.Fatalf("a nil store reads %v, %v", o, err)
	}
}

func TestBundledRealizationsAreNormalizedAndChecked(t *testing.T) {
	list := NormalizeBundledRealizations([]BundledRealization{
		{Capability: " b ", Kind: "process", ProcessID: "p"},
		{Capability: "a", Kind: "worker", WorkerRef: "w"},
		{Capability: "b", Kind: "process", ProcessID: "p"},
	})
	if len(list) != 2 || list[0].Capability != "a" || list[1].Capability != "b" {
		t.Fatalf("normalized = %+v", list)
	}
	findings := ValidateBundledRealizations([]BundledRealization{
		{Capability: "Bad!", Kind: "process", ProcessID: "p"},
		{Capability: "k", Kind: "process", ProcessID: "elsewhere"},
		{Capability: "k", Kind: "worker"},
		{Capability: "k", Kind: "manual"},
	}, map[string]bool{"p": true})
	if len(findings) != 4 {
		t.Fatalf("findings = %v", findings)
	}
	if !strings.Contains(findings[3], "written on the capability") {
		t.Fatalf("a manual realization's finding = %q", findings[3])
	}
}
