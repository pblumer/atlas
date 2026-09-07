package api

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// bootedDataDir starts a real server over a temporary data directory and returns
// the directory, so a test can look at what the server actually put on disk rather
// than at what a list says it did.
func bootedDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	t.Cleanup(srv.Close)
	return dir
}

// TestEveryPersistentStoreHasABackupClass is the guard this whole finding is
// about, and the reason it is written against the filesystem rather than against a
// list.
//
// The backup used to be a hand-kept allowlist that lived in a different file from
// the code creating the stores. Nothing connected the two, so a store added later
// simply was not backed up, and the archive still called itself a full one and
// still succeeded. Twelve stores had drifted out that way — including the vault,
// whose key was backed up while the encrypted secrets it opens were not, and the
// job-type table, whose numbers give already-stored jobs their meaning.
//
// A test over the list could not have caught any of it: the list was internally
// consistent. This one asks the server what it wrote and refuses anything it
// cannot classify, so the next store to be added has to be given a backup decision
// before it can be merged. That is the point — not the twelve, which are a
// symptom, but the mechanism that let them happen.
func TestEveryPersistentStoreHasABackupClass(t *testing.T) {
	dir := bootedDataDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}

	var unclassified []string
	for _, e := range entries {
		if _, ok := storeClassOf(e.Name()); !ok {
			unclassified = append(unclassified, e.Name())
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Fatalf("the server writes %v into its data directory, and the store registry does not "+
			"classify them. Add each to persistentStores with the class that says what a backup "+
			"owes it — a store nobody classified is a store nobody backs up, and the archive will "+
			"still call itself complete", unclassified)
	}
}

// TestTheRegistryDescribesOnlyWhatExists is the other direction: an entry naming a
// directory the server no longer writes is a stale entry, and a backup built from
// it silently stops covering something while looking unchanged.
func TestTheRegistryDescribesOnlyWhatExists(t *testing.T) {
	dir := bootedDataDir(t)
	var missing []string
	for _, e := range persistentStores {
		if e.onDemand {
			continue // only exists once the feature behind it is used
		}
		if _, err := os.Stat(filepath.Join(dir, e.name)); err != nil {
			missing = append(missing, e.name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("the registry classifies %v, which a booted server does not write. Either the "+
			"store was removed and its entry should go, or it is created later than boot and "+
			"belongs in a class that says so", missing)
	}
}

// TestFullBackupCoversEverythingButTheRebuildable: what a full snapshot carries is
// derived from the registry, so this states the rule that derivation encodes.
// Anything that cannot be rebuilt from what is backed up has to be in the archive.
func TestFullBackupCoversEverythingButTheRebuildable(t *testing.T) {
	covered := map[string]bool{}
	for _, d := range fullBackupDirs() {
		covered[d] = true
	}
	for _, f := range fullBackupFiles() {
		covered[f] = true
	}
	for _, e := range persistentStores {
		switch {
		case e.class == classEphemeral:
			if covered[e.name] {
				t.Errorf("%s is rebuildable and does not belong in a full snapshot", e.name)
			}
		case e.ownMechanism:
			// Carried, but not by the generic walk — see storeEntry.ownMechanism.
			if covered[e.name] {
				t.Errorf("%s is carried by its own mechanism and must not also be walked whole", e.name)
			}
		default:
			if !covered[e.name] {
				t.Errorf("%s is not rebuildable from anything else in the archive, so a full "+
					"snapshot without it is not a full snapshot", e.name)
			}
		}
	}
}

// TestDesignTimeBackupIsTheDesignTimeSubset: the smaller design-time backup is a
// projection of the same registry, so the two cannot drift apart the way a second
// hand-kept list would.
func TestDesignTimeBackupIsTheDesignTimeSubset(t *testing.T) {
	got := map[string]bool{}
	for _, d := range backupDirs() {
		got[d] = true
	}
	for _, e := range persistentStores {
		want := e.class == classDesignTime
		if got[e.name] != want {
			t.Errorf("%s: in the design-time backup = %v, want %v (class %s)", e.name, got[e.name], want, e.class)
		}
	}
}

// TestSecretsAreNotInTheDesignTimeBackup: the design-time archive is the one an
// author exports and moves between installations. Secrets and credentials must not
// ride along in it — that is what the full, protected snapshot is for.
func TestSecretsAreNotInTheDesignTimeBackup(t *testing.T) {
	for _, d := range backupDirs() {
		class, ok := storeClassOf(d)
		if !ok {
			t.Fatalf("%s is in the design-time backup but is not classified", d)
		}
		if class == classSecret || class == classCredential {
			t.Errorf("%s is %s and must not be in the design-time backup, which is exported and moved between installations", d, class)
		}
	}
}
