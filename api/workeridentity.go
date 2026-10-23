package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/identity"
	"github.com/pblumer/atlas/api/policy"
	"github.com/pblumer/atlas/authz"
	"github.com/pblumer/atlas/logging"
)

// A configured Worker's identity policy
// (ADR-draft-identity-aware-worker-execution §11): which identity its actions run
// as, for whom, and whether a policy decision is recorded beside them or enforced.
//
// A Worker without one is legacy: it behaves exactly as before, records nothing, and
// is listed by the readiness report as not governed. Nothing is assigned to it
// silently — neither a permission nor a refusal.

// Enforcement settings.
const (
	enforcementShadow  = "shadow"
	enforcementEnforce = "enforce"
)

// actAsInitiator is the one per-job subject this slice offers: whoever started the
// instance, as the engine recorded it.
const actAsInitiator = "initiator"

// workerIdentity is the identity policy on a configured Worker.
type workerIdentity struct {
	Mode string `json:"mode"`
	// ConnectionID is the connection a delegated-fixed Worker acts through.
	ConnectionID string `json:"connectionId,omitempty"`
	// ActAs chooses the subject of a delegated-per-job Worker.
	ActAs       string `json:"actAs,omitempty"`
	Enforcement string `json:"enforcement"`
	SetAt       int64  `json:"setAt"`
	SetBy       string `json:"setBy"`
}

// governedWorker is what the enforcement point needs about one Worker, kept in memory
// so neither a decision nor a completion reads the Worker store.
type governedWorker struct {
	name     string
	kind     string
	identity workerIdentity
}

// governableKinds are the Worker Types whose actions this build can govern. The
// SharePoint slice comes first (§20); a kind whose Worker name a model can compute
// (Entra, Exchange) is excluded until the resolved name is part of the decision.
var governableKinds = map[string]bool{connectorKindSharePoint: true}

// refreshGoverned rebuilds the in-memory set of governed Workers from the store. Runs
// on the loop, whenever the Worker registries are rebuilt.
func (s *Server) refreshGoverned() {
	if s.connectors == nil {
		return
	}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return
	}
	next := map[string]governedWorker{}
	for _, r := range recs {
		if r.Identity != nil {
			next[r.Name] = governedWorker{name: r.Name, kind: r.Kind, identity: *r.Identity}
		}
	}
	s.governed.Store(&next)
}

// governedWorkerNamed is the identity policy of the Worker named name, if it has one.
func (s *Server) governedWorkerNamed(name string) (governedWorker, bool) {
	m := s.governed.Load()
	if m == nil {
		return governedWorker{}, false
	}
	g, ok := (*m)[name]
	return g, ok
}

// validateIdentity says why an identity policy cannot be set on rec, or "".
func (s *Server) validateIdentity(rec connector, wi workerIdentity) string {
	if !governableKinds[rec.Kind] {
		return "a " + rec.Kind + " Worker cannot be governed yet: this build governs SharePoint Workers only"
	}
	switch wi.Enforcement {
	case enforcementShadow, enforcementEnforce:
	default:
		return `enforcement must be "shadow" or "enforce"`
	}
	switch wi.Mode {
	case authz.ModeService:
		if wi.ConnectionID != "" || wi.ActAs != "" {
			return "a service identity takes neither a connection nor an actAs"
		}
		return ""
	case authz.ModeDelegatedFixed, authz.ModeDelegatedPerJob:
	default:
		return `mode must be "service", "delegated-fixed" or "delegated-per-job"`
	}
	// A delegated credential is released only on a Permit, so a delegated Worker is
	// enforced or it is nothing: shadow would release a person's credential on a
	// refusal.
	if wi.Enforcement != enforcementEnforce {
		return "a delegated Worker is always enforced: its credential is released only on a permit"
	}
	if s.identity == nil {
		return "delegated connections are not available on this server (the vault is disabled)"
	}
	if wi.Mode == authz.ModeDelegatedPerJob {
		if wi.ConnectionID != "" {
			return "a per-job Worker chooses its connection per job; it takes no connectionId"
		}
		if wi.ActAs != actAsInitiator {
			return `actAs must be "initiator"`
		}
		return ""
	}
	if wi.ActAs != "" {
		return "a fixed delegated Worker acts as its connection's owner; it takes no actAs"
	}
	c, ok, err := s.identity.Get(wi.ConnectionID)
	switch {
	case err != nil || !ok:
		return "no connection " + wi.ConnectionID
	case c.Status != identity.StatusActive:
		return "connection " + wi.ConnectionID + " is " + c.Status
	case !containsString(c.AllowedWorkers, rec.Name):
		return "the connection's owner has not allowed Worker " + rec.Name + " to act through it"
	}
	return ""
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// handleSetWorkerIdentity sets a Worker's identity policy. An administrator's act:
// it decides as whom the Worker acts.
func (s *Server) handleSetWorkerIdentity(w http.ResponseWriter, r *http.Request) {
	var wi workerIdentity
	body, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().Request))
	if err == nil {
		err = json.Unmarshal(body, &wi)
	}
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	wi.Mode, wi.Enforcement = strings.TrimSpace(wi.Mode), strings.TrimSpace(wi.Enforcement)
	wi.ConnectionID, wi.ActAs = strings.TrimSpace(wi.ConnectionID), strings.TrimSpace(wi.ActAs)
	by := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		by = p.UserID
	}
	// The connection is read before the loop turn: the identity service dispatches
	// onto the loop itself.
	var (
		rec     connector
		found   bool
		refusal string
		saveErr error
	)
	s.do(func() { rec, found, saveErr = s.connectors.Get(r.PathValue("id")) })
	if saveErr == nil && found {
		refusal = s.validateIdentity(rec, wi)
	}
	if saveErr == nil && found && refusal == "" {
		s.do(func() {
			cur, ok, err := s.connectors.Get(rec.ID)
			if err != nil || !ok {
				saveErr, found = err, ok
				return
			}
			wi.SetAt, wi.SetBy = s.now(), by
			cur.Identity = &wi
			if saveErr = s.connectors.Save(cur); saveErr == nil {
				rec = cur
				s.refreshGoverned()
			}
		})
	}
	switch {
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, saveErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no worker with that id")
	case refusal != "":
		httpapi.Error(w, http.StatusBadRequest, refusal)
	default:
		audit(r, logging.PolicyPublished, "set a Worker's identity policy",
			slog.String("worker", rec.Name), slog.String("mode", wi.Mode), slog.String("enforcement", wi.Enforcement))
		httpapi.JSON(w, http.StatusOK, rec)
	}
}

// handleClearWorkerIdentity returns a Worker to legacy behavior.
func (s *Server) handleClearWorkerIdentity(w http.ResponseWriter, r *http.Request) {
	var (
		found   bool
		saveErr error
		name    string
	)
	s.do(func() {
		cur, ok, err := s.connectors.Get(r.PathValue("id"))
		if err != nil || !ok {
			saveErr, found = err, ok
			return
		}
		found, name = true, cur.Name
		cur.Identity = nil
		if saveErr = s.connectors.Save(cur); saveErr == nil {
			s.refreshGoverned()
		}
	})
	switch {
	case saveErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, saveErr.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no worker with that id")
	default:
		audit(r, logging.PolicyPublished, "cleared a Worker's identity policy", slog.String("worker", name))
		w.WriteHeader(http.StatusNoContent)
	}
}

// readinessRow is one Worker in the readiness report.
type readinessRow struct {
	Worker      string `json:"worker"`
	Kind        string `json:"kind"`
	Governed    bool   `json:"governed"`
	Governable  bool   `json:"governable"`
	Mode        string `json:"mode"`
	Enforcement string `json:"enforcement"`
	// Credential says where the Worker's credential is, never what it is.
	Credential string `json:"credential"`
	// CredentialKind is the same as a word a client can translate: brokered, worker
	// or none; CredentialRef is the vault reference a Worker-held credential is under.
	CredentialKind string `json:"credentialKind"`
	CredentialRef  string `json:"credentialRef,omitempty"`
	Note           string `json:"note,omitempty"`
}

// handleSecurityReadiness lists every configured Worker with whether its actions are
// governed, and how: the inventory an operator migrates from (§17).
func (s *Server) handleSecurityReadiness(w http.ResponseWriter, _ *http.Request) {
	var (
		recs []connector
		err  error
	)
	s.do(func() { recs, err = s.connectors.LoadAll() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := []readinessRow{}
	for _, r := range recs {
		row := readinessRow{Worker: r.Name, Kind: r.Kind, Governable: governableKinds[r.Kind], Mode: "legacy", Enforcement: "off"}
		switch {
		case r.Identity != nil && authz.IsDelegated(r.Identity.Mode):
			row.Credential, row.CredentialKind = "brokered by Atlas per authorized job", "brokered"
		case r.CredentialsRef != "":
			row.Credential, row.CredentialKind = "held by the Worker Instance (reference "+r.CredentialsRef+")", "worker"
			row.CredentialRef = r.CredentialsRef
		default:
			row.Credential, row.CredentialKind = "none configured", "none"
		}
		if r.Identity != nil {
			row.Governed, row.Mode, row.Enforcement = true, r.Identity.Mode, r.Identity.Enforcement
			switch {
			case r.Identity.Enforcement == enforcementShadow:
				row.Note = "decisions are recorded and never block: this is not enforcement"
			case r.Identity.Mode == authz.ModeService:
				row.Note = "enforced cooperatively: a refusal fails the job and a completion without a permit is refused, but the Worker Instance holds its own credential"
			default:
				row.Note = "enforced at the credential for supervised Worker Instances"
			}
		} else if !row.Governable {
			row.Note = "this Worker Type cannot be governed yet"
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Worker < rows[j].Worker })
	out := map[string]any{"workers": rows}
	if s.policies != nil {
		if err := s.policies.LoadError(); err != nil {
			out["policySetError"] = err.Error()
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// policyRoute serves a policy-service route. The service always exists — it opens
// whatever state its store is in — but the route table is built from a value, so it
// is resolved per request rather than captured nil.
func (s *Server) policyRoute(h func(*policy.Service) http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.policies == nil {
			httpapi.Error(w, http.StatusServiceUnavailable, "policies are not available on this server")
			return
		}
		h(s.policies)(w, r)
	}
}

// identityRoute serves an identity-service route, which exists only with a vault: a
// delegated credential has nowhere else it may live.
func (s *Server) identityRoute(h func(*identity.Service) http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.identity == nil {
			httpapi.Error(w, http.StatusServiceUnavailable, "delegated connections need the vault, which is disabled on this server")
			return
		}
		h(s.identity)(w, r)
	}
}
