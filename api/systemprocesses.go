package api

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// The embedded platform-process bundle (ADR-0119): Atlas's own operating
// processes (user intake, access review, offboarding) and their forms, compiled
// into the binary so a fresh instance has them with no authoring step. The
// human-readable mirror lives in examples/benutzerverwaltung; these copies are
// the deployable source of truth.
//
//go:embed systemprocesses/*.bpmn systemprocesses/*.json
var systemProcessesFS embed.FS

const systemProcessesDir = "systemprocesses"

// systemProcess is one embedded process: its BPMN and the id/name parsed from it.
type systemProcess struct {
	processID string
	name      string
	xml       []byte
}

// systemForm is one embedded form: its id and the raw form-js schema document.
type systemForm struct {
	id     string
	schema []byte
}

// loadSystemBundle reads the embedded processes and forms, deterministically
// ordered by file name. A .bpmn without a <process id> or a .json without an
// "id" is a packaging error, surfaced so New fails loudly rather than starting
// with a half-populated system project.
func loadSystemBundle() ([]systemProcess, []systemForm, error) {
	entries, err := fs.ReadDir(systemProcessesFS, systemProcessesDir)
	if err != nil {
		return nil, nil, fmt.Errorf("system bundle: read dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var procs []systemProcess
	var forms []systemForm
	for _, name := range names {
		data, err := systemProcessesFS.ReadFile(systemProcessesDir + "/" + name)
		if err != nil {
			return nil, nil, fmt.Errorf("system bundle: read %s: %w", name, err)
		}
		switch {
		case strings.HasSuffix(name, ".bpmn"):
			pid, pname := processIdentity(data)
			if pid == "" {
				return nil, nil, fmt.Errorf("system bundle: %s has no <process id>", name)
			}
			procs = append(procs, systemProcess{processID: pid, name: pname, xml: data})
		case strings.HasSuffix(name, ".json"):
			var doc struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				return nil, nil, fmt.Errorf("system bundle: %s is not valid JSON: %w", name, err)
			}
			if strings.TrimSpace(doc.ID) == "" {
				return nil, nil, fmt.Errorf("system bundle: %s has no \"id\"", name)
			}
			forms = append(forms, systemForm{id: doc.ID, schema: data})
		}
	}
	return procs, forms, nil
}

// sha256Hex returns the hex SHA-256 of b, the idempotency key for a system
// deployment: identical bytes hash identically, so an unchanged embedded process
// is recognized as already deployed and not re-versioned.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// latestDeploymentFor returns the highest-version deployment of a process id, if
// any. It must be called on the run-loop goroutine (or, at bootstrap, before the
// loop serves). It backs the idempotency check: the deployed definitions are
// themselves the durable state, so no separate bookkeeping store is needed.
func (s *Server) latestDeploymentFor(pid string) (*deployment, bool) {
	var best *deployment
	for _, d := range s.deployments {
		if d.ProcessID == pid && (best == nil || d.Version > best.Version) {
			best = d
		}
	}
	return best, best != nil
}

// ensureSystemProcesses bootstrap-deploys the embedded platform processes into
// the protected system project (ADR-0119): it seeds their forms, files their
// drafts under the system project, and deploys each process only when it is
// absent or its embedded bytes differ from the newest deployed version — so a
// restart with the same binary adds no versions, and a changed process in a new
// binary adds exactly one. It records the system process ids so the delete guard
// can refuse removing a platform definition.
//
// Like loadDeployments, it runs on the constructing goroutine after recovery and
// before the loop serves traffic, so it touches the stores, the deployment
// registry, and the processor directly, within the single-writer discipline.
func (s *Server) ensureSystemProcesses(now int64) error {
	procs, forms, err := loadSystemBundle()
	if err != nil {
		return err
	}
	s.systemPIDs = make(map[string]bool, len(procs))
	for _, p := range procs {
		s.systemPIDs[p.processID] = true
	}

	// Seed forms (idempotent overwrite) filed under the system project.
	for _, f := range forms {
		if err := s.forms.save(form{ID: f.id, Name: f.id, ProjectID: systemProjectID, SavedAt: now, Schema: string(f.schema)}); err != nil {
			return fmt.Errorf("seed system form %s: %w", f.id, err)
		}
	}

	for _, p := range procs {
		// File the draft under the system project so the Modeler lists it there.
		if err := s.drafts.save(draft{ProcessID: p.processID, Name: p.name, ProjectID: systemProjectID, SavedAt: now, XML: string(p.xml)}); err != nil {
			return fmt.Errorf("file system draft %s: %w", p.processID, err)
		}
		// Idempotent deploy: skip when the newest deployed version is byte-identical.
		if d, ok := s.latestDeploymentFor(p.processID); ok && sha256Hex(d.xml) == sha256Hex(p.xml) {
			continue
		}
		if _, compErr, persistErr := s.deployModel(p.xml, nil, now); compErr != nil {
			// A compile failure here is a packaging error, not a client error.
			return fmt.Errorf("compile system process %s: %w", p.processID, compErr)
		} else if persistErr != nil {
			return fmt.Errorf("deploy system process %s: %w", p.processID, persistErr)
		}
	}
	return nil
}
