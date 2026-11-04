package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// The system grant: the resource `…/system` (ADR-0512 §2).
//
// A connected system reads Atlas on its own — the deployed processes it draws, the
// open tasks it keeps a copy of, the feed of what changed — and until this existed it
// did so with three API tokens an administrator minted and copied into its
// environment. A system grant replaces them with one approval: an administrator,
// signed in, approves the system for the resource `<origin>/system`, and chooses on the
// consent screen which of the capabilities it asked for it gets and how far each
// reaches.
//
// What it may become is deliberately no more than those tokens were:
//
//   - **Only the narrow scopes a system reads with.** `diagrams` (ADR-0493),
//     `tasklist` and `events` (ADR-0510), named in the standard `scope` parameter.
//     `full`, `worker` and every other scope are refused for this resource, so no
//     approval on this screen can hand a system the product surface.
//   - **Each with a reach the approver chose.** Projects, for each of them: for
//     `diagrams` as a token minted with a reach; for `tasklist` the tasks of processes
//     deployed from those projects; for `events` those tasks' facts and nothing else.
//     The token of a system grant resolves, per request, to exactly the principal an
//     API token of the matching scope and reach would be (systemPrincipal), so every
//     route decides as it already does for such a token and no handler reads a scope.
//   - **It carries its approver** (ADR-0196), and only an administrator may approve
//     it. Their roles are snapshotted and kept current like every grant's, and the
//     approver ceasing to be an administrator withdraws it (setUserGrantRoles).
//   - **One per client.** A new approval replaces the standing one in the run-loop
//     turn that saves it, so changing what a system reaches is approving it again.
//
// Connecting, changing and disconnecting are audited as system.connect,
// system.change and system.disconnect, with the approver, the capabilities and their
// reach.

// oauthSystemResource is the path that, after the server's origin, names the
// connected system's own reading of this server as a resource.
const oauthSystemResource = "/system"

// systemCapabilityScopes are the scopes a system grant may carry, in the order the
// consent screen lists them. Each is a mintable scope whose allowlist and reach are
// already what a system reading this server needs.
var systemCapabilityScopes = []string{apiScopeDiagrams, apiScopeTaskList, apiScopeEvents}

// systemCapability is one scope a system grant carries and the projects it reaches;
// no projects means unrestricted, as a token minted without a reach is.
type systemCapability struct {
	Scope    string   `json:"scope"`
	Projects []string `json:"projects"`
}

// oauthSystem is what makes a grant a system grant: what the approver chose.
type oauthSystem struct {
	Capabilities []systemCapability `json:"capabilities"`
	// Preconsent says the approver let the system's people connect their own accounts
	// without a consent screen of their own (ADR-0512 §3).
	Preconsent bool  `json:"preconsent"`
	ApprovedAt int64 `json:"approvedAt"`
}

// scopes is the capabilities' scopes, space-separated, as the token response's
// `scope` names them.
func (o *oauthSystem) scopes() string {
	out := make([]string, 0, len(o.Capabilities))
	for _, c := range o.Capabilities {
		out = append(out, c.Scope)
	}
	return strings.Join(out, " ")
}

// systemScopesFrom reads the scope parameter of an authorization request for the
// system resource: the capabilities the system asks for, in the consent screen's
// order. Empty asks for all three. Anything that is not one of them is refused — the
// caller turns that into invalid_scope — rather than dropped, so a system that asks
// for `full` learns it may not instead of learning it got less.
func systemScopesFrom(raw string) ([]string, bool) {
	asked := strings.Fields(raw)
	if len(asked) == 0 {
		return append([]string(nil), systemCapabilityScopes...), true
	}
	for _, a := range asked {
		if !slices.Contains(systemCapabilityScopes, a) {
			return nil, false
		}
	}
	out := []string{}
	for _, s := range systemCapabilityScopes {
		if slices.Contains(asked, s) {
			out = append(out, s)
		}
	}
	return out, true
}

// isSystemResource reports whether a resource is this server's `…/system`.
func isSystemResource(resource string) bool {
	return strings.HasSuffix(resource, oauthSystemResource)
}

// chooseSystemCapabilities settles what an approval grants: the capabilities the
// approver kept, each among those the system asked for, at most once, with a reach of
// projects this server has. nil chosen keeps every one asked for, unrestricted — what a
// caller that sends no choice is taken to mean — and an empty choice is refused, because
// a system approved for nothing is a refusal written as an approval.
func (s *Server) chooseSystemCapabilities(asked []string, chosen []systemCapability) ([]systemCapability, string, error) {
	if chosen == nil {
		out := make([]systemCapability, 0, len(asked))
		for _, a := range asked {
			out = append(out, systemCapability{Scope: a, Projects: []string{}})
		}
		return out, "", nil
	}
	if len(chosen) == 0 {
		return nil, "choose at least one capability, or do not allow the system", nil
	}
	var (
		projs   map[string]project
		loadErr error
	)
	s.do(func() { projs, loadErr = s.projectsByID() })
	if loadErr != nil {
		return nil, "", loadErr
	}
	out := []systemCapability{}
	for _, want := range systemCapabilityScopes {
		for _, c := range chosen {
			if c.Scope != want {
				continue
			}
			if !slices.Contains(asked, c.Scope) {
				return nil, "the system did not ask for " + c.Scope, nil
			}
			if slices.ContainsFunc(out, func(o systemCapability) bool { return o.Scope == c.Scope }) {
				return nil, "a capability is chosen once: " + c.Scope, nil
			}
			reach := []string{}
			for _, id := range c.Projects {
				if id = strings.TrimSpace(id); id == "" || slices.Contains(reach, id) {
					continue
				}
				if _, ok := projs[id]; !ok {
					return nil, "the reach of " + c.Scope + " names no project this server has: " + id, nil
				}
				reach = append(reach, id)
			}
			out = append(out, systemCapability{Scope: c.Scope, Projects: reach})
		}
	}
	for _, c := range chosen {
		if !slices.Contains(systemCapabilityScopes, c.Scope) {
			return nil, c.Scope + " is not a capability a system may be given", nil
		}
	}
	return out, "", nil
}

// systemPrincipal is who a system grant's token is on this request: the principal an
// API token of the capability that serves the route would be, with that capability's
// reach (ADR-0512 §2).
//
// Resolved per request because a grant carries several capabilities and each has its
// own reach, while a principal holds one scope and one reach. The capabilities'
// allowlists are disjoint, so a route is served by at most one of them; a route none
// of them serves resolves to the system scope itself, which reaches only who-am-I — and
// the boundary refuses everything else with the scope's ordinary 403.
//
// Its roles are what a token minted by the approver would carry (tokenRolesFor): never
// admin, so a system grant is never more than its tokens were, and for `events` the
// feed reader's role alone.
func systemPrincipal(g oauthGrant, r *http.Request) *httpapi.Principal {
	approver := &httpapi.Principal{Roles: g.Roles}
	p := &httpapi.Principal{UserID: g.UserID, Username: g.Username, GroupIDs: g.GroupIDs, Scope: apiScopeSystem}
	for _, c := range g.System.Capabilities {
		mux, ok := apiScopeMux[c.Scope]
		if !ok {
			continue
		}
		if _, pattern := mux.Handler(r); pattern == "" {
			continue
		}
		p.Scope = c.Scope
		var projects []string
		if len(c.Projects) > 0 {
			projects = append([]string(nil), c.Projects...)
		}
		// The feed's own reach names catalogues (ADR-0432); a system's names projects,
		// so it travels apart and the feed reads it as such.
		if c.Scope == apiScopeEvents {
			p.FeedProjects = projects
		} else {
			p.Reach = projects
		}
		break
	}
	p.Roles, _ = tokenRolesFor(p.Scope, approver)
	return p
}

// systemGrantOf is the system grant the request's bearer token belongs to, if it
// belongs to one. For the few handlers that answer a system differently — who-am-I
// with what was approved, a task that ended told from one out of reach — and that
// therefore ask which grant this is rather than what scope it has.
func (s *Server) systemGrantOf(r *http.Request) (oauthGrant, bool) {
	tok, ok := bearerToken(r)
	if !ok {
		return oauthGrant{}, false
	}
	g, ok := s.oauthGrants.matchAccess(tok, time.Now().Unix())
	if !ok || g.System == nil {
		return oauthGrant{}, false
	}
	return g, true
}

// systemView is what a system learns about its own grant at GET /api/v1/auth/me
// (ADR-0512 §3): what it needs to tell a task that left its reach from one that ended,
// and to know whether to send its people through a silent connection.
func (s *Server) systemView(g oauthGrant) map[string]any {
	name := g.ClientName
	if c, ok := s.oauthClients.lookup(g.ClientID); ok && c.Name != "" {
		name = c.Name
	}
	caps := make([]systemCapability, 0, len(g.System.Capabilities))
	for _, c := range g.System.Capabilities {
		projects := c.Projects
		if projects == nil {
			projects = []string{}
		}
		caps = append(caps, systemCapability{Scope: c.Scope, Projects: projects})
	}
	return map[string]any{
		"clientId":     g.ClientID,
		"clientName":   name,
		"approvedBy":   g.Username,
		"approvedAt":   time.Unix(g.System.ApprovedAt, 0).UTC().Format(time.RFC3339),
		"capabilities": caps,
		"preconsent":   g.System.Preconsent,
	}
}

// auditSystem records a connection made, changed or ended, with what it reaches.
func auditSystem(r *http.Request, e logging.Event, msg string, g oauthGrant, attrs ...slog.Attr) {
	caps := []string{}
	if g.System != nil {
		for _, c := range g.System.Capabilities {
			reach := "all"
			if len(c.Projects) > 0 {
				reach = strings.Join(c.Projects, ",")
			}
			caps = append(caps, c.Scope+"="+reach)
		}
	}
	base := []slog.Attr{
		slog.String("grant_id", g.ID), slog.String("client_id", g.ClientID),
		slog.String("approver", g.Username), slog.String("capabilities", strings.Join(caps, " ")),
		slog.Bool("preconsent", g.System != nil && g.System.Preconsent),
	}
	if r != nil {
		audit(r, e, msg, append(base, attrs...)...)
		return
	}
	logging.Info(e, msg, append(base, attrs...)...)
}

// replaceSystemGrants withdraws every system grant of a client but keep, inside the
// caller's run-loop turn: one system grant per client (ADR-0512 §2). It returns what it
// withdrew, for the audit.
func (s *Server) replaceSystemGrants(clientID, keep string) ([]oauthGrant, error) {
	recs, err := s.oauthGrantStore.LoadAll()
	if err != nil {
		return nil, err
	}
	var gone []oauthGrant
	for _, g := range recs {
		if g.System == nil || g.ClientID != clientID || g.ID == keep {
			continue
		}
		if err := s.oauthGrantStore.Delete(g.ID); err != nil {
			return gone, err
		}
		s.oauthGrants.remove(g.ID)
		gone = append(gone, g)
	}
	return gone, nil
}

// noteSystemEnded records on a client's record that its system grant ended and why,
// inside the caller's run-loop turn, so the Console can say what happened to a system
// that is no longer connected rather than forgetting it was. A client without a record
// — one entered here never saves one this way — is left as it is.
func (s *Server) noteSystemEnded(clientID, reason string, at int64) {
	rec, ok := s.oauthClients.lookup(clientID)
	if !ok {
		return
	}
	rec.DisconnectedAt, rec.DisconnectReason = at, reason
	if err := s.oauthClientStore.Save(rec); err != nil {
		return
	}
	s.oauthClients.add(rec)
}

// withdrawSystemGrantsOfApprover ends the system grants a person approved once they no
// longer hold the admin role: only an administrator may connect a system, so a grant
// whose approver is not one any more stands on an approval nobody could give now
// (ADR-0512 §2). Another administrator connects the system again with one approval.
func (s *Server) withdrawSystemGrantsOfApprover(userID string) {
	var gone []oauthGrant
	now := time.Now().Unix()
	s.do(func() {
		recs, err := s.oauthGrantStore.LoadAll()
		if err != nil {
			return
		}
		for _, g := range recs {
			if g.System == nil || g.UserID != userID {
				continue
			}
			if err := s.oauthGrantStore.Delete(g.ID); err != nil {
				continue
			}
			s.oauthGrants.remove(g.ID)
			s.noteSystemEnded(g.ClientID, "the administrator who approved it is no longer an administrator", now)
			gone = append(gone, g)
		}
	})
	for _, g := range gone {
		auditSystem(nil, logging.SystemDisconnect, "connected system disconnected: its approver is no longer an administrator", g)
	}
}

// systemProjectsFor lists the projects a reach may name, for the consent screen.
func systemProjectsFor(projs map[string]project) []map[string]string {
	out := make([]map[string]string, 0, len(projs))
	for _, p := range projs {
		out = append(out, map[string]string{"id": p.ID, "name": p.Name})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i]["name"] != out[j]["name"] {
			return out[i]["name"] < out[j]["name"]
		}
		return out[i]["id"] < out[j]["id"]
	})
	return out
}

// clientHostOf is the host a client is shown by: its client_id's, for a client named
// by its metadata document, and "" for one entered here.
func clientHostOf(c oauthClient) string {
	if !c.Metadata {
		return ""
	}
	if u, err := url.Parse(c.ID); err == nil {
		return u.Host
	}
	return ""
}

// errOnlyAdmins is the refusal a person without the admin role gets for approving a
// system grant.
var errOnlyAdmins = errors.New("only an administrator can connect a system; ask one to start the connection from the system")
