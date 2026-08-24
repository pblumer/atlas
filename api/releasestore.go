package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// applicationRelease is one publish of a process application: the manifest of what
// shipped together, at which artifact versions (ADR-0128).
//
// It is design-time metadata, not an engine fact — publishing does not put a record
// in the event log, and a release never participates in replay. The per-processId
// version on each member is the authoritative ADR-0019 deployment version; Version
// here is a *bundle-level* counter layered above it, answering "which release of
// this application is that?" rather than replacing the per-process number.
//
// Membership is snapshotted by value: a release records the refs and versions that
// were live at publish time, so later edits to the application's artifacts cannot
// rewrite the history of what was already shipped.
type applicationRelease struct {
	ID string `json:"id"`
	// ApplicationID is the owning application. On disk this is the ADR-0034
	// projectId — ADR-0128 renames the API/UI boundary, not the stored shape.
	ApplicationID string          `json:"applicationId"`
	Version       int32           `json:"version"` // per-application counter, 1-based
	PublishedAt   int64           `json:"publishedAt"`
	PublishedBy   string          `json:"publishedBy,omitempty"`
	Note          string          `json:"note,omitempty"`
	Members       []releaseMember `json:"members"`
}

// releaseMember is one artifact as it shipped in a release.
type releaseMember struct {
	Kind        string `json:"kind"` // "process" today; "decision"/"form" as later slices add them
	Ref         string `json:"ref"`  // processId for a process
	ArtifactVer int32  `json:"artifactVer"`
	Key         uint64 `json:"key,omitempty"` // definition key for a process
}

// releaseStore is a durable store for application releases, one JSON file per
// release id under a single directory (ADR-0128). A release belongs to an
// application, so the lookups below are by application rather than by id.
type releaseStore struct {
	*sidecar.Store[applicationRelease]
}

// newReleaseStore opens (creating if needed) the releases directory. Releases
// list grouped by application, newest version first within each, tie-broken by id
// so the order is deterministic.
func newReleaseStore(dir string) (*releaseStore, error) {
	s, err := sidecar.NewStore(dir, "releasestore",
		func(rec applicationRelease) string { return rec.ID },
		sidecar.Order(func(a, b applicationRelease) bool {
			if a.ApplicationID != b.ApplicationID {
				return a.ApplicationID < b.ApplicationID
			}
			if a.Version != b.Version {
				return a.Version > b.Version
			}
			return a.ID < b.ID
		}),
	)
	if err != nil {
		return nil, err
	}
	return &releaseStore{s}, nil
}

// forApplication returns an application's releases, newest version first. Never
// nil, so a caller can encode the result as a JSON array without a guard.
func (s *releaseStore) forApplication(appID string) ([]applicationRelease, error) {
	all, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	out := []applicationRelease{}
	for _, rec := range all {
		if rec.ApplicationID == appID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// deleteForApplication removes every release of an application — the cleanup that
// runs when the application itself is deleted. Deleting nothing is not an error.
func (s *releaseStore) deleteForApplication(appID string) error {
	all, err := s.forApplication(appID)
	if err != nil {
		return err
	}
	for _, rec := range all {
		if err := s.Delete(rec.ID); err != nil {
			return err
		}
	}
	return nil
}
