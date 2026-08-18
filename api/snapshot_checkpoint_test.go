package api_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// ADR-0131 slice 6. Compaction deletes WAL segments a checkpoint makes redundant, which
// retires the assumption the whole-instance snapshot (ADR-0109) was built on: it carries
// `wal/` and not `state/` because `state == replay(wal)`. Once a prefix is gone the WAL
// alone no longer reproduces state, so an archive of it would restore a *silently
// partial* engine — instances whose events were in the deleted prefix would simply be
// missing. ADR-0109 considered carrying a state checkpoint (its option B) and rejected it
// for want of a checkpoint API; ADR-0131 built one, so the snapshot now carries the newest
// verified checkpoint and the restore installs it as the state store.
//
// This is the last consumer-safety prerequisite ADR-0131's decision drivers name. Nothing
// here schedules compaction; that is the next slice.

// bootCompactible is boot() with a one-byte WAL segment cap, so segments roll per batch
// and a compaction has something to delete, and with recovery wired through the
// checkpoint root the way cmd/atlas does.
func bootCompactible(t *testing.T, dir string) *stack {
	t.Helper()
	lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal"), MaxSegmentSize: 1})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, lg, store, nil)
	if err := proc.RecoverFrom(checkpoint.Dir(dir)); err != nil {
		t.Fatalf("RecoverFrom: %v", err)
	}
	srv, err := api.New(proc, store, dir)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return &stack{ts: httptest.NewServer(srv.Handler()), srv: srv, store: store, log: lg}
}

// offline runs fn against an engine opened over dir while no server holds it, for the
// two operations that have no HTTP surface yet: taking a checkpoint and compacting.
func offline(t *testing.T, dir string, fn func(*engine.Processor)) {
	t.Helper()
	lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal"), MaxSegmentSize: 1})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, lg, store, nil)
	if err := proc.RecoverFrom(checkpoint.Dir(dir)); err != nil {
		t.Fatalf("RecoverFrom: %v", err)
	}
	fn(proc)
	if err := store.Close(); err != nil {
		t.Errorf("store.Close: %v", err)
	}
	if err := lg.Close(); err != nil {
		t.Errorf("log.Close: %v", err)
	}
}

func deployAndStartN(t *testing.T, s *stack, n int) {
	t.Helper()
	if code, body := doReq(t, s.ts, "POST", "/api/v1/deployments", sampleBPMN, "application/xml"); code != 200 {
		t.Fatalf("deploy: %d %s", code, body)
	}
	for i := 0; i < n; i++ {
		if code, body := doReq(t, s.ts, "POST", "/api/v1/processes/1/instances", "", "application/json"); code != 200 {
			t.Fatalf("create instance %d: %d %s", i, code, body)
		}
	}
}

func statsBody(t *testing.T, s *stack) string {
	t.Helper()
	code, body := doReq(t, s.ts, "GET", "/api/v1/stats", "", "")
	if code != 200 {
		t.Fatalf("stats: %d %s", code, body)
	}
	return string(body)
}

// TestFullSnapshotRestoresACompactedLog is the property this slice exists for: a
// snapshot taken from an engine whose WAL prefix has been compacted away still restores
// every running instance, because the archive carries the checkpoint that covers the
// deleted prefix and the restore installs it as the state store.
//
// It is not vacuous: without the checkpoint, replaying the compacted WAL would find no
// creation events for the first three instances and bring back only the fourth.
func TestFullSnapshotRestoresACompactedLog(t *testing.T) {
	src := t.TempDir()

	// Three instances, then a checkpoint covering them, then a fourth past it.
	first := bootCompactible(t, src)
	deployAndStartN(t, first, 3)
	first.shutdown()
	offline(t, src, func(p *engine.Processor) {
		if _, err := p.Checkpoint(checkpoint.Dir(src)); err != nil {
			t.Fatalf("Checkpoint: %v", err)
		}
	})
	second := bootCompactible(t, src)
	if code, body := doReq(t, second.ts, "POST", "/api/v1/processes/1/instances", "", "application/json"); code != 200 {
		t.Fatalf("create instance past the checkpoint: %d %s", code, body)
	}
	second.shutdown()

	// Compact: the segments the checkpoint covers are gone from here on.
	offline(t, src, func(p *engine.Processor) {
		removed, err := p.CompactLog(checkpoint.Dir(src), nil)
		if err != nil {
			t.Fatalf("CompactLog: %v", err)
		}
		if removed == 0 {
			t.Fatal("compaction deleted nothing; the fixture proves nothing")
		}
	})

	third := bootCompactible(t, src)
	if got := statsBody(t, third); !strings.Contains(got, `"activeProcessInstances":4`) {
		t.Fatalf("source engine after compaction: %s, want 4 active instances", got)
	}
	code, archive := doReq(t, third.ts, "GET", "/api/v1/backup/full", "", "")
	if code != 200 {
		t.Fatalf("backup/full status=%d", code)
	}
	third.shutdown()

	names := tarGzEntries(t, archive)
	if !hasPrefix(names, "checkpoints/") {
		t.Fatalf("snapshot of a compacted log carried no checkpoint, so the deleted prefix is unrecoverable: %v", names)
	}
	for n := range names {
		if strings.HasPrefix(n, "state/") {
			t.Fatalf("snapshot carried the live state store %q; only the checkpoint is consistent", n)
		}
	}

	// Restore onto a fresh instance.
	dst := t.TempDir()
	staging := bootCompactible(t, dst)
	if code, body := doReqBytes(t, staging.ts, "POST", "/api/v1/restore/full", archive, "application/gzip"); code != 200 {
		t.Fatalf("restore/full status=%d body=%s", code, body)
	}
	staging.shutdown()

	applied, err := api.ApplyPendingRestore(dst)
	if err != nil || !applied {
		t.Fatalf("ApplyPendingRestore: applied=%v err=%v", applied, err)
	}

	restored := bootCompactible(t, dst)
	defer restored.shutdown()
	if code, body := doReq(t, restored.ts, "GET", "/api/v1/processes", "", ""); code != 200 || !strings.Contains(string(body), `"processId":"order"`) {
		t.Fatalf("processes after restore: %d %s", code, body)
	}
	if got := statsBody(t, restored); !strings.Contains(got, `"activeProcessInstances":4`) {
		t.Fatalf("restored engine: %s, want all 4 instances back", got)
	}
}

// publishCheckpoint takes one checkpoint over dir and returns its position, for tests
// that need a published checkpoint without a running server.
func publishCheckpoint(t *testing.T, dir string) uint64 {
	t.Helper()
	var pos uint64
	offline(t, dir, func(p *engine.Processor) {
		var err error
		if pos, err = p.Checkpoint(checkpoint.Dir(dir)); err != nil {
			t.Fatalf("Checkpoint: %v", err)
		}
	})
	if pos == 0 {
		t.Fatal("checkpoint published nothing; the fixture has no applied state")
	}
	return pos
}

// TestFullSnapshotCarriesExactlyTheNewestVerifiedCheckpoint: several checkpoints are
// kept on disk, but only one belongs in an archive — carrying every kept snapshot of the
// same store would multiply the download for no recovery value.
func TestFullSnapshotCarriesExactlyTheNewestVerifiedCheckpoint(t *testing.T) {
	dir := t.TempDir()
	s := bootCompactible(t, dir)
	deployAndStartN(t, s, 1)
	s.shutdown()
	older := publishCheckpoint(t, dir)

	s2 := bootCompactible(t, dir)
	if code, body := doReq(t, s2.ts, "POST", "/api/v1/processes/1/instances", "", "application/json"); code != 200 {
		t.Fatalf("create instance: %d %s", code, body)
	}
	s2.shutdown()
	newer := publishCheckpoint(t, dir)
	if older == newer {
		t.Fatalf("both checkpoints landed at %d; the fixture proves nothing", newer)
	}

	s3 := bootCompactible(t, dir)
	defer s3.shutdown()
	code, archive := doReq(t, s3.ts, "GET", "/api/v1/backup/full", "", "")
	if code != 200 {
		t.Fatalf("backup/full status=%d", code)
	}
	names := tarGzEntries(t, archive)
	wantPrefix := "checkpoints/" + checkpoint.DirName(newer) + "/"
	dontWant := "checkpoints/" + checkpoint.DirName(older) + "/"
	if !hasPrefix(names, wantPrefix) {
		t.Errorf("archive lacks the newest checkpoint %q: %v", wantPrefix, names)
	}
	if hasPrefix(names, dontWant) {
		t.Errorf("archive also carried the superseded checkpoint %q", dontWant)
	}
}

// TestFullSnapshotSkipsAnUnverifiableCheckpoint: the archive's checkpoint stands in for
// a WAL prefix that may no longer exist on the restoring box, so one whose state files
// do not match its manifest is worthless. An older checkpoint that still verifies is
// carried instead — a shorter shortcut, never a broken one.
func TestFullSnapshotSkipsAnUnverifiableCheckpoint(t *testing.T) {
	dir := t.TempDir()
	s := bootCompactible(t, dir)
	deployAndStartN(t, s, 1)
	s.shutdown()
	good := publishCheckpoint(t, dir)

	s2 := bootCompactible(t, dir)
	if code, body := doReq(t, s2.ts, "POST", "/api/v1/processes/1/instances", "", "application/json"); code != 200 {
		t.Fatalf("create instance: %d %s", code, body)
	}
	s2.shutdown()
	bad := publishCheckpoint(t, dir)

	// Corrupt a state file inside the newest checkpoint; its manifest still decodes.
	corruptCheckpointState(t, filepath.Join(checkpoint.Dir(dir), checkpoint.DirName(bad)))

	s3 := bootCompactible(t, dir)
	defer s3.shutdown()
	code, archive := doReq(t, s3.ts, "GET", "/api/v1/backup/full", "", "")
	if code != 200 {
		t.Fatalf("backup/full status=%d", code)
	}
	names := tarGzEntries(t, archive)
	if hasPrefix(names, "checkpoints/"+checkpoint.DirName(bad)+"/") {
		t.Errorf("archive carried the corrupt checkpoint at %d", bad)
	}
	if !hasPrefix(names, "checkpoints/"+checkpoint.DirName(good)+"/") {
		t.Errorf("archive fell past the older checkpoint at %d that still verifies: %v", good, names)
	}
}

// corruptCheckpointState replaces one state file inside a published checkpoint, leaving
// its manifest intact so only full verification catches it.
//
// It unlinks before writing on purpose: a Pebble checkpoint *hard-links* the SSTables it
// captured, so writing through the path would corrupt the live store at the same inode
// and take this test's own engine down with it.
func corruptCheckpointState(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir checkpoint: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == checkpoint.ManifestName {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Remove(path); err != nil {
			t.Fatalf("unlink %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(path, []byte("corrupted"), 0o644); err != nil {
			t.Fatalf("corrupt %s: %v", e.Name(), err)
		}
		return
	}
	t.Fatal("checkpoint held no state file to corrupt")
}

// TestRestoreFullAlwaysStagesACheckpointEntry pins the staging contract the apply relies
// on: even an archive with no checkpoint stages the entry, so the apply's rename pass
// replaces the local checkpoint root instead of leaving checkpoints of the replaced log
// in place. Without it the apply would have to ask "did this archive carry one?", a
// question a crash mid-apply makes unanswerable.
func TestRestoreFullAlwaysStagesACheckpointEntry(t *testing.T) {
	ts, dir := newBackupServer(t)
	arc := makeTarGz(t, map[string]string{"wal/0000000000000001.wal": "log-bytes"})
	if code, body := doReqBytes(t, ts, "POST", "/api/v1/restore/full", arc, "application/gzip"); code != 200 {
		t.Fatalf("restore/full status=%d body=%s", code, body)
	}
	staged := filepath.Join(dir, ".restore-pending", checkpoint.DirBase)
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("archive without checkpoints staged no checkpoint entry: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("staged checkpoint entry is not a directory")
	}
}

// TestRestoreFullRejectsCheckpointsAsAFile: a hostile archive that puts a regular file
// where the checkpoint directory belongs must not slip past the staging contract — the
// entry that has to be a directory is created there, and the collision is reported
// rather than leaving a staging the apply would misread.
func TestRestoreFullRejectsCheckpointsAsAFile(t *testing.T) {
	ts, dir := newBackupServer(t)
	arc := makeTarGz(t, map[string]string{
		"wal/0000000000000001.wal": "log-bytes",
		checkpoint.DirBase:         "i am a file, not a directory",
	})
	code, _ := doReqBytes(t, ts, "POST", "/api/v1/restore/full", arc, "application/gzip")
	if code != 500 {
		t.Fatalf("checkpoints-as-a-file status=%d, want 500", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".restore-pending")); !os.IsNotExist(err) {
		t.Fatalf("a rejected restore left a staging dir behind (err=%v)", err)
	}
}
