// Package releasenotes serves the release notes the Console's landing page shows.
//
// They are CHANGELOG.md as the binary was built from it, read while the server runs
// (ADR-draft-release-notes-from-the-changelog). That replaces the What's New feed,
// which was generated from the same file at authoring time, committed, curated per
// entry in two languages and checked for staleness in CI — every one of which was a
// step somebody had to remember on every change, and a merge conflict on every
// merge. Reading the source directly removes all of them: a changelog entry is in the
// Console the moment it is in the binary.
//
// The area owns no state, so unlike most services under api/ it holds no run loop
// (ADR-0147): the CHANGELOG is parsed once, on first use, and never changes after.
package releasenotes

import (
	"net/http"
	"sync"

	"github.com/pblumer/atlas/api/httpapi"
)

// Service answers the release-notes routes from one CHANGELOG.
type Service struct {
	src, index string
	once       sync.Once
	notes      *Notes
}

// New returns a Service over changelog, resolving the records it cites through
// adrIndex, the decision records' index. Parsing waits for the first request: the
// server is constructed far more often than the landing page is opened, by every test
// that builds one among others, and a megabyte of markdown is not free to read.
func New(changelog, adrIndex string) *Service {
	return &Service{src: changelog, index: adrIndex}
}

func (s *Service) load() *Notes {
	s.once.Do(func() { s.notes = Parse(s.src, Records(s.index)) })
	return s.notes
}

// Index is the answer to the list route.
type Index struct {
	Releases []Summary `json:"releases"`
}

// HandleList lists the releases, newest first, with the number of changes each
// carries. The changes themselves are a second request per release: the whole
// CHANGELOG is several hundred kilobytes, and the landing page opens one release.
func (s *Service) HandleList(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, Index{Releases: s.load().Releases()})
}

// HandleGet answers one release by its version, "Unreleased" included.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("version")
	rel, ok := s.load().Release(version)
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no release "+version+" in this server's release notes")
		return
	}
	httpapi.JSON(w, http.StatusOK, rel)
}
