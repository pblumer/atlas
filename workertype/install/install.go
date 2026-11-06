// Package install keeps the Worker Type artifacts installed on this server: a
// content-addressed store under the data directory, from which the supervisor
// launches a Worker Type's executable after verifying it again (ADR-0535 §2,
// migration step 3).
//
// An installed release lives at <dir>/<id>/<version>/<hex digest>/, holding the
// executable and installed.json (the manifest as given, and what this server
// recorded about it). A release is immutable: installing the same id and version
// again with the same digest changes nothing, with another digest is refused. The
// digest is checked when the artifact is installed and again before every launch,
// so a file altered on disk is refused rather than run.
//
// Nothing here runs on the engine's run loop or touches durable engine state: an
// installed Worker Type is design-time state on disk, like the other sidecar stores.
package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/internal/dirsync"
	"github.com/pblumer/atlas/workertype"
)

// MaxArtifactBytes bounds one installed executable. A Worker Type is one process's
// code; a file larger than this is not one, and the bound keeps an install from
// filling the data directory.
const MaxArtifactBytes = 512 << 20

// MaxManifestBytes bounds a manifest, as workertype does for a contract.
const MaxManifestBytes = 256 << 10

const metadataFile = "installed.json"

// Origin says where an installed release came from. Only a local file exists before
// the registry of step 5; a signed release from atlas hub will add its own.
const OriginFile = "file"

// Installed is one installed Worker Type release.
type Installed struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	// Digest is the artifact's sha256 as the manifest declared it and as it was
	// verified at install.
	Digest   string   `json:"digest"`
	JobTypes []string `json:"jobTypes"`
	Origin   string   `json:"origin"`
	// Signed is false until signature verification exists (ADR-0535 §6, step 5):
	// every installed release is then an unsigned, locally installed one, and is
	// shown as such.
	Signed      bool  `json:"signed"`
	InstalledAt int64 `json:"installedAt"`
	// Path is the executable's location, filled in when read; it is not stored.
	Path string `json:"-"`
	// Manifest is the manifest exactly as it was installed.
	Manifest json.RawMessage `json:"manifest"`
}

// Store is the installed Worker Types under one directory. Its methods are safe for
// concurrent use; installs and removals are serialized.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open opens (creating if needed) the store under dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("worker type store: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Platform is this process's platform as a manifest names it, os/arch.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// ErrConflict is an install of a version already installed with other content.
var ErrConflict = errors.New("this Worker Type version is already installed with another artifact; a release is immutable, so publish a new version")

// Install verifies manifest and the artifact read from r against each other and
// records the release. The manifest must declare an artifact for platform, of the
// executable media type, whose sha256 the bytes read from r match.
func (s *Store) Install(manifest []byte, r io.Reader, platform string, now time.Time) (Installed, error) {
	if len(manifest) > MaxManifestBytes {
		return Installed{}, fmt.Errorf("manifest is larger than %d bytes", MaxManifestBytes)
	}
	m, err := workertype.ParseJSON(manifest)
	if err != nil {
		return Installed{}, err
	}
	if m.Spec.Runtime.Mode != workertype.RuntimeExternal {
		return Installed{}, fmt.Errorf("spec.runtime.mode is %q; only an external runtime is installed as an artifact", m.Spec.Runtime.Mode)
	}
	a, ok := m.ArtifactFor(platform)
	if !ok {
		return Installed{}, fmt.Errorf("the manifest declares no artifact for %s", platform)
	}
	if a.MediaType != workertype.MediaTypeExecutable {
		return Installed{}, fmt.Errorf("the %s artifact is %s; this server launches only %s", platform, a.MediaType, workertype.MediaTypeExecutable)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	incoming := filepath.Join(s.dir, ".incoming")
	if err := os.MkdirAll(incoming, 0o700); err != nil {
		return Installed{}, err
	}
	tmp, err := os.CreateTemp(incoming, "artifact-*")
	if err != nil {
		return Installed{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, MaxArtifactBytes+1))
	if err == nil && n > MaxArtifactBytes {
		err = fmt.Errorf("the artifact is larger than %d bytes", MaxArtifactBytes)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Installed{}, err
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if got != a.Digest {
		return Installed{}, fmt.Errorf("the artifact's digest is %s, the manifest declares %s; nothing was installed", got, a.Digest)
	}

	versionDir := filepath.Join(s.dir, m.Metadata.ID, m.Metadata.Version)
	if existing, err := s.readVersionLocked(versionDir); err == nil {
		if existing.Digest == a.Digest {
			return existing, nil
		}
		return Installed{}, ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return Installed{}, err
	}

	inst := Installed{
		ID: m.Metadata.ID, Version: m.Metadata.Version, Name: m.Metadata.Name,
		Platform: platform, Digest: a.Digest, JobTypes: append([]string(nil), m.Spec.JobTypes...),
		Origin: OriginFile, InstalledAt: now.UnixMilli(), Manifest: json.RawMessage(manifest),
	}
	releaseDir := filepath.Join(versionDir, strings.TrimPrefix(a.Digest, "sha256:"))
	if err := os.MkdirAll(releaseDir, 0o700); err != nil {
		return Installed{}, err
	}
	exe := filepath.Join(releaseDir, executableName())
	if err := os.Chmod(tmp.Name(), 0o700); err != nil {
		return Installed{}, err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		return Installed{}, err
	}
	meta, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return Installed{}, err
	}
	// The metadata is written last: a release without it is an interrupted install,
	// which List skips and a repeated Install completes.
	if err := writeFileSynced(filepath.Join(releaseDir, metadataFile), meta); err != nil {
		return Installed{}, err
	}
	for _, d := range []string{releaseDir, versionDir, filepath.Dir(versionDir), s.dir} {
		if err := syncDir(d); err != nil {
			return Installed{}, err
		}
	}
	inst.Path = exe
	return inst, nil
}

// List returns every installed release, ordered by id and version.
func (s *Store) List() ([]Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []Installed
	for _, id := range ids {
		if !id.IsDir() || strings.HasPrefix(id.Name(), ".") {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(s.dir, id.Name()))
		if err != nil {
			return nil, err
		}
		for _, v := range versions {
			if !v.IsDir() {
				continue
			}
			inst, err := s.readVersionLocked(filepath.Join(s.dir, id.Name(), v.Name()))
			if errors.Is(err, os.ErrNotExist) {
				continue // an interrupted install
			}
			if err != nil {
				return nil, err
			}
			out = append(out, inst)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// Verified returns the installed release id@version after hashing its executable
// again: an artifact whose bytes no longer match the digest it was installed with is
// refused, so the supervisor never launches a file altered on disk.
func (s *Store) Verified(id, version string) (Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, err := s.readVersionLocked(filepath.Join(s.dir, id, version))
	if err != nil {
		return Installed{}, err
	}
	f, err := os.Open(inst.Path)
	if err != nil {
		return Installed{}, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, MaxArtifactBytes+1)); err != nil {
		return Installed{}, err
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != inst.Digest {
		return Installed{}, fmt.Errorf("installed artifact %s@%s no longer matches its digest (%s, installed as %s); refusing to run it", id, version, got, inst.Digest)
	}
	return inst, nil
}

// Remove deletes the installed release id@version. Removing one that is not
// installed is not an error.
func (s *Store) Remove(id, version string) error {
	if !validSegment(id) || !validSegment(version) {
		return fmt.Errorf("no installed Worker Type %s@%s", id, version)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	versionDir := filepath.Join(s.dir, id, version)
	if err := os.RemoveAll(versionDir); err != nil {
		return err
	}
	idDir := filepath.Dir(versionDir)
	if rest, err := os.ReadDir(idDir); err == nil && len(rest) == 0 {
		_ = os.Remove(idDir)
	}
	return syncDir(s.dir)
}

// readVersionLocked reads the one release under a version directory.
func (s *Store) readVersionLocked(versionDir string) (Installed, error) {
	if !strings.HasPrefix(filepath.Clean(versionDir), filepath.Clean(s.dir)+string(filepath.Separator)) {
		return Installed{}, os.ErrNotExist
	}
	digests, err := os.ReadDir(versionDir)
	if err != nil {
		return Installed{}, err
	}
	for _, d := range digests {
		if !d.IsDir() {
			continue
		}
		releaseDir := filepath.Join(versionDir, d.Name())
		raw, err := os.ReadFile(filepath.Join(releaseDir, metadataFile))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Installed{}, err
		}
		var inst Installed
		if err := json.Unmarshal(raw, &inst); err != nil {
			return Installed{}, fmt.Errorf("%s: %w", filepath.Join(releaseDir, metadataFile), err)
		}
		if strings.TrimPrefix(inst.Digest, "sha256:") != d.Name() {
			return Installed{}, fmt.Errorf("%s records digest %s under directory %s", releaseDir, inst.Digest, d.Name())
		}
		inst.Path = filepath.Join(releaseDir, executableName())
		return inst, nil
	}
	return Installed{}, os.ErrNotExist
}

func validSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`)
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "worker.exe"
	}
	return "worker"
}

func writeFileSynced(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func syncDir(dir string) error {
	d, err := dirsync.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
