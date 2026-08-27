package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// Who owns a connector, and who may reach its configuration (ADR-0205, measure
// M11).
//
// A connector used to belong to nobody. Its record carried a name, a kind, an
// endpoint and a credential reference, and nothing that said whose it was — a fair
// description while every connector was infrastructure, and no longer one since
// ADR-0075 made a connector a way for the outside world to reach in. What that
// left, measured: any authenticated account could list every connector with its
// endpoint and sender mailbox, edit one, delete somebody else's, and point somebody
// else's inbound connector at a message name of its choosing.
//
// The vocabulary here is ADR-0071's, deliberately unchanged: an owner, a
// visibility, and a member list of {ref, role} — so ADR-0180's groups work with no
// further thought, and "share it with my colleague" and "share it with the team"
// are one action. What is new is only where it is applied.
//
// Two rules are worth stating up front because they are the ones that could quietly
// break things:
//
//   - **Execution is not authoring.** Nothing in this file is consulted by the
//     runtime. The registries, the inbound bridge and a service task resolving a
//     connector by name read the store directly, exactly as before. A sharing rule
//     that reached the runtime would stop a deployed process the moment its author
//     changed roles, which is not access control, it is an outage.
//   - **Existence is not configuration.** Every authenticated caller still sees a
//     connector's name, kind and enabled flag, because the modeler fills its
//     connector picker from the same listing and a modeller who sees an empty
//     dropdown cannot author. What ownership governs is the *configuration* —
//     endpoint, sender, credential reference, who it is shared with, and its inbound
//     subscriptions.

// connectorRole reports the role a principal holds over a connector, and "" for
// none. It mirrors project.effectiveRole and differs only where a connector
// genuinely differs from a project.
//
//   - Auth disabled: the server is open by declaration (ADR-0195), so everyone is
//     an owner and nothing below ever refuses.
//   - admin: owner-equivalent, as it is on a project.
//   - Ownerless (a record written before this): nobody but an administrator, which
//     is the one place this departs from ADR-0071. That record let a legacy artifact
//     stay open because it was adding a capability; this one is closing a hole, and
//     a measure that exempts every installation that already has connectors has
//     closed nothing. The catalog entry stays visible either way, so authoring
//     against such a connector keeps working while an administrator assigns owners.
//   - The owner is an implicit owner; a shared connector grants each matching member
//     their role, highest wins across a direct grant and any group grants.
func connectorRole(c connector, pr *httpapi.Principal, authEnabled bool) string {
	if !authEnabled {
		return ScopeRoleOwner
	}
	if pr == nil {
		return ""
	}
	if pr.HasRole(RoleAdmin) {
		return ScopeRoleOwner
	}
	if c.OwnerID == "" {
		return ""
	}
	if pr.UserID != "" && pr.UserID == c.OwnerID {
		return ScopeRoleOwner
	}
	if c.Visibility != VisibilityShared {
		return ""
	}
	best := ""
	for _, m := range c.Members {
		matches := false
		switch m.Ref.Type {
		case PrincipalTypeUser:
			matches = pr.UserID != "" && m.Ref.ID == pr.UserID
		case PrincipalTypeGroup:
			matches = pr.InGroup(m.Ref.ID)
		}
		if matches && scopeRank(m.Role) > scopeRank(best) {
			best = m.Role
		}
	}
	return best
}

// checkConnectorRole authorizes an already-loaded connector at a minimum role.
// Status 0 means authorized. As with a project, no access at all reads as 404 —
// the connector is hidden from this caller's configuration listing, so its
// existence must not leak from a refusal either — and insufficient access reads as
// 403. Pure: it only reads the request's principal, so it is safe inside a run-loop
// closure.
func (s *Server) checkConnectorRole(r *http.Request, c connector, minRole string) (int, string) {
	rank := scopeRank(connectorRole(c, httpapi.PrincipalFrom(r.Context()), s.authEnabled))
	switch {
	case rank == 0:
		return http.StatusNotFound, "no connector with that id"
	case rank < scopeRank(minRole):
		return http.StatusForbidden, "insufficient connector access"
	default:
		return 0, ""
	}
}

// authorizeConnector loads a connector by id and authorizes the request against it
// at minRole, running the store read on the run loop. Handlers that already load
// the record inside their own do() call checkConnectorRole instead.
func (s *Server) authorizeConnector(r *http.Request, id, minRole string) (connector, int, string) {
	var (
		rec    connector
		found  bool
		getErr error
	)
	s.do(func() { rec, found, getErr = s.connectors.Get(id) })
	if getErr != nil {
		return connector{}, http.StatusInternalServerError, "read connector: " + getErr.Error()
	}
	if !found {
		return connector{}, http.StatusNotFound, "no connector with that id"
	}
	if code, msg := s.checkConnectorRole(r, rec, minRole); code != 0 {
		return connector{}, code, msg
	}
	return rec, 0, ""
}

// connectorCatalogEntry is what a caller with no role on a connector sees: that it
// exists, what it is called, what kind it is, and whether it is on.
//
// A separate shape rather than the full one with its fields blanked, on purpose. A
// blanked endpoint is indistinguishable from an unconfigured one, and an operator
// reading "endpoint: " would be told something false about the connector rather
// than something true about their own access.
type connectorCatalogEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
	// Problem is why the connector is not usable, and it is not configuration: a
	// modeller authoring against a connector that will park every task deserves to
	// know that before they deploy, whoever owns it.
	Problem string `json:"problem,omitempty"`
}

func catalogEntry(c connector, problem string) connectorCatalogEntry {
	return connectorCatalogEntry{
		ID: c.ID, Name: c.Name, Kind: c.Kind, Enabled: c.Enabled, Problem: problem,
	}
}

// mayUseCredentialRef reports whether this caller may ask the server to resolve a
// stored credential reference, and is the rule behind the connector check
// (POST /api/v1/connectors/test).
//
// That endpoint resolves whatever reference its body names and, given a recipient,
// sends real mail with it. Locking the connector record while anyone could still
// borrow its credential would be theatre — so a reference may be named only by
// somebody who may already edit a connector that uses it. Naming no reference is
// nobody's secret and stays open.
//
// Must be called on the run-loop goroutine: it reads the connector store.
func (s *Server) mayUseCredentialRef(r *http.Request, ref string) (bool, error) {
	if ref == "" || !s.authEnabled {
		return true, nil
	}
	if p := httpapi.PrincipalFrom(r.Context()); p != nil && p.HasRole(RoleAdmin) {
		return true, nil
	}
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return false, err
	}
	for _, c := range recs {
		if c.CredentialsRef != ref {
			continue
		}
		if code, _ := s.checkConnectorRole(r, c, ScopeRoleEditor); code == 0 {
			return true, nil
		}
	}
	return false, nil
}

// handleSetConnectorMember shares a connector, or changes a member's role.
// PUT /api/v1/connectors/{id}/members/{principalId}, body
// {"role":"viewer"|"editor", "type":"user"|"group"}. Owner (or admin) only.
//
// Adding the first member flips the connector to shared, exactly as it does for a
// project: a grant that sat inert behind a still-private visibility would be a
// share that silently did nothing.
func (s *Server) handleSetConnectorMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	principalID := r.PathValue("principalId")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxUserBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Role string `json:"role"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if !isScopeRole(payload.Role) {
		httpapi.Error(w, http.StatusBadRequest, `role must be "viewer" or "editor"`)
		return
	}
	refType := payload.Type
	if refType == "" {
		refType = PrincipalTypeUser
	}
	if refType != PrincipalTypeUser && refType != PrincipalTypeGroup {
		httpapi.Error(w, http.StatusBadRequest, `member type must be "user" or "group"`)
		return
	}

	rec, code, msg := s.authorizeConnector(r, id, ScopeRoleOwner)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	if refType == PrincipalTypeUser && principalID == rec.OwnerID {
		httpapi.Error(w, http.StatusBadRequest, "the owner already has full access and cannot be added as a member")
		return
	}

	var (
		targetMissing   bool
		lookupErr, sErr error
	)
	s.do(func() {
		if s.authEnabled {
			// Only grant to an identity that exists. With auth off there is no directory
			// to consult and sharing is moot anyway.
			var (
				ok bool
				e  error
			)
			if refType == PrincipalTypeGroup {
				_, ok, e = s.groups.Get(principalID)
			} else {
				_, ok, e = s.users.Get(principalID)
			}
			if e != nil {
				lookupErr = e
				return
			} else if !ok {
				targetMissing = true
				return
			}
		}
		rec.Members = upsertMember(rec.Members, refType, principalID, payload.Role)
		rec.Visibility = VisibilityShared
		sErr = s.connectors.Save(rec)
	})
	switch {
	case lookupErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "look up member: "+lookupErr.Error())
	case targetMissing:
		httpapi.Error(w, http.StatusBadRequest, "no "+refType+" with that id")
	case sErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "share connector: "+sErr.Error())
	default:
		audit(r, logging.AuthConnectorShared, "connector shared",
			slog.String("connector_id", rec.ID), slog.String("connector_name", rec.Name),
			slog.String("subject_type", refType), slog.String("subject_id", principalID),
			slog.String("role", payload.Role))
		httpapi.JSON(w, http.StatusOK, rec)
	}
}

// handleRemoveConnectorMember withdraws a member's access.
// DELETE /api/v1/connectors/{id}/members/{principalId}. Owner (or admin) only, and
// idempotent: removing somebody who is not a member succeeds.
//
// Visibility is left alone, as it is for a project: an owner who wants to seal the
// connector sets it private explicitly, and a share withdrawn one member at a time
// should not silently change what the remaining members can reach.
func (s *Server) handleRemoveConnectorMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	principalID := r.PathValue("principalId")

	rec, code, msg := s.authorizeConnector(r, id, ScopeRoleOwner)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	var sErr error
	s.do(func() {
		rec.Members = removeMember(rec.Members, principalID)
		sErr = s.connectors.Save(rec)
	})
	if sErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "unshare connector: "+sErr.Error())
		return
	}
	audit(r, logging.AuthConnectorUnshared, "connector share withdrawn",
		slog.String("connector_id", rec.ID), slog.String("connector_name", rec.Name),
		slog.String("subject_id", principalID))
	httpapi.JSON(w, http.StatusOK, rec)
}

// handleSetConnectorVisibility seals a shared connector again, or opens it to its
// member list. PUT /api/v1/connectors/{id}/visibility, body
// {"visibility":"private"|"shared"}. Owner (or admin) only.
//
// Private does not clear the members: sealing a connector for a while and opening
// it again is a thing an owner does, and making them re-enter the list each time
// would be a reason not to seal it at all.
func (s *Server) handleSetConnectorVisibility(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxUserBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if payload.Visibility != VisibilityPrivate && payload.Visibility != VisibilityShared {
		httpapi.Error(w, http.StatusBadRequest, `visibility must be "private" or "shared"`)
		return
	}

	rec, code, msg := s.authorizeConnector(r, id, ScopeRoleOwner)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	var sErr error
	s.do(func() {
		rec.Visibility = payload.Visibility
		sErr = s.connectors.Save(rec)
	})
	if sErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "set connector visibility: "+sErr.Error())
		return
	}
	audit(r, logging.AuthConnectorShared, "connector visibility changed",
		slog.String("connector_id", rec.ID), slog.String("connector_name", rec.Name),
		slog.String("visibility", rec.Visibility))
	httpapi.JSON(w, http.StatusOK, rec)
}

// handleTransferConnector hands a connector to somebody else.
// PUT /api/v1/connectors/{id}/owner/{userId}. Owner (or admin) only.
//
// It exists because the alternative is an administrator editing a JSON file. A
// person leaves, and the mailbox they configured has to become somebody's — and an
// ownerless connector is admin-only, so without this the answer to "Anna left" is
// "an administrator now manages every connector Anna made".
func (s *Server) handleTransferConnector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID := r.PathValue("userId")

	rec, code, msg := s.authorizeConnector(r, id, ScopeRoleOwner)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	var (
		targetMissing   bool
		lookupErr, sErr error
	)
	s.do(func() {
		if s.authEnabled {
			_, ok, e := s.users.Get(userID)
			if e != nil {
				lookupErr = e
				return
			} else if !ok {
				targetMissing = true
				return
			}
		}
		rec.OwnerID = userID
		// The new owner is an owner, so a leftover member grant for them would be
		// noise — and one that reads "editor" beside their own connector reads as a
		// restriction that is not there.
		rec.Members = removeMember(rec.Members, userID)
		rec.UpdatedAt = time.Now().Unix()
		sErr = s.connectors.Save(rec)
	})
	switch {
	case lookupErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "look up user: "+lookupErr.Error())
	case targetMissing:
		httpapi.Error(w, http.StatusBadRequest, "no user with that id")
	case sErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "transfer connector: "+sErr.Error())
	default:
		audit(r, logging.AuthConnectorShared, "connector transferred",
			slog.String("connector_id", rec.ID), slog.String("connector_name", rec.Name),
			slog.String("new_owner", userID))
		httpapi.JSON(w, http.StatusOK, rec)
	}
}
