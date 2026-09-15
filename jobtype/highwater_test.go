package jobtype_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/jobtype"
)

// The acceptance suite for the durable high-water mark
// (ADR-draft-the-job-type-index-space-never-goes-backwards).
//
// The guarantee under test is the one this package has always stated: an index,
// once issued, is never issued again. It is only visible across a reopen, because
// that is where the counter was rebuilt — so every case here opens a second
// registry over the same directory, which is what a server restart does.

// reopen is that restart: a fresh registry over a directory another one has
// written.
func reopen(t *testing.T, dir string) *jobtype.Registry {
	t.Helper()
	r, err := jobtype.NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func intern(t *testing.T, r *jobtype.Registry, name string) int32 {
	t.Helper()
	idx, err := r.Intern(name)
	if err != nil {
		t.Fatalf("Intern(%q): %v", name, err)
	}
	return idx
}

// removeRecord deletes one entry file the way a hand, a partial restore or a future
// prune would — the case the package doc names in as many words.
func removeRecord(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, hex.EncodeToString([]byte(name))+".json")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove %q: %v", name, err)
	}
}

// TestAnIndexIsNotReissuedAfterItsRecordIsRemoved is the headline, written as the
// corruption rather than as the counter.
//
// Before the mark, removing the highest entry and reopening handed that index
// straight to the next new job type — measured at 1001 for both. A job parked under
// 1001 carries the number, not the name, so a worker subscribed to the new type
// would have been handed the old type's work.
func TestAnIndexIsNotReissuedAfterItsRecordIsRemoved(t *testing.T) {
	dir := t.TempDir()
	r := reopen(t, dir)
	intern(t, r, "charge-card")
	sendEmail := intern(t, r, "send-email")

	removeRecord(t, dir, "send-email")

	again := reopen(t, dir)
	shipParcel := intern(t, again, "ship-parcel")
	if shipParcel == sendEmail {
		t.Fatalf("ship-parcel was issued %d, which send-email's parked jobs still carry", shipParcel)
	}
	if name, ok := again.Name(sendEmail); ok {
		t.Errorf("index %d now means %q; it was spent when it was issued", sendEmail, name)
	}
}

// TestTheMarkSurvivesEveryRecordBeingRemoved: with nothing on disk to derive a
// counter from, the mark is the only thing that remembers. This is the case the
// derivation got most wrong — an emptied table restarted at the floor.
func TestTheMarkSurvivesEveryRecordBeingRemoved(t *testing.T) {
	dir := t.TempDir()
	r := reopen(t, dir)
	issued := map[int32]string{}
	for _, name := range []string{"one", "two", "three"} {
		issued[intern(t, r, name)] = name
	}
	for name := range map[string]bool{"one": true, "two": true, "three": true} {
		removeRecord(t, dir, name)
	}

	again := reopen(t, dir)
	next := intern(t, again, "four")
	if was, taken := issued[next]; taken {
		t.Fatalf("four was issued %d, which %q had already been issued", next, was)
	}
}

// TestAnEntryTheLoadRefusesStillSpendsItsIndex needs no hand editing at all: it is
// what an upgrade does.
//
// A model author may write any task type, `io.atlas.` names included, and a later
// build may turn one of those names into a built-in. remember then refuses the
// stored entry — correctly, the constant owns that name now — and before this the
// refusal also dropped the entry's claim on its index, so a high index that
// model-authored jobs still carry went back into circulation.
func TestAnEntryTheLoadRefusesStillSpendsItsIndex(t *testing.T) {
	dir := t.TempDir()
	reserved := compiler.ReservedJobTypes()
	nowBuiltIn := reserved[len(reserved)-1]
	high := compiler.FirstDynamicJobTypeIndex() + 5
	seedStored(t, dir,
		jobtype.Entry{Name: "send-email", Index: compiler.FirstDynamicJobTypeIndex()},
		jobtype.Entry{Name: nowBuiltIn, Index: high},
	)

	r := reopen(t, dir)
	// The entry is still refused: the reserved name keeps its constant.
	if idx, ok := r.Index(nowBuiltIn); !ok || idx >= compiler.ReservedJobTypeCount() {
		t.Fatalf("%q resolves to %d (known %v); a reserved name keeps its built-in index", nowBuiltIn, idx, ok)
	}
	// But its index is spent all the same.
	for i := 0; i < 10; i++ {
		if idx := intern(t, r, "task-"+string(rune('a'+i))); idx == high {
			t.Fatalf("index %d was issued again, to a different name, while jobs carrying it mean %q", high, nowBuiltIn)
		}
	}
}

// TestAStorePredatingTheMarkGetsOneOnItsFirstOpen is the upgrade path, and the only
// moment the knowledge still exists: the entries say how far the table got, and they
// say so now. Waiting until the next Intern would let a record removed in between
// take its index with it.
func TestAStorePredatingTheMarkGetsOneOnItsFirstOpen(t *testing.T) {
	dir := t.TempDir()
	high := compiler.FirstDynamicJobTypeIndex() + 3
	seedStored(t, dir,
		jobtype.Entry{Name: "send-email", Index: compiler.FirstDynamicJobTypeIndex()},
		jobtype.Entry{Name: "charge-card", Index: high},
	)

	reopen(t, dir) // the upgrade's first boot: nothing is interned
	removeRecord(t, dir, "charge-card")

	// Several names, not one: without the write-back the counter falls to just past
	// the surviving entry, and the reissue lands a few interns later rather than on
	// the first — a test that only tried one would pass for the wrong reason.
	again := reopen(t, dir)
	for i := 0; i < 10; i++ {
		if idx := intern(t, again, "task-"+string(rune('a'+i))); idx == high {
			t.Fatalf("index %d was issued again, and charge-card's parked jobs still carry it", high)
		}
	}
}

// TestAFreshTableWritesNoMark: the mark is about issued indices, and a table that
// has issued none has nothing to say. It also keeps a brand-new data directory free
// of a file that would only ever read as zero.
func TestAFreshTableWritesNoMark(t *testing.T) {
	dir := t.TempDir()
	reopen(t, dir)
	if _, err := os.Stat(filepath.Join(dir, "highest.json")); !os.IsNotExist(err) {
		t.Errorf("a table that has issued nothing wrote a mark (stat err = %v)", err)
	}
}

// TestTheMarkIsNotReadAsAnEntry pins a coupling that would otherwise fail quietly.
//
// The mark shares the entries' directory, and it is only invisible to them because
// entry files are named by hex-encoding the job type and the entry store ignores
// every stem that is not hex. Change either side and the mark starts being loaded as
// a job type with an empty name — or an entry starts overwriting the mark.
func TestTheMarkIsNotReadAsAnEntry(t *testing.T) {
	dir := t.TempDir()
	r := reopen(t, dir)
	idx := intern(t, r, "send-email")

	body, err := os.ReadFile(filepath.Join(dir, "highest.json"))
	if err != nil {
		t.Fatalf("the mark is not where the entries are: %v", err)
	}
	var mark struct {
		Highest int32 `json:"highest"`
	}
	if err := json.Unmarshal(body, &mark); err != nil {
		t.Fatalf("decode mark: %v (%s)", err, body)
	}
	if mark.Highest != idx {
		t.Errorf("mark = %d, want the index just issued, %d", mark.Highest, idx)
	}

	again := reopen(t, dir)
	for _, e := range again.All() {
		if e.Name == "" {
			t.Fatalf("the mark was loaded as a job type: %+v", again.All())
		}
	}
	found, err := jobtype.Collisions(dir)
	if err != nil {
		t.Fatalf("Collisions: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("the offline check read the mark as an entry: %+v", found)
	}
}

// TestTheMarkIsWrittenBeforeTheRecord states the ordering as the outcome it buys. A
// crash between the two writes is indistinguishable from the record never having been
// written — which is exactly the state removing it by hand produces — and in that
// state the index must still be gone.
func TestTheMarkIsWrittenBeforeTheRecord(t *testing.T) {
	dir := t.TempDir()
	r := reopen(t, dir)
	lost := intern(t, r, "send-email")
	removeRecord(t, dir, "send-email") // the record half of the write, undone

	again := reopen(t, dir)
	if idx := intern(t, again, "send-email"); idx == lost {
		t.Errorf("re-interning reused %d; the mark had already spent it", lost)
	}
}

// TestAnIndexIsSpentEvenWhenItsRecordCannotBeWritten is the ordering itself, and
// the only case that separates it from its reverse.
//
// The mark goes first precisely so that a failure of the second write is survivable:
// the index is gone, nothing claims it, and the table carries a gap. Writing the
// record first and the mark second passes every other case here and fails this one,
// because the index would then be claimed by a record that is not there and forgotten
// by a mark that never heard of it.
//
// The failure is produced rather than simulated: a directory standing where the
// record's file must go makes the rename fail, which is what a full disk or a lost
// volume does at the same moment.
func TestAnIndexIsSpentEvenWhenItsRecordCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	r := reopen(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, hex.EncodeToString([]byte("send-email"))+".json"), 0o755); err != nil {
		t.Fatalf("block the record's path: %v", err)
	}

	blocked, err := r.Intern("send-email")
	if err == nil {
		t.Fatalf("Intern returned %d with its record unwritable; it must report the failure", blocked)
	}

	// What the mark recorded before the record failed.
	body, err := os.ReadFile(filepath.Join(dir, "highest.json"))
	if err != nil {
		t.Fatalf("the index was issued without being marked spent: %v", err)
	}
	var mark struct {
		Highest int32 `json:"highest"`
	}
	if err := json.Unmarshal(body, &mark); err != nil {
		t.Fatalf("decode mark: %v (%s)", err, body)
	}

	again := reopen(t, dir)
	if idx := intern(t, again, "ship-parcel"); idx <= mark.Highest {
		t.Errorf("ship-parcel was issued %d, at or below the spent %d", idx, mark.Highest)
	}
}

// TestNoIndexIsIssuedWhenTheMarkCannotBeWritten is the other half of the ordering:
// if the durable half fails, nothing is issued at all.
//
// An index handed back after the mark failed would be an index the table cannot
// remember, which is the defect arriving through the front door. Refusing costs a
// failed deploy, and a failed deploy is repeatable.
func TestNoIndexIsIssuedWhenTheMarkCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	r := reopen(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "highest.json"), 0o755); err != nil {
		t.Fatalf("block the mark's path: %v", err)
	}

	idx, err := r.Intern("send-email")
	if err == nil {
		t.Fatalf("Intern returned %d with the mark unwritable", idx)
	}
	if got, ok := r.Index("send-email"); ok {
		t.Errorf("send-email resolves to %d after a failed Intern; it was never issued", got)
	}
	entry := filepath.Join(dir, hex.EncodeToString([]byte("send-email"))+".json")
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Errorf("an entry was written for an index the mark never recorded (stat err = %v)", err)
	}
}

// TestACorruptMarkRefusesToOpenTheTable states a judgement rather than a mechanism,
// and it is the opposite of the one collision.go makes next door.
//
// A collision is a condition whose repair has not been decided, so the table loads
// and reports it. A mark that will not parse is not that: continuing would mean
// falling back to the derivation this file exists to replace, and reissuing indices
// quietly is exactly what must not happen. An operator can read the file; a silently
// reset counter leaves nothing to read.
func TestACorruptMarkRefusesToOpenTheTable(t *testing.T) {
	dir := t.TempDir()
	r := reopen(t, dir)
	intern(t, r, "send-email")

	if err := os.WriteFile(filepath.Join(dir, "highest.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupt the mark: %v", err)
	}
	if _, err := jobtype.NewRegistry(dir); err == nil {
		t.Fatal("the table opened over a mark it could not read; the counter would silently fall back to the entries")
	}
}
