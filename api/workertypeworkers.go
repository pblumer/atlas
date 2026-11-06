package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/logging"
	"github.com/pblumer/atlas/workersdk"
	"github.com/pblumer/atlas/workertype"
	"github.com/pblumer/atlas/workertype/install"
)

// Configured Workers of installed Worker Types (ADR-0535 §4, migration step 4): the
// server half of a Worker Type made of data instead of code. An installed manifest
// carries its configuration contract as a JSON Schema (spec.configuration); an
// administrator configures a Worker against it; secrets are never stored in the
// Worker, only the name of the vault entry that holds each; and the launched instance
// receives the resolved configuration in the launch contract (ATLAS_WORKER_CONFIG).
// No line of this file knows any particular Worker Type.

// installedWorker is one configured Worker of an installed Worker Type.
type installedWorker struct {
	WorkerType string         `json:"workerType"`
	Name       string         `json:"name"`
	Config     map[string]any `json:"config"`
	// Secrets names, per secret property of the schema, the vault entry holding it.
	Secrets   map[string]string `json:"secrets,omitempty"`
	UpdatedAt int64             `json:"updatedAt"`
}

func (w installedWorker) key() string { return w.WorkerType + "/" + w.Name }

var installedWorkerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// secretPlaceholder stands in for a secret while the rest of a configuration is
// validated at save time: the value is the vault's, and the server does not read it
// until it launches an instance.
const secretPlaceholder = "********"

// openInstalledWorkers opens the configured Workers' store beside the artifacts.
func (s *Server) openInstalledWorkers() error {
	st, err := sidecar.NewStore[installedWorker](filepath.Join(s.workerTypesDir, ".workers"), "installed worker",
		func(w installedWorker) string { return w.key() })
	if err != nil {
		return err
	}
	s.installedWorkers = st
	return nil
}

// workersOfLocked lists the configured Workers of one Worker Type. Run loop only.
func (s *Server) workersOfLocked(typeID string) ([]installedWorker, error) {
	all, err := s.installedWorkers.LoadAll()
	if err != nil {
		return nil, err
	}
	var out []installedWorker
	for _, w := range all {
		if w.WorkerType == typeID {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// launchedWorker is one configured Worker as the launched instance receives it.
type launchedWorker struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config"`
}

// installedWorkerConfigEnv is ATLAS_WORKER_CONFIG for an instance of inst: every
// configured Worker of its type whose configuration, secrets resolved from the vault,
// satisfies this release's schema. A Worker that does not is left out and said so —
// the instance still serves the rest.
func (s *Server) installedWorkerConfigEnv(inst install.Installed) []string {
	var launched []launchedWorker
	s.do(func() {
		workers, err := s.workersOfLocked(inst.ID)
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read an installed Worker Type's configured Workers",
				slog.String("workerType", inst.ID), slog.String("error", err.Error()))
			return
		}
		schema, schemaErr := manifestConfiguration(inst)
		for _, w := range workers {
			cfg, err := s.resolvedConfig(w, schema, schemaErr)
			if err != nil {
				logging.Warn(logging.WorkerSupervisorFailed, "a configured Worker is not handed to its instance",
					slog.String("workerType", inst.ID), slog.String("worker", w.Name), slog.String("error", err.Error()))
				continue
			}
			launched = append(launched, launchedWorker{Name: w.Name, Config: cfg})
		}
	})
	if len(launched) == 0 {
		return nil
	}
	raw, err := json.Marshal(map[string]any{"workers": launched})
	if err != nil {
		return nil
	}
	return []string{workersdk.EnvConfig + "=" + string(raw)}
}

// resolvedConfig is w's configuration with its secrets read from the vault, checked
// against the release's schema. Run loop only.
func (s *Server) resolvedConfig(w installedWorker, schema *workertype.ConfigurationSchema, schemaErr error) (map[string]any, error) {
	if schemaErr != nil {
		return nil, schemaErr
	}
	if schema == nil {
		return nil, errors.New("this release declares no configuration")
	}
	cfg := make(map[string]any, len(w.Config)+len(w.Secrets))
	for k, v := range w.Config {
		cfg[k] = v
	}
	for prop, ref := range w.Secrets {
		if s.vault == nil {
			return nil, errors.New("the vault is disabled, so no secret can be resolved")
		}
		value, ok, err := s.vault.Get(ref)
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", prop, err)
		}
		if !ok {
			return nil, fmt.Errorf("secret %s names vault entry %q, which does not exist", prop, ref)
		}
		cfg[prop] = value
	}
	if err := schema.Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func manifestConfiguration(inst install.Installed) (*workertype.ConfigurationSchema, error) {
	m, err := workertype.ParseJSON(inst.Manifest)
	if err != nil {
		return nil, err
	}
	return m.Configuration()
}

// workerBody is a configured Worker as a request gives it.
type workerBody struct {
	Config  map[string]any    `json:"config"`
	Secrets map[string]string `json:"secrets"`
}

// checkWorker holds a configured Worker to every installed release of its type: no
// secret inline, a vault entry that exists for each secret named, and the rest of the
// configuration valid with each secret standing in as a placeholder. Run loop only.
func (s *Server) checkWorker(typeID string, b workerBody) error {
	releases, err := s.installedTypes.List()
	if err != nil {
		return err
	}
	found := false
	for _, inst := range releases {
		if inst.ID != typeID {
			continue
		}
		found = true
		schema, err := manifestConfiguration(inst)
		if err != nil {
			return err
		}
		if schema == nil {
			return fmt.Errorf("%s@%s declares no configuration", inst.ID, inst.Version)
		}
		probe := make(map[string]any, len(b.Config)+len(b.Secrets))
		for k, v := range b.Config {
			if schema.IsSecret(k) {
				return fmt.Errorf("%s is a secret: name the vault entry that holds it under secrets, never its value", k)
			}
			probe[k] = v
		}
		for prop := range b.Secrets {
			if !schema.IsSecret(prop) {
				return fmt.Errorf("%s is not a secret of %s@%s; give it under config", prop, inst.ID, inst.Version)
			}
			probe[prop] = secretPlaceholder
		}
		if err := schema.Validate(probe); err != nil {
			return fmt.Errorf("%s@%s: %w", inst.ID, inst.Version, err)
		}
	}
	if !found {
		return fmt.Errorf("no installed Worker Type %s", typeID)
	}
	for prop, ref := range b.Secrets {
		if s.vault == nil {
			return errors.New("the vault is disabled, so no secret can be referenced")
		}
		if _, ok, err := s.vault.Get(ref); err != nil {
			return fmt.Errorf("secret %s: %w", prop, err)
		} else if !ok {
			return fmt.Errorf("secret %s names vault entry %q, which does not exist", prop, ref)
		}
	}
	return nil
}

// handleListInstalledWorkers is GET /api/v1/worker-types/installed/{id}/workers.
func (s *Server) handleListInstalledWorkers(w http.ResponseWriter, r *http.Request) {
	if s.installedWorkers == nil {
		httpapi.Error(w, http.StatusNotFound, "this server keeps no installed Worker Types")
		return
	}
	var (
		out []installedWorker
		err error
	)
	s.do(func() { out, err = s.workersOfLocked(r.PathValue("id")) })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out == nil {
		out = []installedWorker{}
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"workers": out})
}

// handlePutInstalledWorker is PUT /api/v1/worker-types/installed/{id}/workers/{name}:
// create or replace a configured Worker. Its instances are restarted with it.
func (s *Server) handlePutInstalledWorker(w http.ResponseWriter, r *http.Request) {
	if s.installedWorkers == nil {
		httpapi.Error(w, http.StatusNotFound, "this server keeps no installed Worker Types")
		return
	}
	typeID, name := r.PathValue("id"), r.PathValue("name")
	if !installedWorkerName.MatchString(name) {
		httpapi.Error(w, http.StatusBadRequest, "a Worker's name is lower-case letters, digits and hyphens, at most 63")
		return
	}
	var b workerBody
	if err := json.NewDecoder(io.LimitReader(r.Body, workertype.MaxConfigurationSchemaBytes)).Decode(&b); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if b.Config == nil {
		b.Config = map[string]any{}
	}
	rec := installedWorker{WorkerType: typeID, Name: name, Config: b.Config, Secrets: b.Secrets, UpdatedAt: time.Now().UnixMilli()}
	var err error
	s.do(func() {
		if err = s.checkWorker(typeID, b); err != nil {
			return
		}
		err = s.installedWorkers.Save(rec)
	})
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.supervisor != nil {
		s.supervisor.refresh()
	}
	httpapi.JSON(w, http.StatusOK, rec)
}

// handleDeleteInstalledWorker is DELETE /api/v1/worker-types/installed/{id}/workers/{name}.
func (s *Server) handleDeleteInstalledWorker(w http.ResponseWriter, r *http.Request) {
	if s.installedWorkers == nil {
		httpapi.Error(w, http.StatusNotFound, "this server keeps no installed Worker Types")
		return
	}
	key := r.PathValue("id") + "/" + r.PathValue("name")
	var (
		found bool
		err   error
	)
	s.do(func() {
		if _, found, err = s.installedWorkers.Get(key); err != nil || !found {
			return
		}
		err = s.installedWorkers.Delete(key)
	})
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no configured Worker "+strings.TrimPrefix(key, "/"))
		return
	}
	if s.supervisor != nil {
		s.supervisor.refresh()
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"removed": key})
}
