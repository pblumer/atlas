package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/workersdk"
	"github.com/pblumer/atlas/workertype/install"
)

// Installed Worker Types (ADR-0535, migration step 3): an artifact an operator
// installs is kept in a content-addressed store under the data directory, verified
// against its manifest's digest, and launched by the supervisor — which verifies it
// again before every start, gives it an allowlisted environment and the launch
// contract, and never the server's own environment.
//
// Until signatures exist (step 5) every installed artifact is unsigned, and the
// server refuses to install one unless its operator started it with
// --allow-unsigned-worker-types. That switch is for development; it is the
// operator's own command line, never a request.

// WithInstalledWorkerTypes keeps installed Worker Type artifacts under dir and
// launches them, telling each to work for serverURL. allowUnsigned admits unsigned
// artifacts, for development only.
func WithInstalledWorkerTypes(dir, serverURL string, allowUnsigned bool) Option {
	return func(s *Server) {
		s.workerTypesDir = dir
		s.installedURL = serverURL
		s.allowUnsignedWorkerTypes = allowUnsigned
	}
}

// installedSupervisorID is the supervised worker an installed release runs as.
func installedSupervisorID(id, version string) string {
	return "workertype:" + id + "@" + version
}

// openInstalledWorkerTypes opens the store, if one was configured.
func (s *Server) openInstalledWorkerTypes() error {
	if s.workerTypesDir == "" {
		return nil
	}
	st, err := install.Open(s.workerTypesDir)
	if err != nil {
		return err
	}
	s.installedTypes = st
	return s.openInstalledWorkers()
}

// superviseInstalled registers every installed release with the supervisor, creating
// it when the command line asked for no other worker. It runs before start.
func (s *Server) superviseInstalled(quit <-chan struct{}) {
	if s.installedTypes == nil {
		return
	}
	list, err := s.installedTypes.List()
	if err != nil {
		logging.Warn(logging.WorkerSupervisorFailed, "could not read the installed Worker Types; none is started",
			slog.String("error", err.Error()))
		return
	}
	// The supervisor exists whenever a store does, even an empty one: an artifact
	// installed later is added to it, and it is never created after start, so no
	// request swaps it out from under the Workers view.
	if s.supervisor == nil {
		s.supervisor = newSupervisor(quit)
	}
	for _, inst := range list {
		s.superviseRelease(inst)
	}
}

// superviseRelease adds one installed release to the supervisor.
func (s *Server) superviseRelease(inst install.Installed) {
	id := installedSupervisorID(inst.ID, inst.Version)
	store, typeID, version := s.installedTypes, inst.ID, inst.Version
	spec := SuperviseSpec{
		ID: id, Kinds: append([]string(nil), inst.JobTypes...),
		Executable: inst.Path,
		Verify: func() error {
			_, err := store.Verified(typeID, version)
			return err
		},
	}
	env := func() []string {
		out := []string{
			workersdk.EnvServer + "=" + s.superviseURLOrDefault(),
			workersdk.EnvWorkerID + "=" + id,
			workersdk.EnvTypeID + "=" + inst.ID,
			workersdk.EnvTypeVersion + "=" + inst.Version,
			workersdk.EnvTypeDigest + "=" + inst.Digest,
		}
		out = append(out, s.installedWorkerConfigEnv(inst)...)
		return append(out, s.workerTokenEnv()...)
	}
	s.supervisor.addLive(spec, nil, env)
}

// superviseURLOrDefault is where a launched instance reaches this server.
func (s *Server) superviseURLOrDefault() string {
	if s.installedURL != "" {
		return s.installedURL
	}
	return s.superviseURL
}

// installedView is one installed release as the API shows it.
type installedView struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Name        string   `json:"name,omitempty"`
	Platform    string   `json:"platform"`
	Digest      string   `json:"digest"`
	JobTypes    []string `json:"jobTypes"`
	Origin      string   `json:"origin"`
	Signed      bool     `json:"signed"`
	InstalledAt int64    `json:"installedAt"`
	// Supervised is the worker id the supervisor runs it under.
	Supervised string `json:"supervised"`
}

func viewOf(inst install.Installed) installedView {
	return installedView{
		ID: inst.ID, Version: inst.Version, Name: inst.Name, Platform: inst.Platform,
		Digest: inst.Digest, JobTypes: inst.JobTypes, Origin: inst.Origin, Signed: inst.Signed,
		InstalledAt: inst.InstalledAt, Supervised: installedSupervisorID(inst.ID, inst.Version),
	}
}

// handleListInstalledWorkerTypes is GET /api/v1/worker-types/installed.
func (s *Server) handleListInstalledWorkerTypes(w http.ResponseWriter, _ *http.Request) {
	out := []installedView{}
	if s.installedTypes != nil {
		list, err := s.installedTypes.List()
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "read the installed Worker Types: "+err.Error())
			return
		}
		for _, inst := range list {
			out = append(out, viewOf(inst))
		}
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{
		"installed": out, "platform": install.Platform(), "allowUnsigned": s.allowUnsignedWorkerTypes,
	})
}

// handleInstallWorkerType is POST /api/v1/worker-types/installed: a multipart body
// with the manifest and the artifact for this server's platform. The artifact is
// verified against the manifest's digest before anything is kept, and the release
// is started at once.
func (s *Server) handleInstallWorkerType(w http.ResponseWriter, r *http.Request) {
	if s.installedTypes == nil {
		httpapi.Error(w, http.StatusConflict, "this server keeps no installed Worker Types")
		return
	}
	if !s.allowUnsignedWorkerTypes {
		httpapi.Error(w, http.StatusForbidden, "unsigned Worker Type artifacts are refused: signatures are not verified yet, "+
			"so only a server started with --allow-unsigned-worker-types installs one — for development only (ADR-0535 §8)")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, install.MaxArtifactBytes+install.MaxManifestBytes+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "want multipart/form-data with the parts manifest and artifact: "+err.Error())
		return
	}
	var manifest []byte
	var inst install.Installed
	installed := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "read the upload: "+err.Error())
			return
		}
		switch part.FormName() {
		case "manifest":
			if manifest, err = io.ReadAll(io.LimitReader(part, install.MaxManifestBytes+1)); err != nil {
				httpapi.Error(w, http.StatusBadRequest, "read the manifest: "+err.Error())
				return
			}
		case "artifact":
			if manifest == nil {
				httpapi.Error(w, http.StatusBadRequest, "the manifest part must come before the artifact")
				return
			}
			if inst, err = s.installedTypes.Install(manifest, part, install.Platform(), time.Now()); err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, install.ErrConflict) {
					status = http.StatusConflict
				}
				httpapi.Error(w, status, err.Error())
				return
			}
			installed = true
		}
		_ = part.Close()
	}
	if !installed {
		httpapi.Error(w, http.StatusBadRequest, "want the parts manifest and artifact")
		return
	}
	logging.Info(logging.WorkerSupervisorStarted, "installed a Worker Type artifact",
		slog.String("id", inst.ID), slog.String("version", inst.Version), slog.String("digest", inst.Digest),
		slog.Bool("signed", inst.Signed))
	if s.supervisor != nil && !s.supervisor.has(installedSupervisorID(inst.ID, inst.Version)) {
		s.superviseRelease(inst)
	}
	httpapi.JSON(w, http.StatusCreated, viewOf(inst))
}

// handleRemoveInstalledWorkerType is DELETE /api/v1/worker-types/installed/{id}/{version}:
// its instance is stopped, then its files removed.
func (s *Server) handleRemoveInstalledWorkerType(w http.ResponseWriter, r *http.Request) {
	if s.installedTypes == nil {
		httpapi.Error(w, http.StatusNotFound, "this server keeps no installed Worker Types")
		return
	}
	id, version := strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("version"))
	if _, err := s.installedTypes.Verified(id, version); errors.Is(err, os.ErrNotExist) {
		httpapi.Error(w, http.StatusNotFound, "no installed Worker Type "+id+"@"+version)
		return
	}
	if s.supervisor != nil {
		s.supervisor.remove(installedSupervisorID(id, version))
	}
	if err := s.installedTypes.Remove(id, version); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"removed": id + "@" + version})
}
