package api

import (
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/checkpoint"
)

// Whether a published checkpoint still verifies, remembered rather than re-read on every
// scrape (ADR-0488).
//
// Verifying a checkpoint re-hashes every byte of its state files. The /metrics gauges
// asked that question of every checkpoint on every scrape, so a Prometheus scraping a
// 23 GiB store every fifteen seconds read the store, times the number of checkpoints
// kept, four times a minute — and a scrape that takes minutes is a scrape Prometheus has
// already given up on.
//
// A published checkpoint is immutable: it is renamed into place complete, and nothing
// writes into it afterwards (ADR-0131). So the answer can be kept, as long as two things
// that would change it are watched for:
//
//   - a file replaced, truncated or rewritten — seen at once, because the verdict is
//     keyed by a fingerprint of the files' names, sizes and modification times, which a
//     directory walk reads without opening a file;
//   - a byte that rots in place, which changes neither — caught by re-verifying once the
//     verdict is older than checkpointReverifyAfter.
//
// A checkpoint this process published is recorded as verified when it is published,
// because the commit has just hashed exactly those files; otherwise every new checkpoint
// — one every cadence — would be read again by the next scrape.
//
// GET /api/v1/checkpoints still verifies afresh: an operator who asks whether a
// checkpoint still verifies is asking that question now.

// checkpointReverifyAfter is how old a remembered verdict may grow before the files are
// hashed again, which bounds how long rot that changes no file metadata goes unreported.
const checkpointReverifyAfter = 24 * time.Hour

// checkpointVerdicts holds the last verdict for each published checkpoint. The mutex is
// held across a verification, so concurrent scrapes of a cold cache wait for one read
// instead of starting one each.
type checkpointVerdicts struct {
	mu    sync.Mutex
	byPos map[uint64]checkpointVerdict
}

// checkpointVerdict is one checkpoint's verification outcome and what it was taken over.
type checkpointVerdict struct {
	print string // checkpointFingerprint of the directory when it was verified
	at    int64  // server clock, unix nanoseconds
	m     *checkpoint.Manifest
	err   error
}

// checkpointFingerprint describes a checkpoint directory's state files by name, size and
// modification time — everything a stat says and nothing a read would. The manifest is
// left out, as checkpoint.ChecksumDir leaves it out of the hash.
func checkpointFingerprint(dir string) (string, error) {
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == checkpoint.ManifestName {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b.WriteString(rel)
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(info.Size(), 10))
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
		b.WriteByte('\n')
		return nil
	})
	return b.String(), err
}

// rememberedVerify answers whether the checkpoint at pos verifies, from the remembered
// verdict when its files are unchanged and it is recent enough, and by verifying —
// once, whoever else is asking — when not. A fingerprint that cannot be taken is never
// trusted: the checkpoint is verified, and nothing is remembered about it.
func (s *Server) rememberedVerify(pos uint64) (*checkpoint.Manifest, error) {
	print, perr := checkpointFingerprint(filepath.Join(s.checkpointRoot, checkpoint.DirName(pos)))
	s.verdicts.mu.Lock()
	defer s.verdicts.mu.Unlock()
	if perr == nil {
		if v, ok := s.verdicts.byPos[pos]; ok && v.print == print && s.now()-v.at < int64(checkpointReverifyAfter) {
			return v.m, v.err
		}
	}
	m, err := s.verifyCheckpoint(s.checkpointRoot, pos)
	if perr == nil {
		s.rememberLocked(pos, checkpointVerdict{print: print, at: s.now(), m: m, err: err})
	}
	return m, err
}

// rememberPublished records a checkpoint this process has just published as verified:
// its commit hashed exactly the files now on disk. A fingerprint or manifest that cannot
// be read records nothing, and the next scrape verifies it the slow way.
func (s *Server) rememberPublished(pos uint64) {
	print, err := checkpointFingerprint(filepath.Join(s.checkpointRoot, checkpoint.DirName(pos)))
	if err != nil {
		return
	}
	m, err := checkpoint.Load(s.checkpointRoot, pos)
	if err != nil {
		return
	}
	s.verdicts.mu.Lock()
	defer s.verdicts.mu.Unlock()
	s.rememberLocked(pos, checkpointVerdict{print: print, at: s.now(), m: m})
}

func (s *Server) rememberLocked(pos uint64, v checkpointVerdict) {
	if s.verdicts.byPos == nil {
		s.verdicts.byPos = make(map[uint64]checkpointVerdict)
	}
	s.verdicts.byPos[pos] = v
}

// forgetCheckpointsExcept drops the verdicts of checkpoints no longer on disk, so the
// map stays the size of the checkpoint root.
func (s *Server) forgetCheckpointsExcept(positions []uint64) {
	keep := make(map[uint64]struct{}, len(positions))
	for _, p := range positions {
		keep[p] = struct{}{}
	}
	s.verdicts.mu.Lock()
	defer s.verdicts.mu.Unlock()
	for p := range s.verdicts.byPos {
		if _, ok := keep[p]; !ok {
			delete(s.verdicts.byPos, p)
		}
	}
}
