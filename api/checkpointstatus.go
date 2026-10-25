package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/logging"

	"github.com/pblumer/atlas/api/httpapi"
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
	// Compaction says whether this pass compacted: always on a server started with
	// --compact-wal, and on the one pass an administrator asked to compact otherwise.
	Compaction      bool   `json:"compaction"`
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
	// unchecked marks a row described from its manifest alone (?verify=false). It holds
	// no verdict, so MarshalJSON writes none rather than a false that reads as damage.
	unchecked bool
}

// MarshalJSON leaves verified out of a row nothing was checked for.
func (c checkpointInfo) MarshalJSON() ([]byte, error) {
	type plain checkpointInfo
	if !c.unchecked {
		return json.Marshal(plain(c))
	}
	return json.Marshal(struct {
		Position     uint64 `json:"position"`
		CreatedAt    int64  `json:"createdAt"`
		AtlasVersion string `json:"atlasVersion,omitempty"`
		Error        string `json:"error,omitempty"`
	}{c.Position, c.CreatedAt, c.AtlasVersion, c.Error})
}

// checkpointRequest is an operator's on-demand pass, handed to the checkpoint goroutine
// with the channel its outcome comes back on. compact asks for compaction on this pass
// on a server that does not compact on its own.
type checkpointRequest struct {
	compact bool
	reply   chan checkpointPass
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
//
// Verifying a checkpoint re-hashes every byte of its state files, and on a store of
// tens of GiB that is minutes of reading per checkpoint. ?verify=false describes them
// from their manifests instead — what the dashboard asks for every time it loads
// (ADR-0486) — and the full read stays the
// default, because "does it still verify" is the question this endpoint was built for.
func (s *Server) handleCheckpointStatus(w http.ResponseWriter, r *http.Request) {
	verify := true
	if raw := r.URL.Query().Get("verify"); raw != "" {
		v, err := boolQuery(raw, "verify")
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		verify = v
	}
	// One or the other, never both: the verifying read is the expensive one, and asking
	// for it only to replace its answer is what kept the dashboard card waiting.
	var checkpoints []checkpointInfo
	if verify {
		checkpoints = s.publishedCheckpoints()
	} else {
		checkpoints = s.checkpointManifests()
	}
	resp := checkpointStatusResp{
		Enabled:     s.checkpointInterval > 0,
		Compaction:  s.compactWAL,
		Root:        s.checkpointRoot,
		Checkpoints: checkpoints,
	}
	if resp.Enabled {
		resp.IntervalSeconds = s.checkpointInterval.Seconds()
		resp.Keep = s.checkpointKeep
	}
	s.lastPassMu.Lock()
	resp.LastPass = s.lastPass
	s.lastPassMu.Unlock()
	resp.WALSegments, resp.WALBytes = s.walFootprint()
	httpapi.JSON(w, http.StatusOK, resp)
}

// publishedCheckpoints describes every checkpoint currently on disk, newest last, each
// with whether it still verifies — asked now, by re-hashing every one.
func (s *Server) publishedCheckpoints() []checkpointInfo {
	return s.checkpointRows(func(pos uint64) (*checkpoint.Manifest, error) {
		return s.verifyCheckpoint(s.checkpointRoot, pos)
	})
}

// rememberedCheckpoints is publishedCheckpoints with each verdict taken from what this
// server remembers when the checkpoint's files are unchanged and the verdict is less
// than a day old — what a metrics scrape asks
// (ADR-0488).
func (s *Server) rememberedCheckpoints() []checkpointInfo {
	rows := s.checkpointRows(s.rememberedVerify)
	positions := make([]uint64, len(rows))
	for i := range rows {
		positions[i] = rows[i].Position
	}
	s.forgetCheckpointsExcept(positions)
	return rows
}

// checkpointRows describes every checkpoint on disk, newest last, with the verdict verify
// gives for it.
func (s *Server) checkpointRows(verify func(pos uint64) (*checkpoint.Manifest, error)) []checkpointInfo {
	positions, err := checkpoint.List(s.checkpointRoot)
	if err != nil {
		return nil
	}
	out := make([]checkpointInfo, 0, len(positions))
	for _, pos := range positions {
		info := checkpointInfo{Position: pos}
		if m, err := verify(pos); err != nil {
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

// checkpointManifests describes every checkpoint on disk from its manifest alone, newest
// last, without reading its state files. A manifest that does not load is reported with
// its error, as the verifying read does.
func (s *Server) checkpointManifests() []checkpointInfo {
	positions, err := checkpoint.List(s.checkpointRoot)
	if err != nil {
		return nil
	}
	out := make([]checkpointInfo, 0, len(positions))
	for _, pos := range positions {
		info := checkpointInfo{Position: pos, unchecked: true}
		if m, err := checkpoint.Load(s.checkpointRoot, pos); err != nil {
			info.Error = err.Error()
		} else {
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
//
// ?compact=true compacts on this pass even on a server started without --compact-wal
// (ADR-0486): the flag decides whether
// compaction runs on its own, and an administrator asking for it is the person the flag
// leaves the choice to. The route is allowed on a frozen server (ADR-0481), so this is
// also how a server that froze without --compact-wal gives WAL space back.
func (s *Server) handleCheckpointNow(w http.ResponseWriter, r *http.Request) {
	compact, err := boolQuery(r.URL.Query().Get("compact"), "compact")
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.checkpointRequests == nil {
		httpapi.Error(w, http.StatusConflict,
			"checkpointing is disabled on this server; start it with --checkpoint-interval to take one (ADR-0131)")
		return
	}
	if compact && !s.compactWAL {
		logging.Info(logging.WALCompactionRequested,
			"an administrator asked for one compaction pass on a server not started with --compact-wal",
			slog.String("by", principalName(r)))
	}
	reply := make(chan checkpointPass, 1)
	select {
	case s.checkpointRequests <- checkpointRequest{compact: compact, reply: reply}:
	case <-s.quit:
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	}
	select {
	case res := <-reply:
		httpapi.JSON(w, http.StatusOK, res)
	case <-s.quit:
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
	}
}
