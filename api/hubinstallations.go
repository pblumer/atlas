package api

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/pblumer/atlas/api/blueprintregistry"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/sidecar"
)

// The two states of a hub installation record.
const (
	// hubPending is written before the import writes anything, and replaced when
	// it ends. A record still pending was left by a server that stopped in between:
	// the application may hold part of the release, all of it, or none.
	hubPending = "pending"
	// hubInstalled says this server's own import of the release finished: the
	// application holds the release's source as the import's plan and decisions
	// left it. It says nothing about catalogues, constants or a published release —
	// this import does not write those.
	hubInstalled = "installed"
)

// hubInstallation is where an application installed from atlas hub came from, as
// the server-side import (POST /api/v1/blueprints/{id}/releases/{version}/import)
// fetched and wrote it: the registry the signed index names, the index URL the
// operator configured, the fingerprint of the pinned key whose signature verified
// the index, the release and its exact digest (ADR-0499, amendment g). It is keyed
// by the application's key, which is unique on an installation, and it is this
// installation's own history: it never travels with an export, and it is never an
// engine event.
type hubInstallation struct {
	ApplicationKey string `json:"applicationKey"`
	ApplicationID  string `json:"applicationId,omitempty"`
	State          string `json:"state"`
	RegistryID     string `json:"registryId"`
	IndexURL       string `json:"indexUrl"`
	SignedBy       string `json:"signedBy"`
	PublisherID    string `json:"publisherId"`
	BlueprintID    string `json:"blueprintId"`
	Version        string `json:"version"`
	Digest         string `json:"digest"`
	// CataloguesNotImported are the catalogues the package offers that this import
	// did not install; the Console installs them beside it, separately.
	CataloguesNotImported []string `json:"cataloguesNotImported"`
	InstalledBy           string   `json:"installedBy,omitempty"`
	// Expected is what the verified package carried — its processes, forms,
	// decisions, constants, catalogues and products — as the import read it before
	// writing: what the installation's status is read against (amendment h).
	// Absent on a record kept before that.
	Expected *hubExpected `json:"expected,omitempty"`
	// RecordedAt is when the record took its state, in Unix seconds.
	RecordedAt int64 `json:"recordedAt"`
}

// samePin reports whether two records name the same package from the same source.
func (h hubInstallation) samePin(o hubInstallation) bool {
	return h.RegistryID == o.RegistryID && h.IndexURL == o.IndexURL && h.SignedBy == o.SignedBy &&
		h.PublisherID == o.PublisherID && h.BlueprintID == o.BlueprintID && h.Version == o.Version &&
		h.Digest == o.Digest
}

type hubInstallationStore = sidecar.Store[hubInstallation]

func newHubInstallationStore(dir string) (*hubInstallationStore, error) {
	return sidecar.NewStore(dir, "blueprintinstallationstore", func(rec hubInstallation) string {
		return rec.ApplicationKey
	})
}

// hubJournal records one server-side hub import. Its two steps run on the run loop
// in the same turn as the import itself (importSourceFiles), so no other request
// sees the pending record or writes in between; only a server that stops halfway
// leaves it behind.
type hubJournal struct {
	store    *hubInstallationStore
	rec      hubInstallation
	previous hubInstallation
	had      bool
}

func (s *Server) newHubJournal(r *http.Request, release blueprintregistry.Release, origin blueprintregistry.Origin,
	inspection blueprintregistry.PackageInspection, constants []string) *hubJournal {
	exp := &hubExpected{PublishCatalogues: inspection.CataloguePublish}
	for _, c := range inspection.Catalogues {
		exp.Catalogues = append(exp.Catalogues, c.ID)
	}
	for _, p := range inspection.Products {
		exp.Products = append(exp.Products, p.ID)
	}
	exp.Constants = append(exp.Constants, constants...)
	rec := hubInstallation{
		ApplicationKey: inspection.ApplicationKey, State: hubPending,
		RegistryID: origin.RegistryID, IndexURL: origin.IndexURL, SignedBy: origin.SignedBy,
		PublisherID: release.PublisherID, BlueprintID: release.BlueprintID, Version: release.Version,
		Digest: release.Artifact.Digest, CataloguesNotImported: sortedUnique(exp.Catalogues),
		RecordedAt: s.now() / int64(time.Second), Expected: exp,
	}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		rec.InstalledBy = p.UserID
	}
	return &hubJournal{store: s.hubInstalls, rec: rec}
}

// expectTree adds what the source tree carries to what the record expects: its
// processes, forms and decisions, and the constants its manifest declares.
func (j *hubJournal) expectTree(man sourceManifest, offered []heldConstant) {
	exp := j.rec.Expected
	for _, p := range man.Processes {
		exp.Processes = append(exp.Processes, p.ID)
	}
	for _, f := range man.Forms {
		exp.Forms = append(exp.Forms, f.ID)
	}
	for _, d := range man.Decisions {
		exp.Decisions = append(exp.Decisions, d.ID)
	}
	for _, c := range offered {
		exp.Constants = append(exp.Constants, c.Name)
	}
	for _, list := range []*[]string{&exp.Processes, &exp.Forms, &exp.Decisions, &exp.Constants, &exp.Catalogues, &exp.Products} {
		*list = sortedUnique(*list)
	}
}

// sortedUnique is a sorted copy of list without repeats, never nil.
func sortedUnique(list []string) []string {
	out := append([]string{}, list...)
	sort.Strings(out)
	return slices.Compact(out)
}

// begin writes the pending record before the import writes anything. An error
// stops the import: an application installed without its record would be one
// nobody can trace back to its registry.
func (j *hubJournal) begin() error {
	previous, had, err := j.store.Get(j.rec.ApplicationKey)
	if err != nil {
		return fmt.Errorf("read atlas hub installation record: %w", err)
	}
	j.previous, j.had = previous, had
	if err := j.store.Save(j.rec); err != nil {
		return fmt.Errorf("record atlas hub installation: %w", err)
	}
	return nil
}

// The ways a hub import can end, as far as its record is concerned.
type hubOutcome int

const (
	// hubFinished: the import wrote the release.
	hubFinished hubOutcome = iota
	// hubStopped: the importer refused or stopped before it wrote anything — a role
	// missing, a change nobody decided.
	hubStopped
	// hubUnknown: the import failed in a way that may have left part of it written.
	hubUnknown
)

// end replaces the pending record by what the import did. A finished import
// records the release as installed — unless the same package was already recorded
// as installed in the same application, whose first record stays. An import that
// stopped wrote nothing, so the record goes back to what it was before. An import
// that failed halfway keeps its record pending: what the application holds is not
// known, and a record that said otherwise would be wrong.
func (j *hubJournal) end(res sourceImportResult, outcome hubOutcome) error {
	switch {
	case outcome == hubUnknown:
		return nil
	case outcome == hubStopped && j.had:
		return j.store.Save(j.previous)
	case outcome == hubStopped:
		return j.store.Delete(j.rec.ApplicationKey)
	case j.had && j.previous.State == hubInstalled && j.previous.samePin(j.rec) && j.previous.ApplicationID == res.ApplicationID:
		return j.store.Save(j.previous)
	default:
		j.rec.State, j.rec.ApplicationID = hubInstalled, res.ApplicationID
		return j.store.Save(j.rec)
	}
}

// handleListHubInstallations lists where this server's atlas hub installations
// came from and what of each is here now (amendment h), read from its own disk: it
// answers with the registry unreachable or not configured at all. Anybody who may
// model reads the installations into applications they may view; who installed one
// is an administrator's to read.
func (s *Server) handleListHubInstallations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var (
		recs []hubInstallation
		err  error
	)
	// The records on the loop, so a record an import is writing is never read
	// halfway; what they are compared with is read after, off it.
	s.do(func() { recs, err = s.hubInstalls.LoadAll() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read atlas hub installation records: "+err.Error())
		return
	}
	views := []hubInstallationView{}
	if len(recs) > 0 {
		holdings, err := s.hubHoldings()
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "read what the atlas hub installations installed: "+err.Error())
			return
		}
		admin := s.isAdmin(r)
		for _, rec := range recs {
			visible, err := s.hubVisible(r, rec)
			if err != nil {
				httpapi.Error(w, http.StatusInternalServerError, "read atlas hub installation records: "+err.Error())
				return
			}
			if !visible {
				continue
			}
			v, err := holdings.view(rec)
			if err != nil {
				httpapi.Error(w, http.StatusInternalServerError, "read what the atlas hub installations installed: "+err.Error())
				return
			}
			if !admin {
				v.InstalledBy = ""
			}
			views = append(views, v)
		}
	}
	sort.Slice(views, func(i, k int) bool { return views[i].ApplicationKey < views[k].ApplicationKey })
	httpapi.JSON(w, http.StatusOK, views)
}
