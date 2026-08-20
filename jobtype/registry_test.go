package jobtype_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/jobtype"
)

func newRegistry(t *testing.T, dir string) *jobtype.Registry {
	t.Helper()
	r, err := jobtype.NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

// A reserved job type keeps the index its compiler constant documents. The whole
// point of the registry is that one table decides what an index means, so it must
// agree with the builder about the built-in half of that table rather than
// assigning its own indices to them.
func TestReservedNamesKeepTheirCompilerIndices(t *testing.T) {
	r := newRegistry(t, t.TempDir())
	for want, name := range compiler.ReservedJobTypes() {
		got, err := r.Intern(name)
		if err != nil {
			t.Fatalf("Intern(%q): %v", name, err)
		}
		if got != int32(want) {
			t.Errorf("Intern(%q) = %d, want the reserved %d", name, got, want)
		}
	}
}

// Reserved names are compile-time constants, so nothing about them belongs on
// disk: persisting them would let a stored copy drift from the constants and
// silently re-point every job written under the old index.
func TestReservedNamesAreNotPersisted(t *testing.T) {
	dir := t.TempDir()
	r := newRegistry(t, dir)
	if _, err := r.Intern(compiler.MailJobType); err != nil {
		t.Fatalf("Intern: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("interning a reserved name wrote %d file(s) into %s, want none", len(entries), dir)
	}
}

// A model-authored name is assigned from just past the reserved range and is
// stable for the life of the registry: the index is what jobs on disk carry, so a
// second call must never move it.
func TestModelAuthoredNamesAssignStableIndices(t *testing.T) {
	r := newRegistry(t, t.TempDir())

	first, err := r.Intern("send-email")
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	if want := compiler.FirstDynamicJobTypeIndex(); first != want {
		t.Errorf("first model-authored index = %d, want %d", first, want)
	}
	second, err := r.Intern("charge-card")
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	if second != first+1 {
		t.Errorf("second index = %d, want %d", second, first+1)
	}
	again, err := r.Intern("send-email")
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	if again != first {
		t.Errorf("re-interning send-email moved it from %d to %d", first, again)
	}
}

// The table outlives the process: a job written before a restart must still name
// the same job type after it, which is the property that makes the index durable
// rather than merely consistent within one run.
func TestIndicesSurviveAReopen(t *testing.T) {
	dir := t.TempDir()
	before := map[string]int32{}
	r := newRegistry(t, dir)
	for _, name := range []string{"send-email", "charge-card", "ship-order"} {
		idx, err := r.Intern(name)
		if err != nil {
			t.Fatalf("Intern(%q): %v", name, err)
		}
		before[name] = idx
	}

	reopened := newRegistry(t, dir)
	for name, want := range before {
		got, ok := reopened.Index(name)
		if !ok {
			t.Errorf("%q is missing after reopening", name)
			continue
		}
		if got != want {
			t.Errorf("%q = %d after reopening, want %d", name, got, want)
		}
	}
	// A name interned after the reopen must not reuse an index already handed out.
	next, err := reopened.Intern("refund")
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	for name, used := range before {
		if next == used {
			t.Errorf("a new name reused %d, already held by %q", next, name)
		}
	}
}

// An index is never recycled. If a record is removed from the directory by hand,
// the next assignment must still go past the highest index ever issued — reusing
// it would silently re-point jobs still on disk under the old meaning.
func TestIndicesAreNeverRecycled(t *testing.T) {
	dir := t.TempDir()
	r := newRegistry(t, dir)
	for _, name := range []string{"a", "b", "c"} {
		if _, err := r.Intern(name); err != nil {
			t.Fatalf("Intern(%q): %v", name, err)
		}
	}
	high, ok := r.Index("c")
	if !ok {
		t.Fatal("c missing")
	}
	// Remove the *first* record, leaving a hole below the high-water mark.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	removed := false
	for _, e := range entries {
		idx, err := indexInFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if idx == compiler.FirstDynamicJobTypeIndex() {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				t.Fatalf("remove: %v", err)
			}
			removed = true
			break
		}
	}
	if !removed {
		t.Fatal("did not find the lowest dynamic record to remove")
	}

	got, err := newRegistry(t, dir).Intern("d")
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	if got <= high {
		t.Errorf("a new name got %d, at or below the high-water mark %d — an index was recycled", got, high)
	}
}

// Name is the reverse direction, and it must answer for both halves of the table:
// the operator-facing views exist to turn an index on a job back into something
// readable.
func TestNameResolvesBothReservedAndDynamic(t *testing.T) {
	r := newRegistry(t, t.TempDir())
	idx, err := r.Intern("send-email")
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	for _, tc := range []struct {
		index int32
		want  string
	}{
		{compiler.MailJobTypeIndex, compiler.MailJobType},
		{compiler.DMNJobTypeIndex, compiler.DMNJobType},
		{idx, "send-email"},
	} {
		got, ok := r.Name(tc.index)
		if !ok {
			t.Errorf("Name(%d) not found, want %q", tc.index, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("Name(%d) = %q, want %q", tc.index, got, tc.want)
		}
	}
	if _, ok := r.Name(9999); ok {
		t.Error("Name(9999) reported a name for an index nothing was assigned")
	}
}

// An empty job type is not a job type. ResolveJobTypes skips them, so one arriving
// here means a caller is confused — say so rather than assigning it an index that
// every unnamed task would then share.
func TestInterningAnEmptyNameFails(t *testing.T) {
	r := newRegistry(t, t.TempDir())
	if _, err := r.Intern(""); err == nil {
		t.Error("Intern(\"\") succeeded, want an error")
	}
}

// Intern is the resolution seam a compiled process is handed, so it has to satisfy
// that signature directly rather than through an adapter at each call site.
func TestInternSatisfiesTheCompilerResolutionSeam(t *testing.T) {
	r := newRegistry(t, t.TempDir())
	b := compiler.NewBuilder(1, "orders", 1)
	node := b.AddServiceTask("send-email", 1)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := cp.ResolveJobTypes(r.Intern); err != nil {
		t.Fatalf("ResolveJobTypes: %v", err)
	}
	want, _ := r.Index("send-email")
	if got := cp.ServiceTask(cp.Node(node).Detail).GlobalJobType(); got != want {
		t.Errorf("GlobalJobType() = %d, want %d", got, want)
	}
}

// The reserved half of the table is defined by the compiler's constants, so a
// record on disk must never be able to re-point one — not a reserved *name* to a
// new index, and not a reserved *index* to a different name. Either would silently
// change what every job already written under that index means.
func TestStoredRecordsCannotRepointTheReservedRange(t *testing.T) {
	for _, tc := range []struct {
		label string
		entry jobtype.Entry
		check func(t *testing.T, r *jobtype.Registry)
	}{
		{
			label: "a reserved name pointed at a dynamic index",
			entry: jobtype.Entry{Name: compiler.MailJobType, Index: 99},
			check: func(t *testing.T, r *jobtype.Registry) {
				if got, _ := r.Index(compiler.MailJobType); got != compiler.MailJobTypeIndex {
					t.Errorf("Index(%q) = %d, want the reserved %d", compiler.MailJobType, got, compiler.MailJobTypeIndex)
				}
			},
		},
		{
			label: "a new name pointed into the reserved range",
			entry: jobtype.Entry{Name: "send-email", Index: compiler.TemisDecisionJobTypeIndex},
			check: func(t *testing.T, r *jobtype.Registry) {
				if got, ok := r.Name(compiler.TemisDecisionJobTypeIndex); !ok || got != compiler.TemisDecisionJobType {
					t.Errorf("Name(%d) = %q, want %q", compiler.TemisDecisionJobTypeIndex, got, compiler.TemisDecisionJobType)
				}
			},
		},
	} {
		t.Run(tc.label, func(t *testing.T) {
			dir := t.TempDir()
			writeRecord(t, dir, tc.entry)
			tc.check(t, newRegistry(t, dir))
		})
	}
}

// A directory that cannot be created is a startup failure, not something to carry
// on without: without the table, deploys would hand out per-process indices again.
func TestNewRegistryFailsWhenTheDirectoryIsUnusable(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := jobtype.NewRegistry(filepath.Join(blocked, "jobtypes")); err == nil {
		t.Error("NewRegistry succeeded with a file in the path, want an error")
	}
}

// A record that cannot be decoded must fail the same way: assigning fresh indices
// on top of an unreadable table would re-point whatever it held.
func TestNewRegistryFailsOnAnUnreadableRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, hex.EncodeToString([]byte("send-email"))+".json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := jobtype.NewRegistry(dir); err == nil {
		t.Error("NewRegistry succeeded over an undecodable record, want an error")
	}
}
