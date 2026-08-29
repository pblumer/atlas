package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// ADR-0131 slice 8: the operator surface. Until now everything checkpointing and
// compaction did was visible only in the server log — an operator could not ask what
// had been captured, how much of the log it made redundant, or take a checkpoint before
// a planned restart. These tests pin the read and the one control.
//
// The control runs its pass on the *same* loop as the cadence rather than beside it, so
// an on-demand pass and a scheduled one can never overlap; the tests below drive it with
// no tick at all, which is what makes that observable.

func statusOf(t *testing.T, h *compactionHarness) checkpointStatusResp {
	t.Helper()
	code, body := h.x.do(http.MethodGet, "/api/v1/checkpoints", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/checkpoints status=%d body=%s", code, body)
	}
	var out checkpointStatusResp
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode status: %v (%s)", err, body)
	}
	return out
}

func checkpointNow(t *testing.T, h *compactionHarness) (int, checkpointPassResp) {
	t.Helper()
	code, body := h.x.do(http.MethodPost, "/api/v1/checkpoints", "")
	var out checkpointPassResp
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode pass: %v (%s)", err, body)
		}
	}
	return code, out
}

// TestCheckpointStatusReportsWhatIsOnDisk: the status answers the questions an operator
// actually has before a restart — is this on, what has been captured, and how much log
// still has to be replayed past it.
func TestCheckpointStatusReportsWhatIsOnDisk(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir, WithCheckpointRetention(2))
	h.deploy()
	h.create(2)

	before := statusOf(t, h)
	if !before.Enabled {
		t.Error("status reports checkpointing disabled on a server that has it configured")
	}
	if before.Compaction {
		t.Error("status reports compaction enabled on a server that never asked for it")
	}
	if len(before.Checkpoints) != 0 {
		t.Errorf("checkpoints = %v before any pass, want none", before.Checkpoints)
	}
	if before.WALSegments == 0 {
		t.Error("status reports no WAL segments for a server that has written some")
	}

	h.pass()

	after := statusOf(t, h)
	if len(after.Checkpoints) != 1 {
		t.Fatalf("checkpoints = %v after one pass, want exactly one", after.Checkpoints)
	}
	cp := after.Checkpoints[0]
	if cp.Position == 0 || !cp.Verified {
		t.Errorf("checkpoint = %+v, want a non-zero verified position", cp)
	}
	if after.LastPass == nil {
		t.Fatal("status reports no last pass after one ran")
	}
	if after.LastPass.Position != cp.Position {
		t.Errorf("last pass position = %d, want the published %d", after.LastPass.Position, cp.Position)
	}
	if after.LastPass.CheckpointError != "" {
		t.Errorf("last pass reported an error: %q", after.LastPass.CheckpointError)
	}
	if after.Keep != 2 {
		t.Errorf("keep = %d, want the configured 2", after.Keep)
	}
}

// TestCheckpointStatusWhenCheckpointingIsOff: the status is readable on a server that
// does not checkpoint at all — an operator asking "is this on?" deserves an answer
// rather than a 404.
func TestCheckpointStatusWhenCheckpointingIsOff(t *testing.T) {
	dir := t.TempDir()
	srv, cleanup := plainServer(t, dir)
	defer cleanup()
	x := deployTestHarness{t, srv.Handler()}

	code, body := x.do(http.MethodGet, "/api/v1/checkpoints", "")
	if code != http.StatusOK {
		t.Fatalf("status on a non-checkpointing server: %d %s", code, body)
	}
	var out checkpointStatusResp
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode status: %v (%s)", err, body)
	}
	if out.Enabled {
		t.Error("status reports checkpointing enabled on a server that has none")
	}
	if out.LastPass != nil {
		t.Errorf("status reports a last pass (%+v) on a server that never ran one", out.LastPass)
	}
}

// TestCheckpointNowRunsAPassOnDemand: the control publishes a checkpoint with no tick
// involved — the case that matters is an operator taking one right before a restart.
func TestCheckpointNowRunsAPassOnDemand(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(2)

	code, pass := checkpointNow(t, h)
	if code != http.StatusOK {
		t.Fatalf("POST /api/v1/checkpoints status=%d", code)
	}
	if pass.Position == 0 {
		t.Fatal("an on-demand pass published nothing on a server with applied state")
	}
	if pass.SegmentsRemoved != 0 {
		t.Errorf("segmentsRemoved = %d with compaction off, want 0", pass.SegmentsRemoved)
	}
	st := statusOf(t, h)
	if len(st.Checkpoints) != 1 || st.Checkpoints[0].Position != pass.Position {
		t.Fatalf("checkpoints = %v, want the one the pass reported at %d", st.Checkpoints, pass.Position)
	}
}

// TestCheckpointNowCompactsWhenCompactionIsOn: an on-demand pass does exactly what a
// scheduled one does, deletion included — it is the same pass, not a second code path.
func TestCheckpointNowCompactsWhenCompactionIsOn(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir, WithWALCompaction())
	h.deploy()
	h.create(3)
	before := h.segments()

	code, pass := checkpointNow(t, h)
	if code != http.StatusOK {
		t.Fatalf("POST /api/v1/checkpoints status=%d", code)
	}
	if pass.SegmentsRemoved == 0 {
		t.Fatal("an on-demand pass with compaction on deleted nothing")
	}
	if got := h.segments(); got != before-pass.SegmentsRemoved {
		t.Fatalf("segments = %d, want %d after deleting %d", got, before-pass.SegmentsRemoved, pass.SegmentsRemoved)
	}
	if st := statusOf(t, h); st.LastPass == nil || st.LastPass.SegmentsRemoved != pass.SegmentsRemoved {
		t.Fatalf("status last pass = %+v, want the %d segments the response reported", st.LastPass, pass.SegmentsRemoved)
	}
}

// TestCheckpointNowRefusedWhenCheckpointingIsOff: with no checkpoint loop there is
// nothing to ask, and saying so beats hanging or silently doing nothing.
func TestCheckpointNowRefusedWhenCheckpointingIsOff(t *testing.T) {
	dir := t.TempDir()
	srv, cleanup := plainServer(t, dir)
	defer cleanup()
	x := deployTestHarness{t, srv.Handler()}

	code, body := x.do(http.MethodPost, "/api/v1/checkpoints", "")
	if code != http.StatusConflict {
		t.Fatalf("on-demand checkpoint without checkpointing: status=%d body=%s, want 409", code, body)
	}
}

// TestCheckpointEndpointsRequireAdmin: taking a checkpoint is an operator action and the
// status names on-disk paths, so both sit behind the admin gate like backup/restore.
// Asserted at the boundary, where the role is decided now (routeroles.go): the
// routes name the admin role and withAuth refuses before either handler runs.
func TestCheckpointEndpointsRequireAdmin(t *testing.T) {
	ordinary := []string{RoleModeler, RoleOperator, RoleUser}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if got := boundaryStatus(t, ordinary, method, "/api/v1/checkpoints"); got != http.StatusForbidden {
			t.Errorf("%s /api/v1/checkpoints without an admin principal: status=%d, want 403", method, got)
		}
	}
}

// TestCheckpointStatusIsDocumented keeps the operator surface discoverable: the OpenAPI
// document the UI and any client read must describe the endpoints.
func TestCheckpointStatusIsDocumented(t *testing.T) {
	dir := t.TempDir()
	srv, cleanup := plainServer(t, dir)
	defer cleanup()
	x := deployTestHarness{t, srv.Handler()}

	code, body := x.do(http.MethodGet, "/api/v1/openapi.json", "")
	if code != http.StatusOK {
		t.Fatalf("openapi.json status=%d", code)
	}
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode openapi: %v", err)
	}
	ops, ok := doc.Paths["/api/v1/checkpoints"]
	if !ok {
		t.Fatal("openapi does not document /api/v1/checkpoints")
	}
	for _, method := range []string{"get", "post"} {
		if _, ok := ops[method]; !ok {
			t.Errorf("openapi documents /api/v1/checkpoints but not its %s", method)
		}
	}
}

// plainServer boots a server with no checkpointing configured at all — the default
// shape an operator asking "is this on?" would be running.
func plainServer(t *testing.T, dir string) (*Server, func()) {
	t.Helper()
	lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, lg, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return srv, func() {
		srv.Close()
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
		if err := lg.Close(); err != nil {
			t.Errorf("log.Close: %v", err)
		}
	}
}

// TestCheckpointStatusFlagsAnUnverifiableCheckpoint: a checkpoint whose state files no
// longer match its manifest licenses no deletion and would not carry a restore, so the
// status must say so plainly. This is how an operator diagnoses a log that has stopped
// shrinking — without it, the entry looks identical to a healthy one.
func TestCheckpointStatusFlagsAnUnverifiableCheckpoint(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(2)
	h.pass()

	st := statusOf(t, h)
	if len(st.Checkpoints) != 1 || !st.Checkpoints[0].Verified {
		t.Fatalf("checkpoints = %+v before corruption, want one verified", st.Checkpoints)
	}
	pos := st.Checkpoints[0].Position

	// Replace a state file inside the published checkpoint. Unlink first: a Pebble
	// checkpoint hard-links its SSTables, so writing through the path would corrupt the
	// live store at the same inode.
	cpDir := filepath.Join(checkpoint.Dir(dir), checkpoint.DirName(pos))
	entries, err := os.ReadDir(cpDir)
	if err != nil {
		t.Fatalf("ReadDir checkpoint: %v", err)
	}
	corrupted := false
	for _, e := range entries {
		if e.IsDir() || e.Name() == checkpoint.ManifestName {
			continue
		}
		p := filepath.Join(cpDir, e.Name())
		if err := os.Remove(p); err != nil {
			t.Fatalf("unlink %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(p, []byte("corrupted"), 0o644); err != nil {
			t.Fatalf("corrupt %s: %v", e.Name(), err)
		}
		corrupted = true
		break
	}
	if !corrupted {
		t.Fatal("checkpoint held no state file to corrupt")
	}

	after := statusOf(t, h)
	if len(after.Checkpoints) != 1 {
		t.Fatalf("checkpoints = %+v, want the corrupt one still listed", after.Checkpoints)
	}
	got := after.Checkpoints[0]
	if got.Verified {
		t.Error("status reports a checkpoint with corrupt state files as verified")
	}
	if got.Error == "" {
		t.Error("status flags the checkpoint unverified but says nothing about why")
	}
	// It is still described from its manifest, not reduced to a bare position.
	if got.CreatedAt == 0 {
		t.Error("an unverifiable checkpoint lost the manifest metadata the status can still read")
	}
}

// TestCheckpointStatusSurvivesUnreadableDirectories: the status is a diagnostic, so it
// has to answer even when what it is reporting on is broken — an operator asking why
// something is wrong should not be met with a 500. A checkpoint root that is a file and
// a data dir with no WAL are reported as "nothing there" rather than as a failure.
func TestCheckpointStatusSurvivesUnreadableDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(checkpoint.Dir(dir), []byte("in the way"), 0o644); err != nil {
		t.Fatalf("plant file: %v", err)
	}
	s := &Server{dataDir: dir, checkpointRoot: checkpoint.Dir(dir)}

	rec := httptest.NewRecorder()
	s.handleCheckpointStatus(rec, httptest.NewRequest(http.MethodGet, "/api/v1/checkpoints", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status over broken directories: %d %s", rec.Code, rec.Body)
	}
	var out checkpointStatusResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if len(out.Checkpoints) != 0 {
		t.Errorf("checkpoints = %v over an unreadable root, want none", out.Checkpoints)
	}
	if out.WALSegments != 0 || out.WALBytes != 0 {
		t.Errorf("wal footprint = %d segments / %d bytes with no WAL directory, want 0/0", out.WALSegments, out.WALBytes)
	}
}

// TestWALFootprintCountsOnlySegments: the WAL directory is not guaranteed to hold only
// segments, and an operator watching this number to judge compaction should not see it
// inflated by whatever else lands there.
func TestWALFootprintCountsOnlySegments(t *testing.T) {
	dir := t.TempDir()
	walDir := filepath.Join(dir, "wal")
	if err := os.MkdirAll(filepath.Join(walDir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(walDir, "0000000000000001.wal"), []byte("12345"), 0o644); err != nil {
		t.Fatalf("write segment: %v", err)
	}
	if err := os.WriteFile(filepath.Join(walDir, "notes.txt"), []byte("not a segment"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	s := &Server{dataDir: dir}
	count, bytes := s.walFootprint()
	if count != 1 {
		t.Errorf("segments = %d, want only the one .wal file", count)
	}
	if bytes != 5 {
		t.Errorf("bytes = %d, want only the segment's 5", bytes)
	}
}

// TestCheckpointNowDuringShutdownIsRefused: a request that arrives as the server is
// stopping has no loop left to run its pass, so it is told so rather than blocking on a
// goroutine that will never read it.
func TestCheckpointNowDuringShutdownIsRefused(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(1)
	h.close() // stops the checkpoint loop

	rec := httptest.NewRecorder()
	h.srv.handleCheckpointNow(rec, httptest.NewRequest(http.MethodPost, "/api/v1/checkpoints", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("on-demand checkpoint during shutdown: status=%d body=%s, want 503", rec.Code, rec.Body)
	}
}
