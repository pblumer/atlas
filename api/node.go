package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// The Atlas node descriptor (ADR-0189 §6, P4a).
//
// Live correlation across servers needs a stable identity for each one, and
// `/api/v1/info` cannot provide it: it reports what the *binary* is, not which
// *runtime* is answering. Two servers built from one commit are indistinguishable
// through it, and one server that restarts looks like a different answer to
// nothing at all. Everything P4 does — resolving a deployment-target binding into
// a live status, telling "this node is unreachable" from "some node is" — starts
// from an identity that survives a restart and is the same string every caller
// sees.
//
// Three constraints from the record are structural here rather than documented:
//
//   - The descriptor never returns credentials, environment variables, filesystem
//     paths, or secret material. Everything in it is either operator-authored, a
//     build constant, or derived from the mounted route table. Nothing reads the
//     process environment or the data directory's layout.
//   - The id is generated once and persisted, or set by the operator. A generated
//     id that changed on restart would be worse than none: it would make one node
//     look like an endless series of new ones.
//   - Remote access uses a least-privilege scope of its own (apiScopeStatus). A
//     correlator that needs to read this must not be handed deploy rights to get
//     it.

// maxNodeLabels and maxNodeLabelLen bound what an operator can attach. The
// descriptor is read by other servers, so it is a payload this node hands out
// unauthenticated-by-them and must stay small and predictable; a label map is a
// place free-form text otherwise accumulates without limit.
const (
	maxNodeLabels    = 20
	maxNodeLabelLen  = 200
	maxNodeFieldLen  = 200
	maxNodeBodyBytes = 16 << 10
)

// nodeIdentity is the operator-owned half of the descriptor, persisted under the
// data directory. It is a singleton setting like the theme, not a record keyed by
// id: a server is one node.
type nodeIdentity struct {
	ID          string            `json:"id"`
	Name        string            `json:"name,omitempty"`
	Environment string            `json:"environment,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// nodeDescriptor is what the route returns: the operator-owned identity, what this
// binary is, and what this build can be asked for.
type nodeDescriptor struct {
	ID          string            `json:"id"`
	Name        string            `json:"name,omitempty"`
	Environment string            `json:"environment,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`

	Product   string `json:"product"`
	Version   string `json:"version"`
	Revision  string `json:"revision,omitempty"`
	BuildTime string `json:"buildTime,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
	Go        string `json:"go,omitempty"`

	// Partition is the one this process drives, and Partitions how many it drives.
	// Atlas is one partition per process (invariant I3), so the count is 1 today —
	// stated rather than assumed, because a correlator reading a descriptor from an
	// unknown version must not have to infer it.
	Partition  uint16 `json:"partition"`
	Partitions int    `json:"partitions"`

	// Features are what this node can be asked for, derived from the routes it
	// actually mounts. Deriving rather than listing is the point: a hand-kept list
	// is a claim that goes stale the first time a route is renamed, and a node that
	// advertises a capability it does not serve is worse to a correlator than one
	// that advertises nothing.
	Features []string `json:"features"`
}

// nodeFeatures maps each advertised feature id to the route that proves it. The
// ids are deliberately coarse — a correlator asks "can I read observations here",
// not "which query parameters does this build accept" — and named for what they
// let a caller learn rather than for the handler behind them.
var nodeFeatures = map[string]string{
	"observations.stats":     "GET /api/v1/stats",
	"observations.processes": "GET /api/v1/processes",
	"observations.instances": "GET /api/v1/instances",
	"observations.runtime":   "GET /api/v1/processes/{key}/runtime",
	"applications.releases":  "GET /api/v1/applications/{id}/releases",
	"panorama.models":        "GET /api/v1/panorama/models",
	"panorama.mesh":          "GET /api/v1/panorama/mesh",
	"panorama.bindings":      "GET /api/v1/panorama/models/{id}/bindings",
	"panorama.c4":            "GET /api/v1/panorama/models/{id}/c4",
}

// supportedFeatures returns the feature ids whose route this server mounts, sorted.
// A feature whose route is gone stops being advertised without anybody editing a
// second list.
func (s *Server) supportedFeatures() []string {
	mounted := map[string]bool{}
	for _, r := range s.apiRoutes() {
		mounted[r.method+" "+r.pattern] = true
	}
	out := make([]string, 0, len(nodeFeatures))
	for id, route := range nodeFeatures {
		if mounted[route] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// newNodeID mints the stable runtime id. It is 16 random bytes rather than
// anything derived from the host: a hostname, a MAC address or a data-directory
// path would leak where this server runs into a document other servers read.
func newNodeID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("node: mint id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ensureNodeIdentity reads the stored identity, minting and persisting an id the
// first time. Called once at startup rather than lazily on first read, so the id a
// caller sees is never the side effect of somebody's GET — and so a server whose
// data directory is read-only fails at startup, where an operator will see it,
// rather than on a request weeks later.
func ensureNodeIdentity(settings *settingsStore) (nodeIdentity, error) {
	identity, ok, err := settings.getNode()
	if err != nil {
		return nodeIdentity{}, err
	}
	if ok && identity.ID != "" {
		return identity, nil
	}
	if identity.ID, err = newNodeID(); err != nil {
		return nodeIdentity{}, err
	}
	if err := settings.saveNode(identity); err != nil {
		return nodeIdentity{}, err
	}
	return identity, nil
}

// nodeIdentity reads the stored identity, treating its absence as a failure rather
// than as a default.
//
// Startup mints the id before the first request is served (see ensureNodeIdentity),
// so nothing here can be missing on a healthy server: an absent record means the
// data directory has gone missing underneath a running one. Every caller of this
// is publishing an identity — the descriptor, or the runtime catalog a model binds
// against — and a blank one is not a partial answer, it is a wrong one. Refusing
// keeps a broken directory from reading as "this node has no name" on one route and
// "no such runtime exists" on the other.
//
// Run-loop goroutine only: it reads the settings store.
func (s *Server) nodeIdentity() (nodeIdentity, error) {
	identity, ok, err := s.settings.getNode()
	if err != nil {
		return nodeIdentity{}, err
	}
	if !ok || identity.ID == "" {
		return nodeIdentity{}, fmt.Errorf("node: no identity is stored")
	}
	return identity, nil
}

// describeNode assembles the descriptor. Run-loop goroutine only: it reads the
// settings store.
func (s *Server) describeNode() (nodeDescriptor, error) {
	identity, err := s.nodeIdentity()
	if err != nil {
		return nodeDescriptor{}, err
	}
	b := buildInfo()
	return nodeDescriptor{
		ID: identity.ID, Name: identity.Name, Environment: identity.Environment,
		Labels:  identity.Labels,
		Product: "Atlas", Version: Version,
		Revision: b.Revision, BuildTime: b.Time, Modified: b.Modified, Go: b.Go,
		Partition: s.proc.Partition(), Partitions: 1,
		Features: s.supportedFeatures(),
	}, nil
}

// handleNode returns this server's node descriptor.
func (s *Server) handleNode(w http.ResponseWriter, _ *http.Request) {
	var (
		desc nodeDescriptor
		err  error
		ran  bool
	)
	s.do(func() { ran = true; desc, err = s.describeNode() })
	switch {
	case !ran:
		// The loop is closing. An identity is a claim about which server answered,
		// so an empty one is not a lesser answer, it is a wrong one.
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read node identity: "+err.Error())
	default:
		httpapi.JSON(w, http.StatusOK, desc)
	}
}

// updateNodeReq is what an operator may set. The id is deliberately absent: a
// stable id that a PUT could change is not stable, and every correlation already
// made against the old one would silently point at nothing. Changing it is a
// data-directory operation, done deliberately, not a form field.
type updateNodeReq struct {
	Name        *string           `json:"name"`
	Environment *string           `json:"environment"`
	Labels      map[string]string `json:"labels"`
}

// handleUpdateNode sets the operator-owned half of the descriptor. Absent fields
// are left alone, so setting an environment does not silently clear a name.
func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxNodeBodyBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req updateNodeReq
	if err := json.Unmarshal(body, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}
	if msg := validateNodeUpdate(req); msg != "" {
		httpapi.Error(w, http.StatusBadRequest, msg)
		return
	}

	var (
		desc     nodeDescriptor
		saveErr  error
		readErr  error
		ran      bool
		identity nodeIdentity
	)
	s.do(func() {
		ran = true
		// Through the same guard as the read, so an update never *creates* an
		// identity: saving a name over an absent record would persist a node with a
		// name and no id, which every later read would then refuse. A server whose
		// identity is unreadable needs an operator, not a write.
		if identity, readErr = s.nodeIdentity(); readErr != nil {
			return
		}
		if req.Name != nil {
			identity.Name = strings.TrimSpace(*req.Name)
		}
		if req.Environment != nil {
			identity.Environment = strings.TrimSpace(*req.Environment)
		}
		if req.Labels != nil {
			identity.Labels = trimmedLabels(req.Labels)
		}
		if saveErr = s.settings.saveNode(identity); saveErr != nil {
			return
		}
		desc, saveErr = s.describeNode()
	})
	switch {
	case !ran:
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
	case readErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read node identity: "+readErr.Error())
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "save node identity: "+saveErr.Error())
	default:
		httpapi.JSON(w, http.StatusOK, desc)
	}
}

// validateNodeUpdate bounds what goes into a document other servers read. It
// returns the message to refuse with, or the empty string.
//
// An empty label map is not the same as no label map: sending {} clears the
// labels, and that has to be possible or a mistyped label could never be removed.
func validateNodeUpdate(req updateNodeReq) string {
	if req.Name != nil && len(*req.Name) > maxNodeFieldLen {
		return fmt.Sprintf("name is longer than %d characters", maxNodeFieldLen)
	}
	if req.Environment != nil && len(*req.Environment) > maxNodeFieldLen {
		return fmt.Sprintf("environment is longer than %d characters", maxNodeFieldLen)
	}
	if len(req.Labels) > maxNodeLabels {
		return fmt.Sprintf("more than %d labels", maxNodeLabels)
	}
	for key, value := range req.Labels {
		if strings.TrimSpace(key) == "" {
			return "a label key is empty"
		}
		if len(key) > maxNodeLabelLen || len(value) > maxNodeLabelLen {
			return fmt.Sprintf("label %q is longer than %d characters", key, maxNodeLabelLen)
		}
	}
	return ""
}

// trimmedLabels normalizes an operator's label map. A key that is only whitespace
// is refused by validateNodeUpdate before it reaches here, so this does not check
// for one again: a second guard the boundary makes unreachable is not a guard, it
// is decoration that reads like one.
//
// It returns nil for an empty map so the descriptor omits the field rather than
// carrying an empty object — and an empty map is how an operator clears labels.
func trimmedLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}
