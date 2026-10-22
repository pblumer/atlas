package api

import (
	"time"

	"github.com/pblumer/atlas/api/sidecar"
)

// importBaseline is what this installation's imports remember of one scope: per
// artifact, the hash of the version the last import brought (ADR-0467,
// amended). It is the third version an import compares with, and what tells an
// artifact edited here from one the import has moved on from.
//
// A scope is an application ("application:<id>"), for what its source tree and its
// constants carry, or the catalogue ("catalogue"), for what catalogue documents
// carry. The hashes are importplan.Hash over the parts each kind is compared by, so a
// base says only whether a version is the one last imported, never what it was.
type importBaseline struct {
	Scope     string            `json:"scope"`
	Hashes    map[string]string `json:"hashes"`
	UpdatedAt int64             `json:"updatedAt"`
}

// importBaselineStore is a durable store for the bases, one JSON file per scope —
// the sidecar approach every design-time store uses. Like them it is owned by the
// run-loop goroutine.
type importBaselineStore = sidecar.Store[importBaseline]

func newImportBaselineStore(dir string) (*importBaselineStore, error) {
	return sidecar.NewStore(dir, "importbaselinestore", func(rec importBaseline) string { return rec.Scope })
}

// applicationBaselineScope is the scope an application's imports remember under. It
// names the application by its id on this server: the bases are this server's.
func applicationBaselineScope(appID string) string { return "application:" + appID }

// catalogueBaselineScope is the scope catalogue documents remember under.
const catalogueBaselineScope = "catalogue"

// importBaselines answers the bases of a scope, empty when no import wrote any.
// Runs on the run loop.
func (s *Server) importBaselines(scope string) (map[string]string, error) {
	rec, ok, err := s.baselines.Get(scope)
	if err != nil || !ok {
		return map[string]string{}, err
	}
	if rec.Hashes == nil {
		return map[string]string{}, nil
	}
	return rec.Hashes, nil
}

// recordImportBaselines merges hashes into a scope's bases. Runs on the run loop.
func (s *Server) recordImportBaselines(scope string, hashes map[string]string) error {
	if len(hashes) == 0 {
		return nil
	}
	rec, _, err := s.baselines.Get(scope)
	if err != nil {
		return err
	}
	if rec.Hashes == nil {
		rec.Hashes = map[string]string{}
	}
	for ref, h := range hashes {
		rec.Hashes[ref] = h
	}
	rec.Scope, rec.UpdatedAt = scope, time.Now().Unix()
	return s.baselines.Save(rec)
}

// forgetImportBaselines drops refs from a scope's bases, so the next import of a
// document carrying them meets them as new rather than as records somebody here
// deleted on purpose. Runs on the run loop.
func (s *Server) forgetImportBaselines(scope string, refs []string) error {
	if s.baselines == nil {
		return nil
	}
	rec, ok, err := s.baselines.Get(scope)
	if err != nil || !ok {
		return err
	}
	forgot := false
	for _, ref := range refs {
		if _, had := rec.Hashes[ref]; had {
			delete(rec.Hashes, ref)
			forgot = true
		}
	}
	if !forgot {
		return nil
	}
	rec.UpdatedAt = time.Now().Unix()
	return s.baselines.Save(rec)
}

// catalogBaselines is the catalogue service's memory of what the last import of a
// document brought, kept in this server's baseline store under the catalogue's
// scope. The service calls it on the run loop it shares with the server.
type catalogBaselines struct{ s *Server }

func (b catalogBaselines) ImportBaselines() (map[string]string, error) {
	return b.s.importBaselines(catalogueBaselineScope)
}

func (b catalogBaselines) RecordImportBaselines(hashes map[string]string) error {
	return b.s.recordImportBaselines(catalogueBaselineScope, hashes)
}
