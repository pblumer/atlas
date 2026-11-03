package api

import (
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// Pre-consent, and seeing and ending a connection (ADR-0512 §3, §4).
//
// An administrator who connects a system may let its people connect their own
// accounts without a consent screen of their own. What they connect is still each
// person's own grant for `…/tasks` (ADR-0510 §3) — their account, their roles, their
// tasks, and Atlas deciding every claim and completion — only the page that would ask
// them is skipped, and only while they are signed in. A system that tries in the
// background sends `prompt=none` and is answered login_required or consent_required
// instead of a page.
//
// A person is not bound by somebody else's yes. The systems pre-approved for them are
// listed to them on Connected accounts, and withdrawing such a grant — there or on the
// AI access page — records that they declined the client: pre-consent no longer applies
// to them, and the consent screen asks them again if they connect themselves, which
// clears the mark.
//
// Administrators see every connected system on Connected systems: who approved it and
// when, what it reaches, whether pre-consent stands, how many people are connected and
// when it last used Atlas. They turn pre-consent off there, or disconnect the system —
// its grant withdrawn and, if they choose, every person's grant of it with it. Changing
// what a system reaches is approving it again, started from the system.

// preconsented reports whether a person's request may be approved without the consent
// page: a request for their tasks, from a client whose standing system grant lets its
// people connect without being asked, by a person who has not declined it.
func (s *Server) preconsented(req oauthRequest, p *httpapi.Principal) bool {
	if !strings.HasSuffix(req.resource, oauthTasksResource) {
		return false
	}
	g, ok := s.oauthGrants.systemOf(req.client.ID)
	if !ok || !g.System.Preconsent {
		return false
	}
	if rec, ok := s.oauthClients.lookup(req.client.ID); ok && slices.Contains(rec.Declined, p.UserID) {
		return false
	}
	return true
}

// authorizeWithoutPage answers an authorization request that needs no page, and
// reports whether it did: one pre-consent approves, and one sent with prompt=none,
// which must never be shown a page (OpenID Connect Core §3.1.2.1, ADR-0512 §3).
//
// Everything that is wrong with the client or its redirect URI is still the page's to
// show and never redirected, prompt=none or not — the rule parseAuthorizeRequest
// explains. Only a session counts as signed in: the browser of a person, not a bearer
// token sent to a page.
func (s *Server) authorizeWithoutPage(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	silent := q.Get("prompt") == "none"
	req, clientErr, fatal := s.parseAuthorizeRequest(r, q, false)
	if fatal != "" {
		return false
	}
	redirect := func(to string) bool {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, to, http.StatusFound)
		return true
	}
	if clientErr != "" {
		return silent && redirect(errorRedirect(req.redirectURI, clientErr, req.state))
	}
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || !p.Session {
		return silent && redirect(errorRedirect(req.redirectURI, "login_required", req.state))
	}
	if s.preconsented(req, p) {
		to, err := s.issueCode(req, p, nil)
		if err != nil {
			return silent && redirect(errorRedirect(req.redirectURI, "server_error", req.state))
		}
		audit(r, logging.AuthOAuthGranted, "oauth: approved without the consent page: an administrator pre-consented for this system",
			slog.String("client_id", req.client.ID), slog.String("client_name", req.client.Name),
			slog.String("resource", req.resource), slog.Bool("preconsent", true))
		return redirect(to)
	}
	return silent && redirect(errorRedirect(req.redirectURI, "consent_required", req.state))
}

// markDeclined records that a person withdrew their grant of a client whose system
// grant pre-consents, inside the caller's run-loop turn. Where nothing pre-consents
// there is nothing to decline, and nothing is written.
func (s *Server) markDeclined(clientID, userID string) {
	g, ok := s.oauthGrants.systemOf(clientID)
	if !ok || !g.System.Preconsent {
		return
	}
	rec, ok := s.oauthClients.lookup(clientID)
	if !ok || slices.Contains(rec.Declined, userID) {
		return
	}
	rec.Declined = append(append([]string(nil), rec.Declined...), userID)
	if err := s.oauthClientStore.Save(rec); err == nil {
		s.oauthClients.add(rec)
	}
}

// clearDeclined takes a person off a client's declined list once they approved it on
// the consent screen themselves, inside the caller's run-loop turn.
func (s *Server) clearDeclined(clientID, userID string) {
	rec, ok := s.oauthClients.lookup(clientID)
	if !ok || !slices.Contains(rec.Declined, userID) {
		return
	}
	rec.Declined = slices.DeleteFunc(append([]string(nil), rec.Declined...), func(id string) bool { return id == userID })
	if err := s.oauthClientStore.Save(rec); err == nil {
		s.oauthClients.add(rec)
	}
}

// connectedSystemView is one row of Connected systems.
type connectedSystemView struct {
	// ID is the standing system grant's id, which the row's actions address; empty
	// for a system that is no longer connected.
	ID       string `json:"id"`
	ClientID string `json:"clientId"`
	Host     string `json:"host"`
	Name     string `json:"name"`

	Connected    bool               `json:"connected"`
	ApprovedBy   string             `json:"approvedBy,omitempty"`
	ApprovedAt   int64              `json:"approvedAt,omitempty"`
	Capabilities []systemCapability `json:"capabilities"`
	Preconsent   bool               `json:"preconsent"`

	// People is how many people's grants of this client stand.
	People int `json:"people"`
	// LastUsed is when any of this client's grants was last used, in Unix seconds: a
	// token presented since this server started, or else last issued.
	LastUsed int64 `json:"lastUsed,omitempty"`

	DisconnectedAt   int64  `json:"disconnectedAt,omitempty"`
	DisconnectReason string `json:"disconnectReason,omitempty"`
}

// handleListConnectedSystems is GET /api/v1/connected-systems: every system connected
// to this server, and every one that was until its grant ended (admin-only).
func (s *Server) handleListConnectedSystems(w http.ResponseWriter, r *http.Request) {
	var (
		clients []oauthClient
		grants  []oauthGrant
		err     error
	)
	s.do(func() {
		if clients, err = s.oauthClientStore.LoadAll(); err != nil {
			return
		}
		grants, err = s.oauthGrantStore.LoadAll()
	})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list connected systems: "+err.Error())
		return
	}
	byClient := map[string]oauthClient{}
	for _, c := range clients {
		byClient[c.ID] = c
	}
	rows := map[string]*connectedSystemView{}
	row := func(clientID, fallbackName string) *connectedSystemView {
		if v, ok := rows[clientID]; ok {
			return v
		}
		c := byClient[clientID]
		name := c.Name
		if name == "" {
			name = fallbackName
		}
		v := &connectedSystemView{ClientID: clientID, Host: clientHostOf(c), Name: name,
			Capabilities: []systemCapability{}, DisconnectedAt: c.DisconnectedAt, DisconnectReason: c.DisconnectReason}
		rows[clientID] = v
		return v
	}
	for _, c := range clients {
		if c.DisconnectedAt != 0 {
			row(c.ID, c.Name)
		}
	}
	for _, g := range grants {
		if g.System == nil {
			continue
		}
		v := row(g.ClientID, g.ClientName)
		v.ID, v.Connected = g.ID, true
		v.ApprovedBy, v.ApprovedAt = g.Username, g.System.ApprovedAt
		v.Capabilities, v.Preconsent = g.System.Capabilities, g.System.Preconsent
		v.DisconnectedAt, v.DisconnectReason = 0, ""
	}
	for _, g := range grants {
		v, ok := rows[g.ClientID]
		if !ok {
			continue
		}
		if g.System == nil {
			v.People++
		}
		used := s.oauthGrants.lastUse(g.ID)
		if issued := g.AccessExpires - int64(oauthAccessLifetime.Seconds()); issued > used {
			used = issued
		}
		if used > v.LastUsed {
			v.LastUsed = used
		}
	}
	out := make([]connectedSystemView, 0, len(rows))
	for _, v := range rows {
		for i := range v.Capabilities {
			if v.Capabilities[i].Projects == nil {
				v.Capabilities[i].Projects = []string{}
			}
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Connected != out[j].Connected {
			return out[i].Connected
		}
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].ClientID < out[j].ClientID
	})
	httpapi.JSON(w, http.StatusOK, out)
}

// handleSetSystemPreconsent is PUT /api/v1/connected-systems/{id}/preconsent with
// {"preconsent": false}: an administrator stops a system's people from connecting
// without being asked (admin-only). It turns pre-consent off only — giving it is part
// of the approval on the consent screen, where the administrator sees what the system
// reaches — and people already connected keep their own grants.
func (s *Server) handleSetSystemPreconsent(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Preconsent *bool `json:"preconsent"`
	}
	if !s.decodeJSONBody(w, r, &payload) {
		return
	}
	if payload.Preconsent == nil || *payload.Preconsent {
		httpapi.Error(w, http.StatusBadRequest,
			`only {"preconsent": false} is accepted here; pre-consent is given on the consent screen, by approving the system again`)
		return
	}
	id := r.PathValue("id")
	var (
		found   bool
		changed oauthGrant
		err     error
	)
	s.do(func() {
		var g oauthGrant
		if g, found, err = s.oauthGrantStore.Get(id); err != nil || !found || g.System == nil {
			found = found && g.System != nil
			return
		}
		sys := *g.System
		sys.Preconsent = false
		g.System = &sys
		if err = s.oauthGrantStore.Save(g); err != nil {
			return
		}
		s.oauthGrants.put(g)
		changed = g
	})
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "change connected system: "+err.Error())
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no connected system with that id")
	default:
		auditSystem(r, logging.SystemChange, "connected system changed: its people are asked again before they connect", changed)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDisconnectSystem is DELETE /api/v1/connected-systems/{id}: the system grant is
// withdrawn, and with ?people=withdraw every person's grant of the same client as well
// (admin-only). The client's record stays, saying when and why it was disconnected, so
// approving the system again is one approval from the system.
func (s *Server) handleDisconnectSystem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	withPeople := r.URL.Query().Get("people") == "withdraw"
	var (
		gone   oauthGrant
		found  bool
		people int
		err    error
	)
	now := time.Now().Unix()
	s.do(func() {
		var recs []oauthGrant
		if recs, err = s.oauthGrantStore.LoadAll(); err != nil {
			return
		}
		for _, g := range recs {
			if g.ID == id && g.System != nil {
				gone, found = g, true
			}
		}
		if !found {
			return
		}
		for _, g := range recs {
			if g.ClientID != gone.ClientID || (g.ID != gone.ID && (!withPeople || g.System != nil)) {
				continue
			}
			if err = s.oauthGrantStore.Delete(g.ID); err != nil {
				return
			}
			s.oauthGrants.remove(g.ID)
			if g.ID != gone.ID {
				people++
			}
		}
		s.noteSystemEnded(gone.ClientID, systemEndedVia("connected-systems"), now)
	})
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "disconnect system: "+err.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no connected system with that id")
		return
	}
	auditSystem(r, logging.SystemDisconnect, "connected system disconnected", gone,
		slog.String("via", "connected-systems"), slog.Int("people_withdrawn", people))
	w.WriteHeader(http.StatusNoContent)
}

// mySystemView is one row of a person's Connected accounts: a connected system, and
// where the person stands with it.
type mySystemView struct {
	ClientID string `json:"clientId"`
	Host     string `json:"host"`
	Name     string `json:"name"`
	// Grants are the ids of the person's own standing grants of this client.
	Grants []string `json:"grants"`
	// PreApproved says an administrator lets this system's people connect without
	// being asked, and that this applies to the person — it does not once they
	// declined.
	PreApproved bool `json:"preApproved"`
	Declined    bool `json:"declined"`
}

// handleMyConnectedSystems is GET /api/v1/me/connected-systems: the systems the
// signed-in person is connected to, and the ones pre-approved for them.
func (s *Server) handleMyConnectedSystems(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		// Authentication is off: there is no account to be connected, and an open
		// server answers rather than refusing.
		httpapi.JSON(w, http.StatusOK, []mySystemView{})
		return
	}
	rows := map[string]*mySystemView{}
	row := func(clientID string) *mySystemView {
		if v, ok := rows[clientID]; ok {
			return v
		}
		c, _ := s.oauthClients.lookup(clientID)
		v := &mySystemView{ClientID: clientID, Host: clientHostOf(c), Name: c.Name, Grants: []string{},
			Declined: slices.Contains(c.Declined, p.UserID)}
		rows[clientID] = v
		return v
	}
	for _, g := range s.oauthGrants.all() {
		if g.System != nil {
			v := row(g.ClientID)
			if v.Name == "" {
				v.Name = g.ClientName
			}
			v.PreApproved = g.System.Preconsent && !v.Declined
		}
	}
	for _, g := range s.oauthGrants.all() {
		if g.System != nil || g.UserID != p.UserID {
			continue
		}
		c, _ := s.oauthClients.lookup(g.ClientID)
		if _, isSystem := rows[g.ClientID]; !isSystem && !c.Metadata {
			continue // an AI assistant or another application: the AI access page lists those
		}
		v := row(g.ClientID)
		if v.Name == "" {
			v.Name = g.ClientName
		}
		v.Grants = append(v.Grants, g.ID)
	}
	out := make([]mySystemView, 0, len(rows))
	for _, v := range rows {
		sort.Strings(v.Grants)
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	httpapi.JSON(w, http.StatusOK, out)
}

// handleWithdrawFromSystem is POST /api/v1/me/connected-systems/withdraw with
// {"clientId": …}: the signed-in person withdraws their own grants of a connected
// system and, where pre-consent stands, declines it, so it is not connected for them
// again without their asking. It answers 204 whether or not they held a grant, because
// declining a system one never connected is a decision of its own.
func (s *Server) handleWithdrawFromSystem(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		httpapi.Error(w, http.StatusBadRequest, "there is no account to withdraw for: authentication is off")
		return
	}
	var payload struct {
		ClientID string `json:"clientId"`
	}
	if !s.decodeJSONBody(w, r, &payload) {
		return
	}
	clientID := strings.TrimSpace(payload.ClientID)
	if _, ok := s.oauthClients.lookup(clientID); !ok {
		if _, ok := s.oauthGrants.systemOf(clientID); !ok {
			httpapi.Error(w, http.StatusNotFound, "no connected system with that client id")
			return
		}
	}
	var (
		gone []oauthGrant
		err  error
	)
	s.do(func() {
		var recs []oauthGrant
		if recs, err = s.oauthGrantStore.LoadAll(); err != nil {
			return
		}
		for _, g := range recs {
			if g.ClientID != clientID || g.UserID != p.UserID || g.System != nil {
				continue
			}
			if err = s.oauthGrantStore.Delete(g.ID); err != nil {
				return
			}
			s.oauthGrants.remove(g.ID)
			gone = append(gone, g)
		}
		s.markDeclined(clientID, p.UserID)
	})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "withdraw: "+err.Error())
		return
	}
	for _, g := range gone {
		audit(r, logging.AuthOAuthGrantRevoked, "oauth grant revoked",
			slog.String("grant_id", g.ID), slog.String("client_id", g.ClientID),
			slog.String("subject", g.Username), slog.String("via", "connected-accounts"))
	}
	w.WriteHeader(http.StatusNoContent)
}
