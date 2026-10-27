package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/pblumer/atlas/api/blueprintregistry"
	"github.com/pblumer/atlas/api/httpapi"
)

// handleBeginBlueprintInstallation durably journals an explicitly selected,
// still-verified Blueprint BEFORE the native importer may write any artifacts.
// It intentionally never marks a journal entry complete. Catalogue import and
// application publish are multiple independent fsyncs: only a future server-side
// reconciliation may make that claim (BMP1, issue #1422).
func (s *Server) handleBeginBlueprintInstallation(w http.ResponseWriter, r *http.Request) {
	if !s.requireBlueprints(w) {
		return
	}
	if r.Header.Get("If-Match") == "" {
		httpapi.Error(w, http.StatusPreconditionRequired, "preview a Blueprint before beginning an installation")
		return
	}
	release, compressed, registryID, ok := s.selectedBlueprint(w, r)
	if !ok {
		return
	}
	if r.Header.Get("If-Match") != "\"" + release.Artifact.Digest + "\"" {
		httpapi.Error(w, http.StatusPreconditionFailed, "Blueprint digest changed since preview; review again")
		return
	}
	inspected, err := blueprintregistry.InspectPackage(release, compressed)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "Blueprint package failed safe archive inspection")
		return
	}
	origin, fingerprint := s.blueprints.TrustIdentity()
	catalogueIDs := make([]string, 0, len(inspected.Catalogues))
	for _, c := range inspected.Catalogues {
		catalogueIDs = append(catalogueIDs, c.ID)
	}
	sort.Strings(catalogueIDs)
	rec := blueprintInstallation{
		ApplicationKey: inspected.ApplicationKey, RegistryID: registryID,
		RegistryOrigin: origin, SignerFingerprint: fingerprint,
		PublisherID: release.PublisherID, BlueprintID: release.BlueprintID,
		Version: release.Version, Digest: release.Artifact.Digest,
		CatalogueIDs: catalogueIDs, Status: "pending", RequestedAt: s.now() / int64(time.Second),
	}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		rec.RequestedBy = p.UserID
	}
	var status int
	var errSave error
	s.do(func() {
		// This check is on the same single-writer loop as source imports.
		// A Modeler without editor access must not pin somebody else's
		// existing private application or the protected platform project.
		apps, err := s.projects.LoadAll()
		if err != nil {
			errSave = err
			return
		}
		for _, app := range apps {
			if app.Key != rec.ApplicationKey {
				continue
			}
			if app.Protected {
				status = http.StatusConflict
				return
			}
			if code, _ := s.checkProjectRole(r, app, ScopeRoleEditor); code != 0 {
				status = code
				return
			}
			break
		}
		previous, found, err := s.blueprintInstalls.Get(rec.ApplicationKey)
		if err != nil {
			errSave = err
			return
		}
		if found {
			// Retries of this exact pin leave its original pending journal
			// byte-for-byte unchanged. A different source, version or digest
			// cannot overwrite a previous unfinished install.
			if previous.RegistryID == rec.RegistryID &&
				previous.RegistryOrigin == rec.RegistryOrigin &&
				previous.SignerFingerprint == rec.SignerFingerprint &&
				previous.PublisherID == rec.PublisherID &&
				previous.BlueprintID == rec.BlueprintID &&
				previous.Version == rec.Version &&
				previous.Digest == rec.Digest {
				rec = previous
				return
			}
			status = http.StatusConflict
			return
		}
		errSave = s.blueprintInstalls.Save(rec)
	})
	if errSave != nil {
		httpapi.Error(w, http.StatusInternalServerError, "record pending Blueprint installation: "+errSave.Error())
		return
	}
	if status != 0 {
		// Do not leak a private application's identity when its editor gate
		// intentionally reports 404.
		httpapi.Error(w, status, "Blueprint application cannot be reserved for installation")
		return
	}
	httpapi.JSON(w, http.StatusOK, rec)
}

// handleListBlueprintInstallations shows the installation's durable journal
// to administrators even if the public registry is unavailable. Pending is a
// warning about incomplete work, not proof of a successfully installed package.
func (s *Server) handleListBlueprintInstallations(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var (
		recs []blueprintInstallation
		err  error
	)
	s.do(func() { recs, err = s.blueprintInstalls.LoadAll() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read Blueprint installation journal: "+err.Error())
		return
	}
	if recs == nil {
		recs = []blueprintInstallation{}
	}
	httpapi.JSON(w, http.StatusOK, recs)
}
