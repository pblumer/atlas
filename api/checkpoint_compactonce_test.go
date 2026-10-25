package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/checkpoint"
)

// An administrator asks for one compaction pass on a server that does not compact on
// its own (ADR-0486). The flag decides whether
// compaction runs by itself; the request is the person's choice for one pass, and the
// pass keeps every gate the scheduled one has.

func checkpointNowWith(t *testing.T, h *compactionHarness, query string) (int, checkpointPassResp, []byte) {
	t.Helper()
	code, body := h.x.do(http.MethodPost, "/api/v1/checkpoints"+query, "")
	var out checkpointPassResp
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode pass: %v (%s)", err, body)
		}
	}
	return code, out, body
}

// TestCheckpointNowCompactsOnceWhenAsked: ?compact=true deletes the redundant segments
// on a server started without --compact-wal, says it compacted, and leaves the server
// as configured — the next plain pass deletes nothing.
func TestCheckpointNowCompactsOnceWhenAsked(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir) // no WithWALCompaction
	h.deploy()
	h.create(3)
	before := h.segments()

	code, pass, body := checkpointNowWith(t, h, "?compact=true")
	if code != http.StatusOK {
		t.Fatalf("POST ?compact=true status=%d body=%s", code, body)
	}
	if !pass.Compaction {
		t.Error("the pass does not say it compacted")
	}
	if pass.SegmentsRemoved == 0 {
		t.Fatal("a pass asked to compact deleted nothing on a log with redundant segments")
	}
	if got := h.segments(); got != before-pass.SegmentsRemoved {
		t.Fatalf("segments = %d, want %d after deleting %d", got, before-pass.SegmentsRemoved, pass.SegmentsRemoved)
	}

	h.create(3)
	code, pass, body = checkpointNowWith(t, h, "")
	if code != http.StatusOK {
		t.Fatalf("plain POST status=%d body=%s", code, body)
	}
	if pass.Compaction || pass.SegmentsRemoved != 0 {
		t.Errorf("a plain pass on a server without --compact-wal = %+v, want no compaction", pass)
	}
	if st := statusOf(t, h); st.Compaction {
		t.Error("asking for one pass switched compaction on for the server")
	}
}

// TestCheckpointNowSaysCompactionRanWhenConfigured: on a server that compacts on its
// own, every pass says so, asked for or not.
func TestCheckpointNowSaysCompactionRanWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir, WithWALCompaction())
	h.deploy()
	h.create(2)
	if _, pass, _ := checkpointNowWith(t, h, ""); !pass.Compaction {
		t.Errorf("pass on a compacting server = %+v, want compaction reported", pass)
	}
}

// TestCheckpointNowRefusesAMalformedCompactFlag: a value it cannot read is a 400, not a
// guess about whether to delete.
func TestCheckpointNowRefusesAMalformedCompactFlag(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(2)
	before := h.segments()
	if code, _, body := checkpointNowWith(t, h, "?compact=yes"); code != http.StatusBadRequest {
		t.Fatalf("POST ?compact=yes status=%d body=%s, want 400", code, body)
	}
	if got := h.segments(); got != before {
		t.Fatalf("a refused request changed the log: %d segments, want %d", got, before)
	}
}

// TestCheckpointStatusWithoutVerification: ?verify=false answers from the manifests
// alone — what the dashboard asks every time it loads — and does not claim a
// verification it did not do.
func TestCheckpointStatusWithoutVerification(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(2)
	h.pass()

	code, body := h.x.do(http.MethodGet, "/api/v1/checkpoints?verify=false", "")
	if code != http.StatusOK {
		t.Fatalf("GET ?verify=false status=%d body=%s", code, body)
	}
	if strings.Contains(string(body), `"verified"`) {
		t.Errorf("an unverified listing reports verification: %s", body)
	}
	var st checkpointStatusResp
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(st.Checkpoints) != 1 || st.Checkpoints[0].Position == 0 || st.Checkpoints[0].CreatedAt == 0 {
		t.Fatalf("checkpoints = %+v, want the one published, described from its manifest", st.Checkpoints)
	}
	if st.LastPass == nil || st.WALSegments == 0 || !st.Enabled {
		t.Errorf("status = %+v, want configuration, the last pass and the WAL", st)
	}

	if code, body := h.x.do(http.MethodGet, "/api/v1/checkpoints", ""); code != http.StatusOK || !strings.Contains(string(body), `"verified":true`) {
		t.Errorf("the full status = %d %s, want it to verify", code, body)
	}
	if code, body := h.x.do(http.MethodGet, "/api/v1/checkpoints?verify=maybe", ""); code != http.StatusBadRequest {
		t.Errorf("GET ?verify=maybe status=%d body=%s, want 400", code, body)
	}
}

// TestCheckpointStatusWithoutVerificationReadsNoStateFile: ?verify=false re-hashes no
// checkpoint at all. It used to verify every one and then throw the answer away, so on
// a 23 GiB store each dashboard load read tens of GiB and the storage card, which
// waited for the answer, never appeared (ADR-0486). Verification is counted where it
// happens rather than inferred from the answer, because the answer looked right either
// way.
func TestCheckpointStatusWithoutVerificationReadsNoStateFile(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	h.deploy()
	h.create(2)
	h.pass()

	verified := 0
	h.srv.verifyCheckpoint = func(root string, pos uint64) (*checkpoint.Manifest, error) {
		verified++
		return checkpoint.Verify(root, pos)
	}
	if code, body := h.x.do(http.MethodGet, "/api/v1/checkpoints?verify=false", ""); code != http.StatusOK {
		t.Fatalf("GET ?verify=false status=%d body=%s", code, body)
	}
	if verified != 0 {
		t.Fatalf("GET ?verify=false verified %d checkpoint(s), want none", verified)
	}
	if code, body := h.x.do(http.MethodGet, "/api/v1/checkpoints", ""); code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", code, body)
	}
	if verified != 1 {
		t.Errorf("the full status verified %d checkpoint(s), want the one published", verified)
	}
}
