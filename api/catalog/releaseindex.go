package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/pblumer/atlas/internal/sharedread"
)

// The newest releases of a catalogue, without reading the others.
//
// Every publish writes a release, and none is ever deleted short of a purge, so a
// catalogue that is worked on accumulates hundreds. The shop only wants the newest
// one of each catalogue it shows. ReleasesOf reads and decodes every release file of
// every catalogue to answer that, and the shop's list route then sent all of them:
// on a demo catalogue with 222 releases that was 4.4 MB of JSON per shop load, read
// on the catalogue's run loop.
//
// The index keeps three fields per release file: its id, its catalogue and when it
// was published. Releases are immutable, so a file once read never has to be read
// again for these. Each call lists the directory (names only), so a release written
// or removed by anything, including another process, is noticed. Only files not yet
// in the index are read, and only the n releases asked for are decoded in full.

// releaseHead is what the index keeps of one release file.
type releaseHead struct {
	ID        string `json:"id"`
	CatalogID string `json:"catalogId"`
	CreatedAt int64  `json:"createdAt"`
}

// releaseIndex maps a release file's name to its head. The mutex is there because
// some readers of the store run off the catalogue's run loop.
type releaseIndex struct {
	mu    sync.Mutex
	heads map[string]releaseHead
}

// NewestReleasesOf lists the n newest releases of one catalogue, newest first, in
// the order ReleasesOf gives. n <= 0 means all of them, which is ReleasesOf.
func (s *Store) NewestReleasesOf(catalogID string, n int) ([]Release, error) {
	if n <= 0 {
		return s.ReleasesOf(catalogID)
	}
	heads, err := s.releaseHeads()
	if err != nil {
		return nil, err
	}
	mine := heads[:0]
	for _, h := range heads {
		if h.CatalogID == catalogID {
			mine = append(mine, h)
		}
	}
	// The release store's own order: newest first, the id breaking a tie.
	sort.Slice(mine, func(i, j int) bool {
		if mine[i].CreatedAt != mine[j].CreatedAt {
			return mine[i].CreatedAt > mine[j].CreatedAt
		}
		return mine[i].ID < mine[j].ID
	})
	out := []Release{}
	for _, h := range mine {
		if len(out) == n {
			break
		}
		r, found, err := s.releases.Get(h.ID)
		if err != nil {
			return nil, err
		}
		// Removed between the listing and this read: gone is a normal state.
		if found {
			out = append(out, r)
		}
	}
	return out, nil
}

// releaseHeads brings the index up to date with the release directory and returns
// a copy of every head in it.
func (s *Store) releaseHeads() ([]releaseHead, error) {
	dir := s.releases.Dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("catalogreleasestore: read dir: %w", err)
	}
	s.index.mu.Lock()
	defer s.index.mu.Unlock()
	if s.index.heads == nil {
		s.index.heads = map[string]releaseHead{}
	}
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		seen[name] = true
		if _, known := s.index.heads[name]; known {
			continue
		}
		data, err := sharedread.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				delete(seen, name)
				continue
			}
			return nil, fmt.Errorf("catalogreleasestore: read %s: %w", name, err)
		}
		var h releaseHead
		if err := json.Unmarshal(data, &h); err != nil {
			return nil, fmt.Errorf("catalogreleasestore: decode %s: %w", name, err)
		}
		// Kept even without an id, so it is not read again; it is not listed.
		s.index.heads[name] = h
	}
	out := make([]releaseHead, 0, len(s.index.heads))
	for name, h := range s.index.heads {
		if !seen[name] {
			delete(s.index.heads, name)
			continue
		}
		// A file that is not a release (no id) is not listed.
		if h.ID != "" {
			out = append(out, h)
		}
	}
	return out, nil
}
