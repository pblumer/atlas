package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/checkpoint"
)

// The operator surface for recovery checkpoints and WAL compaction (ADR-0131 slice 8).
// Everything the cadence does was previously visible only in the server log, which left
// an operator with no way to answer the two questions that actually come up: what has
// been captured (and therefore how much log a restart still has to replay), and can I
// take a checkpoint right now, before I restart this thing.
//
// Both endpoints are admin-gated like backup/restore: the status names on-disk positions
// and sizes, and the control performs an operator action that, with compaction enabled,
// deletes data.

// checkpointPass is the outcome of one checkpoint-and-compaction pass, returned to
// whoever asked for it and kept as the "last pass" the status reports. A pass that did
// nothing for a benign reason says so in Note rather than reporting a bare zero.
type checkpointPass struct {
	At              int64  `json:"at"`                        // unix nanoseconds
	Position        uint64 `json:"position"`                  // 0 when nothing was published
	CheckpointError string `json:"checkpointError,omitempty"` // empty when it succeeded
	SegmentsRemoved int    `json:"segmentsRemoved"`
	CompactionError string `json:"compactionError,omitempty"`
	Note            string `json:"note,omitempty"`
}

// checkpointInfo is one published checkpoint as the status reports it.
type checkpointInfo struct {
	Position     uint64 `json:"position"`
	CreatedAt    int64  `json:"createdAt"` // unix nanoseconds, from the manifest
	AtlasVersion string `json:"atlasVersion,omitempty"`
	// Verified is whether the manifest *and* the state files still check out. It is the
	// same check compaction gates deletion on, so a false here explains why a log is not
	// shrinking — and warns that this checkpoint would not carry a restore.
	Verified bool   `json:"verified"`
	Error    string `json:"error,omitempty"` // why it did not verify
}

// checkpointStatusResp is GET /api/v1/checkpoints.
type checkpointStatusResp struct {
	Enabled         bool             `json:"enabled"`
	IntervalSeconds float64          `json:"intervalSeconds,omitempty"`
	Keep            int              `json:"keep,omitempty"`
	Compaction      bool             `json:"compaction"`
	Root            string           `json:"root"`
	Checkpoints     []checkpointInfo `json:"checkpoints"`
	LastPass        *checkpointPass  `json:"lastPass,omitempty"`
	WALSegments     int              `json:"walSegments"`
	WALBytes        int64            `json:"walBytes"`
}

// checkpointPassResp is POST /api/v1/checkpoints — the pass that just ran.
type checkpointPassResp = checkpointPass

// handleCheckpointStatus reports what checkpointing and compaction are configured to do
// and what they have actually done (ADR-0131). It reads the filesystem rather than the
// processor, so it never touches the run loop.
func (s *Server) handleCheckpointStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	resp := checkpointStatusResp{
		Enabled:     s.checkpointInterval > 0,
		Compaction:  s.compactWAL,
		Root:        s.checkpointRoot,
		Checkpoints: s.publishedCheckpoints(),
	}
	if resp.Enabled {
		resp.IntervalSeconds = s.checkpointInterval.Seconds()
		resp.Keep = s.checkpointKeep
	}
	s.lastPassMu.Lock()
	resp.LastPass = s.lastPass
	s.lastPassMu.Unlock()
	resp.WALSegments, resp.WALBytes = s.walFootprint()
	writeJSON(w, http.StatusOK, resp)
}

// publishedCheckpoints describes every checkpoint currently on disk, newest last, each
// with whether it still verifies.
func (s *Server) publishedCheckpoints() []checkpointInfo {
	positions, err := checkpoint.List(s.checkpointRoot)
	if err != nil {
		return nil
	}
	out := make([]checkpointInfo, 0, len(positions))
	for _, pos := range positions {
		info := checkpointInfo{Position: pos}
		if m, err := checkpoint.Verify(s.checkpointRoot, pos); err != nil {
			info.Error = err.Error()
			// Fall back to the manifest alone so a checkpoint whose state files are
			// damaged is still described rather than reduced to a bare position.
			if m, lerr := checkpoint.Load(s.checkpointRoot, pos); lerr == nil {
				info.CreatedAt, info.AtlasVersion = m.CreatedUnixNano, m.AtlasVersion
			}
		} else {
			info.Verified = true
			info.CreatedAt, info.AtlasVersion = m.CreatedUnixNano, m.AtlasVersion
		}
		out = append(out, info)
	}
	return out
}

// walFootprint is the WAL's current segment count and total bytes — the number an
// operator watches to decide whether compaction is earning its keep.
func (s *Server) walFootprint() (int, int64) {
	entries, err := os.ReadDir(filepath.Join(s.dataDir, "wal"))
	if err != nil {
		return 0, 0
	}
	count, bytes := 0, int64(0)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".wal" {
			continue
		}
		count++
		if info, err := e.Info(); err == nil {
			bytes += info.Size()
		}
	}
	return count, bytes
}

// handleCheckpointNow runs one checkpoint-and-compaction pass on demand and reports what
// it did (ADR-0131) — for an operator about to restart, who would rather replay seconds
// of log than hours.
//
// The pass runs on the checkpoint goroutine, so it serializes with the scheduled cadence
// by construction and is the very same code. With no checkpointing configured there is
// no goroutine to ask, which is a 409 rather than a hang or a silent no-op.
func (s *Server) handleCheckpointNow(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.checkpointRequests == nil {
		writeError(w, http.StatusConflict,
			"checkpointing is disabled on this server; start it with --checkpoint-interval to take one (ADR-0131)")
		return
	}
	reply := make(chan checkpointPass, 1)
	select {
	case s.checkpointRequests <- reply:
	case <-s.quit:
		writeError(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	}
	select {
	case res := <-reply:
		writeJSON(w, http.StatusOK, res)
	case <-s.quit:
		writeError(w, http.StatusServiceUnavailable, "server is shutting down")
	}
}
